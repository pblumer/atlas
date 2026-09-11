// Panorama's derived landscape mesh (ADR-0211).
//
// The graph on screen is not a drawing: the server computes it from resources
// Atlas already holds — applications, deployed processes, and the call activities
// between them — so this view says something on an instance where nobody has
// modeled anything. Nothing here is stored, and nothing here is authored.
//
// Layout runs in the browser, in plain JS with no bundler and no CDN (ADR-0012).
// It is a fixed-iteration force simulation rather than an animated one: the same
// graph must land in the same place every time, or a reload looks like a change.
// Beyond the server's size budget the payload arrives already collapsed to
// applications and says so, which this view repeats rather than hides.

import {
  captureView, frameFor, pinsFor, readViews, removeView, saveView, writeViews,
} from "./panorama-views.js";
import {
  exportName, exportStyles, rasterise, save, standaloneSVG, stampLines,
} from "./panorama-export.js";
// The runtime counts here are the engine's own, the same ones the Operations badges
// carry — so they are grouped in thousands the same way (numfmt.js).
import { fmtCount, spanText } from "./numfmt.js";

const esc = (value) => String(value ?? "").replace(/[&<>"']/g, (character) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character]);

// KIND describes every node kind the mesh can carry: how it is drawn, and what it
// is called in the legend. Restricted and unresolved are deliberately distinct —
// "you may not see it" and "it is not deployed" are different findings, and a
// picture that renders them alike answers the wrong question.
//
// Radii carry rank as well as kind. At a few hundred nodes the eye sorts by size
// before it reads anything, so an application has to be unmistakably the largest
// thing on screen and a leaf unmistakably the smallest — otherwise every node
// competes for attention and the picture reads as one texture.
//
// `r` is the floor of a kind's band and `grow` is how far connectivity may carry a
// node up it (see radiusFor). The bands are deliberately closed: the top of one is
// below the floor of the next, so a much-used worker is drawn larger than a lonely
// one and still smaller than any process. Size therefore says two things at once
// without either overwriting the other — what kind of thing this is, and how much
// of the landscape hangs off it.
// The fills are literals rather than the page's soft tokens, and that is the same
// argument the stylesheet makes for --mesh-ink and --mesh-attention. A soft token is
// tuned to sit behind text in a panel, where it must not compete with the words on
// it; a node on a canvas is a shape at a distance, seen against white and at a fifth
// of its drawn size once the estate is big enough to need this view. The panel values
// vanish there — --accent-soft is 1.06 against the canvas — so the kinds stopped
// being told apart by colour at all and shape was left carrying it alone.
//
// A step of tint each, which is enough to separate them and nowhere near enough to
// compete with a status mark: the strongest of them is 1.35 against the canvas, where
// the amber badge is 3.59 and the red 5.44. --ok has no soft companion at :root and
// never had one, which is how this file came to hold the first of these literals.
const KIND = {
  application: { r: 30, grow: 12, shape: "circle", fill: "#dbe6ff", stroke: "var(--accent)", label: "Application" },
  process: { r: 17, grow: 5, shape: "square", fill: "#e9edf5", stroke: "var(--mesh-ink)", label: "Process — deployed" },
  // A saved diagram nobody has deployed. It keeps the process square, because that is
  // what it is going to be, and it is told apart by the two channels a *state* may use
  // here: a warmer fill and a dashed outline. Not by shape — shape is what kind of
  // thing a node is, and a draft is a process; giving it its own outline would say a
  // draft and a process are different kinds of thing, which is the wrong reading.
  //
  // The dash is not decoration. It is the same mark the two placeholder kinds carry,
  // and it means the same thing on all three: what is drawn here is not running. On a
  // printout or to a reader who does not separate the hues, that is the channel that
  // survives (ADR-0211 §4).
  //
  // Smaller than a deployed process, by the width of one rank rather than a whole
  // band: the eye sorts by size first, and on a picture with drafts switched on the
  // running estate has to stay the thing you see. Still comfortably larger than a
  // worker, because it is not one.
  //
  // The fill is lighter than any other kind's, and that is the measured part. Its
  // first draft was a warm tone at the same *luminance* as the process fill — 1.00
  // against it — so the two differed in hue alone: identical on a projector, in a
  // print, and to a reader who does not separate those hues, leaving the dash to carry
  // the whole distinction. This is 1.09 against the process fill and 1.08 against the
  // canvas, so the colour channel does measurable work and a deployed process is the
  // more substantial mark of the two — which is the way round it has to be, since the
  // running estate is what this view is about. It stays far below a finding: the amber
  // status badge is 3.59 against the canvas and the red 5.44, and a kind must never
  // compete with those (ADR-0211 §4).
  draft: { r: 14, grow: 4, shape: "square", fill: "#fbf6ee", stroke: "var(--muted)", label: "Draft — saved, not deployed", dashed: true },
  worker: { r: 12, grow: 3.5, shape: "hexagon", fill: "#d9efe1", stroke: "var(--ok)", label: "Worker" },
  decision: { r: 12, grow: 3.5, shape: "triangle", fill: "#dbe6ff", stroke: "var(--accent-hover)", label: "Decision" },
  // A placeholder for something real whose kind we may not learn, so it takes the
  // shape that is not any kind's. Drawing it as one of them would be a guess wearing
  // the same clothes as a fact.
  restricted: { r: 11, grow: 3, shape: "diamond", fill: "var(--bg)", stroke: "var(--muted)", label: "Restricted — outside your access", dashed: true },
  // Shape comes from the id, which names the kind of thing that is missing — see
  // shapeForNode. The fallback is the same "no kind" diamond.
  unresolved: { r: 11, grow: 3, shape: "diamond", fill: "#fdedd1", stroke: "var(--warn)", label: "Unresolved — nothing here provides it", dashed: true },
  // A peer Atlas this server can promote to. Drawn large, because it is the only
  // thing on this landscape whose state was fetched over the network — and therefore
  // the only one that can be *unreachable* or *stale*, which is exactly what somebody
  // scanning for trouble needs to find first.
  target: { r: 24, grow: 6, shape: "pentagon", fill: "var(--surface)", stroke: "var(--accent-hover)", label: "Deployment target — a peer this server can promote to" },
};

// Shape is the third channel, after colour and size, and the one that survives what
// they do not: a printout, a projector, and a reader who does not separate the hues.
// Colour already carries the kind *and* the ArchiMate layer; size already carries
// rank and connectivity. Form was the last thing left, and a landscape of four
// hundred identical circles was spending it on nothing.
//
// Every shape is **inscribed in the circle the layout reserved** — no vertex is
// further from the centre than the radius the simulation kept clear. That is what
// makes the change free: the separation guarantee is stated in circles, and a shape
// that never leaves its circle cannot break it. It also means the shapes are a
// little smaller than the circles they replace, which is the right way round: the
// application stays the largest thing on screen.

// shapeForNode is which outline a node is drawn with.
//
// An unresolved dependency takes the shape of the thing that is *missing* rather
// than a shape meaning "missing": its id names the kind — a deployment, a worker, a
// decision — and drawing the gap in the silhouette of what should fill it says what
// is wrong at a glance. The dashes already say it is not there.
export function shapeForNode(node, notation) {
  // A projection speaks for the kinds it has a word for and stays silent about the
  // rest, so a placeholder keeps the shape that says what it is rather than being
  // dressed as an element of a notation that has no such element.
  const projected = notation ? typeIn(node?.kind, notation) : null;
  if (projected) return projected.shape;
  if (node?.kind === "unresolved") {
    const missing = String(node.id || "").split(":")[1];
    return KIND[missing]?.shape || KIND.unresolved.shape;
  }
  return (KIND[node?.kind] || KIND.process).shape;
}

// shapeVertices returns a shape's corners at radius r, and an empty list for the
// circle, which has none. Exported because "no vertex leaves the reserved circle" is
// the property the layout depends on, and a property is worth checking as arithmetic
// rather than trusting to the drawing code that happens to implement it.
export function shapeVertices(shape, r) {
  const at = (sides, rotation) => Array.from({ length: sides }, (_, i) => {
    const angle = rotation + (i * 2 * Math.PI) / sides;
    return [Math.cos(angle) * r, Math.sin(angle) * r];
  });
  switch (shape) {
    // Apex up, the way a warning triangle and a flowchart decision both point.
    case "triangle": return at(3, -Math.PI / 2);
    // Flat top and bottom, which is what reads as a hexagon rather than as a blob.
    case "hexagon": return at(6, 0);
    // Point up, so it is told from the hexagon by silhouette rather than by counting
    // corners — which nobody does at a glance, and nobody can do at all zoomed out.
    case "pentagon": return at(5, -Math.PI / 2);
    case "diamond": return at(4, -Math.PI / 2);
    // Axis-aligned, so it reads as a tile rather than as a rotated diamond. Its
    // half-diagonal is r, which is what keeps it inside the reserved circle.
    case "square": return at(4, -Math.PI / 4);
    default: {
      // ArchiMate's own outlines, at the same rule as everything else: the furthest
      // corner sits *on* the reserved circle and none is outside it.
      const icon = ARCHIMATE_ICONS[shape];
      if (icon) return iconPoints(icon.points, r);
      // The wide rectangles the notation projections draw in (see NOTATION_SHAPES). Same
      // rule as every other shape: the corners sit *on* the reserved circle, so the
      // separation guarantee transfers unchanged and a projection cannot make two
      // nodes overlap that did not overlap before.
      const rect = RECTS[shape];
      if (!rect) return [];
      const half = r / Math.hypot(rect.aspect, 1);
      const wide = half * rect.aspect;
      return [[-wide, -half], [wide, -half], [wide, half], [-wide, half]];
    }
  }
}

// RECTS are the shapes drawn as rectangles rather than as polygons: aspect is width
// over height, round is the corner radius as a fraction of the short side.
//
// C4 draws everything as the same box and tells its types apart by the annotation
// under the name rather than by silhouette, so it needs two of them and no more.
// `am-service` is here rather than among the polygons because a stadium is a
// rectangle whose corners are its own half-height, which is what `round: 0.5` says.
const RECTS = {
  square: { aspect: 1, round: 0.26 },
  box: { aspect: 1.9, round: 0.08 },
  rounded: { aspect: 1.9, round: 0.42 },
  "am-service": { aspect: 16 / 9, round: 0.5 },
};

// ARCHIMATE_ICONS is ArchiMate's own notation, as geometry.
//
// The standard defines two ways to draw an element: a rectangle carrying the name
// with a small type icon in its corner, or the icon itself at full size with the name
// beneath it. Both are the notation; the choice is about the space there is. This
// canvas takes the second, and the reason is arithmetic rather than taste — a process
// is drawn at a radius of 17 and a corner icon is about a tenth of the element it
// sits in, which is one or two pixels here. An icon nobody can resolve is a rectangle
// with a smudge in the corner, and every node on the picture would be that same
// rectangle. Drawn as the icon, the silhouette carries the type at a glance, which is
// what this view is read at.
//
// The coordinates are ArchiMate's proportions taken from Archi's own drawing routines
// (ApplicationComponentFigure, ProcessFigure, FunctionFigure, ServiceFigure,
// NodeFigure), centred on the origin and left at their natural scale: `iconPoints`
// normalises them so the furthest corner lands exactly on the reserved circle. So the
// table reads as the shape, and the one number that matters — that nothing leaves the
// circle the layout reserved — is computed rather than hand-fitted.
//
// `detail` is interior line work that is part of the drawing but not of the
// silhouette: the two edges that make a Node's box read as three-dimensional. It is
// drawn from the same coordinates, so it cannot drift away from the outline.
const ARCHIMATE_ICONS = {
  // Application Component: a rectangle with two lugs on its left edge. The lugs sit
  // at a quarter and three quarters of the height, which is where a reader of UML
  // has been looking for them since before ArchiMate existed.
  "am-component": {
    points: [[-3.5, -6.5], [6.5, -6.5], [6.5, 6.5], [-3.5, 6.5], [-3.5, 4.5], [-6.5, 4.5],
      [-6.5, 2], [-3.5, 2], [-3.5, -2], [-6.5, -2], [-6.5, -4.5], [-3.5, -4.5]],
  },
  // Application Process: an arrow. Behaviour with a direction — something is being
  // carried from one end to the other.
  "am-process": {
    points: [[-7, -2], [1, -2], [1, -5], [7, 0], [1, 5], [1, 2], [-7, 2]],
  },
  // Application Function: a chevron. Behaviour gathered by what it can do rather than
  // by the order it happens in, so it points up instead of along.
  "am-function": {
    points: [[-6, 7], [-6, -2], [0, -7], [6, -2], [6, 7], [0, 1]],
  },
  // Node: a box in perspective. The silhouette is the six-sided outline; the two
  // interior edges are what make it a cuboid rather than an arrow-notched rectangle.
  "am-node": {
    points: [[-7, -4], [-4, -7], [7, -7], [7, 4], [4, 7], [-7, 7]],
    detail: [[[-7, -4], [4, -4], [4, 7]], [[4, -4], [7, -7]]],
  },
};

// iconPoints scales one of those outlines to the radius the layout reserved.
//
// Normalised on the furthest corner, so the shape is inscribed in the reserved circle
// exactly as every other shape here is: the separation guarantee is about that circle,
// and a notation that drew outside it could make two nodes overlap that the layout had
// kept apart.
function iconPoints(points, r) {
  let furthest = 0;
  for (const [x, y] of points) furthest = Math.max(furthest, Math.hypot(x, y));
  const k = furthest > 0 ? r / furthest : 0;
  return points.map(([x, y]) => [x * k, y * k]);
}

// bodyElement is the node's own outline, as SVG. Everything downstream keys off the
// mesh-body class rather than off the element name, so severity, hover and impact
// styling are unchanged by a node being a square.
//
// data-r carries the radius the layout reserved. The drawn outline is inscribed in
// it and no longer reports it as an attribute of its own, and it is the reserved
// circle — not the polygon — that the separation guarantee is about.
function bodyElement(shape, r, attrs) {
  const common = `class="mesh-body" data-r="${r.toFixed(1)}" ${attrs}`;
  // Interior line work is drawn *beside* the outline rather than as part of it, and
  // under its own class: the severity and hover rules select .mesh-body and would put
  // a three-pixel red stroke on the fold of a box if it were one element. It is also
  // the reason there is never more than one .mesh-body in a node — everything that
  // reads the picture back, the tests included, asks for the outline by that name.
  const icon = ARCHIMATE_ICONS[shape];
  if (icon) {
    const path = (list) => iconPoints(list, r)
      .map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
    const detail = (icon.detail || []).map((line) =>
      `<polyline class="mesh-body-detail" fill="none" points="${path(line)}"/>`).join("");
    return `<polygon ${common} points="${path(icon.points)}"/>${detail}`;
  }
  const rect = RECTS[shape];
  if (rect) {
    const half = r / Math.hypot(rect.aspect, 1);
    const wide = half * rect.aspect;
    return `<rect ${common} x="${(-wide).toFixed(1)}" y="${(-half).toFixed(1)}"
      width="${(wide * 2).toFixed(1)}" height="${(half * 2).toFixed(1)}"
      rx="${(half * rect.round * 2).toFixed(1)}"/>`;
  }
  const vertices = shapeVertices(shape, r);
  if (!vertices.length) return `<circle ${common} r="${r.toFixed(1)}"/>`;
  const points = vertices.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
  return `<polygon ${common} points="${points}"/>`;
}

// The notations the landscape can be drawn in (ADR-0211 §8).
//
// The *mapping* — what each notation calls each kind, and what it cannot carry —
// comes from the server, and that is the point. Three things read it: these labels,
// the stamp on the image export, and the ArchiMate document the server generates
// from the same landscape. A copy of the table here would eventually have the
// picture calling a node an Application Process beside a file that called it
// something else, which is the failure ADR-0189's connection subset is served to the
// browser to avoid.
//
// What stays here is the half the server has no business having an opinion about:
// which outline to draw. ArchiMate's own convention is a rectangle for structure and
// a rounded one for behaviour; C4 draws everything as the same box and tells its
// types apart by the annotation under the name. Both are rendering.
const NOTATION_SHAPES = {
  "archimate-3.2": {
    application: "am-component", process: "am-process", worker: "am-service",
    decision: "am-function", target: "am-node",
  },
  "c4-projection": {
    application: "rounded", process: "rounded", worker: "rounded",
    decision: "rounded", target: "box",
  },
};

// NOTATION_PAINT is what a projection fills its elements with.
//
// Here for the same reason NOTATION_SHAPES is: the server owns the vocabulary — what
// a node is *called* in a notation — and the browser owns how it is drawn. A colour
// is drawing.
//
// **These are not standard colours, and saying so matters.** ArchiMate 3.2 defines
// none: the specification states that colour carries no formal semantics, and a model
// is free to use any. What everybody recognises as the ArchiMate palette is two
// things at once — the convention the specification's own figures are drawn in, and
// the default fills of Archi, the tool most of these models are made in. The values
// below are Archi's, read off its source rather than sampled from a picture
// (AbstractArchimateElementUIProvider: defaultApplicationColor is rgb(181,255,255),
// defaultTechnologyColor rgb(201,231,183)). They are a de-facto standard, and a
// reader who works in ArchiMate recognises a landscape drawn in them at a glance,
// which is the whole of what this projection is for.
//
// They are also *pale on purpose*, which is what makes them safe here. A layer fill
// is a ground for black text, not a signal; the amber and red a finding is marked
// with have to stay the loudest thing on the canvas (ADR-0211 §4), and these sit far
// below them. The outline is the canvas's own ink rather than a literal, so a node
// keeps the same line weight and the same colour as every edge and every other
// outline on the picture.
//
// Only the kinds the notation has a word for are painted. A draft, a restricted
// placeholder and an unresolved dependency keep Atlas's own colours, for the same
// reason they keep Atlas's own shapes: ArchiMate has no element for them, and
// dressing them as one would be a claim the notation does not make.
const ARCHIMATE_APPLICATION = "#B5FFFF";
const ARCHIMATE_TECHNOLOGY = "#C9E7B7";
const NOTATION_PAINT = {
  "archimate-3.2": {
    application: { fill: ARCHIMATE_APPLICATION, stroke: "var(--mesh-ink)" },
    process: { fill: ARCHIMATE_APPLICATION, stroke: "var(--mesh-ink)" },
    worker: { fill: ARCHIMATE_APPLICATION, stroke: "var(--mesh-ink)" },
    decision: { fill: ARCHIMATE_APPLICATION, stroke: "var(--mesh-ink)" },
    // A Node is Technology, and the layer is the thing its colour says. Drawing a
    // deployment target in the application blue would put it on the wrong floor of
    // the only diagram whose readers count the floors.
    target: { fill: ARCHIMATE_TECHNOLOGY, stroke: "var(--mesh-ink)" },
  },
};

// paintFor is the fill and outline one node is drawn with: the notation's, where it
// has one for that kind, and Atlas's own otherwise.
//
// One function, because two of them would eventually disagree — the key beside the
// picture draws its swatches through this as well, and a key that painted its
// swatches from its own copy would be a legend for a different picture.
export function paintFor(node, notation) {
  const style = KIND[node?.kind] || KIND.process;
  const spoken = notationOf(notation?.id ?? notation);
  const paint = NOTATION_PAINT[spoken.id]?.[node?.kind];
  return paint ? { ...style, ...paint } : style;
}

// ArchiMate's relationship notation, as the two ends of a line.
//
// The elements were already drawn in the notation's own symbols; the lines between
// them were not, and for an ArchiMate reader that is the other half of the alphabet.
// Serving, Triggering and Assignment are told apart by what sits at the ends of an
// otherwise identical solid line: a filled arrowhead, an open one, and a ball at the
// far end. Nothing else distinguishes them — not the colour, not the dash.
//
// Which is also why the dash has to go in this notation. Atlas's own picture draws
// `uses` dashed and `contains` dotted, which is a free channel in a vocabulary that
// has no opinion about it. ArchiMate has one: a dashed line with an open arrowhead
// is a Flow, and a dotted line with a hollow triangle is a Realization. Keeping the
// derived dash would not be a missing statement, it would be a wrong one — so in the
// ArchiMate view all three are solid and the ends carry the whole distinction.
//
// The geometry is Archi's again, read off its connection figures rather than guessed:
// Assignment is a BallEndpoint at the source and a filled PolygonDecoration at the
// target, Triggering a filled PolygonDecoration, Serving an unfilled
// PolylineDecoration — GEF's triangle at its default 7-by-3 scale, and a ball of
// radius 3. The proportions below are that triangle; the size is smaller, because
// Archi draws on boxes of a hundred and twenty pixels and a node here has a radius of
// eleven, so Archi's own pixel count would put a third of a node on the end of every
// line.
const AM_HEAD = 6;        // how far an arrowhead reaches back along the line
const AM_HALF = 2.6;      // half its width across the line — 6:2.6 is Archi's 7:3
const AM_BALL = 2.6;      // Assignment's ball, at the relationship's source

// RELATION_MARKS is what each ArchiMate relationship puts at each of its own ends.
//
// `tail` is the relationship's source and `head` its target, which is not the same
// as the drawn line's two ends: a Serving runs from the provider to the consumer
// while the derived edge runs the other way, and the served row's `flip` is what
// maps one onto the other. Saying it in the relationship's own terms keeps that
// reversal in one place instead of baked into a mark name.
const RELATION_MARKS = {
  Assignment: { tail: "ball", head: "filled" },
  Triggering: { head: "filled" },
  Serving: { head: "open" },
};

// MARKER_IDS resolves a mark and the end it lands on to the marker that draws it.
//
// Two entries per shape because an SVG marker points along the path, and a mark at
// the *start* of a line has to point back into the node the line starts at. SVG 2's
// `orient="auto-start-reverse"` says exactly that and is not old enough to rely on
// here, so the reversed form is a second marker whose own geometry is mirrored —
// which needs no feature at all.
const MARKER_IDS = {
  filled: { end: "am-head-filled", start: "am-head-filled-back" },
  open: { end: "am-head-open", start: "am-head-open-back" },
  // A ball is the same ball whichever end it sits on.
  ball: { end: "am-ball", start: "am-ball" },
};

// MARKER_DEFS draws each of them, in world units.
//
// markerUnits is userSpaceOnUse rather than the default strokeWidth: the lines are
// drawn with a non-scaling stroke, so tying the mark to the stroke would freeze it at
// one size on screen while every node around it grew with the zoom. In user space it
// scales with the picture, exactly as the node outlines do.
//
// The paint is a presentation attribute and the stylesheet raises it to
// `context-stroke` (see .mesh-edge-mark): where that is understood a mark takes the
// colour of the line it ends, including the accent a hovered edge is lit in, and
// where it is not the attribute stands and the mark is the resting line colour. Both
// are readable; neither can be the wrong relationship.
const MARKER_DEFS = {
  "am-head-filled": `<polygon class="mesh-edge-mark" fill="var(--mesh-line)"
    points="0,0 ${AM_HEAD},${AM_HALF} 0,${2 * AM_HALF}"/>`,
  "am-head-filled-back": `<polygon class="mesh-edge-mark" fill="var(--mesh-line)"
    points="${AM_HEAD},0 0,${AM_HALF} ${AM_HEAD},${2 * AM_HALF}"/>`,
  "am-head-open": `<polyline class="mesh-edge-mark mesh-edge-mark-open" fill="none"
    stroke="var(--mesh-line)" points="0,0 ${AM_HEAD},${AM_HALF} 0,${2 * AM_HALF}"/>`,
  "am-head-open-back": `<polyline class="mesh-edge-mark mesh-edge-mark-open" fill="none"
    stroke="var(--mesh-line)" points="${AM_HEAD},0 0,${AM_HALF} ${AM_HEAD},${2 * AM_HALF}"/>`,
  "am-ball": `<circle class="mesh-edge-mark" fill="var(--mesh-line)"
    cx="${AM_BALL}" cy="${AM_BALL}" r="${AM_BALL}"/>`,
};

// markerElement is one <marker>, sized and anchored so the drawn point lands on the
// line's end rather than beside it.
function markerElement(id) {
  const body = MARKER_DEFS[id];
  if (!body) return "";
  if (id === "am-ball") {
    return `<marker id="${id}" markerUnits="userSpaceOnUse" orient="auto"
      markerWidth="${2 * AM_BALL}" markerHeight="${2 * AM_BALL}"
      refX="${AM_BALL}" refY="${AM_BALL}">${body}</marker>`;
  }
  // refX is the tip: at the far end for a forward head, at the near end for the
  // mirrored one, which is what puts both points exactly on the line's end.
  const refX = id.endsWith("-back") ? 0 : AM_HEAD;
  return `<marker id="${id}" markerUnits="userSpaceOnUse" orient="auto"
    markerWidth="${AM_HEAD}" markerHeight="${2 * AM_HALF}"
    refX="${refX}" refY="${AM_HALF}">${body}</marker>`;
}

// markEnds says which marker goes on which end of one drawn line.
//
// The drawn line runs from the derived edge's `from` to its `to`. A relationship the
// notation runs the other way puts its head on `from` — the arrowhead moves rather
// than the line, so the picture stays the landscape's own geometry and only the
// claim on it is the notation's.
export function markEnds(kind, notation) {
  const relation = relationIn(kind, notation);
  const mark = relation?.mark;
  if (!mark) return null;
  const headAt = relation.flip ? "start" : "end";
  const tailAt = relation.flip ? "end" : "start";
  const ends = { relation, start: null, end: null };
  if (mark.head) ends[headAt] = MARKER_IDS[mark.head][headAt];
  if (mark.tail) ends[tailAt] = MARKER_IDS[mark.tail][tailAt];
  return ends;
}

// EDGE_TRIM_MOST is the most of a line either end may give up to the node it touches.
//
// A line is shortened by each node's reserved radius so the mark sits outside the
// shape instead of under it. Two nodes closer together than their radii — which the
// separation pass discourages and a drag can still produce — would shorten it past
// its own midpoint and draw it inside out. The cap makes that case a short line with
// its marks still the right way round.
const EDGE_TRIM_MOST = 0.42;

// trimEdge shortens one line to the two circles it runs between.
export function trimEdge(a, b, ra, rb) {
  const dx = b.x - a.x, dy = b.y - a.y;
  const d = Math.hypot(dx, dy);
  if (!(d > 0)) return { x1: a.x, y1: a.y, x2: b.x, y2: b.y };
  const ux = dx / d, uy = dy / d;
  const cutA = Math.min(ra, d * EDGE_TRIM_MOST);
  const cutB = Math.min(rb, d * EDGE_TRIM_MOST);
  return {
    x1: a.x + ux * cutA, y1: a.y + uy * cutA,
    x2: b.x - ux * cutB, y2: b.y - uy * cutB,
  };
}

// The landscape drawn as itself: Atlas's own kinds, no projection, nothing to
// declare. It is here rather than fetched because it is what the view falls back to
// when the mapping cannot be read at all — a picture in its own vocabulary is never
// wrong about which vocabulary it is in.
const DERIVED_NOTATION = {
  id: "atlas", label: "Atlas (derived)", short: "Atlas",
  projection: false, mappingVersion: 0, types: {}, relations: {}, loss: [], weigh: "degree",
};

// HEATS are the ways of drawing the same landscape with its *sizes* carrying a
// quantity the engine holds, rather than the structure the layout already draws.
//
// They sit in the notation picker rather than beside it as switches, and that is a
// claim worth making explicitly: neither is a second vocabulary — they say nothing
// about what a node is *called* — but each is the same kind of choice. A notation
// decides how the picture is drawn; so does this. And they are mutually exclusive
// with the projections and with each other for a reason the picture cannot argue its
// way out of: size is one channel, and it can carry connectivity or load or trouble,
// never two of them. A checkbox beside the picker offered exactly the combination
// that has no reading — an ArchiMate landscape whose radii mean two things at once —
// and a list where one entry is chosen is the honest shape of "pick what size means
// here".
//
// What a weighting costs: a node's size no longer says what kind of thing it is or
// how much hangs off it. Shape and colour still carry the kind, and the key says so.
// What it buys is a question the structural picture cannot answer at a glance.
//
// Three of them, because they are three different questions.
//
//   - *Instances* — "where is the work". Capacity, reading a load test, finding the
//     process that is actually carrying the estate.
//   - *Incidents* — "where is it stuck". The severity badges already say *which* nodes
//     have a finding (ADR-0211 §4); what they cannot say is how much is parked behind
//     each, and a node with four hundred stuck tokens wears the same badge as one with
//     a single retry.
//   - *Incident age* — "how long has it been stuck", and this is the one that changes
//     a decision. Four hundred incidents raised in the last five minutes is a worker
//     that has just fallen over and will drain itself once somebody restarts it; three
//     standing since Friday is a process nobody is coming back to. The count ranks
//     those the wrong way round, every time.
//
// One table because they differ only in what they read off a node and what they call
// it. Weightings written out separately would drift — one gaining a floor rule or a
// grouped number the others never got — and the reader would have no way to know
// which of the pictures they were looking at was the maintained one.
//
// The third is a *duration* rather than a count, which is the only thing in the table
// that is not uniform: it is measured against a clock rather than read off the node,
// so every reader passes the moment it is measuring at. One moment per repaint, or
// the largest node could come out larger than the reference it is a share of.
// counted is the two spellings of a plain tally: the plain one for the canvas, where
// the text is drawn as SVG, and the rich one for a ranking row, where the number is
// the part worth setting in bold. One word per weighting, so the two cannot end up
// calling a tally different things.
const counted = (unit) => ({
  text: (n) => `${fmtCount(n)} ${unit}`,
  rich: (n) => `<b>${fmtCount(n)}</b> ${unit}`,
  // And the bare quantity, for the scale in the key, where the unit is written once
  // in the heading above the row instead of five times along it.
  tick: (n) => fmtCount(n),
});

const HEATS = {
  instances: {
    key: "instances", label: "Instances (heatmap)", short: "Instances",
    // The engine's own tally, the same one the Operations badges carry. Absent on
    // every kind that cannot have instances, which is why this reads through the
    // optional chain rather than defaulting: "no instances" and "cannot have
    // instances" are different facts and neither is a zero to be drawn.
    of: (node) => node?.runtime?.running,
    // One running instance is the smallest thing this weighting can be asked about,
    // and it is a real thing rather than a rounding: a process running one is running.
    least: 1,
    leastPhrase: "one running instance",
    // What the nothing-at-all mark on the scale selects, in this weighting's own
    // words. "Nothing instances at all" is what a generic phrasing produces, and a
    // control that reads like that is one nobody trusts.
    bandNone: "Nothing running at all",
    ...counted("running"),
    // What size means, in one sentence, for the key and for whoever has to read the
    // picture after it has been pasted somewhere with no key beside it.
    heading: "Size is load here, not structure.",
    peakPhrase: (peak) => `the busiest one on this landscape, which is running
      <b>${fmtCount(peak)}</b>`,
    floorNote: `Anything with no running instances of its own sits at the floor — a
      worker, a decision, and an application too, whose load is on the processes it
      holds — so nothing drops off the picture, and one running instance is already
      unmistakably above it.`,
    quiet: `<b>Size is load here, not structure</b> — and nothing is running on this
      landscape at all, so every node is drawn at the same floor.`,
    // Why a node can be sizeable and still carry no number under its name.
    absent: `Running instances are drawn under the names that have any. A process with
      none carries no number; select it to see the zero, and what it has finished.`,
    // What the column beside the picture calls this ordering, and what it says when
    // there is nothing to order.
    rankHeading: "Busiest",
    rankSub: "how much each one is running",
    rankEmpty: `Nothing is running on this landscape, so there is nothing to rank by
      load. The blast-radius ranking is on the derived drawing.`,
  },
  incidents: {
    key: "incidents", label: "Incidents (heatmap)", short: "Incidents",
    // Unresolved incidents the engine holds against this node. Only a process can
    // carry one — an incident belongs to a token — and a collapsed application
    // carries the sum of the processes it stands for.
    of: (node) => node?.incidents,
    // One incident is one incident. There is no smaller amount of trouble.
    least: 1,
    leastPhrase: "one incident",
    bandNone: "Nothing parked at all",
    ...counted("incident(s)"),
    heading: "Size is trouble here, not structure.",
    peakPhrase: (peak) => `the worst one on this landscape, which is holding
      <b>${fmtCount(peak)}</b>`,
    floorNote: `Everything with nothing parked on it sits at the floor, so a healthy
      estate reads as a flat one and a single incident is already the shape of an
      exception. The badges still say which nodes have a finding; the size says how
      much is behind each.`,
    quiet: `<b>Size is trouble here, not structure</b> — and nothing on this landscape
      is parked at all, so every node is drawn at the same floor. That is the answer,
      not a missing one.`,
    absent: `Open incidents are drawn under the names that have any. A node with none
      carries no number, and a kind that cannot hold one — a worker, a decision — never
      does: an incident belongs to a token, and only a process has tokens.`,
    rankHeading: "Most parked",
    rankSub: "how much is stuck on each",
    rankEmpty: `Nothing on this landscape is parked, so there is nothing to rank. That
      is the answer rather than an empty list — and it is the one worth having.`,
  },
  "incident-age": {
    key: "incident-age", label: "Incident age (heatmap)", short: "Incident age",
    // How long the earliest unresolved incident on this node has been standing. The
    // server sends the moment it was raised (Unix nanoseconds) rather than an age,
    // because an age computed there would be stale by the time it was drawn — and
    // because a moment is the same fact for every reader, wherever their clock is.
    //
    // The *oldest* incident, which is the server's choice and the right one: the newest
    // says only that something happened lately, which the runtime tally already says
    // better, and an average is not a fact about any incident, so nothing can be
    // pointed at.
    of: (node, at) => (node?.oldestIncident > 0
      ? Math.max(0, at - node.oldestIncident / 1e6) : 0),
    // A minute, because that is the smallest age worth drawing a difference for: an
    // incident raised forty seconds ago and one raised ten are the same finding, and
    // the raw number here is nanoseconds, where "one" means nothing to anybody.
    least: 60_000,
    leastPhrase: "a minute stuck",
    bandNone: "Nothing parked at all",
    text: (ms) => `stuck ${spanText(ms)}`,
    rich: (ms) => `stuck <b>${esc(spanText(ms))}</b>`,
    tick: (ms) => spanText(ms),
    heading: "Size is age here, not structure.",
    peakPhrase: (peak) => `the longest-parked one on this landscape, which has been
      stuck <b>${esc(spanText(peak))}</b>`,
    floorNote: `Everything with nothing parked on it sits at the floor, and anything
      parked at all stands above it. A process that parked its first token an hour ago
      is small beside one that parked its first on Friday, however many each is
      holding — how much is the other picture, and the two routinely rank the same
      estate the opposite way round.`,
    quiet: `<b>Size is age here, not structure</b> — and nothing on this landscape is
      parked at all, so every node is drawn at the same floor. That is the answer, not
      a missing one.`,
    absent: `How long each has been stuck is drawn under the names that have any. A node
      with nothing parked carries no number — and neither does one whose incidents were
      all raised before this engine recorded the moment, which is a fact about the
      record rather than about the process.`,
    rankHeading: "Stuck longest",
    rankSub: "how long each has been parked",
    rankEmpty: `Nothing on this landscape is parked, so there is nothing to rank. That
      is the answer rather than an empty list — and it is the one worth having.`,
  },
};

// heatOf is which quantity a way of drawing spends its radii on, or null for the
// ones that spend them on structure. One lookup rather than a flag carried beside
// the notation: four things need the answer — the radius, the number under the name,
// the margin the layout reserves for it, and the sentence the key writes — and a
// picture where two of them disagreed would draw a number a node has no room for.
export function heatOf(notation) {
  return HEATS[notationOf(notation?.id ?? notation).weigh] || null;
}

// The heat weightings, as entries the picker can offer. Built from the table above
// so a weighting cannot exist as a picker entry the renderer has never heard of.
const HEAT_NOTATIONS = Object.fromEntries(Object.values(HEATS).map((heat) => [heat.key, {
  id: heat.key, label: heat.label, short: heat.short,
  projection: false, mappingVersion: 0, types: {}, relations: {}, loss: [], weigh: heat.key,
}]));

// The local entries are held here rather than fetched, by the split this file already
// keeps: what a node is *called* is the server's table (ADR-0211 §8), and how big it
// is drawn is this side's business, exactly like NOTATION_SHAPES.
let notations = { atlas: DERIVED_NOTATION, ...HEAT_NOTATIONS };

// useNotations takes what the server serves and adds this side's shapes to it. An
// entry with no shapes is still usable — every kind falls back to its derived
// outline — so a notation the server learns about before this file does degrades to
// a vocabulary change rather than to a blank canvas.
export function useNotations(served) {
  const next = { atlas: DERIVED_NOTATION, ...HEAT_NOTATIONS };
  for (const notation of Array.isArray(served) ? served : []) {
    // The locally-defined entries win over a served row of the same id. They are
    // rendering decisions rather than vocabularies, and a server that grew a word for
    // one of them must not be able to turn a weighting into a projection.
    if (!notation?.id || next[notation.id]) continue;
    const shapes = NOTATION_SHAPES[notation.id] || {};
    next[notation.id] = {
      id: notation.id,
      label: notation.label || notation.id,
      short: notation.short || notation.label || notation.id,
      projection: Boolean(notation.projection),
      mappingVersion: notation.mappingVersion ?? 0,
      loss: Array.isArray(notation.loss) ? notation.loss : [],
      // The served row carries what a person is shown *and* the notation's own
      // machine token; the picture wants the first and the exported document the
      // second, and they come from one row so the two cannot drift apart.
      types: Object.fromEntries(Object.entries(notation.types || {}).map(([kind, type]) =>
        [kind, { name: type?.name || kind, type: type?.type || "", shape: shapes[kind] || null }])),
      // The same for the edges, with the mark this side draws each relationship
      // with. Keyed by the notation's own machine token rather than by the derived
      // edge kind: the served row already says which relationship an edge is, and
      // reading the mark off that token is what makes the arrowhead on the picture
      // and the xsi:type in the exported file two readings of one answer.
      relations: Object.fromEntries(Object.entries(notation.relations || {}).map(([kind, rel]) =>
        [kind, {
          name: rel?.name || kind, type: rel?.type || "", flip: Boolean(rel?.flip),
          mark: RELATION_MARKS[rel?.type] || null,
        }])),
    };
  }
  notations = next;
  return notations;
}

// notationsAvailable is what the picker offers, in the order the server listed them.
export function notationsAvailable() {
  return Object.values(notations);
}

// notationOf resolves an id, falling back to the derived vocabulary. An unknown id
// is a stale saved view or a hand-edited URL, and drawing the landscape as itself is
// the answer that cannot mislead.
export function notationOf(id) {
  return notations[id] || notations.atlas;
}

// typeIn is what a notation calls this kind of node, or null where it has no word
// for it. Null is a real answer and never an empty string: the caller draws the
// derived shape and the legend lists the kind as loss.
export function typeIn(kind, notation) {
  return notationOf(notation?.id ?? notation).types[kind] || null;
}

// relationIn is what a notation calls this kind of edge, or null where it has no
// word for it. Null draws the derived line, unmarked — the same answer typeIn gives
// for a node the notation cannot name, and for the same reason: a mark invented here
// would be a claim the notation does not make.
export function relationIn(kind, notation) {
  return notationOf(notation?.id ?? notation).relations[kind] || null;
}

// DEGREE_FULL is the number of dependencies at which a node is drawn at the top of
// its band. It is a fixed reference rather than the busiest node in this particular
// graph, and that is the point: normalising against the graph would make the same
// node change size when a filter removes something else, so its size would describe
// the current screen rather than the node. Twelve is where the curve below flattens
// — past it, "very connected" is the whole of the answer and the exact count is
// what the panel is for.
const DEGREE_FULL = 12;

// radiusFor draws a node at its kind's floor plus however far its connectivity
// carries it up the kind's band.
//
// Logarithmic, because the difference between one dependency and four is the one
// worth seeing: it is the difference between a leaf and a small hub. Between forty
// and fifty there is nothing left to say that the size could carry, and a linear
// scale would spend the whole band saying it.
export function radiusFor(node, degree) {
  const style = KIND[node.kind] || KIND.process;
  const reach = Math.log2(1 + Math.max(0, degree || 0)) / Math.log2(1 + DEGREE_FULL);
  return style.r + (style.grow || 0) * Math.min(1, reach);
}

// HEAT_FLOOR is the radius every node keeps on a heat weighting, whatever its tally
// — and it is the whole reason the weightings are usable at all.
//
// A size that were *only* the count would draw an idle process at nothing, and a
// landscape whose quiet half is invisible is not a picture of where the trouble is:
// it is a picture with the context deleted, and a reader cannot tell "nothing here"
// from "not on this server". So the floor is a node that is unmistakably a node — at
// the size a worker is drawn on the structural picture — and the count is what is
// added on top of it.
const HEAT_FLOOR = 11;
// HEAT_SPAN is how much radius the worst node earns above the floor. Getting on for
// three times the floor, which puts it past the largest thing the structural picture
// ever draws: the two ends of the estate are then told apart at a glance rather than
// by measurement, which is the whole of what a weighting is for.
const HEAT_SPAN = 30;
// HEAT_STEP is what a node earns the moment it carries anything at all, before the
// weighting has said how much.
//
// It exists because "some" and "none" is the first question a heatmap is asked, and
// on a ratio scale that question has no answer at the bottom: the smallest tally is
// the origin, so a process running one instance and a process running none would be
// drawn the same size. So the scale starts a step up, and the step is the whole of
// what "this one is doing something" costs.
//
// Six, which puts the smallest node that carries anything at 17 against a floor of
// 11. That is two and a third times the area, and — the part that makes it the right
// number rather than a large one — it is exactly the gap between a worker and a
// process on the structural picture (KIND: 12 and 17). So "has any at all" reads at
// the same glance as "is a different kind of thing", which is the glance this view is
// read at.
const HEAT_STEP = 6;

// heatPeak is the largest tally on a landscape, and the reference every node on it is
// drawn against.
//
// Taken from the *whole* landscape rather than from whatever is currently on screen,
// and that is deliberate: filtering to two nodes must not make the smaller of them
// swell into the largest thing ever drawn. It is the same objection DEGREE_FULL
// answers with a constant, answered differently because the quantity is different —
// twelve dependencies is a lot on every Atlas ever deployed, and "a lot of running
// instances" is three on one server and forty thousand on the next, while "a lot of
// incidents" is one on an estate that has never had any. A constant would draw one
// server as uniformly idle and the next as uniformly saturated.
//
// The price is that a radius means something only against a stated reference, so the
// key and the export stamp state it. A picture that did not say what its largest node
// stands for would be a quantity with no unit.
//
// `at` is the moment a duration weighting is measured against, and every node on one
// picture has to be measured against the same one: read per node, the reference would
// be taken a few milliseconds before the node that set it, and the largest node would
// come out larger than the whole it is a share of. The counts ignore it.
export function heatPeak(graph, heat, at = Date.now()) {
  const read = heatReader(heat);
  if (!read) return 0;
  let peak = 0;
  for (const node of graph?.nodes || []) {
    const value = read(node, at);
    if (typeof value === "number" && value > peak) peak = value;
  }
  return peak;
}

// radiusForHeat sizes a node by whatever the chosen weighting counts on it.
//
// Three rules, and the whole encoding is in them:
//
//	value 0      →  r = HEAT_FLOOR
//	value least  →  r = HEAT_FLOOR + HEAT_STEP
//	value peak   →  r = HEAT_FLOOR + HEAT_SPAN
//
// and between the last two the radius rises with the **logarithm of the ratio**:
//
//	r = HEAT_FLOOR + HEAT_STEP + (HEAT_SPAN - HEAT_STEP) · ln(value/least) / ln(peak/least)
//
// So equal steps of radius are equal *multiples* of the tally. A process running ten
// instances stands as far above one running one as one running a hundred stands above
// it. That is a ratio scale, and it is the right one for this quantity: an estate's
// instance counts span one to several thousand, its incident counts one to a handful,
// and its incident ages a minute to a fortnight — ranges no linear reading can carry,
// because the top of each decides the scale and everything an order of magnitude below
// it lands in the same place.
//
// **What this replaced, and what it gave up.** The radius used to rise with the square
// root of the share of the peak, which makes a circle's *area* proportional to the
// tally — the textbook encoding for a quantity drawn as a disc, and the one the eye is
// calibrated against on a bubble chart. It answers "how much", and the arithmetic of
// it was right. What it could not do is the thing this view is opened for. On a span
// of thirty, a node at a hundredth of the peak was drawn three units above a node
// carrying nothing at all, and a node at a thousandth was drawn one unit above it: the
// whole quiet majority of a real landscape collapsed onto the floor, and the reader
// could not tell a process running one from a process running none. The ratio scale
// answers "how many times" instead, and that is the question an operator is actually
// asking of a heatmap. It is a deliberate trade and the key says which one is on the
// picture, because a radius means nothing without the law that produced it.
//
// **least** is the smallest tally a weighting distinguishes, declared by the weighting
// rather than found on the landscape (see HEATS). One running instance, one incident,
// one minute of age. Declaring it is what keeps the picture stable — a reading taken
// off the landscape would rescale every node the moment one quiet process appeared —
// and what keeps it honest for a duration, where the raw number is nanoseconds and
// "one of them" means nothing to anybody.
//
// A landscape whose peak is at or below its least — every incident is the only
// incident — has no range to speak of, and everything carrying anything is
// simultaneously the smallest and the largest of it. They are drawn at the top,
// because being the worst is what they are.
//
// A node with no tally at all — a worker, a decision, a deployment target, a draft,
// a placeholder — sits on the floor rather than being sized as a zero, and that is
// the same fact rather than a missing one: nothing is counted there because nothing
// can be.
export function radiusForHeat(node, peak, heat, at = Date.now()) {
  const entry = heatEntry(heat);
  return radiusForTally(entry?.of ? entry.of(node, at) : 0, peak, entry);
}

// radiusForTally is that law over a bare number, and it is separate for one reason:
// the scale in the key is drawn from it too. A key whose circles were sized by their
// own copy of the arithmetic would be a picture of a scale rather than the scale, and
// the two would part company the first time either was touched.
export function radiusForTally(tally, peak, heat) {
  const entry = heatEntry(heat);
  const value = Math.max(0, tally || 0);
  if (!entry || value <= 0) return HEAT_FLOOR;
  const least = heatLeast(entry);
  // The top is never below the value in hand: a peak read from a landscape this node
  // is no longer on would otherwise size it past the maximum.
  const top = Math.max(peak || 0, value, least);
  const rise = top > least
    ? Math.log(Math.max(value, least) / least) / Math.log(top / least)
    : 1;
  return HEAT_FLOOR + HEAT_STEP + (HEAT_SPAN - HEAT_STEP) * Math.min(1, Math.max(0, rise));
}

// HEAT_TICKS is how many circles the scale in the key is allowed to show, the
// nothing-at-all one included. Five, because the row has to stay on one line beside a
// legend that already carries the kinds, the edges, the severities and the
// provenances — and because a scale a reader counts along is one they have stopped
// reading. What it must never drop is either end: the smallest thing that counts, and
// the largest thing there is.
const HEAT_TICKS = 5;

// heatTicks are the tallies the scale in the key puts a circle against.
//
// Powers of ten from the weighting's least upward, and then the peak. That is the
// scale's own ruling: the law is logarithmic, so the marks a reader can interpolate
// between are the decades, and a linear set of marks on a logarithmic scale would be
// four of them crowded at one end.
//
// The decade just below the peak is dropped when the peak is sitting on top of it,
// because two circles a hair apart with different numbers under them read as a
// rendering fault rather than as a scale.
export function heatTicks(peak, heat) {
  const entry = heatEntry(heat);
  if (!entry || !(peak > 0)) return [];
  const least = heatLeast(entry);
  const top = Math.max(peak, least);
  const decades = [least];
  for (let v = least * 10; v < top; v *= 10) decades.push(v);
  if (decades.length > 1 && top / decades[decades.length - 1] < 2) decades.pop();
  const crowned = top > least;
  // Places left for decades once the nothing-at-all circle and the peak have taken
  // theirs.
  const room = HEAT_TICKS - 1 - (crowned ? 1 : 0);
  let kept = decades;
  if (decades.length > room) {
    // Thinned by a constant stride rather than by picking `room` of them evenly: a
    // ladder whose rungs are a hundredfold apart is one a reader can carry, where a
    // ladder of ten, a hundredfold, and then fourfold is three different rules on one
    // line. Fewer rungs than there is room for is the price, and it is the right way
    // round.
    const stride = Math.ceil(decades.length / room);
    kept = decades.filter((_, i) => i % stride === 0);
  }
  return crowned ? [...kept, top] : kept;
}

// heatLeast is the smallest tally a weighting tells apart from the next one up. One,
// for anything counted; a weighting that measures something continuous says so itself.
function heatLeast(entry) {
  return entry?.least > 0 ? entry.least : 1;
}

// heatReader resolves either spelling of a weighting — the entry itself, or the key
// naming it — to the function that reads a node's tally. A weighting this build does
// not know reads as none rather than as zero everywhere, so an unfamiliar saved view
// draws the structural picture instead of a flat one.
function heatReader(heat) {
  return heatEntry(heat)?.of || null;
}

// heatEntry resolves either spelling of a weighting to the weighting itself, which is
// what the size law needs: it reads the tally *and* the smallest tally that weighting
// distinguishes, and the two have to come from the same place or they can disagree.
function heatEntry(heat) {
  return (typeof heat === "string" ? HEATS[heat] : heat) || null;
}

// A target is not part of the dependency graph — no edge is derived to it, because
// a promotion is an act rather than a stored relationship and this server does not
// record which of its applications is running over there. It sits beside the
// landscape rather than in it, which is what it is.

// PROVENANCE describes how a node is known (ADR-0211 §2). It is rendered on every
// node, always: a picture that mixed what Atlas found with what somebody declared,
// without saying which is which, is exactly the conflation the record exists to
// prevent. Shape carries it as well as colour — a dashed ring for something only
// declared, a second ring for something known from both sides.
const PROVENANCE = {
  derived: { label: "Derived — Atlas has it, nothing models it" },
  both: { label: "Both — Atlas has it and a model binds to it", ring: true },
  modeled: { label: "Modeled — a model declares it, Atlas does not have it", ghost: true },
};

// SEVERITY is ADR-0211 §4's three classes plus the neutral one, as this view draws
// them. Two rules from the record are structural here rather than stylistic:
//
//   - Color is never the only channel. Every class carries a distinct glyph, and the
//     legend is text, so the picture is readable without color perception.
//   - Unknown is neutral, not a fourth level of badness. Most nodes on a young
//     instance are unobserved, and drawing them as a problem makes the whole mesh a
//     problem — which is how a status view teaches people to ignore it.
const SEVERITY = {
  critical: { glyph: "!", stroke: "var(--danger)", beats: true, label: "Critical — it cannot do work" },
  attention: { glyph: "•", stroke: "var(--mesh-attention)", beats: true, label: "Attention — something inside it went wrong" },
  ok: { glyph: "", stroke: "", label: "OK — nothing is wrong here" },
  unknown: { glyph: "?", stroke: "", label: "Unwatched — nothing here observes it" },
};

// EDGE_KEY is what each derived edge means, in the order the key lists them.
//
// Three kinds, three claims — this application holds that process, this process
// invokes that one, this process needs that worker — and until now two of them were
// drawn alike. The ArchiMate export has always told them apart (Assignment,
// Triggering, Serving), so the canvas was the one surface where the distinction the
// data carries was not on screen.
//
// The strokes live in the stylesheet, keyed by the same kind (.mesh-edge-*), and the
// legend draws its swatches with those classes rather than with a copy of them. The
// order here is the order the key reads in, and it is deliberate: the two kinds that
// carry a failure path first, the structure they hang on last.
//
// Two labels per kind, because the same claim is introduced by two different things.
// In Atlas's own picture the line style is what a reader has to be told about, so the
// row leads with it. In a notation that names the relationship, the name leads and the
// line style is no longer the distinction — every ArchiMate line here is solid, and
// the ends carry it.
const EDGE_KEY = [
  ["calls", "Solid line — calls: a process invokes another process",
    "a process invokes another process"],
  ["uses", "Dashed line — uses: a process depends on a worker or a decision",
    "a process depends on a worker or a decision"],
  ["contains", "Dotted line — belongs to: an application and the processes it holds",
    "an application and the processes it holds"],
];

// PULSE_BUDGET is how many beating nodes the view will animate at once.
//
// A landscape where three things are wrong should draw the eye to those three. One
// where two hundred are wrong is not a picture of three problems, it is a picture of
// an outage — and two hundred simultaneous animations say less than a still frame
// does while costing a great deal more to paint. Past the budget the rings stay,
// unmoving: the findings are still marked, they have simply stopped competing.
const PULSE_BUDGET = 80;

// STATE_TEXT names the observation state under a severity (ADR-0189 §6). The class
// is a reading aid for a zoomed-out picture and never a replacement: an operator
// acting on a finding needs the state, so both are shown wherever there is room.
const STATE_TEXT = {
  healthy: "healthy",
  degraded: "degraded",
  "not-ready": "not ready",
  unreachable: "unreachable",
  stale: "stale",
  unbound: "unbound — nothing observes it",
};

// LABEL_TIERS decides which names are painted, from how large they will actually
// come out on screen.
//
// This used to be a rule about *density*: under about twenty-five nodes every name
// was painted, above it only the applications, because a few hundred names on one
// canvas is a wall of text with circles behind it. That rule was solving the wrong
// problem. Names collided because the graph was compressed into the viewport, so
// there was no room between nodes for them — and the count was standing in for the
// crowding it caused.
//
// With the graph laid out in a world of its own size there is always room beside a
// node for its name, in world units. What decides whether it can be *read* is how
// much of the screen a world unit gets, which is the zoom. So the question becomes
// the honest one: at this magnification, is this text large enough to read? A name
// is painted when it is, and it is not painted when it would be a smear — which is
// what a zoomed-out universe should look like, and why zooming in is how you read
// it.
//
// Applications cross the threshold first. They are the largest things on screen and
// carry the largest names, and they are what somebody navigates by: "where is
// Billing" is the first question asked of this view, and it has to be answerable
// before the detail is.
//
// Everything else still shows its name on hover and on keyboard focus, which the
// stylesheet does with no re-render, and the selected node keeps it while selected.
const LABEL_TIERS = {
  // The font sizes, in world units, that the stylesheet paints each tier at. They
  // are sized against the circles they belong to — a process is 17 units across the
  // radius, an application 30 — so a name stays proportionate to its node at every
  // magnification instead of swelling or shrinking relative to it.
  all: 15,
  anchors: 26,
  // readable is the smallest rendered text worth painting, in screen pixels. Below
  // it a name is not small, it is noise sitting on top of the structure the picture
  // is carrying.
  readable: 7,
};

// labelTier reports which names a given magnification can carry. scale is screen
// pixels per world unit.
export function labelTier(scale) {
  if (LABEL_TIERS.all * scale >= LABEL_TIERS.readable) return "all";
  if (LABEL_TIERS.anchors * scale >= LABEL_TIERS.readable) return "anchors";
  return "none";
}

// depthOf reads a typed depth as a number of hops.
//
// A field somebody is mid-edit in holds anything: empty, a minus sign, "1e9". The
// floor is one hop rather than zero because a walk of zero is a picture of one node
// with nothing drawn around it — a broken answer rather than a narrow one — and the
// ceiling is there because the walk is bounded by the graph long before 99 anyway,
// so a larger number is a typo rather than a question.
export function depthOf(value) {
  if (value === "all" || value === Infinity) return Infinity;
  const n = Math.floor(Number(value));
  if (!Number.isFinite(n)) return 1;
  return Math.min(Math.max(n, 1), 99);
}

// mulberry32 is a small seeded PRNG. The seed is fixed so the initial scatter —
// and therefore the settled layout — is identical on every load of the same graph.
function mulberry32(seed) {
  return function () {
    seed |= 0; seed = (seed + 0x6D2B79F5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// NODE_ROOM is the personal space a node needs beyond its own circle: enough for
// its name and a gap to the next one. It is what makes the difference between a
// graph that is technically non-overlapping and one somebody can read.
const NODE_ROOM = 34;

// WORLD_FILL is how much of the world the nodes' own cells take up.
//
// It is what decides between a pile and a void, and it was tuned by measuring
// rather than by taste. The thing to hold on to is that it trades air against
// magnification and nothing else: the opening view fits the whole world into the
// canvas, so a roomier world is shown at a smaller scale, and the nodes and their
// names give up exactly what the gaps between them gain. Far in either direction
// costs the picture — at 0.09 a hundred nodes settle with far more space than
// reading needs and the landscape is too small on screen to carry any names at all;
// well above 0.3 the names are large and sit on each other.
//
// 0.22 rather than the 0.28 it was, because the landscape dropped the centred
// content column (see .landscape-mode in app.css) and the width that freed had to
// be spent on one or the other. Measured on the 42-node fixture the e2e suite uses,
// at a 1400px window: in the old column, nodes 6.4-14.8px in the radius with 56px
// between the average nearest pair. Widened at 0.28 they are 7.7-18.0px with 62px —
// the same picture, magnified. Widened at 0.22 they are 6.5-15.2px, the size they
// already were, with 72px. So the reclaimed width goes into the space between
// things rather than into making them bigger, which is what a landscape that is
// hard to take in at a glance actually needs.
const WORLD_FILL = 0.22;

// radiusOf is the radius a node is actually drawn at. renderGraph sizes every node
// once, from its connectivity, and everything downstream — the world budget, the
// separation pass, the circle itself — asks here rather than re-deriving it, so
// they cannot disagree about how big a node is.
function radiusOf(node) {
  return node.r ?? (KIND[node.kind] || KIND.process).r;
}

// worldFor sizes the space the graph is laid out in, from the graph rather than
// from the viewport.
//
// This is the correction that matters. The layout used to settle inside the frame
// and then be scaled to fill it — and fitToFrame scales *positions* while radii
// stay fixed, so any graph whose settled extent exceeded the frame was compressed
// into it with its circles left at full size. That is arithmetic that guarantees
// overlap, and it got worse with every node added, which is exactly how a landscape
// ends up as a knot of interpenetrating bubbles.
//
// So the world grows with the content instead. The frame is a window onto it, the
// opening view shows the whole thing, and reading it closely is what the zoom is
// for.
//
// It grows with the content and *only* with the content. There used to be a floor
// here — a small graph got at least a frame's worth of world — on the reasoning that
// the handful-of-nodes case was already comfortable and did not need changing. It
// was the single biggest thing wrong with the opening view, and the arithmetic says
// why. The world is shown at whatever scale fits it into the canvas, so a world
// bigger than its content needs is a picture drawn smaller than it had to be, and
// the floor is exactly that: with cells of about 98 units square, the floor stopped
// binding only somewhere past twenty-five nodes, and everything below it was laid
// out in a world several times too large. Measured at 1400x820, as the share of the
// canvas the nodes' own footprints cover:
//
//	nodes         4    8    8   13   15   24   40   47  122
//	with floor   3%   6%   6%  10%  12%  17%  18%  17%  17%
//	without     17%  17%  18%  17%  17%  17%  18%  17%  17%
//
// The bottom row is what WORLD_FILL asks for, at every size. The row above it opens
// at 3%: a four-node landscape drawn as four small circles adrift in an empty canvas,
// which is what a small estate actually looked like, and what "the window is not
// being used" meant. The floor's own defence — that a small graph was comfortable —
// was never measured against the large graphs it was being compared to.
function worldFor(nodes, frame) {
  let cells = 0;
  for (const n of nodes) {
    // The node's own radius, not its kind's floor: connectivity has already sized
    // it, and a world budgeted from the floor would be too small for the hubs.
    const cell = 2 * (radiusOf(n) + NODE_ROOM);
    cells += cell * cell;
  }
  const aspect = Math.max(frame.width, 1) / Math.max(frame.height, 1);
  // One density law for every estate size: the cells the nodes need, at the fill
  // WORLD_FILL asks for, in the shape of the frame it will be shown in.
  const width = Math.sqrt((cells / WORLD_FILL) * aspect);
  return { width, height: width / aspect };
}

// separate pushes overlapping circles apart until none intersect, in whatever
// coordinates it is handed.
//
// It runs *after* the fit as well as inside the settle, and that is the point: the
// settle's guarantee is made in layout coordinates, and a rescale carries positions
// across while radii stay behind. Re-establishing it where the circles are actually
// drawn is the only place the guarantee means anything.
function separate(nodes, radii, gap, rounds = 24) {
  for (let round = 0; round < rounds; round++) {
    let moved = false;
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const a = nodes[i], b = nodes[j];
        let dx = a.x - b.x, dy = a.y - b.y;
        let d = Math.hypot(dx, dy);
        if (d < 0.01) { dx = 0.1; dy = 0.1; d = 0.1414; }
        const room = radii[i] + radii[j] + gap;
        if (d >= room) continue;
        // A node somebody is holding does not move: the other one gets out of its
        // way instead. Splitting the push evenly would slide a held node out from
        // under the pointer, which is the one thing a drag must never do.
        const [pushA, pushB] = share(room - d, a.held, b.held);
        a.x += (dx / d) * pushA; a.y += (dy / d) * pushA;
        b.x -= (dx / d) * pushB; b.y -= (dy / d) * pushB;
        moved = true;
      }
    }
    if (!moved) return;
  }
}

// GATHER_REACH is how far a node may be drawn from its nearest neighbour, as a
// multiple of how far the median node is from its own.
//
// The ratio is not a new idea about what "too far out" means: the table at
// LOOSE_PULL is that exact quantity, and it already reads a worst case of about 1.6
// as good and worst cases of 2.1 and 3.1 as bad. What is new is that it is enforced
// rather than hoped for, and where the line falls was measured against pictures
// rather than reasoned about. On a hub of twelve processes with one unattached one —
// which is the estate shape this was reported on — the straggler settles at 2.07,
// so a ceiling of 2 leaves it exactly where it was and the report stands. At 1.5 it
// comes in, and the canvas it was holding open goes back into the picture. Below
// that the returns stop: 1.3 moves it a little further for no visible difference.
//
// The ceiling costs nothing elsewhere. Across twenty-three estate shapes at four
// window sizes, imposing it leaves the average share of the canvas the picture spans
// exactly where it was, at 0.692, and takes the closest pair in the whole set from
// 33 units of clear space to 41. On an estate that is mostly unattached nodes — six
// applications and forty loose processes — the picture is indistinguishable either
// way, because there the loose nodes are each other's neighbours already and nothing
// is over the ceiling to begin with.
const GATHER_REACH = 1.5;

// gather is the dual of separate: separate puts a floor under how close two nodes
// may be drawn, gather puts a ceiling on how far one *piece* of the picture may
// drift from the rest of it.
//
// It exists because the forces cannot promise this and the framing cannot survive
// without it. A piece the springs do not tie to anything else — a node with no
// edges, or a small cluster joined only to itself — sits where the centring pull
// balances a repulsion falling off as 1/d², and that balance is a cube root of the
// constants: it lands far out, and tuning the pull moves it by very little
// (LOOSE_PULL is that tuning, and the worst case in its own table is still 3.1× the
// median). What happens next is the expensive part. fitToFrame scales the *bounding
// box* onto the world, so a piece a long way out is not merely a piece a long way
// out — it is the thing that decides the scale, and everything else is squeezed into
// the fraction of the canvas it leaves. That is the picture this was reported as
// twice: a mass in part of the window, stragglers against the far edges, and most of
// the canvas empty.
//
// So it is bounded here, deterministically, in the same place and for the same
// reason separate is: a guarantee the simulation cannot make is made afterwards, by
// arithmetic.
//
// **The piece, not the node.** The first version of this measured each node against
// its own nearest neighbour, which catches a lone node and nothing else: two
// processes that call each other and nothing else are each other's nearest
// neighbour at a spring's rest length, so by that measure neither was far from
// anything, and the pair sailed past the ceiling together. On a real estate that is
// not the rare case — a landscape is full of test processes, conformance samples and
// one-off flows that touch nothing else — and it was those pairs and triples, not
// lone nodes, that were still holding the canvas open. So the unit is the connected
// component: everything the edges tie together is one piece, and a piece is measured
// against everything outside it.
//
// A piece over the ceiling is translated *rigidly* toward whatever is nearest to it,
// until the gap is exactly the ceiling and no further. Rigidly, because every
// distance inside a component is something the layout is saying — the springs put it
// there — and a pass that squeezed a component would be editing the picture's
// content rather than its placement. Between components there are no edges and so
// nothing was being said: the gap is an artifact of where the repulsion and the pull
// happened to balance, which is exactly the quantity that may be overruled. The
// piece stays the outlying thing it is, on the side of the picture it settled on,
// and stops being the thing that sets the scale for everybody else.
//
// The largest component never moves. It is the mass the rest is measured against,
// and something has to hold still or the pass chases itself.
//
// The reach is the picture's own median nearest-neighbour distance rather than a
// number, so it carries across every world size and every estate: it says "further
// out than this landscape's own spacing warrants", which is a statement about the
// landscape and not about pixels. The median is taken once, before anything moves,
// so the pass cannot chase its own tail.
//
// Held nodes do not move — somebody put them there — so a component containing one
// is left alone, and the whole pass is skipped while anything is pinned, exactly as
// the fit is, because both would slide a hand-made arrangement out from under the
// hand that made it.
function gather(nodes, links, radii, rounds = 4) {
  // Below three nodes there is no median to speak of and nothing to be an outlier
  // from: two nodes are each other's nearest neighbour whatever the distance.
  if (nodes.length < 3) return nodes;
  const sorted = nearestOf(nodes).map((n) => n.d).sort((a, b) => a - b);
  const median = sorted[Math.floor(sorted.length / 2)];
  if (!(median > 0)) return nodes;
  const reach = GATHER_REACH * median;
  gatherPieces(nodes, links, radii, reach, rounds);
  gatherNodes(nodes, radii, reach, rounds);
  return nodes;
}

// gatherPieces brings in whole components — the coarse half of the ceiling, and the
// half a per-node rule cannot do.
function gatherPieces(nodes, links, radii, reach, rounds) {
  const piece = piecesOf(nodes.length, links);
  // The mass: the piece with the most nodes in it. Ties go to the one whose first
  // node comes first, so the same graph anchors on the same piece every time.
  const count = new Map();
  for (const p of piece) count.set(p, (count.get(p) || 0) + 1);
  let mass = piece[0];
  for (const [p, n] of count) if (n > count.get(mass)) mass = p;
  if (count.get(mass) === nodes.length) return nodes;

  // A piece somebody is holding by one of its nodes is not moved: the drag put it
  // where it is, and dragging one node of a pair must not teleport the pair.
  const pinned = new Set();
  for (let i = 0; i < nodes.length; i++) if (nodes[i].held) pinned.add(piece[i]);

  for (let round = 0; round < rounds; round++) {
    // The nearest thing outside each piece, re-measured every round: moving one
    // piece in changes what the next one is nearest to, and can bring a piece that
    // was over the ceiling under it without touching it.
    const out = new Map();
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        if (piece[i] === piece[j]) continue;
        const d = Math.hypot(nodes[i].x - nodes[j].x, nodes[i].y - nodes[j].y);
        const room = radii[i] + radii[j] + NODE_ROOM;
        const a = out.get(piece[i]), b = out.get(piece[j]);
        if (!a || d < a.d) out.set(piece[i], { d, from: i, to: j, room });
        if (!b || d < b.d) out.set(piece[j], { d, from: j, to: i, room });
      }
    }
    let moved = false;
    for (const [p, near] of out) {
      if (p === mass || pinned.has(p)) continue;
      // Never closer than the separation pass is about to insist on anyway, or the
      // two would be pushed apart again and the round would have been wasted.
      const want = Math.max(reach, near.room);
      if (near.d <= want) continue;
      const step = (near.d - want) / near.d;
      const dx = (nodes[near.to].x - nodes[near.from].x) * step;
      const dy = (nodes[near.to].y - nodes[near.from].y) * step;
      for (let i = 0; i < nodes.length; i++) {
        if (piece[i] !== p) continue;
        nodes[i].x += dx;
        nodes[i].y += dy;
      }
      moved = true;
    }
    if (!moved) return nodes;
  }
  return nodes;
}

// gatherNodes is the fine half: one node at a time, against whatever is nearest to
// it, wherever that is.
//
// It is not made redundant by the piece pass and does not make it redundant. A piece
// is measured against what is outside it, so a node stretched away from its own
// neighbours *inside* a large component — a long call chain the springs did not pull
// back in — is invisible to it; and a node is measured against its nearest
// neighbour, so a pair adrift together is invisible to a per-node rule. Measured on
// a 120-node estate with six small islands, dropping this half took the worst node's
// distance from 1.5 times the median to 1.9. Both halves, or neither property holds.
//
// This one moves a node rather than a piece, and that does shorten whatever edge it
// was stretched along — deliberately, and only past the ceiling, where the length
// had stopped being a reading of the graph and started being a hole in the picture.
function gatherNodes(nodes, radii, reach, rounds) {
  for (let round = 0; round < rounds; round++) {
    // Re-measured each round: pulling one node in can make it somebody else's
    // nearest neighbour, and can leave whoever it was furthest from on its own.
    const at = nearestOf(nodes);
    let moved = false;
    for (let i = 0; i < nodes.length; i++) {
      const n = nodes[i], j = at[i].at;
      if (n.held || j < 0) continue;
      const target = nodes[j];
      // Never closer than the separation pass is about to insist on anyway, or the
      // two would be pushed apart again and the round would have been wasted.
      const want = Math.max(reach, radii[i] + radii[j] + NODE_ROOM);
      if (at[i].d <= want) continue;
      const step = (at[i].d - want) / at[i].d;
      n.x += (target.x - n.x) * step;
      n.y += (target.y - n.y) * step;
      moved = true;
    }
    if (!moved) return nodes;
  }
  return nodes;
}

// piecesOf labels every node with the component it belongs to, as the index of one
// representative node. Union-find over the edges, so a chain of a hundred calls is
// one piece for the same cost as a pair.
function piecesOf(count, links) {
  const parent = new Int32Array(count);
  for (let i = 0; i < count; i++) parent[i] = i;
  const root = (i) => { while (parent[i] !== i) { parent[i] = parent[parent[i]]; i = parent[i]; } return i; };
  for (const [a, b] of links) {
    const ra = root(a), rb = root(b);
    if (ra !== rb) parent[ra] = rb;
  }
  const out = new Int32Array(count);
  for (let i = 0; i < count; i++) out[i] = root(i);
  return out;
}

// nearestOf reports, for every node, which node is closest to it and how far away
// that is. One pass over the pairs, so both the median and the offenders come out of
// the same arithmetic.
function nearestOf(nodes) {
  const out = nodes.map(() => ({ at: -1, d: Infinity }));
  for (let i = 0; i < nodes.length; i++) {
    for (let j = i + 1; j < nodes.length; j++) {
      const d = Math.hypot(nodes[i].x - nodes[j].x, nodes[i].y - nodes[j].y);
      if (d < out[i].d) { out[i].d = d; out[i].at = j; }
      if (d < out[j].d) { out[j].d = d; out[j].at = i; }
    }
  }
  return out;
}

// share splits an overlap between two nodes, giving the whole of it to whichever
// one is free to move. Both held is a standoff: neither moves, and the arrangement
// somebody placed by hand is left exactly as they placed it.
function share(overlap, aHeld, bHeld) {
  if (aHeld && bHeld) return [0, 0];
  if (aHeld) return [0, overlap];
  if (bHeld) return [overlap, 0];
  return [overlap / 2, overlap / 2];
}

// LOOSE_PULL is how much harder the centring pull works on a node with no edges.
//
// The pull is anisotropic — weaker along the wider axis, so the graph takes the shape
// of the frame — and that shape is decided for a node the springs are also holding.
// A node with no edge has no springs: the pull is the whole of what keeps it near the
// picture, and it balances against a repulsion that falls off as 1/d². Measured on a
// 34-node estate with ten unattached processes at 1400x900, the balance put two of
// them hard against the left and right edges of an otherwise centred picture, with
// everything else squeezed into the middle — the frame was "filled" by two stragglers
// rather than by the content, which is why the fill test never saw it.
//
// The number is measured rather than reasoned. Across five estate shapes — from six
// nodes to a hundred and nineteen, from one loose node to eighty-three — this is the
// worst node's distance to its nearest neighbour, as a multiple of the median:
//
//	              1×     2×     3×     4×     8×
//	1 app + 1     1.60   1.32   1.21   1.15   1.04
//	2 apps + 3    1.28   1.03   1.08   1.06   1.05
//	4 apps + 10   3.11   1.10   1.22   1.39   1.44
//	6 apps + 83   1.32   1.56   1.71   1.94   2.56
//	1 app + 40    2.14   1.19   1.18   1.67   2.18
//
// Two is the only column with no bad case in it. Higher is not better and the table
// says why: past it the loose nodes stop being spread through the picture and collapse
// into a lump of their own in the middle, with the applications pushed out around it —
// the same defect mirrored. The pull that holds a straggler in is not the pull that
// packs a crowd.
const LOOSE_PULL = 2;

// forcesFor derives the constants the simulation runs on from the world it runs in.
//
// Everything scales with the world rather than being a fixed number, because a
// graph settled at a fixed spring length inside a world sized for its content is a
// knot in the middle of an empty field — and enlarging the knot is not the same as
// spreading it out. Repulsion also scales with the node count so density stays
// roughly constant instead of rising with it: the "Klüngel" a fixed constant
// produces, where fifty nodes are comfortable and three hundred are one dark blob.
function forcesFor(nodes, width, height) {
  const reach = Math.min(width, height);
  // The frame is usually wider than it is tall. Both the initial scatter and the
  // centring pull are shaped by that ratio, so the settled graph is the shape of
  // the space it has to live in.
  const aspect = Math.max(width, 1) / Math.max(height, 1);
  return {
    cx: width / 2, cy: height / 2,
    pullX: 1 / aspect, pullY: aspect,
    repulsion: 5200 * Math.max(1, Math.sqrt(nodes.length / 40)) * Math.max(1, reach / 720),
    spring: 0.012,
    damping: 0.85,
    rest: Math.max(130, reach * 0.16),
  };
}

// settle runs the simulation over nodes that already have positions, for as many
// steps as it is given. It is the whole of the physics, and it is a function of its
// own so that a drag can run a few steps of exactly the same thing the initial
// layout runs two hundred of — a graph that settled one way while being dragged and
// another way on the next paint would be two layouts wearing one name.
//
// A node marked `held` is not simulated: its position is whatever put it there, and
// everything else arranges itself around it.
function settle(nodes, links, radii, force, iterations) {
  // Which nodes have an edge at all. A sprung node is held in place by its springs,
  // which are ten times the centring pull; a node with no edge is held by the pull
  // alone, and the pull was tuned for nodes that also have springs.
  const linked = new Uint8Array(nodes.length);
  for (const [a, b] of links) { linked[a] = 1; linked[b] = 1; }
  for (let step = 0; step < iterations; step++) {
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const a = nodes[i], b = nodes[j];
        let dx = a.x - b.x, dy = a.y - b.y;
        let d2 = dx * dx + dy * dy;
        if (d2 < 0.01) { dx = 0.1; dy = 0.1; d2 = 0.02; }
        const magnitude = force.repulsion / d2;
        const d = Math.sqrt(d2);
        const fx = (dx / d) * magnitude, fy = (dy / d) * magnitude;
        a.vx += fx; a.vy += fy; b.vx -= fx; b.vy -= fy;

        // Separation. Repulsion alone is a soft force that a spring can overpower,
        // so two nodes joined by an edge will happily sit on top of each other —
        // which is the one arrangement that makes a picture unreadable rather than
        // merely tight. This pushes overlapping circles apart directly, and it is
        // in the same pass because that pass already visits every pair.
        const room = radii[i] + radii[j] + NODE_ROOM;
        if (d < room) {
          const [pushA, pushB] = share((room - d) * 0.5, a.held, b.held);
          a.x += (dx / d) * pushA; a.y += (dy / d) * pushA;
          b.x -= (dx / d) * pushB; b.y -= (dy / d) * pushB;
        }
      }
    }
    for (const [ai, bi] of links) {
      const a = nodes[ai], b = nodes[bi];
      const dx = b.x - a.x, dy = b.y - a.y;
      const d = Math.hypot(dx, dy) || 0.01;
      const magnitude = (d - force.rest) * force.spring;
      const fx = (dx / d) * magnitude, fy = (dy / d) * magnitude;
      a.vx += fx; a.vy += fy; b.vx -= fx; b.vy -= fy;
    }
    for (let i = 0; i < nodes.length; i++) {
      const n = nodes[i];
      // A held node keeps its place and its stillness: carrying velocity through a
      // drag would make it spring away the moment it was let go.
      if (n.held) { n.vx = 0; n.vy = 0; continue; }
      const hold = linked[i] ? 1 : LOOSE_PULL;
      // The pull toward the centre is anisotropic, weaker along the wider axis, so
      // the graph settles into the shape of the frame instead of into a disc. A disc
      // in a wide viewport is what produced the empty bands on either side: the
      // content was never the shape of the space it had.
      n.vx += (force.cx - n.x) * 0.0012 * force.pullX * hold;
      n.vy += (force.cy - n.y) * 0.0012 * force.pullY * hold;
      n.vx *= force.damping; n.vy *= force.damping;
      n.x += n.vx; n.y += n.vy;
    }
  }
}

