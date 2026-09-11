# ADR-0304: A capability record says when somebody last confirmed it

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-10 (built 2026-09-11)
- **Deciders:** Atlas maintainers

## Context and problem statement

The gap report
([ADR-0305](0305-business-capabilities-and-value-streams.md))
compares the capability map against what a server actually runs, and every one of its
eight findings is a **fact Atlas checked**: this process is not deployed, this
dependency names nothing, two capabilities claim one implementation. That is the half
of a capability record Atlas can see.

The other half it takes entirely on trust:

| Field | What it asserts | Why Atlas cannot check it |
|-------|-----------------|---------------------------|
| `owner` | who is accountable | people change jobs; the field is free text on purpose |
| `scope` | what it is and is **not** responsible for | boundaries move when teams reorganise |
| `slas` | what it has promised, to whom | promises are renegotiated between people |
| `kpis` | where it is trying to get to | targets are reset in planning cycles |
| `inputs` / `outputs` / `resources` | the interface and what it draws on | prose about somebody else's systems |

Those five are not incidental fields. They are the reason the record exists: the
method distributes an end-to-end target downward as **internal SLAs** on the
capabilities beneath it, and it settles boundary arguments by pointing at **scope**.
A map whose realisations are all green and whose owners left two years ago is worse
than no map, because it is confidently wrong in the fields anybody acts on. The
escalation goes to somebody who has left. The three-day disbursement goal is
distributed against an SLA nobody has agreed to since.

This is the failure two records in this tree already name.
[ADR-0289](0289-worker-type-setup-in-the-panel.md) has each Worker Type's setup steps
carry a `checked` date, because nothing here can observe Google's console, and
`TestSetupDocsAreRecentlyChecked` fails once an entry has stood unread for a year.
[ADR-0293](0293-open-questions-in-records-expire.md) generalised the shape to a
record's open question. Both describe the same thing: text that goes on looking
authoritative after it has quietly stopped being true, where the reader least able to
tell is the one relying on it.

**And both are build-time tests over content in this repository.** A capability record
is runtime data in somebody else's installation. No Go test will ever fail over it,
nobody reviews it in a pull request, and the person who wrote it is not the person who
will read it. The failure is the same and the enforcement cannot be — which is what
makes this a decision rather than a third application of an existing one.

## Decision drivers

- **A date must be a statement of fact, not a promise.** ADR-0293 settled this for
  records: a `checked` month says somebody looked; a `recheck by` month is set freely
  by the author and `2099-01` makes the mechanism a no-op that still looks enforced.
- **It has to fire without anybody touching the record.** The record not being touched
  is the condition it exists to catch.
- **The person accountable usually cannot confirm it themselves.** The business owner is
  free text precisely because they often have no Atlas account, so whoever performs the
  confirmation is normally *not* them — and a date that quietly means something weaker
  than it appears to is the failure this record exists to fix, not one it may commit.
- **It must not make the map less useful when it fires.** A stale record is still the
  best information there is, and this must not become a reason to hide it.
- **One convention in this tree, not three.** A reader who has met the `checked` date
  on a Worker Type and on an open question should recognise this on sight.
- **It must be honest about what it is.** Every other gap finding is something Atlas
  verified. This one can only ever say *nobody has asserted this recently* — and the
  report must not blur the two.

## Considered options

1. **Nothing.** The gap report exists; trust that reviews happen because the map is
   visibly useful.
2. **A per-record forward deadline** — each capability names its own `reviewBy` date.
3. **A backward-looking `confirmedAt` / `confirmedBy` pair**, set only by an explicit
   act, with a horizon, surfaced as a gap finding and on the read.
4. **Derive freshness from `updatedAt`.** No new field at all.
5. **Model the review as a process in Atlas.** A recurring BPMN process with a human
   task per capability owner — the engine reviewing its own registry.

## Decision outcome

Chosen option: **3**, with option 5 named as a follow-up it enables rather than a
rival.

### What the record carries, on both kinds

