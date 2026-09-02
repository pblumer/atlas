# ADR-0215: The Modeler Playground — batch simulation and analysis of a draft

- **Status:** Proposed
- **Date:** 2026-08-31
- **Deciders:** Atlas maintainers

## Context and problem statement

The Modeler has two top-level tabs today: **Design** (draw the flow) and
**Implement** (wire the technical detail). Between "the diagram looks right" and
"the process behaves right" there is nothing. An author who wants to know what a
model actually *does* has three unsatisfying options:

- The **token simulation** in the Design view ([ADR-0078](0078-design-view-token-simulation.md),
  [ADR-0096](0096-token-simulation-events-and-inclusive-gateways.md)) — a
  browser-side teaching aid. It moves tokens along flows; it deliberately does not
  evaluate FEEL, conditions, DMN or data. It answers "where can a token go", never
  "what happens with *this* data".
- **Deploy & run** — real, durable, versioned instances in the engine's log. It
  answers everything, at the cost of polluting the runtime with throwaway data and
  firing real side effects, and it is one instance at a time.
- Nothing at all for the question authors actually ask before a go-live: *"here are
  200 real cases — what does the process do with them, which paths does it take,
  where does it pile up, and how long does it take?"*

[ADR-0030](0030-play-mode-simulation.md) already decided the *shape* of the answer
for the single-case question — an ephemeral engine sandbox rather than a second
control-flow implementation — but it was scoped to stepping one instance by hand,
and it has not been built. The question this record answers is the broader one:

**How does the Modeler let an author run a whole dataset through a draft, on a
timeline they control, and get an analysis back — without deploying, without side
effects, and without a second engine to keep in sync with the real one?**

## Decision drivers

- **The tested model is the shipped model.** Making a model testable must not mean
  editing it. If an author has to swap a REST task for a mockup task
  ([ADR-0120](0120-mockup-service-task.md)) to try the process, what they tested is
  not what they deploy, and the swap-back is a manual step that will be forgotten.
- **Same semantics as production, by construction.** A simulator that disagrees with
  the engine teaches authors a lie. No second control-flow implementation
  (invariant I5's spirit; the reason [ADR-0030](0030-play-mode-simulation.md)
  rejected a JS engine).
- **Provably side-effect-free.** A playground run must not be able to send mail,
  call a REST endpoint, write to clio, or create a Jira issue — not "is configured
  not to", but *cannot*.
- **Nothing durable.** No version minted, no instance in Operations, no row in the
  durable log, nothing to clean up afterwards.
- **A day in a second.** "Spread these 200 cases over a working day" must not take a
  working day. Simulated time has to be decoupled from wall-clock time.
- **Reproducible.** The same dataset, config and seed must produce the same report,
  or the feature cannot be used as a regression check and its numbers cannot be
  cited in a review.
- **Scale.** 50 000 cases in one run is a stated requirement, not a stretch goal.
  That is what rules out holding the run in a request, the report in memory, or the
  case list in the browser.
- **Steppable, not only batchable.** An author must be able to stop mid-run, look at
  a case, fill in a user task themselves and carry on. Without that the "play" in
  playground is missing, and the only way to inspect a single case is to re-run it.
- **Bounded.** An abandoned or oversized run must not eat the server.

## Considered options

1. **Extend the browser token simulation** to batch mode and collect statistics
   client-side.
2. **Deploy to a throwaway namespace** on the real engine, run the dataset there,
   analyse the resulting runtime data, then delete it.
3. **An ephemeral engine sandbox per run** — the real compiler and the real
   processor over a non-durable partition with a **virtual clock**, driven by an
   arrival scheduler, with every external leaf answered by a sandbox-local stub
   policy, reporting from the sandbox's own event log.
4. **A dedicated simulation engine**: derive a queueing / discrete-event model from
   the BPMN and simulate that.

## Decision outcome

