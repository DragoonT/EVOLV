package execution

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// jqFixtureTime maps a minute offset to the fixed timestamp used by the
// seeded run-a fixture, so every event has a distinct, parseable time.
func jqFixtureTime(minute int) time.Time {
	return time.Date(2026, 1, 1, 10, minute, 0, 0, time.UTC)
}

// jqFixtureAt is the wire form of jqFixtureTime, exactly what
// Journal.Append would write.
func jqFixtureAt(minute int) string { return jqFixtureTime(minute).Format(time.RFC3339) }

// jqSeedJournal writes events as a real newline-delimited journal at the
// store's own journal path, with hand-picked timestamps and sequences so the
// window and ordering tests are exact.
func jqSeedJournal(t *testing.T, path string, events []Event) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			t.Fatalf("encode seed event: %v", err)
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
}

// jqFixtureStore builds a real Store over a temp workspace holding three
// seeded runs. run-a has five events with known times, kinds and tasks; run-b
// has two; run-c has an event with an unreadable timestamp.
func jqFixtureStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()

	// A throwaway store hands us the on-disk journal layout without
	// hardcoding the file name.
	pathfinder, err := Open(root)
	if err != nil {
		t.Fatalf("open empty store: %v", err)
	}
	runA := []Event{
		{Seq: 1, At: jqFixtureAt(0), Kind: KindRunCreated, Data: json.RawMessage(`{"title":"seed a","status":"running"}`)},
		{Seq: 2, At: jqFixtureAt(1), Kind: KindTaskAdded, Task: "t1", Data: json.RawMessage(`{"id":"t1","title":"one","status":"queued"}`)},
		{Seq: 3, At: jqFixtureAt(2), Kind: KindTaskAdded, Task: "t2", Data: json.RawMessage(`{"id":"t2","title":"two","status":"queued"}`)},
		{Seq: 4, At: jqFixtureAt(3), Kind: KindTaskMoved, Task: "t1", Data: json.RawMessage(`{"from":"queued","to":"paused"}`)},
		{Seq: 5, At: jqFixtureAt(4), Kind: KindTaskMoved, Task: "t2", Data: json.RawMessage(`{"from":"queued","to":"failed"}`)},
	}
	runB := []Event{
		{Seq: 1, At: "2026-01-01T11:00:00Z", Kind: KindRunCreated, Data: json.RawMessage(`{"title":"seed b"}`)},
		{Seq: 2, At: "2026-01-01T11:00:01Z", Kind: KindTaskAdded, Task: "u1", Data: json.RawMessage(`{"id":"u1","title":"u","status":"queued"}`)},
	}
	runC := []Event{
		{Seq: 1, At: "2026-01-01T12:00:00Z", Kind: KindRunCreated, Data: json.RawMessage(`{"title":"seed c"}`)},
		{Seq: 2, At: "not-a-timestamp", Kind: KindTaskAdded, Task: "x1", Data: json.RawMessage(`{"id":"x1","title":"x","status":"queued"}`)},
	}
	jqSeedJournal(t, pathfinder.journalPath("run-a"), runA)
	jqSeedJournal(t, pathfinder.journalPath("run-b"), runB)
	jqSeedJournal(t, pathfinder.journalPath("run-c"), runC)
	if err := pathfinder.CloseAll(); err != nil {
		t.Fatalf("close pathfinder: %v", err)
	}

	s, err := Open(root)
	if err != nil {
		t.Fatalf("open fixture store: %v", err)
	}
	t.Cleanup(func() { _ = s.CloseAll() })
	return s
}

// jqSeqs extracts the sequence numbers, the cheapest fingerprint of order.
func jqSeqs(events []Event) []int64 {
	out := make([]int64, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Seq)
	}
	return out
}

// jqWantSeqs asserts the exact sequence fingerprint of a result.
func jqWantSeqs(t *testing.T, got []Event, want ...int64) {
	t.Helper()
	gotSeqs := jqSeqs(got)
	if len(gotSeqs) != len(want) {
		t.Fatalf("sequence fingerprint = %v, want %v", gotSeqs, want)
	}
	for i := range want {
		if gotSeqs[i] != want[i] {
			t.Fatalf("sequence fingerprint = %v, want %v", gotSeqs, want)
		}
	}
}

