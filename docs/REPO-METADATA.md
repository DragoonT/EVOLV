# Repo metadata — paste these into GitHub

Everything here is tuned for GitHub search, Explore recommendations and
social cards. Copy-paste, don't think.

## 1. About (repo sidebar, "⚙" next to About)

**Description** (≤ 350 chars):

```
Self-evolving AI workspace — it rewrites its own UI, tools, skills and plugins while it runs, live on screen. Local-first, one Go binary, no API key needed to try. Watch the demo: it grows a Flappy Bird into itself.
```

**Website**: `https://github.com/dxn111/EVOLV#readme` (or your landing page when you have one)

**Topics** (paste into the same ⚙ dialog, comma-separated — 20 is the max, all are real GitHub search terms):

```
ai-agent, self-evolving, self-modifying, agentic-ai, ai-workspace, llm-agent, autonomous-agents, go, golang, websocket, live-coding, generative-ui, code-generation, ai-tools, local-first, ollama, chatbot, developer-tools, terminal, hacktoberfest
```

**Releases**: v0.10.0 already tagged — publish the draft release so the
Release badge lights up (releases rank in search too).

**Social preview** (Settings → General → Social preview): upload
`docs/assets/og.png`. It's 1200×630 and shows the banner + one-liner.

## 2. Where GitHub actually looks

- **About description + topics** → repo search, Explore, topic pages.
  Topics are the biggest lever: `ai-agent`, `self-evolving` and
  `generative-ui` all have active topic pages that recommend new repos.
- **README first screen** → GitHub's semantic code search and Google.
  The banner alt text, the H1 and the first paragraph are already
  keyword-loaded ("self-evolving AI workspace", "rewrites its own UI").
- **Releases + stars velocity in week 1** → Trending. Trending is
  per-day, per-language. The best shot: push a release + README with a
  fresh GIF, then share it — a few dozen stars in 24h puts you on the
  Go trending page, which compounds.

## 3. First-month push plan (10k is viral, 1k is grind — both start the same)

1. **Day 0 (push day)** — push main + tags, publish release, set About +
   topics, upload social preview. Then post the mp4 (not a link, the
   actual video) on:
   - X/Twitter with #BuildInPublic
   - r/LocalLLaMA and r/SideProject — title like *"I built an AI workspace that rewrites its own UI while it runs (Go, local-first)"*
   - Hacker News, Show HN: *"Show HN: Evolv – a self-evolving AI workspace"*
   - Discord servers: Ollama, LangChain, Go community
2. **Week 1** — answer every issue within hours (speed is the star
   multiplier), add a `GOOD FIRST ISSUES` label batch, keep the GIF
   fresh on X every 2–3 days (different scenario each time: flappy,
   self-edit, zen mode).
3. **Week 2–4** — ship one visible release per week (marketplace, voice,
   vision), each with its own GIF; cross-post each release. GitHub
   Topic pages + Trending recency favor repos that ship.
4. **Ongoing** — the tweet-intent link at the bottom of the README does
   real work: every happy user is one click from spreading it.
