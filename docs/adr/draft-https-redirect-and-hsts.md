# ADR-DRAFT: A redirect from plain HTTP and a transport policy, on request

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-18
- **Deciders:** Atlas maintainers (request of 2026-11-18 to give atlas what the wiki it
  works with was given the day before)
- **Open question:** whether the redirect listener should answer a certificate
  authority's HTTP challenge (`/.well-known/acme-challenge/`) for a renewal tool that
  needs port 80 beside the server.
- **Question checked:** 2026-11

## Context and problem statement

[ADR-0191](0191-built-in-tls-listener.md) lets the binary terminate TLS and lists, under
*What is not covered*, "HTTP→HTTPS redirect and HSTS. A redirect needs a listener on :80
and an opinion about the public name; HSTS is a header the proxy sets today and a
footgun to enable by default on a hostname Atlas does not own." `docs/install.md` says
the same from the operator's side: the proxy is where `Strict-Transport-Security` is
set, atlas does not send it.

Both sentences are right about a default and leave an installation without a proxy with
two gaps. A person who types the server's name without `https://` reaches port 80, where
nothing answers. And a browser that reached the server over HTTPS still sends a later
`http://` request in the clear, where whoever sits on the path can answer in the
server's place; `Strict-Transport-Security` (RFC 6797) is what closes that, by having
the browser turn every later request to the host into HTTPS for a stated time.

The reasons ADR-0191 gave still stand. The policy binds the host name, not the port: a
browser that holds it turns `http://host:5000` into `https://host:5000`, which fails
where another service under the same name answers plain HTTP. It lasts in each browser
until its time runs out, whatever the server does afterwards. And port 80 is one port of
the host, which another program may hold.

The record this one follows was left out of the first slice, not refused. How does the
binary send readers of plain HTTP to HTTPS and keep them there, without binding hosts
and ports it was not given?

## Decision drivers

- **Nothing existing changes.** A server started as before sends no new header and
  opens no new port.
- **Nothing beyond the server by default** (ADR-0191's reason, kept).
- **Every setting has a way back.**
- **Several names keep their own** (ADR-draft-tls-certificates-by-name).
- **A request in the clear is not made worse:** a redirect must not teach a client to
  keep sending a credential over plain HTTP.
- **Encryption is not authorization** (ADR-0191): the port open to plain HTTP reaches
  nothing of the server.

## Considered options

1. **Both as settings, off by default:** `--redirect-addr` and `--hsts-max-age`.
2. **Both on whenever the binary terminates TLS.**
3. **The redirect alone.**
4. **Neither**, as ADR-0191 left it.

## Decision outcome

Chosen option: **1.**

### The redirect

- `atlas serve --redirect-addr ADDR` (or `ATLAS_REDIRECT_ADDR`), e.g. `:80`: one more
  listener beside `--addr` and the loopback one. It needs `--tls-cert` and `--tls-key`;
  without them the server does not start, because nothing would answer the HTTPS it
  names.
- **GET and HEAD** are answered with `302 Found` to `https://<host><path>?<query>`: the
  host the request names, without its port, so every name keeps its own. A host that is
  not a plain name or address is refused.
- **The port of HTTPS** is 443 unless `--external-url` is an `https` origin that names
  another. The port of `--addr` does not tell: a container, a chart or a load balancer
  publishes it under another.
- **Temporary, not permanent.** A browser keeps a permanent redirect without an end; it
  would outlast a server that stops terminating TLS, with nothing to withdraw it. What
  makes HTTPS stick is the policy below, which has an end and a way back.
- **Every other method is refused** with `400`. Its body and credentials have crossed
  the network in the clear already, and a client that is redirected would send them so
  again each time, unnoticed.
- **Nothing else is answered there:** no API, no Console, no `/healthz`. The handler is
  the redirect, not the server's with a filter in front.
- It is bound when serving starts, as `--addr` is, under the same PROXY protocol reading
  (`--trusted-proxies`, ADR-0448), and it shares the one grace period and the one fate
  of `serveUntil`: any listener failing ends the process.

### The transport policy

- `--hsts-max-age SECONDS` (or `ATLAS_HSTS_MAX_AGE`): `Strict-Transport-Security:
  max-age=<seconds>` with every answer to a request that arrived over HTTPS — this
  server's own TLS, or a proxy that ended it and says so in `X-Forwarded-Proto`, the
  rule a cookie's `Secure` attribute already follows (`arrivedOverHTTPS`). Over plain
  HTTP it is left out, where a browser ignores it; the loopback listener therefore never
  carries it.
- **Never `includeSubDomains`, never `preload`:** both bind hosts this server is not.
- **The way back** is `--hsts-max-age 0`, which tells each browser to forget the policy
  at its next visit over HTTPS; only then should TLS be taken away.
- A value that is no whole number from zero up stops the server.
- A browser ignores the header on a connection whose certificate it did not accept, so
  trying a certificate that is not trusted binds nobody.

The startup line `server.listening` carries `redirect_addr` and `hsts`, empty where
unset.

### Scale-out readiness

Both are stateless per request and per node. The redirect is a pure function of the
request line and one flag; the policy is a constant header. Nodes behind one name must
be configured alike, or a browser learns the policy from one and meets plain HTTP on
another — the same rule that already holds for their certificates. Nothing touches
partition state, routing or the log.

### Consequences

- **Positive:** a person who types the bare name arrives; with the policy set, later
  requests never leave the browser in the clear; an installation that sets neither is
  untouched; ADR-0191's "a proxy sets it" stops being the only answer.
- **Negative / trade-offs accepted:** the policy binds every port of the host name in
  each browser for up to the time set — which is why it is a setting, documented with
  that sentence beside it. A browser's first request is still in the clear; only a
  preload list would close that, which is left out. The redirect trusts the host a
  request names for the address it sends that same client to. Three listeners instead of
  two. A renewal tool that needs port 80 cannot share it.
- **Follow-ups / risks to watch:** the open question; the chart renders neither flag.

## Pros and cons of the options

### Option 1 — settings, off by default (chosen)
- Good: what reaches beyond the server happens only when whoever answers for the host
  asks for it, and each can be taken back.
- Bad: an installation that would be safer with both gets neither until someone sets
  them.

### Option 2 — on with TLS
- Good: the safest outcome without reading anything, for a host that serves only this.
- Bad: a host name shared with a plain-HTTP service loses it in every browser that
  visited; port 80 may be held, and the server would stop starting on upgrade — both
  against ADR-0191's "nothing existing may break".

### Option 3 — the redirect alone
- Good: nothing that outlasts the server.
- Bad: a redirect without the policy can be answered by anyone on the path, every time.

### Option 4 — neither
- Good: no new surface; a proxy does both well.
- Bad: the single binary reaches HTTPS but not the people who type its name.

## Links

- amends [ADR-0191](0191-built-in-tls-listener.md) — takes up what it left uncovered,
  keeping its reason as the reason both are opt-in
- relates to [ADR-0448](0448-trusted-proxies.md) — the redirect listener reads the PROXY
  protocol from the same listed peers
- relates to ADR-draft-tls-certificates-by-name — a redirect keeps the name asked for
