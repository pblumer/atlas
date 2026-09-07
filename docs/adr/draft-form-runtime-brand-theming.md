# ADR-DRAFT: The brand palette reaches the form runtime

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0113 gave an instance one brand accent, stored server-side and applied to the
Console by overriding three CSS custom properties on the document root. That works
because the Console's chrome is painted from those properties.

The forms a person actually fills in are not painted from them. Every form — a user
task in the Tasks app, an incident's repair form (ADR-0169), the start form behind a
public share link (ADR-0029) — is rendered by the vendored form-js runtime
(ADR-0028), which ships its own stylesheet, its own colours and its own typeface. So
an organisation that set its brand colour got a tinted Console around a form that was
still stock blue, in a different font, on borders a shade off the ones beside it.

Two related gaps sat next to it:

- The **public** start-form page and the OAuth consent screen carried a hard-coded
  copy of the default palette and the built-in Atlas mark. They asked the server for
  neither the accent nor the logo — so the one surface an organisation's own
  customers see was the one surface that ignored its branding entirely.
- Text on an accent fill was the literal `#fff`, written out at some twenty rules.
  That is correct for the stock blue and a guess for anybody else's colour: white on
  a pale brand yellow is roughly 1.5:1, a primary button no accessibility review
  passes, and nothing in the CSS could express the dependency.

## Decision drivers

- A form is where a person does the work. If branding stops at its edge, the instance
  does not read as the organisation's, whatever the chrome does.
- The form runtime is **vendored** (ADR-0013). Whatever is done must survive an
  upgrade of it, and must not become a fork to maintain.
- A themed UI must not become an unreadable one. A brand colour is chosen for a logo,
  not for a button label.
- No engine involvement: this is design-time display configuration, as ADR-0113 set
  out.

## Considered options

1. **Fork the vendored form-js stylesheet** and rewrite its colours to Atlas tokens.
2. **Restyle form-js from Atlas CSS by selector** — `.fjs-input { … }` and so on.
3. **Map Atlas's palette onto the tokens form-js already reads**, in one bridge
   stylesheet loaded wherever a form is rendered.

## Decision outcome

Chosen option: **"Map onto the tokens form-js already reads"** — `api/web/form-theme.css`.

form-js resolves each of its own colours through an IBM Carbon token first and a
built-in default second, e.g. `--color-text: var(--cds-text-primary, <grey>)`. That
Carbon layer is not declared on `.fjs-container` (its reset to `initial` sits on
`.fjs-no-theme`, a class the runtime never emits), so naming those tokens on `:root`
inherits all the way into a form. The bridge therefore restyles nothing by selector:
it assigns tokens, and form-js's own rules do the painting. The handful of tokens
form-js declares on the container itself with no Carbon backing — its typeface and
font sizes — are overridden at `:root .fjs-container`, which outranks that
declaration on specificity rather than on load order.

Two things follow from the same decision:

- **The palette gained `--accent-ink`**, derived in `theme.js` from the accent's own
  WCAG relative luminance: white or the UI's near-black, whichever contrasts more
  against the chosen colour. Every rule that paints on an accent fill — the Console's
  primary button included — reads it instead of restating `#fff`. Its name begins with
  `--accent` deliberately, so the existing no-flash bootstrap in `index.html`, which
  applies exactly that prefix, carries it to first paint with no change.
- **The two public pages apply the org accent and logo**, through the same public
  `GET /api/v1/settings/theme` and `/api/v1/settings/logo` the Console uses. Neither
  request is awaited: an unreachable settings endpoint must never be the reason a
  form or a consent decision does not appear.

One deviation from "assign tokens only" is deliberate and documented in the file: a
form schema's Button component is painted grey rather than in the accent form-js
asks for, because form-js's own `:read-only` rule outranks its accent rule and
`:read-only` matches every button. That one needs a rule, not a token.

### Consequences

- **Positive:** A form, its fields, its labels, its focus ring and its buttons carry
  the organisation's colour and the Console's typeface. The public start form —
  the surface with the widest audience and the least branding — is themed like the
  rest. A pale brand colour no longer produces an unreadable primary button.
- **Negative / trade-offs accepted:** The bridge is coupled to form-js's token names,
  which are not a public API of that project; an upgrade can rename them. That is
  answered with tests rather than with hope: `api/formtheme_internal_test.go` checks
  both directions — every token the bridge writes is one form-js reads, and every
  token it reads is one its host pages declare — and fails loudly on a rename instead
  of quietly reverting a themed form to stock. The "inverted" grey family is left
  unmapped, because form-js spends those tokens on two things at once.
- **Follow-ups / risks to watch:** Only the accent family is server-configurable.
  Surface, text, border and status colours, the type stack (`--font-sans` /
  `--font-mono` exist as tokens now but nothing can set them), and the shape scale
  are still fixed, so a full corporate design — a house typeface, a radius, a dark or
  high-contrast scheme — needs the theme record to carry more than one colour, and
  needs the ~180 colour literals still written into `app.css` to become tokens first.
  Delivering the palette as a server-rendered stylesheet would also let the public
  pages paint the brand before first paint rather than just after it.

## Pros and cons of the options

### Option 1 — fork the vendored stylesheet
- Good: total control; no dependence on which tokens the vendor happens to expose.
- Bad: every form-js upgrade becomes a merge of an 86 KB stylesheet. ADR-0013 vendors
  precisely to avoid that kind of ownership.

### Option 2 — restyle by selector from Atlas CSS
- Good: no dependence on the vendor's token layer.
- Bad: couples Atlas to form-js's *class names*, which change more often than its
  tokens, and fails silently when one does. It also re-implements decisions form-js
  already makes correctly (states, spacing, focus), so the two drift apart.

### Option 3 — map onto the tokens form-js reads
- Good: no fork, no selector coupling, ~60 lines; the vendor keeps deciding how a
  form looks and Atlas only says in which colours.
- Bad: depends on the vendor's token names and on their not being reset closer to the
  container — both assumptions now held by tests rather than by inspection.

## Links

- relates to ADR-0113 (org-wide UI brand theme) — extends its palette with `--accent-ink`
- relates to ADR-0148 (org-wide brand logo) — the public pages now apply it
- relates to ADR-0028 (forms and the Tasks app), ADR-0013 (vendoring), ADR-0169
  (incident repair forms), ADR-0029 (public share links)
