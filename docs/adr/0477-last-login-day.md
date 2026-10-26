# ADR-0477: The roster says on which day an account last signed in

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-23
- **Deciders:** Atlas maintainers

## Context and problem statement

The Organization page lists every account with two columns about it: **Status**, which
says whether the account may sign in, and **Presence**
([ADR-0228](0228-user-presence.md)), which says whether somebody is using it this
minute. Neither answers the question an administrator brings to that page most often:
*which of these accounts is anybody still using?*

On an installation with a few dozen accounts the roster reads as a wall of `active` and
`offline`. An account created for a test in spring, one belonging to somebody who left
the project, and one used every morning look identical. That is an operational gap
and a security one: an account nobody uses still accepts a password, still holds its
roles, and is exactly the account whose compromise nobody would notice. Finding it
today means reading the audit log (ADR-0197), which is a log stream shipped elsewhere,
one line per login, and not something the Console can ask.

ADR-0228 saw this request coming and said where it must not come from: "the next
request will be for history — who was logged in yesterday. That is a different feature
with a different consent conversation attached, and it must not arrive as an
implementation detail of this one." Presence is derived, never stored, and gone after a
restart, by design. The question here needs a fact that survives a restart, so it needs
its own decision, and the decision has to say why it is not the attendance record that
ADR-0228 refused to build.

## Decision drivers

- **Answer "is this account still in use?"** — reliably, across restarts, for every
  way of signing in (password and federated, ADR-0210).
- **Not an attendance record.** Nothing stored may answer when a person arrives, how
  long they stay, or on which days they were present. That is the data a works council
  asks about, and under the data-minimisation principle of the Swiss FADP and the GDPR
  what a purpose does not need is not to be kept.
- **The login never waits for the run loop** (ADR-0265). Account records are written
  on the loop; signing in must not be.
- **No lost edits.** A write caused by a login must never carry an older copy of the
  account over an administrator's change.
- **Administrators only**, like the roster it annotates.

## Considered options

What is stored:

1. **Read the answer out of the audit log.**
2. **Persist the instant of the last login.**
3. **Persist the UTC calendar day of the last login**, overwritten at each login.
4. **Persist every login** (a sign-in history per account).

Where it is written:

- A. **On the account record, by a writer on the run loop after the login answered.**
- B. **In a store of its own beside the account, by the login itself before it
  answers.**

## Decision outcome

Chosen: **option 3, written as B — one record per account holding the UTC calendar day
of its most recent successful login, overwritten and never appended to, in a store
`last-logins` beside the account record.**

What is stored, and what is not:

- **A day, not an instant.** "2026-10-23", never "2026-10-23 07:42". A day answers
  whether an account is in use completely; the time of day adds only when somebody
  starts work. For an account that is used daily, the instant of its last login *is*
  that morning's arrival time, and an administrator who reads the roster each day would
  be reading a time clock.
- **One value, not a list.** The next login replaces it. Nothing can be read back about
  any earlier day.
- **Successful logins only.** A refused attempt — wrong password, disabled account —
  is not somebody using the account and must not make a guessed-at account look alive.
- **Every login, whatever opened it.** The password login and the federated callback
  both record it; they are the two places a session is created.
- **A UTC day.** Atlas has no instance-wide time zone; every person chooses their own
  (ADR-0459). A day counted in the server's zone or the reader's would mean different
  things to different readers. Near midnight a login lands on the neighbouring day,
  which does not change whether an account is in use. The Console counts the age
  ("today", "3 days ago") against today's UTC day too, so the age is right for every
  reader.

How it is written:

- **By the login, before it answers, off the run loop.** Account records are written on
  the loop, and a login must not wait for it (ADR-0265). So the day does not go on the
  account record: it goes into its own store, one small record per account, which the
  login writes itself after the session exists and before the response. When the login
  has answered, its day is on disk.
- **One writer, its own mutex.** Writes to the store are serialised by a mutex of its
  own, as the session store's are. Two browsers of one person can sign in at the same
  moment, and the sidecar writes a record through a temp file whose name is fixed per
  key. This is an exception to ADR-0265's "writes do not move", and a narrow one: no
  invariant spans this store and the account store except "no day for an account that
  is gone", and the mutex keeps that (below).
- **Cheap in the common case.** A record that already says today is read and left
  alone, without the lock: a person who signs in five times a day costs one write.
- **The day never moves backwards**, so two logins either side of midnight leave the
  later one whichever is written last.
- **The account record is not touched**, nor its `UpdatedAt`: signing in is using an
  account, not changing it.
- **No day for a deleted account.** Deleting an account deletes its day after the
  account; writing a day first checks, under the same mutex, that the account exists.
  A login that read the account just before a delete therefore finds it gone and writes
  nothing. Whatever is left anyway — a delete whose second step failed, an account file
  removed by hand — is pruned at startup, before anybody can sign in.
