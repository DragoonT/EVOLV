# EVOLV engineering notes

Developer-facing notes for the subsystems touched by v0.31.0.

## Repository layout (relevant packages)

| Package | Responsibility |
|---|---|
| `internal/agent` | system prompt, action protocol, tool/plugin runtime, plans, loop guards |
| `internal/execution` | durable task lifecycle, dependency graph, journal, run store |
| `internal/providers` | model adapters, SSE decoding, error classification, retries |
| `internal/workspace` | genome on disk, config, migrations, filesystem safety helpers |
| `internal/candidate` | isolated self-evolution candidates, promotion, binary swap |
| `internal/server` | HTTP API, WebSocket bus bridge, metrics, runs API |
| `internal/swarm` | multi-agent colony (discussion mode) |
| `internal/lint` | security linting for evolved components and tools |

## Running everything locally

```bash
gofmt -l .                     # must print nothing
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go build ./...
```

Fuzzing (run longer locally, CI runs the seeds):

```bash
go test ./internal/agent -run FuzzExtractProtocol -fuzz FuzzExtractProtocol -fuzztime 30s
```

## Durable execution model

- Every run lives in `workspace/execution/runs/<id>/`.
- `journal.ndjson` is append-only; each line is one event with a sequence
  number. `run.json` is a snapshot written atomically after every mutation
  and is only a fast path — replaying the journal on top of it is the source
  of truth.
- Recovery converts active tasks to `queued`, records an attempt and the
  interruption reason, and journals the correction so the next restart sees
  consistent state.
- Status transitions are validated (`execution.CanTransition`). Adding a new
  status means updating the table, the tests and this document.

## Protocol contract

An assistant turn may contain:

    ```evolv
    {"action":"read_file","path":"internal/agent/agent.go"}
    {"action":"write_file","path":"x.go","content":"...","expected_hash":"sha256:..."}
    ```

Rules enforced by `extractProtocol`:

- fenced `evolv` blocks win; the last unterminated fence is still parsed;
- unfenced action JSON is only salvaged when the turn has no fenced block;
- each action must be valid JSON, under 256 KiB, under 64 levels deep;
- at most 64 actions per turn;
- identical actions run once;
- anything rejected becomes a `protocol_error` line in the tool-results
  message so the model can correct itself.

## Error handling

`providers.Classify` turns every failure into a `ProviderError` with a kind.
Read `providers.ErrorHint` for the user-facing suggestion. Never print a raw
provider error: use `providers.SafeErrorText`, which redacts credentials.

## Filesystem safety

- Use `workspace.AtomicWrite` for anything a crash could truncate.
- Use `workspace.GuardedWrite` when a client passes the hash it read; it
  returns a `FileError` with an HTTP status to surface.
- Use `workspace.CheckWithin` before touching a path derived from input: it
  resolves symlinks in the existing prefix and rejects escapes.

## Migrations

Add a `Migration` to `workspace.migrations` with a new ordered id, a `Since`
version and an idempotent `Apply`. Migrations run during `Load`, record
themselves in `workspace/migrations.json`, and must never delete user data.

## Metrics

`/api/metrics` exposes Prometheus text; `/api/health` exposes the same data
as JSON under `runs`. Label cardinality is bounded: action names are clipped
to 32 characters and nothing model- or path-specific becomes a label.
