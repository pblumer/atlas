# ADR-DRAFT: The operator's CA bundle also counts for the wiki

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-18
- **Deciders:** Atlas maintainers (request of 2026-11-18 to give atlas what the wiki it
  works with was given the day before, where the wiki now trusts an organisation's own
  certificate authority for its calls to atlas)
- **Open question:** whether other Worker Types whose endpoint is the operator's own
  server, not a third party's, should take the bundle too, and by which rule one tells
  them apart.
- **Question checked:** 2026-11

## Context and problem statement

`--tls-ca` adds an operator's certificate authorities to the host's roots.
[ADR-0191](0191-built-in-tls-listener.md) drew its reach narrowly and said why: it
"reaches no further than Atlas talking to Atlas — a Worker Type calling Jira, a mail
server or Graph keeps the machine's roots, because that endpoint is somebody else's."
`atlas serve` uses it for deployment targets, `atlas worker` and `atlas mcp` for their
hop to the server.

Since then atlas works with a wiki of the same operator in two places. The
wikiwikibangbang Worker Type writes tasks and pages into it
([ADR-0498](0498-wikiwikibangbang-worker.md)), from a worker process. The
catalogue reads its pages as content sources
([ADR-0491](0491-service-content-from-an-external-source.md)), from the server. That wiki can now
terminate TLS itself, and inside an organisation its certificate comes from the
organisation's own authority — the one `--tls-ca` already names for the peer atlas.

Both calls verify against the host's roots alone. Against such a wiki every job of the
Worker Type and every read of the catalogue fails at verification, and in the container
image the host's roots cannot be added to without building another image, which is the
argument ADR-0191 made for the flag in the first place. The wiki settled the mirror
image the day before: it trusts a bundle for its calls to process engines and for
nothing else it calls.

Whose certificate may the bundle vouch for?

## Decision drivers

- **The rule ADR-0191 stated, kept:** the bundle is for the operator's own servers, not
  for third parties.
- **Added, never replacing; no way around verification.**
- **Nothing existing changes** for an installation whose wiki has a publicly issued
  certificate, or none.
- **A supervised worker must not need hand configuration** the engine could give it.
- **One bundle per installation**, not one per place it is used.

## Considered options

1. **`--tls-ca` also counts for the wiki:** the Worker Type and the content sources.
2. **A bundle of its own** for the wiki (`--wiki-tls-ca`).
3. **A CA per Worker**, in the Worker's configuration.
4. **Leave it to the host's store.**

## Decision outcome

Chosen option: **1.**

- **The Worker Type.** A worker serving wikiwikibangbang Workers verifies each wiki
  against the host's roots and the bundle of `--tls-ca` (`ATLAS_TLS_CA`). It keeps the
  bound every Worker's outbound call has and the installation's outbound proxy
  (ADR-0464); only whom it trusts changes. A bundle that cannot be read stops the worker
  at startup.
- **A supervised worker is handed the bundle.** Where `atlas serve` was given `--tls-ca`
  and holds wikis, the wikiwikibangbang worker it starts gets the bundle's path under
  `ATLAS_TLS_CA`, the name its own flag defaults to. Without wikis nothing is handed
  over; a bundle alone starts no worker. The child reaches the server over the plaintext
  loopback hop, so there the bundle serves the wikis alone.
- **The content sources.** Where `atlas serve` was given `--tls-ca`, the catalogue's
  content sources read with a clone of the process-wide transport that trusts the
  bundle besides the host's roots. The process-wide transport itself is not changed, so
  no other outbound call of the server starts trusting it.
- **Nobody else.** REST, mail, Graph, Jira and every other Worker Type keep the host's
  roots, as ADR-0191 says. The flag's help, the handbook and `docs/install.md` name the
  wiki beside the peer atlas.
- With no bundle, every client is the one it was.

### Scale-out readiness

The bundle is configuration, read from a file on each node; it holds no state and no
node-local truth another node depends on. Workers are processes that lease jobs from
whichever node, each verifying with the bundle it was started with, so nodes and workers
are configured alike, as they already are for the peer atlas. Nothing touches partition
state, routing or the log.

### Consequences

- **Positive:** atlas and the wiki of one organisation talk over TLS in both directions
  with the organisation's own certificates, in the container image too, with the one
  bundle an operator already has.
- **Negative / trade-offs accepted:** whoever holds an authority in the bundle can
  present a wiki's name to atlas and so receive that Worker's token, as with every
  authority a host trusts. The bundle counts for every wiki an installation holds, also
  one that is not the operator's own; a Worker's endpoint is entered by an administrator,
  who is the one trusted with that. `--tls-ca` now means "the operator's own servers",
  which is one sentence longer than "another atlas".
- **Follow-ups / risks to watch:** the open question.

## Pros and cons of the options

### Option 1 — `--tls-ca` also counts for the wiki (chosen)
- Good: no new setting; the flag is already on the three commands that need it; the
  reason is ADR-0191's own.
- Bad: one bundle for the peer atlas and the wiki, with no way to trust it for one only.

### Option 2 — a bundle of its own
- Good: the narrowest trust per call, and what the wiki's binary does for its calls to
  atlas.
- Bad: a second file for what is, inside one organisation, the same authority; three
  more places for a flag; the wiki chose a name of its own because its own flag was
  already decided not to be read by its server, which has no parallel here.

### Option 3 — a CA per Worker
- Good: trust travels with the endpoint it is for; LDAP's Worker Type does this.
- Bad: a certificate in every Worker record and every content source, entered in the
  Console, for a bundle the operator already gave the process.

### Option 4 — the host's store
- Good: one store per machine.
- Bad: the released image cannot be added to, which is where ADR-0191 started.

## Links

- amends [ADR-0191](0191-built-in-tls-listener.md) — the reach of `--tls-ca`, by its own
  rule
- relates to [ADR-0498](0498-wikiwikibangbang-worker.md) and
  [ADR-0491](0491-service-content-from-an-external-source.md) — the two callers
- relates to [ADR-0464](0464-windows-system-proxy.md) — the proxy both clients keep
