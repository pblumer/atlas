# ADR-DRAFT: A system process announces its facts as signals

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers

## Context and problem statement

Self-service registration (ADR-0126) starts the protected system process
`proc_benutzer_aufnahme` from a public form. The request then waits at "Antrag
freigeben" for somebody in `benutzerverwaltung`. Nothing tells that person a request
has arrived. On an instance where registration is rare, the request can sit for days
because nobody looked, which is the very failure a governed front door exists to
avoid.

The obvious fix is a notice in the channel the operator already reads, such as
Discord (ADR-0258) or mail (ADR-0079). The operator cannot model that notice into
the process. The process lives in the protected system project (ADR-0122): the API
refuses edits to it, and the bootstrap re-deploys the embedded bytes on the next
start anyway. The process also cannot carry the notice itself. It ships inside the
binary to every installation, and a Discord Worker name or a channel id is a fact
about one installation, not about Atlas.

Atlas has no other way for a process to reach an observer it does not know about.
It has no execution or task listeners (they are on the roadmap), no outbound
webhooks, and no event feed covering process starts: the feed from ADR-0429 carries
the outcomes of actions on order positions and granted or revoked rights only. ADR-0343 also rules out a notification path inside Atlas
itself. A modelled process sends; Atlas does not.

The question is how a protected platform process lets each installation react to
what happens in it, without the process naming the installation's targets.

## Decision drivers

- **The protected process names no installation's targets.** No Worker, no
  channel, no address.
- **Nothing to configure where nobody listens.** An installation that wants no
  notice must not see a parked job, an incident or a setting it has to switch off.
- **The notice never holds up the request.** A Discord outage must not stall a
  registration.
- **No secret leaves the process.** `initialpasswort` comes into being at the
  approval step and must never reach a listener.
- **No engine change.** The invariants stay as they are. A mechanism that already
  exists is preferred over a new one.

## Considered options

1. **Throw a BPMN signal at the fact.** An installation listens with a signal start
   in a process of its own.
2. **A Discord (or mail) task inside the system process.**
3. **A wrapper process behind the registration setting.** ADR-0126's setting points
   the login link at a tenant process, which notifies and then calls the system
   process.
4. **Poll.** A timer-started process, or a cron job outside Atlas, lists the open
   "Antrag freigeben" tasks and reports them.
5. **Build execution listeners or a process-event feed** and hang the notice on
   that.

## Decision outcome

Chosen option: **option 1, a signal.** `proc_benutzer_aufnahme` throws
`atlas.user.requested` from an intermediate event between "Zugangsdaten vorschlagen"
and "Antrag freigeben".

A signal already has the semantics the drivers ask for (ADR-0088):

- **The thrower knows no receiver.** It broadcasts by name and starts every deployed
  process with a matching signal start.
- **A signal nobody listens for is a no-op.** An installation without a listener is
  untouched.
- **The receiver runs as its own instance.** A failing notice becomes an incident
  there, not in the request.
- **The receiver gets the facts as variables.** The broadcast carries the throwing
  instance's variables. The listener sees `vorname`, `nachname`, `email`,
  `abteilung`, `begruendung` and `benutzername` as ordinary FEEL variables.

**The throw sits before the approval on purpose.** At that point the instance holds
what the requester typed and the proposed username, and nothing the approval adds.
`initialpasswort`, `rolle` and `entscheidung` do not exist yet, so no listener can
receive them. Moving the throw later would hand the initial password to every
listener. A test guards the position.

**The signal is an interface.** Its name, its position and the variables present at
the throw are now a contract with every installation's listener. Renaming it,
moving it after the approval, or adding a variable that holds a secret before it
breaks or endangers listeners Atlas cannot see. Such a change needs its own record.

**The name follows the system processes' own convention.** The order fulfilment
process (`api/systemprocesses/auftrag-erfuellung.bpmn`) already uses
`atlas.order.placed`, which sets the pattern `atlas.<subject>.<what happened>`.

**What a listener does is the installation's choice.** The recipe in
`examples/benutzerverwaltung/README.md` sends one Discord message. A listener
building a message from a public form's input must silence mentions with
`allowed_mentions: {parse: []}`, because a stranger can type `@everyone` as a first
name.

### Consequences

- **Positive:**
  - An installation hears about every intake request, from the public form or
    started by a signed-in user, with one process of its own and no change to the
    platform.
  - The same pattern is available to the other system processes when they have a
    fact worth announcing.
  - No engine, compiler or API change.
- **Negative / trade-offs accepted:**
  - **A signal is engine-wide.** It matches by name across all projects, so anyone
    who may deploy can listen and receive a requester's name, email and
    justification. That widens who can read that data beyond `benutzerverwaltung`.
    It is accepted because the people who may deploy are already trusted with the
    engine. It is the reason the payload stops before anything secret.
  - **A signal is not buffered.** A listener that is not deployed, or is
    deactivated, when a request arrives never hears of it.
  - **A notice that fails is an incident in Operations,** which is not where the
    person who missed the request is looking.
- **Follow-ups / risks to watch:**
  - **A repeating reminder.** A non-interrupting repeating timer on "Antrag
    freigeben" (ADR-0236) could announce a second fact while the request waits.
    That would cover a missed or failed first notice.
  - **A narrower payload.** A signal carries every instance variable. A way to send
    only named ones would let the payload shrink below what the throw position
    happens to hold.

## Pros and cons of the options

### Option 1: a signal
- Good: an existing mechanism. The platform names no target, nothing happens where
  nobody listens, and the notice is isolated from the request.
- Bad: engine-wide by name, unbuffered, and the payload is whatever the instance
  holds at the throw.

### Option 2: a task inside the system process
- Good: the smallest change, and the notice sits visibly in the diagram.
- Bad: it names one installation's Worker and channel in a process every
  installation runs. Elsewhere it would park or raise an incident, and a Discord
  outage would hold up the request.

### Option 3: a wrapper behind the registration setting
- Good: no change to Atlas at all, and the data stays inside one process tree.
- Bad:
  - A second front door duplicates the start form.
  - A signed-in user starting the intake directly bypasses it.
  - The old public link stays valid until revoked by hand.
  - A later change to the intake form drifts silently.

### Option 4: polling
- Good: the only option that repeats a notice someone missed.
- Bad:
  - It needs an operator token with full scope stored for a connector.
  - It filters instances by a definition key that changes with every version.
  - It reports late.

### Option 5: listeners or a process-event feed
- Good: the general answer, and it would serve every process, not only the
  platform's.
- Bad: neither exists, and both are engine and API work far larger than this need.
  A signal does not preclude either later.

## Links

- relates to ADR-0088 (signal events), whose semantics this relies on
- relates to ADR-0122 (protected system project) and ADR-0126 (self-service
  registration)
- relates to ADR-0258 (Discord Worker), the recipe's channel
- relates to ADR-0343 (Atlas does not send; a modelled process does)
- relates to ADR-0236 (repeating non-interrupting boundary events), the reminder
  follow-up
