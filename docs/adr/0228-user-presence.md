# ADR-0228: User presence in the Console

- **Status:** Proposed
- **Date:** 2026-09-02
- **Deciders:** Atlas maintainers

## Context and problem statement

The Organization page lists every account with a status pill that says `active` or
`disabled`. That is a property of the *record*: it answers "may this person sign in",
and it answers it identically for somebody who is looking at the screen right now and
for somebody who left the company's building in March.

The question an administrator actually arrives with is the other one. Before
restarting an instance: is anybody in the middle of something. While tracing a change
somebody just made: who is even here to have made it. When a task has to go to a
person rather than to a role: who is around. Atlas could answer none of it. The audit
log answers who *was* here, one login line at a time, which is a different question
asked in a much less convenient way.

The information exists and is thrown away. Enforcement resolves every request through
one in-memory session map (ADR-0044): a session already carries the user id, the
roles, and an expiry, and it already self-cleans on use. Nothing reads it except the
credential check.

Two things make this less trivial than "expose `lastSeen`".

**A request is not a person.** The Console polls on its own — the incident badge
every five seconds while Operations is open, follow-mode tables every two — and each
poll carries the session cookie. Presence derived from "when did a request last
arrive" says *online* for a tab that has been sitting behind a browser window since
Tuesday, which is the answer that makes the feature worthless: everyone is always
green.

**A session outlives the browser that opened it.** The TTL is twelve hours and
nothing tells the server that a laptop was closed. A person who shut their machine at
five is, by the session map alone, signed in until the small hours.

And the third thing, which is not a technical problem but decides the shape: this
is one small step from workplace monitoring. Who is at their desk, from when to when,
is exactly the data a works council asks about, and a feature that starts as a green
dot ends as an attendance report unless the record says where it stops.

## Decision drivers

- **Tell "at the keyboard" from "tab left open" from "gone".** A design that cannot
  distinguish the three is not worth building; the first two are the ones a single
  timestamp conflates.
- **Derived, never stored.** No record, no event, no line in a backup. What cannot be
  read back tomorrow cannot become an attendance history by accident, and a restart
  showing nobody present is then *correct* rather than a gap to explain.
- **Coarse on purpose.** Three states and no detail — never which page, which model,
  which action. The question is "is somebody there", and anything finer answers a
  question nobody asked and some people would rather not have answered.
- **Administrators only.** Same reach as the roster it annotates. Where a colleague is
  sitting is not something a task list needs to know.
- **Cheap enough to poll.** A Console page re-reading it every half minute must not
  touch disk, the run loop, or engine state — presence is not engine state and must
  not borrow its machinery (I3 stays untouched because the session map was never
  behind it).
- **Whatever opens a session is covered.** A federated login ends in the same session
  store as a password login (ADR-0210); presence must not be a property of one of them.

## Considered options

1. **One timestamp: when a request last carried this session.**
2. **A presence channel — WebSocket or SSE per open tab**, as the collaborative
   modeler already runs for one diagram (ADR-0140).
3. **Persist `lastSeenAt` on the user record.**
4. **Two timestamps on the in-memory session, one of them written only by the
   browser's own report.**

## Decision outcome

Chosen option: **4 — two timestamps, one beacon, three states, admin-only, nothing
persisted.**

A session gains `lastSeen` and `lastActive`:

- `lastSeen` is stamped by the session lookup that every request already performs.
  Any request counts, a poll included: it says the tab is still there.
- `lastActive` is stamped only by `POST /api/v1/auth/presence` with `{"active":true}`.
  The Console posts that beacon once a minute — running whether the tab is in front or
  behind, because a window behind another one is still signed in — and sets the flag
  only when a real pointer, key, scroll, touch or tab-focus happened since the last
  one. One boolean per minute; the beacon carries nothing about what was done.

`presenceWindow` is five minutes, for both halves, and the two are read in this order:

| | |
|---|---|
| `lastSeen` older than the window | **offline** — the browser stopped reporting, whatever the session says |
| otherwise, `lastActive` older than the window | **idle** — a tab left open |
| otherwise | **online** |

Connection is asked first on purpose: activity from a session that has since gone
silent describes a browser that is no longer there. One constant serves both because
a heartbeat that has missed five minutes has not been slowed down, it has stopped.

Two routes, both thin: `POST /api/v1/auth/presence` (any signed-in caller, stamps the
caller's own session, reports on nobody) and `GET /api/v1/users/presence` (admin, one
entry per account holding a live session). The roster itself carries the same
annotation so the page's first paint is already right. Presence of a *person*, not of
a browser: sessions are aggregated per user id, so somebody working in one tab is
online with three forgotten ones behind them.

### Consequences

- **Positive:** no new storage of any kind, so no migration, no backup content, no
  retention question, and no way to ask it about yesterday. It is one file of Go
  plus two timestamps on a struct that already existed. A federated login gets it
  for free. Logging out is instant, because the session is gone.
- **Positive:** the states are honest in the two cases that a naive version gets
  wrong — a polling tab reads as idle, and a closed laptop reads as offline while its
  session is still technically alive.
- **Negative / trade-offs accepted:** presence depends on the browser reporting, so a
  client that is not the Console — a script holding a session cookie — will read as
  idle however busy it is. API tokens and OAuth grants hold no session and are never
  present at all; they are machines, and presence is about people in the Console.
- **Negative:** the window is a constant, not a setting. A knob is what turns "is
  somebody there" into "how long was Meier away", and the coarse answer is the whole
  point.
- **Follow-ups / risks to watch:** the next request will be for history — "who was
  logged in yesterday". That is a different feature with a different consent
  conversation attached, and it must not arrive as an implementation detail of this
  one. The audit log already records logins for the operators who need that, with its
  own retention.

## Pros and cons of the options

### Option 1 — one timestamp
- Good: nothing to add anywhere; the lookup already runs on every request.
- Bad: the Console's own polling makes every open tab permanently online, which is the
  failure mode that makes the feature not worth having. It also cannot see a closed
  browser at all until the session expires hours later.

### Option 2 — a presence channel
- Good: precise, and disconnection is immediate and unambiguous.
- Bad: a long-lived connection per open tab, instance-wide, to carry one boolean.
  ADR-0140's registry exists because a diagram needs selections, locks and a roster
  streamed between a handful of people editing one thing; this is a green dot on an
  admin page. It would also make presence depend on the connection surviving proxies
  and idle timeouts, which is a support burden bought for nothing.

### Option 3 — persist it on the user record
- Good: survives a restart; the roster query needs nothing else.
- Bad: it is the attendance history this record is careful not to build — durable, in
  every backup, readable long after the fact. It writes to the user store on a
  schedule, for a value whose truth expires in minutes. And "survives a restart" is
  not a feature here: after a restart nobody *is* signed in, and reporting last
  night's presence would be false rather than useful.

### Option 4 — two timestamps and a beacon (chosen)
- Good: separates the two questions that a single timestamp conflates, at the cost of
  one field and one endpoint. Nothing durable, nothing to migrate, nothing to retain.
- Bad: needs a client that reports; a non-Console caller reads as idle. Accepted,
  because the Console is what the Organization page is describing.

## Links

- builds on [ADR-0044](0044-user-management-and-authentication-boundary.md) — sessions are in memory, and a restart really does end them
- relates to [ADR-0210](0210-federated-authentication.md) — a federated login ends in the same session store
- relates to [ADR-0140](0140-live-collaborative-modeling-sessions.md) — presence within one modeling session, and why that machinery is not this one
- relates to [ADR-0209](0209-roles-per-endpoint-group.md) — the admin role is what reaches the roster and now its annotation
