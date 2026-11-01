# EVOLV — 24-HOUR MASTER PLAN

*Engineering plan for the autonomous evolution sprint. Generated from a full repository audit during the v0.12.0–v0.21.0 sprint; revised each release. The hourly roadmap in `/home/z/my-project/evolv-hourly-plan.md` stays authoritative for its queue — this document is the strategic layer above it.*

---

## 1. Repository audit (verified, not assumed)

### What exists and works (verified this sprint)

| Area | Location | Status |
|---|---|---|
| Go kernel | `cmd/evolv`, `internal/{agent,server,workspace,bus,providers,watcher,update}` | builds + tests green at every release |
| Agent loop | `internal/agent` — multi-turn tool loop, `commit()` ledger, dreaming flag | works; every mutation audited in `evolution.jsonl` |
| Tool calling | JSON actions over the bus (`evolv` fenced blocks), ~20 action types | works; mocked-brain E2E every release |
| Self-built UI | `assets/ui` shell + `workspace/ui/components/*.html` hot-remount via watcher | works; 17→18 shell versions, zero-refresh verified |
| Generative dream engine | `internal/providers/mock_script.go` — parametric organ composer (5 families × 6 palettes), self-rewrites its own organ files | works; test enforces "no repeated code" |
| Snapshots / rollback / diff | `/api/snapshots`, `/api/snapshots/diff` (LCS), restore | works |
| Plugins | `internal/agent/pluginvm.go` (goja), live event bus access | works; sandboxing is thin — see §3 |
| Update mechanism | `internal/update`, release watch, `evolv update` | works; preserves workspace |
| Perf | virtualized ledger (DOM cap 200 + pager), rAF-batched streaming | v0.21.0, 9/9 live assertions |
| Search, export, mobile, sound | ctrl+f search, `/api/export` standalone HTML, <820px pass, WebAudio chimes | shipped v0.14–v0.16 |

### Partially implemented / weak

1. **Plugin isolation** — goja VM has bus access but no permission tiers, no resource limits, no per-plugin filesystem policy.
2. **Workspace portability** — snapshots exist locally; no export/import/share package format, no checksums/manifests.
3. **Skill system** — skills are prompt-files; no versioning, no evaluation harness, no dedup, no decay of stale skills.
4. **Agent planning** — the loop reacts well but has no explicit plan/task-state model for long multi-file missions; no resume-after-crash of a half-done task.
5. **Tests** — kernel tests are real but coverage is uneven (`internal/server`, `watcher` have none); no frontend test harness (smoke tests are ad-hoc CDP scripts).
6. **Docs** — README is strong; no generated docs site (→ v0.22.0), no API reference, no tutorial.

### Security posture

- Path resolution is workspace-rooted (`resolvePath`) — good; shell action is the main escape hatch, gated on nothing yet.
- Token handling: release-watch only; never persisted into workspace.
- Installer verifies nothing yet (no checksums on release tarballs — see §3 S3).

---

## 2. Strategic priorities (scored)

| # | Capability | Value | Effort | Dependency | Verdict |
|---|---|---|---|---|---|
| P0 | Agent task-planner + persistent task state (§4A) | unlock everything | M | none | **do first** |
| P0 | Candidate-build self-evolution loop (§4B) | the differentiator | L | planner | do in parallel |
| P1 | Workspace export/import package (§5) | community growth | M | snapshots | after P0 |
| P1 | Docs site + installer `--version` (v0.22.0) | first impression | S | none | now |
| P1 | Plugin permission tiers + limits | trust | M | audit | after P0 |
| P2 | Skill eval harness + versioning | quality | M | planner | later |
| P2 | Design system consolidation (tokens, motion) | premium feel | S | none | ongoing |
| P2 | Marketing kit + demo scripts (§9) | adoption | S | docs site | after v0.22 |

---

## 3. Engineering roadmap

### A. Autonomous agent engine
- **Planner**: explicit `plan.json` task-state (steps, status, artifacts), rendered in the UI as a live checklist card; agent writes/reads it via a new `set_plan` action; survives restarts.
- **Repository understanding**: `map_repo` action → tree + symbol outline (Go AST / JS heuristics) cached in workspace.
- **Verification loop**: new `run_tests` action parses go test / node output into structured pass-fail the agent reasons over.
- **Completion reports**: final turn emits a structured summary card (files, tests, confidence).
- Acceptance: mock-brain E2E where the agent completes a 3-file feature change unattended, with tests green.

### B. Self-evolution engine
- **Candidate builds**: `self_modify` proposal → isolated worktree/copy → `go build` + `go test` → health check (`/api/health` + boot smoke) → swap only on green; auto-rollback on failed boot.
- Every candidate archived under `workspace/candidates/<tag>/` with its diff and test output (auditable evolution).
- Never touch the running binary; swap-on-success only.

### C. Self-built UI (already strong — keep the bar)
- Components must ship with data, not decoration; ban template look-alikes via the dream-engine test.
- Add `evolve_layout` action for full-shell redesigns behind a confirm.

### D. Tools / plugins / skills
- Tool manifests with typed IO; plugin `manifest.json` with permissions (`fs:read`, `net:none`, ...), enforced in goja bridge; resource ceiling per plugin.
- Skills: versioned, with a 5-case eval file; dream mode prunes skills that fail eval.

### E. External project development
- Project binding (`set_project`) hardened: work only inside bound root; `open_project` explorer flow in UI.

## 4. Workspace: indestructible + portable
- Updates already preserve workspace; add **versioned migrations** (`workspace/migrations/*.go`, run on boot, journal + dry-run flag).
- **Export**: `evolv export` → `.evolvpack` (tar+manifest: version, checksums per file, component list, NO keys — secret scan refuses to pack credential-shaped files).
- **Import**: manifest validation → conflict report → user chooses per-file; never auto-executes imported plugins (quarantine flag until user enables).

## 5. Security backlog
- S1: shell action allowlist + confirmation for destructive verbs outside workspace.
- S2: plugin permission tiers + CPU/memory ceiling.
- S3: installer checksum verification (sha256 from release notes) + `--version` flag (v0.22.0).
- S4: credential redaction in logs/ledger (scan for token shapes before writing).

## 6. QA standard for every release
`gofmt` → `go vet ./...` → `go build ./...` → `go test ./...` → (`-race` weekly) → live boot smoke (isolated HOME) → browser assertions where UI changed → tag → Release notes with evidence.

## 7. Marketing (build in public, no hype)
- Positioning: *"An AI workspace that evolves its own software."*
- Assets per release: changelog row + release notes + one demo; the promo video (1:48, zero dead-air) is the flagship.
- Docs site (v0.22.0) doubles as landing content; og.png refresh only on brand change.
- Community posts prepared, published only with maintainer authorization.

## 8. Measurable completion criteria
Every shipped item must have: green build+tests, one live E2E proof (command + observed output), a changelog row, a release, and a log entry. No exceptions, no invented statistics.
