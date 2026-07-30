// Token simulation for the Design view — a client-side, engine-free walk-through of a
// BPMN diagram so newcomers can *see* how control flow moves (ADR-0078). It is a
// teaching aid, not the engine: it never deploys, never talks to the server, and makes
// no claim to execute FEEL, conditions, or scripts. It animates the one thing the
// Design view is about — the shape of the control flow — by moving "tokens" along
// sequence flows, forking and joining at gateways, and pausing at exclusive choices so
// the user picks the path and watches what happens.
//
// It ships as a bpmn-js additional module (like the rest of the vendored modeler, this
// stays buildless and self-contained — no npm, no CDN; see ADR-0012/0013). The module
// exposes the `atlasTokenSimulation` service; the editor toolbar drives it and listens
// for `atlasSim.changed` on the eventBus to keep its controls in sync.

// --- BPMN shape/flow helpers -------------------------------------------------------

const isSequenceFlow = (c) => c && c.type === "bpmn:SequenceFlow";
const outFlows = (el) => (el.outgoing || []).filter(isSequenceFlow);
const inFlows = (el) => (el.incoming || []).filter(isSequenceFlow);

const isStart = (el) => el.type === "bpmn:StartEvent";
const isEnd = (el) => el.type === "bpmn:EndEvent";
const isParallel = (el) => el.type === "bpmn:ParallelGateway";
const isInclusive = (el) => el.type === "bpmn:InclusiveGateway";

// needsChoice is true for a diverging gateway where the user should pick the path:
// exclusive, inclusive, and event-based gateways with more than one outgoing flow. A
// parallel gateway forks unconditionally, and an implicit split (a plain task with two
// outgoing flows) forks too — only these "which way?" gateways pause for a decision,
// which is exactly the moment worth teaching.
function needsChoice(el) {
  const t = el.type;
  return (
    (t === "bpmn:ExclusiveGateway" ||
      t === "bpmn:InclusiveGateway" ||
      t === "bpmn:EventBasedGateway") &&
    outFlows(el).length > 1
  );
}

// isJoin is true for a converging parallel/inclusive gateway: it waits for a token on
// every incoming flow before it emits one. Exclusive/event-based converging gateways
// are pass-through (each token proceeds on its own), so they are not joins.
function isJoin(el) {
  return (isParallel(el) || isInclusive(el)) && inFlows(el).length > 1;
}

// defaultFlowId returns the id of a gateway's default sequence flow, if it has one, so
// a choice can highlight the modelled default.
function defaultFlowId(el) {
  const bo = el.businessObject;
  return (bo && bo.default && bo.default.id) || null;
}

const labelOf = (el) => {
  const bo = el && el.businessObject;
  return (bo && (bo.name || bo.id)) || (el && el.id) || "?";
};

// --- The simulation service --------------------------------------------------------

export function TokenSimulation(eventBus, elementRegistry, canvas, overlays) {
  this._eventBus = eventBus;
  this._registry = elementRegistry;
  this._canvas = canvas;
  this._overlays = overlays;

  this._active = false;
  this._playing = false;
  this._speed = 1;

  // resting: how many tokens currently sit ON each element, keyed by element id. A
  // token "rests" between moves; the badge overlay shows the count.
  this._resting = new Map();
  // joinWait: for each converging parallel/inclusive gateway, how many tokens have
  // arrived per incoming flow. The gateway fires once every incoming flow has one.
  this._joinWait = new Map();
  this._completed = 0; // tokens that reached an end event / ran off the graph

  this._overlayIds = []; // token/join badges we own, so we clear only ours
  this._startIds = []; // "spawn" affordance overlays on start events
  this._markers = new Set(); // element ids we've marked, cleared on teardown
  this._choiceFlows = new Set(); // sequence-flow ids currently offered as a choice
  this._epoch = 0; // bumped on reset/deactivate to abort in-flight animations
  this._pumpTimer = null; // auto-advance timer while playing

  // Editing must not happen mid-simulation: cancel the interaction gestures at high
  // priority while active. The palette and context pad are hidden by CSS (the editor
  // adds a `.sim-active` class); these cover keyboard/drag entry points bpmn-js still
  // wires up. Returning false stops bpmn-js's default handling.
  const block = () => (this._active ? false : undefined);
  [
    "shape.move.start",
    "shape.resize.start",
    "connect.start",
    "global-connect.start",
    "bendpoint.move.start",
    "create.start",
    "element.dblclick",
    "spaceTool.selection.start",
    "lasso.selection.start",
  ].forEach((ev) => eventBus.on(ev, 2000, block));

  // Clicks drive the simulation while active: spawn on a start event, take an offered
  // choice on a sequence flow, or hand-advance a token that is resting on an element.
  eventBus.on("element.click", 2000, (e) => {
    if (!this._active) return;
    this._onClick(e.element);
    return false; // suppress the default selection while simulating
  });

  // A fresh diagram import invalidates all token state.
  eventBus.on("import.done", () => {
    if (this._active) this.reset();
  });
}

