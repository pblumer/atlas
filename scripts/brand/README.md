# Social card → PNG and outlines

Builds the two derived cuts of the link-preview card from
[`docs/brand/atlas-social.svg`](../../docs/brand/atlas-social.svg), which is the
only file to edit:

| Built | What it is for |
|-------|----------------|
| `atlas-social.png` | The raster card. The only form GitHub's **Settings → General → Social preview** accepts, and the only one Teams and Slack will render. |
| `atlas-social-flat.svg` | The same card with its text converted to outlines, for anywhere the fonts are not guaranteed. |

```bash
pip install fonttools pillow
scripts/brand/social.py            # rebuild both
scripts/brand/social.py --check    # verify, write nothing, exit 1 on drift
```

A Chromium is required. It is found through `PLAYWRIGHT_BROWSERS_PATH`, the
`CHROME` environment variable, or the `PATH`.

## How the outlines are made

Not by re-shaping the text. Chromium is asked where it actually puts every
character through `getStartPositionOfChar`, and only the glyph outline is lifted
out of the font and placed there. Re-shaping would mean reproducing kerning and
letter-spacing, and being subtly wrong about both.

The fonts come from `fc-match` on the card's own font stacks, which is how
Chromium resolves them too — on a machine with the Liberation faces installed,
`Arial` lands on `Liberation Sans`, whose metrics match Arial's exactly.

## Why it checks itself

The script renders the source and the outlined cut and compares them before it
writes anything. Text rendering hints glyphs onto the pixel grid and path
rendering does not, so the comparison runs at 4x, where that difference washes
out and only real geometry is left.

This is what protects against the failure that matters: on a machine whose
fontconfig resolves `Arial` to something else, the outlines would be silently
wrong — right positions, wrong letterforms. The comparison catches it and the
script exits without touching either file.

## In CI

`--check` rebuilds both cuts in memory and compares them against what is
committed, so a change to `atlas-social.svg` that leaves the derived files
behind fails rather than going unnoticed. It needs the same fonts as a build:
where those are not guaranteed, the run reports a font mismatch instead of a
stale file, which is a true statement about that machine but not the one the
check is asking about.
