// The UML class canvas, on diagram-js (ADR-0237).
//
// What Atlas owns here is the part that is Atlas's: how a class, a data store and
// the four association kinds are drawn, and what the subset permits between them.
// Selection, move, zoom, pan, outline, undo/redo and keyboard handling come
// from diagram-js, which is the whole reason for the change — every one of them was
// missing from the hand-rolled SVG this replaces, and none of them was missing on
// purpose.
//
// The difference from the ArchiMate canvas beside it (ADR-0189) is the edit
// contract. That canvas never creates anything: the server owns the document, so a
// new element is written server-side and the view re-read. An information model is a
// working copy with an explicit Save, which is what lets somebody draw three classes
// and two relationships and then decide — and what makes an undo stack mean
// anything. So this canvas edits locally, and the rules refuse at the point of
// drawing what the server would refuse at the point of writing.

import Diagram from "diagram-js/lib/Diagram";
import BaseRenderer from "diagram-js/lib/draw/BaseRenderer";
import SelectionModule from "diagram-js/lib/features/selection";
import MoveCanvasModule from "diagram-js/lib/navigation/movecanvas";
import ZoomScrollModule from "diagram-js/lib/navigation/zoomscroll";
import ModelingModule from "diagram-js/lib/features/modeling";
import MoveModule from "diagram-js/lib/features/move";
import OutlineModule from "diagram-js/lib/features/outline";
import RulesModule from "diagram-js/lib/features/rules";
import LassoToolModule from "diagram-js/lib/features/lasso-tool";
import KeyboardModule from "diagram-js/lib/features/keyboard";
import KeyboardMoveSelectionModule from "diagram-js/lib/features/keyboard-move-selection";
import { append, attr, create } from "tiny-svg";
import inherits from "inherits-browser";
import RuleProvider from "diagram-js/lib/features/rules/RuleProvider";

// Box geometry, carried over from the canvas this replaces so a saved model opens
// looking the way its author left it. A class is as tall as its members make it, so
// the shape of the diagram carries information rather than a grid does.
export const BOX_W = 200;
const HEAD_H = 34;
const ROW_H = 20;
const PAD = 10;
const STORE_H = 52;

export function classHeight(cls) {
  const rows = (cls.stereotype === "enumeration" ? cls.literals : cls.attributes) || [];
  return HEAD_H + PAD + Math.max(1, rows.length) * ROW_H + PAD / 2;
}
export const STORE_HEIGHT = STORE_H;

function svg(name, attributes, parent) {
  const node = create(name);
  attr(node, attributes);
  if (parent) append(parent, node);
  return node;
}

function text(parent, content, attributes) {
  const node = svg("text", attributes, parent);
  node.textContent = content;
  return node;
}

// classVisual draws the UML class box: a header carrying the «stereotype» over the
// name, a rule, and one compartment of members. An «enumeration» carries literals
// where the others carry attributes, which is the one place the two diverge.
function classVisual(parent, shape) {
  const bo = shape.businessObject || {};
  const rows = (bo.stereotype === "enumeration" ? bo.literals : bo.attributes) || [];
  // The name is on the group as well as in it, so a reader — a test, an operator
  // taking a screenshot — can address a box by what it is called rather than by
  // where it happens to sit.
  const g = svg("g", {
    class: `uml-class ${bo.stereotype || ""}${bo.invalid ? " invalid" : ""}` +
      `${bo.unreachable ? " unreachable" : ""}`,
    "data-name": bo.name || "", "data-id": bo.id || "",
  }, parent);

  svg("rect", { x: 0, y: 0, width: shape.width, height: shape.height, rx: 6, class: "uml-box" }, g);
  svg("line", { x1: 0, y1: HEAD_H, x2: shape.width, y2: HEAD_H, class: "uml-sep" }, g);
  // The stereotype rides above the name in guillemets, which is how UML says what
  // kind of classifier this is.
  text(g, `«${bo.stereotype || "businessObject"}»`,
    { x: shape.width / 2, y: 14, class: "uml-stereo", "text-anchor": "middle" });
  text(g, bo.name || "unnamed",
    { x: shape.width / 2, y: 28, class: "uml-cname", "text-anchor": "middle" });

  if (!rows.length) {
    text(g, bo.stereotype === "enumeration" ? "no literals yet" : "no attributes yet",
      { x: PAD, y: HEAD_H + PAD + 12, class: "uml-empty" });
    return g;
  }
  rows.forEach((row, i) => {
    const y = HEAD_H + PAD + i * ROW_H + 13;
    if (bo.stereotype === "enumeration") {
      text(g, row, { x: PAD, y, class: "uml-literal" });
      return;
    }
    // The business key is marked on the box because it is the fact the whole model
    // turns on: what makes Order#ORD-1 the same order in two processes.
    const isKey = (bo.identity || []).includes(row.name);
    const line = svg("text", { x: PAD, y, class: `uml-attr${isKey ? " key" : ""}` }, g);
    const span = (content, cls) => {
      const t = svg("tspan", { class: cls }, line);
      t.textContent = content;
    };
    span(`${isKey ? "⚿ " : ""}${row.name}`, "uml-attr-name");
    span(`: ${row.type}`, "uml-attr-type");
    if (row.multiplicity && row.multiplicity !== "1") span(` [${row.multiplicity}]`, "uml-attr-mult");
  });
  return g;
}

