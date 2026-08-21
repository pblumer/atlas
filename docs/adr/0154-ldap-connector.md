# ADR-0154: Generic LDAP connector

- **Status:** Proposed (amended 2026-08-21 — paging, per-value modify, a
  client-certificate bind and connection pooling; see the amendment note below)

> **Amendment (2026-08-21): hardening.** Four of the follow-ups below are done, and
> two of them change the shape of what the connector does rather than only adding to
> it.
>
> - **A search is paged and bounded.** `pageSize` drives the simple paged-results
>   control (RFC 2696), so a directory's administrative size limit no longer refuses
>   a legitimate search — an author who has never met that limit has no reason to know
>   the control exists, so paging defaults *on* at 500. `maxEntries` caps what may
>   land in a process variable and defaults to 1000; exceeding it **fails** rather
>   than truncating, for the reason [ADR-draft-generic-sql-connector](draft-generic-sql-connector.md) gives
>   about rows: a short result set is a wrong answer, not a partial one. `0` is the
>   authored way to say unbounded for either. The compiler writes the effective value
>   into the compiled process, so the runtime interprets nothing (I5).
>
>   **This changes behaviour for an existing model**: a subtree search that returned
>   more than a thousand entries used to succeed and now fails. That is deliberate —
>   an unbounded search into a process variable is the failure mode being hardened
>   against — and `maxEntries="0"` restores the old behaviour explicitly.
>
> - **Modify can change individual values.** The original `modify` replaced an
>   attribute wholesale, which is the wrong shape for a multi-valued attribute more
>   than one process writes: adding a member to a group is not a statement about
>   everyone else's membership. `add-values` and `delete-values` join it, sharing the
>   authored attribute object and differing only in the change operation. The three
>   are expressed with the same `Mod` shape the AD connector uses, so the two
>   directory connectors describe a change the same way.
>
> - **A client-certificate bind.** `clientCertSecret` names a secret holding one PEM
>   bundle — certificate and private key together, because splitting them would be two
>   things to rotate together and one more way to get it half-done. With a bind DN the
>   certificate is transport only; **with no bind DN the certificate is the identity**
>   and the connector binds SASL EXTERNAL, because presenting a certificate and then
>   staying anonymous would authenticate nothing. An optional `CACert` covers a
>   directory behind a private CA.
>
>   `Dial` now takes a `DialOptions` struct: the parameter list had reached four and
>   TLS added two more, past which a call site is a row of positional booleans nobody
>   can read.
>
> - **Connections are pooled.** A connection per operation is the right default and
>   the wrong steady state: a joiner run over a few hundred accounts pays a TCP
>   handshake, a TLS handshake and a bind for every entry it touches, against a server
>   that would happily have kept the first connection. `ldap.Pool` sits in front of the
>   dialer, and the server holds one and closes it at shutdown.
>
>   **The key is the whole credential** — URL, STARTTLS, bind DN, bind password, client
>   certificate, CA — hashed. A connection bound as one identity therefore cannot be
>   handed to a job asking for another, and a rotated password does not reuse a
>   connection bound with the old one: the fingerprint moves with it and the stale
>   entries age out unused. The parts are length-prefixed before hashing so a bind DN
>   ending where a password begins cannot collide with the other split of the same
>   bytes.
>
>   **Any error retires the connection.** LDAP does not reliably distinguish "your
>   filter was wrong" from "this socket is gone", and betting a later job on the
>   difference is not worth the saved handshake. **The pool also does not retry**: a
>   connection the server closed while it sat idle fails its next operation, and that
>   job takes the ordinary retry-then-incident path (ADR-0061). Retrying inside the
>   connector would mean re-sending a write whose outcome is unknown, which is a worse
>   failure than the one it would paper over. The idle window is 30 seconds for the
>   same reason — long enough to carry a burst, short enough that the stale case stays
>   rare.
>
> **Still open from the list below:** a sync/delta cookie. The
> delta read is not one feature but two vendor protocols — DirSync for Active
> Directory, RFC 4533 content sync elsewhere — and the AD half belongs with the AD
> connector for the reason [ADR-0166](0166-active-directory-connector.md) gives:
> a vendor's own mechanism deserves a named operation rather than a generic one that
> guesses which server it is talking to.

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
  dialer. The network methods themselves are covered against a minimal in-process
  LDAP server (`testdirectory_test.go`) that speaks the six operations the connector
  issues, so the adapter is tested on a real socket without a live directory. That
  server answers `asn1-ber` packets directly, which promotes asn1-ber from an
  indirect dependency to a direct (test-only) one; no new module enters `go.sum`.

### Consequences

- **Positive:** provisioning processes can manage directory accounts natively; the
  bind password stays a reference; the call is bounded; the connector is testable
  without a live server, the network methods included.
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