Chosen option: **Option 3 — an ephemeral engine sandbox per playground run**,
extending [ADR-0030](0030-play-mode-simulation.md) from "step one instance" to
"run a dataset on a simulated timeline and report on it".

A Playground run is a **Sandbox**: its own partition, its own single-writer
goroutine (invariant I3), its own WAL in a temp directory and its own state store,
none of them reachable from the durable engine, all discarded when the run ends. It
compiles the *current draft* with the real compiler (reusing the dry-run compile of
[ADR-0026](0026-problems-panel-and-versioned-validation.md), so a model that will
not deploy also will not run) and executes it on the real processor. Control flow,
FEEL conditions, gateways, multi-instance, boundary events, DMN decisions, data
objects: all of them are the production code path, because there is no other one.

Four pieces make it a *playground* rather than a second engine:

**A virtual clock.** The sandbox's `engine.Clock` is owned by the run's scheduler,
not by the wall. Time advances in two ways: the scheduler releases the next arrival,
or — when nothing is runnable — it jumps to the next due timer. A process with a
three-day escalation timer therefore finishes in milliseconds, and "arrivals spread
over a working day" is a property of the plan, not of how long the author waits.
The engine already takes a `Clock` (`engine.New(..., clock)`) and already freezes
timestamps into events (invariant I6), so this needs no engine change — the same
trick the conformance driver uses (`conformance/runner.go`).

**An arrival plan.** The dataset (one row = one case) is turned into a list of
`(virtual time, start variables)` pairs by a *timing profile*: all at once,
sequentially (next starts when the previous finishes), a fixed rate, a Poisson
arrival stream, or a day profile with a load curve and business hours. The plan is
computed up front from the seed, so it is part of the reproducible input.

**A stub and resource policy that lives in the run, not in the model.** Every leaf
that would leave the process — a job task, a connector task, a user task, a send
task, an inbound message — is answered by the sandbox from a per-element policy: a
duration distribution, an optional FEEL result expression, an optional failure
probability with an incident or a business error code. This is deliberately the same
semantic vocabulary as the mockup service task
([ADR-0120](0120-mockup-service-task.md)) — but supplied as **run configuration**
against the untouched draft, so the model that was tested is byte-for-byte the model
that deploys. Defaults are derived (a connector task's kind suggests a latency band;
a user task defaults to a work duration; an element's example data is the default
result), so a run is possible before a single stub is configured by hand.

The policy also carries **resource pools**: a named pool with a capacity and a
calendar, and the elements that draw on it. A job the sandbox picks up does not
start being served immediately — it queues for a free slot in its pool, and it is
only served while that pool's calendar is open. So a task's elapsed time splits into
*queue time* and *work time*, and "do three clerks suffice for 200 applications a
day" becomes a question the report answers rather than a number the author had to
guess and configure. This needs no engine change: it is the sandbox's own job runner
deciding *when* to complete a job it already holds, in virtual time.

A pool's **utilisation is measured against the calendar, not the wall clock**: the
denominator is capacity times the working time the calendar offered over the run,
so the nights and the weekend a simulated fortnight spans are not counted as
capacity the pool declined to use. Dividing by the run's span instead reports a
pool with three hundred cases queued as a quarter busy — two numbers in the same
report that cannot both be acted on.

**Isolation by absence.** The sandbox registers **no connector factories, no vault,
no mail transport, no HTTP client at all**. A REST task in the sandbox cannot reach
the network because there is nothing in the sandbox that can. Side-effect freedom is
a structural property here, not a configuration flag. Its partitions come from a
reserved range at the top of the partition space (`0xF000` and above) while the
durable engine runs far below it, so a sandbox key is recognisable on sight and can
never be mistaken for a real one.

