package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/dxn111/EVOLV/internal/redact"
)

// ErrorKind classifies a provider failure so the agent can decide whether a
// retry is worthwhile and the user gets a useful explanation.
type ErrorKind string

const (
	ErrAuth       ErrorKind = "auth"        // bad or missing credentials — never retried
	ErrRateLimit  ErrorKind = "rate_limit"  // 429 — retried after a delay
	ErrServer     ErrorKind = "server"      // 5xx — retried with backoff
	ErrNetwork    ErrorKind = "network"     // transport failure — retried
	ErrTimeout    ErrorKind = "timeout"     // deadline exceeded — retried
	ErrQuota      ErrorKind = "quota"       // billing/quota — not retried
	ErrBadRequest ErrorKind = "bad_request" // 4xx we caused — not retried
	ErrCanceled   ErrorKind = "canceled"    // user cancellation — not retried
	ErrUnknown    ErrorKind = "unknown"
)

// ProviderError is the structured failure surfaced to the agent loop.
type ProviderError struct {
	Provider string
	Status   int
	Kind     ErrorKind
	Message  string
	RetryIn  time.Duration // Retry-After hint, when the server sent one
	Err      error
}

func (e *ProviderError) Error() string {
	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	msg = redact.Redact(msg)
	if e.Status != 0 {
		return fmt.Sprintf("%s http %d (%s): %s", e.Provider, e.Status, e.Kind, msg)
	}
	if e.Kind != "" && e.Kind != ErrUnknown {
		return fmt.Sprintf("%s (%s): %s", e.Provider, e.Kind, msg)
	}
	return fmt.Sprintf("%s: %s", e.Provider, msg)
}

func (e *ProviderError) Unwrap() error { return e.Err }

// Retryable reports whether repeating the request could succeed.
func (e *ProviderError) Retryable() bool {
	switch e.Kind {
	case ErrRateLimit, ErrServer, ErrNetwork, ErrTimeout:
		return true
	}
	return false
}

// Classify converts any error into a classified ProviderError.
func Classify(provider string, status int, err error) *ProviderError {
	pe := &ProviderError{Provider: provider, Status: status, Err: err}
	if err != nil {
		pe.Message = err.Error()
	}
	switch {
	case errors.Is(err, context.Canceled):
		pe.Kind = ErrCanceled
	case errors.Is(err, context.DeadlineExceeded):
		pe.Kind = ErrTimeout
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		pe.Kind = ErrAuth
	case status == http.StatusTooManyRequests:
		pe.Kind = ErrRateLimit
	case status == http.StatusPaymentRequired:
		pe.Kind = ErrQuota
	case status >= 500:
		pe.Kind = ErrServer
	case status >= 400:
		pe.Kind = ErrBadRequest
	default:
		var netErr net.Error
		if errors.As(err, &netErr) {
			if netErr.Timeout() {
				pe.Kind = ErrTimeout
			} else {
				pe.Kind = ErrNetwork
			}
		} else if err != nil {
			pe.Kind = ErrNetwork
		} else {
			pe.Kind = ErrUnknown
		}
	}
	lower := strings.ToLower(pe.Message)
	if pe.Kind == ErrUnknown || pe.Kind == ErrBadRequest {
		switch {
		case strings.Contains(lower, "insufficient") && strings.Contains(lower, "quota"):
			pe.Kind = ErrQuota
		case strings.Contains(lower, "invalid api key") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "authentication"):
			pe.Kind = ErrAuth
		case strings.Contains(lower, "rate limit"):
			pe.Kind = ErrRateLimit
		}
	}
	return pe
}

// RetryableError reports whether err is worth one automatic retry.
func RetryableError(err error) bool {
	if err == nil {
		return false
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Retryable()
	}
	return false
}

// SafeErrorText returns an error message with credentials removed.
func SafeErrorText(err error) string {
	if err == nil {
		return ""
	}
	return redact.Redact(err.Error())
}

// ErrorHint returns a short actionable suggestion for a classified error.
func ErrorHint(err error) string {
	var pe *ProviderError
	if !errors.As(err, &pe) {
		return "Try another model in Settings — free anonymous models rotate often."
	}
	switch pe.Kind {
	case ErrAuth:
		return "The API key was rejected. Check Settings → Provider and paste a fresh key."
	case ErrQuota:
		return "This account is out of quota. Add credit or pick a free provider."
	case ErrRateLimit:
		return "The provider is rate limiting. Wait a moment or switch models."
	case ErrNetwork:
		return "The provider could not be reached. Check your connection or base URL."
	case ErrTimeout:
		return "The provider took too long to answer. Retry, or pick a faster model."
	case ErrServer:
		return "The provider had an internal error. Retry in a few seconds."
	case ErrCanceled:
		return "The request was cancelled."
	}
	return "Try another model in Settings — free anonymous models rotate often."
}
