# ADR-0166: Active Directory connector

- **Status:** Proposed (amended 2026-08-21 four times — the operation set covers the
  whole lifecycle, the connector runs on a worker, it can read a DirSync delta, and it
  can create a contact; see the amendment notes below)
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

> **Amendment (2026-08-21): the lifecycle is complete.** The original decision shipped
> six operations — create-user, set-password, enable, disable, and add/remove a group
> member — chosen because each is an AD-specific encoding the generic LDAP connector
> cannot express. That was the right filter for *why a connector exists* and the wrong
> one for *what a process needs*. `docs/comparisons/mim.md` put it plainly: a
> joiner/mover/leaver process cannot be modelled without changing an attribute, moving
> an entry, or deleting one, and a **mover in a directory literally is a DN change**.
> Four operations are added:
>
> - **update-attributes** replaces exactly the attributes the authored object names,
>   leaving the rest of the entry alone. The changes are sent in a stable order, so a
>   replayed job (delivery is at-least-once) produces an identical request.
> - **move** takes one target `newDN` — how a person thinks about it — and the
>   connector splits it into the relative name and the new parent that LDAP's ModifyDN
>   wants, respecting backslash escapes so a comma inside a value does not split the
>   name in the wrong place. `deleteOldRDN` is true; keeping an entry's former names as
>   spare attribute values is a directory slowly filling with history nobody asked for.
> - **delete** removes the entry at `dn`. There is no separate delete-user and
>   delete-group because LDAP's delete does not distinguish them, and inventing two
>   names for one operation would be a fiction the connector then has to maintain.
> - **create-group** joins create-user. The two are the same LDAP add, and they are
>   separate operations because they differ in the one thing that matters: each
>   supplies its own `objectClass` chain when the authored attributes omit one. AD
>   rejects an add without `objectClass`, and forgetting it is the single most common
>   way a first create fails — not a business decision worth making every author
>   repeat. An authored `objectClass` always wins.
>
> **Three of the four are not AD-specific**, so the original decision's own reasoning
> would have left them with the generic LDAP connector. That is rejected on use rather
> than on purity: an identity process that must drop the AD connector and pick up the
> LDAP one to rename an account has been handed two ways to bind to the same
> directory, two places to configure a credential, and a seam in the middle of one
> lifecycle. A little overlap between the two connectors is the cheaper mistake.
>
> **Still not here, and still deliberately:** search and read stay with the LDAP
> connector (ADR-0154), as the *Negative* note below says. The follow-ups below are
> unchanged — Kerberos/NTLM bind, userAccountControl helpers beyond enable/disable
> (unlock, must-change-password-at-next-logon), a delta/DirSync read, and a marketplace
> element-template.
>
> **Amendment (2026-08-21, second): the connector runs on a worker.** The kind now has
> a worker half, following [ADR-0168](0168-connector-work-on-a-worker.md), and AD's
> shape puts the boundary in a different place than mail's.
>
> A mail task names a connector and nothing else, so both the endpoint and the
> credential moved. An AD task authors its own server URL and bind DN — this record
> decided the directory is model data — so those keep travelling with the job. **The
> only thing that moves is where the bind password behind the reference is read.**
> `ad.Job` carries the reference the model authored, never a value, and the process
> running the job resolves it: the engine from its vault or environment as before, an
> `atlas worker --connector ad` from `ATLAS_CONNECTOR_<REF>_TOKEN` in *its* own
> environment. Offloading the kind therefore moves one variable and changes nothing
> about any model.
>
> What that buys is that a compromised engine no longer yields a bind credential with
> write access to the directory — which, now that the operation set includes delete
> and move, is worth more than it was when the connector could only create and
> disable.
>
> Two consequences worth stating plainly:
>
> - **An offloaded AD task cannot use the engine's vault** (ADR-0069/0070). The vault
>   is server-side and a worker has none, so a deployment that keeps bind passwords
>   there trades that for the worker's environment when it offloads. That is the same
>   trade ADR-0168 accepted for every kind that moves; it is called out here because
>   AD is the first kind whose secret was a *vault-backed reference* rather than a
>   connector-record credential.
> - **The new password a set-password writes does travel** on the job. It is the
>   operation's own data and always was a process variable, and a worker leasing the
>   job already receives the task's variables — so carrying it on the payload adds no
>   exposure the lease did not already have. It is not a *bind* credential, and the
>   distinction is the point.
>
> As with mail, the in-process handler now calls the same `Resolve`/`Run` pair the
> worker does, so there is one definition of what a resolved AD task means rather than
> two that drift; the existing suite passes unchanged against it, which is the evidence
> the relocation did not alter behaviour. The kind stays registered in process by
> default and is turned off per ADR-0157's switch — `--offload-connectors ad` — so no
> running installation is disturbed.

