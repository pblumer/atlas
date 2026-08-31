# ADR-0191: TLS 1.3 in the binary — an optional listener with operator-supplied certificates

- **Status:** Accepted (2026-08-31: implemented — the optional TLS 1.3 listener, the
  plaintext loopback hop, and `--tls-ca` for the client side, which is how the first
  of the three questions below was answered; see the acceptance note)
- **Date:** 2026-08-26
- **Deciders:** Atlas maintainers

> **Amendment (2026-08-31): three questions this record leaves open.** The decision below
> stands as written and none of what follows reverses it. But an implementer reading it
> hits three things it does not answer, and the first of them decides whether the feature
> delivers what it exists for.
>
> - **The trust anchor on the client side is undecided — and that is the motivating
>   case.** This record settles how Atlas *serves* TLS and says nothing about how Atlas
>   *verifies* it. The promotion path it exists to unblock is a client: `pushBundle` and
>   `fillRemoteStatus` call `http.DefaultClient.Do` in `api/promote.go`, and `atlas
>   worker --server` builds a bare `http.Client` in `worker/worker.go`. Both verify
>   against the host's system roots and nothing else. The on-prem pair this feature is
>   aimed at usually gets its certificate from an internal CA, so with the listener
>   turned on, `validateTargetURL` accepts the `https://` URL and the request then fails
>   at verification instead — a worse experience than today's refusal, because it fails
>   later and further from the cause. Skip-verify is refused twice over (ADR-0129, and
>   the comment in `api/targetstore.go` this record quotes), so the anchor has to come
>   from somewhere. Two defensible answers: a `--tls-ca` / `ATLAS_TLS_CA` PEM bundle
>   that *adds* to the system pool for the Atlas-to-Atlas and worker-to-server clients —
>   added, never replacing, and deliberately not a global trust override for the REST,
>   mail or Graph connectors, which reach third-party endpoints and own their trust
>   separately; or the explicit decision that this belongs to the host, which then has
>   to be documented as such, container included (the runtime image is
>   `debian:bookworm-slim` with `ca-certificates`, so it means a mount into
>   `/usr/local/share/ca-certificates` and `update-ca-certificates`, not a flag). Either
>   is fine. Neither being chosen leaves the ADR-0129 gap open in exactly the
>   deployments this record names.
>
> - **The loopback listener has to be bound before the handler is built, and the startup
>   log changes with it.** In `runServe`, `loopbackURL(addr)` is consumed by the MCP
>   loopback client and `selfURL(addr)` by the supervised workers — both while the
>   `api.New` options are being assembled, both before anything is bound and before the
>   one `http.Server` is constructed. With the loopback port ephemeral, those two
>   helpers stop being pure functions of `--addr`: the plaintext listener must be bound
>   first (`net.Listen` on `127.0.0.1:0`, port read back off the listener), its address
>   carried into both call sites, and only then the handler built and the TLS listener
>   served on `--addr`. Two consequences belong in the decision rather than in whoever
>   writes it: the two servers share one `--shutdown-timeout` and one error channel, and
>   either listener failing to serve ends the process, because a server that reached
>   half its interfaces is worse than one that stopped; and every URL in the
>   `server.listening`, `server.docs_enabled` and `server.metrics_enabled` lines is
>   derived from `loopbackURL(addr)` today, which after this change would print an
>   ephemeral port nobody can use. Those lines should name the reachable origin —
>   `https://` on `--addr`, or `--external-url` where it is set — and the loopback
>   listener should appear as the internal detail it is, if at all.
>
> - **The chart's probes break, and the certificate still has to reach the pod.** The
>   liveness, readiness and startup probes in `deploy/helm/atlas/values.yaml` are
>   `httpGet` with no `scheme`, which means HTTP, against the service port. Turn TLS on
>   for `--addr` and the kubelet speaks plaintext to a TLS listener; the loopback
>   listener is on `127.0.0.1` by design and unreachable from outside the container, so
>   there is nothing to fall back to. The chart needs `scheme: HTTPS` on all three
>   probes when TLS is configured — the kubelet does not verify a probe's certificate,
>   so a private CA is not a problem there — and a way to mount certificate and key from
>   a Secret with the two flags pointing at them. `--addr` should keep defaulting to
>   `:8080` either way: a port that moves when a certificate is configured is a surprise
>   in a chart, a systemd unit and a firewall rule at once.
>
> One addition to the documentation obligation this record already calls load-bearing:
> `docs/install.md` § 8 is not the only place that states the fact. The handbook the
> binary serves (`api/web/handbuch.html`) says "Atlas spricht kein TLS" and "Atlas speaks
> no TLS" in its Betrieb section, in both languages, and that is what an operator actually
> reads. Whatever § 8 comes to say has to be said there too, in both — including that a
> reverse proxy is still wanted for `/mcp` and `/metrics`.
>
> These three are what an acceptance has to settle. What it settled them as is in the
> note below.

