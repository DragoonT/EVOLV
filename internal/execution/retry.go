package execution

import (
	"errors"
	"math/rand"
	"time"
)

// Retry policy: one reusable place that decides whether a failed operation
// should be attempted again, and how long to wait before the next attempt.
// Attempts are 0-based — the first retry is attempt 0 — so a policy with
// MaxAttempts N allows attempt numbers 0 through N-1.
//
// The wait before attempt N is Initial * Multiplier^N, capped at Max, then
// scaled by a random jitter factor in [1-Jitter, 1+Jitter). The result is
// clamped so it never falls below the jitter floor (delay * (1-Jitter)),
// never exceeds Max, and is never negative.

// Policy describes when failures should be retried and how long to wait
// between attempts. The zero value retries every error immediately, forever,
// without dividing by zero or producing negative durations.
type Policy struct {
	// Initial is the wait before the first retry (attempt 0). Negative
	// values are treated as zero.
	Initial time.Duration
	// Max caps every wait. Zero or negative means uncapped.
	Max time.Duration
	// Multiplier grows the delay exponentially. Values below 1 are treated
	// as 1 (constant delay) so the curve can never shrink or flip negative.
	Multiplier float64
	// Jitter randomizes each wait by a fraction of itself: the wait lands in
	// [delay*(1-Jitter), delay*(1+Jitter)). Values are clamped to [0, 1];
	// 0 disables jitter.
	Jitter float64
	// MaxAttempts is the number of attempts allowed. Next returns false once
	// attempt >= MaxAttempts. Zero means unlimited.
	MaxAttempts int
	// RetryIf decides whether an error is worth retrying. Nil means every
	// error is retryable; use DefaultPolicy for built-in classification of
	// PermanentError.
	RetryIf func(error) bool
	// RNG, when set, makes jitter reproducible; otherwise the package-global
	// math/rand source is used.
	RNG *rand.Rand
}

// DefaultPolicy returns a policy with conservative, generally useful
// defaults: start at 500ms, double every attempt up to 30s, add ±20% jitter,
// allow five attempts, and retry everything except permanent errors.
func DefaultPolicy() Policy {
	return Policy{
		Initial:     500 * time.Millisecond,
		Max:         30 * time.Second,
		Multiplier:  2,
		Jitter:      0.2,
		MaxAttempts: 5,
		RetryIf:     retryClassify,
	}
}

// Next reports whether the operation should be attempted again and how long
// to wait first. It returns false when the attempt budget is exhausted or
// RetryIf rejects the error.
func (p Policy) Next(attempt int, err error) (time.Duration, bool) {
	if attempt < 0 {
		attempt = 0
	}
	if p.MaxAttempts > 0 && attempt >= p.MaxAttempts {
		return 0, false
	}
	if p.RetryIf != nil && !p.RetryIf(err) {
		return 0, false
	}
	grow := p.Multiplier
	if grow < 1 {
		grow = 1
	}
	delay := float64(retryNonNegative(p.Initial))
	for i := 0; i < attempt; i++ {
		delay *= grow
		if p.Max > 0 && delay >= float64(p.Max) {
			delay = float64(p.Max)
			break
		}
	}
	jitter := p.Jitter
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}
	if jitter > 0 {
		floor := delay * (1 - jitter)
		delay *= 1 + jitter*(2*retryRandom(p)-1)
		// Belt and braces: the factor above already keeps delay inside
		// [delay*(1-jitter), delay*(1+jitter)), but rounding must never
		// push the wait below the jitter floor.
		if delay < floor {
			delay = floor
		}
	}
	if delay < 0 {
		delay = 0
	}
	if p.Max > 0 && delay > float64(p.Max) {
		delay = float64(p.Max)
	}
	return time.Duration(delay), true
}

// retryRandom returns the next jitter factor in [0, 1), deterministic when
// the policy carries an RNG.
func retryRandom(p Policy) float64 {
	if p.RNG != nil {
		return p.RNG.Float64()
	}
	return rand.Float64()
}

// retryNonNegative clamps a duration at zero.
func retryNonNegative(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

// retryClassify retries every error except ones marked permanent. It is the
// RetryIf used by DefaultPolicy.
func retryClassify(err error) bool {
	var perm *PermanentError
	return !errors.As(err, &perm)
}

// RetryableError marks an error that is worth retrying. Wrap one with
// NewRetryableError or construct it directly; policies and callers can then
// detect the marker through wrapping via errors.Is and errors.As.
type RetryableError struct {
	Err error
}

// Error implements the error interface.
func (e *RetryableError) Error() string {
	if e == nil || e.Err == nil {
		return "retryable error"
	}
	return "retryable: " + e.Err.Error()
}

// Unwrap exposes the wrapped error to errors.Is and errors.As.
func (e *RetryableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is matches any other *RetryableError by type so errors.Is finds the marker
// even through layers of wrapping with different payloads.
func (e *RetryableError) Is(target error) bool {
	_, ok := target.(*RetryableError)
	return ok
}

// PermanentError marks an error that must not be retried, such as a bad
// request or an exhausted budget. It mirrors RetryableError.
type PermanentError struct {
	Err error
}

// Error implements the error interface.
func (e *PermanentError) Error() string {
	if e == nil || e.Err == nil {
		return "permanent error"
	}
	return "permanent: " + e.Err.Error()
}

// Unwrap exposes the wrapped error to errors.Is and errors.As.
func (e *PermanentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is matches any other *PermanentError by type so errors.Is finds the marker
// even through layers of wrapping with different payloads.
func (e *PermanentError) Is(target error) bool {
	_, ok := target.(*PermanentError)
	return ok
}

// NewRetryableError wraps err as retryable. A nil err stays nil so wrapping
// never manufactures an error out of success.
func NewRetryableError(err error) error {
	if err == nil {
		return nil
	}
	return &RetryableError{Err: err}
}

// NewPermanentError wraps err as permanent. A nil err stays nil so wrapping
// never manufactures an error out of success.
func NewPermanentError(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}
