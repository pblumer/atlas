# ADR-0432: The event feed is narrowed by the catalogue that maintains the product

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0430](0430-the-event-feed-has-its-own-role-and-token-scope.md) gave the event feed its
own role, `feedreader`, and its own token scope, `events`. It refused a `reach` on an
`events` token, because nothing read one: every token read every catalogue's facts. It named
the narrowing as the follow-up — "a billing system for one catalogue would want only that
catalogue's rows" — and said it needs a feed row to carry its catalogue.

A row does not carry one. What the log holds of each fact is a principal, an item, a variant
and, where an order produced it, the order (`model.ActionOutcomeValue`,
`model.EntitlementValue`, `model.EntitlementHistoryValue`). Two catalogues can be reached
from there, and they answer different questions:

- **The item's home** (`catalog.Item.HomeCatalog`): the one catalogue whose scope governs
  editing the product. An item appears in several catalogues and is maintained by exactly
  one; the others include it read-only. Items are never deleted (`catalog.State`), so every
  row that names an item reaches its home.
- **The catalogue the order was placed in**: the order names a release, the release names
  its catalogue. A right a commissioning load adopted has no order (ADR-0333), and an order
  may be deleted by retention years before the right it produced ends
  (`api/order/inventory.go`).

## Decision drivers

- **A leaked narrowed credential leaks its catalogues and no others.** A billing system's
  token for one catalogue must not read who holds the products of another.
- **One meaning for every row.** A consumer must be able to say in one sentence which rows it
  is given.
- **Every row has an answer.** A row the narrowing cannot place must not be guessed into a
  catalogue, and must not leave a reader waiting for a row it will never be given.
- **The authority to grant matches what is shown.** Whoever mints a narrowed token must
  already be entitled to see what it reads.
- **The log does not change.** No fact gains a field, so replay, the record format and the
  rows already written are untouched.
- **A narrowed page is bounded work.** A token whose catalogues hold one row in a million
  must not make one request walk the whole feed.

## Considered options

1. **The item's home catalogue, resolved when the page is read.**
2. **The catalogue the order was placed in**, resolved when the page is read through the
   order and its release, with the home for a right no order produced.
3. **A catalogue frozen into each fact when it is written**, as a new field on the outcome
   and entitlement records.
4. **No narrowing in Atlas**: the consumer reads the whole feed and drops what is not its
   own.

## Decision outcome

Chosen: **option 1.**

**What a row belongs to.** Every row of the feed belongs to the catalogue that maintains its
product: the home of the item the row names. The envelope says so in its data as
`homeCatalog`, for every reader, so a consumer of the whole feed can partition it the same
way. An item the catalogue store does not have has no home; its rows carry none.

**The reach.** An `events` token may be minted with a `reach` naming catalogues
(`catalogReach` in `api/apitokens.go`). Each must exist, and the minter must maintain it
(`catalog.MayMaintain`): being in a catalogue's audience is not enough, because the feed of a
catalogue is the map of who holds its products — the estate behind it, not the shop in
front. A reach is optional: without one the token reads the whole feed, as every `events`
token minted before this did. The landscape scope's reach still names projects; what a reach
names follows from the scope.

**The filter.** The feed (`handleListEvents` in `api/eventfeed.go`) answers a reader with a
reach only the rows whose `homeCatalog` it names. A row with no home is answered to no
narrowed reader. The rows a reader is not given are passed over, and its cursor moves past
them, so the next page does not read them again and the reader never waits on a row that is
not its own. A page reads at most 10 000 rows (`eventFeedScan`): a narrowed page cut there
answers what it found, `next` at the last row read, and `more`, so it can be short, or empty
with `more` set. A catalogue store that cannot be read fails the page with 500. It is never
read as "no home", because passing a row over moves the reader's cursor past it for good.

**Read time, not write time.** The home is read when the page is, once per product per page,
from the catalogue store off the run loop, as the approval page already reads it
(`Server.catalogStore`). An item moved to another home moves its rows with it, as its
editing already moved.

**MCP** is unchanged: the feed stays outside it (ADR-0429 §5), and minting is an admin
route.

### Why not the others

**Option 2** has the strongest case where a catalogue is a tenant's shop. A subsidiary's
catalogue that offers the group's laptop would bill for every laptop ordered *through it*,
and under option 1 it is given none, because the laptop is maintained elsewhere. But it
cannot give every row one meaning. A right a commissioning load adopted was ordered nowhere,
so it falls back to the home (ADR-0333), and a consumer narrowed to a catalogue is then
given "orders placed here, and adopted rights to products maintained here", which is
neither the shop nor the estate. An order deleted by retention leaves the right's rows
without a shop, so a row that was placed yesterday is unplaceable tomorrow. Reading it costs
an order and a release per row, and a release carries the whole snapshot of its items. If a
tenant's billing case arrives, it is a second dimension of reach (the shop), added beside this
one, not a replacement for it.

**Option 3** gives every row an answer that never changes and costs nothing to read. It
changes two log records, and the rows already written would still need option 1, so the feed
would carry two meanings split at an upgrade. It would also freeze the home a product had
when it was granted, while editing and the catalogue's own reports follow the current one.

**Option 4** is the status quo, and it is what the follow-up was written to end. A
consumer that is handed everything and drops most of it holds everything: a leaked billing
credential for one catalogue would expose who holds the products of every catalogue.

### Consequences

- **Positive:**
  - A billing system or a CMDB for one catalogue holds a credential that reads only the
    facts about that catalogue's products.
  - Every event names its product's catalogue, so a reader of the whole feed can partition
    it without a second credential to read the catalogue.
  - No record of the log changed, so the narrowing applies to every row already held.
- **Negative / trade-offs accepted:**
  - `homeCatalog` is the one value in the data that is not frozen in the fact. A row
    re-read after its product moved to another home names the new one under the same
    `id`. Moving a product needs edit rights on both catalogues (`api/catalog/service.go`), so
    it is a deliberate act of the people who maintain them, not something a reader causes.
  - A product offered by several catalogues is followed only by its home. A catalogue that
    offers another's product does not read its facts.
  - A narrowed reader can be answered an empty page with `more` set, and has to keep
    asking, where a reader of the whole feed never is.
  - A page reads the catalogue store once per product it names, and a catalogue store that
    cannot be read now fails every reader's page, not only a narrowed one's. The
    alternative, leaving `homeCatalog` out when a read fails, would give one event two
    different bodies depending on a disk read.
- **Follow-ups / risks to watch:**
  - **The shop as a second dimension** (option 2), if a tenant's billing case arrives.
  - **Push delivery.** The Worker that pushes the feed (ADR-0429 §5, prepared) should honour
    the same reach.

## Links

- [ADR-0430](0430-the-event-feed-has-its-own-role-and-token-scope.md): the feed's role and
  token scope, and the follow-up this record builds.
- [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) §5: the feed.
- [ADR-0410](0410-a-peer-credential-carries-the-reach-a-membership-cannot-give-it.md): a
  credential's reach, and that a minter cannot grant a reach they do not hold.
- [ADR-0194](0194-api-tokens.md): API tokens and scopes.
- [ADR-0315](0315-portal-roles-and-responsibilities.md): an item is edited only through its
  home catalogue, which every other catalogue includes read-only.
- [ADR-0333](0333-inventory-commissioning-load.md): the adopted rights no order produced.
