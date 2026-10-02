# ADR-DRAFT: Atlas shows the signals its deployed models throw and receive

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers
- **Open question:** whether deployed definitions are meant to stay readable by every signed-in
  caller. `GET /api/v1/processes/{key}/xml` is `roleAny` today. [ADR-0071](0071-sharing-scopes.md)
  left runtime visibility out of scope, and [ADR-0275](0275-instance-visibility.md) gated
  instances by project but not definitions. §5 lets a modeler read every project's signal points
  because that XML already says the same. If definitions become object-gated, §5 narrows with
  them.
- **Question checked:** 2026-10

## Context and problem statement

A modeler gave a process a signal throw named `test.signal_1`, deployed it, and looked for the
signal on the Console's *Events* page. It was not there, and nothing on the page said why.

The page is correct by its own definition. It lists the event catalogue
([ADR-0435](0435-one-catalogue-of-the-events-atlas-emits.md)), the events Atlas itself emits under
`atlas.*`, held to the code by tests in both directions. A model's own signal is not an Atlas
fact, so it does not belong in the catalogue. The page's first words, "Everything atlas emits",
read to a modeler as if it did.

Behind the misunderstanding is a real gap. A signal couples models by name alone:

- **Engine-wide.** A throw reaches every catch, boundary, event subprocess and signal start of
  the same name, in every instance and every project
  ([ADR-0088](0088-signal-events.md)). Sharing scopes govern authoring, not execution
  ([ADR-0071](0071-sharing-scopes.md)).
- **Not buffered.** A throw that matches nothing is a no-op. There is no "must be caught"
  validation (ADR-0088).
- **Only a model throws one.** `api/openapi.go` declares no route that broadcasts a signal. The
  deployed definitions are therefore the complete set of throwers.
- **Every variable is the payload.** The throw writes every variable of the throwing instance
  into each receiving instance's scope (`instanceVariables` in `engine/behavior.go`,
  [ADR-0431](0431-system-processes-announce-their-facts-as-signals.md)). Nothing on the throw
  narrows it: a throw event carries no I/O mapping, and declared signal payloads are an open item
  of ADR-0435 §10.
- **Only the newest version starts.** A redeploy supersedes the signal starts of older versions
  (`supersedeStarts` in `engine/processor.go`). Catches, boundaries, event subprocesses and
  throws of an older version stay live while its instances run. A deactivated definition does
  not start on a broadcast ([ADR-0119](0119-deactivate-deployed-process.md)).

Four things can go wrong without anybody seeing them:

1. **A throw reaches nobody.** Every throw of the name is lost.
2. **A receiver waits for a name nothing throws.** A typo between thrower and receiver produces
   exactly this pair: one name thrown and never received, one received and never thrown.
3. **Two projects chose the same name.** They are coupled without knowing it. A throw in one
   project fires an interrupting boundary or event subprocess in the other, and the work it guards
   is cancelled.
4. **Data crosses project lines.** ADR-0275 lets the members of the project a definition was
   deployed from read its instances. A signal copies every variable of the throwing instance
   into an instance of whichever project receives it. ADR-0435 §6 closes this only for
   catalogued `atlas.*` events with personal-data fields. It says so itself: "Signals outside
   the catalogue are outside the rule."

What exists today:

- **For `atlas.*`:** the catalogue, and an administrator's view of who listens
  (`GET /api/v1/event-catalog/listeners`, ADR-0435 §7).
- **For messages:** `GET /api/v1/message-sources`
  ([ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) §6). It lists inbound
  watches, product actions and where processes wait, and the Modeler's message picker reads it to
  say whether a name is fed.
- **For signals:** nothing beyond the model. The Modeler's signal picker offers the model's own
  signals and the listenable catalogue entries, not the names other deployed models use.

The question this record answers: **how Atlas shows the signals the installation's own models
throw and receive, so that a modeler and an operator can see who throws a name, who receives it,
and where it crosses a project, without presenting an observation as a contract and without
undoing ADR-0435's split.**

## Decision drivers

- **An observation is not a contract.** A catalogue entry promises a payload, marks personal
  data, and is held to the code by tests. Nothing declares an own signal's payload, so nothing
  about it may look like that promise.
- **Complete for what it claims.** Since only a model throws a signal, a view derived from the
  deployed definitions can be complete for definitions. It cannot be complete for instances, and
  must say so.
- **No engine change, no new store, no cost that grows with instances.** Throw and receive
  points are compile-time facts (I5). A read whose cost grows with the instance population does
  not belong on the run loop ([ADR-0239](0239-off-loop-queries.md)).
