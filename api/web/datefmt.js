// How the console prints an instant (ADR-draft-display-preferences-per-user).
//
// Before this module there were forty-three `toLocaleString()` calls with no
// arguments, and that is not a decision — it is a default. Each one rendered in
// whatever time zone the browser happened to be in, so a server running UTC, an
// operator in Zurich and a colleague in Boston read the same incident timestamp
// as three different wall clocks, with nothing on screen saying which one they
// were looking at. That is an operational error waiting to happen, not a
// cosmetic one, and it is the reason the time zone is a stored preference.
//
// Two rules keep the output honest:
//
//   - The *instant* comes from the chosen time zone, or the device's own when
//     nobody chose one. Intl does the conversion; the browser carries the same
//     IANA database the server validates against.
//   - The *writing* comes from the chosen region, assembled here from numeric
//     parts rather than handed to a locale. `toLocaleString("de-CH")` renders
//     whatever that browser's CLDR data says this year — which cannot be
//     previewed on the settings screen and cannot be asserted in a test. Four
//     patterns in one table can be both.

import { region, timeZone } from "./prefs.js";

// PATTERNS is how each region writes a date and a clock.
//   order — which of year/month/day comes first
//   sep   — what goes between them
//   clock — "24" or "12"
// A fifth region is a row here, a row in numfmt.js, and an entry in the server's
// list. Nothing else.
const PATTERNS = {
  international: { order: "ymd", sep: "-", clock: "24" }, // 2026-10-15 17:18:29
  ch: { order: "dmy", sep: ".", clock: "24" },            // 15.10.2026 17:18:29
  de: { order: "dmy", sep: ".", clock: "24" },            // 15.10.2026 17:18:29
  us: { order: "mdy", sep: "/", clock: "12" },            // 10/15/2026 5:18:29 PM
};

// pattern is the active region's writing, or the one a caller names — the settings
// screen previews a region before it is chosen, and a preview that changed the console
// around it would be a setting applied rather than shown.
function pattern(regionId) {
  return PATTERNS[regionId || region()] || PATTERNS.international;
}

// asDate accepts what the call sites have: a Date, milliseconds since the epoch,
// or a string a Date can parse. Anything that is not a real instant comes back as
// null, and every formatter renders that as the empty string — these run inside
// render paths, where throwing would take out the screen around the timestamp.
function asDate(value) {
  if (value == null || value === "") return null;
  const d = value instanceof Date ? value : new Date(value);
  return Number.isNaN(d.getTime()) ? null : d;
}

// parts reads an instant's numbers *in the chosen zone*. en-US with h23 is used
// only as a carrier for numeric parts — none of its own formatting survives,
// which is what makes the output the same in every browser.
function parts(date, zone) {
  const dtf = new Intl.DateTimeFormat("en-US", {
    timeZone: (zone === undefined ? timeZone() : zone) || undefined,
    hourCycle: "h23",
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit",
  });
  const out = {};
  for (const p of dtf.formatToParts(date)) {
    if (p.type !== "literal") out[p.type] = p.value;
  }
  return out;
}

// datePart writes the day in the region's order.
function datePart(p, regionId) {
  const { order, sep } = pattern(regionId);
  const by = { y: p.year, m: p.month, d: p.day };
  return order.split("").map((k) => by[k]).join(sep);
}

// timePart writes the clock, with seconds unless a caller says otherwise. The
// twelve-hour form is spelled out here rather than taken from a locale so that
// midnight is "12:00:00 AM" in every browser rather than "00:00:00 AM" in one.
function timePart(p, { seconds = true, region: regionId } = {}) {
  const { clock } = pattern(regionId);
  let hour = Number(p.hour);
  let suffix = "";
  if (clock === "12") {
    suffix = hour < 12 ? " AM" : " PM";
    hour = hour % 12;
    if (hour === 0) hour = 12;
  }
  const hh = clock === "12" ? String(hour) : String(hour).padStart(2, "0");
  return `${hh}:${p.minute}${seconds ? `:${p.second}` : ""}${suffix}`;
}

// fmtDateTime is the replacement for a bare `toLocaleString()`: the day and the
// clock, in the chosen region's writing and the chosen zone's instant.
export function fmtDateTime(value, opts = {}) {
  const d = asDate(value);
  if (!d) return "";
  const p = parts(d, opts.timeZone);
  return `${datePart(p, opts.region)} ${timePart(p, opts)}`;
}

// fmtDate is the day alone (`toLocaleDateString()`).
export function fmtDate(value, opts = {}) {
  const d = asDate(value);
  if (!d) return "";
  return datePart(parts(d, opts.timeZone), opts.region);
}

// fmtTime is the clock alone (`toLocaleTimeString()`).
export function fmtTime(value, opts = {}) {
  const d = asDate(value);
  if (!d) return "";
  return timePart(parts(d, opts.timeZone), opts);
}

// zoneName is the zone these timestamps are in: the chosen one, or the device's
// own when nobody chose. It is always a real IANA name — "the browser's zone" is
// not something a reader can act on, and a screen that has to say which clock it
// is showing has to be able to name it.
export function zoneName() {
  if (timeZone()) return timeZone();
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC"; // an engine with no zone data at all still has to render something
  }
}

// zoneNote is the sentence a screen puts beside a column of timestamps: it names
// the zone, and says when that zone is this device's rather than a choice. A
// reader who sees "17:18" has to be able to resolve it to an instant, and this is
// the one place that says how — rather than a suffix on every row, which would
// cost a column's width to repeat one fact.
export function zoneNote() {
  return timeZone() ? `Times in ${timeZone()}` : `Times in ${zoneName()} (this device)`;
}
