# ADR-0405: A decision is shown, not described

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-19
- **Deciders:** Core team
- **Open question:** whether a decision service should record a rule trace at all, which is not ours to settle — see _Follow-ups_.
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0066](0066-decision-evaluation-records.md) made a decision debuggable: every
evaluation a business rule task makes is kept as durable history carrying the input
context it saw, the outputs it produced, and the temis trace of which rules fired.
The record has been right since. What was built on top of it was not.

The evaluation reached an operator as a card — inputs as pills, the result as a row —
with the rule matrix behind a **hover**. Three things follow from that, and all three
were reported together from a live installation:

- **The rules were not reachable.** A hover is not an affordance: nobody can see it,
  it cannot be reached on a touch device, and what it reveals cannot be scrolled,
  selected or pointed at while somebody else is looking at the screen. The one thing
  the record exists to answer was the one thing behind a gesture.
- **The result did not read as a result.** The card set the output name and its value
  in a grid whose value column was right-aligned, so in the replay panel — full width,
  unlike the table the rule was written for — the answer sat against the far edge, a
  hand's width from the name it answered to.
- **The decision was nowhere.** A DMN decision is a *graph*: input data feeding
  decisions feeding decisions. That graph is the picture the model was authored in and
  the only picture in which "why was this rejected" has a shape. The operator surfaces
  showed a flat list of key/value pairs and never drew it.

Underneath this is a question about who the reader is. Debugging a decision is one
job; **accounting** for one is another, and it is the one the business asks for: an
underwriter has to be able to say why this application was refused, to somebody who
has never seen Atlas and will not be taught it. That reader needs the decision itself
on screen, with the case on it.

## Decision drivers

- The record is evidence. Nothing shown may be inferred, reconstructed or re-run:
  what is drawn has to come from the frozen record, or not be drawn.
- The picture must be of the model that *ran*, not the model as it reads today.
- No work on the run loop, and no compilation at read time (invariants I1/I5).
- One renderer. A decision should be the same picture wherever it is looked at, the
  way [`dmn-trace.js`](../../api/web/dmn-trace.js) already made a trace one matrix
  everywhere.
- Say the true thing about a silence. "No rules were recorded" and "no rules matched"
  are different accounts of a case, and telling a reader the wrong one is worse than
  telling them nothing.

## Considered options

1. **Make the hover better** — a click-to-pin popover, a wider table, a fixed result
   column.
2. **A page per evaluation** — a route showing one evaluation in full, linked from
   every surface that lists decisions.
3. **The decision graph, opened from the task** — double-clicking the business rule
   task opens a modal holding the decision's requirements graph with the case drawn on
   it, the rule matrices, and the result.

## Decision outcome

Chosen option: **option 3 — the decision graph, opened from the task**, with the
result-row layout fixed underneath it for every surface that shares it.

- **The gesture is the one the diagram already teaches.** A call activity answers a
  double-click with the process behind it ([ADR-0076](0076-call-activities.md),
  [ADR-0245](0245-call-activity-drilldown.md)). A business rule task is the only
  other element whose contents are a model of their own, so it answers the same
  gesture the same way. The Decisions tab's cards carry the same door for a reader
  scrolling the list rather than looking at the diagram.
- **One read serves the window.** `GET /api/v1/instances/{key}/decisions/{at}/graph`
  returns the evaluation *and* the requirements graph of the model it ran against.
  The graph is resolved from the process definition the record carries, so a decision
  re-modelled since does not redraw an old case; a definition that is gone falls back
  to the newest model providing the decision, which is what a latest-bound task would
  evaluate.
- **The graph is frozen at deploy time.** `Registry.Graph` is a map lookup, because
  the picture is built once at registration where the document is in hand — the same
  place the decision's names and its services are worked out. Nothing is compiled at
  read time, and the read that wants it goes through the run loop (I1/I5). A model
  carrying no DMNDI is completed on the way in by the generator the read-only viewer
  already uses ([ADR-0325](0325-dmn-diagram-is-completed-on-read.md)), so the client
  never invents a layout.