TokenSimulation.$inject = ["eventBus", "elementRegistry", "canvas", "overlays"];

// setActive turns simulation mode on or off. Turning it off clears every token and
// affordance and restores plain editing.
TokenSimulation.prototype.setActive = function (on) {
  on = !!on;
  if (on === this._active) return;
  this._active = on;
  if (on) {
    this._drawStartAffordances();
    this._notify();
  } else {
    this.reset();
    this._clearStartAffordances();
    this._playing = false;
  }
};

TokenSimulation.prototype.isActive = function () {
  return this._active;
};

// setSpeed scales the animation and dwell timings (1 = normal).
TokenSimulation.prototype.setSpeed = function (mult) {
  this._speed = Math.max(0.25, Number(mult) || 1);
};

// spawnAt drops a fresh token on an element (normally a start event) and, if playing,
// lets it start moving.
TokenSimulation.prototype.spawnAt = function (el) {
  if (!this._active || !el) return;
  this._rest(el.id, 1);
  this._render();
  this._notify();
  if (this._playing) this._pump();
};

TokenSimulation.prototype.play = function () {
  if (!this._active) return;
  this._playing = true;
  this._notify();
  this._pump();
};

TokenSimulation.prototype.pause = function () {
  this._playing = false;
  if (this._pumpTimer) {
    clearTimeout(this._pumpTimer);
    this._pumpTimer = null;
  }
  this._notify();
};

// step advances exactly one resting token that can move without a decision. It lets a
// user single-step the flow; tokens waiting on an exclusive choice are left for a click.
TokenSimulation.prototype.step = function () {
  if (!this._active) return;
  for (const [id, n] of this._resting) {
    if (n <= 0) continue;
    const el = this._registry.get(id);
    if (el && !needsChoice(el)) {
      this._departOne(el);
      return;
    }
  }
};

// reset clears all tokens, joins, and in-flight animations but stays in simulation mode.
TokenSimulation.prototype.reset = function () {
  this._epoch++; // abort any running dot animations
  if (this._pumpTimer) {
    clearTimeout(this._pumpTimer);
    this._pumpTimer = null;
  }
  this._playing = false;
  this._resting.clear();
  this._joinWait.clear();
  this._completed = 0;
  this._clearChoice();
  this._render();
  this._notify();
};

// stats reports the numbers the toolbar shows.
TokenSimulation.prototype.stats = function () {
  let live = 0;
  for (const n of this._resting.values()) live += n;
  return {
    active: this._active,
    playing: this._playing,
    live,
    completed: this._completed,
    waiting: this._choiceFlows.size > 0,
  };
};

// --- Internals ---------------------------------------------------------------------

TokenSimulation.prototype._rest = function (id, delta) {
  const next = (this._resting.get(id) || 0) + delta;
  if (next > 0) this._resting.set(id, next);
  else this._resting.delete(id);
};

