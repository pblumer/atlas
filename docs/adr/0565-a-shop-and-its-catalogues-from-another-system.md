# ADR-0565: A shop and its catalogues from another system — a person's grant for `…/shop`, and a catalogue maintained where it is published from

- **Status:** Proposed
- **Implementation:** Partial
- **Date:** 2026-11-24
- **Deciders:** requested 2026-11-24 by the maintainer, who wants the wiki Orbithek (in Atlas code still wikiwikibangbang, ADR-0491, ADR-0498, ADR-0510, ADR-0512) to show Atlas's shop to its people — order, follow orders, decide approvals, work tasks, each as themselves — and to let product managers keep catalogues as wiki pages and publish them into Atlas through the catalogue document import (ADR-0436). Atlas stays the system of record for releases, orders, inventory, approvals and fulfilment. The maintainer decided that only people with an account may order: there is no anonymous portal. Proposed for the maintainers' acceptance.
- **Open question:** what a wiki-maintained catalogue loses on its next publish. The import replaces a record with the document's (ADR-0436); a field Atlas has and the wiki does not map — a product's actions, its target references, its service level, its configuration form, an approval rule the wiki does not write — is empty in the document and therefore empty after the publish. While the catalogue is maintained in the wiki nobody can set those fields in Atlas either, because §5 refuses the write. Either the wiki carries every field it does not map through unchanged (it can read them from the export), or the import learns to keep fields a document leaves out for a catalogue maintained elsewhere. Which of the two is right depends on how much of the product the wiki will ever model, which nobody knows yet.
- **Question checked:** 2026-11

## Context and problem statement

ADR-0510 §3 gave a person's tasks a resource of their own, `…/tasks`, so a connected system works a person's tasks with that person's grant and nothing else; ADR-0512 lets an administrator connect such a system once and pre-consent its people's grants. ADR-0550 opened the shop's customer side to an agent acting as the connected person. ADR-0436 imports a whole shop as one document, all or nothing, and ADR-0467 compares it with what the last import brought.

Two needs of a system like the wiki are not met by that:

- **To show a person the shop.** The wiki wants to list the catalogues a person may order from, place and follow their orders, let them change or cancel a line, take a customer's action, decide the approvals waiting for them — and keep working their tasks. A grant for `…/tasks` reaches none of the shop; a grant for the whole server reaches everything the person can do, the engine and the administration included for an administrator; a grant for `/mcp` reaches the transport only.
- **To publish catalogues it maintains.** A product manager who keeps a catalogue as wiki pages publishes it through `POST /api/v1/catalogs/import`. That import replaces every record of the same id. If anybody changes the catalogue in Atlas's own editor in between — renames a product, adds an edge — the next publish overwrites the change without a trace, and nothing tells either side.

The question: **what is the smallest surface through which another system shows a person the shop as that person and publishes the catalogues it maintains — and how does Atlas keep an edit made in one place from being silently replaced by the other?**

## Decision drivers

