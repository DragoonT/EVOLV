package swarm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var swarmRErrBoom = errors.New("boom")

func TestSwarmRBackoffDelay(t *testing.T) {
	tests := []struct {
		name    string
		bo      Backoff
		attempt int
		want    time.Duration
	}{
		{"zero value first retry", Backoff{}, 2, 50 * time.Millisecond},
		{"zero value doubles", Backoff{}, 3, 100 * time.Millisecond},
		{"zero value caps at 1s", Backoff{}, 12, time.Second},
		{"unset max caps initial", Backoff{Initial: 2 * time.Second}, 2, time.Second},
		{"explicit max caps growth", Backoff{Initial: 40 * time.Millisecond, Max: 100 * time.Millisecond, Multiplier: 3}, 3, 100 * time.Millisecond},
		{"explicit max not reached", Backoff{Initial: 40 * time.Millisecond, Max: 100 * time.Millisecond, Multiplier: 3}, 2, 40 * time.Millisecond},
		{"constant multiplier holds", Backoff{Initial: 10 * time.Millisecond, Multiplier: 1}, 5, 10 * time.Millisecond},
		{"zero multiplier defaults to 2", Backoff{Initial: 10 * time.Millisecond}, 4, 40 * time.Millisecond},
		{"negative initial defaults to 50ms", Backoff{Initial: -1}, 2, 50 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := swarmRbackoffDelay(tt.bo, tt.attempt); got != tt.want {
				t.Fatalf("swarmRbackoffDelay(%+v, %d) = %v, want %v", tt.bo, tt.attempt, got, tt.want)
			}
		})
	}
}

func TestRunAllPanicIsolation(t *testing.T) {
	jobs := []Job{{ID: "a"}, {ID: "boom", Attempts: 2}, {ID: "c"}, {ID: "d"}}
	h := func(ctx context.Context, job Job, attempt int) (any, error) {
		if job.ID == "boom" {
			panic("kaboom in " + job.ID)
		}
		return "ok-" + job.ID, nil
	}
	sum := RunAll(context.Background(), jobs, h, 4, Backoff{Initial: time.Millisecond, Max: 2 * time.Millisecond})
	if sum.Succeeded != 3 || sum.Failed != 1 {
		t.Fatalf("Succeeded=%d Failed=%d, want 3/1", sum.Succeeded, sum.Failed)
	}
	for _, id := range []string{"a", "c", "d"} {
		if sum.Results[id] != "ok-"+id {
			t.Errorf("Results[%q] = %v, want ok-%s (panic must not poison neighbours)", id, sum.Results[id], id)
		}
	}
	if err := sum.Errors["boom"]; err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Errorf("Errors[boom] = %v, want a panic-derived error", err)
	}
	if _, ok := sum.Results["boom"]; ok {
		t.Errorf("panicking job recorded a result: %v", sum.Results["boom"])
	}
}

func TestRunAllTimeoutFast(t *testing.T) {
	h := func(ctx context.Context, job Job, attempt int) (any, error) {
		<-ctx.Done() // block forever unless the per-attempt timeout fires
		return nil, ctx.Err()
	}
	sum := RunAll(context.Background(), []Job{{ID: "slow", Timeout: 20 * time.Millisecond}}, h, 1, Backoff{})
	if sum.Wall > 50*time.Millisecond {
		t.Fatalf("timeout path took %v, want <= 50ms", sum.Wall)
	}
	if !errors.Is(sum.Errors["slow"], context.DeadlineExceeded) {
		t.Fatalf("Errors[slow] = %v, want context.DeadlineExceeded", sum.Errors["slow"])
	}
	if _, ok := sum.Results["slow"]; ok {
		t.Errorf("timed-out job recorded a result: %v", sum.Results["slow"])
	}
}

func TestRunAllRetryCountsMatchAttempts(t *testing.T) {
	tests := []struct {
		name      string
		attempts  int
		failFor   int // the first N handler calls fail
		wantOK    bool
		wantCalls int
		wantRetry int
	}{
		{"first try succeeds", 1, 0, true, 1, 0},
		{"zero attempts means one", 0, 0, true, 1, 0},
		{"two failures then success", 3, 2, true, 3, 2},
		{"exhausts budget", 2, 99, false, 2, 1},
		{"never exceeds budget", 4, 99, false, 4, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int64
			var attemptsSeen []int
			bo := Backoff{Initial: time.Millisecond, Max: 2 * time.Millisecond}
			h := func(ctx context.Context, job Job, attempt int) (any, error) {
				calls.Add(1)
				attemptsSeen = append(attemptsSeen, attempt)
				if int(calls.Load()) <= tt.failFor {
					return nil, swarmRErrBoom
				}
				return "done", nil
			}
			sum := RunAll(context.Background(), []Job{{ID: "j", Attempts: tt.attempts}}, h, 1, bo)
			if calls.Load() != int64(tt.wantCalls) {
				t.Fatalf("handler called %d times, want %d", calls.Load(), tt.wantCalls)
			}
			for i, a := range attemptsSeen {
				if a != i+1 {
					t.Fatalf("attempt numbers = %v, want 1..%d", attemptsSeen, tt.wantCalls)
				}
			}
			if tt.wantOK {
				if sum.Results["j"] != "done" {
					t.Fatalf("Results[j] = %v, want done", sum.Results["j"])
				}
				if len(sum.Errors) != 0 {
					t.Fatalf("unexpected errors: %v", sum.Errors)
				}
			} else {
				if _, ok := sum.Results["j"]; ok {
					t.Fatalf("failed job recorded a result: %v", sum.Results["j"])
				}
				if !errors.Is(sum.Errors["j"], swarmRErrBoom) {
					t.Fatalf("Errors[j] = %v, want wrapped boom", sum.Errors["j"])
				}
			}
			if sum.Retried != tt.wantRetry {
				t.Fatalf("Retried = %d, want %d", sum.Retried, tt.wantRetry)
			}
		})
	}
}