- **A value is tied to a node by name, or not at all.** Three sources, in order of
  authority: the recorded input context (*given*), the value the trace says a table's
  input column evaluated to (*derived*), and the recorded outputs (*result*). A column
  whose expression is not a bare name ties to nothing and is not looked up. A node
  nothing can speak for is drawn back and says "not part of this case" rather than
  being left blank or guessed at.
- **Colour says where a value came from, not what kind of node holds it** — the shape
  already says that. So a *decision* the caller supplied is drawn like an input datum,
  because that is what it was. This is the only place a decision service's boundary is
  visible at all.
- **An evaluation is addressed by a string.** A nanosecond timestamp is past 2^53, so
  a browser cannot parse one without rounding it — `…033700` arrives as `…033800`, and
  every link would have addressed an evaluation that does not exist. The listing
  serves `atKey` beside `at`: the number is for reading and arithmetic, the string is
  the identity.

### Consequences

- **Positive:** the question the record was built to answer is now answered on one
  screen, in the picture the decision was authored in, with the rule that carried the
  result marked and the ones that did not showing which condition ruled them out. It
  is shareable in the sense that matters — somebody can be shown it. The result-row
  fix reaches the Operations decision table and the live view's panel too, since all
  three share the markup. `renderDrgSvg` moved into the new module and the Modeler's
  read-only DMN view imports it, so there is one renderer rather than two drifting.
- **Negative / trade-offs accepted:** the registry holds a `ModelGraph` per registered
  model, and a model with no diagram costs one extra compile at registration to
  generate one. Both are deploy-time, and neither is on any hot path. The window
  duplicates no state — everything it shows comes from the single read — which means
  it does not follow a live instance while it is open.
- **Follow-ups / risks to watch:** **a decision service still records no rule trace**
  ([ADR-0398](0398-a-business-rule-task-can-call-a-decision-service.md)), because
  temis's `CompiledService.Evaluate` takes no `EvalOption` and the pinned version is
  already the newest published. The window says so in as many words and keeps drawing
  the values, which are exact. Fabricating the trace by re-evaluating the output
  decision was considered and **rejected**: ADR-0066 rejected re-evaluation as the
  mechanism for exactly this reason, temis states a `Trace` is "derived from the actual
  evaluation, never reconstructed after the fact", and a model reading `now()` could
  make a replay name a different rule that produces the same output — undetectable by
  any output-equality guard, and a silently wrong rule is the worst failure this
  window has. The fix is upstream and small: `CompiledService.Evaluate` taking
  `opts ...EvalOption` and setting the evaluator's recorder the way
  `CompiledDecision.Evaluate` already does.

## Pros and cons of the options

### Make the hover better
- Good: smallest change; touches one file.
- Bad: keeps the affordance invisible and the decision's shape off screen. It makes
  debugging slightly nicer and does nothing at all for accounting, which is the job
  that was actually reported. Rejected.

### A page per evaluation
- Good: a real URL; sharable by link; room for anything.
- Bad: a navigation away from the instance being read, and back again for the next
  decision — when the question is nearly always asked *about the diagram on screen*.
  The window this ADR chooses can still grow a route later; starting with one would
  have put a page between the reader and the gesture. Rejected for now.

### The decision graph, opened from the task (chosen)
- Good: the gesture is already taught by the diagram; the picture is the one the model
  was authored in; the case, the rules and the answer are on one screen.
- Bad: a modal is not linkable, and it holds a snapshot rather than following a live
  instance.

## Links

- shows the record of [ADR-0066](0066-decision-evaluation-records.md), and answers its
  follow-up about surfacing an evaluation in the replay
- inherits the generated diagram of [ADR-0325](0325-dmn-diagram-is-completed-on-read.md)
- names the trace gap of [ADR-0398](0398-a-business-rule-task-can-call-a-decision-service.md)
  without working around it
- draws the matrix of [ADR-0326](0326-trying-a-decision-before-it-runs.md)'s shared
  renderer, and borrows the drill-in gesture of [ADR-0076](0076-call-activities.md)
- uses the two names of a decision from [ADR-0385](0385-a-decision-is-addressed-by-both-of-its-names.md) to
  tie a value to a node
- `api/web/decision-graph.js`, `e2e/decision-graph.spec.mjs`, `api/decisiongraph_http_test.go`
