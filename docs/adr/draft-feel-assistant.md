# ADR-DRAFT: A FEEL expression is written in a conversation, and checked by the engine before anybody reads it

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas engine team
- **Open question:** Whether a small free model — the kind an installation points an
  OpenRouter Worker at — writes this build's FEEL well enough within three attempts is not
  measured. The bound on correction rounds, and the claim that the engine's check makes
  such a model usable, rest on that gap. Every request now reports what it came to (see
  *Measuring the prompt*), so the question is answerable from an installation's own log
  and metrics; it stays open until somebody has read them.
- **Question checked:** 2026-10

## Context and problem statement

FEEL is where a process model does its arithmetic: gateway conditions, input and output
mappings, script tasks, correlation keys, a decision's output cells and literal
expressions. It is also the part of a model its authors find hardest to write. The syntax
is small but unfamiliar, the null semantics are unforgiving (`sum([])` is null, not 0;
`true and null` is null), and the dialect most people learnt it from is not this one —
`is defined`, `get or else` and `trim` are Camunda's, and here a call to any of them can
only be null ([ADR-0388](0388-a-call-that-can-only-be-null-is-refused-at-deploy.md)).

Atlas already has the pieces for help: an AI Worker an operator configured
([ADR-0255](0255-agent-models-are-console-workers.md)), a design-time path that asks it
([ADR-0260](0260-ai-form-generation.md)), a route that validates FEEL and one that
evaluates it, and a FEEL editor. What it lacks is a place where an author says what an
expression should compute, gets one, tries it, and puts it where it belongs — from any
screen, because FEEL fields are on many screens.

The question is how, given that the model most likely to be configured for this is a
free one, and a free model's FEEL is a guess.

## Decision drivers

- **One configuration.** The model, its endpoint and its key are the agent Worker's
  (ADR-0255). No second place to configure a model, no key in the browser.
- **The engine decides what is valid.** A model's FEEL must be judged by the engine that
  will run it, with the deploy's own refusals (ADR-0388), not by the model's confidence.
- **Usable on a free model.** An installation that points an OpenRouter Worker at a free
  model must get expressions that run, without paying for a frontier model.
- **Reachable everywhere.** One shortcut from any screen, and an affordance on every
  field that takes an expression.
- **Nothing stored without the author.** As for diagrams (ADR-0032) and forms (ADR-0260):
  what a model writes is a proposal the author reads and puts somewhere themselves.

## Considered options

1. **Call a provider from the browser.** The console asks a model directly.
2. **Leave it to MCP.** An author with an MCP client asks their own agent, which checks
   with the FEEL routes.
3. **A design-time service on the agent Worker, with the engine's check and a bounded
   correction loop, and a console-wide assistant over it.**
4. **Option 3 with a multi-turn adapter.** Teach `connector/agent` to send a conversation
   as turns rather than one message.

## Decision outcome

Chosen option: **3**.

**The service.** `api/feelgen` takes a conversation, asks the agent Worker the request
names (or the only one there is) through the same closures form generation uses, and
reads the answer into a contract: an expression, an explanation, an example and the
result the example produces. Every expression is then put through the engine:
`expr.CompileAuto`, `expr.CheckCallsError` (the deploy's refusal of calls that can only
be null, with its standard equivalents), and an evaluation against the example. The
stated result is compared with FEEL's own equality — and, because JSON has no date or
duration, a stated text is also read as each temporal type before the two are called
different. An answer that fails, binds no value for an input it reads, or disagrees with
its own stated result goes back to the model with the engine's words, at most twice. The
author then gets the last answer and the engine's verdict on it, whichever way it went.

**The prompt is held to the engine.** Its function list is the engine's registry, read
when it is built (`expr.BuiltinNames`); every claim it makes about the language is an
expression evaluated in a test; every worked example passes the check a real answer gets.
A prompt that disagrees with the engine teaches the model to be wrong with confidence, and
the model has no other source for this dialect.

**The conversation is the console's.** The adapters send one message per call, and the
service keeps nothing between requests: the console sends the whole conversation each
time, and every response carries the reply to keep as the model's turn — the proposal in
the contract's shape, whatever shape the model actually wrote it in.

**The assistant.** `api/web/feel-assistant.js`: a chat, a FEEL editor, a test pane over
the evaluate route, Copy, and Apply. It opens with Ctrl/⌘+Shift+E from anywhere, from a
spark in the top bar, from a mini spark on every FEEL field (added where every FEEL field
is made, `attachFeelEditor`), and from a spark beside a focused dmn-js cell. Opened from a
field, it starts from that field's expression and Apply writes into it through the field's
own input and change events — an fx field keeps its `=` marker. A decision table's input
cell is recognised and not written to: it takes a unary test, which this assistant neither
writes nor checks. The history of the last thirty expressions and the favourites are kept
in the browser's local storage, the conversation in its session storage.

