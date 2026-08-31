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
| Test + race (required) | `make race` | `go test -race -timeout=25m ./...` |
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

Atlas is a **`0.x` developer preview**: a broad slice of BPMN 2.x runs durably on
a single node, but the pre-1.0 API and on-disk formats are unstable. The
[`ROADMAP.md`](ROADMAP.md) is organized by milestone and marks what's done (✅),
in progress (🚧), and not started (🔲); the [`CHANGELOG.md`](CHANGELOG.md) records
what shipped in each release. Pick work from the current milestone, and check the
[layout](docs/ARCHITECTURE.md#component-map) — not every package exists yet.

## Where things live

```
.github/workflows/   CI, e2e, Pages, and the tag-driven release build
docs/                architecture, ADRs, glossary, invariants
AGENTS.md            agent operating guide (read first)
CLAUDE.md            pointer to AGENTS.md for Claude Code
Makefile             canonical commands
ROADMAP.md           milestones
CHANGELOG.md         per-release notes
```

## Making architectural changes

If you change a decision recorded in an [ADR](docs/adr/), don't edit the old ADR — write a new one (copy [`docs/adr/template.md`](docs/adr/template.md)), mark the old one *Superseded*, and update [`docs/adr/README.md`](docs/adr/README.md). This keeps the decision history intact.

## Cutting a release

Releases are driven entirely by a git tag — there is no manual binary building.

1. Move the `## [Unreleased]` notes in [`CHANGELOG.md`](CHANGELOG.md) under a new
   `## [x.y.z]` heading with today's date, and update the compare links at the
   bottom.
2. Make sure the full check sequence is green on `main` (`go build ./...`,
   `go test -race -timeout=25m ./...`, `go vet ./...`, empty `gofmt -l .`).
3. Tag and push:

   ```bash
   git tag -a v0.1.0 -m "Atlas v0.1.0"
   git push origin v0.1.0
   ```

The [`release` workflow](.github/workflows/release.yml) then cross-compiles the
single binary for linux (amd64, arm64, 32-bit arm/v6), macOS (amd64, arm64), and
windows (amd64), stamps the tag into the
version string via `-ldflags` (so `atlas version` reports it), and publishes a
GitHub Release with the archives and a `SHA256SUMS` file. Every `0.x` tag is
marked a prerelease. No CGO is involved, so all targets build from the one Linux
runner.
