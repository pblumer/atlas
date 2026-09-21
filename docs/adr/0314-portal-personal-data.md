# ADR-0314: Personal data in the portal — a reference by default, a destroyable key for the rest

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers
- **Open question:** whether an erasure can be carried out *responsibly* without the engine
  knowing which instances a subject appears in. Destroying the key is one call and reaches
  every copy; finding the live instances that will then park rests entirely on the model
  having declared the data-subject variable searchable, which nothing enforces. The route
  destroys the key either way. Checked by reading the code and the one example, not by an
  operator having done it: no erasure has been carried out on a real installation.
- **Question checked:** 2026-09

Two earlier questions are closed. Whether a portal process can be written without computing
on a declared variable was closed by a real process rather than by argument:
`examples/account-bestellung` built a UPN, a mailNickname and a display name out of a first
and last name — one `zeebe:script` plus three output mappings, all engine-evaluated — all
four moved into the create-user task's own `attributes` expression, and it deploys with **no
exception needed**. What that cost is recorded below rather than glossed: the example lost a
fail-closed gateway, and later grew a Personalnummer it had no business reason for. And
whether a worker expression still sees plaintext once values are enciphered is closed by
construction: every connector builds its FEEL bindings through one function over a
`state.Reader`, and the reader handed to them opens enciphered values as it yields them. Had
it not, all 74 exempted expression fields would have evaluated FEEL over ciphertext while
still looking correct.

## Context and problem statement

The portal keeps its evidence forever. Who ordered what, who approved it, what was
provisioned and when is the record an audit asks for years later, and
[ADR-0312](0312-portal-catalogue-order-inventory.md)
makes the inventory durable for exactly that reason.

An order is also full of personal data. It names the person who ordered, the person it
is for, and their superior; its fulfilment carries a display name, a UPN, an address,
an initial password. Art. 6 para. 4 of the Swiss Data Protection Act requires personal
data to be kept only as long as the purpose requires. "Forever" is not an admissible
default for that half, whatever is true of the evidence.

Atlas cannot resolve this the way an ordinary application would, because it cannot
delete. `docs/compliance/isds-konzept.md` states the problem as risk R-06 and rates it
amber: retention deletes the *state record* of a finished instance
([ADR-0115](0115-history-retention-hard-delete.md), [ADR-0144](0144-per-definition-history-ttl.md)),
but the events stay in the log until compaction, and the same values have by then been
copied into checkpoints ([ADR-0131](0131-engine-recovery-checkpoints-and-wal-compaction.md)),
OpenSearch ([ADR-0114](0114-opensearch-event-exporter.md)), backups and instance
snapshots. Each copy has its own retention, its own operator and its own schedule, and
a value is only gone when the last of them has let go of it. The concept's own answer
is a *modelling recommendation* — prefer a reference to the content — which is advice,
enforced by nothing.

The question: **how does a portal that keeps its evidence forever stop keeping personal
data forever, in an engine that cannot delete and whose copies it does not control?**

## Decision drivers

- Evidence and personal data have different required lifetimes. A design that gives
  them one lifetime is wrong in one direction or the other.
- `applyToState` runs live and on recovery and must stay deterministic and free of side
  effects (I4, [ADR-0001](0001-event-sourcing-and-log-structured-state.md)). It may not
  read a key, and it may not fail because a key is gone.
- No per-command allocation on the processor path (I1).
- Whatever is done must reach **every** copy, including backups already written and
  offline. A measure that only reaches the live store does not answer R-06.
- It must not become a second secret store. The vault exists
  ([ADR-0069](0069-engine-internal-encrypted-secret-vault.md),
  [ADR-0070](0070-vault-on-by-default-with-generated-key.md)).

## Considered options

1. **Retention alone** — shorten the fuses, enable compaction, accept the residue.
2. **Rewrite history** — rewrite or redact log segments when a subject is erased.
3. **Reference by default** — carry principal ids, never content, and accept that some
   payload cannot be expressed that way.
