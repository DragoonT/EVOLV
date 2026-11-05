# Workflow Engine

Durable execution and workflow packages: `internal/execution` (state, scheduling, recovery, worktrees), `internal/workflow` (declarative in-process DAG), and the resilience/progress/strategy layer in `internal/swarm`.

## Durable state (`Store`, `Journal`)

`Store` owns runs on disk: one directory per run with `run.json` (snapshot) and `journal.ndjson` (append-only log). Every mutation holds the store lock, then journals, then snapshots. Loading replays events past the snapshot's `Seq`; tasks mid-flight when the process died return to `queued`, journalled as `interrupted`.

- `Status` (`task.go`): `queued → planning → running → verifying → completed`, plus `waiting/paused/failed/cancelled`; `CanTransition(from, to)` validates every edge; terminal states reopen only via explicit requeue.
- `Task` carries `DependsOn`, `Priority`, `FilesPlan`, `Attempts`, `LastError`, `Verify []Verification`, `Checkpoint`, `RunID`; `Task.Level()` classifies evidence `verified | untested | partial | failed | blocked`.
- Store API: `Open`, `CreateRun(id, title, objective, tasks)`, `AddTasks`, `Transition`, `RecordAttempt`, `RecordVerification`, `Checkpoint`, `Close`, `Cancel`, `Retry`, `Get`, `List`, `Events`, `AttachWorktree`, `CloseAll`; `NewRunID()` mints sortable `run-YYYYMMDD-HHMMSS` ids.
- `Journal` (`journal.go`) appends newline-delimited `Event{Seq, At, Kind, Task, Data}`, fsyncs each write, truncates a torn final record on open. Kinds: `run.created`, `task.added`, `task.moved`, `task.attempt`, `task.error`, `verify.added`, `checkpoint`, `run.closed`, `run.reopened`, `worktree.created|updated|removed`; `NeedsRoll`/`Roll` archive at 8 MiB.

## Graph, waves, and plan compilation

`Graph` (`graph.go`) wraps a task snapshot: `Validate()` reports every problem at once (empty/duplicate ids, unknown/self deps, cycles via `Cycle()`), `Ready()` lists runnable tasks by priority then creation time, `Blocked()` lists tasks behind failed/cancelled deps, `Progress()` counts states, `Conflicts(ready)` flags pairs whose `FilesPlan` overlap.

`Schedule(g Graph) [][]string` (`plan.go`) groups tasks into waves: wave *N* only holds tasks whose deps live in earlier waves, so a wave runs concurrently. An unschedulable remainder (cycle, dangling dep) comes back as a final wave instead of being hidden.

`CompilePlan(spec PlanSpec) CompileResult` (`plan.go`) compiles a submitted plan (`PlanStepSpec{ID, Title, After, Files, Verify, Phase, Priority}`) into tasks: deps resolve by id **or** title, duplicate ids are reported, self/unknown deps are problems, 64-step cap. `CompileResult` carries `Tasks`, `Problems`, `Order`, `Waves`; `Summary()` renders a deterministic one-line-per-step listing (problems replace the schedule on failure). Entry point behind `internal/server/plan_api.go`.

## Scheduler: wave-parallel execution

`Scheduler` (`scheduler.go`) is storage-agnostic — it never touches `Store`. The caller supplies the work function; journalling happens inside it, so crash recovery uses the normal replay path.

```go
type Config struct {
    Workers     int            // per-wave concurrency, default 4
    Retry       SchedRetryFunc // (taskID, attempt, err) → (delay, again)
    OnTaskDone  func(taskID string)
    OnTaskError func(taskID string, err error)
    OnWaveStart func(wave int, taskIDs []string)
}
func NewScheduler(cfg Config, work func(ctx context.Context, task Task) error) (*Scheduler, error)
func (s *Scheduler) Run(ctx context.Context, tasks []Task) (SchedulerResult, error)
```

Semantics: structural problems are one error before anything runs; the blocked closure (failed deps propagate transitively) is computed before wave 0; a failed task never stops its wave-mates, only its dependents; context cancellation drains the wave in flight, starts no further waves, and names never-started tasks in `SchedulerResult.Remaining` (`Completed`, `Failed`, `Blocked`, `Waves` account for every input task).

