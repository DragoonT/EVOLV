package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// wfOK returns a Func that always succeeds with v.
func wfOK(v any) Func {
	return func(context.Context, map[string]any) (any, error) { return v, nil }
}

// wfFail returns a Func that always fails with an error carrying msg.
func wfFail(msg string) Func {
	return func(context.Context, map[string]any) (any, error) { return nil, errors.New(msg) }
}

// wfAssertStatus checks that res contains step id with the wanted status.
func wfAssertStatus(t *testing.T, res *Result, id string, want Status) {
	t.Helper()
	got, ok := res.Steps[id]
	if !ok {
		t.Fatalf("step %q missing from result", id)
	}
	if got.Status != want {
		t.Fatalf("step %q status = %q, want %q (err=%v)", id, got.Status, want, got.Err)
	}
}

func TestValuePassingThroughDeps(t *testing.T) {
	spec := Spec{Name: "vals", Steps: []Step{
		{ID: "a", Fn: wfOK("A")},
		{ID: "b", DependsOn: []string{"a"}, Fn: func(_ context.Context, deps map[string]any) (any, error) {
			return deps["a"].(string) + "+B", nil
		}},
		{ID: "c", DependsOn: []string{"a", "b"}, Fn: func(_ context.Context, deps map[string]any) (any, error) {
			return deps["a"].(string) + "/" + deps["b"].(string), nil
		}},
	}}
	res, err := (&Executor{}).Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	for _, id := range []string{"a", "b", "c"} {
		wfAssertStatus(t, res, id, StatusSucceeded)
	}
	if got := res.Outputs["a"]; got != "A" {
		t.Errorf("Outputs[a] = %v, want A", got)
	}
	if got := res.Outputs["b"]; got != "A+B" {
		t.Errorf("Outputs[b] = %v, want A+B", got)
	}
	if got := res.Outputs["c"]; got != "A/A+B" {
		t.Errorf(`Outputs[c] = %v, want "A/A+B"`, got)
	}
	if res.Duration <= 0 {
		t.Errorf("res.Duration = %v, want > 0", res.Duration)
	}
}

func TestOrderRespectsDeps(t *testing.T) {
	var mu sync.Mutex
	var order []string
	record := func(id string) Func {
		return func(context.Context, map[string]any) (any, error) {
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			return id, nil
		}
	}
	spec := Spec{Name: "ordered", Steps: []Step{
		{ID: "a", Fn: record("a")},
		{ID: "b", DependsOn: []string{"a"}, Fn: record("b")},
		{ID: "c", DependsOn: []string{"b"}, Fn: record("c")},
		{ID: "z", Fn: record("z")}, // independent; may interleave anywhere
	}}
	res, err := (&Executor{Parallelism: 4}).Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(order) != 4 {
		t.Fatalf("recorded %d steps (%v), want 4", len(order), order)
	}
	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}
	for _, id := range []string{"a", "b", "c", "z"} {
		wfAssertStatus(t, res, id, StatusSucceeded)
	}
	if !(pos["a"] < pos["b"] && pos["b"] < pos["c"]) {
		t.Errorf("execution order %v violates a -> b -> c", order)
	}
}

func TestFailureSkipsTransitiveDependents(t *testing.T) {
	spec := Spec{Name: "cascade", Steps: []Step{
		{ID: "a", Fn: wfFail("boom")},
		{ID: "b", DependsOn: []string{"a"}, Fn: wfOK("B")},
		{ID: "c", DependsOn: []string{"b"}, Fn: wfOK("C")},
		{ID: "d", Fn: wfOK("D")}, // unaffected
	}}
	res, err := (&Executor{}).Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	wfAssertStatus(t, res, "a", StatusFailed)
	wfAssertStatus(t, res, "b", StatusSkipped)
	wfAssertStatus(t, res, "c", StatusSkipped)
	wfAssertStatus(t, res, "d", StatusSucceeded)
	if res.Steps["a"].Err == nil || res.Steps["a"].Err.Error() != "boom" {
		t.Errorf("a.Err = %v, want boom", res.Steps["a"].Err)
	}
	if res.Steps["b"].Err == nil || !strings.Contains(res.Steps["b"].Err.Error(), `dependency "a"`) {
		t.Errorf("b.Err = %v, want mention of dependency \"a\"", res.Steps["b"].Err)
	}
	if len(res.Outputs) != 1 {
		t.Fatalf("Outputs = %v, want only d", res.Outputs)
	}
	if got, ok := res.Outputs["d"]; !ok || got != "D" {
		t.Errorf("Outputs[d] = %v (%v), want D", got, ok)
	}
}

