# ADR-0154: Generic LDAP connector

- **Status:** Proposed
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

## Context and problem statement

Identity provisioning processes must read from and write to directory servers —
Active Directory, OpenLDAP, 389 Directory Server, Oracle Directory — which almost all
speak **LDAP**, not HTTP. The REST and SCIM connectors (ADR-0067/0153) cover
HTTP/JSON APIs, but a joiner/mover/leaver process cannot create an account in AD,
reset a password, or search a directory through them. LDAP is a connection-oriented
binary protocol (ASN.1/BER over TCP, optionally TLS), so it needs a genuinely
different client from the HTTP connectors.

The question: how should a BPMN process perform an LDAP directory operation
(search / add / modify / delete / set-password) against a server, keeping the engine's
durability and single-writer invariants and the credentials-never-in-the-model rule?

## Decision drivers

- **Cover the directory operations provisioning needs:** read (search), write
  (add/modify/delete), and password set — the capabilities the wishlist marks for
  this connector.
- **Credentials never in the model.** The bind password must be a *reference* to a
  server-side secret (ADR-0041); only the server URL, bind DN, and target/base DN are
  model data.
- **Stay on the connector seam.** A `TypeConnectorTask` → reserved `*JobTypeIndex` →
  in-process worker, off the run loop and after fsync (ADR-0007/0067), bounded by the
  shared connector budget (ADR-0149).
- **Testable without a live directory.** The protocol-touching code must be isolable
  so the worker and the request translation are unit-tested.

## Considered options

1. **Hand-roll an LDAP client** over `net`/`crypto/tls` (implement BER + the LDAP PDUs).
2. **Vendor `github.com/go-ldap/ldap/v3`** — the de-facto-standard pure-Go LDAP
   library — behind a small connector-owned interface.
3. **Shell out to `ldapsearch`/`ldapmodify`** via the script connector (ADR-0047).

## Decision outcome

Chosen option: **"vendor go-ldap behind a connector interface"** (option 2).
Implementing LDAP/BER by hand (option 1) is a large, security-sensitive surface for no
benefit; shelling out (option 3) needs the CLI tools present on the host, loses typed
errors, and passes credentials through argv. A focused third-party library per
connector already has precedent in the tree (goquery for the web-scraping connector),
and go-ldap is pure Go, so the single-binary posture (ADR-0011) holds.

Concretely:

- Reserved job type `io.atlas.ldap` at `LdapJobTypeIndex == 17`, one in-process
  worker per deployed process.
- `<atlas:ldapConnector url bindDN bindSecret startTLS operation dn baseDN filter
  scope entryVariable newPassword resultVariable>` on a service task. `url`/`bindDN`/
  `dn`/`baseDN`/`filter`/`newPassword` are literal-or-FEEL values (the ADR-0067 fx
  toggle) evaluated over the instance's variables at call time; `bindSecret` names the
  server-side bind-password secret (empty → an anonymous bind); `operation` is one of
  search/add/modify/delete/modify-password.
- The worker dials (bounded by `nettimeout.Default`), optionally STARTTLS-upgrades,
  binds, performs the operation, and closes — per job. A **search** writes its entries
  (DN + multi-valued attributes) into `resultVariable` as a JSON array; **add/modify**
  take the entry's attributes from a named JSON-object variable (`entryVariable`),
  coercing scalars and arrays into LDAP's multi-valued form; **modify-password** uses
  the RFC 3062 Password Modify extended operation.
- go-ldap is confined to `client.go` behind a `Dialer`/`Conn` interface; the worker
  and the request-building/response-mapping helpers are unit-tested with a fake
  dialer, and the network methods are exercised by integration.

### Consequences

- **Positive:** provisioning processes can manage directory accounts natively; the
  bind password stays a reference; the call is bounded; the connector is testable
  without a live server for everything except the thin network methods.
- **Negative / trade-offs accepted:** three new dependencies enter go.sum (go-ldap and
  its asn1-ber and go-ntlmssp transitives); a connection is dialt/bound per job (no
  pooling yet); **delta/paged** search (per the wishlist) is not implemented; modify is
  a whole-attribute *replace*, not per-value add/delete; the new password flows through
  a process variable (inherent to provisioning, mirroring ADR-0123's user connector).
- **Follow-ups / risks to watch:** connection pooling / reuse; paged results and a
  sync/delta cookie; per-value modify operations; client-certificate (mTLS) bind;
  a Modeler palette entry for the extension.

## Pros and cons of the options

### Option 1 — hand-rolled LDAP client
- Good: no dependency.
- Bad: large security-sensitive protocol surface to build and maintain for no gain.

### Option 2 — vendor go-ldap (chosen)
- Good: pure-Go, de-facto standard, typed errors; confined behind an interface;
  precedent (goquery).
- Bad: adds go-ldap + two transitive dependencies.

### Option 3 — shell out to ldap* CLIs
- Good: reuses the script connector.
- Bad: needs the tools on the host; untyped text errors; credentials via argv.

## Links

- relates to ADR-0067 (service-task connector catalog / REST connector)
- relates to ADR-0153 (SCIM connector) — the HTTP sibling for identity provisioning
- relates to ADR-0041 (connector management and secret store)
- relates to ADR-0149 (bounded connector call budget) — the dial/operation timeout
- relates to ADR-0007 (job protocol durability)
