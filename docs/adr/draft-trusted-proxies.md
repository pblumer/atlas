# ADR-DRAFT: Trusted proxies — the client's address behind a load balancer, and the health check that is not news

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-08
- **Deciders:** Atlas maintainers, at an operator's request

## Context and problem statement

An installation put a load balancer in front of Atlas. Atlas terminates TLS itself
(ADR-0191), and the balancer forwards TCP to it. A day of that server's log showed two
things.

**The health check drowned the log.** Every ten seconds, around the clock:

```
level=INFO msg="http: TLS handshake error from 10.179.2.139:42658: EOF"
```

That is the balancer's TCP health check: it opens a connection to the TLS port and
closes it before sending a ClientHello. net/http reports every such connection through
the standard logger, which `logging.Setup` routes into the one stream at INFO with no
event name (ADR-0142). 8640 lines a day say the check works, and they bury the
handshake failures that do mean something — a client that cannot speak TLS 1.3, a
certificate somebody refused.

**Every client was the balancer.** The same log's security trail:

```
event=auth.login         client_ip=10.179.2.139 username=admin
event=auth.password_set  client_ip=10.179.2.139 actor=admin user_id=usr_3b0e…
event=auth.user_updated  client_ip=10.179.2.139 actor=Patrick roles=[admin]
```

`httpapi.ClientIP` is the connection's own address and deliberately ignores
`X-Forwarded-For`: a client-supplied value would let one caller spread its password
guesses across as many throttle buckets as it cares to invent (ADR-0197). Behind a
balancer that refusal costs two things:

- **The audit trail loses the one fact it exists to keep.** Every login, password
  change and role change says it came from the balancer. If one were ever disputed,
  the trail could not say from which workstation.
- **The login throttle becomes one bucket for everyone.** The per-address budget (20
  attempts, refilling at one every two seconds) is shared by every person behind the
  balancer. ADR-0197 sized it generously for an office behind one NAT address, and
  ordinary use does not exhaust it. But anybody behind the same balancer who keeps up
  more than one attempt every two seconds keeps it empty, and nobody else can sign in.

The balancer knows the real address. The question is how Atlas can take its word
without taking the client's.

## Decision drivers

- **A client must never be able to name its own address.** ADR-0197's reason for
  refusing forwarded headers stands. Whatever is read must be readable only from a
  peer the operator vouched for.
- **Both kinds of balancer.** One that speaks HTTP can append `X-Forwarded-For`. One
  that forwards TCP to a TLS-terminating Atlas — the deployment that prompted this —
  cannot add a header to a stream it does not decrypt. Its only means is the PROXY
  protocol, a header written in front of the stream.
- **Nothing changes unless asked.** A server started without the new setting must
  behave exactly as before, down to not wrapping the listener.
- **A typo stops the boot.** Like `--tls-cert` without `--tls-key`, a list that
  silently lost an entry trusts less than the operator believes, and one that
  silently grew trusts more.
- **The health-check line is demoted, not destroyed.** A port scan leaves the same
  trace, so an operator must be able to see it on demand.

## Considered options

1. **Leave it.** Operators read client addresses from the balancer's own log.
2. **Read `X-Forwarded-For` from everyone.**
3. **`--trusted-proxies`: one list of addresses and prefixes. From a connection whose
   peer is on it, read a PROXY header and `X-Forwarded-For`. From any other, read
   neither.**
4. Option 3, with the PROXY protocol taken from a library (`pires/go-proxyproto`).
5. Option 3, with separate lists for the PROXY protocol and for `X-Forwarded-For`.

For the log line: (a) drop it, (b) demote it to DEBUG and add `--log-level`, or
(c) tell operators to switch their health check to HTTPS.

## Decision outcome

Chosen: **option 3, with (b) for the log line**.

**`--trusted-proxies` / `ATLAS_TRUSTED_PROXIES`** takes addresses and CIDR prefixes,
separated by commas or spaces. An entry that does not parse refuses the start. So does
a prefix that covers every address (`0.0.0.0/0`, `::/0`): trusting everybody to name
the client is trusting the client to name itself. An IPv4 address or prefix written in
4-in-6 form is stored as the IPv4 one it means, so a dual-stack socket cannot decide
who is trusted. A startup line, `server.trusted_proxies`, records the list as the
server understood it.

**The PROXY protocol, v1 and v2** (`internal/trustedproxy`):

- Read only on a connection from a listed peer. Any other connection is handed to
  net/http untouched — its first bytes are not even peeked at. A header from such a
  peer is just the start of its TLS stream, and the handshake refuses it.
- Optional from a listed peer. The first bytes decide: `PROXY ` starts v1, the
  12-byte signature starts v2, anything else — a ClientHello, an HTTP request line,
  nothing at all — is left in the buffer and read as before. That keeps an HTTP-mode
  balancer, and a TCP health check that sends nothing, working unchanged.
- Read on the connection's own goroutine at first use, bounded by ten seconds (the
  listener's `ReadHeaderTimeout`). Never on the accept loop, so a slow peer holds up
  only itself. In net/http the first use is `RemoteAddr`, called before the server
  sets any deadline of its own, which is what makes setting and clearing one here
  safe.
- `LOCAL` (v2), `UNKNOWN` (v1), and an unspecified, UDP or UNIX family keep the
  peer's own address. A header that began and is broken ends the connection, and
  net/http logs the reason.
