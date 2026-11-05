# Embedded UI Overview

The shell UI lives in `assets/ui/` and is served by the Go server as plain
static files — no build step, no framework, no modules. Pages are plain HTML +
`<script>` IIFEs; optional modules attach to `window.EVOLV*` namespaces so the
app boots normally when a script is missing or broken. All lifecycle truth
lives in the backend (`/api/*`, the `/ws` WebSocket); the files here only
reflect it.

## File map

| File | Responsibility |
| --- | --- |
| `index.html` | Shell document: ambient canvas stage, sidebar, chat column, composer, drawer, modals, script load order |
| `style.css` | Only stylesheet of the shell: design tokens + all shell/component styling |
| `app.js` | Core client logic: WebSocket, chat rendering, slash commands, palette wiring, settings, genome inspector, workbench |
| `command-palette.js` | Optional slash-command palette; exposes `window.EVOLVPalette` |
| `runs.js` | Durable-execution run inspector backed by `/api/runs` |
| `organs.js` | Grown-asset ledger (tools/skills/plugins) backed by `/api/organs` |
| `genome.html` | Standalone inline DNA genome page (own tokens/scripts); embedded via iframe |
| `swarm.html` | Standalone inline swarm-mode page (own tokens, WebSocket client) |
| `ambient.js` | Background canvas constellation behind the workspace |
| `appearance.js` | Appearance playground panel in the settings drawer |
| `workspace-layout.js` | Dockable, persistent sidebar panel layout + right inspector dock |
| `toasts.js` | Opt-in toast module (`window.EVOLVToasts`): `show(msg, {type, timeout, actions})` → `{dismiss(), el}` + `success/error/info` shorthands; mounts at `#toasts`, coexists with `app.js` `toast()` |
| `shortcuts.js` | Opt-in keyboard-shortcut module (`window.EVOLVKeys`): `register(combo, handler, {label, allowInInput, section})`, `list()`, `help()` overlay; `mod+k` focus input, `?` help, `mod+shift+l` sidebar; self-activating |

### `index.html` (404 lines)

Static shell. Opens with the `#ambient-stage` canvas layer (`aria-hidden`),
then `#app` = sidebar + chat column. The sidebar groups its sections with
`data-sb-group` labels — `WORKSPACE` (mind, plan), `EXECUTION` (runs),
`KNOWLEDGE` (organs, pinned), `ACTIVITY` (evolution stream) — plus a
`.sb-bottom` navigation block (library, workbench, swarm link, organs, genome
inspector, settings, provider pill). Accessibility: `aria-label` on the
sidebar and drawer, `role="img"` + `aria-label` on the logo mark, every modal
(`hologram`, `genome-modal`, `wb-modal`, `organ-modal`, `diff-modal`,
`replay-modal`, library, run, organs) carries `role="dialog" aria-modal="true"`
with labelled close buttons; `#toasts` is an `aria-live="polite"` region (the
anchor the future `toasts.js` and today's `window.evolvToast` use). Script
order at the bottom: `ambient.js → command-palette.js → app.js →
workspace-layout.js → runs.js → organs.js → appearance.js → toasts.js →
shortcuts.js` (palette before
`app.js` so it exists when `attachPalette` runs; `app.js`'s own Enter-to-send
handler checks `paletteOpen()` and defers).

### `app.js` (core)

The organism's front door. `connect()` opens `wss://…/ws` with reconnect and
`handle(ev)` switches on server event types (`hello`, tokens, tool calls,
evolution, plan, fs changes — including a shell-reload guard for
`workspace/ui/index.html`/`app.js` edits). It renders the conversation
(`mdLite` markdown, thinking state, tool cards, reasoning), grafts organ
components into the canvas, drives the evolution stream/replay/diff modals,
the genome inspector and workbench editors (ctrl+s writes via `/api`), the
settings drawer, sound blips (default off), theme toggle (`applyTheme`), and
search/export. Owns `SLASH_COMMANDS` and the `/command` routing (below), and
exposes `window.evolvToast` as the current toast helper writing into
`#toasts`.

### `command-palette.js`

Zero-dependency palette, evaluated safely even in a bare JS VM. Contract:

```js
EVOLVPalette.attach(inputEl, {
  getCommands: () => [{ name, description, args?, hint? }],
  onSubmit: (cmdName, restText) => true /* consumed */ | false /* fall back */,
}) // → { destroy(), isOpen() }
EVOLVPalette.fuzz(query, names) // names sorted best-first (pure)
EVOLVPalette.tokenize(value)    // { name, rest, hasArgs } or null if not "/…"
```

Fuzz ranking tiers (case-insensitive): exact prefix (4000) > word-boundary
prefix (3000) > contiguous substring (2000) > sparse subsequence (1000), with
distance/adjacency bonuses; ties break on shorter name, then the caller's list
order — stable, so list order floats common commands up. Accepted keys get
`ev.__evolvPaletteHandled = true` so the app's keydown handler can defer.
Renders a `role="listbox"` anchored under the input (`combobox`,
`aria-activedescendant`), max 8 rows, match-character highlighting. Note:
app.js's registry entries carry `desc`/`title`, while the palette renders
`description`/`args`/`hint` — align field names if you want descriptions shown
in the palette UI.

### `runs.js`

Interactive inspector for durable runs, mounted into the sidebar `#runs-panel`
plus the `#run-modal` detail dialog; its header comment pins the exact id
contract with `index.html`/`app.js`. Status grouping drives the scan order —
`Active` (running/planning/verifying) → `Waiting` (waiting/paused) → `Queued` →
`Completed` → `Failed / Cancelled` → `Other` — and each run card renders
progress from the backend's task graph. The detail modal has two tabs, "Tasks
& Graph" (task cards with dependency and verification evidence: `✓ PASSED` /
`✕ FAILED (exit N)`) and "Journal & Evidence" (event rows). Polls
`/api/runs`, `/api/runs/detail`, `/api/runs/events`; cancel/retry/status
updates POST to `/api/runs/*` and re-render from the response. State boxes use
`role="alert"`/`role="status"`.

