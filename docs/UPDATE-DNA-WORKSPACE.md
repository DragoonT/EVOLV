# EVOLV DNA Workspace Update

This update turns EVOLV's accumulated mutations into a manageable, theme-aware workspace while adding safe DNA cloning and live genome inspection.

## Highlights

### Candidate evolution safety

- `stage_candidate` copies the detected EVOLV source into `workspace/candidates/<id>/source` and never edits the running tree.
- Every replacement file must include the SHA-256 hash of the live source it was based on.
- Each candidate runs `go test ./...` and `go build ./...` with bounded execution time; output, duration, changed paths, and hashes are archived in `manifest.json`.
- EVOLV candidates also compile a standalone executable, launch it with an isolated home and temporary loopback port, and require an HTTP 200 response from `/api/health` before becoming ready.
- The smoke-test process is always terminated after the probe; its isolated runtime files and compiled artifact remain with the candidate evidence.
- Failed candidates remain inspectable but cannot be promoted.
- Promotion is explicit, revalidates every live source hash before the first write, and aborts if the source changed after staging.
- The Creation Library includes a Candidates view with validation evidence, changed files, status, and a confirmed promotion control.

### Live DNA experience

- A slowly animated DNA strand remains visible beneath the application layers.
- **Live DNA View** opens a clean, closable hologram over the existing UI.
- The hologram retains genome labels, inspection, rotation, and zoom controls.
- The visualization follows the active light/dark theme and color palette.

### DNA Builder and agent strands

- **DNA Builder** starts with the one real main strand, **EVOLV1**.
- **Grow New Strand** clones the current genome only when requested.
- Before cloning, the operator can edit identity and system instructions and choose the encoded tools and skills.
- Selected capabilities use a restrained green glow that remains clear across themes.
- New strands receive stable names in sequence: EVOLV2, EVOLV3, and so on.
- Each strand has separate profile data and conversation memory, can be monitored, and can be selected for interaction.
- Clone IDs cannot overwrite an existing strand, and encoded capabilities are validated against the current genome before the clone is persisted.
- The builder now consumes the host workspace's surface, accent, typography, radius, density, and light/dark palette tokens instead of carrying a separate purple theme.
- Builder controls share the main app's focus, hover, input, button, and feedback patterns, with mobile reflow, backdrop/Escape close behavior, and inline request errors.

### Creation Library

- Creations, tools, plugins, widgets, and evolution events are collected in one searchable library instead of continually expanding the primary canvas.
- Items can be inspected, pinned, enabled or disabled, and archived.
- Pinned HTML widgets render live in the right sidebar, not in the center of the app.
- The library provides dedicated views for creations, pinned items, and evolution history.
- Newly grown creations are discovered from the workspace and exposed through a consistent API.

### Mutation integrity

- `read_file` now returns a SHA-256 content hash.
- Existing files require that hash as `expected_hash` before `write_file` or `edit_file` can mutate them.
- A changed hash rejects the mutation and forces a fresh read, preventing stale plans from overwriting newer work.
- Identical writes are no-ops and do not create repeated mutation records.
- Duplicate action payloads are deduplicated before execution.
- Cloned strands may only run tools encoded in their selected DNA profile.

### Modular interface

- The main shell, workspace layout, appearance controls, application behavior, DNA renderer, and styles remain separate assets.
- The DNA experience lives in `assets/ui/genome.html` and is embedded by the shell rather than duplicating the primary app.
- Panel placement remains managed by the workspace layout module, including the new pinned-creations sidebar panel.

## API additions

| Endpoint | Purpose |
| --- | --- |
| `GET /api/agents` | List the main DNA and cloned agent profiles. |
| `POST /api/agents` | Create a numbered DNA clone with identity, prompt, tools, and skills. |
| `POST /api/agents` with `action: "switch"` | Switch the active DNA strand. |
| `GET /api/creations` | Return the normalized creation inventory and evolution history. |
| `POST /api/creations` | Pin, archive, enable, or disable a creation. |
| `GET /api/widgets` | Return widget state for dashboard controls. |
| `POST /api/widgets` | Enable or disable a grown widget. |
| `GET /api/candidates` | List archived candidate builds and their test evidence. |
| `POST /api/candidates` | Explicitly promote a ready candidate after fresh hash validation. |

## Persistence and compatibility

- Agent profiles are stored under `workspace/agents/<id>/profile.json`.
- Conversation memory for clones is stored per profile under `workspace/agents/<id>/conversation.json`.
- Widget, pin, and archive preferences are added to workspace configuration with empty-map defaults, preserving existing configurations.
- EVOLV1 continues to use the main workspace genome and existing conversation storage.

## Operator flow

1. Open the main UI and use **Creation Library** to manage accumulated output.
2. Pin a widget to render its live HTML in the right sidebar.
3. Open **Live DNA View**, then select **DNA Builder**.
4. Select **Grow New Strand**, customize its identity and capabilities, and create it.
5. Switch between EVOLV1 and numbered clones from the DNA roster.

## Validation

- Go sources are formatted and the full application compiles with `go build ./...`.
- Browser scripts pass `node --check`.
- Repository whitespace is checked with `git diff --check`.
- Focused server tests cover the workbench-only initialization path; the existing full Windows suite still depends on isolated `~/.evolv` state and a POSIX `sh` executable.
- DNA-agent API tests cover the single-main initial roster, stable numbering, persistence across restart, capability normalization, duplicate rejection, and unavailable-runtime behavior.
- The branch includes upstream reasoning-token streaming and clean installer extraction changes merged from `upstream/main`.