- Written by hand — one file of under 300 lines, every statement covered by tests —
  rather than taken from a library. It parses bytes from the network ahead of the TLS
  handshake, which is code that should be read in this tree, and the protocol is small
  and fixed.

**`X-Forwarded-For`**, from the handler: the request's `RemoteAddr` is the starting
point. After the PROXY listener that is already the client a balancer declared. Where
that address is itself a listed proxy, the header is walked from the right. Listed
proxies are passed over, and the first unlisted address is the client. Everything to
its left is what the client wrote and is never read. An entry that cannot be parsed
ends the walk where it stands. RFC 7239's `Forwarded` is not read; it is an amendment
when a balancer needs it.

**Only the public listener.** The plaintext loopback listener for this process's own
children (ADR-0191) and the MCP adapter (ADR-0016) is built without either layer, so
nothing on it is read as a balancer, whatever the list says about `127.0.0.1`.

**`httpapi.ClientIP`** returns the client a listed proxy vouched for, and otherwise
the connection's own address as before. Every rate limiter and every audit line
already reads it, so all of them follow. Audit lines gain `via`, the proxy that named
the client. A request that came round the balancer has no `via`, and that absence is
worth reading.

**The log line.** The HTTP servers get an `ErrorLog` from the logging package. It
writes exactly `http: TLS handshake error from <peer>: EOF` as
`server.tls_handshake_aborted` at DEBUG, with the peer as `remote`. Every other line
goes on through the standard logger, so it keeps its wording and level. `unexpected
EOF` — a ClientHello cut off part-way — is not what a health check does, and stays at
INFO. **`--log-level`** (`debug`, `info`, `warn`, `error`; default `info`) is the knob
ADR-0142 left out "while nothing emits below Info". Something now does.

### Consequences

- **Positive:** behind a listed balancer the login throttle charges each person
  rather than everyone at once, and the audit trail names who acted, from where, and
  through what. The health check no longer fills the log, and the TLS failures that
  matter stand out. Unset, nothing changes.
- **Negative / trade-offs accepted:**
  - **Trust is transitive.** Listing a proxy trusts it to append honestly to
    `X-Forwarded-For` and not to forward a client's bytes ahead of its own. An HTTP
    proxy that passed a client's first line through verbatim could let that client
    write a v1 header. That is the same trust `X-Forwarded-For` already requires, so
    one list for both is not a weaker position than two (option 5), only a simpler
    one. Listing a subnet trusts every host in it.
  - The handshake-EOF line is hidden at the default level, including from a port scan.
    `--log-level=debug` shows it.
  - The public listener now binds with `net.Listen` and `Serve`/`ServeTLS` instead of
    `ListenAndServe`/`ListenAndServeTLS`, so the PROXY listener can sit under the TLS
    one. The behaviour is the same, including binding only once recovery is complete.
- **Follow-ups / risks to watch:**
  - A TCP health check against the TLS port still opens and closes a connection per
    interval. The operator documentation recommends an HTTPS check of `/readyz`, which
    tells the balancer whether this instance can serve, not only that the port is open.
  - The MCP adapter's loopback calls still record `127.0.0.1`, as before. Carrying the
    original caller's address across that hop is a separate decision.
  - The Helm chart does not set the list. An ingress controller's pod addresses are a
    cluster fact, not a chart default.

## Pros and cons of the options

### 1 — Leave it
- Good: no new code at the network edge.
- Bad: the throttle stays one bucket per balancer, and the audit trail stays
  unattributable — the gap ISDS R-13 recorded, answered with "somebody else's log".

### 2 — `X-Forwarded-For` from everyone
- Bad: undoes ADR-0197. Any client spreads its guesses over invented addresses, and
  any client writes whatever address it likes into the audit trail.

### 3 — One list, both mechanisms, chosen
- Good: covers both kinds of balancer with one setting the operator already has to
  think about — which addresses are the balancer.
- Good: unset, the listener and the handler are returned unwrapped.
- Bad: one more piece of parsing in front of TLS, which is why it is small, written
  here, and fully covered by tests.

### 4 — A PROXY protocol library
- Good: maintained elsewhere, and used widely.
- Bad: a dependency at the most exposed point of the process, for a fixed protocol of
  two short header formats. Its policy knobs (`USE`, `REQUIRE`, `REJECT`, …) answer
  questions this record answers once.

### 5 — Separate lists
- Good: an operator could trust a peer for one mechanism and not the other.
- Bad: no deployment needs that distinction. The trust involved is the same either way
  (see Consequences), and a second list is a second thing to get wrong.

### (a) Drop the line / (c) leave it to the health check
- (a) Bad: loses the trace a port scan leaves, with no way to get it back.
- (c) Good: the right operational advice, and the documentation gives it. Bad: it fixes
  one installation and not the code, and a TCP check is a legitimate choice an operator
  should not pay for in log volume.

## Links

- Relates to ADR-0197 (login throttle and audit log): the per-address bucket and the
  audit attributes this feeds.
- Relates to ADR-0191 (built-in TLS listener): the TLS termination that makes the PROXY
  protocol necessary, and the loopback listener that is deliberately left out.
- Amends ADR-0142 (logs as a contract): adds `--log-level`, which it deferred until
  something logged below Info.
- Relates to ADR-0016 (MCP over the HTTP API): the loopback hop whose callers are not
  re-attributed.