// storeVisual draws the cylinder. A store is one line about where a class is kept,
// so it is a band rather than a box — and deliberately not the same shape as a
// class, because it is not one (ADR-0230 §7).
function storeVisual(parent, shape) {
  const bo = shape.businessObject || {};
  const w = shape.width;
  const g = svg("g", {
    class: `uml-store${bo.invalid ? " invalid" : ""}${bo.unreachable ? " unreachable" : ""}`,
    "data-name": bo.name || "", "data-id": bo.id || "",
  }, parent);
  svg("path", {
    class: "uml-store-body",
    d: `M0,10 A${w / 2},10 0 0 1 ${w},10 L${w},${STORE_H - 10} A${w / 2},10 0 0 1 0,${STORE_H - 10} Z`,
  }, g);
  svg("path", { class: "uml-store-lip", fill: "none", d: `M0,10 A${w / 2},10 0 0 0 ${w},10` }, g);
  text(g, bo.name || "unnamed", { x: w / 2, y: 30, class: "uml-store-name", "text-anchor": "middle" });
  text(g, bo.class ? `«${bo.mode || "read"}» ${bo.class}` : "holds nothing yet",
    { x: w / 2, y: 44, class: "uml-store-sub", "text-anchor": "middle" });
  return g;
}

// The markers are the notation. A reader tells an aggregation from a composition by
// whether the diamond is filled, and a generalization by the hollow triangle — which
// is the whole reason to draw four kinds rather than four labelled lines.
const MARKERS = {
  aggregation: { id: "uml-diamond-open", path: "M10,5 L5,9 L0,5 L5,1 Z", fill: "var(--surface)", at: "source" },
  composition: { id: "uml-diamond-solid", path: "M10,5 L5,9 L0,5 L5,1 Z", fill: "var(--text)", at: "source" },
  generalization: { id: "uml-triangle", path: "M0,0 L10,5 L0,10 Z", fill: "var(--surface)", at: "target" },
};

function ensureMarker(canvas, kind) {
  const spec = MARKERS[kind];
  if (!spec) return null;
  const defs = canvas._svg.querySelector("defs") || svg("defs", {}, canvas._svg);
  if (!defs.querySelector(`#${spec.id}`)) {
    const marker = svg("marker", {
      id: spec.id, markerWidth: 12, markerHeight: 12, refX: spec.at === "source" ? 0 : 10, refY: 5,
      orient: "auto", markerUnits: "userSpaceOnUse",
    }, defs);
    svg("path", { d: spec.path, fill: spec.fill, stroke: "var(--text)", "stroke-width": 1 }, marker);
  }
  return spec;
}

function UmlRenderer(eventBus, canvas) {
  BaseRenderer.call(this, eventBus, 1500);
  this.canvas = canvas;
}
inherits(UmlRenderer, BaseRenderer);
UmlRenderer.$inject = ["eventBus", "canvas"];

