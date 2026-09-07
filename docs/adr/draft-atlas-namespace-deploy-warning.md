# ADR-DRAFT: The engine stays namespace-blind, and the deploy says so

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

The compiler reads a worker extension by its **local name only**:

```go
Discord *xmlDiscordConnector `xml:"extensionElements>discordConnector"`
```

Go's `encoding/xml` matches that tag against the element name and ignores the namespace.
So a model that binds the `atlas` prefix to the wrong URI compiles, deploys, runs, and
recovers exactly like a correct one:

```xml
<bpmn:definitions xmlns:atlas="http://atlas.dev/schema/1.0">   <!-- wrong -->
  <atlas:discordConnector connector="discord_pblumer" operation="send-message" .../>
```

The Modeler is not blind in the same way. `api/web/atlas-moddle.json` declares

```json
{ "name": "Atlas", "prefix": "atlas", "uri": "http://atlas/schema/1.0" }
```

and bpmn-moddle resolves `atlas:*` against that one URI. An element outside it is not an
Atlas element to the Modeler at all: it is parsed into an unknown namespace and then
**fails to serialize**, with

```
save failed: no namespace uri given for prefix <ns0>
```

Importing appears to work. Every Save, Deploy and Auto-layout afterwards errors.

This is not hypothetical, and it is not only a repository-hygiene problem.
[ADR-0258](0258-discord-worker.md)'s two example processes were authored by hand against
a live server with `http://atlas.dev/schema/1.0`. They deployed, they compiled, they
produced jobs, they raised and cleared incidents — for over an hour — and the mistake
surfaced only when their author opened one in the Modeler and pressed Save.

The repository already knew about this failure. `examples/models_test.go` carries
`TestAtlasExtensionsUseTheModdleNamespace`, which walks the `.bpmn` files under
`examples/`, `conformance/models/` and `postman/` and fails the build on exactly this
mistake, deriving the namespace and the element names from the same moddle. That guard
is why every model Atlas *ships* is bound correctly.

It has one blind spot, and it is the population that matters most: **it can only see
files in the repository.** A model that arrives over `POST /api/v1/deployments` — the
Modeler, the CLI, an MCP client, a hand-written file — is never walked by any test.

The Go test corpus showed how the wrong spelling spreads. At the time of writing, 112
inline BPMN fixtures across the repository declared `http://atlas.dev/schema/1.0` and 16
declared the canonical `http://atlas/schema/1.0`. None of those fixtures ever reaches a
Modeler, so none of them was ever *wrong* in a way anything could notice — but they are
what somebody copies when they write a model by hand. That is, in all likelihood,
exactly how the Discord example got its namespace.

## Decision

**Keep the engine lenient. Warn at deploy. Derive both from the moddle.**

1. **The parser does not tighten.** Matching on the local name stays. A deployed
   definition is recompiled from its stored XML on recovery, so a parser that rejected a
   stray namespace would turn every already-deployed model into one that no longer
   loads. Invariant I5 draws this line already: validation gates *deploying* a model,
   not *running* one, which is why `ReloadNamed` compiles without the gate.

2. **The deploy warns.** `foreignAtlasNamespaceWarnings` walks the submitted bytes and
   returns one sentence per offending namespace, listing the elements found in it, and
   naming the edit that fixes it. It is appended to `deployResp.Warnings` — the same
   channel as an unconfigured worker reference ([ADR-0158](0158-a-connector-reference-that-explains-itself.md)),
   for the same reason: the deploy is the moment somebody is looking.

3. **The deploy still succeeds.** The model runs. Refusing it would break every
   deployment pipeline carrying a model that has always worked, to prevent an editing
   problem the author may not even have.

4. **The check is derived, not restated.** It reads `web/atlas-moddle.json` out of the
   embedded FS and takes two facts from it: the `uri` is the canonical namespace, and
   the `types[].name` list — first letter lowercased, per the moddle's own
   `"tagAlias": "lowerCase"` — is the set of Atlas element names. Add a type to the
   moddle and the check covers it. Rename the moddle's `uri` and the check follows.
   There is no list to keep in step, which is the property that matters: the question
   the warning answers is *"will the Modeler read this back"*, so it is answered by
   reading what the Modeler reads.

The repository's own inline BPMN fixtures were normalized to the canonical namespace in
the same change, so the corpus stops teaching the wrong spelling.

## Considered and rejected

**Refuse the deploy (`SeverityError`).** The codebase's own definition of that severity
is a problem that "makes the model unrunnable or structurally invalid". A stray namespace
makes a model neither; it runs correctly. Refusing would also be a breaking change for
any pipeline holding a hand-authored model that has deployed for its whole life.

**A `compiler.Problem`, so the Problems panel shows it too.** `Validate` operates on a
linearized `CompiledProcess`, by which point the namespace is gone. Surfacing it there
means carrying the finding from `decodeDefinitions` through `compileProcess` into
`CompiledProcess`, and `decodeDefinitions` is shared with the *reload* path — the finding
would then have to be suppressed there to avoid re-reporting it on every recovery. Worth
doing if the panel is asked for; not worth the plumbing for the deploy warning alone.

**A guard over the Go sources**, asserting every `xmlns:atlas="…"` literal in the
repository is canonical. This would stop the fixtures drifting again, and it was worked
through far enough to find what sinks it: it needs an exemption list.
`api/infomodel/import_test.go` and `api/formgen/outline_test.go` bind `atlas` to *UML
profile* namespaces in XMI documents, which is entirely legitimate and has nothing to do
with BPMN; and the warning's own message-building line contains the literal.
Distinguishing them requires guessing from the URI's shape. An exemption list that must
be kept true is the failure mode this record exists to avoid — and the runtime check
needs none, because it works on resolved namespaces of known element names rather than
on text.

**Teaching bpmn-moddle both URIs.** It would make the wrong spelling permanently valid
and double the surface every future reader has to know about.

## Consequences

- A model bound to a foreign namespace deploys, runs, and now says so, once, at the
  moment it is deployed, in a sentence that names the elements and the fix.
- The engine's leniency is now a *documented* decision rather than an accident of how
  `encoding/xml` matches tags. It has to stay lenient: recovery depends on it.
- Two implementations of the same walk now exist — this one and
  `misboundAtlasExtensions` in `examples/models_test.go`. They cannot disagree about the
  facts, because both read the same moddle; they differ only in what they do with a
  finding (fail a build vs. word a warning). Unifying them would mean exporting a
  detector from `api` for a test in another package to call, which widens the public
  surface for no runtime benefit. If a third caller appears, that trade changes.
- The check runs once per deploy over bytes already in memory, on a path that is not the
  hot path (invariant I1 is untouched).

## References

- [ADR-0158](0158-a-connector-reference-that-explains-itself.md) — deploy warnings: told now, not by the first token to park
- [ADR-0258](0258-discord-worker.md) — the Discord Worker Type, whose example models exposed this
- [`docs/architecture/invariants.md`](../architecture/invariants.md) — I5: compile, don't interpret
- `examples/models_test.go` — the three CI-time namespace guards this complements
