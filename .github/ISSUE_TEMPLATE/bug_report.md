---
name: Bug report
about: Something in the organism broke (yes, including "it evolved itself into a corner")
title: ''
labels: bug
assignees: ''
---

**What happened?**

**What did you ask it to do?** (the exact message you sent the agent)

**What did you see instead?** Screenshots or the offending chat text welcome — raw JSON leaks included.

**Environment**
- OS:
- Version (`evolv --version` or the release you installed):
- Provider (Kilo / OpenAI / Anthropic / Ollama / mock):
- Did it involve the agent's own mutations? (components / tools / skin edits)

**Genome evidence** (if relevant)
- Paste the last lines of `~/.evolv/workspace/evolution.jsonl`
- `evolv --reset` restores the factory genome (ledger survives) — did that fix it?
