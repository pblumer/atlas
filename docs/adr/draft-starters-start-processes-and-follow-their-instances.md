# ADR-DRAFT: A starter grant lets people start an application's processes and follow the instances they started

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-28
- **Deciders:** Atlas maintainers (requested 2026-10-28 by the maintainer: a process shared with a
  group may be started by its members, also from a wiki page; a person sees the instances they
  started, in a view of their own and below the start form, and opens one for its status)
- **Open question:** whether every process with a start form in an application its starters may use
  should be offered to them, or each process should be offered explicitly. This record offers all of
  them, because a process nobody should start directly has no start form or belongs in another
  application; no installation has yet said whether that holds for its estate.
- **Question checked:** 2026-10

## Context and problem statement

Starting an instance needs the role `operator`: `POST /api/v1/processes/{key}/instances` and
`POST /api/v1/instances` are declared `RoleOperator` (`api/openapi.go`), and the Tasks app's *Start*
view is offered to `operator` only (`api/web/app.js`). [ADR-0209](0209-roles-per-endpoint-group.md)
gives `operator` "start, cancel, terminate and repair instances; read runtime data", and
[ADR-0466](0466-start-view-groups-and-favourites.md) states that the people who start processes hold
`operator`. An operator also reads every variable of every instance (`instanceAccessFor`,
`api/instancescope.go`).

So a person who is to enter their holidays, report an incident or request an account through a start
form must be made an operator, and then reads every other person's holiday requests. Atlas has no
narrower way to let a signed-in person start a process under their own name:

- **Sharing** ([ADR-0071](0071-sharing-scopes.md), [ADR-0180](0180-groups-as-members.md)) grants users
  and groups `viewer`, `editor` or `owner` on an application. Its lowest role, `viewer`, already reads
  every instance of the application's processes ([ADR-0275](0275-instance-visibility.md)).
- **The catalogue** assigns a catalogue to people by their groups and lets the role `user` order
  ([ADR-0312](0312-portal-catalogue-order-inventory.md)). It is built for orderable products, their
  approvals and their inventory, not for every process with a form.
- **A public start link** ([ADR-0029](0029-public-process-start-links.md)) starts a process for anyone
  who has the link and records no initiator.

The engine records who started an instance: `ProcessInstanceValue.StartedBy`, the principal id, frozen
into the activation event ([ADR-0475](0475-identity-aware-worker-execution.md) §3). No route lists
instances by their starter, and no rule lets the starter read anything of an instance they started. A
person who entered their holidays cannot see whether the request is still open.

The question is how an application's owner lets a group start its processes without letting it read
or change anything else, and what a starter sees of what they started.

## Decision drivers

- Starting does not imply reading other people's instances, and does not imply design-time access.
- Who may use an application is managed where who may see and edit it already is: its sharing.
- An existing check must not widen by accident: a new grant fails closed everywhere it is not asked
  about.
- What a starter sends is bounded by the start form, as what an anonymous filler sends through a public
  link is ([ADR-0029](0029-public-process-start-links.md), `api/publicstartfields.go`).
- A process a catalogue product starts is started through its order.
- A starter can follow what they started without asking anybody.

## Considered options

1. **A `viewer` share lets its members start** the application's processes.
2. **A new grant `starter`** on an application's sharing, outside the rank of `viewer`, `editor` and
   `owner`.
3. **Starter groups per process in the model**, as an extension on the process element.
4. **Grant the role `operator`** to the people who start.
5. **A catalogue product for every process** people start.

## Decision outcome

Chosen option: **2**, because it is managed where an application's access already is, it grants
starting and following one's own instances and nothing else, and by construction it widens no check
that exists.

### The grant

- `starter` becomes a member role an owner may grant on a shared application, to a user or a group,
  beside `viewer` and `editor` (`isScopeRole`). It is not a rank: `scopeRank("starter")` stays 0, so
  every existing check, "any role" (`> 0`) and "at least viewer", excludes it unchanged. A starter sees
  no design-time artifact, no application listing and no instance data through the existing routes.
- A new `mayStart(application, principal)` reads the member list for a `starter` grant to the principal
  or to one of their groups, independently of `effectiveRole`, which keeps returning the highest rank.
  `viewer`, `editor` and `owner` do not imply it: roles do not nest (ADR-0209). An owner who wants to
  start grants it to themselves or a group they are in.
- A private application grants nothing to its members, starters included, as ADR-0071 has it.
- Operators and administrators start every process, as today.

### What a starter may start

- The newest deployed version of a process filed under the application, when it is executable and
  active, its start event names a start form, and the current catalogue release binds no product
  action to it ([ADR-0427](0427-converting-a-product-to-a-lifecycle-process.md),
  [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md)).
- `GET /api/v1/me/startable-processes` lists them with their application's name, grouped by
  application and sorted by name, for the *Start* view's grouping and favourites (ADR-0466). A starter sees the
  name of an application they may start in, and no other.

### Starting

- The two start routes take the role `user` and ask the object question in the handler. An operator
  or administrator passes as before. Anybody else is answered 403 unless `mayStart` holds for the
  definition's application and the process is startable as above. The listing of processes already
  names every deployed process to every account, so 403 reveals nothing.
- A starter's start variables are limited to what the start form can submit, by the check public links
  use; anything else is answered 400. Declared start variables the form does not ask for take their
  defaults ([ADR-0455](0455-start-variable-defaults.md)). Start variables are sealed as on every start
  ([ADR-0314](0314-portal-personal-data.md)).
