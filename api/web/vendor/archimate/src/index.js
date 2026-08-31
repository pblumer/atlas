import Diagram from "diagram-js/lib/Diagram";
import BaseRenderer from "diagram-js/lib/draw/BaseRenderer";
import SelectionModule from "diagram-js/lib/features/selection";
import MoveCanvasModule from "diagram-js/lib/navigation/movecanvas";
import ZoomScrollModule from "diagram-js/lib/navigation/zoomscroll";
import { append, attr, create } from "tiny-svg";
import inherits from "inherits-browser";

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

export class Viewer {
  constructor(container, onSelection) {
    this.diagram = new Diagram({
      canvas: { container },
      modules: [ RendererModule, SelectionModule, MoveCanvasModule, ZoomScrollModule ],
    });
    this.canvas = this.diagram.get("canvas");
    this.factory = this.diagram.get("elementFactory");
    this.selection = this.diagram.get("selection");
    this.diagram.get("eventBus").on("selection.changed", (event) => {
      onSelection?.((event.newSelection && event.newSelection[0])?.businessObject || null);
    });
  }

  render(view) {
    if (this.root) this.canvas.removeRootElement(this.root);
    const root = this.factory.createRoot({ id: `root-${view.id}` });
    this.canvas.setRootElement(root);
    this.root = root;
    const shapes = new Map();
    for (const item of view.shapes) {
      const shape = this.factory.createShape({
        id: item.id, type: "archimate:shape", x: item.x, y: item.y, width: item.width, height: item.height,
        businessObject: { kind: "element", ...item.semantic, fill: item.fill },
      });
      this.canvas.addShape(shape, root);
      shapes.set(item.id, shape);
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
    requestAnimationFrame(() => this.fit());
  }

  fit() { this.canvas.zoom("fit-viewport", "auto"); }
  zoom(delta) {
    const current = this.canvas.zoom();
    this.canvas.zoom(Math.max(0.2, Math.min(4, current * delta)), "auto");
  }
  destroy() { this.diagram.destroy(); }
}