## Retry policy

`Policy` (`retry.go`) decides retries in one reusable place. The wait before attempt *N* is `Initial * Multiplier^N`, capped at `Max`, jittered by `[1-Jitter, 1+Jitter)`; `MaxAttempts` bounds attempts (0 = unlimited); `RetryIf` filters errors. `DefaultPolicy()`: 500ms start, ×2 to 30s, ±20% jitter, 5 attempts, retry everything except `PermanentError`. Errors are classified by wrapping (`NewRetryableError` / `NewPermanentError`, matched via `errors.As`). `Policy.Next(attempt, err)` returns `(delay, shouldRetry)`; the zero `Policy` retries immediately forever.

## Run recovery: `ResumePlan`

`PlanResume(s *Store, runID) (*ResumePlan, error)` classifies every task of a dead/interrupted run: `Skip` (completed, kept), `Requeue` (interrupted or retryable, dependency-ordered), `Inspect` (waiting/paused — needs a human), `Blocked` (dependency parked in Inspect), plus `LastSeq` and journal-derived `Notes`. `ApplyResume(s, plan) ([]string, error)` requeues via public store mutations only: failed/cancelled tasks through `Store.Retry` first (it reopens a closed run), mid-flight tasks via `Transition` to `queued`; each requeue bumps `Attempts` so `LastError` is preserved. `ResumePlan.String()` renders bucket counts and ids.

## Journal queries

```go
type Query struct {
    RunID, TaskID string; Kinds []string
    After, Before time.Time; AfterSeq int64; Limit int64
}
func QueryJournal(s *Store, q Query) ([]Event, error)
func Summarize(entries []Event) Summary // {Total, ByKind, First, Last}
```

Filters AND: empty ids/kinds match everything; time bounds are inclusive; `AfterSeq` is a pagination cursor; `Limit` keeps the earliest matches. Results are deterministic — per-run sequence-ascending, runs in id order. `Summary` is sized for headers and badges.

## Event bus (`Bus`)

The journal is truth on disk; `Bus` (`events.go`) lets live observers see transitions without polling. `BusEvent` embeds the journal `Event` (one shape for both) and adds `RunID` plus `From`/`To` for `task.moved`; `BusEventFrom(runID, ev)` decodes the endpoints out of `Data` for you.

```go
bus := execution.NewBus()
sub, _ := bus.Subscribe(64)                    // buffered, clamped to ≤4096
for ev := range sub.C { ... }                  // closed by Unsubscribe/Close
bus.Publish(execution.BusEventFrom(runID, ev)) // never blocks
stats := bus.Stats()                           // BusStats{Published, Dropped, ...}
```

`Publish` is non-blocking: a full subscriber buffer drops the event and bumps that subscriber's counter in `BusStats.Dropped` — a slow consumer can never stall a mutation path.

## Hooks: lifecycle points

`Hooks` (`hooks.go`) is an ordered registry of synchronous callbacks at four `HookPoint`s: `HookBeforeTask`, `HookAfterTask`, `HookOnFailure`, `HookOnCancel`. `Register(point, name, fn)` replaces by name keeping position; `Fire(ctx, point, TaskInfo)` runs hooks in registration order with panic isolation and joins errors (`TaskInfo{TaskID, RunID, From, To, Err, Attempt, At}`). Keep hooks fast — they run inline on the caller's goroutine.

## Worktrees: isolation, GC, pooling

Per-run isolation (`worktree.go`): `CreateWorktree(repoDir, runID, opts)` adds `<repoRoot>/.evolv/worktrees/run-<runID>` on branch `<prefix><runID>` (default prefix `evolv/run-`, base = current ref); `CommitWorktree`, `DiffWorktree`, `MergeWorktree`, `RemoveWorktree` manage the lifecycle; `Run.Worktree` tracks it in the store via `AttachWorktree`/`UpdateWorktree`/`DetachWorktree`.

