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

// How far fitting will magnify a model that is smaller than its window, and how much
// of the room it leaves as margin. Past MAX_FIT the drawing stops gaining anything
// from the extra pixels — a class box has a fixed amount to say — and starts looking
// like a zoom somebody left on by accident.
const MAX_FIT = 1.6;
const FIT_MARGIN = 0.94;
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
    // The whole selection, not the first of it. A marquee selects several at once,
    // and a host told only about the first puts that one back on the canvas as *the*
    // selection — which takes the other three off again before anything can be done
    // with them. So both are reported: the one the panel edits, and all of them.
    eventBus.on("selection.changed", (e) => {
      const all = (e.newSelection || []).map((el) => el.businessObject).filter(Boolean);
      options.onSelection?.(all[0] || null, all);
    });
    if (this.editable) {
      // Whether the next drag draws a box, so the host's button can say so. Reported
      // rather than returned because the host is not what ends it: the gesture is
      // spent once the box is drawn, and Escape takes it back, and a button left lit
      // through either would promise a drag that is back to panning.
      //
      // Read from the drag itself rather than from diagram-js's tool manager, which
      // is built for a palette and is wrong at both ends here: it lets go of the tool
      // the moment the box starts being drawn, and it is never told at all when
      // Escape cancels an armed drag that had not begun.
      eventBus.on(["lasso.selection.init", "lasso.init"], () => options.onTool?.("marquee"));
      eventBus.on(["lasso.selection.cleanup", "lasso.cleanup"], () => options.onTool?.(null));
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
    if (Array.isArray(id)) {
      this.selection.select(id.map((one) => this.element(one)).filter(Boolean));
      return;
    }
    this.selection.select(this.element(id) || null);
  }

  element(id) { return this.shapes.get(id) || this.connections.get(id) || null; }

  // The marquee: a box drawn round several classes to take hold of them together.
  //
  // It is a mode you enter rather than a drag you just do, and that is not a
  // shortcoming — it is the only thing a plain drag on empty sheet can mean once
  // panning exists. Panning claims every left-drag, because a diagram bigger than its
  // window has to be movable, so the marquee asks for the gesture first. That is what
  // the process modeler beside it does with the same tool, and Shift and drag reaches
  // it there and here without the button.
  //
  // Arming it is all this does. Ending it belongs to diagram-js — the box being drawn
  // ends it, and so does Escape — and there is no way to make a second press of the
  // button mean "never mind": the press itself is what diagram-js takes as the start
  // of the gesture, so by the time a click could be read the mode is already spent.
  marquee(event) {
    if (!this.editable) return;
    this.diagram.get("lassoTool").activateSelection(event);
  }

  // Fit the model into the room, and then use the room.
  //
  // diagram-js fits by shrinking only — it never magnifies past 100% — so a model
  // smaller than the window is drawn at its own size in the middle of it, with the
  // width the screen has going to nothing on either side. That is the right default
  // for a canvas whose diagrams are usually larger than the viewport; a class diagram
  // of six classes on a wide screen is the other case, and it reads as a picture that
  // will not fill its frame.
  //
  // So a model that has room to grow is grown into it, short of the point where it
  // stops being a diagram and starts being a poster: MAX_FIT is where a class box is
  // still a class box. A model bigger than the window is untouched — shrinking to fit
  // is what fitting means there, and that half was never wrong.
  fit() {
    this.canvas.zoom("fit-viewport", "auto");
    const box = this.canvas.viewbox();
    if (!box.inner.width || !box.inner.height) return; // nothing drawn yet
    const room = Math.min(box.outer.width / box.inner.width, box.outer.height / box.inner.height);
    // A margin, so the outermost boxes do not sit against the edge of the sheet.
    const wanted = Math.min(room * FIT_MARGIN, MAX_FIT);
    if (wanted > this.canvas.zoom()) this.canvas.zoom(wanted, "auto");
  }
  zoom(delta) {
    const now = this.canvas.zoom();
    this.canvas.zoom(Math.max(0.2, Math.min(4, now * delta)), "auto");
  }
  destroy() { this.diagram.destroy(); }
}