> **Accepted and implemented (2026-08-31).** Option 2 is built as described, and the
> three questions above are answered here rather than left to whoever reads the code.
>
> - **The trust anchor: `--tls-ca`, scoped to a peer Atlas.** A PEM bundle added to
>   the host's roots — never replacing them — used by `WithTargetTLSRoots` for the
>   deployment-target client in `api/promote.go`, and by `atlas worker --tls-ca` for
>   a worker on another host. The alternative, leaving it to the host's trust store,
>   was rejected for the container: there it is an image change or an init container,
>   for a deployment shape (on-prem, internal CA) this record is aimed at. It reaches
>   no further than Atlas talking to Atlas — a Worker Type calling Jira, a mail server
>   or Graph keeps the machine's roots, because that endpoint is somebody else's — and
>   there is still no way to skip verification anywhere.
>
> - **The bind order, and what the log prints.** The loopback listener is bound
>   before `api.New`, and `internalURL` in `cmd/atlas/listeners.go` is what the MCP
>   client and the supervised workers are handed; `selfURL` is gone, having been
>   `loopbackURL` under a second name. `serveUntil` runs both servers, gives them one
>   `--shutdown-timeout` between them, and ends the process if either stops serving.
>   The startup lines print `reachableOrigin` — `--external-url` where it is set,
>   otherwise `https://` on `--addr` — and carry `tls=true/false`.
>
> - **The chart.** `atlas.tls` mounts a `kubernetes.io/tls` Secret and passes the
>   pair; the `atlas.probe` helper renders all three probes with `scheme: HTTPS`
>   where TLS is on. The Secret is mounted `0440` rather than `0400`, because a
>   Secret volume is owned by root with the pod's `fsGroup` and the server runs as
>   65532 — owner-only would leave it unable to read its own certificate. Enabling
>   TLS without naming a Secret fails the render. `--addr` still defaults to `:8080`.
>
> Two details the record left open and the implementation had to decide. **A failed
> reload keeps the certificate already loaded and warns** (`server.tls_reload_failed`)
> rather than refusing handshakes: the ordinary cause is a renewal caught half-written,
> and the loaded certificate is still valid. It does not latch — the failing pair's
> modification times are remembered, so the retry happens when the files change again,
> without re-reading and re-logging on every handshake. And **HTTP/2 was tested rather
> than assumed**, as the trade-off above insists: `TestServeStreamsOverHTTP2` runs the
> collaboration session stream (ADR-0140) over a negotiated h2 connection. `TLSNextProto`
> stays nil; the escape hatch is unused and that test is what would say to reach for it.
>
> **One correction to the text below.** It says `/mcp` is unauthenticated for external
> callers by construction. That stopped being true with
> [ADR-0196](0196-authenticated-mcp-transport.md): `/mcp` now passes the same boundary
> as the rest of the API, so under `--auth` a request without a credential is refused
> before the adapter sees it. The reverse proxy therefore survives this record for
> `/metrics`, `/healthz` and `/readyz` — unauthenticated by design, because a kubelet
> has no credential to offer (ADR-0142) — and not for `/mcp`. The conclusion is
> unchanged: this record removes the cryptographic reason to run a proxy, not the
> authorization one, and `docs/install.md` § 8 says so in those words.
>
> **Still not covered**, beyond what the record already excludes: `atlas mcp --server
> https://…`, the stdio adapter, takes no `--tls-ca`. It is a client of a remote Atlas
> like the others, but it runs on a person's own machine, where the host trust store is
> the ordinary answer and usually already carries the company CA. If that turns out to
> be wrong, it is the same flag in a third place.

