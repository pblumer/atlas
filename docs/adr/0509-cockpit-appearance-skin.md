# ADR-0509: A gamified cockpit skin as an opt-in appearance

- **Status:** Proposed
- **Implementation:** Partial
- **Date:** 2026-10-28
- **Deciders:** atlas maintainers

## Context and problem statement

ADR-0507 separated **appearance** (`light | dark | system`, how a browser draws the
console, an accessibility and coherence preference) from the organisation **brand
accent** (ADR-0113). It deliberately scoped appearance to those three words and
guards against advertising unreleased dark imagery as a shipped feature.

Separately, a product request asks for a **gamified skin** — a starship-cockpit look
with cyan HUD instrumentation and subtle motion — as an opt-in bit of fun, not a
default and not a brand statement. The question is where such a skin lives without
inventing a second theming engine, and without muddying ADR-0507's accessibility
axis or breaking its marketing guard.

## Decision drivers

- Reuse, don't reinvent: ADR-0507 already paints the whole chrome, the bpmn-js/dmn-js
  canvases and the code editors from semantic tokens and vendor bridges keyed on
  `data-appearance`. A skin should ride that, not fork it.
- A skin is a **personal** opt-in, never the default and never chosen by the system
  scheme — the opposite of an accessibility default.
- Buildless (ADR-0012): vanilla CSS/JS only; motion must honour
  `prefers-reduced-motion` and must not become a performance drag.
- Display-only: it touches no server, workflow or engine state.
- Honesty (ADR-0507's marketing guard): the skin is a labelled bit of fun, not
  advertised as a core capability, and no mockup of it implies a runnable feature.

## Considered options

1. **A fourth `data-appearance` value, `cockpit`** — reuse ADR-0507's mechanism,
   tokens and vendor bridges; add one dark-surfaced palette block plus pure-CSS
   decoration.
2. **A separate skin axis** (e.g. `data-skin`) orthogonal to appearance, layered on
   dark — two attributes, two resolvers.
3. **An org-wide theme** stored on the server — one cockpit for the whole instance.

## Decision outcome

Chosen option: **1 — a fourth `data-appearance` value**.

- **Mechanism:** `cockpit` joins `APPEARANCES` in `appearance.js`; `resolveAppearance`
  returns `"cockpit"` for it (an explicit choice the system scheme never selects), and
  the inline first-paint bootstrap in `index.html` mirrors that. The preference lives
  only in `localStorage["atlas.appearance"]`, exactly like the others.
- **Palette:** a `:root[data-appearance="cockpit"]` block answers **every** palette
  colour token the dark block answers (held honest by
  `TestDarkSurfacedAppearancesAnswerEveryPaletteColour`), in a deep-space, cyan-HUD
  key, with `color-scheme: dark`. Because the diagram renderer configuration
  (`DIAGRAM_RENDERER`, ADR-0507) reads `var(--diagram-*)` and the vendor bridges read
  the semantic tokens, the bpmn-js/dmn-js canvases, the palette, context pad, popups
  and the code editors theme for cockpit through the **same** rules — those selectors
  simply add `cockpit` beside `dark`. No new renderer, no forked editor.
- **Decoration:** a drifting starfield/nebula backdrop, a soft HUD glow, a radar sweep
  over the brand mark, faint scanlines and status lights — all pure CSS, decorative
  only (`pointer-events: none`, behind the content), and **fully disabled under
  `prefers-reduced-motion`**, which leaves the static cockpit palette and glow intact.
- **Selection:** like `dark` today, cockpit is reachable through the preference but not
  yet offered by a visible control; ADR-0507's S1 switch is where all four appearances
  become selectable in the UI. The brand accent (ADR-0113) stays inline and still
  applies; cockpit ships its own accent that a set org accent overrides.
- **Honesty:** cockpit is a labelled skin, not a core feature claim; it inherits
  ADR-0507's marketing guard.

### Consequences

- **Positive:** the gamified skin with essentially no new infrastructure — one palette
  block, a handful of shared selectors widened, and decoration; it tracks ADR-0507's
  dark coverage automatically as that work lands.
- **Negative / trade-offs accepted:** `cockpit` stretches the appearance axis beyond
  the accessibility `light|dark|system` ADR-0507 framed; it is a per-browser opt-in
  with no cross-device sync; the decoration is bespoke CSS to maintain.
- **Follow-ups / risks to watch:** the visible switch (shared with ADR-0507 S1) must
  offer cockpit; the Atlas-specific canvas overlays, form-js and other canvases
  ADR-0507 lists as not-yet-dark are equally not cockpit-verified; an `e2e`
  cockpit case should join `e2e/appearance.spec.mjs` once the switch exists.

## Pros and cons of the options

### Option 1 — a fourth `data-appearance` value (chosen)
- Good: maximum reuse of ADR-0507's tokens, bridges and bootstrap; one palette block.
- Bad: widens the appearance axis past its accessibility framing.

### Option 2 — a separate skin axis
- Good: keeps appearance purely accessibility; a skin can layer on any appearance.
- Bad: a second attribute and resolver, two bootstraps, more to test, for one skin.

### Option 3 — an org-wide server-stored theme
- Good: central, cross-device.
- Bad: a fun opt-in is not an organisation decision; needs an endpoint and admin gate.

## Links

- builds on ADR-0507 (appearance is a personal preference) — shares its mechanism,
  tokens, vendor bridges and marketing guard
- relates to ADR-0113 (org-wide brand accent), preserved and independent
- relates to ADR-0012 (buildless web UI) — vanilla CSS/JS only