// linksAmong resolves edges to index pairs, dropping any whose ends are not on
// screen — a filtered graph carries edges to nodes it no longer contains.
function linksAmong(nodes, edges) {
  const index = new Map(nodes.map((n, i) => [n.id, i]));
  return edges
    .map((e) => [index.get(e.from), index.get(e.to)])
    .filter(([a, b]) => a !== undefined && b !== undefined);
}

// tethersFor records the edges that a drag will actually move, and how long each of
// them is right now.
//
// Recording the *current* length rather than the layout's rest length is the whole
// trick. A settled graph has been fitted since it was simulated, and the fit
// rescales every distance — so its edges are not at the simulation's rest length,
// and a spring aiming for that length would haul the neighbourhood inward the
// moment somebody touched a node, without them having moved it anywhere. Aiming for
// the length the edge has at the instant of the grab means nothing moves until the
// node does, and then only in proportion to how far it went.
//
// Only edges touching a held node are recorded. Those are the ones whose geometry
// is about to change; everything else must stay exactly where the reader last saw
// it, and moves only if something ends up on top of it.
function tethersFor(nodes, edges) {
  const index = new Map(nodes.map((n, i) => [n.id, i]));
  const out = [];
  for (const e of edges) {
    const a = index.get(e.from), b = index.get(e.to);
    if (a === undefined || b === undefined) continue;
    if (!nodes[a].held && !nodes[b].held) continue;
    out.push([a, b, Math.hypot(nodes[a].x - nodes[b].x, nodes[a].y - nodes[b].y)]);
  }
  return out;
}

