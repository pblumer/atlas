# ADR-0123: A sanctioned automated user-provisioning path for system processes

- **Status:** Accepted
- **Date:** 2026-08-13
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0122 gave Atlas a protected system project holding its own operating
processes — user intake, access review, offboarding. Those processes deliberately
stop short of the privileged act: the intake process *coordinates* onboarding but
a human admin still creates the account by hand, because user management is
admin-gated (ADR-0044) and the automation identity intentionally cannot manage
users (ADR-0049). Passwords are mailed by the sanctioned mail connector; the
account itself is a manual step.

We now want to close that last gap for the platform processes only: let the
intake process **create the Atlas login itself**, and let offboarding **disable
it** and password-reset **set a new password** — automatically, with the mail
already automated. This is what "Atlas verwaltet seine eigenen Benutzer per
Prozess" ultimately means.

Doing so deliberately reopens the boundary ADR-0044/0049 drew. The question is
**how** to grant a running process the ability to mutate the user store without
recreating the footguns those ADRs closed: no broad admin capability handed to
arbitrary models, no secret in a model, no way for a tenant's own process to
provision accounts, no un-audited privileged writes, and no way to lock every
operator out.

This is not a proposal to automate *all* user changes (the native console stays
the direct/break-glass surface, ADR-0122). It is a narrow, opt-in capability for
the platform's own, non-editable processes.

## Decision drivers

- **Least privilege, not "admin in a box".** The capability must be exactly
  create-user / set-password / disable-user, not a general admin credential that
  could do anything the admin API allows.
- **Gated to the platform.** Only deployments in the protected system project
  (ADR-0122) may invoke it. A user-authored model must not be able to provision
  accounts.
- **No secret in the model (ADR-0041/0067).** Whatever credential or capability
  is used is resolved server-side; the model carries only intent.
- **Keep the ADR-0044 safety rails.** The last-enabled-admin lockout guard,
  uniqueness checks, and password-length minimum must still apply — this path
  reuses the store logic, it does not go around it.
- **At-least-once safe.** A connector job can be retried (like the EntraID
  script task, `examples/entra-create-account.bpmn`); provisioning must be
  idempotent so a retry never double-creates.
- **Auditable.** A privileged write made by a process must be attributable to
  that process instance, distinct from a human admin's action (ADR-0044's audit
  trail intent).
- **Off by default.** An instance that does not want automated provisioning must
  be unaffected — the same opt-in stance as ADR-0122's `WithSystemProcesses`.

## Considered options

1. **REST connector to the loopback user API with a vault-stored admin
   credential.** The intake process carries an `<atlas:restConnector>` task
   `POST http://127.0.0.1:8080/api/v1/users`, authenticating with an admin
   credential resolved from the connector store / vault (ADR-0041/0069). This is
   the lightest build — the REST connector already exists (ADR-0067) and already
   resolves secrets server-side.
2. **A dedicated in-process user-provisioning connector**, gated to the system
   project. A new connector flavor (`<atlas:userConnector operation="create|
   set-password|disable">`, sibling to mail/sharepoint/remedy in the connector
   catalog) runs on the job path *inside* the server and calls the user store
   directly through a bounded capability — no loopback HTTP, no credential in the
   model at all. The deploy/runtime path refuses it for any process not in the
   protected system project.
3. **Status quo (ADR-0122): keep it a human admin task.** The process
   coordinates; a person clicks "create".

## Decision outcome

Chosen option: **option 2 — a dedicated, least-privilege, in-process
user-provisioning connector, gated to the protected system project, opt-in, and
audited.**

It is the only option that opens the boundary *narrowly*. Concretely (proposed
shape; a follow-up implements it test-first):

- **A `userConnector` job flavor** in the connector catalog (ADR-0067), with
  `operation ∈ {create, set-password, disable}` and FEEL-valued fields
  (`username`, `email`, `displayName`, `roles`, `password`, …). Like the mail
  connector, only intent lives in the model; there is **no** endpoint or
  credential in the XML.
