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
//
// There are two kinds of diagram surface here and one control over both. A canvas
// that owns its zoom -- diagram-js, which every framework-backed diagram in Atlas is
// built on, and the Panorama mesh, which rolls its own -- passes a `controller` and
// keeps its own zooming; the control only asks. A picture Atlas drew as plain SVG
// passes none, and the default controller resizes it. What a reader sees is the same
// either way, which is the point: before this, three surfaces had three different
// sets of buttons and three had none.

const MIN = 0.2;
const MAX = 4;
const STEP = 1.25; // one press, a quarter closer — enough to see, small enough to aim

// attachDiagramZoom gives a diagram a zoom control: buttons for in, out and fit,
// ctrl+wheel over the diagram, and a stated percentage.
//
// With `controller` ({ get, set, fit }), that control drives the caller's canvas and
// the control is placed inside `frame`. Without one, `frame` must be the scrolling
// element (overflow:auto) around an <svg>, and the svg is what gets resized.
//
// Returns a handle: { zoom(factor), fit(), destroy() }. `label` names the diagram
// for assistive technology; pass what the diagram is, e.g. "Decision requirements
// graph". Calling it twice on the same frame is a no-op, so a view that re-renders
// can call it unconditionally.
export function attachDiagramZoom(frame, opts = {}) {
  if (!frame || frame.dataset.dzoom === "1") return null;
  const ctl = opts.controller || null;
  const svg = ctl ? null : frame.querySelector("svg");
  if (!ctl && !svg) return null;
  frame.dataset.dzoom = "1";

  let baseW = 1, baseH = 1;
  if (svg) {
    // The diagram's own size, in CSS pixels at 100%. An SVG that Atlas draws carries
    // width/height alongside its viewBox; fall back to the viewBox when it does not,
    // and to the rendered box when it carries neither.
    const box = (svg.getAttribute("viewBox") || "").split(/[\s,]+/).map(Number);
    baseW = Number(svg.getAttribute("width")) || (box.length === 4 ? box[2] : 0)
      || svg.getBoundingClientRect().width || 1;
    baseH = Number(svg.getAttribute("height")) || (box.length === 4 ? box[3] : 0)
      || svg.getBoundingClientRect().height || 1;

    // The authored max-width:100% is what shrinks a wide diagram to the column. It
    // also caps every zoom above the frame's width, which is the one thing zoom is
    // for, so the explicit size below replaces it.
    svg.style.maxWidth = "none";
  }

  let z = 0;
  const level = document.createElement("span");
  level.className = "dzoom-level";

  const show = () => { level.textContent = Math.round(z * 100) + "%"; };

  const apply = (next) => {
    z = Math.min(MAX, Math.max(MIN, next));
    if (ctl) ctl.set(z);
    else {
      svg.style.width = (baseW * z).toFixed(1) + "px";
      svg.style.height = (baseH * z).toFixed(1) + "px";
    }
    show();
  };

  // fitScale is what shows the whole diagram: the frame's inner size over the
  // diagram's own, never enlarging past 100% — a small diagram blown up to fill a
  // wide frame is not "fitted", it is just big. A canvas answers this itself.
  const fitScale = () => {
    const w = frame.clientWidth - 16, h = frame.clientHeight - 16;
    if (w <= 0 || h <= 0) return 1;
    return Math.min(1, w / baseW, h / baseH);
  };

  // doFit asks the canvas to fit and then reads back what it settled on, so the
  // stated percentage is the canvas's answer and not our guess at it.
  const doFit = () => {
    if (!ctl) return apply(fitScale());
    ctl.fit();
    z = Math.min(MAX, Math.max(MIN, Number(ctl.get()) || 1));
    show();
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
    btn("Fit the diagram", "⤢", doFit),
  );

  // The controls sit over the top-right corner. A canvas pans internally, so they go
  // inside its frame; a scrolling frame would carry them away with the content, so
  // that one gets a wrapper to position them against instead.
  let wrap = null;
  if (ctl) {
    if (getComputedStyle(frame).position === "static") frame.style.position = "relative";
    frame.appendChild(controls);
  } else {
    wrap = document.createElement("div");
    wrap.className = "dzoom-wrap";
    frame.parentNode.insertBefore(wrap, frame);
    wrap.append(frame, controls);
  }

  // Ctrl+wheel zooms, a plain wheel scrolls. This is the platform convention for a
  // zoomable surface inside a scrolling page, and the alternative — a bare wheel
  // that zooms — rescales the diagram when a reader is only passing it by.
  const onWheel = (e) => {
    if (!e.ctrlKey && !e.metaKey) return;
    e.preventDefault();
    apply(e.deltaY < 0 ? z * STEP : z / STEP);
  };
  frame.addEventListener("wheel", onWheel, { passive: false });

  // A canvas has already fitted itself by the time it is handed over (every caller
  // imports and fits), so adopt what it is showing rather than fitting it again and
  // undoing a view the caller chose deliberately.
  if (ctl) { z = Math.min(MAX, Math.max(MIN, Number(ctl.get()) || 1)); show(); }
  else apply(fitScale());

  return {
    zoom: (factor) => apply(factor),
    fit: doFit,
    // sync restates the factor after the canvas was zoomed by something other than
    // this control — a scroll gesture, or the caller's own fit on re-import.
    sync: () => { if (ctl) { z = Number(ctl.get()) || z; show(); } },
    // show(false) takes the control off a surface that is currently not a diagram:
    // the DMN editor's decision table is a table, and a zoom control over it would
    // be a button that does nothing.
    show: (visible) => { controls.hidden = !visible; },
    destroy() {
      frame.removeEventListener("wheel", onWheel);
      controls.remove();
      if (wrap) {
        if (wrap.parentNode) wrap.parentNode.insertBefore(frame, wrap);
        wrap.remove();
      }
      delete frame.dataset.dzoom;
    },
  };
}

// canvasController adapts a diagram-js canvas (bpmn-js, dmn-js, the class canvas,
// the Panorama viewer) to the control above. It takes a getter rather than the
// canvas itself because a view may re-import and replace it underneath.
// The guards are not defensive habit: the control is attached when the canvas is
// created, which is before anything has been imported into it, and a diagram-js
// canvas with no root throws rather than answering a zoom.
export function canvasController(getCanvas) {
  const on = (fn, fallback) => {
    try {
      const c = getCanvas();
      return c ? fn(c) : fallback;
    } catch {
      return fallback; // nothing imported yet, or the canvas is gone
    }
  };
  return {
    get: () => on((c) => c.zoom(), 1),
    set: (z) => on((c) => c.zoom(z), undefined),
    fit: () => on((c) => c.zoom("fit-viewport"), undefined),
  };
}