Two smaller commitments fall out of this and are worth stating, because both were
found the hard way. Reproducibility is seeded on a key's **counter**, not the whole
key: the high bits carry the partition, which a sandbox is handed as it opens, so
seeding on the key would make the same dataset and seed produce different runs in
two sandboxes. And a session is **owned by the principal that opened it**: it can
hold the variables of a draft only that person may read, so another caller's request
for it is answered as "not found" rather than "forbidden" — an existing id must not
become an oracle.

**A run is a session, not a request.** 50 000 cases will not finish inside an HTTP
call, and stepping through one case by hand is a conversation rather than a call. So
the sandbox is created as a server-side session with a lifetime: it is started,
driven (free-run, pause, step, resume, complete a parked task by hand, publish a
message, jump the clock), polled for progress, read for results, and torn down —
explicitly, or by a TTL when the author walks away. Batch and interactive play are
the *same* session with the scheduler either free-running or held; there is no
second mode with second semantics.

The report is computed from the sandbox's **own event log** — the same
`(ValueType, Intent)` facts Operations reads, with their frozen timestamps — so the
analysis is derived from what the engine did, not from a parallel accounting the UI
keeps. That gives, for free and without new instrumentation: per-element and
per-sequence-flow visit counts (the heat map, the same `visits` shape the runtime
overlay already draws), per-element activate→complete durations, per-instance path
and outcome, incidents, and the timeline of everything that happened.

Two details of that turned out to matter enough to write down.

**A heat map has to list what it did not see.** Element counts come from the
maintained visit counters (ADR-0080), which exist only for elements a token has
been to — so a map drawn from them alone cannot say "this branch never ran with
your data", which is the coverage question an author is usually asking. The map is
therefore built from the *model's* shape, every element and every sequence flow,
with the counts filled in and the rest left at zero. Zero is its own shade on the
canvas: "never reached" is a different statement from "reached least", and
rendering them alike would answer neither question.

**One overlay at a time, and zero does not always mean the same thing.** The same
diagram answers four questions — how often each part ran, how long the work took,
how long cases queued, and where they are stuck — and it answers one of them at a
time. A diagram shaded by two quantities at once is two answers to a question nobody
asked, and a reader cannot tell which colour belongs to which. The switcher sits over
the canvas with the scale beside it, because a shade means nothing until it is read
against one: "dark" says nothing until it says "dark is twelve days of waiting".

Two things differ between the four. Only the token counts exist for a **sequence
flow** — an edge has no work time, no queue and nothing to fail on — so the other
three leave the flows alone rather than colouring them from a different quantity
than the shapes, and the legend says "shapes only" where that is what it is. And only
for the counts does **zero deserve its own shade**: there it means "the data never
got here", which is the coverage question this map exists to answer. On the other
three zero means "no waiting here", "no work here", "nothing failed here" — the
ordinary case for most of a healthy diagram, and drawing it dashed and faded says
"never reached" about a start event that every case went through. Those are simply
left as they are, badge included.

**Incidents are counted per element** for the same reason the visits are: "five
incidents" says a run went wrong, "five incidents, all at the payout" says where.
They are folded onto the element's existing entry rather than beside it, because a
failing answer is still an answer — the element has a run and a work time too, and an
overlay shading by incidents has to agree with one shading by runs about whether
anything happened there.

**Sequence flows have no counter, and no id.** The engine aggregates elements, not
edges, so flow counts are folded out of the causal token history (ADR-0136), whose
activation records carry the flow each token arrived on. That is one scan of the
run's activations — 24 ms over ten thousand cases, and cheaper than the report's
own per-case reads. A compiled flow carries no BPMN id either, and adding one for
this would put a field on a structure every deployment builds; a flow travels named
by the two elements it joins instead, which the only client there is — one holding
the diagram — resolves against its own registry.

### Three columns and a strip

The mode began as one 300 px panel with everything stacked in it, and that panel
answers three questions that are not the same question. What the run *will* be —
the dataset, the timing, the policy, what it has to show — is decided before it
starts and read back afterwards to see what produced a number. What the run *did*
is watched while it happens. And the cases themselves are a table, which is a shape
a narrow column cannot hold at all.

