# ADR-0195: Requiring a login is the default

- **Status:** Proposed
- **Date:** 2026-08-26
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0044 made authentication **opt-in**: `--auth` off by default, mirroring how
`--docs` gates the API explorer (ADR-0043). The reasoning was sound at the time
and is worth restating, because this record reverses it rather than declaring it
a mistake. Auth arrived into a code base with no concept of a user at all;
turning it on for everyone in one step would have broken every existing
deployment, the MCP adapter, the API explorer and the test suite at once — the
opposite of the "smallest honest first slice" the work was scoped to.

Two things have changed since.

**The reasons for opt-in have been worked through.** MCP is no longer broken by
`--auth` — the transport is gated and forwards its caller's credential
(ADR-0196), and the stdio adapter can hold one of
its own. Which routes are gated is now a declared class per route rather than a
path-prefix rule, so what turning auth on actually does is legible and tested
(ADR-0199). The remaining objection to flipping the default
was the objection of a half-finished feature, and it is finished.

**The default is what a deployment gets.** Every piece of guidance around Atlas —
the install guide, the Helm chart, the ISDS compliance concept — says "turn on
`--auth` before exposing this". A default that every document tells you to change
is not a default; it is a trap with documentation around it. An operator who
forgets one flag gets a workflow engine that anyone who can reach the port can
deploy code to. Nothing in the product says so at the time it matters.

The question: **should `atlas serve` require a login when nobody says otherwise?**

## Decision drivers

- **Secure by default.** The out-of-the-box state should be the safe one; opening
  it up should be the act that takes a decision and leaves a trace.
- **Open must stay possible, and easy.** A laptop, a demo, a throwaway container
  and the repository's own examples are legitimate; the change must not make
  those awkward.
- **Being open must be loud.** The failure this prevents is not "somebody chose
  wrong", it is "nobody noticed". A silent open server is the whole problem.
- **Don't break the library or its tests.** `api.New` is consumed by one binary
  and by several hundred tests that construct an open server deliberately.
- **First run must stay possible.** Requiring a login is worthless if it also
  means nobody can get in.

## Considered options

1. **Leave it opt-in**, and keep saying so in the documentation.
2. **Flip the flag default to true**, keeping `--auth=false` as an explicit,
   loudly-logged opt-out. The `api` package option stays as it is.
3. **Flip both the flag and the library**, replacing `WithAuth()` with
   `WithoutAuth()`.
4. **Flip the flag, and refuse `--auth=false` on a non-loopback address.**

## Decision outcome

Chosen: **option 2.**

`--auth` defaults to `true`. `--auth=false` still runs the server fully open and
now writes one WARN line at startup under the stable event name `auth.disabled`,
naming what is open — the API, the UI, and `/mcp`, which can deploy and run
processes. That line is the point of the option as much as the flag flip is: a
demo becoming a deployment is the failure being designed against, and it is a
failure of nobody noticing rather than of anybody deciding.

First run stays possible because `bootstrapAdmin` already handled it: with an
empty user store it seeds one administrator from `ATLAS_ADMIN_USERNAME` /
`ATLAS_ADMIN_PASSWORD`, or generates a strong password and logs it exactly once.
That path stops being the thing you reach after step 6 of the install guide and
becomes the ordinary first start.

In the same change, `GET /api/v1/openapi.json` and the Scalar explorer at
`/api/docs` move from `accessPublic` to `accessAuthenticated`. Nothing on the
login screen reads either, and the explorer's "Try it out" drives the same
mutating API a session is required for — the argument ADR-0043 already makes for
`--docs`, one step further. They move together deliberately: an explorer that
renders and then cannot fetch its own document is worse than one that says
plainly it needs a login.

The Helm chart follows the binary: `atlas.auth.enabled` defaults to `true`, and
the template no longer refuses to render when no admin password source is set. That
guard made sense while auth was opt-in — opting in meant you had chosen to control
the credential — but as the default it would fail the default path. With no source
the chart now omits the credential environment entirely and the server generates
and logs a password, exactly as the binary does; `atlas.auth.existingSecret` is
what anything beyond a scratch install should set.

Option 1 is rejected: the documentation already says to change the default, which
is an admission that the default is wrong. Option 3 is rejected as
disproportionate — the security property that matters is what an operator gets
from `atlas serve`, and `api.New` is an internal library whose only non-test
consumer is that binary; inverting it would rewrite several hundred test
constructions for no gain in what a deployment does. Option 4 is rejected because
it makes a guess about the deployment: a server behind an authenticating proxy on
a private network is a legitimate reason to run open on a routable address, and
refusing it would push operators toward worse workarounds. Warning is the right
strength — it informs without overriding.

### Consequences

- **Positive:** the out-of-the-box state is the safe one, and the unsafe one
  announces itself. The install guide, the chart and the compliance concept stop
  opening with an instruction to change a default. M-02 in the ISDS concept becomes
  "check that `--auth=false` is not set", which is checkable from the startup log
  rather than from an argument list.
- **Negative / trade-offs accepted:** **this is a breaking change** for anyone who
  ran `atlas serve` with no flags and expected an open server; they need
  `--auth=false`, or credentials. Trying Atlas now costs one step — reading a
  generated password out of the startup log — where it used to cost none, and in
  Kubernetes that means `kubectl logs`. The chart's render-time refusal is gone,
  so a chart install with no credential source now succeeds and puts a generated
  password in a pod log; that is the binary's behaviour, and the values file and
  README say to set `existingSecret` instead.
- **Follow-ups / risks to watch:** login hardening is now more load-bearing than it
  was — a rate limit and a lockout on `/api/v1/auth/login`, which the existing
  token bucket already has the machinery for. A security audit log (login, failed
  login, logout, admin actions) is what makes this default demonstrable rather than
  merely true. `/metrics` remains public and is the last route whose exposure still
  depends on a proxy rule.

## Pros and cons of the options

### Option 1 — leave it opt-in
- Good: nothing breaks; every existing invocation keeps working.
- Bad: the safe state requires remembering a flag, and every document about Atlas
  has to spend a paragraph saying so; forgetting it is silent.

### Option 2 — flip the flag, loud opt-out (chosen)
- Good: safe by default; open is a decision with a trace; the library and its
  tests are untouched; open stays one flag away.
- Bad: breaking for flagless invocations; a first start now involves reading a
  password out of a log.

### Option 3 — flip the flag and the library
- Good: one story at both levels; an embedder gets the safe default too.
- Bad: rewrites several hundred test constructions and every `api.New` call site
  for a package that one binary consumes; large diff, no change in what a
  deployment does.

### Option 4 — flip the flag, refuse open on a routable address
- Good: catches the worst case outright.
- Bad: guesses at the deployment and is wrong for a server behind an
  authenticating proxy; a refusal that is wrong sometimes gets worked around in
  ways worse than the thing it prevented.

## Links

- reverses the opt-in enforcement of
  [ADR-0044](0044-user-management-and-authentication-boundary.md); the
  `*Principal` boundary and the `WithAuth()` option are unchanged
- depends on ADR-0196 (without it, this default
  would break MCP) and ADR-0199 (which is what makes "what
  does auth gate" answerable)
- narrows [ADR-0043](0043-openapi-spec-and-embedded-api-explorer.md): `--docs`
  still decides whether the explorer is served, and a login now decides who reads it
- the product-side concept this implements:
  [`docs/compliance/zugriffsschutz-konzept.md`](../compliance/zugriffsschutz-konzept.md), measure M5