func TestQueryJournalFiltersOnSeededFixture(t *testing.T) {
	s := jqFixtureStore(t)

	// Task filter: t1 was added (seq 2) and moved (seq 4).
	got, err := QueryJournal(s, Query{RunID: "run-a", TaskID: "t1"})
	if err != nil {
		t.Fatalf("query by task: %v", err)
	}
	jqWantSeqs(t, got, 2, 4)
	for _, ev := range got {
		if ev.Task != "t1" {
			t.Fatalf("task filter leaked event for %q", ev.Task)
		}
	}

	// Kind filter.
	got, err = QueryJournal(s, Query{RunID: "run-a", Kinds: []string{KindTaskMoved}})
	if err != nil {
		t.Fatalf("query by kind: %v", err)
	}
	jqWantSeqs(t, got, 4, 5)

	// An empty kind set behaves like nil: every kind.
	got, err = QueryJournal(s, Query{RunID: "run-a", Kinds: []string{}})
	if err != nil {
		t.Fatalf("query with empty kinds: %v", err)
	}
	jqWantSeqs(t, got, 1, 2, 3, 4, 5)

	// A kind that never occurs yields a clean empty result.
	got, err = QueryJournal(s, Query{RunID: "run-a", Kinds: []string{"kind.absent"}})
	if err != nil {
		t.Fatalf("query with unknown kind: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("unknown kind returned %d events, want 0", len(got))
	}

	// ANDed filters: t2 plus {task.added, task.moved} keeps seq 3 and 5 only.
	got, err = QueryJournal(s, Query{RunID: "run-a", TaskID: "t2", Kinds: []string{KindTaskAdded, KindTaskMoved}})
	if err != nil {
		t.Fatalf("query with combined filters: %v", err)
	}
	jqWantSeqs(t, got, 3, 5)
	if got[0].Kind != KindTaskAdded || got[1].Kind != KindTaskMoved {
		t.Fatalf("combined filter kinds = %q,%q", got[0].Kind, got[1].Kind)
	}
}

func TestQueryJournalTimeWindow(t *testing.T) {
	s := jqFixtureStore(t)

	// Both bounds are inclusive: [10:02, 10:04] keeps seq 3, 4, 5.
	got, err := QueryJournal(s, Query{RunID: "run-a", After: jqFixtureTime(2), Before: jqFixtureTime(4)})
	if err != nil {
		t.Fatalf("query with window: %v", err)
	}
	jqWantSeqs(t, got, 3, 4, 5)

	// Lower bound only.
	got, err = QueryJournal(s, Query{RunID: "run-a", After: jqFixtureTime(3)})
	if err != nil {
		t.Fatalf("query with after: %v", err)
	}
	jqWantSeqs(t, got, 4, 5)

	// Upper bound only.
	got, err = QueryJournal(s, Query{RunID: "run-a", Before: jqFixtureTime(1)})
	if err != nil {
		t.Fatalf("query with before: %v", err)
	}
	jqWantSeqs(t, got, 1, 2)

	// A single-instant window keeps exactly the event stamped at it.
	got, err = QueryJournal(s, Query{RunID: "run-a", After: jqFixtureTime(2), Before: jqFixtureTime(2)})
	if err != nil {
		t.Fatalf("query with instant window: %v", err)
	}
	jqWantSeqs(t, got, 3)

	// A window past the end of history matches nothing, cleanly.
	got, err = QueryJournal(s, Query{RunID: "run-a", After: jqFixtureTime(5), Before: jqFixtureTime(9)})
	if err != nil {
		t.Fatalf("query with empty window: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty window returned %d events, want 0", len(got))
	}

	// run-c's second event carries an unreadable timestamp: with a time
	// window active it cannot be proven inside, so it is excluded.
	got, err = QueryJournal(s, Query{RunID: "run-c", After: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("query run-c with window: %v", err)
	}
	jqWantSeqs(t, got, 1)

	// Without a window the same event is ordinary history again.
	got, err = QueryJournal(s, Query{RunID: "run-c"})
	if err != nil {
		t.Fatalf("query run-c: %v", err)
	}
	jqWantSeqs(t, got, 1, 2)
}

func TestQueryJournalSequenceWindowAndLimit(t *testing.T) {
	s := jqFixtureStore(t)

	// The cursor is exclusive: strictly after seq 2.
	got, err := QueryJournal(s, Query{RunID: "run-a", AfterSeq: 2})
	if err != nil {
		t.Fatalf("query with cursor: %v", err)
	}
	jqWantSeqs(t, got, 3, 4, 5)

	// Limit caps the ordered result: the earliest matches win.
	got, err = QueryJournal(s, Query{RunID: "run-a", AfterSeq: 2, Limit: 2})
	if err != nil {
		t.Fatalf("query with cursor and limit: %v", err)
	}
	jqWantSeqs(t, got, 3, 4)

	got, err = QueryJournal(s, Query{RunID: "run-a", Limit: 3})
	if err != nil {
		t.Fatalf("query with limit: %v", err)
	}
	jqWantSeqs(t, got, 1, 2, 3)

	// Limit zero and oversized limits are unbounded.
	got, err = QueryJournal(s, Query{RunID: "run-a"})
	if err != nil {
		t.Fatalf("query without limit: %v", err)
	}
	jqWantSeqs(t, got, 1, 2, 3, 4, 5)

	got, err = QueryJournal(s, Query{RunID: "run-a", Limit: 99})
	if err != nil {
		t.Fatalf("query with oversized limit: %v", err)
	}
	jqWantSeqs(t, got, 1, 2, 3, 4, 5)

	// A non-positive cursor is off.
	got, err = QueryJournal(s, Query{RunID: "run-a", AfterSeq: -1})
	if err != nil {
		t.Fatalf("query with negative cursor: %v", err)
	}
	jqWantSeqs(t, got, 1, 2, 3, 4, 5)
}

func TestQueryJournalRunSelectionAndOrdering(t *testing.T) {
	s := jqFixtureStore(t)

	// One run: its own sequence order.
	got, err := QueryJournal(s, Query{RunID: "run-b"})
	if err != nil {
		t.Fatalf("query run-b: %v", err)
	}
	jqWantSeqs(t, got, 1, 2)

	// Every run: ascending run id, sequence ascending within each run.
	got, err = QueryJournal(s, Query{})
	if err != nil {
		t.Fatalf("query all runs: %v", err)
	}
	jqWantSeqs(t, got, 1, 2, 3, 4, 5, 1, 2, 1, 2)

	// Deterministic: the same query twice returns identical output.
	again, err := QueryJournal(s, Query{})
	if err != nil {
		t.Fatalf("repeat query: %v", err)
	}
	if !reflect.DeepEqual(got, again) {
		t.Fatal("identical queries returned different results")
	}

	// An unknown run is an error, not a silent empty page.
	if _, err := QueryJournal(s, Query{RunID: "run-absent"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown run error = %v, want not-found error", err)
	}

	// A nil store is a programming error worth reporting.
	if _, err := QueryJournal(nil, Query{}); err == nil {
		t.Fatal("nil store returned no error")
	}
}

func TestQueryJournalEmptyAndNoMatches(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open empty store: %v", err)
	}
	defer func() { _ = s.CloseAll() }()

	got, err := QueryJournal(s, Query{})
	if err != nil {
		t.Fatalf("query empty store: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty store returned %d events, want 0", len(got))
	}
}

// TestQueryJournalFromRealStoreLifecycle drives the store through its real
// constructors and cross-checks the query result against an independent
// in-test filter over the full history.
func TestQueryJournalFromRealStoreLifecycle(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = s.CloseAll() }()

	if _, err := s.CreateRun("jq-run", "query lifecycle", "inspect me", []Task{NewTask("t1", "one"), NewTask("t2", "two")}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := s.Transition("jq-run", "t1", StatusPlanning); err != nil {
		t.Fatalf("transition t1: %v", err)
	}
	if _, err := s.Transition("jq-run", "t1", StatusRunning); err != nil {
		t.Fatalf("transition t1: %v", err)
	}
	if err := s.RecordAttempt("jq-run", "t2", "boom"); err != nil {
		t.Fatalf("record attempt: %v", err)
	}
	if _, err := s.Transition("jq-run", "t2", StatusCancelled); err != nil {
		t.Fatalf("transition t2: %v", err)
	}

	// Journal history so far: run.created, t1 added, t2 added, t1 → planning,
	// t1 → running, t2 attempt, t2 error, t2 → cancelled.
	full, err := QueryJournal(s, Query{RunID: "jq-run"})
	if err != nil {
		t.Fatalf("query full history: %v", err)
	}
	jqWantSeqs(t, full, 1, 2, 3, 4, 5, 6, 7, 8)

	// Task filter on live data: t1 is events 2, 4, 5, in ascending order.
	got, err := QueryJournal(s, Query{RunID: "jq-run", TaskID: "t1"})
	if err != nil {
		t.Fatalf("query t1: %v", err)
	}
	jqWantSeqs(t, got, 2, 4, 5)
	for _, ev := range got {
		if ev.Task != "t1" {
			t.Fatalf("task filter leaked event for %q", ev.Task)
		}
	}

	// Kind filter: exactly one attempt, and the paired error event.
	got, err = QueryJournal(s, Query{RunID: "jq-run", Kinds: []string{KindTaskAttempt}})
	if err != nil {
		t.Fatalf("query attempts: %v", err)
	}
	jqWantSeqs(t, got, 6)

	got, err = QueryJournal(s, Query{RunID: "jq-run", TaskID: "t2", Kinds: []string{KindTaskError}})
	if err != nil {
		t.Fatalf("query t2 errors: %v", err)
	}
	jqWantSeqs(t, got, 7)

	// Limit over live history keeps the earliest events.
	got, err = QueryJournal(s, Query{RunID: "jq-run", Limit: 3})
	if err != nil {
		t.Fatalf("query with limit: %v", err)
	}
	jqWantSeqs(t, got, 1, 2, 3)

	// Time window cross-check: an independent reimplementation of the
	// inclusive filter applied to the full history must agree exactly.
	after, ok := jqParseAt(full[4].At)
	if !ok {
		t.Fatalf("journal timestamp %q did not parse", full[4].At)
	}
	got, err = QueryJournal(s, Query{RunID: "jq-run", After: after})
	if err != nil {
		t.Fatalf("query with live window: %v", err)
	}
	want := []Event{}
	for _, ev := range full {
		if at, ok := jqParseAt(ev.At); ok && !at.Before(after) {
			want = append(want, ev)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("window query = %v, want %v", jqSeqs(got), jqSeqs(want))
	}

	// Summarize agrees with the query it summarises.
	sum := Summarize(full)
	if sum.Total != len(full) {
		t.Fatalf("summary total = %d, want %d", sum.Total, len(full))
	}
	counted := 0
	for _, n := range sum.ByKind {
		counted += n
	}
	if counted != sum.Total {
		t.Fatalf("by-kind counts sum to %d, want %d", counted, sum.Total)
	}
	if sum.First != full[0].At || sum.Last != full[len(full)-1].At {
		t.Fatalf("summary span = %q..%q, want %q..%q", sum.First, sum.Last, full[0].At, full[len(full)-1].At)
	}
	if sum.ByKind[KindTaskAttempt] != 1 || sum.ByKind[KindRunCreated] != 1 {
		t.Fatalf("by-kind = %v, want one attempt and one run.created", sum.ByKind)
	}
}

func TestSummarizeCountsMixedFixture(t *testing.T) {
	entries := []Event{
		{Seq: 1, At: "2026-01-01T10:00:00Z", Kind: KindRunCreated},
		{Seq: 2, At: "2026-01-01T10:00:01Z", Kind: KindTaskAdded, Task: "t1"},
		{Seq: 3, At: "2026-01-01T10:00:02Z", Kind: KindTaskAdded, Task: "t2"},
		{Seq: 4, At: "2026-01-01T10:00:03Z", Kind: KindTaskMoved, Task: "t1"},
		{Seq: 5, At: "2026-01-01T10:00:04Z", Kind: KindTaskError, Task: "t1"},
	}
	sum := Summarize(entries)
	if sum.Total != 5 {
		t.Fatalf("total = %d, want 5", sum.Total)
	}
	wantByKind := map[string]int{
		KindRunCreated: 1,
		KindTaskAdded:  2,
		KindTaskMoved:  1,
		KindTaskError:  1,
	}
	if !reflect.DeepEqual(sum.ByKind, wantByKind) {
		t.Fatalf("by-kind = %v, want %v", sum.ByKind, wantByKind)
	}
	if sum.First != entries[0].At {
		t.Fatalf("first = %q, want %q", sum.First, entries[0].At)
	}
	if sum.Last != entries[len(entries)-1].At {
		t.Fatalf("last = %q, want %q", sum.Last, entries[len(entries)-1].At)
	}
}

func TestSummarizeEmptyInput(t *testing.T) {
	for _, entries := range [][]Event{nil, {}} {
		sum := Summarize(entries)
		if sum.Total != 0 {
			t.Fatalf("total = %d, want 0", sum.Total)
		}
		if sum.ByKind == nil || len(sum.ByKind) != 0 {
			t.Fatalf("by-kind = %v, want a non-nil empty map", sum.ByKind)
		}
		if sum.First != "" || sum.Last != "" {
			t.Fatalf("span = %q..%q, want empty bounds", sum.First, sum.Last)
		}
	}
}
