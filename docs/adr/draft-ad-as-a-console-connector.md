# ADR-DRAFT: Active Directory is a connector you configure, not one you write into a model

- **Status:** Proposed
- **Date:** 2026-08-27
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0166](0166-active-directory-connector.md) gave Atlas an Active Directory connector
with AD-semantic operations, and put the directory in the **model**: an
`<atlas:adConnector>` task carries its own `url`, `bindDN` and `bindSecret`. That
decision was made almost in passing — 0166 argues at length about *semantics*
(UTF-16LE `unicodePwd`, the `ACCOUNTDISABLE` bit, incremental `member` changes) and
inherits the target's placement from the LDAP connector, mirroring
[ADR-0067](0067-service-task-connector-catalog.md)'s "the URL lives in the model".

That inheritance put AD on the wrong side of a line this repository otherwise draws
cleanly:

| target is credential-bearing infrastructure → **a Console record** | target is addressed per call, nothing secret about the address → **the model** |
|---|---|
| mail, clio, temis, SharePoint, Remedy, Jira, Entra, PostgreSQL, MariaDB, SQL Server | REST, web scrape, CSV, directory-file, script |

AD is a domain controller, a service account and a password. It belongs on the left —
SharePoint, which is Graph plus an OAuth bundle, is there for exactly this reason. AD
ended up on the right, and paid for it three times.

**An operator could not create one.** Every other credential-bearing integration is
added in the Console; AD was the one kind with nothing to add. The connector catalogue
had to label it "model-authored" and explain the exception.

**The mockup switch had nowhere per-directory to live.** ADR-0193 put it in the Console
because a start-up variable is the wrong ceremony for a thing you flip while trying a
process out — but with no per-directory record to hang it on, it could only be
**org-wide**: one switch, one seed, for every AD task in the instance.

**And "I serve two forests" had no good answer.** The real path handles it fine — each
task carries its own URL, so one worker binds to any number of directories. The *mock*
does not: `MockDirectory.Dial` validates the URL's scheme, journals it, and then returns
the same single in-memory forest whatever the URL was. Creating the same DN in what the
model says are two different directories fails with `entry already exists` — something
real AD would never do. The mockup is least trustworthy in exactly the topology that
most needs it.

Nor could more workers fix that. A worker activates jobs by **type** only
(`{"type": jobType, "worker": id}`); there is no routing by target. Two AD workers pull
from the same queue, and which one gets a job is a race — so "a worker per forest" is
not expressible, and would be nondeterministic if attempted.

## Decision drivers

- An integration whose target carries credentials should be configured where every
  other one is, and referenced from a model by name.
- The engine must never hold an AD service account: it is typically allowed to write
  half a forest.
- Models written before this must keep compiling and running, unchanged.
- Whatever the Console decides has to end in the same environment variables an external
  worker reads — [ADR-0157](0157-worker-processes-supervision-and-console.md) forbids a
  private channel between engine and supervised child.

## Considered options

1. **Keep the model-authored shape; fix only the mock**, giving it one in-memory forest
   per URL.
2. **Make AD a Console connector record**, named from the model like every other
   credential-bearing kind, with the model-authored shape kept as a compatibility path.
3. **Make AD a Console record and remove the model-authored shape**, migrating models at
   deploy time.

## Decision outcome

Chosen option: **AD becomes a Console connector record (option 2).**

- A record is `{name, kind: "ad", endpoint: <LDAP URL>, credentialsRef}`, where the
  reference names a vault bundle `{"bindDN": …, "password": …}` — the Remedy and Entra
  shape, so the record holds no credential and not even the service account's name (I6).
  The bundle is why AD needs **no new form field**: the Console gained an option, not a
  layout.
- `<atlas:adConnector connector="prod-forest" …>` addresses it. `url`/`bindDN`/
  `bindSecret` stay accepted, so every existing model compiles and runs unchanged.
- **Both at once is refused**, not resolved by precedence. Whichever rule we picked,
  half the readers of a model would assume the other — and the two point at different
  directories, so a silent winner writes to the wrong forest.
- The kind is `workerOnly`, like Entra: the engine builds no client and subscribes no
  in-process handler, so the service account never enters it. Only `superviseEnv`
  touches it, rendering `ATLAS_AD_CONNECTORS` plus per-name `_URL`, `_BIND_DN` and
  `_PASSWORD` — the Remedy renderer, name for name.
- The endpoint's scheme is validated on create. Getting it wrong has a specific and
  expensive failure: AD refuses to set a password over an unencrypted channel, so an
  `ldap://` directory works for every operation *except* the one a joiner needs most,
  and only discovers that on a real run.