// ---------------------------------------------------------------------------
// The object diagram: the run-time twin of the class diagram above.
//
// UML draws types and instances as two diagrams, and that split is the reason UML
// was the right notation for Atlas at all — it falls on the design-time/run-time
// line the engine already has (ADR-0230 §4). The class canvas above draws what an
// order *is*; this one draws the three orders an instance is actually carrying.
//
// It arrives here later than the class canvas because it was written earlier, by
// hand: `renderObjectDiagram` built SVG strings with its own layout and had no
// zoom, no pan and no selection. That is word for word the complaint ADR-0237 made
// about the class canvas, one altitude down, and the answer is the same one — the
// look was downstream of the substrate.
//
// Two things stay different from the canvas above, and both follow from the graph
// being *derived*:
//
//   - **It is read-only.** There is no document to write back to. The server derives
//     the picture from the instance's data objects because the rules for what
//     relates to what are model semantics (ADR-0230 §4), so a box moved here would
//     be moved back by the next refresh. Move, resize, connect and undo are
//     therefore absent rather than refused: a canvas that offers a gesture it
//     silently discards is worse than one that does not offer it.
//   - **Layout is the canvas's own.** The server sends no geometry and should not:
//     where a box sits is drawing, not semantics. The arrangement below — roots
//     across, their parts beneath them — is carried over from the hand-rolled
//     renderer, because it is the shape a person reads an object diagram in and it
//     needs no force simulation to arrive at.
//
// Selection is kept, though nothing edits what is selected: in a diagram of a dozen
// objects, clicking a box to outline it is how a reader follows one of its lines.

const OBJ_W = 210;
const OBJ_HEAD_H = 34;
const OBJ_ROW_H = 18;
const OBJ_GAP_X = 60;
const OBJ_GAP_Y = 40;

// An object box is as tall as its members make it — the same rule the class box
// follows, so the two diagrams read as one notation rather than two.
export function objectHeight(node) {
  return OBJ_HEAD_H + Math.max(1, (node.attributes || []).length) * OBJ_ROW_H + 10;
}

// objectVisual draws one object: its reading, a rule, then its members as
// `name = value`. The classes are the ones the stylesheet already carries, because
// the appearance of this diagram was never the thing that was wrong with it.
function objectVisual(parent, shape) {
  const bo = shape.businessObject || {};
  const g = svg("g", {
    class: `og-node${bo.nested ? " nested" : ""}${bo.unset ? " unset" : ""}`,
    "data-name": bo.name || "", "data-id": bo.id || "",
  }, parent);

  svg("rect", { width: shape.width, height: shape.height, rx: 6, class: "og-box" }, g);
  svg("line", { x1: 0, y1: OBJ_HEAD_H, x2: shape.width, y2: OBJ_HEAD_H, class: "og-sep" }, g);
  // The label is `order : Order`, underlined by the stylesheet — UML's own way of
  // saying "this is an instance, not a type", and the one mark that tells the two
  // diagrams apart at a glance.
  text(g, bo.label || "", { x: 10, y: 15, class: "og-label" });
  if (bo.state) {
    text(g, `[${bo.state}]`,
      { x: shape.width - 10, y: 15, class: "og-state", "text-anchor": "end" });
  }

  const rows = bo.attributes || [];
  if (!rows.length) {
    // An object whose class is unknown, or whose value is not a structure, has no
    // member list to show — so it shows what it holds, and an unset one says so
    // rather than rendering as an empty box.
    const line = svg("text", { x: 10, y: OBJ_HEAD_H + 13, class: "og-attr" }, g);
    const only = svg("tspan", { class: bo.unset ? "og-absent" : "og-val" }, line);
    only.textContent = bo.unset ? "unset" : (bo.value || "");
    return g;
  }
  rows.forEach((a, i) => {
    const line = svg("text", { x: 10, y: OBJ_HEAD_H + i * OBJ_ROW_H + 13, class: "og-attr" }, g);
    const span = (content, cls) => {
      const t = svg("tspan", { class: cls }, line);
      t.textContent = content;
    };
    // The key is marked because it is what makes this object *this* order, and what
    // another object's reference has to match to become a line.
    span(`${a.key ? "⚿ " : ""}${a.name}`, `og-attr-name${a.key ? " key" : ""}`);
    span(" = ", "og-eq");
    // "this object does not carry that member" and "it carries it, empty" are
    // different facts about a datum, and the first is usually the one worth noticing.
    span(a.absent ? "not set" : a.value, a.absent ? "og-absent" : "og-val");
  });
  return g;
}

