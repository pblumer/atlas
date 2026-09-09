# ADR-DRAFT: An OpenAPI document as element templates

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-09
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0217](0217-openapi-mock-server.md) made an OpenAPI document *answer* like the API it
describes, so a process with a REST connector task can be run before that API exists.
It left the other half untouched: the task still has to be configured by hand. Somebody
reads the document, copies the method, pastes the URL, works out which path parameters
the process must hold, and types a result variable — once per operation, for every
process that calls it, getting it slightly wrong somewhere.

The document already states all of it. [ADR-0027](0027-element-templates.md) chose the
bpmn.io element-templates schema for exactly this kind of reusable task configuration,
and [ADR-0081](0081-community-marketplace-for-connectors-and-tasks.md) wraps a template
in a package with an id, a version and an author. What was missing is anything that
turns one into the other.

The awkward part is that **the applier does not exist**.
[ADR-0212](0212-element-template-applier.md) decided how a template binding writes onto
a task and is Proposed with its implementation not started; a running server's catalog
is compiled into the binary (`//go:embed repository_catalog/*.json`) and installing
takes a catalog id, not a file. So a generated package can be neither applied nor
loaded today. That is the thing to be honest about rather than to discover later.

## Decision drivers

- **The document is the source of truth, and it is already written.** Anything a person
  retypes from it is a place to be wrong.
- **The same file should serve both halves.** One document, a mock to call and a
  template to configure the caller — the reader is written either way.
- **A template must not invent what the document cannot decide.** A field filled in
  with a guess is worse than an empty one, because it looks decided.
- **Generating for a gap that is not yet built is a cost.** Whatever is produced has to
  be worth having before the applier lands, or it should wait for it.

## Considered options

1. **Generate packages now, in the catalog's own shape**, and say plainly what can and
   cannot be done with them until ADR-0212 is built.
2. **Wait for the applier.** Build nothing until a generated template can be applied.
3. **Generate a different, Atlas-only shape** designed around what the modeller can
   consume today.
4. **Emit BPMN snippets** — a ready `<atlas:restConnector>` element per operation, to
   paste into a model.

## Decision outcome

Chosen option: **"generate packages now, in the catalog's own shape"**, in package
`connector/rest/openapitemplate`, behind `atlas openapi-template`.

- **One package per operation**, with the bindings the bundled `rest-outbound` package
  already uses — `atlas:method`, `atlas:url`, `atlas:headers`, `atlas:authType`,
  `atlas:credentialsRef`, `atlas:resultVariable`. Nothing new is invented at the seam:
  a generated package differs from a hand-written one only in who typed it.
- **Method is fixed** to the operation's, as a dropdown with that one choice. A dropdown
  offering the others would offer a request the template does not describe.
- **The URL is filled in**: the document's first server plus the operation's path,
  literal where there is nothing to substitute and a FEEL expression where there is —
  `="https://api.digitalocean.com/v2/droplets/" + string(droplet_id)`. The document knows
  where the value goes; only the process knows what it is, so the parameter becomes a
  variable and the description names it.
- **What the document cannot decide is left empty**: headers, authentication, the
  credential reference. In particular `securitySchemes` are *not* mapped onto Atlas's
  auth types — the useful ones need a token endpoint and a client id that live on the
  server (ADR-0041), and a guess there is a wrong answer wearing a filled-in field.
- **A URL with no host is called out.** A document may name no server, and it may name a
  relative one — Swagger's own Petstore declares `/api/v3`. Both produce a URL no task
  can call, and the second is the one nobody notices, because the field looks complete.
  The generated description says so.

Option 2 was rejected on the second look rather than the first: the generator is the
reader of ADR-0217 pointed the other way, so it costs a package rather than a project,
and building it now means the OpenAPI path works the day the applier lands. Option 3
throws away interop for nothing, which is the argument ADR-0027 already had. Option 4
produces something usable today and unmaintainable tomorrow: a pasted element carries no
identity, so nothing can update it, which is the whole point of a template.

### Consequences

- **Positive:** an API's operations become task configuration without anybody retyping
  them; the same document that mocks the API configures its caller; a generated package
  is a catalog package, so the applier gets its input for free.
- **Negative / trade-offs accepted:** until ADR-0212 is built, the output is a file to
  commit or to keep, not one to install — the CLI says this where it writes them, which
  is a poor substitute for it simply working. A large document produces a large number
  of packages (DigitalOcean's is 659), and nothing here decides which of them are worth
  having.
- **Follow-ups / risks to watch:** the applier (ADR-0212) is what makes this useful;
  installing a package that is not in the bundled catalog is a second gap and its own
  decision; mapping `securitySchemes` onto configured credentials could be done later
  from the server side, where the credential actually lives.

## Pros and cons of the options

### Option 1 — generate now, in the catalog's shape
- Good: costs a package, not a project; ready for the applier; interop with the
  element-templates ecosystem.
- Bad: produces artifacts nothing can install yet, which has to be said out loud.

### Option 2 — wait for the applier
- Good: nothing is built that cannot be used.
- Bad: the applier then lands with no source of templates, and the OpenAPI half is a
  second project rather than a day's work already done.

### Option 3 — an Atlas-only shape
- Good: could target exactly what the Console does today.
- Bad: discards the schema ADR-0027 chose, and the catalog would hold two kinds of
  template.

### Option 4 — BPMN snippets to paste
- Good: usable immediately, with no applier.
- Bad: a pasted element has no identity, so it cannot be updated, versioned or listed —
  which is what a template is for.

## Links

- follows [ADR-0217](0217-openapi-mock-server.md) (the same document, serving the other half)
- builds on [ADR-0027](0027-element-templates.md) (the element-templates schema) and [ADR-0081](0081-community-marketplace-for-connectors-and-tasks.md) (a template inside a package)
- blocked for usefulness by [ADR-0212](0212-element-template-applier.md) (applying a template to a task)
- relates to [ADR-0041](0041-connector-management-and-secret-store.md) (why credentials are not generated into a template)
