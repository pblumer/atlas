# ADR-0552: A calendar Worker Type finds free slots and books them on Microsoft 365, Google, CalDAV and Exchange Server

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-11-10
- **Deciders:** pblumer (maintainer; approval to implement on 2026-11-10)
- **Open question:** Whether Microsoft Graph and Google Calendar show an event to a
  free/busy read that starts after the event's create has returned to another client.
  The conflict rule below is safe only if they do. Neither provider's documentation
  that this record's author has read states it either way; a test against a real
  tenant has to answer it before the rule is relied on there.
- **Question checked:** 2026-11

## Context and problem statement

An appointment booking — a customer picks a service, sees the free slots of a shop,
picks one and books it — is a process atlas can almost model today: a public start
link ([ADR-0029](0029-public-process-start-links.md)), a public wizard that walks an
anonymous filler through the screens ([ADR-0449](0449-a-wizard-is-a-subprocess-one-filler-walks-through.md),
`examples/reisebuchung`), options from a decision for the list of services
([ADR-0511](0511-form-options-from-a-decision.md)), a confirmation mail, a cancel link
([ADR-0523](0523-user-task-answered-by-link.md)), and an application that travels
through atlas hub ([ADR-0499](0499-blueprint-registry-and-distribution.md)).

What it cannot do is the part that makes it a booking: **know which slots are free, and
take one without taking it twice.** atlas keeps no business data of its own, and no
Worker Type reads or writes a calendar. The Exchange Worker sets how a mailbox processes
meeting requests (`get-calendar-processing`, `set-calendar-processing`) but neither
reads busy times nor creates an event; nothing else in `connector/` mentions a calendar.

The calendar is where the staff of the organisation already keep their time. A person
blocks an afternoon in Outlook, a mechanic is on holiday, a room is taken by a meeting.
A booking that consults its own table instead of that calendar offers slots that are not
free. So the question this record answers is: **how does atlas read free slots from, and
book into, the calendars organisations actually use — and which ones?**

The calendars in common use by the organisations atlas is built for are, as far as this
record's author knows (no market figures were collected):

1. **Microsoft 365 / Exchange Online**, reached through Microsoft Graph.
2. **Google Workspace**, reached through the Google Calendar API.
3. **CalDAV servers** (RFC 4791) — Nextcloud, iCloud, Fastmail, Synology, SOGo,
   Radicale, and the calendars of hosting providers.
4. **Exchange Server on premises**, which Graph does not reach. Its programmatic calendar
   interface is Exchange Web Services (EWS). Microsoft announced the retirement of EWS
   *in Exchange Online* for October 2026; this record does not use EWS against Exchange
   Online and has not verified whether that retirement took effect as announced.

## Decision drivers

- **A Blueprint names one job type.** An application installed from atlas hub must run
  against whichever calendar the installing organisation has, without editing the model.
  The provider is a property of the installation, not of the process.
- **The calendar is the source of truth.** Busy is whatever the calendar says, including
  what people enter by hand. atlas must not keep a second record of availability that can
  disagree with it.
- **No double booking, with any number of Worker Instances.** Two customers choosing the
  same slot at the same moment must not both get it. The guarantee must not rest on a
  single Worker process holding a lock — that is a host-local singleton, which the
  scale-out checklist forbids as a cluster-wide source of truth.
- **At-least-once jobs.** A job can be delivered twice
  ([ADR-0538](0538-operations-declare-idempotency-and-effect.md)). A second delivery of a
  booking must not create a second event.
- **Data minimisation.** Finding free slots needs start and end of busy intervals, not
  subjects, attendees or bodies of other people's appointments.
- **Credentials stay on the Worker** ([ADR-0164](0164-no-in-process-service-tasks.md),
  [ADR-0168](0168-connector-work-on-a-worker.md),
  [ADR-0545](0545-installed-worker-instances-hold-their-own-credential.md)). The engine
  never holds a calendar credential.
- **The engine does not change.** The six invariants are not touched; everything here
  runs on a Worker.

## Considered options

1. **One Worker Type `calendar` with a backend per provider**, one operation contract
   for all four.
2. **One Worker Type per provider** (`graph-calendar`, `google-calendar`, `caldav`,
   `ews-calendar`), each with its own operations.
3. **The generic REST connector** against each provider's API, with the slot and conflict
   logic in FEEL and gateways.
4. **Microsoft Bookings** (Graph `bookingBusinesses`) as the booking backend.
5. **Bookings in a SQL table with a unique constraint** (the SQL Worker,
   [ADR-0173](0173-generic-sql-connector.md)), synchronised to the calendar.

## Decision outcome

