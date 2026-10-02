# ADR-0437: A package is imported from the command line

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers

## Context and problem statement

An example that ships a shop, such as `examples/verwaltung-dienstleistungen`, needs three
steps to install:

1. its processes and forms go into an application;
2. the application is published, so the processes are deployed;
3. the catalogue document is imported
   ([ADR-0436](0436-a-catalogue-is-imported-as-one-document.md)).
   Its products are bound to the processes step 2 deployed.

The shop handbook's installer takes these steps in a browser. Nothing else did. A CI job
that equips a test and a production installation alike, or an operator at a terminal, had to
reproduce the sequence with `curl`. That meant one request per form and per draft, placeholder
substitution by hand, and no check that an answer named anything on the server.

Two further problems sat in the installer itself:

- **The questions lived in the page.** What each `{{placeholder}}` asks was a table in the
  page's JavaScript. That table belongs to the package, and only a browser could read it.
- **Answers were ids.** Ids are minted per server. A group chosen from a list on the test
  installation has a different id on production, so a recorded answer did not carry over.

## Decision drivers

- **No browser needed.** A package installs from a terminal and from a pipeline, and the
  same command run again converges instead of duplicating.
- **Nothing new on the server.** The command uses the routes and the authority the Console
  and the installer already use. It gains no power they lack.
- **Refuse before writing.** Whatever can be found wrong without writing is found first, and
  all of it at once.
- **One source for the questions.** The page and the command ask the same questions, from
  the package.
- **Answers that travel.** One answers file serves every installation.

## Considered options

1. **A CLI command over the existing routes:** `atlas import DIR`.
2. **One server route that takes the whole package** and installs it in one request.
3. **A shell script in the repository** that makes the requests with `curl`.

## Decision outcome

Chosen: **option 1.**

**A package is a directory** in the source layout an application export already produces
([ADR-0134](0134-git-backed-applications.md)), plus a shop:

| File | Content |
|---|---|
| `atlas.json` | the application's manifest: format version, key, name, and the path of every process and form |
| the files it names | the processes (`.bpmn`) and forms (`.form.json`), verbatim |
| `katalog.json` (optional) | the catalogue document |
| `fragen.json` (optional) | per placeholder: `kind` (`group`, `user` or `text`), and `label` and `hint` in each language |

A source export becomes a package once a catalogue is added. A package is read by the source
import unchanged.

**`atlas import [flags] DIR`** (`cmd/atlas/importpkg.go`) takes the installer's steps in its
order:

1. Packs `atlas.json` and the files it names into the gzip tar that
   `POST /api/v1/applications/source` reads. The application is found by its key or created.
   Artifacts of the application that the package does not name stay and are listed.
2. Publishes the application with `POST /api/v1/applications/{id}/publish`. A publish the
   server declines (`deployed: false`) is a failure here, with the server's reason. Going on
   would only have the catalogue refused a step later, further from the cause.
3. Imports `katalog.json`, with every placeholder answered, through
   `POST /api/v1/catalogs/import`.

**Answers** come from `--answers FILE` (a JSON object) and from repeated `--set name=value`.
`--set` wins, so a pipeline keeps the shared answers in a file and overrides one per
installation.

An answer to a `group` or `user` question must be in the server's directory
(`GET /api/v1/principals`), given by its **id or its name**:

- Names are accepted because they stay the same across installations while ids do not.
- A name two principals share is refused and the matching ids are listed. Group names are
  unique on a server; display names of people are not.
- An answer the directory does not have is refused. An audience group that does not exist
  reaches nobody, and the import would otherwise succeed without saying so.

A package with a shop is refused before step 1 on a server whose catalogue is switched off
([ADR-0434](0434-the-catalogue-can-be-switched-off.md)): `GET /api/v1/info` says so, and the
application is not published for a shop that cannot follow it.

Every unanswered or unresolvable question is reported at once, **before step 1**, so a
refused run leaves the server as it found it. An answer is inserted as the inside of a JSON
string, so it cannot break the document. An answer the package does not ask for is noted,
not refused, so one answers file can serve several packages.

**The questions moved into the package.** The shop handbook's installer reads them from
`examples-catalog.json`, into which the examples generator copies `fragen.json`. The page no
longer carries a table of its own.

Two guards in `examples/shopcatalog_test.go` back this:

- `TestEveryPlaceholderIsAsked` holds `fragen.json` to the placeholders in both directions,
  with labels in both languages.
- `TestEveryPackageManifestNamesWhatItShips` holds `atlas.json` to the directory: every
  `.bpmn` and every form, each under the id its file carries. The source import writes what
  the manifest lists under the manifest's ids and opens no file to check them. A process the
  manifest forgets would never be deployed, and one listed under the wrong id would be a draft
  the catalogue's binding cannot find.

### Why not the others

**Option 2** is the strongest alternative. One request could check and write everything
together, and a refused package would leave nothing behind even after the publish.

But the three steps write three different kinds of state under two different roles:
- design-time drafts and forms, under `modeler` and an editor's level on the application;
- deployments, which are engine records;
- catalogues, under `productmanager`.

A deployment is in the event log and is not undone by a later refusal. A route that promised
"all or nothing" across it would promise what the engine cannot keep. What the route could
honestly offer — check first, then take the steps in order — the command offers too, and
each step on its own is safe to repeat. The single route would also be a fourth way to write
each of these stores, beside the three routes it duplicated.

**Option 3** needs no Go code and shows every request in plain sight. But:

- Placeholder substitution and JSON escaping in shell break on the first answer with a quote
  in it.
- Resolving names against the directory would need `jq`.
- The script would not run on the Windows installations the CI covers.
- Unlike a command, it could not be tested against a real server in `go test`, which is how
  `cmd/atlas/importpkg_test.go` proves the example installs.

**Interactive prompting** for missing answers was set aside too. A pipeline has no one to
answer, and a command that waits for input in CI hangs instead of failing. A missing answer
fails with the question's text and the `--set` that answers it.

### Consequences

- **Positive:**
  - A shop example installs from a terminal or a pipeline, and a second run updates.
  - The handbook's installer and the command ask the same questions from the same file.
  - An answers file written with names serves every installation.
  - A refused run writes nothing and names every problem.
- **Negative / trade-offs accepted:**
  - **Not atomic across the steps.** If the catalogue is refused after the publish, the
    application stays published. Running again once the cause is fixed completes the install.
  - **Every run publishes**, and so records a new application release even when nothing
    changed.
  - **A token needs both roles**, `modeler` and `productmanager`. A token carries its
    minter's non-admin roles, so the minter must hold both.
  - **Name resolution reads the whole directory** with one `GET /api/v1/principals`. That
    route is not paged today; if it becomes paged, the command must follow it.
- **Follow-ups:**
  - **An `atlas.json` for the other examples**, so every example installs from a terminal
    and not only the shop.
  - **A catalogue export**, already a follow-up of the catalogue import, would close the
    round trip: build on test, export, commit, import on production.

## Links

- [ADR-0128](0128-process-applications.md): applications and publishing.
- [ADR-0134](0134-git-backed-applications.md): the source layout and its import.
- [ADR-0436](0436-a-catalogue-is-imported-as-one-document.md):
  the catalogue document.
- [ADR-0215](0215-modeler-playground.md): `atlas playground`, the other command a
  pipeline runs against a server.