UmlRenderer.prototype.canRender = (element) => /^uml:/.test(element.type || "");

UmlRenderer.prototype.drawShape = function(parent, shape) {
  return shape.type === "uml:store" ? storeVisual(parent, shape) : classVisual(parent, shape);
};

UmlRenderer.prototype.drawConnection = function(parent, connection) {
  const bo = connection.businessObject || {};
  const points = connection.waypoints.map((p) => `${p.x},${p.y}`).join(" ");
  // A store's line to its class is an annotation, not a relationship — a store and
  // its class do not relate, one *is kept in* the other (ADR-0230 §7) — so it is not
  // one of the edges, and nothing that counts relationships counts it.
  const g = svg("g", {
    class: bo.element === "store-link" ? "uml-store-link" : `uml-edge ${bo.kind || "association"}`,
    "data-id": bo.id || "",
  }, parent);
  const line = svg("polyline", { points, class: "uml-edge-line" }, g);
  // setAttribute, not tiny-svg's attr: attr routes every name that is also a CSS
  // property — fill, stroke, marker-start — into the inline style instead, which
  // draws the same but leaves no attribute for a reader (or a test) to see. Stroke
  // and dash live in the stylesheet with the rest of the edge's appearance; the
  // marker does not, because which end carries it is what the notation *means*.
  const spec = ensureMarker(this.canvas, bo.kind);
  if (spec) line.setAttribute(spec.at === "source" ? "marker-start" : "marker-end", `url(#${spec.id})`);
  if (bo.name) {
    const mid = connection.waypoints[Math.floor(connection.waypoints.length / 2)];
    text(g, bo.name, { x: mid.x, y: mid.y - 4, class: "uml-edge-label", "text-anchor": "middle" });
  }
  // The ends carry the role and the multiplicity, which is the half of a class
  // diagram that says how many and in what capacity — "1 customer places 0..* orders"
  // is the sentence, and a line without them only says the two are related. A
  // generalization has neither: "is a kind of" is not a counted relationship.
  if (bo.element !== "store-link" && bo.kind !== "generalization") {
    const wp = connection.waypoints;
    // Set in from the endpoint rather than on it, and along the segment that leaves
    // it: on the endpoint the label sits under the box it belongs to and under the
    // diamond that says which end this is, and a loop's endpoints are on two
    // different sides of one box, so interpolating between them crosses the box.
    const inward = (from, toward) => {
      const dx = toward.x - from.x;
      const dy = toward.y - from.y;
      const len = Math.hypot(dx, dy) || 1;
      const d = Math.min(24, len * 0.4);
      return { x: from.x + (dx / len) * d, y: from.y + (dy / len) * d - 6 };
    };
    const ends = [
      [bo.from, inward(wp[0], wp[1])],
      [bo.to, inward(wp[wp.length - 1], wp[wp.length - 2])],
    ];
    for (const [end, at] of ends) {
      const parts = [end?.role, end?.multiplicity].filter(Boolean).join(" ");
      if (parts) text(g, parts, { x: at.x, y: at.y, class: "uml-end-label", "text-anchor": "middle" });
    }
  }
  return g;
};

UmlRenderer.prototype.getShapePath = function(shape) {
  return `M${shape.x},${shape.y} l${shape.width},0 l0,${shape.height} l-${shape.width},0 z`;
};

const RendererModule = {
  __init__: ["umlRenderer"],
  umlRenderer: ["type", UmlRenderer],
};

const center = (shape) => ({ x: shape.x + shape.width / 2, y: shape.y + shape.height / 2 });
export const dock = (shape, other) => {
  const here = center(shape);
  const there = center(other);
  const dx = there.x - here.x;
  const dy = there.y - here.y;
  const scale = Math.min(
    dx ? shape.width / 2 / Math.abs(dx) : Infinity,
    dy ? shape.height / 2 / Math.abs(dy) : Infinity,
  );
  return { x: here.x + dx * scale, y: here.y + dy * scale };
};