### `organs.js`

The improvement ledger for everything the organism grew: tools, skills,
prompts, plugins, components — with real health and one-click rollback. Data
comes from `/api/organs` (never invented client-side). Health verdicts
(`healthy`, `unproven`, `flaky`, `broken`, `stale`/retired) map to
`HEALTH_TIER { broken:0, flaky:1, unproven:2, healthy:3, stale:4 }` (unknown
verdicts sink below stale), deliberately mirroring `internal/genome`
`HealthReport` ordering. `sortedOrgans` is deterministic: tier, then name,
then kind, then version. Cards expose rollback (`openRollback`/`rollback`),
enable/disable (`setStatus`), and version history.

### `style.css`

Single stylesheet, explicitly written to be rewritten by the organism at
runtime. Opens with the `:root` token block (below), then styles the shell
grid, sidebar, chat, composer, modals, run cards, library, appearance panel,
palette hooks (`.evolv-palette-*` styles are injected by `command-palette.js`
itself, reusing `var(--bg3)`/`--r-sm` etc.), and a light `.paper` theme plus
density/radius overrides driven by `appearance.js` setting CSS variables
inline on `<body>`.

### `genome.html` / `swarm.html`

Self-contained inline pages: each embeds its own `<style>` and `<script>`, so
they work standalone and inside iframes. `genome.html` ("Live DNA View", ~1.5k
lines) declares its own dark token set and renders the genome as interactive
strands — a builder roster ("DNA Strands", `#strand-list`, "Grow New Strand")
plus a main canvas of living gene cards; `index.html` embeds it lazily via
`#hologram-frame` with `data-src="/ui/genome.html?embed=1"`. `swarm.html` is
the swarm-mode page with its own warm light tokens (serif headings, clay
accent): mission composer with agent-size select, agent rail with role filter,
phase rail, live thread (`aria-live="polite"`), colony map canvas, polls and
artifacts panels — all driven by its own `/ws` WebSocket client with
reconnect. Because both pages restate tokens locally, global token changes in
`style.css` do not reach them.

### Support modules: `ambient.js`, `appearance.js`, `workspace-layout.js`

- `ambient.js` — the quiet constellation behind the workspace: a DPR-capped
  canvas on `#ambient-stage`, density scaled to viewport (22–76 particles),
  accent color read live from `--accent`, paused when the tab is hidden,
  reduced to a still frame under `prefers-reduced-motion`.
- `appearance.js` — local-only theming panel appended to the settings drawer:
  palette presets (obsidian, arctic, graphite, nord, forest, rose), accent
  swatches + custom color, density (`compact|comfortable|spacious`), corner
  radius (0–24). Applies as CSS variables/`data-*` on `<body>`, persists to
  `localStorage["evolv.appearance.v1"]`, and stays in step with the global
  `applyTheme` paper/dark switch.
