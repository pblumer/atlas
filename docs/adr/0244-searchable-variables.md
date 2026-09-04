# ADR-0244: Searchable variables — a declared value index

- **Status:** Accepted
- **Date:** 2026-09-03
- **Deciders:** Atlas engine team

## Context and problem statement

[ADR-0241](0241-finding-an-instance.md) made a version's instances a bounded range
scan and an instance key a point read, and named the layer it deliberately left
out: the operator's *actual* question is a business value — "where is MT-1998?" —
and answering it still means reading every instance of the version and every one of
its variables.

That record sketched the answer (a declarative variable value index, exact and
prefix only) and left it as a follow-up. This record decides it, and states the two
places where building it moved the design.

## Decision drivers

- **The common case must be a seek.** A business key is how an operator arrives;
  the cost of finding it should be the number of matches, not the instance count.
- **Derived state is folded, not computed (I4/I6).** The index must be maintained in
  `applyToState` from the event alone, so replay rebuilds it identically.
- **A process that wants nothing pays nothing.** Indexing every value would double
  the variable write path and fill the index with JSON blobs.
- **Don't index what cannot be sought.** An ordered key-value store answers equality
  and prefix. Substring and full text are not accelerable by it.
- **Change no answer an operator already gets.** A query that works today must keep
  meaning what it means today.

## What building it changed

Two things ADR-0241 assumed turned out to be wrong, and both changed the shape:

**The fold cannot ask a compiled process anything.** `applyToState` receives the
record and the transaction — no processor, no deployment, no `CompiledProcess`. So
"is this name searchable" cannot be answered where the index is written, and the
index key cannot use the interned name id ADR-0241 imagined, because interning is
per definition and the fold does not know the definition either.

The answer is the discipline the record already uses for keys and timestamps (I6):
decide at command time, freeze into the event. `VariableValue` gains an `Indexed`
flag, stamped in `ProcessingContext.AppendVariableEvent` — the single place every
variable event goes through, and already the place the producer key is stamped "so
no path can forget it" (ADR-0219). Replay then indexes exactly what the live write
indexed, because it is reading the same flag.

**No backfill is needed, and that is not a compromise.** ADR-0241 worried that a
missing index "reads empty, which is believed". It does not arise here: the
`atlas:searchable` attribute did not exist before this change, so every definition
that *can* declare it is deployed after it, and all of that definition's instances
carry the flag from their first write. A definition deployed earlier declared
nothing and is missing nothing. The declaration is per version, which is exactly
the scope the index has.

## Decision outcome

**A process declares what it can be found by.** `atlas:searchable="identityId,item"`
on `<bpmn:process>`, resolved at deploy time into a set on the compiled process
(I5). A declaration that cannot mean anything — a nameless entry, the same name
twice — fails the deploy, for the same reason a malformed TTL does: it would
otherwise index nothing while looking like it worked.

**One column family, keyed by what it answers:**

```
varIdx:<len><name><value>\0<piKey> → nil
```

Valueless: the key is the whole fact. The name is length-prefixed so `id` and
`identityId` cannot share a range. The NUL terminator is what separates the two
questions an ordered index can answer — without it an exact search for `MT-19`
would also return `MT-1998` — and it is safe because a value containing NUL is
refused rather than indexed.

**What goes in:** a write at the instance's *root* scope (an activity-local scope is
scratch that disappears on completion, ADR-0068), of a declared name, whose value is
a scalar under 256 bytes. `VariableValue.IndexText` decides that in one place, and
the search calls the same function on the query, so a query and a write agree on the
bytes by construction rather than by two implementations happening to match.

**Maintenance rides on the writes that already happen.** `PutVariable` moves the
entry — a variable is a current value, not a log, so an overwrite that left the old
entry would let the index answer with a value the instance no longer holds. Finding
the old entry needs the old value, so there is a read, but only when one of the two
sides is indexed: a process that declares nothing never pays it. `DeleteVariable`
drops the entry; the ADR-0146 purge drops the instance's entries, read off the
variables that are about to go.

**`name=value` is answered from the index for a declared name.** This changes no
existing answer: a declaration is the only way into the index path, and no model
could carry one before now. An undeclared name keeps the walk it always had. The
index path requires `?process=`, because the declaration is per definition — the
same reason ADR-0241's paged listing does.

What a term *means* — matched whole, widened only by an explicit `*` or `?` — is
not decided here but in `draft-search-terms-are-literal`. That record came out of
this one: making the index path exact while the walk stayed a substring search
left the same query meaning two different things depending on whether the model
declared the name, which is not something an operator can see from the search box.

### Consequences

- **Positive:** the operator's real query costs the matches plus a seek. A version
  holding hundreds of thousands of instances answers `identityId=MT-1998` in the
  time it takes to read one row.
- **Positive:** a process that declares nothing pays one length check per variable
  write and holds no entries — the declaration is what makes the feature free for
  everyone who does not want it.
- **Negative / trade-offs accepted:** a declared name costs one extra read and one
  or two index writes per write of that variable. A structured or over-long value of
  a declared name is silently not indexed — the search still finds it by the walk if
  the name is undeclared, but under a declaration it will not be found by value.
  That is the honest cost of refusing to truncate a key.
- **Negative:** the index answers equality and prefix and nothing else. Substring
  and free text over a declared name are not accelerated, and over cold history they
  belong in the OpenSearch export (ADR-0114), not in another engine index.
- **Follow-ups / risks to watch:** a value that many instances share (a status, a
  tenant) makes a long range — the search cap bounds the answer, but such a name is
  a poor declaration and the Modeler could say so. Case sensitivity is the other
  edge: the index is byte-ordered, so a declared name matches the case the value was
  written in, while the walk is case-insensitive. That difference is visible only
  for declared names and is stated in the search hint; folding case into the key
  would need a second encoding and is not worth it until somebody asks.

## Pros and cons of the options

### Index every root-scope scalar (rejected)
- Good: no BPMN surface, no declaration, complete by construction, backfillable from
  state alone.
- Bad: doubles the variable write path for every process, indexes values nobody
  searches for, and grows an index whose size nobody chose.

### Declared names (chosen)
- Good: the cost lands only where the value was asked for. The set is resolved at
  deploy time, so the runtime asks a set lookup, not a parse.
- Bad: a new BPMN attribute, and a model that forgot to declare gets the walk — a
  silence the search hint has to explain.

### OpenSearch for value search (rejected as the primary answer)
- Good: already exported, and genuinely right for substring and full text.
- Bad: optional, lagging and external. "Where is MT-1998, right now" must be
  answerable from the engine's own state.

## Links

- follows [ADR-0241](0241-finding-an-instance.md), which named this layer as its
  follow-up and whose per-definition indexes this sits beside
- rides the command-time stamping of [ADR-0219](0219-variable-write-attribution.md)
  in the one place every variable event passes through
- relates to ADR-0068 (activity-local variable scopes), ADR-0146 (history purge),
  ADR-0114 (the OpenSearch export that owns full text)
