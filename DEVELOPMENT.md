# Development

Setup and workflow details for working on Atlas. For the architectural rules you must follow, see [`AGENTS.md`](AGENTS.md) and [`docs/architecture/invariants.md`](docs/architecture/invariants.md).

## Prerequisites

- **Go 1.26 or newer.** Check with `go version`.
- **No CGO toolchain needed.** The core is pure Go by policy ([ADR-0010](docs/adr/0010-go-and-no-cgo.md)); `CGO_ENABLED=0` should always build.
- Optional: `golangci-lint` for the `make lint` target.

## Getting started

```bash
git clone https://github.com/pblumer/atlas.git
cd atlas
make check        # build + vet + format check + race tests
```

`make check` is the full gate — the same checks CI runs. A change is "done" only when it passes.

## Common commands

All canonical commands live in the [`Makefile`](Makefile):

| Task | Make target | Raw command |
|------|-------------|-------------|
| Build | `make build` | `go build ./...` |
| Test | `make test` | `go test ./...` |
| Test + race (required) | `make race` | `go test -race ./...` |
| Vet | `make vet` | `go vet ./...` |
| Format | `make fmt` | `gofmt -w .` |
| Format check | `make fmt-check` | `gofmt -l .` (must be empty) |
| Full gate | `make check` | build + vet + fmt-check + race |
| Tidy modules | `make tidy` | `go mod tidy` |

Run a single package or test:

```bash
go test ./engine/...
go test ./engine/ -run TestProcessorRecovery -v
```

## Testing philosophy

Atlas's correctness hinges on a few properties that ordinary unit tests don't automatically cover. See [`AGENTS.md`](AGENTS.md#testing-conventions) for the full list. The two most important:

- **Recovery tests.** The core invariant is "state built live == state rebuilt by replaying the log." Anything that emits events should have a test that processes commands, simulates a restart, replays, and asserts equality.
- **Determinism.** No dependence on wall-clock time or goroutine scheduling. Inject the `Clock`; drive the processor synchronously where possible.

For processor-path changes, check allocations:

```bash
go test ./engine/ -run XXX -bench BenchmarkProcessor -benchmem
```

and consider `testing.AllocsPerRun` to guard the no-allocation-on-the-hot-path invariant.

## Project status

The project is at **Milestone 0** (foundations). Many packages in the [layout](docs/ARCHITECTURE.md#component-map) don't exist yet — check before assuming. The [`ROADMAP.md`](ROADMAP.md) is organized by milestone; pick work from the current one.

## Where things live

```
.github/workflows/   CI
docs/                architecture, ADRs, glossary, invariants
AGENTS.md            agent operating guide (read first)
CLAUDE.md            pointer to AGENTS.md for Claude Code
Makefile             canonical commands
ROADMAP.md           milestones
```

## Making architectural changes

If you change a decision recorded in an [ADR](docs/adr/), don't edit the old ADR — write a new one (copy [`docs/adr/template.md`](docs/adr/template.md)), mark the old one *Superseded*, and update [`docs/adr/README.md`](docs/adr/README.md). This keeps the decision history intact.