| Field | Meaning |
|-------|---------|
| `confirmedAt` | unix seconds: when a person last said this record still describes reality |
| `confirmedBy` | the principal who said it |
| `confirmedWith` | free text, optional: who was **asked**, when that is somebody other than the confirmer |
| `confirmationNote` | one line, overwritten each time: what the last review found |

The note is one line and is replaced, not appended. Panorama's drift journal
([ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md)) made the same
call for the same reason: this area is a correlation surface, not a log. What the last
review found is worth carrying; a history of every review is a second thing to prune.

### Only an explicit act sets them

A confirmation is its own operation — `POST /api/v1/capabilities/{key}/confirmation`,
and the value-stream twin — and **no edit sets the date**, including an edit that
changes the owner or an SLA.

That is one rule with no exceptions to reason about, and each exception anybody would
propose is the mechanism failing:

- If any save refreshed it, fixing a typo in the summary would assert that the owner,
  the scope and every SLA had been re-checked. ADR-0289 names exactly this — bumping
  the date without re-reading "tells the next reader they were verified when they were
  not" — and doing it as a side effect makes it automatic, and therefore invisible.
- If only a save that *touches* those fields refreshed it, then changing the owner
  would assert that the SLA was re-read. It was not.

An author who has just rewritten a record confirms it in the same breath. That is one
extra call, and it is the call that carries the meaning.

**Creating a record confirms it**, to the creator and to now. Somebody just wrote it
down; that is an assertion, and the honest date is today.

### Who confirms, and who was asked

`confirmedBy` is whoever called the endpoint. It is almost never the owner.

That is not an oversight to close. The business owner is free text *because* they are
frequently an SVP or a department head with no account here, and requiring them to
confirm would leave the mechanism unusable for the majority of a map — which is the
same trap as requiring a principal in the `owner` field itself. So a confirmation is
performed by an architect, and it says so.

But then the date means "somebody with an account wrote this down", not "the
accountable person stood behind it", and those are different claims. Left as one field,
the map confirms itself: the architect who wrote the record re-reads their own prose,
and the answer comes back green in a way a reader cannot distinguish from a review the
owner actually sat in.

So the record carries **`confirmedWith`** — free text, optional, naming who was asked.
Three things follow, and each is a decision rather than a detail:

- **It is optional, and its absence is the information.** Empty means the confirmer
  spoke for the record alone. That is a legitimate confirmation — an architect
  re-reading is better than nobody re-reading — and it is a weaker one, which the
  record now says instead of implying the opposite. It also makes "which confirmations
  asked nobody" a filter rather than a reading exercise.
- **It is another unverifiable claim, and that is not a reason to refuse it.** Atlas
  cannot check that Frau Meier was asked any more than it can check that she is the
  owner. It earns its place for the reason `owner` does: the alternative is that the
  information exists only in the head of whoever did the asking, which is the person
  who will not be reading this in two years.
- **`confirmedWith` disagreeing with `owner` is a feature, not a defect.** The
  confirmation that names somebody who is not the recorded owner is exactly the case
  worth seeing — either the owner has changed, or the wrong person was asked.

**Neither of those is a finding.** The report does not flag a self-confirmation: in a
four-person installation the architect is the only person who *can* confirm, so
flagging it would flag the normal case, and a report that fires on the normal case
stops being read. And it does not compare `confirmedWith` against `owner.name`, because
string-comparing two free-text names produces a finding about spelling — "A. Meier"
against "Anna Meier" — which teaches a reader to ignore the report.

What happens instead is that the coverage read *shows* both: `confirmedBy`,
`confirmedWith`, and — since `updatedBy` is already on the record — that the
confirmation came from the same person who last wrote it. Three facts, side by side,
for a reader to weigh. No rule, no field beyond the one, and nothing pretending to know
what it cannot.

### There is no bulk confirm

No "confirm everything", no "confirm every capability I own". Refused, and the reason
is the whole mechanism: an endpoint that dates two hundred records nobody re-read is
the failure both precedents describe, industrialised and given a button.

