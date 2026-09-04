// Zoom for a diagram Atlas draws itself.
//
// The framework-backed canvases get zoom from diagram-js: the BPMN modeler and its
// live/replay views, the DMN editor, the class canvas, the Panorama viewer. A
// diagram we render as plain SVG has no framework under it, and a picture that
// cannot be approached is a worse diagram than the same picture in a canvas — the
// reader's only recourse is the browser's page zoom, which scales the whole console
// around it. So the ability belongs to the diagram, not to whichever library
// happened to draw it.
//
// It works on already-rendered markup, the way groupifyPanel does: hand it the
// scrolling frame that holds an <svg>, and it adds the controls and the behaviour.
// Nothing that renders a diagram has to know how zooming works.

const MIN = 0.2;
const MAX = 4;
const STEP = 1.25; // one press, a quarter closer — enough to see, small enough to aim

// attachDiagramZoom gives the <svg> inside `frame` a zoom control: buttons for in,
// out and fit, ctrl+wheel over the diagram, and a stated percentage. `frame` must be
// the scrolling element (overflow:auto); the svg inside it is what gets resized.
//
// Returns a handle: { zoom(factor), fit(), destroy() }. `label` names the diagram
// for assistive technology; pass what the diagram is, e.g. "Decision requirements
// graph". Calling it twice on the same frame is a no-op, so a view that re-renders
// can call it unconditionally.
export function attachDiagramZoom(frame, opts = {}) {
  if (!frame || frame.dataset.dzoom === "1") return null;
  const svg = frame.querySelector("svg");
  if (!svg) return null;
  frame.dataset.dzoom = "1";

  // The diagram's own size, in CSS pixels at 100%. An SVG that Atlas draws carries
  // width/height alongside its viewBox; fall back to the viewBox when it does not,
  // and to the rendered box when it carries neither.
  const box = (svg.getAttribute("viewBox") || "").split(/[\s,]+/).map(Number);
  const baseW = Number(svg.getAttribute("width")) || (box.length === 4 ? box[2] : 0)
    || svg.getBoundingClientRect().width || 1;
  const baseH = Number(svg.getAttribute("height")) || (box.length === 4 ? box[3] : 0)
    || svg.getBoundingClientRect().height || 1;

  // The authored max-width:100% is what shrinks a wide diagram to the column. It
  // also caps every zoom above the frame's width, which is the one thing zoom is
  // for, so the explicit size below replaces it.
  svg.style.maxWidth = "none";

  let z = 0;
  const level = document.createElement("span");
  level.className = "dzoom-level";

  const apply = (next) => {
    z = Math.min(MAX, Math.max(MIN, next));
    svg.style.width = (baseW * z).toFixed(1) + "px";
    svg.style.height = (baseH * z).toFixed(1) + "px";
    level.textContent = Math.round(z * 100) + "%";
  };

  // fitScale is what shows the whole diagram: the frame's inner size over the
  // diagram's own, never enlarging past 100% — a small diagram blown up to fill a
  // wide frame is not "fitted", it is just big.
  const fitScale = () => {
    const w = frame.clientWidth - 16, h = frame.clientHeight - 16;
    if (w <= 0 || h <= 0) return 1;
    return Math.min(1, w / baseW, h / baseH);
  };

  const btn = (label, glyph, onClick) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "btn ghost small";
    b.setAttribute("aria-label", label);
    b.title = label;
    b.textContent = glyph;
    b.addEventListener("click", onClick);
    return b;
  };

  const controls = document.createElement("div");
  controls.className = "dzoom";
  if (opts.label) controls.setAttribute("aria-label", `Zoom: ${opts.label}`);
  controls.append(
    btn("Zoom out", "−", () => apply(z / STEP)),
    level,
    btn("Zoom in", "+", () => apply(z * STEP)),
    btn("Fit the diagram", "⤢", () => apply(fitScale())),
  );

  // The controls sit over the frame's top-right corner, so they stay put while the
  // diagram scrolls under them. The wrapper is what positions them; the frame keeps
  // its own scrolling and its own styling.
  const wrap = document.createElement("div");
  wrap.className = "dzoom-wrap";
  frame.parentNode.insertBefore(wrap, frame);
  wrap.append(frame, controls);

  // Ctrl+wheel zooms, a plain wheel scrolls. This is the platform convention for a
  // zoomable surface inside a scrolling page, and the alternative — a bare wheel
  // that zooms — rescales the diagram when a reader is only passing it by.
  const onWheel = (e) => {
    if (!e.ctrlKey && !e.metaKey) return;
    e.preventDefault();
    apply(e.deltaY < 0 ? z * STEP : z / STEP);
  };
  frame.addEventListener("wheel", onWheel, { passive: false });

  apply(fitScale());

  return {
    zoom: (factor) => apply(factor),
    fit: () => apply(fitScale()),
    destroy() {
      frame.removeEventListener("wheel", onWheel);
      controls.remove();
      if (wrap.parentNode) wrap.parentNode.insertBefore(frame, wrap);
      wrap.remove();
      delete frame.dataset.dzoom;
    },
  };
}
