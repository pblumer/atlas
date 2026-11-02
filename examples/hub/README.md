# atlas hub: the sample registry

The installable examples in this directory are also published as signed
**Blueprint Registry v1** packages ([ADR-0499](../../docs/adr/0499-blueprint-registry-and-distribution.md)).
An atlas installation lists, filters, previews and imports them from the Console's
**atlas hub** dialog. No binary upgrade is needed, and the import is the same package
import (ADR-0467) as for an uploaded package: dry-run plan, conflict decisions, questions
for a shop's audiences and approvers, explicit publish.

| File | What it is |
| --- | --- |
| [`bundles.json`](bundles.json) | Which examples travel together as one application, and the tags each bundle carries. |
| [`releases.json`](releases.json) | The release lock. It records the registry and publisher identity, the licence, the public key the index is signed for, and every `(bundle, version, digest, minAtlas)` ever recorded. Entries are only ever appended. |

## Bundles: a few applications, by theme

Trying out the samples should leave a handful of applications behind, not one per model.
`bundles.json` therefore groups them:

| Bundle | Application | Content |
| --- | --- | --- |
| `einstieg` | Beispiele: Einstieg | 7 small examples that run without a configured worker |
| `auftrag-finanzen` | Beispiele: Auftrag & Finanzen | dunning, price changes, product capture, order-to-cash, record checks |
| `personal` | Beispiele: Personal & Mitarbeitende | relocation, examination, travel booking, workplace equipment |
| `umsysteme` | Beispiele: Umsysteme | Google Sheets, Jira, MS SQL, SharePoint |
| `identitaet` | Beispiele: Identität & Verzeichnis | 17 Entra, AD and Exchange examples |
| `bewerbermanagement` | Bewerbermanagement | the handbook's workshop |
| `mitarbeitereintritt` | Lehrgang: Mitarbeitereintritt | the handbook's guiding case |
| `onboarding-tutorial` | Onboarding | the handbook's end-to-end tutorial |
| `verwaltung-dienstleistungen` | Beispiel: Dienstleistungen der Verwaltung | shop package with two catalogues |
| `m365-dienste` | Beispiel: Microsoft-365-Dienste | shop package |

Rules, checked by `go test ./cmd/atlas`:

- Every example the handbook offers for installation is in exactly one bundle.
  Studies and system processes are in none.
- A bundle of several examples needs a `name`. That name becomes the application, and
  its portable key is derived from the name.
- A bundle of one example keeps that example's own application, the one the handbook's
  install button creates. A shop package travels only alone.
- Inside a bundle, no process, form or decision id may occur twice. Form ids are unique
  across all examples anyway.

**Tags** label a bundle, for example `einstieg`, `identitaet` or `handbuch`. The build
adds a tag for each Worker Type the processes name (`entra`, `jira`, …) and the tag
`shop` when the bundle brings a catalogue. Tags are signed with the index. In the
Console, the tags filter the listing; they grant nothing.

**Worker names are per server.** Examples bundled into one application still share the
installation's workers. One case is known: the Entra examples and `exchange-austritt`
both name a worker `contoso`, one as Worker Type `entra` and one as `exchange`. One of
the two needs a worker under another name on that server. The import plan names the
mismatch.

## Versions are immutable

`releases.json` records, for every bundle, each version it was published under and the
exact SHA-256 of that version's archive. The archive is deterministic: sorted USTAR
members, fixed time and mode. `go test ./cmd/atlas -run TestHubReleasesAreCurrent` fails
as soon as a bundle's package no longer matches its latest recorded version. After
changing an example or `bundles.json`, run:

```bash
go test ./examples -run TestExampleCatalogMatchesTheFiles -update
```

```bash
go run ./cmd/atlas hub lock
```

`lock` appends the next patch version for each changed bundle. It sets the new entry's
`minAtlas` to the release line of the binary that runs it (`0.21.0-dev` gives
`0.21.0-0`). It never rewrites an earlier entry.

**Limitation (BMP1):** the published index lists only the latest version of each
bundle. Older archives are not kept on the Pages site. Pinned updates across versions
are BMP2.

## Publishing

`.github/workflows/pages.yml` builds the registry into `hub/` of the GitHub Pages site.
It runs on every push to `main` that touches the examples, provided the repository
secret `ATLAS_HUB_SIGNING_KEY` is set. Without the secret the registry step is skipped,
and the rest of the site is still published.

### First publication: key ceremony

Do this once, before anything is published, on a trusted machine and outside the
repository.

1. **Create the key.**

   ```bash
   go run ./cmd/atlas hub keygen --out ~/atlas-hub-signing.key
   ```

   The command writes the private key (mode 0600, never overwritten) and prints the
   public key and its fingerprint. The private key never enters the repository, a chat,
   a ticket or a screenshot.
2. **Make the offline backup** (next section) and check it with `atlas hub fingerprint`
   before you go on.
3. **Record the public key.** Put the printed public key into `releases.json` as
   `"publicKey"` and merge that change. `build` refuses any key whose public half
   differs, so a mistaken secret cannot publish an index no installation accepts.
4. **Store the secret.** Put the content of the key file into the repository secret
   `ATLAS_HUB_SIGNING_KEY` (Settings → Secrets and variables → Actions). Enable GitHub
   Pages with source "GitHub Actions" if it is not enabled yet.
5. **Delete the working copy.** Remove `~/atlas-hub-signing.key` from the machine, keeping
   only the offline backups, and run the **Pages** workflow.