- **In-process execution through a bounded capability.** The job handler calls
  the same user-store operations the admin API handlers use — including the
  last-admin lockout guard, uniqueness, and password-length checks — so the rails
  of ADR-0044 are reused, not bypassed. It never gains general admin power; it
  can do exactly these three things.
- **Gated to the system project.** The handler refuses to run for any process
  whose id is not a bootstrap-deployed system process (the `systemPIDs` set from
  ADR-0122). A tenant model that somehow declared the connector would fail at
  runtime; ideally the compiler also rejects it outside the system project.
- **Idempotent.** `create` treats an existing username as success (returns the
  existing id, `created:false`), mirroring the EntraID example, so an
  at-least-once retry never double-creates.
- **Audited.** Each provisioning write records the acting process instance key as
  the actor, so the audit trail distinguishes "provisioned by intake instance X"
  from "changed by admin Y" (ADR-0044 follow-up: audit logging).
- **Opt-in.** A server option (e.g. `WithUserProvisioning`) enables the flavor,
  off by default; `WithSystemProcesses` does not imply it. An instance keeps the
  human-in-the-loop ADR-0122 behavior until an operator opts in.

This supersedes, for the system project only, the ADR-0044/0049 rule that no
automated identity may manage users — and *only* there, with a bounded capability
and an audit record. The native console and the human-in-the-loop processes
remain valid and are unchanged.

### Consequences

- **Positive:** The platform's own processes can fully provision, reset, and
  disable Atlas logins with the safety rails intact. No admin credential exists
  to leak or rotate (option 1's burden is avoided); no secret enters a model; the
  blast radius is a bounded, system-project-only capability rather than general
  admin. Existing instances are untouched until they opt in.
- **Negative / trade-offs accepted:**
  - A new connector flavor plus its worker and gating is more code than wiring a
    REST connector to an admin credential (option 1).
  - The ADR-0044/0049 boundary is genuinely relaxed for the system project;
    that relaxation must be justified by the gating + least-privilege + audit,
    and reviewed as such.
  - Provisioning runs on the post-fsync job path; a mailed password for an
    account whose create later fails on retry needs the same care the EntraID
    example takes (idempotent create, `ForceChangePasswordNextSignIn`-style
    "must change on first login" semantics once the local user model grows one).
- **Follow-ups / risks to watch:** a "must change password at next login" flag on
  the local `User` (the model has none today); compiler-level rejection of the
  connector outside the system project, not just runtime; whether access-review
  outcomes may *act* (disable) automatically or only propose; rate/complexity
  policy for generated initial passwords.

## Pros and cons of the options

### Option 1 — REST connector + vault admin credential
- **Good:** Smallest build; reuses the existing REST connector (ADR-0067) and
  secret resolution (ADR-0041/0069). No new engine surface.
- **Bad:** Hands a **general admin credential** to a connector — far more
  privilege than "provision a user", and a real credential to store, rotate, and
  protect. Calls the API over loopback HTTP from inside the same process. Gating
  to the system project is awkward (any model that knew the URL + had the
  connector configured could call it). Weaker attribution (the write looks like
  an admin API call, not a process action).

### Option 2 — dedicated in-process provisioning connector (chosen)
- **Good:** Least privilege (exactly three operations); no credential anywhere;
  natural gating to the system project via the ADR-0122 `systemPIDs`; reuses the
  store's safety guards; clean attribution to the process instance.
- **Bad:** More code — a new connector flavor, worker, gating, and opt-in.

### Option 3 — status quo (human admin task)
- **Good:** No boundary relaxation at all; simplest and safest.
- **Bad:** Does not deliver the goal — the account is still created by hand.

## Links

- supersedes (for the system project only) the automation-cannot-manage-users
  rule of ADR-0044 and ADR-0049
- builds on ADR-0122 (protected system project + `systemPIDs` gating), ADR-0067
  (service-task connector catalog), ADR-0041 (connector management / secret
  store), ADR-0069/0070 (encrypted vault), and the at-least-once idempotency
  pattern of `examples/entra-create-account.bpmn`
- relates to ADR-0044 (user management & the auth boundary), ADR-0028 (forms /
  Tasks app), ADR-0079 (mail connector — the already-sanctioned side effect)
