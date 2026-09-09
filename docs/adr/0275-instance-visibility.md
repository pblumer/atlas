# ADR-0275: Reading a running instance is an object question

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0209](0209-roles-per-endpoint-group.md) gave every `/api/v1` route a role.
[ADR-0071](0071-sharing-scopes.md) gave design-time artifacts a second axis: a
project's membership is inherited by the artifacts filed into it, so *which* draft
or form you may open is a question about the object, not about your role. The
audit's F09 extended that axis to deployment.

`GET /api/v1/instances/{key}/variables` has only the first axis, and deliberately
the weaker end of it. The route table said why:

> Every signed-in identity, not the operator role the rest of this group carries: a
> task form is prefilled from the variables of the instance the task belongs to, so
> a role narrower than "signed in" would hand a task worker an empty form. What
> this route needs is the *other* axis — may you see this instance — and that is
> open work (O-02), not something a role per endpoint group can express.

The reasoning is right and the conclusion was a placeholder. An external
architecture review signed in as an unrelated `user` account and read the
confidential runtime variables of somebody else's process instance. The same
endpoint answers for the MCP surface (the tool is a client of this route,
ADR-0196), so the reach is every integration too.

The endpoint is also asked for a *scope* key, not only an instance key: the Tasks
app prefills from the task's own element instance so a task inside a subprocess
fills from its own fields (ADR-0084). Both have to resolve, and both have to be
authorized.

## Decision drivers

- **A task worker must still be able to work.** The reason this route is open is
  real. Anything that closes it must leave the Tasks app working, including for a
  task nobody has claimed yet.
- **The narrower answer is the safer one.** A task worker needs the fields their
  form asks for. Handing them every variable the instance holds because one of
  them is on their form is how a review's comment box comes with the salary
  attached.
- **One rule, in one place.** The role gate is enforced at a single boundary; the
  object gate should be one function too, not a check copied into each handler.
- **No new store index on the read path.** The check must cost the instance, not
  the server.
- **Do not confirm what you will not show.** A caller with no relationship to an
  instance should not be able to use the endpoint to discover that the instance
  exists.

## Considered options

1. Raise the route to `operator`.
2. Grant by project membership only.
3. Grant by project membership, and give a task holder the fields their task's
   form asks for. (chosen)
4. Grant any signed-in caller who holds *any* task on the instance the whole
   instance.

## Decision outcome

Chosen option: **option 3**, as one function (`instanceAccessFor`) the handler
calls before it reads anything:

1. **Authentication off** — everything. Single-user mode is a single user.
2. **Operator or admin** — everything. They can already list every instance; this
   endpoint is not where that gets narrower.
3. **A member of the project the definition was deployed from** — everything.
   ADR-0071's inheritance, followed one step further: project → draft → deployment
   → the instances it runs. A deployment with *no* project grants nothing, because
   "no project" is not "everyone".
4. **Somebody holding a user task on the instance** — only the field keys that
   task's form asks for, collected from the stored form-js schema. A task with no
   form contributes nothing: there is no declared set of fields, and guessing one
   is how an allowlist becomes a formality.
5. **Anyone else** — 404, the same answer as "no such instance", so the endpoint
   is not an oracle for which keys exist. This is what `authorizeArtifact` already
   does for a draft.

Option 1 was rejected in the route comment before this record existed and is
rejected again: it locks out exactly the people the route is for. Option 2 is
option 1 with extra steps for a task worker who is not on the project. Option 4
was rejected on the second driver — holding one task is not a reason to read
everything the instance knows.

**The candidate-group rule is a decision this change had to make**, and it is the
part most worth arguing with. A BPMN candidate group is free text in the model, and
Atlas has never defined its relationship to an identity group; ADR-0042 listed
precisely this ("authorization once the server has identity, mutable candidate
groups") as a follow-up, and the Tasks app only ever used the attribute to say
"this is a group task". The rule adopted here is: an unclaimed task's candidate
group matches an identity group the caller belongs to, **by group name
(case-insensitively) or by group id**. It is the least surprising reading, and it
is what keeps a group task's form prefilling before anyone claims it. A claimed
task grants nothing to the group — once somebody holds it, it is theirs.

**Cost.** The task check walks the instance's own live element instances and asks
the existing `JobOfElement` index for each, so it costs the instance's token count
rather than the server's whole job population. It runs only for a caller who is
neither privileged nor a project member.

### Consequences

- **Positive:** an unrelated account gets 404 where it used to get another
  department's runtime data, on the HTTP API and on MCP alike, because MCP is a
  client of this route.
- **Positive:** a task worker sees their form's fields and nothing else, which is
  narrower than what they got before this change even though this change is what
  closed the endpoint.
- **Negative / trade-offs accepted:** a *formless* user task now grants its holder
  nothing through this endpoint. That is the fail-closed direction, and a task
  without a form has no declared fields to grant; a caller who needs those values
  is asking for an operator's view of the instance.
- **Negative:** the field allowlist is derived from the form-js schema, which
  couples this check to that schema's shape (`components[].key`, nested). A schema
  that will not parse contributes no fields, so the failure direction is closed.
- **Negative:** the candidate-group rule is new product behaviour, not a
  restatement of an existing one. A stricter installation may want claimed-only;
  that is one predicate, spelt out where it can be found.
- **Follow-ups / risks to watch:** `GET /api/v1/tasks/{key}` has the same shape of
  gap — any signed-in identity may read any user task — and is not addressed here.
  So do the other instance-scoped reads that carry the operator role today: they
  are gated, but by role rather than by relationship, so a member of one project
  with the operator role still sees every instance on the server. Both are the
  same axis and deserve the same treatment.

## Pros and cons of the options

### Option 1 — raise the route to operator
- Good: one line; consistent with the rest of the instance group.
- Bad: the Tasks app stops prefilling forms for exactly the people it is for.

### Option 2 — project membership only
- Good: pure ADR-0071 inheritance, nothing new to define.
- Bad: a task worker who is not on the project — the normal case for a shared
  business process — loses their form.

### Option 3 — project membership plus a task holder's form fields (chosen)
- Good: closes the leak, keeps the Tasks app working, and hands a task worker less
  than they had; one function, one place.
- Bad: needs a candidate-group rule Atlas had not defined, and couples to the form
  schema's shape.

### Option 4 — any task holder sees the whole instance
- Good: simplest rule that keeps the app working.
- Bad: one task on an instance becomes a key to everything the instance holds.

## Links

- extends [ADR-0071](0071-sharing-scopes.md) (membership is inherited by what the
  project contains) from artifacts to the instances a deployment runs
- relates to [ADR-0209](0209-roles-per-endpoint-group.md) (the role axis this is
  the second axis to)
- relates to [ADR-0042](0042-user-task-assignment-and-claim.md), whose follow-up
  list named this decision
- relates to [ADR-0180](0180-groups-as-members.md) (identity groups, which the
  candidate-group rule matches against)
- relates to [ADR-0196](0196-authenticated-mcp-transport.md) (MCP acts with the caller's
  credential, so it inherits this check)
- closes the code half of point O-02 in
  [`docs/compliance/isds-offene-punkte.md`](../compliance/isds-offene-punkte.md)
- reported as F11 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
