package execution

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// retryTestBaseDelay recomputes the pre-jitter delay for an attempt, used as
// the jitter floor in bounds tests.
func retryTestBaseDelay(p Policy, attempt int) time.Duration {
	grow := p.Multiplier
	if grow < 1 {
		grow = 1
	}
	delay := float64(p.Initial)
	for i := 0; i < attempt; i++ {
		delay *= grow
		if p.Max > 0 && delay >= float64(p.Max) {
			delay = float64(p.Max)
			break
		}
	}
	return time.Duration(delay)
}

func TestRetryPolicyExactGrowthCurve(t *testing.T) {
	p := Policy{
		Initial:    100 * time.Millisecond,
		Max:        2 * time.Second,
		Multiplier: 2,
		RNG:        rand.New(rand.NewSource(1)),
	}
	want := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1600 * time.Millisecond,
		2 * time.Second, // capped
		2 * time.Second,
	}
	for attempt, w := range want {
		got, ok := p.Next(attempt, errors.New("boom"))
		if !ok {
			t.Fatalf("attempt %d: want retryable, got false", attempt)
		}
		if got != w {
			t.Fatalf("attempt %d: want delay %v, got %v", attempt, w, got)
		}
	}
}

func TestRetryPolicyFractionalMultiplierCurve(t *testing.T) {
	p := Policy{Initial: 40 * time.Millisecond, Multiplier: 1.5} // uncapped
	want := []time.Duration{
		40 * time.Millisecond,
		60 * time.Millisecond,
		90 * time.Millisecond,
		135 * time.Millisecond,
		202500 * time.Microsecond,
	}
	for attempt, w := range want {
		got, ok := p.Next(attempt, errors.New("boom"))
		if !ok {
			t.Fatalf("attempt %d: want retryable, got false", attempt)
		}
		if got != w {
			t.Fatalf("attempt %d: want delay %v, got %v", attempt, w, got)
		}
	}
}

func TestRetryPolicyJitterStaysInBounds(t *testing.T) {
	const max = time.Second
	for seed := int64(0); seed < 100; seed++ {
		p := Policy{
			Initial:    50 * time.Millisecond,
			Max:        max,
			Multiplier: 2,
			Jitter:     0.75,
			RNG:        rand.New(rand.NewSource(seed)),
		}
		for attempt := 0; attempt <= 12; attempt++ {
			got, ok := p.Next(attempt, errors.New("boom"))
			if !ok {
				t.Fatalf("seed %d attempt %d: want retryable, got false", seed, attempt)
			}
			floor := time.Duration(float64(retryTestBaseDelay(p, attempt)) * 0.25)
			if got < floor {
				t.Fatalf("seed %d attempt %d: delay %v below jitter floor %v", seed, attempt, got, floor)
			}
			if got < 0 {
				t.Fatalf("seed %d attempt %d: negative delay %v", seed, attempt, got)
			}
			if got > max {
				t.Fatalf("seed %d attempt %d: delay %v above Max %v", seed, attempt, got, max)
			}
		}
	}
}

func TestRetryPolicySeededRNGIsDeterministic(t *testing.T) {
	mk := func(seed int64) Policy {
		return Policy{
			Initial:    100 * time.Millisecond,
			Max:        10 * time.Second,
			Multiplier: 2,
			Jitter:     0.5,
			RNG:        rand.New(rand.NewSource(seed)),
		}
	}
	first, second := mk(7), mk(7)
	other := mk(8)
	for attempt := 0; attempt < 10; attempt++ {
		a, okA := first.Next(attempt, errors.New("boom"))
		b, okB := second.Next(attempt, errors.New("boom"))
		c, _ := other.Next(attempt, errors.New("boom"))
		if !okA || !okB {
			t.Fatalf("attempt %d: want retryable from both same-seed policies", attempt)
		}
		if a != b {
			t.Fatalf("attempt %d: same seed gave %v and %v", attempt, a, b)
		}
		if a == c {
			t.Fatalf("attempt %d: different seeds gave identical delay %v", attempt, a)
		}
	}
}