// follow moves the neighbours of whatever is being held, and gets everything else
// out of the way. This is what a drag calls on every frame.
//
// Deliberately local, and deliberately not the layout's own physics. Resuming the
// full simulation from a settled picture reorganises the whole landscape the moment
// a node is touched — the picture on screen has been fitted since it was simulated,
// so it is not at the simulation's equilibrium and restarting it there is a second
// layout wearing the first one's coordinates. What somebody dragging a node is
// asking for is smaller and more useful than that: the things joined to it come
// along, and whatever they run into moves aside.
function follow(nodes, tethers, radii, { steps = 3 } = {}) {
  for (let step = 0; step < steps; step++) {
    for (const [ai, bi, rest] of tethers) {
      const a = nodes[ai], b = nodes[bi];
      const dx = b.x - a.x, dy = b.y - a.y;
      const d = Math.hypot(dx, dy) || 0.01;
      // Toward the length the edge had when it was grabbed, a fraction at a time, so
      // a neighbour trails the node it is joined to instead of being welded to it.
      const [moveA, moveB] = share((d - rest) * 0.28, a.held, b.held);
      a.x += (dx / d) * moveA; a.y += (dy / d) * moveA;
      b.x -= (dx / d) * moveB; b.y -= (dy / d) * moveB;
    }
    // Two rounds rather than one pass to convergence: a drag is continuous, so each
    // frame only has to move the picture a little way toward being untangled, and
    // the next frame carries on from there.
    separate(nodes, radii, NODE_ROOM, 2);
  }
  return nodes;
}

// layout settles the graph with repulsion between every pair, springs along edges,
// and a pull toward the centre. Returns the elapsed milliseconds so the caller can
// report what the budget actually costs on this machine.
//
// `pinned` is the map of nodes somebody has dragged somewhere. It changes two
// things, and both are consequences of one rule — a hand-placed node stays where it
// was placed:
//
//   - Pinned nodes are held throughout, so the simulation arranges the rest of the
//     landscape around them instead of pulling them back.
//   - The fit is skipped while anything is pinned, because fitting rescales every
//     position and would slide the pins off the spots they were dropped on. So the
//     picture stops re-framing itself once you start arranging it by hand, which is
//     the trade: your arrangement is worth more than the last few percent of margin.
//
// `from` is where the nodes already are, so a repaint continues the picture on
// screen rather than re-deriving one around the pins. Without it, filtering after a
// drag would keep the pinned nodes and re-scatter everything else — the arrangement
// would survive and its context would not, which is the worse half of both.
function layout(nodes, edges, { width, height, iterations = 220, pinned, from, margin = LABEL_MARGIN } = {}) {
  const started = performance.now();
  const random = mulberry32(0x5EED);
  const force = forcesFor(nodes, width, height);
  // Anchored by the pins that are actually on screen: a pin on something a filter
  // removed must not stop the rest of the picture from being fitted.
  const anchored = Boolean(pinned) && nodes.some((n) => pinned.has(n.id));

  for (const n of nodes) {
    const pin = pinned?.get(n.id);
    const was = from?.get(n.id);
    if (pin) {
      // Wherever it was put, world or no world. The clamp that used to be here was
      // the same argument place() made — a pin outside the world is somewhere the
      // fitted view never shows — and it stopped holding when the fit began framing
      // the content: a pin beyond the edge is inside the next Fit, and a resize that
      // shrinks the world no longer drags an arrangement back through it.
      n.x = pin.x;
      n.y = pin.y;
      n.held = true;
    } else if (anchored && was) {
      n.x = was.x; n.y = was.y;
    } else {
      // Seeded scatter on an ellipse, so nothing starts coincident (which would make
      // the repulsion term divide by zero and fling nodes to infinity).
      const angle = random() * Math.PI * 2;
      const radius = 0.15 + random() * 0.85;
      n.x = force.cx + Math.cos(angle) * radius * width * 0.42;
      n.y = force.cy + Math.sin(angle) * radius * height * 0.42;
    }
    n.vx = 0; n.vy = 0;
  }

  // Every node's own footprint, so the separation pass knows what "touching" means
  // for this pair rather than assuming one radius for all of them.
  const radii = nodes.map(radiusOf);
  const links = linksAmong(nodes, edges);
  // Settled in two stretches, with a look at the result in between.
  //
  // The centring pull is anisotropic so the graph takes the shape of the space it
  // has to live in (see forcesFor), and the fit that follows scales both axes by one
  // factor — so whatever shape the settle lands on is the shape that gets framed,
  // and any difference from the canvas's own shape is left over as a band of empty
  // canvas along one edge. Measured on a 36-node estate: the settle came out at 2.8:1
  // whatever the canvas was, so at 1400x900 it filled 93% of the width and 55% of the
  // height, and the nodes were crowded into that half with eight of their names
  // overlapping. Nothing was wrong with the framing; the picture was simply not the
  // shape of the frame it was being fitted into.
  //
  // The pull is what decides that shape, and it cannot be set in advance: the shape
  // it produces depends on the graph as well — how much of it is one hub's spokes,
  // how long the chains are. So it is aimed rather than assumed. Most of the run
  // settles the graph, then the shape it reached is measured against the shape it is
  // for, and the rest of the run is spent under a pull corrected by the difference.
  // The correction is free — the loop was going to run those steps anyway — and it is
  // bounded, because a graph that genuinely wants to be a line (a chain of twelve
  // processes) must not be crushed into a square to fill a frame.
  const aimed = Math.max(1, Math.round(iterations * 0.6));
  settle(nodes, links, radii, force, aimed);
  if (nodes.length > 2) {
    const box = contentBox(nodes, 0);
    const has = box.width / Math.max(box.height, 1);
    const wants = Math.max(width, 1) / Math.max(height, 1);
    const off = Math.min(Math.max(has / wants, 0.4), 2.5);
    const correction = Math.sqrt(off);
    force.pullX *= correction;
    force.pullY /= correction;
  }
  settle(nodes, links, radii, force, iterations - aimed);
  // Nothing may be left stranded off the side of the picture before it is framed,
  // because the framing is what turns one stranded node into an empty canvas.
  if (!anchored) gather(nodes, links, radii);
  if (!anchored) fitToFrame(nodes, width, height, margin);
  // And once more where the circles are actually drawn. The fit scales positions
  // and leaves radii alone, so whatever the settle guaranteed is only true again
  // after this. Anything it moves outside the world is pulled back by the re-fit.
  separate(nodes, radii, NODE_ROOM);
  if (!anchored) fitToFrame(nodes, width, height, margin);
  return performance.now() - started;
}

// LABEL_MARGIN is the room a node needs around its own centre. It is asymmetric
// because a node's label is: the text hangs below the circle (dy = r + 14) and is
// centred, so the bottom carries more than the top does.
//
// The vertical figures are the largest node's own arithmetic rather than a guess.
// fitToFrame places *centres* inside this margin, so anything the margin does not
// cover leaves the frame: the biggest application is drawn at r = 42 (KIND.r 30 plus
// its full growth), and its name — 26px, baseline at r + 14 — reaches about 62 below
// that centre. At the 26 and 42 these were, a node landing on the top edge could put
// 16 units of its own outline outside the frame and one on the bottom edge 20 units
// of its name; 46 and 68 are those two numbers with a little room over.
//
// (An earlier note here blamed the margin for an export whose lowest names were drawn
// across the provenance stamp. That was a different bug — the file laid the picture
// out against the whole page rather than against its band, see standaloneSVG — and
// the margin is only what the arithmetic above says it is.)
//
// The sides stay under the widest name on purpose. A long name is centred, so
// covering it would mean reserving half a label on both edges — the picture would
// shrink by more than the overhang costs, and a name overhanging into empty canvas
// is legible where a smaller picture is not.
const LABEL_MARGIN = { top: 46, right: 46, bottom: 68, left: 46 };

// fitToFrame maps the settled graph onto the frame so it fills it, leaving only the
// margin a label needs. The scale is uniform: stretching the axes independently
// would fill the last pixel of the frame and misreport distance, and distance is the
// one thing a force layout is trying to say.
//
// Positions are scaled, radii are not, so nodes stay round and legible at any graph
// size. A single node — or a set that settled on one line — has no extent on some
// axis, so that axis falls back to the frame rather than dividing by zero.
export function fitToFrame(nodes, width, height, pad = LABEL_MARGIN) {
  if (!nodes.length) return nodes;
  // The margin is what a node's own decoration needs, not decoration itself: a label
  // hangs below its circle, so the bottom needs more than the top, and a long name is
  // centred so it overhangs sideways. Sizing each side to what it actually carries is
  // what turns the leftover space back into picture.
  const m = typeof pad === "number" ? { top: pad, right: pad, bottom: pad, left: pad } : pad;
  const xs = nodes.map((n) => n.x), ys = nodes.map((n) => n.y);
  const minX = Math.min(...xs), maxX = Math.max(...xs);
  const minY = Math.min(...ys), maxY = Math.max(...ys);
  const spanX = maxX - minX, spanY = maxY - minY;
  const usableW = Math.max(width - m.left - m.right, 1);
  const usableH = Math.max(height - m.top - m.bottom, 1);
  const scale = Math.min(
    spanX > 0.001 ? usableW / spanX : Infinity,
    spanY > 0.001 ? usableH / spanY : Infinity,
  );
  // Everything coincident: nothing to scale, so centre it and stop.
  const k = Number.isFinite(scale) ? scale : 1;
  const offsetX = m.left + (usableW - spanX * k) / 2 - minX * k;
  const offsetY = m.top + (usableH - spanY * k) / 2 - minY * k;
  for (const n of nodes) {
    n.x = n.x * k + offsetX;
    n.y = n.y * k + offsetY;
  }
  return nodes;
}

// CAPTION_SPOTS is where a node's name may be written, in the order it is preferred.
//
// Under the circle first, because that is where it has always been and a picture
// whose names all sit in one place is the one that reads fastest. Above next, then
// beside — the sides last because a name beside a node overhangs by its whole width
// where one under it overhangs by half, and the fit reserves the same margin either
// way (see LABEL_MARGIN).
const CAPTION_SPOTS = ["under", "over", "right", "left"];

// captionSpot is where one caption lands for a given choice, as an offset from where
// it was drawn. `box` is the caption's own bounds relative to its node's centre, as
// the browser measured them, so this is arithmetic on a real box rather than on an
// estimate of how wide a name might be.
function captionSpot(spot, box, room) {
  switch (spot) {
    // Its bottom just above the circle instead of its top just below it.
    case "over": return { dx: 0, dy: -room - (box.y + box.height) };
    // Left edge clear of the circle, and centred on it rather than hanging under it.
    case "right": return { dx: room - box.x, dy: -(box.y + box.height / 2) };
    case "left": return { dx: -room - (box.x + box.width), dy: -(box.y + box.height / 2) };
    default: return { dx: 0, dy: 0 };
  }
}

// HALO is the ring of surface colour painted behind a name so it stays readable
// where it crosses a line (see .mesh-label-ink). It is drawn in world units like the
// text itself, and it is what a reader sees, so the placement counts it as part of
// the name rather than measuring the letters alone.
const HALO = 3;

const hits = (a, b) => a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom;

// placeCaptions decides where each node's name is written, and which names are not
// written at all.
//
// The alternative was to make the layout itself keep names apart — to give every node
// personal space as wide as its name. That trade is a bad one and the numbers say so:
// names here run to 250 world units against node radii of 6 to 18, so the world would
// grow by about an order of magnitude in area, and since the opening view fits the
// whole world onto the canvas, every node and every name would be drawn that much
// smaller. It answers "the names overlap" with "the names are too small to read",
// which is the same complaint one step further on.
//
// So the graph is left exactly where it settled and the names move instead. Each one
// is offered four places around its own node and takes the first that is free;
// biggest node first, because a hub's name is worth more than a leaf's and it is the
// one a reader is navigating by. A name with nowhere to go is not written — it comes
// back on hover, on focus, on selection and when the view is moved in, which the
// stylesheet already does for every name the zoom has taken away.
//
// The circles are obstacles too, not only the other names: a name written across a
// node is no more readable than a name written across a name, and it also lies about
// which node it belongs to.
//
// It is all in world units, which is what makes it worth doing once. A caption's size
// is in world units as much as its position is — the text scales with the view — so
// two names that clear each other here clear each other at every magnification, and
// panning and zooming need no re-placement at all.
//
// Deterministic: same graph, same layout, same names in the same places. Ties are
// broken by id rather than by array order, so a repaint cannot rearrange the names
// while the picture underneath it stands still.
export function placeCaptions(items) {
  // Under the node is where a name belongs, and it is given up only to another name.
  //
  // The two kinds of collision do not cost the same. A name lying across another name
  // destroys both of them and looks like a rendering fault. A name crossing a circle
  // is still readable — the ink is painted with a halo behind it for exactly that
  // reason — and costs only a little clarity about which node it belongs to. Treating
  // them alike is worse than either: measured on a 36-node estate it moved 31 of 36
  // names off their nodes and left six unwritten, for a picture where a reader has to
  // work out every association, instead of moving six and leaving one.
  //
  // So a name moves when another name is in the way, and not otherwise. Among the
  // places it can move to, one clear of the circles wins.
  const circles = items.map((it) => ({
    left: it.x - it.r, right: it.x + it.r, top: it.y - it.r, bottom: it.y + it.r,
  }));
  const written = [];
  const spots = new Map();
  const order = [...items].sort((a, b) => b.r - a.r || String(a.id).localeCompare(String(b.id)));
  for (const it of order) {
    if (!it.box || !it.box.width) continue;
    const free = CAPTION_SPOTS.map((spot) => {
      const at = captionSpot(spot, it.box, it.r + 8);
      return {
        spot,
        at,
        // The halo the ink is painted with is part of what a reader sees, so it is
        // part of what has to fit: getBBox reports the letters alone.
        rect: {
          left: it.x + it.box.x + at.dx - HALO, top: it.y + it.box.y + at.dy - HALO,
          right: it.x + it.box.x + at.dx + it.box.width + HALO,
          bottom: it.y + it.box.y + at.dy + it.box.height + HALO,
        },
      };
    }).filter((o) => !written.some((w) => hits(o.rect, w)));
    const found = free[0]?.spot === CAPTION_SPOTS[0]
      ? free[0]
      : free.find((o) => !circles.some((c) => hits(o.rect, c))) || free[0] || null;
    if (found) written.push(found.rect);
    spots.set(it.id, found ? found.at : null);
  }
  return spots;
}

// contentBox is the box the drawn nodes actually occupy, in world units, including
// each node's own footprint and the room its name needs beside it.
//
// It exists because the world and the picture in it are not the same thing. The
// world is an *area budget* — sized from the graph so the layout has room to settle
// — and the layout normally spreads the content across it, so the two coincide and
// framing the world frames the picture. They stop coinciding the moment a node is
// pinned: the fit is skipped then (it would drag the pins off the spots they were
// dropped on), and the content is left wherever it settled. Framing the world after
// that shows the picture in one corner of a mostly empty sheet, which is exactly
// what "Fit" is for and exactly what it stopped doing.
export function contentBox(nodes, pad = LABEL_MARGIN) {
  if (!nodes.length) return { x: 0, y: 0, width: 0, height: 0 };
  const m = typeof pad === "number" ? { top: pad, right: pad, bottom: pad, left: pad } : pad;
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (const n of nodes) {
    const r = radiusOf(n);
    minX = Math.min(minX, n.x - r - m.left);
    maxX = Math.max(maxX, n.x + r + m.right);
    minY = Math.min(minY, n.y - r - m.top);
    maxY = Math.max(maxY, n.y + r + m.bottom);
  }
  return { x: minX, y: minY, width: maxX - minX, height: maxY - minY };
}

