# ADR-0166: Active Directory connector

- **Status:** Proposed
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

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
  set-password / enable / disable / add-group-member / remove-group-member.
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
