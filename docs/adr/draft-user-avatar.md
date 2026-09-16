# ADR-DRAFT: A picture of a person belongs to the account, in two provenances and one place

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-09-16
- **Open question:** Whether a picture should ever be **derived** where none was
  given — initials on a coloured disc, the thing most products draw. It is not
  built, and the reason is that a derived picture is indistinguishable on screen
  from a chosen one: a person who has never uploaded anything would appear to have
  made a choice, and two colleagues with the same initials would wear the same
  face. The circle says "nobody has chosen", which is true. This could be revisited
  the day the absence is measured to be the problem rather than assumed to be.
- **Question checked:** 2026-09

## Context and problem statement

Atlas showed people as strings. An approval said `usr_4be5b4ad`, the portal's
corner drew an empty circle, a recipient picked out of the directory was a name in
a list of names.

That is fine while somebody works with three colleagues. It stops being fine in an
estate, and it stops being fine first exactly where the mistake is expensive:
ordering in somebody else's name, and deciding somebody else's request. Reading a
face is faster and less error-prone than reading a name, and the names Atlas shows
are often not even names.

> As a user I want a profile picture, uploaded or taken from Entra ID.

## Decision drivers

- Two provenances were asked for and both have to be real. A tenant that already
  holds a photo for everybody must not be asked to collect them a second time.
- Atlas holds no tenant credential and must not start holding one (ADR-0332). The
  mirror pulls through a worker; nothing in the server calls Graph.
- An uploaded image is attacker-influenced content the server persists and serves
  on to every browser. Here the uploader is **every account**, not an
  administrator — which is a wider door than any image route Atlas had.
- A picture that outlives its account is not untidy, it is wrong: ids are
  assigned, so the next account handed that id inherits a stranger's face.

## Considered options

For **where it is stored**:

1. **Beside the account record**, in the users directory.
2. **A store of its own**, keyed by account id.
3. **On the record**, base64 in the JSON.

For **where it comes from**:

1. **An upload route, and the directory mirror** — two paths, one file.
2. **An upload route only**, and Entra photos put there by a script.
3. **Atlas fetches from Graph** when it has no picture.

## Decision outcome

**Beside the account record**, and **an upload route plus the directory mirror**.

### Why beside the record

Because two things then follow without anybody arranging them. A snapshot that
carries the accounts carries their pictures — the backup walks whole directories,
so nothing had to be added to an allowlist. And the store can make deletion
atomic in the sense that matters: `userStore.Delete` clears the picture and then
the record, so every deletion path does it rather than every deletion path having
to remember to.

A store of its own would have been a second directory, a second allowlist entry
and a second thing to clean up. Base64 on the record would have put a quarter of a
megabyte into every listing that reads accounts, which is most of them.

The file is named from [sidecar.Store.FileFor] rather than built from the id, and
that is the whole of the path safety: the id reaching the handler came off a URL,
and encoding it the way the store encodes its own keys means a picture can no more
escape the directory than a record can. There is no second encoding to keep in
step.

### Why two provenances, recorded

`AvatarSource` is on the account: `uploaded` or `entra`, empty where there is no
picture. It is recorded rather than derived, because nothing in a JPEG says who
chose it — and the difference is exactly what somebody looking at a wrong picture
needs to know: **whether to change it here or in the directory.** It is also what
lets a mirror leave alone a picture a person put there themselves.

The constant for the directory exists before the path that produces it. That is
deliberate: a field whose meaning widens later is a field whose old records have
to be re-read, and there is nothing to gain by having "uploaded" mean "from
somewhere" for one release.

### Why Atlas does not fetch from Graph

Because it would put a credential that can read an entire directory into the
server, which is the thing ADR-0332 decided against, for a picture. The photo
arrives the way every other directory fact arrives: a process reads it through the
Entra worker and reports it here. The worker gains one operation; the server gains
nothing it did not already have.

The cost is honest and worth stating: **the directory half is not in this
change.** What is here is the store, the routes, the provenance and the surfaces.
What follows is the worker operation and the carriage on the sync message.

### What this route refuses

**A vector.** A brand mark may be an SVG — it is drawn, it is scaled, a designer
delivers one — and [brandimage.Serve] makes a hostile one inert. A photograph has
no such reason: it comes from a camera or from a directory, and both give raster
bytes. Accepting a document format with scripting in it, in the one place where
the uploader is every account rather than an administrator, is widening the
surface for nothing. So `brandimage` now has a set per surface — `Mark` is PNG
and SVG, `Photo` is PNG and JPEG — over **one** content check. Which types a
surface takes is a policy and the surfaces differ; whether bytes really are the
type they claim is a security question with one answer everywhere, and two copies
of that is one copy a future hardening will miss.

JPEG is in the set because that is what Graph answers with. A set that refused it
would have made the directory path impossible and said nothing about why.

### Who may change it

The account itself, or an administrator. **Not an operator**: an operator runs
what is deployed, and changing the face a colleague wears to everybody else is not
running anything. Reading is everybody signed in, which is the point of having a
picture at all — it is read beside a name in a task list, an approval and a
recipient picker, by colleagues rather than by administrators, and it discloses
less than the principals directory the same caller already reads (ADR-0073).

### Consequences

- **Positive:** people are recognisable where the mistake is expensive; the
  backup and the deletion path needed no new rules; the image check stayed single.
- **Negative / trade-offs accepted:** the directory half is a second change; there
  is no derived picture, so most accounts show a circle until somebody uploads;
  nothing resizes or re-encodes, so an account can carry a picture at the full
  asset budget and every page naming that person downloads it.
- **Follow-ups / risks to watch:** the roster carries `avatarSource` so a list of
  people asks only for the pictures that exist. A page that stopped reading it and
  asked per row would turn one listing into a request per account, most of them
  404 — which works, and looks fine, and is why the field is held by a test.

## Pros and cons of the options

### Storage
- **Beside the record:** carried by the snapshot for free, deleted with the
  account, named by the store's own encoding. Slightly unusual to find a JPEG in a
  directory of JSON — mitigated by the name, which no listing reads.
- **Its own store:** tidier to describe; a second allowlist entry, a second
  cleanup, a second path-safety argument.
- **On the record:** no second file at all; puts image bytes into every listing
  that reads accounts.

### Provenance
- **Upload route plus mirror:** both asked-for sources, no credential in the
  server, and the record says which.
- **Upload only:** half the ask, and the tenant collects photos twice.
- **Server fetches from Graph:** one fewer moving part and a directory-wide
  credential in the server. Refused by ADR-0332, not by taste.

## Links

- relates to ADR-0332 — the directory mirror, and why Atlas pulls rather than holds
  a credential
- relates to ADR-0148 and ADR-0316 — the instance's brand mark and a catalogue's,
  the two images this one shares its checks with
- relates to ADR-0073 — the principals directory, which is what a picture is shown
  beside