// The composition diamond, and only it. The two kinds of line are different claims:
// a containment is a part read out of its whole's value, and carries the diamond; a
// reference is an inference from two values agreeing on a business key, and is drawn
// dashed and bare. Marking both would say the graph knows more than it does.
function ensureObjectMarker(canvas) {
  const defs = canvas._svg.querySelector("defs") || svg("defs", {}, canvas._svg);
  if (!defs.querySelector("#og-diamond")) {
    const marker = svg("marker", {
      id: "og-diamond", markerWidth: 18, markerHeight: 12, refX: 16, refY: 6,
      orient: "auto-start-reverse", markerUnits: "userSpaceOnUse",
    }, defs);
    svg("path", { d: "M0,6 L8,1 L16,6 L8,11 Z", class: "og-mark" }, marker);
  }
}

function ObjectRenderer(eventBus, canvas) {
  BaseRenderer.call(this, eventBus, 1500);
  this.canvas = canvas;
}
inherits(ObjectRenderer, BaseRenderer);
ObjectRenderer.$inject = ["eventBus", "canvas"];

ObjectRenderer.prototype.canRender = (element) => /^uml:object/.test(element.type || "");
ObjectRenderer.prototype.drawShape = (parent, shape) => objectVisual(parent, shape);

ObjectRenderer.prototype.drawConnection = function(parent, connection) {
  const bo = connection.businessObject || {};
  const wp = connection.waypoints;
  const g = svg("g", { class: "og-line-group", "data-id": bo.id || "" }, parent);
  const line = svg("polyline", {
    points: wp.map((p) => `${p.x},${p.y}`).join(" "),
    class: `og-line ${bo.kind || "association"}`,
  }, g);
  // setAttribute, not tiny-svg's attr: attr routes every name that is also a CSS
  // property — fill, marker-start — into the inline style instead, which draws the
  // same but leaves no attribute for a reader (or a test) to see. Which end carries
  // the diamond is what the notation *means*, so it stays an attribute.
  line.setAttribute("fill", "none");
  if (bo.kind === "composition") {
    ensureObjectMarker(this.canvas);
    line.setAttribute("marker-start", "url(#og-diamond)");
  }
  if (bo.label) {
    // A containment drops out of the bottom, so its label rides beside the vertical
    // leg; a reference runs across, so its label sits above the middle of it.
    const at = wp.length > 2
      ? { x: wp[0].x + 8, y: (wp[0].y + wp[1].y) / 2 }
      : { x: (wp[0].x + wp[1].x) / 2, y: (wp[0].y + wp[1].y) / 2 - 6 };
    text(g, bo.label, { x: at.x, y: at.y, class: "og-line-label" });
  }
  return g;
};

ObjectRenderer.prototype.getShapePath = function(shape) {
  return `M${shape.x},${shape.y} l${shape.width},0 l0,${shape.height} l-${shape.width},0 z`;
};

const ObjectRendererModule = {
  __init__: ["objectRenderer"],
  objectRenderer: ["type", ObjectRenderer],
};

const OBJECT_VIEW_MODULES = [
  ObjectRendererModule, SelectionModule, MoveCanvasModule, ZoomScrollModule, OutlineModule,
];

// layoutObjects places every node: roots across, their parts beneath them.
//
// Containment is what nests, so it is what the arrangement follows — a part hangs
// under the whole whose value it came out of. Anything the walk does not reach (a
// cycle of containment the server's guard capped) still gets a place afterwards, so
// no object silently vanishes from a picture of the data.
export function layoutObjects(graph) {
  const nodes = graph.nodes || [];
  const children = {};
  for (const l of graph.links || []) {
    if (l.via === "containment") (children[l.from] = children[l.from] || []).push(l.to);
  }
  const nested = new Set(Object.values(children).flat());
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const pos = new Map();
  let x = 20;

  const place = (node, left, top) => {
    const h = objectHeight(node);
    pos.set(node.id, { x: left, y: top, w: OBJ_W, h, node });
    let bottom = top + h;
    for (const childId of children[node.id] || []) {
      const child = byId.get(childId);
      if (!child || pos.has(childId)) continue;
      bottom = place(child, left + 40, bottom + OBJ_GAP_Y);
    }
    return bottom;
  };
  for (const root of nodes.filter((n) => !nested.has(n.id))) {
    place(root, x, 20);
    x += OBJ_W + OBJ_GAP_X + 40;
  }
  for (const n of nodes) {
    if (pos.has(n.id)) continue;
    place(n, x, 20);
    x += OBJ_W + OBJ_GAP_X;
  }
  return pos;
}

