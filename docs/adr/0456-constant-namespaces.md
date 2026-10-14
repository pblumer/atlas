# ADR-0456: Constants the installation holds are read under the namespace that holds them, and the installation holds its own

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-14
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0454 gave a process constants written in its model, and ADR-0453 let a constant take
its value from the application on the installation where an instance starts. Using the
two as shipped in 0.12.0 showed four gaps.

- **A bound constant read like any variable.** `<atlas:constant name="TenantId"
  source="application"/>` became a root variable `TenantId`. Nothing at the place it was
  read said where the value came from, and it shared the namespace of every variable the
  process writes. A worker result of the same name was dropped without a word.
- **No value for every application.** A tenant is usually the same for every application
  on an installation. With only application values, each application held its own copy,
  and the copies could drift apart.
- **No facts.** A process that needs to know which environment it runs in, or which
  application it belongs to, had to be given that as a value somebody typed. The
  installation already knows both: the node identity (ADR-0189 §6) names its environment,
  and the application has a name and a portable key.
- **Names in any case.** `TenantId` and `tenantid` are one variable to PowerShell and two
  to Atlas. A constant could not be told from a variable at the place it was read.

There was also a naming problem. The Console called the application's half "Values" while
the Modeler called the model's half "Constants", for one concept.

The question: **how does a model read a value the installation holds — the application's
or the installation's own — and the facts the installation knows, so that the source is
visible where it is read and the model still declares what it reads?**

## Decision drivers

- **The source is visible where the value is read.** `application.TENANT_ID` and
  `server.TENANT_ID` say what `TENANT_ID` cannot.
- **The model declares what it reads.** ADR-0453's deploy warning, its Constants page and
  its refusal of a start without a value all rest on the declaration. Reading without
  declaring would lose all three, because Atlas does not parse script bodies.
- **Nothing for a process that reads nothing.** An extra variable per instance costs a
  write per start. A reserved name breaks a model that already uses it. Neither may fall on
  a process that never asked for a namespace.
- **A spelling never stops a server.** A stored definition is compiled again on every
  restart (ADR-0177).
- **One name for one concept** in the Modeler, the Console, the API and MCP.

## Considered options

1. **Keep the root variables, add server constants as more root variables.**
2. **Namespaces, filled implicitly**: every instance of an application's process carries
   `application` and `server` holding everything the installation holds.
3. **Namespaces, declared** (chosen): a process declares each member it reads, and its
   instances carry only the namespaces and the members it declares.

## Decision outcome

Chosen option: **3, namespaces, declared**.

### Reading

- A constant whose value the installation holds is a **member of a namespace object**, not
  a variable of its own: `application.TENANT_ID` for the application's,
  `server.TENANT_ID` for the installation's. FEEL reads it as a path. PowerShell reads it
  as `$application.TENANT_ID`, because the namespace arrives as a hashtable. JavaScript
  reads it with a dot and Python as `application["TENANT_ID"]`.
- A process constant (ADR-0454) stays a root variable of its own name.
- A namespace is created only in an instance of a process that declares one of its
  members, and is then a constant itself. The model may not write it or ask for it as a
  start variable; a deploy refuses both, and the engine drops a write at run time. A
  variable named like a member (`TENANT_ID`) is the process's own.

### The installation's own constants

- `<atlas:constant name="TENANT_ID" source="server"/>` declares a member of `server`. Its
  value comes from the installation's constants: a sidecar store `server-constants`, one
  record per installation, classified as instance data like the application's (a
  snapshot carries it, an export and a promotion do not).
- `GET /api/v1/server/constants` is open to any signed-in caller, as an application's are
  open to its viewers. `PUT` is admin-only. The Console edits them under **Admin → Engine →
  Server constants**, and MCP reads them with `atlas_server_constants`.
- Missing values are handled exactly as an application's: a deploy warning, a state on the
  page (*set*, *missing*, *fallback*, *type differs*), and a start by a person or the API
  refused with 409 until the value is set. Timer, message and signal starts are not refused.

### Facts

- Each namespace carries a few facts Atlas fills itself, named in lower case:
  - `application.id`, `application.key` (the portable key, ADR-0134) and
    `application.name`;
  - `server.id` (the runtime id), `server.name` and `server.environment`, from the node
    identity.
- They are declared like a constant: `<atlas:constant name="environment" source="server"/>`.
  A fallback may be given; a type other than string is refused.
- A fact that is not set here (a node with no environment) falls back like a missing
  constant. The deploy warns about it, but no start is refused for it.
