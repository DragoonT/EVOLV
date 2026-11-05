package execution

// Tests for the in-process event bus (events.go). Every blocking wait is
// bounded by a deadline well under 100ms so a regression surfaces as a fast,
// precise failure instead of a hung test run.

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// evtDeadline is the upper bound for any single wait in these tests.
const evtDeadline = 60 * time.Millisecond

// evtRecv tries to receive one event before the deadline.
func evtRecv(c <-chan BusEvent, timeout time.Duration) (BusEvent, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case ev, ok := <-c:
		return ev, ok
	case <-timer.C:
		return BusEvent{}, false
	}
}

// evtCollect gathers want events before the deadline, failing the test
// otherwise, and returns what it got.
func evtCollect(t *testing.T, c <-chan BusEvent, want int, timeout time.Duration) []BusEvent {
	t.Helper()
	var got []BusEvent
	deadline := time.After(timeout)
	for len(got) < want {
		select {
		case ev, ok := <-c:
			if !ok {
				t.Fatalf("channel closed after %d/%d events", len(got), want)
			}
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timed out after %d/%d events", len(got), want)
		}
	}
	return got
}

func TestBusPublishReachesAllSubscribersInOrder(t *testing.T) {
	bus := NewBus()
	defer bus.Close()
	subs := make([]Subscription, 3)
	for i := range subs {
		sub, err := bus.Subscribe(32)
		if err != nil {
			t.Fatalf("subscribe %d: %v", i, err)
		}
		subs[i] = sub
	}
	const n = 20
	for i := 0; i < n; i++ {
		bus.Publish(BusEventFrom("run-1", Event{Seq: int64(i + 1), At: timestamp(), Kind: KindTaskMoved, Task: "t1"}))
	}
	for i, sub := range subs {
		got := evtCollect(t, sub.C, n, evtDeadline)
		for j, ev := range got {
			if ev.Seq != int64(j+1) {
				t.Fatalf("subscriber %d: event %d has Seq %d, want %d", i, j, ev.Seq, j+1)
			}
			if ev.RunID != "run-1" || ev.TaskID() != "t1" {
				t.Fatalf("subscriber %d: bad routing fields: %+v", i, ev)
			}
		}
	}
	if stats := bus.Stats(); stats.Published != n || stats.Subscribers != 3 || len(stats.Dropped) != 3 {
		t.Fatalf("stats = %+v, want %d published, 3 subscribers, empty drops", stats, n)
	}
}

func TestBusSlowSubscriberDoesNotBlockOthers(t *testing.T) {
	bus := NewBus()
	defer bus.Close()
	slow, err := bus.Subscribe(1)
	if err != nil {
		t.Fatalf("subscribe slow: %v", err)
	}
	fast, err := bus.Subscribe(128)
	if err != nil {
		t.Fatalf("subscribe fast: %v", err)
	}
	const n = 100
	start := time.Now()
	for i := 0; i < n; i++ {
		bus.Publish(BusEventFrom("run-1", Event{Seq: int64(i + 1), At: timestamp(), Kind: KindTaskMoved, Task: "t1"}))
	}
	// Publishing must never block on the full slow subscriber.
	if elapsed := time.Since(start); elapsed > evtDeadline {
		t.Fatalf("publishing %d events took %v, publish is blocking", n, elapsed)
	}
	got := evtCollect(t, fast.C, n, evtDeadline)
	for i, ev := range got {
		if ev.Seq != int64(i+1) {
			t.Fatalf("fast subscriber: event %d has Seq %d, want %d (order broken)", i, ev.Seq, i+1)
		}
	}
	stats := bus.Stats()
	if stats.Dropped[slow.ID] == 0 {
		t.Fatalf("slow subscriber dropped nothing with buffer 1 and %d events: %+v", n, stats)
	}
	if stats.Dropped[fast.ID] != 0 {
		t.Fatalf("fast subscriber dropped %d events: %+v", stats.Dropped[fast.ID], stats)
	}
	// The slow subscriber can never hold more than its buffer allowed, so a
	// plain drain terminates after at most cap(slow.C) events.
	evtRecv(slow.C, evtDeadline) // optional: buffered events may or may not remain
	for {
		if _, ok := evtRecv(slow.C, time.Millisecond); !ok {
			break
		}
	}
}