4. **Crypto-shredding** — write personal data enciphered under a per-subject key and
   destroy the key.
5. **Options 3 and 4 together.**

## Decision outcome

Chosen option: **3 and 4 together** — a reference is the rule, and what cannot be a
reference is enciphered under a key that can be destroyed.

Option 1 is the status quo and is what R-06 rates amber: it depends on a chain of
independently configured retentions, and it can never reach a backup tape. Option 2 is
rejected outright — rewriting the log breaks the one property every other guarantee in
Atlas rests on, that state after replay equals state built live. Neither 3 nor 4 alone
is sufficient: a reference cannot express the UPN a target system must be given, and
enciphering everything would put ciphertext where the engine needs to route on a value.

### The rule: a reference, not the content

Orders, order lines and entitlements name people by **principal id** and by nothing
else. Names, mail addresses, departments and superiors are resolved from the account
when a screen is rendered, never copied into an order, an entitlement or a variable.
This turns the ISDS concept's modelling recommendation into a property of the portal's
data model, where it is checkable rather than remembered.

It also settles a question the inventory record left open by implication: because an
entitlement holds an id, nothing personal enters the inventory column family, and the
column family exempt from retention is therefore not an exemption for personal data.

The residue this leaves is exactly the payload a target system requires and a reference
cannot carry: display name, UPN, initial password, postal address. That residue is what
option 4 is for.

### The mechanism: a data key per subject, wrapped in the vault

The vault today seals every secret under one master key. Erasing one subject by
destroying that key would make the whole instance unreadable, so this adds one level:

- Each data subject gets a random **data key**, stored in the vault under its principal
  id — which means it is itself sealed under the master key, with no new key material
  on disk and no second store.
- A variable a process declares as personal is enciphered under that data key before it
  ever becomes a command.
- Erasing a subject deletes that one vault entry.

Two things this stated loosely, corrected by building it (2026-09).

The id is **not** necessarily a principal id. This record assumed the subject is already a
principal in Atlas; the one real process that needed the mechanism is an account order,
whose subject is a joiner with no principal precisely because the account is what is being
ordered. The vault entry is named by whatever id the model declares, which for that process
comes from outside Atlas — and the model had to grow a field to carry it, which is recorded
as a cost below.

And the key is **stored** rather than derived, which is not an implementation preference. A
key derived from the master key and the subject id (HKDF, say) would need no storage at all
— which is exactly what makes it useless here. There would be nothing to destroy.

### Whose key: the data subject is declared, not inferred

This record said a declared variable "is enciphered under that data key" and never said how
an instance knows whose key that is. The gap cannot be left to the enciphering code, because
both ways of getting it wrong are silent: encipher under the wrong subject and an erasure
destroys the wrong key, so the data stays readable; encipher under too broad a subject and
it destroys somebody else's data along with it.

So the model says it. `atlas:dataSubject` names the one variable holding the id of the
person the declared variables are about, and the compiler refuses the two declarations apart
— personal data with no subject could never be erased, which is the whole purpose, and a
subject with nothing personal enciphers nothing while looking like protection.

Three cheaper answers were available and each is wrong:

- **The instance's starter.** An account is ordered *for* somebody, routinely by somebody
  else. The starter is the wrong person about half the time, and nothing would say so.
- **A key per instance**, which needs no declaration at all. The key must outlive the
  instance's state record, and history retention deletes that routinely
  ([ADR-0115](0115-history-retention-hard-delete.md)); after it there would be nothing left
  to find the key by, so the ciphertext in every backup would stay readable forever. This is
  the argument that settles it: erasure has to work after retention has already run.
- **A conventional variable name.** That is the modelling recommendation R-06 already has,
  which is why R-06 is still amber.