```go
base, _ := execution.WorktreesBaseDir(repoDir)     // <repoRoot>/.evolv/worktrees
removed, err := execution.GCWorktrees(base, 24*time.Hour)
pool := execution.NewWorktreePool(execution.WorktreePoolConfig{
    RepoDir: repoDir, BranchPrefix: "evolv/run-", MaxIdle: 2,
})
wt, err := pool.Acquire(runID) // reuse (reset --hard + clean -fdx) or create
defer pool.Release(wt, false)  // pool for reuse; discard=true removes now
```

`GCWorktrees` removes dirs older than the cutoff, fsyncs the base dir, and prunes git metadata. There is **no on-disk active marker**, so call it only when no live run holds a worktree (startup is the natural point). `WorktreePool` trades a fresh checkout per run for a bounded set of reusable directories; **uncommitted work in a released worktree is destroyed by the next `Acquire`** — commit before releasing.

## `internal/workflow`: declarative DAG

Stdlib-only, decoupled from `internal/execution`. A `Spec` lists `Step`s; an `Executor` runs ready steps concurrently and passes dependency outputs forward:

```go
type Func func(ctx context.Context, deps map[string]any) (any, error)
type Step struct {
    ID string; DependsOn []string
    Timeout time.Duration // per attempt; timeout ⇒ StatusTimeout, not retried
    Attempts int          // total tries, 0 means 1; retried with small backoff
    Fn Func
}
func Validate(spec Spec) error // ids, nil Fn, unknown deps, cycles
func (e *Executor) Run(ctx context.Context, spec Spec) (*Result, error)
```

Semantics: `deps` maps each direct dependency's ID to its output — every entry succeeded by construction, because a failed or timed-out step **skips all transitive dependents** (`StatusSkipped` with the cause). Cancelling `ctx` stops scheduling: queued steps become skipped, running steps end with whatever their `Fn` returns. `Run` returns a `Result` for every valid spec (per-step `StepResult`, `Outputs` of succeeded steps); the error return is reserved for `Validate` rejections; step panics are recovered into errors. Templates (`templates.go`): `Sequential(name, fns...)` chains steps (ids `name.0…`, read `deps["name.<i-1>"]`); `FanOut(name, branches, join)` runs branches in parallel then a join receiving `deps["outputs"] = []any`; `MapReduce(name, items, mapFn, reduceFn)` maps per item (`deps["item"]`, `deps["index"]`) then reduces over `deps["items"] = []any`.

## `internal/swarm` additions

- **`RunAll` / `CircuitBreaker`** (`resilience.go`): `RunAll(ctx, jobs, h, parallelism, backoff) Summary` runs `Job`s with per-job `Timeout`/`Attempts`, interruptible exponential `Backoff` (50ms→1s defaults), panic recovery, and cancellation that reports never-started jobs as errors. `CircuitBreaker{MaxConsecutiveFailures, Cooldown}` with `Allow`, `Record`, `State` (`closed`/`open`/`half-open`) guards a failing dependency. Neither touches `Engine` state — adoptable without contract changes.
- **`Tracker`** (`progress.go`): mutex-guarded per-agent rollup. `Register`/`Deregister`, `Mark(id, status)` (Engine words normalized onto `idle|running|done|error|stopped`; unknown words are non-terminal), `MarkNote`, `Snapshot()` → counts, `PerAgent` (`AgentState` cards enriched from `Roles` for `role-nn` ids), 5-minute `ThroughputPerMin`, newest-first `LastErrors` ring (cap 5). Not yet wired into `Engine`.
- **Strategy layer** (`strategies.go`): `Unit{ID, Tags, Weight}` / `Worker{ID, Capacity, Tags, Load}`, pure `Strategy` implementations `RoundRobinStrategy` (dealing cursor), `LeastLoadedStrategy`, `TagMatchStrategy` (shared-tag eligibility, least-loaded tie-break). `Assign(units, workers, strategy)` tolerates a nil strategy; unplaceable units are left out (never failed) — run `ValidateAssignments` when placement must be guaranteed.

