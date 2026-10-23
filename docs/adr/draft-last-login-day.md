# ADR-DRAFT: The roster says on which day an account last signed in

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

1. **Read the answer out of the audit log.**
2. **Persist the instant of the last login** on the account record.
3. **Persist the UTC calendar day of the last login** on the account record,
   overwritten at each login.
4. **Persist every login** (a sign-in history per account).

## Decision outcome

Chosen: **option 3 — one field, `lastLoginDate`, holding the UTC calendar day of the
account's most recent successful login, overwritten and never appended to.**

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

How it is written without breaking ADR-0265:

- The login leaves `(user id → day)` in an in-memory map and returns. A writer
  goroutine moves the map onto the account records in one run-loop turn. The map has one
  entry per account, so a burst of logins is one write per account however long the
  loop is busy, and it cannot grow beyond the roster.
- An account whose record already says today is not noted at all: a person who signs in
  five times a day costs one write, and the common case costs no loop turn.
- In that turn each record is **read again** and only `lastLoginDate` changes. An edit
  an administrator made after the login read the account (disabling it, say) is never
  overwritten. An account deleted in between is not recreated. The day never moves
  backwards.
- `UpdatedAt` is not touched: it says when the account was *changed*, and signing in is
  using it.
- `Close` writes whatever the writer had not reached, after the loop has stopped, so a
  login just before a shutdown is not lost.

The Console shows a **Last login** column between Status and Presence, with the age and
the day, and sorts it so the accounts nobody uses come first. The field is on the public
projection of an account, which only an administrator or the account itself ever
receives.

### Consequences

- **Positive:** the roster finally separates the account used this morning from the one
  nobody has opened since spring, and an administrator can sort for the latter. It
  survives restarts, travels with the account in a backup, and is deleted with it.
- **Positive:** no new store, no new route, no new record type. One field on a record
  that already existed, written by the same single writer as every other account change.
- **Negative / trade-offs accepted:** this *is* stored personal data about use, where
  presence stored nothing. It is the smallest fact that answers the question: one day per
  account, overwritten. It is in backups for as long as the account is.
- **Negative:** accounts signed in to before this shipped show no day until their next
  login, and the Console says "no login recorded" rather than inventing one. An account
  used only through an API token or an OAuth grant (an AI client, a script) never signs
  in and reads the same way, although its credential may be in daily use. Telling those
  apart needs a "last used" on tokens and grants, which is a separate decision.
- **Negative:** a write that fails (an unreadable record, a full disk) is logged as
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

## Links

- relates to [ADR-0228](0228-user-presence.md) — presence, and the history it said must
  not arrive as an implementation detail
- relates to [ADR-0265](0265-login-off-the-run-loop.md) — signing in does not wait for
  the run loop
- relates to [ADR-0197](0197-login-throttle-and-audit-log.md) — the audit log keeps the
  exact instant of every login
- relates to [ADR-0210](0210-federated-authentication.md) — a federated login is a login
- relates to [ADR-0459](0459-display-preferences-per-user.md) — why the day is a UTC day
- builds on [ADR-0044](0044-user-management-and-authentication-boundary.md) — accounts
  are a durable sidecar written on the run loop