**Measuring the prompt.** The prompt is tuned rather than designed, and tuning it by
impression is how a prompt grows sentences nobody can justify. So every request that
reached a model reports an outcome: the Worker and model, how it ended (`settled`,
`question`, `unsettled`, `cut_short`, `unanswered`), and per round the form the model
answered in (`contract`, `code_block`, `prose`, `unusable`), what the check found (`none`,
`compile`, `calls`, `evaluate`, `missing`, `mismatch`, `empty`, `unusable`), the engine's
verdict and the callees it refused. The server writes it as one log line,
`event=feel_assistant.answered`, and — where metrics are on — counts it:
`atlas_feel_assistant_requests_total{outcome}`, `atlas_feel_assistant_attempts_total{format}`,
`atlas_feel_assistant_attempt_faults_total{fault}` and `atlas_feel_assistant_request_seconds`.
The counters carry only the closed lists `feelgen` declares; the model and the callees are
values a request or a model invents, so they are in the log line and never a label
(ADR-0142). Neither carries the conversation or an expression: a chat may hold anything,
and an expression a literal the author typed.

**Not an MCP tool.** As for form generation: the caller of a tool is already a model, and
asking Atlas to ask a second one is a detour. An agent writes FEEL and checks it with the
validate and evaluate routes the assistant uses.

### Consequences

- **Positive:** A free model is usable, because what reaches the author has run. The
  deploy's refusals reach the model as corrections rather than reaching the author as
  deploy errors. One configuration and one credential serve the runtime, form generation
  and this. The prompt cannot drift from the engine without a test failing.
- **Negative / trade-offs accepted:**
  - A request costs up to three model calls, and a rate-limited free model may refuse the
    second; the service then returns the first answer with a warning rather than failing.
  - The engine evaluates expressions a model wrote, on the server, as the evaluate route
    already evaluates what an author typed. A pathological expression costs the same CPU
    in either case; nothing here bounds it further.
  - The history and favourites are per browser: they do not follow an author to another
    device, are lost with the browser's data, and cannot be shared with a team.
  - There is no audit and no cost meter, as for form generation.
  - Unary tests are not supported, so a decision table's input cells get no help beyond
    the prompt telling the model how a table splits a condition.
- **Follow-ups / risks to watch:**
  - Read the measurements: how often a free model settles in one, two or three
    attempts, how often it keeps to the contract, and which foreign functions it reaches
    for. The bound, the prompt's examples and its list of unavailable names are where
    that data goes.
  - Shared favourites — a team's library of expressions — would be a design-time store
    with owners and scopes (ADR-0205), not local storage.
  - Implementing the foreign functions the prompt names as unavailable remains ADR-0388's
    Option 5: a separate decision, not this one's.
  - Firefox binds Ctrl+Shift+E to its network monitor; whether a page may take it there
    was not established. The top-bar spark and the field sparks reach the assistant
    without it.

## Pros and cons of the options

### Option 1 — Call a provider from the browser
- Good: no server code.
- Bad: a key in the browser, a second configuration beside the agent Worker, and no
  engine between the model and the author.

### Option 2 — Leave it to MCP
- Good: nothing to build; an MCP agent can already validate and evaluate FEEL.
- Bad: it serves only the authors who have an MCP client and their own model, which is
  the opposite of the person in the Modeler who has neither.

### Option 3 — A service on the agent Worker with the engine's check (chosen)
- Good: one configuration, the engine as the judge, usable on a free model, stateless.
- Bad: up to three calls per message; a transcript in one message rather than turns.

### Option 4 — A multi-turn adapter
- Good: a provider sees a real conversation and may cache its prefix per turn.
- Bad: it changes the runtime adapters every agent round and ai task depends on, for a
  design-time feature that a transcript serves; worth it only if the transcript is shown
  to be the limit.

## Links

- relates to [ADR-0260](0260-ai-form-generation.md) — the design-time seam this reuses
- relates to [ADR-0255](0255-agent-models-are-console-workers.md) — the agent Worker record
- relates to [ADR-0388](0388-a-call-that-can-only-be-null-is-refused-at-deploy.md) — the refusal the check applies
- relates to [ADR-0032](0032-modeler-ai-copilot.md) — generated content is a proposal
- relates to [ADR-0145](0145-developer-view-for-code-fields.md) — the field contract Apply writes through
- relates to [ADR-0267](0267-console-speaks-german-first.md) — the message catalogue

## As built

- `api/feelgen`: the service, the answer contract (`ParseAnswer`), the check (`Evaluate`),
  the prompt and its tests; `expr.BuiltinNames` and `expr.ValueKind.Label` beside it.
- Routes `GET /api/v1/feel/generate/workers` and `POST /api/v1/feel/generate`, role
  modeler, both omitted from MCP with their reasons.
- `api/web/feel-assistant.js`, the mini spark in `feel.js`, the top-bar button, the
  catalogue entries in `i18n.js`; `e2e/feel-assistant.spec.mjs`.
- The measurement: `feelgen.Outcome` and its closed lists, the `Observe` hook,
  `feel_assistant.answered` in the logging catalogue, and the four metrics registered
  with the server's others (`api/feelgeneration.go`).
