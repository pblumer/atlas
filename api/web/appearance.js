// Atlas appearance — light, dark, or following the system (ADR-0507).
//
// Appearance is how *this browser* wants the console drawn, and nothing else: it is
// not the organisation's brand colour (theme.js, ADR-0113), it reaches no server and it
// changes no workflow state. So it lives in localStorage and nowhere else, and a reset
// of the brand cannot touch it.
//
// The preference is one of four words. `light` and `dark` are choices; `system`
// follows prefers-color-scheme and keeps following it while the page is open; `cockpit`
// is a gamified HUD skin layered on the dark foundation, an explicit opt-in that the
// system scheme never selects (ADR-0509). Anything else — nothing stored, a value from
// a later version, storage that throws — is today's light console: the default stays
// light until the dark palette has been verified on every screen, which is the record's
// condition for changing it.
//
// The resolved appearance is written as data-appearance on the document root, and the
// stylesheet does the rest (app.css, `:root[data-appearance="dark"]`). The tiny inline
// bootstrap in index.html writes it before first paint and mirrors resolveAppearance
// below; this module is what keeps `system` live and what a test or a later switch
// calls.

// localStorage key. The value is the bare word, not JSON.
export const APPEARANCE_KEY = "atlas.appearance";

// The words a preference may be. The first is the default. `cockpit` is a skin, not
// an accessibility appearance, but it rides the same data-appearance mechanism.
export const APPEARANCES = ["light", "dark", "system", "cockpit"];

// DIAGRAM_RENDERER is the colour configuration every bpmn-js and dmn-js canvas is
// built with (editor.js newModeler, dmn-editor.js). The renderers write a shape's fill
// and stroke as inline style, which no stylesheet rule outranks, so a dark stylesheet
// alone leaves white boxes on a dark canvas. Handing them var() references instead
// makes the inline style read the appearance's tokens: switching re-colours an open
// diagram at once, without importing it again.
//
// The fallback inside each var() is not decoration. It is bpmn-js's own default, and
// it is what an exported SVG resolves to: rasterized for a document (doc-graphics.js)
// the picture is an image of its own that sees none of the page's custom properties,
// so it keeps the light colours the PDF is laid out for. Without the fallback a fill
// that cannot be resolved is inherited black — the failure app.css's @property block
// describes. Nothing here is written into the model: these are renderer defaults, and
// only an explicit DI colour (bioc:fill, color:background-color) reaches the XML.
export const DIAGRAM_RENDERER = Object.freeze({
  defaultFillColor: "var(--diagram-shape-fill, #fff)",
  defaultStrokeColor: "var(--diagram-shape-stroke, hsl(225, 10%, 15%))",
  defaultLabelColor: "var(--diagram-label, hsl(225, 10%, 15%))",
});

// readAppearance returns the stored preference, or "light" for anything that is not
// one of APPEARANCES, including storage that refuses to be read.
export function readAppearance() {
  try {
    const value = localStorage.getItem(APPEARANCE_KEY);
    return APPEARANCES.includes(value) ? value : APPEARANCES[0];
  } catch {
    return APPEARANCES[0];
  }
}

// resolveAppearance turns a preference into what is drawn: "cockpit" for the skin
// (an explicit choice the system scheme never selects); "dark" for a dark choice, or
// for `system` while the system is dark; "light" otherwise.
export function resolveAppearance(preference, systemDark) {
  if (preference === "cockpit") return "cockpit";
  return preference === "dark" || (preference === "system" && systemDark) ? "dark" : "light";
}

function darkQuery() {
  return typeof matchMedia === "function" ? matchMedia("(prefers-color-scheme: dark)") : null;
}

// applyAppearance resolves the preference (the stored one when none is given) and
// writes the result onto the document root. Returns what it wrote.
export function applyAppearance(preference = readAppearance()) {
  const query = darkQuery();
  const resolved = resolveAppearance(preference, !!(query && query.matches));
  document.documentElement.setAttribute("data-appearance", resolved);
  return resolved;
}

// watchSystemAppearance keeps the document in step with two things that change under
// an open page: the system's scheme, which matters only while the preference is
// `system` — an explicit choice is never overridden by it — and a preference changed
// in another tab. Returns a function that stops watching.
export function watchSystemAppearance() {
  const query = darkQuery();
  const onScheme = () => { if (readAppearance() === "system") applyAppearance("system"); };
  const onStorage = (e) => { if (e.key === APPEARANCE_KEY || e.key === null) applyAppearance(); };
  if (query) query.addEventListener("change", onScheme);
  window.addEventListener("storage", onStorage);
  return () => {
    if (query) query.removeEventListener("change", onScheme);
    window.removeEventListener("storage", onStorage);
  };
}
