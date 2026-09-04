// Digit grouping for the counts the UI prints.
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
// property of whoever is looking, so the same screenshot in a ticket would say something
// different to the person who received it.

// GROUP_SEP is the thousands separator, in one place: switching the whole UI to the
// Swiss apostrophe ("25'864") is this constant, and nothing else.
export const GROUP_SEP = "\u202F"; // NARROW NO-BREAK SPACE

// fmtCount renders a count with its thousands grouped. Anything under a thousand comes
// back untouched — a separator on "999" is noise in a pill that small.
//
// It is total on purpose, because it sits in render paths that must not throw over a
// field a server left out: a value that is not a number at all is passed through as it
// came, so a badge shows "—" or an empty slot rather than "NaN".
export function fmtCount(value) {
  if (value == null || value === "") return "";
  const n = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(n)) return String(value);
  const [whole, frac] = Math.abs(n).toString().split(".");
  // Group from the right: the leading group is whatever is left over (1|234|567).
  const grouped = whole.replace(/\B(?=(\d{3})+$)/g, GROUP_SEP);
  return (n < 0 ? "-" : "") + grouped + (frac ? `.${frac}` : "");
}