func TestRetryCounting(t *testing.T) {
	var calls int32
	flaky := func(context.Context, map[string]any) (any, error) {
		if atomic.AddInt32(&calls, 1) < 3 {
			return nil, errors.New("flaky")
		}
		return "ok", nil
	}
	spec := Spec{Name: "retry", Steps: []Step{{ID: "flaky", Attempts: 3, Fn: flaky}}}
	res, err := (&Executor{}).Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	wfAssertStatus(t, res, "flaky", StatusSucceeded)
	if got := res.Steps["flaky"].Attempts; got != 3 {
		t.Errorf("Attempts = %d, want 3", got)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("Fn invocations = %d, want 3", got)
	}
	if got := res.Outputs["flaky"]; got != "ok" {
		t.Errorf("Outputs[flaky] = %v, want ok", got)
	}
}

func TestStepTimeoutFast(t *testing.T) {
	slow := func(ctx context.Context, _ map[string]any) (any, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
			return "late", nil
		}
	}
	spec := Spec{Name: "timeout", Steps: []Step{{ID: "slow", Timeout: 25 * time.Millisecond, Fn: slow}}}
	start := time.Now()
	res, err := (&Executor{}).Run(context.Background(), spec)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	wfAssertStatus(t, res, "slow", StatusTimeout)
	if res.Steps["slow"].Attempts != 1 {
		t.Errorf("Attempts = %d, want 1 (timeouts are not retried)", res.Steps["slow"].Attempts)
	}
	if !errors.Is(res.Steps["slow"].Err, context.DeadlineExceeded) {
		t.Errorf("slow.Err = %v, want context.DeadlineExceeded", res.Steps["slow"].Err)
	}
	if elapsed >= 100*time.Millisecond {
		t.Errorf("timeout path took %v, want < 100ms", elapsed)
	}
}

func TestParallelismCap(t *testing.T) {
	const total = 6
	var mu sync.Mutex
	active, maxActive := 0, 0
	fn := func(context.Context, map[string]any) (any, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return nil, nil
	}
	steps := make([]Step, total)
	for i := range steps {
		steps[i] = Step{ID: fmt.Sprintf("s%d", i), Fn: fn}
	}
	start := time.Now()
	res, err := (&Executor{Parallelism: 2}).Run(context.Background(), Spec{Name: "cap", Steps: steps})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	for i := 0; i < total; i++ {
		wfAssertStatus(t, res, fmt.Sprintf("s%d", i), StatusSucceeded)
	}
	if maxActive != 2 {
		t.Errorf("max concurrent Fn = %d, want exactly 2", maxActive)
	}
	// Six 40ms steps with a cap of 2 need at least three waves.
	if elapsed < 100*time.Millisecond {
		t.Errorf("run finished in %v, too fast for Parallelism=2", elapsed)
	}
}

func TestParentCancelStopsScheduling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()
	slow := func(ctx context.Context, _ map[string]any) (any, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return "done", nil
		}
	}
	spec := Spec{Name: "cancel", Steps: []Step{
		{ID: "slow", Fn: slow},
		{ID: "after", DependsOn: []string{"slow"}, Fn: wfOK("x")},
		{ID: "z", Fn: wfOK("z")}, // queued/finished before the cancel lands
	}}
	start := time.Now()
	res, err := (&Executor{}).Run(ctx, spec)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("Run hung for %v after parent cancel", elapsed)
	}
	wfAssertStatus(t, res, "slow", StatusFailed)
	if !errors.Is(res.Steps["slow"].Err, context.Canceled) {
		t.Errorf("slow.Err = %v, want context.Canceled", res.Steps["slow"].Err)
	}
	wfAssertStatus(t, res, "after", StatusSkipped)
	wfAssertStatus(t, res, "z", StatusSucceeded)
}

func TestValidateRejectsDuplicateID(t *testing.T) {
	spec := Spec{Steps: []Step{{ID: "a", Fn: wfOK(1)}, {ID: "a", Fn: wfOK(2)}}}
	err := Validate(spec)
	if err == nil {
		t.Fatal("Validate accepted duplicate step ID")
	}
	if !strings.Contains(err.Error(), `duplicate step ID "a"`) {
		t.Errorf("error = %v, want duplicate step ID mention", err)
	}
}

func TestValidateRejectsUnknownDep(t *testing.T) {
	spec := Spec{Steps: []Step{{ID: "a", DependsOn: []string{"ghost"}, Fn: wfOK(1)}}}
	err := Validate(spec)
	if err == nil {
		t.Fatal("Validate accepted unknown dependency")
	}
	if !strings.Contains(err.Error(), `unknown step "ghost"`) {
		t.Errorf("error = %v, want unknown step mention", err)
	}
}

func TestValidateRejects3NodeCycleNamingPath(t *testing.T) {
	spec := Spec{Steps: []Step{
		{ID: "a", DependsOn: []string{"b"}, Fn: wfOK(1)},
		{ID: "b", DependsOn: []string{"c"}, Fn: wfOK(1)},
		{ID: "c", DependsOn: []string{"a"}, Fn: wfOK(1)},
	}}
	err := Validate(spec)
	if err == nil {
		t.Fatal("Validate accepted a dependency cycle")
	}
	if !strings.Contains(err.Error(), "a -> b -> c -> a") {
		t.Errorf("error = %v, want cycle path a -> b -> c -> a", err)
	}
}