func TestRetryPolicyMaxAttemptsCutoff(t *testing.T) {
	p := Policy{
		Initial:     time.Second,
		MaxAttempts: 3,
	}
	for attempt := 0; attempt < 3; attempt++ {
		if _, ok := p.Next(attempt, errors.New("boom")); !ok {
			t.Fatalf("attempt %d: want retryable within budget", attempt)
		}
	}
	for attempt := 3; attempt < 6; attempt++ {
		if _, ok := p.Next(attempt, errors.New("boom")); ok {
			t.Fatalf("attempt %d: want cutoff after budget of 3", attempt)
		}
	}

	// MaxAttempts 0 means unlimited.
	unlimited := Policy{Initial: time.Second}
	if _, ok := unlimited.Next(500, errors.New("boom")); !ok {
		t.Fatal("attempt 500: want retryable with MaxAttempts 0")
	}
}

func TestRetryPolicyRetryIfHonored(t *testing.T) {
	errBoom := errors.New("boom")
	errPermanent := errors.New("nope")

	never := Policy{Initial: time.Second, RetryIf: func(error) bool { return false }}
	if d, ok := never.Next(0, errBoom); ok || d != 0 {
		t.Fatalf("RetryIf=false: got (%v, %v), want (0, false)", d, ok)
	}

	selector := Policy{Initial: time.Second, RetryIf: func(err error) bool {
		return err == errBoom
	}}
	if _, ok := selector.Next(0, errPermanent); ok {
		t.Fatal("RetryIf rejected errPermanent but Next returned true")
	}
	if _, ok := selector.Next(0, errBoom); !ok {
		t.Fatal("RetryIf accepted errBoom but Next returned false")
	}

	// Nil RetryIf means every error is retryable, permanent markers included.
	always := Policy{Initial: time.Second}
	if _, ok := always.Next(0, NewPermanentError(errPermanent)); !ok {
		t.Fatal("nil RetryIf must retry every error")
	}
}

func TestRetryPolicyErrorClassification(t *testing.T) {
	sentinel := errors.New("disk full")
	p := DefaultPolicy()

	if _, ok := p.Next(0, errors.New("transient")); !ok {
		t.Fatal("plain error must be retryable under DefaultPolicy")
	}
	if _, ok := p.Next(0, NewRetryableError(sentinel)); !ok {
		t.Fatal("RetryableError must be retryable under DefaultPolicy")
	}
	if _, ok := p.Next(0, NewPermanentError(sentinel)); ok {
		t.Fatal("PermanentError must not be retryable under DefaultPolicy")
	}
	deepPerm := fmt.Errorf("step: %w", fmt.Errorf("inner: %w", NewPermanentError(sentinel)))
	if _, ok := p.Next(0, deepPerm); ok {
		t.Fatal("PermanentError buried under wrapping must not be retryable")
	}

	// errors.As finds the marker through arbitrary wrapping.
	var re *RetryableError
	if !errors.As(NewRetryableError(sentinel), &re) {
		t.Fatal("errors.As must find *RetryableError directly")
	}
	if re.Err != sentinel {
		t.Fatalf("RetryableError payload = %v, want %v", re.Err, sentinel)
	}
	deepRe := fmt.Errorf("outer: %w", NewRetryableError(sentinel))
	if !errors.As(deepRe, &re) {
		t.Fatal("errors.As must find *RetryableError through wrapping")
	}
	var pe *PermanentError
	if errors.As(deepRe, &pe) {
		t.Fatal("retryable error must not match *PermanentError")
	}

	// errors.Is matches the markers by type, both ways, and still unwraps.
	if !errors.Is(deepRe, &RetryableError{}) {
		t.Fatal("errors.Is must match *RetryableError through wrapping")
	}
	deepPermOuter := fmt.Errorf("outer: %w", NewPermanentError(sentinel))
	if !errors.Is(deepPermOuter, &PermanentError{}) {
		t.Fatal("errors.Is must match *PermanentError through wrapping")
	}
	if errors.Is(deepPermOuter, &RetryableError{}) {
		t.Fatal("permanent error must not match *RetryableError marker")
	}
	if errors.Is(deepRe, &PermanentError{}) {
		t.Fatal("retryable error must not match *PermanentError marker")
	}
	if !errors.Is(NewRetryableError(sentinel), sentinel) {
		t.Fatal("RetryableError must unwrap to its payload for errors.Is")
	}
	if !errors.Is(NewPermanentError(sentinel), sentinel) {
		t.Fatal("PermanentError must unwrap to its payload for errors.Is")
	}
	if errors.Is(errors.New("plain"), &RetryableError{}) {
		t.Fatal("plain error must not match *RetryableError marker")
	}

	// Nil-safe constructors.
	if NewRetryableError(nil) != nil || NewPermanentError(nil) != nil {
		t.Fatal("wrapping nil must stay nil")
	}
}