## Context and problem statement

Atlas serves plaintext HTTP and nothing else. `serve` in `cmd/atlas/main.go` builds
one `http.Server` and calls `ListenAndServe`; there is no `ListenAndServeTLS` call, no
`tls.Config`, and no certificate flag anywhere in the binary. `docs/install.md` says so
in as many words — "Atlas speaks plain HTTP, never TLS. The binary has no certificate
handling at all" — and makes a TLS-terminating reverse proxy a precondition for any
exposure beyond the host.

That absence was never decided. It is the state the server has always been in, and no
record argues for it. This one exists because two things now push against it from
directions that are not "we would like encryption".

**A feature Atlas already ships cannot be used with what Atlas ships.**
[ADR-0129](0129-remote-deployment-targets.md) lets one Atlas server publish an
application to another. `validateTargetURL` in `api/targetstore.go` accepts `https`
and refuses `http` for anything but loopback, because a deploy token presented over
plaintext is a credential handed to everyone on the path. That refusal is right. Its
consequence is that a two-server promotion requires a reverse proxy on the receiving
side: Atlas demands of a peer a transport it cannot itself provide.

**The same gap is open for workers.** `atlas worker --server` authenticates with a
bearer token, and a supervised worker is handed this server's internal service token
through its environment (`workerTokenEnv` in `api/superviseenv.go`,
[ADR-0049](0049-internal-service-auth-for-mcp.md)). On one host that token never
crosses a network. The moment a worker runs on another host, it does, in clear.

Against that stands the reason the status quo is defensible: certificates are not a
feature, they are a lifecycle. Issuance, renewal, chains, formats, expiry at 03:00 on
a Sunday. nginx, Caddy, Traefik, IIS and every Ingress controller already own that
lifecycle, do it well, and are in the operator's runbook regardless.

The question this record answers is therefore narrow. It is not "should traffic to
Atlas be encrypted" — wherever an operator put a proxy, it already is. It is
**whether the binary should be able to terminate TLS itself, and how much of the
certificate lifecycle it takes on if it does.**

## Decision drivers

- **A requirement Atlas imposes, Atlas should be able to satisfy.** `validateTargetURL`
  refusing `http://` is the sharpest form of this: the constraint is correct and the
  product cannot meet it unaided.
- **The single-binary claim should hold for a small install.**
  [ADR-0011](0011-single-binary-distribution-and-web-ui.md) sells one file, one
  process. "One binary, plus a mandatory second process for TLS" is a footnote on the
  pitch, and it is felt most by exactly the on-prem and edge installs the binary is
  aimed at.
- **Do not own a lifecycle you cannot do better than the ecosystem.** Anything taken on
  here is support surface forever.
- **No footguns**, in the sense [ADR-0044](0044-user-management-and-authentication-boundary.md)
  used the phrase. `api/targetstore.go` states the house rule already: there is
  deliberately no "skip TLS verification" option anywhere, because "it would be the
  first thing reached for when a certificate is wrong, which is exactly when it must
  not be available." Nothing here may create the pressure that gets that switch added.
