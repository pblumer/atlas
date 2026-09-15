# ADR-0311: The approver decides on a page of their own, in the customer's colours

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers
- **Open question:** Whether an approver should be able to ask a question back — "what
  is this for?" — without refusing. Today the two answers are approve and refuse, and a
  refusal with a reason is the only way to say anything at all, which turns a question
  into a rejection the orderer has to re-place. A third outcome is a change to the
  approval models and to the order's line statuses, not to this page.
- **Question checked:** 2026-09

## Context and problem statement

An approval is a user task, and the Console can work one. For most of the people who
receive one, the Console is the wrong place to be.

The three approval kinds a catalogue can bind describe three different populations. A
*fixed* approver is an integration manager, permanently responsible for a service and
plausibly a Console user. A *role* approver is whoever holds a named group. A *superior*
approver is the line manager the directory resolved for whoever ordered — an ordinary
employee who will approve perhaps four times a year, and for whom a tool whose
navigation reads Deployments, Instances, Incidents and Workers is a tool they will ask a
colleague to operate for them.

Branding that tool in the customer's colours does not fix it. It is still the operator's
instrument, it still shows every other task on the instance, and its chrome belongs to
the organisation running the server rather than to the customer whose order is waiting.

There is also something the Console cannot show. An approval names an order, and the
order names a release, and the release names a catalogue — and the approver may walk
none of those links. They are not the orderer, so the order is not theirs to read; they
are not the catalogue's audience, so the catalogue is not theirs either. In the Console
an approval therefore reads as `vpn` for `usr_3f2a`, which is an id and a stranger.

## Decision drivers

- The decision should look like the customer it is being taken for. An approver holding
  requests from three customer groups should be able to tell them apart without reading.
- None of the refusals above should be relaxed. A customer's brand is not shown to other
  customers; that is what the catalogue's own logo gate exists for.
- A person who approves four times a year should need no training and should see nothing
  they cannot act on.
- What a decision *means* stays in the model. A page that wrote the order itself would
  be a second implementation of the approval process.

## Considered options

1. **Brand the Console's task pane** — the task detail carries the catalogue's accent,
   typeface and mark, scoped to that panel, inside a Console that stays the operator's.
2. **A page of its own**, reached from the notification, carrying the brand of the order
   it is deciding.
3. **Both**, the pane first.

## Decision outcome

Chosen option: **a page of its own** — `api/web/genehmigung.html` and `genehmigung.js`,
reading `GET /api/v1/approvals`.

Option 1 was the cheaper half and is the one a *fixed* approver would notice least:
they are in the Console anyway. It does nothing for the case that actually needs it,
which is the line manager, and it would have put a catalogue's brand inside the
operator's own instrument — the one place [ADR-0113](0113-org-wide-ui-theme.md)'s
reasoning still holds unchanged.

### The brand follows the decision, not the visitor

The portal resolves a brand from *who you are*: a visitor's catalogue is a fact about
their groups, so it is cached and painted before the first frame. This page resolves it
from *what you are working on*. An approver holds requests from several customer groups
at once and there is no one brand for that list; the brand belongs to the decision.

So nothing is cached here, deliberately, and a test holds that. A cached paint would
open the page in the colours of whoever was approved last — telling an approver, in
colour, that this request belongs to a customer it does not.

A list with nothing open carries the operator's brand, which is the honest answer: no
decision is open, so no customer is being decided for.

### The join happens on the server

`GET /api/v1/approvals` answers everything the page shows: the tasks addressed to the
caller, the order line each decides, the product named as the release froze it, and the
catalogue's brand. It does the walk the approver may not do, under the one right they
actually hold, and opens nothing else — the catalogue stays 404 to them, which a test
asserts alongside the brand arriving.

The mark travels the same way, from `GET /api/v1/approvals/{key}/logo` rather than from
the catalogue's own route. Widening the catalogue's would hand one customer's mark to
everybody who ever holds a task.

What makes a task an approval is not its name or its process id but the order: the
instance carries an `orderId` and an `itemId`, the order has that line, and the line
says this process is what decides it. That is general on purpose — an installation
approving through its own model binds a product to it by name and lands here unchanged.

### Deciding is completing the task

The page posts the answer to `POST /api/v1/tasks/{key}/complete` and writes no order.
What a decision means — start provisioning, or record the refusal and tell the orderer —
is modelled in the approval process. A page that also wrote it would be a second answer
to one question, and the two would eventually disagree.

That endpoint had to be closed first: before
[ADR-0317](0317-task-commands-are-an-object-question.md),
any signed-in account could complete any open task, which for a portal customer meant
approving their own order.

### Consequences

- **Positive:** The approver sees what was ordered, for whom, in the customer's colours,
  with two buttons and no training. No new authority is granted to anybody: the page
  reads what the task already entitles its holder to.
- **Negative / trade-offs accepted:** A second page to keep in step with the portal —
  the locale machinery, the typeface mirror and the mark cascade are now stated twice in
  JavaScript, held in step by tests rather than by sharing. The listing walks the open
  tasks and is paged like the inbox; an approver on an instance with a task flood sees a
  "there are more" line rather than a complete list.