Stacked, the third pushed out the first: a finished run filled the panel, and going
back to change one figure meant scrolling past everything the last run produced. So
the setup goes left, the analysis right, and the cases into a band under the diagram
— where a table has width, and where it sits beside the thing it is a table of.

The editor body becomes a grid for this rather than growing nested elements, because
`#canvas` is shared with the Design and Implement views and with bpmn-js: it keeps
its place in the DOM and is only *placed* differently. The same reasoning applies to
what the mode takes away — the palette, the context pad and direct editing are hidden
here as they are in the token simulation, because the sandbox compiled the model as
it stood when it started and a shape added now would not be in what is running.

The cases are read a page at a time from the endpoint that already pages them, so
the strip costs the same on the fifty-thousandth case as on the fiftieth. Rendering
them turned up a defect older than the strip: a case's variables were read from the
text field alone, which a boolean does not use — so a dataset carrying a flag showed
an empty column for it, in the table and in the streamed CSV alike. An empty column
is worse than a missing one: it reads as "no case had one".

### A dataset described rather than listed

There were two ways to put data into a run, and between them they left a hole at
exactly the size the Playground is built for. A list of cases is typed into the
panel: fine for three, and nobody writes three hundred, still less reviews a pull
request containing them. A CSV is uploaded and parsed on the server by the same
code a real import uses — which is the right call for a file somebody exported, and
is also why a run driven by one can never be stored as a scenario: its rows are on
the server and the browser has nothing to keep.

The third way is to describe the dataset instead: a count, and per start variable
how its value is drawn — a whole number or a decimal in a range, a boolean with a
probability, a weighted choice, a constant, a sequence that numbers the cases, or an
instant in a window. Twenty lines produce three hundred cases, and unlike three
hundred rows they are something a person writes, a reviewer reads, and a scenario
stores. The generator is deliberately field-level and unremarkable: it is not a
model of a business, it is the smallest thing that spreads values across the
branches an author drew.

Three decisions inside it are worth recording.

**It draws on the run's seed and the case's position, not on a random source.** The
same description run twice produces the same three hundred amounts, which is what
lets a generated run be quoted in a review and compared against a baseline. Because
each case draws only on its own position, the first ten cases of a run of fifty
thousand can be produced without producing the rest — which is what makes the
panel's preview *the run* rather than an illustration of it. A preview showing rows
the run would not carry would be worse than showing none.

**Dates are relative to the run's own simulated start**, not absolute. The run
happens on a virtual clock; a date typed in once is stale by the time the scenario
runs again, while "some time in the last thirty days" stays true. So a timestamp
field carries two offsets, and a scenario stored today still means what it said in
a year.

**The description travels in the run request, beside the case list.** It could have
been an endpoint of its own with the rows sent back for the caller to submit; that
would have moved fifty thousand rows through a browser to no purpose, and would have
left a generated run unstorable for the same reason a CSV one is. As a field of the
request the run endpoint already takes, a generated run is a scenario with no new
plumbing at all: the CI runner and the Modeler send the same body to the same
endpoint. A request carrying both a list and a description is refused rather than
resolved — it states two different things about what is about to run, and picking
one silently is how a run comes to mean something other than what was asked.

### One case out of the run

A results row opens the case it names: the diagram drops the run's aggregate and
draws that one case's path instead. It is the last thing the analysis cannot do — a
report says the p90 is twenty-nine hours, a rule says four cases broke it, and both
of them leave the reader wanting the one case in front of them.

The steps are **numbered in the order the case reached them**, and that number is the
whole difference between this and a second heat map: an element a case looped through
carries every step it was, because "3, 7" is the loop and a single count would hide
exactly what somebody opened the case to see. An unfinished case is drawn standing
where it stopped rather than merely visited there, which is what a stuck case is
opened for.

