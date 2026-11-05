package execution

// This file adds a typed in-process event bus on top of the durable journal.
// The journal (journal.go) is the source of truth on disk; the bus is how the
// server and the UI observe run and task transitions live, without polling
// and without coupling writers to readers.
//
// Design rules that matter:
//
//   - The payload reuses the journal's own Event entry instead of declaring a
//     second Event type: there is exactly one Event shape in this package, so
//     journal records and live events can never drift apart. BusEvent embeds
//     it and adds only what the journal does not persist (which run the event
//     belongs to, and the from/to of a task.moved transition).
//   - Publish is non-blocking. A slow or stuck subscriber must never stall
//     the store's mutation path, so a full subscriber buffer drops the event
//     and counts the drop per subscriber; callers can decide what losing
//     events means for them.
//   - Every mutation of bus state happens under one mutex, which also makes
//     delivery order well defined: events reach each subscriber in publish
//     order, and a publisher's own events stay in that publisher's order even
//     under concurrent publishing.

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// evtDefaultBuffer is the subscriber buffer used when Subscribe is called
// with a zero or negative buffer.
const evtDefaultBuffer = 64

// evtMaxBuffer bounds any subscriber buffer: a huge buffer would only delay
// the drop decision, not prevent it.
const evtMaxBuffer = 4096

// BusEvent is one live execution event. Event is the journal entry that
// records the same fact on disk (Seq, At, Kind, Task, Data), so a subscriber
// can correlate a live event with the journal history byte for byte.
type BusEvent struct {
	Event
	// RunID identifies the run the event belongs to. The journal lives
	// one file per run, so the on-disk Event cannot carry this.
	RunID string `json:"run_id,omitempty"`
	// From and To are the transition endpoints; they are only set for
	// task.moved events, where the journal stores them inside Data.
	From Status `json:"from,omitempty"`
	To   Status `json:"to,omitempty"`
}

// TaskID returns the task the event is about; it is empty for run-level
// events. It is the journal's Task field under its usual bus-side name.
func (b BusEvent) TaskID() string { return b.Task }

// Time parses the journal timestamp of the event. The zero time is returned
// for an unparsable or empty At field.
func (b BusEvent) Time() time.Time {
	t, err := time.Parse(time.RFC3339Nano, b.At)
	if err != nil {
		return time.Time{}
	}
	return t
}

// BusEventFrom wraps a journal entry as a bus event for runID. For
// task.moved events it decodes the from/to endpoints out of Data so
// subscribers do not have to re-parse the payload.
func BusEventFrom(runID string, ev Event) BusEvent {
	be := BusEvent{Event: ev, RunID: runID}
	if ev.Kind != KindTaskMoved || len(ev.Data) == 0 {
		return be
	}
	var evtMove struct {
		From Status `json:"from"`
		To   Status `json:"to"`
	}
	if err := json.Unmarshal(ev.Data, &evtMove); err == nil {
		be.From, be.To = evtMove.From, evtMove.To
	}
	return be
}

// Subscription is a live feed of bus events. C is receive-only and is closed
// by Unsubscribe or Bus.Close, so the idioms `ev, ok := <-sub.C` and
// `for ev := range sub.C` both terminate cleanly. Buffered events that were
// enqueued before the close remain readable, then the channel reports closed.
type Subscription struct {
	ID uint64
	C  <-chan BusEvent
}

// BusStats is a point-in-time view of bus health.
type BusStats struct {
	// Subscribers is the number of live subscriptions.
	Subscribers int
	// Published counts Publish calls the bus accepted (not closed).
	Published uint64
	// Dropped maps subscription ID to the number of events that were
	// discarded because that subscriber's buffer was full.
	Dropped map[uint64]uint64
	// Closed reports whether the bus has been closed.
	Closed bool
}

// evtSubscriber is the bus's private view of one subscription. All fields are
// guarded by Bus's mutex.
type evtSubscriber struct {
	id      uint64
	ch      chan BusEvent
	dropped uint64
}

// Bus is a typed in-process pub/sub bus for execution events. It is safe for
// concurrent use: Subscribe, Unsubscribe, Publish, Stats and Close may all be
// called from any goroutine at any time. A zero Bus is ready to use, but
// callers normally create one with NewBus.
type Bus struct {
	evtMu     sync.Mutex
	evtSubs   map[uint64]*evtSubscriber
	evtNextID uint64
	evtPub    uint64
	evtClosed bool
}

// NewBus returns an empty, ready-to-use event bus.
func NewBus() *Bus {
	return &Bus{evtSubs: map[uint64]*evtSubscriber{}}
}

// Subscribe registers a new subscription. A buffer of zero or less selects a
// sane default; oversized buffers are clamped. Subscribe fails only after the
// bus has been closed.
func (b *Bus) Subscribe(buffer int) (Subscription, error) {
	if buffer <= 0 {
		buffer = evtDefaultBuffer
	}
	if buffer > evtMaxBuffer {
		buffer = evtMaxBuffer
	}
	b.evtMu.Lock()
	defer b.evtMu.Unlock()
	if b.evtClosed {
		return Subscription{}, fmt.Errorf("event bus is closed")
	}
	b.evtNextID++
	sub := &evtSubscriber{id: b.evtNextID, ch: make(chan BusEvent, buffer)}
	b.evtSubs[sub.id] = sub
	return Subscription{ID: sub.id, C: sub.ch}, nil
}

// Unsubscribe removes a subscription and closes its channel, so a receiver
// blocked on it is released. Unknown IDs are ignored; unsubscribing twice is
// harmless. Events already buffered remain readable before the close is
// observed.
func (b *Bus) Unsubscribe(id uint64) {
	b.evtMu.Lock()
	defer b.evtMu.Unlock()
	sub, ok := b.evtSubs[id]
	if !ok {
		return
	}
	delete(b.evtSubs, id)
	close(sub.ch)
}

// Publish delivers ev to every live subscription without ever blocking. If a
// subscriber's buffer is full the event is dropped for that subscriber and
// its drop counter is bumped; all other subscribers still receive it.
// Publishing to a closed bus is a no-op.
func (b *Bus) Publish(ev BusEvent) {
	b.evtMu.Lock()
	defer b.evtMu.Unlock()
	if b.evtClosed {
		return
	}
	b.evtPub++
	for _, sub := range b.evtSubs {
		select {
		case sub.ch <- ev:
		default:
			sub.dropped++
		}
	}
}

// Stats returns a snapshot of bus health, including per-subscriber drop
// counts, for callers that need to know whether live delivery lost events.
func (b *Bus) Stats() BusStats {
	b.evtMu.Lock()
	defer b.evtMu.Unlock()
	stats := BusStats{
		Subscribers: len(b.evtSubs),
		Published:   b.evtPub,
		Dropped:     make(map[uint64]uint64, len(b.evtSubs)),
		Closed:      b.evtClosed,
	}
	for id, sub := range b.evtSubs {
		stats.Dropped[id] = sub.dropped
	}
	return stats
}

// Close shuts the bus down: every subscription is released by closing its
// channel, and later Subscribe and Publish calls fail or no-op. Buffered
// events remain readable, so a receiver ranging over its channel drains what
// was already delivered and then observes the shutdown. Close is safe to call
// more than once.
func (b *Bus) Close() {
	b.evtMu.Lock()
	defer b.evtMu.Unlock()
	if b.evtClosed {
		return
	}
	b.evtClosed = true
	for id, sub := range b.evtSubs {
		close(sub.ch)
		delete(b.evtSubs, id)
	}
}
