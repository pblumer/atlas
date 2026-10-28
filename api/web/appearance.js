// Atlas appearance — light, dark, or following the system (ADR-0507).
//
// Appearance is how *this person* wants the console drawn, and nothing else: it is not
// the organisation's brand colour (theme.js, ADR-0113), and it changes no workflow
// state. It is the fourth of the display preferences ADR-0459 keeps per person — stored
// with the account where there is one, in the browser where there is not — so
// prefs.js owns the record and its browser mirror, and this module only draws what
// the record says.
//
// The preference is one of three words. `light` and `dark` are choices; `system`
// follows prefers-color-scheme and keeps following it while the page is open. Anything
// else — nothing chosen, a value from a later version, storage that throws — is today's
// light console: the default stays light until the dark palette has been verified on
// every screen, which is the record's condition for changing it, and until then the
// dark choice is offered as a preview.
//
// The resolved appearance is written as data-appearance on the document root, and the
// stylesheet does the rest (app.css, `:root[data-appearance="dark"]`). The tiny inline
// bootstrap in index.html writes it before first paint from the same mirror and
// mirrors resolveAppearance below; prefs.js applies every record it loads or saves;
// this module keeps `system` live and follows a choice made in another tab.

// PREFS_MIRROR is the browser copy prefs.js keeps of the whole display record (its
// KEY). The appearance is read from it here, before prefs.js could answer, for the
// same reason the bootstrap reads it: the first paint must already be right.
export const PREFS_MIRROR = "atlas.prefs";

// The words a preference may be. The first is the default.
export const APPEARANCES = ["light", "dark", "system"];

// APPEARANCE_EVENT is dispatched on the document whenever the drawn appearance is
// (re)applied, with { preference, resolved } as its detail, so a control that shows
// the current state — the top bar's switch — can follow a change it did not make.
export const APPEARANCE_EVENT = "atlas:appearance";

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

// readAppearance returns the preference in the browser mirror, or "light" for
// anything that is not one of APPEARANCES — nothing chosen, a record that is not
// JSON, storage that refuses to be read.
export function readAppearance() {
  try {
    const record = JSON.parse(localStorage.getItem(PREFS_MIRROR) || "{}");
    const value = record && record.appearance;
    return APPEARANCES.includes(value) ? value : APPEARANCES[0];
  } catch {
    return APPEARANCES[0];
  }
}

// resolveAppearance turns a preference into what is drawn: "dark" for a dark choice,
// or for `system` while the system is dark; "light" otherwise.
export function resolveAppearance(preference, systemDark) {
  return preference === "dark" || (preference === "system" && systemDark) ? "dark" : "light";
}

function darkQuery() {
  return typeof matchMedia === "function" ? matchMedia("(prefers-color-scheme: dark)") : null;
}

// applyAppearance resolves the preference (the mirrored one when none is given) and
// writes the result onto the document root. Returns what it wrote.
export function applyAppearance(preference = readAppearance()) {
  const query = darkQuery();
  const resolved = resolveAppearance(preference, !!(query && query.matches));
  document.documentElement.setAttribute("data-appearance", resolved);
  document.dispatchEvent(new CustomEvent(APPEARANCE_EVENT, { detail: { preference, resolved } }));
  return resolved;
}

// currentAppearance is what the document is drawn in right now: "light" or "dark".
export function currentAppearance() {
  return document.documentElement.getAttribute("data-appearance") === "dark" ? "dark" : "light";
}

// watchSystemAppearance keeps the document in step with two things that change under
// an open page: the system's scheme, which matters only while the preference is
// `system` — an explicit choice is never overridden by it — and a preference changed
// in another tab. Returns a function that stops watching.
export function watchSystemAppearance() {
  const query = darkQuery();
  const onScheme = () => { if (readAppearance() === "system") applyAppearance("system"); };
  const onStorage = (e) => { if (e.key === PREFS_MIRROR || e.key === null) applyAppearance(); };
  if (query) query.addEventListener("change", onScheme);
  window.addEventListener("storage", onStorage);
  return () => {
    if (query) query.removeEventListener("change", onScheme);
    window.removeEventListener("storage", onStorage);
  };
}
