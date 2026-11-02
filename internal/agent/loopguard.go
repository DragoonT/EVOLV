package agent

import (
	"fmt"
	"time"
)

// loopBudget bounds a single Chat turn so a model that repeats itself cannot
// run forever. Defaults are generous: a large engineering assignment should
// still complete, but a pathological loop must terminate.
type loopBudget struct {
	MaxTurns       int           // model calls in one turn
	MaxDuration    time.Duration // wall clock for one turn
	MaxRepeat      int           // identical action batch before stopping
	MaxFailureRun  int           // consecutive failed batches before stopping
	MaxResultBytes int           // total tool-output bytes fed back
}

func defaultLoopBudget() loopBudget {
	return loopBudget{
		MaxTurns:       40,
		MaxDuration:    30 * time.Minute,
		MaxRepeat:      2,
		MaxFailureRun:  4,
		MaxResultBytes: 512 << 10,
	}
}

// loopGuard tracks repetition and failure streaks inside one turn.
type loopGuard struct {
	budget  loopBudget
	started time.Time
	turns   int
	repeats map[string]int
	lastKey string
	streak  int
	bytes   int
}

func newLoopGuard(b loopBudget) *loopGuard {
	if b.MaxTurns <= 0 {
		b = defaultLoopBudget()
	}
	return &loopGuard{budget: b, started: time.Now(), repeats: map[string]int{}}
}

// nextTurn accounts for one model call; it returns a non-empty reason when the
// turn should stop.
func (g *loopGuard) nextTurn() string {
	g.turns++
	if g.turns > g.budget.MaxTurns {
		return fmt.Sprintf("turn limit reached: %d model calls without finishing (raise it by asking for a smaller step)", g.budget.MaxTurns)
	}
	if elapsed := time.Since(g.started); elapsed > g.budget.MaxDuration {
		return fmt.Sprintf("time limit reached: %s elapsed in one turn", elapsed.Round(time.Second))
	}
	return ""
}

// observeBatch records one executed action batch. It returns a stop reason
// when the model has repeated the same batch too many times.
func (g *loopGuard) observeBatch(key string, failed bool) string {
	if key == g.lastKey {
		g.repeats[key]++
	} else {
		g.lastKey = key
		g.repeats[key] = 1
	}
	if g.repeats[key] > g.budget.MaxRepeat+1 {
		return fmt.Sprintf("repeated action batch stopped after %d identical attempts", g.repeats[key])
	}
	if failed {
		g.streak++
		if g.streak >= g.budget.MaxFailureRun {
			return fmt.Sprintf("%d consecutive failing batches — stopping so the user can inspect", g.streak)
		}
	} else {
		g.streak = 0
	}
	return ""
}

// accountBytes tracks how much tool output was fed back to the model and
// returns true when the budget is exhausted for this turn.
func (g *loopGuard) accountBytes(n int) bool {
	g.bytes += n
	return g.bytes > g.budget.MaxResultBytes
}
