# ADR-0491: A service's content may come from an external source, copied into the draft and frozen with the release

- **Status:** Accepted (amended 2026-10-26 — an unlink restores the product's own content; amended 2026-10-27 — a child page may describe a service of its own; amended 2026-10-28 — the description keeps the wiki's layout, images and embedded items; see the amendments below)
- **Implementation:** Partial
- **Date:** 2026-10-26
- **Deciders:** Atlas maintainers
- **Open question:** How the product owner learns that a product manager unlinked their
  page. The unlink is recorded in Atlas's audit log (§ 6), but the product owner has no
  Atlas account, and the source's credential is read-only by design (§ 4), so Atlas
  cannot comment on the wiki page either. A notification channel to a person outside
  Atlas is not settled here.
- **Question checked:** 2026-10

## Context and problem statement

Everything the shop shows about a service is authored in Atlas today, by the product
manager, on the catalogue screen: the name, the description per language
(`Item.Descriptions`), the picture ([ADR-0391](0391-product-picture.md)), the keywords
([ADR-0355](0355-catalogue-search.md)) and the price
([ADR-0361](0361-product-price.md)). That fits an installation where one person
both knows what a service is and decides what is offered. For a small company it is
the right shape and it must stay exactly as it is.

A larger organisation splits the work, and the split is a separation of duties rather
than a convenience. The rule is **wiki = description, finance system = cost, Atlas =
catalogue**:

| Who | Owns | Works in |
|---|---|---|
| Product owner | what a **service** is: its description, pictures and keywords; releases a wiki space for the shop | the organisation's wiki — no role in Atlas |
| Product manager | what is **offered**: which services make a market offering, its name, its description, its bundle price, approval, eligibility | Atlas, role `productmanager` ([ADR-0315](0315-portal-roles-and-responsibilities.md)) |
| Modeler | how a service is **provisioned, changed and returned**, including its preconditions | Atlas, role `modeler` |
| Finance system | what a service **costs** to produce | read by Atlas; decided in a record of its own |
| Atlas operations | the technical account Atlas reads the wiki with | Atlas and the wiki |

With content authored only in Atlas, the product owner's description has to be
retyped by the product manager. The retyped copy then drifts from the wiki, and
nobody can tell which of the two the shop shows. The construction kit
([ADR-0395](0395-product-construction-kit.md)) already lets a product manager assemble
market offerings out of services. What is missing is that the services' content can
be owned by somebody who never opens Atlas.

