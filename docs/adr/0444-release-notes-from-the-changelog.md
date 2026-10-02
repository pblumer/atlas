# ADR-0444: The Console shows the CHANGELOG as release notes, read while the server runs

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers
- **Open question:** Whether the people who open the Console in German read English
  release notes, or stop reading them. The curated feed this replaces existed in two
  languages for them; the decision rests on the judgement that the time the second
  language cost on every change is worth more than what it gave, and nobody has asked
  those readers.
- **Question checked:** 2026-10

## Context and problem statement

The Console's landing page carried a **What's New** feed: the newest user-facing
changes, in plain language, in German and English, with tags, an optional tutorial and
a "Try it" link. It was generated from `CHANGELOG.md` by a Go command
(ADR-0375), merged with a curated override file per entry, and the result was
committed as `api/web/whats-new.json`, because ADR-0012 keeps the web UI buildless and
nothing regenerated it at build or run time.

Every part of that was a step somebody had to take, on every change that touched the
changelog:

- **Regenerate and commit.** A changelog bullet without a regenerated feed failed CI,
  in two jobs, on a check none of the ordinary Go commands covered.
- **Curate.** An entry without an override showed its English changelog wording to
  German readers, so each user-facing change owed a bilingual summary in
  `scripts/whats-new/overrides/<id>.json`, named by a slug of its headline. There were
  434 of them, 1.9 MB of prose — twice the size of the changelog they summarised.
