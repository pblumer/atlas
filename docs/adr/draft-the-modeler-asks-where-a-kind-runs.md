# ADR-DRAFT: The Modeler asks the server where a connector kind runs

- **Status:** Accepted
- **Date:** 2026-08-25
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0164](0164-no-in-process-service-tasks.md) decided that a side-effecting service
task belongs on a worker, and the Modeler's connector picker was given a badge to say
so at the moment the choice is made: every kind Atlas runs itself carries an
**in-engine** mark, and the chosen kind carries a notice recommending a job worker
instead. The badge was decided in the browser, from a constant on the catalog entry:
the plain job worker set `outOfProcess: true`, and everything else was in-engine by
omission.

That was true the day it was written and false a day later, twice over.

- **A kind's placement is per install.** [ADR-0168](0168-connector-work-on-a-worker.md)
  moved connector work onto workers, and the following change made four kinds —
  CSV, mail, script, web scraping — offloaded *by default*, onto a worker the server
  supervises itself. `--offload-connectors` moves more; `--in-process-connectors`
  moves none. All three are the server's command line, which a browser cannot read.
- **A kind's placement can be fixed the other way.** [ADR-0173](0173-generic-sql-connector.md)
  built the three SQL connectors worker-first, with no in-process handler at all, and
  Entra ID followed. No configuration brings those into the engine.

So the picker told an author that the E-Mail connector runs in the engine on an install
where the engine had already handed it to a child process, and told them the SQL
connectors run in the engine directly beside their own description, "…against a SQL
Server database **on a worker**". A badge that cannot be wrong in the direction that
matters is worse than no badge: it is read as a fact about *this* server.

The question this record answers is where that fact should come from.

## Decision drivers

- **The badge is only worth having if it is about this install.** An author reads it as
  "what will happen when I deploy this here", because that is what it looks like.
- **One authority, not a mirror of one.** The engine already holds the answer:
  offloading a kind is implemented as *removing its in-process handler*, and the same
  registry decides whether the type-keyed pull may lease the job at all.
- **A new connector must not silently re-open the gap.** The last one (SQL) shipped
  correct code and a wrong badge, because nothing failed.
- **Say nothing rather than something false.** The failure mode being fixed is a
  confident wrong answer, so every degraded path must fall back to silence.
- **The advice has to be takeable.** "Prefer a job worker" is useless on a kind that
  has no out-of-process form, and misdescribes one that was never in the engine.

## Considered options

1. **Correct the constants.** Mark the worker-born kinds `outOfProcess`, add a third
   value for the kinds offloaded by default, and keep the decision in the browser.
2. **Ask the server.** A read-only endpoint reports, per catalog kind, where this
   server runs it; the picker renders the badge from the answer.
3. **Drop the badge.** Say where work runs only in the Workers view, which already
   reports it per job type from live state.

## Decision outcome

Chosen: **option 2 — the picker asks the server**, over a new
`GET /api/v1/connector-kinds`.

The answer is derived, not declared. Two facts the server already holds decide it:
`jobRunner.Handles(jobType)` says whether this server runs the kind itself — that is
the very thing `applyOffloadedKinds` removes — and `offloadableKinds` says whether an
out-of-process form exists at all. Four placements fall out, and each is a different
sentence to an author:

| placement | means | the notice says |
|---|---|---|
| `engine` | runs here, can be moved | prefer a job worker (ADR-0164) |
| `engine-only` | runs here, has no worker form | there is nothing to move |
| `worker` | offloaded on this server | where the connector is configured (ADR-0168) |
| `worker-only` | born on a worker | it has no in-engine form (ADR-0173) |

The two `-only` values are not pedantry. Each is exactly the case where the plain
value's *advice* would be wrong, and giving unusable advice is how a badge loses the
reader's trust a second time.

**Option 1 was the cheap fix and is the one that just failed.** It leaves the same
class of bug in place — a second list, in another language, that a connector author
must remember to update — and it still cannot answer for `--offload-connectors`,
because the browser does not know the server's command line. Deriving the answer from
the registry means a kind that moves later needs no edit here at all.

**Option 3 gives up something real.** The Workers view reports the consequence after
the fact, to an operator. The picker is where the choice is made, by an author, and
ADR-0164's whole point is to reach them there.

**Degradation is silence.** A kind the server does not report, an unreachable server, a
Modeler mounted with no API: no badge and no notice, rather than a guess. Two catalog
entries are deliberately unreported for the same reason — the plain job worker, whose
name already is the statement, and the Mockup, which the engine simulates and which
creates no job that could run anywhere.

### Consequences

- **Positive:** the badge describes the install the author is deploying to, including
  the default four kinds on a supervised worker and the kinds born on one. The advice
  attached to it is takeable in every case. A kind that moves later — by default, by
  flag, or by being written worker-first — needs no browser-side edit.
- **Negative / trade-offs accepted:** the picker now depends on a request, so it renders
  once without badges and re-renders when the answer lands (once per page, shared across
  panels); a Modeler with no server reachable shows no placement at all, which is a loss
  against a constant that was at least sometimes right. The catalog id → job type table
  is a second place a *new* connector must be named — held to the picker by
  `TestEveryCatalogKindHasAPlacement` and to `--offload-connectors` by
  `TestPlacementJobTypesAgreeWithOffloadableKinds`, so the omission fails a test rather
  than shipping.
- **Follow-ups / risks to watch:** the placement answers about the kind, not about
  whether a feature is switched on — a server built without user provisioning still
  reports that kind `engine-only`, because "worker" would be a worse answer for work
  that has no worker form. If a second feature-gated kind appears, that reading is worth
  revisiting. The Workers view's "configured nowhere" list and this badge now answer
  neighbouring questions from the same two authorities; they should stay consistent.
  And two panels outside the connector picker author work that is equally offloadable
  and still say nothing about where it runs — a script task's language and a business
  rule task's decision binding, both of which `--offload-connectors` accepts and the
  first of which is offloaded by default. The derivation extends to them unchanged —
  they are already named in `offloadableKinds` — but they are not in the catalog table,
  so the endpoint does not report them and neither panel shows a badge yet.

## Pros and cons of the options

### Option 1 — correct the constants
- Good: no endpoint, no request, no degraded path; the picker stays synchronous.
- Bad: cannot express `--offload-connectors` or `--in-process-connectors` at all, so it
  is wrong on any install that uses them; keeps a hand-maintained mirror in a second
  language, which is the mechanism that produced this bug.

### Option 2 — ask the server
- Good: one authority, the same one the lease path consults; correct under every flag;
  a moved kind needs no browser edit; wrong answers become silence.
- Bad: an asynchronous dependency in a panel that was synchronous; no badge at all
  without a server.

### Option 3 — drop the badge
- Good: nothing to keep true.
- Bad: says nothing where the choice is made, which is the one place ADR-0164 wanted it
  said.

## Links

- states where the work should run: [ADR-0164](0164-no-in-process-service-tasks.md)
- what moves a kind, and where its credential then lives: [ADR-0168](0168-connector-work-on-a-worker.md)
- the kinds born on a worker: [ADR-0173](0173-generic-sql-connector.md)
- the view that reports the same fact after deployment: [ADR-0157](0157-worker-processes-supervision-and-console.md)
- the catalog the badge is rendered from: [ADR-0067](0067-service-task-connector-catalog.md)