// fitView frames a content box in a viewport, and keeps a corner of that viewport
// clear.
//
// Two things it does that framing the world did not:
//
//   - **The returned view has the viewport's own aspect ratio**, so the SVG has
//     nothing to letterbox and the content is centred in what is left rather than
//     pushed to one side of a box of a different shape.
//   - **It holds `reserve` pixels of the bottom-right corner free.** The zoom
//     controls float over the canvas there, and a node underneath them cannot be
//     clicked or dragged — the pointer lands on the panel. That is not a rare
//     coincidence either: the fit pushes content to the edges by construction, so
//     the corner is where a node reliably ends up, and it happens most in a filtered
//     or drilled picture, where there are few enough nodes for one of them to be the
//     one you wanted. Reserving the corner in the *framing* rather than in the
//     layout keeps the arithmetic exact: the scale is known here, so pixels of chrome
//     convert to world units without a second guess.
//
// The chrome sits in a *corner*, and that is the whole subtlety. Subtracting its
// width and its height both — the obvious reading — reserves two full strips whose
// intersection is the corner, and gives away a quarter of a wide canvas to a panel
// two hundred pixels across. What the picture actually has to avoid is the corner
// rectangle, and the largest rectangle that avoids it is one of exactly two: the
// frame minus the panel's width, or the frame minus its height. Whichever holds the
// content at the larger scale wins, which on a landscape-shaped canvas with a short
// panel is nearly always the second and costs almost nothing.
export function fitView(box, frame, reserve = { width: 0, height: 0 }) {
  const boxW = Math.max(box.width, 1), boxH = Math.max(box.height, 1);
  // Chrome is chrome: it may never take more than half the picture, whatever it
  // reports its size as.
  const takeW = Math.min(reserve.width || 0, frame.width * 0.5);
  const takeH = Math.min(reserve.height || 0, frame.height * 0.5);
  const options = [
    { w: Math.max(frame.width - takeW, 1), h: Math.max(frame.height, 1), takeW, takeH: 0 },
    { w: Math.max(frame.width, 1), h: Math.max(frame.height - takeH, 1), takeW: 0, takeH },
  ];
  let best = null;
  for (const option of options) {
    const scale = Math.min(option.w / boxW, option.h / boxH);
    if (!best || scale > best.scale) best = { ...option, scale };
  }
  const { scale } = best;
  const w = frame.width / scale, h = frame.height / scale;
  // Whatever the box does not use of the region it is allowed, split evenly, so the
  // picture sits in the middle of the space it can actually be reached in.
  const leftoverX = Math.max(w - best.takeW / scale - boxW, 0);
  const leftoverY = Math.max(h - best.takeH / scale - boxH, 0);
  return { x: box.x - leftoverX / 2, y: box.y - leftoverY / 2, w, h };
}

// ZOOM_RANGE bounds how far the viewer can push the frame, as multiples of the
// fitted one. In is capped where a node fills the frame and there is nothing further
// to see. Out was capped at 1.6 on the argument that zooming past the content only
// adds the empty space the fit exists to remove — true when the content could not
// leave the world, and no longer true now that a node can be dragged anywhere: past
// the fitted frame there is arrangement to find, and pulling back to look for it is
// how somebody finds it without giving up their arrangement to Fit.
const ZOOM_RANGE = { min: 1 / 24, max: 4 };

// zoomView returns the frame after zooming by `factor` about a point, in the same
// user units as the frame itself. Zooming about the pointer rather than the centre
// is what makes a wheel feel like a map instead of a slider: whatever is under the
// cursor stays under it.
//
// Pure, so the behaviour can be checked without a browser: the frame is data, and
// the only thing the DOM does with it is carry it into a viewBox attribute.
export function zoomView(view, factor, focus, base) {
  const limitOut = base.w * ZOOM_RANGE.max, limitIn = base.w * ZOOM_RANGE.min;
  const w = Math.min(Math.max(view.w * factor, limitIn), limitOut);
  const applied = w / view.w; // what the clamp actually allowed
  const h = view.h * applied;
  return {
    x: focus.x - (focus.x - view.x) * applied,
    y: focus.y - (focus.y - view.y) * applied,
    w,
    h,
  };
}

// DEPENDENCY_EDGES are the edge kinds impact analysis walks. Containment is
// deliberately absent: an application *contains* its processes, it does not depend
// on them, and walking it would drag every sibling into the answer through their
// shared application — which would make "what breaks if this goes down" name half
// the landscape and mean nothing.
const DEPENDENCY_EDGES = new Set(["calls", "uses"]);

// degreesOf counts, for every node, how many dependency edges touch it.
//
// Containment is left out for the same reason impact analysis leaves it out: an
// application does not depend on the processes it holds, and counting them would
// make every application a hub by construction and say nothing. What is counted is
// what would actually propagate — calls and uses — so the count means "how much
// traffic runs through this", which is what makes a node worth noticing.
//
// Both ends of an edge are counted, and self-edges once, so a node that calls three
// things and is called by two has degree five.
export function degreesOf(graph) {
  const degree = new Map(graph.nodes.map((n) => [n.id, 0]));
  const bump = (id) => { if (degree.has(id)) degree.set(id, degree.get(id) + 1); };
  for (const e of graph.edges) {
    if (!DEPENDENCY_EDGES.has(e.kind)) continue;
    bump(e.from);
    if (e.to !== e.from) bump(e.to);
  }
  return degree;
}

// impactFrom answers ADR-0211 §6's question over the graph the viewer already has:
// what breaks if this node goes down (direction "dependents", walking edges
// backwards), or what this node needs to work at all ("dependencies", forwards).
// "both" walks either way.
//
// It runs on the delivered graph rather than through a second endpoint, and that is
// the point: the answer must be about the picture on screen. A server-side walk over
// an unfiltered graph could name resources this viewer cannot see, and a second
// implementation could disagree with the drawing it is supposed to explain.
//
// Two rules carry the honesty of the answer:
//
//   - A restricted placeholder is included but never walked through. We may not see
//     past it, so the reachable set beyond it is unknown — the result records every
//     placeholder it stopped at and reports complete: false. An impact answer that
//     quietly stopped at a permission boundary would read as "nothing further
//     depends on this", which is the one thing it must not say.
//   - Unknown ids return null rather than an empty set, for the same reason: an
//     empty answer means "nothing depends on this", and that is a claim.
//
// Returns { starts, nodes, direct, edges, truncatedBy, complete } or null.
export function impactFrom(graph, startId, opts = {}) {
  return impactOf(graph, [startId], opts);
}

// impactOf is the same question asked about several nodes at once: what stops if all
// of these go down together.
//
// It is one walk from all of them rather than a walk each, and that is the whole
// point rather than an optimisation. Blast radii overlap, so the union is not the
// sum — two services that share a queue break the same eleven things, and a window
// panel that added their counts would say twenty-two where the truth is eleven.
// Seeding the frontier with every start makes the union the thing that is computed
// and the individual totals the thing derived from it (see windowOverlap), which is
// the direction that cannot produce a number nobody could reach.
//
// An id that is not in this picture makes the whole answer null, as it does for one:
// a window is a claim about a specific set, and silently planning around two of the
// three nodes somebody named is worse than refusing.
export function impactOf(graph, startIds, { direction = "dependents", depth = Infinity } = {}) {
  const starts = [...new Set(startIds || [])];
  if (!starts.length) return null;
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  if (starts.some((id) => !byId.has(id))) return null;
  const walked = walkImpact(edgeIndex(graph), byId, starts, { direction, depth });
  return {
    starts,
    nodes: [...walked.hops.keys()],
    // The nodes with an edge straight to one of these. They are the difference
    // between "who do I call about this" and "how far does it go": a direct dependent
    // breaks at its own boundary, and everything past it breaks because that one did.
    direct: [...walked.hops].filter(([, hop]) => hop === 1).map(([id]) => id),
    edges: walked.edges,
    truncatedBy: walked.truncatedBy,
    complete: walked.truncatedBy.length === 0,
  };
}

// edgeIndex builds the adjacency an impact walk needs, once. It is separate from
// the walk because the ranking below walks from every node in the graph, and
// rebuilding this per node would turn an O(N·E) answer into an O(N·E) answer with a
// large constant in front of it for no reason.
function edgeIndex(graph) {
  const forward = new Map(), backward = new Map();
  for (const e of graph.edges) {
    if (!DEPENDENCY_EDGES.has(e.kind)) continue;
    if (!forward.has(e.from)) forward.set(e.from, []);
    if (!backward.has(e.to)) backward.set(e.to, []);
    forward.get(e.from).push(e);
    backward.get(e.to).push(e);
  }
  return { forward, backward };
}

// walkImpact is the traversal itself, shared by the single answer and the ranking.
// One walk with two callers rather than two walks: the ranking's numbers and the
// panel's have to be the same numbers, and the only way to guarantee that is for
// them to come from the same code.
//
// hops maps every reached node to its distance from the start, which is what makes
// "direct" answerable; edges are collected only when a caller wants to draw them.
function walkImpact(index, byId, starts, { direction, depth, edges: wantEdges = true }) {
  const step = (id) => {
    const out = [];
    if (direction !== "dependencies") {
      out.push(...(index.backward.get(id) || []).map((e) => [e, e.from]));
    }
    if (direction !== "dependents") {
      out.push(...(index.forward.get(id) || []).map((e) => [e, e.to]));
    }
    return out;
  };

  const from = new Set(starts);
  const hops = new Map([...from].map((id) => [id, 0]));
  const seenEdges = new Set();
  const edges = [];
  const truncatedBy = [];
  // Asking about the boundary itself is a different question from arriving at one.
  // The edges *into* a placeholder are ours — the nodes at their other end are in
  // this caller's own picture — so they are walked; what is beyond it is not, and
  // the payload draws no edge out of a placeholder for exactly that reason. So the
  // walk runs and the answer is a floor. Skipping the start instead would have
  // answered "nothing depends on this", which is the one thing a boundary must
  // never be allowed to say.
  for (const id of from) {
    if (byId.get(id)?.kind === "restricted") truncatedBy.push(id);
  }
  let frontier = [...from];
  for (let hop = 0; hop < depth && frontier.length; hop++) {
    const next = [];
    for (const id of frontier) {
      // A placeholder stands for something we may not see, so its own edges are not
      // ours to follow — it is a boundary, not a waypoint.
      if (!from.has(id) && byId.get(id)?.kind === "restricted") continue;
      for (const [edge, other] of step(id)) {
        if (wantEdges && !seenEdges.has(edge)) {
          seenEdges.add(edge);
          edges.push(edge);
        }
        if (hops.has(other)) continue;
        hops.set(other, hop + 1);
        if (byId.get(other)?.kind === "restricted") truncatedBy.push(other);
        next.push(other);
      }
    }
    frontier = next;
  }
  return { hops, edges, truncatedBy };
}

// impactSummary is the answer's shape rather than only its size: how many, how many
// of them are already in trouble, and how many are one edge away.
//
// The severity mix is *not* a causal claim, and the panel that renders it says so.
// A node's class is what that node reports about itself; that three of a worker's
// twelve dependents are critical may be the worker's fault, may be why the worker
// looks busy, or may be unrelated. What the mix is good for is triage — a blast
// radius that is already burning is a different morning from one that is quiet —
// and stating causation from a correlation is how a panel stops being believed.
export function impactSummary(graph, result, startId) {
  if (!result) return null;
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  // Every node the question was asked about, not only the one named here. With a
  // window of three, the other two are things the reader is taking down on purpose —
  // counting them as collateral would inflate the answer with the plan itself.
  const from = new Set(result.starts || [startId]);
  const others = result.nodes.filter((id) => !from.has(id));
  const bySeverity = { critical: 0, attention: 0, ok: 0, unknown: 0 };
  for (const id of others) {
    const sev = byId.get(id)?.severity;
    bySeverity[sev in bySeverity ? sev : "unknown"] += 1;
  }
  return {
    total: others.length,
    direct: result.direct.length,
    indirect: others.length - result.direct.length,
    bySeverity,
    complete: result.complete,
  };
}

// impactList names the nodes the count is counting, worst first.
//
// The count and the highlight together still leave the reader hunting: on four
// hundred circles "twelve depend on this" means finding twelve lit dots, and the
// three that matter are the ones already reporting a problem. This is the findings
// list's argument applied to the impact answer — the same nodes, as an index.
//
// Direct before transitive within a severity, because those are the ones somebody
// has to be told about first.
export function impactList(graph, result, startId, { limit = 8 } = {}) {
  if (!result) return [];
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const direct = new Set(result.direct);
  const from = new Set(result.starts || [startId]);
  return result.nodes
    .filter((id) => !from.has(id))
    .map((id) => ({ ...(byId.get(id) || { id }), direct: direct.has(id) }))
    .sort((a, b) =>
      ((SEVERITY_ORDER[b.severity] ?? 0) - (SEVERITY_ORDER[a.severity] ?? 0)) ||
      (Number(b.direct) - Number(a.direct)) ||
      String(a.name || a.id).localeCompare(String(b.name || b.id)))
    .slice(0, limit);
}

// blastRanking answers the impact question without being asked about a node first.
//
// Until now the analysis needed a selection, which means the reader had to already
// suspect the node that matters — and "which of these four hundred would hurt most"
// is the question they actually arrive with, especially before a change. So the walk
// runs from every node and the results are ranked.
//
// It is O(N·E) over the graph already in the browser, which the size budget (§7)
// bounds: past it the payload arrives collapsed to applications, so N is a few dozen
// rather than a few hundred exactly where the ranking would have cost the most.
//
// Ties go to the node that is itself in trouble. Two nodes carrying twelve each are
// not the same finding when one of them is already failing.
export function blastRanking(graph, { direction = "dependents", depth = Infinity, limit = 6 } = {}) {
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const index = edgeIndex(graph);
  const rows = [];
  for (const node of graph.nodes) {
    const walked = walkImpact(index, byId, [node.id], { direction, depth, edges: false });
    const total = walked.hops.size - 1;
    if (total <= 0) continue;
    rows.push({
      id: node.id, name: node.name, kind: node.kind, severity: node.severity,
      total,
      direct: [...walked.hops.values()].filter((hop) => hop === 1).length,
      // A walk that stopped at a permission boundary produces a floor, not a total —
      // and in a *ranking* that matters twice over, because the order is a claim
      // about the rows as well as the numbers.
      complete: walked.truncatedBy.length === 0,
    });
  }
  rows.sort((a, b) =>
    (b.total - a.total) ||
    ((SEVERITY_ORDER[b.severity] ?? 0) - (SEVERITY_ORDER[a.severity] ?? 0)) ||
    String(a.name || a.id).localeCompare(String(b.name || b.id)));
  return rows.slice(0, limit);
}

// heatRanking answers, in a list, the question the heat weighting asks of the
// picture: which nodes carry the most of whatever is being drawn.
//
// It exists because the picture and the column beside it were answering different
// questions at once. With a weighting on, the canvas ranks the estate by a tally
// while the list ranked it by blast radius, so the largest circle and the first row
// were routinely different nodes — and a reader has no way to tell that two orderings
// on one screen are deliberate rather than a contradiction.
//
// It is not a re-listing of the picture. Two things a circle cannot give: the exact
// number — nobody reads 41 against 38 off two areas — and the name, which at a
// zoomed-out magnification is not painted at all (see LABEL_TIERS).
//
// Each row also carries the reach the blast ranking would have measured, and that is
// what turns a count into a priority: forty incidents on a leaf process is a contained
// problem, twelve on something two hundred things need is an outage. It follows the
// same direction and depth controls as everything else in this column, so the two
// numbers on one row were measured the way the panel measures them.
//
// The walk runs only for the rows that survived the cut, which is why the tie-break is
// severity rather than reach: ranking *by* reach would mean walking from every node to
// order rows most of which are then thrown away, and the ordering the reader came for
// is the tally.
export function heatRanking(graph, heat, { direction = "dependents", depth = Infinity, limit = 6, at = Date.now() } = {}) {
  const read = heatReader(heat);
  if (!read) return [];
  const rows = [];
  for (const node of graph.nodes) {
    const value = Number(read(node, at));
    // Only where there is something to rank. A "most parked" list padded out with
    // zeroes is a list whose first rows are the answer and whose rest is noise — and
    // on a healthy estate it would be nothing but noise.
    if (!Number.isFinite(value) || value <= 0) continue;
    rows.push({ id: node.id, name: node.name, kind: node.kind, severity: node.severity, value });
  }
  rows.sort((a, b) =>
    (b.value - a.value) ||
    ((SEVERITY_ORDER[b.severity] ?? 0) - (SEVERITY_ORDER[a.severity] ?? 0)) ||
    String(a.name || a.id).localeCompare(String(b.name || b.id)));
  const top = rows.slice(0, limit);
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const index = edgeIndex(graph);
  for (const row of top) {
    const walked = walkImpact(index, byId, [row.id], { direction, depth, edges: false });
    row.total = walked.hops.size - 1;
    // A walk stopped at a permission boundary produces a floor rather than a total,
    // and the row says so rather than printing a number it cannot stand behind.
    row.complete = walked.truncatedBy.length === 0;
  }
  return top;
}

// windowOverlap is the arithmetic a maintenance window needs and a count cannot give.
//
// The question behind it is not "what does each of these break" but "what does the
// evening cost". Those differ, and the difference is the whole reason to plan a
// window rather than three changes: blast radii overlap, so three services that
// break twelve, nine and seven things do not break twenty-eight. Adding the counts
// is the mistake this function exists to make impossible — the union is walked (see
// impactOf) and the individual totals are reported beside it, so the two numbers are
// on screen together and the reader can see how much of the cost is shared.
//
// Two things it reports that a single answer has no way to say:
//
//   - **shared** — how many nodes sit in more than one radius. It is a count of
//     nodes rather than the difference between the two totals, because with three or
//     more starts a node reached by all of them is double-counted twice and "sum
//     minus union" would name a number that is not a set of anything.
//   - **covered** — the selected nodes that another selected node already takes down.
//     Taking those down changes nothing the window does not already do, which is
//     worth knowing before writing it into a change request.
//
// Every individual total excludes the other selected nodes for the same reason the
// union does: they are going down on purpose, and counting the plan as its own
// collateral inflates every number in the panel.
export function windowOverlap(graph, startIds, { direction = "dependents", depth = Infinity } = {}) {
  const union = impactOf(graph, startIds, { direction, depth });
  if (!union) return null;
  const from = new Set(union.starts);
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const index = edgeIndex(graph);

  const reachedBy = new Map();
  const each = union.starts.map((id) => {
    const walked = walkImpact(index, byId, [id], { direction, depth, edges: false });
    const reach = [...walked.hops.keys()].filter((other) => !from.has(other));
    for (const other of reach) reachedBy.set(other, (reachedBy.get(other) || 0) + 1);
    const node = byId.get(id) || {};
    return {
      id, name: node.name, kind: node.kind, severity: node.severity,
      total: reach.length,
      // The other selected nodes this one reaches, which is what makes one of them
      // redundant in the window rather than merely overlapping with it.
      covers: [...walked.hops.keys()].filter((other) => other !== id && from.has(other)),
      complete: walked.truncatedBy.length === 0,
    };
  });

  const covered = new Set();
  for (const row of each) for (const id of row.covers) covered.add(id);
  return {
    starts: union.starts,
    each,
    total: union.nodes.filter((id) => !from.has(id)).length,
    sum: each.reduce((n, row) => n + row.total, 0),
    shared: [...reachedBy.values()].filter((times) => times > 1).length,
    covered: [...covered],
    complete: union.complete,
  };
}

// hrefFor is where a node is opened, when its inside is somewhere else.
//
// Panorama owns the landscape and application altitudes and links into the ones below
// rather than reimplementing them (ADR-0211 §5), so two kinds have an elsewhere: a
// process opens on its live view, with its instances and its tokens, and a decision on
// its evaluation history, which is the same question one altitude down — what has this
// actually done, and with what.
//
// A draft's inside is the other direction. It has never run, so there is nothing for
// Operations to show and sending a reader there would be sending them to an empty
// page; what a draft has is a diagram, so it opens in the Modeler. That is the same
// rule, not an exception to it — a node is opened where the thing it stands for
// actually lives.
//
// Everything else answers "" and is opened *here*, by becoming the centre of the
// picture. The two placeholder kinds are covered by that: a decision the caller may
// not see is a restricted node and one nothing provides is an unresolved node, and
// neither is a `decision`, so neither is offered a link to a page that would not have
// it (ADR-0211 §3 — an absence must never read as a fact).
function hrefFor(node) {
  if (node.kind === "process") {
    const key = node.id.slice("process:".length);
    return `#/operations/p/${encodeURIComponent(key)}`;
  }
  if (node.kind === "decision") {
    const id = node.id.slice("decision:".length);
    return `#/operations/decisions/${encodeURIComponent(id)}`;
  }
  if (node.kind === "draft") {
    const id = node.id.slice("draft:".length);
    return `#/modeler/draft/${encodeURIComponent(id)}`;
  }
  return "";
}

// Where that link goes, in the words of the page it lands on. It is derived from the
// kind rather than written beside each href so the two cannot come apart: a link that
// says Operations and opens the Modeler is worse than no link.
function insideName(node) {
  return node.kind === "draft" ? "Modeler" : "Operations";
}

function nodeTitle(node, notation) {
  // What the notation calls it, first, because in a projection that is the word the
  // reader is looking at. Atlas's own name for the kind follows it rather than being
  // replaced: the projection is a way of speaking about these resources, not a claim
  // that they are something else.
  const typed = typeIn(node.kind, notation);
  if (typed) {
    const parts = [node.name || node.id, typed.name,
      `Atlas ${(KIND[node.kind] || {}).label?.split(" — ")[0]?.toLowerCase() || node.kind}`];
    if (node.state && node.state !== "unbound") parts.push(STATE_TEXT[node.state] || node.state);
    if (node.reason) parts.push(node.reason);
    return parts.join(" · ");
  }
  if (node.kind === "target") {
    // Never its base URL: that is this operator's map of where their infrastructure
    // lives, and a landscape is opened by anybody with modeler access.
    const parts = [node.name || node.id, "deployment target"];
    if (node.state && node.state !== "unbound") parts.push(STATE_TEXT[node.state] || node.state);
    if (node.reason) parts.push(node.reason);
    return parts.join(" · ");
  }
  if (node.kind === "restricted") {
    return "A resource outside your access. The dependency is real; its identity is not shown.";
  }
  if (node.kind === "unresolved") {
    // The id carries what kind of thing is missing, which is what makes the
    // sentence actionable: a missing deployment and a missing worker are fixed in
    // different places.
    const of = node.id.split(":")[1] || "dependency";
    return `Nothing on this server provides the ${of} "${node.name}". Work reaching it would park.`;
  }
  if (node.kind === "draft") {
    // Said in words rather than left to the shared sentence below, which would have
    // reached for a version this node has not got. That is not a cosmetic slip: a
    // draft's whole claim is that nothing about it is running, and a tooltip reading
    // "v undefined" would be the picture contradicting itself.
    return `${node.name || node.id} · ${node.processId} · a saved diagram, not ` +
      `deployed. Nothing runs here yet; open it in the Modeler.`;
  }
  const parts = [node.name || node.id];
  if (node.modelName && node.modelName !== node.name) parts.push(`modeled as “${node.modelName}”`);
  // The version only where there is one. A node can carry a process id without being
  // a deployment, and printing "v undefined" beside it would invent a fact.
  if (node.processId) {
    parts.push(node.version ? `${node.processId} v${node.version}` : node.processId);
  }
  if (node.workerType) parts.push(`${node.workerType} worker`);
  if (node.children) parts.push(`${node.children} process(es) collapsed`);
  // The state, then the reason, then — if it was inherited — which descendant it
  // came from. ADR-0211 §4 requires the last of those: a red parent that cannot say
  // which child is red is not actionable, and trains an operator to ignore the color.
  if (node.state && node.state !== "unbound") parts.push(STATE_TEXT[node.state] || node.state);
  if (node.reason) parts.push(node.reason);
  if (node.severityFrom) parts.push(`inherited from ${node.severityFrom}`);
  return parts.join(" · ");
}

// matches decides what a search term keeps. It reads the name, the kind, and a
// process's BPMN id — the three things somebody actually types — and never the
// node id, whose prefixes would make every term match its own kind by accident.
function matches(node, term) {
  if (!term) return true;
  // Severity and state are search axes, not only colours (ADR-0211 §6 names status
  // among the things the mesh must be filterable by): typing "critical" is how an
  // operator gets from a few hundred nodes to the handful that are broken.
  const hay = [node.name, node.kind, node.processId, node.workerType, node.severity, node.state]
    .filter(Boolean).join(" ").toLowerCase();
  return hay.includes(term);
}

// CONTEXT_HOPS is how far around a match the filter reaches for context.
//
// One, and it is not a placeholder for a setting. A filtered node on its own is a
// circle in an empty field: it answers "does this exist" and nothing else, when the
// question somebody types a name to ask is nearly always "and what is it attached
// to". One hop answers that. Two would not answer it better — nearly everything in a
// landscape hangs off some hub, and reaching through one drags most of the graph back
// onto the screen, which is the filter failing to filter.
const CONTEXT_HOPS = 1;

// filterGraph keeps the matching nodes, the immediate neighbourhood around them, and
// every edge between what is left.
//
// The neighbourhood is *context*, and it is marked as such rather than presented as a
// result: `matched` names the nodes that actually matched the term, the drawing draws
// the rest more faintly, and the header counts them separately. A search that
// silently returned things which do not match the search would be a worse answer than
// the empty field it is fixing.
//
// A search is the viewer's own choice, unlike a sharing scope, so what is left out
// here is not a lie — but the header still reports how much is hidden, because a
// filtered mesh looks exactly like a small one.
// filterGraph narrows the landscape to what the reader has asked for: a term typed
// into the box, a band chosen off the scale in the key, or both.
//
// Both at once is an *intersection*, and it goes through one walk rather than two.
// Narrowing twice would take the context of the context — a node two hops from
// something that merely explains a match — and the picture would grow as the question
// got narrower, which is the opposite of what was asked. So the two criteria pick the
// seeds together and `around` is called once, exactly as it is for a term alone.
function filterGraph(graph, { term, band, heat, at = Date.now() } = {}) {
  if (!term && !band) return graph;
  const read = band && heat?.of ? (n) => heat.of(n, at) : null;
  const matched = new Set(graph.nodes
    .filter((n) => (!term || matches(n, term)) && (!read || inBand(read(n), band)))
    .map((n) => n.id));
  return around(graph, matched, CONTEXT_HOPS);
}

// around cuts the graph down to a set of nodes and whatever is within `hops` of
// them, marking which of the survivors were asked for and which are only there to
// explain them.
//
// One walk for both ways of narrowing the picture — a search and a drilldown — for
// the reason every other rule in this file is written once: two implementations of
// "and its neighbours" would eventually disagree about what a neighbour is, and the
// difference would show up as a picture that answers a slightly different question
// depending on how you got to it.
//
// Direction is not consulted. "What is this attached to" includes the application it
// sits in and the worker it calls alike; a hop that only counted arrows pointing one
// way would answer a question nobody asked while looking like this one.
function around(graph, seeds, hops) {
  const keep = new Set(seeds);
  let frontier = [...seeds];
  for (let hop = 0; hop < hops && frontier.length; hop++) {
    const reached = new Set(frontier);
    const next = [];
    for (const e of graph.edges) {
      for (const [here, there] of [[e.from, e.to], [e.to, e.from]]) {
        if (!reached.has(here) || keep.has(there)) continue;
        keep.add(there);
        next.push(there);
      }
    }
    frontier = next;
  }
  return {
    ...graph,
    matched: new Set(seeds),
    nodes: graph.nodes.filter((n) => keep.has(n.id)),
    edges: graph.edges.filter((e) => keep.has(e.from) && keep.has(e.to)),
  };
}

// drillInto is the whole landscape reduced to one node and what it touches.
//
// It is the same cut as a search that matched exactly one thing, and deliberately
// so: reading a landscape is mostly narrowing, and there should be one idea of what
// narrowed looks like rather than one per gesture. What differs is the reach — a
// search keeps one hop because a name can match a hundred nodes, while a drilldown
// starts from exactly one and can afford to follow the depth already on screen.
//
// Returns null when the node is not in this graph. A drilldown onto nothing is not
// an empty landscape; it is a question that can no longer be asked, and the caller
// has to say so rather than draw a blank canvas.
export function drillInto(graph, id, hops) {
  if (!graph.nodes.some((n) => n.id === id)) return null;
  return around(graph, new Set([id]), hops);
}

// legendEntries is the key to the picture: one swatch per thing the picture
// actually contains, drawn by the same functions that drew it.
//
// It exists as data rather than as markup because the legend now has two readers.
// Beside the canvas it is HTML in the page; inside an exported file it is SVG in
// the artifact, where it matters more — the app has a legend one scroll away and a
// file that travels has nothing at all, so a hexagon or an orange dot arrives
// undefined. Two renderers over one list, for the same reason the swatch is drawn
// by the node's own function: a second list would eventually explain a picture it
// no longer matched.
//
// Only what is present is listed, in every group. A legend describing findings the
// picture does not contain is a legend nobody reads twice.
function legendEntries(graph, notation) {
  const spoken = notationOf(notation?.id ?? notation);
  const present = new Set(graph.nodes.map((n) => n.kind));
  const entries = Object.entries(KIND)
    .filter(([kind]) => present.has(kind))
    .map(([kind, style]) => {
      const typed = typeIn(kind, spoken);
      const paint = paintFor({ kind }, spoken);
      return {
        group: "kind",
        tone: "",
        // In a projection the swatch is labelled with the notation's word and keeps
        // Atlas's own beside it. Replacing it outright would leave a reader unable
        // to get from the picture back to the thing it is about, which is the whole
        // reason they opened the landscape.
        label: typed ? `${typed.name} — ${style.label.split(" — ")[0]}` : style.label,
        mark: `<g transform="translate(8,8)">${bodyElement(typed?.shape || style.shape, 6,
          `fill="${paint.fill}" stroke="${paint.stroke}" stroke-width="2" ` +
          (paint.dashed ? 'stroke-dasharray="3 2"' : ""))}</g>`,
      };
    });

  // The line styles — the half of the picture the shapes above do not explain.
  // Every swatch so far names a *thing*; every line on the canvas is a claim about
  // how the estate hangs together, and a reader could see that some edges were drawn
  // differently with no way to find out from what.
  //
  // One row per kind the picture actually contains, and the swatch is drawn with the
  // canvas's own class rather than with a copy of its dash pattern — the same rule
  // paints both, so the key cannot come to disagree with the picture it explains.
  // That holds in an exported file too: the harvested stylesheet carries every
  // `.mesh-` rule, this one included.
  const edgeKinds = new Set((graph.edges || []).map((e) => e.kind));
  for (const [kind, label, claim] of EDGE_KEY) {
    if (!edgeKinds.has(kind)) continue;
    // The notation's own name for the relationship where it has one, and the swatch
    // drawn with the same marks the canvas puts on it — the key explains the picture
    // beside it rather than a picture of its own. A relationship the notation runs
    // backwards says so in words too: the arrowhead alone is a thing a reader has to
    // already know the notation to read, and the row is for the one who does not.
    const ends = markEnds(kind, spoken);
    entries.push({
      group: "edge", tone: "",
      label: ends
        ? `${ends.relation.name} — ${claim}${ends.relation.flip ? ", drawn from the provider" : ""}`
        : label,
      mark: `<line x1="2" y1="8" x2="14" y2="8"
        class="mesh-edge mesh-edge-${kind}${ends ? " mesh-edge-marked" : ""}"${
        ends?.start ? ` marker-start="url(#${ends.start})"` : ""}${
        ends?.end ? ` marker-end="url(#${ends.end})"` : ""}/>`,
    });
  }

  const severityPresent = new Set(graph.nodes.map((n) => n.severity).filter(Boolean));
  for (const key of ["critical", "attention", "ok", "unknown"]) {
    if (!severityPresent.has(key)) continue;
    entries.push({
      group: "severity",
      // The class the swatch's colour comes from, carried on its own rather than
      // baked into a class list: the page wants it beside .mesh-swatch, and the
      // export wants it without — an inline-flex rule means nothing on an SVG
      // group and only invites the browser to interpret it.
      tone: `mesh-sev-${key}`,
      label: SEVERITY[key].label,
      mark: `<circle cx="8" cy="8" r="6" fill="var(--surface)"
        stroke="${SEVERITY[key].stroke || "var(--border-strong)"}" stroke-width="2"/>` +
        (SEVERITY[key].glyph
          ? `<text x="8" y="11.5" text-anchor="middle" class="mesh-sev-glyph">${esc(SEVERITY[key].glyph)}</text>`
          : ""),
    });
  }

  // Provenance only once a model has been overlaid: with none, everything is
  // derived and three swatches saying so would be three swatches about nothing.
  if (graph.modeled > 0 || graph.unmodeled > 0 || graph.outOfScope > 0) {
    for (const key of ["derived", "both", "modeled"]) {
      entries.push({
        group: "provenance",
        tone: "",
        label: PROVENANCE[key].label,
        mark: (PROVENANCE[key].ring
          ? `<circle cx="8" cy="8" r="7" fill="none" stroke="var(--muted)" stroke-width="1" opacity="0.55"/>`
          : "") +
          `<circle cx="8" cy="8" r="5" fill="${PROVENANCE[key].ghost ? "none" : "var(--surface)"}"
            stroke="var(--muted)" stroke-width="2"
            ${PROVENANCE[key].ghost ? 'stroke-dasharray="3 2"' : ""}/>`,
      });
    }
  }
  return entries;
}

