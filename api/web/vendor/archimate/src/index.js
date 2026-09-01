import Diagram from "diagram-js/lib/Diagram";
import BaseRenderer from "diagram-js/lib/draw/BaseRenderer";
import SelectionModule from "diagram-js/lib/features/selection";
import MoveCanvasModule from "diagram-js/lib/navigation/movecanvas";
import ZoomScrollModule from "diagram-js/lib/navigation/zoomscroll";
// The authoring half (ADR-0189 §2, P2a). Moving and resizing shapes, and the
// command stack that makes both undoable. They are loaded only when the caller may
// edit — see Viewer's `editable` option — so a reader's canvas carries no modeling
// behaviour at all rather than carrying it disabled.
import ModelingModule from "diagram-js/lib/features/modeling";
import MoveModule from "diagram-js/lib/features/move";
import ResizeModule from "diagram-js/lib/features/resize";
import OutlineModule from "diagram-js/lib/features/outline";
import RulesModule from "diagram-js/lib/features/rules";
import { append, attr, create } from "tiny-svg";
import inherits from "inherits-browser";
import RuleProvider from "diagram-js/lib/features/rules/RuleProvider";

const XSI = "http://www.w3.org/2001/XMLSchema-instance";

const children = (node, localName) => [...node.children].filter((child) => child.localName === localName);
const first = (node, localName) => children(node, localName)[0] || null;
const nameOf = (node) => {
  const names = children(node, "name");
  return (names.find((name) => name.getAttribute("xml:lang") === "en") || names[0])?.textContent?.trim() || "";
};
const xsiType = (node) => node.getAttributeNS(XSI, "type") || node.getAttribute("xsi:type") || "";
const number = (node, name) => Number(node.getAttribute(name) || 0);

function fillOf(node) {
  const style = first(node, "style");
  const color = style && first(style, "fillColor");
  if (!color) return "";
  const values = [ "r", "g", "b" ].map((key) => Math.max(0, Math.min(255, number(color, key))));
  return `rgb(${values.join(", ")})`;
}

export function parseOpenExchange(xml) {
  const document = new DOMParser().parseFromString(xml, "application/xml");
  const parseError = [...document.getElementsByTagNameNS("*", "parsererror")][0];
  if (parseError) throw new Error("The Open Exchange XML is not well formed");
  const root = document.documentElement;
  if (!root || root.localName !== "model") throw new Error("The document has no ArchiMate model root");

  const elements = new Map();
  for (const node of root.getElementsByTagNameNS("*", "element")) {
    const id = node.getAttribute("identifier");
    if (!id) continue;
    elements.set(id, {
      id, type: xsiType(node) || "Element", name: nameOf(node),
      documentation: first(node, "documentation")?.textContent?.trim() || "",
    });
  }
  const relationships = new Map();
  for (const node of root.getElementsByTagNameNS("*", "relationship")) {
    const id = node.getAttribute("identifier");
    if (!id) continue;
    relationships.set(id, {
      id, type: xsiType(node) || "Relationship", name: nameOf(node),
      source: node.getAttribute("source") || "", target: node.getAttribute("target") || "",
    });
  }

  const problems = [];
  const views = [];
  for (const node of root.getElementsByTagNameNS("*", "view")) {
    if (xsiType(node) && xsiType(node) !== "Diagram") continue;
    const shapes = [];
    const walk = (parent, offsetX, offsetY) => {
      for (const shape of children(parent, "node")) {
        const x = offsetX + number(shape, "x");
        const y = offsetY + number(shape, "y");
        const elementRef = shape.getAttribute("elementRef") || "";
        const semantic = elements.get(elementRef);
        const id = shape.getAttribute("identifier") || `node-${shapes.length + 1}`;
        if (!semantic) problems.push({ severity: "warning", message: `View node ${id} references missing element ${elementRef || "(empty)"}` });
        shapes.push({
          id, elementRef, x, y, width: number(shape, "w") || 160, height: number(shape, "h") || 64,
          fill: fillOf(shape), semantic: semantic || { id: elementRef, type: "Element", name: elementRef || "Missing element" },
        });
        walk(shape, x, y);
      }
    };
    walk(node, 0, 0);

    const connections = children(node, "connection").map((connection, index) => {
      const relationshipRef = connection.getAttribute("relationshipRef") || "";
      const relationship = relationships.get(relationshipRef);
      const id = connection.getAttribute("identifier") || `connection-${index + 1}`;
      if (!relationship) problems.push({ severity: "warning", message: `View connection ${id} references missing relationship ${relationshipRef || "(empty)"}` });
      return {
        id, relationshipRef, source: connection.getAttribute("source") || "", target: connection.getAttribute("target") || "",
        bendpoints: children(connection, "bendpoint").map((point) => ({ x: number(point, "x"), y: number(point, "y") })),
        semantic: relationship || { id: relationshipRef, type: "Relationship", name: relationshipRef || "Missing relationship" },
      };
    });
    views.push({ id: node.getAttribute("identifier") || `view-${views.length + 1}`, name: nameOf(node) || `View ${views.length + 1}`, shapes, connections });
  }

  return { id: root.getAttribute("identifier") || "", name: nameOf(root), elements, relationships, views, problems };
}

