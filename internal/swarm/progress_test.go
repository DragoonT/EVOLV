package swarm

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// progFakeClock pins progNow to a mutable instant the test advances by hand,
// and restores the real clock when the test ends.
func progFakeClock(t *testing.T, start time.Time) *time.Time {
	orig := progNow
	cur := start
	progNow = func() time.Time { return cur }
	t.Cleanup(func() { progNow = orig })
	return &cur
}

// progMixedFixture registers seven agents and drives them through a mixed set
// of statuses so count assertions stay exact.
func progMixedFixture(tr *Tracker) {
	tr.Register("coder-02", "")
	tr.Register("critic-01", "Critic Prime")
	tr.Register("ideator-01", "")
	tr.Register("judge-01", "")
	tr.Register("tester-01", "")
	tr.Register("worker-a", "Worker A")
	tr.Register("worker-b", "")
	tr.MarkNote("ideator-01", "provider timeout: context deadline exceeded")
	tr.MarkNote("tester-01", "tests green")
	tr.Mark("coder-02", "running")
	tr.Mark("critic-01", "done")
	tr.Mark("ideator-01", "error")
	tr.Mark("judge-01", "stopped")
	tr.Mark("tester-01", "succeeded")
	tr.Mark("worker-b", "thinking")
}

func progAssertCounts(t *testing.T, snap Snapshot, total, pending, running, succeeded, failed, cancelled int) {
	t.Helper()
	if snap.Total != total || snap.Pending != pending || snap.Running != running ||
		snap.Succeeded != succeeded || snap.Failed != failed || snap.Cancelled != cancelled {
		t.Fatalf("counts = total:%d pending:%d running:%d succeeded:%d failed:%d cancelled:%d, want %d/%d/%d/%d/%d/%d",
			snap.Total, snap.Pending, snap.Running, snap.Succeeded, snap.Failed, snap.Cancelled,
			total, pending, running, succeeded, failed, cancelled)
	}
	if sum := snap.Pending + snap.Running + snap.Succeeded + snap.Failed + snap.Cancelled; sum != snap.Total {
		t.Fatalf("buckets sum to %d but Total is %d", sum, snap.Total)
	}
}

func TestTrackerMixedFixtureCounts(t *testing.T) {
	base := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	progFakeClock(t, base) // freeze the clock at base for exact timestamp asserts
	tr := NewTracker()
	progMixedFixture(tr)

	snap := tr.Snapshot()
	progAssertCounts(t, snap, 7, 1, 2, 2, 1, 1)
	if !snap.StartedAt.Equal(base) || !snap.UpdatedAt.Equal(base) {
		t.Fatalf("timestamps = started:%v updated:%v, want both %v", snap.StartedAt, snap.UpdatedAt, base)
	}
	wantIDs := []string{"coder-02", "critic-01", "ideator-01", "judge-01", "tester-01", "worker-a", "worker-b"}
	if len(snap.PerAgent) != len(wantIDs) {
		t.Fatalf("PerAgent has %d cards, want %d", len(snap.PerAgent), len(wantIDs))
	}
	for i, want := range wantIDs {
		if got := snap.PerAgent[i].ID; got != want {
			t.Fatalf("PerAgent[%d].ID = %q, want %q (sorted by id)", i, got, want)
		}
	}
	byID := map[string]AgentState{}
	for _, card := range snap.PerAgent {
		byID[card.ID] = card
	}
	if got := byID["worker-b"].Status; got != ProgStatusRunning {
		t.Fatalf("worker-b status = %q, want %q (thinking alias)", got, ProgStatusRunning)
	}
	if got := byID["tester-01"].Status; got != ProgStatusDone {
		t.Fatalf("tester-01 status = %q, want %q (succeeded alias)", got, ProgStatusDone)
	}
	if got := byID["ideator-01"].Last; got != "provider timeout: context deadline exceeded" {
		t.Fatalf("ideator-01 last = %q, want the failure note", got)
	}
	if got, want := snap.LastErrors, []string{"ideator-01: provider timeout: context deadline exceeded"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("LastErrors = %v, want %v", got, want)
	}
	if len(snap.PerAgent) != snap.Total {
		t.Fatalf("PerAgent len %d != Total %d", len(snap.PerAgent), snap.Total)
	}
}

