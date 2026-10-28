# ADR-0507: Appearance mode is a personal preference, not an organisation brand

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-28
- **Deciders:** atlas maintainers
- **Milestone:** S — Single-binary server & web UI; A — Modeler & authoring experience
- **Review gate:** Do not advertise a dark Console/Modeler/Operations screenshot as an implemented feature until browser-level checks pass on the actual shipped UI.

## Context

The existing Console is light-only. `api/web/app.css` defines `color-scheme: light` and a light semantic palette; `api/web/index.html` and `api/web/theme.js` support organisation-wide **accent colours**, not appearance modes. The design mockup at `docs/design/modeler-kopfleiste-mockups.html` states this explicitly. The dark UI image discussed for `pblumer/atlas-cloud` is aspirational art, not a screenshot of a released atlas feature.

Nevertheless, a coherent dark appearance is valuable across Console, Modeler, Tasks, Operations, Insights, the BPMN/DMN workspaces, user-task and start forms, and the heatmap. A single new body background would produce mixed light/dark panels and misleading screenshots.

Existing architectural decisions constrain the solution:

- [ADR-0012](0012-web-ui-app-shell.md) and [ADR-0013](0013-embed-bpmn-js-modeler.md): buildless, embedded web UI; do not fork the BPMN renderer.
- [ADR-0113](0113-org-wide-ui-theme.md): an organisation-wide brand **accent** is authoritative on the server and cached locally to prevent flash.
- [ADR-0263](0263-form-runtime-brand-theming.md): form-js is themed through the vendor's existing tokens, not a fork; contrast must be checked.
- [ADR-0316](0316-portal-theme-per-catalogue.md): a catalogue can override a brand after authentication; the login page keeps the operator's brand.
- [ADR-0496](0496-operations-heatmap.md): heatmap styles use meaningful blue-to-red intensity, badges, dashed never-taken flows, and modes Live/Runs/Waiting/Working/Duration.

The UI change must never touch processor, WAL, event sourcing, routing, replay or runtime semantics.

## Options

1. **Keep atlas light only.** Market the dark console as a labelled future concept. No engineering cost, but the product does not match the design direction.
2. **Add a set of dark CSS overrides or invert the canvas.** Fast to prototype but fragile: hard-coded light colours, vendor widgets, semantic warning colours and heatmap contrast cannot be corrected systematically. Rejected as the delivery design.
3. **Introduce semantic surface/text/status tokens and one personal appearance mode**, then adapt the vendor-token bridges and diagram/canvas wrappers in focused slices. Chosen *proposal*, subject to acceptance and implementation evidence.
4. **Make dark mode another admin-wide theme or catalogue brand property.** Rejected: reading conditions and individual preference are not the same thing as shared branding; this would undermine ADR-0113's ownership model.

## Proposed decision

### Separate appearance from brand

- **Brand accent** stays operator- or catalogue-scoped, from the existing endpoints and derivation in `theme.js`. Do not create another palette resolver.
- **Appearance** is a non-sensitive *browser preference* `light | dark | system`, with `system` following `prefers-color-scheme`. It does not change server or workflow state and needs no new HTTP endpoint. A later authenticated, cross-device preference would require its own design.
- **Default**: preserve today's light mode until dark-mode coverage is verified. Prefer a clearly discoverable appearance switch in the account menu. The chosen preference must apply to the login screen and across app navigation.
- **First paint**: the small bootstrap in `api/web/index.html` sets `data-appearance` before styles paint, independently of the organisation accent cache, with safe fallback for storage disabled/invalid. The system listener updates only `system` mode and never overrides an explicit choice.
- **CSS**: declare light/dark values for existing semantic tokens such as `--bg`, `--surface`, `--diagram-bg`, `--diagram-dot`, `--border`, `--border-strong`, `--text`, `--muted`, `--danger`, `--warn`, plus popup/hover/status variants needed by actual components. Replace hard-coded light literals in the affected components instead of adding generic inversion filters.
- **Accent soft/hover on dark surfaces**: adapt the shared accent derivation to be appearance-aware (or express the soft surface via CSS) while keeping one implementation, clear precedence and readable `--accent-ink` across customer colours. Never make a dark theme silently override catalogue branding.
- **Embedded widgets**: use supported vendor tokens for form-js; inspect `bpmn-js`, `dmn-js` and any code editors' theme APIs and canvas layers separately. Do not maintain a fork or invent a BPMN renderer. Test the vendor controls (palette, overlays, minimap, properties panel, menus, form elements) and untouched BPMN semantics in both modes.
- **Operations heatmap**: preserve ADR-0496's five semantic heat modes, square-root scale, glow/outline choice, task/flow shading, zero/dashed states and legend. Validate the same values against both canvas backgrounds; adjust contrast/luminance in the render layer only, not the underlying statistics.
- **Contrast and accessibility**: verify WCAG AA for normal text, active/disabled focus, badges, incidents, decisions and information overlays. Honour reduced motion and forced-colours. Do not encode outcomes by colour alone.
- **Scope**: include Console, Modeler, Tasks, Operations, Insights and shared dialogs in the initial public feature claim. Catalogue/customer portals and public start forms must be explicitly audited and have a declared light fallback if they are not in the initial release.

### Implementation and acceptance slices

1. **S1 — shell and preference:** red browser tests for first-paint flash and preference persistence, then CSS semantic palette and `light/dark/system` switching. Test login/logout, routing, organisation accents and catalogue branding.
2. **S2 — process modelling and forms:** test the *actual* bpmn-js/DMN editor and form-js, complete the token bridges and canvas backgrounds. No CSS filters/inversions. Preserve light appearance and vendor branding.
3. **S3 — live runtime and heatmap:** test token visibility, badges, incidents, panels, heatmap glow/outline in all modes, as well as the previously unvisited branch and the relative colour scale.
4. **S4 — screenshot/marketing contract:** capture reviewed screenshots from a built atlas binary with the dark mode selected; only then replace the `atlas-cloud` concept art. Automated UI/e2e checks for both appearance modes remain part of CI.

Maintain the project definition of done: build, full test suite, race tests (with documented timeouts), vet, format and Playwright tests. This is a display-only change; no recovery test is required unless implementation unexpectedly changes persistence or engine behaviour, in which case stop and review scope.

## Consequences and risks

- Positive: honest product imagery, stronger visual coherence, individual accessibility preference, reusable components across website and product.
- Negative: significant CSS audit (including numerous fixed literals), vendor widget differences, colour contrast and screenshot testing overhead.
- Compatibility: existing light installations and organisation accent themes must remain visually unchanged by default. Resetting appearance cannot alter the stored organisation accent.
- Rollback: remove appearance override and return to the existing light palette without migrations.
- **Marketing guard:** A website simulation in `atlas-cloud` may use dark styling but must be labelled a **conceptual illustration** until atlas itself ships the feature. Never imply that an animated web marketing mockup proves a runnable BPMN model or an operational heatmap.

## Alternatives not taken

- Inverting CSS with `filter: invert()` (distorts screenshots, colour semantics and logos).
- A separate skin under `atlas-cloud` that pretends to be the actual Console.
- Full server-persisted, arbitrary per-user theme tokens (needless identity and storage coupling for an appearance preference).

## References

- [Theme logic](../../api/web/theme.js)
- [Current light palette](../../api/web/app.css)
- [Form theming bridge](../../api/web/form-theme.css)
- [Operations heatmap](../../api/web/heatoverlay.js)
- [Website](https://github.com/pblumer/atlas-cloud)
