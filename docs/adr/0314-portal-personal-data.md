# ADR-0314: Personal data in the portal — a reference by default, a destroyable key for the rest

- **Status:** Accepted
- **Implementation:** Not started
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers
- **Open question:** Whether every portal process can be written without computing on
  a declared personal variable. The rule below refuses a deployment that reads one in
  an expression, and `examples/account-bestellung` already does exactly that — it
  builds a UPN from a mail nickname in a script task. Whether such transforms move
  cleanly into the worker, or whether the rule has to admit a narrow exception, is
  established by no portal process that exists yet.
- **Question checked:** 2026-09

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

The property that makes this the answer to R-06 and not merely another retention knob:
**every copy carries the same ciphertext.** The WAL segment, the state record, the
checkpoint, the OpenSearch document, last year's backup, the instance snapshot an
operator exported — all of them hold bytes that the destroyed key decrypted. Nothing
has to be found, coordinated or reached. The tape in the safe is covered by the same
act as the live store, and that is something no retention schedule can claim.

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
- **Follow-ups / risks to watch:** Whether erasure should be blocked while the subject
  has running instances or entitlements — this record does not decide it. Whether the
  variable audit ([ADR-0098](0098-external-variable-modification-audit.md)) needs to
  show that a value was declared personal rather than showing ciphertext. Key rotation
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
