# atlas hub: the sample registry

Every installable example in this directory is also published as a signed
**Blueprint Registry v1** package ([ADR-0499](../../docs/adr/0499-blueprint-registry-and-distribution.md)),
so an atlas installation lists, previews and imports the samples from the Console's
**atlas hub** dialog — without a binary upgrade, and through the same package import
(ADR-0467) as an uploaded package: dry-run plan, conflict decisions, questions for a
shop's audiences and approvers, explicit publish.

| File | What it is |
| --- | --- |
| [`releases.json`](releases.json) | The release lock: registry and publisher identity, licence, the public key the index is signed for, and every `(sample, version, digest, minAtlas)` ever recorded. Append-only. |

## How a sample becomes a package

`atlas hub build` composes one package per example from
[`api/web/examples-catalog.json`](../../api/web/examples-catalog.json) (which files make
up an example) and the files themselves (their content):

- An example that ships a shop is already a package directory (`atlas.json`,
  `katalog.json`, `fragen.json`) and travels as `atlas import` reads it. An answers file
  stays behind: answers belong to the installation that gives them.
- Any other example gets the `atlas.json` the handbook's install button would produce:
  the same application name and therefore the same portable key, the same process, form
  and decision identities; DMN models travel with their decisions (format 2).
- Studies and system-process examples, which the handbook does not install, are not
  offered.

The archive is deterministic (sorted USTAR members, fixed time and mode, BestCompression),
so the same files always give the same digest.

## Versions are immutable

`releases.json` records, for every sample, each version it was published under and the
exact SHA-256 of that version's archive. `go test ./cmd/atlas -run TestHubReleasesAreCurrent`
fails as soon as an example's package no longer matches its latest recorded version. After
changing an example:

```bash
go test ./examples -run TestExampleCatalogMatchesTheFiles -update
```

```bash
go run ./cmd/atlas hub lock
```

`lock` appends the next patch version for each changed sample and fixes its `minAtlas` to
the release line of the building binary (`0.21.0-dev` gives `0.21.0-0`). It never rewrites
an earlier entry. `build` refuses to publish a sample whose files no longer give its latest
recorded digest.

**Limitation (BMP1):** the published index lists only the latest version of each sample;
older archives are not kept on the Pages site. Pinned updates across versions are BMP2.

## Publishing

`.github/workflows/pages.yml` builds the registry into `hub/` of the GitHub Pages site on
every push to `main` that touches the examples, when the repository secret
`ATLAS_HUB_SIGNING_KEY` is set. One-time setup, by the repository owner:

1. Create the signing key on a trusted machine, outside the repository:

   ```bash
   go run ./cmd/atlas hub keygen --out ~/atlas-hub-signing.key
   ```

   It prints the public key and its fingerprint. The private key never enters the
   repository, a chat or a ticket.
2. Record the printed public key as `"publicKey"` in `releases.json` and merge that
   change. `build` refuses any key whose public half differs, so a wrong secret cannot
   publish an index no installation accepts.
3. Store the content of the key file as the repository secret `ATLAS_HUB_SIGNING_KEY`
   (Settings → Secrets and variables → Actions), and enable GitHub Pages with source
   "GitHub Actions" if it is not already enabled.
4. Run the **Pages** workflow (or push a change to `examples/`).

A local build for inspection:

```bash
go run ./cmd/atlas hub build --key-file ~/atlas-hub-signing.key /tmp/atlas-hub
```

## Connecting an installation

The operator of each installation sets, before startup:

```bash
ATLAS_BLUEPRINT_REGISTRY_URL=https://pblumer.github.io/atlas/hub/index.json
ATLAS_BLUEPRINT_REGISTRY_KEY=<publicKey from releases.json>
```

Take the key from this repository (out of band), never from the Pages site: the registry
host is not its own trust root. The server then verifies the index signature and every
package digest; a person with the `modeler` role opens **atlas hub** in the Console,
previews a sample and imports it. A shop sample's catalogue import needs `productmanager`.

What a sample does not bring is named in its preview and in the import plan: Worker Types
and workers, server constants, and the groups and people a shop's questions ask for.