It **reads rather than drives**. Step mode's controls act on the whole sandbox, so
offering them over a finished batch would invite stepping a run that is over; the
case is a view, and the one control is the way back to the run. The strip over the
canvas names what is on screen instead of offering measures that are not being
drawn, and the case's detail sits *above* the report rather than instead of it — the
reader came from the run, and going back to it should not cost them the numbers they
were reading.

### The shape of the numbers, not only the numbers

Two places in the panel a number alone was the wrong answer.

**The arrival stream is drawn before the run.** "A stream of twenty per hour" and "one
every three minutes" are the same rate and not the same picture: a Poisson stream is
bursty, a takt is not, and a calendar cuts either of them into working days with
nothing overnight. The bursts are what the pools downstream will feel, and they are
invisible in the two boxes that describe them. So the panel draws the profile — a
sparkline of how many cases land in each slice of the span — as soon as the timing
changes.

The shape comes **from the server**, from the same code that lays a plan's arrivals
out. Computing it in the browser would have been less plumbing and a second
implementation of the arithmetic: a picture drawn by anything but the planner is a
picture of a stream nobody is going to get, and this record has already refused a
second simulator once. The endpoint takes a *count* rather than the cases, because
the timing does not depend on them — building fifty thousand rows to find out when
they would arrive is paying the dataset's cost to answer a question it has no part
in. A sequential plan is reported as having no schedule rather than as a flat line,
because its next arrival waits on the run; that is a thing to say, not to draw.

**Numbers that are compared carry a gauge.** The report's tables answer "which is the
big one" badly — total waiting per element, utilisation per pool, and the duration of
each case in a page of fifty are all read by scanning a column of formatted
durations. Each of those now carries a track under it, filled to the value's share of
its column's scale, and the four duration tiles share one scale so the spread between
the fastest case and the slowest is a shape rather than four numbers to subtract.

The rules are the same everywhere they appear. The **value stays in text** beside the
gauge: a bar is the comparison, never the fact, and nothing here is read off a length
alone. The **empty part of the track is drawn**, because a full bar with no track
underlines its own number and a small one reads as a stray mark. A column is scaled
to the **largest value in it**, so a length means something against the rows beside it
— except utilisation, which is scaled to a full hundred, because the question there is
how full a pool was rather than which of them was fullest, and a bar that filled at
the busiest would read as saturated at forty percent.

### From a screen somebody reads to a check something runs

A report answers "how did that go?" and needs a person to judge it. Two things
turn it into an answer a machine can act on, and both are pure functions of the
report — no sandbox, no clock — so either can be applied to a stored run long
after the sandbox that produced it is gone.

**Expectations** state what the run has to show: completions, incidents, the three
duration bounds, per-element visit bounds, and a queue bound per pool. Coverage and
outcome turn out to be the same statement about the same counter — "the approval
branch must be exercised" is a minimum of one, "the error branch must stay rare" is
a maximum — so they share one field rather than two features. Every expectation is
optional and an omitted one is not checked; the two places where zero is itself the
target ("no incidents at all", "this queue must never form") are a pointer and a map
entry, because a zero value that silently asserts something is how an expectation
fails a run nobody aimed it at.

**A rule** is the expectation stated per case rather than per run, and it is the
statement the bounds above cannot make. "The median is under four hours" is true of
a run; "an application under 50 000 from a grade-A customer is approved" is true of
a *case*, and a run that holds it nine times in ten is not nine tenths right — it is
wrong for the tenth, and the run-wide numbers will not say which one.

Both halves are FEEL, the language the diagram's own gateways are written in, so an
author states the rule the way the model states the decision: a `when` that selects
the cases and a `then` they have to show. The case's variables are the scope, as the
run left them, plus `end` — the BPMN id of the last element it reached — and
`durationSeconds`. Those two shadow a case variable of the same name deliberately:
a rule is written against that vocabulary, and quietly reading the model's own `end`
instead would make it mean something other than what it says.