Chosen: **option 1**, one Worker Type `calendar` whose configured Worker names its
provider, with the conflict rule, the operations and the phasing below.

### Why not the others, at their strongest

**Option 2** has real precedent: contacts are two Worker Types, `carddav` and
`googlecontacts`. Separate types keep each credential schema small, let each provider
release on its own cadence, and keep binaries small. But a process names a job type, so
four Worker Types mean four variants of every booking process, or an installation that
edits the model — exactly what a hub Blueprint must not need. Technical capability claims
([ADR-0480](0480-technical-capability-contract.md)) could one day let a model name a
capability instead of a type, but ADR-0483 explicitly makes no runtime selection
decision. Until one exists, portability needs one job type. The Exchange Worker is the
precedent for this shape: one operation table over two backends (`connector/exchange/backend.go`),
"which is what lets one model run against either".

**Is option 1 not the "universal directory Worker Type" ADR-0480 rejected?** That is the
strongest objection to it. ADR-0480 refused one directory type because a person, an
account and a contact are different resources, and a common surface would hide that and
invite unsafe substitution. The four calendar families do not differ in the resource:
each stores events with a start, an end, a transparency and recurrence on a calendar —
the RFC 5545 model Graph, Google, CalDAV and EWS all map onto. They differ in
constraints (which states count as busy, how consistent a read is, whether a service
account may invite). Constraints are what a contract pins and evidence checks, so the
answer is not to trust the common surface but to define it as technical capability
contracts and require evidence for every backend (below). Where a backend cannot meet a
contract, its claim stays unverified and the Console says so.

**Option 3** needs no new code in atlas and is what an integrator would try first. It
fails on the parts that are hard: recurring events, time zones and daylight-saving
transitions, the slot grid, and a conflict check that has to read, compare and delete in
one job. In FEEL across gateways, each of those is a model nobody can review, and the
conflict check spans several jobs, which removes the property it exists for.

**Option 4** is the strongest alternative for Microsoft-only customers. Bookings already
knows services, staff, durations, buffers and availability, and does its own conflict
check. It is Microsoft-only, needs a Bookings licence, and moves the opening hours and the
service list out of the application into a Microsoft admin page. It stays a possible
fifth backend later; it cannot be the design.

**Option 5** gives an atomic uniqueness check, which no calendar API offers. It also
makes atlas's table the source of truth, and a mechanic's holiday entered in Outlook then
does not block a slot. Synchronising both directions is a second system with its own
conflicts. It is the right design where there is no calendar; it is not the answer to
"book into the calendar".

### The operations

