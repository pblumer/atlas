// How this person reads the console: number format, time zone, interface
// language (ADR-0459), and whether it is drawn light or dark (ADR-0507).
//
// One module owns the answer, because three different screens ask it and a
// formatter that kept its own copy would print one fact two ways. The store is
// the server — a preference belongs to a person, not to a browser, and the time
// zone especially: somebody who answers tasks on a laptop and checks a stuck
// instance on a tablet has one time zone and two browsers.
//
// The browser copy is still here, and it does three jobs rather than one:
//
//   - The first paint is right. Every formatter is synchronous and runs long
//     before a fetch comes back; reading the mirror at import time is what keeps
//     a badge from being drawn twice in two formats.
//   - An unreachable server leaves the console reading the way it did yesterday.
//   - On an instance with authentication off there is no identity to own a
//     preference, the endpoint answers 404, and the mirror is not a cache at all
//     — it is the whole answer.
//
// Nothing here formats. numfmt.js and datefmt.js do that, and they read from
// here; the dependency runs one way so a formatter can be tested without a
// server and this module never grows a second copy of a format table.

import { setLocale } from "./i18n.js";
import { applyAppearance } from "./appearance.js";

// KEY is the browser copy. It is one record rather than a key per field: the
// fields are saved together by one screen, and a half-applied set — a new time
// zone with yesterday's number format — is a state nobody chose. index.html's
// bootstrap and appearance.js read the appearance from it before this module has
// loaded, so the name is written there too.
const KEY = "atlas.prefs";

// DEFAULTS is what the console did before this screen existed, spelled out so
// "unset" has a name: the locale-neutral separator, the device's own time zone,
// and whatever language the catalogue defaults to. An instance where nobody opens
// the settings screen keeps exactly this.
const DEFAULTS = { region: "international", timeZone: "", language: "", appearance: "" };

// clean keeps only the four fields, as strings. Anything else in the stored
// record — a field from a newer version, a hand-edited value — is dropped rather
// than carried into a formatter.
function clean(raw) {
  const r = raw && typeof raw === "object" ? raw : {};
  return {
    region: typeof r.region === "string" ? r.region : "",
    timeZone: typeof r.timeZone === "string" ? r.timeZone : "",
    language: typeof r.language === "string" ? r.language : "",
    appearance: typeof r.appearance === "string" ? r.appearance : "",
  };
}

function read() {
  try {
    return clean(JSON.parse(localStorage.getItem(KEY) || "{}"));
  } catch {
    return clean(null); // private mode, no storage, or a record that is not JSON
  }
}

function write(next) {
  try { localStorage.setItem(KEY, JSON.stringify(next)); } catch { /* nothing to remember it with */ }
}

let current = read();

// apply makes a loaded record the active one, including the two fields other
// modules own: the language (i18n.js resolves it at import time, before the server
// has answered) and the appearance, which the stylesheet draws from the document
// root — so a choice made on another device takes effect as soon as the server's
// record arrives.
function apply(next) {
  current = clean(next);
  if (current.language) setLocale(current.language);
  applyAppearance(current.appearance);
  return current;
}

apply(current);

// prefs returns the active record. A copy, so a caller holding it cannot change
// what the formatters read.
export function prefs() { return { ...current }; }

// region is the number and date format id — never "", so a formatter can index a
// table with it directly.
export function region() { return current.region || DEFAULTS.region; }

// timeZone is the IANA name to render instants in, or "" for the device's own.
// The empty string is meaningful and is passed to Intl as undefined, which is
// what makes "follow this device" a real choice rather than a hidden default.
export function timeZone() { return current.timeZone; }

// language is the chosen catalogue, or "" when the catalogue's default stands.
export function language() { return current.language; }

// appearance is the chosen appearance — "light", "dark" or "system" — or "" when
// nothing was chosen, which draws the light console.
export function appearance() { return current.appearance; }

// loadPrefs reads this person's record from the server and makes it active.
//
// It answers how the preference is held, because the settings screen has to say
// so: "account" when the server owns it, "browser" when there is no identity to
// own one. A server that cannot be reached is not the same as one with no
// identity — the mirror still stands, and saying "browser" there would invite
// somebody to re-set a preference that is not actually lost.
export async function loadPrefs(api) {
  try {
    const rec = await api("GET", "/api/v1/me/preferences");
    apply(rec);
    write(current);
    return { scope: "account", ...current };
  } catch (err) {
    if (notFound(err)) return { scope: "browser", ...current };
    return { scope: "offline", ...current };
  }
}

// savePrefs stores a record and makes it active, writing through to the server
// where there is somebody to store it for.
//
// The local copy is written either way and is written *first*: the screen must
// not hand back a console still reading in the old format while a request is in
// flight, and on an instance without authentication the local copy is the whole
// store.
export async function savePrefs(api, next) {
  apply(next);
  write(current);
  try {
    await api("PUT", "/api/v1/me/preferences", current);
    return { scope: "account", ...current };
  } catch (err) {
    if (notFound(err)) return { scope: "browser", ...current };
    throw err; // a real failure: the screen says the server did not take it
  }
}

// notFound recognizes "there is no signed-in identity here" across the shapes an
// error arrives in: the console's api() helper carries a status, a bare fetch
// rejection carries the message.
function notFound(err) {
  if (!err) return false;
  if (err.status === 404) return true;
  return /\b404\b/.test(String(err.message || err));
}
