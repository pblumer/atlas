# ADR-0427: Converting a product to a lifecycle process keeps the old processes alive until nothing needs them

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-29
- **Deciders:** Atlas maintainers
- **Open question:** how many orders a production installation holds. The deletion guard
  below scans the order store off the run loop, which was measured at 0.24 s for 10,000
  orders and 11 s for 200,000 on this tree. Whether a scan of that size is acceptable for
  a rare operator act, or whether the index deferred below is needed from the start,
  depends on estates nobody has measured.
- **Question checked:** 2026-09

## Context and problem statement

ADR-0425 lets a catalogue
product bind **one** lifecycle process instead of a provisioning and a deprovisioning
process. It deliberately migrates nothing: an order line freezes its product's binding at
placement (ADR-0312), so lines placed before a product is converted keep the two old
process ids.

A product is converted by publishing a catalogue release in which the item carries the
lifecycle binding. What that reaches:

| Who reads the binding                         | Which binding it gets after conversion |
|-----------------------------------------------|----------------------------------------|
| an order placed after the release             | the lifecycle binding                  |
| a line placed before it and not yet started   | the **two frozen ids** — it is provisioned by the old process |
| a line placed before it and held              | the **two frozen ids** — it is returned by the old process |
| a line whose return failed (`returnFailed`)   | the two frozen ids, on every retry     |
| a reconciliation of an unmanaged right        | the lifecycle binding — it reads the catalogue, because there is no order (`reconcileactions.go`) |

Which line statuses can still **start** one of the two frozen processes, traced through
`api/order` rather than assumed. The code's own word for this is not `Settled()`: that
includes `done` and `failed`, and a done line is exactly one that will be returned.

| Status | May still start the provisioning process | May still start the deprovisioning process | Why |
|---|---|---|---|
| `pending` | yes | yes, once done | `Next` offers only pending lines |
| `blocked` | yes | yes, once done | derived; recomputed to `pending` when its cause is repaired (`Propagate`) |
| `running` | no new start; its instance runs | yes, once done | the instance holds its definition, and deletion already refuses a definition with running instances |
| `failed` | no | **yes** | nothing starts a failed line again (`Next` skips it; no restart path exists), but the repaired instance may still report `done` — `Apply` has no from-status check except for `returned` |
| `done` | no | yes | `Returnable` accepts it; a recertification returns through the same path |
| `returning` | no | no new start; its instance runs | `Returnable` refuses a second return |
| `returnFailed` | no | yes, on every retry | the **only** status from which a revocation is started again |
| `skipped`, `rejected`, `abandoned`, `cancelled`, `returned` | no | no | final; `skipped` is not `Returnable`, because this order never granted it |

`returnFailed` is therefore the only status that *retries* a start. `failed` is the one
that looks final and is not: it keeps the deprovisioning process alive.

So the old processes are **not retired by the conversion**. They stay in use until the
last line that froze them can no longer start them, and for a service held for years that
is years.

What exists today does not protect that period:

- Deleting a process refuses only while it has **running instances**
  (`handleDeleteProcess`). An old deprovisioning process with no instance running at the
  moment deletes without objection, and the next return of a line that froze it fails
  with *no deployed process* — loud, but a revocation that cannot run.
- Deactivating it (ADR-0119) is safe: an explicit create is not gated, so returns keep
  working while nothing else starts it. That is the state an old process should be put in.

## Decision drivers

- **A revocation must stay possible** for every right the inventory says is held.
- **The record says what was in force when a right was granted** (ADR-0312), and a change
  to that is legible as a change (ADR-0359).
- **An operator can tell when an old process is no longer needed**, instead of guessing
  or keeping it forever.
- **The run loop is not stalled** by a scan whose size grows with the estate.

## Considered options

1. **Nothing beyond the draft it builds on:** freeze, and rely on operators not deleting
   the old processes.
2. **Guard, report and an explicit rebinding act.**
3. **Rewrite every open line to the lifecycle binding when the product is converted.**
4. **Replace the old process ids with thin shims** that forward to the lifecycle process.

## Decision outcome

Chosen option: **2 — guard, report and an explicit rebinding act.**

### 1. A frozen binding keeps its process alive

Deleting a process is refused while any line of any order binds it and may still start
it, by the table above: as provisioning process for `pending` and `blocked`; as
deprovisioning process for `pending`, `blocked`, `running`, `failed`, `done` and
`returnFailed`. It is also refused while the **current** catalogue release binds it. The
refusal names how many lines, by product — a count, for the reason ADR-0353 gives counts
rather than lists. Deactivation remains allowed and is what a conversion recommends for
the old pair.

