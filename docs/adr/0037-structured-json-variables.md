# ADR-0037: Structured JSON variables

- **Status:** Accepted
- **Date:** 2026-07-23
- **Deciders:** Patrick Blumer

## Context and problem statement

Atlas process variables have been scalar-only since Milestone 0: a `VarKind` of
null, boolean, number, or string, matching the FEEL scalar subset (ADR-0008).
When a FEEL expression produced a list or context, `Classify` lossy-serialized it
to its canonical FEEL text and stored it as a string — the variable survived, but
downstream expressions could not access its members (e.g. `customer.name` on a
stored `"{name: \"acme\", id: 7}"` string is a type error, not `"acme"`).

Users want to seed instances with structured data (a customer record, a list of
items, a configuration block) and have FEEL expressions read their members — the
normal expectation for a modern process engine. The Modeler's start-variable
declaration editor was limited to the three scalar types, with no way to author
or visualize JSON structures.

## Decision drivers

- Users expect `customer.name` and `items[2].price` to work on seeded data.
- The durable record format (ADR-0009) is length-prefixed text; adding a kind
  byte does not change the wire format, only the interpretation.
- Structured values must survive persistence and recovery identically — the
  recovery property (invariant I4) must hold for JSON variables.
- The FEEL library already has first-class `Context` and `List` value types;
  Atlas just needs to bridge JSON ↔ FEEL and persist the canonical JSON.
- The web Modeler is buildless (ADR-0012); any new editor must be self-contained
  JavaScript with no bundler or CDN dependency.

## Considered options

1. **Store as canonical JSON text under a new `VarJSON` kind** — re-parsed into
   FEEL context/list on binding, serialized back to canonical JSON on storage.
   No schema change, just a new `VarKind` byte.
2. **Store as FEEL text** (the existing lossy fallback) — leave `Classify` as-is,
   parse the FEEL text back on binding. Would require a FEEL parser in Go, which
   is heavier than JSON parsing, and the canonical FEEL text includes types that
   JSON does not (dates, ranges, functions).
3. **Decompose into scalar variables** — flatten `{a: {b: 1}}` into variable
   `a.b` = 1. Defeats the purpose; `items[2]` is not a static path.

## Decision outcome

Chosen option: **Store as canonical JSON text (option 1)**, because it is the
simplest change that round-trips correctly: the new `VarJSON` kind byte
(value 4) tells `FromStored` to `json.Unmarshal` the text field into a FEEL
value tree, and `Classify` encodes lists/contexts to canonical JSON (sorted keys,
exact-decimal numbers via `json.Number`) so the stored text is deterministic
across replays.

### Consequences

- **Positive:** `customer.name`, `items[2].price`, `for x in items return x * 2`
  — all FEEL member/index/iteration over seeded or computed structured data works
  end-to-end with no extra infrastructure.
- **Positive:** The durable format is unchanged — a `VarJSON` record is a kind
  byte followed by length-prefixed UTF-8 JSON text, identical in shape to a
  string variable. A pre-0037 engine seeing kind byte 4 treats it as `VarNull`
  (the default arm), which is safe degradation.
- **Positive:** The Modeler gains a professional inline JSON editor with syntax
  highlighting, auto-indent, bracket matching, and live validation — consistent
  with the existing FEEL editor's technique and quality bar.
- **Negative / trade-offs accepted:** JSON cannot represent FEEL temporals,
  ranges, or functions. If a script task produces one of those inside a context,
  it degrades to its canonical FEEL string on the JSON round-trip. This is the
  same lossy behavior the scalar path already had; it is acceptable until Atlas
  models non-scalar non-JSON values natively.
- **Follow-ups / risks to watch:** Deeply nested structures could grow large;
  variable-size limits are not enforced yet (tracked under Milestone 2).

## Detailed design

### Model layer

`model.VarKind` gains `VarJSON = 4`. The existing `encode`/`decode` are
unchanged — `VarJSON` uses the same `Text` field as `VarString`, differentiated
only by the `Kind` byte.

### Expr layer

`expr.ValueKind` gains `KindJSON`. New functions:

- `ToJSON(Value) (string, bool)` — canonical JSON from a FEEL list/context.
  Object keys are sorted (`encoding/json` does this for `map[string]any`);
  numbers keep their exact decimal via `json.Number`.
- `ParseJSON(text) (Value, error)` — inverse; `json.Decoder` with `UseNumber`.
- `FromJSON(any) Value` — converts a decoded JSON tree (as from `json.Unmarshal`
  with `UseNumber`) into a FEEL value tree.
- `Classify` dispatches `KindList`/`KindContext` to `ToJSON` instead of the
  lossy `v.String()`.
- `FromStored(KindJSON, …)` calls `ParseJSON`.

### Engine layer

`toVarKind`/`toExprKind` gain the `VarJSON ↔ KindJSON` arms. `bindInputs`
calls `FromStored`, which now handles `KindJSON` — so a stored JSON variable
becomes a FEEL context/list and downstream expressions access its members.

### API layer

`parseStartVariables` accepts `map[string]any` and `[]any` JSON values,
round-tripping them through `expr.FromJSON`/`expr.ToJSON` to produce canonical
text. `feelBindings` does the same for the FEEL evaluator endpoint.
`toVariableView` and `feelKindName` render `"json"` as the kind label.

### Clio connector

`varToAny` re-parses `VarJSON` text back to a nested `any` tree (via
`json.Decoder` with `UseNumber`) so the connector payload nests the structure as
a real JSON object/array rather than a JSON-in-a-string blob.

### Web UI

- `json-editor.js` — a buildless, self-contained JSON editing surface following
  the same overlay technique as `feel.js`: transparent textarea over a
  highlighted `<pre>`, with tokenization, auto-indent, auto-close brackets, a
  Format button, and live JSON validation.
- `START_VAR_TYPES` gains `"json"`, and the declaration editor renders a JSON
  editor textarea for json-typed variables.
- The Deploy & Start forms render a JSON editor for json-typed declared
  variables, and the free-form textarea is also upgraded.
- `parseStartVariables` (client) no longer rejects objects/arrays.

## Links

- relates to [ADR-0008](0008-feel-expression-strategy.md) — FEEL expression compilation
- relates to [ADR-0009](0009-record-serialization-format.md) — record serialization format
- relates to [ADR-0012](0012-web-ui-app-shell.md) — buildless web UI
