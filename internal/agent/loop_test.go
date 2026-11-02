package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dxn111/EVOLV/internal/bus"
	"github.com/dxn111/EVOLV/internal/providers"
	"github.com/dxn111/EVOLV/internal/workspace"
)

// scriptedProvider replays canned responses and records the prompts it saw.
type scriptedProvider struct {
	mu      sync.Mutex
	replies []string
	errs    []error
	calls   int
	prompts [][]providers.Message
}

func (p *scriptedProvider) Name() string { return "scripted" }
func (p *scriptedProvider) Models(context.Context) ([]string, error) {
	return []string{"scripted-1"}, nil
}
func (p *scriptedProvider) Stream(_ context.Context, _ string, msgs []providers.Message, onDelta func(providers.Delta)) (string, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.prompts = append(p.prompts, append([]providers.Message{}, msgs...))
	p.mu.Unlock()
	if idx < len(p.errs) && p.errs[idx] != nil {
		return "", p.errs[idx]
	}
	if idx >= len(p.replies) {
		if len(p.replies) > 0 {
			return p.replies[len(p.replies)-1], nil
		}
		return "done", nil
	}
	reply := p.replies[idx]
	if onDelta != nil && reply != "" {
		onDelta(providers.Delta{Content: reply})
	}
	return reply, nil
}

func newLoopTestAgent(t *testing.T, p providers.Provider) *Agent {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ws, err := workspace.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(ws, bus.New(), p, "scripted-1")
	a.maxLoops = 12
	return a
}

func TestLoopStopsOnRepeatedBatch(t *testing.T) {
	repeat := "```evolv\n{\"action\":\"list_dir\",\"path\":\".\"}\n```"
	p := &scriptedProvider{}
	for i := 0; i < 20; i++ {
		p.replies = append(p.replies, repeat)
	}
	a := newLoopTestAgent(t, p)
	out := a.Chat(context.Background(), "keep listing")
	if !strings.Contains(out, "repeated action batch") {
		t.Fatalf("loop not stopped: %q", out)
	}
	if p.calls > 6 {
		t.Fatalf("executed %d turns before stopping", p.calls)
	}
}

func TestLoopStopsAtTurnLimit(t *testing.T) {
	var replies []string
	for i := 0; i < 30; i++ {
		replies = append(replies, "```evolv\n{\"action\":\"list_dir\",\"path\":\""+strings.Repeat("a", i+1)+"\"}\n```")
	}
	p := &scriptedProvider{replies: replies}
	a := newLoopTestAgent(t, p)
	a.maxLoops = 3
	out := a.Chat(context.Background(), "loop forever")
	if !strings.Contains(out, "turn limit") {
		t.Fatalf("turn limit not enforced: %q", out)
	}
	if p.calls != 3 {
		t.Fatalf("calls = %d, want 3", p.calls)
	}
}

func TestLoopReportsProtocolProblems(t *testing.T) {
	p := &scriptedProvider{replies: []string{
		"```evolv\n{\"action\":\"read_file\",\"path\":\"a.go\"\n```",
		"All done.",
	}}
	a := newLoopTestAgent(t, p)
	a.Chat(context.Background(), "read something")
	if len(p.prompts) < 2 {
		t.Fatalf("prompts = %d", len(p.prompts))
	}
	last := p.prompts[1][len(p.prompts[1])-1].Content
	if !strings.Contains(last, "protocol_error") {
		t.Fatalf("protocol problem not fed back: %q", last)
	}
}

func TestLoopRetriesTransientError(t *testing.T) {
	p := &scriptedProvider{
		errs:    []error{&providers.ProviderError{Provider: "scripted", Status: 503, Kind: providers.ErrServer, Message: "upstream"}},
		replies: []string{"recovered"},
	}
	a := newLoopTestAgent(t, p)
	out := a.Chat(context.Background(), "hello")
	if !strings.Contains(out, "recovered") {
		t.Fatalf("retry did not recover: %q", out)
	}
	if p.calls != 2 {
		t.Fatalf("calls = %d, want 2", p.calls)
	}
}

