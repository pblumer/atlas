# ADR-DRAFT: A directory photo arrives the way every other directory fact does

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-16
- **Open question:** Whether the size Graph is asked for should be a model's choice.
  Graph serves a photo at fixed sizes (`/photos/48x48/$value` … `648x648`) as well
  as the default `/photo/$value`, and a picture shown at 30 pixels does not need
  the largest. It is not offered because a fixed size a tenant does not hold
  answers 404 — indistinguishable here from "this person has no photo" — so
  choosing one would trade a bounded cost for a silent wrong answer. The default
  is the one size that exists whenever any does. This would change the day the
  bytes are measured to be a problem rather than assumed to be.
- **Question checked:** 2026-09

## Context and problem statement

[ADR-draft-user-avatar] gave an account a picture and two provenances, and built
one of them: an upload. The other was the reason the field records where a picture
came from at all — a tenant that already holds a photo for everybody should not be
asked to collect them a second time.

The constraint that shapes everything below is not about pictures. **Atlas holds no
tenant credential** (ADR-0332): the directory mirror *pulls*, through a process
that reads Graph with the Entra worker and reports what it read. Nothing in the
server calls Graph, and a photo is not a good enough reason to be the first thing
that does.

## Decision drivers

- A credential that can read an entire directory must not move into the server.
- A person without a photo is the ordinary case, not a failure. A tenant of two
  thousand people must not produce two thousand incidents.
- A picture somebody chose themselves must survive a mirror run.
- The mirror's own discipline: the decider produces a complete plan and the apply
  writes it without re-deciding anything (ADR-0332). A photo must not be the
  exception that puts a lookup back into the write.

## Considered options

For **reading the photo**:

1. **A worker operation** that returns the bytes, like every other Graph read.
2. **The server fetches** from Graph when an account has no picture.
3. **A script** outside Atlas that uploads through the ordinary avatar route.

For **carrying it in**:

1. **On the sync message**, beside the users and groups it already carries.
2. **The process uploads** to `PUT /api/v1/users/{id}/avatar` through the REST
   worker.

## Decision outcome

**A worker operation**, and **on the sync message**.

### The worker reads bytes, which it could not do before

`Client.Call` decoded JSON and nothing else, because every Graph operation Atlas
had was a JSON one. A photo is `image/jpeg`.

The change is one field on the request — `Binary` — rather than a second method on
the interface. A method would have broken every fake in every test that stands in
for Graph, for a distinction that is a property of *the request* and not of the
client: this one asks for bytes. The client returns them with the type Graph
declared, and `Run` turns that into `{contentType, data}` with the data base64 —
because a process variable is FEEL, and FEEL has no bytes.

**A 404 is an answer, not a failure.** `/photo/$value` answers 404 both for a
person who has no photo and for a user id that is not anybody's, and Graph's error
code distinguishing the two is not something to hang a sync on. So a binary read
reports *absent*, and the trade is stated rather than hidden: a mistyped id yields
"no photo" instead of an error. The other way round, every person without a photo
would fail a job, and a tenant where most people have none would produce an
incident queue nobody can read. A missing picture is the cheaper wrong answer by a
wide margin.

The read is **capped**, and refuses rather than truncates. An unbounded body into a
process variable is the failure the listing cap exists for, and half a JPEG is not
a smaller JPEG.

### The photo travels on the message, not on the user object

`directoryUser` is documented as one object from `/users/delta`, and a photo is
not in that page — it is a second thing the run read, one call per person. Putting
it there would have been a small lie in the one file where a reader most needs to
know what came from where. So the message carries `photos` at the top level, a
list rather than a map so its order is fixed and the plan stays deterministic.

Each entry names the object id and either the bytes or `removed`. **Removal is
explicit on purpose.** A process that asked and got 404 knows something a process
that did not ask does not, and without a way to say so, a photo deleted in the
tenant would stay on the account forever — the absence of an entry has to keep
meaning "not fetched".

### A fingerprint, so "unchanged" stays true

The decider compares the record before and after and calls an account unchanged
when they match. A photo that changed while the record did not would then be
planned as `unchanged` and written anyway, which breaks the one rule that makes
the reporting mode trustworthy: the plan says what the apply does.

So the record carries `AvatarFingerprint`, a digest of the bytes. A changed photo
changes the record, the decider sees it, and the plan says so. It also makes the
write idempotent: the same photo arriving twice is not a write at all.

### A mirror does not overwrite a choice

A picture whose provenance is `uploaded` is left alone, and the run counts how
often that happened rather than writing a line per person — it is the ordinary
case for anybody who ever set their own, and a report nobody can read is a report
nobody reads. A refusal is a note: bytes that are not a picture are few and
surprising, and the tenant should hear about them.

Removal follows the same rule. The directory may take away what the directory
gave; it may not take away what a person chose.

### The apply still does not decide

The bytes ride on the decision, beside the record, under `json:"-"` like the
record itself — so a reporting run answers with counts and notes and never with a
megabyte of base64. `applyDirectoryPlan` writes the picture and then the record,
in that order: a failure between them leaves a picture no record points at, which
the next run repairs through the fingerprint, where the other order would leave a
record claiming a picture that is not there.

### Consequences

- **Positive:** both provenances the story asked for exist; no credential moved
  into the server; the mirror's plan/apply split survived intact.
- **Negative / trade-offs accepted:** a mistyped user id reads as "no photo"; a
  photo change is only noticed when the process fetches one, and Graph's delta does
  not report photo changes, so keeping pictures current is a decision the process
  makes rather than something the mirror does by itself; nothing resizes, so a
  tenant with large photos puts large photos in front of every reader.
- **Follow-ups / risks to watch:** the binary path is the first place in this
  worker where a non-2xx is not an error. It is confined to `Binary` requests and
  held by a test, because the day it leaks into the JSON path is the day a failed
  directory read looks like an empty one.

## Pros and cons of the options

### Reading
- **A worker operation:** the credential stays where it is; one field on a request;
  the process decides whom to fetch for.
- **The server fetches:** fewer moving parts, and a directory-wide credential in
  the server. Refused by ADR-0332, not by taste.
- **A script outside Atlas:** nothing to build, and the provenance becomes a lie —
  every such picture would record as `uploaded`, and nobody could tell which ones
  the directory owns.

### Carrying
- **On the sync message:** one mechanism, one plan, the existing
  directory-id-to-account mapping.
- **The process uploads:** Atlas calling itself with an Atlas credential, and the
  process would have to learn the Atlas account id, which only the mirror knows.

## Links

- relates to ADR-draft-user-avatar — the picture itself, and the provenance this
  one fills in
- relates to ADR-0332 — the mirror, its plan/apply split, and why the credential
  stays in the worker
- relates to ADR-0172 — the worker's typed façade over Graph, which this extends by
  one operation
