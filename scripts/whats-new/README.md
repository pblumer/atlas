# What's New feed

The Console landing page (`#/console`, the "Welcome to Atlas" view) shows a compact,
collapsible **What's New** section: the newest user-facing features, in plain
language, newest-first, each with a link to its PR/ADR and — where the UI is involved
— a small step-by-step tutorial and a "Try it" deep link. It is bilingual (DE/EN)
with a per-visitor toggle.

## How it works

```
CHANGELOG.md ──┐
               ├─► gen.mjs ──► api/web/whats-new.json ──► app.js renderWhatsNew()
overrides.json ┘
```

- **`CHANGELOG.md` is the source of truth.** `gen.mjs` reads its version sections and
  `Added` / `Changed` / `Fixed` bullets and derives each entry's structure: a stable
  id (the title slug), the headline, the version/date, and a link to the ADR or PR the
  bullet names. New CHANGELOG entries appear automatically.
- **`overrides.json` adds the human polish.** Keyed by the derived id, it supplies the
  layman-friendly, bilingual summary, tags, an optional tutorial, and an optional
  "Try it" route — none of which belong in the developer-facing CHANGELOG. It can also
  `hidden: true` a purely internal/API bullet so it stays out of the user list.
- **`api/web/whats-new.json` is generated and committed.** It is served straight off
  the embedded FS (`//go:embed web` in `api/server.go`), so the web UI stays buildless
  (ADR-0012): the generator runs only at authoring time, never at runtime.

## Adding or polishing an entry

1. Write the change in `CHANGELOG.md` as usual.
2. Run the generator once to see the derived id:
   ```bash
   node scripts/whats-new/gen.mjs   # or: make whats-new
   ```
   Look up the entry's `id` in `api/web/whats-new.json`.
3. (Optional but recommended for anything a user should notice) add an override in
   `overrides.json` under that id. Every field is optional:

   ```json
   "the-secrets-panel-says-what-a-value-has-to-be": {
     "tags": ["Organization", "Secrets"],
     "summary": { "en": "…one or two plain sentences…", "de": "…dito auf Deutsch…" },
     "tutorial": { "en": ["Step 1", "Step 2"], "de": ["Schritt 1", "Schritt 2"] },
     "try": { "label": { "en": "Open Organization", "de": "Organisation öffnen" },
              "route": "#/console/org" },
     "hidden": false
   }
   ```

   - `summary` / `title` / `try.label`: either a `{ en, de }` object or a bare string
     (used for both languages). `de` falls back to `en`.
   - `try.route` must start with a known hash route: `#/console`, `#/modeler`,
     `#/operations`, or `#/tasks` (the UI ignores anything else).
   - `link`: only needed to override the auto-derived PR/ADR link.

4. Re-run `make whats-new` and **commit** the regenerated `api/web/whats-new.json`.

The only entries shown are the newest `MAX_ENTRIES` (see `gen.mjs`) after hidden ones
are dropped. `api/whatsnew_test.go` guards the committed JSON (valid, non-empty,
required fields present, newest-first).