const defaultFill = (type) => {
  if (/^(Capability|CourseOfAction|Resource|ValueStream)$/.test(type)) return "#f5b4c5";
  if (/^(Stakeholder|Driver|Assessment|Goal|Outcome|Principle|Requirement|Constraint|Meaning|Value)$/.test(type)) return "#e4c5f3";
  if (/^Business/.test(type) || /^(Product|Contract|Representation)$/.test(type)) return "#fff0ad";
  if (/^Application/.test(type) || type === "DataObject") return "#b5daf7";
  if (/^(Node|Device|SystemSoftware|Technology|CommunicationNetwork|Path)$/.test(type)) return "#bce3c5";
  if (/^(Artifact|WorkPackage|Deliverable|ImplementationEvent|Plateau|Gap)$/.test(type)) return "#d9d9d9";
  return "#f5f6f7";
};

function svg(name, attributes, parent) {
  const node = create(name);
  attr(node, attributes || {});
  if (parent) append(parent, node);
  return node;
}

function wrapLabel(parent, label, width, height) {
  const words = (label || "Unnamed element").split(/\s+/);
  const max = Math.max(8, Math.floor((width - 24) / 7));
  const lines = [ "" ];
  for (const word of words) {
    const current = lines[lines.length - 1];
    if (current && `${current} ${word}`.length > max && lines.length < 3) lines.push(word);
    else lines[lines.length - 1] = current ? `${current} ${word}` : word;
  }
  const text = svg("text", { x: width / 2, y: height / 2 - (lines.length - 1) * 8, "text-anchor": "middle", class: "archimate-label" }, parent);
  lines.forEach((line, index) => {
    const span = svg("tspan", { x: width / 2, dy: index ? 16 : 0 }, text);
    span.textContent = line;
  });
}

function shapeVisual(parent, shape) {
  const { width: w, height: h } = shape;
  const bo = shape.businessObject;
  const type = bo.type;
  const common = { fill: bo.fill || defaultFill(type), stroke: "#3f464d", "stroke-width": 1.4 };
  let primary;
  if (/Service$/.test(type) || type === "Capability") {
    primary = svg("rect", { ...common, width: w, height: h, rx: Math.min(18, h / 2), ry: Math.min(18, h / 2) }, parent);
  } else if (type === "Node" || type === "Device") {
    primary = svg("path", { ...common, d: `M 0 10 L 12 0 H ${w} V ${h - 10} L ${w - 12} ${h} H 0 Z` }, parent);
    svg("path", { d: `M 0 10 H ${w - 12} L ${w} 0 M ${w - 12} 10 V ${h}`, fill: "none", stroke: "#3f464d", "stroke-width": 1 }, parent);
  } else if (type === "Artifact" || type === "DataObject") {
    primary = svg("path", { ...common, d: `M 0 0 H ${w - 16} L ${w} 16 V ${h} H 0 Z` }, parent);
    svg("path", { d: `M ${w - 16} 0 V 16 H ${w}`, fill: "none", stroke: "#3f464d", "stroke-width": 1 }, parent);
  } else if (type === "CommunicationNetwork") {
    primary = svg("path", { ...common, d: `M 12 0 H ${w - 12} L ${w} ${h / 2} L ${w - 12} ${h} H 12 L 0 ${h / 2} Z` }, parent);
  } else {
    primary = svg("rect", { ...common, width: w, height: h, rx: 2, ry: 2 }, parent);
  }

  if (/Component$/.test(type)) {
    svg("rect", { x: w - 28, y: 8, width: 18, height: 14, fill: "none", stroke: "#3f464d", "stroke-width": 1.2 }, parent);
    svg("rect", { x: w - 32, y: 11, width: 7, height: 4, fill: common.fill, stroke: "#3f464d", "stroke-width": 1 }, parent);
    svg("rect", { x: w - 32, y: 18, width: 7, height: 4, fill: common.fill, stroke: "#3f464d", "stroke-width": 1 }, parent);
  } else if (/Interface$/.test(type)) {
    svg("ellipse", { cx: w - 18, cy: 14, rx: 7, ry: 10, fill: "none", stroke: "#3f464d", "stroke-width": 1.2 }, parent);
  } else if (/Process$/.test(type) || type === "ValueStream") {
    svg("path", { d: `M ${w - 30} 10 H ${w - 12} M ${w - 18} 5 L ${w - 12} 10 L ${w - 18} 15`, fill: "none", stroke: "#3f464d", "stroke-width": 1.2 }, parent);
  } else if (type === "SystemSoftware") {
    svg("path", { d: `M 10 ${h - 8} H ${w - 8} V 8`, fill: "none", stroke: "#3f464d", "stroke-width": 1.1 }, parent);
  }
  wrapLabel(parent, bo.name, w, h);
  return primary;
}

