# ADR-0171: A directory-file connector — LDIF and DSML

- **Status:** Proposed
- **Date:** 2026-08-21
- **Deciders:** Atlas maintainers

## Context and problem statement

[`docs/comparisons/mim.md`](../comparisons/mim.md) lists five file agents Microsoft
Identity Manager ships. Three are text *tables* — delimited, fixed-width,
attribute-value pair — and [ADR-0139](0139-csv-to-json-connector.md)'s amendment
closed those as formats of the connector that already read CSV.

Two are left, and they are not tables. **LDIF** (RFC 2849) and **DSML v1** carry
*directory entries*: a distinguished name plus multi-valued attributes. An identity
process meets them constantly — an LDIF is how a directory hands over a change set it
cannot apply itself, and how a migration is staged.

So: where do they go?

## Decision drivers

- **A file of entries should read like a directory.** Atlas already produces entries
  from `ldap` search and from `ad` sync, and a process that handles those should not
  need a second way to handle the same thing from a file.
- **Both directions.** MIM's file agents import *and* export, and writing LDIF is how
  a process hands a change set to a directory it cannot reach.
- **Don't guess.** A file format read wrongly is the failure mode that produces
  plausible nonsense rather than an error.

## Considered options

1. **More formats on the text-file connector** (ADR-0139), beside csv/fixed-width/avp.
2. **A separate directory-file connector.**
3. **Extend the `ldap` connector** with a read-a-file operation.

## Decision outcome

Chosen option: **a separate directory-file connector** (option 2), kind `ldif`,
reserved job type `io.atlas.ldif` at index 24.

Option 1 is the one that looks obvious and is wrong, and the reason is worth stating
because it is the opposite of the call ADR-0139's amendment just made. There, three
formats became one kind because they *produce the same thing*: whichever table format
a file arrived in, a process gets back a list of row objects. Here they do not. LDIF
and DSML produce

```json
{"dn": "…", "attributes": {"cn": ["Ada"], "objectClass": ["top", "person"]}}
```

which is the shape `ldap` search and `ad` sync return. Folding them into the text-file
connector would have made **the result shape depend on a dropdown** — and a process
downstream of that task could no longer be written against a known shape. Keeping them
apart is what lets an LDIF read feed exactly the handling a live directory read does,
and it is why a `format` on this connector is safe while a shared kind would not have
been.

Option 3 is rejected because `ldap` is about talking to a server. A file is not a
server, needs no endpoint, no bind and no credential, and putting it there would mean
every LDIF task carried a connection panel it must leave empty.

**No default format.** Unlike the text-file connector, `format` is required. A
directory file is LDIF or it is DSML, and guessing from the bytes is precisely how a
malformed file becomes a plausible-looking empty result.

**Both directions**, as `operation` read (default) or write. Writing takes the same
`{dn, attributes}` array a directory read produces, so a live read can be written
straight to a file with nothing in between.

### What the formats actually require

Three things about LDIF are load-bearing and quiet when wrong:

- **Continuation lines.** A line beginning with one space continues the previous one.
  Producers wrap at 76 characters, so a parser reading line-by-line splits long DNs
  and base64 values in half.
- **Base64 values.** `attr:: value` is how a value with a newline, a leading space, or
  non-ASCII text is carried. Ignoring the second colon corrupts exactly the values
  that needed the encoding — and the file still parses.
- **Repeated attributes** are how a multi-valued attribute is written, so a later
  value appends rather than replaces.

Writing applies the same rules in reverse: a value with a leading or trailing space, a
leading `:` or `<`, a newline, non-ASCII text, or invalid UTF-8 is base64-encoded,
because each of those would otherwise change what the file says while leaving it
readable.

DSML is XML and is read against local element names, because a file in the wild
carries whichever namespace prefix its producer chose. Its dedicated `<objectclass>`
element folds back into an ordinary `objectClass` attribute, which is what makes the
result the same shape a directory read gives; writing puts it back.

Output is **stable**: attributes are written in a fixed order, so a file written twice
from the same entries is byte-for-byte the same file — which is what makes a diff of
two runs mean something.

### In process and on a worker

This kind has an in-process handler as well as a worker one, and that is not a lapse
from [ADR-0164](0164-no-in-process-service-tasks.md). Parsing a file is pure
computation with no network and no credential — the same category as a FEEL script or
a local DMN evaluation, which that record explicitly leaves in the engine. It is
offloadable all the same (`--offload-connectors ldif`, `atlas worker --connector
ldif`), and both paths go through one `Run` and one `Result.Variables()`, so neither
can decide separately what a read's entries or a write's file look like.

### Consequences

- **Positive:** MIM's last two file agents close, in both directions. A file of entries
  and a live directory read hand a process the same thing, so the handling is written
  once. And the *shape* argument now has two worked examples pointing opposite ways
  (ADR-0139's amendment and this record), which is a better guide for the next
  connector than either alone.
- **Negative / trade-offs accepted:**
  - **Two file connectors**, and an author has to know which. The split follows the
    result shape rather than the file extension, which is the right line but not the
    obvious one; the Modeler names them "Text File" and "Directory File" to make it
    readable.
  - **LDIF change records are not interpreted.** A `changetype:` line comes through as
    an ordinary attribute rather than driving an add/modify/delete. Reading a change
    set as entries is still useful, and applying one belongs with the connector that
    can talk to a directory, not with the one that reads a file.
  - **`attr:< url`** — a value fetched from a URL — is refused rather than skipped.
    This connector reads a file it was handed, and quietly producing an empty
    attribute would hide the difference.
  - **DSML v1 only.** DSMLv2 is a SOAP protocol rather than a file format, and nothing
    emits it any more; if that turns out to be wrong it is the `soap` connector's
    problem, not this one's.

## Pros and cons of the options

### Option 1 — more formats on the text-file connector
- Good: one file connector; one palette entry.
- Bad: the result shape would depend on a dropdown, so nothing downstream could be
  written against a known shape.

### Option 2 — a separate directory-file connector (chosen)
- Good: entries from a file are the entries a directory read gives; no endpoint or
  credential on a task that needs none.
- Bad: two file connectors, split by result shape rather than by "it's a file".

### Option 3 — a read-a-file operation on the LDAP connector
- Good: entries stay in one package.
- Bad: `ldap` is about talking to a server; every file task would carry a connection
  panel it must leave empty.

## Relationship to other records

- completes the file-agent row of [`docs/comparisons/mim.md`](../comparisons/mim.md),
  whose table half [ADR-0139](0139-csv-to-json-connector.md)'s amendment closed
- produces the entry shape of [ADR-0154](0154-ldap-connector.md)'s search and
  [ADR-0166](0166-active-directory-connector.md)'s DirSync delta
- stays in process under [ADR-0164](0164-no-in-process-service-tasks.md)'s carve-out
  for pure computation, and is offloadable under [ADR-0168](0168-connector-work-on-a-worker.md)
- rides the connector seam of [ADR-0007](0007-job-worker-protocol.md)/[ADR-0067](0067-service-task-connector-catalog.md)