func TestRunAllPartialResultsOnMixedOutcome(t *testing.T) {
	jobs := []Job{{ID: "good-1"}, {ID: "good-2"}, {ID: "bad"}, {ID: "good-3"}}
	h := func(ctx context.Context, job Job, attempt int) (any, error) {
		if job.ID == "bad" {
			return nil, swarmRErrBoom
		}
		return "ok-" + job.ID, nil
	}
	sum := RunAll(context.Background(), jobs, h, 4, Backoff{})
	if sum.Succeeded != 3 || sum.Failed != 1 || sum.Retried != 0 {
		t.Fatalf("Succeeded=%d Failed=%d Retried=%d, want 3/1/0", sum.Succeeded, sum.Failed, sum.Retried)
	}
	for _, id := range []string{"good-1", "good-2", "good-3"} {
		if sum.Results[id] != "ok-"+id {
			t.Errorf("partial result lost for %q: %v", id, sum.Results[id])
		}
	}
	if !errors.Is(sum.Errors["bad"], swarmRErrBoom) {
		t.Errorf("Errors[bad] = %v, want wrapped boom", sum.Errors["bad"])
	}
	if len(sum.Errors) != 1 {
		t.Errorf("len(Errors) = %d, want 1", len(sum.Errors))
	}
}

func TestRunAllParallelismCap(t *testing.T) {
	const total, limit = 12, 4
	var inFlight, peak atomic.Int64
	jobs := make([]Job, total)
	for i := range jobs {
		jobs[i] = Job{ID: fmt.Sprintf("job-%02d", i)}
	}
	h := func(ctx context.Context, job Job, attempt int) (any, error) {
		cur := inFlight.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		defer inFlight.Add(-1)
		time.Sleep(30 * time.Millisecond)
		return job.ID, nil
	}
	sum := RunAll(context.Background(), jobs, h, limit, Backoff{})
	if got := peak.Load(); got > limit {
		t.Fatalf("in-flight peaked at %d, cap is %d", got, limit)
	}
	if peak.Load() < 2 {
		t.Fatalf("in-flight peaked at %d; jobs never overlapped so the cap was not exercised", peak.Load())
	}
	if sum.Succeeded != total || sum.Failed != 0 {
		t.Fatalf("Succeeded=%d Failed=%d, want %d/0", sum.Succeeded, sum.Failed, total)
	}
}

func TestRunAllCancelStopsPendingJobs(t *testing.T) {
	const total = 5
	jobs := make([]Job, total)
	for i := range jobs {
		jobs[i] = Job{ID: fmt.Sprintf("job-%d", i)}
	}
	var (
		started  = make(chan struct{})
		gate     = make(chan struct{})
		startedN atomic.Int64
	)
	h := func(ctx context.Context, job Job, attempt int) (any, error) {
		if startedN.Add(1) == 1 {
			close(started)
		}
		<-gate
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return "ok-" + job.ID, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Summary, 1)
	go func() { done <- RunAll(ctx, jobs, h, 1, Backoff{}) }()
	<-started   // exactly one job holds the single slot
	cancel()    // queued jobs must stop before starting
	close(gate) // let the started job finish
	sum := <-done

	if len(sum.Results) != 0 {
		t.Fatalf("Results = %v, want empty after cancellation", sum.Results)
	}
	if len(sum.Errors) != total {
		t.Fatalf("len(Errors) = %d, want %d (every job reported)", len(sum.Errors), total)
	}
	cancelledBeforeStart := 0
	for id, err := range sum.Errors {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Errors[%q] = %v, want wrapped context.Canceled", id, err)
		}
		if strings.Contains(err.Error(), "cancelled before start") {
			cancelledBeforeStart++
		}
	}
	// Exactly one job started before cancel; cancel must stop scheduling, so
	// the other four report "cancelled before start" and none runs an attempt.
	if cancelledBeforeStart != total-1 {
		t.Errorf("%d jobs reported 'cancelled before start', want %d", cancelledBeforeStart, total-1)
	}
	if sum.Retried != 0 {
		t.Errorf("Retried = %d, want 0 (cancel stops new attempts)", sum.Retried)
	}
}

func TestRunAllCancelInterruptsBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(20*time.Millisecond, cancel)
	var calls atomic.Int64
	h := func(ctx context.Context, job Job, attempt int) (any, error) {
		calls.Add(1)
		return nil, swarmRErrBoom // always fail; retries must not outlive ctx
	}
	bo := Backoff{Initial: 100 * time.Millisecond, Max: time.Second, Multiplier: 2}
	sum := RunAll(ctx, []Job{{ID: "grinder", Attempts: 5}}, h, 1, bo)
	if sum.Wall >= 150*time.Millisecond {
		t.Fatalf("RunAll took %v; the backoff sleep was not interrupted by cancel", sum.Wall)
	}
	if !errors.Is(sum.Errors["grinder"], context.Canceled) {
		t.Fatalf("Errors[grinder] = %v, want wrapped context.Canceled", sum.Errors["grinder"])
	}
	if calls.Load() >= 5 {
		t.Errorf("handler ran %d times; cancel should have stopped further attempts", calls.Load())
	}
}

func TestRunAllNilHandlerAndEmptyBatch(t *testing.T) {
	sum := RunAll(context.Background(), []Job{{ID: "a"}, {ID: "b"}}, nil, 0, Backoff{})
	if sum.Succeeded != 0 || sum.Failed != 2 {
		t.Fatalf("nil handler: Succeeded=%d Failed=%d, want 0/2", sum.Succeeded, sum.Failed)
	}
	if len(sum.Errors) != 2 {
		t.Fatalf("nil handler: Errors = %v, want one per job", sum.Errors)
	}
	empty := RunAll(context.Background(), nil, func(ctx context.Context, job Job, attempt int) (any, error) {
		return nil, nil
	}, 0, Backoff{})
	if empty.Succeeded != 0 || empty.Failed != 0 || empty.Retried != 0 {
		t.Fatalf("empty batch: %+v, want zero counters", empty)
	}
	if empty.Results == nil || empty.Errors == nil {
		t.Fatal("empty batch: maps must be non-nil")
	}
}

func TestCircuitBreakerOpenHalfOpenClose(t *testing.T) {
	cb := &CircuitBreaker{MaxConsecutiveFailures: 3, Cooldown: 40 * time.Millisecond}
	if cb.State() != "closed" || !cb.Allow() {
		t.Fatalf("fresh breaker: State=%s Allow=%v, want closed/true", cb.State(), cb.Allow())
	}
	cb.Record(false)
	cb.Record(false)
	if cb.State() != "closed" || !cb.Allow() {
		t.Fatalf("below threshold: State=%s Allow=%v, want closed/true", cb.State(), cb.Allow())
	}
	cb.Record(false)
	if cb.State() != "open" || cb.Allow() {
		t.Fatalf("after 3 failures: State=%s Allow=%v, want open/false", cb.State(), cb.Allow())
	}
	time.Sleep(45 * time.Millisecond)
	if cb.State() != "half-open" || !cb.Allow() {
		t.Fatalf("after cooldown: State=%s Allow=%v, want half-open/true", cb.State(), cb.Allow())
	}
	cb.Record(false) // failed probe re-opens with a fresh cooldown
	if cb.State() != "open" || cb.Allow() {
		t.Fatalf("after failed probe: State=%s Allow=%v, want open/false", cb.State(), cb.Allow())
	}
	time.Sleep(45 * time.Millisecond)
	if cb.State() != "half-open" || !cb.Allow() {
		t.Fatalf("after second cooldown: State=%s Allow=%v, want half-open/true", cb.State(), cb.Allow())
	}
	cb.Record(true) // the next success closes
	if cb.State() != "closed" || !cb.Allow() {
		t.Fatalf("after probe success: State=%s Allow=%v, want closed/true", cb.State(), cb.Allow())
	}
	cb.Record(false) // count restarted: a single failure must not open
	if cb.State() != "closed" || !cb.Allow() {
		t.Fatalf("after reset: State=%s Allow=%v, want closed/true", cb.State(), cb.Allow())
	}
}

func TestCircuitBreakerNeverOpensBelowZeroThreshold(t *testing.T) {
	for _, threshold := range []int{0, -1} {
		cb := &CircuitBreaker{MaxConsecutiveFailures: threshold, Cooldown: time.Hour}
		for i := 0; i < 10; i++ {
			cb.Record(false)
		}
		if cb.State() != "closed" || !cb.Allow() {
			t.Errorf("threshold %d: State=%s Allow=%v, want closed/true (never opens)", threshold, cb.State(), cb.Allow())
		}
	}
}