### The horizon is one setting, defaulting to twelve months

Twelve matches ADR-0289 and ADR-0293, so the number is inherited rather than invented.

It is configurable — one `confirmationSetting` record beside the theme and the
registration setting, absence meaning the default — and that is the one place this
departs from its two precedents. Those govern content in this repository, maintained
by people who share one cadence. This governs a customer's own data, reviewed by their
business architects: a bank on a quarterly governance cycle and a four-person team on
an annual one cannot share a constant, and an interval they cannot set is one they
route around by not filling the field in at all.

**The attack is real and is accepted.** Somebody can set the interval to a hundred
years and silence the check. What that buys over option 2's per-record deadline is
that it is *one visible number in the settings*, and the gap report states the
interval it applied — so a silenced check is legible to the next reader, rather than
hidden one record at a time in a field nobody scrolls to.

### What expiry does

- **A ninth gap finding: `capability.unconfirmed`**, with the value-stream twin
  `value-stream.unconfirmed`. One kind, not two, with the detail distinguishing *never
  confirmed* from *confirmed and lapsed*. They call for different work, but they are
  one shape of disagreement — and on the day this ships every record is "never", so
  two kinds would mean one enormous list and one empty one, which then swap over a
  year. That is the same list twice.
- **The listing gains `?stale=true`.** It is the exact twin of `?realized=false`: one
  query for the work nobody has automated, one for the work nobody has re-read. Two
  backlogs, each one call.
- **The coverage read carries the confirmation block**, so a reader looking at what a
  capability promises sees, beside it, when anybody last stood behind that.

### What expiry does not do

It does not refuse a stale record, mark it invalid, or exclude it from any answer. A
record nobody has confirmed for two years is still the best information there is, and
withholding it would make the map least useful at exactly the moment it most needs
attention. It is flagged, everywhere it is read, and served.

Nor does it date the **realisation**. That half is checked against the deployment
registry every time it is read; putting a freshness date on the one thing Atlas can
verify would be ceremony where there is already a fact.

### Day one, stated rather than discovered

Every record that exists when this lands is unconfirmed, and the report will say so.
An installation with three hundred capabilities gets three hundred findings that
morning.

**Backfilling `confirmedAt` from `updatedAt` at migration is refused.** It would assert
a confirmation nobody made, for the entire map at once, silently — the same lie as
bumping a date without reading, except that no diff records it. The noise here *is*
the signal: a map nobody has ever reviewed is precisely the finding, and the report
already counts by kind, so it arrives as one number to work down rather than three
hundred lines to read.

### Consequences

- **Built.** `Confirmation` on both records, set at creation and by
  `POST …/{key}/confirmation` and by nothing else; the horizon as a settings record with
  the default at twelve; `?stale=true` on both listings; the confirmation block on the
  coverage read; and two gap findings, `capability.unconfirmed` and
  `value-stream.unconfirmed`, taking the report to ten kinds. Exposed as two MCP tools,
  whose descriptions say in as many words that only what was actually re-read may be
  confirmed. The horizon is admin-only to set and readable by anyone, because every
  answer that applies it already carries it.
- **Positive.** The two halves of a capability record are finally treated alike: the
  realisation is checked against reality, and the prose is dated by whoever last stood
  behind it. Neither is taken on trust.
- **Positive.** `?stale=true` makes review a worklist rather than an intention, in the
  same shape the automation backlog already has.
- **Positive.** It is the third instance of one pattern in this tree, so it needs
  almost no explaining to somebody who has met the other two.
- **Negative / trade-offs accepted.** A new field on both records, a new operation, a
  new setting and a ninth finding — for a property Atlas can never verify, only date.
  Every other finding in the report is a fact; this one is the absence of an
  assertion, and the report has to say so rather than letting it read like the others.
- **Negative.** `confirmedWith` is a second free-text claim about a person, next to
  `owner.name` and `owner.contact`, and nothing keeps the three consistent. That is
  accepted on the same terms as the first one: three fields that can disagree are more
  honest than one field that cannot be wrong because it says nothing.
