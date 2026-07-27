# ADR-0069: An engine-internal encrypted secret vault (ADR-0041 option A3)

- **Status:** Accepted
- **Date:** 2026-07-27
- **Deciders:** Atlas engine team

## Context and problem statement

[ADR-0041](0041-connector-management-and-secret-store.md) settled the secret
model for managed connectors: the engine persists only a `credentialsRef`, and a
resolver turns that reference into a value at runtime. It chose **A2 (environment
/ mounted-secret references)** as the base — `credentialsRef: gmail_ops` →
`ATLAS_CONNECTOR_GMAIL_OPS_TOKEN` — and it *explicitly deferred* one option to a
future ADR of its own:

> **encrypted in-engine store (A3, optional follow-up):** for single-node
> convenience, the Console writes a token, the engine encrypts it at rest and
> resolves it here. This is the *only* variant where Atlas custodies a secret,
> which is why it is opt-in and deferred to its own ADR (key management,
> rotation).

That deferral is what this ADR closes. The operational gap A2 leaves is real: in
the base build an operator must provision every secret out-of-band (an env var
per credential, a restart to add one), and there is no in-product way to set a
credential from the Console. Teams running a single Atlas node — the common
case — want to type a token into the UI and have it work, without standing up a
Vault/cloud secret manager (A4) or hand-editing the process environment.

The question this ADR answers is therefore **not** "should Atlas hold secrets" —
ADR-0041 already decided that A3 is a legitimate, opt-in posture — but "**how**
does an engine-internal secret store hold them safely": where the encryption key
lives, what is written to disk, how it plugs into the existing `credentialsRef`
resolution, and how none of it leaks into the event log.

## Decision drivers

- **Secrets are never events, model data, log records, or variables (I6).** This
  is ADR-0041's load-bearing rule and it is not relaxed here. The event log and
  the WAL stay replay-safe and secret-free; a sealed secret lives only in a
  sidecar store, and its plaintext exists only in worker memory at call time.
- **Least *key* custody.** The engine may now custody a *ciphertext* at rest, but
  it should custody the *key* as little as possible. The master key comes from the
  deployment environment; the engine never generates it, and never writes it to
  its own data directory.
- **Opt-in, explicit enablement.** A2 stays the default. The vault does nothing
  until a master key is configured — no key, no vault, and writes are refused
  rather than silently storing recoverable plaintext.
- **Reuse the existing seam.** A3 is a new *resolver backend* behind the same
  `credentialsRef` indirection ADR-0041 defined, not a new concept. A connector
  keeps naming a reference; only where the value comes from changes.
- **No new crypto stack, no CGO ([ADR-0010](0010-go-and-no-cgo.md)).** Use the Go
  standard library's authenticated encryption (AES-256-GCM); do not vendor a
  crypto library or add a native dependency.
- **Durable before visible (I2), off the hot path (I1/I4).** Sealing and
  unsealing are side effects: they run in request/worker handling on the
  run-loop's store-owning goroutine, never inside `applyToState`, and the sidecar
  write is atomic + fsynced before the write is acknowledged.

## Considered options

**Where the master key comes from:**

1. **From the environment / a mounted file (chosen).** `ATLAS_VAULT_KEY` (a 32-byte
   key, base64 or hex) or `ATLAS_VAULT_KEY_FILE` pointing at a mounted secret. The
   engine reads it at startup, holds it in memory, and never persists it. This is
   the same "the platform provisions the root secret" posture A2 already relies
   on, narrowed to a single key.
2. **Engine-generated key persisted next to the data.** Turnkey (nothing to
   provision) but the key sits on the same disk as the ciphertext it protects —
   encryption-at-rest in name only. Rejected: it defeats the point.
3. **A KMS / external key manager (envelope encryption).** Strongest, but an
   operational dependency that A3's whole reason for being (single-node
   convenience) is meant to avoid. Kept as a future backend, not the base.

**What is written to disk:**

A. **Plaintext in a config file** — this is A1 from ADR-0041, already rejected
   there. B. **AES-256-GCM ciphertext in a sidecar store (chosen)**: per secret a
   record `{name, keyId, nonce, ciphertext, createdAt, updatedAt}`, one file per
   secret, mirroring the `connectorStore` sidecar (ADR-0019). The key never
   appears; `keyId` is a non-secret fingerprint of the master key so a
   wrong-key/rotated-key situation is detectable rather than surfacing as a raw
   GCM auth failure.

**How it resolves:**

The vault is consulted **first** for a `credentialsRef`; if it holds no secret of
that name, resolution falls through to the A2 environment lookup. So the two
backends coexist and an operator moves a credential from env to vault (or back)
without touching any model.

## Decision outcome

Chosen: **an engine-internal encrypted secret vault — option 1 (key from the
environment) + option B (AES-256-GCM ciphertext in a sidecar store) — wired as a
resolver backend that precedes the existing environment lookup.**

Concretely:

