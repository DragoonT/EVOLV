package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseServer streams raw bytes in chunks and flushes between them, exactly like
// a real gateway that splits frames at arbitrary boundaries.
func sseServer(t *testing.T, chunks []string, status int, headers map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		if status != 0 {
			w.WriteHeader(status)
		}
		fl, _ := w.(http.Flusher)
		for _, c := range chunks {
			io.WriteString(w, c)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
}

func streamText(t *testing.T, url string) (string, error) {
	t.Helper()
	p := &openaiCompat{name: "test", baseURL: url, client: &http.Client{Timeout: 5 * time.Second}}
	var out strings.Builder
	text, err := p.Stream(context.Background(), "m", []Message{{Role: "user", Content: "hi"}}, func(d Delta) {
		out.WriteString(d.Content)
	})
	if err == nil && text != out.String() {
		t.Fatalf("returned %q != streamed %q", text, out.String())
	}
	return text, err
}

func TestStreamSplitAcrossChunks(t *testing.T) {
	srv := sseServer(t, []string{
		"data: {\"choices\":[{\"delta\":{\"cont",
		"ent\":\"Hel\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n",
		"\ndata: [DONE]\n\n",
	}, 0, map[string]string{"Content-Type": "text/event-stream"})
	defer srv.Close()
	text, err := streamText(t, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if text != "Hello" {
		t.Fatalf("text = %q", text)
	}
}

func TestStreamCRLFAndComments(t *testing.T) {
	srv := sseServer(t, []string{
		": keep-alive\r\n\r\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\r\n\r\n",
		"event: message\r\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\r\n\r\n",
	}, 0, map[string]string{"Content-Type": "text/event-stream"})
	defer srv.Close()
	text, err := streamText(t, srv.URL)
	if err != nil || text != "ab" {
		t.Fatalf("text=%q err=%v", text, err)
	}
}

func TestStreamMalformedEventSkipped(t *testing.T) {
	srv := sseServer(t, []string{
		"data: not json at all\n\n",
		"data: {broken\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n",
	}, 0, map[string]string{"Content-Type": "text/event-stream"})
	defer srv.Close()
	text, err := streamText(t, srv.URL)
	if err != nil || text != "ok" {
		t.Fatalf("text=%q err=%v", text, err)
	}
}

func TestStreamEndsWithoutDone(t *testing.T) {
	srv := sseServer(t, []string{"data: {\"choices\":[{\"delta\":{\"content\":\"tail\"}}]}\n"}, 0, map[string]string{"Content-Type": "text/event-stream"})
	defer srv.Close()
	text, err := streamText(t, srv.URL)
	if err != nil || text != "tail" {
		t.Fatalf("text=%q err=%v", text, err)
	}
}

func TestStreamNoDuplicates(t *testing.T) {
	srv := sseServer(t, []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n",
	}, 0, map[string]string{"Content-Type": "text/event-stream"})
	defer srv.Close()
	text, err := streamText(t, srv.URL)
	if err != nil || text != "xx" {
		t.Fatalf("text=%q err=%v", text, err)
	}
}

func TestStreamErrorStatusClassified(t *testing.T) {
	srv := sseServer(t, []string{`{"error":{"message":"rate limited"}}`}, http.StatusTooManyRequests, map[string]string{"Retry-After": "3"})
	defer srv.Close()
	_, err := streamText(t, srv.URL)
	if err == nil {
		t.Fatal("expected error")
	}
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Kind != ErrRateLimit {
		t.Fatalf("err = %v", err)
	}
	if pe.RetryIn != 3*time.Second {
		t.Fatalf("RetryIn = %v", pe.RetryIn)
	}
	if !RetryableError(err) {
		t.Fatal("rate limit should be retryable")
	}
}

func TestStreamServerErrorEvent(t *testing.T) {
	srv := sseServer(t, []string{"data: {\"error\":{\"message\":\"upstream exploded\"}}\n\n"}, 0, map[string]string{"Content-Type": "text/event-stream"})
	defer srv.Close()
	_, err := streamText(t, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "upstream exploded") {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamCancellation(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-block
	}))
	defer srv.Close()
	defer close(block)
	ctx, cancel := context.WithCancel(context.Background())
	p := &openaiCompat{name: "test", baseURL: srv.URL, client: &http.Client{}}
	go func() { time.Sleep(80 * time.Millisecond); cancel() }()
	_, err := p.Stream(ctx, "m", []Message{{Role: "user", Content: "hi"}}, func(Delta) {})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Kind != ErrCanceled {
		t.Fatalf("err = %v (kind classification)", err)
	}
	if RetryableError(err) {
		t.Fatal("cancellation must not be retried")
	}
}