- **Merge.** The feed is a function of the changelog, not text git can merge, so it was
  marked `-merge`: every merge of a branch that added a bullet into a main that had
  moved stopped on a conflict in a generated file, resolved by `make whats-new-resolve`.
  GitHub's merge button ignores that attribute, so a workflow on main
  (`whats-new-sync.yml`) regenerated the feed after every push and pushed the
  correction; the numbering workflow regenerated it too, because rewriting a draft
  citation in the changelog made the committed feed stale (ADR-0170's amendment).

The maintainers measured that against what the feed returned and decided it cost more
than it gave. The question is what the landing page shows instead, and how little it
can cost.

ADR-0375 asked the same question from the other side and left it open: whether the
feed should stop being a committed file and be derived at runtime from an embedded
changelog. It refused that because it would have embedded 1.9 MB of source — 640 KB of
changelog and 1.2 MB of overrides — to serve 23 KB, and named the condition under which
the arithmetic would change: if the overrides ever shrank. Dropping the curation takes
them to nothing.

## Decision drivers

- A change that reaches the changelog reaches the Console with **no further step**:
  nothing to generate, translate, commit or resolve.
- **No generated file in the tree**, so nothing for two branches to conflict over and
  nothing for a workflow to repair on main.
- The landing page still says **what changed, per release** — including what to read
  before upgrading, which the curated feed never showed.
- The web UI stays buildless (ADR-0012) and the server stays one binary (ADR-0011).

## Considered options

1. **Release notes read at runtime from the CHANGELOG the binary embeds.**
2. **Keep the generator, drop the curation** — the committed feed, from changelog
   wording alone.
3. **Link out** — no section on the landing page, a link to the changelog or to the
   GitHub release.
4. **Leave it.**

## Decision outcome

Chosen option: **1, release notes read at runtime**, because it is the only option that
removes every per-change step and every generated file while keeping the Console's
account of what changed.

- `CHANGELOG.md` is embedded by the module's root package (`changelog.go`): `go:embed`
  reaches only files at or below the embedding package, and the root is the one
  package the file sits in. The decision records' index, `docs/adr/README.md`, is
  embedded beside it (65 KB): most entries cite a record by its number alone, a
  record's file name cannot be derived from its number, and the index — which
  `go test ./docs/adr` keeps in step with the directory — is where it is written down.
  So a citation links to its record, as the feed's did when the generator read the
  directory.
- `api/releasenotes` parses it once, on first request, into releases — each with its
  introduction (the release's own account of itself and its upgrade notes), its changes
  (category, headline, the rest of the bullet's first paragraph, and a link to the pull
  request or record it is about — the first of the citations it closes with, the
  changelog's convention, or else the first it makes) and a link to its section of the changelog as tagged.
  It owns no state, so it holds no run loop (ADR-0147), like `api/formgen`.
- `GET /api/v1/release-notes` lists the releases with the number of changes each
  carries; `GET /api/v1/release-notes/{version}` answers one. The whole changelog is
  several hundred kilobytes and the landing page opens one release, so the changes come
  a release at a time. Both routes are behind the login, with any role: the page that
  reads them is, and `/api/v1/info` already tells an anonymous visitor the version.
  They are not MCP tools — prose for a person, which `atlas_info` and the repository's
  `CHANGELOG.md` already give an agent.
- The Console's section lists the newest releases, opens the newest, loads an older one
  when it is opened, and renders the three pieces of inline markdown the notes keep
  (`code`, **bold**, *emphasis*) after escaping everything. Its labels follow the
  landing page's language; the notes themselves are English, as the changelog is.
- `go test ./api/releasenotes` holds the parser to the file it is given: no merge
  markers, every version heading a release, every top-level bullet under a category a
  change — counted independently of the parser — dates newest-first, and no headline
  or text that starts with punctuation, the signature of a lead-in left unstripped.
  That replaces the feed's staleness check, which has nothing left to compare.

Removed with the feed: the generator and its overrides (`scripts/whats-new/`),
`api/web/whats-new.json` and its test, `make whats-new` and `make whats-new-resolve`,
the `-merge` attribute, `whats-new-sync.yml`, both CI steps, and the numbering
workflow's regeneration step — which ADR-0170's amendment of 2026-08-24 added, and
which therefore lapses with it.

### Consequences

- **Positive:** a changelog bullet is the whole of the work; a merge of two branches
  that each add one conflicts only where the changelog itself does; the Console shows
  every release rather than the newest twelve entries, with its introduction and its
  upgrade notes; a workflow, three workflow steps, two make targets, a Go package of
  some 700 lines and 1.9 MB of prose leave the tree.
- **Negative / trade-offs accepted:** German readers get English notes — the open
  question above. The notes read like the changelog, which is written for people who
  run and develop Atlas rather than for someone who only uses the Console; there are no
  tutorials and no "Try it" links. The binary carries the changelog, about 1 MB today
  and growing with every release, and the records' index beside it. Links point to
  GitHub, as the feed's did, which an installation without internet access cannot
  follow. The landing page makes two requests where it made one.
- **Follow-ups / risks to watch:**
  - ADR-0375 is superseded in effect. Its status names this record as an amendment,
    because the guard accepts `Superseded by ADR-NNNN` only with a number, and this
    record has none until it lands; once it has, ADR-0375 becomes `Superseded by` it,
    `Implementation: Superseded`.
  - The parser reads the changelog's conventions: `## [version] — date`, `###`
    categories, `- **Headline.** text` bullets. A bullet written another way still
    appears — its first sentence stands in for a headline — but a new convention, a
    deeper heading or a table, would need the parser to learn it. The guard says so by
    failing.
  - If the changelog grows to where embedding it matters, the same parser can run over
    a trimmed copy; nothing in the routes depends on where the text comes from.

## Pros and cons of the options

### Option 1 — read at runtime
- Good: no step beyond the bullet; no generated file; every release shown, with its
  upgrade notes.
- Bad: English only; about 1 MB more binary; the parser lives in the product rather than
  in a script.

### Option 2 — keep the generator, drop the curation
- Good: no translation work; the landing page looks as it did.
- Bad: keeps everything that made the feed expensive to *merge* — the generated file,
  its conflicts, the sync workflow, the CI steps, the numbering step — which is most of
  the time the feed cost, and none of what the curation added.

### Option 3 — link out
- Good: no code at all.
- Bad: the landing page stops saying what changed, and the link leads to GitHub, which
  an installation without internet access cannot reach and a reader without access to
  the repository cannot open.

### Option 4 — leave it
- Good: German readers keep German summaries, and tutorials.
- Bad: every change keeps paying the cost the maintainers decided against.

## Links

- supersedes in effect ADR-0375 — the Go generator, and the open question this answers
- lapses ADR-0170's amendment of 2026-08-24 — the numbering commit no longer carries a feed
- relates to ADR-0012 — the buildless web UI, which a runtime read keeps buildless
- relates to ADR-0011 — the single binary the changelog is embedded in
- relates to ADR-0147 — area services, and why this one holds no run loop
