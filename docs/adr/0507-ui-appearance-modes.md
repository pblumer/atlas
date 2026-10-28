# ADR-0507: Appearance mode is a personal preference, not an organisation brand

- **Status:** Proposed
- **Implementation:** Partial
- **Date:** 2026-10-28
- **Deciders:** atlas maintainers
- **Open question:** Whether a light ground behind the bpmn.io watermark, rather than a recoloured logo, is what the bpmn-js and dmn-js licence means by the watermark staying "fully visible" and not being "changed" — the licence names the source code and visibility, not colour
- **Question checked:** 2026-10

Milestones S (single-binary server and web UI) and A (Modeler and authoring
experience). Review gate: no dark Console, Modeler or Operations screenshot is
advertised as an implemented feature until browser-level checks pass on the shipped
UI.

## Context and problem statement

The Console is light-only. `api/web/app.css` declares `color-scheme: light` and one
light semantic palette; `api/web/index.html` and `api/web/theme.js` carry an
organisation-wide **accent colour**, which is not an appearance mode. The design
mockup at `docs/design/modeler-kopfleiste-mockups.html` says so explicitly, and the
dark UI image discussed for `pblumer/atlas-cloud` is aspirational art, not a
screenshot of a released atlas feature.

A coherent dark appearance is nevertheless valuable across Console, Modeler, Tasks,
Operations, Insights, the BPMN and DMN workspaces, user-task and start forms, and the
heatmap. A new body background alone would produce mixed light and dark panels and
misleading screenshots.

Existing decisions constrain the solution:

- [ADR-0012](0012-web-ui-app-shell.md) and [ADR-0013](0013-embed-bpmn-js-modeler.md):
  buildless, embedded web UI; do not fork the BPMN renderer.
- [ADR-0113](0113-org-wide-ui-theme.md): an organisation-wide brand **accent** is
  authoritative on the server and cached locally to prevent flash.
- [ADR-0263](0263-form-runtime-brand-theming.md): form-js is themed through the
  vendor's existing tokens, not a fork; contrast must be checked.
- [ADR-0316](0316-portal-theme-per-catalogue.md): a catalogue can override a brand
  after authentication; the login page keeps the operator's brand.
- [ADR-0459](0459-display-preferences-per-user.md): how a person reads the console —
  number format, time zone, language — is a per-person preference stored on the
  server, with a browser mirror for the first paint.
- [ADR-0496](0496-operations-heatmap.md): heatmap styles use meaningful blue-to-red
  intensity, badges, dashed never-taken flows, and the modes Live, Runs, Waiting,
  Working and Duration.

The UI change must never touch the processor, the WAL, event sourcing, routing,
replay or runtime semantics.

## Decision drivers

- Today's installations look exactly as they do now unless somebody chooses otherwise.
- The brand colour stays the organisation's; reading conditions are the person's.
- No fork of a vendored editor, no generic inversion filter.
- Every colour claim is measured (WCAG 2.1), not judged by eye.
- An exported diagram or document is the same picture whatever the screen showed.

## Considered options

1. **Keep atlas light only.** Market the dark console as a labelled future concept.
   No engineering cost, but the product does not match the design direction.
2. **Add dark CSS overrides or invert the canvas** (`filter: invert()`). Fast to
   prototype but fragile: it turns the brand accent, the status colours, the Worker
   glyphs, model DI colours and the bpmn.io logo, and the heatmap's scale with them.
3. **Semantic surface, text and status tokens with one personal appearance mode**, then
   the vendor-token bridges and the diagram and canvas wrappers in focused slices.
4. **Dark mode as another admin-wide theme or catalogue brand property.** Reading
   conditions and individual preference are not shared branding; this would undermine
   ADR-0113's ownership model.
5. **Dark chrome around a light diagram canvas** ("dark UI, light paper"). The
   canvas would keep matching the export and the colours models were drawn in, and
   the overlay contrasts measured against white would stay valid. But the largest area
   on screen would stay bright, which is the one thing a dark appearance exists to
   avoid, and the prototype below shows the dark canvas itself is the cheap part.

## Decision outcome

Chosen option: **3**, subject to acceptance and to the remaining slices.

### Separate appearance from brand

- **Brand accent** stays operator- or catalogue-scoped, from the existing endpoints
  and the derivation in `theme.js`. There is no second palette resolver.
- **Appearance** is `light | dark | system`, with `system` following
  `prefers-color-scheme` and an explicit choice never overridden by it. It is the
  fourth of ADR-0459's display preferences (decided in S1, 2026-10-28): stored with
  the account in `/api/v1/me/preferences` as `appearance`, mirrored in the browser
  (`atlas.prefs`) for the first paint, and the whole store on an instance without
  sign-in. One person uses several browsers; `system` covers the person who wants each
  device to follow its own scheme. It changes no workflow state.
