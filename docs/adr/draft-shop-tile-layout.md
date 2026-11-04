# ADR-DRAFT: A catalogue chooses the shop's layout, and a product names its icon

- **Status:** Proposed
- **Implementation:** Partial
- **Date:** 2026-11-04
- **Deciders:** Atlas maintainers, catalogue owner
- **Open question:** Whether the tile layout meets eCH-0059 / WCAG 2.1 AA. The catalogue owner deferred accessibility to a later step and asked for it to be recorded as a requirement, not dropped. Nobody has audited the tiles yet: the horizontal track, the scroll button, the icon-only parts of a tile and the contrast of the accent banner are the places most likely to fail.
- **Question checked:** 2026-11

## Context and problem statement

The shop draws one layout: the cascade of [ADR-0383](0383-portal-level-names.md) — category, product group, product, services — as headings and rows. The catalogue owner showed the landing page of a federal *Digital Workplace* shop as the target for a second one: a category menu and a search field at the top, one horizontal row of tiles per catalogue with a symbol, a name and "Paket enthält N Elemente", a link "Alle N Elemente anzeigen" that opens the row into a grid, and a product page with a coloured banner, the symbol, and the order information in sections.

Three things the shop does not have stood in the way:

- a way to choose a layout at all;
- a symbol per product. [ADR-0391](0391-product-picture.md) gives a product a picture, which is an upload and one request per row; a tile row of twenty products would be twenty requests for something that is a glyph;
- the symbols themselves. The screenshots use a commercial icon set that Atlas may not redistribute.

The question this record answers: how does a catalogue get a second layout without a second shop, and where does a product's symbol come from?

## Decision drivers

- One shop, one order model. A layout is a way to draw the same release, not a second product path: the basket, the approval, the configuration form and the order stay what they are.
- A catalogue owner chooses, not the visitor and not the operator: ten catalogues for ten customer groups (ADR-0316) may want different shops.
- No request per tile, no file the catalogue uploads for a glyph, no third-party markup a catalogue can inject.
- Only symbols Atlas may redistribute, with their licence in `THIRD_PARTY_NOTICES.md`.

## Considered options

1. **A layout per catalogue in its theme, an icon name per product, the symbols shipped with the shop** (chosen).
2. A layout the visitor toggles in the shop.
3. Icons as uploaded pictures, reusing ADR-0391.
4. A separate tile shop page beside `shop.html`.

## Decision outcome

Chosen option: **1**, because the theme is already the catalogue's statement of how its shop looks (ADR-0316), and a name the shop resolves costs nothing per row and cannot carry foreign markup.

- **Layout.** `Theme.Layout` is `cascade` (empty, the default) or `tiles`. It is set where the accent and the typeface are, by an administrator, through `PUT /api/v1/catalogs/{id}/theme`; any other value is refused. The shop reads it from the catalogue whose theme it already wears — with several catalogues (ADR-0522) the highest-ranked — and lays out every catalogue the person reaches in that layout, one section per catalogue.
- **Icon.** `Item.Icon` names one symbol from `catalog.Icons`. Publishing refuses any other name, so a typo fails at publish rather than drawing a parcel in production. Empty is allowed and draws the parcel. The cascade does not draw it.
- **Symbols.** The shop carries 36 symbols from Tabler Icons 3.19.0 (MIT) in `api/web/shop-icons.js`, reduced to their inner markup and chosen to stand for what the screenshots show: accounts, groups, devices, mail, telephony, software, support. A guard test holds the module's keys equal to `catalog.Icons`, and another holds the MCP schema's enum to the same list.
- **Basket kept.** A tile opens the product page; the page adds to and removes from the basket, and the order — form, approval, recipient — is completed in the basket as in the cascade. The product page shows what the release says: the description, the services always included, the options, the approval rule.
- **Search and menu.** The category menu and the product-group menu filter by the capability keys of ADR-0524; the search uses the same matching as the cascade (name, description, keywords).

### Consequences

- **Positive:** a catalogue switches layout with one field and back with one field; no product, release or order changes shape. An icon is one short string in the release, frozen with it.
- **Negative / trade-offs accepted:** the symbol set is closed. A catalogue that needs a symbol Atlas does not ship waits for a release that adds it. That is deliberate: an open set is either uploads (option 3) or markup from the catalogue.
- **Negative / trade-offs accepted:** the layout is per catalogue, but a person reaching several catalogues sees one layout, the highest-ranked catalogue's. A merged shop in two layouts at once has no meaning.
- **Follow-ups / risks to watch:**
  - **Accessibility (eCH-0059 / WCAG 2.1 AA) is a recorded later requirement**, deferred by the catalogue owner: keyboard order and focus in the tile track, a name for the scroll button and the icons, contrast of text on the accent banner, the 200 % zoom and reflow behaviour of the two-row track. It must be audited before a federal catalogue switches to tiles.
  - Resource tiles (FAQ, guides) below the product rows, a shareable link to a product page and the favourite star's persistence are later steps; the star is drawn but stores nothing yet.
  - The screenshots carry a federal logo and a commercial icon set; neither is in the repository, and neither may be added.

## Pros and cons of the options

### Option 1 — layout in the theme, icon name, shipped symbols
- Good: one field per catalogue, one per product; no request per tile; no foreign markup.
- Good: the order path is unchanged, so every rule of ADR-0524 keeps holding.
- Bad: the set of symbols is fixed per Atlas release.

### Option 2 — a visitor toggle
- Good: a visitor who prefers rows keeps rows.
- Bad: the catalogue owner asked for a shop that looks a certain way for a customer group; a toggle makes that a preference, and a second layout every visitor can reach doubles what has to be tested and audited for accessibility.

### Option 3 — icons as pictures (ADR-0391)
- Good: any symbol a catalogue wants.
- Bad: one request per tile, an upload per product for a glyph, and colour that cannot follow the catalogue's accent.

### Option 4 — a separate tile page
- Good: the cascade's code is untouched.
- Bad: two shops to keep in step — basket, approval, locale strings, theme — which is the drift the hosted-page guard exists to stop.
