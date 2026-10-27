# ADR-DRAFT: An administrator profiles the running server through the API

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-27
- **Deciders:** Atlas maintainers
- **Open question:** whether unattended profiling — a support script, a capture on an alert — needs a path that does not go through an administrator's session; today none does, because an API token never carries the admin role (ADR-0209)
- **Question checked:** 2026-10

## Context and problem statement

"The server is slow" has one useful answer: where it spends its time. The metrics say
*that* a run-loop turn waits or holds longer than it did (`atlas_runloop_turn_wait_seconds`,
`atlas_runloop_turn_held_seconds`); they cannot say which code it is. The Operations
heatmap (ADR-0496) was the latest case: an operator saw the server slow down with the
heatmap open, and the answer had to be reasoned out of the code, because nothing could be
measured on the server it happened on.

Go's runtime keeps exactly what is needed — CPU samples, the heap, every goroutine's stack,
an execution trace — and `go tool pprof` reads it. A profile taken on a laptop against a
test load measures the laptop; the profile worth having is the one from the installation
that is slow, while it is slow, without a restart that would make it fast again.

Atlas had no way to reach any of it. The usual way in Go — importing `net/http/pprof` —
registers handlers on `http.DefaultServeMux` as a side effect of the import: no login, no
role, no bound on how long a recording runs, and a `cmdline` handler that hands back the
process's flags. ADR-0196 and ADR-0199 removed exactly that shape from Atlas — a route
whose protection depends on where it happens to be mounted — and ADR-0198 brought
`/metrics`, the last such route, behind a credential.

## Decision drivers

- The profile must come from the running server, without a restart or a rebuild.
- Reaching it must go through the same boundary as everything else: a principal, a role
  declared in the route table, an audit line (ADR-0197, ADR-0209).
- Nothing it hands out may carry a secret.
- One request must not be able to hold the profiler, or its cost, for as long as it likes.
- The format must be what the standard tools open, so nobody needs Atlas-specific tooling.

## Considered options

1. Import `net/http/pprof` and mount its handlers behind the boundary.
2. A separate debug listener (`--pprof-addr`), bound to localhost.
3. One admin-only route in the `/api/v1` table, written against `runtime/pprof` and
   `runtime/trace` directly.
4. No endpoint: document attaching a profiler from outside (`perf`, a debug build).

## Decision outcome

Chosen option: **3, one admin-only route over the runtime packages**, because it is the
only option that gives the running server's profile through the boundary every other
route goes through, and it costs about a hundred and fifty lines.

`GET /api/v1/debug/pprof/{profile}` serves:

| `{profile}` | What | Parameters |
|---|---|---|
| `profile` | CPU samples over a window, for `go tool pprof` | `seconds` 1–120, default 30 |
| `trace` | An execution trace, for `go tool trace` | `seconds` 1–30, default 5 |
| `heap`, `allocs` | Live and allocated memory by call site | `debug` 0–1, `gc=1` collects first |
| `goroutine` | Every goroutine's stack | `debug` 0–2; 2 is the panic-style dump |
| `threadcreate` | Where OS threads were created | `debug` 0–1 |

- **Admin-only, at the boundary.** The route names `RoleAdmin` in the route table and is on
  `wantAdminRoutes`. A profile shows the server's insides and a recording costs the server
  while it runs; that is an administrator's, like the log and the backup.
- **Not `net/http/pprof`.** The runtime packages it wraps are used directly, so nothing is
  registered on the default mux. `TestTheStandardPprofHandlersAreNotLinked`, in
  `cmd/atlas` because that test binary links everything the server does, fails if anything
  — Atlas or a dependency — links the package.
- **What is offered is a list.** `cmdline` is not on it: a flag can carry a secret, and an
  API that returns the flags is a second place a secret is read from. `symbol` is not
  needed, since the profiles carry their symbols. `block` and `mutex` are not on it: they
  record nothing unless a rate is set process-wide, and setting one per request would let
  two requests change each other's measurement. Anything else is 404, naming what is
  offered.
- **A recording is bounded and single.** The runtime keeps one CPU profile and one trace;
  a second request while one runs is answered 409 rather than handed an empty file. A
  window ends at its `seconds` or when the client goes away, whichever is first. The
  server's write timeout is zero by design (`cmd/atlas/listeners.go`), so a two-minute
  profile is not cut off.
- **Audited.** Every download writes `profile.downloaded` at INFO with the actor, the
  profile and, for a recording, the seconds.
- **The binary route is not an MCP tool; a summary of it is.** A gzip-compressed protobuf
  is a file for a person with a profiler, and in an agent's context it is worse than
  nothing — but what it says is exactly what an agent diagnosing a slow server needs. So
  `GET /api/v1/debug/pprof/{profile}/summary` reads the server's own profile and answers
  as a list, and `atlas_runtime_profile` is the tool over it:
  - `profile` records CPU time over at most 20 s (default 10) — inside the thirty seconds
    the MCP client waits for a call — and lists the functions by their own time (flat) and
    with their callees (cum), in milliseconds and percent, with the cores used;
  - `heap` and `allocs` list live and allocated memory the same way, in bytes;
  - `goroutine` counts the goroutines and lists the largest groups sharing a stack, leaf
    frame first, at most 16 frames kept from both ends.

  Every list is bounded by `?top=` (default 20, at most 50), and every answer carries a
  note saying what its figures mean. The goroutine summary is built from the aggregated
  profile, never from the panic-style dump, which prints each frame's arguments as raw
  words. The profile is decoded in `api/pprofproto.go` — varints and four wire types —
  rather than by adding `github.com/google/pprof` as a module.
