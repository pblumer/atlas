# ADR-0014: Pluggable form providers

- **Status:** Accepted
- **Date:** 2026-07-03
- **Deciders:** Core team

## Context and problem statement

User tasks (ADR-0013) and start events present a **form** to a human: a schema
describing fields, layout, and validation, rendered at runtime and producing a
set of variables on completion. We want two things that are in tension if handled
naively:

- Offer our **own form engine** — a native Atlas form schema and renderer — so
  Atlas is batteries-included and not dependent on a third party for a core
  experience.
- Let teams use **forms.io** (open source), because many already have forms.io
  form definitions and tooling and should not have to rebuild them to adopt Atlas.

The risk is baking one form technology into the engine and the tasklist, which
would either lock users into our schema or make forms.io a bolted-on special
case. We need a decision that treats forms as *pluggable* from the start.

## Decision drivers

- **No form technology in the engine.** The processor executes control flow; it
  must not parse, validate, or render forms (invariant 5 spirit — no interpreting
  on the hot path).
- **Batteries-included.** A native form engine ships and is the default, so a new
  user needs nothing external to build a working form.
- **Interoperability.** forms.io definitions are a first-class input, not a
  second-class import.
- **Extensibility.** Adding a third provider later must not touch the engine.
- **Consistent output contract.** Whatever the provider, completing a form yields
  the same thing: a variables map fed to `CompleteUserTask`.

## Considered options

1. **Native form engine only.** One schema, one renderer, ours.
2. **forms.io only.** Adopt forms.io as the form technology.
3. **Pluggable form providers.** A form is a stored artifact tagged with a
   provider discriminator; the tasklist selects a matching renderer. Ship a
   native provider (default) and a forms.io provider.

## Decision outcome

Chosen option: **pluggable form providers, with a native engine as the default
and forms.io as a first-class provider.**

- A **form is a design-time artifact** stored in the model repository (ADR-0011's
  design-time store), versioned alongside the process. It carries a **provider
  discriminator** (`atlas` | `formio` | future) plus the provider-specific schema.
- A user task (or start event) references a form by id + version. The **engine
  stores only that reference** on the `UserTaskCreated` event (ADR-0013); it never
  reads the schema. Forms stay entirely out of the engine and off the hot path.
- The **tasklist surface** resolves the reference, picks the renderer by
  discriminator, renders the form, and on submit produces a **variables map** —
  the single output contract regardless of provider — passed to `CompleteUserTask`.
- **Native provider (default):** an Atlas form schema plus a renderer component,
  usable for user-task forms and start forms. This is the batteries-included path.
- **forms.io provider:** store the forms.io definition as the artifact and embed
  the forms.io (open source) renderer in the tasklist to render it.
- Providers are a **frontend/registry concern**: adding one means a new
  discriminator value and a renderer, with zero engine changes.

Per ADR-0012 the form providers available in the tasklist are on by default;
disabling one (e.g. the forms.io renderer) is an explicit opt-out.

### Consequences

- **Positive:** The engine stays form-agnostic — one string reference, no schema
  parsing. Users get a working native form engine out of the box and can still
  bring forms.io definitions unchanged. New providers never touch the engine.
  A single output contract (variables map) keeps `CompleteUserTask` uniform.
- **Negative / trade-offs accepted:** Two renderers to build and maintain from day
  one (native + forms.io), each with its own schema and quirks. Embedding the
  forms.io renderer brings a third-party frontend dependency and its **license**
  into the tasklist bundle — due diligence required before shipping. The native
  form engine is real scope (schema, validation, renderer, editor in phase 2).
- **Follow-ups / risks to watch:** Verify the forms.io renderer's license and
  bundling constraints before committing to embed it. Define the native form
  schema and its validation model. Define how a form maps to/from process
  variables (input mapping to prefill, output mapping on submit). Decide whether
  the native form editor lives in the design studio (ADR-0011 phase 2). Confirm
  the provider discriminator lives on the artifact, not on the engine event
  (the event only references the artifact).

## Pros and cons of the options

### Native only
- Good: one schema/renderer; full control.
- Bad: forces existing forms.io users to rebuild; no interop.

### forms.io only
- Good: instant interop; mature renderer.
- Bad: third-party dependency for a core experience; no batteries-included
  native option; ties Atlas to another project's roadmap and license.

### Pluggable providers
- Good: batteries-included native default *and* forms.io interop; engine stays
  form-agnostic; extensible without engine changes.
- Bad: two renderers to maintain; a registry/discriminator to design.

## Links

- forms are referenced by user tasks per ADR-0013 (engine stores the reference only)
- forms are stored in the design-time repository from ADR-0011; provider
  availability follows the opt-out principle of ADR-0012
- respects invariant 5 (no interpreting on the hot path — forms never enter the engine)