- **Failures do not fail the login.** A day that cannot be written is logged as
  `auth.login_date_unrecorded`; the next login writes it again. A day that cannot be
  read leaves that row of the roster empty and is logged; it does not take the page down.

The store is registered as identity data (ADR-0282): it travels with the accounts it
describes in a whole-instance snapshot.

The Console shows a **Last login** column between Status and Presence, with the age and
the day, and sorts it so the accounts nobody uses come first. The day is on the admin
roster (`GET /api/v1/users`, `lastLoginDate`) and nowhere else.

### Consequences

- **Positive:** the roster finally separates the account used this morning from the one
  nobody has opened since spring, and an administrator can sort for the latter. It
  survives restarts, travels with the accounts in a snapshot, and is deleted with the
  account.
- **Positive:** nothing is left to happen after the login has answered. The account
  record, every check that nothing changed in it, and every edit of it are exactly as
  they were.
- **Negative:** a new store, written off the run loop by its own mutex — an exception to
  ADR-0265's rule that design-time writes stay on the loop, argued above. The first
  login of the day pays for one small fsynced write.
- **Negative / trade-offs accepted:** this *is* stored personal data about use, where
  presence stored nothing. It is the smallest fact that answers the question: one day per
  account, overwritten. It is in backups for as long as the account is.
- **Negative:** accounts signed in to before this shipped show no day until their next
  login, and the Console says "no login recorded" rather than inventing one. An account
  used only through an API token or an OAuth grant (an AI client, a script) never signs
  in and reads the same way, although its credential may be in daily use. Telling those
  apart needs a "last used" on tokens and grants, which is a separate decision.
- **Negative:** a write that fails (an unwritable directory, a full disk) is logged as
  `auth.login_date_unrecorded` and not retried; the account's next login writes it again.
- **Follow-ups / risks to watch:** if somebody asks for the time of day, or for a
  history, that is option 2 or 4, and it is the consent conversation ADR-0228 asked for —
  not a widening of this field. A threshold that marks an account as dormant (90 days?)
  is policy; this record leaves it to the reader and the sort.

## Pros and cons of the options

### Option 1 — read the audit log
- Good: nothing new is stored at all; the audit log already has every login.
- Bad: the audit log is a log stream (ADR-0197), shipped to wherever an operator ships
  logs, with that system's retention. Atlas cannot query it, and on an installation that
  ships no logs it does not exist.

### Option 2 — the instant of the last login
- Good: the most precise answer; what most identity products store.
- Bad: for a daily user it is that day's arrival time, readable by every administrator
  every day and kept in every backup — the attendance record ADR-0228 refused to build,
  by a side door. The purpose does not need it, and the audit log keeps the exact instant
  for the operators whose job is to read it.

### Option 3 — the UTC day of the last login (chosen)
- Good: answers the question completely and nothing beyond it; one write per account
  per day at most.
- Bad: a login just after local midnight is counted on the previous UTC day; accounts
  used only through tokens read as unused.

### Option 4 — every login
- Good: answers every question anybody could ask, including "how often".
- Bad: it is an attendance history, with the retention and consent questions that come
  with one, and an unbounded store. Nobody asked for it.

### A — on the account record, written later on the run loop
- Good: no new store; the day travels inside the account record.
- Bad: tried first, and the full test sweep showed why not. The write lands at a moment
  nobody chose, after the login has answered: over a check that a reporting directory
  run wrote nothing, over a record a test had just removed (bringing it back), over one
  it had just corrupted (repairing it). In production the same timing means a login can
  rewrite an account record after an administrator's edit and needs a goroutine, a flush
  on shutdown and a re-read to avoid undoing that edit. Every one of those is a
  consequence of putting a fact about use on the record that describes the account.

### B — beside the account, written by the login (chosen)
- Good: when the login has answered, the day is on disk and nothing else is pending; the
  account record is never touched by a login.
- Bad: a second store, and a mutex of its own outside the run loop.

## Links

- relates to [ADR-0228](0228-user-presence.md) — presence, and the history it said must
  not arrive as an implementation detail
- amends [ADR-0265](0265-login-off-the-run-loop.md) — signing in does not wait for the
  run loop; this store is written off it, by its own mutex
- relates to [ADR-0282](0282-store-registry.md) — the store is classified as identity data
- relates to [ADR-0197](0197-login-throttle-and-audit-log.md) — the audit log keeps the
  exact instant of every login
- relates to [ADR-0210](0210-federated-authentication.md) — a federated login is a login
- relates to [ADR-0459](0459-display-preferences-per-user.md) — why the day is a UTC day
- builds on [ADR-0044](0044-user-management-and-authentication-boundary.md) — accounts
  are a durable sidecar written on the run loop