func TestValidateRejectsNilFn(t *testing.T) {
	spec := Spec{Steps: []Step{{ID: "x", Fn: nil}}}
	err := Validate(spec)
	if err == nil {
		t.Fatal("Validate accepted nil Fn")
	}
	if !strings.Contains(err.Error(), `nil Fn`) {
		t.Errorf("error = %v, want nil Fn mention", err)
	}
}

func TestValidateAcceptsValidSpecs(t *testing.T) {
	if err := Validate(Spec{}); err != nil {
		t.Errorf("Validate(empty spec) = %v, want nil", err)
	}
	spec := Sequential("ok", wfOK(1), wfOK(2))
	if err := Validate(spec); err != nil {
		t.Errorf("Validate(sequential) = %v, want nil", err)
	}
}

func TestRunRejectsInvalidSpec(t *testing.T) {
	spec := Spec{Steps: []Step{{ID: "a", DependsOn: []string{"a"}, Fn: wfOK(1)}}} // self-cycle
	res, err := (&Executor{}).Run(context.Background(), spec)
	if err == nil {
		t.Fatal("Run accepted an invalid spec")
	}
	if res != nil {
		t.Errorf("Run returned a Result alongside an error: %v", res)
	}
}

func TestSequentialHappyPath(t *testing.T) {
	spec := Sequential("seq",
		func(_ context.Context, _ map[string]any) (any, error) { return "a", nil },
		func(_ context.Context, deps map[string]any) (any, error) { return deps["seq.0"].(string) + "b", nil },
		func(_ context.Context, deps map[string]any) (any, error) { return deps["seq.1"].(string) + "c", nil },
	)
	if err := Validate(spec); err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	if len(spec.Steps) != 3 {
		t.Fatalf("len(Steps) = %d, want 3", len(spec.Steps))
	}
	if got := spec.Steps[1].DependsOn; len(got) != 1 || got[0] != "seq.0" {
		t.Errorf("Steps[1].DependsOn = %v, want [seq.0]", got)
	}
	if got := spec.Steps[2].DependsOn; len(got) != 1 || got[0] != "seq.1" {
		t.Errorf("Steps[2].DependsOn = %v, want [seq.1]", got)
	}
	res, err := (&Executor{}).Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	for _, id := range []string{"seq.0", "seq.1", "seq.2"} {
		wfAssertStatus(t, res, id, StatusSucceeded)
	}
	if got := res.Outputs["seq.2"]; got != "abc" {
		t.Errorf(`Outputs[seq.2] = %v, want "abc"`, got)
	}
}

func TestFanOutHappyPath(t *testing.T) {
	join := func(_ context.Context, deps map[string]any) (any, error) {
		outputs, ok := deps["outputs"].([]any)
		if !ok {
			return nil, errors.New("join: missing outputs")
		}
		sum := 0
		for _, o := range outputs {
			v, ok := o.(int)
			if !ok {
				return nil, errors.New("join: non-int branch output")
			}
			sum += v
		}
		return sum, nil
	}
	spec := FanOut("fan", []Func{wfOK(1), wfOK(2), wfOK(3)}, join)
	if err := Validate(spec); err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	res, err := (&Executor{}).Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	for _, id := range []string{"fan.branch.0", "fan.branch.1", "fan.branch.2"} {
		wfAssertStatus(t, res, id, StatusSucceeded)
	}
	wfAssertStatus(t, res, "fan.join", StatusSucceeded)
	if got := res.Outputs["fan.join"]; got != 6 {
		t.Errorf("Outputs[fan.join] = %v, want 6", got)
	}
}

func TestMapReduceHappyPath(t *testing.T) {
	mapFn := func(_ context.Context, deps map[string]any) (any, error) {
		return deps["item"].(int) * 2, nil
	}
	reduceFn := func(_ context.Context, deps map[string]any) (any, error) {
		items, ok := deps["items"].([]any)
		if !ok {
			return nil, errors.New("reduce: missing items")
		}
		sum := 0
		for _, it := range items {
			v, ok := it.(int)
			if !ok {
				return nil, errors.New("reduce: non-int item")
			}
			sum += v
		}
		return sum, nil
	}
	spec := MapReduce("mr", []any{1, 2, 3, 4}, mapFn, reduceFn)
	if err := Validate(spec); err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	res, err := (&Executor{}).Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	for i := 0; i < 4; i++ {
		wfAssertStatus(t, res, fmt.Sprintf("mr.map.%d", i), StatusSucceeded)
	}
	wfAssertStatus(t, res, "mr.reduce", StatusSucceeded)
	if got := res.Outputs["mr.reduce"]; got != 20 {
		t.Errorf("Outputs[mr.reduce] = %v, want 20", got)
	}
}