function ensureMarker(visuals, id, type) {
  const root = visuals.ownerSVGElement;
  if (!root || root.querySelector(`#${id}`)) return;
  let defs = root.querySelector("defs");
  if (!defs) defs = svg("defs", {}, root);
  const marker = svg("marker", { id, viewBox: "0 0 10 10", refX: 9, refY: 5, markerWidth: 8, markerHeight: 8, orient: "auto-start-reverse" }, defs);
  if (type === "Realization") svg("path", { d: "M 0 0 L 10 5 L 0 10 Z", fill: "white", stroke: "#495057", "stroke-width": 1 }, marker);
  else svg("path", { d: "M 0 0 L 10 5 L 0 10", fill: "none", stroke: "#495057", "stroke-width": 1.5 }, marker);
}

function ArchiMateRenderer(eventBus) {
  BaseRenderer.call(this, eventBus, 1500);
}
inherits(ArchiMateRenderer, BaseRenderer);
ArchiMateRenderer.$inject = [ "eventBus" ];
ArchiMateRenderer.prototype.canRender = (element) => String(element.type || "").startsWith("archimate:");
ArchiMateRenderer.prototype.drawShape = (visuals, shape) => shapeVisual(visuals, shape);
ArchiMateRenderer.prototype.drawConnection = function(visuals, connection) {
  const relationshipType = connection.businessObject.type;
  const markerId = `archimate-marker-${relationshipType.toLowerCase()}`;
  ensureMarker(visuals, markerId, relationshipType);
  const path = svg("path", {
    d: connection.waypoints.map((point, index) => `${index ? "L" : "M"} ${point.x} ${point.y}`).join(" "),
    fill: "none", stroke: "#495057", "stroke-width": 1.4,
    "stroke-dasharray": /^(Realization|Influence|Access)$/.test(relationshipType) ? "6 4" : "",
    "marker-end": /^(Association)$/.test(relationshipType) ? "" : `url(#${markerId})`,
  }, visuals);
  return path;
};
ArchiMateRenderer.prototype.getShapePath = (shape) => `M ${shape.x} ${shape.y} H ${shape.x + shape.width} V ${shape.y + shape.height} H ${shape.x} Z`;
ArchiMateRenderer.prototype.getConnectionPath = (connection) => connection.waypoints.map((point, index) => `${index ? "L" : "M"} ${point.x} ${point.y}`).join(" ");

const RendererModule = {
  __init__: [ "archimateRenderer" ],
  archimateRenderer: [ "type", ArchiMateRenderer ],
};