func TestTrackerDeregisterRecounts(t *testing.T) {
	tr := NewTracker()
	progMixedFixture(tr)
	before := tr.Snapshot()
	if before.LastErrors[0] != "ideator-01: provider timeout: context deadline exceeded" {
		t.Fatalf("LastErrors = %v", before.LastErrors)
	}

	tr.Deregister("tester-01") // a succeeded agent leaves the roster
	after := tr.Snapshot()
	progAssertCounts(t, after, 6, 1, 2, 1, 1, 1)
	for _, card := range after.PerAgent {
		if card.ID == "tester-01" {
			t.Fatal("deregistered agent still in PerAgent")
		}
	}
	if len(after.LastErrors) != 1 || after.LastErrors[0] != before.LastErrors[0] {
		t.Fatalf("history should survive deregister, got %v", after.LastErrors)
	}

	ghostBefore := tr.Snapshot()
	tr.Deregister("ghost") // unknown id is a no-op
	ghostAfter := tr.Snapshot()
	progAssertCounts(t, ghostAfter, 6, 1, 2, 1, 1, 1)
	if !ghostAfter.UpdatedAt.Equal(ghostBefore.UpdatedAt) {
		t.Fatal("no-op deregister must not touch UpdatedAt")
	}
}

func TestProgLastErrorsRingCapFive(t *testing.T) {
	tr := NewTracker()
	for _, id := range []string{"e1", "e2", "e3", "e4", "e5", "e6", "e7"} {
		tr.Register(id, "")
	}
	tr.MarkNote("e3", "boom-3")
	tr.MarkNote("e5", "boom-5")
	for _, id := range []string{"e1", "e2", "e3", "e4", "e5", "e6", "e7"} {
		tr.Mark(id, ProgStatusError)
	}
	// Seven failures, ring keeps the five newest, newest first.
	want := []string{"e7: error", "e6: error", "e5: boom-5", "e4: error", "e3: boom-3"}
	if got := tr.Snapshot().LastErrors; len(got) != len(want) {
		t.Fatalf("LastErrors len = %d, want %d: %v", len(got), len(want), got)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("LastErrors[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
			}
		}
	}

	// Re-marking an already-failed agent must not push a duplicate.
	tr.Mark("e7", ProgStatusError)
	if got := tr.Snapshot().LastErrors; got[0] != "e7: error" || got[1] != "e6: error" {
		t.Fatalf("duplicate failure recorded: %v", got)
	}
	// Marks on unknown ids never touch the ring.
	tr.Mark("ghost", ProgStatusError)
	if got := tr.Snapshot().LastErrors; got[0] != "e7: error" {
		t.Fatalf("unknown-id mark polluted ring: %v", got)
	}
	// A new failure event after recovery re-enters the ring as the newest.
	tr.Mark("e6", ProgStatusDone)
	tr.Mark("e6", ProgStatusError)
	want2 := []string{"e6: error", "e7: error", "e6: error", "e5: boom-5", "e4: error"}
	got2 := tr.Snapshot().LastErrors
	for i := range want2 {
		if got2[i] != want2[i] {
			t.Fatalf("LastErrors[%d] = %q, want %q (full: %v)", i, got2[i], want2[i], got2)
		}
	}
}

func TestProgThroughputPerMinWindow(t *testing.T) {
	base := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	clock := progFakeClock(t, base)
	tr := NewTracker()
	tr.Register("a", "A")
	tr.Register("b", "B")

	*clock = base.Add(4 * time.Minute)
	tr.Mark("a", ProgStatusDone) // success event at +4m
	*clock = base.Add(6 * time.Minute)
	tr.Mark("b", ProgStatusDone) // success event at +6m

	*clock = base.Add(8 * time.Minute) // window (+3m, +8m]: both events
	if got := tr.Snapshot().ThroughputPerMin; got != 0.4 {
		t.Fatalf("ThroughputPerMin = %v, want 0.4 (2 events / 5 minutes)", got)
	}

	tr.Mark("a", ProgStatusDone) // same-status re-mark adds no event
	if got := tr.Snapshot().ThroughputPerMin; got != 0.4 {
		t.Fatalf("re-mark double-counted: ThroughputPerMin = %v, want 0.4", got)
	}

	*clock = base.Add(10 * time.Minute) // window (+5m, +10m]: only b
	if got := tr.Snapshot().ThroughputPerMin; got != 0.2 {
		t.Fatalf("ThroughputPerMin = %v, want 0.2", got)
	}

	*clock = base.Add(12 * time.Minute) // window (+7m, +12m]: empty
	if got := tr.Snapshot().ThroughputPerMin; got != 0 {
		t.Fatalf("ThroughputPerMin = %v, want 0 outside the window", got)
	}

	// A tracker that never saw activity has no known rate.
	var zero Tracker
	if z := zero.Snapshot(); z.ThroughputPerMin != 0 || !z.StartedAt.IsZero() {
		t.Fatalf("unknown rate = %v started=%v, want 0 and zero time", z.ThroughputPerMin, z.StartedAt)
	}
}