**How the check runs.** Orders are a sidecar store: one JSON file per order, and `All()`
reads every file. Measured on this tree with orders of three lines (about 640 bytes each,
warm page cache, 4 cores): 10,000 orders in 0.24 s, 50,000 in 1.3 s, 200,000 in 11 s.
Real orders are larger (configuration answers, amendments, instances), so these are lower
bounds. That rules out running the scan inside the run loop, where `handleDeleteProcess`
does its work today: at 200,000 orders it would stall the engine for over ten seconds.
The scan therefore runs **off the loop** (a sidecar store may be read there, ADR-0239),
and only the delete itself runs on it.

The gap between the two is safe without a lock, because nothing can add a binding to an
old process in it: a new order freezes the current release, which the in-loop half
checks; a rebinding (§3) only moves lines *away* from an old process; and a retry of
`returnFailed` is a line the scan already counted.

Deleting a process is a rare operator act, so a scan of seconds is acceptable there. An
index from process id to lines is **not** built now. It becomes necessary when the report
of §2 is read routinely, because that is the same scan on a read path; the report is
where the index is introduced, if it is.

### 2. The remainder is visible

The fulfilment report (`fulfilmentreport.go`) gains, per product, the number of lines
still able to start each old process. A maintainer can see when the old pair is no longer
needed, instead of guessing.

### 3. Rebinding is an explicit, recorded act, never a side effect of converting

A maintainer may move the lines of one product that can still start an old process to
the product's current lifecycle binding. Each moved line records what it was bound to
before, who moved it, when and why — the recorded correction ADR-0359 uses for a held
line's details, for the same reason: the record must still say what was in force when the
right was granted, and that it was changed afterwards. A line that is `running` or
`returning` is not moved, because a process has it now.

**Why rebinding is offered at all**, although ADR-0312 freezes the binding so that a
grant is undone by the rules in force when it was made. Its strongest reading holds for
rules — an approval, a ceiling — which must not change under somebody. A deprovisioning
process is not only a rule; it is a conversation with a target system as that system is
**now**. When the target's interface is replaced, the old process cannot succeed any more,
and keeping it is not fidelity but a return that will fail. The default stays frozen; the
act exists for that case, and it leaves a trace.

**What the lifecycle process must accept** to be a valid target for rebinding: its
`deprovision` branch reads the same variables every return and reconciliation hands over
today — `itemId`, `positionId`, `orderId`, `recipient`, `variantId` where there is one,
and `reason` where something other than the orderer asked. A right provisioned by the old
process must be revocable by the new branch; the catalogue cannot check that, so it is a
convention, stated here beside the existing one that every branch reports its outcome.

### Consequences

- **Positive:** an old process cannot be deleted from under a right that still needs it.
- **Positive:** the moment an old pair can be retired is visible, per product.
- **Positive:** where a target system changed, lines can be moved to the new process
  without losing what they were granted under.
- **Negative / trade-offs accepted:** deleting a process gains a refusal it does not
  have today. An operator cleaning up old versions meets it; the message says which
  products still need the process and how many lines.
- **Negative:** the guard is a full scan of the order store, measured above.
- **Follow-ups / risks to watch:**
  - The same full scan already runs on the loop elsewhere — `Store.For` walks every order
    for one person's shop listing — which is a scaling limit of the order store in
    general, not of this record, and is left to its own.
  - Rebinding assumes the new branch can revoke what the old process granted. Where the
    lifecycle process represents the right differently in the target system (another
    group, another role), rebinding is a data migration in that system first, and no
    record in Atlas can make it one.
  - `Line.Valid` was not traced for further transition checks; the table above is
    therefore conservative about what may still reach `done`.

## Pros and cons of the options

### Option 1 — freeze and rely on operators
- Good: no new code.
- Bad: one routine cleanup deletes the process a return needs, and nothing says which
  processes are still needed.

### Option 2 — guard, report and rebinding (chosen)
- Good: protects every right the inventory holds; makes retirement visible; handles a
  replaced target system without rewriting history silently.
- Bad: a scan of the order store; a new refusal on delete; a new act to build.

### Option 3 — rewrite all open lines at conversion
- Good: the old processes can be retired immediately.
- Bad: rewrites what every line was granted under as a side effect of editing the
  catalogue — the change ADR-0312 freezes against — with no record of who decided it.

### Option 4 — shims under the old ids
- Good: no data changes; old lines keep working.
- Bad: a hidden indirection: the diagram under an old id no longer says what runs, and
  each shim is again a process started by an untriggered create, forwarding to the
  lifecycle process through a trigger.

## Links

- builds on ADR-0425
- relates to ADR-0312 (frozen bindings) and ADR-0359 (recorded corrections)
- relates to ADR-0119 (deactivating a process), ADR-0239 (off-loop reads) and
  ADR-0353 (counts rather than lists)