And once values are sealed under it, the subject cannot move. Sealing follows whatever the
data-subject variable says at the moment of each write, so changing it halfway splits one
person's values across two keys — after which erasing either subject leaves the other half
readable while the request looks honoured, and nothing downstream could notice, because
every value opens perfectly well under the key it names. The writer refuses such a write
with an incident, and refuses it only once something has actually been sealed: correcting a
mistyped id before any personal value exists costs nothing, and refusing that would be
pedantry dressed as safety. Afterwards the honest remedy is a new instance, not a retry,
which the incident says.

The subject's id stays in the clear, deliberately: it is a reference, which is what the rule
above keeps readable, and it may be routed on, matched and indexed like any other variable.
Declaring it searchable is the intended combination — erasing a person starts with finding
their instances. Declaring a *personal* variable searchable is refused instead: the index
stores what the engine sees, which is ciphertext under a random nonce, so two writes of one
name differ and no exact match could ever hit.

The property that makes this the answer to R-06 and not merely another retention knob:
**every copy carries the same ciphertext.** The WAL segment, the state record, the
checkpoint, the OpenSearch document, last year's backup, the instance snapshot an
operator exported — all of them hold bytes that the destroyed key decrypted. Nothing
has to be found, coordinated or reached. The tape in the safe is covered by the same
act as the live store, and that is something no retention schedule can claim.

An erasure writes one line to the security audit trail
([ADR-0197](0197-login-throttle-and-audit-log.md)), naming the subject and who acted, and that
line is load-bearing rather than informative: afterwards the key is gone, the ciphertext
says nothing and the subject leaves no other trace anywhere in Atlas, so it is the only
remaining evidence that a request was honoured, when, and by whom. Demonstrability is half
of what the obligation asks for, and an operator who had destroyed the data but could not
show it would have satisfied neither half. It is the one place these events carry a personal
identifier, deliberately: an erasure record that does not say who was erased proves nothing.

Note what is *not* claimed: this is erasure in the sense of rendering the data
permanently unreadable, not physical removal of the bytes. Whether that satisfies a
given supervisory authority is a legal judgement for the operator's data protection
officer, not a property this record can assert.

### Where the boundary sits: the engine sees only ciphertext

The engine never enciphers and never deciphers. That is the whole of how this stays
inside the invariants:

- **In** at the edge — the API handler accepting a form submission, the worker result
  landing a job's output. The command the processor receives already holds ciphertext,
  so nothing is enciphered per command on the hot path (I1).
- **Out** at the edge — the job payload handed to a worker after fsync, the task detail
  or form a person is shown. Deciphering happens in the post-commit phase and in
  handlers, where reading a key is permitted (I2,
  [ADR-0005](0005-group-commit-and-fsync-strategy.md)).
- `applyToState` stores and returns bytes. It never holds a key, never fails on a
  missing one, and replays a destroyed subject's instance exactly as it replayed it
  before — the ciphertext is still there and still deterministic (I4).

#### Where those edges turned out to be (2026-09)

Named as built, because "at the edge" is only an instruction until somebody has to find
them:

- **In.** A start submission (JSON body, CSV upload, public form link), a worker's
  completion, a task's submitted form, an operator's variable override, and the in-process
  job runner's own handler outputs. That last one is the one worth naming: it never passes
  through the HTTP completion endpoint, so an in-process connector would otherwise have
  been a hole in the middle of the mechanism. Each seals off the run loop, and each fails
  closed when no data subject can be resolved — storing the value in the clear would leave
  it un-erasable forever, and sealing it under an empty subject would give every instance
  one shared key.
- **Out.** The payload handed to a worker, and the variable read a task's form and detail
  are built from. For connector expressions the intervention is a single one rather than
  one per connector: every connector builds its FEEL bindings through
  `state.VisibleVariablesMap` over a `state.Reader`, so the reader handed to them is one
  that opens enciphered values as it yields them. It covers the nineteen that exist and any
  that follow.
- **Neither.** The timeline, the variable audit and the instance lists report an enciphered
  value as *what it is* — personal, and whose — rather than printing base64 or spending a
  vault read per row. That answers the follow-up below about the variable audit, in the
  cheapest way available.