func TestRetryPolicyZeroValueSafe(t *testing.T) {
	var p Policy
	for attempt := 0; attempt < 5; attempt++ {
		got, ok := p.Next(attempt, errors.New("boom"))
		if !ok {
			t.Fatalf("attempt %d: zero-value policy must retry", attempt)
		}
		if got != 0 {
			t.Fatalf("attempt %d: zero-value delay = %v, want 0", attempt, got)
		}
		if got < 0 {
			t.Fatalf("attempt %d: negative delay %v", attempt, got)
		}
	}

	// Hostile field values must never produce negative or uncapped durations.
	hostile := Policy{
		Initial:    -5 * time.Second,
		Max:        -time.Second,
		Multiplier: -3,
		Jitter:     5,
		RNG:        rand.New(rand.NewSource(42)),
	}
	for attempt := 0; attempt <= 6; attempt++ {
		got, ok := hostile.Next(attempt, errors.New("boom"))
		if !ok {
			t.Fatalf("attempt %d: hostile policy must retry", attempt)
		}
		if got < 0 {
			t.Fatalf("attempt %d: hostile policy produced negative delay %v", attempt, got)
		}
	}
}

func TestRetryPolicyDefaultPolicySanity(t *testing.T) {
	p := DefaultPolicy()
	if p.Initial <= 0 || p.Max <= p.Initial || p.Multiplier <= 1 {
		t.Fatalf("DefaultPolicy fields not sane: %+v", p)
	}
	if p.MaxAttempts <= 0 {
		t.Fatalf("DefaultPolicy MaxAttempts = %d, want > 0", p.MaxAttempts)
	}
	if p.RetryIf == nil {
		t.Fatal("DefaultPolicy must classify errors")
	}
	got, ok := p.Next(0, errors.New("boom"))
	if !ok {
		t.Fatal("DefaultPolicy attempt 0 must be retryable")
	}
	floor0 := time.Duration(float64(p.Initial) * (1 - p.Jitter))
	ceil0 := time.Duration(float64(p.Initial) * (1 + p.Jitter))
	if got < floor0 || got > ceil0 {
		t.Fatalf("DefaultPolicy attempt 0 = %v, want within [%v, %v]", got, floor0, ceil0)
	}
	// Jitter must actually apply around the grown curve, not shift it off.
	p.RNG = rand.New(rand.NewSource(3))
	for attempt := 0; attempt < p.MaxAttempts; attempt++ {
		d, ok := p.Next(attempt, errors.New("boom"))
		if !ok {
			t.Fatalf("attempt %d: DefaultPolicy budget exhausted early", attempt)
		}
		base := retryTestBaseDelay(p, attempt)
		floor := time.Duration(float64(base) * (1 - p.Jitter))
		if d < floor || d > p.Max {
			t.Fatalf("attempt %d: delay %v outside [%v, %v]", attempt, d, floor, p.Max)
		}
	}
}