Three decisions in how it judges:

- **A `when` that does not evaluate to true does not select the case.** That is the
  reading a sequence-flow condition already gets, and it is why a rule naming a
  variable no case carries selects nothing rather than failing everything. The
  outcome reports the matched count, so a rule that selected nothing says so instead
  of passing quietly.
- **An unfinished case leaves the rule undecided**, counted but neither passed nor
  failed. A rule about an outcome cannot be decided for a case that has no outcome;
  failing it here would report one problem twice, under a name that does not describe
  it, when the completion expectation already covers it.
- **The offending cases are named, and bounded.** A count sends a reader looking; the
  case numbers send them to the rows that did it, which the results strip marks. A
  rule broken in fifty thousand cases would otherwise put fifty thousand indices in a
  response nobody reads, so the sample is capped and says when it was cut.

The rules are judged in a pass of their own rather than inside the report, because
they cost what the report deliberately does not: the report reads each case's record,
this reads its variables too. A run with no rules should not pay for that. Their
outcomes then join the verdict as checks, so one thing decides whether a build goes
red — a panel showing a passing verdict beside a broken rule would be two answers to
one question. A check carries a mark saying it came from a rule, so a client can show
the sentence and its breakdown once rather than twice in a column too narrow for
either.

**A comparison** answers what one report cannot however complete it is: did that
change help? It carries the raw numbers and the direction that counts as good, so a
reader does not have to know that more completions is progress and a longer p90 is
not. Utilisation is reported and deliberately left unjudged — a pool that fell from
99 % to 62 % has room now, or the work stopped arriving, and the same number cannot
tell you which.

**A scenario is the three requests that make a run** — open a session, start the
batch, judge the report — stored as the bodies those endpoints already take. The
alternative was a parallel set of structures describing a stub policy, an arrival
profile and a set of expectations a second time. This cannot drift from the
endpoints, because it *is* them: a client that can run a scenario is one that can
replay three requests, which is exactly what the CI runner does and exactly what
the Modeler does with the same record. The design-time store keeps it opaque for the
reason a form's schema is opaque to it (ADR-0028): storage has no business
understanding a stub policy.

The **baseline** — one kept report — is what the next run is measured against. It is
recorded by a call of its own, because editing what a scenario runs must not throw
away the run it is compared with, and only from a run that passed: a baseline is the
thing to beat, so keeping a failing one would hide the failure from every run after
it.

**A published seed has to survive the client that reads it.** The session response
carries its seed precisely so a caller can write it down and repeat the run, and the
first one was a clock in nanoseconds — around 1.8 × 10¹⁸, past the 2⁵³ a JSON number
holds without rounding. Every browser that read one back got a different number, so
a scenario saved as reproducible came back with different figures and nothing said
why. A generated seed is now bounded to what a JSON number carries exactly; fifty-
three bits of clock is every bit as good a seed. Anything else this API hands out
for a client to hand back has the same obligation, or travels as a string the way an
instance key does.

### Scope decided

Four questions were open when this record was first drafted; all four are decided
here, because each of them changes what has to be built.

- **The run's source is either a draft or a deployed version.** The draft is the
  everyday case; pointing the playground at a deployed definition answers the other
  question authors ask — "why did version 7 behave like that?" — with the same
  machinery. The source is a parameter of the session; everything after the compile
  is identical, and a deployed source is still copied into a sandbox partition and
  never touched in place.
- **Interactive stepping is in scope from the start.** Pause, step, inspect a case's
  variables, complete a parked user task through its real form, publish a message,
  jump the clock. This is what ADR-0030 called Play, folded into the same session as
  the batch run instead of shipped as a separate mode.
- **The resource model is in the first round, not a follow-up.** Without pooled
  capacity, every reported waiting time is just a sum of durations the author typed
  in, and the bottleneck ranking says nothing they did not already know. Pools are
  what make the temporal analysis worth reading.