func TestLoopDoesNotRetryAuthError(t *testing.T) {
	p := &scriptedProvider{errs: []error{&providers.ProviderError{Provider: "scripted", Status: 401, Kind: providers.ErrAuth, Message: "bad key"}}}
	a := newLoopTestAgent(t, p)
	out := a.Chat(context.Background(), "hello")
	if !strings.Contains(out, "bad key") {
		t.Fatalf("error not surfaced: %q", out)
	}
	if p.calls != 1 {
		t.Fatalf("auth error retried: %d calls", p.calls)
	}
}

func TestLoopCancellationStops(t *testing.T) {
	p := &scriptedProvider{replies: []string{"```evolv\n{\"action\":\"list_dir\",\"path\":\".\"}\n```"}}
	a := newLoopTestAgent(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := a.Chat(ctx, "hello")
	if !strings.Contains(out, "cancelled") {
		t.Fatalf("cancellation not reported: %q", out)
	}
}

func TestLoopGuardBudget(t *testing.T) {
	g := newLoopGuard(loopBudget{MaxTurns: 2, MaxDuration: time.Minute, MaxRepeat: 1, MaxFailureRun: 2, MaxResultBytes: 10})
	if r := g.nextTurn(); r != "" {
		t.Fatalf("turn 1 stopped: %s", r)
	}
	if r := g.nextTurn(); r != "" {
		t.Fatalf("turn 2 stopped: %s", r)
	}
	if r := g.nextTurn(); !strings.Contains(r, "turn limit") {
		t.Fatalf("turn 3 = %q", r)
	}
	g2 := newLoopGuard(loopBudget{MaxTurns: 10, MaxDuration: time.Minute, MaxRepeat: 1, MaxFailureRun: 2})
	g2.observeBatch("a", false)
	g2.observeBatch("a", false)
	if r := g2.observeBatch("a", false); !strings.Contains(r, "repeated") {
		t.Fatalf("repeat = %q", r)
	}
	g3 := newLoopGuard(loopBudget{MaxTurns: 10, MaxDuration: time.Minute, MaxRepeat: 5, MaxFailureRun: 2})
	g3.observeBatch("a", true)
	if r := g3.observeBatch("b", true); !strings.Contains(r, "failing") {
		t.Fatalf("failure streak = %q", r)
	}
	if !g3.accountBytes(11) {
		t.Fatal("byte budget not enforced")
	}
}

func TestClassifyErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		kind   providers.ErrorKind
		retry  bool
	}{
		{errors.New("x"), 401, providers.ErrAuth, false},
		{errors.New("x"), 429, providers.ErrRateLimit, true},
		{errors.New("x"), 500, providers.ErrServer, true},
		{errors.New("x"), 404, providers.ErrBadRequest, false},
		{context.DeadlineExceeded, 0, providers.ErrTimeout, true},
		{context.Canceled, 0, providers.ErrCanceled, false},
	}
	for _, c := range cases {
		pe := providers.Classify("p", c.status, c.err)
		if pe.Kind != c.kind || pe.Retryable() != c.retry {
			t.Errorf("classify(%v,%d) = %s/%v", c.err, c.status, pe.Kind, pe.Retryable())
		}
	}
	if providers.RetryableError(errors.New("plain")) {
		t.Error("plain error should not retry")
	}
}

func TestProviderErrorRedactsSecrets(t *testing.T) {
	err := &providers.ProviderError{Provider: "x", Status: 401, Kind: providers.ErrAuth, Message: "bad key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"}
	text := providers.SafeErrorText(err)
	if strings.Contains(text, "sk-ant-api03-abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("secret leaked: %s", text)
	}
}

func TestTurnStatsAccounted(t *testing.T) {
	p := &scriptedProvider{replies: []string{"```evolv\n{\"action\":\"list_dir\",\"path\":\".\"}\n```", "done"}}
	a := newLoopTestAgent(t, p)
	a.Chat(context.Background(), "list something")
	st := a.Stats()
	if st.Turns != 1 {
		t.Fatalf("turns = %d", st.Turns)
	}
	if st.ToolCalls == 0 {
		t.Fatal("tool calls not counted")
	}
	if st.LastTurnMS <= 0 {
		t.Fatal("turn duration not recorded")
	}
}

func TestTurnStatsCancelled(t *testing.T) {
	p := &scriptedProvider{replies: []string{"done"}}
	a := newLoopTestAgent(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.Chat(ctx, "hi")
	if a.Stats().TurnsCancelled != 1 {
		t.Fatalf("cancelled = %d", a.Stats().TurnsCancelled)
	}
}