- Renaming the node or the application reaches the next instance. A running instance keeps
  the value it started with.

### Names in capitals

- Every constant name is written in capitals: letters A–Z, digits and underscores,
  starting with a letter. This holds for a process constant, an application constant and
  a server constant. `APPLICATION` and `SERVER` are reserved, because PowerShell would not
  tell them from the namespaces.
- A deploy refuses any other name, and the API refuses one on save. A reload of a
  definition deployed before the rule keeps the constant under its old name and reports
  it as `constant.name`, so the server still starts.
- The Modeler and the Console apply the rule while a name is typed: umlauts spell out
  (`Ä` → `AE`), and a space, dot or hyphen becomes an underscore.
- Lower case is left to the facts, so a fact and a constant never share a name in one
  namespace.

### Names in the product

| Before (0.12.0) | Now |
|---|---|
| Application *Values* tab | **Constants** tab |
| `GET`/`PUT /api/v1/applications/{id}/values`, body `values` | `/api/v1/applications/{id}/constants`, body `constants` |
| MCP `atlas_application_values` | `atlas_application_constants`, plus `atlas_server_constants` |
| store `application-values` | `application-constants`, plus `server-constants` |

No installation was known to use the 0.12.0 names, so they were renamed without aliases.
A response also carries `updatedByName`, so the pages name the person who last changed
the constants rather than showing an id.

### The Modeler

- The Constants group sets each constant's source: *process*, *application* or *server*.
  A fact's row has a fixed name, type and source.
- The Variables panel lists a declared member by the path it is read under. Below the
  list it shows what the process could read but does not declare yet: the installation's
  constants and each namespace's facts. Each entry has a **Declare** button.
- FEEL and script completion offer the same undeclared members, marked "declares it".
  Picking one inserts the path and adds the declaration to the process. The model keeps
  saying what it reads without a trip to the process panel.

### The engine

- The namespace's value is built once per definition and configuration change, then
  cached on the processor. A start allocates nothing for it after the first (I1).
- Each change to the configuration drops the cache entries it can affect: the
  installation's or an application's constants, a definition moving between applications,
  a node or application rename, an undeploy.
- The value is frozen into the creation event like any variable's, so replay never
  rebuilds it (I4, I6).

### Consequences

- **Positive:**
  - The source of a value is visible in the expression or script that reads it.
  - A tenant is held once per installation.
  - A process can ask which environment it runs in without anybody typing it in.
  - A constant cannot be mistaken for a variable, and cannot collide with one in PowerShell.
- **Negative / trade-offs accepted:**
  - **Breaking against 0.12.0.** A process that read `$TenantId` from a constant bound to
    its application now reads `$application.TENANT_ID`, and a lower-case constant no
    longer deploys. A stored lower-case one keeps loading, reported.
  - **Reserved names.** A process that declares a member of a namespace cannot use a
    variable of that name (`application`, `server`). A process that declares none keeps
    the name free.
  - **Python reads a member with brackets**, because the namespace arrives as a dict.
- **Follow-ups / risks to watch:**
  - **Promotion pre-check.** Promotion could say before it ships which declared constants
    the target installation does not hold. The target's deploy warns today.
  - **More facts.** The facts are a fixed list. Adding one is a compiler, engine and
    Modeler change together, on purpose: a fact is a promise about every installation.

## Pros and cons of the options

### Option 1 — root variables
- Good: no reserved names, and no change for 0.12.0 models.
- Bad: the source stays invisible where a value is read, the collisions stay, and an
  installation's constant would be one more anonymous root variable.

### Option 2 — namespaces, filled implicitly
- Good: no declaration to keep.
- Bad: every instance of every process carries both objects and reserves both names. No
  deploy can say which value a process needs, so the warning, the Constants page and the
  409 refusal all go.

### Option 3 — namespaces, declared (chosen)
- Good: the source is visible, the checks stay, and nothing falls on a process that reads
  nothing. The Modeler writes the declaration when a member is picked.
- Bad: a declaration to keep, which the Modeler offers to write, and a breaking change
  against 0.12.0.

## Links

- amends [ADR-0453](0453-application-values-per-installation.md) (an application's values become its constants, read as application.NAME)
- amends [ADR-0454](0454-process-constants.md) (constant names in capitals)
- relates to [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) (the node identity the server facts come from)
- relates to [ADR-0177](0177-reload-skips-the-deploy-gate.md) (a rule gates a deploy, not a reload)
- relates to [ADR-0105](0105-per-server-call-activity-target-overrides.md) (operator configuration pushed into the processor)
