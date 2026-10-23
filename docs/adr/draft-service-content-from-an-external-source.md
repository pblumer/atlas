# ADR-DRAFT: A service's content may come from an external source, copied into the draft and frozen with the release

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-23
- **Deciders:** Atlas maintainers

## Context and problem statement

Everything the shop shows about a service is authored in Atlas today, by the product
manager, on the catalogue screen: the name, the description per language
(`Item.Descriptions`), the picture ([ADR-0391](0391-product-picture.md)), the keywords
([ADR-0355](0355-catalogue-search.md)) and the price
([ADR-0361](0361-product-price.md)). That fits an installation where one person
both knows what a service is and decides what is offered. For a small company it is
the right shape and it must stay exactly as it is.

A larger organisation splits the two, and the split is a separation of duties rather
than a convenience:

| Who | Owns | Works in |
|---|---|---|
| Product owner | what a **service** is: its description, its production cost, its documentation | the organisation's wiki — no role in Atlas |
| Product manager | what is **offered**: which services make a market offering, its name, its description, its bundle price, approval, eligibility | Atlas, role `productmanager` ([ADR-0315](0315-portal-roles-and-responsibilities.md)) |
| Modeler | how a service is **provisioned, changed and returned**: its lifecycle process, with input from specialists and the product owner | Atlas, role `modeler` |

With content authored only in Atlas, the product owner's description has to be
retyped by the product manager. The retyped copy then drifts from the wiki, and
nobody can tell which of the two the shop shows. The construction kit
([ADR-0395](0395-product-construction-kit.md)) already lets a product manager assemble
market offerings out of services. What is missing is that the services' content can
be owned by somebody who never opens Atlas.