TokenSimulation.prototype._onClick = function (el) {
  if (!el) return;
  // Offer a choice: clicking one of the highlighted outgoing flows sends a token that
  // way. This is the core learning interaction at an exclusive/inclusive gateway.
  if (isSequenceFlow(el) && this._choiceFlows.has(el.id)) {
    const src = el.source;
    if (src && (this._resting.get(src.id) || 0) > 0) {
      this._rest(src.id, -1);
      this._clearChoice();
      this._render();
      this._travel(el);
    }
    return;
  }
  if (isStart(el)) {
    this.spawnAt(el);
    return;
  }
  // Hand-advance a token resting on a normal element (paused single-step by click).
  if ((this._resting.get(el.id) || 0) > 0 && !needsChoice(el)) {
    this._departOne(el);
  }
};

// _departOne removes one resting token from an element and moves it onward: off the end
// of the graph (completed), a decision (offer a choice), or a fork/step along flows.
TokenSimulation.prototype._departOne = function (el) {
  if ((this._resting.get(el.id) || 0) <= 0) return;
  const outs = outFlows(el);
  if (outs.length === 0) {
    // No outgoing flow — an end event or a dangling element. The token leaves.
    this._rest(el.id, -1);
    this._completed++;
    this._flash(el);
    this._render();
    this._notify();
    return;
  }
  if (needsChoice(el)) {
    this._offerChoice(el);
    return;
  }
  // Parallel gateway or implicit split: fork a token onto every outgoing flow. A single
  // outgoing flow is just the trivial case (move one token along).
  this._rest(el.id, -1);
  this._render();
  this._notify();
  for (const f of outs) this._travel(f);
};

// _offerChoice highlights a diverging gateway's outgoing flows and waits for the user to
// click one. The modelled default flow is marked so the intended path is visible.
TokenSimulation.prototype._offerChoice = function (el) {
  this._clearChoice();
  const def = defaultFlowId(el);
  for (const f of outFlows(el)) {
    this._choiceFlows.add(f.id);
    this._addMarker(f.id, "atlas-sim-choice");
    if (f.id === def) this._addMarker(f.id, "atlas-sim-default");
  }
  this._addMarker(el.id, "atlas-sim-deciding");
  this._notify();
};

TokenSimulation.prototype._clearChoice = function () {
  for (const id of this._choiceFlows) {
    this._removeMarker(id, "atlas-sim-choice");
    this._removeMarker(id, "atlas-sim-default");
  }
  this._choiceFlows.clear();
  // Drop any "deciding" glow left on gateways.
  for (const id of Array.from(this._markers)) {
    if (id.endsWith("::atlas-sim-deciding")) {
      const [elId] = id.split("::");
      this._removeMarker(elId, "atlas-sim-deciding");
    }
  }
};

// _travel animates a dot along a sequence flow, then delivers the token to its target.
TokenSimulation.prototype._travel = function (flow) {
  const target = flow.target;
  if (!target) return;
  const epoch = this._epoch;
  this._addMarker(flow.id, "atlas-sim-flow");
  this._animateDot(flow, () => this._epoch !== epoch).then(() => {
    this._removeMarker(flow.id, "atlas-sim-flow");
    if (this._epoch !== epoch) return; // superseded by a reset
    this._arrive(target, flow);
  });
};

// _arrive delivers a token to an element: it joins at a converging AND/OR gateway,
// completes at an end event, or comes to rest (and, if playing, is scheduled to move on).
TokenSimulation.prototype._arrive = function (target, viaFlow) {
  if (isJoin(target)) {
    const wait = this._joinWait.get(target.id) || new Map();
    wait.set(viaFlow.id, (wait.get(viaFlow.id) || 0) + 1);
    this._joinWait.set(target.id, wait);
    const incoming = inFlows(target);
    const ready = incoming.every((f) => (wait.get(f.id) || 0) > 0);
    if (ready) {
      for (const f of incoming) wait.set(f.id, wait.get(f.id) - 1);
      this._rest(target.id, 1); // the merged token now sits on the gateway
    }
    this._render();
    this._notify();
    if (ready && this._playing) this._pump();
    return;
  }
  if (isEnd(target)) {
    this._completed++;
    this._flash(target);
    this._render();
    this._notify();
    return;
  }
  this._rest(target.id, 1);
  this._render();
  this._notify();
  if (this._playing) this._pump();
};