- **The ceiling is 50 000 cases per run.** It is affordable, but not for the reason
  first written here. The estimate was taken from the engine's own baseline
  (~840 instances/sec with an fsync per batch against ~6 900–16 500/sec with the log
  in memory, `benchmarks/results/baseline-5b1b9f2.md`) and predicted "seconds to tens
  of seconds". The sandbox is slower than the raw engine, because per case it also
  draws an answer, queues it against a pool, moves a virtual clock and measures what
  happened: **`go test ./playground -bench=Batch -benchtime=50000x` measures ~2.0 ms
  per case — 50 000 cases in about a minute and a half** on the four-core VM the rest
  of these numbers come from. Ten thousand take seventeen seconds and a thousand
  take under one, which is the size an author actually iterates on.
  Three things bought a 3.5× improvement over the first working version and are load-
  bearing rather than tuning: the sandbox's log does not fsync (it is discarded, so
  the "durable before visible" cost buys nothing — `wal.Options.NoFsync`, used
  nowhere else), the work-in-progress count reads the maintained per-definition
  counter instead of scanning every instance (ADR-0080), and a run settles once per
  occurrence rather than twice. What remains is the scan of the activatable jobs on
  every settle, which is the next thing to attack and is why the number is minutes
  rather than seconds.
  Size, as opposed to speed, is what the design has to respect: the report is folded
  in one pass and holds no object per case, the case list is read page by page out of
  the sandbox's own store, and the CSV is streamed. Anything that holds one object
  per case in memory — in the server or in the browser — is ruled out by this number.

Option 1 is rejected for the reason ADR-0030 gave: a browser walker that also
produced *numbers* would be a second engine whose statistics look authoritative and
are not. Option 2 is rejected because it is exactly what Play mode exists to avoid —
durable versions, real instances, real side effects — and because the real engine
cannot fast-forward a day. Option 4 is rejected because a derived queueing model
diverges from the engine on the first non-trivial construct (event subprocesses,
compensation, correlation), and because we already own a fast, deterministic
executor of the real semantics.

### Consequences

- **Positive:** the analysis is the real engine's behavior, so it cannot drift from
  production; nothing durable is created and nothing can leave the process; the
  model under test is unmodified; a run is reproducible from (dataset, config, seed)
  and therefore usable as a saved regression scenario, not just a demo; the heat map
  and the timeline reuse the runtime overlay and the event log rather than adding an
  instrumentation path; a day-long scenario runs in milliseconds.
- **Negative / trade-offs accepted:** a sandbox is a second processor in the
  process, so isolation from durable partitions has to be guaranteed structurally
  (reserved partition range, separate stores, separate run loop) and resources have
  to be bounded (instance cap, virtual-time horizon, wall-clock budget, session
  TTL). The session lifecycle, the streaming aggregation and the paginated result
  store are all consequences of the 50 000-case ceiling, and all three are work that
  a one-shot "run and return a report" design would not have needed. Finished
  instances stay in the sandbox's state store (that is the history the report and
  the per-case replay read), so a run's footprint grows with the number of cases and
  not merely with the peak work in progress — it needs a budget and a documented
  degradation, not a promise. The stub policy is a real design surface of its own, and every number the
  report gives is only as good as the durations the author configured — the report
  must say so rather than presenting a modelled duration as a measurement. Timing
  fidelity is *modelled*, not measured: the playground answers "given these service
  times, where does it pile up", never "how fast is our REST endpoint".
