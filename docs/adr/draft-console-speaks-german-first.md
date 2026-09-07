# ADR-DRAFT: The console speaks German first, through a catalogue rather than a rewrite

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas's console is written in English, hard-coded at the point of use: `app.js`
alone carries several hundred literal strings inside template expressions, and
`app.css` a few more in `content:` rules. There is no message catalogue and no
locale concept. [ADR-0012](0012-web-ui-app-shell.md) chose a buildless vanilla-JS
shell, so there is also no framework bringing one.

That was tenable while every screen's reader was the person who deploys models.
The Tasks app is where it stops being tenable: its readers are the people doing the
work. In this installation they work in German — the processes, the task names and
the forms are German already, and the chrome around them is not. Task folders make
it acute, because a folder is *authored* by that person: they name it, they read
back what it filters on, and they meet an error message when they get it wrong.

The question this record answers: **what does "the console is in German" mean, given
that translating all of it is a project and shipping half of it is worse than
shipping none?**

## Decision drivers

- The buildless constraint stands (ADR-0012): no framework, no bundler, no
  extraction toolchain, no runtime dependency.
- A screen that has been translated must not need untangling again later.
- A half-translated screen is worse than an untranslated one: a person cannot tell
  a missing translation from a technical term.
- Whatever the answer is, the server must not become part of it. An API that
  returns display text has to be translated too, and it has no idea who is reading.

## Considered options

1. **Translate in place.** Replace the English literals with German ones.
2. **A message catalogue, applied where a screen is translated.** New and
   translated screens read from it; the rest keeps its literals until its turn.
3. **A full i18n library and an extraction pass over the whole console.**

## Decision outcome

Chosen option: **"A message catalogue, applied where a screen is translated"** —
`api/web/i18n.js`, about 200 lines and no dependency.

**German is the default, and not the browser's language.** The catalogue's default
locale is `de`, and `navigator.language` is deliberately not consulted. The rest of
the console is still English; guessing from a browser setting nobody made would
hand somebody half a translated screen for a reason they cannot see. A locale is
chosen explicitly — `?lang=` records itself, and the choice is remembered per
browser like the other per-viewer preferences.

**A second locale is a second object.** `en` ships beside `de` for the strings that
exist, so the mechanism is demonstrably multi-locale rather than a single-language
stub with a hook. It is not selected by default.

**A missing key renders as the key.** `t("tasks.folders.save")` on screen is
visibly broken, and that is the point: a hole in the catalogue should fail in
review, not fall back silently to a language the viewer did not choose.

**The boundary is the API.** The server sends ids and model data — a field id, an
operator id, a process's own name — and never interface text. The catalogue turns
`process` into "Prozess". A test on the folder-fields endpoint asserts that no
interface word appears in its response, so the boundary is held rather than
remembered.

**Adoption is per screen, not per release.** Task folders are the first screen
through the catalogue. Nothing else has to change, and nothing else is blocked.

### Consequences

- **Positive:** a screen can be translated without touching any other, and once it
  has been, it never has to be untangled again.
- **Positive:** the ordinary case — one language, German — costs one function call
  per string and no build step.
- **Negative:** the console is now mixed. The Tasks sidebar reads "Meine Ordner"
  above four English built-in folders until those are converted too. That is
  visible, and it is the honest state of a migration rather than a hidden one.
- **Negative:** dates, numbers and plurals are only as good as `plural()` and the
  browser's own formatters. German and English agree on one/other; a locale that
  does not will need a real rule rather than a stretched one.
- **Follow-ups:** convert the built-in task folders, the task detail pane and the
  Start view — the rest of the Tasks app — so one app is whole before another is
  started. A language picker in the account menu once more than one screen answers
  to it.

## Pros and cons of the options

### Translate in place
- Good: no new concept; the smallest possible change for one language.
- Bad: it spends the work and buys nothing reusable — the second language starts
  from where the first one did.

### A message catalogue, adopted per screen
- Good: no dependency; each screen converts once; the API boundary gets stated.
- Bad: a mixed console while the migration runs.

### A full i18n library plus extraction
- Good: plurals, dates and interpolation solved by someone else.
- Bad: a bundler and a runtime dependency, which ADR-0012 declined for reasons that
  have not changed; and an extraction pass over hundreds of literals is the
  translation project this record is trying not to start by accident.

## Links

- constrained by [ADR-0012](0012-web-ui-app-shell.md) (buildless UI)
- first adopted by ADR-draft-task-folders-are-saved-filters
