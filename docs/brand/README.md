# Atlas — Brand assets

The Atlas mark is a **hexagon holding a converging flow** — two nodes join into
one, a BPMN gateway. The hexagon is the durable tile Atlas carries; the flow is
what runs inside it. A single bold blue, no fine detail, so it stays legible
from a hero down to a 16px favicon.

The mark uses a fixed color (no theme dependency) so it reads on **any**
background, light or dark.

| Color | Hex | Used for |
|-------|-----|----------|
| Blue | `#2563EB` | The whole mark (hexagon + flow) |
| Blue (bright) | `#4C8DF5` | Mark on dark grounds (e.g. the social card) |

## Files

| File | What it is |
|------|-----------|
| `atlas-mark.svg` | Full mark — README, docs, wherever the logo appears |
| `favicon.svg` | Simplified mark that stays legible to 16px |
| `favicon-16.png` `favicon-32.png` `favicon-48.png` | Raster favicons |
| `apple-touch-icon.png` | 180×180 home-screen icon |
| `atlas-mark-256.png` `atlas-mark-512.png` | Raster logo (transparent) |
| `atlas-social.svg` / `atlas-social.png` | 1200×630 social / link-preview card |

The PNGs are rendered from the SVGs. To regenerate them, re-run the render
step used to produce them (headless Chromium screenshot of each SVG at the
target size) — the SVGs are the source of truth.

## Link previews (Teams / Slack / GitHub)

The "card" that appears when you paste the repo link comes from an
**Open Graph image**. There are two places it needs to live:

1. **GitHub repository** — the most common case. GitHub does *not* read
   `og:image` from the README. Upload `atlas-social.png` under
   **Settings → General → Social preview**. After that, pasting the repo URL
   into Teams/Slack shows the card.

2. **A website / docs page** (if/when one exists) — add these tags to the
   page `<head>` and host `atlas-social.png` at a public URL:

   ```html
   <meta property="og:title" content="Atlas — durable BPMN workflow engine in Go" />
   <meta property="og:description" content="A durable, blazing-fast BPMN 2.x workflow engine. Bears the load, never drops a token." />
   <meta property="og:image" content="https://YOUR-DOMAIN/atlas-social.png" />
   <meta property="og:image:width" content="1200" />
   <meta property="og:image:height" content="630" />
   <meta name="twitter:card" content="summary_large_image" />
   <link rel="icon" href="/favicon.svg" type="image/svg+xml" />
   <link rel="icon" href="/favicon-32.png" sizes="32x32" />
   <link rel="apple-touch-icon" href="/apple-touch-icon.png" />
   ```
