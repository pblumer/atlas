// How the UI prints the numbers it puts on a picture: counts, and spans of time.
//
// Both live here rather than in the views that draw them because both are read in two
// places that have to agree — a badge on screen, and the same number in a file that
// has left the app — and a formatter each of them kept a copy of is a formatter that
// eventually prints one fact two ways.
//
// The counts come first, and what they need is digit grouping.
//
// The runtime views put their numbers on the diagram itself — how many tokens completed
// on a shape, how many were cancelled there, how many are alive there now — and on a
// busy server those are five and six digits each. Printed as an unbroken run, "25864"
// beside "50002" beside "23436", every badge is correct and none of them is legible: a
// digit run that long is read by counting, and two of them cannot be compared at a
// glance at all, which is the only reason the counts are drawn on the shapes instead of
// listed in a table.
//
// So the thousands are grouped, and the separator is a NARROW NO-BREAK SPACE rather than
// a locale's own mark. Atlas is read in one place and operated from another: "25.864" is
// twenty-five thousand to one reader and twenty-five point eight to the next, "25,864"
// the same disagreement mirrored, and a badge has no room to say which it meant. A space
// is the one grouping mark no locale reads as a decimal point (ISO 31-0 / SI), and the
// narrow no-break variant keeps a badge one line at any count — a count that wrapped
// inside its pill would cost more than the grouping ever bought.
//
// Deliberately not `toLocaleString()`: the browser's locale would make the separator a
// property of *whoever is looking* rather than of what this person chose, so the same
// screenshot in a ticket would say something different to the person who received it,
// and no test could pin what a badge prints.
//
// What a person may choose instead is a region, from a fixed list
// (ADR-draft-display-preferences-per-user). Each one is a row in the table below — two
// characters, no locale data — so the preview on the settings screen promises exactly
// what the badge will show, and the default is still the neutral one: an instance where
// nobody opens that screen keeps the behaviour it has.

import { region } from "./prefs.js";

// REGIONS is how each offered region writes a number. Adding a fifth is a row here and
// an entry in the server's list (api/userprefs), and nothing else.
export const REGIONS = {
  international: { group: "\u202F", decimal: "." }, // 25 864.50 — ISO 31-0 / SI
  ch: { group: "'", decimal: "." },                  // 25'864.50 — the Swiss apostrophe
  de: { group: ".", decimal: "," },                  // 25.864,50
  us: { group: ",", decimal: "." },                  // 25,864.50
};

// GROUP_SEP is the default thousands separator: the narrow no-break space, which is
// what a console nobody has configured prints.
export const GROUP_SEP = REGIONS.international.group;

// marks are the two characters a region writes with: the active one, or the id a caller
// names. The override exists for the settings screen, which has to show what a region
// would print *before* anybody commits to it — a preview that changed the console around
// it would be a setting applied, not a preview.
//
// An unknown id — a record from a newer version, a hand-edited browser copy — falls back
// to the neutral default rather than throwing inside a render path.
function marks(regionId) {
  return REGIONS[regionId || region()] || REGIONS.international;
}

// fmtCount renders a count with its thousands grouped. Anything under a thousand comes
// back untouched — a separator on "999" is noise in a pill that small.
//
// It is total on purpose, because it sits in render paths that must not throw over a
// field a server left out: a value that is not a number at all is passed through as it
// came, so a badge shows "—" or an empty slot rather than "NaN".
export function fmtCount(value, opts) {
  if (value == null || value === "") return "";
  const n = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(n)) return String(value);
  // Guarded rather than destructured: this is handed to `map` in places, where the
  // second argument is an index rather than options.
  const { group, decimal } = marks(opts && typeof opts === "object" ? opts.region : "");
  const [whole, frac] = Math.abs(n).toString().split(".");
  // Group from the right: the leading group is whatever is left over (1|234|567).
  const grouped = whole.replace(/\B(?=(\d{3})+$)/g, group);
  return (n < 0 ? "-" : "") + grouped + (frac ? decimal + frac : "");
}

// spanText renders a length of time in the coarsest unit that still says it.
//
// The question behind a span on a landscape is "how long has this been wrong", not
// "how long exactly": five minutes and seven minutes are the same answer, Friday and
// Thursday are not. So it rounds hard and stops pretending to be precise at a day,
// which is the granularity the decision it feeds actually has.
//
// It is a *length*, with no "ago" on it, because the same number is read two ways —
// "stuck 3 d" under a node and "the longest-parked one has been stuck 3 d" in a key —
// and a formatter that baked in one phrasing would be unusable for the other.
export function spanText(ms) {
  const n = typeof ms === "number" ? ms : Number(ms);
  if (!Number.isFinite(n) || n <= 0) return "";
  const seconds = Math.round(n / 1000);
  // Below the first useful tick, said as a bound rather than as a number: "43 s" on a
  // picture read at a glance is precision nobody asked for and nobody acts on.
  if (seconds < 90) return "under 2 min";
  const minutes = Math.round(seconds / 60);
  if (minutes < 90) return `${minutes} min`;
  const hours = Math.round(minutes / 60);
  if (hours < 36) return `${hours} h`;
  return `${fmtCount(Math.round(hours / 24))} d`;
}