- **Atlas decides, for the person.** Every order, approval and task is decided as for the person in the shop and over MCP (ADR-0550, ADR-0317, ADR-0421); nothing lets the system decide instead (ADR-0512's driver, ADR-0510 option 4).
- **A credential reaches what its holder needs** (ADR-0194). The reach of a scope stays readable in `api/apitokenscope.go`, and handlers never read a scope (ADR-0493).
- **One place of maintenance per catalogue.** Two writers on one record with no coordination means the later one wins in silence; the person whose change vanishes is never told.
- **Say where, not just no.** A refusal names the system and the address where the change belongs.
- **Reversible by an administrator.** A catalogue that is no longer maintained elsewhere must be maintainable here again without editing files.
- **The catalogue stays design-time state** (ADR-0312): nothing here reaches the event log, the processor or recovery.

## Considered options

1. **One more person resource, `…/shop`, whose reach is the tasks plus the shop and catalogue maintenance; and a mark on each catalogue a system's import writes, which every other write respects.**
2. **Two person resources, `…/shop` for the customer side and `…/catalogue` for maintenance.**
3. **A system grant that acts for its people** — the connected system orders or publishes naming the person.
4. **No lock: last writer wins**, as every import does today, and the Console warns.
5. **Merging imports**: the import keeps what was edited here and takes only what the document changed.

## Decision outcome

Chosen option: **1**, because it adds one canonical resource built exactly like `…/tasks`, keeps every decision on the person's own grant, and turns the silent overwrite into a refusal that names where the change belongs — with one administrator act to undo it.

### 1. The resource `…/shop` and the scope `shop`

A fifth canonical resource beside the server, `/mcp`, `/tasks` (ADR-0510 §3) and `/system` (ADR-0512 §2): the server's origin followed by `/shop`, with its protected-resource metadata at `/.well-known/oauth-protected-resource/shop`. A grant for it carries the scope `shop`, which is not mintable — it follows from the resource the person approved, as `tasks` does. Its reach is every pattern of `tasks` and:

- the customer side, as the portal and MCP drive it: `GET /api/v1/shop/catalogs`, `GET /api/v1/shop/catalog`, `POST /api/v1/shop/releases/{release}/items/{item}/form-data/{field}/query`, `GET /api/v1/shop/tasks`, `GET /api/v1/shop/orders/{id}/lines/{position}/progress`, `GET /api/v1/catalog-products/{id}/picture`, `GET /api/v1/catalog-products/{id}/content`, `GET /api/v1/catalog-products/{id}/content-images/{image}`, `GET /api/v1/catalogs/{id}/logo`, `POST /api/v1/orders`, `GET /api/v1/orders`, `GET /api/v1/orders/{id}`, `POST /api/v1/orders/{id}/cancel`, `POST /api/v1/orders/{id}/lines/{item}/cancel`, `…/details`, `…/return`, `…/change`, `GET /api/v1/orders/{id}/lines/{item}/actions`, `POST /api/v1/orders/{id}/lines/{item}/actions/{action}`, `GET /api/v1/approvals`, `POST /api/v1/approvals/decide`;
- catalogue maintenance: `GET /api/v1/catalogs`, `GET /api/v1/catalogs/{id}`, `GET /api/v1/catalogs/{id}/document`, `GET /api/v1/catalogs/{id}/releases`, `POST /api/v1/catalogs/import`, `PUT /api/v1/catalog-products/{id}/picture` — a document carries no pictures (ADR-0436), so the picture is the one write beside the import.

Each route keeps its own role and object check: the grant of somebody who is not a product manager reaches the import and is refused by it. Not in the reach: the three order routes ADR-0550 keeps off MCP — reporting a line's outcome, starting a line, an approver's decision on one line — which are the fulfilment's and the approval process's own calls; and the order builder's `next` and `round` reads, which the portal's basket uses and a connected system does not need. The `--mcp-shop=false` switch (ADR-0550) is about agents over MCP and does not apply to a shop grant.

The consent page says what is allowed: see the catalogues and order from them as oneself; follow and change one's orders and decide one's approvals; work one's tasks; and, for whoever maintains catalogues, import and publish them, which then makes them maintained in that application. Pre-consent of a connected system (ADR-0512 §3) applies to `…/shop` exactly as to `…/tasks`. The AI-access page labels such a grant "the person's shop and tasks only".

On a server with the catalogue switched off (ADR-0434) the resource stays published and a shop grant still reaches the tasks; the shop's routes answer as that switch has them answer.

### 2. Which system a request comes through

A person's OAuth grant becomes a principal carrying `ClientID` and `ClientName` (`httpapi.Principal`), the client the grant belongs to and the name it shows. The catalogue service is told, by a function the server supplies (`Service.Publisher`, `api.catalogPublisher`), which client a request publishes for: the client of a grant whose scope is `shop`, and nobody for every other principal — a session, a tasks grant, a grant for the whole server, an API token. The scope is read by the server, not by the catalogue package, which keeps ADR-0493's rule that handlers never read a scope.

### 3. A catalogue is maintained in a system

`Catalog` gains `maintainedIn`, `{client, name, url}`: the OAuth client id, the client's name as the grant shows it, and an https address where the catalogue is maintained (optional). It is stored with the catalogue in its sidecar store, as every other field.

- **Only an import through a shop grant sets it.** Every catalogue such an import writes — created, updated, or the catalogue `?into=` names — is marked with the grant's client and name and the address the document gives under `catalogs[].maintainedIn.url`. The address must be an absolute https URL with a host and no user information, or absent; anything else refuses the import (422). What else a document says under `maintainedIn` — a client, a name — is ignored: a document cannot claim to come from a system.
- **Any other import ignores the document's `maintainedIn`**, so an export moved to another installation arrives maintained there.
- **The export carries it** (`GET /api/v1/catalogs/{id}/document`), so whoever holds the file sees where the catalogue lives.
- **It takes part in the import's comparison** (ADR-0467): a first publish from a system over a catalogue made here differs from it, and is asked about rather than written unless the importer says to.

### 4. An import may not overwrite a catalogue maintained elsewhere

The import is refused — with every other problem, before anything is written, a dry run included — when it would write a catalogue maintained in a system, or a product whose home is one (or a product it would take from one), unless it comes through that system's own shop grant. The problem carries `maintainedIn`, and the answer is `409` (authority problems still answer `403` first): it is the conflict of two places of maintenance, not a malformed document. A shop grant of a *different* system is refused the same way.

### 5. Every other write is refused while it is maintained elsewhere

A write to a catalogue maintained in a system, or to a product whose home is one, answers `409 {"error": "<catalogue or product> is maintained in <name> (<url>); change it there …", "maintainedIn": {…}}`, unless the request comes through that system's shop grant. The check is one function (`heldElsewhere`, through `mayWrite` beside `mayEdit` in `api/catalog`), asked inside the same run-loop turn as the write it guards and after the authority check, so a caller who may not change the catalogue learns nothing about where it is maintained. Held:

- `PATCH /api/v1/catalogs/{id}` — texts, rank, languages, items, edges, groups, members, reporting groups, structure;
- `POST /api/v1/catalog-products` — saving a product whose home, or former home, is held;
- `POST /api/v1/catalogs/{id}/releases` — publishing (the system publishes through the import's `publish`);
- `PUT` and `DELETE /api/v1/catalog-products/{id}/picture`; `POST /api/v1/catalog-products/{id}/content` and `…/content/unlink` — the binding is the document's;
- `POST /api/v1/catalogs/{id}/import` — the ArchiMate import;
- `DELETE /api/v1/catalogs/{id}` and `DELETE /api/v1/catalog-products/{id}` — deleting for good, which the next publish would undo, and stripping a deleted product from a held catalogue;
- `POST /api/v1/catalogs/import`, as §4 says.

Open on purpose: creating a catalogue; the theme and logo, an administrator's (ADR-0316), which no document carries; pruning releases, an administrator's housekeeping of releases nobody orders against; a content refresh, which fetches what the document's binding names as the recheck does on its own (ADR-0491); a rebind, which moves orders — Atlas's — and writes no product; the import's table of names and its answers. `TestEveryCatalogueWriteIsHeldOrOpenOnPurpose` holds every write route of the catalogue to one of the two lists, so a route added later has to be decided.

### 6. Taking it back

`DELETE /api/v1/catalogs/{id}/maintainer`, an administrator's, clears `maintainedIn`: from then on the catalogue is maintained here, and every write §5 refused goes through. It advances the catalogue's revision and is recorded in the audit trail as `catalog.maintainer_cleared` with the system it was taken from. The system's next publish is then compared like any import (ADR-0467): unchanged parts are left alone, and a change is a question (`409`, `undecided`), not an overwrite. A catalogue maintained here already is answered as it is. Not offered as an MCP tool: it decides between two systems which one wins, and the refusal it lifts exists to stop an edit an agent was asked to make from being overwritten.

### 7. The Console

The catalogue editor shows, for a catalogue maintained elsewhere, a banner — "This catalogue is maintained in <name>", why changes are made there, a link to the address when it is https — and disables every control of the sections the server would refuse (products, descriptions, relations, publishing, settings, sharing, customer responsibles, deleting for good), keeping the appearance and the export open. An administrator sees **Take the catalogue back**, which asks first. The texts go through the locale catalogue in German and English.

### As built

- §1: `api/apitokenscope.go` (`apiScopeShop`, `apiTasksReach`, `apiShopReach`), `api/oauthserver.go` (`oauthShopResource`, `isCanonicalResource`), `api/oauthgrantstore.go` (`oauthGrant.scope`), `api/server.go` (the metadata route), `api/connectedsystems.go` (`preconsented`), `api/web/oauth-consent.html`, `api/web/aiaccess.js`.
- §2: `api/httpapi/httpapi.go` (`Principal.ClientID`, `ClientName`), `api/auth.go` (set where a grant becomes a principal), `api/catalogmaintainer.go` (`catalogPublisher`), `api/catalog/service.go` (`Service.Publisher`).
- §3–§5: `api/catalog/catalog.go` (`Maintainer`, `Catalog.MaintainedIn`), `api/catalog/maintained.go` (`heldElsewhere`, `mayWrite`, `HeldMessage`, `RefuseHeld`, `maintainerURL`), the write paths in `api/catalog/service.go`, `image.go`, `content.go`, `archimate.go`, `document.go` (`planImport`, `refuseImport`, `DocumentProblem.MaintainedIn`), and `api/catalogpurge.go` with `heldPurge` (`api/catalogmaintainer.go`).
- §6: `Service.TakeBack`, `api/catalogmaintainer.go` (`handleTakeBackCatalog`), the route in `api/openapi.go`, `logging.CatalogMaintainerCleared`, `mcp/tool_registry_drift_test.go` (omitted, with the reason).
- §7: `api/web/catalog-admin.js` (`maintainedBanner`, `holdEditing`, `wireTakeBack`), the `catalog.maintained.*` keys in `api/web/locales/`; the handbook card `#shop-im-anderen-system`; `docs/integrations/connected-systems.md`.
- Tested by `api/catalog/maintained_test.go` (the mark, the address, every held write and the take-back, the system's own picture, the appearance left open, the store reopened), and in `api`: `TestTheShopReachIsTheTasksReachAndTheShop`, `TestTheShopIsACanonicalResource`, `TestOnlyAShopGrantOfAClientPublishesForIt`, `TestEveryCatalogueWriteIsHeldOrOpenOnPurpose`, `TestAShopGrantShopsAsThePersonAndPublishesTheCataloguesItMaintains` (end to end: a connected system, pre-consent for `…/shop`, the reach, an import marking the catalogue, the Console's writes and the purge refused, the take-back), `TestTheShopScopeIsNotMintable`, and the Console's source in `api/catalogmaintained_ui_internal_test.go`.

### Consequences

- **Positive:**
  - A connected system shows the shop and the tasks with one person grant, and Atlas decides every order, approval and task as it decides in the shop.
  - A product manager maintains a catalogue in one place; a change attempted in the other is refused with where to make it instead, never lost.
  - An administrator can always end the arrangement with one act, which the audit trail records.
- **Negative / trade-offs accepted:**
  - The shop grant reaches the catalogue maintenance routes for everyone; what a person who is not a product manager gets there is the route's refusal. Two resources would have kept that off the consent page of people who never maintain a catalogue (option 2).
  - A product homed in a catalogue maintained here may still be written by a system's import, as by any import of a person who maintains it; only the catalogues the document carries are marked.
  - While a catalogue is maintained elsewhere, its fields the system does not map cannot be set anywhere (the open question).
  - The first publish from a system over a catalogue made here is a question (ADR-0467), which the system has to answer with `?overwrite=`.
- **Follow-ups / risks to watch:**
  - The open question: keeping unmapped fields across a publish.
  - Whether a claim of maintenance by an import should be audited too, beside the take-back.

### Scale-out readiness

- **State ownership:** `maintainedIn` lives in the catalogue's sidecar store, which the run loop owns as the single writer of design-time state (I3); grants live in the OAuth grant store as today. No partition owns either, and nothing reaches the event log or `applyToState` (I1, I2, I4, I6 untouched).
- **Routing and service boundaries:** the check runs in the catalogue service, on the request's principal, in the same run-loop turn as the write; no route needs another partition or another node's state.
- **Durability and failover:** the mark is written with the catalogue record by the store's atomic write and fsync; it survives a restart with the record (`TestWhereACatalogueIsMaintainedSurvivesReopeningTheStore`). A replicated metadata service (ADR-0175's target) carries it as one more field of the record.
- **Idempotency:** importing the same document again writes nothing; taking back a catalogue maintained here writes nothing.
- **Cross-partition communication:** none.
- **Query consistency:** the decision reads the record it guards in the same single-writer turn, so a refusal and a write can never disagree about the mark.
- **Migration seam:** standalone mode is unchanged; a clustered design-time store performs the same check in the transaction of the write. No host-local singleton is introduced.

## Pros and cons of the options

### Option 2: two resources, `…/shop` and `…/catalogue`

Strongest form: a person who only orders never sees "import and publish catalogues" on their consent screen; a leak of a customer's grant cannot reach the import route at all, even to be refused; and the reach of each scope says one thing.

- Good: narrower consent for most people; each scope's name says its whole reach.
- Bad: a system that does both — the case that motivates this record — asks every product manager for two grants and holds two tokens per person, and pre-consent and withdrawal double with them. The import is refused to whoever lacks `productmanager` anyway, so the extra resource buys a shorter consent text, not a narrower power. Splitting later stays possible: a second resource would take routes out of `shop`'s reach, not change what any route decides.

### Option 3: a system grant acting for its people

Strongest form: one credential, no token per person, and the system names the person on each order; Atlas still checks that person's rights.

- Good: simplest for the system.
- Bad: refused by ADR-0512's driver and ADR-0510's option 4 — Atlas would decide on an identity another system asserts and cannot verify, and a leak of the one credential orders and decides approvals as anybody.

### Option 4: no lock, last writer wins

Strongest form: it is what the import does today and what every other importer lives with; ADR-0467's comparison already asks before replacing work done here when a base exists; and nothing is ever refused.

- Good: no new state, no new refusal, nothing to take back.
- Bad: the comparison only asks the importer — the wiki — about a change made in Atlas, and a system that publishes on every save answers `overwrite` by design; the person who edited in Atlas is never told. A warning in the Console says "this will be overwritten" and still lets the edit be made and lost.

### Option 5: merging imports

Strongest form: both places stay editable; the import takes only what the document changed since the base and keeps what was edited here, field by field, as a three-way merge.

- Good: nobody is refused anything.
- Bad: a catalogue is a graph — items, edges, members, bindings — and a field-wise merge of two edited graphs can produce one neither side wrote and that publishing refuses, far from either edit. Conflicts still need a person to decide, and the wiki has no screen for Atlas's fields. It is a much larger mechanism to answer a question one place of maintenance does not raise.

## Links

- extends ADR-0510 §3 (a person resource for OAuth grants) and ADR-0512 §3 (pre-consent) with `…/shop`
- follows ADR-0550 (the shop's customer side, decided for the connected person) and ADR-0194, ADR-0493 (scopes, and handlers that never read one)
- constrains ADR-0436 (the catalogue document import) and ADR-0467 (the import's comparison) for catalogues maintained elsewhere; keeps ADR-0316 (the theme is an administrator's)
- relates to ADR-0434 (the catalogue can be switched off), ADR-0491 (content from the wiki), ADR-0498 (the wiki as a Worker Type), ADR-0312 (the catalogue is design-time state)
