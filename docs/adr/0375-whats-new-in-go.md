# ADR-0375: The feed generator is Go, so the Go checks stop needing Node

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-16
- **Open question:** Whether the feed should stop being a committed file at all —
  derived at runtime from an embedded CHANGELOG instead. It was measured and
  refused: deriving it would mean embedding **1.9 MB** of source (a 640 KB
  CHANGELOG and 1.2 MB of overrides) to produce **23 KB** of feed, and the
  CHANGELOG would go on conflicting anyway, because it is the source. The
  arithmetic would change if the overrides ever shrank or the cap ever grew to the
  point where most of what is embedded is actually served.
- **Question checked:** 2026-09

## Context and problem statement

The Console's "What's New" feed is generated from `CHANGELOG.md` and committed,
because ADR-0012 keeps the web UI buildless: nothing regenerates it at build or run
time. That decision is unchanged and right.

What it cost was not visible in it. The generator was a Node script, and CI
regenerates the feed to check the commit is current — so **four Go jobs carried a
JavaScript toolchain for one step**: `build · vet · fmt · race · cover`, the `docs`
job, the ADR-numbering workflow and the feed-sync workflow each installed Node to
run one command.

ADR-0012's own driver reads:

> `go build ./...` must remain the whole story for the server. A front-end
> toolchain (npm, bundlers) must not become a prerequisite for building or testing
> Atlas in CI.

The feed generator was that prerequisite, in the job that decides whether a change
is good.

## Decision drivers

- The main check job should need one toolchain, and it should be the one the
  product is written in.
- The output is a committed file CI regenerates and diffs, so a port that produced
  *equivalent* JSON would fail that check on every run. It has to produce the same
  bytes.
- 259 curated override files are named by the id the generator derives. A port that
  slugified differently would orphan them, and an orphaned override renders from
  the CHANGELOG's own wording — indistinguishable from one nobody wrote.

## Considered options

1. **Port the generator to Go**, keeping the committed file.
2. **Derive the feed at runtime** from an embedded CHANGELOG, and stop committing
   it.
3. **Leave it.**

## Decision outcome

**Port it.** The command is `go run ./scripts/whats-new`; the rules live in the
`feed` package beside it, so the guards call them directly instead of starting a
process and reading what it printed.

Node now appears in exactly two places, and in both it is the technology rather
than an accident: the browser end-to-end suite (`npm ci`, Chromium, `npm test`) and
the screenshot capture.

### Byte-compatibility is the contract, and it was verified against the original

Two defaults in Go point the wrong way and both had to be turned around:

- **HTML escaping.** Go's encoder writes `<`, `>` and `&` as `<` and friends;
  JavaScript's does not. Left alone it would have changed thousands of bytes of
  prose.
- **Key order.** `JSON.stringify` writes an object's keys in insertion order and Go
  writes a struct's in declaration order. The types therefore declare their fields
  in the order the JavaScript literals did, and a field moved for tidiness would
  change every byte after it.

Verified rather than assumed: both implementations were run over the same tree with
the entry cap lifted, and all **418** entries — every bullet in a 9,310-line
CHANGELOG — came out byte-identical.

### One rule is new, and it was earned

The original ignored keys it did not know. An override carried `route` at the top
level instead of inside `try`, so that entry's deep link did nothing and nothing
anywhere said so. That is the same failure the orphan check already guards one
level up: **a key that does nothing is indistinguishable from a key nobody wrote.**
So unknown keys are refused, and the one file that had one was corrected — it gains
the "Try it" link it was always meant to have.

### Consequences

- **Positive:** the main check job, the docs job and two workflows lost their Node
  setup; the guards test the rules directly instead of shelling out, so they no
  longer skip when node is absent; a class of silent override mistake is now a
  refusal.
- **Negative / trade-offs accepted:** the rules are in Go and no longer readable by
  anybody who only knows the web side; `scripts/` gains a Go package, which it did
  not have before.
- **Follow-ups / risks to watch:** the slug rule is load-bearing for 259 file names
  and is now expressed twice in the repository's history rather than once in a
  script. It is held by a test that spells out the cases, including the one where a
  dash survives the 60-character cut.

## Pros and cons of the options

### Option 1 — port it
- Good: one toolchain in the job that matters; guards call the code; same bytes.
- Bad: a Go package under `scripts/`; the rules move out of the language the rest
  of the web tooling is written in.

### Option 2 — derive at runtime
- Good: no generated file in the tree at all.
- Bad: 1.9 MB embedded to serve 23 KB, a markdown parser at startup, and the
  CHANGELOG still conflicts. It also reopens ADR-0012's buildless rule from the
  other side.

### Option 3 — leave it
- Good: nothing to undo.
- Bad: the check that decides whether a change is good keeps installing a toolchain
  for one command, which is the thing ADR-0012 said it would not do.

## Links

- relates to ADR-0012 — the buildless web UI, and the driver this serves
- relates to ADR-0011 — the single binary the feed is embedded in