- **One answer on every surface.** The Console, the Modeler and MCP read the same route
  ([ADR-0016](0016-mcp-server-over-http-api.md)).
- **No role reads more than it already can.** The view may aggregate what a caller can read, not
  widen it.
- **ADR-0435 stays whole.** Receivers of `atlas.*` names remain the administrator's view.

## Considered options

1. **Status quo, with clearer page text.**
2. **Admit own signals to the catalogue.** Either as entries in `eventcatalog.Entries`, or as a
   design-time registry of installation signals with a declared meaning and payload.
3. **A derived inventory of the deployed definitions.**
4. **Runtime observation.** Record every broadcast and show how often a name was thrown, when
   last, and how many receivers it reached.

## Decision outcome

Chosen: **option 3, with the page text of option 1.** Option 2's registry and option 4's counts
are follow-ups that build on it (§7).

### 1. What the inventory is

For each signal name, the inventory lists every deployed element that throws or receives it. It
is derived on request from the compiled definitions, through `CompiledProcess.SignalPoints`
(added by ADR-0435's first slice). It is not stored, so there is nothing to drift.

A point carries:

- the process id, name, version, and whether that version is the newest of its process;
- its project, and whether it is a system process;
- whether the definition is active;
- the element id and its role: `throw`, `start`, `catch`, `boundary` or `event-subprocess`. A
  signal end event throws, and is reported with the role `throw`, as `SignalPoints` does;
- for a boundary or event subprocess, whether it interrupts.

### 2. What it covers

- **Signals whose name is outside `atlas.*`.**
- **A throw of an `atlas.*` name by a model outside the system project.** It appears with the
  finding `signal.reserved-name` (§4), because the throw is the installation's own act. A
  receiver of an `atlas.*` name stays where ADR-0435 put it.
- **Not drafts.** A draft runs nothing.
- **Not instances.** See §7.
- **Not messages.** A message has sources the definitions do not show: the publish route, inbound
  watches, product actions. Message-sources already answers "is this name fed" for the Modeler.
  What it lacks, a process's own message throw as a source, is a change with its own record
  ([ADR-draft-the-messages-deployed-models-send-and-receive](draft-the-messages-deployed-models-send-and-receive.md)). Keeping it out keeps this record to the channel that has no view at all.

### 3. Versions

- **All versions are listed.** The inventory lists every deployed version and marks the newest.
- **Findings read only the newest version of each process.** An older version's start never
  fires. Whether its catches and throws are still live depends on running instances, which this
  slice does not read.
- **The page states the limit.** An older version's running instance can still throw a name the
  newest version dropped, or wait for one.

### 4. Findings

One function computes them. The route, the Console and MCP show its answer, so the three
surfaces cannot disagree.

| Finding | When | What it means |
|---|---|---|
| `signal.unreceived` | The name is thrown, and no newest, active definition receives it. | Every throw is lost: a broadcast is not buffered (ADR-0088). |
| `signal.unthrown` | The name is received, and no deployed definition throws it. | The receiver waits until a model that throws it is deployed. No route broadcasts a signal. |
| `signal.crosses-projects` | The name is thrown in one project and received in another, or by a definition without a project. | Not an error. Every variable of the throwing instance is written into an instance of the other project, whose members may read it (ADR-0275). A throw here fires an interrupting receiver there, which cancels the work it guards. |
| `signal.reserved-name` | A model outside the system project throws an `atlas.*` name. | It speaks for Atlas. The Modeler already warns before the deploy (ADR-0435); this shows what was deployed anyway. |

**Similar names are not a finding** (`order-cancelled` beside `order_cancelled`). A heuristic
produces false positives in a list meant to be trusted. The typo case is caught from both ends
anyway, as one `signal.unreceived` and one `signal.unthrown`.

**Not in the Problems panel.** Whether a receiver exists depends on other deployments, and the
order of deploys is legitimately free: a receiver may be deployed after its thrower. Validation
stays a property of the model ([ADR-0026](0026-problems-panel-and-versioned-validation.md)) and,
since ADR-0435, of the caller. The Modeler shows the inventory as a hint beside the signal picker
(§6), the way it already shows a message's sources.

### 5. Who may read it

- **The route.** `GET /api/v1/signals` requires `modeler`. Choosing a name is modelling, as for
  `GET /api/v1/event-catalog` and `GET /api/v1/message-sources`.
- **Every project's points.** The same facts are in each deployed definition's XML, which
  `GET /api/v1/processes/{key}/xml` hands every signed-in caller. Withholding the aggregate would
  protect nothing that one listing and one read per definition do not already give.
- **One rule.** The listing passes each definition through the check that governs reading that
  definition. Today that check admits every signed-in caller, so the filter changes nothing. If
  definitions become object-gated, the inventory narrows with them. That is the open question
  above.
- **No personal-data column.** Nothing declares which variables of an own signal are personal.
  The inventory cannot say, and does not guess.
- **ADR-0435's split is unchanged.** `GET /api/v1/event-catalog/listeners` stays
  administrator-only for `atlas.*` names.

**The case for administrator-only, and why it is not taken.** ADR-0435 §7 made the cross-project
map of `atlas.*` listeners the administrator's, because it is a map of where personal data
flows. This inventory is also a map of where data flows, and it is a list of names somebody
could catch. It is still given to the modeler, for three reasons:

1. What it shows, the definitions already show.
2. The person who most needs `signal.crosses-projects` is the modeler whose instance's variables
   leave the project. An administrator-only view hides the leak from the one person who can fix
   the model.
3. The protection that matters is a rule at deploy (§7), not a hidden list.

The cost is accepted: a modeler with bad intent finds a name to catch faster.

### 6. Surfaces

- **The Console's *Events* page** gets two sections.
  - *Events atlas emits*: the catalogue, unchanged.
  - *Signals of this installation*: the inventory, grouped by name, with its findings and a
    filter for names that have one.

  The introduction says which section is which. It no longer opens with "Everything atlas emits"
  unqualified.
- **The Modeler.**
  - A receiving signal element's picker gains a group *Signals deployed models throw*, beside
    *Events atlas emits*.
  - A throwing element shows one line: "received by N deployed processes", or "no deployed
    process receives it". The line links to the page.
- **MCP.** A tool `atlas_signals` proxies the route. It is a read that leaks nothing the route
  hides, so it is a tool (ADR-0016). The tool count in `README.md` moves with it.
- **The handbook.** The chapter *Ereignisse / Events* gains a section, in both languages, on what
  the catalogue holds and what the inventory holds.

### 7. What this record does not decide

- **Runtime counts.** These are how many instances wait on a name now, how often it was thrown,
  and when last.
  - Waiting now is a scan of open subscriptions per name (`SubscribedSignals`). It grows with
    instances, so it runs off the loop (ADR-0239) or becomes a maintained counter
    ([ADR-0080](0080-runtime-aggregate-counters.md)).
  - Thrown counts need the broadcast recorded. Today a broadcast leaves no record of its own
    beyond the throw element's and the correlated subscriptions'. A zero-receiver broadcast
    leaves only the throw element's.
  - A metric labelled by signal name has unbounded cardinality, because the names are the
    installation's.

  That is a record of its own.
- **Refusing an `atlas.*` throw by a model outside the system project.** The finding shows such a
  model, and the Modeler warns before the deploy (ADR-0435). Whether the deploy is refused is a
  rule with its own record. Such a rule must not stop a server from starting with a model
  deployed before it (`AGENTS.md`, [ADR-0177](0177-reload-skips-the-deploy-gate.md)).
  - **It protects integrity, not privilege.** `modeler` includes deploy, and deploy is code
    execution: risk R-09 in `docs/compliance/isds-konzept.md`, restated in
    [ADR-0315](0315-portal-roles-and-responsibilities.md). A modeler who throws an `atlas.*` name
    gains nothing that deploying does not already give.
  - **What the rule would add** is that a model cannot speak for Atlas, by a name chosen by
    accident or on purpose, and that the attempt is refused where it is made instead of found
    later.
  - **It does not replace the measures R-09 names.** These are a defined circle of accounts that
    may deploy (M-05), the `modeler` role given only to authors, and script languages switched
    off where they are not needed (M-09). They bound what any model may do. A rule on names
    bounds only this one way of doing it.
- **Declared signal payloads.** Choosing on the throw which variables a signal carries, instead
  of every variable of the instance, is an engine change named in ADR-0435 §10. It is a record of
  its own.
- **A deploy rule for receivers in another project.** This would close, for own signals, what
  ADR-0435 §6 closed for catalogued ones. It needs declared signal payloads or a project rule,
  and it is a record of its own. This record makes the gap visible. It does not close it.
- **A registry of the installation's own signals** (option 2). Once payloads can be declared, a
  declared own signal can carry the same contract and the same access rule as a catalogue entry.
  The inventory then marks which names are declared.
- **Messages.** A process's own message throw as a fourth kind of message source, beside inbound
  watches, product actions and waiting processes, is decided in
  ADR-draft-the-messages-deployed-models-send-and-receive. A message-kind send task compiles to a
  message throw ([ADR-0112](0112-send-tasks.md)) and belongs there too.
- **Cross-partition broadcast** ([ADR-0006](0006-partition-routing-and-cross-partition.md),
  ADR-0088). The inventory reads definitions, which are server-wide, so it is unaffected.

### Consequences

- **Positive:**
  - The question "who throws this, who receives it" has an answer outside the model. The
    Modeler asks it while modelling, and an operator asks it across the installation.
  - A typo between thrower and receiver shows as a pair of findings instead of a process that
    simply never moves.
  - A name shared by two projects, and the variables that cross with it, become visible to the
    people on both sides.
  - The catalogue stays a contract. The page says what it lists and what it does not.
  - Nothing is stored and nothing touches the engine. The cost is bounded by the number of
    deployed definitions.
- **Negative / trade-offs accepted:**
  - The findings read definitions, not instances. An older version's running instance can make a
    finding wrong in either direction until runtime counts exist. The page says so.
  - A modeler sees every project's signal names and points in one list. That is no more than the
    definitions already give, but it is easier to find.
  - `signal.crosses-projects` names a data flow it cannot stop. A finding without a remedy in
    Atlas is uncomfortable, and it is accepted because the alternative is not knowing.
  - Two lists on one page invite the question this record started from: why a name is in one and
    not the other. The page has to answer it in its own text.
  - Messages keep their own, narrower view until
    ADR-draft-the-messages-deployed-models-send-and-receive lands.
- **Follow-ups / risks to watch:**
  - The open question. If deployed definitions become object-gated, §5's filter must follow
    them.
  - Declared signal payloads, then a deploy rule for cross-project receivers.
  - The deploy rule for `atlas.*` throws (§7), weighed as integrity rather than privilege
    (R-09).
  - Runtime counts (§7).
  - A process's own message throws in message-sources
    (ADR-draft-the-messages-deployed-models-send-and-receive).
  - Whether `signal.crosses-projects` should also be raised to a project's owner, not only shown
    to whoever looks.

## Pros and cons of the options

### Option 1: status quo, with clearer page text
- Good: nothing to build. It removes the misunderstanding that prompted this record.
- Bad: none of the four failures becomes visible. A modeler still has to read every other
  deployed model to know whether a name is in use.

### Option 2: admit own signals to the catalogue
- Good:
  - One list for everything that can be listened to.
  - With declared payloads, own signals could carry the same personal-data rule as `atlas.*`.
- Bad:
  - The catalogue is held to Atlas's code by drift tests and promises payloads. An own signal's
    payload is every variable of the throwing instance, so any promise about it would be untrue.
  - A registry needs a design-time store, an owner per entry and a review step. That is
    governance an installation may not want, before the engine can enforce what the registry
    says.
  - It mixes Atlas's contract with the installation's habits on the page whose purpose is to tell
    them apart.

### Option 3: a derived inventory (chosen)
- Good:
  - Complete for definitions, because only a model throws a signal.
  - No store, no engine change, no drift.
  - The same shape as message-sources, which the Modeler already reads.
- Bad:
  - Blind to instances until runtime counts exist.
  - Shows a data flow without stopping it.

### Option 4: runtime observation
- Good: answers what actually happened, including in older versions.
- Bad:
  - A broadcast would need a record or a counter of its own on the processor path (I1).
  - A metric per name has unbounded cardinality.
  - It still says nothing about a name that has not been thrown yet, which is exactly the typo
    case.

## Links

- [ADR-0435](0435-one-catalogue-of-the-events-atlas-emits.md): the catalogue of Atlas's own
  events, which this record keeps separate.
- [ADR-0088](0088-signal-events.md): signal semantics: broadcast, not buffered, no "must be
  caught" check.
- [ADR-0431](0431-system-processes-announce-their-facts-as-signals.md): a signal carries every
  variable of the throwing instance.
- [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) §6: message sources,
  the pattern this follows for signals.
- [ADR-0071](0071-sharing-scopes.md), [ADR-0275](0275-instance-visibility.md): what a project
  member may read, and why a signal crossing projects matters.
- [ADR-0119](0119-deactivate-deployed-process.md): a deactivated definition does not start on a
  broadcast.
- [ADR-0026](0026-problems-panel-and-versioned-validation.md): why the findings are not in the
  Problems panel.
