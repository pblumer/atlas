# ADR-0113: Org-wide UI brand theme

- **Status:** Accepted
- **Date:** 2026-08-11
- **Deciders:** Atlas maintainers

## Context and problem statement

Operators want the Console to carry their organisation's brand colour rather than
the stock Atlas blue. The whole UI chrome (buttons, links, active navigation, focus
rings, pills, toggles) is already painted from three CSS custom properties
(`--accent`, `--accent-hover`, `--accent-soft`) declared on `:root`, so re-tinting
is a matter of overriding those. The open question is *where the chosen colour
lives*: only in the operator's browser, or once for the whole instance.

An initial cut stored the accent in `localStorage`. That is per-browser: every user
would have to set it, and it could not be a genuine organisation setting. We want
one brand colour applied for everyone on the instance.

## Decision drivers

- The brand colour is an organisation property, not a personal preference — it must
  be shared across all users and devices.
- It must be applied before login (the sign-in screen should already be branded)
  and without a flash of the default colour on load.
- Operational simplicity: reuse the existing sidecar-store and admin-gating
  patterns rather than introduce new machinery.
- It is display configuration, not engine state — it must not touch the processor,
  the WAL, or any invariant on the hot path.

## Considered options

1. **Per-browser only** — keep the accent in `localStorage`.
2. **Server-stored, org-wide** — persist the accent in a design-time sidecar store
   behind a small `/api/v1/settings/theme` endpoint; the browser caches it only to
   avoid a flash.
3. **Full theming system** — a table of every colour/spacing token, multiple named
   themes, per-user overrides.

## Decision outcome

Chosen option: **"Server-stored, org-wide"**.

A new singleton `settingsStore` persists one record, `settings/theme.json`, holding
just the source accent hex (`{ "accent": "#rrggbb" }`). It follows the same
atomic-write + directory-fsync discipline as the other design-time sidecar stores
(ADR-0019/0041) and is owned by the server run-loop goroutine (accessed via `s.do`),
so it needs no locking. Three endpoints expose it:

- `GET /api/v1/settings/theme` — **public** (like `/api/v1/info`), so the login
  screen and every browser can apply the brand colour before authenticating.
- `PUT /api/v1/settings/theme` — **admin-gated** when auth is on; validates and
  canonicalises the hex server-side (`normalizeAccent`).
- `DELETE /api/v1/settings/theme` — **admin-gated**; resets to the built-in default.

The server stores only the *source* accent. The hover/soft shades are derived in
the browser (`theme.js`, `derivePalette`), keeping that derivation in exactly one
place. `localStorage` remains solely as a no-flash cache: a tiny inline bootstrap in
`index.html` applies the cached variable map before first paint, and `app.js` calls
`syncFromServer()` on boot to reconcile the cache with the authoritative server
value (the server always wins; an unreachable server leaves the cached paint
intact). The theme file is added to the design-time backup allowlist (ADR-0107) so
it travels with a backup; it holds no secret material.

Option 3 was rejected as far more than the need — a single brand accent covers the
ask, and the token indirection is already in the CSS if a richer system is ever
wanted.

### Consequences

- **Positive:** One brand colour for the whole instance, set once by an admin,
  applied for every user and on the login screen. No new storage mechanism — it
  reuses the sidecar/admin-gate patterns. No engine or hot-path involvement.
- **Negative / trade-offs accepted:** The colour is instance-wide, not per-user or
  per-tenant; there is no multi-theme support. Writes are gated by the single
  `admin` role the MVP enforces, not a finer "branding" permission.
- **Follow-ups / risks to watch:** If per-tenant or per-user theming is ever needed,
  the store and endpoint generalise (key the record, widen the payload) without
  changing how the browser applies it. A future richer theme could store more tokens
  in the same record.

## Pros and cons of the options

### Option 1 — Per-browser only
- Good: zero backend; trivial.
- Bad: not an organisation setting — every user/device must set it; can't brand the
  shared login screen consistently.

### Option 2 — Server-stored, org-wide (chosen)
- Good: genuinely org-wide; public read brands the login screen; reuses existing
  patterns; backup-friendly; no engine impact.
- Bad: needs an endpoint and a store (small); one colour for the whole instance.

### Option 3 — Full theming system
- Good: maximum flexibility (many tokens, named themes, per-user).
- Bad: large surface for a feature whose ask is a single brand colour; more to
  validate, persist, back up, and test.