- **And the figures that come before a profile.** A profile says where the time goes;
  first somebody has to know whether the server is slow and in which part. Those figures
  were only in `/metrics`, which a scraper with a metrics token reads.
  `GET /api/v1/debug/runtime`, and `atlas_runtime_health` over it, read them twice
  `?seconds=` apart (default 5, at most 20; 0 answers at once, since the start) and answer
  with the window between: the run loop's turns and how long callers waited for the writer
  and each turn held it, the writer's throughput, queue and fsync and commit latencies,
  the scheduler latency and the runnable goroutines, the collector's cycles and pauses,
  the allocation rate and the heap. The run loop's and the writer's histograms are the
  Prometheus ones, so the route has them only where metrics are on, and says so where
  they are not. Percentiles are bucket bounds and read "at most". The collector's share
  of the CPU is since the start, because the runtime updates its CPU figures only at a
  collection.
- **Both stay admin-only.** A tool call carries its caller's roles, and an OAuth grant
  carries the roles of the person who approved it (ADR-0200), so the tools serve an
  administrator's agent and answer 403 to anybody else's — and to every API token, which
  never carries the admin role. The data leaves the server for whatever model the agent
  runs on; what it holds is function names, the call graph and sizes, no business data,
  which is less than the log the MCP surface already leaves out.

### Consequences

- **Positive:** the next "it is slow" is answered by measuring the installation it happens
  on. The answer is a file the standard tools open (`go tool pprof -http=: cpu.pb.gz`).
  Nobody gains a way in they did not have: an administrator could already read the log,
  download the whole instance including the vault key, and run scripts.
- **Negative / trade-offs accepted:** `go tool pprof https://…/profile` cannot fetch from
  the route directly, because it sends no session cookie; the profile is downloaded first
  (browser or `curl` with the session cookie) and opened from the file. Delta profiles
  (`heap?seconds=`) are not offered; two heap snapshots compared with `-diff_base` give the
  same answer. A CPU profile costs the server while it records — typically a few percent
  at the default 100 Hz, which has not been measured on Atlas — and a trace under load
  costs more and grows by megabytes a second, hence its shorter bound. A link an
  administrator follows from another site can start a recording, since the route is a GET
  and the session cookie is `SameSite=Lax`; the file goes to the administrator, never to
  that site, and the cost is bounded by the window — the exposure every admin GET route,
  the full backup among them, already has.
- **Follow-ups / risks to watch:** the open question in the front matter. If unattended
  capture is needed, the candidates are a dedicated token scope with its own non-admin
  role, or option 2 beside this route; either one widens who can reach the server's
  insides and needs its own record. The rules of thumb the runtime health's note gives
  (a run-loop wait well above its hold, an fsync above about 50 ms, a scheduler latency
  above about 10 ms, a collector share above about a quarter) are starting points for a
  reading, not thresholds anybody measured on Atlas; an agent should treat them so.

## Pros and cons of the options

### Option 1 — `net/http/pprof` behind the boundary
- Good: the standard handlers, with every profile, delta profiles and the URLs
  `go tool pprof` knows.
- Bad: the import registers the same handlers, unauthenticated, on the default mux; the day
  anything serves that mux — Atlas or a dependency — they are public. It brings `cmdline`,
  and its windows are bounded only by the server's write timeout, which is zero here. And
  the main advantage, `go tool pprof <url>`, does not survive authentication anyway.

### Option 2 — a localhost debug listener
- Good: no authentication to design; `go tool pprof http://localhost:6060/…` works
  directly; widespread practice in Go services.
- Bad: protection by network position, which is the rule ADR-0199 replaced; no principal,
  so no audit line says who looked; in a container it needs an exec or a port-forward,
  which is a second credential path to reason about; one more port and flag to configure
  and to forget open.

### Option 3 — one admin-only route over the runtime packages (chosen)
- Good: the same boundary, role table, audit trail and inventory tests as every route; a
  closed list of what is offered; bounded windows; no import side effect.
- Bad: Atlas owns a small amount of code the standard library already has; no delta
  profiles; fetched as a file rather than by URL from the profiler.

### Option 4 — profile from outside
- Good: no code at all.
- Bad: `perf` sees Go's frames poorly and cannot see goroutines; a debug build is a
  restart, which is exactly what makes the slow server fast again before it is measured.

## Links

- relates to ADR-0196 and ADR-0199 (what reaches the server without a principal),
  ADR-0209 (roles at the boundary), ADR-0197 (the audit trail),
  ADR-0198 (`/metrics` behind a credential), ADR-0142 (metrics and probes),
  ADR-0496 (the heatmap whose cost prompted this)
