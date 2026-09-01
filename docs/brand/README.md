# Atlas — Brand assets

The Atlas mark is a **white peak carrying a cross, on a black tile** — the load
Atlas bears, with the Swiss cross of where it is built. It is one solid shape:
the notch at the base and the cross are cut out of the peak (SVG `evenodd`), so
there are no strokes and no fine detail to lose. That is what keeps it readable
from a hero down to a 16px favicon.

The mark uses fixed colors (no theme dependency) so it reads on **any**
background, light or dark.

| Color | Hex | Used for |
|-------|-----|----------|
| Black | `#000000` | The tile |
| White | `#FFFFFF` | The peak |
| Off-white | `#F4F7FB` | The tile-less glyph on dark grounds (the social card) |

**Three cuts, one drawing.** `atlas-mark.svg` is the mark as it ships. Where the
ground is already dark, or the container draws its own tile (the Console's `.mark`
box), use the tile-less `atlas-glyph.svg`, which inherits `currentColor`.
`favicon.svg` is the same drawing pushed heavier — a larger peak and a thicker
cross — because at 16px the shipped weights close up.

## Files

| File | What it is |
|------|-----------|
| `atlas-mark.svg` | Full mark (tile + peak) — README, docs, wherever the logo appears |
| `atlas-glyph.svg` | Peak only, `currentColor`, no tile — inlined by the Console's `.mark` box |
| `favicon.svg` | Heavier cut of the mark that stays legible to 16px |
| `favicon-16.png` `favicon-32.png` `favicon-48.png` | Raster favicons |
| `apple-touch-icon.png` | 180×180 home-screen icon |
| `atlas-mark-256.png` `atlas-mark-512.png` | Raster logo (tile included; transparent outside its rounded corners) |
| `atlas-social.svg` / `atlas-social.png` | 1200×630 social / link-preview card |
| `icons/*.svg` | The feature icons the README's Highlights list carries |

The PNGs are rendered from the SVGs — the SVGs are the source of truth. To
regenerate them, screenshot each SVG at the target size with headless Chromium
(the page needs `margin:0` and the `<svg>` sized in CSS; crop the shot, because
the headless viewport comes back shorter than `--window-size` asks for).

**The mark also lives inline in three other places**, because the Console is
buildless and cannot fetch a file before it paints: the `<link rel="icon">` data
URI at the top of each page in `api/web/` (the `favicon.svg` cut), the `.mark`
spans in those same pages, and `BUILTIN_MARK` in `api/web/logo.js` (both the
`atlas-glyph.svg` cut). Change the drawing here and change those with it.

## Feature icons (`icons/`)

Eight icons, one per Highlights bullet. They exist because the alternative was
emoji, and a row of emoji down the left of a feature list reads as generated
filler no matter what the words say.

They are **drawn from the mark's own vocabulary** so the set and the logo look
like one family: solid shapes with their detail cut out of them, the peak as the
enclosing form, and nodes as filled dots. Same constants throughout —

| | |
|---|---|
| Colour | `#6E7781` only. No second colour, no gradient. |
| Canvas | `viewBox="0 0 256 256"`, matching the mark, so weights carry over unscaled. |
| Form | Filled shapes, never strokes. Interior detail is a hole in the fill (`fill-rule="evenodd"`), exactly as the mark cuts its notch and cross. |
| Weight | No feature under ~22 units — that is 1.5px at the size these ship. |
| Nodes | Filled circles, r 19–28 by prominence. |

**Why grey, when the mark is black and white.** These are `<img>` tags in a
GitHub README, so they get no theme information and cannot use `currentColor`:
one fixed colour has to survive both the light and the dark page. The mark's own
black does not — it disappears on the dark theme — and the old blue is now a
colour the brand uses nowhere else. `#6E7781` is the achromatic middle that
holds on both grounds. If these ever move somewhere that *can* see the theme,
switch them to `currentColor` and drop the constant.

| File | What it draws |
|------|---------------|
| `one-binary.svg` | Everything Atlas is, inside the mark's own peak |
| `durable.svg` | A record landing on the append-only log |
| `compiled.svg` | Many strands resolving into one path |
| `modeler.svg` | A canvas holding a modelled flow |
| `tokens.svg` | One live token on the flow, singled out |
| `human.svg` | A person — work handed to someone, not to a worker |
| `decisions.svg` | A rule table with the row that hit marked |
| `agents.svg` | The peak driven from outside, through two connectors |

**Drawing another one.** Keep to the constants above and to the vocabulary —
if a motif needs a shape the mark does not use, it probably belongs to a
different set. Then check it at **18px**, not at 256: that is the size it ships
at, and it is where fine detail dies. Check it on **both** grounds while you are
there, white and `#0d1117`. The 256px view on white flatters everything and will
not tell you either.

In the README they are decorative — the bold lead carries the meaning — so each
SVG is `aria-hidden="true"` and each `<img>` takes an empty `alt`. They are
inlined as `<img …  align="absmiddle">`, which centres them on the text; without
it they sit on the baseline and ride visibly high.

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