- **Nothing existing may break.** Today's plaintext deployments behind a proxy must
  keep working untouched, on upgrade, with no configuration change.
- **Encryption is not authorization.** Whatever is decided must not be readable as
  "TLS is on, therefore this is safe to expose".

## Considered options

1. **Keep the status quo** — plaintext only, reverse proxy mandatory — and record the
   absence as a decision so it stops being an accident.
2. **An optional TLS 1.3 listener with operator-supplied certificate files.**
3. **Option 2 plus ACME** — the binary obtains and renews its own certificates.
4. **Option 2 plus a generated self-signed certificate by default**, on the
   [ADR-0070](0070-vault-on-by-default-with-generated-key.md) model.

## Decision outcome

Chosen option: **2 — an optional TLS 1.3 listener, certificates supplied by the
operator, off by default.**

### The listener

- `--tls-cert` and `--tls-key`, both or neither; naming one without the other fails at
  startup rather than falling back to plaintext. Unset means today's behaviour exactly.
- `TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}` on the existing `http.Server`,
  served with `ListenAndServeTLS`.
- **TLS 1.3 only, and that is the point of choosing it.** Go's `crypto/tls` ignores
  `CipherSuites` for TLS 1.3 — the suites are fixed by the protocol. There is therefore
  no cipher list to expose as a flag, nothing an operator can weaken, and no CBC-versus-
  RC4 conversation with an auditor. The configuration surface Atlas would otherwise have
  to design, document and defend simply does not exist.
- **No new dependency.** `crypto/tls` is standard library and pure Go: `CGO_ENABLED=0`
  in the Dockerfile and the release workflow keeps working, [ADR-0010](0010-go-and-no-cgo.md)
  is untouched, and `go.mod` gains nothing.
- **Certificates load through a `GetCertificate` callback** over a cached, mtime-checked
  file pair — not through the filename arguments of `ListenAndServeTLS`, which read the
  files once at startup. A 90-day certificate renews roughly six times a year. If each
  renewal requires restarting a stateful engine, operators will either not renew or not
  use the feature. This is the one piece of the lifecycle worth owning, because the
  alternative is a restart.

### The scheme stops being a constant — and the loopback hop stays plaintext

`loopbackURL` and `selfURL` in `cmd/atlas/main.go` hardcode `http://`. Three things
depend on them: the MCP adapter's loopback client (`mcp.NewClient(loopbackURL(addr), …)`,
[ADR-0016](0016-mcp-server-over-http-api.md)), supervised workers spawned with
`selfURL(addr)` and the internal token, and the startup log lines an operator copies out
of the journal. `atlas worker --server`, `atlas mcp --server` and the Helm probes in
`deploy/helm/atlas/values.yaml` carry the same assumption.

The obvious move — point those clients at `https://127.0.0.1:<port>` and have them trust
the configured certificate — does not work, and the reason is not policy but naming. A
certificate issued for `atlas.example.com` carries no SAN for `127.0.0.1`. Verification
fails no matter which root the client trusts, and the only thing that would make it pass
is the skip-verify switch this repository has decided twice not to have.

So: **when `--tls-cert` is set, `--addr` serves TLS and a second plaintext listener is
bound to `127.0.0.1:0`**, whose port is discovered at bind time and handed to
`selfURL`/`loopbackURL`. Both listeners serve the same handler. The process's own
children keep the loopback hop they have today, which does not cross a network — the
same exception `validateTargetURL` already carves out — and no internal client has to
verify a certificate at all. The three callers above do not change.

This is strictly better than the status quo, where the *only* listener is plaintext and
`--addr` defaults to `:8080`, meaning every interface.

### What this does not change, and must not be read as changing

TLS is confidentiality on the wire. It is not authentication, and this record adds none.

