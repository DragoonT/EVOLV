package swarm

// resilience.go holds standalone resilience primitives for the colony:
// bounded parallel execution with per-job timeout, retry with interruptible
// backoff, and a circuit breaker. Nothing here touches Engine state, so the
// engine can adopt these pieces later without changing their contracts.

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Job is one unit of work handed to RunAll. IDs should be unique across a
// batch: they key Summary.Results and Summary.Errors, and a duplicate ID
// overwrites the earlier outcome.
type Job struct {
	ID       string        // stable identifier
	Timeout  time.Duration // per-attempt budget; <= 0 inherits the parent context
	Attempts int           // total attempts including the first; <= 0 means 1
}

// Handler runs one attempt of a job. attempt is 1-based. A panic inside the
// handler is recovered and reported as that job's error.
type Handler func(ctx context.Context, job Job, attempt int) (any, error)

// Backoff spaces out retry attempts exponentially. The zero value is usable:
// 50ms before the first retry, doubling each retry, capped at Max or 1s.
type Backoff struct {
	Initial    time.Duration // delay before the first retry; <= 0 means 50ms
	Max        time.Duration // delay ceiling; <= 0 means 1s
	Multiplier float64       // growth per retry; <= 0 means 2
}

// Summary is the outcome of one RunAll call. Results keeps partial successes
// even when other jobs failed; Errors holds every job that did not succeed,
// including jobs cancelled before their first attempt. Retried counts retry
// attempts that actually ran, beyond each job's first attempt.
type Summary struct {
	Succeeded int
	Failed    int
	Retried   int
	Results   map[string]any
	Errors    map[string]error
	Wall      time.Duration
}

// RunAll executes jobs concurrently with at most parallelism workers
// (<= 0 defaults to 4). Each attempt runs under a context derived from ctx
// with the job's Timeout; a failed attempt is retried up to job.Attempts
// total attempts, sleeping an exponential backoff between tries that ctx
// cancellation cuts short. Cancelling ctx also stops scheduling: jobs that
// never started an attempt report a "cancelled before start" error, and jobs
// already running observe the cancellation through their attempt context.
func RunAll(ctx context.Context, jobs []Job, h Handler, parallelism int, bo Backoff) Summary {
	if parallelism <= 0 {
		parallelism = 4
	}
	sum := Summary{Results: map[string]any{}, Errors: map[string]error{}}
	start := time.Now()
	if h == nil {
		for _, job := range jobs {
			sum.Errors[job.ID] = fmt.Errorf("job %q has no handler", job.ID)
		}
		sum.Failed = len(sum.Errors)
		sum.Wall = time.Since(start)
		return sum
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, parallelism)
	for _, job := range jobs {
		wg.Add(1)
		go func(job Job) {
			defer wg.Done()
			// Cancellation stops scheduling. A job that never acquires a
			// slot never starts, and is reported instead of dropped.
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				swarmRmarkCancelled(&mu, &sum, job, ctx)
				return
			}
			// select does not prefer Done over a ready slot, so re-check
			// after acquiring: a job handed a slot after cancellation must
			// not start an attempt.
			if err := ctx.Err(); err != nil {
				<-sem
				swarmRmarkCancelled(&mu, &sum, job, ctx)
				return
			}
			defer func() { <-sem }()
			result, retried, err := swarmRrunJob(ctx, job, h, bo)
			mu.Lock()
			if err != nil {
				sum.Errors[job.ID] = err
			} else {
				sum.Results[job.ID] = result
			}
			sum.Retried += retried
			mu.Unlock()
		}(job)
	}
	wg.Wait()
	sum.Succeeded = len(sum.Results)
	sum.Failed = len(sum.Errors)
	sum.Wall = time.Since(start)
	return sum
}

// swarmRmarkCancelled records that a job never ran because ctx was already
// cancelled when it reached the front of the scheduling queue.
func swarmRmarkCancelled(mu *sync.Mutex, sum *Summary, job Job, ctx context.Context) {
	mu.Lock()
	defer mu.Unlock()
	sum.Errors[job.ID] = fmt.Errorf("job %q cancelled before start: %w", job.ID, ctx.Err())
}