A local build for inspection, with the key restored from a backup:

```bash
go run ./cmd/atlas hub build --key-file ~/atlas-hub-signing.key /tmp/atlas-hub
```

## Offline backup of the signing key

The secret in GitHub cannot be read back. If the backup is missing, a lost secret means
a rotation (below), and every installation has to be reconfigured. Keep the key
therefore in **two offline copies, in two places**:

- **Paper.** The key file is one line of 44 base64 characters, the Ed25519 seed. Print it
  or write it down, together with the fingerprint, and keep it in a safe or a sealed
  envelope. Do not use a networked printer.
- **Encrypted medium.** A USB stick with an encrypted volume (VeraCrypt, LUKS, BitLocker To
  Go) or an encrypted password-manager entry, separate from the paper copy.
- **Label both copies:** "atlas hub signing key", the fingerprint, the date and
  `releases.json`'s `registryId`. Never write down the passphrase of the encrypted
  medium together with the medium itself.

**Check every backup** before you rely on it: after creating it, and once a year.

```bash
go run ./cmd/atlas hub fingerprint --key-file /path/to/restored.key
```

The command prints the public key and the fingerprint, never the private key. It fails
unless the key matches the `publicKey` in `releases.json`. Delete the restored file
afterwards.

Who holds the copies, and who may restore one, is an owner decision. Record it outside
this public repository.

## Rotating the signing key

ADR-0499 deliberately has no automatic key distribution: a key that arrives with the
registry would prove nothing. Every key an installation trusts is pinned by its operator
in `ATLAS_BLUEPRINT_REGISTRY_KEY`, one key or, during a rotation, two separated by a
comma:

```bash
ATLAS_BLUEPRINT_REGISTRY_KEY=<old publicKey>,<new publicKey>
```

An index signed by either key is accepted. More than two keys, or one key twice, stop the
server at startup. At startup the server logs the pinned fingerprints as event
`hub.registry_keys_pinned`: as INFO for one key, and as a WARN for two, so a rotation that
was never finished does not go unnoticed.

### Planned rotation, without interruption

Reasons for a planned rotation: the key holder changes, or you want regular rotation.

1. Create the new key with `atlas hub keygen` and back it up as described above. Check
   the backups with `atlas hub fingerprint`. Before step 4, `releases.json` still records
   the old key, so the check reports a mismatch. Compare the fingerprint it prints with
   the one `keygen` printed instead.
2. **Announce** the new public key and its fingerprint to the operators of all
   installations. Use a channel that is not the Pages site, for example a release note in
   this repository or a direct message.
3. **Operators pin both keys**: `ATLAS_BLUEPRINT_REGISTRY_KEY=<old>,<new>`, then restart.
   The hub keeps working, because the registry is still signed with the old key. Wait
   until every installation confirms this step.
4. **Switch the signing key.** Replace `"publicKey"` in `releases.json` with the new key
   (one commit, merged), replace the secret `ATLAS_HUB_SIGNING_KEY`, and run the
   **Pages** workflow. Installations that pin both keys accept the new index without a
   gap.
5. **Operators remove the old key**: `ATLAS_BLUEPRINT_REGISTRY_KEY=<new>`, then restart.
   The startup WARN disappears.
6. Destroy the old key's backups once every installation has done step 5. Record the date
   and both fingerprints.

An installation that skipped step 3 refuses the new index after step 4. Only its hub
dialog is affected ("trusted Blueprint registry is unavailable or invalid"). Applications
that are already imported and deployed keep running, and nothing is uninstalled. It
recovers by pinning the new key.

### Compromised or lost key

If the key is compromised, someone else can sign an index. To reach an installation,
they also need to serve it under the pinned URL. Act anyway, immediately:

1. **Tell the operators to stop trusting the compromised key now.** Each removes both
   `ATLAS_BLUEPRINT_REGISTRY_*` variables and restarts, which disables the hub dialog.
   Nothing that is already installed changes. **Never** pin a compromised key next to the
   new one: the two-key form exists for planned rotations only.
2. Delete the secret `ATLAS_HUB_SIGNING_KEY`, check who had access to it, and review the
   repository's recent workflow runs and Pages deployments.
3. Check what the operators may have imported since the suspected compromise. The import
   plan and the application releases show what came in. Compare it with the signed
   packages you built yourself (`atlas hub build` locally).
4. Create a new key, record it in `releases.json`, set the secret, and publish. The
   operators then set the URL and **only** the new key.

A lost key without suspected misuse cannot sign any more, so the two-key overlap of a
planned rotation does not help: the registry can only be re-signed with the new key.
Announce the new key, switch (step 4 above), and have the operators replace the key
directly.

## Connecting an installation

The operator of each installation sets, before startup:

```bash
ATLAS_BLUEPRINT_REGISTRY_URL=https://pblumer.github.io/atlas/hub/index.json
ATLAS_BLUEPRINT_REGISTRY_KEY=<publicKey from releases.json>
```

Take the key from this repository (out of band), never from the Pages site, because the
registry host is not its own trust root. During a rotation the variable holds two keys
(see above). The installation must run a build that knows
release tags (this change, ADR-0499 amendment 2026-11-02). An older build refuses the
index.

The server then verifies the index signature and every package digest. A person with the
`modeler` role opens **atlas hub** in the Console, filters by tag, previews a bundle and
imports it. A shop bundle's catalogue import needs `productmanager`.

A bundle's preview and its import plan name what it does not bring:

- Worker Types and workers
- server constants
- the groups and people a shop's questions ask for
