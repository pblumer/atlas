# ADR-DRAFT: How a person reads — number format, time zone and language, per user

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-15
- **Deciders:** Atlas maintainers

## Context and problem statement

Three things about *how the console reads* are currently decided for the viewer
rather than by them, in three different ways:

- **Numbers.** `api/web/numfmt.js` groups thousands with a narrow no-break space
  and says why: a dot is a thousands mark in Zurich and a decimal point in Boston,
  so a badge that used either would mean two different things to two readers of
  the same screenshot. The separator is a constant, the same for everybody.
- **Dates and times.** Forty-three call sites say `toLocaleString()` with no
  arguments. That is not a decision; it is a default. The format follows the
  browser's language and — the part that matters — the instant is rendered in the
  browser's own time zone. A server running UTC, an operator in Zurich and a
  colleague in Boston read the same incident timestamp as three different wall
  clocks, with nothing on screen saying which one they are looking at.
- **Language.** [ADR-0267](0267-console-speaks-german-first.md) put the translated
  screens behind a catalogue with a locale remembered per browser, reachable only
  by typing `?lang=` into the address bar. That record's own follow-up list says
  "a language picker in the account menu once more than one screen answers to it";
  more than one screen now does.

So the question is not "should the console be localizable" — it partly is. It is:
**where does the answer to "how does this person read" live, and what does it
cover?**

The non-obvious half is *where*. ADR-0267 settled that a language is per browser,
on the reasoning that it is how one person reads rather than how an instance is
configured. That reasoning holds for the language of an interface somebody reads
at one desk. It does not survive the time zone: a person who answers tasks on a
laptop and checks a stuck instance on a tablet is the same person with the same
time zone, and a preference they have to set again per device is one they will
have set wrong somewhere.

## Decision drivers

- A timestamp read in the wrong time zone is an operational error, not a cosmetic
  one. It is the part of this with a wrong answer rather than an ugly one.
- The same person uses more than one browser. A preference that does not travel is
  a preference that is stale somewhere.
- A half-translated console must not be *promised* as translated (ADR-0267).
- The buildless constraint stands ([ADR-0012](0012-web-ui-app-shell.md)): no
  bundler, no framework, no runtime dependency, so whatever this is must be a
  small module and a few hundred lines.
- The server must not start returning interface text (ADR-0267's API boundary). A
  stored *preference* is not interface text — it is a setting, like a task
  folder — so the boundary is not crossed by storing one.
- Authentication can be off. Whatever is chosen must still work on an instance
  with no identity at all, because that is how Atlas is first run.
- A format somebody picked must be the format they see. A list of regions whose
  output depends on the browser's own data is a setting that cannot be tested and
  cannot be previewed honestly.

## Considered options

1. **Per browser, extending ADR-0267's `atlas.locale`.** Add region and time zone
   beside the language in `localStorage`, and a settings screen that writes them.
2. **Per user, on the server**, with the browser copy as a cache.
3. **Per instance**, set by an administrator for everybody.

## Decision outcome

Chosen option: **"per user, on the server, with the browser copy as a cache"**.

A new design-time store (`api/userprefs`, one `sidecar.NewStore` keyed by user id)
holds one record per signed-in user, and `GET`/`PUT /api/v1/me/preferences` read
and write it. The console loads it once at boot and mirrors it into
`localStorage`, which makes the mirror do three jobs: the first paint renders in
the right format instead of flashing the default, an unreachable server leaves the
console reading the way it did yesterday, and on an instance **with
authentication off — where there is no identity to own a preference — the browser
copy is not a cache but the whole answer**.

The record holds three fields, because they answer three different questions and
a single "locale" would weld them together:

| Field | Question it answers | Values |
|---|---|---|
| `region` | How is a number or a date *written*? | `international` (default), `ch`, `de`, `us` |
| `timeZone` | *Which instant* is a timestamp? | `` (the device's own, default) or an IANA name |
| `language` | Which *words*? | `` (the catalogue's default), `de`, `en` |

**The region is a fixed list, not a locale picker.** Four entries, each shown with
its own example — `25 864.50` · `25'864.50` · `25.864,50` · `25,864.50` — and each
one a table in `numfmt.js` rather than a string handed to `toLocaleString()`. A
full locale list reads as more capable and is worse: the output would depend on
the browser's CLDR data, the preview could not promise what the badge will show,
and a test could only assert whatever Chromium does this year. Four regions cover
the readers Atlas has, and a fifth is a row in a table.

**Nothing moves until somebody chooses.** An unset record is today's behaviour
exactly: the neutral space, the device's time zone, the catalogue's default
language. Upgrading an instance changes no pixel.

**The time zone is named wherever it is not obvious.** A timestamp rendered in a
zone the viewer did not pick carries that zone in its tooltip, so "14:32" can
always be resolved to an instant.

### Consequences

- **Positive:** the time zone becomes a stated fact instead of a property of
  whichever laptop is open, and it travels with the person.
- **Positive:** ADR-0267's two open follow-ups — a language picker, and "dates and
  numbers are only as good as the browser's own formatters" — are answered by one
  screen rather than three mechanisms.
- **Negative / trade-offs accepted:** a screenshot is no longer universally
  readable. `25.864` in a ticket now means what the sender chose, not one agreed
  thing. This is the cost of the feature and it is paid deliberately: the default
  stays the unambiguous one, so an installation that never opens the screen keeps
  the old guarantee.
- **Negative:** the console gains a store and two endpoints it did not have, and
  the language picker makes the mixed-translation state of the console visible to
  anybody who switches to English. ADR-0267 called that state honest; it is now
  also discoverable.
- **Follow-ups / risks to watch:** every *new* date rendering has to go through
  the shared formatter or it silently reverts to the browser's zone. The
  forty-three existing sites are converted with this record; a test that fails on
  a bare `toLocaleString()` in `api/web` is the way to keep the forty-fourth from
  appearing.

## Pros and cons of the options

### Per browser, extending `atlas.locale`
- Good: no server surface at all; consistent with the record it extends; works
  with authentication off without a special case.
- Bad: the time zone — the field with a wrong answer rather than an ugly one — is
  exactly the field that must not be re-set per device.

### Per user, on the server
- Good: one answer per person, on every device; the natural home for a setting
  that describes a person rather than a browser.
- Bad: a store, two endpoints, and a fallback path for instances with no
  identity — the fallback being the per-browser option, kept rather than replaced.

### Per instance, set by an administrator
- Good: the smallest surface; one place to look.
- Bad: wrong by construction for the case that motivates the record. An instance
  read from Zurich and from Boston has no single right time zone, and an
  administrator cannot pick one for both readers.

## Links

- supersedes the per-browser half of [ADR-0267](0267-console-speaks-german-first.md)
  (the catalogue, the API boundary and the per-screen adoption all stand)
- constrained by [ADR-0012](0012-web-ui-app-shell.md) (buildless UI)