`requiresAuth` in `api/auth.go` gates `/api/v1` and nothing else. `/mcp` is
unauthenticated for external callers by construction (ADR-0016) while its own loopback
calls carry the internal service token (ADR-0049) — so an exposed `/mcp` is a way past
`--auth` entirely, and encrypting it changes nothing about that. `/metrics` is
unauthenticated by design ([ADR-0142](0142-prometheus-metrics.md)), a choice that
record makes explicitly on the strength of the deployment guidance: "put a reverse proxy
in front of anything you expose beyond the host."

Therefore **this record does not retire the reverse proxy from the documentation.** It
removes the cryptographic reason to run one. The authorization reason for `/mcp` and
`/metrics` stands until a separate decision addresses it. `docs/install.md` § 8 must be
rewritten to say that in those words, or the feature will be read as "TLS is on,
therefore this is safe to expose" — which is the single most likely way for this change
to make a deployment less safe rather than more.

If the goal is "no reverse proxy needed at all", TLS is the smaller half of it. The
other half is authentication on `/mcp` and `/metrics`, and it is a different record.

### What is not covered

- **ACME (option 3).** An account key, a cache directory, port 80 or a DNS-01
  credential, and a renewal state machine — for a deployment shape (public DNS, publicly
  reachable) that the on-prem installs most interested in this feature least often have.
- **A generated certificate by default (option 4).** Rejected below.
- **mTLS and certificate-derived principals.** ADR-0044 built a `Principal` boundary that
  could carry one, which is where this would land — with a client trust store, a
  revocation story and a subject-to-user mapping. A record of its own.
- **The Windows certificate store.** Files only. `docs/install.md` § Windows Server
  currently sends operators to IIS for TLS; with this they can use files instead, but a
  shop that keeps certificates in the machine store is not served.
- **HTTP→HTTPS redirect and HSTS.** A redirect needs a listener on :80 and an opinion
  about the public name; HSTS is a header the proxy sets today and a footgun to enable by
  default on a hostname Atlas does not own.
- **A `--tls-min-version` escape hatch.** See the trade-off below: it is the amendment if
  a real deployment needs it, not part of the first slice.

### Consequences

- **Positive:** ADR-0129's remote deployment targets work between two stock Atlas
  servers. `validateTargetURL` stops being a requirement the product imposes and cannot
  satisfy.
- **Positive:** `atlas worker --server` across hosts stops carrying a bearer token in
  clear, and with it the internal service token a supervised worker is given.
- **Positive:** the ADR-0011 claim holds for a small install — one binary, one service,
  no second process to learn.
- **Positive:** no configuration surface for ciphers, because TLS 1.3 has none. Nothing
  to weaken and nothing to review.
- **Negative / trade-off accepted:** `ListenAndServeTLS` turns on HTTP/2. Go's
  `net/http` negotiates h2 over ALPN whenever TLS is configured and `TLSNextProto` is
  nil, so the long-poll and streaming endpoints — collaboration sessions
  ([ADR-0140](0140-live-collaborative-modeling-sessions.md)), the log tail — would run
  over h2 for the first time. The likely effect is an improvement, since the six-
  connections-per-origin limit those endpoints are shaped around disappears. But it is a
  behavioural change on the endpoints least covered by unit tests, and it must be tested
  rather than assumed. Setting `TLSNextProto` to an empty non-nil map disables h2 and is
  the escape hatch if it goes badly.
- **Negative / trade-off accepted:** two listeners instead of one, and "is the API
  reachable in plaintext" now has two answers depending on the interface. `docs/install.md`
  and the Helm chart both have to state it.
- **Negative / trade-off accepted:** every TLS problem an operator has becomes an Atlas
  issue — expired chains, a missing intermediate, a key format the loader does not read,
  a scanner's opinion. That support surface currently belongs to nginx and would not come
  back.
- **Negative / trade-off accepted:** TLS 1.3-only excludes clients that cannot negotiate
  it. Browsers are not the risk. TLS-inspecting corporate middleboxes, Windows PowerShell
  5.1 on unpatched hosts, and old Java clients are. There is deliberately no
  `--tls-min-version` here; if a real deployment hits this, that flag is the amendment,
  added as a documented, opt-in downgrade rather than by relaxing the default for
  everyone.
