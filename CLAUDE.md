# CLAUDE.md

This file is for Claude Code and other Anthropic agents.

**Read [`AGENTS.md`](AGENTS.md) first — it is the canonical operating guide for this repository.** It contains the architectural invariants you must not break, the exact build/test commands, the repository layout, and how to approach a task.

Do not duplicate guidance here; `AGENTS.md` is the single source of truth so it can't drift out of sync. The most important points, repeated only so they are impossible to miss:

- **Run `go test -race ./...` and `gofmt -l .` before considering any change done.** A change isn't complete until the full check sequence in `AGENTS.md` is green.
- **Honor the six invariants** in `AGENTS.md` / [`docs/architecture/invariants.md`](docs/architecture/invariants.md). If a task seems to require breaking one, stop and say so rather than working around it.
- **`applyToState` runs both live and on recovery** — keep it deterministic and side-effect-free.
- **When in doubt about *why*, read the relevant [ADR](docs/adr/).**
