# ADR-DRAFT: The element-template applier, and what a template binding means in Atlas

- **Status:** Proposed
- **Date:** 2026-08-31
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0027](0027-element-templates.md) chose the Camunda element-template format for
reusable, shareable task configuration, and described applying one as "pure data over
the ADR-0025 write path". [ADR-0081](0081-community-marketplace-for-connectors-and-tasks.md)
then built distribution on top of that format: a curated catalog compiled into the
binary, an install endpoint, and a durable store of installed templates.

**The applier ADR-0027 decided was never built.** The distribution half exists without
the half that gives it an effect:

- There is no `/api/v1/templates` route, and no applier anywhere in the tree. The only
  files mentioning element templates are `api/repository.go`, `api/repositorystore.go`
  and `api/openapi.go` — the distribution machinery itself.
- The Modeler does not read the installed-template store. Its service-task kind picker
  is `SERVICE_TASK_KINDS` in `api/web/editor.js`, hand-written and compiled into the
  binary; the thirteen API paths `editor.js` fetches contain no template or repository
  route.
- The only reader of `/api/v1/repository/installed` is the Console gallery, which uses
  it to render an "Installed" pill and to uninstall.

So installing a package records a choice and puts nothing in the palette. This surfaced
when someone went looking for the Jira connector in the Repository, found no package,
and asked how to install and enable it — a question the Console's own "Install" button
invites and cannot answer.

**The harder half is not plumbing.** The nine catalog packages carry bindings whose
*meaning in Atlas* has never been decided. In the schema Atlas cites, a
`{"type": "property", "name": "atlas:connector"}` binding writes an attribute on the
element the template is applied to:

```xml
<bpmn:serviceTask id="t" atlas:connector="acme" atlas:operation="create-issue"/>
```

The Atlas compiler reads none of that. It reads a child element inside
`<extensionElements>`, one per connector kind:

```xml
<bpmn:serviceTask id="t">
  <bpmn:extensionElements>
    <atlas:jiraConnector connector="acme" operation="create-issue"/>
  </bpmn:extensionElements>
</bpmn:serviceTask>
```

A faithful applier would therefore produce models the engine silently ignores — which is
exactly the failure ADR-0027's own follow-up list names: *"keep the applier honest so a
template can never write an extension the compiler will reject."* Nothing in a template
today says which extension element it configures, so the applier cannot bridge the gap
by inspection. That is the decision this record exists to make.

Note also that the schema is only *cited*, by a `$schema` URL on unpkg.com; it is not
vendored (ADR-0013 vendored the modeller toolkit, not this schema). Whatever we decide,
nothing validates these files against the upstream schema today.

## Decision drivers

- **Never write XML the compiler rejects or ignores.** A template that produces a task
  which deploys and does nothing is worse than no template at all: it fails at the
  moment a token reaches it, far from the edit that caused it.
- **The Console must not offer an action that has no effect.** An "Install" button whose
  observable result is a pill is a promise the product does not keep.
- **Interop was ADR-0027's main reason for this format.** A binding dialect only Atlas
  understands gives up the reason the format was chosen.
- **The buildless UI constraint** (ADR-0012): the panel is plain JS in the browser, so
  the applier must be simple enough to live there without a build step.
- **The built-in kinds already have a panel.** `SERVICE_TASK_KINDS` covers every kind
  that ships, with per-operation field visibility a flat template cannot express. So the
  value of templates is *third-party and community* kinds, and pre-filled variants of
  built-in ones — not the built-ins themselves.
- **There are zero third-party kinds today.** Every catalog package configures a kind the
  panel already offers.

## Considered options

1. **Name the extension element in the manifest.** Add one Atlas field, e.g.
   `"extension": "atlas:jiraConnector"`, and define a `property` binding as an attribute
   *on that element* rather than on the task. The template body stays Camunda-shaped.
2. **An Atlas-specific binding type**, e.g. `{"type": "atlas:connectorProperty",
   "element": "atlas:jiraConnector", "name": "connector"}`.
3. **Infer the extension from the kind**, by matching the template against
   `SERVICE_TASK_KINDS` and reusing that entry's `ext`.