- **Follow-ups / risks to watch:** mTLS will be asked for, and the `Principal` boundary
  is where it belongs. If [ADR-0175](0175-replicated-partition-cells.md) proceeds, its
  node-to-node traffic needs transport security and should reuse this configuration
  rather than grow a second one. And the docs change described above is not optional
  polish — it is the part of this record that keeps the change from being a net loss.

## Pros and cons of the options

### Option 1 — status quo, plaintext only
- Good: zero code and zero new support surface. A reverse proxy does certificates better
  than a workflow engine ever will, and the operator is already running one.
- Good: the failure modes stay in software built for them.
- Bad: ADR-0129 remains unusable without third-party software, which is the concrete
  cost and the reason this record exists.
- Bad: the single-binary story carries a mandatory second process in its footnote.

### Option 2 — optional listener, operator-supplied certificates (chosen)
- Good: closes the ADR-0129 gap and the worker-token gap with a flag pair, a
  `tls.Config`, and a certificate cache.
- Good: off by default, so no existing deployment changes on upgrade.
- Good: no dependency; no-cgo and single-binary intact.
- Bad: Atlas owns certificate loading, reload, and every error message that comes with
  them.
- Bad: does not remove the reverse proxy from the documentation, because `/mcp` and
  `/metrics` still need one. The change is smaller than it will appear to readers.

### Option 3 — plus ACME
- Good: the complete story for an internet-facing install; renewal stops being anyone's
  problem.
- Bad: a dependency and a subsystem, for the deployment shape on-prem Atlas installs have
  least often.
- Bad: its failure mode is a server that will not start, or one quietly serving a stale
  certificate, at a moment nobody is watching.

### Option 4 — generated self-signed certificate by default, on the ADR-0070 model
- Good: appealing symmetry with the vault key — on by default, generated if absent,
  operator-supplied as the stronger posture. Encryption everywhere with no configuration.
- Bad: **the symmetry is false.** A generated vault key is immediately usable, because
  nothing else has to agree with it. A generated certificate is not: every client rejects
  it until someone distributes the root. The pressure that creates lands precisely on the
  skip-verify switch `api/targetstore.go` and ADR-0129 both refuse to have.
- Bad: it would train operators to click through certificate warnings on the Console,
  which is a worse outcome than plaintext behind a proxy, not a better one.
- Bad: on by default changes the scheme of every existing deployment's URL on upgrade,
  against the "nothing existing may break" driver.

## Links

- relates to [ADR-0010](0010-go-and-no-cgo.md) — `crypto/tls` is stdlib and pure Go, so
  the no-cgo constraint holds
- relates to [ADR-0011](0011-single-binary-distribution-and-web-ui.md) — what makes the
  single-binary claim true for a small install
- relates to [ADR-0016](0016-mcp-server-over-http-api.md) and
  [ADR-0049](0049-internal-service-auth-for-mcp.md) — `/mcp` and the internal service
  token: the surface TLS does not protect
- relates to [ADR-0044](0044-user-management-and-authentication-boundary.md) — opt-in
  enforcement is the shape this record copies, and the `Principal` boundary is where
  mTLS would land
- relates to [ADR-0070](0070-vault-on-by-default-with-generated-key.md) — the template
  option 4 tries to reuse, and why it does not transfer
- relates to [ADR-0129](0129-remote-deployment-targets.md) — the requirement Atlas
  cannot currently satisfy; its no-skip-verify stance is binding here
- relates to [ADR-0142](0142-prometheus-metrics.md) — `/metrics` is unauthenticated by
  design, on the same reverse-proxy guidance
- relates to [ADR-0175](0175-replicated-partition-cells.md) — future node-to-node
  traffic should reuse this configuration