- **Default**: today's light mode until dark coverage is verified on every screen in
  scope; until then the dark choice is offered as a **preview** and says so. The
  switch sits in the top bar beside the favourites and flips between light and dark;
  the Display screen offers all three. It applies to the sign-in screen (from the
  mirror), right after signing in (the account's record is loaded before the first
  screen), and across navigation.
- **First paint**: the bootstrap in `api/web/index.html` writes `data-appearance`
  before the body is parsed, from the preferences mirror and independently of the
  accent cache, and falls back to light when storage is disabled or the value is
  invalid.
- **CSS**: light and dark values for the semantic tokens (`--bg`, `--surface`,
  `--diagram-bg`, `--diagram-dot`, `--border`, `--border-strong`, `--text`, `--muted`,
  `--danger`, `--warn` and the rest), and hard-coded light literals replaced in the
  components that carry them, not hidden behind an inversion.
- **Accent shades on dark surfaces**: the brand colour itself stays inline and
  untouched, in both appearances — a filled button is the same colour with the same
  ink. The shades derived from it for a light surface are derived for the dark one in
  CSS from whichever accent is in force. Where the accent is read as *text*, rules say
  `color: var(--accent-text)`: the accent itself in light, and in dark the tone
  theme.js derives for it (`accentOnDark`, `--accent-on-dark`), lightened towards white
  until it stands 4.5:1 on the dark surface and on its own soft tint.
- **Status fills**: text on a danger, ok, warning or muted fill reads `--status-ink`,
  white in light and near-black in dark, where the status colours are lightened to
  read as text.
- **Embedded widgets**: vendor tokens only — no forked editor and no invented
  renderer. The vendor controls (palette, overlays, minimap, properties panel, menus,
  form elements) and untouched BPMN semantics are tested in both modes.
- **Operations heatmap**: ADR-0496's five modes, square-root scale, glow and outline,
  task and flow shading, zero and dashed states and the legend are preserved; values
  are validated against both canvas backgrounds and adjusted in the render layer only.
- **Contrast and accessibility**: WCAG AA for text, focus, badges, incidents, decisions
  and overlays; reduced motion and forced colours honoured; no outcome encoded by
  colour alone.
- **Scope of the public claim**: Console, Modeler, Tasks, Operations, Insights and
  shared dialogs. Catalogue and customer portals and public start forms are audited
  explicitly and keep a declared light fallback until they are in.

### Slices

1. **S1 — shell and preference:** first-paint and persistence tests, the semantic
   palette, the visible switch, login and logout, routing, organisation accents and
   catalogue branding.
2. **S2 — process modelling and forms:** the actual bpmn-js, dmn-js and form-js
   editors, the token bridges and the canvas backgrounds.
3. **S3 — live runtime and heatmap:** token visibility, badges, incidents, panels,
   the heatmap glow and outline in all modes, the unvisited branch and the scale.
4. **S4 — screenshot contract:** reviewed screenshots from a built binary with dark
   selected; only then is the `atlas-cloud` concept art replaced. The e2e checks for
   both appearances stay in CI.

The definition of done is the project's: build, full test suite, race tests with the
documented timeout, vet, format, and the Playwright suite. This is a display-only
change; no recovery test is required unless an implementation changes persistence or
engine behaviour, in which case it stops for review.

### Consequences

- **Positive:** honest product imagery, visual coherence, an individual accessibility
  preference, components reusable between website and product.
- **Negative / trade-offs accepted:** a broad CSS audit (several hundred fixed
  literals), vendor widget differences, contrast and screenshot testing overhead.
- **Compatibility:** existing light installations and organisation accents are
  visually unchanged by default; resetting the appearance never alters the stored
  organisation accent.
- **Rollback:** remove the appearance attribute and the dark blocks; no migration.
- **Marketing guard:** a website simulation in `atlas-cloud` may use dark styling but
  is labelled a conceptual illustration until atlas ships the feature, and never
  implies that an animated mockup proves a runnable model or an operational heatmap.

## As built so far

### S1 — shell and preference (2026-10-28)

- **The preference** is `appearance` in `api/userprefs` (validated against
  `light`, `dark`, `system` or empty), carried by `prefs.js` with the other three and
  applied by it on every load and save; `appearance.js` draws it and keeps `system` and
  another tab's choice live. Signing in now loads the account's preferences before the
  first screen, which also settles ADR-0459's number format and zone at that moment
  rather than on the next reload.
- **The switches**: `#appearance-toggle` in the top bar beside the favourites (shown
  signed in or not; `aria-pressed` is the state, the title says what a press does) and
  the Display screen's fourth setting, whose dark option reads "Dunkel (Vorschau)".
- **The accent as text** is solved as described above, and the prototype's lighter
  stock accent in dark is withdrawn: the stock `#0b5cff` keeps white ink at 5.3:1 as a
  fill and reads as `#6095ff` (5.6:1) as text. `api/appearance_internal_test.go`
  refuses a rule that sets `color: var(--accent)`.
