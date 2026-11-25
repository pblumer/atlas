# ADR-0564: What a script task may reach when nobody said

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-24
- **Deciders:** Atlas maintainers (to decide; put forward after the security review of
  2026-11-13)
- **Open question:** how many installations run script tasks that read files outside
  their scratch directory or open a network connection. Every option below that changes
  a default is judged by that number, and nothing in the product reports it.
- **Question checked:** 2026-11

## Context and problem statement

Atlas runs model-authored PowerShell, Python and JavaScript in interpreter processes
(ADR-0047). Two defaults decide what that means for an installation that set nothing:

- **The languages are on.** `--python`, `--javascript` and `--powershell` default to
  `true` in the binary; the chart turns Python and JavaScript on and PowerShell off,
  because the default image ships no `pwsh`.
- **The sandbox is off.** `--script-sandbox` defaults to `off` in the binary and in the
  chart. ADR-0303 chose that deliberately — "an upgrade does not revoke filesystem or
  network capabilities from already-deployed models" — and named the condition under
  which it could change: capability declarations per script (#895).

With both, a script runs under the operating-system account of the atlas process and
reaches what that account reaches: the data directory, the vault key and with it every
stored credential, the environment of atlas processes under the same account, and the
network. `docs/install.md` ("What a script can reach with `--script-sandbox=off`") says
so in full, and the compliance concept carries it as risk R-09.

Deploying a model needs the role `modeler`. Put together: **on an installation with
default settings, the role `modeler` can do what the role `admin` can do, and more** —
it can read what no role is meant to read. The role model (ADR-0209) presents `modeler`
as an authoring role below `admin`. An operator who grants it by that description
grants more than the description says. The product already draws the line once: trying
a script from the Console (`POST /api/v1/scripts/run`) needs `admin`. Deploying the
same script inside a model does not.

This is a documented property and not a defect in the code: every piece behaves as its
record describes. The question is whether the *defaults* should keep producing it, and
what a change costs the installations that exist.

## Decision drivers

- **A role should mean what its name says** on an installation nobody tuned.
- **An upgrade must not stop running processes** without the operator having been told
  beforehand and given a way to keep them (ADR-0303's driver, still valid).
- **Scripts that call a service are the common case,** not the exception. A default
  that forbids sockets forbids most of what script tasks are used for.
- **A security setting that has to be discovered protects the installations that need
  it least.**
- **Windows and macOS have no `strict`.** Whatever is decided has to say what happens
  there.
- **No best-effort isolation** (ADR-0303): a profile either holds or the server says it
  cannot provide it.

## Considered options

1. **Leave both defaults** and rely on documentation, R-09 and a small circle of
   `modeler` accounts.
2. **`strict` as the default everywhere** — binary, image and chart.
3. **`strict` as the default of the image and the chart only;** the bare binary keeps
   `off`.
4. **The languages off by default;** an operator turns on what the installation uses.
5. **A middle profile as the default, once it exists** (#893): the data directory and
   the environments of other processes are closed, the network and the rest of the
   filesystem stay as they are. `strict` remains the opt-in for scripts that need
   neither.
6. **A separate permission for code.** Deploying a model that carries a script task
   needs more than `modeler`: `admin`, as trying a script already does, or a role of
   its own.
7. **No silent default.** A server that has a script language on and was given no
   `--script-sandbox` value says so at start — as a warning first, as a refusal to
   start in a later release.

The options are not exclusive: 5, 6 and 7 answer different halves of the problem.

## Decision outcome

**Not decided.** This record is put forward for the decision; nothing in the code
changes with it.

Recommended: **7 now, 5 as the default when #893 is built, and 6 decided separately on
its own merits.** Options 2 and 4 are not recommended as defaults today.

### Why this combination

- **7** costs no installation anything and removes the one thing that makes the present
  state hard to defend: that it is reached by saying nothing. The warning names the
  setting, the three values and the section of `docs/install.md`. Turning it into a
  refusal to start is a second step with a release between, announced in the first.
- **5** is the change that makes `modeler` mean `modeler` without breaking the common
  script. What separates `modeler` from `admin` in practice is the data directory, the
  vault key and the environments of the atlas processes — exactly what #893 closes —
  and not the network, which a `modeler` reaches through any REST connector anyway.
  Where the profile cannot be installed (Windows, macOS, a kernel without Landlock
  ABI 3), the server has to say so at start and run `off` only when told to, which is
  option 7 again.
- **6** is the only option that works on every platform, because it is authorization
  and not isolation, and it has a precedent in the route that tries a script. It is
  also a change to the role model, with its own questions (which role; what happens to
  deploy tokens and to application imports that carry a script task), and deserves its
  own record rather than a paragraph here.

### Why not the others

- **1** keeps a state in which the role table is right only for installations whose
  operator read one section of the install guide.
- **2 and 3** stop every script that opens a socket or reads a file outside its scratch
  directory, on the next upgrade, on installations whose operators set nothing — and
  3 does it only for the people who use the image, which is most of them. ADR-0303
  made #895 and #898 the precondition for that, and neither is built.
- **4** stops every script task on every installation that did not set the flags,
  leaving jobs pending. It protects installations that use no scripts, which lose
  nothing under 5 either.

### What each option does to an existing installation

| Option | Upgrade with no setting changed | Platform without Landlock |
|---|---|---|
| 1 | nothing changes | nothing changes |
| 2 | scripts that use the network or files outside scratch fail | server must be told `off` explicitly, or refuses script languages |
| 3 | as 2 for image and chart users; nothing for the bare binary | as 2 |
| 4 | every script task stops until the language is turned on | same |
| 5 | scripts that read the data directory or another process's environment fail; others run | as 2 |
| 6 | running models keep running; the next deployment with a script task needs the new permission | same on every platform |
| 7 (warning) | one more line at start | same |
| 7 (refusal, later) | the server does not start until `--script-sandbox` is set | same |

### Scale-out readiness

None of the options touches partition state, routing or the log. The sandbox profile is
a property of the process that starts an interpreter and has to be the same on every
node and every supervised worker; option 6 is a check at deployment, made once per
command before it is appended.

### Consequences

- **Positive (of the recommendation):** the default installation stops equating
  `modeler` with `admin`; no running process stops on the upgrade that introduces the
  warning; the change of default arrives with a profile that keeps the common script
  working.
- **Negative / trade-offs accepted:** until #893 exists, the state described above
  stays, softened by a warning. Option 5 still breaks a script that reads the data
  directory on purpose; no such use is known, and it is the use the profile exists to
  end.
- **Follow-ups / risks to watch:** #893 (the middle profile), #895 (capabilities per
  model, the condition for `strict` as a default), #896 (consumption is not bounded by
  any profile), #897 (an operating-system identity of its own for script workers, which
  would close the data directory by ownership rather than by policy), #898 (an egress
  policy between "all" and "none"). A count of affected installations — the open
  question above — would change this record's weighting and nothing collects it.

