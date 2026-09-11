# ADR-DRAFT: A theme belongs to a catalogue, the sign-in screen belongs to the operator

- **Status:** Accepted
- **Implementation:** Not started
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers
- **Open question:** Whether the brand change at sign-in reads as a transition or as a
  fault. A returning visitor sees the operator's brand on the sign-in screen and their
  own from the first painted frame after it, and nobody has watched an external
  customer do that. If it reads as a fault, the answer is a catalogue-specific entry
  URL, which this record deliberately does not build yet.
- **Question checked:** 2026-09

## Context and problem statement

The portal serves about ten catalogues, each for a different customer group, and each
is to carry that group's colours, logo and typeface.

Atlas brands itself once. [ADR-0113](0113-org-wide-ui-theme.md) stores a single source
accent in `settings/theme.json`, serves it from a **public** `GET /api/v1/settings/theme`
so the sign-in screen is already branded, derives the hover and soft shades in the
browser, and paints from a `localStorage` cache before first paint so there is no flash
of the default. [ADR-0263](0263-form-runtime-brand-theming.md) carried that palette into
the form runtime and added `--accent-ink`, chosen from the accent's own WCAG relative
luminance so text on an accent fill stays legible whatever colour was picked.

Both records are explicit that the brand is an **organisation** property, not a personal
one. Ten catalogue brands do not contradict that reasoning; they relocate it — the brand
belongs to the catalogue, and an instance serving several customer groups has several.

The difficulty is not storage. It is that a catalogue is resolved from the signed-in
user's rank, so **at sign-in there is no catalogue yet**: the one surface an external
customer meets first is the one surface that cannot know which brand to wear.

## Decision drivers

- The existing no-flash path must keep working; a branded UI that flashes the default
  first is what ADR-0113 was written to avoid.
- Contrast must hold for every one of the brands, not for the one somebody tested.
- Nothing may leak the catalogue inventory to an unauthenticated caller. The theme
  endpoint is public.
- A typeface must not become a request to a third party on every page load.
- This is display configuration. No engine involvement, as ADR-0113 already established.

## Considered options

1. **One brand still** — refuse the requirement; catalogues differ in content only.
2. **Catalogue theme everywhere**, including sign-in, resolved from a catalogue named
   in the URL.
3. **Catalogue theme after authentication**, operator brand before it.

## Decision outcome

Chosen option: **3 — the operator brands the door, the catalogue brands the room.**

Option 2 is what a customer-facing portal ideally does, and it fails on the public
endpoint: to theme the sign-in screen per catalogue, an unauthenticated caller must be
able to ask for a named catalogue's theme, which turns a public route into an oracle
for which catalogues exist and what they are called. For an instance whose catalogues
are named after customers, that is a disclosure with no compensating benefit at this
stage.

So `settings/theme.json` stays exactly as it is, serving exactly what it serves today,
and the sign-in screen, the OAuth consent screen and the public start-form page are
unchanged. What is added is a theme **on the catalogue record**, applied once a
principal is known.

### What a catalogue theme holds

The same three things ADR-0113 and ADR-0263 already paint from, and nothing more
(colours, logo, typeface — the stated scope):

- **the source accent**, one hex value. Hover, soft and `--accent-ink` stay derived in
  `theme.js`. Storing derived shades per catalogue would put that derivation in eleven
  places and let one of them be wrong;
- **a logo**, as the instance logo is stored today;
- **a typeface**, chosen from a fixed set of stacks the binary ships.

The typeface is a list rather than a URL on purpose. A web-font URL would mean every
portal page load reaching a third party, carrying the visitor's address there — an
outbound dependency on a page that must render when nothing else is reachable
(ADR-0263 already refuses to await even Atlas's own settings endpoints for that
reason), and a data flow an operator in public administration cannot accept on a
customer-facing surface. A catalogue that needs a corporate face beyond the shipped
stacks is a case for a later record with a font-upload path, not for an outbound
request added quietly.

Contrast needs no new thinking: `--accent-ink` is computed from whichever accent is
active, so the tenth brand is as legible as the first. That is the property ADR-0263
bought, and this is the first place it pays.

### How it is applied without a flash

The no-flash bootstrap in `index.html` applies a cached variable map under the
`--accent` prefix before first paint, then `syncFromServer()` reconciles it and the
server wins. That mechanism is reused unchanged, with one addition: the cache records
**which catalogue** it was painted for, alongside the values.

So a returning user is painted in their catalogue's brand from the first frame, and a
first-time user sees the operator's brand until their catalogue resolves. An
unreachable server leaves the cached paint intact, exactly as today. A user whose
catalogue changed sees one correction on the first load after the change, which is the
same behaviour ADR-0113 already has when an admin changes the accent.

### Who maintains it

`admin`, as the instance brand is maintained today. The product manager
([ADR-draft-portal-roles-and-responsibilities](draft-portal-roles-and-responsibilities.md))
maintains the catalogue's contents, not its appearance.

That is the smaller change deliberately: leaving the theme with `admin` means the logo
upload keeps the admin gate it has, with no new file-validation surface on a route a
less-privileged role can reach. The cost is that ten brands go through one queue —
acceptable, because a brand changes rarely and a catalogue's contents change weekly. If
that turns out to be the wrong trade, moving it to the catalogue's editor is a route
annotation and an upload check, not a redesign.

### Consequences

- **Positive:** Ten brands, one derivation, one no-flash path, no new contrast problem
  and no new public surface. ADR-0113's record and its endpoints are untouched, so the
  sign-in screen, the consent screen and public forms keep working with no change.
- **Negative / trade-offs accepted:** The first screen an external customer sees is not
  their brand. Brand changes queue behind `admin`. A catalogue cannot use a typeface
  outside the shipped set.
- **Follow-ups / risks to watch:** The open question above. Whether the theme should
  travel in the design-time backup allowlist (ADR-0107) as `settings/theme.json` does —
  it should, and the catalogue record carrying it means it does so for free, which is
  worth a test rather than an assumption.

## Pros and cons of the options

### One brand still
- Good: nothing to build, nothing to review.
- Bad: does not meet the requirement. Ten customer groups on one instance see the same
  face.

### Catalogue theme everywhere, including sign-in
- Good: an external customer never sees anybody else's brand.
- Bad: makes a public endpoint answer questions about named catalogues, disclosing the
  inventory and the naming to anybody who asks.

### Catalogue theme after authentication
- Good: no new public surface; reuses the whole existing path; the change is a field on
  a record.
- Bad: a visible brand transition at sign-in, whose acceptability is the open question.

## Links

- extends [ADR-0113](0113-org-wide-ui-theme.md) — the brand is a catalogue property when a catalogue is known, an instance property otherwise
- extends [ADR-0263](0263-form-runtime-brand-theming.md) — the derived `--accent-ink` is what makes ten accents safe
- themes the catalogue defined in [ADR-draft-portal-catalogue-order-inventory](draft-portal-catalogue-order-inventory.md)
- maintenance stays with `admin` rather than the role in [ADR-draft-portal-roles-and-responsibilities](draft-portal-roles-and-responsibilities.md)
