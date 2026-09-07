# Training nuggets

The handbook's **Schulungsnuggets** chapter (`api/web/handbuch.html#nuggets`) plays short
animated click-throughs: a three-minute overview, then one path per Atlas role. Every
frame is a screenshot of the running product, because the point of a nugget is that the
learner recognises the screen later.

```
scenes.mjs ──► capture.mjs ──┬──► api/web/nuggets/*.webp
   (source)    (run this)    └──► #nug-data block in api/web/handbuch.html
```

## Re-taking the shots

```bash
cd e2e && npm ci          # first time only — capture.mjs borrows Playwright from here
make nuggets              # or: node scripts/nuggets/capture.mjs
git add api/web/nuggets api/web/handbuch.html
```

The script builds the current tree, runs it on a throwaway data directory with `--auth=false`,
seeds it with this repo's own `examples/order-to-cash.bpmn` plus a built-in process that has a
form, takes every shot, writes the images, rewrites the data block, then stops the server and
deletes the data. It touches nothing outside `api/web/nuggets/` and the one block in the
handbook.

**Run it after any UI change that moves what a nugget points at**, and commit the images
together with the handbook — they are one artifact in two files.

## Why the coordinates are not in the source

A scene says *which* element to highlight, never where it is:

```js
{ t: 8500, img: "modeler-diagram", focus: "deploy", tap: true, cap: { de: "…", en: "…" } }
```

`capture.mjs` reads the Deploy button's bounding box out of the live page and writes the
rectangle into the generated block. So a button that moves is re-measured on the next run
rather than re-guessed, and a target that disappears fails the capture loudly instead of
leaving a ring on empty space.

This is also why the shots have to be regenerated as a set: an image and the rectangles
drawn on it are only valid together. Editing `#nug-data` by hand is how they come apart.

## Who notices that a re-take is due

Nobody re-takes screenshots on a schedule, so a weekly workflow asks the question
instead. **Nugget screenshots** (`.github/workflows/nuggets-check.yml`) runs

```bash
make nuggets-check      # node scripts/nuggets/capture.mjs --check
```

every Monday: it starts a throwaway Atlas exactly as the capture does, measures where
every highlighted element actually is, and compares that against the committed block.
It writes nothing — no images, no commits. A difference opens an issue labelled
`nuggets-stale` naming the targets that moved, with the measurements; a difference that
is still there next week comments on that issue rather than opening a second one.

Two findings, and they mean different things:

- **A target no longer resolves.** The capture dies on the spot with the selector that
  matched nothing. The element was renamed or removed, so the nugget is pointing at
  something that does not exist.
- **A target moved.** The screenshot is of an older layout: still wrong for a learner,
  but it degrades rather than breaks. The tolerance is 1.5 percentage points, which is
  wide enough that fonts and scrollbars on a different machine do not trip it.

Deliberately the cheap half: the workflow does not regenerate and commit the images
itself. Automating that would keep the chapter current without anybody looking, at the
price of a bot writing ~800 KB of image data into the history on a schedule — and of
captions drifting away from pictures nobody read. Being told, and then re-taking them
by hand, is the slower loop that keeps a human eye on the captions.

## Which images change on a re-take

Not all of them, and the split is predictable: a shot changes when what it shows
changes. Measured over consecutive runs with no code change between them, seven of the
twenty differ — the ones carrying a clock or a counter:

| Changes every run | Stable |
|---|---|
| `ops-instances`, `ops-process`, `ops-workers` (timestamps, "running for") | `apps`, `tasks-*`, `panorama`, `data-model` |
| `console-engine`, `console-org`, `console-workers` (live counts) | `modeler-diagram`, `modeler-playground`, `ops-decisions`, `ops-incidents` |
| `modeler-home` (saved-at times) | `console`, `console-ai`, `console-audit` |

So a diff touching only that first group is the capture doing nothing but re-photograph
the clock. A diff touching the second group means something actually moved — read it.

**Take the set whole either way.** An image and the rectangles drawn on it are only valid
together, so committing some images and not others is how they come apart. `--check`
compares measurements, not pixels, so it stays quiet about the clock.

## Adding or changing a nugget

1. Add a shot to `SHOTS` in `scenes.mjs` if the screen you need is not captured yet — a
   `route`, optionally an `act` to click into the right state, and the `targets` you want
   measured.
2. Add scenes to the right entry in `NUGGETS`: `t` (milliseconds), a bilingual `cap`, an
   `img` from `SHOTS`, and optionally a `focus` naming one of that shot's targets.
3. Run the capture and read the diff. **Read each caption against the shot it now sits
   under** — a caption promising "four cases waiting here" over a picture of a different
   process is the failure this chapter has already had once, and no test catches it.

`e2e/nuggets.spec.mjs` holds the rest: every scene names an image that ships, every shipped
image is used, no highlight runs off the frame, no tap without a cursor, and every referenced
screenshot is actually served.

## What the shots deliberately show

- **A model whose gateway branches.** `order-to-cash` splits at *Summe > 100 EUR?* into a
  human approval and a straight-through path, then forks in parallel for picking, shipping
  and invoicing. An exclusive gateway with one outgoing flow is not a gateway, and teaching
  one in BPMN material is worse than teaching nothing.
- **Five instances at once**, with baskets on both sides of that gateway, so the diagram
  carries tokens on both branches and the variables panel has real values in it.
- **A task with a form.** The order approval has none, so the seed also starts a built-in
  process whose user task does.