## Pros and cons of the options

### 1. Leave both defaults
- Good: nothing breaks; nothing to build.
- Bad: the role model is accurate only where an operator hardened by hand.

### 2. `strict` everywhere
- Good: the strongest default; one sentence to explain.
- Bad: breaks the common script on upgrade; unavailable on two of three platforms;
  ADR-0303's stated preconditions are not met.

### 3. `strict` in image and chart
- Good: the hardened default reaches the deployment most installations use.
- Bad: as 2 for exactly those installations; binary and image then disagree about a
  security default, which is its own source of surprise.

### 4. Languages off
- Good: an installation without scripts has no interpreter path at all.
- Bad: every installation with scripts stops on upgrade; does nothing for the ones that
  turn a language on, which then sit at `off` as before.

### 5. Middle profile as default
- Good: closes what separates `modeler` from `admin`; keeps network scripts working.
- Bad: does not exist yet (#893); Linux only; leaves the network open.

### 6. A separate permission for code
- Good: platform-independent; makes "who may run code" a line in the role table.
- Bad: changes the role model and every credential that carries `modeler`; does not
  limit what a permitted script reaches.

### 7. No silent default
- Good: free for every installation in its first step; makes the choice visible where
  operators look.
- Bad: a warning changes nothing by itself; the refusal step is a breaking change that
  has to be announced a release ahead.

## Links

- builds on [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md) and
  [ADR-0303](0303-script-sandbox-isolation.md), whose default this record reopens
- relates to [ADR-0209](0209-roles-per-endpoint-group.md) (the role a route asks for)
- issues [#893](https://github.com/pblumer/atlas/issues/893),
  [#895](https://github.com/pblumer/atlas/issues/895),
  [#896](https://github.com/pblumer/atlas/issues/896),
  [#897](https://github.com/pblumer/atlas/issues/897),
  [#898](https://github.com/pblumer/atlas/issues/898)
- `docs/install.md`, "What a script can reach with `--script-sandbox=off`";
  `docs/compliance/isds-konzept.md`, R-09 and M-09
