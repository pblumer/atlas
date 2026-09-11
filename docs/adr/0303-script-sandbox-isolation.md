# ADR-0303: Isolate general-purpose script tasks inside a restrictive OS sandbox

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-09-09
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas executes model-authored PowerShell, Python and JavaScript in interpreter
processes owned by a script Worker Instance. ADR-0297 confines that worker's bearer
credential to the worker protocol, and the interpreter receives an environment
allowlist without that credential. Those controls reduce what a stolen token can
do, but they do not make the interpreter an operating-system security boundary.

On a server binary and in the default container, the interpreter still has the
worker's operating-system identity. It can read every file that identity can read,
including the Atlas data directory, inspect same-user processes where the host
allows it, open network connections, and leave child processes behind after its
parent exceeds the wall-clock timeout. A read-only container root and a dedicated
Worker Instance do not close those paths: `/data` is deliberately readable by the
Atlas uid, and containers in one Pod share a network namespace.

The question is how Atlas can provide a useful per-execution boundary without
requiring a privileged helper, a Docker socket, or Linux user namespaces that are
commonly unavailable under container and Kubernetes security profiles. The rollout
must also acknowledge existing processes whose scripts deliberately read files or
call services.

## Decision drivers

- Model-authored code must not be able to read Atlas state or deployment files.
- A script that needs no network must not be able to reach Atlas or another service.
- Restrictions must be inherited by subprocesses and cannot be removable by the
  interpreted program.
- The default image and Helm chart must keep their non-root uid, read-only root,
  dropped capabilities and `RuntimeDefault` seccomp profile.
- Existing script processes must not stop working merely because Atlas is upgraded.
- An operator selecting the strict profile must get fail-closed startup and execution,
  never a silent fallback to unsandboxed code.

## Considered options

1. Run every interpreter through Bubblewrap namespaces.
2. Run a built-in Atlas sandbox helper using Landlock and seccomp.
3. Put the script Worker Instance in a separate OCI/Kubernetes workload.
4. Keep only the environment allowlist and worker-token scope.

## Decision outcome

Chosen for this slice: **a built-in, opt-in strict Linux profile using Landlock and
seccomp**.

`--script-sandbox=off|strict` configures both in-process and supervised script
workers. `off` remains the initial default so an upgrade does not revoke filesystem
or network capabilities from already-deployed models. `strict` is an explicit
security contract: Atlas first proves the interpreter and the required Landlock ABI
are available, and refuses execution if it cannot install the complete policy.
There is no best-effort mode.

Because an allowlist cannot subtract a child path after granting its parent,
strict startup also refuses an Atlas data directory placed below an allowed system
runtime root such as `/usr`. The normal `/data`, `/var/lib/atlas`, and
working-directory-relative locations remain valid.

For each strict execution, the Worker Instance creates a new private scratch
directory and starts the Atlas executable as a small internal sandbox launcher. The
launcher locks itself to one operating-system thread, sets `no_new_privs`, installs
a Landlock allowlist, installs a seccomp filter that refuses creation of network and
Unix-domain sockets, changes into the scratch directory, and replaces itself with
the resolved interpreter. The interpreter can read and execute the installed system
runtime, read the small set of loader and trust files needed to start, and read and
write only its own scratch hierarchy. It receives that hierarchy as `HOME` and all
temporary-directory variables. It cannot read Atlas's working directory, `/data`,
or another execution's scratch directory.

The existing wall-clock timeout is applied to a new process group. When it expires,
Atlas kills the whole group, not only the direct interpreter process. The existing
bounded stdout and stderr capture remains in force.

Landlock is chosen over a mount namespace because it is specifically designed for
unprivileged self-restriction and composes with the container's existing access
controls. Bubblewrap itself documents that it constructs policy rather than being a
policy, and its unprivileged mode depends on user namespaces; nested user namespaces
are not a reliable assumption for the supported container and Kubernetes path.

### Consequences

- **Positive:** strict scripts cannot read Atlas durable state, deployment secrets or
  other host files outside the runtime allowlist, and cannot create network sockets.
- **Positive:** restrictions follow every subprocess, and timeout cleanup covers the
  complete process group.
- **Positive:** the mechanism needs no setuid executable, capability, Docker socket,
  writable root filesystem or additional image package.
