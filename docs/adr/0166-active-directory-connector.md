# ADR-0166: Active Directory connector

- **Status:** Proposed (amended 2026-08-21 — the operation set now covers the whole
  lifecycle; see the amendment note below)
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
> element-template — as is the fact that this connector still runs **in process**,
> which [ADR-0164](0164-no-in-process-service-tasks.md) deprecates. Moving it onto a
> worker is a migration in its own right and is not part of this amendment.

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
