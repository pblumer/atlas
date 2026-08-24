# ArchiMate diagram sources

These SVGs are the diagrams embedded in
[`../enterprise-architecture.md`](../enterprise-architecture.md). SVG was chosen over
PNG and inline Mermaid because it keeps an exact, designed layout while staying a
small, diffable, theme-aware text file — the embedded `prefers-color-scheme` style
(with fallback colours) renders correctly in GitHub's light or dark theme.

Every box carries the standard **ArchiMate element-type icon** in its top-right
corner (service, process, object, application component, node, system software,
artifact, role/stakeholder, driver, goal, principle, requirement, plateau),
following the notation convention.

| File | Used for |
|------|----------|
| `overview.svg` | The four-layer map (motivation → business → application → technology) |
| `motivation-trace.svg` | The motivation through-line (stakeholder → … → architecture element) |
| `business.svg` | Business layer detail |
| `application.svg` | Application layer detail |
| `technology.svg` | Technology layer detail |
| `deployment.svg` | Deployment view — host, partitions, local durable store, external nodes |
| `implementation.svg` | Implementation roadmap — plateaus M0–M6, coloured by status |

## Regenerating

Edit the diagrams by editing the generator, **not** the SVG files by hand:

```bash
python3 gen_diagrams.py     # rewrites the *.svg files in this directory
```

The layout (bands, boxes, labels, colours) is defined near the bottom of
[`gen_diagrams.py`](gen_diagrams.py). If a target renderer needs raster images, the
script header documents the headless-Chromium command that rasterizes each SVG to a
2× PNG.

## See also

These SVGs illustrate the view; for the same model as a formal, tool-loadable file,
see the ArchiMate Open Exchange export in [`../model/`](../model/) — importable into
Archi and other conformant tools.