- **Negative / trade-offs accepted:** strict mode is Linux-only and requires a kernel
  with Landlock ABI 3 or newer. Windows and macOS keep the compatible `off` mode.
- **Negative / trade-offs accepted:** a process must be reviewed before opting in if
  its script intentionally reads local files, imports code outside the system runtime,
  uses Unix sockets, or calls a network service.
- **Negative / trade-offs accepted:** Landlock restricts access rather than creating a
  new filesystem image. Some path metadata may remain observable even though file
  contents and directory listings are denied.
- **Defect in what first landed, since fixed:** ~~the strict allowlist omits `/proc` and
  `/etc/passwd`, which the .NET runtime requires, so PowerShell does not start under
  `strict` at all. The failure surfaces on the first script job rather than at startup,
  because `CheckSandbox` proves the Landlock ABI and never that an enabled interpreter can
  start. Python and JavaScript are unaffected.~~ The allowlist now carries this process's
  own `/proc` entry, `/proc/meminfo`, `/proc/mounts` and `/etc/passwd` — its own entry and
  no other, because `/proc` as a whole would expose every same-uid process's environ. And
  strict no longer takes the profile on trust: it starts each enabled interpreter once,
  inside the real policy, and refuses to start the process when one cannot
  ([#892](https://github.com/pblumer/atlas/issues/892)).
- **Follow-ups / risks to watch.** Each one now has an issue, so it can be picked up by
  somebody who never reads this record:
  - **Model-level capability declarations**, so a profile is chosen per script rather than
    per installation. Until then the installation-wide setting follows its most demanding
    script, which is what keeps `off` the default
    ([#895](https://github.com/pblumer/atlas/issues/895)).
  - **Dedicated cgroups or workloads** for aggregate memory and CPU accounting. Landlock
    restricts access, not consumption, so the wall-clock timeout is the only bound a
    script's memory use has ([#896](https://github.com/pblumer/atlas/issues/896)).
  - **A distinct OS identity** for script Worker Instances. Sharing the engine's uid is
    what makes every file the engine can read readable by a script as well
    ([#897](https://github.com/pblumer/atlas/issues/897)).
  - **An optional network egress policy**, so a script that legitimately calls one service
    is not pushed back to `off` by the all-or-nothing socket ban
    ([#898](https://github.com/pblumer/atlas/issues/898)).

## Pros and cons of the options

### Bubblewrap namespaces

- Good: an empty mount namespace can make disallowed paths absent and a network
  namespace gives an intuitive boundary.
- Bad: unprivileged Bubblewrap now depends on user namespaces, which hosts and
  container seccomp profiles may disable. Enabling capabilities or setuid support to
  make arbitrary-code execution work would weaken the deployment boundary.
- Bad: Atlas would still have to define and maintain the filesystem policy and add an
  external runtime dependency to every installation.

### Built-in Landlock and seccomp profile

- Good: unprivileged, inherited, irreversible restrictions that compose with normal
  Unix permissions and the existing container security context.
- Good: one typed launcher path is identical for Python, JavaScript and PowerShell.
- Bad: Linux and kernel-version specific; Landlock does not provide cgroup resource
  accounting or make denied path metadata disappear.

### Dedicated OCI/Kubernetes workload

- Good: strongest operational boundary and natural place for a dedicated identity,
  cgroup limits and Kubernetes NetworkPolicy.
- Bad: no uniform mechanism exists for the standalone binary to create such a
  workload, and giving Atlas a container-runtime socket would be a larger privilege.
- Bad: a sidecar in the same Pod still shares the Pod network; a separate Pod also
  needs automatic per-worker credential lifecycle and deployment orchestration.

### Environment and token confinement only

- Good: portable and compatible; already landed in ADR-0297.
- Bad: it does not stop file access, network access, same-user attacks or surviving
  subprocesses, so it is defence in depth rather than a sandbox.

## Links

- complements [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md)
- implements part of the sandbox follow-up in
  [ADR-0297](0297-confine-internal-worker-token.md)
- relies on the Linux kernel's Landlock unprivileged access-control contract
  ([kernel documentation](https://docs.kernel.org/userspace-api/landlock.html))
- compares Bubblewrap's documented user-namespace and policy model
  ([upstream documentation](https://github.com/containers/bubblewrap/blob/main/README.md))
