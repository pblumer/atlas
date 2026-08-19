# ADR-0126: Self-service registration link on the login screen

- **Status:** Accepted
- **Date:** 2026-08-13
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas models its own user lifecycle as BPMN processes and forms (ADR-0122): the
`proc_benutzer_aufnahme` intake process is bootstrap-deployed into the protected
system project, an admin approves each request, and the account is provisioned by
the sanctioned `userConnector` (ADR-0123). But that process could only be *started*
by someone already signed in. There was no way for a person **without an account**
to ask for one — the very first step of onboarding still happened out-of-band
(email, chat, a ticket), outside the process Atlas is supposed to own.

We already have the mechanism for anonymous, unauthenticated process starts:
public start links (ADR-0029) publish one process's start form at
`/public/forms/<token>`, rate-limited and payload-capped. What was missing was
(a) a hook on the login screen that points an unauthenticated visitor at that
form, and (b) a way for the intake process to be a *safe* public form — its start
form let the requester pick their own role (`user` / `admin`), which a stranger
must never be able to do.

## Decision drivers

- **Dogfooding.** Registration should start Atlas's own intake process, not a
  bespoke code path — "Atlas bildet seine eigenen Prozesse ab".
- **Security.** An anonymous registrant must not be able to request an elevated
  account. The human approval step (Freigabe) is the gate; the requester's input
  must never decide privilege.
- **Works out of the box.** A fresh install should show the "Registrieren" link
  with no manual publishing step, while an operator can reconfigure or switch it
  off.
- **Reuse, don't reinvent.** Prefer the existing public-link and settings-sidecar
  machinery over a new subsystem.

## Considered options

1. **A dedicated public registration process**, separate from the intake process,
   with its own role-less form and a hard-coded `roles="user"`.
2. **Make the one intake process public-safe** by removing the role choice from
   its start form and moving role assignment to the admin approval step, then
   publish *that* process via a public link chosen by an org-wide setting.
3. **A bespoke `/register` endpoint** that creates a pending user directly, outside
   the process engine.

## Decision outcome

Chosen option: **Option 2 — make the intake process itself public-safe and wire the
login screen to its public link via an org-wide setting.**

Concretely:

- **Start form (`ba-antrag`) drops the role field.** The requester supplies name,
  email, department, and justification — never a role. With no `rolle` bound, the
  connector's `roles="=rolle"` resolves to the base `user` role by default, so the
  worst case is still a plain user.
- **The admin assigns the role at approval (`ba-konto`).** The approval form's
  `rolle` becomes an editable radio (default `user`); the admin who already decides
  *whether* to create the account now also decides *at what privilege*. This is the
  security boundary that makes the same process safe to expose publicly.
- **A `registration` org-wide setting** (settings sidecar, alongside the theme —
  ADR-0113) names the process whose public form the login link opens. Absent
  record → the built-in default (`proc_benutzer_aufnahme`); stored empty id →
  explicitly disabled.
- **A public, read-only `GET /api/v1/settings/registration`** (unauthenticated,
  like `/info` and the theme) tells the login screen whether to show a
  "Registrieren" link and the `/public/forms/<token>` URL it points at. The public
  link is minted at bootstrap (default) or when an admin configures the setting
  (`PUT`), never on the anonymous GET, which stays a pure read.
- **The login screen** fetches that config and reveals the link when enabled; a
  disabled instance or a fetch failure simply leaves it hidden.

### Consequences

- **Positive:** Onboarding now starts inside Atlas's own process, reachable by
  someone with no account. The role is never requester-controlled — a strictly
  safer model than before, on the internal path too. No new engine surface: it is
  ADR-0029 public links + an ADR-0113-style setting + a form change.
- **Negative / trade-offs accepted:** A fresh instance publishes the intake
  process's start form publicly by default. That is deliberate (the feature's whole
  point) and bounded — the endpoint is rate-limited, the payload is capped, every
  request still waits for human approval, and an admin can switch registration off
  with one call. Upgrading instances gain the public link on first start after this
  change; operators who do not want it disable it.
- **Follow-ups / risks to watch:** Spam/abuse of the anonymous start (mitigated by
  the rate limiter and the approval gate, but worth watching); a CAPTCHA or
  email-verification step could be added later without changing this wiring.

## Pros and cons of the options

### Option 1 — dedicated registration process
- Good: the public and internal forms are physically separate; no shared form to
  reason about.
- Bad: duplicates the whole intake process (approval, mail, gateway) for no
  behavioural difference; two processes to keep in sync; more surface, not less.

### Option 2 — public-safe intake process + setting (chosen)
- Good: one process, one source of truth; the role-at-approval change hardens the
  internal path too; reuses public links and the settings sidecar.
- Bad: the same process serves two audiences, so the "no role field" invariant of
  its start form must be preserved deliberately (documented in the BPMN and guarded
  by a test).

### Option 3 — bespoke `/register` endpoint
- Good: total control over the request shape.
- Bad: abandons dogfooding — a privileged write path outside the engine, exactly
  the ADR-0044/0049 boundary we work to keep closed; no approval, audit, or process
  history for free.

## Links

- builds on ADR-0029 (public start links)
- builds on ADR-0122 (protected system project + bootstrap deployment)
- builds on ADR-0123 (sanctioned user provisioning) and respects ADR-0044/0049
- mirrors ADR-0113 (org-wide UI setting served before login) for the settings shape