// _pump auto-advances the flow while playing: on each tick it departs one eligible
// token (not one blocked on a choice) and re-arms itself if work remains. Departures
// animate asynchronously; taking the resting token is synchronous, so a token is never
// double-fired.
TokenSimulation.prototype._pump = function () {
  if (this._pumpTimer) return; // a tick is already scheduled
  if (!this._playing || !this._active) return;
  const delay = 650 / this._speed;
  this._pumpTimer = setTimeout(() => {
    this._pumpTimer = null;
    if (!this._playing || !this._active) return;
    let moved = false;
    for (const [id, n] of Array.from(this._resting)) {
      if (n <= 0) continue;
      const el = this._registry.get(id);
      if (el && !needsChoice(el)) {
        this._departOne(el);
        moved = true;
        break; // one departure per tick keeps the animation legible
      }
    }
    // Keep pumping while tokens remain that can move on their own. If everything left is
    // waiting on a user choice, stop until that click arrives.
    if (this._anyAutoMovable() || moved) this._pump();
  }, delay);
};

TokenSimulation.prototype._anyAutoMovable = function () {
  for (const [id, n] of this._resting) {
    if (n <= 0) continue;
    const el = this._registry.get(id);
    if (el && !needsChoice(el)) return true;
  }
  return false;
};

// --- Rendering ---------------------------------------------------------------------

// _render repaints the resting-token badges and the partial-join indicators.
TokenSimulation.prototype._render = function () {
  for (const id of this._overlayIds) {
    try {
      this._overlays.remove(id);
    } catch {
      /* overlay already gone */
    }
  }
  this._overlayIds = [];
  // Mark where tokens currently sit and badge the count.
  for (const [id, n] of this._resting) {
    if (n <= 0) continue;
    this._addMarker(id, "atlas-sim-here");
    try {
      this._overlayIds.push(
        this._overlays.add(id, "atlas-sim-token", {
          position: { bottom: 6, right: 6 },
          html: `<span class="atlas-sim-token" title="${n} token${n > 1 ? "s" : ""} here">${n}</span>`,
        }),
      );
    } catch {
      /* shape without graphics — skip */
    }
  }
  // Remove "here" glow from elements that no longer hold a token.
  for (const id of Array.from(this._markers)) {
    if (!id.endsWith("::atlas-sim-here")) continue;
    const [elId] = id.split("::");
    if ((this._resting.get(elId) || 0) <= 0) this._removeMarker(elId, "atlas-sim-here");
  }
  // Partial-join indicators: how many of a join's incoming branches have arrived.
  for (const [gwId, wait] of this._joinWait) {
    const el = this._registry.get(gwId);
    if (!el) continue;
    let arrived = 0;
    for (const c of wait.values()) arrived += c;
    if (arrived <= 0) continue;
    try {
      this._overlayIds.push(
        this._overlays.add(gwId, "atlas-sim-join", {
          position: { top: -8, left: -8 },
          html: `<span class="atlas-sim-join" title="waiting to join">${arrived}/${inFlows(el).length}</span>`,
        }),
      );
    } catch {
      /* skip */
    }
  }
};

// _drawStartAffordances puts a "spawn a token here" play glyph on every start event, so
// where to begin is obvious the moment simulation turns on.
TokenSimulation.prototype._drawStartAffordances = function () {
  this._clearStartAffordances();
  this._registry.forEach((el) => {
    if (!isStart(el)) return;
    // The affordance is an HTML overlay sitting *above* the canvas, so a click on it
    // never reaches bpmn-js as an `element.click`. It needs its own DOM listener —
    // hand bpmn-js a real element (not a markup string) and wire the click here.
    const btn = document.createElement("span");
    btn.className = "atlas-sim-spawn";
    btn.title = "Spawn a token here";
    btn.innerHTML = "&#9654;";
    btn.addEventListener("click", (ev) => {
      ev.stopPropagation();
      this.spawnAt(el);
    });
    try {
      this._startIds.push(
        this._overlays.add(el.id, "atlas-sim-spawn", {
          position: { top: -14, left: -14 },
          html: btn,
        }),
      );
    } catch {
      /* skip */
    }
  });
};

