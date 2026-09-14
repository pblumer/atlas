// dmn-collab.js — the collaboration surface for a decision draft
// (ADR-draft-co-editing-a-decision).
//
// ADR-0140's session, its registry, its transport and its lock semantics are
// reused unchanged; collab.js holds all of that. What differs is the editor, and
// it differs in one way that matters: **dmn-js is not one canvas.**
//
//   - the DRG view is diagram-js — the same library bpmn-js uses, so a decision,
//     an input datum and a requirement are elements with ids, and presence, lock
//     badges and the change pulse all work there as they do for a diagram;
//   - a decision-table view is a grid. The things two people would collide over —
//     a rule, a cell, a column — have no diagram-js element and no id the session
//     could name.
//
// So the lock is the decision. In the DRG that is literally ADR-0140's rule, and
// in a table view it is the same rule applied to the element that exists: opening
// a decision's table claims that decision, and leaving it releases the claim. Two
// people can work on two decisions of one model at once; two people cannot fill in
// one table together, and the editor says which it is.

// tableLockId reports the decision a view claims when it is opened, or "" for a
// view that claims nothing. The DRG claims nothing by opening — selecting inside
// it is what claims an element, exactly as in a diagram.
function tableLockId(view) {
  if (!view || view.type === "drd") return "";
  return (view.element && view.element.id) || "";
}

// activeViewer is the diagram-js instance behind whatever view is on screen. A
// table view has one too, but it has no canvas in the diagram-js sense, so every
// caller here treats a throw as "nothing to draw on" — which collab.js already
// does for a mid-import canvas.
function activeViewer(modeler) {
  const viewer = modeler.getActiveViewer();
  if (!viewer) throw new Error("no active view");
  return viewer;
}

// dmnSurface is what attachCollab needs to run over a decision draft.
export const dmnSurface = {
  base: (id) => `/api/v1/dmn-drafts/${encodeURIComponent(id)}/session`,
  xml: (id) => `/api/v1/dmn-drafts/${encodeURIComponent(id)}/xml`,

  // get reaches a diagram-js service of the view that is currently open. It
  // changes as the author moves between the graph and a table, which is why every
  // use in collab.js goes through the surface rather than holding a reference.
  get: (modeler, name) => activeViewer(modeler).get(name),

  // Everything in a decision requirements graph is worth locking: a decision, an
  // input datum, a business knowledge model. There is no label-only shape to
  // exclude the way a BPMN canvas has, but the guard is kept so a future one is
  // handled rather than locked by accident.
  isLockable: (el) => !!(el && el.id && el.type && el.type !== "label"),

  // bind subscribes the session's handlers to whatever view is open, and re-binds
  // when the author switches views — a new view is a new diagram-js instance, so
  // listeners left on the old one would go quiet without saying so.
  //
  // Switching into a decision's table also *is* a selection of that decision, so
  // it is reported to the same handler that claims a lock in the graph. One rule,
  // stated once, true in both views.
  bind: (modeler, h) => {
    let bound = null;
    const bindActive = () => {
      unbindActive();
      let viewer;
      try { viewer = activeViewer(modeler); } catch { return; }
      viewer.on("selection.changed", h.onSelection);
      viewer.on("element.changed", h.onChange);
      bound = viewer;
    };
    const unbindActive = () => {
      if (!bound) return;
      try { bound.off("selection.changed", h.onSelection); } catch { /* gone with its view */ }
      try { bound.off("element.changed", h.onChange); } catch { /* gone with its view */ }
      bound = null;
    };
    const onViews = () => {
      bindActive();
      // A table view claims its decision; the graph claims nothing until something
      // in it is selected. Either way the handler is told what is now "selected",
      // so the lock follows the author without a second code path.
      const id = tableLockId(modeler.getActiveView());
      h.onSelection({ newSelection: id ? [{ id, type: "decision" }] : [] });
    };
    modeler.on("views.changed", onViews);
    bindActive();
    return () => {
      unbindActive();
      try { modeler.off("views.changed", onViews); } catch { /* torn down */ }
    };
  },
};
