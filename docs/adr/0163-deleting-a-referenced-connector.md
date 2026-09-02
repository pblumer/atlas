# ADR-0163: Deleting a connector deployed models still reference — and keeping a table inside its card

- **Status:** Accepted
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0158](0158-a-connector-reference-that-explains-itself.md) started from a parked
mail task reporting `mail: no connector registered as "Patrick Blumer"` about a
connector that was configured and visible in the list. It fixed the message, and it
added a check at deploy: `CompiledProcess.ConnectorRefs` enumerates every connector a
model references, the deploy resolves each against the connector store, and what will
not resolve comes back as a warning beside the success.

That check has run in exactly one place ever since. `handleDeleteConnector` runs none:

```go
func (s *Server) handleDeleteConnector(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.do(func() {
		if delErr = s.connectors.Delete(id); delErr != nil { return }
		delErr = s.rebuildConnectorRegistries()
	})
	w.WriteHeader(http.StatusNoContent)
}
```

So the same failure ADR-0158 was written about can still be produced — from the other
end, and by the operator rather than by the modeler. Delete a connector that three
deployed processes reference, and: the registry rebuilds without it, nothing says
anything, `204 No Content`, and every token that later reaches one of those tasks parks
with `no connector registered as "…"`. Which is now, thanks to ADR-0158, a *true*
sentence — and still a useless one, because the thing an operator needs to know is that
they deleted it thirty seconds ago.

The asymmetry is not an oversight of symmetry for its own sake. A deploy that references
a connector which does not exist yet is legitimate and common (deploy first, provision
later, or deploy to an environment that provisions on its own schedule), which is why
ADR-0158 warns and does not refuse. Deleting a connector that models are actively
resolving through is the opposite: it takes something away that is in use, and it is not
undone by waiting.

## Decision drivers

- **The check already exists.** `ConnectorRefs` and the connector store are both here;
  what was missing was calling them at the second moment it matters.
- **Warn where it is speculative, refuse where it is destructive.** Deploying early is a
  plan; deleting something in use is a loss.
- **A count is not actionable.** "3 processes use this" tells an operator they should
  worry, not what to do. The processes themselves, their versions, and how many
  instances are running on them is a decision they can actually make.
- **The API is the gate, not the UI.** An operator deleting through the HTTP API, the
  MCP tools, or a script must hit the same check as one clicking Delete.

## Considered options

1. **Leave it, and rely on the deploy warning.** It catches nothing here: the models
   were deployed while the connector existed.
2. **Refuse the delete when deployed models reference the connector, with an explicit
   override**, and show the same usage on the connector row so it is known before the
   button is pressed.
3. **Refuse unconditionally.** No override — the connector must be unreferenced before
   it can go.
4. **Soft-delete (disable and hide).** The record survives, so nothing parks.

## Decision outcome

Chosen option: **2**.

**`connectorUseIndex` reads the deploy check backwards.** Instead of "what does this
model reference that is missing?", it asks "what would break if this connector were
gone?": one pass over the deployed definitions, indexing each `ConnectorRefs` entry by
`kind/name`, with the per-definition active-instance count of ADR-0080 beside it. One
pass rather than one per connector, because the list view needs the answer for every
row at once. It reads the deployment map and the store, so it runs on the run-loop
goroutine (I3).

**`DELETE /api/v1/connectors/{id}` refuses with 409 and the list**, naming each
referencing process, its version, the elements that resolve through the connector, and
how many instances are running on it — not a count. `?force=true` proceeds. This is a
breaking change to the endpoint, and a deliberate one: the previous behavior's only
signal was a parked token some time later.

**The connector row says it before the button is pressed.** `GET /connectors` carries
`usedBy` per record, and the Console renders "Used by Zahlung v3, Mahnung v1 · 2 running
instances" under each connector — or "Referenced by no deployed process", which is the
only state in which deleting is uneventful and is worth saying plainly.

**The UI asks twice, and the second question carries the answer.** The first confirm is
the ordinary one. If the server refuses, the second names the processes, the running
counts, the elements, and what will actually happen to them — then forces. It does not
pre-flight the check itself: the usage on the row can be stale by the time the button is
pressed, and the server is the one that decides. `deleteConnectorFlow` and
`connectorUsageHTML` live in `api/web/connectordialog.js` rather than in `app.js`,
because `app.js` boots the whole console on import and anything left in it is only ever
exercised by hand.

**`apiRaw` now attaches `status` and the decoded `body` to the error it throws.** A
refusal that names what is in the way is only useful if the caller can read it; the
message is unchanged, so nothing that only reports the failure notices.

**And a table stays inside its card.** Adding the usage line was the second time in a
week that a console table grew — [ADR-0160](0160-fix-the-connector-from-the-incident.md)
put a third action button on every incidents row, which pushed that table's
*min-content* width past the card holding it. A table cannot be laid out narrower than
its min-content width, so it was drawn past the card's right edge: border and header
rule stopping short, buttons hanging in the page beside it. Two answers, and the first
is the one that matters: **any card holding a table scrolls it horizontally**
(`.card:has(> table)`), a rule about every table rather than about the one that
overflowed, because the next column added to any of them would do this again. Second,
the incidents row adopts the console's own convention — one visible action (`Resolve…`)
plus the `⋯` overflow menu that every other table there uses — so a fourth way out of
an incident costs no width at all. The incident block *beside a diagram* is not a table
and keeps its buttons visible, wrapping them instead, because that panel is resizable.