func TestProgStatusAliasesNormalize(t *testing.T) {
	tr := NewTracker()
	aliases := []struct {
		input string
		want  string
	}{
		{"pending", ProgStatusIdle},
		{"ready", ProgStatusIdle},
		{"idle", ProgStatusIdle},
		{"", ProgStatusIdle},
		{"bogus-status", ProgStatusIdle},
		{"running", ProgStatusRunning},
		{"thinking", ProgStatusRunning},
		{"succeeded", ProgStatusDone},
		{"done", ProgStatusDone},
		{"voted", ProgStatusDone},
		{"failed", ProgStatusError},
		{"error", ProgStatusError},
		{"cancelled", ProgStatusStopped},
		{"canceled", ProgStatusStopped},
		{"stopped", ProgStatusStopped},
	}
	for i, tc := range aliases {
		id := fmt.Sprintf("agent-%02d", i)
		tr.Register(id, "")
		tr.Mark(id, tc.input)
	}
	snap := tr.Snapshot()
	// 5 idle-ish (pending, ready, idle, "", bogus) / 2 running / 3 done-ish /
	// 2 failed-ish / 3 stopped-ish.
	progAssertCounts(t, snap, 15, 5, 2, 3, 2, 3)
	for _, card := range snap.PerAgent {
		if card.Status != ProgStatusIdle && card.Status != ProgStatusRunning &&
			card.Status != ProgStatusDone && card.Status != ProgStatusError && card.Status != ProgStatusStopped {
			t.Fatalf("card %s carries non-canonical status %q", card.ID, card.Status)
		}
	}
	for i, tc := range aliases {
		if got := snap.PerAgent[i].Status; got != tc.want {
			t.Fatalf("Mark(%q) stored %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestProgZeroValueTrackerUsable(t *testing.T) {
	var tr Tracker // zero value: no constructor call
	snap := tr.Snapshot()
	progAssertCounts(t, snap, 0, 0, 0, 0, 0, 0)
	if snap.PerAgent == nil || snap.LastErrors == nil {
		t.Fatal("empty snapshot must expose non-nil slices so JSON shows []")
	}
	if !snap.StartedAt.IsZero() {
		t.Fatalf("StartedAt = %v, want zero", snap.StartedAt)
	}

	tr.Register("x", "X")
	tr.Mark("x", "running")
	tr.MarkNote("x", "halfway")
	tr.Mark("x", ProgStatusDone)
	snap = tr.Snapshot()
	progAssertCounts(t, snap, 1, 0, 0, 1, 0, 0)
	tr.Deregister("x")
	if got := tr.Snapshot().Total; got != 0 {
		t.Fatalf("after deregister Total = %d, want 0", got)
	}
	if _, err := json.Marshal(tr.Snapshot()); err != nil {
		t.Fatalf("zero-value snapshot JSON: %v", err)
	}
}

func TestProgSnapshotJSONRoundTrip(t *testing.T) {
	tr := NewTracker()
	progMixedFixture(tr)
	snap := tr.Snapshot()

	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var keys map[string]any
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	for _, key := range []string{
		"total", "pending", "running", "succeeded", "failed", "cancelled",
		"started_at", "updated_at", "per_agent", "throughput_per_min", "last_errors",
	} {
		if _, ok := keys[key]; !ok {
			t.Fatalf("JSON missing key %q: %s", key, raw)
		}
	}

	var back Snapshot
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal to Snapshot: %v", err)
	}
	if back.Total != snap.Total || back.Failed != snap.Failed || back.Cancelled != snap.Cancelled {
		t.Fatalf("round trip counts differ: %+v vs %+v", back, snap)
	}
	if len(back.PerAgent) != len(snap.PerAgent) || back.PerAgent[0].ID != snap.PerAgent[0].ID {
		t.Fatalf("round trip PerAgent differs: %+v", back.PerAgent)
	}
	if len(back.LastErrors) != 1 || back.LastErrors[0] != snap.LastErrors[0] {
		t.Fatalf("round trip LastErrors differs: %v", back.LastErrors)
	}
	if !back.StartedAt.Equal(snap.StartedAt) || !back.UpdatedAt.Equal(snap.UpdatedAt) {
		t.Fatalf("round trip timestamps differ: %v/%v", back.StartedAt, back.UpdatedAt)
	}
}

func TestProgConcurrentMarkSnapshot(t *testing.T) {
	tr := NewTracker()
	const goroutines = 8
	const iterations = 300
	statuses := []string{"running", "done", "error", "stopped", "thinking", "bogus"}
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := fmt.Sprintf("agent-%02d", (g*5+i)%40)
				switch i % 4 {
				case 0:
					tr.Register(id, fmt.Sprintf("agent label %d-%d", g, i))
				case 1:
					tr.Mark(id, statuses[i%len(statuses)])
				case 2:
					tr.MarkNote(id, fmt.Sprintf("note %d-%d", g, i))
				case 3:
					_ = tr.Snapshot()
				}
			}
		}(g)
	}
	wg.Wait()

	snap := tr.Snapshot()
	progAssertCounts(t, snap, 40, snap.Pending, snap.Running, snap.Succeeded, snap.Failed, snap.Cancelled)
	if len(snap.PerAgent) != 40 {
		t.Fatalf("PerAgent len = %d, want 40", len(snap.PerAgent))
	}
	for i := 1; i < len(snap.PerAgent); i++ {
		if snap.PerAgent[i-1].ID >= snap.PerAgent[i].ID {
			t.Fatalf("PerAgent not strictly sorted at %d: %q >= %q", i, snap.PerAgent[i-1].ID, snap.PerAgent[i].ID)
		}
	}
}