- **Negative.** The horizon is uniform within an installation. A capability that
  genuinely changes twice a decade comes due on the same cycle as one that changes
  quarterly, and the honest response is to confirm it saying nothing has changed —
  a minute's work, and a record of having looked. ADR-0293 accepted the same cost.
- **Follow-ups / risks to watch.** Option 5 becomes buildable on top of this rather
  than instead of it: the confirmation endpoint is exactly what a recurring review
  process would call from a user task, which would give an installation a real audit
  trail without this area growing one. An SLA's own `reviewBy` — a date the *business*
  set, which is a different thing from a date Atlas recorded — is a separate later
  refinement and deliberately not folded in here.
- **The obvious way this fails:** somebody confirms without reading — and the quieter
  variant, somebody confirms without asking. Nothing can prevent either, and the API's
  own wording should say so. What the mechanism buys is that confirming is now a
  deliberate act with a name and a date attached rather than the absence of one, and
  that a confirmation nobody was asked for is visibly that: `confirmedWith` empty,
  `confirmedBy` equal to `updatedBy`, both on the read where somebody is already
  looking.

## Pros and cons of the options

### Option 1 — nothing
- Good: no field, no operation, no setting, no ninth finding.
- Bad: the only thing keeping the prose true is the memory of whoever wrote it, and
  that person is by definition not the one reading it two years later.
- Bad: it makes the map exactly the artefact the method warns about — a process
  landscape that documents rather than transforms, and is "likely to be detached from
  reality".

### Option 2 — a per-record forward deadline
- Good: a capability that genuinely needs a long horizon can say so, per record.
- Good: no installation-wide setting to argue about.
- Bad: a deadline is a promise the author sets freely. `2099-01` silences the check
  and still looks enforced — ADR-0293's argument, unchanged.
- Bad: closing that needs a second rule capping how far out a deadline may be, which
  is a rule to explain where the chosen form needs none.

### Option 3 — a confirmed date with a configured horizon *(chosen)*
- Good: a statement of fact, in the shape this repository already uses twice.
- Good: the horizon is one number in one visible place, so silencing the check is
  legible rather than hidden per record.
- Bad: a new field, operation and setting, for something that can only be dated and
  never verified.
- Bad: it departs from its two precedents by being configurable at all, and the
  hundred-year interval is a real hole.

### Option 4 — derive freshness from `updatedAt`
- Good: costs nothing. The field already exists on both records.
- Bad: and it is the wrong field. `updatedAt` moves when somebody fixes a typo in the
  summary, so the map would assert that owners and SLAs had been re-checked whenever
  anything at all was edited. That is the failure ADR-0289 names, made automatic — and
  an automatic lie is worse than a manual one, because there is no diff in which
  anybody could have noticed.

### Option 5 — model the review as a process in Atlas
- Good: it is what the engine is *for*, it produces a real audit trail, and it puts a
  named human task in front of the owner rather than a row in a report.
- Good: an organisation running the method already has the governance cadence this
  would formalise.
- Bad: it makes the registry tell the truth only after a customer has built and
  deployed something, so the map is stale by default until then.
- Bad: it puts a running instance in the path of a design-time read.
- Not actually a rival: it needs an endpoint that records a confirmation, which is
  what option 3 is. Built on top, it is the better answer; built instead, there is
  nothing for it to call.

## Links

- extends [ADR-0305](0305-business-capabilities-and-value-streams.md)
  — the records this dates, and the gap report it adds a finding to
- follows [ADR-0289](0289-worker-type-setup-in-the-panel.md) — the first `checked`
  date in this tree, and the argument against bumping one without re-reading
- follows [ADR-0293](0293-open-questions-in-records-expire.md) — the same shape
  generalised, and the argument for a backward-looking fact over a forward promise
- relates to [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) —
  a correlation surface rather than a log, which is why the note is one line
- described for readers in [`docs/architecture/business-architecture.md`](../architecture/business-architecture.md)