> **Amendment (2026-08-21, third): the delta read.** [ADR-0154](0154-ldap-connector.md)
> listed a sync/delta cookie as a follow-up for the *generic LDAP* connector and its
> hardening amendment declined to build it there, on the grounds that a delta read is
> not one feature but two vendor protocols — DirSync for Active Directory, RFC 4533
> content sync elsewhere — and that a generic connector guessing which server it is
> talking to is the wrong shape. This is the AD half, and it lands here for the same
> reason everything else in this record does: it is Microsoft's own mechanism, not
> LDAP's.
>
> A `sync` operation performs one DirSync pass (MS-ADTS `LDAP_SERVER_DIRSYNC_OID`). It
> is the only AD operation that reads rather than writes and the only one that
> addresses a naming context rather than an entry, so it takes a `baseDN` and a
> `filter` instead of a `dn`. AD answers DirSync only at a naming context root and only
> for the whole subtree, so there is no scope for a model to author.
>
> **The cookie is the design.** `cookieVariable` names *one* process variable that the
> operation reads and writes: the pass presents the cookie it finds and writes the
> server's new one back. A reconciliation modelled as a loop — sync, handle the
> changes, wait on a timer, sync again — therefore carries its own position forward,
> and **no sync state lives in the connector or the engine at all**. The alternative
> was a server-side cursor keyed by something, which would have made a stateless
> connector stateful to save a model one variable. The cookie is opaque binary and a
> process variable holds text, so it travels base64-encoded; a value no pass ever wrote
> fails the job rather than silently starting over and handing the process a full
> directory it believes to be a change set.
>
> Three smaller decisions worth recording:
>
> - **A response without the DirSync control is an error.** It usually means the bind
>   account lacks the *Replicating Directory Changes* right, or the base is not a
>   naming context root. AD answers such a request as an ordinary search, and returning
>   those entries would be the same failure as a bad cookie: a full directory presented
>   as a delta.
> - **`objectSecurity`** exposes the one DirSync flag that decides whether an account
>   without that right can use the operation at all — it then reports only the objects
>   the account can read. It is an operational blocker rather than a tuning knob, which
>   is why it is in the model and the other flags are not.
> - **`maxEntries` caps a pass and defaults to 1000.** Unlike a plain search's cap
>   (ADR-0154 amended, ADR-draft-generic-sql-connector) this costs nothing but a second pass, because a pass
>   is resumable by construction: the cookie says where it got to. The result carries
>   `more`, the server's own signal that further changes are already waiting, so a loop
>   can go straight round again instead of waiting for its timer.
>
> A deleted object arrives as an entry carrying `isDeleted=TRUE`. AD reports a deletion
> as a change rather than as an absence, and flattening that away would remove the only
> signal a leaver process has.
>
> **Amendment (2026-08-21, fourth): create-contact.** A contact joins create-user and
> create-group, and for the same reason they are separate: it supplies its own object
> classes — `top`/`person`/`organizationalPerson`/`contact` — which AD rejects an add
> without. A contact is a mail-enabled entry with no account behind it, which is how a
> person in one forest appears in another's address book.
>
> It was added for GALSync, and that is worth recording because of what GALSync did
> *not* need. MIM ships cross-forest address-list sync as a management agent; here it
> is [`examples/galsync.bpmn`](../../examples/galsync.bpmn), a process. GALSync has no
> wire protocol of its own — it is a rule about which objects in one forest should
> appear in another's address book, and every mechanism it uses was already a
> connector: the DirSync delta above, `ldap search` to find an existing contact (search
> deliberately not being this connector's job), `update-attributes`, `delete`, and a
> timer to loop. Only the contact primitive was missing. Building the rest in Go would
> have put business policy in the engine, which is the thing a BPMN engine exists to
> avoid.
>
> **Not built:** `LDAP_DIRSYNC_INCREMENTAL_VALUES`, which returns only the changed
> values of a multi-valued attribute rather than the whole attribute. It matters for a
> group with tens of thousands of members, and it is left out deliberately rather than
> forgotten: it changes the *shape* of what comes back (ranged attribute names), so how
> that shape reaches a model deserves its own decision rather than a flag.

## Context and problem statement

Active Directory is the core target system for identity provisioning in most
organizations, and the connector wishlist marks it Wave 1. AD speaks LDAP, and Atlas
already has a generic LDAP connector (ADR-0154) that can search, add, modify, and
delete entries against any directory including AD. But the operations that make an AD
joiner/mover/leaver process work are expressed through AD-specific mechanisms the
generic LDAP connector cannot author directly:

- **Setting a password** is not the RFC 3062 Password Modify extended operation the
  LDAP connector uses (AD does not support it) — it is a write of the binary
  `unicodePwd` attribute, UTF-16LE and quote-wrapped, over an encrypted channel.
- **Enabling or disabling an account** is a bit (`ACCOUNTDISABLE`, 0x2) in the
  `userAccountControl` integer — a read-modify-write, not a plain attribute set.
- **Group membership** is an *incremental* add/delete of a `member` value, whereas the
  generic connector's modify replaces an attribute wholesale.

The question: how should a BPMN process perform these AD provisioning primitives?

## Decision drivers

- **Cover the AD lifecycle** the wishlist marks: create a user, set a password,
  enable/disable, and manage group membership.
- **Credentials never in the model** — the bind password is a secret reference
  (ADR-0041), like every other connector.
- **Reuse, don't duplicate the protocol** — AD is LDAP, so the connection path
  (dial/bind/TLS, bounded by ADR-0149) is the LDAP connector's, via go-ldap.
- **First-class AD operations** — a modeler should pick "Disable account", not hand-
  author a `userAccountControl` modify with a magic integer.

## Considered options

1. **Do nothing** — model AD with the generic LDAP connector (ADR-0154), authoring
   `unicodePwd`/`userAccountControl`/incremental-member modifies by hand.
2. **Widen the LDAP connector** with AD-aware options (a password-attribute toggle, an
   enable/disable operation).
3. **A dedicated AD connector** with AD-semantic operations, reusing go-ldap for the
   connection.

## Decision outcome

Chosen option: **"a dedicated AD connector"** (option 3). Option 1 pushes AD's
encoding rules (UTF-16LE unicodePwd, the ACCOUNTDISABLE bit, incremental member
changes) into every process and is a foot-gun; option 2 muddies the generic connector
with a vendor's specifics. A dedicated connector gives the modeler AD-semantic
operations while reusing the LDAP protocol machinery.

Concretely:

- Reserved job type `io.atlas.ad` at `AdJobTypeIndex == 18`, one in-process worker per
  deployed process.
- `<atlas:adConnector url bindDN bindSecret startTLS operation dn memberDN
  entryVariable newPassword>` on a service task. `url`/`bindDN`/`dn`/`memberDN`/
  `newPassword` are literal-or-FEEL values; `operation` is one of create-user /
  create-group / update-attributes / set-password / enable / disable / move / delete /
  add-group-member / remove-group-member (the last four added by the 2026-08-21
  amendment, which also adds the `newDN` attribute a move targets).
- The worker dials/binds like the LDAP connector (go-ldap, bounded by
  `nettimeout.Default`) and maps each operation: **set-password** replaces `unicodePwd`
  with the UTF-16LE quote-wrapped encoding (LDAPS/STARTTLS); **enable/disable** read
  `userAccountControl`, flip the `ACCOUNTDISABLE` bit while preserving other flags, and
  write it back; **add/remove-group-member** issue an incremental add/delete of the
  `member` value on the group DN; **create-user** adds the entry from a named attribute
  object.
- The bind password is a secret reference (ADR-0041); the new password is a
  literal-or-FEEL value (usually a FEEL reference to a variable), mirroring the
  user-provisioning connector (ADR-0123).

### Consequences

- **Positive:** AD onboarding/offboarding is authorable as first-class operations; the
  AD encoding rules live in one worker, not every model; it reuses the LDAP protocol
  path and the go-ldap dependency (no new one).
- **Negative / trade-offs accepted:** a second connector that overlaps the generic LDAP
  one (search/read stays with LDAP, deliberately); enable/disable is a read-modify-
  write (two round-trips, not atomic); a password set requires an encrypted channel the
  model must configure (LDAPS or STARTTLS) — an unencrypted attempt is rejected by AD
  into an incident; no Kerberos/NTLM bind yet (simple bind over TLS only).
- **Follow-ups / risks to watch:** Kerberos/NTLM bind; primary-group and
  userAccountControl-flag helpers beyond enable/disable; a delta/DirSync read; a
  marketplace element-template.

## Pros and cons of the options

### Option 1 — generic LDAP connector
- Good: no new code.
- Bad: unicodePwd encoding, the UAC bit, and incremental membership are hand-authored
  in every process; error-prone; no AD-semantic operations.

### Option 2 — widen the LDAP connector
- Good: one connector.
- Bad: AD specifics leak into the generic connector; operation set becomes conditional
  on a vendor.

### Option 3 — dedicated AD connector (chosen)
- Good: AD-semantic operations; encoding rules centralized; reuses go-ldap/LDAP path.
- Bad: overlaps the LDAP connector for the generic parts (kept there on purpose).

## Links

- relates to ADR-0154 (generic LDAP connector) — the protocol path AD reuses; generic
  search/read stays there
- relates to ADR-0041 (connector management and secret store)
- relates to ADR-0149 (bounded connector call budget) — the dial/operation timeout
- relates to ADR-0123 (sanctioned user provisioning) — the internal counterpart
- relates to ADR-0007 (job protocol durability)
