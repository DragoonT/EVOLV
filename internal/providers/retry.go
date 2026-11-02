package providers

import (
	"context"
	"errors"
	"math/rand"
	"time"
)

// RetryPolicy bounds automatic retries for provider failures.
type RetryPolicy struct {
	MaxAttempts int           // total attempts including the first
	BaseDelay   time.Duration // first backoff step
	MaxDelay    time.Duration // cap for a single backoff step
	Jitter      float64       // 0..1 fraction of random spread
}

// DefaultRetryPolicy is conservative: transient failures recover quickly,
// auth/quota failures fail immediately.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, BaseDelay: 800 * time.Millisecond, MaxDelay: 8 * time.Second, Jitter: 0.3}
}

// Delay computes the wait before attempt n (1-based) for err.
func (p RetryPolicy) Delay(attempt int, err error) time.Duration {
	var pe *ProviderError
	if errors.As(err, &pe) && pe.RetryIn > 0 {
		return pe.RetryIn
	}
	if attempt < 1 {
		attempt = 1
	}
	d := p.BaseDelay
	for i := 1; i < attempt; i++ {
		d *= 2
		if p.MaxDelay > 0 && d >= p.MaxDelay {
			d = p.MaxDelay
			break
		}
	}
	if p.Jitter > 0 {
		spread := float64(d) * p.Jitter
		d += time.Duration((rand.Float64()*2 - 1) * spread)
	}
	if d < 0 {
		d = 0
	}
	return d
}

// StreamWithRetry calls provider and retries failures the classification says
// are transient. It never retries after onDelta has received content, because
// replaying a partially streamed reply would duplicate text.
func StreamWithRetry(ctx context.Context, p Provider, model string, msgs []Message, onDelta func(Delta), policy RetryPolicy) (string, error) {
	if policy.MaxAttempts <= 0 {
		policy = DefaultRetryPolicy()
	}
	var lastErr error
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		emitted := false
		wrapped := func(d Delta) {
			if d.Content != "" || d.Reasoning != "" {
				emitted = true
			}
			if onDelta != nil {
				onDelta(d)
			}
		}
		text, err := p.Stream(ctx, model, msgs, wrapped)
		if err == nil {
			return text, nil
		}
		lastErr = err
		if ctx.Err() != nil || emitted || !RetryableError(err) || attempt == policy.MaxAttempts {
			return text, err
		}
		delay := policy.Delay(attempt, err)
		select {
		case <-ctx.Done():
			return text, lastErr
		case <-time.After(delay):
		}
	}
	return "", lastErr
}