- A directory whose bundle does not resolve is left out **whole**, its name included: a
  name in `CONNECTORS` with no URL behind it is the misconfiguration the worker refuses
  to start on, which would take down every other kind that worker serves.
- A named directory missing a field **is** refused at startup. The operator named it, so
  the omission is a mistake to report while they are watching. A bind DN with no
  password is refused for a sharper reason: Active Directory treats a simple bind naming
  a DN with an empty password as **anonymous** and succeeds, so the failure lands far
  from the cause — or does not land at all, and quietly reads what anonymous may read.

We did **not** take option 3. Removing the model-authored shape buys tidiness and costs
every existing model a migration, for a form that is not itself wrong — a model that
must carry its own directory (one per tenant, resolved by FEEL) is a real case, and the
compatibility path is one branch in the compiler and one in the resolver.

Option 1's mock fix is included here (below); on its own it would have left the other
two complaints standing.

### And the mock is keyed by directory

`MockDirectory` served every URL from one set of entries. That made it lie in exactly
the topology that most needs a mockup: a process addressing two forests found that
creating the same DN in the *second* failed with `entry already exists`, which no real
pair of domain controllers would ever do. It is fixed here rather than later, because
the record is what makes "two directories" expressible in the first place and shipping
the expression without the fix would invite the wrong answer.

- One `mockForest` per LDAP URL, each with its own entries **and its own DirSync change
  counter**. A shared counter would be the subtler half of the same bug: a
  reconciliation loop over one forest would report writes that happened in another, and
  the cookie would make that look authoritative.
- The seed is a **template**, not shared state. Each forest gets its own copy on first
  contact, so "the accounts a process expects to find" apply to whichever directory that
  process addresses, and the copies diverge once written to.
- The journal stays shared and keeps naming the URL per operation: it is the record of
  what *this worker* did, not what one directory holds.

The **switch** stays org-wide, deliberately. Per-directory mocking would let one run
write to a real forest while simulating another — a half-state whose whole risk is that
it looks like a full mockup run. "Everything is simulated" and "everything is real" are
the two states worth having, and the seam between them is the thing the switch exists to
make unambiguous. If a per-directory switch is ever wanted, the record is now the place
for it.

### Consequences

- **Positive:** AD is created and edited like every other integration, and appears in
  the connector list with the same "configured but not working" reporting. Two forests
  are two records, served by one worker, provably separate — in mockup mode too. A model
  stops carrying infrastructure. The engine holds no bind account. Nothing existing
  breaks.
- **Negative / trade-offs accepted:** Two shapes exist for the same task, and will for
  as long as the older one is supported — one extra branch in the compiler, the
  resolver and the worker, plus the refusal that keeps them from being mixed. The
  Console's connector list gains a kind whose records the engine never uses itself,
  which reads oddly until you know `workerOnly`.
- **Follow-ups / risks to watch:** Whether the
  model-authored shape should eventually be deprecated in the Modeler's UI while staying
  valid in the compiler — that is a documentation decision, not a compiler one.

## Pros and cons of the options

### Keep the model-authored shape, fix only the mock
- Good: smallest change; no new record type, no compatibility branch.
- Bad: an operator still cannot create an AD connector; the mockup switch stays org-wide
  because there is nothing per-directory to attach it to; AD stays the exception the
  catalogue has to explain.

### AD becomes a Console record, the model-authored shape kept
- Good: AD joins the line it belongs on; per-directory configuration becomes possible,
  which is what the mock needs next; no existing model changes.
- Bad: two shapes to support, and a refusal to keep them apart.

### AD becomes a Console record, the model-authored shape removed
- Good: one shape, no branch, nothing to explain.
- Bad: every existing model needs editing before it deploys again; removes a form that
  is legitimately useful when the directory is itself process data.

## Links

- amends [ADR-0166](0166-active-directory-connector.md), which placed the directory in
  the model
- unblocks a per-directory mockup, which [ADR-0181](0181-ad-connector-mock-mode.md) and
  ADR-0193 could not express
- follows [ADR-0172](0172-entra-id-connector.md)'s worker-only shape and
  [ADR-0106](0106-bmc-remedy-connector.md)'s credential bundle
- constrained by [ADR-0157](0157-worker-processes-supervision-and-console.md): the
  Console's decision has to end in the environment variables an external worker reads
- the secret model is [ADR-0041](0041-connector-management-and-secret-store.md) (references, never values)
