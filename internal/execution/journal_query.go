package execution

import (
	"fmt"
	"sort"
	"time"
)

// Query selects a slice of the append-only journal: run inspection and the UI
// filter history with it instead of re-implementing the matching rules. The
// zero value is sane and matches everything with no limit.
type Query struct {
	RunID    string    `json:"run_id,omitempty"`    // "" = every run in the store
	TaskID   string    `json:"task_id,omitempty"`   // "" = every task and run-level events
	Kinds    []string  `json:"kinds,omitempty"`     // nil or empty = every kind
	After    time.Time `json:"after,omitempty"`     // zero = unbounded; inclusive lower bound
	Before   time.Time `json:"before,omitempty"`    // zero = unbounded; inclusive upper bound
	AfterSeq int64     `json:"after_seq,omitempty"` // 0 = unbounded; strictly greater than
	Limit    int64     `json:"limit,omitempty"`     // 0 = unbounded
}

// QueryJournal returns the journal events matching every active filter in q.
// Filters are ANDed:
//
//   - RunID picks one run; the empty string queries every run in the store.
//   - TaskID matches ev.Task exactly; the empty string matches everything,
//     including run-level events (run.created, run.closed, worktree.*).
//   - Kinds matches ev.Kind exactly; nil or empty matches every kind.
//   - After and Before bound ev.At (an RFC 3339 timestamp) inclusively. An
//     event whose timestamp cannot be parsed is excluded while a time window
//     is active, because it cannot be proven to lie inside it.
//   - AfterSeq keeps events with Seq strictly greater than it — a pagination
//     cursor: pass the last sequence seen to resume a feed.
//   - Limit caps the result after filtering and ordering, keeping the earliest
//     matches; zero or negative means unbounded.
//
// The result is deterministic: events from each run are returned in ascending
// sequence order, and runs are visited in ascending id order (single-run
// queries are simply sequence-ascending, the journal's own append order). A
// query with no matches returns nil. Reading mirrors Store.Events: the store
// lock is held while each journal is replayed from disk.
func QueryJournal(s *Store, q Query) ([]Event, error) {
	if s == nil {
		return nil, fmt.Errorf("journal query requires a store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]string, 0, 1)
	if q.RunID != "" {
		if _, ok := s.runs[q.RunID]; !ok {
			return nil, fmt.Errorf("run %q not found", q.RunID)
		}
		ids = append(ids, q.RunID)
	} else {
		for id := range s.runs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
	}

	// jqEntry keeps the owning run so a multi-run result still has one
	// deterministic order even though sequence numbers restart per run.
	type jqEntry struct {
		run string
		ev  Event
	}
	all := make([]jqEntry, 0, 16)
	for _, id := range ids {
		events, err := Replay(s.journalPath(id))
		if err != nil {
			return nil, err
		}
		for _, ev := range events {
			if jqMatches(ev, q) {
				all = append(all, jqEntry{run: id, ev: ev})
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].run != all[j].run {
			return all[i].run < all[j].run
		}
		return all[i].ev.Seq < all[j].ev.Seq
	})
	if q.Limit > 0 && int64(len(all)) > q.Limit {
		all = all[:q.Limit]
	}
	if len(all) == 0 {
		return nil, nil
	}
	out := make([]Event, 0, len(all))
	for _, e := range all {
		out = append(out, e.ev)
	}
	return out, nil
}

// jqMatches reports whether one event satisfies every active filter in q.
// Inactive filters — an empty id, an empty kind set, a zero time, a zero
// sequence — match everything.
func jqMatches(ev Event, q Query) bool {
	if q.TaskID != "" && ev.Task != q.TaskID {
		return false
	}
	if len(q.Kinds) > 0 && !jqKindIn(ev.Kind, q.Kinds) {
		return false
	}
	if !q.After.IsZero() || !q.Before.IsZero() {
		at, ok := jqParseAt(ev.At)
		if !ok {
			// An unreadable timestamp cannot be proven inside the window.
			return false
		}
		if !q.After.IsZero() && at.Before(q.After) {
			return false
		}
		if !q.Before.IsZero() && at.After(q.Before) {
			return false
		}
	}
	if q.AfterSeq > 0 && ev.Seq <= q.AfterSeq {
		return false
	}
	return true
}

// jqKindIn reports whether kind is one of the requested kinds.
func jqKindIn(kind string, kinds []string) bool {
	for _, want := range kinds {
		if want == kind {
			return true
		}
	}
	return false
}

// jqParseAt parses a journal timestamp (always written as RFC 3339 by
// Journal.Append) back to a comparable time.
func jqParseAt(at string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Summary is a quick aggregation over a slice of journal events, sized for
// headers and badges: how much happened, of what, and over which span.
type Summary struct {
	Total  int            `json:"total"`
	ByKind map[string]int `json:"by_kind"`
	First  string         `json:"first"` // ev.At of the first entry, "" when empty
	Last   string         `json:"last"`  // ev.At of the last entry, "" when empty
}

// Summarize aggregates the entries as given: First and Last are the
// timestamps of the first and last slice elements, so pass an ordered slice —
// such as the output of QueryJournal — for meaningful bounds. ByKind is always
// non-nil, so an empty aggregation marshals as {} rather than null.
func Summarize(entries []Event) Summary {
	sum := Summary{ByKind: map[string]int{}}
	for i, ev := range entries {
		sum.Total++
		sum.ByKind[ev.Kind]++
		if i == 0 {
			sum.First = ev.At
		}
		sum.Last = ev.At
	}
	return sum
}
