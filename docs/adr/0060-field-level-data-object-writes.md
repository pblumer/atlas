# ADR-0060: Field-level data object writes — set one member of a structured object

- **Status:** Accepted
- **Date:** 2026-07-25
- **Deciders:** Patrick Blumer

## Context and problem statement

A data object can hold a **structured** value — a JSON object or list (ADR-0037,
`VarJSON`) — so `order` can be `{"name": …, "amount": …, "status": …}`. ADR-0058's
data output association writes a data object, but it writes the **whole** value: the
association's FEEL `<from>` expression produces a value and it *replaces* the object.

Authors want to write **one field** of a structured object — "set `order.name`",
leaving the rest of `order` intact — without reconstructing the whole object in
FEEL every time. This is the natural way to build up a record as a token moves
through a process: one step sets the name, another the amount, another the status.
This ADR decides how an output association targets a single member.

## Decision drivers

- **Incremental structured writes.** Setting `order.name` should keep `order`'s
  other members, so a record accrues across steps rather than being rebuilt wholesale.
- **BPMN-native.** A `<dataOutputAssociation>`'s `<assignment>` already has a `<to>`
  target expression; use it to name the member path, so the model stays standard.
- **Reuse the structured-value machinery.** The merge should produce the same
  canonical JSON (`VarJSON`) ADR-0037 defined, so members round-trip and replay
  deterministically.
- **Recovery (I4/I6).** The *resulting* merged value is frozen into the
  `DataObjectStateChanged` event; replay re-applies it without re-reading or
  re-merging.

## Considered options

1. **Target-path merge.** The output association's `<assignment><to>` names a member
   path (`name`, or a dotted `customer.name`); at write time the engine reads the
   object's current JSON, sets that path to the FEEL `<from>` result, and writes the
   merged canonical JSON back. An empty `<to>` writes the whole value (ADR-0058's
   behavior, unchanged).
2. **Author the merge in FEEL.** Require the `<from>` to reconstruct the whole
   object, e.g. `=put(order, "name", customerName)`. Works with no engine change,
   but the object is the *target*, not an input, so it is not in scope for `<from>`;
   and it pushes context-function boilerplate onto every field write.
3. **Model each field as its own data object.** `orderName`, `orderAmount`, …
   Defeats the point — the author wanted one structured `order`, and downstream
   readers want it whole.

## Decision outcome

Chosen option: **option 1 — target-path merge.** It is BPMN-native (the `<to>` is
where a member target belongs), keeps the rest of the object, reuses the canonical
JSON machinery, and needs no FEEL gymnastics. An empty `<to>` is exactly ADR-0058's
whole-object write, so existing associations are unaffected.

### Design

**Authoring (BPMN).** The association carries the member path in `<assignment><to>`:

```xml
<dataOutputAssociation>
  <targetRef>DataObject_order</targetRef>
  <assignment><from>=customerName</from><to>name</to></assignment>
</dataOutputAssociation>
```

When the activity completes, Atlas evaluates `=customerName`, reads `order`'s
current value, sets its `name` member to the result, and writes the merged object
back — `order` becomes `{"name": "Acme", …the rest…}`.

**Compiler.** The output association gains an interned `TargetPath` (the `<to>`
member path, `-1` when absent). Everything else is unchanged.

**Engine.** In `applyDataOutputAssociations`, when a `TargetPath` is set, the write
is a read-modify-write:

1. Read the data object's current value; if it is `VarJSON`, decode it (numbers as
   `json.Number` so decimals stay exact); otherwise start from an empty object.
2. Set the dotted path to the FEEL `<from>` result encoded as canonical JSON
   (`expr.ToJSON`), creating intermediate objects as needed.
3. Marshal the whole object canonically (sorted keys) and write it as the object's
   new `VarJSON` value.

The merged value rides in the `DataObjectStateChanged` event, so replay re-applies
it without re-reading the object or re-merging (I4/I6). Writing a field into an
absent or non-object value creates the object (`{path: value}`), so the very first
`order.name` write is enough to *store the structure* — no separate initialization.

### Consequences

- **Positive:** Structured records accrue field by field across a process — the
  natural way data grows as a token flows. Reuses the ADR-0037 canonical-JSON
  format; recovery-correct; empty `<to>` keeps ADR-0058's whole-object write, so no
  migration. Writing a member into an unset object creates it, so "store an object
  and write only its name" is a single field write.
- **Negative / trade-offs accepted:** The write is a read-modify-write against the
  object's value *at completion time* — two associations on the same activity that
  target the same object see each other's writes in order (they run in the loop
  sequence), which is the intended accrual, but there is no cross-token merge (a
  data object has a single current value per instance scope, as everywhere in the
  engine today). A dotted path addresses object members only; list-index targets
  (`items[2]`) are a follow-up. A path write onto a value that is a scalar/list
  replaces it with an object — the pragmatic choice, flagged rather than errored.
- **Follow-ups / risks to watch:** List-index and mixed path segments; a declared
  item definition (schema) so a field write can be validated against the object's
  shape (the ADR-0053 itemDefinition follow-up); removing a member (writing null vs.
  deleting).

## Pros and cons of the options

### Option 1 — target-path merge (chosen)
- Good: BPMN-native `<to>`; keeps the rest of the object; reuses canonical JSON;
  recovery-correct; empty `<to>` unchanged.
- Bad: read-modify-write per field; object-member paths only for now.

### Option 2 — author the merge in FEEL
- Good: no engine change.
- Bad: the target object isn't in the `<from>` scope; context-function boilerplate
  on every field write; error-prone.

### Option 3 — one data object per field
- Good: trivial.
- Bad: defeats the structured-object goal; downstream readers want `order` whole.

## Links

- extends ADR-0058 (data output associations — this adds a member target to the
  write; an empty target is ADR-0058 unchanged)
- builds on ADR-0037 (structured JSON variables — the canonical-JSON format the
  merge reads and writes) and ADR-0053 (first-class data objects)
- relates to the ADR-0053 itemDefinition follow-up (a schema to validate field
  writes against)