const center = (shape) => ({ x: shape.x + shape.width / 2, y: shape.y + shape.height / 2 });
const dock = (shape, other) => {
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

// ArchiMateRules says what may be edited. diagram-js asks before every move,
// resize and connect, and with no rules provider it asks nobody and allows
// everything — including the operations this slice does not implement, which would
// then change the canvas and never reach the document.
//
// So the answer is explicit: shapes move and resize, and nothing else is permitted
// yet. Creating elements and drawing relationships arrive with their own slices and
// their own semantic rules; until then the canvas must not offer them, because an
// edit that cannot be saved is worse than one that cannot be made.
function ArchiMateRules(eventBus) {
  RuleProvider.call(this, eventBus);
}
inherits(ArchiMateRules, RuleProvider);
ArchiMateRules.$inject = [ "eventBus" ];
ArchiMateRules.prototype.init = function() {
  this.addRule("elements.move", ({ shapes, target }) => {
    // Only shapes, and only within the view they are already on: re-parenting a
    // shape into another shape is containment, which is a semantic change to the
    // model rather than an arrangement.
    if (!shapes || !shapes.length) return false;
    if (target && target.parent) return false;
    return shapes.every((shape) => shape.type === "archimate:shape");
  });
  this.addRule("shape.resize", ({ shape }) => shape.type === "archimate:shape");
  for (const forbidden of [ "shape.create", "connection.create", "elements.delete", "connection.reconnect" ]) {
    this.addRule(forbidden, () => false);
  }
};

const RulesProviderModule = {
  __depends__: [ RulesModule ],
  __init__: [ "archimateRules" ],
  archimateRules: [ "type", ArchiMateRules ],
};

// The modules a read-only canvas loads, and the ones authoring adds on top.
const VIEW_MODULES = [ RendererModule, SelectionModule, MoveCanvasModule, ZoomScrollModule ];
const EDIT_MODULES = [ ModelingModule, MoveModule, ResizeModule, OutlineModule, RulesProviderModule ];

export class Viewer {
  constructor(container, onSelection, options = {}) {
    this.editable = Boolean(options.editable);
    this.diagram = new Diagram({
      canvas: { container },
      modules: this.editable ? [ ...VIEW_MODULES, ...EDIT_MODULES ] : VIEW_MODULES,
    });
    this.canvas = this.diagram.get("canvas");
    this.factory = this.diagram.get("elementFactory");
    this.selection = this.diagram.get("selection");
    const eventBus = this.diagram.get("eventBus");
    eventBus.on("selection.changed", (event) => {
      onSelection?.((event.newSelection && event.newSelection[0])?.businessObject || null);
    });
    if (this.editable) {
      this.commandStack = this.diagram.get("commandStack");
      // One event for "the picture changed", whatever changed it. The host does not
      // need to know whether a shape was dragged, resized, undone or redone — only
      // that what is on screen no longer matches what was loaded.
      eventBus.on([ "commandStack.changed" ], () => options.onChange?.());
    }
  }

  // moved reports every shape whose geometry differs from the document it was drawn
  // from, as the server's layout writer wants it.
  //
  // It is computed by comparing against what was loaded rather than by accumulating
  // the drags as they happen. Dragging a box away and back is not a change, and an
  // accumulating list would report it as one — which would save a revision that
  // moved nothing and make everybody else's open editor conflict for it.
  moved() {
    const changes = [];
    for (const [ id, origin ] of this.origin || []) {
      const shape = this.shapes.get(id);
      if (!shape) continue;
      const now = { x: Math.round(shape.x), y: Math.round(shape.y), w: Math.round(shape.width), h: Math.round(shape.height) };
      if (now.x === origin.x && now.y === origin.y && now.w === origin.w && now.h === origin.h) continue;
      changes.push({ nodeId: id, ...now });
    }
    return changes;
  }

  undo() { this.commandStack?.canUndo() && this.commandStack.undo(); }
  redo() { this.commandStack?.canRedo() && this.commandStack.redo(); }
  canUndo() { return Boolean(this.commandStack?.canUndo()); }
  canRedo() { return Boolean(this.commandStack?.canRedo()); }

  render(view) {
    if (this.root) this.canvas.removeRootElement(this.root);
    const root = this.factory.createRoot({ id: `root-${view.id}` });
    this.canvas.setRootElement(root);
    this.root = root;
    const shapes = new Map();
    // origin is the geometry the document declared, kept so `moved` can compare
    // against it rather than against the last drag.
    this.origin = new Map();
    this.shapes = shapes;
    for (const item of view.shapes) {
      const shape = this.factory.createShape({
        id: item.id, type: "archimate:shape", x: item.x, y: item.y, width: item.width, height: item.height,
        businessObject: { kind: "element", ...item.semantic, fill: item.fill },
      });
      this.canvas.addShape(shape, root);
      shapes.set(item.id, shape);
      this.origin.set(item.id, { x: item.x, y: item.y, w: item.width, h: item.height });
    }
    for (const item of view.connections) {
      const source = shapes.get(item.source);
      const target = shapes.get(item.target);
      if (!source || !target) continue;
      const waypoints = [ dock(source, target), ...item.bendpoints, dock(target, source) ];
      const connection = this.factory.createConnection({
        id: item.id, type: "archimate:connection", source, target, waypoints,
        businessObject: { kind: "relationship", ...item.semantic },
      });
      this.canvas.addConnection(connection, root);
    }
    this.selection.select(null);
    // A view switch is a new baseline. Leaving the stack would let undo reach back
    // into a view that is no longer on screen and move shapes nobody can see.
    this.commandStack?.clear();
    requestAnimationFrame(() => this.fit());
  }

  fit() { this.canvas.zoom("fit-viewport", "auto"); }
  zoom(delta) {
    const current = this.canvas.zoom();
    this.canvas.zoom(Math.max(0.2, Math.min(4, current * delta)), "auto");
  }
  destroy() { this.diagram.destroy(); }
}