## How a run executes end to end

1. **Plan → tasks.** A mission becomes a `PlanSpec`, compiled by `CompilePlan`; a result with `Problems` is never scheduled.
2. **Run opens.** `store.CreateRun(execution.NewRunID(), …)` journals `run.created` and snapshots the graph; callers may attach a worktree (`AttachWorktree`) for an isolated checkout — what `internal/agent/run_actions.go`, `internal/server/runs.go` and the swarm engine do today via `CreateWorktree`.
3. **Scheduling.** `Schedule` computes the wave order (also embedded in `CompileResult.Waves`). Two execution styles: `Scheduler.Run` for wave-parallel automated runs, or the server/agent path driving each task through `Store.Transition` (`queued → running → verifying → completed/failed`) with `RecordVerification` journalling real evidence.
4. **Failure handling.** `Store.Retry` requeues a failed task and reopens a closed run; a terminal failure journals `task.error` and, on run close, `OutcomeLog.RecordRunOutcomes` appends one `Outcome` per terminal task (`outcomes.jsonl`) for `Summarise`-based insights.
5. **Observation.** `Store.Events` replays a run's journal for the UI (`/api/runs/events`); `QueryJournal`/`Summarize` are the reusable filter layer; the `execution.Bus` is available for push instead of poll.
6. **Crash → resume.** On restart, `Store.Open` re-queues mid-flight tasks. For explicit recovery, `PlanResume` buckets the run and `ApplyResume` requeues it; the plan names what a human must inspect.
7. **Merge back.** `CommitWorktree` snapshots the work, `MergeWorktree` fast-forwards the base repo, `RemoveWorktree`/`Release` cleans up; the store's `worktree.*` events record each step.

## Adoption checklist

- **Worktree creation → `WorktreePool`.** `run_actions.go`, `server/runs.go` and the swarm engine call `CreateWorktree` per run (swarm uses `BranchPrefix: "evolv/swarm-"`). Switch to a shared `NewWorktreePool(...).Acquire(runID)` / `Release(wt, discard)` with its own `BranchPrefix`; keep `CommitWorktree` before release; `MaxIdle` 2–4.
- **`GCWorktrees` placement.** Once at server/agent startup, before any run is scheduled: `GCWorktrees(WorktreesBaseDir(repoDir), 24*time.Hour)`. Never while runs hold worktrees; a long-lived server should filter candidates against live `Store` runs instead.
- **Where to publish `BusEvents`.** Nothing outside `internal/execution` constructs the execution bus today (`internal/agent` uses the separate `internal/bus` topic bus). Publish at the store mutation call sites — `Transition`, `RecordAttempt`, `RecordVerification`, `Close`, `Cancel`, `Retry`, `AttachWorktree` in `run_actions.go` and the `/api/runs/*` handlers — converting journal events with `BusEventFrom(runID, ev)` for subscribers streaming runs to the UI (replacing `runs.js` polling). Store methods return tasks, not events, so publish the replayed tail from `Store.Events` or wrap the store so mutations fan out to the bus.
- **`Scheduler` + `Policy`.** The per-task transition handlers can become a `NewScheduler` whose `work` func performs one task and journals via the store; wire `Policy.Next` (or `DefaultPolicy`) into `Config.Retry`.
- **`PlanResume`/`ApplyResume`.** Expose an operator "resume run" endpoint; on boot, surface runs with `Inspect`/`Blocked` buckets instead of silently re-queueing everything.
- **`QueryJournal`/`Summarize`.** `/api/runs/events` re-implements filtering; route it through `QueryJournal` and attach `Summarize` counts.
- **`workflow` templates.** Plan → implement → verify flows fit `Sequential`; parallel file rewrites fit `FanOut`/`MapReduce` with the join as verifier. No package imports `internal/workflow` yet.
- **`RunAll`/`CircuitBreaker`/`Tracker`/strategies.** The swarm `Engine` can move its agent loop to `RunAll`, guard provider calls with a `CircuitBreaker`, and serve snapshots from `Tracker` instead of recomputing from `Engine.Snapshot()`.
