# ADR-0027: Element templates for pre-configured, reusable elements

- **Status:** Proposed
- **Date:** 2026-07-23
- **Deciders:** Atlas maintainers

## Context and problem statement

The reference modeler's "Implement" panel offers **Template → Select**: applying a
named template turns a bare task into a pre-configured element — a fixed set of
properties (a job type, specific input/output variables, documentation) with a
curated form so the modeler only sees the fields that element needs. This is how
connectors (REST, a queue, an email sender) are packaged: the integration author
ships a template, the process author fills in a few typed inputs.

Atlas authors service tasks by hand today (ADR-0013): the user types a raw job
type and, once ADR-0025 lands, raw I/O mappings. That is flexible but error-prone
and unshareable — every team re-types the same "call our REST connector" wiring.

The question: **should Atlas support element templates, and in what format**, so a
worker/connector author can package a reusable, validated element without Atlas
inventing a bespoke schema or a plugin runtime?

## Decision drivers

- **Reuse an existing format, don't invent one.** The bpmn.io ecosystem already
  defines an **element-templates** JSON schema that the vendored toolkit (ADR-0013)
  understands. Adopting it means interop with existing connector templates.
- **Templates are data, not code.** Applying a template must only set BPMN
  properties the engine already runs (a job type, `zeebe:ioMapping`,
  documentation). No new execution path, no plugin sandbox.
- **Buildless (ADR-0012).** Loading and applying templates must work in the
  hand-written panel without a bundler.
- **Deployable-as-authored.** A template must only produce models the compiler
  accepts; it is sugar over ADR-0025's properties, never a way around the gate.

## Considered options

1. **Adopt the bpmn.io element-templates JSON schema**; ship a small applier in
   the hand-written panel that maps template properties to the same extensions
   ADR-0025 writes. Templates are loaded from a server-served catalog directory.
2. **Invent an Atlas-specific template format** tuned to exactly our extensions.
3. **No templates** — hand-author every element.

## Decision outcome

Chosen option: **Option 1 — adopt the bpmn.io element-templates schema.** A
template is a JSON document (id, version, applies-to element types, a list of
typed properties bound to `zeebe:*` extensions). The server serves a catalog of
templates from a directory under the data dir (alongside deployments and drafts,
ADR-0019/0021); the Modeler's "Template → Select" lists them, applies the chosen
one by writing its bound properties through the modeling API (the same write path
as ADR-0025), and renders only the template's declared fields. Applying a template
never bypasses the compiler — the result is ordinary executable BPMN.

Option 2 is rejected: it throws away interop with an established schema and a body
of existing connector templates for no gain. Option 3 is the status quo this ADR
improves on.

### Consequences

- **Positive:** Reusable, shareable, validated elements; connector authors ship a
  JSON file, not code; interop with the existing element-templates ecosystem;
  applying a template stays pure data over the ADR-0025 write path.
- **Negative / trade-offs accepted:** We implement a subset of the template
  schema's property bindings (only those mapping to extensions Atlas runs) and
  must document what is unsupported; template versioning/upgrade of already-applied
  elements is its own follow-up.
- **Follow-ups / risks to watch:** Decide how a catalog is populated (bundled
  defaults vs. user-dropped files); handle "template updated, instances already
  applied"; keep the applier honest so a template can never write an extension the
  compiler will reject.

## Links

- builds on ADR-0025 (the property write path templates target) and ADR-0013
  (vendored toolkit that defines the element-templates schema)
- relates to ADR-0019/0021 (server-side catalog storage), ADR-0007 (service task
  job types templates configure)