The first source is [wikiwikibangbang](https://github.com/dbuchs7/wikiwikibangbang),
and Confluence or a similar system must be able to follow without a second design.
wikiwikibangbang is a typed item model (its ADR-0001): an item carries typed
`properties`, an optional workflow `state`, a block-document `body` (not HTML or
Markdown), and a `version` that counts every change to the item. Its API is
authenticated, and an API token can be limited to scope `read` (its ADR-0027).
Translatable user content is an open question there (its ADR-0017), so the product
owner keeps a service as **one page per language** — "Service X-DE", "Service X-FR" —
each possibly with child pages. The same service is therefore several pages in the
source.

Three decisions already taken constrain any answer:

- **A release is frozen.** An order is placed against a release and keeps what it was
  promised ([ADR-0312](0312-portal-catalogue-order-inventory.md)). For the figure an
  approver sees, that is explicit: it must not change after the decision
  (ADR-0361).
- **A picture is not an external URL.** ADR-0391 refused that option because a
  picture breaks when somebody else's server moves, and because every viewer's
  browser would send a request to a third party.
- **A release proves its texts.** A description present in one of the catalogue's
  languages and missing in another does not publish.

The product owner's condition has a second half that is not about content: every
service carries its own lifecycle, and its preconditions are caught in the
orchestration. The lifecycle already exists. Every item needs a provisioning and a
deprovisioning binding to publish, or one lifecycle process
([ADR-0425](0425-a-product-lifecycle-is-one-process-with-message-triggers.md)), and
further service events are product actions
([ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md)). A
precondition — a Teams add-on that needs its licence — is ensured by the **modeler**
in the service's lifecycle process. Atlas does not check it on its own: a `requires`
edge orders the lines of one order (ADR-0312), and whether the recipient holds a
precondition the order does not carry stays the lifecycle process's question, as
`api/order/advance.go` already says. This record changes nothing about that.

## Decision drivers

- **Separation of duties.** The product owner decides what a service is, the product
  manager what is offered. Neither may decide the other's half, and the source must
  never be a way to change an approval rule or an audience.
- **Content only.** What the shop *shows* may come from outside. Anything that decides
  who may order, who approves, how long a right lasts or which process runs stays in
  Atlas.
- **A release stays a release.** What an order was placed against must not change
  when a wiki page is edited.
- **The shop does not depend on the wiki.** A wiki outage must not break the shop, the
  approval page or a publish.
- **Unchanged without a source.** An installation that configures no source behaves
  exactly as today, with no new step, no new screen in the way and no new setting to
  understand.
- **One design for several sources.** wikiwikibangbang first, Confluence next,
  without the catalogue knowing either.
- **Foreign content is never markup.** Text from a source reaches the shop as text,
  never as HTML.

## Considered options

1. **The shop reads the source live** whenever a service is shown.
2. **A link only.** The item carries the URL of its wiki page, and the shop links out.
3. **Pull on demand into the draft, freeze with the release.**
4. **The source pushes into Atlas** through the catalogue API with a credential of its
   own.
5. **The product owner maintains services in Atlas** in a home catalogue of their own,
   with a role of their own.

## Decision outcome

Chosen option: **3 — pull on demand into the draft, freeze with the release.** It is
the only option in which the product owner's content reaches the shop and a release
still means what it means today. The pull is the one moment Atlas talks to the source,
and it happens while a product manager is looking.

### 1. What a source may supply

| Field | Service bound to a source | Otherwise |
|---|---|---|
| Name (`Texts`) | product manager | product manager |
| Description per language (`Descriptions`) | **source** | product manager |
| Picture | **source** | product manager |
| Keywords | **source** | product manager |
| Production cost (new, § 2) | **source** | product manager |
| Price, category | product manager | product manager |
| Approval, eligibility, `MaxDays`, order form, targets | product manager | product manager |
| Lifecycle bindings, actions, `CommandedBy` | product manager, built by the modeler | same |
| Structure and precedence edges | product manager | product manager |
| Link to the wiki page per language (§ 5) | product manager | product manager |

The list of supplied fields is closed, and it holds content only. Nothing a source
supplies can widen an audience, remove an approval or rebind a process. The product
owner's write access in the wiki is therefore never an authorisation in Atlas.

A binding is chosen per item by the product manager. The fields it supplies are
read-only on the catalogue screen while the item is bound, and become editable again
when the binding is removed, keeping the last fetched values. A market offering — an
item with parts — is normally not bound: its description and its bundle price are
the product manager's.

### 2. Production cost

The product owner states what a service costs to produce. It becomes a new item field,
`Cost`, shaped like the price of ADR-0361: a sentence the source writes, frozen like
a rule and computed by nothing.

- The catalogue screen shows it to the product manager, beside each part in the
  construction kit, so that a bundle price can be set with the parts' costs in view.
- It is **never served** by the shop, the approval page or the order. A production
  cost is internal; the price is what the orderer and the approver see.
- Atlas does not add costs up, for the reason ADR-0361 gives for totals: adding needs
  a currency, a rate and a date, which are an installation's finance rules and not
  the catalogue's.

### 3. A content source

A content source is configured by an administrator, never by a product manager. It
has:

- an `id` and a display name;
- a `kind`, `wikiwikibangbang` first;
- a base URL;
- a `credentialsRef` into the secret store
  ([ADR-0041](0041-connector-management-and-secret-store.md)), never a secret in the
  catalogue (for wikiwikibangbang, a token of scope `read`);
- a **mapping** from the source's own fields to the fields of § 1: the description
  is a language page's body, and `Cost` is read from a property of kind number or
  text (§ 4 says from which page);
- optionally an **accepted state**: only an item in that workflow state is fetched (for
  example `released`), so that a product owner's draft in the wiki never reaches a
  catalogue.

The mapping is owned by Atlas and lives in this configuration, for the reason
[ADR-0387](0387-the-catalogue-against-the-standards-boundary.md) gives: Atlas keeps
its own model and owns the translation from somebody else's. A `kind` is an adapter
behind one interface. Given a reference, it returns the mapped fields, the picture
bytes, the source's version of the page and its URL. Confluence is a second adapter
and changes nothing else.

The source is off until an administrator configures one. Without one, the binding
does not appear on the catalogue screen at all.

### 4. The binding on an item

A service is bound to **one page per language**, because that is how the product owner
keeps it (Context). The binding names one of them as the **primary page**; the
language-neutral fields — production cost and picture — are read from it.

```
ContentSource {
  Source  string                 // the content source's id
  Primary string                 // the language whose page supplies cost and picture
  Pages   map[string]PageRef     // per language tag, e.g. "de" -> "Service X-DE"
}

PageRef {
  Ref       string    // the page in the source
  Version   int       // the source's version of that page at the last fetch
  FetchedAt time.Time
  URL       string    // where the product owner edits it
}
```

- **Description.** Each language's description is the body of that language's page.
  Child pages are not fetched: they are the wiki's further documentation, and the shop
  links to the language page for whoever may read it there. A description that needs
  more than the page itself belongs on the page.
- **Keywords** are the union of the keywords on every language page. Atlas keeps
  keywords as one flat list on purpose (ADR-0355), and a searcher's language is not
  the catalogue's.
- **Languages are proven at publish, as today.** A catalogue declaring German and
  French needs a page bound for each. A language the catalogue declares and the
  binding lacks does not publish, which is the existing description rule applied to
  the source.
- **The reference is the page's stable identifier**, not its key, where the source has
  one: in wikiwikibangbang a key can change when an item moves between spaces, its ID
  never does.

### 5. Linking a description to its wiki page

The product manager links an item to its pages on the catalogue screen. A bound item
shows one slot per language the catalogue declares, and the primary slot is marked.

**Choosing a page.** A slot takes a page in one of two ways, and both end in the same
place:

- **Search.** The product manager types part of a title. The adapter searches the
  source (in wikiwikibangbang its search API, `GET /api/search`) and lists the hits
  with title, space, key and workflow state.
- **Address or key.** The product manager pastes what they already have open in the
  wiki: the page's address (`https://wiki.example.org/#/item/SVC-12`) or its key
  (`SVC-12`). The adapter parses it. An address of another host than the source's base
  URL is refused, so a slot cannot point at a server the administrator did not
  configure.

**Resolving at once.** Either way Atlas reads the page immediately, through the
source's credential. It stores the page's stable identifier rather than what was
typed or pasted (§ 4), and shows the page's title, state and version, along with the
description it would supply. The product manager therefore sees what they linked
before they save. Saving writes the binding and the fetched content in one draft save,
so a linked slot is never empty.

**What linking refuses.**

- A page the source's credential cannot read is reported as not found. The product
  manager's own rights in the wiki play no part: Atlas reads as its source credential,
  and that credential decides what can be linked.
- A page outside the **accepted state** (§ 3) can be linked, so that work can begin
  before the product owner releases it. It is not fetched, though, and the slot says
  which state it waits for. A catalogue with such a slot does not publish, by the
  existing description rule.
- The same page in two slots of one item is refused. So is a page already bound to
  another item, naming that item: one page describes one service, and a service is
  one item offered by reference in as many catalogues as need it (ADR-0315).

**Unlinking.** Removing a slot's page keeps the description last fetched and makes it
editable again (§ 1). Removing the binding as a whole does the same for every field.

**The link in the shop.** The shop can show "more in the wiki" under a description,
leading to the page in the reader's language. Whether it does is a setting of the
source, and it is **off by default**: it is useful only where the shop's orderers
can read the wiki, and a link most readers cannot open is a broken link.

**A market offering may be linked without content.** An item with parts keeps the
product manager's own description (§ 1). It may still name a page per language as a
link only — the offering's documentation, kept by whoever writes it — through the same
slots. Such a link is resolved and frozen like any other, but nothing is fetched from
it, and its fields are not read-only.

**Over the API and MCP.** A binding is written through the catalogue API with a page's
key, address or identifier, and is resolved the same way before it is stored
([ADR-0376](0376-catalogue-maintenance-over-mcp.md)). A client that names a page
cannot bypass the host check or the refusals above.

### 6. Refreshing is an act, not a schedule

A product manager **refreshes** one bound item, or every bound item of a catalogue. A
refresh fetches every bound page of an item, maps them and writes the result into the
**draft** item, carrying the
revision precondition every catalogue save carries
([ADR-0376](0376-catalogue-maintenance-over-mcp.md)). Per item it is all or nothing.
A refresh that cannot read one of an item's pages names the page and the reason, and
leaves that item exactly as it was.

- **Text is converted to text.** A block document or a Confluence page becomes
  paragraphs of plain text on the server. Unknown constructs are dropped, and the drop
  is reported. Atlas renders the result as text and never as markup.
- **A picture is copied** into the picture store of ADR-0391, so the shop never
  requests anything from the source.
- **The fetch runs outside the engine.** It is a catalogue-service request, made before
  any command and outside the processor. What it writes is ordinary catalogue data.
  Nothing is fetched during replay, so I2, I4 and I6 are untouched.

**Knowing when to refresh is organisational for now.** The product owner and the
product manager agree on it between their roles, or the product manager **watches**
the bound pages in the wiki and is notified of a change there (wikiwikibangbang's
ADR-0046). Atlas neither polls the source nor compares versions. The binding still
stores each page's version, so that a check in Atlas can be added later without
changing what is stored.

### 7. Publish never calls out

Publishing proves what it proves today, on what the draft holds, and the description
check per language included. It never contacts a source. A wiki outage therefore
cannot block a release, and the same draft always publishes to the same release.

The release freezes the fetched content together with the binding (each page's
`Ref`, `Version` and `URL`). An order can therefore say which version of which wiki
page its description came from, and that holds after the wiki page has changed.

### Consequences

- **Positive:** the product owner maintains one description, in the wiki, and the
  shop shows it without retyping. The product manager can assemble market offerings
  from services whose content they do not own, and cannot change that content by
  accident. A release, an approval and an order keep exactly the guarantees they
  have today. An installation without a source sees no difference.
- **Negative / trade-offs accepted:** the shop is not live. A wiki edit reaches the
  shop only after a refresh and a publish, and that delay is the point: it is the
  product manager's decision to republish. Two people now share one service, and
  whether a change reaches the shop depends on their agreement or on the product
  manager watching the pages. Atlas does not notice a stale description. Conversion to plain text loses formatting that the wiki can show. The
  shop still links to the language page (`URL`) for whoever is allowed to read it
  there. A service in three languages is three pages to bind.
- **Follow-ups / risks to watch:**
  - A check in Atlas that compares the stored page versions with the source, if the
    agreement between the roles turns out not to be enough.
  - A Confluence adapter, which needs its storage format converted the way § 6
    converts the block document.
  - Pushing a refresh from a source webhook (option 4) once a source can sign one,
    which would still write into the draft and not past it.
  - If production cost ever needs a currency, that becomes the money type
    ADR-0361 left open, not a second string.

## Pros and cons of the options

### Option 1 — the shop reads the source live
- Good: always current. No refresh, no check, no copy.
- Bad: breaks every constraint above. The figure an approver sees can change after
  the decision, a release no longer says what was promised, and the description check
  per language cannot be made. Every shop page depends on the wiki being up and
  readable by the shop's service identity. It is the external-URL picture ADR-0391
  refused, applied to every field.

### Option 2 — a link only
- Good: almost nothing to build. The wiki stays the only copy.
- Bad: the shop and the approval page show no description, only a link that most
  orderers cannot open without a wiki account. Nothing is frozen, and nothing is
  translated or checked. It moves the problem to the wiki's access rights instead of
  solving it.

### Option 3 — pull on demand into the draft, freeze with the release (chosen)
- Good: the source supplies the content and Atlas keeps its guarantees. The only
  contact with the source is an act somebody watches. Publish stays deterministic and
  independent of the source. Sources differ only behind an adapter.
- Bad: content can be stale, and only the agreement between the roles closes that
  gap. A second copy of the content exists, by design.

### Option 4 — the source pushes into Atlas
- Good: the strongest alternative. The wiki knows when its content changes, so a push
  removes the staleness without asking anybody to look. Confluence can send webhooks.
- Bad: it gives an external system write access to catalogue drafts and requires a
  credential that can write. Every source then needs a way to send, sign and retry,
  and wikiwikibangbang's webhooks are message channels to people, not change events
  on items. The decision that matters — to publish what changed — stays with the
  product manager either way. Kept as the follow-up above, writing into the draft.

### Option 5 — the product owner maintains services in Atlas
- Good: no external dependency, no conversion, no copy. The home-catalogue scope of
  ADR-0315 already expresses "this person edits these items".
- Bad: the product owner has no role in Atlas, and the organisation keeps the
  documentation of a service in its wiki anyway, so this option produces exactly the
  second, drifting copy the record sets out to remove. It remains what a small
  installation does today, which § 3 keeps intact.

## Links

- relates to [ADR-0312](0312-portal-catalogue-order-inventory.md) — releases freeze
  what an order was placed against, and the basket resolves an item already held.
- relates to [ADR-0315](0315-portal-roles-and-responsibilities.md) — the product
  manager's role and the home catalogue.
- relates to [ADR-0361](0361-product-price.md) — the price stays the product
  manager's, and production cost takes its shape.
- relates to [ADR-0355](0355-catalogue-search.md) — keywords stay one flat list.
- relates to [ADR-0391](0391-product-picture.md) — a fetched picture is copied into
  its store, for the reasons that record refused an external URL.
- relates to [ADR-0387](0387-the-catalogue-against-the-standards-boundary.md) — Atlas
  owns the mapping from a source's vocabulary.
- relates to [ADR-0395](0395-product-construction-kit.md) — the construction kit shows
  the parts' production costs.
- relates to [ADR-0041](0041-connector-management-and-secret-store.md) — the
  source's credential lives in the secret store.
- relates to [ADR-0376](0376-catalogue-maintenance-over-mcp.md) — a refresh carries
  the revision precondition of a catalogue save, and a binding written over MCP is
  resolved like one made on the screen.
- relates to [ADR-0425](0425-a-product-lifecycle-is-one-process-with-message-triggers.md)
  and [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) — the
  per-service lifecycle and its service events, which this record presupposes.
- external: wikiwikibangbang's
  [ADR-0001](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0001-unified-item-data-model.md)
  (item model),
  [ADR-0014](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0014-closed-renderer-for-user-content.md)
  (content is never markup),
  [ADR-0017](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0017-internationalisation-via-message-catalogue.md)
  (translatable content is open),
  [ADR-0046](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0046-watching-and-notifications-from-the-log.md)
  (watching a page) and
  [ADR-0027](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0027-local-accounts-sessions-and-api-tokens.md)
  (read-only API tokens).