// route gives a connection its waypoints. A class related to its own kind — an
// Employee who reports to an Employee — needs a loop rather than a line: the toolbar
// refuses to draw one, but an imported model may well contain one (ADR-0232), and
// docking a shape against itself is a division by a zero-length direction. The shape
// is UML's: out of the right edge, back into the top.
export const route = (source, target) => {
  if (source !== target) return [dock(source, target), dock(target, source)];
  const right = source.x + source.width;
  const midY = source.y + source.height / 2;
  const backIn = source.x + source.width - 40;
  const out = right + 50;
  const up = source.y - 40;
  return [
    { x: right, y: midY }, { x: out, y: midY },
    { x: out, y: up }, { x: backIn, y: up }, { x: backIn, y: source.y },
  ];
};

// Subset holds the table the server served and answers the one question the canvas
// asks of it. It is a thin wrapper on purpose: the rules live on the server, and
// anything decided here would be a second copy of them — which is how a canvas comes
// to permit an arrow the write path then rejects.
function Subset(config) {
  this.matrix = (config && config.matrix) || {};
}
Subset.$inject = ["config.subset"];
Subset.prototype.allowedBetween = function(source, target) {
  return this.matrix[`${source}>${target}`] || [];
};

// UmlRules says what may be edited. diagram-js asks before every move and connect,
// and with no provider it asks nobody and allows everything.
//
// Unlike the ArchiMate canvas, creating and connecting *are* permitted here: this is
// a working copy, and a local edit reaches the document when the author saves. What
// stays refused is what the subset refuses — an enumeration is a closed set of
// values, so nothing points at it, and no line may be drawn that the server would
// then reject.
function UmlRules(eventBus, umlSubset) {
  this.subset = umlSubset;
  RuleProvider.call(this, eventBus);
}
inherits(UmlRules, RuleProvider);
UmlRules.$inject = ["eventBus", "umlSubset"];

UmlRules.prototype.init = function() {
  this.addRule("elements.move", ({ shapes, target }) => {
    if (!shapes || !shapes.length) return false;
    // Re-parenting a shape into another shape would be containment, which is a
    // statement about the model rather than an arrangement of it.
    if (target && target.parent) return false;
    return shapes.every((s) => /^uml:(class|store)$/.test(s.type || ""));
  });
  this.addRule("connection.create", ({ source, target }) => {
    if (!source || !target || source === target) return false;
    if (source.type !== "uml:class" || target.type !== "uml:class") return false;
    return this.subset.allowedBetween(
      (source.businessObject || {}).stereotype, (target.businessObject || {}).stereotype).length > 0;
  });
  this.addRule("elements.delete", ({ elements }) => elements.filter((e) => e.type !== "uml:store-link"));
  this.addRule("shape.resize", () => false); // a class is as tall as its members make it
};

const RulesProviderModule = {
  __depends__: [RulesModule],
  __init__: ["umlRules"],
  umlRules: ["type", UmlRules],
  umlSubset: ["type", Subset],
};

const VIEW_MODULES = [
  RendererModule, SelectionModule, MoveCanvasModule, ZoomScrollModule, OutlineModule,
];
const EDIT_MODULES = [
  ModelingModule, MoveModule, RulesProviderModule, LassoToolModule,
  KeyboardModule, KeyboardMoveSelectionModule,
];

export class ClassCanvas {
  constructor(container, options = {}) {
    this.editable = options.editable !== false;
    this.diagram = new Diagram({
      canvas: { container },
      // The subset the server served, handed to the rules provider. An editable
      // canvas without it would allow every connection, which is worse than allowing
      // none: it would promise what the write path refuses.
      subset: options.subset || { matrix: {} },
      modules: this.editable ? [...VIEW_MODULES, ...EDIT_MODULES] : VIEW_MODULES,
    });
    this.canvas = this.diagram.get("canvas");
    this.factory = this.diagram.get("elementFactory");
    this.selection = this.diagram.get("selection");
    this.shapes = new Map();
    this.connections = new Map();
    this.origin = new Map();
    this.graphics = this.diagram.get("graphicsFactory");

    const eventBus = this.diagram.get("eventBus");
    eventBus.on("selection.changed", (e) => {
      options.onSelection?.((e.newSelection && e.newSelection[0])?.businessObject || null);
    });
    if (this.editable) {
      this.commandStack = this.diagram.get("commandStack");
      // One event for "the picture changed", whatever changed it — a drag, an undo,
      // a redo. The host does not need to know which.
      eventBus.on(["commandStack.changed"], () => options.onChange?.());
    }
  }