4. **Do not build the applier.** Make the Repository an explicit documentation gallery:
   drop or disable Install, and let `SERVICE_TASK_KINDS` remain the single source of what
   the panel offers.

## Decision outcome

Chosen option: **"Name the extension element in the manifest"** (1), with option 4's
honesty fix applied immediately and independently.

Two things, deliberately separable, because they have very different sizes:

**Now, and regardless of the rest:** the Console must stop presenting Install as if it
changed modelling. Either the action says what it does ("Add to library" with the
gallery as its only effect) or it is disabled with the reason shown. This is small, and
it is owed today — the misleading state is live.

**Then:** build the applier under option 1. A package gains one field naming the
extension element it configures; the applier writes that element into
`<extensionElements>` and sets the bound attributes on it; a template naming an extension
the compiler does not read is refused at *validate* time, in `validatePackage`, where the
catalog is already checked and where the server already refuses to start on a malformed
package.

Option 1 over option 2 because the binding stays the upstream one and only the *target*
is named — an author's existing template needs one added field, not a rewrite, which
keeps ADR-0027's interop argument intact. Over option 3 because inference makes the
mapping invisible: a template would apply correctly or silently wrongly depending on a
table in a JavaScript file, and a third-party kind — the case templates exist for — has
no entry there at all.

Option 4 is not a strawman and may well be right *for now*: with zero third-party kinds,
the applier serves pre-filled variants of kinds the panel already covers, which is real
but modest value. **If community kinds are not on the near roadmap, accept option 4 and
revisit** — the immediate fix above is the same either way, and it is the part that
matters this week.

### Consequences

- **Positive:** an install finally does what the word says; a template can no longer
  produce a task the engine ignores, because the target extension is checked where the
  package is validated; third-party connector kinds become expressible without a change
  to `editor.js`.
- **Negative / trade-offs accepted:** one Atlas-specific manifest field, so a package is
  not byte-identical to an upstream one (the bindings still are); the flat template
  cannot express the per-operation field visibility the hand-written panel has, so a
  templated multi-operation kind is a worse editing experience than the built-in panel —
  which is an argument for keeping `SERVICE_TASK_KINDS` as the primary path for built-in
  kinds rather than migrating them onto templates.
- **Follow-ups / risks to watch:** the nine existing packages need the new field, and
  until they have it the applier must refuse them rather than guess; "template updated,
  instances already applied" remains open from ADR-0027 and now also spans installs;
  vendoring the element-templates schema, so `validatePackage` can check shape rather
  than only its own invariants.

## Pros and cons of the options

### 1 — Extension named in the manifest *(chosen)*
- Good: bindings stay upstream-compatible; the target is explicit and checkable at
  validate time; one added field per package.
- Bad: a small dialect of our own; existing packages must be migrated.

### 2 — Atlas-specific binding type
- Good: fully explicit per property; no manifest-level field.
- Bad: gives up the interop that was ADR-0027's reason for the format; an author's
  existing template must be rewritten rather than annotated.

### 3 — Infer from `SERVICE_TASK_KINDS`
- Good: no format change at all; the nine existing packages work untouched.
- Bad: the mapping lives in a browser file and is invisible from the package; a
  third-party kind has no entry, so precisely the case templates exist for is the case
  that cannot work.

### 4 — Do not build it
- Good: no new surface; honest immediately; the panel stays the single source of truth.
- Bad: leaves ADR-0027 decided-but-unbuilt and ADR-0081 distributing a payload nothing
  applies; community connectors stay blocked on an `editor.js` edit.

## Links

- builds on [ADR-0027](0027-element-templates.md) — the format, and the applier it decided
- relates to [ADR-0081](0081-community-marketplace-for-connectors-and-tasks.md) — distribution built on that format
- relates to [ADR-0012](0012-web-ui-app-shell.md) — the buildless UI the applier must live in
- relates to [ADR-0067](0067-service-task-connector-catalog.md) — the connector catalog `SERVICE_TASK_KINDS` renders
- prompted by [ADR-0201](0201-jira-connector.md) — the connector whose missing package exposed the gap