The first source is [wikiwikibangbang](https://github.com/dbuchs7/wikiwikibangbang),
and Confluence or a similar system must be able to follow without a second design.
wikiwikibangbang is a typed item model (its ADR-0001): an item carries typed
`properties`, an optional workflow `state`, labels, attachments, a block-document
`body` (not HTML or Markdown), and a `version` that counts every change to the item.
Translatable user content is an open question there (its ADR-0017), so the product
owner keeps a service as **one page per language** — "Service X-DE", "Service X-FR" —
each with as many child pages as the product owner wants.

The wiki protects content in several ways, and none of it may reach the shop:

- a **read restriction** on a page or on any of its ancestors (its ADR-0022,
  ADR-0078), including one set automatically when sensitive data is saved (its
  ADR-0040);
- an open **data protection hold** (its ADR-0058);
- **protected passages** of a body, veiled for everybody else (its ADR-0088) and
  erasable for good (its ADR-0089);
- trash, purge and redaction (its ADR-0022).

Three decisions already taken in Atlas constrain any answer:

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
in the service's lifecycle process, with no second check. Atlas does not check it on
its own: a `requires` edge orders the lines of one order (ADR-0312), and whether the
recipient holds a precondition the order does not carry stays the lifecycle process's
question, as `api/order/advance.go` already says. This record changes nothing about
that.

## Decision drivers

- **Protected content never reaches the shop.** A restricted page, a held page or a
  protected passage is never shown by any surface of the shop, whatever the technical
  account could read. In doubt, Atlas refuses (fail closed).
- **Separation of duties.** The product owner decides what a service is, the product
  manager what is offered. Neither may decide the other's half, and the source must
  never be a way to change an approval rule or an audience.
- **Content only.** What the shop *shows* may come from outside. Anything that decides
  who may order, who approves, how long a right lasts or which process runs stays in
  Atlas.
- **A release stays a release.** What an order was placed against must not change
  when a wiki page is edited. The one exception is security (§ 9).
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
3. **Pull on demand into the draft, freeze with the release, recheck access
   continuously.**
4. **The source pushes content into Atlas** through the catalogue API with a
   credential of its own.
5. **The product owner maintains services in Atlas** in a home catalogue of their own,
   with a role of their own.

## Decision outcome

Chosen option: **3 — pull on demand into the draft, freeze with the release, recheck
access continuously.** It is the only option in which the product owner's content
reaches the shop and a release still means what it means today. Content moves only
when a product manager acts. Access is rechecked without anybody acting, because
security cannot wait for somebody to remember.

### 1. What a source may supply

| Field | Service bound to a source | Otherwise |
|---|---|---|
| Name (`Texts`) | product manager | product manager |
| Description per language (`Descriptions`) | **source**: the language page and all its child pages (§ 7) | product manager |
| Pictures | **source**: the attachments a field of the primary page names (§ 7) | product manager |
| Keywords | **source**: the labels of every language page, minus excluded labels (§ 7) | product manager |
| Production cost | **finance system** (a record of its own), never the wiki | finance system |
| Price, category | product manager | product manager |
| Approval, eligibility, `MaxDays`, order form, targets | product manager | product manager |
| Lifecycle bindings, actions, `CommandedBy` | modeler | modeler |
| Structure and precedence edges | product manager | product manager |
| Link to the wiki page per language (§ 5) | product manager | product manager |

The list of supplied fields is closed, and it holds content only. Nothing a source
supplies can widen an audience, remove an approval or rebind a process. The product
owner's write access in the wiki is therefore never an authorisation in Atlas.

A binding is chosen per item by the product manager. The fields it supplies are
read-only on the catalogue screen while the item is bound. A market offering — an
item with parts — is normally not bound: its description and its bundle price are the
product manager's.

### 2. Production cost does not come from the wiki

Production cost is internal, and a property in a wiki space released for the shop is
readable by everybody with a role there. wikiwikibangbang protects passages of a body,
not properties. So the product owner does not keep cost in the wiki at all. Atlas reads
it from the finance system (for example SAP) and shows it to the product manager in the
construction kit, never in the shop, the approval page or the order. How Atlas binds
the finance system is decided in a record of its own; this record only excludes the
wiki as a source of cost.

### 3. A content source

A content source is configured by an administrator. It has:

- an `id` and a display name;
- a `kind`, `wikiwikibangbang` first;
- a base URL, `https` only;
- a `credentialsRef` into the secret store
  ([ADR-0041](0041-connector-management-and-secret-store.md)), never a secret in the
  catalogue;
- the **picture field**: the name of the property on the primary page that names the
  attachments to use as pictures;
- the **excluded labels**: labels that organise the wiki and are not keywords, such as
  `withdrawn`;
- **link in shop**: whether the shop shows "more in the wiki" under a description, off
  by default.

The mapping is owned by Atlas and lives in this configuration, for the reason
[ADR-0387](0387-the-catalogue-against-the-standards-boundary.md) gives: Atlas keeps
its own model and owns the translation from somebody else's. A `kind` is an adapter
behind one interface (§ 10).

The source is off until an administrator configures one. Without one, the binding
does not appear on the catalogue screen at all.

### 4. The technical account, and which spaces it may read

Atlas reads the wiki with a technical account of its own, managed by Atlas operations:

- its token has scope `read` (its ADR-0027), so it can change no content;
- it holds the role `viewer` and nothing else: never the system roles `admin` or
  `erasure`, and never space administrator, because administrators are not bound by
  restrictions and read every protected passage;
- operations renew the token before it expires (at most 366 days) and review the
  account's roles.

**A space is released for the shop by its product owner**, by granting the technical
account `viewer` in it. No second person checks that release; it is deliberately the
product owner's decision alone. Atlas reads only from spaces in which the account
holds exactly `viewer`, and refuses every other page.

Before every fetch and every recheck, Atlas reads the account's own standing from the
source — token scope, system roles and space roles. A token not of scope `read`, any
system role, or any space role other than `viewer` stops every fetch and quarantines
every bound item of that source (§ 9). wikiwikibangbang answers scope and system roles
today (`GET /api/me`); the space roles need a new query (§ 11).

### 5. Linking a description to its wiki page

The product manager links an item to its pages on the catalogue screen. A bound item
shows one slot per language the catalogue declares, and the primary slot is marked.

- **Search.** The adapter searches the source (in wikiwikibangbang `GET /api/search`)
  and lists the hits with title, space, key and state.
- **Address or key.** The product manager pastes the page's address
  (`https://wiki.example.org/#/item/SVC-12`) or its key (`SVC-12`). An address of
  another host than the source's base URL is refused.
- **Resolving at once.** Atlas reads the page immediately and stores its stable
  identifier, not what was typed (in wikiwikibangbang a key can change when an item
  moves between spaces, its ID never does). It shows the page's title, state and
  version, the description it would supply, and every passage and child page the
  security rules would drop (§ 8). Saving writes the binding and the fetched content
  in one draft save.
- **What linking refuses:** a page the account cannot read; a page in a space not
  released for the shop; any page that fails a security rule (§ 8); the same page in
  two slots, or another item's own page, naming that item. A child page another
  item's description holds may be linked — see the amendment of 2026-10-27.

### 6. Unlinking is allowed and recorded

The product manager may remove a slot's page or the whole binding. The fields become
editable again and take back the product's own content from before the link — see the
amendment below, which replaced "keep the last fetched values" here. Every unlink is recorded in the
audit log with who, when and a reason the product manager must give. Notifying the
product owner is the open question above.

### 7. Refreshing is an act, not a schedule

A product manager **refreshes** one bound item, or every bound item of a catalogue. A
refresh fetches every bound page, maps it and writes the result into the **draft**
item, carrying the revision precondition of a catalogue save
([ADR-0376](0376-catalogue-maintenance-over-mcp.md)). Per item it is all or nothing. A
refresh that cannot read a language page names the page and the reason, and leaves
the item exactly as it was.

- **Child pages.** A language page's description is its own body followed by the
  bodies of all its child pages, depth first in the wiki's sibling order, over every
  level. Each child page passes the same security rules as the language page. A child
  page that fails one is dropped together with everything below it, since a
  restriction is inherited downwards; the rest is kept, and the preview names what was
  dropped.
- **Text is converted to text.** Paragraphs, headings and list items become plain text.
  Embeds, mentions of people, link targets, comments and earlier versions are never
  taken; a link keeps only its label. Atlas renders the result as text, never as
  markup.
- **Pictures** are the attachments the picture field of the primary page names, copied
  into the picture store of ADR-0391, so the shop never requests anything from the
  source. ADR-0391 holds one picture per product; until it holds more, the first named
  attachment is the picture. More than one is a follow-up.
- **Keywords** are the union of the labels of every language page, minus the excluded
  labels of § 3.
- **The fetch runs outside the engine.** It is a catalogue-service request, made before
  any command and outside the processor. What it writes is ordinary catalogue data.
  Nothing is fetched during replay, so I2, I4 and I6 are untouched.

When content is refreshed stays organisational: the roles agree on it, or the product
manager watches the pages in the wiki. Access is not organisational (§ 9).

### 8. The security rules

Atlas decides on a page's **protection**, never on whether its account could read it.
A page is refused when any of these holds for it:

| Protection in the source | Atlas |
|---|---|
| a read restriction on the page | refuses the page, even if the account is named on it |
| a read restriction on any ancestor | refuses the page; Atlas walks the whole parent chain, and an ancestor it cannot read refuses as well |
| a restriction set by the governance check (its ADR-0040) | refuses it, as any read restriction |
| an open data protection hold (its ADR-0058) | refuses the page while the hold is open |
| trashed, purged or redacted | refuses the page |
| outside a space released for the shop (§ 4) | refuses the page |
| an edit restriction only | no reason to refuse: reading stays open |

A **protected passage** (its ADR-0088) does not refuse the page. Atlas removes every
passage by its mark before the text is converted, whether or not the account could read
it, so the text of a passage never enters Atlas. If the placeholder character `▒`
appears in the result anyway, the page is refused: something was veiled that Atlas did
not recognise as a passage. An erased passage (its ADR-0089) has no text left to take.

### 9. Rechecking, and the quarantine

A release is frozen, and a page can be restricted after its content was copied. So
Atlas rechecks the protection of every bound page — not its content — on its own:

- **at once**, when the source reports a change on a bound page or one of its
  ancestors (§ 11);
- **every 15 minutes**, as the fallback for an event that never arrived;
- before every refresh.

When a recheck finds any protection of § 8, finds the page unreadable or gone, finds
the account's standing wrong (§ 4), or **cannot reach the source at all**, the item is
**quarantined**:

- Its source-supplied content — descriptions, pictures, keywords — is withheld from
  every surface outside the catalogue screen: the shop, the approval page, the order
  views, the API and MCP. The name and the price stay, so orders stay readable.
- It takes effect without a publish. This is the one deliberate exception to a frozen
  release: price and rules stay exactly as frozen, only the showing of content is
  withdrawn.
- There is **no tolerance** for an unreachable source. Security comes before
  availability: an outage of the wiki empties the shop of wiki content until a recheck
  succeeds again.
- A quarantine ends only when the product manager refreshes the item and it passes
  every rule; a recheck that passes again on its own does not lift it, because the
  content Atlas holds is the content that was in question.
- Publishing refuses while any bound item of the catalogue is quarantined or has no
  passed recheck within the last interval. Publish reads only Atlas's own recheck state
  and never calls the source.

The quarantined content stays in the release as evidence, is shown to nobody, and can
be purged by an administrator.

### 10. The adapter contract, for this source and the next

A `kind` implements one interface. For every page it must return the security facts
of § 8 — space, read restriction of the page and of every ancestor, hold, presence,
the marks of protected passages — and for the account its standing of § 4. **A fact
an adapter cannot establish counts as present**, and Atlas refuses. A new adapter is
admitted only when it proves these facts for its source; where its source has no
equivalent of a protection, it drops the content concerned rather than passing it
through. Confluence is a second adapter and changes nothing else; which of its
editions comes first is not decided here.

### 11. What wikiwikibangbang must provide first

The integration does not go live until wikiwikibangbang provides two things. Both are
prerequisites, not follow-ups:

- **A change feed for systems.** A read-only feed of the changes to the items a
  principal watches, by sequence number, so that a recheck runs at once and a missed
  delivery is caught up from the last sequence seen. A read-scope token must be able to
  watch and unwatch, since watching changes no content.
- **The principal's own roles.** A query answering the space roles, direct and through
  groups, of the calling principal, so that Atlas can prove its account is nothing but
  `viewer`.

They are designed in wikiwikibangbang's own record, which must keep its invariant
that an unreadable item is non-existent (its D10).

### As built

The backend and its screens are built; the finance-system cost (§ 2), more than one
picture, an administrator's purge of quarantined content, and telling the product owner
about an unlink (the open question) are not.

- **The rich description** (amendment of 2026-10-28). `contentsource/rich.go`
  converts a body into `Rich` blocks; `preview` builds them for the page, its child
  pages and, through `pages.embeds`, its embedded items; `gatherImages` copies the
  images. `Item.Rich` carries them per language and a release freezes them;
  `PageRef.Embeds` puts the embedded items into the recheck; `contentimage.go`
  stores and serves the images. The shop draws them in `richBlocks`.
- **A child page describing a service of its own** (amendment of 2026-10-27).
  `contentsource.PreviewExcept` and `GatherExcept` take the pages that are other
  items' own pages and drop them, with everything below, from the walk;
  `catalog.Service.ownPages` supplies them, `boundElsewhere` refuses only another
  item's own page, the preview answers `carriedBy`, and `contentProblems` holds back a
  publish while a copy still holds such a page.
- **Where it lives.** `api/contentsource` holds the source configuration and its store
  (`contentsources`, an instance store), the adapter contract (`Adapter`, with every
  security fact a three-valued `Fact` whose zero value is *unknown*, so a fact an adapter
  did not establish refuses), the wikiwikibangbang adapter, the rules of § 8 and § 4 as
  pure functions, the conversion, the recheck and its runner; `api/contentsource/wikifake`
  is a wikiwikibangbang implementing the source's contract for tests. The binding is
  `catalog.Item.ContentSource` (omitted when absent, frozen into a release), the routes of
  § 5–§ 7 are the catalogue's (`/api/v1/catalog-products/{id}/content…`,
  `/api/v1/catalogs/{id}/content/refresh`, role `productmanager` on the home catalogue,
  carrying the product's `revision`), and the configuration routes are admin-only
  (`/api/v1/content-sources…`, with a `check` that reads the account's standing). The
  wikiwikibangbang side is its ADR-0120: `GET /api/me/roles`, `GET /api/changes` and
  item watches with a read token.
- **What a recheck covers, and what is shown.** The recheck covers the draft's binding and
  the binding in the newest release of each catalogue — what the shop reads. Every older
  release's source content is withheld for good, because nothing rechecks it. It watches
  every bound page, every child page whose text was taken and every ancestor walked, by
  item watch only (a space watch never reports a page that became unreadable), and ends
  the watches nothing needs any more by id. The feed's position is read before the sweep
  that follows it, so a change in between is not lost.
- **A lifted quarantine.** A refresh that lifts a quarantine records when the quarantine
  began, and content copied before that moment stays withheld wherever a copy of it is
  left — the releases published before the refresh — while the refreshed copy is shown
  after the next publish. Otherwise a refresh would show again the very content that was
  in question.
- **A recheck that stopped.** Content whose last passed recheck is older than twice the
  interval (30 minutes) is withheld at read time without a quarantine, so a recheck that
  stopped running — the server down, the goroutine gone — fails closed; the next recheck
  that passes shows it again. Publishing refuses after one interval, as § 9 says.
- **A passage protected after the copy.** A binding keeps the ids of the passages it was
  copied without (and of each child page's), and a recheck that finds a passage in a
  taken block that the copy did not remove quarantines: Atlas holds its text.
- **Conversion.** Paragraphs, headings, bullet and task items and ordered items are text;
  quotes, panels, tables, code, embeds, pictures and every computed block are dropped and
  named in the preview. A child page contributes its title and its body. Mark offsets are
  Unicode code points, as wikiwikibangbang counts them.
- **Unlinking one slot** takes that language's description and the keywords only its
  labels gave with it (each slot keeps the labels it contributed), because content no
  binding names is content no recheck covers; the primary slot goes only with the whole
  binding, since the picture is its. The audit record is the log event `content.unlinked`.
- **Withholding** is the release listing (the shop's listing, detail and keyword search,
  and MCP's `atlas_catalog_releases`), the picture route — for every reader, the
  catalogue screen included, which shows the quarantine instead — and the exports, which
  also leave the binding behind. The approval page, the order views and the shop's task
  list carry no description, keyword or picture of their own, and tests hold them to it.
  A picture on a bound product is the source's: the picture routes refuse to set or
  remove one.
- **Removing a source** is refused while a product is bound to it.
- **The screens.** `api/web/content-sources.js` is *Console › Administration › Content sources*, admin-only
  and gone with the catalogue switch: list, create, change, remove (the 409 naming the bound
  products is shown) and the check with every problem. `api/web/catalog-content.js` is the
  product editor's *Content from the wiki* panel — drawn only when the status route names a
  source, so an installation without one sees the screen it had: search, a preview per
  language slot with the text, the dropped passages, children and blocks, every refusal and
  `boundTo`, and one link for all slots; for a bound product its pages with version, copy time
  and children, the recheck record, the quarantine and whether the shop shows it, refresh, and
  unlink of a slot or the whole binding through a dialog that requires the reason and says it is
  audited. The editor draws a bound product's description and keywords read-only and disables
  the picture controls; a catalogue offering a bound product gets *Refresh all wiki content* with
  one line per product. Everything a wiki supplies is drawn as text nodes, never as markup, and a
  page address becomes a link only over https, in a new tab without an opener. The shop shows
  *More in the wiki* from the release's binding, which the server hands it only where the source
  allows a link and only while the content is shown. Words live in the Console's message
  catalogue (`content.*`, `contentSources.*`) and the shop's (`info.wiki`); the shop handbook
  has a section `#wiki-inhalt`.

### Consequences

- **Positive:** the product owner maintains one description, in the wiki, and the
  shop shows it without retyping. A restricted page, a held page and a protected
  passage never reach the shop: on the first fetch by rule, afterwards within one
  feed delivery or at most 15 minutes. A release, an approval and an order keep their
  guarantees apart from the quarantine. An installation without a source sees no
  difference.
- **Negative / trade-offs accepted:** an outage of the wiki removes wiki content from
  the shop. Releasing a space is one person's decision. Every new child page reaches
  the shop at the next refresh, including one meant as a working note. Atlas cannot
  see confidential text the wiki does not mark as protected. Conversion to plain text
  loses formatting, and a removed passage can leave a gap in a sentence. The
  integration depends on two changes in another project.
- **Follow-ups / risks to watch:**
  - The finance-system record for production cost.
  - More than one picture per product (ADR-0391).
  - A check in Atlas whether a page's content is newer than the copy, once the change
    feed exists.
  - The Confluence adapter and its proof of the facts of § 10.

## Amendment (2026-10-26): an unlink restores the product's own content

§ 6 first said that an unlinked product keeps the last fetched values, and drops them
when it is quarantined. In use that was the wrong way round: a product manager who
unbinds a product expects it to be described as it was before, and instead found the
wiki's text left behind as if it were theirs — or, after a quarantine, no description
at all. Worse, the link itself destroyed what it replaced: the product's own
description and keywords were overwritten, and its picture was deleted when the
source supplied none.

So the **first link sets the product's own content aside** — its descriptions, its
keywords and its picture — in the product's content state, which no release carries
and no quarantine withholds; the picture moves beside the product's picture in the
picture store. A refresh keeps what the first link set aside. The **whole unlink puts
it back**, quarantined or not, since the own content was never in question; a product
that had no picture is left with none. **Unlinking one language** puts that language's
own description back and leaves the rest bound. A binding made before this amendment
has nothing set aside, and its unlink behaves as § 6 first said.

## Amendment (2026-10-27): a child page may describe a service of its own

§ 5 first refused any page another item's description held, child pages included.
In use that blocked the shape a product owner reaches for first: a market offering
kept as one page, its services kept as child pages below it — "Notebook Hardware"
with "Tastatur", "Maus" and "Dockingstation" beneath. The offering could be linked
to the page, or a service to its child page, never both.

What stays exclusive is a page that describes an item: an item's **own page** — a
language page of its binding — describes that item and no other, and linking it to a
second item is refused, naming the first. A **child page** is part of a description,
not the subject of one, so it may become an item's own page:

- **A fetch leaves out every other item's own page.** A child page that is another
  item's own page is that item's description; the fetch drops it with everything
  below it, as it drops a restricted page, and the preview names the item it
  describes. The rest of the walk is taken as before.
- **Linking a child page another item holds is allowed.** The preview names the items
  whose description holds it (`carriedBy`). Each of them keeps the copied text until
  its next refresh, which leaves the page out.
- **The publish gate waits for that refresh.** A bound item whose copy still holds a
  child page that is now another item's own page refuses to publish, naming that item,
  so the shop never shows one page's text under two products.
- A child page linked to another item while a fetch reads the pages makes that
  fetch's write stale: it is refused, and repeated, rather than written with text that
  is now another item's.

The security rules of § 8 are unchanged: every page, own or child, passes them where
it is taken.

## Amendment (2026-10-28): the description keeps the wiki's layout, its images and embedded items

§ 7 converted a page to plain text and dropped everything else: tables, panels,
quotes, code, embedded items and images. In use the shop's fact sheet then showed a
product owner's carefully laid-out page as a run of paragraphs, without its tables
of technical data, without the pictures of the device and without the service-level
article embedded in it — and pointed to the wiki for the rest, which a reader in the
shop without an account there cannot follow.

So a fetch now also takes the **rich description**: the page as the source lays it
out, kept beside the plain text a search and the clipboard use.

- **Data, never markup.** Each block is a kind — heading, paragraph, list item,
  quote, code, panel, table, divider, image, child page, embedded item — with plain
  text in fields of its own. The shop draws it with text nodes; nothing a wiki
  author writes becomes script or style. The plain description is unchanged.
- **The security rules apply to every block that holds text**, as they applied to
  paragraphs: every protected passage and every mention is removed by its mark before
  the text is used, a table's text included before it is split into cells; a mark
  outside its text refuses the page; a placeholder left anywhere refuses it.
- **An embedded item passes § 8 on its own**, as a child page does — a read
  restriction on it or an ancestor, a hold, not live, a space not released for the
  shop — and is left out, named in the preview, when it does not. Embeds are followed
  three levels deep, and an item embedding itself is left out. The items taken are
  held in the binding and rechecked like the page, so a restriction set on one later
  quarantines the product.
- **Images are copied**, as the picture is, named by the hash of their bytes, and
  served behind the picture's gate and withheld under a quarantine like it — the shop
  never asks the source for anything. An image that cannot be read or is not a PNG,
  JPEG or SVG is left out; it refuses nothing. A refresh removes no image: an older
  copy may still be what the newest release shows.
- **The shop's link to the wiki is gone.** The fact sheet shows the description
  whole, the catalogue's mark, and the release it describes.

## Pros and cons of the options

### Option 1 — the shop reads the source live
- Good: always current. No refresh, no copy.
- Bad: the figure an approver sees can change after the decision, a release no longer
  says what was promised, and the description check per language cannot be made.
  Every shop page depends on the wiki being up. It is the external-URL picture
  ADR-0391 refused, applied to every field.

### Option 2 — a link only
- Good: almost nothing to build. The wiki stays the only copy.
- Bad: the shop and the approval page show no description, only a link that most
  orderers cannot open without a wiki account.

### Option 3 — pull, freeze, recheck (chosen)
- Good: the source supplies the content and Atlas keeps its guarantees. Protection is
  decided by rule and rechecked without anybody acting. Sources differ only behind an
  adapter that must prove the same facts.
- Bad: content can be stale. A second copy of the content exists, by design. The
  quarantine is an exception to a frozen release.

### Option 4 — the source pushes content into Atlas
- Good: the wiki knows when its content changes.
- Bad: it gives an external system write access to catalogue drafts. The decision that
  matters — to publish what changed — stays with the product manager either way. The
  change feed of § 11 takes the one part of this option that matters for security.

### Option 5 — the product owner maintains services in Atlas
- Good: no external dependency, no conversion, no copy.
- Bad: the product owner has no role in Atlas, and the organisation keeps the
  documentation of a service in its wiki anyway. It remains what a small installation
  does today, which § 3 keeps intact.

## Links

- relates to [ADR-0312](0312-portal-catalogue-order-inventory.md) — releases freeze
  what an order was placed against.
- relates to [ADR-0315](0315-portal-roles-and-responsibilities.md) — the product
  manager's role and the home catalogue.
- relates to [ADR-0355](0355-catalogue-search.md) — keywords stay one flat list.
- relates to [ADR-0361](0361-product-price.md) — the price stays the product
  manager's.
- relates to [ADR-0391](0391-product-picture.md) — a fetched picture is copied into
  its store.
- relates to [ADR-0387](0387-the-catalogue-against-the-standards-boundary.md) — Atlas
  owns the mapping from a source's vocabulary.
- relates to [ADR-0395](0395-product-construction-kit.md) — the construction kit.
- relates to [ADR-0041](0041-connector-management-and-secret-store.md) — the
  source's credential lives in the secret store.
- relates to [ADR-0376](0376-catalogue-maintenance-over-mcp.md) — a refresh carries
  the revision precondition of a catalogue save.
- relates to [ADR-0425](0425-a-product-lifecycle-is-one-process-with-message-triggers.md)
  and [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) — the
  per-service lifecycle and its service events.
- external: wikiwikibangbang's
  [ADR-0001](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0001-unified-item-data-model.md)
  (item model),
  [ADR-0014](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0014-closed-renderer-for-user-content.md)
  (content is never markup),
  [ADR-0022](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0022-permission-model.md)
  (roles and restrictions),
  [ADR-0027](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0027-local-accounts-sessions-and-api-tokens.md)
  (read-only tokens),
  [ADR-0040](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0040-governance-checks-and-automatic-restriction.md)
  (governance),
  [ADR-0046](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0046-watching-and-notifications-from-the-log.md)
  (watching),
  [ADR-0058](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0058-data-protection-review-holds-open-items-as-drafts.md)
  (data protection hold),
  [ADR-0078](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0078-protecting-items-for-people-and-groups.md)
  (protecting an item) and
  [ADR-0088](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0088-protected-passages-of-a-body.md)
  (protected passages).