All times are RFC 3339 with offset; every computation that depends on wall-clock time
uses the IANA zone the job names (`timeZone`, default from the Worker's configuration).

| Operation | Does | Idempotency / effect (ADR-0538) |
|---|---|---|
| `find-free-slots` | Reads busy intervals of one or several calendars in `[from, to)` and returns the slots of a grid that fit | `natural` / `reversible` |
| `reserve-slot` | Creates a placeholder event for one slot, then applies the conflict rule; ends with the event id or the BPMN error `SLOT_TAKEN` | `key` (Graph, Google, CalDAV), `check` (EWS) / `reversible` |
| `confirm-booking` | Sets the final subject, body and — if configured — attendees on a reserved event | `natural` / `reversible` |
| `cancel-booking` | Deletes the event; an event already gone is success | `natural` / `reversible` |
| `get-booking` | Reads start, end and existence of an event the process booked | `natural` / `reversible` |

**`find-free-slots`** takes `calendars` (one or more; a slot is free if *any* of them is
free in it, which is "any free mechanic"), `from`, `to`, `durationMinutes`,
`gridMinutes`, `openingHours` (intervals per weekday, in local time), optional
`bufferMinutes`, `minLeadMinutes` and `maxResults`. Opening hours and service durations
are **inputs**, from a decision or from application values
([ADR-0453](0453-application-values-per-installation.md)), not something the calendar is
asked: a calendar knows busy and free, not when a shop is open. The grid is generated in
local wall time; a slot that does not exist on a daylight-saving day is skipped, and one
that exists twice is offered once.

It reads **free/busy only** where the provider has it — Graph `getSchedule`, Google
`freebusy.query`, CalDAV `free-busy-query` (RFC 4791 §7.10), EWS `GetUserAvailability` —
so no subject of anyone's appointment reaches the Worker. A CalDAV server without
free-busy support is read with a `calendar-query` time-range report and server-side
expansion of recurrences; the Worker keeps start, end and transparency of each instance
and discards the rest before anything leaves the operation. A CalDAV server that offers
neither is refused with that reason, not expanded client-side in the first version.

**`reserve-slot` and `confirm-booking` are two operations on purpose.** A reservation
that loses the conflict rule is deleted again. If it had invited the customer, the
customer would receive an invitation and a cancellation for a slot they never got. So the
reservation is a bare placeholder (a neutral subject such as the service name and a
booking reference, no attendees), and only `confirm-booking`, which runs after the rule
has kept it, adds what the installation configures.

### Whose calendar: a resource mailbox by default, every case possible

All three cases occur in practice, and the operations serve each without a mode:

| Case | `find-free-slots` | Booking |
|---|---|---|
| **One resource** — a workshop bay, a room, a counter. **The Blueprint's default.** | one calendar | `reserve-slot` on it |
| **Any of several people or equal resources** — "any free mechanic" | several calendars, `require: any`; each slot names the calendars free in it | `reserve-slot` on one of them; on `CALENDAR_SLOT_TAKEN` the process may try the next before it asks the customer again |
| **A person and a resource together** — an advisor and a fitting room | several calendars, `require: all` | one `reserve-slot` per calendar; if a later one is taken, the process cancels the earlier ones (compensation in the model) |

A resource mailbox is the default because it is what the organisation books against
already, it carries no personal appointments whose existence the Worker could reveal, and
on Microsoft 365 it can decline conflicts on its own as the second line described above.
Personal calendars need a Worker whose permission reaches each person's calendar, which
is a larger grant to justify.

### Idempotent creation

The job's `idempotencyKey` (ADR-0538) makes the placeholder unique per job:

- **Graph:** the event's `transactionId`, which Graph documents for exactly this — a
  retried POST with the same value does not create a second event.
- **Google:** a client-chosen event `id`, derived from the key in the base32hex alphabet
  the API requires; a second insert with the same id is refused, and the Worker reads the
  existing event instead.
- **CalDAV:** the key is the event's `UID` and its resource name, written with
  `If-None-Match: *`; a second PUT fails the precondition, and the Worker reads the
  existing resource.
- **EWS:** no idempotency token exists. The Worker writes the key into an extended
  property and searches for it before creating (`check`).

The same key marks the event as atlas's for the conflict rule and for `get-booking`.

### The conflict rule

No provider offers "create only if free" on an ordinary calendar. The rule is:

1. **Create** the placeholder (idempotent, above).
2. **Read** the busy intervals of the slot's calendar over the slot, excluding the
   placeholder itself.
3. If anything overlaps — another reservation, a booking, or anything a person entered —
   **delete the placeholder and throw `SLOT_TAKEN`.** Otherwise keep it.

The conflict check reads the slot itself, not the slot widened by the buffer
`find-free-slots` keeps: the buffer is a rule for what is offered, not a property of the
calendar another client's appointment could violate.

**Why at most one of two competing reservations survives.** Let A and B reserve
overlapping slots. Each one's read starts after its own create returned. Whichever read
starts later therefore starts after *both* creates returned. If a read that starts after a
create returned sees that event — the open question at the head of this record — the
later reader sees the other placeholder and deletes its own. So at most one remains, with
any number of Worker Instances and without a lock. Both may also see each other and both
yield; then neither customer gets the slot and both choose again. That costs a rare retry,
never a double booking, which is the right way round for a booking. A tie-break that let
one of them keep the slot (for example the smaller key) is **not** safe: when only one of
the two sees the other, the tie-break can keep both.

**Where the calendar can enforce it itself,** it is used as a second line, not instead:
an Exchange resource mailbox with automatic processing that declines conflicts (settable
through the existing `set-calendar-processing`) declines a conflicting meeting on its own.
It does so asynchronously, through the calendar attendant, so it cannot be the answer a
waiting wizard step gets.

`SLOT_TAKEN` (thrown as `CALENDAR_SLOT_TAKEN`) is a declared, catchable error code. The process catches it on the
reservation task and leads back to the screen where the slot is chosen, with the list
read again. (ADR-0483's first evidence record failed exactly where an operation ended a
known condition with a plain failure instead of a code a process can catch.)

### Credentials and scope

- **Microsoft Graph:** an app registration with the application permission
  `Calendars.ReadWrite`. Unrestricted, that permission reaches **every mailbox in the
  tenant**. The setup guide must restrict it to the booking calendars — Exchange Online's
  application access policies or RBAC for Applications. Which of the two Microsoft
  currently recommends is to be checked when the guide is written. A Worker that is not
  restricted says so in its connection check.
- **Google:** a service account the booking calendar is shared with ("make changes to
  events"). No domain-wide delegation for reading and creating. Adding attendees from a
  service account is refused without domain-wide delegation according to Google's
  documentation (not yet observed in a domain); `confirm-booking` with attendees on Google
  therefore either needs delegation (`subject` in the bundle) or the confirmation mail
  comes from atlas.
- **CalDAV:** user name and an app password over TLS, as the CardDAV Worker does
  (`connector/carddav`). As built, the CalDAV backend has its own small WebDAV client
  rather than the CardDAV one (see phase 3). The account reaches what the server lets it
  reach; scoping is the server's: a dedicated booking account with only the booking
  calendars shared to it.
- **EWS:** a service account with delegate access to the booking mailboxes. The first
  version supports what the Exchange Worker's on-premises backend already supports. As
  built, it supports NTLM and Basic but not Kerberos alone (see phase 4): the Exchange
  Worker reaches Kerberos through the pwsh it starts, this backend speaks HTTP itself.

All of it lives on the Worker (ADR-0545). The engine receives slots and an event id.

### Technical capability contracts, one profile, evidence for every backend

The operations above are written first as technical capability contracts
([ADR-0485](0485-technical-capability-semantics.md) envelope), then implemented:

| Contract | `resourceKind` | `idempotency` | `consistency` | Errors |
|---|---|---|---|---|
| `io.atlas.calendar.slot.find` | `calendar-slot` | `read-only` | `provider-defined` | `CALENDAR_NOT_FOUND` |
| `io.atlas.calendar.booking.reserve` | `calendar-booking` | `conditional` | `strong` | `SLOT_TAKEN`, `CALENDAR_NOT_FOUND`, `CALENDAR_NOT_WRITABLE` |
| `io.atlas.calendar.booking.confirm` | `calendar-booking` | `idempotent` | `provider-defined` | `BOOKING_NOT_FOUND`, `ATTENDEES_NOT_ALLOWED`, `CALENDAR_NOT_WRITABLE` |
| `io.atlas.calendar.booking.cancel` | `calendar-booking` | `idempotent` | `provider-defined` | `CALENDAR_NOT_FOUND`, `CALENDAR_NOT_WRITABLE` (a booking already gone is success) |
| `io.atlas.calendar.booking.get` | `calendar-booking` | `read-only` | `provider-defined` | `CALENDAR_NOT_FOUND` (absence is an output, `exists: false`) |

The contract codes are provider- and product-neutral. The Worker Type throws each as a
BPMN error under its own prefix — `SLOT_TAKEN` as `CALENDAR_SLOT_TAKEN`,
`BOOKING_NOT_FOUND` as `CALENDAR_BOOKING_NOT_FOUND`, and so on — when the task's process
catches it, and as a plain failure otherwise (ADR-0461); the suites map one to the other.

`identityScope` is `calendar` throughout. The contracts also pin what the backends would
otherwise answer differently: **busy** is every event that is not marked transparent or
free, tentative and out-of-office included, so that a backend never offers a slot another
would refuse; the slot list is bounded by `maxItems` in the output schema; and
`reserve`'s `strong` is the property the conflict rule needs (the open question at the
head of this record), stated as a requirement a backend must prove, not as a description
of every provider.

**One profile, not one per provider.** ADR-0480 refuses two claims of the same
`id@version` on one operation even with different profiles, because that would claim two
constraint sets at once. A single `reserve-slot` operation serving four backends can
therefore not claim `+microsoft-graph`, `+google-calendar`, `+caldav` and
`+exchange-ews` side by side. The contracts take one provider-neutral profile,
`free-busy-calendar`, which is what one job type for four backends means: every backend
meets the same constraint set. Relaxing ADR-0480's duplicate rule instead was considered
and not chosen — it would make "which profile does this job meet" depend on the
configured Worker, a runtime question ADR-0480 §5 leaves to a separate decision.

**Evidence covers every backend.** An evidence record (ADR-0483) binds a contract, one
operation and one suite, and its implementation digest covers every runtime path of the
Worker Type. The calendar suites therefore run each case against each backend's fake, and
a case counts as passed only if it passes on all of them. A backend added later reopens
every record. Until a record exists for a contract, the claims are `candidate`, as
ADR-0480's as-built claims are.

Two limits are stated, not hidden:

- The suites run against fakes. They prove that the Worker applies the conflict rule
  correctly *given* a provider that behaves as the fake does; they cannot prove that
  Graph or Google behave that way. That is answered by a live run per provider, recorded
  beside the evidence, not by it.
- ADR-0483's first contract is drafted but not published, because publication waits for
  the code owner's branch protection. The calendar contracts start the same way, in
  `workertype/evidence/testdata/contracts/`, and move to `workertype/contracts/`
  unchanged once publication is possible.

### Business capabilities

The booking application also states, in its bundle's architecture file
([ADR-0528](0528-application-bundle-architecture.md), source-tree format 4), which
**business** capability its process realizes ([ADR-0305](0305-business-capabilities-and-value-streams.md)),
for example `kundentermine-vereinbaren`, realized by the booking process (kind
`process`). On import, a key the organisation's map already has is linked; a missing one
arrives as a proposal, never as a silent write. The chain from strategy to provider is
then the one ADR-0480 §4 sketches: value stream → stage → business capability →
realized by the booking process → uses `reserve-slot` → claims
`io.atlas.calendar.booking.reserve`. The business capability is never inferred from the
operation, and the calendar Worker realizes no business capability itself: a calendar is
a means, not something the organisation can do.

### What travels where

- **The Worker Type** is a signed artifact atlas hub distributes
  ([ADR-0535](0535-worker-types-as-distributed-artifacts.md)). Its manifest declares each
  operation's idempotency and effect and its claims on the contracts above, profile
  `free-busy-calendar`.
- **The booking application** is a Blueprint that requires the Worker Type. Opening
  hours, grid, durations, the booking calendars and the shop's address are application
  values, so one release serves every installation.
- **A mock backend**, as the SQL Worker has ([ADR-0221](0221-sql-mock-mode.md)), so the
  example and the tests run without a tenant. Like that mock, it is faithful where it is
  cheap: a seeded busy interval blocks, and a concurrent reservation is resolved by the
  same rule.

### Phasing

0. The five contracts as drafts, and the conformance suites against the mock, before any
   provider backend: the contract is the specification the backends are written to.
1. Microsoft Graph and the mock, the operations above, and `examples/terminbuchung` with a
   test that books as an anonymous visitor of the public link.
2. Google Calendar.
3. CalDAV, tried against at least two servers (Nextcloud and one other).
4. EWS for Exchange Server.

Each phase also answers this record's open question for its provider, by test.

### As built: phase 0 (2026-11-10)

- `connector/calendar/catalog` — the operation table: the five operations, their bounds
  (window at most 62 days, at most 20 calendars, at most 1000 slots), their ADR-0538
  declarations and their claims under `free-busy-calendar`. It imports nothing of atlas's.
- `connector/calendar` — `Backend`, the provider's half; `slots.go`, the grid in local
  wall time, opening hours, closed dates, buffer, lead time, `require`, and the
  daylight-saving rule; `run.go`, `Job`, `Run`/`RunAt`, the checks, `reserve` (the
  conflict rule), the BPMN codes and `CreateKey`; `mock.go`, the in-memory backend
  that executes rather than answers. Tested, the two interleavings of the conflict rule
  included: one reservation between the other's create and read, and both reads before
  either gives up.
- `workertype/evidence/testdata/contracts/calendar-*.json` — the five contracts, drafted
  and not published.
- `workertype/evidence/suites/calendarbooking` — five suites, each running every case
  against every backend in `backends` (the mock today), registered with the gate and the
  evidence command. Every case passes except the two no fake can show: recurring
  appointments (the mock holds occurrences, not series) and read-after-write visibility
  (the open question). The suite's own tests replace the operation with variants that
  skip the conflict rule, ignore the create key, keep a slot by tie-break, or throw no
  codes, and show the cases fail them.

### As built: phase 1 (2026-11-10)

- **The task.** `<atlas:calendarConnector connector operation input resultVariable>` at
  reserved job type `io.atlas.calendar`, index 40 (`compiler/calendar_compile.go`). It
  carries one `input` attribute instead of one per value: a JSON object whose keys are the
  operation's contract input and whose values may be =expressions, compiled to one FEEL
  context at deploy (I5). The compiler refuses a key outside the operation's row and a
  missing required one, and checks the literal values it can judge (time zone, instants,
  bounds, weekdays). The model, the job and the suites thereby speak one shape, and a
  test holds the catalog's keys to the contracts' input schemas.
- **The engine half.** `connector/calendar.Resolve` evaluates the input; a null value is
  an absent one, and a whole number FEEL computed as 60.0 is taken as 60. The payload arm
  sends the resolved job's own fields, marshalled, so it cannot drift from the job.
- **Microsoft Graph** (`connector/calendar/graph.go`): a calendar is a mailbox, read
  through `calendarView` (recurring appointments expanded) with `$select` narrowed to
  `id,start,end,showAs,isCancelled,transactionId`, in UTC, every page followed up to 5000
  appointments and a window holding more refused, a `nextLink` outside the Graph root
  refused. A reservation carries the create key as `transactionId` and the backend looks
  for its own reservation before posting, because Graph does not say how long it remembers
  a `transactionId`. A 404 on an event is a missing booking only under Graph's
  `ErrorItemNotFound`; the suite caught the first version reading a missing mailbox as a
  missing booking. The codes are taken from Graph's documentation, not observed from a
  tenant. `connector/calendar/graphfake` stands in for Graph in tests and suites; it
  refuses a wide `$select` and does not filter a repeated `transactionId`.
- **Registration.** The built-in, supervised, worker-only kind `calendar`
  (`ATLAS_CALENDAR_*`), its manifest, Repository package, release row, candidate claims,
  Modeler panel (`calendar-ops.js`, held to the catalog), Worker dialog, vault shape,
  setup entry and handbook card. The bundle names the provider: `microsoft-graph` or
  `mock`.
- **Evidence.** With the Worker Type shipped, the record binding each operation to its
  drafted contract derives and verifies (`workertype/evidence/calendarrecord_test.go`);
  none is written to `workertype/evidence/records` while the contracts are unpublished.
  Every case passes on both backends except read-after-write visibility.
- **The example.** `examples/terminbuchung`, a public wizard, and
  `api/terminbuchung_example_test.go`, which books as an anonymous visitor and walks the
  case of a slot taken meanwhile. It needed a public step to be prefilled with the
  variable a choice field takes its options from (ADR-0449, amended 2026-11-10).

Not built in phase 1: Google Calendar, CalDAV and EWS (phases 2 to 4), a live run against
a tenant, and published contracts with written evidence records.

### As built: phase 2 (2026-11-10)

- **Google Calendar** (`connector/calendar/google.go`), provider `google-calendar`. The
  bundle is a service account's `clientEmail` and `privateKey`, and an optional
  `subject` for domain-wide delegation; a bundle field of another provider is refused for
  every provider, not ignored. A calendar is a Google calendar id shared with the service
  account.
- **Reading.** The events list with `singleEvents=true`, which expands recurring
  appointments (without it the list answers the series), `fields` narrowed to
  `id,status,transparency,start,end` and paged by `nextPageToken` up to 5000 events. An
  all-day appointment is read as its dates in the calendar's own zone, which the list
  answers; it blocks those days. Busy is every event not `transparent` and not `cancelled`.
- **Idempotent creation by the event id.** Google lets a client choose an event id in
  base32hex and refuses a second insert with an id it has seen, a deleted event's
  included. The id is `atlas` followed by the create key in hex, so any key is a valid id
  and the key reads back from the id. A repeated delivery that meets `409 duplicate` reads
  the event; when the conflict rule had given it up — it is then `cancelled` — it is
  confirmed again and the rule runs on it again. That is the same outcome the mock and
  Graph give, reached through the way Google keeps ids.
- **A missing calendar is told from a missing booking.** Google answers both on an event
  route with 404 (or 410 for a deleted event), so on such an answer the backend asks for
  the calendar (`GET /calendars/{id}?fields=id`) and reports whichever is missing.
- **Attendees.** `confirm-booking` writes with `sendUpdates=all`, which makes Google
  invite the attendees. A service account without delegation is refused attendees
  (`forbiddenForServiceAccounts`, from Google's documentation); the backend throws that as
  `CALENDAR_ATTENDEES_NOT_ALLOWED`, which the attendees case of the suites now exercises on
  a real backend instead of meeting it vacuously.
- **Evidence.** `connector/calendar/googlefake` stands in for the Calendar API; it refuses
  a wide `fields`, answers a series without `singleEvents`, refuses a repeated id and keeps
  a deleted event as cancelled. Every suite case passes on the mock, Graph and Google
  except read-after-write visibility, which no fake can show.

Not built: CalDAV and EWS (phases 3 and 4), a live run against a tenant or a domain, and
published contracts with written evidence records.

### As built: phase 3 (2026-11-11)

- **CalDAV** (`connector/calendar/caldav.go`, `ical.go`), provider `caldav`. The bundle
  is `username`, `password` and an optional `timeZone`; the Worker's endpoint is required
  and is the **calendar home** (`https://…/remote.php/dav/calendars/verkauf/` on
  Nextcloud), https unless it is loopback. A redirect to another host or down to http is
  refused, so the password never leaves the server it was written for. A calendar is a
  collection name below the home, or a full collection URL on the same host and scheme.
- **Reading.** One `REPORT` calendar-query with a time-range filter, `<C:expand>` so the
  server answers each occurrence of a recurring appointment, and calendar-data narrowed to
  UID, DTSTART, DTEND, DURATION, TRANSP, STATUS and RECURRENCE-ID — no titles or
  descriptions are read. A series that comes back unexpanded (RRULE or RDATE in the
  answer) is refused with that reason rather than read as its first occurrence. Times in
  UTC and with an IANA TZID are read as written; floating and all-day times are read in the
  bundle's `timeZone`, UTC when empty. Busy is every event not `TRANSPARENT` and not
  `CANCELLED`; at most 5000 events per window.
- **Idempotent creation by the resource name.** The event's UID is `atlas-` followed by
  the create key, its resource `atlas-<hex(key)>.ics`, written with `If-None-Match: *`. A
  repeated delivery that meets `412` reads the existing event; the conflict rule then runs
  on it as on the other backends. `confirm-booking` reads the event with its ETag, replaces
  only SUMMARY, DESCRIPTION and ATTENDEE and writes with `If-Match`, once more on `412`.
- **A missing calendar is told from a missing booking.** On a 404 or 410 for an event the
  backend asks for the collection (`PROPFIND`, depth 0) and reports whichever is missing.
- **Attendees.** Whether a server invites the attendees written into the event is its own
  scheduling (RFC 6638): by their documentation Nextcloud (with mail configured), iCloud
  and Fastmail do and Radicale does not; none of this has been observed yet. The backend
  writes them and does not throw `CALENDAR_ATTENDEES_NOT_ALLOWED`; a process that must
  be sure an invitation goes out sends the confirmation mail itself.
- **Deviation from this record.** The CardDAV client (`connector/carddav`) was not
  reused: it is unexported and shaped for address books (vCard, addressbook-query), and
  what calendars need of WebDAV here — one REPORT, PUT with preconditions, GET with ETag,
  DELETE and a depth-0 PROPFIND — is small. Two clients of that size are cheaper than an
  exported one both connectors must agree on.
- **Evidence.** `connector/calendar/caldavfake` stands in for a CalDAV server; it refuses
  a query without a time-range or one asking for SUMMARY or DESCRIPTION, expands a weekly
  series only when asked, and enforces both preconditions. Every suite case passes on the
  mock, Graph, Google and CalDAV except read-after-write visibility.

Not built: the phasing's "tried against at least two servers" — no run against Nextcloud
or another real server has been made, so server quirks (iCloud's discovery, servers that
ignore `expand`) are known only from their documentation. EWS (phase 4), live runs, and
published contracts with written evidence records remain.

### As built: phase 4 (2026-11-11)

- **Exchange Web Services** (`connector/calendar/ews.go`), provider `exchange-ews`, for
  Exchange Server 2016, 2019 and Subscription Edition on premises. The bundle is the
  service account's `username` (`DOMAIN\account` or a UPN) and `password`, and an
  optional `authentication`; the Worker's endpoint is required and is the server's EWS URL
  (`https://mail.contoso.ch/EWS/Exchange.asmx`), https unless it is loopback. Every
  request names schema `Exchange2013_SP1`, which all three versions accept, and works in
  UTC (`TimeZoneContext` UTC). A redirect is refused: EWS does not redirect a SOAP POST.
- **Delegate access, not impersonation.** A calendar is the SMTP address of a mailbox,
  addressed as its distinguished `calendar` folder with that mailbox. The service account
  needs Editor on each booking calendar (the Exchange Worker's `add-folder-permission`);
  to invite attendees also Send on Behalf or Send As (`grant-send-on-behalf`,
  `add-send-as`). Without either, Exchange answers `ErrorSendAsDenied`, which the backend
  throws as `CALENDAR_ATTENDEES_NOT_ALLOWED` — so the attendees case of the suites is
  exercised on EWS, not met vacuously. ApplicationImpersonation was not chosen: unless an
  administrator scopes it, it reaches every mailbox of the organization; delegate access
  reaches only the ones granted.
- **Authentication.** NTLMv2, inside Negotiate where the server offers it, through
  `github.com/Azure/go-ntlmssp` (already in the module graph through the LDAP client,
  now a direct dependency). The negotiator never sends the password as Basic, even to a
  server that asks for it; `"authentication": "basic"` must say so. Kerberos alone is
  refused at configuration: a Go client would need a keytab and a KDC, which is a
  separate decision. Extended Protection for Authentication, where a server enforces it
  for NTLM, is not implemented and would refuse this Worker — not observed, a risk to
  check on the first real server.
- **Reading.** `FindItem` with a `CalendarView`, which expands recurring appointments
  into their occurrences, and an `IdOnly` shape with Start, End, LegacyFreeBusyStatus,
  IsCancelled and the create key — no subject, body, organizer or attendees. Not
  `GetUserAvailability` as this record first said: availability answers no item ids, and
  the conflict rule must tell its own placeholder from the rest. Busy is every item not
  `Free` and not cancelled; a view holding more than 5000 items is refused.
- **Idempotent creation by search (`check`).** EWS has no idempotency token. The create
  key is written into a named extended property in a property set of atlas's own; before
  creating, the backend reads the slot and returns an item carrying the key. This is the
  same check Graph runs before posting. Two deliveries of one job running at the same
  moment could both create; the conflict rule then sees two items with the same key and
  keeps both, a duplicate placeholder for one customer, not a double booking of two. The
  window is a redelivery while the first delivery is still running.
- **Confirmation and cancellation.** `confirm-booking` reads the item, refuses a cancelled
  one, and sets subject, body and required attendees with `SendToAllAndSaveCopy` when
  there are attendees, `SendToNone` otherwise. `cancel-booking` moves the item to the
  mailbox's deleted items and sends attendees the cancellation. On `ErrorItemNotFound`
  the backend asks for the mailbox's calendar (`GetFolder`) and reports whichever is
  missing. Only the response code of a failure is kept, never its message text, which
  can quote addresses.
- **Evidence.** `connector/calendar/ewsfake` stands in for EWS. It authenticates every
  request with NTLMv2 and checks the client's response against the password (a wrong one
  is 401), refuses an item shape wider than the above, refuses a calendar item whose
  elements leave the schema's order, expands a series only in a calendar view, and keeps
  no idempotency of its own. Every suite case passes on the mock, Graph, Google, CalDAV
  and EWS except read-after-write visibility.

Not built: a run against a real Exchange Server; Kerberos; Exchange Online through EWS,
which this record leaves to Graph. The response codes and the element order are taken
from Microsoft's EWS reference, not observed.

### Consequences

- **Positive:** One booking process runs against the four calendar families without a
  change; the installation chooses. Availability stays where people already maintain it.
  No double booking without a lock, with any number of Worker Instances. The Worker never
  sees the content of other people's appointments where the provider has free/busy.
- **Negative / trade-offs accepted:** Four provider backends to maintain and test, each
  against an external API that changes on its owner's schedule. The conflict rule
  occasionally refuses both competing customers. A reservation that loses exists for a
  moment in the calendar, visible to anyone watching it. The safety of the rule depends on
  a read-after-write property that is not yet confirmed for Graph and Google.
- **Follow-ups / risks to watch:**
  - **The public wizard does not deliver a `valuesKey` variable.** A public step is
    prefilled only with the variables its fields bind (`api/publicwizard.go`,
    `formBindingsOf` in `api/publicstartfields.go`), so a choice field whose options come
    from the variable `find-free-slots` wrote shows no options there. Delivering the
    variables a field reads its options from — read, never accepted on submission — is a
    change to ADR-0449's prefill rule and needs its own amendment.
  - **Live slot lookup.** Asking for slots on every change of the date, as a form lookup
    (ADR-0473), would show fresher lists. Public forms refuse lookups today because a
    directory must not answer anonymous visitors; free slots are a different case and
    would need their own amendment of ADR-0473.
  - **Changes made in the calendar.** A booking moved or deleted in Outlook is not seen
    by the process except through `get-booking`. Push notifications (Graph subscriptions,
    Google watch channels, CalDAV sync tokens) correlated to the instance by event id are
    a separate record.
  - **Abuse of the public link.** A booking link blocks real capacity with every fake
    submission. The public rate limit is the only guard today; ADR-0029 left CAPTCHA-style
    mitigation as a follow-up, and a booking makes it more pressing.
  - **Capacity beyond one slot.** A service that needs several hours, or a resource and a
    person at the same time, is a question about the inputs of `find-free-slots` (several
    calendars, all free), deferred until the first version is in use.

### Scale-out assessment

- **State ownership:** none in atlas. The calendar owns availability and bookings; the
  process instance owns the event id it got back.
- **Partition routing, cross-partition communication, query consistency:** unaffected;
  the operations are ordinary jobs on an ordinary service task.
- **Durability and failover:** a Worker Instance that dies after creating a placeholder
  leaves the job to be leased again; the idempotency key finds the placeholder, and the
  rule runs again on it.
- **Idempotency:** per job, through the provider's own mechanism (above).
- **No host-local singleton:** the calendar is the serialisation point, not a Worker.
- **Migration seam:** nothing in standalone mode changes; a multi-node atlas runs the
  same Worker Type.

## Links

- builds on ADR-0203 (Worker Type, Worker, Worker Instance), ADR-0535 (distributed Worker
  Types), ADR-0538 (idempotency and effect), ADR-0545 (Worker credentials)
- serves ADR-0029 (public start links) and ADR-0449 (public wizard)
- technical capability contracts per ADR-0480, ADR-0483 and ADR-0485; business
  capabilities per ADR-0305 and ADR-0528
- packaged per ADR-0499 (atlas hub) and ADR-0453 (application values)
- alternatives: ADR-0173 (SQL Worker), the Exchange Worker's calendar processing