- The start records `StartedBy` as it does today and answers the instance's key
  ([ADR-0335](0335-starting-an-instance-returns-its-key.md)).
- The CSV start, message triggers, cancel, terminate and repair stay `operator`.
- The Tasks app offers *Start* to `user`. A caller without `operator` sees the startable list; an
  operator keeps the full one.

### Following what one started

- A maintained index from `StartedBy` to instance keys, written by `applyToState` when an instance
  activates (a set under a unique key, I1, I4) and dropped with the instance's history.
- `GET /api/v1/me/started-instances` (role `user`, always the caller's own): newest first, paged with
  `?before=` and capped by a budget, filtered by `?process=` and `?state=active|finished`. Each row: the
  key, the process's name and version, when it started, its state (active, completed, terminated), the
  names of the steps it now waits at, and whether it is held up by an incident.
- The starter of an instance may read, as a task holder does under ADR-0275:
  - its progress, `GET /api/v1/instances/{key}/progress`
    ([ADR-draft-every-user-task-is-addressed](draft-every-user-task-is-addressed.md)): which steps are
    done and which are active, with no actor, no timestamp and no variable;
  - the variables the start form asks for, as they now stand, and nothing else.
- **In the Tasks app:**
  - A page *Started by me* (`#/tasks/started`, role `user`) lists them, with the filters above.
  - Below a start form in the *Start* view, the caller's instances of that process are listed with the
    same rows, collapsed when there are none.
  - Selecting an instance's key opens its details: state, started at, current steps, the model drawn
    with the progress marked, and the start form's fields read-only.

### Consequences

- **Positive:** an application's owner lets a team start its processes by sharing it with their group
  as starters, without making anybody an operator; a starter reads nothing of other people's instances
  and nothing at design time.
- **Positive:** a starter follows each request they made, in one list and beside the form they used,
  and the initiator the engine already records becomes visible to the person it names.
- **Positive:** the start routes ask an object question like the task commands do
  ([ADR-0317](0317-task-commands-are-an-object-question.md)), so another system acting with a person's
  credential is held to the same rule.
- **Negative / trade-offs accepted:** one more member role to explain beside `viewer` and `editor`, and
  one that is deliberately not a rank. A person who is a `viewer` of an application is not thereby a
  starter.
- **Negative:** a new maintained index on the hot path's activation event, and a store to back up with
  it.
- **Negative:** a starter may start as many instances as they like; no per-person start budget exists.
- **Follow-ups / risks to watch:**
  - offering processes one by one instead of all with a start form (the open question);
  - a start budget per person in `limits`;
  - making `StartedBy` available to assignment expressions, so a step can be addressed to whoever
    started the instance;
  - another system starting for a person, such as a wiki page with a start form: it needs atlas to
    accept that person's credential on these routes, which is a record of its own.

## Pros and cons of the options

### Option 1: a viewer may start

Strongest form: no new concept; whoever is allowed to see an application may use it, and the sharing
dialog stays as it is.

- Good: nothing new to learn or store.
- Bad: `viewer` reads every instance of the application (ADR-0275). Everyone who may enter their
  holidays would read everyone's holiday requests. Not adopted.

### Option 3: starter groups per process in the model

Strongest form: per process, versioned with the model, reviewable in the diagram, and the way other
BPMN engines have long expressed it.

- Good: as fine-grained as can be; travels with the model.
- Bad: changing who may start needs a new deployment and a new version; the groups of an installation
  would be written into models that move between installations; and an application's access would be
  managed in two places, its sharing and its models. It is the answer to the open question if
  application-wide turns out to be too coarse.

### Option 4: grant `operator`

Strongest form: works today, with no change at all.

- Good: immediate.
- Bad: an operator reads and can cancel, terminate and repair every instance on the server. For people
  who enter their holidays that is the opposite of least privilege.

### Option 5: a catalogue product per process

Strongest form: the catalogue already assigns offers by group, records the orderer, and shows the
orderer their orders.

- Good: built, with a portal of its own.
- Bad: a product carries an inventory, approvals, returns and a release cycle a holiday request or an
  incident report has no use for; making every startable process a product puts that weight on all of
  them. The catalogue stays the path for what is ordered.

## Links

- amends [ADR-0209](0209-roles-per-endpoint-group.md) (the start routes take `user` with an object question) and [ADR-0466](0466-start-view-groups-and-favourites.md) (the Start view is offered to `user`)
- extends [ADR-0071](0071-sharing-scopes.md) and [ADR-0180](0180-groups-as-members.md) (the `starter` grant) and [ADR-0275](0275-instance-visibility.md) (a starter reads the progress and start fields of their instances)
- relates to [ADR-0029](0029-public-process-start-links.md), [ADR-0312](0312-portal-catalogue-order-inventory.md), [ADR-0317](0317-task-commands-are-an-object-question.md), [ADR-0335](0335-starting-an-instance-returns-its-key.md), [ADR-0427](0427-converting-a-product-to-a-lifecycle-process.md), [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md), [ADR-0455](0455-start-variable-defaults.md), [ADR-0475](0475-identity-aware-worker-execution.md)
- relates to [ADR-draft-every-user-task-is-addressed](draft-every-user-task-is-addressed.md)