- **A `vault` seam in the `api` package.** A `secretVault` owns a sidecar store
  (one JSON file per secret, hex-named, atomic write + dir fsync — identical to
  `connectorStore`) and an in-memory AES-256-GCM cipher built from the configured
  master key. It exposes `Set(name, value)`, `Get(name) (value, ok)`,
  `Delete(name)`, and `List()` (names + metadata only). `Set` seals with a fresh
  random 96-bit nonce; `Get` opens with the record's nonce and verifies `keyId`.
- **Enablement is explicit.** With no `ATLAS_VAULT_KEY`/`_FILE` the vault is
  *absent*: the CRUD endpoints return `503 vault not configured`, `Set` is never
  reached, and `resolveConnectorSecret` behaves exactly as today (env only). This
  keeps A2 the untouched default and makes A3 a deliberate operator action.
- **Resolution precedence.** `resolveConnectorSecret(ref)` first asks the vault
  (`Get`) and, only on a miss, reads `ATLAS_CONNECTOR_<REF>_TOKEN`. The decrypted
  value lives solely in the returned string in the caller's memory at call time —
  never written to a variable, the WAL, or an event (I6).
- **HTTP surface, admin-guarded ([ADR-0044](0044-user-management-and-authentication-boundary.md)).**
  `GET /api/v1/secrets` lists names + `{keyId, createdAt, updatedAt}` and **never
  a value**; `PUT /api/v1/secrets/{name}` seals and stores the request body's
  value; `DELETE /api/v1/secrets/{name}` removes it. There is deliberately **no
  endpoint that reads a secret back** — a vault takes secrets in and resolves them
  internally; it does not hand them out. Guarded identically to the connector
  endpoints, and the run-loop goroutine owns the store (`s.do`), so no locking.
- **Crypto.** AES-256-GCM from `crypto/aes` + `crypto/cipher` (no CGO). `keyId` is
  the first bytes of `SHA-256(masterKey)` — enough to detect a key mismatch,
  useless to an attacker. A single active key in this slice; rotation (re-seal
  under a new key) is a documented follow-up, and `keyId` is what makes it
  possible.

The **event log, WAL, and process variables are unchanged**: no new value type,
no `applyToState` change, no processor change. The vault is entirely a
side-effect-phase, sidecar-store concern, exactly like the connector registry.

### Consequences

- **Positive:** an operator can set a connector credential from the Console/API on
  a single node without provisioning env vars or an external secret manager, while
  the engine still writes no plaintext secret anywhere and holds no key on disk.
  A3 slots in behind the `credentialsRef` indirection with no model change and no
  engine-core change; env (A2) and, later, a KMS backend (A4) coexist behind the
  same seam. The log stays replay-safe and secret-free (I6).
- **Negative / trade-offs accepted:** Atlas now custodies ciphertext at rest and a
  key in memory — a larger custody surface than A2, accepted because it is opt-in
  and the key stays off the data disk. A lost/rotated master key makes existing
  ciphertext unrecoverable (surfaced clearly via `keyId`), which is inherent to
  encryption-at-rest. Single active key in this slice: rotation is manual
  (re-`PUT` each secret under a new key) until the rotation follow-up lands.
- **Follow-ups / risks to watch:** key rotation / re-seal tooling using `keyId`; a
  KMS/envelope backend (A4) as an additional resolver; extending the vault to the
  DMN-resolver token and other `ATLAS_*_TOKEN` references; a Console
  Organization → Secrets panel over the CRUD endpoints; audit logging of
  set/delete (not the value); optional memory hygiene (zeroing plaintext buffers).

## Pros and cons of the options

### Key from the environment / mounted file (chosen)
- Good: engine never persists the key; same provisioning posture as A2; trivial to
  reason about against I6; standard in container/orchestrator deployments.
- Bad: operator must still provide one root secret out-of-band; rotation is manual
  in this slice.

### Engine-generated key on the data disk (rejected)
- Good: fully turnkey, nothing to provision.
- Bad: key and ciphertext share a disk — not real encryption-at-rest; a single
  disk compromise yields both. Defeats the driver.

### KMS / envelope encryption (deferred)
- Good: strongest key hygiene, centralized rotation.
- Bad: an external dependency that A3's single-node convenience goal exists to
  avoid; fits as an additional backend behind the same reference, not the base.

## Links

- closes the deferred **A3** of [ADR-0041](0041-connector-management-and-secret-store.md)
  (connector management and the secret store); coexists with its A2 base and
  anticipated A4 backend behind the one `credentialsRef` indirection
- honors invariants I1/I2/I4/I6 — [`docs/architecture/invariants.md`](../architecture/invariants.md)
- no CGO, standard-library crypto only ([ADR-0010](0010-go-and-no-cgo.md))
- admin-guarded like other operator surfaces ([ADR-0044](0044-user-management-and-authentication-boundary.md))
- sidecar persistence mirrors [ADR-0019](0019-durable-deployments.md) and the connector store
