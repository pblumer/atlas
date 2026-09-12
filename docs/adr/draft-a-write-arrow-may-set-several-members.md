# ADR-DRAFT: A write arrow may set several members at once

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-12
- **Deciders:** Patrick Blumer

## Context and problem statement

[ADR-0060](0060-field-level-data-object-writes.md) let a data output association write
one *member* of a structured data object instead of replacing it: the association's
`<assignment><to>` names the member, `<from>` produces its value. That is the right
shape for a record a process builds up step by step.

It is the wrong shape for a record one step builds up. A user task that captures a
person's name, first name, unit, function and start date writes five members at one
moment, from one activity, into one object. ADR-0060 gives one member per association
and an association is drawn as an arrow, so five members are five arrows from one task
to one box. Ten fields are ten arrows. The diagram stops being a picture of the process
and becomes a cable harness, and the author reasonably refuses to draw it.

The way out of that today is the whole-object write: one arrow, `<from>` a FEEL context
literal, no `<to>`. It draws well and it costs the model everything. What is inside a
FEEL expression cannot be read at deploy time, so the members are invisible to the
information model: the write is not checked against the class, the derived model shows
the class as memberless, and the difference reading has to exclude it by name
([ADR-0301 §2](0301-derive-the-model-from-the-processes.md),
[ADR-0310 §3](0310-read-the-difference-between-what-is-built-and-what-is-planned.md)).
The author picks between a readable diagram and a readable model.

The choice is a false one, and Atlas made it up. BPMN's `DataAssociation` carries
`assignment` as `[0..*]` — several assignments on one association is what the standard
says, not an extension of it. Atlas's parser read the element into a single field, so a
second `<assignment>` was silently dropped and the last one won. A model another tool
wrote, or a person wrote by hand, deployed and quietly did something other than what it
said.

## Decision drivers

- **The diagram stays a diagram.** One activity writing one object is one arrow, whatever
  the number of fields it sets.
- **The members stay readable.** Every field a write sets must be a static fact of the
  model, so the class check, the derivation and the difference all see it.
- **Standard, not a dialect.** BPMN already allows this; nothing new is invented.
- **A silent drop is worse than a refusal.** Whatever is decided, a second `<assignment>`
  must stop being discarded without a word.
- **One write is one fact** (invariant I6). What an activity did to an object is one
  thing that happened, not one thing per field it touched.

## Considered options

1. **Read every `<assignment>`, apply them in order, emit one event.** The association
   compiles to a *list* of writes. At completion the engine starts from the object's
   current value, applies each write in document order, and appends a single
   `DataObjectStateChanged` carrying the result.
2. **Read every `<assignment>` and fan out to one compiled association each.** The
   parser turns one arrow with five assignments into five compiled associations. No
   engine change at all — the existing loop already merges member writes correctly.
3. **Leave one assignment per arrow and refuse the rest at deploy.** Keeps ADR-0060
   exactly as it is and ends the silent drop, but answers the author's problem with
   "draw five arrows", which is the problem.
4. **A new Atlas extension element** holding a list of field writes. Solves it, and
   invents a dialect for something the standard already expresses.

## Decision outcome

Chosen option: **option 1 — a list of writes per association, applied in order, one
event.**

Option 2 is tempting because it costs nothing: the fan-out is four lines in the parser
and the engine is untouched. It is wrong at the level that matters. Each compiled
association appends its own event, so one task completion writing five fields would
append five `DataObjectStateChanged` facts for one object. The instance timeline and the
variable audit would show five writes where a person performed one, and four of them
would expose a half-built object that never existed as a state of the business: an
`identity` with a surname and no first name is not something the process ever meant.
Invariant I6 says events are facts; these would be artefacts of how the write was
compiled.

Option 3 answers a question nobody asked. Option 4 writes a dialect for a sentence BPMN
already has.

### Design

**Authoring (BPMN).** Several `<assignment>` elements on one association, which is the
standard's own shape:

```xml
<dataOutputAssociation>
  <targetRef>Ref_identity</targetRef>
  <assignment><from>=surname</from><to>surname</to></assignment>
  <assignment><from>=firstName</from><to>firstName</to></assignment>
  <assignment><from>=unit</from><to>orgUnit</to></assignment>
</dataOutputAssociation>
```

**Compiler.** `DataOutputAssociation` keeps its `DataObject` and `TargetState` and
exchanges its single `Value`/`TargetPath` pair for `Writes []DataObjectWrite`, each
write being one `<assignment>`: a compiled `Value` and an interned `TargetPath` (`-1`
for the whole value). One assignment compiles to one write, so every model that exists
today means exactly what it meant; no assignments at all is an empty list, which is
ADR-0058's state-only transition unchanged.

**Engine.** `applyDataOutputAssociations` walks the writes of one association in
document order over a single accumulating value that starts as the object's current
one. A write with a path merges into that value; a write without one replaces it. The
event is appended once, after the last write, carrying the result and the association's
target state. With one write this is byte-for-byte what ADR-0060 already did.

Order is the document's, and it is load-bearing: two writes to the same path mean the
later one, and a whole-object write followed by member writes means those members land
on the new value. Both fall out of applying them in order and neither is a special case.

**Read associations are untouched.** A `<dataInputAssociation>` reads one object into
one variable, so a second `<assignment>` on one has nothing to mean — BPMN's `[0..*]` is
structural there, not semantic. Its parser still reads a single element, and so a second
assignment on a *read* is still dropped without a word. That is a smaller hole than the
one this record closes (nothing writes such a model: the Modeler emits one, and there is
no authoring intent to serve), and closing it properly means deciding what a second read
assignment would *do*, which is a question rather than an omission. Named here so it is
not mistaken for handled.

**Information model.** Every path is a static fact again, so all three readings that
ADR-0060 enabled work on an arrow that sets ten fields exactly as on one that sets one:
the write is checked against the class that declares the member
([ADR-0230](0230-process-information-model.md)), the derived model lists all ten
([ADR-0301](0301-derive-the-model-from-the-processes.md)), and the difference compares
them rather than excluding the class
([ADR-0310](0310-read-the-difference-between-what-is-built-and-what-is-planned.md)).
This is the point of the record as much as the diagram is: it gives an author a way to
write a whole record in one step *without* going opaque.

**Modeler.** The write arrow's panel becomes a list of write rows with an add and a
delete, the same collapsible group the generic io-mapping editor already uses
([ADR-0068](0068-task-io-variable-mappings.md)). Each row keeps what one arrow had:
the member picker offered by the target's class, its free-text escape, and the FEEL
value. An arrow with one write looks and edits as it always did.

### Consequences

- **Positive:** the diagram and the information model stop competing. A step that
  captures a form's worth of fields is one arrow whose members are all readable, which
  is what the whole-object write bought at the price of going blind.
- **Positive:** the silent drop is gone. A second `<assignment>` from any tool is now
  honoured rather than discarded.
- **Negative:** a write arrow is no longer a single glance. Five fields on one arrow are
  five rows a reader has to open the panel to see, where five arrows were visible on the
  canvas. The group's count badge is the concession: the arrow says how many writes it
  carries without being opened.
- **Negative:** `DataOutputAssociation` changed shape, and it is a public compiler type.
  Pre-1.0, and the four call sites that read it are all in this repository.
- **Follow-ups:** the whole-object write remains, correctly — a value computed at run
  time is a real thing to want. It stays excluded from the difference, and this record
  gives the author who does not need it a way not to use it.