### Consequences

- **Positive:** no console table can silently draw outside its card again, whoever adds
  the next column. And the ADR-0158 failure can no longer be produced by the delete path
  without the operator having read what it would cost. The connector list answers "what
  is this for?", which is useful well before anyone tries to delete anything —
  particularly for the connector nobody remembers configuring. The check runs in the
  API, so scripts and MCP hit it too.
- **Negative / trade-offs accepted:** `DELETE` can now return 409, which is a breaking
  change for any caller that assumed 204. The usage index walks every deployed
  definition's connector references per list request — bounded by deployments × refs,
  small in practice, and on the run loop, so a pathological deployment count would be
  felt there first. And the guard is about *deployed models*, not live tokens: a
  connector referenced only by a deactivated definition (ADR-0119) with nothing running
  still refuses, which is a false alarm an operator resolves with one `force=true`.
  `:has()` is a modern-browser selector; the console already requires one (ES modules,
  no build step), but a browser without it loses the scroll container and gets the old
  overflow back rather than a broken page.
- **Follow-ups / risks to watch:** **disabling** a connector parks exactly the same
  tasks and stays silent — it is reversible, so it is not refused, but the toggle should
  say what it will park. The same usage index would answer **"can I delete this
  process?"** and **"what is this vault secret for?"** (ADR-0155 already answers the
  latter from the connector side only). And the **rename trap** of ADR-0158 is now the
  only way left to break a resolved reference silently: the API has no rename, so it
  takes a delete plus a create under a new name — the delete half of which this record
  now catches.

## Pros and cons of the options

### Leave it, rely on the deploy warning
- Good: nothing to build.
- Bad: the warning cannot fire for this case by construction — the models were deployed
  while the connector existed. It leaves the operator to discover their own action from
  a parked token, which is the exact failure ADR-0158 exists to prevent.

### Refuse with an explicit override, and show usage on the row
- Good: safe by default, possible by decision; the operator sees the cost twice — once
  while browsing, once at the moment of deleting. Reuses the machinery already built.
- Bad: a breaking change to a `204` endpoint, and a false alarm for a connector whose
  only referencing definition is deactivated and idle.

### Refuse unconditionally
- Good: the strongest guarantee, and no `force` flag to reach for reflexively.
- Bad: leaves no way to remove a connector for a model that will never run again short
  of deleting the definition first. An operator who knows what they are doing must not
  be blocked by a rule with no exit.

### Soft-delete
- Good: nothing ever parks, because the record never leaves.
- Bad: it is disable wearing a different name — which Atlas already has — and it makes
  the connector list a graveyard of records that are gone but not gone. "Deleted" should
  mean deleted.

## Amendment (2026-09-02): the usage is a count, and the actions are a menu

The row this record designed was right about *what* to say and wrong about how much of
it to say on the row. The example it gives — "Used by Zahlung v3, Mahnung v1 · 2 running
instances" — is two processes. A shared mail worker on a real instance is twenty-one
deployed definitions of eight processes, and spelled out that is fourteen wrapped lines
of links in one cell. The endpoint above them, the status pill beside them and the
actions after them end up an inch apart, and the row that is hardest to read is the tall
one — which is the row something is wrong with, because a worker nothing references is
a two-line row.

**The row carries the numbers; the list is one click behind them.** `connectorUsageHTML`
now renders a chip — "Used by **8** processes · **21** deployed versions · **1** running
instance" — and `openConnectorUsage` opens the list it stands for. Definitions and
processes are counted separately and said separately, because they are different numbers
and only agree when nothing was ever redeployed; "used by 21 processes" for eight of them
would overstate the very blast radius this record exists to state honestly. The dialog
groups by process and puts the newest version first, so a model redeployed all afternoon
is one entry with a version list rather than eight entries that read as eight processes,
and past a handful of them it filters. It reads the `usedBy` already on the record — the
same set the row counted and the same set the refusal names — so it opens instantly and
the three cannot disagree.

What leaves the cell's *text* is put back where only the filter reads it: the Worker cell
carries a `data-filter` naming those processes, so typing a process name in the column
filter still finds the workers it runs through (`table.js`).

**And the actions moved into the row's ⋯ menu**, which is where every other table in the
console keeps them and where this record's own closing section was already heading. A
configured worker offered up to seven buttons — Provision access, Events, Test, Share,
Edit, Disable, Delete — a wall of identical blue that made every row look the same and
put the table's min-content width back where the `.card:has(> table)` scroll rule below
had to catch it. In the menu they cost no width, group by what they are, and the row is
left saying what it *is*: name, type, who may configure it, where it points, what it is
used by, and whether it works.

## Links

- completes [ADR-0158](0158-a-connector-reference-that-explains-itself.md): that record
  checks a model's references at deploy; this one checks a connector's referents at
  delete, which is the same check from the other end
- uses `CompiledProcess.ConnectorRefs` (ADR-0158) and the per-definition instance
  counters of [ADR-0080](0080-runtime-aggregate-counters.md)
- extends [ADR-0160](0160-fix-the-connector-from-the-incident.md)'s shared connector
  module with the delete flow, for the same reason it exists at all — one place, and
  one that a test can reach
- relates to ADR-0036/0041 (a model refers to a connector by name only, which is what
  makes the reference resolvable in both directions and breakable from either end)
