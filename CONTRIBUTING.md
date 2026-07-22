# Contributing to Atlas

Thanks for your interest in Atlas. The project is in early development, so the most valuable contributions right now are discussion, design feedback, and help landing the Milestone 0/1 foundations (see the [roadmap](ROADMAP.md)).

## Before you start

- Read [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) and the relevant deep-dives in [`docs/architecture/`](docs/architecture/).
- Skim the [ADRs](docs/adr/). They explain *why* things are the way they are. If your change contradicts an ADR, that's fine — but it means writing a new ADR that supersedes it, not quietly diverging.
- For anything non-trivial, open an issue first to align on approach before writing code.

## Architectural guardrails

Atlas's performance and correctness rest on a few load-bearing decisions, captured as **invariants** in [`docs/architecture/invariants.md`](docs/architecture/invariants.md). Changes that violate these need an ADR, not just a PR:

1. **No allocation on the hot path.** Pool records, reuse buffers, prefer value types. The processor batch cycle must not allocate per command.
2. **Durable before visible.** The ordering is fsync → state commit → side effects. Never expose or act on an event that isn't on disk.
3. **Single writer per partition.** No locks on process state; no cross-partition shared mutation. Cross-partition interaction is async messaging only.
4. **One `applyToState`.** State mutation lives in exactly one function, used identically live and on recovery. Don't fork that logic.
5. **Compile, don't interpret.** Work that can happen at deploy time (parsing, validation, interning, expression compilation) must not happen on the hot path.

## Development

Atlas is developed **test-first** — TDD is the default workflow, not an aspiration ([ADR-0018](docs/adr/0018-test-driven-development.md)):

1. **Red** — write a test that states the behavior you want and watch it fail for the right reason. Anything that emits events gets a recovery/replay test up front; a bug fix starts with a failing regression test.
2. **Green** — write the minimum code to make it pass.
3. **Refactor** — clean up with the test holding the line.

Narrow, *stated* exceptions: purely mechanical changes with no behavioral surface (renames, docs, gofmt, dependency bumps), and throwaway spikes that are re-done test-first before merge. If you skip tests, say why in the PR.

```bash
go build ./...
go test ./...
go test -race ./...      # the race detector is mandatory before pushing
go test -cover ./...     # repository-wide statement coverage must stay >= 95%
go vet ./...
gofmt -l .               # must be empty
```

CI runs build, test, `-race`, vet, formatting, and the 95% coverage floor. Please run them locally first.

## Coding conventions

- Standard Go style; `gofmt` is non-negotiable.
- Keep the hot path allocation-free; if you must allocate, justify it in the PR.
- Public APIs get doc comments; non-obvious internal logic gets a *why* comment (not a *what* comment).
- Prefer integer indices and interned strings over runtime string handling in engine code.
- Tests for new behavior. Recovery-sensitive changes need a crash/replay test.

## Commit and PR hygiene

- Small, focused PRs over large ones.
- Reference the issue and, where relevant, the ADR or roadmap milestone.
- Describe the *why*, not just the *what*, in the PR description.
- A green CI (including `-race`) is required to merge.

## Writing an ADR

If you're making or changing a significant architectural decision:

1. Copy [`docs/adr/template.md`](docs/adr/template.md) to the next number.
2. Fill in context, drivers, options, and consequences honestly — including the trade-offs you're accepting.
3. Add it to the table in [`docs/adr/README.md`](docs/adr/README.md).
4. If it replaces an earlier decision, mark the old ADR *Superseded by ADR-XXXX* (don't delete it).

## Reporting bugs and security issues

- Functional bugs: open an issue with a minimal reproduction (ideally a small BPMN model + the command sequence).
- Security issues: please follow [`SECURITY.md`](SECURITY.md) — do **not** open a public issue.

## License and DCO

By contributing, you agree your contributions are licensed under the project's license ([Apache 2.0](LICENSE)). Sign off your commits (`git commit -s`) to certify the [Developer Certificate of Origin](https://developercertificate.org/).