  // moved reports every shape whose position differs from the document it was drawn
  // from. It compares against what was loaded rather than accumulating drags,
  // because dragging a box away and back is not a change — and an accumulating list
  // would report it as one, saving a revision that moved nothing.
  moved() {
    const out = [];
    for (const [id, origin] of this.origin) {
      const shape = this.shapes.get(id);
      if (!shape) continue;
      const x = Math.round(shape.x);
      const y = Math.round(shape.y);
      if (x === origin.x && y === origin.y) continue;
      out.push({ id, kind: shape.type === "uml:store" ? "store" : "class", x, y });
    }
    return out;
  }

  allowedFrom(sourceStereotype, targetStereotype) {
    if (!this.editable) return [];
    return this.diagram.get("umlSubset").allowedBetween(sourceStereotype, targetStereotype);
  }

  undo() { if (this.commandStack?.canUndo()) this.commandStack.undo(); }
  redo() { if (this.commandStack?.canRedo()) this.commandStack.redo(); }
  canUndo() { return Boolean(this.commandStack?.canUndo()); }
  canRedo() { return Boolean(this.commandStack?.canRedo()); }

  // sync brings the canvas up to date with the model, in place.
  //
  // The obvious implementation is to clear the canvas and draw the model again, and
  // it is the wrong one: the editor re-renders on every keystroke in the properties
  // panel, and a redraw would take the viewport, the selection and the undo stack
  // with it every time. Typing a class name would zoom the diagram back to fit and
  // deselect the class being renamed.
  //
  // So shapes are reconciled instead. What exists is updated, what is new is added,
  // what is gone is removed, and everything else — where the author scrolled to, what
  // they had selected, what they could undo — is simply left alone.
  sync(model, findings = [], marks = {}) {
    // Drawing tells the host — setting a root clears the selection, and the host
    // hears that and re-renders. Arriving back here mid-draw would add every shape a
    // second time, so a sync during a draw is the draw already in progress.
    if (this.drawing) return;
    // A sync before anything was drawn is the first draw. The editor renders on
    // mount, but a stray edit arriving first must not add shapes to no root.
    if (!this.root) { this.render(model, findings, marks); return; }
    this.reconcile(model, findings, marks);
  }