func TestRetryAfterParsing(t *testing.T) {
	if d := retryAfter("5"); d != 5*time.Second {
		t.Fatalf("seconds = %v", d)
	}
	if d := retryAfter(""); d != 0 {
		t.Fatalf("empty = %v", d)
	}
	if d := retryAfter("garbage"); d != 0 {
		t.Fatalf("garbage = %v", d)
	}
}

type flakyProvider struct {
	failures int
	calls    int
}

func (f *flakyProvider) Name() string                             { return "flaky" }
func (f *flakyProvider) Models(context.Context) ([]string, error) { return []string{"m"}, nil }
func (f *flakyProvider) Stream(ctx context.Context, _ string, _ []Message, onDelta func(Delta)) (string, error) {
	f.calls++
	if f.calls <= f.failures {
		return "", Classify("flaky", 503, errors.New("temporarily down"))
	}
	if onDelta != nil {
		onDelta(Delta{Content: "ok"})
	}
	return "ok", nil
}

func TestStreamWithRetryRecoversTransient(t *testing.T) {
	p := &flakyProvider{failures: 2}
	policy := RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	text, err := StreamWithRetry(context.Background(), p, "m", nil, nil, policy)
	if err != nil || text != "ok" || p.calls != 3 {
		t.Fatalf("text=%q err=%v calls=%d", text, err, p.calls)
	}
}

func TestStreamWithRetryStopsAfterBudget(t *testing.T) {
	p := &flakyProvider{failures: 10}
	policy := RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
	if _, err := StreamWithRetry(context.Background(), p, "m", nil, nil, policy); err == nil {
		t.Fatal("expected error")
	}
	if p.calls != 2 {
		t.Fatalf("calls = %d, want 2", p.calls)
	}
}

func TestStreamWithRetryNeverReplaysPartialOutput(t *testing.T) {
	p := &partialProvider{}
	policy := RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond}
	_, err := StreamWithRetry(context.Background(), p, "m", nil, func(Delta) {}, policy)
	if err == nil {
		t.Fatal("expected error")
	}
	if p.calls != 1 {
		t.Fatalf("retried after emitting content: %d calls", p.calls)
	}
}

type partialProvider struct{ calls int }

func (p *partialProvider) Name() string                             { return "partial" }
func (p *partialProvider) Models(context.Context) ([]string, error) { return []string{"m"}, nil }
func (p *partialProvider) Stream(ctx context.Context, _ string, _ []Message, onDelta func(Delta)) (string, error) {
	p.calls++
	if onDelta != nil {
		onDelta(Delta{Content: "partial"})
	}
	return "partial", Classify("partial", 503, errors.New("dropped"))
}

func TestRetryPolicyDelayGrowsAndCaps(t *testing.T) {
	p := RetryPolicy{BaseDelay: 100 * time.Millisecond, MaxDelay: 500 * time.Millisecond}
	d1 := p.Delay(1, nil)
	d3 := p.Delay(3, nil)
	d9 := p.Delay(9, nil)
	if d1 != 100*time.Millisecond || d3 != 400*time.Millisecond || d9 != 500*time.Millisecond {
		t.Fatalf("delays = %v %v %v", d1, d3, d9)
	}
	pe := &ProviderError{Kind: ErrRateLimit, RetryIn: 7 * time.Second}
	if got := p.Delay(1, pe); got != 7*time.Second {
		t.Fatalf("Retry-After ignored: %v", got)
	}
}
