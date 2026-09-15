# Video workbench

Builds a narrated introduction to the Atlas Console: a real screen recording of the
running application, with a spoken German voice-over.

```bash
docs/video/build.sh --with-server     # builds Atlas, starts it, records, narrates
```

The result lands in `docs/video/build/atlas-console-intro.de.mp4` (H.264/AAC,
1440×810, about two minutes).

**Why the narration is German while the rest of this repository is English:** the
narration is *content*, in the language its audience speaks, the same exception
[`CONTRIBUTING.md`](../../CONTRIBUTING.md) already makes for the bilingual Console
strings. Everything describing how the video is built — this file, the comments in
the scripts — is English like the rest of the repository. A second language means a
second `*.<locale>.md` script, not a fork of the machinery.

## How it fits together

Sound comes before picture. [`tts.py`](tts.py) synthesizes each scene of the
narration separately and **measures how long it actually takes to say**;
[`record.mjs`](record.mjs) then drives the Console and gives each scene exactly that
much time. Editing the narration moves the picture with it — there is no second
place where timings have to be kept in step.

| File | Role |
|---|---|
| [`console-intro.de.md`](console-intro.de.md) | **The narration script.** This is what you edit. |
| [`pronunciation.de.tsv`](pronunciation.de.tsv) | Substitutions for the synthesizer (`BPMN` → `Be Pe Em En`). Affects the audio only. |
| [`tts.py`](tts.py) | script → one WAV per scene + `timing.json` |
| [`record.mjs`](record.mjs) | the screen recording, through Playwright/Chromium |
| [`mux.sh`](mux.sh) | assembles the audio, trims the lead-in, writes the MP4 |
| [`seed.sh`](seed.sh) | demo data: processes, instances, users, shares |
| [`build.sh`](build.sh) | runs all of it, in order |

## Editing the narration

Every `## <id>` heading is a scene; the text below it is what gets spoken. The
`caption:` line is the lower third shown on screen.

```markdown
## engine
caption: <b>Engine</b> — ein Knoten, eine Partition

Die Seite Engine zeigt den Kern. Der Zustand wird aus einem fortlaufenden
Write-Ahead-Log materialisiert …
```

- **Edit the text freely.** The scene gets shorter or longer by itself.
- **Do not rename an id.** It ties the text to a screen action in `record.mjs`
  (`SCENES.engine` clicks the *Engine* tab). A scene without a matching action logs a
  warning and leaves the previous screen up for its duration.
- **A new scene** needs a section here *and* a function of the same name under
  `SCENES` in `record.mjs`.
- **Total length** is printed by `tts.py`. The knobs are the amount of text,
  `tempo:` in the script's front matter (smaller = faster) and `SCENE_GAP` (the pause
  between scenes, 0.4 s by default).

Rebuilding only the audio is not a thing: the scene lengths are the speech lengths.
The other way round works — `--skip-tts` skips synthesis when only the on-screen
choreography changed.

## The voice

Synthesis runs offline with [Piper](https://github.com/rhasspy/piper). The script's
front matter picks the voice:

```yaml
voice: thorsten-low     # neutral, male
# voice: pavoque-low    # darker, male
```

`build.sh` fetches it into `build/voices/` on the first run. These models normally
live on Hugging Face; the two German male voices are also published as a GitHub
release asset, which is the reachable route from a network that does not allow the
former. They are `low` models at 16 kHz — clear and calm, but audibly synthetic.
For a more natural voice, swap `tts.py` for another speech service; everything
before and after it stays as it is.

## Requirements

- `ffmpeg` (to join sound and picture — the one shipped with Playwright will not do,
  it is built without audio support)
- `python3` with `piper-tts` (`pip install piper-tts`)
- `node` with the [`e2e/`](../../e2e/) dependencies (`npm ci`) and Chromium
- `go`, for `--with-server` only

The build products — the voice model, the WAV fragments, the raw recording and the
finished MP4 — stay out of the repository (see [`.gitignore`](../../.gitignore)).
What describes the video is committed; what a run produces is not.

## What this cannot do

The Console's own interface is English. The catalogue in
[`api/web/i18n.js`](../../api/web/i18n.js) deliberately covers the Tasks app only
([ADR-0267](../adr/0267-console-speaks-german-first.md)). So the video speaks German
over an English interface. Changing that means translating the Console first — not
the video.