- `workspace-layout.js` — turns the static sidebar into a dockable panel
  system: each known section (`mind-sec`, `plan-sec`, `organs-sec`,
  `pinned-sec`, `evo-sec`, plus synthesized `navigation-sec`/`model-sec`)
  gains collapse/move/lock controls, drag and Alt+Arrow reordering, a resize
  grip, and a right-hand `#workspace-inspector` dock. Layout persists to
  `localStorage["evolv.panel-layout.v1"]` and is re-applied on load; only the
  known sections are reparented, group labels travel untouched.

### `toasts.js` / `shortcuts.js` (opt-in modules — mid-flight)

Not present in `assets/ui/` at the time of writing (parallel work in flight);
`index.html` already provides the `#toasts` `aria-live="polite"` anchor and
`app.js` a `window.evolvToast` fallback, so these modules are additive and
optional — the app must boot unchanged without them. Their documented window
API contracts:

- `window.EVOLVToasts` — push notifications into `#toasts`: a `show(msg,
  opts)`-style entry point with lifetime/dismissal handled by the module; the
  module owns `.toast` markup and its enter/exit classes in `style.css`.
- `window.EVOLVKeys` — global keyboard shortcut registry: register/unregister
  bindings (the ctrl+k new chat, `/` focus, ctrl+shift+l daylight, ctrl+f
  search currently hardcoded in `app.js` move here), with the handler returning
  whether it consumed the event so inputs keep typing behavior.

Both ship as opt-in modules loaded at the end of `index.html`. Contract
level: attach to an existing anchor, degrade to a no-op when absent, and
replace no core handler.

## Design tokens

Declared in `style.css` `:root`; everything in the shell must consume these
instead of raw values (`genome.html`/`swarm.html` restate their own sets).

| Scale | Tokens | Values |
| --- | --- | --- |
| Spacing | `--s1 … --s8` | 4, 8, 12, 16, 24, 32, 48, 64 px |
| Radius | `--r-xs`, `--r-sm`, `--r-md`, `--r-lg`, `--r-full` | 4, 8, 12, 16, 999 px; `--radius` aliases `--r-md` and stays overridable (appearance slider sets it inline on `<body>`) |
| Type | `--fs-xs … --fs-3xl` | 12, 13, 14, 16, 18, 22, 28 px |
| Surfaces | `--bg`, `--bg2`, `--bg3`, `--bg4` | main / sidebar / cards / hover layers |
| Lines & text | `--line`, `--line-soft`, `--text`, `--dim`, `--faint` | borders and text emphasis ladder |
| Signal | `--accent` (`#c9ef72` lime), `--accent-soft`, `--ok`, `--danger` | the accent is read live by `ambient.js` and replaced by `appearance.js` |
| Fonts | `--font` (system sans), `--mono` | UI and code |
| Layout | `--sb-w` | sidebar width, 256px |

## How to add a slash command

Commands are data, not switch cases. The single registry is `SLASH_COMMANDS`
in `app.js`; `/help`, the input router, and the palette all read it.

1. **Add a registry entry** to `SLASH_COMMANDS`:

   ```js
   { name: 'shortcuts', title: 'Keyboard shortcuts',
     desc: 'show every binding', action: () => showShortcuts() },
   ```

   `name` must match `/^\/?([a-z][a-z0-9_-]*)/i` (the router's pattern).

2. **Implement or reference a real handler.** `routeSlashCommand` clears the
   input *before* invoking `action(rest)` when the text came from the input,
   so a stubbed or throwing action silently swallows the user's message —
   unknown names like `/foo` fall through to chat unchanged, but a *registered*
   name with a broken action loses the text with no trace. Every existing
   entry calls a real handler in `app.js` (`openGenome`, `openWorkbench`,
   `applyTheme`, `setSound`, …); keep that invariant. `rest` receives any text
   after the command name for argument-bearing commands.

3. **Nothing else to wire.** `showCommandHelp()` lists the new command for
   `/help`; `attachPalette()` exposes it to the palette through
   `getCommands: () => SLASH_COMMANDS` (pass `description` in the entry if you
   want palette-row text — see the palette contract above); `runCommand`
   accepts either the name string or the command object when other code needs
   to trigger it.