// waypointsFor gives a link its shape. A containment drops out of the bottom of the
// whole, which is where the eye expects a part to hang; a reference leaves one box's
// side and enters the other's.
function waypointsFor(link, a, b) {
  if (link.via === "containment") {
    const x1 = a.x + 20;
    const y2 = b.y + b.h / 2;
    return [{ x: x1, y: a.y + a.h }, { x: x1, y: y2 }, { x: b.x, y: y2 }];
  }
  return [
    { x: a.x + a.w, y: a.y + a.h / 2 },
    { x: b.x + (b.x < a.x ? b.w : 0), y: b.y + b.h / 2 },
  ];
}

export class ObjectCanvas {
  constructor(container) {
    this.diagram = new Diagram({ canvas: { container }, modules: OBJECT_VIEW_MODULES });
    this.canvas = this.diagram.get("canvas");
    this.factory = this.diagram.get("elementFactory");
    this.shapes = new Map();
    this.connections = new Map();
  }

  // render draws one derived graph. There is no reconcile half here, and that is the
  // difference the edit contract makes: the class canvas re-renders on every
  // keystroke in its properties panel, so a redraw there would take the viewport and
  // the undo stack with it. This graph changes only when the instance's data objects
  // do — the host drops it and re-derives it — so a draw is the whole story.
  render(graph) {
    // Both maps are emptied onto the canvas before the new root, not just reset here.
    // diagram-js's element registry is per diagram and not per root, so anything left
    // in it makes the next render fail with "element already exists" — and a
    // connection left behind is the easier of the two to forget, because removing the
    // root looks like it should have taken it.
    for (const conn of this.connections.values()) this.canvas.removeConnection(conn);
    for (const shape of this.shapes.values()) this.canvas.removeShape(shape);
    this.shapes = new Map();
    this.connections = new Map();
    if (this.root) this.canvas.removeRootElement(this.root);
    this.root = this.factory.createRoot({ id: "root-objects" });
    this.canvas.setRootElement(this.root);

    const pos = layoutObjects(graph);
    for (const [id, p] of pos) {
      const shape = this.factory.createShape({
        id, type: "uml:object", x: p.x, y: p.y, width: p.w, height: p.h,
        businessObject: p.node,
      });
      this.canvas.addShape(shape, this.root);
      this.shapes.set(id, shape);
    }
    let drawn = 0;
    for (const [i, l] of (graph.links || []).entries()) {
      const a = pos.get(l.from);
      const b = pos.get(l.to);
      if (!a || !b) continue;
      // The index puts connections *behind* the boxes and keeps them in their own
      // order. diagram-js draws in insertion order within one layer and the shapes
      // are already in, so a line added plainly would cross over a box it passes
      // rather than under it — the SVG this replaces drew every line before every
      // box for exactly that reason. Inserting each at its own index rather than all
      // at 0 is the second half of it: all at 0 puts them behind the boxes but
      // reverses them among themselves, which is visible in the order their labels
      // come out.
      const id = `link-${i}`;
      const conn = this.factory.createConnection({
        id, type: "uml:objectlink",
        source: this.shapes.get(l.from), target: this.shapes.get(l.to),
        waypoints: waypointsFor(l, a, b), businessObject: l,
      });
      this.canvas.addConnection(conn, this.root, drawn++);
      this.connections.set(id, conn);
    }
    // Fitting waits a frame, so the canvas can be gone by the time it runs: a reader
    // who switches back to the list within that frame takes the diagram down first,
    // and fitting a destroyed diagram throws where nothing is left to catch it.
    requestAnimationFrame(() => { if (!this.destroyed) this.fit(); });
  }

  // The same fit as the class canvas: diagram-js shrinks to fit but never magnifies,
  // which leaves a three-object diagram as a small picture in the middle of a wide
  // screen. See ClassCanvas.fit above for the whole argument; the constants are
  // shared so the two surfaces zoom alike.
  fit() {
    this.canvas.zoom("fit-viewport", "auto");
    const box = this.canvas.viewbox();
    if (!box.inner.width || !box.inner.height) return;
    const room = Math.min(box.outer.width / box.inner.width, box.outer.height / box.inner.height);
    const wanted = Math.min(room * FIT_MARGIN, MAX_FIT);
    if (wanted > this.canvas.zoom()) this.canvas.zoom(wanted, "auto");
  }
  zoom(delta) {
    const now = this.canvas.zoom();
    this.canvas.zoom(Math.max(0.2, Math.min(4, now * delta)), "auto");
  }
  destroy() {
    this.destroyed = true;
    this.diagram.destroy();
  }
}
