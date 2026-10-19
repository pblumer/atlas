# ADR-DRAFT: Outbound HTTP follows the Windows proxy settings, the way .NET does

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-19
- **Deciders:** Atlas maintainers
- **Open question:** whether a service account's proxy settings are always where .NET
  finds them. The rules below are .NET's `HttpWindowsProxy` rules, read through the same
  WinHTTP calls, and the PAC path is tested against a PAC script on the CI runner — not yet
  on a server whose proxy a group policy sets. Until one is observed, an operator whose
  Worker still goes direct sets `HTTPS_PROXY`, which wins.
- **Question checked:** 2026-10

## Context and problem statement

Atlas runs on Windows Server (the release ships a `windows_amd64` binary, and the
install guide has a Windows section). On such a server the proxy is rarely a variable.
It is a PAC script or a proxy in Internet Options, set by group policy, and every .NET
program — PowerShell among them — finds it there without being told.

Go's default transport reads only `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`. Every
Worker Type written in Go therefore goes direct on such a host, into a firewall that
drops the packets. The request fails with a timeout, not a message.

This was found in practice. On one server the Teams Phone Worker, which runs
PowerShell, reached `login.microsoftonline.com`, and the Entra Worker, in Go, timed out
on the same address under the same service account. `curl.exe` — which, like Go, reads
only the variables — timed out too. Through the proxy that
`[System.Net.WebRequest]::GetSystemWebProxy()` reported, it answered 200. One host had two
network paths, and which one a task took depended on the language its Worker is written
in.

## Decision drivers

- A Worker must reach what a PowerShell on the same host reaches, without per-host
  configuration somebody has to know to write.
- An operator who set a proxy variable deliberately must keep exactly that behaviour.
- A supervised Worker reaches its own server over loopback, or over the address the
  server listens on; nothing may route that through a proxy.
- No new dependency, and nothing that changes behaviour off Windows.

## Considered options

1. **Leave Go's behaviour and document `HTTPS_PROXY`** for the Atlas service.
2. **Read the Windows settings the way .NET does**, once per process, into the default
   transport — the variables still winning.
3. **A proxy per Worker** in the Console.
4. **A third-party package** that reads the Internet Options (such as `go-ieproxy`).

## Decision outcome

Chosen option: **2, the Windows settings read the way .NET reads them**, because it makes
"the same route as PowerShell" true by construction rather than by configuration. Option
1 is what an operator has to discover after a timeout; it also sends every request
through one proxy where a PAC script routes per host. Option 3 asks for the same address
once per Worker and still says nothing about a PAC script. Option 4 is a dependency for
some hundred lines whose rules would be its author's, not .NET's.

### The choice, made once at startup (`internal/sysproxy`, called by `atlas serve` and `atlas worker`)

1. `ATLAS_SYSTEM_PROXY=off` (also `false`, `0`, `no`) keeps Go's own behaviour.
2. A proxy variable — `HTTPS_PROXY` or `HTTP_PROXY`, either case — wins, as it does in .NET,
   and Go's transport reads it as it always did. `NO_PROXY` alone is not one.
3. Otherwise, on Windows, the account's settings as `WinHttpGetIEProxyConfigForCurrentUser`
   reports them: a PAC URL, automatic detection (WPAD), a manual proxy and its bypass
   list. That is what .NET's `HttpWindowsProxy` reads.
4. Anywhere else, or with no settings, nothing changes.

The choice is written into `http.DefaultTransport`, so every client that leaves its
transport to the default follows it — every Worker Type's included. The three clients
with a transport of their own (the worker's client for a server behind `--tls-ca`,
publishing to a deployment target, the MCP client) talk to an Atlas, not to the
internet, and are left as they were. One line, `outbound_proxy.selected`, says at startup
which source a process follows and, for the Windows settings, which settings (without the
credentials a proxy address may carry).

### The rules for one request — .NET's

- **Loopback and the host's own addresses never go through a proxy**, whatever a PAC
  script says: a server listening on one of its addresses is reached there by its
  supervised Workers, and that request does not leave the host.
- **Auto-configuration first**, when set: `WinHttpGetProxyForUrl` runs WPAD and/or the PAC
  script, and its answer is final, `DIRECT` included. A PAC server that asks who is
  calling is retried once with the account's logon, as .NET does.
- **The manual proxy** when auto-configuration cannot answer, with its bypass list:
  `<local>` for a name without a dot, `*` as a wildcard, an optional scheme in front,
  case ignored. A per-protocol list (`http=…;https=…`) is honoured.
- **Direct** with neither.
- WPAD that found nothing is not tried again for **two minutes**, because discovery can
  take seconds; a PAC URL is still used meanwhile.

Two things differ from .NET and are deliberate. Only the scheme, host and port go to the
PAC script, because a path or query is none of its business and may carry a token. And an
answer is cached for five minutes per scheme, host and port — thirty seconds when it was a
fallback after Windows failed, so a passing PAC outage does not pin a host to the wrong
route — where .NET relies on WinHTTP's own cache.

One thing differs because Go cannot do it: Go's transport takes one proxy per request, so
of a list only the first is used, where .NET fails over along it.

### Consequences

- A Windows server behind a group-policy proxy needs no configuration for its Go
  Workers; the variables remain the override and `ATLAS_SYSTEM_PROXY=off` the way back.
- A proxy that demands Windows authentication (NTLM or Kerberos) is still out of reach
  for Go: its transport speaks Basic only. Such a proxy needs an exception for the
  server, as it did before.
- The first request to each host may wait for a PAC script or WPAD; the timeouts on the
  WinHTTP session bound that to seconds.
- Behaviour off Windows is unchanged.

### As built

`internal/sysproxy` holds the rules in platform-free code behind a two-call backend
(read the settings, ask WinHTTP about one URL), tested on every platform with a fake;
`sysproxy_windows.go` is the WinHTTP backend. Its tests on the Windows runner run a real
PAC script served from the test, check the WinHTTP structs against their C sizes, and
check that loopback stays direct with whatever settings the runner has. `cmd/atlas` calls
`sysproxy.Install` after logging is set up, in `serve` and in `worker`.

## Pros and cons of the options

### 1. Document `HTTPS_PROXY`
- Good: no code.
- Bad: found after a timeout; one proxy for every host where a PAC routes per host.

### 2. Read the Windows settings like .NET (chosen)
- Good: the same route as PowerShell on the same host, by construction.
- Good: the variables still win, and one switch turns it off.
- Bad: Windows-only code that only the Windows runner exercises.

### 3. A proxy per Worker
- Good: explicit.
- Bad: the same address entered once per Worker; no PAC.

### 4. A third-party package
- Good: less code here.
- Bad: a dependency, and rules that are not .NET's.

## Links

- [ADR-0172](0172-entra-id-connector.md) — the Entra Worker, whose timeout found this.
- [ADR-0463](0463-teams-phone-worker.md) — the Teams Phone Worker, whose PowerShell found the proxy.
- `docs/install.md`, *Windows Server* — the operator's view.
