<div align="center">

<img src="docs/assets/banner.svg" alt="EVOLV — the workspace that rewrites itself. A self-evolving AI workspace: it grows its own UI, tools, skills and prompts live while you watch." width="100%">

[![GitHub stars](https://img.shields.io/github/stars/dxn111/EVOLV?style=flat-square&color=8b5cf6)](https://github.com/dxn111/EVOLV/stargazers)
[![License: MIT](https://img.shields.io/badge/license-MIT-8b93ab?style=flat-square)](LICENSE)
[![Release](https://img.shields.io/github/v/release/dxn111/EVOLV?style=flat-square&color=22d3ee)](https://github.com/dxn111/EVOLV/releases)

**No API key. No signup. One command.**

```bash
curl -fsSL https://raw.githubusercontent.com/dxn111/EVOLV/main/install.sh | sh
```

```bash
$ evolv          # boots on localhost:7373, opens your browser
```

<img src="docs/assets/demo.gif" alt="EVOLV live demo: the AI evolves a pomodoro timer, a system monitor, sticky notes and a word-count tool directly onto its own UI in real time" width="88%">

</div>

---

## What you just watched

That GIF wasn't scripted. You're looking at an AI **rewriting its own application while it runs**:

1. A human asked for a pomodoro timer.
2. The agent didn't paste code back. It **wrote a real component into its own source tree** (`~/.evolv/workspace/ui/components/pomodoro.html`) and logged the change in an append-only evolution ledger.
3. A filesystem watcher caught the write the instant it happened and pushed it over WebSocket — the new panel appeared in the browser **with no refresh, no rebuild, no restart**.

The app you installed is not the app you're running five minutes later.

> Most "AI workspaces" give you a chat window with plugins someone else wrote.
> EVOLV *is* the chat window — and it writes its own plugins, its own UI, and its own mind.

<div align="center"><img src="docs/assets/06_hero.jpg" alt="EVOLV workspace: live canvas with self-authored pomodoro, system monitor, sticky notes and heartbeat components plus the evolution stream timeline" width="92%"></div>

---

## Why this is different

| | Traditional AI chat | Plugin-based workspace | **EVOLV** |
|---|---|---|---|
| UI | fixed | fixed | **self-rewriting, hot-reloaded** |
| Tools | prebuilt | marketplace | **grown at runtime, compiled live** |
| Skills / prompts | frozen | frozen | **self-edited, versioned in a ledger** |
| New feature request | "here's some code you could paste…" | "wait for the dev" | **materialized on your canvas in seconds** |
| Improvement loop | ends when the chat ends | manual | **continuous, visible, alive** |

### It edits itself. It edits YOUR code.

EVOLV is not a walled-garden canvas. It is a **full agent workspace** with the whole machine as its hands:

- **Own body, every byte.** Its chat shell, its CSS skin, its tool call cards, its front-brain JS, its panels — all plain files it rewrites live. Say *"make your tool call cards look exactly like Claude's"* and it opens its own `style.css`, rewrites the card section, reloads its shell — and the new skin lands in under a second, no refresh.
- **Even the launcher.** If the EVOLV source tree is on the machine, it detects it and can read and edit its own Go kernel (`cmd/`, `internal/`) — the code that runs it. Then `go build ./cmd/evolv`. A program that patches its own bootloader.
- **Your projects.** Point it at any folder (*"work on the project at ~/my-app"*, or `set_project`): it reads, edits, runs tests and ships code there like any serious coding agent — while still being the workspace it lives in.

### Time travel: every state of the organism is kept

Before **every mutation batch** the whole genome is frozen into `~/.evolv/snapshots/` (the last 20 states are kept). The agent can freeze and rewind itself mid-conversation:

```
{"action":"snapshot"}                       → "genome frozen — restore anytime"
{"action":"rollback","path":"latest"}       → the whole organism rewinds, live
```

Rolled back a bad evolution and instantly regretted it? Snapshots are also exposed in the UI/API (`/api/snapshots`), so *you* can time-travel the organism too.

### Dream mode: it evolves itself while you're away

Flip on **Dream mode** in Settings (or press the *💭 dream now* button). When the organism has been idle for the heartbeat interval, it wakes up **on its own**, reviews its own body — skills, prompts, tools, its own skin — grows one real improvement, and rolls over back to sleep. You come back to a chat that quietly says:

> 💭 dreamed while you were away. I taught myself deep-work.

Every dream is a real conversation turn, so the organism *remembers its dreams* — and every dream is snapshotted first, so nightmares are one rollback away.

### The full action list

| Action | What it does |
|---|---|
| `evolve_component` | mounts a new live UI panel mid-conversation |
| `evolve_tool` | compiles a JS tool into its sandbox and calls it |
| `evolve_skill` / `evolve_prompt` | grows its own memory and system prompt |
| `evolve_plugin` | installs a sandboxed JS plugin |
| `write_file` / `edit_file` / `read_file` | any file, any path on your machine |
| `list_dir` / `tree` / `move` / `delete_file` | real filesystem surgery |
| `set_project` / `shell` | binds your project root, runs commands (120s timeout) |
| `snapshot` / `rollback` | time travel: freeze the genome, rewind the whole organism |
| `reload_shell` | forces the browser to recompile its UI now |
| `self_update` | morphs itself to the latest released binary mid-chat |

Every mutation is logged to `workspace/evolution.jsonl` and streamed to your screen as it happens. And you can play too: **the body is plain files.** Edit anything in `~/.evolv/workspace/` — it hot-reloads.

---

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/dxn111/EVOLV/main/install.sh | sh
```

The installer detects your OS/arch, downloads the prebuilt binary (or builds from source if needed), puts `evolv` on your PATH, and prints a launch card.

<details>
<summary><b>Other ways in</b></summary>

```bash
# with Go toolchain
go install github.com/dxn111/EVOLV/evolv@latest

# from source
git clone https://github.com/dxn111/EVOLV
cd EVOLV && make install

# grab a binary from Releases
# macOS / Linux / Windows (amd64 + arm64) — see github.com/dxn111/EVOLV/releases
```
</details>

Then:

```bash
evolv             # → http://localhost:7373 opens in your browser automatically
evolv update      # morph in-place to the latest release (genome survives)
evolv --port 8080 # custom port
evolv --no-open   # headless / ssh
evolv --reset     # restore the factory genome (your evolution ledger + snapshots survive)
```

<div align="center"><img src="docs/assets/07_settings.jpg" alt="EVOLV settings drawer: provider picker with free anonymous Kilo Gateway, BYOK fields for OpenAI, Anthropic, OpenRouter, Groq, Ollama and custom endpoints" width="92%"></div>

---

## Models: free by default, BYOK when you want power

EVOLV ships with the **[Kilo Gateway](https://kilo.ai/gateway)** as its default brain — **anonymous, IP-based free access, zero signup** (200 requests/hour). You download, it thinks.

| Provider | Key needed | Notes |
|---|---|---|
| **Kilo Gateway** *(default)* | none | `kilo-auto/free` rotates the live free-model pool for you |
| Kilo Gateway + BYOK | kilo | unlocks `kilo-auto/frontier`, Claude, GPT, GLM, Kimi, Qwen, Grok, DeepSeek… |
| OpenAI | openai | any OpenAI-compatible endpoint via custom base URL |
| Anthropic | anthropic | native Messages API |
| OpenRouter | openrouter | hundreds of models, one key |
| Groq | groq | absurdly fast |
| Ollama | none (local) | your machine, your weights, your rules |
| Custom | custom | any OpenAI-compatible URL |
| Mock | none | offline evolution demo — mutates with no brain at all |

Change it any time in **Settings → Provider** — the swap is hot, mid-conversation.

> Free anonymous models are small and inconsistent. Great for watching evolution happen; plug in a frontier model via BYOK when you want serious work.

---

## The evolv protocol

Self-modification works on **every model** (even tiny free ones) because it's a plain-text protocol, not a vendor-specific function-calling API. The agent emits:

````markdown
```evolv
{"action":"evolve_component","name":"clock","title":"Clock","icon":"🕰","description":"A live clock","html":"<div id='c-clock'>…</div><script>…</script>"}
{"action":"evolve_tool","name":"dice","description":"Roll a die","code":"function dice(args){return {roll:Math.ceil(Math.random()*((args&&args.sides)||6))}}"}
```
````

The kernel parses it out of the stream, executes each action against the living filesystem, broadcasts the mutation to every connected browser, feeds the results back to the model, and loops — so the agent can **verify its own work and continue**.

### Genome layout

```
~/.evolv/
├── config.json                  # kernel config (provider, keys, project root, port…)
├── logs/
└── workspace/
    ├── ui/                      # the ENTIRE frontend — served & hot-reloaded
    │   ├── index.html           #   the agent can rewrite its own shell
    │   ├── app.js               #   (you'll see a "genome recompiling" flash)
    │   ├── style.css            #   …and its own CSS
    │   └── components/          # hot-mounted panels (one .html file each)
    ├── tools/                   # JS tools the agent grew (goja sandbox)
    ├── skills/                  # markdown skills → part of its mind
    ├── prompts/                 # markdown fragments → appended to its system prompt
    ├── plugins/                 # plugin.json + plugin.js, sandboxed
    ├── evolution.jsonl          # append-only ledger of every mutation, ever
    └── ../snapshots/            # frozen genome states (auto, before every mutation batch)
```

> Wait — `snapshots/` sits at `~/.evolv/snapshots/`, one level above `workspace/`. It holds the last 20 complete genome states, newest first.

### Under the hood

```
┌────────────┐   chat    ┌───────────────┐   evolv blocks   ┌──────────────────┐
│  Browser   │◄─────────►│  evolv kernel  │◄────────────────►│  LLM (Kilo/BYOK)  │
│ (canvas +  │           │  agent loop    │    plain text     └──────────────────┘
│  stream)   │  WebSocket│  + goja sandbox│
└────────────┘    ▲      └───────┬────────┘
                  │              │ writes
                  │ mutations    ▼
                  │      ┌───────────────┐
                  └──────│  file watcher │  ~/.evolv/workspace/**
                         └───────────────┘  (150ms poll, instant feel)
```

- **Single static binary** (Go + embedded genome) — no runtime deps, no node_modules, no docker
- **goja** (pure-Go JS interpreter) executes the tools/plugins the agent writes — no cgo, cross-compiles to every platform
- **150ms filesystem watcher** + WebSocket fan-out = "did it just…? yes it did"
- Everything local. Nothing phones home except the model provider you choose.

---

## It updates itself too

The kernel polls GitHub Releases at boot and every 6 hours. When a newer version exists, a **live banner drops into every open browser** — a program telling you it can become a better version of itself, mid-session:

> 🧬 a newer organism exists: v0.3.0 — **[morph now]**

Click *morph now* (or run `evolv update`) and the latest binary is downloaded and swapped over the running one. Your genome — every evolution it ever made — survives untouched. Restart once and the new body is live.

---

## Security (read this)

EVOLV is a self-modifying agent running on **your** machine — that's the entire point, and it comes with real teeth:

- The agent can run **shell commands** and read/write **any file on the machine** — that's what makes it a real workspace, not a toy. It binds to `127.0.0.1`, and the browser UI only exposes what the kernel serves.
- Free anonymous models may route through providers that log prompts — **don't paste secrets into demo-tier chats.**
- `evolv --reset` restores the factory genome if an evolution goes feral.

Treat it like a very eager intern with root: brilliant, fun, supervise accordingly.

---

## Roadmap

- [x] Ambient heartbeat — **shipped in v0.4.0 as Dream Mode** (idle → autonomous self-evolution)
- [x] Evolution branching — **shipped in v0.4.0 as snapshots + rollback** (time travel through genome states)
- [ ] Component marketplace — export / import genomes as `.evolvpack` files
- [ ] Multi-organism mode — agents that evolve together
- [ ] Vision — components that can see (canvas screenshots as tool input)
- [ ] Voice

## FAQ

<details>
<summary><b>Is this just Electron + eval?</b></summary>
No. It's one Go binary serving its own genome; the "eval" is a properly sandboxed goja runtime for agent-written tools, and UI components are plain files you can audit, edit, or delete at any time.
</details>

<details>
<summary><b>What if the AI breaks its own UI?</b></summary>
It happens sometimes — that's evolution. The factory genome is embedded in the binary; <code>evolv --reset</code> instantly restores it while keeping your evolution ledger. You can also fix it yourself: the UI is plain files.
</details>

<details>
<summary><b>Does it need the internet?</b></summary>
Only for models. With Ollama you get a fully local, offline self-evolving workspace. With the mock provider it evolves with zero brain at all (great for screenshots and parties).
</details>

<details>
<summary><b>Why "EVOLV"?</b></summary>
Because it does. Literally. You watched it happen in the GIF.
</details>

---

## Contribute

The most fun repo on your machine to hack on:

```bash
git clone https://github.com/dxn111/EVOLV && cd EVOLV
make build && ./evolv
```

Ideas that would be amazing: new protocol actions, a genome inspector UI, evolution replay/branching, security linting for evolved components, ports to new platforms. Open an issue or ping me directly — **I answer everything.**

## Support & ideas

- **dxn1.0day@gmail.com** · Discord **@dxn1_ai**

If EVOLV made you say "wait, WHAT?" — a star genuinely helps other people find it.

<details>
<summary><b>About / keywords</b></summary>
EVOLV — self-evolving AI workspace. AI that rewrites its own UI, tools and prompts live. A self-modifying agent and full coding workspace that works on your projects. Local-first, single Go binary, BYOK, free AI models via the Kilo Gateway. Think opencode or cursor, but the app itself is the thing that evolves.
</details>

<div align="center">

[![Star History Chart](https://api.star-history.com/svg?repos=dxn111/EVOLV&type=Date)](https://star-history.com/#dxn111/EVOLV&Date)

</div>

## License

MIT © [dxn111](https://github.com/dxn111)
