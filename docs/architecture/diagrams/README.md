# ArchiMate diagram sources

These SVGs are the diagrams embedded in
[`../enterprise-architecture.md`](../enterprise-architecture.md). SVG was chosen over
PNG and inline Mermaid because it keeps an exact, designed layout while staying a
small, diffable, theme-aware text file — the embedded `prefers-color-scheme` style
(with fallback colours) renders correctly in GitHub's light or dark theme.

| File | Used for |
|------|----------|
| `overview.svg` | The four-layer map (motivation → business → application → technology) |
| `motivation-trace.svg` | The motivation through-line (stakeholder → … → architecture element) |
| `business.svg` | Business layer detail |
| `application.svg` | Application layer detail |
| `technology.svg` | Technology layer detail |

## Regenerating

Edit the diagrams by editing the generator, **not** the SVG files by hand:

```bash
python3 gen_diagrams.py     # rewrites the *.svg files in this directory
```

The layout (bands, boxes, labels, colours) is defined near the bottom of
[`gen_diagrams.py`](gen_diagrams.py). If a target renderer needs raster images, the
script header documents the headless-Chromium command that rasterizes each SVG to a
2× PNG.