func TestProgReRegisterKeepsStatus(t *testing.T) {
	tr := NewTracker()
	tr.Register("r1", "Old Label")
	tr.Mark("r1", "running")
	tr.Register("r1", "New Label") // re-register updates the label, keeps progress
	tr.Register("r2", "")
	snap := tr.Snapshot()
	progAssertCounts(t, snap, 2, 1, 1, 0, 0, 0)
	for _, card := range snap.PerAgent {
		switch card.ID {
		case "r1":
			if card.Name != "New Label" || card.Status != ProgStatusRunning {
				t.Fatalf("r1 = name %q status %q, want New Label/running", card.Name, card.Status)
			}
		case "r2":
			if card.Status != ProgStatusIdle {
				t.Fatalf("fresh r2 status = %q, want idle", card.Status)
			}
		}
	}
}

func TestProgRosterEnrichment(t *testing.T) {
	tr := NewTracker()
	tr.Register("coder-02", "")
	tr.Register("ideator-07", "My Ideator")
	tr.Register("worker-x", "")
	tr.Register("coder-0a", "")
	snap := tr.Snapshot()
	byID := map[string]AgentState{}
	for _, card := range snap.PerAgent {
		byID[card.ID] = card
	}
	coder := byID["coder-02"]
	if coder.Name != "Coder 02" || coder.Role != "coder" || coder.Index != 2 ||
		coder.Icon != "⌘" || coder.Color != "#4f6f8f" || coder.Blurb != "builds the thing" {
		t.Fatalf("coder-02 card = %+v", coder)
	}
	idea := byID["ideator-07"]
	if idea.Name != "My Ideator" || idea.Role != "ideator" || idea.Index != 7 {
		t.Fatalf("ideator-07 card = %+v", idea)
	}
	plain := byID["worker-x"]
	if plain.Role != "" || plain.Name != "" || plain.Index != 0 {
		t.Fatalf("worker-x should stay unenriched, got %+v", plain)
	}
	odd := byID["coder-0a"]
	if odd.Role != "coder" || odd.Index != 0 || odd.Name != "Coder" {
		t.Fatalf("coder-0a card = %+v", odd)
	}
}
