# Contributing to EVOLV

The best contribution is the wildest one. Some ideas, roughly in order of fun:

1. **New protocol actions** — teach the organism a new way to rewrite itself (`internal/agent/actions.go`).
2. **Components** — evolve something beautiful in your own workspace and donate the seed (`assets/seeds/ui/components/`).
3. **Evolution UI** — a genome inspector, diff viewer, or time-travel through `evolution.jsonl`.
4. **Providers** — more model backends are always welcome (`internal/providers/`).
5. **Docs, screenshots, demos** — if you made EVOLV do something ridiculous, we want the GIF.

## Ground rules

- `make build` must pass; keep it pure Go (no cgo) so we cross-compile everywhere.
- Evolved components must be self-contained single files.
- Security-sensitive changes (sandbox, shell, path handling) get extra scrutiny.
- Be kind, be weird, be useful.

Issues and PRs: github.com/dxn111/EVOLV · email dxn1.0day@gmail.com · Discord @dxn1_ai