- **Follow-ups / risks to watch:** the batch identifies its cases by key order
  rather than by looking each one up as it is created — keys are minted from a
  monotonic counter, so ascending key order is arrival order, which costs one sorted
  key list for the whole run instead of a scan per case. The interactive path still
  scans for the newest instance, which is fine for the handful a person starts by
  hand. The activatable-job scan on every settle is the remaining cost at scale; the
  robust fix is a watermark over new job keys checked against the maintained
  open-job counter, so a job that appears out of order is still found rather than
  silently never answered. The fsync in the batch cycle is the one thing
  standing between the durable path and the in-memory numbers above, so the sandbox
  needs a log that does not fsync (a WAL option, or a temp dir on tmpfs) — a
  deliberate, contained deviation, since nothing outside the sandbox observes it.
  Per-case replay needs the log kept whole; at 50 000 cases that is a byte budget
  worth capping, with the report staying complete (it is aggregated live) even where
  the retained log no longer reaches back. The sandbox's WAL is non-durable by construction,
  which is a deliberate, contained deviation from "durable before visible"
  (invariant I2) — nothing outside the sandbox ever observes it, and it must stay
  that way. Deciding how far the *saved scenario* goes (a design-time store keyed to
  the draft, and later a CLI runner so the same scenarios can gate a deploy in CI)
  is deliberately left to a follow-up record. Resource capacity modelling ("three
  clerks share this queue") is a natural extension of the stub policy and is
  explicitly out of scope here.

## Pros and cons of the options

### Option 1 — batch mode in the browser token simulation
- Good: no server work, instant, already exists for control flow.
- Bad: no FEEL, no data, no DMN, no timers — so it cannot answer any question a
  dataset asks; and statistics coming out of a second control-flow implementation
  would be trusted exactly as far as they are wrong.

### Option 2 — deploy to a throwaway namespace
- Good: unquestionably the real engine; no new code path.
- Bad: durable versions and instances for throwaway work; real side effects unless
  every connector is rewired; cannot compress time, so a day-long scenario takes a
  day; cleanup is manual and fallible.

### Option 3 — ephemeral engine sandbox (chosen)
- Good: real semantics by construction; provably side-effect-free; nothing durable;
  virtual time; reproducible; reuses compiler, processor, event log and the existing
  runtime overlay.
- Bad: a second processor to isolate and bound; a stub-policy design surface;
  modelled durations can be mistaken for measurements if the UI is careless.

### Option 4 — a derived discrete-event simulation model
- Good: fast, and the natural home for queueing and capacity questions.
- Bad: a second semantics that must be kept in step with the engine — the fork the
  invariants exist to prevent; diverges first on exactly the constructs authors most
  need help reasoning about.

## Links

- extends [ADR-0030](0030-play-mode-simulation.md) (ephemeral engine sandbox) from
  single-instance stepping to batch runs and analysis
- reuses [ADR-0026](0026-problems-panel-and-versioned-validation.md) (dry-run
  compile), [ADR-0084](0084-csv-batch-validation.md) and
  [ADR-0139](0139-csv-to-json-connector.md) (CSV parsing and the row-list shape),
  [ADR-0025](0025-full-properties-panel.md) (example data as default stub results),
  the runtime overlay's visit counters ([ADR-0080](0080-runtime-aggregate-counters.md))
- borrows the stub vocabulary of [ADR-0120](0120-mockup-service-task.md) as run
  configuration rather than model content
- leaves [ADR-0078](0078-design-view-token-simulation.md) /
  [ADR-0096](0096-token-simulation-events-and-inclusive-gateways.md) in place: the
  Design view keeps its engine-free teaching aid
- follows [ADR-0147](0147-splitting-the-api-server-object.md) (a new API area is its own
  service package) and [ADR-0012](0012-web-ui-app-shell.md) /
  [ADR-0013](0013-embed-bpmn-js-modeler.md) (buildless, self-contained UI)
- honors invariants I3 (single writer per partition — the sandbox owns its own),
  I5 (compile, don't interpret) and I6 (frozen timestamps, deterministic replay);
  deliberately non-durable, unlike the design-time sidecar stores of [ADR-0019](0019-durable-deployments.md)
