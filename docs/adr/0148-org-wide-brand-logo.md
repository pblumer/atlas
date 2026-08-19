# ADR-0148: Org-wide brand logo

- **Status:** Accepted
- **Date:** 2026-08-19
- **Deciders:** Atlas maintainers

## Context and problem statement

An operator can already tint the whole Console with their organisation's brand
colour (ADR-0113). The other half of "make this instance look like ours" is the
**logo**: the Console and the login screen show a built-in mark — a white "A" on a
near-black tile — in the top bar and the app-switcher drawer. A customer, in
particular a Swiss federal office standardising on the CD Bund corporate design,
wants their own logo there instead.

The open questions are the same shape as the theme's: *where the logo lives* (per
browser or once for the whole instance), and — new here because the payload is an
uploaded **image**, not a short validated string — *how to store and serve
attacker-influenced bytes safely*, since an uploaded SVG can carry script.

## Decision drivers

- The logo is an organisation property, not a personal preference — it must be
  shared across all users and devices, and be visible before login (the sign-in
  screen should already carry the customer's mark).
- Reuse the existing org-wide-settings machinery (the settings sidecar store, the
  admin gate, the public pre-auth GET) rather than introduce new mechanisms.
- It is display configuration, not engine state — it must not touch the processor,
  the WAL, or any hot-path invariant.
- **Safety:** an uploaded SVG is untrusted markup. Serving it must not become a
  stored-XSS vector on the Console's own origin.

## Considered options

1. **Per-browser only** — keep the logo in `localStorage`. Rejected for the same
   reason as the theme's ADR-0113 option 1: it is not a genuine organisation
   setting and every user would have to set it.
2. **Server-stored, org-wide, as an image beside the theme** — persist the logo in
   the design-time settings directory behind a small `/api/v1/settings/logo`
   endpoint, mirroring `/settings/theme`; the browser caches only a presence flag
   to avoid a flash of the default mark.
3. **A full asset/media manager** — a general uploaded-asset store with multiple
   images, references, and a management UI. Rejected as over-scoped for one logo,
   the same way ADR-0113 rejected a full multi-token theme system.

## Decision outcome

Chosen option: **"Server-stored, org-wide, as an image beside the theme"**, because
it makes the logo a real instance setting with the least new machinery and keeps
the safety surface small and explicit.

- **Storage.** The logo is stored in the existing settings directory as exactly
  `logo.png` or `logo.svg` — the file extension is the single source of truth for
  its type, so no metadata sidecar is needed. It is written with the shared
  atomic-write + directory-fsync discipline (`api/sidecar`), so "nil error means on
  disk" holds as for every other sidecar. Because the settings directory is already
  captured by the design-time backup (ADR-0107), the logo is backed up and restored
  with no extra wiring.
- **Formats.** Only PNG (raster marks) and SVG (vector marks) are accepted, each
  capped at 512 KiB. The server re-validates the bytes against the declared
  Content-Type — the PNG magic signature, or well-formed UTF-8 containing an `<svg`
  root — so a mislabelled or bogus upload is never persisted.
- **API.** `GET /api/v1/settings/logo` returns the image (404 when none is set) and
  is **public**, like the theme, so the login screen can show it before auth.
  `PUT` (raw image body, Content-Type sets the format) and `DELETE` are
  **admin-gated** — they change what everyone sees.
- **Safety.** The app only ever renders the logo through an `<img>` element, where
  script inside an SVG never executes. As defence in depth for the case of someone
  opening the image URL directly, the `GET` response is served with
  `X-Content-Type-Options: nosniff` and a strict
  `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; sandbox`,
  which neutralises any script even when the SVG is the top-level document. This is
  a serving-time guarantee, not upload-time sanitisation — SVG sanitisation is
  notoriously incomplete, so we rely on never executing the content instead.
- **Client.** A tiny `logo.js` module mirrors `theme.js`: the server is the source
  of truth, `localStorage` caches only a boolean *presence* flag so the boot path
  paints the right mark without a flash, and `applyLogo` swaps every `.mark` box
  between the uploaded `<img>` and the built-in "A". The Appearance panel gains an
  upload/remove control beside the colour presets.

The colour side stays exactly as ADR-0113 decided — a single derived accent. The
"CD Bund" colour preset (the federal red, `#d52b1e`) is the one-click colour
template; a federal office pairs it with its own logo uploaded here. A fuller CD
Bund palette (multiple colour roles, typography) remains deliberately out of scope,
as in ADR-0113.

### Consequences

- **Positive:** an instance can carry a customer's full brand — colour *and* logo —
  with no build step and no new storage subsystem; both are org-wide, admin-gated,
  pre-auth-visible, and backed up. The safety posture for uploaded SVG is explicit
  and centralised in one handler.
- **Negative / trade-offs accepted:** the logo replaces the mark only in the SPA
  shell and login screen (index.html); the standalone `public-form.html` and
  `handbuch.html` pages keep the built-in mark for now. One logo at a time, two
  formats — no per-app or dark/light variants.
- **Follow-ups / risks to watch:** extend the logo to the standalone public pages
  if brand consistency there is requested; consider a separate favicon override
  (the tab icon is still the built-in inline SVG). If a richer CD Bund treatment is
  ever needed, it supersedes ADR-0113's accent-only decision, not this one.

## Pros and cons of the options

### Option 1 — Per-browser only
- Good: no server work.
- Bad: not an organisation setting; every user/device would set it; invisible to
  the login screen.

### Option 2 — Server-stored, org-wide (chosen)
- Good: reuses the settings store, admin gate, public GET and backup; small, safe,
  and symmetric with the theme.
- Bad: adds a binary-serving endpoint with its own content-type/CSP handling.

### Option 3 — Full asset manager
- Good: general; would support many images.
- Bad: far more surface and UI than one logo needs; premature.

## Links

- relates to ADR-0113 (org-wide UI brand theme) — the colour half of instance
  branding; this ADR is the logo half and leaves ADR-0113's accent-only decision
  intact
- relates to ADR-0126 (self-service registration setting) — another org-wide,
  pre-auth, admin-gated setting in the same store
- builds on ADR-0107 (design-time backup) — the settings directory it captures now
  includes the logo
- follows ADR-0005 / `api/sidecar` — the atomic-write durability discipline the
  logo file reuses