// HEAT_SCALE_SHRINK is how much smaller a reference circle is drawn in the key than
// the node it stands for.
//
// Not 1, because the largest node is 41 units across the radius and a row of them
// would be taller than the legend it is in. Not much smaller either: the whole point
// of the row is that the reader compares its circles against the ones on the canvas,
// and a scale drawn at a quarter size is a scale they have to do arithmetic on. A
// half puts the biggest reference circle at 20 pixels, which is a legend row 44
// pixels tall — one line of the legend, and still plainly the same family of shapes.
//
// Everything on the row is shrunk by the same factor, so the *ratios* — which is what
// a ratio scale is read by — are exactly the ratios on the canvas.
const HEAT_SCALE_SHRINK = 0.5;

// heatScaleMarks is the scale itself: the circles a reader measures the picture with,
// as a list of radius-and-label pairs.
//
// The key already says what the law is in words, and words are not a scale. A reader
// looking at a node cannot tell from a sentence whether it is running ten or a
// thousand — they can tell it by holding it against a circle with a number under it,
// which is what a bubble chart has always done and what this was missing.
//
// Sized by radiusForTally, the same function that sized the nodes, so the key cannot
// drift from the canvas: there is one law and one implementation of it, and the row
// is a rendering of that law rather than a picture of it. Drawn in the process kind's
// own fill and stroke, read off KIND rather than copied into a stylesheet, for the
// same reason: a scale has to look like the thing it is measuring, and most of what
// it measures on a heat picture is a process.
//
// The nothing-at-all circle comes first and carries no number, because that is what
// it means. It is the one a reader needs most: the whole complaint this scale answers
// was that a node running one could not be told from a node running none.
export function heatScaleMarks(heat, peak) {
  const ticks = heatTicks(peak, heat);
  if (!ticks.length) return [];
  const tick = heat.tick || ((n) => fmtCount(n));
  return [
    { tally: 0, label: "none", r: radiusForTally(0, peak, heat) },
    ...ticks.map((tally) => ({ tally, label: tick(tally), r: radiusForTally(tally, peak, heat) })),
  ];
}

// heatBand is what one mark on the scale stands for, as a range of tallies.
//
// A mark owns everything from itself up to the next mark, and the last one owns
// everything above it — which is the only reading that covers the whole landscape
// without overlapping, and the only one under which the marks partition it. The
// nothing-at-all mark is its own case: it means a tally of zero, not "less than the
// smallest thing that counts", because those are different facts and the picture
// keeps them apart everywhere else (see HEAT_FLOOR).
//
// `at` is the mark's own tally, which is what the row stores rather than its
// position: the marks are recomputed from the landscape on every paint, and a
// landscape whose peak has moved has different marks in different places. A tally
// survives that where an index does not — and when the tally is no longer a mark at
// all, the caller can see that and let go of the band rather than filter by something
// the reader can no longer point at.
export function heatBand(marks, at) {
  if (at === null || at === undefined) return null;
  if (at === 0) return { from: 0, to: 0 };
  const i = marks.findIndex((m) => m.tally === at);
  if (i < 0) return null;
  return { from: at, to: i + 1 < marks.length ? marks[i + 1].tally : Infinity };
}

// inBand is the test itself. Zero is only ever in the nothing-at-all band, and a node
// that cannot carry a tally at all — a worker, a decision — reads as zero here, which
// is the same answer the picture gives it: it sits on the floor.
export function inBand(value, band) {
  if (!band) return true;
  const tally = Math.max(0, value || 0);
  if (band.to === 0) return tally <= 0;
  return tally >= band.from && tally < band.to;
}

// heatScaleHTML is that row, for the key beside the canvas.
// heatScaleHTML is that row, for the key beside the canvas — where it is also the
// control that narrows the picture to one band of it.
//
// A scale a reader can measure by is a scale they will want to point at: "show me the
// ones running a hundred or more" is the question the row makes askable, and it is
// the question this landscape is opened with. So each mark is a button rather than a
// label, and the picture narrows to the tallies that mark stands for.
//
// Buttons rather than clickable spans, because that is the whole of the keyboard and
// screen-reader behaviour for free, and `aria-pressed` because a filter is a state
// rather than an action. The row loses its `role="img"`: it is a group of controls
// now, and each one carries the band it selects as its own label — "from 100 up to
// 4 200" rather than "100", because the number under a circle is only half a range
// and the half a reader cannot see is the half the button acts on.
function heatScaleHTML(heat, peak, chosen = null) {
  const marks = heatScaleMarks(heat, peak);
  if (!marks.length) return "";
  const box = Math.ceil((HEAT_FLOOR + HEAT_SPAN) * 2 * HEAT_SCALE_SHRINK) + 4;
  const style = KIND.process;
  const step = (mark) => {
    const band = heatBand(marks, mark.tally);
    const on = chosen !== null && chosen === mark.tally;
    return `<button type="button" class="mesh-scale-step${on ? " mesh-scale-on" : ""}"
      data-tally="${mark.tally}" aria-pressed="${on}"
      title="${esc(bandPhrase(heat, marks, mark.tally))}"
      aria-label="${esc(bandPhrase(heat, marks, mark.tally))}">
      <svg width="${box}" height="${box}" aria-hidden="true"><circle
        cx="${box / 2}" cy="${box / 2}" r="${(mark.r * HEAT_SCALE_SHRINK).toFixed(1)}"
        fill="${style.fill}" stroke="${style.stroke}" stroke-width="1"/></svg>
      <span class="mesh-scale-tick">${esc(mark.label)}</span>
    </button>`;
  };
  return `<div class="mesh-scale" role="group"
    aria-label="Size scale — choose a band to narrow the picture to it.">
    ${marks.map(step).join("")}
  </div>`;
}

// bandPhrase says what one mark selects, in the weighting's own words. It is the
// button's title and the only thing a reader who cannot compare two circles has to go
// on, so it says the range rather than repeating the number under the circle.
function bandPhrase(heat, marks, tally) {
  const i = marks.findIndex((m) => m.tally === tally);
  if (i < 0) return "";
  if (tally === 0) return heat.bandNone || "Nothing at all";
  const tick = heat.tick || ((n) => fmtCount(n));
  const next = marks[i + 1];
  return next
    ? `From ${tick(tally)} up to ${tick(next.tally)}`
    : `${tick(tally)} and above`;
}

// heatScaleEntries is the same row for a file, in the shape the export's key lays out.
//
// It travels with the picture for the reason §10 gives for the stamp: beside the
// canvas the key is one scroll away, and in a file pasted into a ticket there is
// nothing to scroll to. A sentence saying the scale is logarithmic is not something a
// reader can hold a circle against.
//
// Every mark is drawn inside the 16-unit box the export's key scales from, so the
// circles are the *ratios* they are on the canvas rather than its pixels — which is
// what a ratio scale is read by. The first one names the weighting, because in a file
// this row arrives after the kinds with nothing above it to say what it is about.
export function heatScaleEntries(heat, peak) {
  const marks = heatScaleMarks(heat, peak);
  if (!marks.length) return [];
  const style = KIND.process;
  const largest = HEAT_FLOOR + HEAT_SPAN;
  return marks.map((mark, i) => ({
    group: "scale",
    tone: "",
    label: i === 0 ? `${heat.short} — ${mark.label}` : mark.label,
    mark: `<circle cx="8" cy="8" r="${(mark.r * 8 / largest).toFixed(2)}"
      fill="${style.fill}" stroke="${style.stroke}" stroke-width="1"/>`,
  }));
}