TokenSimulation.prototype._clearStartAffordances = function () {
  for (const id of this._startIds) {
    try {
      this._overlays.remove(id);
    } catch {
      /* gone */
    }
  }
  this._startIds = [];
};

// _flash briefly pulses an element as a token lands on / leaves through it.
TokenSimulation.prototype._flash = function (el) {
  this._addMarker(el.id, "atlas-sim-hit");
  setTimeout(() => this._removeMarker(el.id, "atlas-sim-hit"), 650);
};

TokenSimulation.prototype._addMarker = function (id, cls) {
  try {
    this._canvas.addMarker(id, cls);
    this._markers.add(id + "::" + cls);
  } catch {
    /* element gone */
  }
};

TokenSimulation.prototype._removeMarker = function (id, cls) {
  try {
    this._canvas.removeMarker(id, cls);
  } catch {
    /* element gone */
  }
  this._markers.delete(id + "::" + cls);
};

// _animateDot moves a token dot along a flow's waypoints over a speed-scaled duration in
// a dedicated SVG layer (diagram coordinates, so it tracks pan/zoom). cancelled() aborts
// mid-flight (a reset or deactivate bumps the epoch). Resolves when the dot arrives.
TokenSimulation.prototype._animateDot = function (flow, cancelled) {
  const wps =
    flow.waypoints && flow.waypoints.length >= 2
      ? flow.waypoints.map((w) => ({ x: w.x, y: w.y }))
      : null;
  if (!wps) return Promise.resolve();
  const layer = this._canvas.getLayer("atlas-sim", 780);
  const NS = "http://www.w3.org/2000/svg";
  const g = document.createElementNS(NS, "g");
  const halo = document.createElementNS(NS, "circle");
  halo.setAttribute("r", "12");
  halo.setAttribute("class", "atlas-sim-halo");
  const dot = document.createElementNS(NS, "circle");
  dot.setAttribute("r", "7");
  dot.setAttribute("class", "atlas-sim-dot");
  g.appendChild(halo);
  g.appendChild(dot);
  layer.appendChild(g);

  const segs = [];
  let total = 0;
  for (let i = 1; i < wps.length; i++) {
    const a = wps[i - 1],
      b = wps[i];
    const len = Math.hypot(b.x - a.x, b.y - a.y);
    segs.push({ a, b, len });
    total += len;
  }
  const dur = Math.max(220, Math.min(1400, 40 + total * 2.2)) / this._speed;

  return new Promise((resolve) => {
    let start = null;
    const frame = (ts) => {
      if (start == null) start = ts;
      const t = total ? Math.min(1, (ts - start) / dur) : 1;
      let d = t * total,
        x = wps[0].x,
        y = wps[0].y;
      for (const s of segs) {
        if (d <= s.len || s === segs[segs.length - 1]) {
          const k = s.len ? Math.min(1, d / s.len) : 1;
          x = s.a.x + (s.b.x - s.a.x) * k;
          y = s.a.y + (s.b.y - s.a.y) * k;
          break;
        }
        d -= s.len;
      }
      g.setAttribute("transform", `translate(${x} ${y})`);
      if (t < 1 && !cancelled()) {
        requestAnimationFrame(frame);
      } else {
        g.remove();
        resolve();
      }
    };
    requestAnimationFrame(frame);
  });
};

// _notify tells the toolbar (via the eventBus) that state changed, so it can refresh
// its counts and button states without polling.
TokenSimulation.prototype._notify = function () {
  this._eventBus.fire("atlasSim.changed", this.stats());
};

// tokenSimulationModule is the didi module the editor registers with the modeler.
export function tokenSimulationModule() {
  return {
    __init__: ["atlasTokenSimulation"],
    atlasTokenSimulation: ["type", TokenSimulation],
  };
}