  // marks are what the host knows and the drawing only shows: which shapes the
  // served matrix rules out while a relationship is being drawn. The rule stays with
  // the host, because that is where the one copy of the matrix already lives.
  reconcile(model, findings = [], marks = {}) {
    const wanted = new Map();
    for (const cls of model.classes || []) wanted.set(cls.id, { kind: "class", item: cls });
    for (const st of model.stores || []) wanted.set(st.id, { kind: "store", item: st });

    const badClass = new Set(findings.map((f) => f.classId).filter(Boolean));
    const badStore = new Set(findings.map((f) => f.storeId).filter(Boolean));
    const invalid = (id) => badClass.has(id) || badStore.has(id);
    const unreachable = new Set(marks.unreachable || []);

    // Gone first, so a class removed and a class added in one edit cannot collide.
    for (const [id, shape] of [...this.shapes]) {
      if (wanted.has(id)) continue;
      this.canvas.removeShape(shape);
      this.shapes.delete(id);
      this.origin.delete(id);
    }
    const byName = new Map();
    for (const [id, { kind, item }] of wanted) {
      const height = kind === "store" ? STORE_H : classHeight(item);
      // `element` says what sort of thing this is; `kind` on an association says
      // which of the four it is. The panel needs both, so they are two names.
      const bo = { element: kind, kind, ...item, invalid: invalid(id), unreachable: unreachable.has(id) };
      let shape = this.shapes.get(id);
      if (shape) {
        // The businessObject is replaced rather than mutated so a stale reference
        // cannot keep a renamed class alive under its old name.
        shape.businessObject = bo;
        shape.height = height;
        // A shape the author has dragged keeps where they put it; one they have not
        // follows the document, which is what moves a class the server repositioned.
        const origin = this.origin.get(id);
        if (origin && shape.x === origin.x && shape.y === origin.y) {
          shape.x = item.x;
          shape.y = item.y;
        }
        this.origin.set(id, { x: item.x, y: item.y });
        this.graphics.update("shape", shape, this.canvas.getGraphics(shape));
      } else {
        shape = this.factory.createShape({
          id, type: kind === "store" ? "uml:store" : "uml:class",
          x: item.x, y: item.y, width: BOX_W, height, businessObject: bo,
        });
        this.canvas.addShape(shape, this.root);
        this.shapes.set(id, shape);
        this.origin.set(id, { x: item.x, y: item.y });
      }
      if (kind === "class") byName.set(item.name, shape);
    }

    // Connections are cheap and few, and a class that moved changes every waypoint
    // touching it — so they are rebuilt rather than reconciled.
    for (const [id, conn] of [...this.connections]) {
      this.canvas.removeConnection(conn);
      this.connections.delete(id);
    }
    const link = (id, type, source, target, businessObject) => {
      if (!source || !target) return;
      const conn = this.factory.createConnection({
        id, type, source, target,
        waypoints: route(source, target), businessObject,
      });
      this.canvas.addConnection(conn, this.root);
      this.connections.set(id, conn);
    };
    for (const a of model.associations || []) {
      link(a.id, "uml:association", this.shapes.get(a.from?.classId), this.shapes.get(a.to?.classId),
        { element: "association", kind: a.kind, ...a });
    }
    // The store's line to the class it holds is derived, not authored: it exists
    // because the store names that class, so it is drawn and never edited.
    for (const st of model.stores || []) {
      link(`link-${st.id}`, "uml:store-link", this.shapes.get(st.id), byName.get(st.class),
        { element: "store-link", kind: "store-link" });
    }
  }

  // render is sync from nothing: a fresh root, a fitted viewport and an empty undo
  // stack. It is what opening a model does; every edit after that goes through sync.
  render(model, findings = [], marks = {}) {
    if (this.drawing) return;
    this.drawing = true;
    try {
      this.draw(model, findings, marks);
    } finally {
      this.drawing = false;
    }
  }

  draw(model, findings, marks = {}) {
    // Clear what is on the canvas before the new root, not just the bookkeeping.
    // diagram-js's element registry is per diagram and not per root, so a shape left
    // in it makes the next render fail with "element already exists" — which is what
    // happens when a model is opened twice in one session.
    for (const conn of this.connections.values()) this.canvas.removeConnection(conn);
    for (const shape of this.shapes.values()) this.canvas.removeShape(shape);
    this.shapes = new Map();
    this.connections = new Map();
    this.origin = new Map();
    if (this.root) this.canvas.removeRootElement(this.root);
    const root = this.factory.createRoot({ id: `root-${model.id || "m"}` });
    // The field is set before the canvas is told, because setting a root clears the
    // selection — and the host hears that, re-renders, and would arrive back here
    // with no root recorded yet. Recording it first makes that second pass a sync.
    this.root = root;
    this.canvas.setRootElement(root);
    this.shapes = new Map();
    this.origin = new Map();
    this.connections = new Map();
    // reconcile() rather than sync(): the guard above is what makes this draw the
    // only one, so the draw itself goes straight to the work.
    this.reconcile(model, findings, marks);
    this.selection.select(null);
    this.commandStack?.clear();
    requestAnimationFrame(() => this.fit());
  }

  // A relationship is selectable too, and by the same id: which line you are editing
  // has to be visible on the drawing, not only in the panel. Connections are rebuilt
  // on every reconcile, so this is also what puts the selection back on the new one.
  select(id) {
    this.selection.select(this.shapes.get(id) || this.connections.get(id) || null);
  }

  fit() { this.canvas.zoom("fit-viewport", "auto"); }
  zoom(delta) {
    const now = this.canvas.zoom();
    this.canvas.zoom(Math.max(0.2, Math.min(4, now * delta)), "auto");
  }
  destroy() { this.diagram.destroy(); }
}