func TestBusUnsubscribeStopsDelivery(t *testing.T) {
	bus := NewBus()
	defer bus.Close()
	sub, err := bus.Subscribe(8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	bus.Publish(BusEventFrom("run-1", Event{Seq: 1, At: timestamp(), Kind: KindTaskAdded, Task: "t1"}))
	if _, ok := evtRecv(sub.C, evtDeadline); !ok {
		t.Fatalf("no event delivered before unsubscribe")
	}
	bus.Unsubscribe(sub.ID)
	bus.Unsubscribe(sub.ID) // idempotent
	for i := 0; i < 5; i++ {
		bus.Publish(BusEventFrom("run-1", Event{Seq: int64(i + 2), At: timestamp(), Kind: KindTaskAdded, Task: "t1"}))
	}
	// The channel is closed by Unsubscribe, so delivery has stopped for good.
	if ev, ok := evtRecv(sub.C, evtDeadline); ok {
		t.Fatalf("received event %+v after unsubscribe", ev)
	}
	if stats := bus.Stats(); stats.Subscribers != 0 {
		t.Fatalf("stats = %+v, want 0 subscribers", stats)
	}
}

func TestBusCloseUnblocksWaitingSubscriber(t *testing.T) {
	bus := NewBus()
	sub, err := bus.Subscribe(8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	waiting := make(chan struct{})
	go func() {
		close(waiting)
		// Block on the channel exactly as a live consumer would.
		<-sub.C
	}()
	<-waiting
	done := make(chan struct{})
	go func() {
		bus.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(evtDeadline):
		t.Fatalf("bus.Close() did not return within %v", evtDeadline)
	}
	// Close releases the receiver: any receive now terminates.
	if _, ok := evtRecv(sub.C, evtDeadline); ok {
		t.Fatalf("channel open after Close")
	}
	bus.Close() // idempotent
	if _, err := bus.Subscribe(1); err == nil {
		t.Fatalf("Subscribe after Close succeeded, want error")
	}
}

func TestBusConcurrentPublishOrderedPerPublisher(t *testing.T) {
	bus := NewBus()
	defer bus.Close()
	const (
		publishers = 4
		perPub     = 50
	)
	subs := make([]Subscription, 2)
	for i := range subs {
		sub, err := bus.Subscribe(512)
		if err != nil {
			t.Fatalf("subscribe %d: %v", i, err)
		}
		subs[i] = sub
	}
	var wg sync.WaitGroup
	for p := 0; p < publishers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perPub; i++ {
				bus.Publish(BusEventFrom("run-1", Event{
					Seq:  int64(p*1000 + i + 1),
					At:   timestamp(),
					Kind: KindTaskMoved,
					Task: "p" + string(rune('a'+p)),
				}))
			}
		}(p)
	}
	wg.Wait()
	for i, sub := range subs {
		got := evtCollect(t, sub.C, publishers*perPub, evtDeadline)
		if len(got) != publishers*perPub {
			t.Fatalf("subscriber %d: got %d events, want %d", i, len(got), publishers*perPub)
		}
		// Every publisher's own sequence must arrive in order.
		last := make(map[string]int64, publishers)
		for _, ev := range got {
			if prev, ok := last[ev.TaskID()]; ok && ev.Seq <= prev {
				t.Fatalf("subscriber %d: publisher %s out of order: Seq %d after %d", i, ev.TaskID(), ev.Seq, prev)
			}
			last[ev.TaskID()] = ev.Seq
		}
		if len(last) != publishers {
			t.Fatalf("subscriber %d: saw %d publishers, want %d", i, len(last), publishers)
		}
	}
	if stats := bus.Stats(); stats.Published != publishers*perPub || stats.Dropped[subs[0].ID] != 0 || stats.Dropped[subs[1].ID] != 0 {
		t.Fatalf("stats = %+v, want %d published and no drops", stats, publishers*perPub)
	}
}

func TestBusSubscribeZeroBufferGetsDefault(t *testing.T) {
	bus := NewBus()
	defer bus.Close()
	for _, buffer := range []int{0, -5} {
		sub, err := bus.Subscribe(buffer)
		if err != nil {
			t.Fatalf("subscribe(%d): %v", buffer, err)
		}
		if got := cap(sub.C); got != evtDefaultBuffer {
			t.Fatalf("subscribe(%d) gave buffer %d, want %d", buffer, got, evtDefaultBuffer)
		}
		bus.Unsubscribe(sub.ID)
	}
	sub, err := bus.Subscribe(1 << 20)
	if err != nil {
		t.Fatalf("subscribe(huge): %v", err)
	}
	if got := cap(sub.C); got != evtMaxBuffer {
		t.Fatalf("oversized buffer clamped to %d, want %d", got, evtMaxBuffer)
	}
}

func TestBusEventFromExtractsTransition(t *testing.T) {
	data, err := json.Marshal(map[string]any{"from": StatusRunning, "to": StatusVerifying})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	at := timestamp()
	ev := BusEventFrom("run-9", Event{Seq: 7, At: at, Kind: KindTaskMoved, Task: "t1", Data: data})
	if ev.RunID != "run-9" || ev.TaskID() != "t1" || ev.Seq != 7 {
		t.Fatalf("journal fields lost: %+v", ev)
	}
	if ev.From != StatusRunning || ev.To != StatusVerifying {
		t.Fatalf("transition not extracted: from=%s to=%s", ev.From, ev.To)
	}
	if got := ev.Time(); got.IsZero() || got.Sub(time.Now().UTC()).Abs() > time.Minute {
		t.Fatalf("Time() = %v, want a parse of %q near now", got, at)
	}
	plain := BusEventFrom("run-9", Event{Seq: 8, At: at, Kind: KindTaskAdded, Task: "t2"})
	if plain.From != "" || plain.To != "" {
		t.Fatalf("non-transition event grew From/To: %+v", plain)
	}
}

func TestBusPublishToEmptyBusIsSafe(t *testing.T) {
	bus := NewBus()
	bus.Publish(BusEventFrom("run-1", Event{Seq: 1, At: timestamp(), Kind: KindRunCreated}))
	if stats := bus.Stats(); stats.Published != 1 || stats.Subscribers != 0 {
		t.Fatalf("stats = %+v, want 1 published, 0 subscribers", stats)
	}
}
