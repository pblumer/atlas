# ADR-DRAFT: A search term is literal, and widening is asked for

- **Status:** Proposed
- **Date:** 2026-09-04
- **Deciders:** Atlas engine team

## Context and problem statement

The instance search widened every term into a substring match. `kdnr=MT-100`
returned MT-100 **and** MT-10001, and the operator who typed it got back a list
holding a row they did not ask for, sitting next to the one they did, with
nothing on either row to tell them apart. That is not a helpful widening: MT-100
and MT-10001 are two different customers, and the search offered no way to ask
about only one of them.

It was also not consistent with itself. ADR-0244 gave a
declared name an exact index lookup, so `kdnr=MT-100` matched exactly when the
model carried `atlas:searchable="kdnr"` and matched as a substring when it did
not. Whether a name is declared is a property of the model. It is not visible
from the search box, it can change with a redeployment, and it had come to decide
what a query *means*.

The same predicate also filters **bulk termination** (`?q=` on the terminate-many
path). A widening the operator did not ask for selects instances they did not
mean to name, and there the cost is not a confusing list.

## Decision drivers

- **A query returns what it asked for.** An operator must be able to name one
  value and get one value. A search that cannot express "exactly this" cannot
  answer the question an instance search exists for.
- **One rule, visible from the search box.** Nothing about a model may change
  what a query means.
- **Widening must stay available.** "Find anything containing MT-1" is a real
  question; it just has to be asked.
- **No new vocabulary.** Operators already know `*` and `?` from shells and file
  pickers.
- **Cheap under an ordered index.** The seek that ADR-0244
  bought must survive: an exact term stays one seek, and a wild one must still
  reach its neighbourhood rather than scanning.

## Considered options

1. **Keep substring matching, add an "exact" toggle (rejected).** A checkbox
   beside the box, or a `==` operator. It leaves the surprising behaviour as the
   default, so the operator meets it first and learns the toggle only after being
   misled once. It also leaves the declared/undeclared inconsistency untouched.
2. **Full regular expressions (rejected).** Expressive, but a regex is a language
   an operator has to know, it is easy to write one that is expensive to evaluate,
   and it cannot be pushed into an ordered index at all — the literal head of a
   glob is what makes the seek possible.
3. **Literal by default, `*` and `?` to widen (chosen).** The term is compared
   whole. `*` stands for any run of characters, `?` for exactly one, and a
   backslash escapes either so a value that genuinely contains a star is still
   reachable. One rule for declared and undeclared names, for structured and
   free-text queries, and for the terminate filter.

## Decision

**A search term is matched whole, and `*` and `?` are how an operator asks for
more.** This holds everywhere the predicate is used: `name=value`, free text over
names and values, the value index, the instance walk, the archive lookup, and the
bulk-terminate filter.

The matcher is a two-pointer glob walk that backtracks to the last `*` rather
than branching at it, so a pattern of many stars costs O(pattern × value) and not
an exponential search — the box is reachable by anyone who may look at instances.

Under the value index a pattern is split into its **literal head** and the rest.
No wildcard means the head is the whole term and the lookup is the exact seek
ADR-0244 built. A wildcard means the head is a seek to a
neighbourhood and everything it returns is matched against the full pattern
before it is reported — without that, `MT-1?` would answer with every MT-1 value
the index holds. A pattern with no head at all (`*0001`) reads that name's whole
range, which is still one name's range rather than every instance.

The archive lookup (ADR-0114) passes the pattern through as written: OpenSearch
spells `*`, `?` and backslash-escaping the same way, so a `wildcard` query and
this matcher agree without a translation layer that could disagree at an edge.

## Consequences

- **Positive:** a query means one thing, and the same thing regardless of the
  model. An operator can name exactly one instance's identifier. Bulk termination
  can no longer select more than was named through an implicit widening.
- **Positive:** the exact path is the fast path — a literal term is a single seek
  under a declared name, with no post-filtering at all.
- **Negative — this is a behaviour change.** A free-text term that used to find a
  value it occurred in now finds only a value it equals; `retail` must be written
  `*retail*`. Anyone who had learned the old behaviour has to learn one rule
  instead of two. The search hint, the handbook, the OpenAPI summary and the MCP
  tool description all state the rule, because a silent change of what a query
  means would be worse than the behaviour it replaces.
- **Neutral:** no stored data changes. The index keys are untouched; only what is
  compared against them changed.
