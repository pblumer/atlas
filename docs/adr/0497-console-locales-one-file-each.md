# ADR-0497: The console's languages are one file each, and none ships incomplete

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-26
- **Deciders:** decided 2026-10-26 by the maintainer, who wants the console to be extensible with further languages and, before the editors are translated, the catalogue structured for it: a language is added by a developer through a pull request, and a missing message keeps showing its key, with the tests refusing an incomplete language

## Context and problem statement

[ADR-0267](0267-console-speaks-german-first.md) gave the console a message catalogue:
`api/web/i18n.js`, German first, "a second locale is a second object in the same
file". Two locales lived there, `de` and `en`, and a language was named in three more
places that had to be kept in step by hand:

- the catalogue's own object (`CATALOGS` in `i18n.js`);
- the language picker's labels (`LANGUAGE_LABELS` in `displaysettings.js`);
- the languages a person may store as their choice (`userprefs.Languages`,
  [ADR-0459](0459-display-preferences-per-user.md)).

Every Go test that holds a screen to the catalogue checked `de` and `en` by name, so a
third language would have been checked by none of them. `plural()` knew two forms,
one and other, which is German's and English's shape and not Polish's, Russian's or
Arabic's.

The catalogue was about to grow by thousands of messages: the BPMN, decision and form
editors are next. Moving those later would mean moving them twice. The question this
record answers: **what does adding a language take, and what stops a half-done one
from reaching a screen?**

## Decision drivers

- Adding a language should be adding a file, not editing a list in three places that
  nothing checks against each other.
- No build step and no dependency ([ADR-0012](0012-web-ui-app-shell.md)).
- ADR-0267's rule stands: a missing message renders as its key, never as another
  language. So completeness has to be enforced before shipping, not papered over at
  run time.
- The browser and the server must agree on which languages exist.
- Messages may carry plain markup where a screen inserts them as HTML; that is safe
  only as long as they come from the console's own source.

## Considered options

1. **Keep one file**, and add a language as a third object in it.
2. **One file per language under `api/web/locales/`**, listed once by an index the
   browser imports, with the server and the tests reading the same directory.
3. **Language packs an operator installs at run time**, as JSON outside the binary.
4. **One file per language, loaded on demand** with a dynamic `import()` once the
   locale is known.

## Decision outcome

Chosen option: **"One file per language under `api/web/locales/`"**.

- **A language is a file.** `api/web/locales/<id>.js` exports one object of messages,
  keyed as before. `locales/index.js` imports each file and exports `CATALOGS`;
  `i18n.js` imports that and keeps `t`, `plural`, `locale`, `locales` and `setLocale`
  as they were. Adding a language is its file plus two lines in the index.
- **A language names itself.** Every file carries `locale.name`, its name in its own
  language; the picker lists it by that, so `LANGUAGE_LABELS` is gone.
- **The server reads the same directory.** The `api` package lists the embedded
  `web/locales/*.js` and gives that list to `userprefs.Languages` before serving, so a
  person may choose exactly the languages the console can show.
- **No language ships incomplete.** A test holds every locale to every key the German
  default has, with the same `{placeholders}`, and to no key German lacks except the
  extra plural forms its own language needs. Another holds the files, the index and the
  server's list to one another. The tests that hold a screen to the catalogue run over
  every locale the directory has, not over `de` and `en` by name. A missing message
  still renders as its key — ADR-0267 unchanged — and the test is what keeps one from
  reaching a screen.
- **Plurals follow the language.** `plural(key, n)` asks `Intl.PluralRules` for the
  count's category and uses `key.<category>`; a category a locale does not spell out
  takes that locale's `.other`, the same language's general form. Every locale carries
  `.one` and `.other` because German does.
- **Who adds a language:** a developer, through a pull request. The messages ship in
  the binary, which is what makes plain markup in them acceptable.

### Consequences

- **Positive:** a language is one reviewable file with a test that says exactly which
  messages it still lacks; the three hand-kept lists are one.
- **Positive:** the editors' messages go straight into the structure they will stay in.
- **Negative / trade-offs accepted:** every locale is loaded with the console, whichever
  one is shown. German is about 120 KB (35 KB compressed) before the editors; with a
  handful of languages that is acceptable, with many it is not (see option 4).
- **Negative:** a contributed language has to be complete when it lands and kept
  complete with every feature after it; a language nobody maintains blocks the next
  pull request that adds a message, by design.
- **Follow-ups / risks to watch:** messages the server composes are English text, not
  codes, and stay English in every language until the API answers with codes; the
  vendored modelers (bpmn-js, dmn-js, form-js) have their own translate services, to be
  fed from this catalogue; the portal keeps its own catalogue
  ([ADR-0313](0313-portal-language-follows-the-browser.md)); right-to-left languages
  were not considered.

## Pros and cons of the options

### Keep one file
- Good: no change at all.
- Bad: the file grows by every language times every message, a contributed language
  is a diff inside everyone else's file, and the three lists stay three.

### One file per language, listed by an index
- Good: one file per language to write and review; the index, the server and the
  tests all derive from the directory and are checked against one another.
- Bad: all languages load eagerly.

### Run-time language packs
- Good: an operator adds a language without a release.
- Bad: messages are inserted as markup in places, so a pack from outside the binary is
  an injection path; it would need plain text only and escaping everywhere, and an
  incomplete pack could not be refused by a test before it reaches a screen.

### Load the active language on demand
- Good: only the language shown is downloaded.
- Bad: `t()` is synchronous and called during rendering, so the console would have to
  wait for the catalogue before its first paint and re-load on every language switch;
  worth it only once there are many languages.

## Links

- amends [ADR-0267](0267-console-speaks-german-first.md) (a second locale is now a second file, not a second object)
- constrained by [ADR-0012](0012-web-ui-app-shell.md) (buildless UI)
- relates to [ADR-0459](0459-display-preferences-per-user.md) (the stored language choice) and [ADR-0313](0313-portal-language-follows-the-browser.md) (the portal's own catalogue)
