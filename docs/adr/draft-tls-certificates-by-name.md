# ADR-DRAFT: Several certificates, chosen by the name a client asks for

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-18
- **Deciders:** Atlas maintainers (request of 2026-11-18 to give atlas what the wiki it
  works with was given the day before: its listener answers several host names and
  chooses the certificate by Server Name Indication)

## Context and problem statement

[ADR-0191](0191-built-in-tls-listener.md) gave the binary an optional TLS 1.3 listener
with one certificate pair, `--tls-cert` and `--tls-key`. Its `GetCertificate` ignores the
handshake: the one certificate answers every name, so a server reached under several
names needs a single certificate that lists them all. The handbook says so in as many
words — "atlas does not choose by the requested name (SNI)".

That holds while one authority certifies every name on one renewal date. It stops
holding in installations this server already has. The Console is reached under the
organisation's name and the hosted apps under an origin of their own
([ADR-0204](0204-hosted-apps-on-an-isolated-origin.md)), which is a second name and
often a second certificate, a wildcard among them. A server is reached under an internal
name certified by the organisation's own authority and under a public one certified by a
public authority, and no authority issues a certificate that spans both.

Which certificate to present is known from one thing only: the name the client asks for
in its handshake, the Server Name Indication. `crypto/tls` chooses by it for a list of
certificates, but a list is read once, and ADR-0191 owns a reload so that a renewal
needs no restart.

How does the listener serve names that are certified apart, without giving up the
reload and without a second way to configure TLS?

## Decision drivers

- **Nothing existing changes.** One pair behaves exactly as before, by the same flags,
  the same environment variables and the same chart values.
- **No second mechanism.** The reload, the both-or-neither rule and TLS 1.3 alone stay
  as ADR-0191 has them.
- **A misconfiguration stops the server**, at startup, whichever pair is wrong.
- **No new concept for the operator.** The handbook's table of TLS flags stays the
  table it is.

## Considered options

1. **One pair**, several names through one certificate (the status quo).
2. **Lists in the two flags:** `--tls-cert a.crt,b.crt --tls-key a.key,b.key`, the key at
   the same place as its certificate, chosen by SNI.
3. **Repeatable flags**, one `--tls-cert`/`--tls-key` per certificate.
4. **A directory of certificates**, every pair in it served.

## Decision outcome

Chosen option: **2 — lists in the two flags.**

- `--tls-cert` and `--tls-key` (and `ATLAS_TLS_CERT`, `ATLAS_TLS_KEY`) each take one
  path or several separated by commas. Position pairs them: the second key belongs to the
  second certificate. Lists of different lengths stop the server, naming both counts;
  one flag without the other stops it as before.
- Every pair is loaded at startup, before the data directory is touched. A wrong path or
  a key that belongs to another certificate stops the server, whichever pair it is.
- **The name chooses the certificate.** For each handshake the server presents the
  first pair, in the order given, whose certificate is made for the name the client asks
  for and whose key type the client can verify — the choice `crypto/tls` makes for a list
  (`ClientHelloInfo.SupportsCertificate`), wildcards as certificates define them. Two
  pairs for one name with keys of different kinds are therefore chosen by what the client
  supports.
- **The first pair answers everything else:** a name no certificate is made for, and a
  client that names none because it asks by address. That is what a single pair answered
  everybody with. The client decides whether to trust it; refusing the handshake instead
  would turn a certificate warning into a connection error that names nothing, and would
  break a load balancer's health check by address.
- **Each pair is reloaded on its own**, by the reloader ADR-0191 built, with its two
  log events unchanged. A renewal of one pair leaves the others in service.
- **The name chooses nothing else.** Every name reaches the same server; which origin a
  request belongs to is still decided where it was (ADR-0204, `--external-url`). This is
  not virtual hosting.
- With one pair nothing is asked of the handshake at all: the code path is the one
  ADR-0191 shipped.

### Scale-out readiness

The choice is a pure function of the handshake and the files on this node; it holds no
state beyond the certificates each node loads from its own disk. Several nodes are
configured alike, as they already are for one pair. Nothing here touches partition
state, routing or the log.

### Consequences

- **Positive:** names certified apart are served from one listener; a hosted-apps origin
  with its own wildcard certificate needs no proxy in front; one pair is untouched.
- **Negative / trade-offs accepted:** pairing by position is easy to get wrong with many
  files, and is caught at startup only when a key does not belong to its certificate —
  two valid pairs in the wrong order are both served, each under its own name, so the
  order matters only for the names no certificate covers. Each handshake costs two file
  lookups per pair. A client asking for an unknown name is shown a certificate for
  another name rather than refused.
- **Follow-ups / risks to watch:** the chart mounts one `kubernetes.io/tls` Secret;
  several pairs in a pod need several mounts, which the chart does not render yet.

## Pros and cons of the options

### Option 1 — one pair
- Good: nothing to build; one certificate is one thing to renew.
- Bad: names certified by different authorities, or on different dates, cannot be
  served, and a wildcard for the apps' origin has to be merged with the Console's name
  by whoever issues certificates.

### Option 2 — lists in the two flags (chosen)
- Good: the same two flags, variables and table; a single path means what it meant.
- Bad: position is the only pairing.

### Option 3 — repeatable flags
- Good: reads naturally on a command line, and is what the wiki's binary takes.
- Bad: an environment variable cannot repeat, so it would need the list anyway, and the
  flags are defined as strings that a test reads back out of the source to hold the
  handbook's table to them; two spellings of one setting is the larger change.

### Option 4 — a directory
- Good: nothing to pair by hand; renewal tools write directories.
- Bad: a naming convention to learn, a rule for which certificate answers unknown names,
  and a stray file changes what the server presents.

## Links

- amends [ADR-0191](0191-built-in-tls-listener.md) — its listener, with the choice among
  several pairs added; its reload, its TLS 1.3 and its both-or-neither rule unchanged
- relates to [ADR-0204](0204-hosted-apps-on-an-isolated-origin.md) — the second name an
  installation most often has
- relates to ADR-draft-https-redirect-and-hsts — the redirect keeps the name asked for