// swarmRrunJob drives one job through its attempt loop: run, and on failure
// sleep the backoff and retry while attempts remain and ctx stays alive.
func swarmRrunJob(ctx context.Context, job Job, h Handler, bo Backoff) (result any, retried int, err error) {
	attempts := job.Attempts
	if attempts <= 0 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if !swarmRsleep(ctx, swarmRbackoffDelay(bo, attempt)) {
				return nil, retried, fmt.Errorf("job %q cancelled during backoff: %w", job.ID, ctx.Err())
			}
			retried++
		}
		var attemptErr error
		result, attemptErr = swarmRattempt(ctx, job, h, attempt)
		if attemptErr == nil {
			return result, retried, nil
		}
		err = attemptErr
	}
	return nil, retried, err
}

// swarmRattempt runs a single attempt under the job's timeout, turning a
// panicking handler into that attempt's error so one bad job cannot kill the
// worker goroutine.
func swarmRattempt(ctx context.Context, job Job, h Handler, attempt int) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			result, err = nil, fmt.Errorf("job %q attempt %d panicked: %v", job.ID, attempt, r)
		}
	}()
	runCtx := ctx
	if job.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, job.Timeout)
		defer cancel()
	}
	result, err = h(runCtx, job, attempt)
	if err != nil {
		return nil, fmt.Errorf("job %q attempt %d: %w", job.ID, attempt, err)
	}
	return result, nil
}

// swarmRsleep waits d, returning false as soon as ctx is cancelled so a
// pending retry never delays a shutdown.
func swarmRsleep(ctx context.Context, d time.Duration) bool {
	if d > 0 {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return false
		}
	}
	return ctx.Err() == nil
}

// swarmRbackoffDelay returns the sleep before the given upcoming attempt
// (attempt >= 2): attempt 2 sleeps Initial and each later attempt grows by
// Multiplier, capped at Max (or 1s when unset). The zero Backoff is usable.
func swarmRbackoffDelay(b Backoff, attempt int) time.Duration {
	initial := b.Initial
	if initial <= 0 {
		initial = 50 * time.Millisecond
	}
	limit := b.Max
	if limit <= 0 {
		limit = time.Second
	}
	mult := b.Multiplier
	if mult <= 0 {
		mult = 2
	}
	d := initial
	for range attempt - 2 {
		d = time.Duration(float64(d) * mult)
		if d > limit {
			return limit
		}
	}
	if d > limit {
		return limit
	}
	if d <= 0 {
		return time.Millisecond
	}
	return d
}

// CircuitBreaker refuses calls to a failing dependency: after
// MaxConsecutiveFailures consecutive failures it opens and Allow returns
// false; after Cooldown it half-opens to let a probe through; the next
// success closes it and a failed probe re-opens it with a fresh cooldown.
// MaxConsecutiveFailures <= 0 means the breaker never opens.
type CircuitBreaker struct {
	MaxConsecutiveFailures int
	Cooldown               time.Duration

	mu               sync.Mutex
	consecutiveFails int
	openedAt         time.Time
}

// Circuit breaker states reported by CircuitBreaker.State.
const (
	swarmRstateClosed   = "closed"
	swarmRstateOpen     = "open"
	swarmRstateHalfOpen = "half-open"
)

// State reports "closed", "open" or "half-open".
func (cb *CircuitBreaker) State() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.swarmRstateLocked()
}

// Allow reports whether a call may proceed right now. In the half-open state
// it returns true so a probe can run.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.swarmRstateLocked() != swarmRstateOpen
}

// Record feeds one observed call outcome into the breaker. A success resets
// the consecutive-failure count and closes the breaker; a failure counts
// toward the open threshold, and a failure in the half-open state re-opens
// the breaker with a fresh cooldown.
func (cb *CircuitBreaker) Record(success bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if success {
		cb.consecutiveFails = 0
		cb.openedAt = time.Time{}
		return
	}
	if cb.MaxConsecutiveFailures <= 0 {
		return // configured never to open; nothing to track
	}
	if cb.swarmRstateLocked() == swarmRstateHalfOpen {
		cb.openedAt = time.Now() // failed probe: re-open, restart the cooldown
		return
	}
	cb.consecutiveFails++
	if cb.consecutiveFails == cb.MaxConsecutiveFailures {
		cb.openedAt = time.Now()
	}
}

// swarmRstateLocked derives the breaker state; cb.mu must be held.
func (cb *CircuitBreaker) swarmRstateLocked() string {
	if cb.MaxConsecutiveFailures <= 0 || cb.consecutiveFails < cb.MaxConsecutiveFailures {
		return swarmRstateClosed
	}
	if time.Since(cb.openedAt) >= cb.Cooldown {
		return swarmRstateHalfOpen
	}
	return swarmRstateOpen
}