function legendHTML(graph, layoutMs, notation, peak = 0, band = null) {
  const spoken = notationOf(notation?.id ?? notation);
  const heat = heatOf(spoken);
  const swatch = (entry) => `<span class="mesh-swatch ${entry.tone}">
    <svg width="16" height="16" aria-hidden="true">${entry.mark}</svg>${esc(entry.label)}</span>`;
  const entries = legendEntries(graph, spoken);
  const swatches = entries.filter((e) => e.group === "kind").map(swatch).join("");
  // On a row of its own under the shapes: the labels are sentences rather than
  // words, and threaded in among the kinds they would push the shapes off the line.
  const rules = entries.filter((e) => e.group === "edge").map(swatch).join("");

  const notes = [];
  if (graph.restricted > 0) {
    notes.push(`<p class="mesh-note"><b>${graph.restricted}</b> node(s) are hidden by your
      access. Their dependencies are drawn, their identities are not — this picture is
      filtered, and says so rather than looking complete.</p>`);
  }
  if (graph.clustered) {
    notes.push(`<p class="mesh-note">This starmap exceeded the size budget, so it is
      collapsed to applications. Each one states how many nodes it stands for.</p>`);
  }
  // What size means on this picture, said before anything else the key says: a reader
  // who takes the radii for the structural ones would read the estate backwards. The
  // reference is named, because an area is a quantity and a quantity with no unit is
  // a decoration. Then what the *count* on the canvas does and does not say — a node
  // with nothing to report carries no number, and a reader who did not know that
  // would read its absence as "not measured", which is the one thing it does not mean.
  if (heat) {
    notes.push(peak > 0
      ? `<p class="mesh-note"><b>${heat.heading}</b> A node carrying nothing sits at the
         floor; ${esc(heat.leastPhrase)} is already a step above it, and from there the
         size grows with each <b>tenfold</b> rather than with the count itself — so
         equal steps of size are equal multiples, and the largest node here is
         ${heat.peakPhrase(peak)}. ${heat.floorNote} That means the area is not the
         tally: the scale answers <em>how many times</em> rather than how much, which
         is the question an estate spanning orders of magnitude can be asked. The
         circles below are that scale, and each one is a control: click it to narrow
         the picture to the band it stands for, and click it again to widen. Kind is
         still carried by shape and colour.</p>`
      : `<p class="mesh-note">${heat.quiet} Kind is still carried by shape and
         colour.</p>`);
    // Under the sentence that explains it rather than up among the kinds: a scale is
    // read against its own caption, and the swatch rows above are about what a node
    // *is* where this row is about how much is on it.
    const scale = heatScaleHTML(heat, peak, band);
    if (scale) notes.push(scale);
    notes.push(`<p class="mesh-note">${heat.absent}</p>`);
  }
  // The comparison counts only mean something once a model has been overlaid; with
  // none, saying "0 unmodeled" would imply the landscape had been checked.
  const compared = graph.modeled > 0 || graph.unmodeled > 0 || graph.outOfScope > 0;
  if (compared) {
    if (graph.modeled > 0) {
      notes.push(`<p class="mesh-note"><b>${graph.modeled}</b> node(s) are declared by a
        model and not present here. That is drift the drawing alone could not show.</p>`);
    }
    if (graph.unmodeled > 0) {
      notes.push(`<p class="mesh-note"><b>${graph.unmodeled}</b> node(s) exist here and no
        model mentions them.</p>`);
    }
    if (graph.outOfScope > 0) {
      notes.push(`<p class="mesh-note"><b>${graph.outOfScope}</b> binding(s) point at
        releases, deployment targets or runtimes. This view does not draw those, so they
        are neither matched nor missing — counted here so they are not simply dropped.</p>`);
    }
  }

  const status = graph.status || {};
  const severity = entries.filter((e) => e.group === "severity").map(swatch).join("");

  // What the picture cannot see is stated beside what it can. Without this an
  // instance nothing observes renders as uniformly well, and a green view that has
  // no way to go red is worse than no view.
  if (Array.isArray(status.unavailable) && status.unavailable.length) {
    notes.push(`<p class="mesh-note"><b>Not watched here:</b>
      ${status.unavailable.map((u) => esc(STATE_TEXT[u.state] || u.state)).join(", ")}.
      ${esc(status.unavailable[0].reason)}</p>`);
  }
  if (status.partial) {
    notes.push(`<p class="mesh-note mesh-truncated">Counting parked work stopped at its
      bound, so a node reported as OK here is a floor rather than a verdict.</p>`);
  }

  const provenance = entries.filter((e) => e.group === "provenance").map(swatch).join("");

  // A projection has to say that it is one, and say what it drops (ADR-0211 §8). A
  // picture in somebody else's vocabulary that does not name the vocabulary is a
  // picture claiming to be a model of it, and the loss list is the difference
  // between a projection and a lie of omission.
  const projection = spoken.projection ? `<details class="mesh-projection">
    <summary><b>Projected into ${esc(spoken.label)}</b>
      <span class="muted">mapping v${esc(spoken.mappingVersion)} · read-only · what it drops</span></summary>
    <p class="mesh-note">Atlas's own resources, drawn in ${esc(spoken.short)}'s vocabulary.
      Nothing on this landscape was modelled, and this projection cannot be edited or
      exported as a ${esc(spoken.short)} document.</p>
    <ul class="mesh-loss">${spoken.loss.map((l) => `<li>${esc(l)}</li>`).join("")}</ul>
  </details>` : "";

  return `<div class="mesh-legend">
    ${projection}
    <div class="mesh-swatches">${swatches}</div>
    ${rules ? `<div class="mesh-swatches mesh-rules">${rules}</div>` : ""}
    ${severity ? `<div class="mesh-swatches">${severity}</div>` : ""}
    ${provenance ? `<div class="mesh-swatches">${provenance}</div>` : ""}
    <div class="mesh-meta">${compared
      ? `Compared against the architecture models you can see. Everything unmarked is
         <b>derived</b> from this server's resources.`
      : `Everything here is <b>derived</b> from this server's resources — nothing on this
         view was drawn.`}
      <span class="muted">Laid out in ${Math.round(layoutMs)} ms.</span></div>
    ${notes.join("")}
  </div>`;
}

function renderGraph(graph, layoutMs, frame,
  { pinned, from, notation, peak = 0, at: measuredAt = Date.now() } = {}) {
  const spoken = notationOf(notation?.id ?? notation);
  // Read off the notation rather than passed in beside it: the numbers under the names
  // and the radii they hang from are one decision, and two arguments that could
  // disagree would eventually draw a number a node has no room for.
  const heat = heatOf(spoken);
  // A projected node carries a second line under its name, so the margin the layout
  // reserves has to carry it too — otherwise the type annotation is the one thing
  // that ends up outside the frame.
  // A projected node carries a second line under its name, and the type is routinely
  // longer than the thing it is typing — "[Application Component]" against
  // "Onboarding". Both directions have to grow, or the annotation is the one part of
  // the picture that ends up over the edge of it.
  // Two things can hang a line under a node's name — the notation's word for it, and
  // whatever tally a heat weighting counts — and the margin has to carry however many
  // are on.
  const underlines = (spoken.projection ? 1 : 0) + (heat ? 1 : 0);
  const margin = underlines
    ? { top: LABEL_MARGIN.top, right: LABEL_MARGIN.right + (spoken.projection ? 44 : 0),
        bottom: LABEL_MARGIN.bottom + 16 * underlines,
        left: LABEL_MARGIN.left + (spoken.projection ? 44 : 0) }
    : LABEL_MARGIN;
  // Sized before anything else asks how big they are: connectivity decides the
  // radius, and the world budget, the separation pass and the circle all read it
  // back off the node (see radiusOf) rather than working it out again.
  const degree = degreesOf(graph);
  // Which quantity the radius is spending itself on. Connectivity by default —
  // structure is what this view is for — or one of the heat tallies, when that is the
  // question being asked of it. Never two: one channel, one meaning.
  const nodes = graph.nodes.map((n) => ({
    ...n,
    r: heat ? radiusForHeat(n, peak, heat, measuredAt) : radiusFor(n, degree.get(n.id)),
  }));
  // The graph is laid out in a world of its own size, not in the viewport. The
  // frame only decides that world's shape, so the opening view fills the window
  // without letterboxing.
  const world = worldFor(nodes, frame);
  const { width, height } = world;
  const ms = layout(nodes, graph.edges, { width, height, pinned, from, margin }) + layoutMs;
  const at = new Map(nodes.map((n) => [n.id, n]));

  // One class per derived kind; the stylesheet gives each its own stroke and EDGE_KEY
  // names it for the legend. A kind this build does not know still draws — as the
  // plain line, unexplained — rather than not drawing at all.
  //
  // A notation with a word for the edge marks its ends as well (see markEnds), and
  // the line is then shortened to the two circles so the mark sits outside the shape
  // rather than under it. Without a word it is the derived line, centre to centre,
  // exactly as before: the geometry is the landscape's and only the marks on it are
  // the notation's.
  const marked = new Set();
  const edges = graph.edges.map((e) => {
    const a = at.get(e.from), b = at.get(e.to);
    if (!a || !b) return "";
    const kind = e.kind || "calls";
    const ends = markEnds(kind, spoken);
    const line = ends ? trimEdge(a, b, radiusOf(a), radiusOf(b))
      : { x1: a.x, y1: a.y, x2: b.x, y2: b.y };
    let mark = "";
    if (ends) {
      if (ends.start) { marked.add(ends.start); mark += ` marker-start="url(#${ends.start})"`; }
      if (ends.end) { marked.add(ends.end); mark += ` marker-end="url(#${ends.end})"`; }
    }
    return `<line x1="${line.x1.toFixed(1)}" y1="${line.y1.toFixed(1)}"
      x2="${line.x2.toFixed(1)}" y2="${line.y2.toFixed(1)}"
      data-from="${esc(e.from)}" data-to="${esc(e.to)}"${ends ? ' data-trimmed="1"' : ""}
      class="mesh-edge mesh-edge-${esc(kind)}${ends ? " mesh-edge-marked" : ""}"${mark}/>`;
  }).join("");

  // Only the markers this picture actually uses. They live inside the canvas SVG
  // rather than in the page, which is what carries them into an exported file — the
  // export serialises this element, and the key beside it references the same ids
  // from the same document.
  const defs = marked.size
    ? `<defs>${[...marked].map(markerElement).join("")}</defs>` : "";

  const circles = nodes.map((n) => {
    // The notation's fill and outline where it has one for this kind, Atlas's own
    // otherwise. Everything else on the node — the radius, the badge, the provenance
    // ring — is about the resource rather than about the vocabulary, and is unchanged
    // by which notation is being spoken.
    const style = paintFor(n, spoken);
    const r = radiusOf(n);
    const label = n.kind === "restricted" ? "" : esc(n.name || "");
    const prov = PROVENANCE[n.provenance] || PROVENANCE.derived;
    const sev = SEVERITY[n.severity] || SEVERITY.unknown;
    // Severity is drawn as a badge on the node's own outline rather than by
    // recolouring it: the fill already carries the ArchiMate layer and the kind, and
    // ADR-0189 §6 keeps those. The glyph is what makes the finding readable without
    // colour perception at all.
    const badge = sev.glyph
      ? `<g class="mesh-badge" transform="translate(${(r * 0.72).toFixed(1)},${(-r * 0.72).toFixed(1)})">
           <circle r="7" class="mesh-badge-dot"/>
           <text text-anchor="middle" dy="3.5" class="mesh-badge-glyph">${esc(sev.glyph)}</text>
         </g>`
      : "";
    // Every node carries its name. Which of them are painted is the stylesheet's
    // decision, from the current magnification (see labelTier) — so zooming reveals
    // names with no re-render, and a selected, hovered or focused node keeps its own
    // whatever the zoom is.
    const named = Boolean(label);
    // Context, not a result: this node is on screen because something next to it
    // matched. Drawn more faintly so the filter is still answering the question it
    // was asked, and named all the same — context nobody can read is not context.
    const context = graph.matched ? !graph.matched.has(n.id) : false;
    // What the notation calls this, written under the name. It is C4's own idiom and
    // ArchiMate's corner icon spelled out, and it is the only thing that makes a
    // canvas of identical boxes readable at all.
    const typed = typeIn(n.kind, spoken);
    // What the weighting counts here, when the reader has asked for it. Only where
    // there is something to say: on a landscape of four hundred processes, "0
    // running" four hundred times is a wall of text that hides the eleven numbers
    // somebody turned this on to find. The panel says the zero for whichever node is
    // selected, and the legend says that the canvas does not.
    const tally = heat ? Number(heat.of(n, measuredAt)) : 0;
    const measured = Number.isFinite(tally) && tally > 0 ? tally : 0;
    const runsAt = r + 28 + (typed ? 14 : 0);
    return `<g transform="translate(${n.x.toFixed(1)},${n.y.toFixed(1)})"
      class="mesh-node mesh-${n.kind} mesh-prov-${esc(n.provenance || "derived")} mesh-sev-${esc(n.severity || "unknown")}${named ? " mesh-named" : ""}${context ? " mesh-context" : ""}${n.held ? " mesh-pinned" : ""}"
      data-node-id="${esc(n.id)}" data-severity="${esc(n.severity || "unknown")}"
      tabindex="0" role="button" aria-label="${esc(nodeTitle(n, spoken))}">
      ${sev.beats ? `<circle class="mesh-beat" r="${r.toFixed(1)}"/>` : ""}
      <circle class="mesh-halo" r="${(r + 6).toFixed(1)}"/>
      ${prov.ring ? `<circle r="${(r + 4).toFixed(1)}" fill="none" stroke="${style.stroke}" stroke-width="1" opacity="0.55"/>` : ""}
      ${bodyElement(shapeForNode(n, spoken), r,
        `fill="${prov.ghost ? "none" : style.fill}" stroke="${sev.stroke || style.stroke}" ` +
        `stroke-width="${sev.stroke ? 3 : 2.2}" ${style.dashed || prov.ghost ? 'stroke-dasharray="4 3"' : ""}`)}
      <circle class="mesh-pin" r="4" cx="${(-r * 0.72).toFixed(1)}" cy="${(r * 0.72).toFixed(1)}"/>
      ${n.children ? `<text class="mesh-count" text-anchor="middle" dy="4">${n.children}</text>` : ""}
      ${badge}
      <g class="mesh-caption" data-room="${(r + 8).toFixed(1)}">
      <text class="mesh-label" text-anchor="middle" dy="${(r + 14).toFixed(1)}"><tspan class="mesh-label-ink">${label}</tspan></text>
      ${typed ? `<text class="mesh-type" text-anchor="middle" dy="${(r + 28).toFixed(1)}"><tspan class="mesh-label-ink">[${esc(typed.name)}]</tspan></text>` : ""}
      ${measured ? `<text class="mesh-runs mesh-runs-${esc(heat.key)}" text-anchor="middle" dy="${runsAt.toFixed(1)}"><tspan class="mesh-label-ink">${esc(heat.text(measured))}</tspan></text>` : ""}
      </g>
      <title>${esc(nodeTitle(n, spoken))}</title></g>`;
  }).join("");

  // Beating is switched on for the whole canvas rather than per node, so the budget
  // is one decision about this picture instead of a rule each node applies to itself.
  const beating = nodes.filter((n) => (SEVERITY[n.severity] || {}).beats).length;

  // The viewBox starts as the whole world, because that is what fitToFrame put the
  // content inside. The world carries the frame's own aspect ratio, so with
  // preserveAspectRatio's default there is nothing to letterbox — the opening
  // picture is the entire landscape, filling the window.
  return { ms, world, nodes, margin, svg: `<svg class="mesh-canvas${
    beating && beating <= PULSE_BUDGET ? " mesh-beating" : ""}" viewBox="0 0 ${width} ${height}"
    role="img" aria-label="Derived starmap">${defs}
    <g class="mesh-edges">${edges}</g>${circles}</svg>` };
}


// How often this view re-reads the landscape it is drawing (ADR-0211 §7).
//
// Everything on the picture has a shelf life — the severity badges are an observation,
// the incident counts move as an operator works through them, and all three heat
// weightings are live quantities, one of them measured against a clock — so a
// landscape left open goes quietly wrong. The failure §10's export stamp exists to
// prevent, happening on the screen the stamp was copied from.
//
// REFRESH_FLOOR is the fastest this view will ask, and it is well inside the
// granularity of every number on the picture: the age weighting's finest bucket is
// "under 2 min", so asking faster would buy nothing a reader could see.
export const REFRESH_FLOOR = 30_000;
// REFRESH_CEILING is the slowest it will settle to. Past this the freshness line is
// doing the honest work and the polling is only noise.
export const REFRESH_CEILING = 5 * 60_000;
// DERIVE_SHARE is the fraction of a derive's cost this view is willing to be. The mesh
// is built on the run loop and the size budget (§7) exists because that is not free,
// so the cadence is a *multiple of what the last one actually cost* rather than a
// constant somebody guessed: a landscape that derives in 40 ms is re-read on the
// floor, and one that takes four seconds backs off to well over a minute on its own. A
// fixed interval would be exactly wrong on the estates where it mattered most.
const DERIVE_SHARE = 0.05;

// refreshEvery is that cadence, in milliseconds.
//
// `derivedMs` is measured as the round trip rather than as the server's own time,
// which over-counts by the network — and erring toward asking less often is the right
// direction to be wrong in.
//
// A failed attempt goes straight to the ceiling rather than retrying on the floor: a
// server that is down does not want thirty requests a minute from every open tab, and
// the freshness line is already saying the picture is not being kept up. A derive that
// could not be measured at all falls to the floor, because "unknown cost" must not
// read as "free".
export function refreshEvery(derivedMs, { failing = false } = {}) {
  if (failing) return REFRESH_CEILING;
  const paced = Number.isFinite(derivedMs) && derivedMs > 0 ? derivedMs / DERIVE_SHARE : 0;
  return Math.min(REFRESH_CEILING, Math.max(REFRESH_FLOOR, paced));
}

// rankingHTML answers "where is the risk on this landscape" without a selection.
//
// Impact analysis has always needed one, which quietly assumes the reader already
// suspects the right node. Before a change, or on an instance somebody has just
// been handed, that assumption is exactly wrong: the question is which node would
// hurt most, and the only way to answer it by clicking was to click all of them.
//
// It follows the direction and depth controls rather than fixing its own, so this
// list and the panel are always answering the same question. Two blast-radius
// numbers on one page that were measured differently would be worse than one.
//
// And it follows the *weighting* for the same reason one step up: with a heat on, the
// canvas ranks the estate by a tally, and a column beside it ranking by blast radius
// would be a second ordering nobody asked for. The reach then becomes the second
// number on a row rather than the first — see heatRanking.
function rankingHTML(graph, direction, depth, heat = null, at = Date.now()) {
  const reach = depth === Infinity ? "any" : depth;
  const sub = direction === "dependencies" ? "how much each one needs to work"
    : direction === "both" ? "how much each one is connected to"
    : "how much stops if this one does";
  const who = (r) => esc(r.name ||
    String((KIND[r.kind] || {}).label || r.kind || r.id).split(" — ")[0]);
  const shell = (heading, body) => `<div class="mesh-rank">
    <div class="mesh-rank-head">${heading}</div>${body}</div>`;

  // With a weighting on, the list ranks by the same quantity the canvas sizes by.
  // Anything else puts two orderings on one screen and leaves the reader to work out
  // that they are deliberate.
  if (heat) {
    const rows = heatRanking(graph, heat, { direction, depth, at });
    if (!rows.length) {
      // On the incident weighting this is the good news, and it has to read as an
      // answer rather than as an empty list — the same argument the flat canvas makes.
      return shell(`<b>${esc(heat.rankHeading)}</b>`,
        `<p class="mesh-note">${heat.rankEmpty}</p>`);
    }
    return shell(
      `<b>${esc(heat.rankHeading)}</b>
       <span class="muted">${esc(heat.rankSub)}, and ${esc(sub)}, within
         ${esc(reach)} hop(s)</span>`,
      `<ol class="mesh-rank-list">${rows.map((r) => `<li>
        <button type="button" class="mesh-rank-go mesh-sev-${esc(r.severity || "unknown")}"
          data-finding="${esc(r.id)}">
          <span class="mesh-rank-who">${who(r)}</span>
          <span class="mesh-rank-count">${heat.rich(r.value)}<span
            class="muted"> · ${r.complete ? "" : "at least "}${r.total} node(s)</span></span>
        </button></li>`).join("")}</ol>`);
  }

  const rows = blastRanking(graph, { direction, depth });
  const heading = direction === "dependencies" ? "Most dependent"
    : direction === "both" ? "Most entangled" : "Biggest blast radius";

  if (!rows.length) {
    // Said as a fact about the edges rather than as reassurance: a landscape whose
    // processes call nothing has no blast radius to rank, and that is not the same
    // as a safe one.
    return shell(`<b>${esc(heading)}</b>`,
      `<p class="mesh-note">Nothing here depends on anything else within ${esc(reach)}
      hop(s), so there is no radius to rank. Containment is not counted: an
      application holds its processes, it does not depend on them.</p>`);
  }
  return shell(
    `<b>${esc(heading)}</b>
     <span class="muted">${esc(sub)}, within ${esc(reach)} hop(s)</span>`,
    `<ol class="mesh-rank-list">${rows.map((r) => `<li>
      <button type="button" class="mesh-rank-go mesh-sev-${esc(r.severity || "unknown")}"
        data-finding="${esc(r.id)}">
        <span class="mesh-rank-who">${who(r)}</span>
        <span class="mesh-rank-count">${r.complete ? "" : "at least "}<b>${r.total}</b>
          node(s)<span class="muted"> · ${r.direct} direct</span></span>
      </button></li>`).join("")}</ol>`);
}

// findingsHTML lists every node with something wrong with it, worst first.
//
// The picture already says which nodes those are, and on a landscape of four hundred
// circles that is not the same as being able to read them: finding three red dots
// means hunting, and hunting is what somebody does instead of noticing. The list is
// the same findings as an index — the count, the state, and the sentence behind it —
// and clicking one goes there.
//
// It counts incidents where there are incidents to count. An incident belongs to a
// token and only a process has tokens, so a node without a count is a node that
// cannot have one rather than a node with none: the two are different facts and the
// list never renders them alike.
function findingsHTML(graph) {
  const found = graph.nodes
    .filter((n) => (SEVERITY[n.severity] || {}).beats)
    .sort((a, b) => (SEVERITY_ORDER[b.severity] - SEVERITY_ORDER[a.severity]) ||
      ((b.incidents || 0) - (a.incidents || 0)) ||
      String(a.name || a.id).localeCompare(String(b.name || b.id)));

  const incidents = found.reduce((sum, n) => sum + (n.incidents || 0), 0);
  // The list describes the picture, so a filtered picture gets a filtered list — and
  // then has to say so. "Findings" over a landscape showing one node in seven would
  // otherwise read as the findings, which is a claim about the six that are not
  // there.
  const scope = graph.matched ? " in the filtered starmap" : "";
  if (!found.length) {
    // Not "everything is fine": most nodes in a young landscape are unobserved, and
    // an empty findings list over an unwatched instance would be a claim nobody made.
    return `<div class="mesh-findings">
      <div class="mesh-findings-head"><b>Findings${esc(scope)}</b></div>
      <p class="mesh-note">Nothing here is reporting a problem. What is not watched is
      listed in the legend — an empty list is not the same as everything being
      well.</p></div>`;
  }
  return `<div class="mesh-findings">
    <div class="mesh-findings-head">
      <b>Findings${esc(scope)}</b>
      <span class="muted">${found.length} node(s)${incidents ? `, ${incidents} incident(s)` : ""}</span>
    </div>
    <ul class="mesh-findings-list">${found.map((n) => `<li>
      <button type="button" class="mesh-finding-go mesh-sev-${esc(n.severity)}"
        data-finding="${esc(n.id)}">
        <span class="mesh-finding-name">${esc(n.name || n.id)}</span>
        <span class="mesh-finding-state">${esc(STATE_TEXT[n.state] || n.state || "")}${
          n.incidents ? ` · ${n.incidents} incident(s)` : ""}</span>
        ${n.reason ? `<span class="mesh-finding-why">${esc(n.reason)}</span>` : ""}
      </button>${sitesHTML(n)}</li>`).join("")}</ul>
  </div>`;
}

// sinceText is how long ago something happened, in the coarsest unit that still
// says it. A landscape is read at a glance and the question behind this number is
// "did anything move lately", not "when exactly" — so minutes are rounded and
// anything past a week stops pretending to be precise.
function sinceText(nanos, now = Date.now()) {
  if (!Number.isFinite(nanos) || nanos <= 0) return "";
  const seconds = Math.max(0, Math.round((now - nanos / 1e6) / 1000));
  if (seconds < 90) return "just now";
  const minutes = Math.round(seconds / 60);
  if (minutes < 90) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 36) return `${hours} h ago`;
  return `${Math.round(hours / 24)} d ago`;
}

// runtimeHTML is what the engine has recorded about a process: how much is live,
// how much has been, and whether anything has happened lately.
//
// It is stated for whichever node is selected whether or not the canvas is showing
// counts, and the zero is stated: "nothing is running here" is an answer, and it is
// the one the canvas deliberately does not have room to give four hundred times.
//
// Finished is a lifetime total rather than a rate, and says so — a number that only
// ever grows is misread as throughput otherwise.
function runtimeHTML(node) {
  const rt = node?.runtime;
  if (!rt) return "";
  const last = sinceText(rt.lastActivity);
  // "Never started" is claimed only where nothing has run *and* nothing has
  // finished. A definition with a lifetime total and no timestamp is one this build
  // has no activity clock for, and saying it was never started beside seven finished
  // instances would be a contradiction the reader has to resolve. Then it says
  // nothing, which is what it knows.
  const never = !last && rt.running === 0 && rt.finished === 0;
  return `<div class="mesh-runtime">
    <span class="mesh-runtime-now"><b>${fmtCount(rt.running)}</b> running</span>
    <span class="muted"><b>${fmtCount(rt.finished)}</b> finished, all time</span>
    ${last ? `<span class="muted">last activity ${esc(last)}</span>` : ""}
    ${never ? `<span class="muted">never started</span>` : ""}
  </div>`;
}

// sitesHTML lists where in a process the parked work actually is.
//
// "Three tokens are parked" says there is a problem; "three on the service task
// charge-card, and the last one said 502 Bad Gateway" says where to go. The element
// is named by its BPMN id and type rather than by a label, because only user tasks
// carry a title in a compiled process — an identifier that is sometimes there is
// worse than one that is always there — and because that is what Operations shows,
// so the two name the same thing the same way.
function sitesHTML(node) {
  if (!Array.isArray(node?.sites) || !node.sites.length) return "";
  return `<ul class="mesh-sites">${node.sites.map((site) => `<li>
    <span class="mesh-site-where">
      ${site.elementType ? `<span class="muted">${esc(site.elementType)}</span> ` : ""}
      <code>${esc(site.elementId)}</code>
      ${site.count > 1 ? `<span class="mesh-site-count">${site.count}×</span>` : ""}
    </span>
    ${site.message ? `<span class="mesh-site-why">${esc(site.message)}</span>` : ""}
  </li>`).join("")}</ul>`;
}

// SEVERITY_ORDER ranks the classes for the findings list. It is the same order the
// server aggregates by (ADR-0211 §4) — unknown below ok, because a node nothing
// observes is unobserved rather than well — and it is here only to sort a list the
// server does not sort.
const SEVERITY_ORDER = { critical: 3, attention: 2, ok: 1, unknown: 0 };

// shortLabel is a node in one word. A restricted placeholder has no name to give —
// that is the point of it — so it falls back to what its kind is called.
const shortLabel = (n) => n.name ||
  String((KIND[n.kind] || {}).label || n.kind || n.id).split(" — ")[0];

// The three pieces an impact answer is made of, written once because two panels now
// render them: one node's radius, and a maintenance window's. Two copies would have
// drifted the first time one of them was worded better.

// impactMixHTML is how bad the radius already is, and how much of it is one edge
// away. The mix is read for triage and never as cause: these nodes report their own
// state, and a panel implying the selection produced it would be wrong the first
// time somebody checked — so `cause` names what is *not* being claimed.
function impactMixHTML(summary, cause) {
  if (!summary || !summary.total) return "";
  const classes = ["critical", "attention", "ok", "unknown"].filter((k) => summary.bySeverity[k]);
  return `
    <div class="mesh-impact-mix">${classes.map((k) => `
      <span class="mesh-impact-chip mesh-sev-${k}"><b>${summary.bySeverity[k]}</b>
        ${esc(SEVERITY[k].label.split(" — ")[0].toLowerCase())}</span>`).join("")}
    </div>
    <p class="mesh-note"><b>${summary.direct}</b> directly, <b>${summary.indirect}</b>
      further out. What those nodes report is their own state — this says what the
      blast radius currently looks like, not what ${esc(cause)} caused.</p>`;
}

// impactListHTML names the nodes the count is counting, so the answer can be acted
// on rather than only repeated.
function impactListHTML(graph, result, startId, summary) {
  const named = graph && result ? impactList(graph, result, startId) : [];
  if (!named.length) return "";
  return `
    <ul class="mesh-impact-list">${named.map((n) => `<li>
      <button type="button" class="mesh-impact-go mesh-sev-${esc(n.severity || "unknown")}"
        data-finding="${esc(n.id)}">
        <span class="mesh-impact-who">${esc(shortLabel(n))}</span>
        <span class="mesh-impact-hop">${n.direct ? "directly" : "further out"}${
          n.state ? ` · ${esc(STATE_TEXT[n.state] || n.state)}` : ""}</span>
      </button></li>`).join("")}</ul>
    ${summary && summary.total > named.length
      ? `<p class="mesh-note">${summary.total - named.length} more, worst first above.</p>`
      : ""}`;
}

// An answer that stopped at a permission boundary must not read as a complete one.
// This is the rule the mesh applies to the picture, applied to the analysis over it:
// the count is a floor, not a total.
function truncationHTML(result) {
  if (!result || result.complete) return "";
  return `<p class="mesh-note mesh-truncated"><b>Incomplete.</b> The walk stopped at
    ${result.truncatedBy.length} node(s) outside your access, so there may be more
    beyond them. Treat the count as a lower bound.</p>`;
}

// impactPanelHTML states the answer in words beside the picture. The counts are the
// point — a highlighted subgraph tells you *which*, a count tells you *how many*,
// and "17 things depend on this worker" is the sentence somebody repeats in a
// change-approval meeting.
function impactPanelHTML(node, result, direction, depth,
  { pinned = false, graph = null, notation = null } = {}) {
  if (!node) {
    return `<div class="mesh-panel mesh-panel-empty">
      <b>Nothing selected</b>
      <p>Select a node to see what depends on it, and what it depends on.</p>
      <p>Hold ${modifierName()} while clicking to plan a maintenance window over
        several nodes at once.</p></div>`;
  }
  const typed = typeIn(node.kind, notation);
  const kindLabel = typed
    ? `${typed.name} · ${((KIND[node.kind] || {}).label || node.kind).split(" — ")[0]}`
    : (KIND[node.kind] || {}).label || node.kind;
  const others = result ? result.nodes.length - 1 : 0;
  const word = direction === "dependents" ? "depend on this" : "are needed by this";
  // The link the double-click duplicates. Every kind that has an elsewhere gets one:
  // a gesture you have to be told about is one most readers never find, so the panel
  // says in words what the double-click does without being asked.
  const inside = hrefFor(node);
  const drill = inside
    ? `<a class="mesh-drill" href="${inside}">Open in ${insideName(node)} →</a>`
    : "";
  // Releasing one hand-placed node lives here, beside the node it is about. It used
  // to be a double-click, which is a thing you have to be told; a button on the
  // thing itself is a thing you can see.
  const release = pinned
    ? `<button type="button" class="mesh-unpin" data-unpin="${esc(node.id)}">
        Release this node</button>`
    : "";
  // An answer that stopped at a permission boundary must not read as a complete
  // one. This is the same rule the mesh applies to the picture, applied to the
  // analysis over it: the count below is a floor, not a total.
  // How bad, and how close — the two things a count alone leaves out. The mix is
  // read for triage and never as cause: these nodes report their own state, and a
  // panel that implied this one produced it would be wrong the first time somebody
  // checked.
  const summary = graph && result ? impactSummary(graph, result, node.id) : null;
  const mix = impactMixHTML(summary, "this node");
  const list = impactListHTML(graph, result, node.id, summary);
  const truncation = truncationHTML(result);
  // The finding, in words, above the impact count. A node's colour says which class
  // it is in; only the state and the reason say what to do about it, and where the
  // severity was inherited the panel names the descendant it came from — a red
  // parent that cannot say which child is red is not actionable (ADR-0211 §4).
  const sev = SEVERITY[node.severity] || SEVERITY.unknown;
  const inherited = node.severityFrom
    ? `<span class="muted"> — inherited from ${esc(node.severityFrom)}</span>`
    : "";
  // How long this has been wrong, beside how much of it there is. The reason above
  // says "12 token(s) are parked"; whether they parked five minutes ago or on Friday
  // is what decides whether somebody restarts a worker or opens the process — and it
  // is the one thing a count can never say. Only where the engine recorded the moment:
  // an incident raised before it did is a gap in the record, not an old one.
  const parkedSince = node.oldestIncident > 0
    ? `<p class="mesh-parked-since muted">Oldest still parked
        ${esc(sinceText(node.oldestIncident))}.</p>`
    : "";
  const finding = `<div class="mesh-finding mesh-sev-${esc(node.severity || "unknown")}">
      <b>${esc(sev.label.split(" — ")[0])}</b>
      <span class="muted">${esc(STATE_TEXT[node.state] || node.state || "unbound")}</span>
      ${node.reason ? `<p>${esc(node.reason)}${inherited}</p>` : ""}
      ${parkedSince}
      ${sitesHTML(node)}
    </div>`;
  return `<div class="mesh-panel">
    <div class="mesh-panel-head">
      <b>${esc(node.name || kindLabel)}</b>
      <span class="muted">${esc(kindLabel)}</span>
    </div>
    ${finding}
    ${runtimeHTML(node)}
    <div class="mesh-impact-count"><b>${others}</b> node(s) ${word}
      <span class="muted">within ${depth === Infinity ? "any" : depth} hop(s)</span></div>
    ${mix}
    ${truncation}
    ${list}
    ${drill}
    ${release}
    <p class="mesh-note">Hold ${modifierName()} and click another node to plan a
      maintenance window over both — their blast radii overlap, and the window says by
      how much.</p>
  </div>`;
}

// modifierName is the key this platform expects for "add to the selection". Naming
// the wrong one is worse than naming none: a reader who tries Ctrl on a Mac and gets
// a context menu concludes the feature is broken rather than that the hint was.
function modifierName() {
  const platform = globalThis.navigator?.platform || "";
  const agent = globalThis.navigator?.userAgent || "";
  return /Mac|iPhone|iPad/.test(`${platform} ${agent}`) ? "⌘" : "Ctrl";
}

// windowPanelHTML answers the question a maintenance window actually asks: not what
// each of these breaks, but what the evening costs.
//
// The two numbers are on screen together on purpose. "One at a time these come to
// 28; together 19" is the whole finding — it is what tells somebody that batching
// three changes into one window is nearly free, or that it is not — and either
// number alone invites the other to be guessed, always by adding. See windowOverlap
// for why the union is the thing computed and the individual totals the thing
// derived.
function windowPanelHTML(nodes, result, direction, depth, { graph = null, overlap = null } = {}) {
  const summary = graph && result ? impactSummary(graph, result, null) : null;
  const others = summary ? summary.total : 0;
  const word = direction === "dependents" ? "depend on these" : "are needed by these";
  const hops = depth === Infinity ? "any" : depth;
  const covered = new Set(overlap?.covered || []);
  const each = new Map((overlap?.each || []).map((row) => [row.id, row]));

  // What is in the window, each with what it costs on its own — because the reader
  // assembled this set one node at a time and the next question is which of them is
  // carrying the cost. The remove button is on the chip rather than in a menu: a set
  // somebody builds by clicking has to be unbuildable the same way.
  const members = nodes.map((n) => {
    const row = each.get(n.id);
    const notes = [];
    if (row) notes.push(`${row.total} on its own`);
    // A node another member already takes down. Worth saying plainly: it makes no
    // difference to the window, and somebody is about to write it into a change
    // request as though it did.
    if (covered.has(n.id)) notes.push("already down with the others");
    // Two lines in one button, the shape the findings and the impact list already
    // use: the name is what a reader scans for and must never be the part that gets
    // squeezed out when the sub-line is long.
    return `<li class="mesh-window-item">
      <button type="button" class="mesh-window-who mesh-sev-${esc(n.severity || "unknown")}"
        data-window-go="${esc(n.id)}" title="Find ${esc(shortLabel(n))} on the canvas">
        <span class="mesh-window-name">${esc(shortLabel(n))}</span>
        <span class="mesh-window-cost">${esc(notes.join(" · "))}</span>
      </button>
      <button type="button" class="mesh-window-drop" data-window-drop="${esc(n.id)}"
        aria-label="Take ${esc(shortLabel(n))} out of this window">×</button>
    </li>`;
  }).join("");

  // The comparison, stated as two totals and the reason they differ. `shared` is a
  // count of nodes rather than the arithmetic difference, which past two nodes is
  // not the size of anything (see windowOverlap).
  const compare = overlap && overlap.starts.length > 1 ? `
    <p class="mesh-note">One at a time these come to <b>${overlap.sum}</b>;
      together <b>${overlap.total}</b>.
      ${overlap.shared
        ? `<b>${overlap.shared}</b> node(s) sit in more than one radius, which is
           where the difference is.`
        : `Nothing is in two of these radii, so the window costs each of them in
           full.`}</p>` : "";

  return `<div class="mesh-panel">
    <div class="mesh-panel-head">
      <b>Maintenance window</b>
      <span class="muted">${nodes.length} node(s) going down together</span>
    </div>
    <ul class="mesh-window-list">${members}</ul>
    <div class="mesh-impact-count"><b>${others}</b> node(s) ${word}
      <span class="muted">within ${hops} hop(s)</span></div>
    ${impactMixHTML(summary, "this window")}
    ${compare}
    ${truncationHTML(result)}
    ${impactListHTML(graph, result, null, summary)}
    <p class="mesh-note">Hold ${modifierName()} and click to add or remove a node.</p>
    <button type="button" class="mesh-window-close" data-window-clear="1">Close this window</button>
  </div>`;
}

export async function mountPanoramaMesh(view, { api, toast }) {
  view.innerHTML = `<div class="card"><h1>Starmap</h1><p class="muted">Deriving…</p></div>`;
  let graph;
  const fetched = performance.now();
  try {
    // The mapping is additive: a landscape is worth drawing even when the notations
    // cannot be read, and without them the picker offers only the derived vocabulary
    // — which is the one that cannot be wrong about what it is.
    const [mesh, served] = await Promise.all([
      api("GET", "/api/v1/panorama/mesh"),
      api("GET", "/api/v1/panorama/notations").catch(() => []),
    ]);
    graph = mesh;
    useNotations(served);
  } catch (e) {
    view.innerHTML = `<div class="card empty"><h1>Starmap</h1>
      <p>${esc(e.message)}</p></div>`;
    return;
  }
  const fetchMs = performance.now() - fetched;

  // Whether drafts are on. It survives the empty check below because that check can
  // turn it on: an instance where nothing is deployed yet is one where everything is
  // a draft, and an empty picture whose only switch is on the page it refuses to draw
  // is a dead end rather than an answer.
  let withDrafts = false;
  if (!graph.nodes.length) {
    try {
      const drafted = await api("GET", "/api/v1/panorama/mesh?drafts=1");
      if (drafted.nodes.length) {
        graph = drafted;
        withDrafts = true;
      }
    } catch {
      // The empty landscape stands. A second request that failed says nothing about
      // the first one, and reporting it would replace a true answer with an error.
    }
  }

  if (!graph.nodes.length) {
    view.innerHTML = `<div class="card empty"><h1>Starmap</h1>
      <p>Nothing is deployed on this server yet, and there are no saved diagrams
      either. The landscape is derived from what Atlas holds, so it fills in as you
      draw and deploy — there is nothing to model first.</p></div>`;
    return;
  }

  view.innerHTML = `<div id="mesh-root" class="card mesh-card">
    <div class="mesh-head">
      <h1>Starmap</h1>
      <input id="mesh-search" type="search" class="mesh-search" autocomplete="off"
        placeholder="Filter by name, kind or process id…" aria-label="Filter the starmap"/>
      <!-- Going *into* the selected node, as a control rather than only as a
           double-click. A gesture you have to be told about is one most readers never
           find, and the drilldown is the thing this view is for once the landscape is
           bigger than a screenful. -->
      <button id="mesh-drill-in" type="button" class="mesh-drill-in" disabled
        aria-label="Zoom into the selected node"
        title="Zoom into the selected node and what it touches">→</button>
      <!-- The path taken through the estate: every node gone into, in order, each
           one a way back to it. "All" is the first station, so leaving is a step
           like any other rather than a separate escape hatch. -->
      <nav id="mesh-drill-trail" class="mesh-trail" aria-label="Where you are" hidden></nav>
      <!-- How the picture is drawn. Beside the picture rather than in the side column,
           because it changes the drawing rather than the answer about it (ADR-0211
           §8) — and it is one list rather than a list plus a switch, because every
           entry on it spends the same channels on a different question and only one
           of them can be answered at a time. -->
      <label class="mesh-notation" for="mesh-notation">Notation</label>
      <select id="mesh-notation" class="mesh-notation-pick"
        title="How this landscape is drawn: Atlas's own kinds, sized by what is running, by what is stuck or by how long it has been stuck, or projected into another vocabulary">${notationsAvailable()
        .map((n) => `<option value="${esc(n.id)}">${esc(n.label)}</option>`).join("")}</select>
      <!-- Saved diagrams nobody has deployed. Off by default, and the one control here
           that re-asks the server rather than re-drawing what is already on screen:
           the drafts are not in the payload until they are wanted, because an estate
           holds several of them per deployed process and carrying them always would
           spend the size budget (ADR-0211 §7) on work that is not running. -->
      <label class="mesh-toggle" title="Also draw saved diagrams that have not been deployed">
        <input id="mesh-drafts" type="checkbox"/> Drafts
      </label>
      <!-- Beside the picture's own controls rather than in the side column: what is
           exported is the picture, including whatever the search box and the
           drilldown have done to it. -->
      <span class="mesh-export" role="group" aria-label="Export this starmap">
        <button id="mesh-export-svg" type="button"
          title="Save this starmap as an SVG, stamped with when and where it was observed">SVG</button>
        <button id="mesh-export-png" type="button"
          title="Save this starmap as a PNG, stamped with when and where it was observed">PNG</button>
        <!-- Generated by the server from the same landscape, so it is the whole of it
             rather than whatever the search box has narrowed this picture to — and it
             carries structure only, never health. A plain navigation, so the session
             cookie authenticates it, exactly as the application source download does. -->
        <button id="mesh-export-archimate" type="button"
          title="Download the whole starmap as an ArchiMate Open Exchange model — structure only, generated, not drawn">ArchiMate XML</button>
      </span>
    </div>
    <!-- How much of the landscape is on screen, on its own line under the controls.
         It was in the head, where it was the one thing with no fixed size: every
         control kept its width and the sentence was squeezed into a column one word
         wide. It describes the picture rather than acting on it, so it reads better
         under the row that does. -->
    <div class="mesh-subhead">
      <!-- When the server read this landscape, always, because the picture claims to
           be live: the severity badges, the incident counts and the three weightings
           are all facts with a shelf life, and an undated one that looks current is
           the failure the export's stamp already exists to prevent. On screen there
           was nothing saying it — a landscape opened at nine and still open at eleven
           showed two-hour-old numbers with no hint of it. -->
      <span id="mesh-observed" class="muted"></span>
      <!-- And the switch that keeps it true. On by default, because a stale status
           view that looks live is worse than a picture that moves: what it costs is a
           derive on the run loop every half minute, and what it buys is that the
           sentence beside it stays "just now". Off is for reading one picture
           carefully — a canvas that re-lays-out under a reader mid-thought is its own
           kind of wrong. -->
      <label class="mesh-toggle mesh-live" title="Re-read the landscape from the server while this view is open">
        <input id="mesh-live" type="checkbox" checked/> Live
      </label>
      <span class="mesh-subhead-gap"></span>
      <span id="mesh-count" class="muted"></span>
    </div>
    <div class="mesh-body">
      <div class="mesh-plot">
        <!-- The stage is the canvas and the controls that float over it, and nothing
             else: the surface's contents are replaced on every repaint, so anything
             inside it is drawn once and then gone. -->
        <div class="mesh-stage">
          <div id="mesh-surface" class="mesh-surface"></div>
          <div class="mesh-zoom" role="group" aria-label="Zoom">
            <button id="mesh-zoom-in" type="button" title="Zoom in">+</button>
            <button id="mesh-zoom-out" type="button" title="Zoom out">−</button>
            <button id="mesh-zoom-fit" type="button" title="Fit the whole starmap">Fit</button>
            <button id="mesh-release" type="button" disabled
              title="Put every node you have dragged back where the layout puts it">Release</button>
          </div>
        </div>
        <!-- The key sits under the picture: it is a reference, consulted while looking
             at the canvas rather than read on the way to it, and above the picture it
             pushed the thing it explains down the page. Under the canvas and not under
             the whole body, so it stays against the picture rather than below
             whichever of the two columns happens to be taller. -->
        <div id="mesh-legend-slot"></div>
      </div>
      <aside class="mesh-side">
        <div class="mesh-controls">
          <!-- Explicit for/id rather than a wrapping label: a select nested inside
               its label takes the option text into its accessible name, which makes
               the control hard to address by name for a screen reader and for a test
               alike. -->
          <label for="mesh-direction">Show</label>
          <select id="mesh-direction">
            <option value="dependents">what depends on it</option>
            <option value="dependencies">what it depends on</option>
            <option value="both">both directions</option>
          </select>
          <!-- How far the walk reaches: a number somebody types, or the whole graph.
               It was a three-option list — 1 hop, 2 hops, all — which answers the
               question at exactly two depths and refuses every other one. A reader
               asking "and one further?" had nothing to press. Two inputs, because
               there are two kinds of answer: a distance, and "however far it goes". -->
          <label for="mesh-depth">Depth</label>
          <div class="mesh-depth">
            <input id="mesh-depth" type="number" min="1" max="99" step="1" value="2"
              inputmode="numeric" aria-label="How many hops the walk reaches"/>
            <label class="mesh-depth-all">
              <input id="mesh-depth-any" type="checkbox"/> all
            </label>
          </div>
        </div>
        <div id="mesh-panel-slot"></div>
        <div id="mesh-findings-slot"></div>
        <div id="mesh-ranking-slot"></div>
        <div class="mesh-views">
          <div class="mesh-views-head">
            <b>Saved views</b>
            <!-- Said plainly rather than discovered later: these live in this
                 browser, and are not shared and do not follow you to another one. -->
            <span class="muted">this browser only</span>
          </div>
          <ul id="mesh-view-list" class="mesh-view-list"></ul>
          <form id="mesh-view-save" class="mesh-view-save">
            <input id="mesh-view-name" type="text" maxlength="60" autocomplete="off"
              placeholder="Name this view…" aria-label="Name for the saved view"/>
            <button type="submit">Save</button>
          </form>
          <p id="mesh-view-note" class="mesh-note" hidden></p>
        </div>
      </aside>
    </div>
  </div>`;

  const search = document.getElementById("mesh-search");
  const drillTrail = document.getElementById("mesh-drill-trail");
  const drillIn = document.getElementById("mesh-drill-in");
  const surface = document.getElementById("mesh-surface");
  const zoomIn = document.getElementById("mesh-zoom-in");
  const zoomOut = document.getElementById("mesh-zoom-out");
  const zoomFit = document.getElementById("mesh-zoom-fit");
  const release = document.getElementById("mesh-release");
  const draftsToggle = document.getElementById("mesh-drafts");
  // The card this mount painted, which is not the element it was handed: the router
  // owns that one and reuses it for every route, replacing what is inside it. So it
  // is this node, not `view`, that stops being in the document when the reader leaves
  // — and the timer below has nothing else to notice its own view is gone by.
  const root = document.getElementById("mesh-root");
  const liveToggle = document.getElementById("mesh-live");
  const observed = document.getElementById("mesh-observed");
  // What the freshness line and the cadence below are computed from. Seeded from the
  // fetch that opened the view, so the first tick reasons about a real request rather
  // than about nothing.
  //
  // Two clocks, and they are two because they answer different questions. `fetchedAt`
  // is when the picture on screen was *read*, and only a success moves it — it is what
  // the freshness sentence falls back to, so a failed attempt advancing it would have
  // a stale picture claiming to be current, which is the one thing this line exists to
  // prevent. `attemptedAt` is when the last attempt *ended*, success or not, and it is
  // what the cadence measures from: without it a sustained outage never advances the
  // clock at all, so the elapsed time grows past the ceiling and stays there, and the
  // back-off refreshEvery promises turns into a retry on every tick — the opposite of
  // what it is for.
  let fetchedAt = Date.now();
  let attemptedAt = fetchedAt;
  let derivedMs = fetchMs;
  let failing = false;
  let refreshing = false;
  // Set from what was actually fetched rather than left at its markup default, so the
  // control agrees with the picture on the first frame as well as on every later one.
  draftsToggle.checked = withDrafts;
  const legendSlot = document.getElementById("mesh-legend-slot");
  const count = document.getElementById("mesh-count");
  const panel = document.getElementById("mesh-panel-slot");
  const dirSelect = document.getElementById("mesh-direction");
  const depthField = document.getElementById("mesh-depth");
  const depthAny = document.getElementById("mesh-depth-any");

  // depthValue is the control read as one answer, in the spelling everything
  // downstream already speaks: "all", or a number of hops as a string. Keeping the
  // old vocabulary is deliberate — the saved views, the export stamp and the header
  // count all carry it, and a stored view written last week has to keep meaning what
  // it meant.
  //
  // A field somebody is mid-edit in can hold anything, empty included, so it is read
  // through a floor rather than trusted: a walk of zero hops is a picture of one
  // node with no dependencies drawn, which is a broken answer rather than a narrow
  // one.
  const depthValue = () => (depthAny.checked ? "all" : String(depthOf(depthField.value)));
  const depthHops = () => (depthAny.checked ? Infinity : depthOf(depthField.value));
  // Setting it from the other direction: a saved view, or a default.
  function setDepth(value) {
    const all = value === "all" || value === Infinity;
    depthAny.checked = all;
    if (!all) depthField.value = String(depthOf(value));
    depthField.disabled = all;
  }
  const findingsSlot = document.getElementById("mesh-findings-slot");
  const rankingSlot = document.getElementById("mesh-ranking-slot");
  const viewList = document.getElementById("mesh-view-list");
  const viewForm = document.getElementById("mesh-view-save");
  const viewName = document.getElementById("mesh-view-name");
  const viewNote = document.getElementById("mesh-view-note");
  const notationPick = document.getElementById("mesh-notation");
  // Which quantity the picture currently sizes its nodes by, if any. Asked of the
  // picker rather than remembered beside it, so the canvas, the key, the saved view
  // and the export stamp cannot end up describing different pictures.
  const weighted = () => heatOf(notationPick.value);
  // The moment the current picture was measured at. A duration weighting is read
  // against a clock, and the canvas, the key and the ranking have to be three
  // readings of one instant — so paint() takes it once and everything drawn from that
  // pass uses it, including the ranking, which is painted separately.
  let measuredAt = Date.now();
  const exportSvgBtn = document.getElementById("mesh-export-svg");
  const exportModelBtn = document.getElementById("mesh-export-archimate");
  const exportPngBtn = document.getElementById("mesh-export-png");

  // The selection is a list rather than one id, because a maintenance window is a
  // question about several nodes at once and the union of their blast radii is not
  // the sum (see windowOverlap). One entry is the ordinary case and reads exactly as
  // it did; the panel changes shape at two.
  let picked = [];
  const only = () => (picked.length === 1 ? picked[0] : null);
  // trail is the path of nodes somebody has gone into, in the order they went. Empty
  // is the whole starmap; the last entry is where they are standing now.
  //
  // A drilldown is a *place to stand*, not a filter over names: it is the node
  // somebody went into and whatever is within the depth already on screen. Going in
  // again from there is the point — the picture recentres on the new node, and the
  // way somebody got there is the path they followed through the estate. That is
  // what makes this a trail rather than a flag: the graph is re-cut from the current
  // node every time, so the sequence is history, and history is what lets a reader
  // step back one node instead of all the way out.
  //
  // It and the search box are two ways of asking the same kind of question, so only
  // one of them is ever in force — entering one clears the other, because two
  // narrowings compounding invisibly is how a picture ends up showing something
  // nobody asked for and nobody can undo.
  let trail = [];
  const drilledAt = () => (trail.length ? trail[trail.length - 1] : null);
  // bandAt is the mark on the scale in the key the reader has chosen, as that mark's
  // own tally — 0 for the nothing-at-all circle, null for none chosen.
  //
  // The tally rather than the mark's position, because the marks are derived from the
  // landscape and a landscape whose peak has moved has different marks: a tally is
  // still the same question afterwards where an index is a different one. paint()
  // checks it against the marks it just computed and lets go of it when it is no
  // longer one of them.
  //
  // It narrows the picture the way the search box does — same walk, same context, and
  // an intersection when both are in force — because they are two ways of asking the
  // same kind of question and answering them differently would be two filters a
  // reader has to hold apart.
  let bandAt = null;
  // pinned holds every node somebody has dragged, by id, at the world coordinates
  // they dropped it on. It is the whole of the arrangement: the layout reads it on
  // every paint, so a hand-placed node survives filtering, selecting and resizing —
  // and clearing this map is all that "Release" has to do.
  //
  // It deliberately keeps entries for nodes a filter has hidden. A search is a
  // temporary question, and losing your arrangement by asking one would make the
  // arrangement not worth making.
  const pinned = new Map();
  // placed is the laid-out graph — the same node objects the SVG was rendered from,
  // with live coordinates. A drag mutates these and writes the result straight into
  // the DOM, so the picture follows the pointer without a re-render.
  let placed = [];
  let at = new Map();
  let shown = graph;
  let nodeEls = new Map();
  let edgeEls = [];
  // frame is the drawing surface in its own units, taken from the element rather
  // than assumed, so the layout settles into the shape the viewer actually has.
  // frameView is the part of it currently on screen; null means fitted, which is where
  // every paint starts — the opening picture is the whole landscape.
  let frame = { width: 1200, height: 720 };
  // laidOut is the box the node positions on screen were actually settled for, which
  // is not the same thing as the box the picture is currently framed for. Reframing
  // is cheap and happens the instant the surface moves; re-settling the graph is the
  // expensive part and is debounced — so the two drift apart for a moment, and the
  // repaint has to be owed against the frame the *layout* used rather than against
  // the one the view was last written for. Comparing against the wrong one is how a
  // re-layout gets marked "already done" and the picture keeps a shape it was never
  // laid out in.
  let laidOut = frame;
  let frameView = null;
  // world is the box the graph was actually laid out in, and the base view is that
  // box rather than the frame: the frame is a window, not the canvas.
  let world = { width: 1200, height: 720 };

  // frameNow is the box the picture has to be laid out for, right now.
  //
  // The floor matters and is not defensive padding: a surface the browser has not
  // laid out yet reports zero, and a world of zero area is not a picture. What the
  // floor cannot do is be *right* — 320x280 is nearly square where the canvas is
  // wide, and the world takes the frame's aspect, so a picture laid out against the
  // floor is letterboxed into a column when it finally gets its box. That is what
  // the observer below exists to correct.
  function frameNow() {
    return {
      width: Math.max(surface.clientWidth || 0, 320),
      height: Math.max(surface.clientHeight || 0, 280),
    };
  }
  function measure() {
    frame = frameNow();
  }

  // reframed re-measures the surface and, when it has moved, frames the picture for
  // the box it actually has. Returns whether anything changed.
  //
  // This is the guarantee that does not depend on being notified. fitView returns a
  // view with the frame's own aspect ratio, so a viewBox written for a frame the
  // surface no longer has is a viewBox of the wrong shape — and preserveAspectRatio
  // letterboxes that, which shrinks the whole drawing and huddles it into the middle
  // of the canvas with empty bands beside it. That is the picture reported as "the
  // nodes are too close together", and until now it could only be undone by a
  // re-layout: nothing else measured. So nothing put it right if the notification
  // that a re-layout was needed never arrived, or arrived while the re-layout was
  // still being postponed.
  //
  // Reframing costs no simulation — a bounding box and a division — so it can happen
  // on every view write, which is far more often than the graph is settled.
  function reframed() {
    const now = frameNow();
    if (Math.abs(now.width - frame.width) < 1 && Math.abs(now.height - frame.height) < 1) return false;
    frame = now;
    // A reader who has zoomed in keeps their magnification and what they are looking
    // at; only the shape of the window changed, so only the height follows from it.
    if (frameView) {
      const cy = frameView.y + frameView.h / 2;
      const h = frameView.w * (now.height / Math.max(now.width, 1));
      frameView = { x: frameView.x, y: cy - h / 2, w: frameView.w, h };
    }
    refit();
    return true;
  }

  function applyView() {
    const svg = surface.querySelector("svg");
    if (!svg) return;
    reframed();
    const v = frameView || baseView();
    svg.setAttribute("viewBox", `${v.x.toFixed(2)} ${v.y.toFixed(2)} ${v.w.toFixed(2)} ${v.h.toFixed(2)}`);
    svg.classList.toggle("mesh-zoomed", frameView !== null);
    // Screen pixels per world unit, which is what decides whether a name can be
    // read. Toggling a class is the whole of it: names appear and disappear as the
    // view moves, with nothing re-rendered and no layout recomputed.
    const tier = labelTier(frame.width / Math.max(v.w, 1));
    svg.classList.toggle("mesh-names-all", tier === "all");
    svg.classList.toggle("mesh-names-anchors", tier === "anchors");
  }

  // chromeReserve is how much of the canvas the zoom panel floats over, measured
  // rather than assumed: it holds a different number of buttons depending on what
  // the picture can do, and a hard-coded box would be wrong the next time one is
  // added. The margin is the panel's own inset plus a little air.
  function chromeReserve() {
    const panel = surface.parentElement?.querySelector(".mesh-zoom");
    const box = panel?.getBoundingClientRect();
    if (!box?.width) return { width: 0, height: 0 };
    return { width: box.width + 22, height: box.height + 22 };
  }

  // fitted is what "Fit" means: the drawn nodes, framed in the space they can
  // actually be read and reached in. Not the world — see contentBox for why the two
  // stop being the same picture the moment anything is pinned.
  //
  // It is *stored* rather than derived on demand, and that is not an optimisation.
  // Every screen-to-world conversion goes through it, and a drag moves the content
  // it is computed from — so a view recomputed per call would shift the coordinate
  // system under the pointer as the node crossed it, and the node would drift away
  // from the cursor by however much it had already moved the picture's own bounds.
  // The frame a gesture started in is the frame it finishes in; reframing is
  // something the reader asks for.
  let fitted = { x: 0, y: 0, w: 1200, h: 720 };
  // The room the current notation's labels need, handed back by the render so the
  // framing reserves exactly what the layout did. A projection's type annotation is
  // part of the picture, and a frame that cut it off would be a frame that disagreed
  // with the drawing it is showing.
  let labelMargin = LABEL_MARGIN;
  function refit() {
    fitted = placed.length
      ? fitView(contentBox(placed, labelMargin), frame, chromeReserve())
      : { x: 0, y: 0, w: world.width, h: world.height };
    return fitted;
  }
  function baseView() {
    return fitted;
  }

  // zoom keeps whatever is under `focus` under it, so the wheel behaves like a map.
  // focus is in the frame's units; omitting it zooms about the centre, which is what
  // the buttons want.
  function zoom(factor, focus) {
    const base = baseView();
    const current = frameView || base;
    const about = focus || { x: current.x + current.w / 2, y: current.y + current.h / 2 };
    frameView = zoomView(current, factor, about, base);
    applyView();
  }

  function paint() {
    const term = search.value.trim().toLowerCase();
    const hops = depthHops();
    // A drilldown onto a node that is no longer in the landscape is not an empty
    // landscape — it is a question that can no longer be asked, and saying so beats
    // drawing a blank canvas somebody would read as "everything is gone".
    // Cut from where the reader is standing. Only the last node matters to the
    // picture — the ones before it are how they got here — so a trail of six is the
    // same cost as a drilldown of one.
    const here = drilledAt();
    const drilledGraph = here ? drillInto(graph, here, hops) : null;
    if (here && !drilledGraph) {
      // The node under the reader's feet was undeployed while they stood on it. Step
      // back rather than throwing the whole path away: the way they came is still a
      // real path, and dropping it would cost them the walk as well as the node.
      trail = trail.slice(0, -1);
      toast("That node is no longer in this starmap.");
    }
    // The vocabulary, the moment and the reference, before the narrowing rather than
    // after it — the band the reader may have chosen off the scale is a criterion in
    // those terms, so they have to exist before there is anything to filter by.
    //
    // The reference the radii are drawn against comes from the whole landscape, not
    // from what the filter has left on screen: narrowing to two nodes must not make
    // the smaller of them swell into the worst thing on the estate.
    //
    // And one moment for the whole repaint, because a duration weighting measures
    // against a clock: the canvas, the key, the ranking beside them and the filter
    // that chose what is on the canvas have to be four readings of one instant, or
    // the picture disagrees with its own caption.
    const spoken = notationOf(notationPick.value);
    measuredAt = Date.now();
    const heatNow = heatOf(spoken);
    const peak = heatPeak(graph, heatNow, measuredAt);
    // The scale's marks are recomputed from the landscape on every paint, so a band
    // chosen against a peak that has since moved may no longer be a mark anybody can
    // point at. It is let go of rather than quietly filtered by: a picture narrowed by
    // a criterion with no control showing it is a picture nobody can widen again.
    const band = heatNow ? heatBand(heatScaleMarks(heatNow, peak), bandAt) : null;
    if (!band) bandAt = null;
    shown = drilledGraph || filterGraph(graph, { term, band, heat: heatNow, at: measuredAt });
    paintDrillChip();
    // A selection that the filter removed is no longer selected: highlighting a node
    // that is not on screen would leave the panel describing something invisible.
    picked = picked.filter((id) => shown.nodes.some((n) => n.id === id));

    measure();
    // Where everything currently is, so a repaint while something is pinned carries
    // the picture on screen forward instead of settling a fresh one around the pins.
    const from = new Map(placed.map((n) => [n.id, { x: n.x, y: n.y }]));
    laidOut = frame;
    const painted = renderGraph(shown, 0, frame, {
      pinned, from, notation: spoken, peak, at: measuredAt,
    });
    const { ms, svg } = painted;
    world = painted.world;
    placed = painted.nodes;
    labelMargin = painted.margin;
    at = new Map(placed.map((n) => [n.id, n]));
    surface.innerHTML = shown.nodes.length
      ? svg
      : `<p class="mesh-empty-filter">Nothing matches ${
        term ? `“${esc(term)}”` : "that"}${band && term ? " in that band" : ""}.</p>`;
    index();
    nameTheNodes();
    // The rendered SVG carries none of the hover highlight, so the record of what is
    // lit has to be cleared with it — otherwise pointing back at the same node would
    // be a no-op and the highlight would never come back.
    lit = null;
    refit();
    applyView();
    legendSlot.innerHTML = legendHTML(shown, ms, spoken, peak, bandAt);
    findingsSlot.innerHTML = findingsHTML(shown);
    paintRanking();
    // The freshness line, on every repaint as well as on every tick: a repaint that
    // followed a re-read would otherwise go on saying the picture was minutes old for
    // up to a tick after it stopped being true.
    sayObserved();
    // Matches and context counted apart. "5 of 101" over a picture where only one
    // node matched the term would be the header agreeing with the drawing and both
    // of them misreporting the search.
    const context = shown.nodes.length - (shown.matched?.size ?? shown.nodes.length);
    if (drilledAt()) {
      count.textContent = `${context} of ${graph.nodes.length} node(s) within ` +
        `${depthAny.checked ? "any" : depthValue()} hop(s)`;
    } else {
      // Two criteria, one sentence, and it names them: a count on its own over a
      // picture narrowed by a circle somebody clicked reads as a landscape that
      // shrank by itself.
      const asked = [
        term ? `“${term}”` : null,
        band ? bandPhrase(heatNow, heatScaleMarks(heatNow, peak), bandAt).toLowerCase() : null,
      ].filter(Boolean);
      count.textContent = asked.length
        ? `${shown.matched?.size ?? 0} of ${graph.nodes.length} node(s) match ` +
          `${asked.join(" and ")}` + (context ? `, ${context} shown for context` : "")
        : `${graph.nodes.length} node(s), ${graph.edges.length} edge(s)`;
    }
    refresh();
    // And check, one frame later, that the box the graph was just settled for is
    // still the box the surface has.
    //
    // A paint changes the page it is drawn on: the key goes under the picture and
    // the side panel fills with findings and a ranking, so the document gets taller,
    // and a taller document can take a scrollbar — which is fifteen pixels off the
    // width of everything, this canvas included. The frame was measured before any
    // of that, at the top of this function, so the picture can be settled for a box
    // that its own arrival destroyed. Asking again on the next frame is the cheapest
    // possible way to notice, and it does not depend on a resize notification
    // arriving, or on it arriving before something else postpones the answer.
    requestAnimationFrame(() => reframe());
  }

  // paintRanking is kept out of refresh deliberately. The ranking is about the
  // graph and the two controls, not about the selection — recomputing a walk from
  // every node each time somebody clicks a circle would spend the whole budget on an
  // answer that had not changed.
  function paintRanking() {
    rankingSlot.innerHTML = rankingHTML(
      shown, dirSelect.value,
      depthHops(), weighted(), measuredAt);
  }

  // refresh answers the impact question about the current selection and shows the
  // answer — the highlight on the picture, the counts beside it.
  //
  // It never re-lays-out, and that is the point. Selecting is the most frequent thing
  // anybody does here, and running a two-hundred-iteration simulation to answer "what
  // depends on this" was both slow at a few hundred nodes and wrong in kind: the
  // answer is about the picture on screen, so the picture must not move while it is
  // being given. The classes go on and come off exactly as the hover highlight's do.
  function refresh() {
    const direction = dirSelect.value;
    const depth = depthHops();
    const result = picked.length ? impactOf(shown, picked, { direction, depth }) : null;
    const highlight = result ? new Set(result.nodes) : null;
    const inWindow = new Set(picked);
    for (const [id, g] of nodeEls) {
      g.classList.toggle("mesh-in-impact", Boolean(highlight?.has(id)));
      g.classList.toggle("mesh-dimmed", Boolean(highlight) && !highlight.has(id));
      // Which of the lit nodes are the ones going down, as opposed to the ones that
      // go with them. Without it a window of three reads as one undifferentiated
      // blob and the reader cannot check the set they assembled against the picture.
      g.classList.toggle("mesh-picked", inWindow.has(id));
    }
    for (const line of edgeEls) {
      const inside = Boolean(highlight?.has(line.dataset.from) && highlight.has(line.dataset.to));
      line.classList.toggle("mesh-in-impact", inside);
      line.classList.toggle("mesh-dimmed", Boolean(highlight) && !inside);
    }
    // Nothing selected is nothing to go into, and being already inside a node is not
    // a place you can go into again.
    // Going into a node needs one node. Several is a window rather than a place.
    drillIn.disabled = !only() || drilledAt() === only();
    if (picked.length > 1) {
      const nodes = picked.map((id) => shown.nodes.find((n) => n.id === id)).filter(Boolean);
      panel.innerHTML = windowPanelHTML(nodes, result, direction, depth, {
        graph: shown, overlap: windowOverlap(shown, picked, { direction, depth }),
      });
      return;
    }
    panel.innerHTML = impactPanelHTML(
      shown.nodes.find((n) => n.id === only()) || null, result, direction, depth,
      { pinned: pinned.has(only()), graph: shown, notation: notationOf(notationPick.value) });
  }

  // What the panel's own buttons do. Releasing a hand-placed node lives beside the
  // node it is about; the window's do too, because a set assembled by clicking has to
  // come apart without a modifier anybody has to be told about.
  panel.addEventListener("click", (event) => {
    const target = event.target;
    const drop = target.closest?.("[data-window-drop]")?.getAttribute("data-window-drop");
    if (drop) {
      picked = picked.filter((id) => id !== drop);
      refresh();
      return;
    }
    if (target.closest?.("[data-window-clear]")) {
      picked = [];
      refresh();
      return;
    }
    const go = target.closest?.("[data-window-go]")?.getAttribute("data-window-go");
    if (go) { frameOn(go); return; }
    const id = target.closest?.("[data-unpin]")?.getAttribute("data-unpin");
    if (!id || !pinned.has(id)) return;
    pinned.delete(id);
    const node = at.get(id);
    if (node) node.held = false;
    updateRelease();
    paint();
  });

  // index caches the elements a drag writes to. Looking them up once per render and
  // then writing coordinates straight onto them is what keeps a drag at frame rate:
  // the alternative is re-rendering the whole SVG on every pointer move, which at a
  // few hundred nodes is a slideshow.
  function index() {
    const svg = surface.querySelector("svg");
    nodeEls = new Map();
    edgeEls = [];
    if (!svg) return;
    for (const g of svg.querySelectorAll(".mesh-node")) nodeEls.set(g.dataset.nodeId, g);
    edgeEls = [...svg.querySelectorAll(".mesh-edge")];
  }

  // nameTheNodes puts every name somewhere it can be read, and says which ones there
  // was no room for. See placeCaptions for why the names move rather than the nodes.
  //
  // The measuring is the only part that touches the browser: getBBox reports a text's
  // bounds in the coordinates it is drawn in, which are world units here, so one pass
  // over the captions is enough for the whole picture and no zoom or pan invalidates
  // it. Reading them all in one loop before writing anything back is deliberate —
  // interleaving reads and writes would make the browser lay the SVG out again
  // between every pair of nodes. Measured at the size budget (400 nodes): about a
  // millisecond to measure and six to place, against the few hundred the simulation
  // itself costs.
  function nameTheNodes() {
    const svg = surface.querySelector("svg");
    if (!svg) return;
    const items = [];
    for (const [id, g] of nodeEls) {
      const caption = g.querySelector(".mesh-caption");
      const n = at.get(id);
      if (!caption || !n) continue;
      // Cleared before measuring: a caption still carrying the last paint's offset
      // would be measured where it was put rather than where it starts.
      caption.removeAttribute("transform");
      items.push({ id, x: n.x, y: n.y, r: radiusOf(n), caption });
    }
    for (const it of items) it.box = it.caption.getBBox();
    const spots = placeCaptions(items);
    for (const it of items) {
      const at_ = spots.get(it.id);
      const g = nodeEls.get(it.id);
      if (at_) {
        if (at_.dx || at_.dy) {
          it.caption.setAttribute("transform", `translate(${at_.dx.toFixed(1)},${at_.dy.toFixed(1)})`);
        }
        g.classList.remove("mesh-crowded");
      } else {
        // Nowhere to write it. The name is not lost — the stylesheet still shows it
        // on hover, on focus, on selection and while the neighbourhood is lit, which
        // is the same way a name the zoom has taken away comes back.
        g.classList.add("mesh-crowded");
      }
    }
  }

  // applyPositions writes the live coordinates into the SVG that is already there.
  function applyPositions() {
    for (const [id, g] of nodeEls) {
      const n = at.get(id);
      if (n) g.setAttribute("transform", `translate(${n.x.toFixed(1)},${n.y.toFixed(1)})`);
    }
    for (const line of edgeEls) {
      const a = at.get(line.dataset.from), b = at.get(line.dataset.to);
      if (!a || !b) continue;
      // A marked line stops at the two circles, and a drag moves the circles — so the
      // trim is recomputed here rather than only at render, or the arrowhead would
      // slide under the node the moment somebody moved it. One hypot per edge, in a
      // loop the simulation already pays far more than that for.
      const p = line.dataset.trimmed
        ? trimEdge(a, b, radiusOf(a), radiusOf(b))
        : { x1: a.x, y1: a.y, x2: b.x, y2: b.y };
      line.setAttribute("x1", p.x1.toFixed(1)); line.setAttribute("y1", p.y1.toFixed(1));
      line.setAttribute("x2", p.x2.toFixed(1)); line.setAttribute("y2", p.y2.toFixed(1));
    }
  }

  // A plain click asks about one node; the same click with the platform's own
  // add-to-selection key builds a maintenance window out of several. Toggling either
  // way, because a set somebody assembles by clicking has to come apart the same way
  // — and clicking the lone selection again still clears it, which is how this
  // behaved before there was more than one.
  function select(id, { add = false } = {}) {
    if (!id) picked = [];
    else if (add) picked = picked.includes(id) ? picked.filter((x) => x !== id) : [...picked, id];
    else picked = picked.length === 1 && picked[0] === id ? [] : [id];
    refresh();
  }

  // Which bubble is connected to which, shown by pointing at one.
  //
  // Impact analysis already answers this properly — it walks the dependency edges
  // to whatever depth is asked for, and states the answer in words beside the
  // picture. But it needs a click, and it answers a bigger question than the one
  // somebody has while reading: *what is this touching?* That question is asked
  // dozens of times while scanning a landscape and deserves to cost nothing.
  //
  // So hovering a node lifts its immediate neighbours and the edges to them, and
  // lets everything else fall back. It is one hop deliberately: the transitive
  // answer is what selecting is for, and a hover that lit up half the landscape
  // would be a worse version of it rather than a different tool. Nothing is
  // re-laid-out and nothing is re-rendered — the classes go on and come off, so the
  // picture cannot move under the pointer while it is being read.
  let lit = null;
  function relate(id) {
    if (lit === id) return;
    lit = id;
    const svg = surface.querySelector("svg");
    if (!svg) return;
    svg.classList.toggle("mesh-relating", id !== null);
    const neighbours = new Set();
    for (const line of svg.querySelectorAll(".mesh-edge")) {
      const from = line.dataset.from, to = line.dataset.to;
      const touches = id !== null && (from === id || to === id);
      line.classList.toggle("mesh-related-edge", touches);
      if (touches) neighbours.add(from === id ? to : from);
    }
    for (const node of svg.querySelectorAll(".mesh-node")) {
      const nodeId = node.dataset.nodeId;
      node.classList.toggle("mesh-related", neighbours.has(nodeId));
      node.classList.toggle("mesh-relating-self", id !== null && nodeId === id);
    }
  }
  surface.addEventListener("pointerover", (event) => {
    // While a node is being dragged the highlight belongs to the node in hand, not
    // to whatever the pointer happens to sweep over on the way.
    if (moving) return;
    const node = event.target.closest?.(".mesh-node");
    relate(node ? node.dataset.nodeId : null);
  });
  surface.addEventListener("pointerleave", () => relate(null));
  // Keyboard reaches the same answer: the relationships are part of what the view
  // says, not a reward for owning a mouse.
  surface.addEventListener("focusin", (event) => {
    if (moving) return;
    const node = event.target.closest?.(".mesh-node");
    if (node) relate(node.dataset.nodeId);
  });
  surface.addEventListener("focusout", (event) => {
    if (!surface.contains(event.relatedTarget)) relate(null);
  });
  // pointToFrame maps a browser point onto the frame's own units, through whatever
  // the current viewBox is. Without it a wheel zoom would drift: the pointer is in
  // CSS pixels and the frame is not.
  function pointToFrame(event) {
    const svg = surface.querySelector("svg");
    if (!svg) return null;
    const rect = svg.getBoundingClientRect();
    if (!rect.width || !rect.height) return null;
    const v = frameView || baseView();
    return {
      x: v.x + ((event.clientX - rect.left) / rect.width) * v.w,
      y: v.y + ((event.clientY - rect.top) / rect.height) * v.h,
    };
  }

  // Picking a bubble up and putting it somewhere.
  //
  // The layout answers "where does this graph want to sit", which is the right
  // first answer and never the last one: the person reading it knows things the
  // simulation does not — that these four belong together, that this hub should be
  // out of the way — and until now had no way to say so. Dragging is how they say
  // it, and the graph rearranging itself around the node in hand is what makes the
  // answer legible: neighbours follow, everything else gets out of the way, and
  // what was connected to what is visible in the motion rather than only in the
  // lines.
  //
  // A dropped node stays dropped. The simulation here settles once rather than
  // running continuously, so releasing a node back into it would put it straight
  // back where it started and make the whole gesture pointless. It is pinned
  // instead — marked on the node, undone per node by double-clicking it and
  // wholesale by "Release", so it is never a state somebody is stuck in.
  let moving = null;

  // place puts a node where the hand put it, anywhere.
  //
  // It used to clamp into the world, on the argument that the fitted view shows the
  // world and a node outside it would be invisible at the view somebody would use to
  // find it. That argument stopped being true when Fit started framing the *content*
  // rather than the world: the fit follows whatever has been dragged, so a node moved
  // past the old edge is still one press of Fit away. What the clamp actually did was
  // refuse the gesture — a node against the top of the canvas simply stopped, which
  // reads as the picture being broken rather than as a boundary being enforced.
  //
  // So the world is a budget for the *layout* to settle in, not a fence around the
  // arrangement. Panning is unconditional for the same reason (see the pointerdown
  // handler): once a node can be anywhere, the frame has to be able to go there.
  function place(node, x, y) {
    node.x = x;
    node.y = y;
  }

  // pin records where a node has been put, and marks it as put there.
  function pin(id, node, element) {
    node.held = true;
    pinned.set(id, { x: node.x, y: node.y });
    (element || nodeEls.get(id))?.classList.add("mesh-pinned");
    updateRelease();
  }

  // grip is what a drag moves: the edges that will pull, at the lengths they had
  // when it started, and every node's footprint. Taken once per gesture rather than
  // per frame, so the geometry a drag is working against cannot drift under it.
  function gripOn() {
    return {
      tethers: tethersFor(placed, shown.edges),
      radii: placed.map((n) => n.r ?? 12),
    };
  }
  let grip = null;

  function beginDrag(id, event) {
    const node = at.get(id);
    const grabbed = node && pointToFrame(event);
    if (!grabbed) return false;
    // The grab offset, so the node does not jump its own centre onto the pointer.
    moving = {
      id, node, pointer: event.pointerId, shifted: false,
      dx: node.x - grabbed.x, dy: node.y - grabbed.y,
      from: { x: node.x, y: node.y },
    };
    node.held = true;
    grip = gripOn();
    relate(id);
    return true;
  }

  // nudge settles the graph a little way toward its new shape and writes the result
  // into the SVG that is already on screen. One frame's worth per frame: a full
  // settle on every pointer move would be slower and would make the picture jump
  // rather than follow.
  let nudging = 0;
  function nudge() {
    if (nudging) return;
    nudging = requestAnimationFrame(() => {
      nudging = 0;
      // No early return when the drag has already ended: the last pointer move can
      // land between two frames, and skipping that frame would leave the node drawn
      // where it was a moment before it was dropped — pinned at one place and
      // painted at another until something else repainted the view.
      if (grip) follow(placed, grip.tethers, grip.radii);
      applyPositions();
    });
  }

  function endDrag() {
    if (!moving) return;
    const { id, node, shifted, pointer } = moving;
    moving = null;
    try { surface.releasePointerCapture(pointer); } catch { /* never captured */ }
    if (!shifted) {
      // A press that never moved is a click, not a drag: it leaves nothing pinned.
      node.held = pinned.has(id);
      grip = null;
      return;
    }
    pin(id, node);
    // Drawn where it was dropped, now, rather than on whichever frame happens to run
    // next. Everything downstream — the pin, the panel, the next repaint — agrees
    // about where this node is, so the picture must not be the one thing that does not.
    follow(placed, grip.tethers, grip.radii);
    applyPositions();
    grip = null;
    dragged = true; // the click that ends this drag must not also select
  }

  function updateRelease() {
    release.disabled = pinned.size === 0;
  }

  // dragged suppresses the click that ends a pan or a drag. Panning, dragging and
  // selecting share the same surface, and a gesture that also selected whatever it
  // started on would make the picture impossible to move without changing the
  // answer beside it.
  let panning = null, dragged = false;
  surface.addEventListener("pointerdown", (event) => {
    if (event.button !== 0) return;
    // Whatever the last gesture suppressed, this one is a fresh question. Reset here
    // rather than in the click handler: a gesture that ends outside the canvas never
    // produces a click, and the flag would then swallow the next real one.
    dragged = false;
    // A node under the pointer is the thing being moved; the background is the view.
    const node = event.target.closest?.("[data-node-id]");
    // Not preventDefault() here, however tempting: cancelling pointerdown also
    // cancels the compatibility mouse events behind it, and the click that selects a
    // node is one of them. The browser's own gesture is held off by the stylesheet
    // instead (the canvas takes no text selection) and by the move below.
    if (node && beginDrag(node.getAttribute("data-node-id"), event)) return;
    // Panning always, at any magnification. It used to be refused at the fitted
    // frame — everything was on screen there, so a drag could only push the picture
    // into the empty space the fit exists to remove. That is no longer the whole
    // truth: a node can now be dragged anywhere (see place), so there is somewhere to
    // pan *to*, and a canvas that only moves when zoomed in is a canvas whose rules
    // a reader has to discover. Fit is the way back, and it is one button.
    const from = pointToFrame(event);
    if (!from) return;
    panning = { from, start: frameView || baseView(), id: event.pointerId };
    dragged = false;
  });
  surface.addEventListener("pointermove", (event) => {
    if (moving && event.pointerId === moving.pointer) {
      const point = pointToFrame(event);
      if (!point) return;
      // Cancelling the *move* is safe where cancelling the press was not, and it is
      // what stops a touch drag from being taken over by the page's own scrolling.
      event.preventDefault();
      const node = moving.node;
      place(node, point.x + moving.dx, point.y + moving.dy);
      if (!moving.shifted && Math.hypot(node.x - moving.from.x, node.y - moving.from.y) > 3) {
        moving.shifted = true;
        // Captured only now that this is a drag rather than a press. Capturing on
        // the press would retarget the compatibility mouse events behind it to the
        // canvas, and the click that selects a node is one of them — so a press that
        // never moved would stop selecting anything. From here on there is no click
        // to protect: this gesture suppresses its own.
        try { surface.setPointerCapture(event.pointerId); } catch { /* no capture, no harm */ }
      }
      nudge();
      return;
    }
    if (!panning || event.pointerId !== panning.id) return;
    const svg = surface.querySelector("svg");
    const rect = svg?.getBoundingClientRect();
    if (!rect?.width) return;
    const dx = ((event.clientX - rect.left) / rect.width) * panning.start.w;
    const dy = ((event.clientY - rect.top) / rect.height) * panning.start.h;
    const moveX = panning.from.x - (panning.start.x + dx);
    const moveY = panning.from.y - (panning.start.y + dy);
    if (Math.abs(moveX) + Math.abs(moveY) > 4) dragged = true;
    if (!dragged) return;
    frameView = { ...panning.start, x: panning.start.x + moveX, y: panning.start.y + moveY };
    applyView();
  });
  const endGesture = () => { endDrag(); panning = null; };
  surface.addEventListener("pointerup", endGesture);
  surface.addEventListener("pointercancel", endGesture);
  // Leaving the canvas ends a pan but not a drag: the pointer was captured for the
  // drag, so it is still this gesture's, and dropping the node at the boundary is
  // the one thing somebody dragging toward the edge is not asking for.
  surface.addEventListener("pointerleave", () => { if (!moving) endGesture(); });

  // Double-clicking a node goes into it, and for a process "into it" is Operations.
  //
  // One gesture, one meaning — go inside the thing this stands for — and the two
  // answers are not a special case so much as where the inside of each kind is.
  // Panorama owns the landscape and application altitudes and links into the process
  // one rather than reimplementing it (ADR-0211 §5), so a process's inside is not
  // here: it is its live view, with its instances and its tokens. Every other kind
  // has no elsewhere to be opened in, and going into it means what it has always
  // meant — this node becomes the centre and the picture is redrawn around it.
  //
  // A process can still be drilled into on the landscape: the "→" in the header does
  // it for whatever is selected, which is the control that gesture was given when the
  // drilldown became a path, and it is the discoverable half of the pair.
  surface.addEventListener("dblclick", (event) => {
    const id = event.target.closest?.("[data-node-id]")?.getAttribute("data-node-id");
    if (!id) return;
    event.preventDefault();
    const inside = hrefFor(at.get(id) || {});
    if (inside) {
      location.hash = inside;
      return;
    }
    drillTo(id);
  });

  release.addEventListener("click", () => {
    if (!pinned.size) return;
    pinned.clear();
    for (const n of placed) n.held = false;
    // With nothing pinned the layout fits again, so the view goes back to the whole
    // landscape rather than to whatever corner the arrangement had been read from.
    frameView = null;
    updateRelease();
    paint();
  });

  surface.addEventListener("wheel", (event) => {
    const focus = pointToFrame(event);
    if (!focus) return;
    event.preventDefault();
    zoom(event.deltaY > 0 ? 1.18 : 1 / 1.18, focus);
  }, { passive: false });

  zoomIn.addEventListener("click", () => zoom(1 / 1.3));
  zoomOut.addEventListener("click", () => zoom(1.3));
  // Fit reframes onto the content as it is now, arrangement included: somebody who
  // has dragged half the landscape into a shape and then asks to see all of it is
  // asking about the shape they made, not about the one the layout proposed.
  zoomFit.addEventListener("click", () => { frameView = null; refit(); applyView(); });

  surface.addEventListener("click", (event) => {
    if (dragged) { dragged = false; return; }
    const node = event.target.closest("[data-node-id]");
    // Shift as well as the platform key: it is what a list expects, and on a canvas
    // there is no range for it to mean anything else.
    const add = event.ctrlKey || event.metaKey || event.shiftKey;
    if (node) select(node.getAttribute("data-node-id"), { add });
    else if (!add) select(null); // a modifier-click on the background is a miss, not a clear
  });
  // Drilling into a node: the landscape reduced to it and what it touches.
  //
  // Double-click, because it is the gesture for "open this" everywhere else and
  // because the single click is already spoken for by selecting. Which cost the
  // double-click its previous job — releasing one pinned node — and that moved into
  // the panel beside the node it is about, where it is visible instead of being
  // folklore.
  // The trail, as a row of stations. Every one of them is a place the reader stood,
  // and pressing one goes back to it — which is the difference between a path and a
  // history you can only leave: following a dependency four nodes deep and then
  // wanting the second one back is the ordinary case, not the exotic one.
  //
  // The whole starmap is the first station, so leaving is a step like any other
  // rather than a separate escape hatch. The last station is where you are, and is
  // not a button: there is nowhere for it to go.
  function paintDrillChip() {
    if (!trail.length) {
      drillTrail.hidden = true;
      drillTrail.innerHTML = "";
      return;
    }
    const nameOf = (id) => (graph.nodes.find((n) => n.id === id) || {}).name || id;
    const stations = [
      `<button type="button" class="mesh-crumb" data-crumb="-1"
        title="Back to the whole starmap">All</button>`,
      ...trail.map((id, i) => (i === trail.length - 1
        ? `<span class="mesh-crumb mesh-crumb-here" aria-current="true">${esc(nameOf(id))}</span>`
        : `<button type="button" class="mesh-crumb" data-crumb="${i}"
            title="Back to ${esc(nameOf(id))}">${esc(nameOf(id))}</button>`)),
    ];
    drillTrail.hidden = false;
    drillTrail.innerHTML = stations.join(`<span class="mesh-crumb-sep" aria-hidden="true">›</span>`);
  }

  // Going into a node from wherever you are standing. It appends rather than
  // replaces, so the path is kept — and going into a node already on the path
  // truncates back to it instead of visiting it twice, because a trail that can
  // contain the same node at two depths is a trail nobody can read.
  function drillTo(id) {
    const seen = trail.indexOf(id);
    trail = seen >= 0 ? trail.slice(0, seen + 1) : [...trail, id];
    // The search box, the scale's bands and the drilldown are three ways of asking
    // the same kind of question, so entering one clears the others rather than
    // compounding with them.
    search.value = "";
    bandAt = null;
    picked = [id];
    // Refitted, because the picture that comes back is a different graph in a
    // different world, and a frame from the old one lands on nothing.
    frameView = null;
    paint();
  }

  // One station back, not all the way out. Stepping out of the fourth node lands on
  // the third, which is where the reader came from — throwing away the whole walk
  // because they wanted one node back is the behaviour this replaces.
  function drillBack(to = trail.length - 2) {
    if (!trail.length) return;
    trail = to < 0 ? [] : trail.slice(0, to + 1);
    frameView = null;
    // The selection is left alone, so the picture comes back with the thing that was
    // being read still marked in it: stepping back should not mean having to find
    // the node again.
    paint();
  }

  // The arrow goes where the double-click goes. Two ways into the same place rather
  // than two behaviours: a reader who has clicked a node and wants to see only it has
  // a control to press, and one who already knows the gesture keeps it.
  drillIn.addEventListener("click", () => {
    if (only()) drillTo(only());
  });

  drillTrail.addEventListener("click", (event) => {
    const at = event.target.closest?.("[data-crumb]")?.getAttribute("data-crumb");
    if (at !== null && at !== undefined) drillBack(Number(at));
  });
  // Escape is what leaves a thing you have gone into, in every other view — and it
  // has to work wherever the focus happens to be, because somebody who has just
  // double-clicked a circle has not focused anything in particular.
  //
  // Bound to the document for that reason, and guarded on this view still being on
  // the page: the landscape is one route of a single-page app, and a handler that
  // outlived it would act on a picture nobody is looking at.
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !trail.length) return;
    if (!document.body.contains(view)) return;
    event.preventDefault();
    // One station, so Escape retraces the path a node at a time. Pressing it enough
    // times still lands on the whole starmap, which is what it did before.
    drillBack();
  });

  // Exporting the picture (ADR-0211 §10).

  // exportMeta describes the artifact about to be written: when the server read
  // this landscape, which instance it came from, and everything the picture is not
  // showing.
  //
  // It is assembled from the payload and from this view's own state, because
  // between them they are the only place the answer exists — and it is assembled at
  // the moment of export rather than at load, since a filter or a drilldown is
  // exactly what changes what the file contains.
  // windowStamp is the maintenance window as the file has to carry it (ADR-0211 §10):
  // who is in it, and what it costs together and apart. Computed here rather than
  // read off the panel, because the panel is markup and the numbers have to be the
  // ones the analysis produced.
  function windowStamp() {
    if (picked.length < 2) return null;
    const direction = dirSelect.value;
    const depth = depthHops();
    const overlap = windowOverlap(shown, picked, { direction, depth });
    if (!overlap) return null;
    const byId = new Map(shown.nodes.map((n) => [n.id, n]));
    return {
      members: picked.map((id) => shortLabel(byId.get(id) || { id })),
      total: overlap.total,
      sum: overlap.sum,
      direction,
      hops: depth === Infinity ? "any" : depth,
    };
  }

  function exportMeta() {
    const term = search.value.trim();
    const status = graph.status || {};
    const spoken = notationOf(notationPick.value);
    const scope = drilledAt()
      ? {
          kind: "drill",
          // Where the reader is standing, and the path they took to get there. A
          // file cropped to one node with no account of how it was reached is a
          // picture whose narrowing cannot be checked.
          name: (graph.nodes.find((n) => n.id === drilledAt()) || {}).name || drilledAt(),
          via: trail.slice(0, -1).map((id) =>
            (graph.nodes.find((n) => n.id === id) || {}).name || id),
          hops: depthValue(),
        }
      : term ? { kind: "filter", term } : { kind: "all" };
    return {
      // The server's reading, never this browser's clock: one dates the facts, the
      // other dates the save, and an export exists to be read later.
      observedAt: graph.observedAt,
      source: location.host,
      // Which vocabulary the file is drawn in, and what that vocabulary drops. A
      // reader who receives a C4-looking picture has no other way to learn that it
      // was projected from something else.
      notation: spoken.projection
        ? { label: spoken.label, short: spoken.short, projection: true,
            loss: spoken.loss, mappingVersion: spoken.mappingVersion }
        : null,
      scope,
      drawn: { nodes: shown.nodes.length },
      total: graph.nodes.length,
      // Landscape-level facts, taken from the payload rather than from the filtered
      // picture: "hidden by your access" is true of the whole regardless of how much
      // of it this file happens to show.
      restricted: graph.restricted || 0,
      clustered: Boolean(graph.clustered),
      // What the rings on the picture mean. Only when there is more than one: a
      // single selection is not a window, and its ring is explained by the fact that
      // somebody clicked it.
      window: windowStamp(),
      // Which quantity the sizes and the numbers under the names carry in this file,
      // and what the largest of them stands for. Said in the stamp rather than left to
      // be inferred: on screen the key says it beside the picture, and a file that has
      // been pasted into a ticket has no key beside it — a reader who took these radii
      // for the structural ones would read the estate exactly backwards, and an area
      // with no stated reference is a quantity nobody can read back.
      heat: weighted()?.key || null,
      // Kept beside it because a stamp rendered by an older build asks this question
      // directly, and the answer it wants is still true.
      instances: weighted()?.key === "instances",
      peak: heatPeak(graph, weighted(), measuredAt),
      drafts: draftsToggle.checked,
      partial: Boolean(status.partial),
      unavailable: (status.unavailable || []).map((u) => ({
        ...u, label: STATE_TEXT[u.state] || u.state,
      })),
    };
  }

  async function exportPicture(kind) {
    const canvas = surface.querySelector("svg");
    if (!canvas) {
      toast("There is nothing here to export.");
      return;
    }
    const theme = getComputedStyle(document.documentElement);
    const token = (name, fallback) => theme.getPropertyValue(name).trim() || fallback;
    // The page's own surface colour rather than a fixed white: the accent and the
    // neutrals are configurable per instance, and a file that ignored them would not
    // look like the landscape it was taken from.
    const background = token("--surface", "#ffffff");
    try {
      const built = standaloneSVG(canvas, {
        stamp: stampLines(exportMeta()),
        // The key travels with the picture. Beside the canvas it is one scroll away;
        // in a file that has been pasted into a ticket there is nothing to scroll to,
        // and a hexagon nobody can name is a shape rather than a worker.
        legend: [
          ...legendEntries(shown, notationOf(notationPick.value)),
          // And the size scale, when size is carrying a quantity. Appended here
          // rather than inside legendEntries because it is the one row of the key
          // that depends on the landscape's peak rather than on its kinds.
          ...(weighted() ? heatScaleEntries(heatOf(notationOf(notationPick.value)),
            heatPeak(graph, weighted(), measuredAt)) : []),
        ],
        css: exportStyles(canvas.outerHTML),
        // The whole world, not the window: the canvas's own viewBox is wherever the
        // reader has zoomed to, and a file cropped to that would drop nodes without
        // saying it had.
        extent: `0 0 ${world.width} ${world.height}`,
        background,
        ink: token("--text", "#111111"),
        muted: token("--muted", "#666666"),
        rule: token("--border", "#dddddd"),
      });
      if (kind === "png") {
        save(await rasterise(built, { background }), exportName("png"));
      } else {
        save(new Blob([built.svg], { type: "image/svg+xml;charset=utf-8" }), exportName("svg"));
      }
    } catch (e) {
      toast("export failed: " + e.message, "err");
    }
  }

  // Choosing a band off the scale in the key. Delegated on the slot rather than bound
  // to the buttons, because the key is written afresh on every paint and a listener
  // on a button that no longer exists is a control that stopped working silently.
  //
  // Clicking the mark that is already chosen widens the picture again. A filter you
  // can only turn on is a trap, and the mark itself is the only obvious place to look
  // for the way out of it.
  legendSlot.addEventListener("click", (event) => {
    const step = event.target.closest(".mesh-scale-step");
    if (!step) return;
    const tally = Number(step.dataset.tally);
    if (!Number.isFinite(tally)) return;
    bandAt = bandAt === tally ? null : tally;
    // Asking about the whole landscape again, exactly as typing in the box does:
    // leaving the trail in force would filter inside a drilldown while the key says
    // otherwise, and the drilldown is what the picture would actually be showing.
    trail = [];
    // A narrowing changes what is on screen, so a frame the reader had zoomed into is
    // about a picture that no longer exists.
    frameView = null;
    paint();
  });

  // A different vocabulary is a different drawing, so the picture is painted again.
  // The arrangement survives it: paint() carries the positions on screen forward and
  // every notation's shape is inscribed in the same reserved circle, so nothing moves
  // except the outlines.
  notationPick.addEventListener("change", paint);

  // Drafts are the one switch that changes the *landscape* rather than the drawing of
  // it, so it is answered by the server. Two things reach for it — the switch, and a
  // saved view that was saved with drafts on — and both go through here, so a view
  // cannot come back showing half the picture it was named for.
  //
  // Everything the reader has arranged survives the swap: positions are kept by node
  // id, and the drilldown trail is pruned rather than cleared — a station whose node
  // is no longer on the picture cannot be a way back to it, and the ones still there
  // still are.
  //
  // `silent` is a refresh nobody asked for — the timer's, rather than the switch's.
  // It leaves the drafts control alone: dimming it every half minute would make the
  // one thing on this row that *is* waiting for the server indistinguishable from the
  // thing that merely does so on its own.
  // Reports whether it took: a silent answer that arrived too late is dropped rather
  // than applied, so the caller knows not to repaint.
  async function loadLandscape(wantDrafts, { silent = false } = {}) {
    if (!silent) draftsToggle.disabled = true;
    const started = performance.now();
    let arrived;
    try {
      arrived = await api("GET", "/api/v1/panorama/mesh" + (wantDrafts ? "?drafts=1" : ""));
    } finally {
      if (!silent) draftsToggle.disabled = false;
    }
    // The reader asked for a different landscape while this one was in flight. A
    // timer's answer is to a question nobody is asking any more: applying it would put
    // the drafts back on the picture and flick the switch under the hand that had just
    // moved it, with nothing said. Their answer is on its way; this one is dropped.
    //
    // The other order — the switch's fetch in flight when the timer fires — is refused
    // at the tick, where the disabled switch already says a load is running.
    if (silent && draftsToggle.checked !== wantDrafts) return false;
    graph = arrived;
    // What this landscape costs to derive, measured every time rather than once at
    // open: an estate grows, and the cadence below is a fraction of the cost.
    derivedMs = performance.now() - started;
    fetchedAt = Date.now();
    attemptedAt = fetchedAt;
    failing = false;
    draftsToggle.checked = wantDrafts;
    trail = trail.filter((id) => graph.nodes.some((n) => n.id === id));
    return true;
  }

  // A failed fetch puts the checkbox back. The picture did not change, so a control
  // left claiming it did would be the one lie this view cannot afford.
  draftsToggle.addEventListener("change", async () => {
    const want = draftsToggle.checked;
    try {
      await loadLandscape(want);
    } catch (e) {
      draftsToggle.checked = !want;
      toast(`Could not ${want ? "add" : "remove"} drafts: ${e.message}`);
      return;
    }
    paint();
  });

  exportModelBtn.addEventListener("click", () => {
    window.location.href = "/api/v1/panorama/mesh/archimate";
  });

  exportSvgBtn.addEventListener("click", () => exportPicture("svg"));
  exportPngBtn.addEventListener("click", () => exportPicture("png"));

  // Saved views.

  //
  // Reading a landscape is not a single act: somebody watching one node filters down
  // to it, zooms in, arranges what is around it — and a reload puts them back at the
  // whole landscape with all of it to do again. A saved view is that setup with a
  // name on it, and opening one is the only thing on this page that changes five
  // controls at once, which is why it is a list of names rather than a URL to
  // remember.
  //
  // What is saved is the whole question: the filter, the direction and depth, the
  // node being watched, how far in the view is zoomed, and the arrangement. What is
  // *not* saved is the graph — the landscape is derived and changes as things are
  // deployed, so a view is a way of looking rather than a snapshot of what was there.
  // Reached once and guarded: in a sandboxed frame or with site data blocked, even
  // *touching* window.localStorage throws, and that must cost the landscape nothing
  // more than its saved views.
  const store = (() => { try { return window.localStorage; } catch { return null; } })();
  let views = readViews(store);

  function say(message) {
    viewNote.textContent = message || "";
    viewNote.hidden = !message;
  }

  function renderViews() {
    viewList.innerHTML = views.length
      ? views.map((v) => `<li>
          <button type="button" class="mesh-view-open" data-view="${esc(v.id)}"
            title="${esc(viewSummary(v))}">${esc(v.name)}</button>
          <button type="button" class="mesh-view-drop" data-drop="${esc(v.id)}"
            aria-label="Forget the view ${esc(v.name)}" title="Forget this view">×</button>
        </li>`).join("")
      : `<li class="mesh-view-empty muted">Set the starmap up the way you want to
          find it, then name it here.</li>`;
  }

  // viewSummary says what a name stands for, so a list of names is still readable a
  // month later. It describes what was saved rather than what would be shown now:
  // the landscape may have moved on, and the view is the question, not the answer.
  function viewSummary(v) {
    const parts = [];
    if (v.term) parts.push(`filter “${v.term}”`);
    // From the notation, which is where the weighting lives, and falling back to the
    // flag for a view stored while the counts were still a switch of their own.
    const weighing = HEATS[v.notation] || (v.instances ? HEATS.instances : null);
    if (weighing) parts.push(`sized by ${weighing.short.toLowerCase()}`);
    if (v.drafts) parts.push("with drafts");
    if (v.trail?.length) parts.push(`${v.trail.length} step(s) in`);
    if (v.picked?.length) parts.push(`a window of ${v.picked.length} node(s)`);
    else if (v.selected) parts.push(`watching ${v.selected}`);
    if (v.zoom < 1) parts.push(`zoomed to ${Math.round(v.zoom * 100)}%`);
    if (v.pins?.length) parts.push(`${v.pins.length} node(s) placed by hand`);
    return parts.length ? parts.join(" · ") : "the whole landscape";
  }

  // openView puts the controls, the arrangement and the frame back.
  //
  // Twice through paint when there is an arrangement, and deliberately: the pins are
  // stored as fractions of the world, and the world is sized from whatever the filter
  // leaves on screen — so the first paint is what establishes the world the second
  // one places them in. Opening a saved view is a rare, deliberate act; paying two
  // layouts for it is cheaper than storing coordinates that mean somewhere else on a
  // different screen.
  async function openView(v) {
    // The landscape first, because everything below is about *this* graph: the trail
    // is filtered against it and the pins are placed in the world it sizes. A view
    // saved before drafts existed carries none, and false is the picture it was
    // looking at. A fetch that fails leaves the landscape as it is and says so — the
    // rest of the view is still worth restoring against it.
    if (Boolean(v.drafts) !== draftsToggle.checked) {
      try {
        await loadLandscape(Boolean(v.drafts));
      } catch (e) {
        toast(`Showing this view without changing the drafts: ${e.message}`);
      }
    }
    search.value = v.term || "";
    // A band saved against a landscape whose peak has since moved is let go of by
    // paint(), which checks it against the marks it has just computed. Restoring it
    // here and letting that check decide is the same rule the live picture follows.
    bandAt = Number.isFinite(v.band) && v.band >= 0 ? v.band : null;
    dirSelect.value = v.direction || "dependents";
    setDepth(v.depth ?? "2");
    // A view saved before notations existed carries none, and the derived drawing is
    // what it was looking at.
    notationPick.value = notationOf(v.notation).id === v.notation ? v.notation : "atlas";
    // A view saved while the counts were a switch beside the picker carries them as
    // their own flag, and the derived drawing as its notation. That combination no
    // longer exists, and the picture it stands for is this one — so it is restored as
    // the weighting rather than dropped. A view saved in a projection keeps the
    // projection: the counts were the lesser half of what it was named for, and
    // silently replacing ArchiMate with a heatmap would reopen a different question.
    if (v.instances && notationPick.value === "atlas") notationPick.value = "instances";
    // The walk, before the paint that draws it: the picture a view saved is the one
    // cut from the last station, so restoring the path is part of restoring the
    // picture rather than something done to it afterwards. Stations whose nodes are
    // gone are dropped, and a walk that loses its last one is a walk back to where
    // it still leads.
    trail = (v.trail || []).filter((id) => graph.nodes.some((n) => n.id === id));
    picked = [];
    pinned.clear();
    frameView = null;
    paint();
    if (v.pins?.length) {
      for (const [id, at] of pinsFor(v, world)) pinned.set(id, at);
      paint();
    }
    updateRelease();
    // The selection last, and only the members still on screen. A landscape is
    // derived: what a view was watching can have been undeployed since, and the panel
    // must not describe something that is not there. A window that lost one member is
    // reopened as the rest of itself and says so — silently planning around two of
    // the three nodes somebody saved would be the worse answer.
    const wanted = v.picked?.length ? v.picked : (v.selected ? [v.selected] : []);
    picked = wanted.filter((id) => shown.nodes.some((n) => n.id === id));
    if (picked.length) refresh();
    frameView = frameFor(v, world, (id) => at.get(id));
    applyView();
    const lost = wanted.length - picked.length;
    say(lost
      ? `Opened “${v.name}”. ${lost} of the node(s) it was watching ${
        lost === 1 ? "is" : "are"} no longer in this starmap.`
      : "");
  }

  // Clicking a finding goes to it: selected, so the panel above explains it, and
  // framed, so it is on screen rather than somewhere in a landscape of four hundred
  // circles. Going *to* a finding is the whole reason the list is worth having.
  // goToNode selects a node and brings the view to it. Three lists now offer the
  // same gesture — the findings, the impact answer's own list, and the ranking — and
  // they behave identically because they are one function.
  function goToNode(id) {
    if (!at.get(id)) return;
    picked = [id];
    refresh();
    frameOn(id);
  }

  // Bringing a node on screen without answering a different question about it. The
  // window panel needs exactly this: its members are buttons, and pressing one to
  // find it on the canvas must not collapse the window into a selection of one.
  function frameOn(id) {
    const node = id && at.get(id);
    if (!node) return;
    // Held at whatever magnification is already in use if the view is zoomed, so a
    // reader working close in is not thrown back out; otherwise close enough to read
    // the node and what is immediately around it.
    const w = frameView ? frameView.w : world.width * 0.3;
    const h = frameView ? frameView.h : world.height * 0.3;
    frameView = { x: node.x - w / 2, y: node.y - h / 2, w, h };
    applyView();
  }

  for (const slot of [findingsSlot, rankingSlot, panel]) {
    slot.addEventListener("click", (event) => {
      const id = event.target.closest?.("[data-finding]")?.getAttribute("data-finding");
      if (id) goToNode(id);
    });
  }

  viewForm.addEventListener("submit", (event) => {
    event.preventDefault();
    const captured = captureView({
      name: viewName.value,
      term: search.value.trim(),
      band: bandAt,
      direction: dirSelect.value,
      depth: depthValue(),
      notation: notationPick.value,
      selected: only(),
      picked,
      // Kept in the stored view even though the notation now carries it: a view is
      // read back by older builds, which ask this question directly and can only
      // answer it about the one weighting they had.
      instances: weighted()?.key === "instances",
      drafts: draftsToggle.checked,
      trail,
      frameView,
      world,
      pinned,
    });
    const outcome = saveView(views, captured);
    if (outcome.error) { say(outcome.error); return; }
    const replaced = views.length === outcome.views.length;
    views = outcome.views;
    renderViews();
    viewName.value = "";
    // A save the browser refused has to be said out loud. Storage can be full or off
    // entirely, and a view that quietly evaporated is worse than one that was
    // refused: the reader would find out by coming back for it.
    say(writeViews(store, views)
      ? `${replaced ? "Updated" : "Saved"} “${captured.name}”.`
      : "This browser is not storing anything, so the view is here until you reload.");
  });

  viewList.addEventListener("click", (event) => {
    const open = event.target.closest?.("[data-view]");
    if (open) {
      const v = views.find((entry) => entry.id === open.getAttribute("data-view"));
      if (v) openView(v);
      return;
    }
    const drop = event.target.closest?.("[data-drop]");
    if (!drop) return;
    views = removeView(views, drop.getAttribute("data-drop"));
    renderViews();
    writeViews(store, views);
    say("");
  });

  renderViews();

  // Arranging the landscape without a mouse. The arrangement is a convenience rather
  // than information — every relationship this view carries is already reachable by
  // focusing a node — but a convenience only some people can have is not one, and
  // stepping a focused node is four lines of the same machinery a drag uses.
  const ARROWS = {
    ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1],
  };
  surface.addEventListener("keydown", (event) => {
    const step = ARROWS[event.key];
    if (!step) return;
    const element = event.target.closest?.("[data-node-id]");
    const id = element?.getAttribute("data-node-id");
    const node = id && at.get(id);
    if (!node) return;
    // Arrows scroll the page by default, which is the opposite of what somebody who
    // has focused a node and pressed one is asking for.
    event.preventDefault();
    // A step is a share of the world rather than a fixed number of units, so it
    // covers the same fraction of the picture whatever size the landscape is. Shift
    // is the coarse one: crossing a large landscape a pixel at a time is not a
    // keyboard equivalent of a drag, it is a punishment for not having a mouse.
    const distance = Math.max(12, world.width * 0.02) * (event.shiftKey ? 5 : 1);
    // Held first, so the edges this step will pull are the ones recorded; pinned
    // after the move, so what is recorded is where the node ended up.
    node.held = true;
    const stepGrip = gripOn();
    place(node, node.x + step[0] * distance, node.y + step[1] * distance);
    pin(id, node, element);
    follow(placed, stepGrip.tethers, stepGrip.radii);
    applyPositions();
  });

  surface.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" && event.key !== " ") return;
    const node = event.target.closest("[data-node-id]");
    if (!node) return;
    event.preventDefault();
    select(node.getAttribute("data-node-id"),
      { add: event.ctrlKey || event.metaKey || event.shiftKey });
  });
  dirSelect.addEventListener("change", () => {
    refresh();
    paintRanking();
  });
  // Depth means two things at once, and only one of them is free. Inside a drilldown
  // it decides how far the picture reaches, so changing it is a different graph and
  // has to be laid out again; outside one it only bounds the impact walk, which is
  // classes on the nodes already drawn.
  function depthChanged() {
    depthField.disabled = depthAny.checked;
    // A drilldown is cut to the depth on screen, so changing it re-cuts the picture
    // and paint() repaints the ranking with it; otherwise only the answers change.
    if (drilledAt()) return paint();
    refresh();
    paintRanking();
  }
  depthAny.addEventListener("change", depthChanged);
  // On input rather than on change: a number field only fires change when it loses
  // focus, and a reader who types 4 and looks at the picture would be looking at the
  // answer for 2. Typing is cheap here — outside a drilldown it is classes on nodes
  // already drawn — and inside one the debounce below keeps a held arrow key from
  // laying the graph out on every repeat.
  let depthPending;
  depthField.addEventListener("input", () => {
    clearTimeout(depthPending);
    depthPending = setTimeout(depthChanged, drilledAt() ? 200 : 0);
  });
  // An empty or nonsense field is left alone while it is being typed in and put
  // right when it is left, so the control never argues with the picture it produced.
  depthField.addEventListener("blur", () => { depthField.value = String(depthOf(depthField.value)); });

  // Re-laying out on every keystroke is the wrong trade at 400 nodes, where the
  // simulation costs a few hundred milliseconds. A short debounce keeps typing
  // responsive and still feels immediate.
  let pending;
  search.addEventListener("input", () => {
    clearTimeout(pending);
    // Typing is asking about the whole landscape again. Leaving the trail in force
    // would search inside it while the box says otherwise.
    trail = [];
    // A filter changes what is on screen, so the frame the viewer had zoomed into is
    // about a picture that no longer exists. Refitting is the honest reset; keeping
    // the old frame would land them on empty space and read as a broken view.
    frameView = null;
    pending = setTimeout(paint, 120);
  });
  paint();

  // The layout is a function of the frame, so the frame is *watched* rather than
  // assumed — and watched on the surface itself rather than on the window.
  //
  // This is what fixes a picture that arrives wrong and stays wrong. The first paint
  // happens as soon as the markup is in the document, and there are ordinary reasons
  // the surface has no box at that moment — a tab opened in the background does no
  // layout at all, and a container mid-reflow reports zero. The measurement then
  // falls back to its floor, the world takes that floor's nearly-square aspect, and
  // the finished picture is letterboxed into a column of the canvas. Nothing was
  // wrong with the graph; it was laid out for a box it never had. It stayed wrong
  // because nothing measured again: changing the notation, or anything else that
  // repaints, fixed it, which is exactly how the report described it.
  //
  // Observing the surface catches every version of that — a late box, a late
  // stylesheet, a font that changes the chrome, a scrollbar appearing, and a resized
  // window, which the window listener used to catch alone.
  //
  // But an observer alone was not enough, and that is the part this correction adds.
  // It made the whole picture depend on being *told*: the framing was only ever
  // recomputed by a full re-layout, the re-layout only ever ran when a notification
  // arrived, and the notification could be postponed indefinitely by the next one.
  // So the two are separated below — the framing is put right by measuring, on every
  // view write and on the frame after every paint, and the re-settling is what the
  // observer schedules. A missed or endlessly deferred notification now costs the
  // arrangement of the nodes, which is a picture drawn for a slightly different
  // shape; it no longer costs the shape of the picture itself.
  //
  // Two answers, because the question has two halves and they cost different
  // amounts. The framing is put right *now* — it is arithmetic on a bounding box,
  // and a view of the wrong shape is a letterboxed, shrunken picture for however
  // long it is left. The layout is re-settled after a pause, because the simulation
  // is the expensive part and a drag-resize asks a hundred times a second.
  let resizing;
  let owedSince = 0;
  const reframe = () => {
    applyView();
    const now = frameNow();
    if (Math.abs(now.width - laidOut.width) < 2 && Math.abs(now.height - laidOut.height) < 2) {
      // Settled for the box it has. Anything owed is owed no longer.
      clearTimeout(resizing);
      owedSince = 0;
      return;
    }
    if (!owedSince) owedSince = performance.now();
    clearTimeout(resizing);
    // A ceiling on the debounce, because a debounce without one is a promise that
    // can be broken for ever: every event resets the timer, so a page that keeps
    // nudging the surface — a lazily loaded panel, a transition, a scrollbar that
    // cannot make up its mind — postpones the re-layout indefinitely, and the reader
    // is left looking at a graph settled for a box that stopped existing.
    resizing = setTimeout(relayout, performance.now() - owedSince > 400 ? 0 : 120);
  };
  function relayout() {
    owedSince = 0;
    frameView = null;
    paint();
  }
  if (typeof ResizeObserver === "function") {
    // No teardown: the observer's only reference is this closure, and once the view
    // is replaced it is watching a detached element and can never fire again. The
    // window listener it replaces had the opposite property — it outlived the view
    // it painted, because nothing here ever removed it.
    new ResizeObserver(reframe).observe(surface);
  } else {
    window.addEventListener("resize", reframe);
  }

  // Keeping the picture true (ADR-0211 §7).
  //
  // Everything this view draws has a shelf life. The severity badges are an
  // observation, the incident counts move as an operator works through them, and all
  // three weightings are live quantities — the age one is *measured against a clock*,
  // so its labels go wrong while nothing on the page changes at all. A landscape
  // opened at nine and still open at eleven showed two-hour-old numbers with nothing
  // saying so, which is the failure §10's export stamp exists to prevent, happening
  // on the screen the stamp was copied from.
  //
  // So the picture says when it was read, always, and re-reads itself while it is
  // being looked at.

  // TICK is how often the freshness line is rewritten, which is not how often the
  // landscape is re-read. Writing a sentence costs nothing and the sentence is the
  // thing that must never be wrong; the re-read costs a derive on the run loop, and is
  // paced by refreshEvery.
  const TICK = 10_000;
  const refreshDue = () =>
    Date.now() - attemptedAt >= refreshEvery(derivedMs, { failing });

  // sayObserved writes when this landscape was read, and whether the last attempt to
  // re-read it failed. Both, because they are different facts: a picture can be four
  // minutes old because nobody asked for a newer one, or because the server would not
  // give one, and only the second is a reason to stop believing it.
  function sayObserved() {
    const age = graph.observedAt
      // The payload carries Unix *seconds*; sinceText speaks the nanoseconds the
      // runtime tallies are in.
      ? sinceText(graph.observedAt * 1e9)
      : sinceText(fetchedAt * 1e6);
    observed.textContent = `observed ${age || "just now"}` +
      (failing ? " · could not re-read" : "");
    observed.classList.toggle("mesh-stale", failing);
  }
  sayObserved();

  // The re-read itself — named for what it does rather than "refresh", which in this
  // view already means answering the impact question about the current selection.
  //
  // Everything the reader has arranged survives it, because it
  // goes through the same path the drafts switch does: positions are kept by node id,
  // the trail is pruned rather than cleared, and a selection the new landscape no
  // longer contains is dropped by paint().
  async function reread() {
    if (refreshing) return;
    refreshing = true;
    try {
      if (await loadLandscape(draftsToggle.checked, { silent: true })) paint();
    } catch {
      // The picture stands. A landscape that blanked itself because one request
      // failed would have thrown away a true answer for an error, and the freshness
      // line says the current one is no longer being kept up.
      failing = true;
    } finally {
      refreshing = false;
      // The attempt is over either way, and the next one is paced from here. On a
      // success loadLandscape has already said so; this is the failure's own record,
      // and the reason a server that is down is asked once per ceiling rather than
      // once per tick.
      attemptedAt = Date.now();
      sayObserved();
    }
  }

  const ticking = setInterval(() => {
    // The view is gone: the router replaced what this closure painted. Unlike the
    // ResizeObserver above, an interval outlives its view and would go on asking the
    // server for a picture nobody is looking at for as long as the tab is open — so it
    // ends itself the first time it notices. The card is what is checked rather than
    // the container it sits in, because the container is the router's and outlives
    // every route it holds.
    if (!root.isConnected) {
      clearInterval(ticking);
      return;
    }
    sayObserved();
    // A hidden tab is not a reader. Nothing is re-read behind a background tab; the
    // first tick after it comes back is due immediately, because the clock kept
    // running while the picture did not.
    if (document.visibilityState === "hidden") return;
    if (!liveToggle.checked || !refreshDue()) return;
    // The drafts switch is mid-fetch: it is disabled for exactly as long as its own
    // request is in flight. Starting a second landscape read across it is how the two
    // answers end up racing to overwrite each other.
    if (draftsToggle.disabled) return;
    // Never under the reader's hand. A re-layout in the middle of a drag or a pan
    // takes the picture out from under the gesture that is moving it.
    if (moving || panning) return;
    reread();
  }, TICK);

  // Turning it back on is a request for a current picture, not a request to wait
  // another half minute for one.
  liveToggle.addEventListener("change", () => {
    if (liveToggle.checked && !moving && !panning) reread();
  });

  if (fetchMs > 2000) toast(`The starmap took ${Math.round(fetchMs)} ms to derive.`);
}