One thing that reading follows from, and is worth stating so it is not discovered by
inference: **enciphering is about erasability, not access control.** What a caller may read
of an instance at all is [ADR-0275](0275-instance-visibility.md)'s decision, and a
declared variable does not quietly become a second boundary with different rules.

#### The check the edges cannot be: the writer refuses a value in the clear

Not every path into an instance has an edge to seal at. A message payload correlates to its
instance *inside the engine*, which holds no key and must not have one. So the single writer
refuses a write of a declared variable that arrives readable, and parks an incident naming
it. Without that check, that one path would write an un-erasable personal value while the
declaration said the opposite, and nothing about it would look wrong.

It can ask the question without a key: an enciphered value carries a marker, and recognising
one is a prefix comparison on bytes the engine already holds. That is why the marker lives
beside the variable's own encoding while the sealing stays in the vault.

The compiler refuses the deploy-time half of the same thing: an engine-evaluated expression
may not *write* into a declared variable either. The engine cannot encipher, so a value it
computes into one would be stored in the clear; and the variable is not thereby unusable,
because a name arrives from a form, a worker or an operator, and every one of those seals.

Which variables are personal is **declared on the process**, as a comma-separated
attribute resolved at compile time — the same shape and the same place as the
searchable-variable declaration of [ADR-0244](0244-searchable-variables.md), so this
adds a list, not a mechanism. A variable is a record either way
([ADR-0294](0294-a-variable-is-a-record.md)); this one carries bytes the engine cannot
read.

### The consequence that bites: a declared variable is payload, not a value

Ciphertext cannot be compared, matched, or routed on. So a declared personal variable
may not appear in any expression: no gateway condition, no sequence-flow condition, no
input/output mapping, no assignment expression, no script.

**The compiler refuses a deployment that does** — it holds both the declaration and
every compiled expression, so this is decidable at deploy time and belongs there
(I5, [ADR-0008](0008-feel-expression-strategy.md)). A rule that relies on a modeller
remembering is the modelling recommendation R-06 already has, and it is the reason
R-06 is still amber.

#### What it costs, measured on the one process that had to move (2026-09)

The transform moved cleanly, and something else did not survive. `account-bestellung` had
**two** fail-closed gateways, and the first checked the process's own computed UPN against
`jml-test-*@contoso.com` before any write reached Entra. With the construction in the
worker there is no such process variable, so there is nothing for a gateway to read: the
test-object boundary is now carried by the `jml-test-` literal inside the connector's
`attributes` expression — in the BPMN, visible in review, but **structural rather than
checked at runtime**.

So the rule has a consequence this record did not state: **it removes the ability to assert
a runtime invariant on a value derived from personal data.** Not on the personal data itself,
which was never routable, but on anything computed from it — and derived identifiers are
exactly what such gateways tend to guard.

It is a smaller loss here than it first looks, which is why the example took it rather than
an exception: the gateway was checking a value the same process had built two steps earlier,
so it guarded a disagreement between a script and a check. Once construction and boundary
are one expression, they cannot disagree. Where the derived value comes from *outside* the
process, that reasoning does not transfer and the loss is real.

Routing therefore happens on references, which is what the rule above already makes
available: a task is assigned to a principal id, not to a name; an approval routes to
the superior's id, not their mail address. Transformations that build one personal
value out of another — deriving a UPN from a name — belong in the worker that needs
the result ([ADR-0047](0047-polyglot-script-tasks-via-job-workers.md)), where the
plaintext exists for the duration of one call and is never written back in the clear.
The open question above records that this has not yet been proven against a real
portal process.

### Consequences

- **Positive:** One act of erasure reaches every copy, including ones nobody can reach
  any more. The evidence survives it — an order still shows that a principal ordered
  and an approver approved, with the ids intact. No new secret store, no new key
  material on disk. The rule is enforced by the compiler rather than by discipline.