- **Follow-ups / risks to watch:** The open question above. The notification names the
  ordered product by id rather than by name: the approval process holds an `itemId` and
  the release that knows the word for it is not something the process reads. The page
  shows the name; the mail does not. Resolving it would mean either the orchestrator
  passing the text in or the mail reaching back for it, and neither is obviously right.

## Implementation

`api/approvals.go` holds both routes and the join; `api/web/genehmigung.{html,js}` is the
page. Eight tests hold the page's properties (catalogue completeness, no palette of its
own, the typeface mirror, the mark cascade, no cached brand, deciding through the
process) and two hold the endpoints end to end: an approver sees their request with the
catalogue's texts and theme while the catalogue itself stays 404 to them, and the mark
arrives under the task's gate while the catalogue's own logo route refuses them.

**Three defects in the fulfilment slice were found while building this, because nothing
had ever run it end to end.** Two are fixed here or beside it: the approval process id
was assembled by string concatenation and named nothing deployed, and
`POST /api/v1/instances` — which every provisioning run and every approval start posts
to — did not exist, because Atlas could only start an instance by *definition key* and a
model knows an id. `TestEverySystemProcessCallsARouteThatExists` now walks every REST
call in every system process against the route table, which is what should have caught
both.

The third was an engine gap rather than a wiring mistake, and it is fixed in
[ADR-0318](0318-user-task-assignment-expressions.md):
the three shipped models address their task with `assignee="=approvalRef"`, and Atlas
interned the model's string verbatim, so the task was assigned to the literal
`=approvalRef` and no person held it. A user task's assignment is now evaluated at
activation and frozen into the job, so all three reach their approver — a test starts
each of the two that can be started without a directory and checks who holds the task.

### The notification, and the link it carries

The page is reached from a message, and the message is sent by the approval process —
not by the server. Which approver is told, and what the message says, is part of what an
approval *is*, and an installation that replaces the shipped approval models replaces
its notification with them. A server that sent it instead would be a second author of
the same act.

It runs **beside** the task rather than in front of it, on a parallel branch. In front,
an unconfigured mail worker would hold the token and there would be no approval at all.
An approval nobody was told about is worse than an approval nobody was told about
*existing* — the approver still finds it in their list — so the notification may fail
without taking the decision with it. What it costs is that the mail is written in the
same drive as the task activation: a task whose assignment then fails to resolve leaves
a message about an approval that parked. The operator sees the incident.

**The link names the order line, not the task.** A task key does not exist when the
message is written, and it changes when a task is reassigned or retried; an order and a
product are what the approver was told about and they are stable. So the page takes
`?order=&item=` and finds the approval in the list it fetches anyway. A link naming an
approval the reader does not hold — decided already, or never theirs — falls back to
their list with a line saying so, because there is nothing else they can do about it.

**The recipient is a reference, not an address.** The model writes `to="=approvalRef"` —
the person or group the *product* named — and the server resolves it in the account at
send time, through `mail.Directory`. That is
[ADR-0314](0314-portal-personal-data.md)'s rule applied to a
message instead of a screen: no mail address enters a process variable, an order or the
event log. The residue is the resolved job and the SMTP conversation, which is the least
any mail can be sent with.

The four spellings the directory accepts — username, principal id, group id, group name
— are deliberately the four [ADR-0042](0042-user-task-assignment-and-claim.md)'s
`holdsTask` accepts. The people told and the people who may act have to be the same set,
or the message reaches somebody who can do nothing with it.

**The origin comes from the operator's configuration and nothing else.** `portalBaseUrl`
is `--external-url` (ADR-0200), carried into the order's fulfilment as a start variable
and on into the approval. Deriving it from whichever host the orderer happened to reach
would put an internal address into a mail to somebody who cannot resolve it. Unset, it
is an empty string rather than absent, and the model says where to go instead of
printing a link nobody can follow.

## Links

- needs [ADR-0317](0317-task-commands-are-an-object-question.md) — an approval a customer can grant themselves is not an approval
- brands from [ADR-0316](0316-portal-theme-per-catalogue.md) — the same accent, typeface and mark, resolved from the order instead of the visitor
- decides the orders of [ADR-0312](0312-portal-catalogue-order-inventory.md)
- keeps [ADR-0113](0113-org-wide-ui-theme.md) untouched — the Console stays the operator's
- uses [ADR-0042](0042-user-task-assignment-and-claim.md) — who holds a task, and the four spellings the notification resolves
- honours [ADR-0314](0314-portal-personal-data.md) — a recipient is a reference, resolved at send time
- extends [ADR-0079](0079-outbound-mail-connector.md) — a mail recipient may be somebody this server knows rather than an address
- needs [ADR-0200](0200-mcp-oauth-resource-server.md)'s configured origin — a link has to be one somebody else can follow
- needs [ADR-0318](0318-user-task-assignment-expressions.md) — without it no shipped approval reaches an approver