- **The shell audit** ran the console in dark against a real server with the
  examples deployed and instances, tasks and incidents present, scanning every screen
  for light surfaces and for text under 4.5:1 against what is actually behind it. What
  it found was fixed at the source: status fills now take `--status-ink`, the pale
  status washes read `--ok-soft`, `--danger-soft` and `--warn-soft`, the JSON editor's
  tokens joined the dark code palette, and the components that write a colour out got
  their dark answer in one block of `app.css`. Left on purpose: the mail HTML previews
  stay white, as the reader's mail client shows them.

### The editor prototype (2026-10-28)

The prototype landed first, behind a browser-only preference, to establish what the
editors cost. What it established:

- **The BPMN and DMN canvases need no fork and almost no code.** bpmn-js and dmn-js
  accept `defaultFillColor`, `defaultStrokeColor` and `defaultLabelColor`, and write
  them as inline *style* (tiny-svg sets `fill` and `stroke` as CSS properties), so a
  `var()` reference works: `appearance.js` `DIAGRAM_RENDERER` hands both renderers
  `var(--diagram-shape-fill, #fff)` and its two siblings. All five bpmn-js instances
  are built through `newModeler` in `editor.js`, dmn-js through one constructor in
  `dmn-editor.js`. A switch re-colours an open diagram without importing it again.
- **Exports stay light by construction.** The `var()` fallback is bpmn-js's own
  default, and a rasterized SVG is an image of its own that sees none of the page's
  properties, so the PDF documents keep the light picture they are laid out for. The
  renderer defaults never reach the XML; only explicit DI colours do.
- **diagram-js's chrome is a 30-line bridge.** The vendored `diagram-js.css` paints the
  palette, context pad, popups and search from custom properties on `.djs-parent`,
  with two literal colours in the whole sheet.
- **dmn-js and its properties panel are a 24-line bridge.** They declare some two
  hundred properties, but all resolve to one neutral primitive ramp; turning that ramp
  over in dark keeps every pairing's contrast.
- **The code editors** (script, FEEL, JSON, Markdown, templates) share 21 token
  classes; GitHub's dark palette keeps each hue at 5.3:1 or more on `--surface`.
- **The accent needed care.** The bootstrap applies theme.js's derived shades
  inline, which no ordinary rule outranks, so in dark `--accent-soft` and
  `--accent-hover` are derived in CSS with `!important`, leaving the brand colour
  itself inline. A brand colour as text was the open part — the federal red is 3.3:1
  on the dark surface, and `app.css` had 103 rules setting `color: var(--accent)` —
  and S1 solved it with `--accent-text` (above).
- **The watermark.** bpmn-js draws it in `#404040` and dmn-js in black — 1.73:1 and
  1.17:1 on the dark canvas. The logo is left as drawn and given a light ground
  instead (see the open question).
- **Measured, not judged:** `e2e/appearance.spec.mjs` holds the first paint, the
  `system` behaviour, broken storage, the brand cache, every shape of a diagram that
  carries most of what bpmn-js draws, label and arrow contrast, the live switch, the
  light export, the watermark, the code tokens in both palettes and the DRD;
  `api/appearance_internal_test.go` refuses a palette colour the dark block does not
  answer.

### Still open

The bulk of what remains is S2's and S3's: Atlas's own canvas overlays (problem, live
and replay state, token simulation, collaboration, heatmap — some 55 rules with fixed
colours, most of them `!important`), the form-js editor and runtime (the "inverse"
grey family ADR-0263 names), the ArchiMate, UML, decision-graph and Panorama canvases,
and the pages that load their own palette (the handbook pages follow
`prefers-color-scheme` on their own and ignore an explicit choice). The preview label
comes off, and the default can be reconsidered, when those are done and S4's
screenshots are reviewed.

## Pros and cons of the options

### Option 2 — inversion

- Good: one line; leaves model and export untouched.
- Bad: inverts what must not move — brand, status colours, logos, the heatmap scale.

### Option 3 — tokens and one personal mode

- Good: each surface is corrected where it is drawn; measurable; reversible.
- Bad: the audit is long, and the vendor bridges have to be maintained across upgrades.

### Option 5 — dark chrome, light canvas

- Good: the canvas matches every export; overlays keep their measured contrast.
- Bad: the brightest area remains; the canvas turned out to be the cheapest part.

## Links

- [Theme logic](../../api/web/theme.js), [appearance](../../api/web/appearance.js)
- [Current palette](../../api/web/app.css), [form theming bridge](../../api/web/form-theme.css)
- [Operations heatmap](../../api/web/heatoverlay.js)
- relates to ADR-0012, ADR-0013, ADR-0113, ADR-0263, ADR-0316, ADR-0459, ADR-0496