- **Negative / trade-offs accepted:** Declared variables are opaque to the engine, and
  a model that needs to compute on one has to be restructured. Erasing a subject with
  a live instance leaves that instance unable to provision — correctly, but it will be
  reported as an incident rather than as a clear message, unless the erasure path
  refuses while instances are running. Key loss is now data loss for that subject, so
  vault backup discipline (M-03 in the ISDS concept) becomes load-bearing for business
  data, not only for credentials.
- **Negative / what building it cost (2026-09):** Three, each paid somewhere the design
  did not predict. **A field in the model:** encipherment needs a subject and a deletion
  request needs an id it can name, so `account-bestellung`'s start form grew a
  Personalnummer no business requirement asked for — and for a joiner that id necessarily
  comes from outside Atlas. **A read on the writer:** the worker payload and the connector
  payload are both assembled on the run loop today, so opening a value there is a vault
  file read on the single writer — bounded, and only for instances that carry personal
  data, but on the writer. Moving connector resolution off the loop
  ([ADR-0239](0239-off-loop-queries.md)'s pattern) is the fix and is not done. **A log
  line where an incident belongs:** a job whose values can no longer be opened is withheld
  from activation and says so in the log, which is diagnosable but is not the incident this
  record predicted; a worker polling for it sees nothing.
- **Follow-ups / risks to watch:** Whether erasure should be blocked while the subject
  has running instances or entitlements — this record still does not decide it, and the
  route destroys the key regardless, which is what the open question above is about.
  Whether a withheld job should raise an incident rather than a log line. Whether the
  variable audit ([ADR-0098](0098-external-variable-modification-audit.md)) needs more than
  the "personal, for subject X" label the views now show. Key rotation
  is out of scope here: the master key rotates as it does today, and a data key is
  never rotated, because a rotated data key would leave the old ciphertext readable
  under the old one and defeat the purpose.

## Pros and cons of the options

### Retention alone
- Good: exists today, nothing to build.
- Bad: depends on several independently configured schedules agreeing; cannot reach a
  written backup; leaves R-06 amber by the concept's own assessment.

### Rewriting the log
- Good: the data is genuinely gone.
- Bad: destroys replay equivalence, which every other guarantee rests on. Not
  considered further.

### Reference by default, alone
- Good: shrinks every copy at once and costs nothing at runtime.
- Bad: cannot express the payload a target system must receive, which is where the
  most sensitive values are.

### Crypto-shredding, alone
- Good: reaches every copy with one act.
- Bad: applied to everything, it puts ciphertext where the engine needs to route, and
  it enciphers ids that did not need it.

### Both
- Good: each covers what the other cannot; the references stay computable and the
  payload stays erasable.
- Bad: two mechanisms to understand, and a deploy-time rule that will occasionally
  refuse a model somebody thought was fine.

## Links

- required by [ADR-0312](0312-portal-catalogue-order-inventory.md) — the inventory stores a principal reference because of this record
- answers risk R-06 in `docs/compliance/isds-konzept.md`, and amends its modelling recommendation into a rule
- amends [ADR-0115](0115-history-retention-hard-delete.md) and [ADR-0144](0144-per-definition-history-ttl.md) — retention is no longer the only erasure mechanism
- builds on [ADR-0069](0069-engine-internal-encrypted-secret-vault.md) and [ADR-0070](0070-vault-on-by-default-with-generated-key.md) — the vault wraps the data keys
- follows the shape of [ADR-0244](0244-searchable-variables.md) — a declared variable list on the process
- constrained by [ADR-0001](0001-event-sourcing-and-log-structured-state.md), [ADR-0005](0005-group-commit-and-fsync-strategy.md) and [ADR-0008](0008-feel-expression-strategy.md) — I1, I2, I4, I5
- reaches [ADR-0114](0114-opensearch-event-exporter.md) and [ADR-0131](0131-engine-recovery-checkpoints-and-wal-compaction.md) copies without addressing them individually
