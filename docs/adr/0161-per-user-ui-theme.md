# ADR-0161: Per-user UI theme (visual skins)

- **Status:** Proposed
- **Date:** 2026-10-28
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0113 gave the Console a single org-wide **brand accent** — the one colour an
admin sets for the whole instance. It deliberately stopped there, and its own
follow-up note anticipated this record: *"If per-tenant or per-user theming is
ever needed … A future richer theme could store more tokens."*

Operators now want selectable **visual themes** (full skins), not just a re-tint:
a light theme (today's look), a **dark** theme, and a playful, gamified
**spaceship-cockpit** theme with subtle motion. The whole UI chrome is already
painted from a complete set of CSS custom properties on `:root` in `app.css`
(`--bg`, `--surface`, `--border`, `--text`, `--muted`, the `--accent` trio,
status colours, `--shadow`, `--radius`), so a skin is a matter of overriding that
variable set — no component CSS changes. The open questions are *where a chosen
theme lives* and *how it coexists with the org accent*.

A theme like "cockpit" is a personal, fun preference, not a company brand
statement. The brand accent (ADR-0113) is the opposite: one value for everyone,
applied pre-login on the shared sign-in screen. These are different in kind, so
conflating them into one org-wide setting would be wrong.

## Decision drivers

- A visual skin is a **personal** preference — each user picks their own; it must
  not be forced instance-wide the way the brand accent is.
- Must coexist with ADR-0113's org accent without fighting it: a neutral theme
  defers to the org accent; an opinionated theme (cockpit) may define its own.
- No flash of the wrong theme on load — apply before first paint, exactly as the
  accent bootstrap and ADR-0160's language bootstrap already do.
- **Buildless** (ADR-0012): vanilla CSS/JS only, no npm/bundler. Motion must honour
  `prefers-reduced-motion` and never become a performance drag.
- Display configuration only — it must not touch the processor, the WAL, or any
  hot-path invariant.

## Considered options

1. **Org-wide theme, server-stored** — generalise ADR-0113's `settingsStore`
   record to carry a theme id, admin-gated like the accent.
2. **Per-user theme, client-stored** — the chosen theme id lives in
   `localStorage` (`atlas.theme.mode`); a `data-theme` attribute on the document
   root selects a palette block in `app.css`; applied pre-paint by the inline
   bootstrap. The org accent stays exactly as ADR-0113 left it.
3. **Full tokenised theme engine** — a server table of every token with named
   themes and per-user overrides layered over org defaults.

## Decision outcome

Chosen option: **"Per-user theme, client-stored"**.

A skin is personalisation, so it lives where personalisation belongs — the user's
own browser — and needs no endpoint, store, admin gate, or backup entry. This
keeps the change entirely in `api/web/` and entirely orthogonal to the org accent.

- **Selection:** `localStorage["atlas.theme.mode"]` holds one id: `light`
  (default), `dark`, or `cockpit`. `theme.js` applies it by setting
  `data-theme="…"` on `<html>` (absent/`light` = the stock `:root`).
- **Palettes:** `app.css` gains `:root[data-theme="dark"]` and
  `:root[data-theme="cockpit"]` blocks that override the existing variable set.
  Because every surface already reads those variables, the whole chrome re-skins
  with no per-component edits.
- **Accent coexistence (ADR-0113 preserved):** `light` and `dark` leave
  `--accent*` untouched, so the org brand accent still tints them. `cockpit` is an
  opinionated skin that sets its own accent; the org accent, when present, is an
  inline style on the root (set by `theme.js`) and therefore still wins over the
  stylesheet for operators who want brand colour even inside the cockpit. The two
  mechanisms are independent: changing one never clears the other.
- **No-flash boot:** the inline bootstrap in `index.html` — the same one that
  applies the cached accent and ADR-0160's language — reads `atlas.theme.mode` and
  sets `data-theme` before first paint.
- **Motion:** the cockpit theme's decorative motion (an animated starfield/nebula
  backdrop, a soft HUD glow and pulse on accents, faint scanlines) is pure CSS,
  decorative-only (`aria-hidden`, `pointer-events:none`), and fully disabled under
  `@media (prefers-reduced-motion: reduce)`, which leaves the static cockpit
  palette intact.

Option 1 was rejected because a fun skin is not an org brand decision and must not
be one-per-instance. Option 3 is far more than the ask — the token indirection is
already in the CSS, so named skins need no server engine; it can be revisited if
users ever need to author and sync their own themes.

### Consequences

- **Positive:** Each user picks light/dark/cockpit; dark mode essentially free
  given the existing variables; the cockpit theme adds the requested gamification
  without new infrastructure. Zero backend, zero engine impact, no new ADR-0107
  backup surface. ADR-0113's org accent is untouched and still applies.
- **Negative / trade-offs accepted:** The choice is per-browser, so it does not
  follow a user across devices and cannot be centrally mandated. There is no
  server record of which skins exist — the set is defined in `app.css`/`theme.js`.
- **Follow-ups / risks to watch:** If a user-level setting that syncs across
  devices is ever wanted, the id can move to a per-principal store without
  changing how the browser applies it (still a `data-theme` attribute). An
  org-default theme (admin picks the initial skin, user may override) is a natural
  later extension that reuses ADR-0113's store.

## Pros and cons of the options

### Option 1 — Org-wide theme, server-stored
- Good: central control; reuses ADR-0113 exactly; syncs across devices.
- Bad: a visual skin is not a brand decision — forcing one cockpit on the whole
  instance is wrong; needs admin gating for a personal preference.

### Option 2 — Per-user theme, client-stored (chosen)
- Good: matches the preference's nature; no backend, endpoint, gate or backup;
  pre-paint apply already solved; orthogonal to the org accent.
- Bad: per-browser only (no cross-device sync, no central mandate).

### Option 3 — Full tokenised theme engine
- Good: maximum flexibility — user-authored themes, layered defaults.
- Bad: large surface (persist, validate, back up, test) for an ask that three
  named skins already satisfy.

## Links

- relates to ADR-0113 (org-wide UI brand theme) — this is its anticipated
  per-user follow-up; the accent mechanism is preserved unchanged
- relates to ADR-0148 (org-wide brand logo) and ADR-0160 (multilingual web UI) —
  shares the pre-paint `localStorage` bootstrap pattern
- relates to ADR-0012 (buildless web UI) — themes stay vanilla CSS/JS
