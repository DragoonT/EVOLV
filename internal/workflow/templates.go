package workflow

import (
	"context"
	"fmt"
)

// Sequential returns a spec that runs fns one after another, each step
// depending on the previous one. Step IDs are name.0, name.1, ... so a step
// reads its predecessor's output from deps[<name>.<i-1>].
func Sequential(name string, fns ...Func) Spec {
	steps := make([]Step, len(fns))
	for i, fn := range fns {
		steps[i] = Step{ID: fmt.Sprintf("%s.%d", name, i), Fn: fn}
		if i > 0 {
			steps[i].DependsOn = []string{steps[i-1].ID}
		}
	}
	return Spec{Name: name, Steps: steps}
}

// FanOut returns a spec that runs every branch in parallel and then runs
// join once all branches have completed. The join Fn receives
// deps{"outputs": []any} holding the branch outputs in branch order.
// Branch steps have no dependencies and IDs name.branch.<i>; the join step
// has ID name.join. A nil join produces a step with nil Fn, which Validate
// rejects.
func FanOut(name string, branches []Func, join Func) Spec {
	steps := make([]Step, 0, len(branches)+1)
	joinID := name + ".join"
	branchIDs := make([]string, 0, len(branches))
	for i, branch := range branches {
		id := fmt.Sprintf("%s.branch.%d", name, i)
		branchIDs = append(branchIDs, id)
		steps = append(steps, Step{ID: id, Fn: branch})
	}
	steps = append(steps, Step{ID: joinID, DependsOn: branchIDs, Fn: wfCollectFunc(join, "outputs", branchIDs)})
	return Spec{Name: name, Steps: steps}
}

// MapReduce returns a spec that runs mapFn once per item, all map steps in
// parallel (bounded by the Executor's Parallelism), and then runs reduceFn
// once over the collected map outputs. mapFn receives deps{"item": item,
// "index": i}; reduceFn receives deps{"items": []any} with the map outputs
// in item order. Map steps have IDs name.map.<i> and the reduce step has ID
// name.reduce. A nil reduceFn produces a step with nil Fn, which Validate
// rejects.
func MapReduce(name string, items []any, mapFn Func, reduceFn Func) Spec {
	mapIDs := make([]string, len(items))
	steps := make([]Step, 0, len(items)+1)
	for i, item := range items {
		id := fmt.Sprintf("%s.map.%d", name, i)
		mapIDs[i] = id
		item, idx, fn := item, i, mapFn
		steps = append(steps, Step{ID: id, Fn: func(ctx context.Context, _ map[string]any) (any, error) {
			return fn(ctx, map[string]any{"item": item, "index": idx})
		}})
	}
	steps = append(steps, Step{ID: name + ".reduce", DependsOn: mapIDs, Fn: wfCollectFunc(reduceFn, "items", mapIDs)})
	return Spec{Name: name, Steps: steps}
}

// wfCollectFunc adapts an aggregator Fn: it rebuilds deps as a single key
// holding the dependency outputs in dependency order, so aggregators see a
// deterministic slice instead of a map.
func wfCollectFunc(fn Func, key string, depIDs []string) Func {
	if fn == nil {
		return nil // let Validate reject the nil Fn
	}
	return func(ctx context.Context, deps map[string]any) (any, error) {
		collected := make([]any, len(depIDs))
		for i, id := range depIDs {
			collected[i] = deps[id]
		}
		return fn(ctx, map[string]any{key: collected})
	}
}
