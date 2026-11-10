# ADR-DRAFT: A calendar Worker Type finds free slots and books them on Microsoft 365, Google, CalDAV and Exchange Server

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-10
- **Deciders:** pblumer (maintainer)
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

`SLOT_TAKEN` is a declared, catchable error code. The process catches it on the
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
  service account is, to this record's author's knowledge, refused without domain-wide
  delegation; `confirm-booking` with attendees on Google therefore either needs
  delegation or the confirmation mail comes from atlas. To be verified.
- **CalDAV:** user name and an app password over TLS, as the CardDAV Worker does
  (`connector/carddav`), whose WebDAV client the CalDAV backend reuses.
- **EWS:** a service account with delegate access to the booking mailboxes. The first
  version supports what the Exchange Worker's on-premises backend already supports.

All of it lives on the Worker (ADR-0545). The engine receives slots and an event id.

### What travels where

- **The Worker Type** is a signed artifact atlas hub distributes
  ([ADR-0535](0535-worker-types-as-distributed-artifacts.md)). Its manifest declares each
  operation's idempotency and effect, and the technical capabilities it claims (ADR-0480),
  e.g. `io.atlas.calendar.slots.find` and `io.atlas.calendar.booking.reserve`, with one
  profile per provider (`microsoft-graph`, `google-calendar`, `caldav`, `exchange-ews`).
  Claiming them makes no conformance statement until evidence exists (ADR-0483).
- **The booking application** is a Blueprint that requires the Worker Type. Opening
  hours, grid, durations, the booking calendars and the shop's address are application
  values, so one release serves every installation.
- **A mock backend**, as the SQL Worker has ([ADR-0221](0221-sql-mock-mode.md)), so the
  example and the tests run without a tenant. Like that mock, it is faithful where it is
  cheap: a seeded busy interval blocks, and a concurrent reservation is resolved by the
  same rule.

### Phasing

1. Microsoft Graph and the mock, the operations above, and `examples/terminbuchung` with a
   test that books as an anonymous visitor of the public link.
2. Google Calendar.
3. CalDAV, tried against at least two servers (Nextcloud and one other).
4. EWS for Exchange Server.

Each phase also answers this record's open question for its provider, by test.

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
- capability claims per ADR-0480 and ADR-0483
- packaged per ADR-0499 (atlas hub) and ADR-0453 (application values)
- alternatives: ADR-0173 (SQL Worker), the Exchange Worker's calendar processing
