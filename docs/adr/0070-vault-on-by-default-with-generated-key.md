# ADR-0070: The secret vault is on by default, with a generated key

- **Status:** Accepted
- **Date:** 2026-07-27
- **Deciders:** Atlas engine team

## Context and problem statement

[ADR-0069](0069-engine-internal-encrypted-secret-vault.md) shipped the
engine-internal encrypted secret vault as **opt-in**: it does nothing until an
operator provisions a master key in `ATLAS_VAULT_KEY` / `ATLAS_VAULT_KEY_FILE`,
and with no key its endpoints return `503`. That was the conservative default —
it keeps Atlas holding no key at all until an operator deliberately asks for it,
which is the strongest at-rest posture.

In practice the opt-in gate is friction that defeats the feature's own goal. The
vault exists so a single-node operator can set a credential from the Console
without touching the environment — but opt-in means they *must* touch the
environment (set a 32-byte key, restart) before the Console panel does anything
but show "not configured". The common case wants the vault to **just work**: open
the Console, add a secret, done. This ADR revisits ADR-0069's enablement and key
sourcing decision (only those two points; everything else in ADR-0069 stands).

## Decision drivers

- **Turnkey by default.** The feature should work out of the box on a fresh
  single-node install, with no environment provisioning.
- **Operator control is preserved and preferred.** An operator who provisions a
  key must still win — a generated key must never override an explicit one — and
  disabling the vault entirely must remain a single flag.
- **Honesty about the trade-off.** Generating a key and storing it on disk is
  exactly the posture ADR-0069 rejected ("key and ciphertext share a disk — not
  real encryption-at-rest"). Choosing it as the default is a real reduction of
  the at-rest guarantee and must be documented and mitigable, not hidden.
- **No change to the crypto, storage, or resolution** already decided in
  ADR-0069 — only *where the key comes from when the operator provides none*.

## Considered options

1. **Keep opt-in (ADR-0069 as-is).** Strongest at-rest guarantee; worst first-run
   UX for the feature's own target user.
2. **On by default, generate a key on first start if none is provided (chosen).**
   The vault is enabled unless `--vault=false`; an operator key still takes
   precedence; absent one, Atlas generates a 32-byte key and persists it (mode
   `0600`) to a key file, reusing it on later starts.
3. **On by default, but refuse to start without a key.** Enabled by default yet
   still requires the operator to provide a key — the worst of both: the friction
   of opt-in with none of opt-in's "off is off" clarity.

## Decision outcome

Chosen: **option 2 — the vault is on by default with a generated key.**

Key sourcing, in precedence order, at server start:

1. **Operator key (unchanged, preferred):** `ATLAS_VAULT_KEY` or
   `ATLAS_VAULT_KEY_FILE`. If set it is used and **never written to disk by
   Atlas** — the ADR-0069 posture, intact for operators who opt into it. An
   operator key that is present but invalid still fails startup loudly.
2. **Generated key file (new default):** with no operator key, Atlas loads a key
   from `<data-dir>/vault.key`, or, if absent, generates a fresh 32-byte key and
   writes it there at mode `0600` (parent `0700`), fsynced. Later starts reuse it,
   so previously sealed secrets keep opening across restarts.

Enablement mirrors the polyglot-script opt-out ([ADR-0047](0047-polyglot-script-tasks-via-job-workers.md)):
the vault runs unless **`--vault=false`** is passed (the `api` library stays
explicit — `WithoutVault()` — so an embedder can still disable it). When disabled
the vault is absent exactly as in ADR-0069: endpoints return `503` and secret
resolution falls back to the environment references (ADR-0041 A2).

Everything else from ADR-0069 is unchanged: AES-256-GCM sealing, the sidecar
store, the `keyId` fingerprint, the value bound as AEAD additional data, the
vault-then-environment resolution order, and the admin-gated, value-free HTTP
surface. The event log, WAL, and variables still never see a secret (I6).

### Consequences

- **Positive:** the vault works on a fresh install with zero configuration — the
  Console "Secrets" panel is functional immediately. Operators who want the
  strong at-rest posture set `ATLAS_VAULT_KEY` and get exactly ADR-0069's
  behavior (Atlas persists no key). Anyone who wants no vault at all passes
  `--vault=false`. One flag, three clear postures.
- **Negative / trade-offs accepted:** in the default posture Atlas now writes a
  master key to disk (`<data-dir>/vault.key`), beside the ciphertext it protects.
  A host or backup compromise that captures the data directory captures **both**,
  so the default is encryption-at-rest against *disk theft of the sealed files
  alone*, not against full data-directory capture. This is a genuine weakening of
  ADR-0069's guarantee, accepted for the UX win and mitigated by (a) operator-key
  precedence for those who need more, (b) `0600`/`0700` permissions, and (c) a
  startup log line stating a key was generated and how to harden it. Losing or
  deleting `vault.key` makes existing secrets unrecoverable — inherent to any
  encryption-at-rest, surfaced clearly via `keyId`.
- **Follow-ups / risks to watch:** document the key file in the ops guide and
  recommend excluding it from the same backup stream as the data dir, or mounting
  it separately; the KMS/envelope backend (A4, ADR-0069 follow-up) becomes the
  real answer for deployments that cannot accept a key on disk; key rotation still
  applies to both the operator and generated key.

## Pros and cons of the options

### Option 1 — keep opt-in
- Good: strongest at-rest guarantee; Atlas holds a key only when asked.
- Bad: the feature is inert on a fresh install; the Console panel shows only "not
  configured" until the operator provisions a key and restarts.

### Option 2 — on by default with a generated key (chosen)
- Good: turnkey; operator key still wins and is still never persisted; disabling
  is one flag.
- Bad: default posture stores the key beside the ciphertext, weakening at-rest
  against full data-directory capture.

### Option 3 — on by default, refuse to start without a key
- Good: never generates a key.
- Bad: combines opt-in's provisioning friction with an on-by-default that can't
  actually run; confusing.

## Links

- amends the enablement and key-source decision of
  [ADR-0069](0069-engine-internal-encrypted-secret-vault.md) (engine-internal
  encrypted secret vault); all other parts of ADR-0069 stand
- enablement mirrors the opt-out of [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md)
- coexists with the environment-reference base (A2) and anticipated KMS backend
  (A4) of [ADR-0041](0041-connector-management-and-secret-store.md)
- honors invariants I2/I4/I6 — [`docs/architecture/invariants.md`](../architecture/invariants.md)
