// The console's message catalogue (ADR-0267).
//
// Atlas's console was written in English, hard-coded at every call site. The
// Tasks app is where that stopped being tenable: its folders are built and read
// by the people doing the work, not by the people who deploy the models, and in
// this installation those people work in German.
//
// So the answer is not a switch that flips the whole console — that is a
// translation project, and doing it badly is worse than not doing it. It is a
// boundary: text that a person reads lives in a catalogue keyed by id, German is
// the language it is written in first, and a second locale is a second file
// (locales/, ADR-draft-console-locales-one-file-each). Nothing else in the console
// has to change until it is translated, and a screen that has been translated never
// has to be untangled again.
//
// Three rules keep it honest:
//
//   - The server sends ids and model data, never interface text. A process is
//     named by its model; "Prozess" is a word this file owns.
//   - A missing key renders as the key, visibly. A catalogue with a hole in it
//     should look broken in review, not fall back to a language nobody chose.
//   - The default is German, not the browser's language. The rest of the console
//     is still English, and guessing would give somebody half a translated screen
//     because of a setting they never made.

// CATALOGS holds one object per locale, keyed by message id, one file each under
// locales/. Ids are dotted paths naming the screen and then the thing —
// `tasks.folders.save` — so a screen's strings sit together and an unused one is
// findable.
import { CATALOGS } from "./locales/index.js";

// The default. It is the language the messages are written in first
// (locales/de.js), and the one a viewer gets unless they have deliberately chosen
// otherwise.
const DEFAULT_LOCALE = "de";

// LOCALE_KEY is where a deliberate choice is remembered. It is per browser, like
// the other per-viewer preferences the console keeps (the task list width, the
// sort order): a language is how one person reads, not how the instance is
// configured.
const LOCALE_KEY = "atlas.locale";

// pick resolves the locale once, in the order a choice was made: an explicit
// ?lang= in the URL (which also records itself, so a shared link keeps working
// after a reload), then a remembered choice, then the default. The browser's own
// language is deliberately not consulted — see the note at the top.
function pick() {
  try {
    const url = new URLSearchParams(location.search).get("lang");
    if (url && CATALOGS[url]) {
      localStorage.setItem(LOCALE_KEY, url);
      return url;
    }
    const saved = localStorage.getItem(LOCALE_KEY);
    if (saved && CATALOGS[saved]) return saved;
  } catch { /* private mode, or no storage — the default is a fine answer */ }
  return DEFAULT_LOCALE;
}

let current = pick();

// locale returns the active locale id.
export function locale() { return current; }

// locales returns the locale ids that have a catalogue.
export function locales() { return Object.keys(CATALOGS); }

// setLocale switches language and remembers the choice. It does not re-render
// anything: the caller knows what is on screen.
export function setLocale(id) {
  if (!CATALOGS[id]) return false;
  current = id;
  try { localStorage.setItem(LOCALE_KEY, id); } catch { /* nothing to remember it with */ }
  return true;
}

// t looks up a message and fills its {placeholders}.
//
// An unknown key renders as the key itself. That is deliberate: a hole in the
// catalogue then shows up as `tasks.folders.save` on the screen, where somebody
// notices it, instead of silently falling through to a language the viewer did
// not choose.
export function t(key, params) {
  const table = CATALOGS[current] || CATALOGS[DEFAULT_LOCALE];
  let s = table[key];
  if (s === undefined) s = key;
  if (!params) return s;
  return s.replace(/\{(\w+)\}/g, (m, name) => (name in params ? String(params[name]) : m));
}

// localeName is a locale's name in its own language — how the language picker lists
// it, so somebody looking for their language never has to read another one first.
export function localeName(id) {
  const table = CATALOGS[id];
  return (table && table["locale.name"]) || id;
}

// pluralRules caches one Intl.PluralRules per locale: the browser's own answer to
// which form a count takes, so a language with more than two forms (Polish's few and
// many, Arabic's six) needs only its messages, not a rule written here.
const pluralRules = new Map();

// plural picks the form of a counted message for the active locale: keyBase plus the
// count's plural category — `.one`, `.other`, and in some languages `.zero`, `.two`,
// `.few` or `.many`. A category the locale does not spell out takes its `.other`,
// which is the same language's general form rather than a fall into another one; the
// tests hold every locale to the `.one` and `.other` the German default carries.
export function plural(keyBase, n, params) {
  let rules = pluralRules.get(current);
  if (!rules) {
    try { rules = new Intl.PluralRules(current); } catch { rules = new Intl.PluralRules(DEFAULT_LOCALE); }
    pluralRules.set(current, rules);
  }
  const table = CATALOGS[current] || CATALOGS[DEFAULT_LOCALE];
  const exact = `${keyBase}.${rules.select(n)}`;
  const key = exact in table ? exact : `${keyBase}.other`;
  return t(key, Object.assign({ n }, params));
}
