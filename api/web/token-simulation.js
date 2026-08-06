// Token simulation for the Design view — a client-side, engine-free walk-through of a
// BPMN diagram so newcomers can *see* how control flow moves (ADR-0078, extended by
// ADR-0096). It is a teaching aid, not the engine: it never deploys, never talks to the
// server, and makes no claim to execute FEEL, conditions, or scripts. It animates the one
// thing the Design view is about — the shape of the control flow — by moving "tokens"
// along sequence flows, forking and joining at gateways, pausing at choices so the user
// picks the path, and parking at message/timer/signal events so the user fires them and
// watches what happens. ADR-0096 extends the original walkthrough with event triggers, the
// inclusive-gateway subset split / quiescence OR-join, and an auto-decide mode.
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
const isEventBased = (el) => el.type === "bpmn:EventBasedGateway";
const isBoundary = (el) => el.type === "bpmn:BoundaryEvent";

// Event definitions carried by an event/task business object decide whether an element
// *catches* (waits for something) or *throws* (emits something and continues).
const eventDefs = (el) => (el.businessObject && el.businessObject.eventDefinitions) || [];
const hasDef = (el, t) => eventDefs(el).some((d) => d.$type === t);
const isMessageEvent = (el) => hasDef(el, "bpmn:MessageEventDefinition");
const isTimerEvent = (el) => hasDef(el, "bpmn:TimerEventDefinition");
const isSignalEvent = (el) => hasDef(el, "bpmn:SignalEventDefinition");

// messageName / signalName read the correlated name off the event definition, so a throw
// can find the catches it wakes. The simulation matches on name alone — it deliberately
// does not evaluate correlation keys (that is the engine's job, not a teaching aid's).
const defName = (el, defType, ref) => {
  const d = eventDefs(el).find((x) => x.$type === defType);
  return (d && d[ref] && d[ref].name) || null;
};
const messageName = (el) => defName(el, "bpmn:MessageEventDefinition", "messageRef");
const signalName = (el) => defName(el, "bpmn:SignalEventDefinition", "signalRef");

// A catch parks the token until the modelled event "occurs" — the user fires it. Message,
// timer, signal and conditional intermediate catch events and receive tasks all wait.
const isCatch = (el) =>
  el.type === "bpmn:IntermediateCatchEvent" || el.type === "bpmn:ReceiveTask";

// A throw emits a message/signal and the token continues: a send task, a message/signal
// intermediate throw, or a message/signal end event. A plain throw with no event
// definition is just a pass-through marker.
const isThrow = (el) =>
  el.type === "bpmn:SendTask" ||
  (el.type === "bpmn:IntermediateThrowEvent" && (isMessageEvent(el) || isSignalEvent(el))) ||
  (isEnd(el) && (isMessageEvent(el) || isSignalEvent(el)));

// A catch-like target can receive a thrown message/signal dot — a catch event, a boundary
// event, or a start event (a message can begin a new instance). A throw is never a target.
const isCatchLike = (el) => isCatch(el) || isBoundary(el) || isStart(el);

// arrivesFromEventGateway: a catch immediately after an event-based gateway does not park
// again — the gateway's own "which event fires first?" choice already represented it.
const arrivesFromEventGateway = (el) =>
  inFlows(el).some((f) => f.source && isEventBased(f.source));

// needsTrigger is true when a resting token here waits for the user to fire an external
// event before it can move on.
const needsTrigger = (el) => isCatch(el) && !arrivesFromEventGateway(el);

// needsChoice is true for a diverging gateway where the user should pick the path(s):
// exclusive/event-based (pick one) and inclusive (pick one or more) with more than one
// outgoing flow. A parallel gateway forks unconditionally, and a plain task with two
// outgoing flows is an implicit fork — only these "which way?" gateways pause.
function needsChoice(el) {
  const t = el.type;
  return (
    (t === "bpmn:ExclusiveGateway" ||
      t === "bpmn:InclusiveGateway" ||
      t === "bpmn:EventBasedGateway") &&
    outFlows(el).length > 1
  );
}

// A parallel converging gateway joins by counting: one token on *every* incoming flow.
const isParallelJoin = (el) => isParallel(el) && inFlows(el).length > 1;
// An inclusive converging gateway joins by quiescence: it fires once no still-active token
// can reach it, merging whatever arrived. This is the standard OR-join teaching model and
// the reason the inclusive split may safely activate only a subset of its branches.
const isInclusiveJoin = (el) => isInclusive(el) && inFlows(el).length > 1;

// defaultFlowId returns the id of a gateway's default sequence flow, if it has one, so a
// choice can highlight (and auto-mode can prefer) the modelled default.
function defaultFlowId(el) {
  const bo = el.businessObject;
  return (bo && bo.default && bo.default.id) || null;
}

const centerOf = (el) => ({ x: el.x + (el.width || 0) / 2, y: el.y + (el.height || 0) / 2 });

// isEventSubStart reports whether a start event is the trigger of an event subprocess — it
// sits directly inside a subprocess whose triggeredByEvent flag is set (ADR-0082). Such a
// start is fired by its event while the enclosing scope runs, not spawned by the user, so
// it gets a "fire" affordance rather than the process-start spawn glyph.
const isEventSubStart = (el) => {
  const p = el.parent && el.parent.businessObject;
  return !!(p && p.$type === "bpmn:SubProcess" && p.triggeredByEvent === true);
};
// A process start is a plain entry point the user spawns tokens from — every start event
// except an event-subprocess trigger.
const isProcessStart = (el) => isStart(el) && !isEventSubStart(el);
// An interrupting event-subprocess start terminates its enclosing scope when it fires; a
// non-interrupting one runs alongside (ADR-0082).
const isInterruptingSub = (el) => el.businessObject && el.businessObject.isInterrupting !== false;

// Multi-instance activities (ADR-0077) run their body several times — in parallel or in
// sequence. Data-driven multiplicity (a collection size) is not something the simulation
// evaluates, so it visualises a clearly-labelled number of instances. That number is the
// toolbar-configurable default, unless the model pins a fixed loop cardinality (below).
const MI_DEFAULT_INSTANCES = 3;
const loopChars = (el) => el.businessObject && el.businessObject.loopCharacteristics;
const isMultiInstance = (el) => {
  const lc = loopChars(el);
  return !!(lc && lc.$type === "bpmn:MultiInstanceLoopCharacteristics");
};
const isSequentialMI = (el) => {
  const lc = loopChars(el);
  return !!(lc && lc.isSequential === true);
};
// literalCardinality reads a modelled fixed loop cardinality — a `<loopCardinality>` that is
// a plain positive integer (optionally FEEL-prefixed `=3`). A data-driven or expression
// cardinality returns null, so the caller falls back to the configurable default.
const literalCardinality = (el) => {
  const lc = loopChars(el);
  const body = lc && lc.loopCardinality && lc.loopCardinality.body;
  if (body == null) return null;
  const m = String(body).replace(/^=\s*/, "").trim();
  if (!/^\d+$/.test(m)) return null;
  const n = parseInt(m, 10);
  return n > 0 ? n : null;
};

// --- The simulation service --------------------------------------------------------

export function TokenSimulation(eventBus, elementRegistry, canvas, overlays) {
  this._eventBus = eventBus;
  this._registry = elementRegistry;
  this._canvas = canvas;
  this._overlays = overlays;

  this._active = false;
  this._playing = false;
  this._auto = false; // auto-decide: resolve choices and fire triggers without clicks
  this._speed = 1;
  this._miCount = MI_DEFAULT_INSTANCES; // simulated instances for data-driven multi-instance

  // resting: how many tokens currently sit ON each element, keyed by element id. A token
  // "rests" between moves; the badge overlay shows the count.
  this._resting = new Map();
  // joinWait: for a converging gateway, how many tokens have arrived per incoming flow.
  // A parallel join fires once every flow has one; an inclusive join fires on quiescence.
  this._joinWait = new Map();
  // inflight: tokens currently animating toward a target element, keyed by target id. The
  // OR-join quiescence check must count these as "still coming".
  this._inflight = new Map();
  // deciding: gateways currently offering a choice, keyed by gateway id →
  // { flows:Set, armed:Set, inclusive:bool }. Multiple gateways can be pending at once.
  this._deciding = new Map();
  this._flowOwner = new Map(); // offered-flow id → owning gateway id, for click routing
  this._boundaries = new Map(); // host activity id → [boundary elements], indexed on import
  this._eventSubStarts = []; // event-subprocess start events, indexed on import
  this._miRemaining = new Map(); // multi-instance activity id → { left, total } instances
  this._reach = new Map(); // memoised "can `from` reach `to`?" over sequence flows
  this._completed = 0; // tokens that reached an end event / ran off the graph

  this._overlayIds = []; // token/join/fire badges we own, so we clear only ours
  this._startIds = []; // "spawn" affordance overlays on start events
  this._markers = new Set(); // element ids we've marked, cleared on teardown
  this._epoch = 0; // bumped on reset/deactivate to abort in-flight animations
  this._pumpTimer = null; // auto-advance timer while playing

  // Editing must not happen mid-simulation: cancel the interaction gestures at high
  // priority while active. The palette and context pad are hidden by CSS (the editor adds
  // a `.sim-active` class); these cover keyboard/drag entry points bpmn-js still wires up.
  // Returning false stops bpmn-js's default handling.
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
  // choice, confirm an inclusive selection, fire a parked catch/boundary, or hand-advance
  // a token resting on a normal element.
  eventBus.on("element.click", 2000, (e) => {
    if (!this._active) return;
    this._onClick(e.element);
    return false; // suppress the default selection while simulating
  });

  // A fresh diagram import invalidates all token state and the cached indexes.
  eventBus.on("import.done", () => {
    this._reach.clear();
    if (this._active) {
      this._indexDiagram();
      this.reset();
    }
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
    this._indexDiagram();
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

// setMiCount sets how many instances a data-driven multi-instance activity runs in the
// simulation (a model with a fixed loop cardinality still uses its own number). Clamped to a
// legible range; it applies to activities entered from now on, not ones already counting.
TokenSimulation.prototype.setMiCount = function (n) {
  const v = Math.floor(Number(n));
  this._miCount = Math.max(1, Math.min(20, Number.isFinite(v) ? v : MI_DEFAULT_INSTANCES));
};

// _miInstancesFor returns the instance count to run for a multi-instance activity: the
// modelled fixed loop cardinality if there is one, otherwise the configurable default.
TokenSimulation.prototype._miInstancesFor = function (el) {
  return literalCardinality(el) || this._miCount;
};

// setAuto toggles auto-decide mode: while playing, choices resolve themselves (the default
// flow if one is modelled, otherwise every branch for inclusive / the first for exclusive)
// and parked catch events fire on their own, so Play runs end-to-end without clicks. It is
// off by default — the manual "which way?" and "fire the event" gestures are the lesson.
TokenSimulation.prototype.setAuto = function (on) {
  this._auto = !!on;
  this._notify();
  if (this._playing) this._pump();
};

// spawnAt drops a fresh token on an element (normally a start event) and, if playing, lets
// it start moving.
TokenSimulation.prototype.spawnAt = function (el) {
  if (!this._active || !el) return;
  this._rest(el.id, 1);
  this._land(el);
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

// step advances the flow by one visible move: it departs one resting token that can move
// on its own, or — failing that — fires one parked catch event. Choices are left for a
// click; picking the path is the point.
TokenSimulation.prototype.step = function () {
  if (!this._active) return;
  for (const [id, n] of this._resting) {
    if (n <= 0) continue;
    const el = this._registry.get(id);
    if (el && !needsChoice(el) && !needsTrigger(el)) {
      this._emit(el);
      return;
    }
  }
  for (const [id, n] of this._resting) {
    if (n <= 0) continue;
    const el = this._registry.get(id);
    if (el && needsTrigger(el)) {
      this._fireTrigger(el);
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
  this._inflight.clear();
  this._miRemaining.clear();
  this._completed = 0;
  this._clearAllDeciding();
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
    auto: this._auto,
    live,
    completed: this._completed,
    deciding: this._deciding.size > 0,
    waiting: this._hasPendingTrigger(),
  };
};

// --- Internals ---------------------------------------------------------------------

TokenSimulation.prototype._rest = function (id, delta) {
  const next = (this._resting.get(id) || 0) + delta;
  if (next > 0) this._resting.set(id, next);
  else this._resting.delete(id);
};

// _indexDiagram builds the host→boundary-events lookup and the event-subprocess-start list
// once, so rendering their fire affordances doesn't rescan the registry each frame.
TokenSimulation.prototype._indexDiagram = function () {
  this._boundaries.clear();
  this._eventSubStarts = [];
  this._registry.forEach((el) => {
    if (isBoundary(el)) {
      const host = el.host || (el.businessObject && el.businessObject.attachedToRef);
      const hostId = host && host.id;
      if (!hostId) return;
      const list = this._boundaries.get(hostId) || [];
      list.push(el);
      this._boundaries.set(hostId, list);
    } else if (isStart(el) && isEventSubStart(el)) {
      this._eventSubStarts.push(el);
    }
  });
};

// _totalLive reports whether the process is "running" — any token resting, in flight, or
// parked in a join. Event-subprocess triggers are only offered while the scope is live.
TokenSimulation.prototype._totalLive = function () {
  let n = 0;
  for (const c of this._resting.values()) n += c;
  for (const c of this._inflight.values()) n += c;
  for (const w of this._joinWait.values()) for (const c of w.values()) n += c;
  return n;
};

TokenSimulation.prototype._hasPendingTrigger = function () {
  for (const [id, n] of this._resting) {
    if (n <= 0) continue;
    const el = this._registry.get(id);
    if (el && needsTrigger(el)) return true;
  }
  return false;
};

TokenSimulation.prototype._onClick = function (el) {
  if (!el) return;
  // Take / arm an offered choice: clicking a highlighted outgoing flow sends (exclusive)
  // or arms (inclusive) that branch. This is the core learning interaction at a gateway.
  if (isSequenceFlow(el) && this._flowOwner.has(el.id)) {
    this._onChoiceFlowClick(el);
    return;
  }
  // Confirm an inclusive multi-select by clicking the gateway itself.
  const dec = this._deciding.get(el.id);
  if (dec && dec.inclusive && dec.armed.size > 0) {
    this._fireInclusive(el, Array.from(dec.armed));
    return;
  }
  // Fire a parked boundary event while its host holds a token.
  if (isBoundary(el)) {
    this._fireBoundary(el);
    return;
  }
  if (isEventSubStart(el)) {
    // Trigger the handler by clicking its start, but only while the scope is running.
    if ((this._resting.get(el.id) || 0) <= 0 && this._totalLive() > 0) this._fireEventSub(el);
    return;
  }
  if (isStart(el)) {
    this.spawnAt(el);
    return;
  }
  if ((this._resting.get(el.id) || 0) <= 0) return;
  // Fire a parked catch event, or hand-advance a token resting on a normal element.
  if (needsTrigger(el)) this._fireTrigger(el);
  else if (!needsChoice(el)) this._emit(el);
};

TokenSimulation.prototype._onChoiceFlowClick = function (flow) {
  const gwId = this._flowOwner.get(flow.id);
  const gw = this._registry.get(gwId);
  const dec = this._deciding.get(gwId);
  if (!gw || !dec || (this._resting.get(gwId) || 0) <= 0) return;
  if (dec.inclusive) {
    // Toggle this branch in the armed subset; the user confirms by clicking the gateway.
    if (dec.armed.has(flow.id)) {
      dec.armed.delete(flow.id);
      this._removeMarker(flow.id, "atlas-sim-armed");
    } else {
      dec.armed.add(flow.id);
      this._addMarker(flow.id, "atlas-sim-armed");
    }
    this._notify();
  } else {
    // Exclusive / event-based: the clicked branch is taken immediately.
    this._takeSingle(gw, flow);
  }
};

// _land settles a token that has just come to rest on an element: it offers a choice at a
// diverging gateway, parks at a catch event, or — for a plain element — moves on when
// playing. It also re-checks the OR-joins, which a new resting token may have unblocked.
TokenSimulation.prototype._land = function (el) {
  this._render();
  this._notify();
  if (needsChoice(el)) {
    this._offerChoice(el);
  } else if (!needsTrigger(el)) {
    if (this._playing) this._pump();
  }
  this._settleJoins();
};

// _emit moves one token out of an element along its outgoing flow(s): a throw first fires
// its message/signal, then the token forks onto every outgoing flow (the trivial case is a
// single flow). No outgoing flow means the token leaves the graph (completed).
TokenSimulation.prototype._emit = function (el) {
  if ((this._resting.get(el.id) || 0) <= 0) return;
  // Multi-instance: consume one instance per move, keeping the token on the activity until
  // the last instance is done — so a step / Play visibly counts the body running N times.
  if (isMultiInstance(el)) {
    let mi = this._miRemaining.get(el.id);
    if (mi === undefined) {
      const total = this._miInstancesFor(el); // seed for a token spawned straight onto it
      mi = { left: total, total };
    }
    if (mi.left > 1) {
      this._miRemaining.set(el.id, { left: mi.left - 1, total: mi.total });
      this._flash(el);
      this._render();
      this._notify();
      if (this._playing) this._pump();
      return;
    }
    this._miRemaining.delete(el.id); // last instance — fall through and leave the activity
  }
  if (isThrow(el)) this._throwEvent(el);
  const outs = outFlows(el);
  this._rest(el.id, -1);
  if (outs.length === 0) {
    this._completed++;
    this._flash(el);
    this._render();
    this._notify();
    this._settleJoins();
    return;
  }
  this._render();
  this._notify();
  for (const f of outs) this._travel(f);
};

// _fireTrigger releases a token parked on a catch event — "the message/timer/signal
// occurred now" — and lets it continue.
TokenSimulation.prototype._fireTrigger = function (el) {
  if ((this._resting.get(el.id) || 0) <= 0) return;
  this._flash(el);
  this._emit(el);
};

// _throwEvent visualises a message/signal throw: a dot flies from the throwing element to
// every catch-like element that names the same message (1:1) or signal (broadcast). What
// happens when the dot lands depends on the target (see _deliverToCatch): a modelled throw
// that reaches a *waiting* catch delivers — it fires it — so throw→catch correlation
// actually completes; a start event begins a new instance; nothing waiting is only pinged.
// Matching is by message/signal name only; correlation keys are the engine's job.
TokenSimulation.prototype._throwEvent = function (el) {
  const mName = messageName(el);
  const sName = signalName(el);
  if (!mName && !sName) return;
  const from = centerOf(el);
  this._registry.forEach((t) => {
    if (t === el || !isCatchLike(t)) return;
    const match = (mName && messageName(t) === mName) || (sName && signalName(t) === sName);
    if (!match) return;
    const epoch = this._epoch;
    this._animateDot([from, centerOf(t)], () => this._epoch !== epoch, "atlas-sim-msg-dot").then(
      () => {
        if (this._epoch !== epoch || !this._active) return;
        this._deliverToCatch(t);
      },
    );
  });
};

// _deliverToCatch resolves a thrown message/signal that has reached a target element.
TokenSimulation.prototype._deliverToCatch = function (t) {
  if (isEventSubStart(t)) {
    // Trigger the event subprocess, but only while its scope is actually running.
    if (this._totalLive() > 0) this._fireEventSub(t);
    else this._ping(t);
    return;
  }
  if (isProcessStart(t)) {
    this._flash(t);
    this.spawnAt(t); // the message starts a new instance of this process
    return;
  }
  if (isBoundary(t)) {
    // Fire the boundary if its host activity is holding a token; otherwise just show it.
    const host = t.host || (t.businessObject && t.businessObject.attachedToRef);
    if (host && (this._resting.get(host.id) || 0) > 0) this._fireBoundary(t);
    else this._ping(t);
    return;
  }
  // A catch / receive task with a token already waiting: the correlated message has arrived,
  // so it delivers — fire it and let the token continue. This is what makes a throw in one
  // pool actually complete a catch in another. With nothing waiting there is no token to
  // release (the message finds no one home), so it is only pinged; a catch reached before
  // its token, or a timer/external event, still waits for a manual fire or Auto-decide.
  if (needsTrigger(t) && (this._resting.get(t.id) || 0) > 0) this._fireTrigger(t);
  else this._ping(t);
};

// _offerChoice highlights a diverging gateway's outgoing flows and waits for the user.
// Exclusive/event-based take the first click; inclusive arms a subset and fires on the
// gateway click. The modelled default flow is marked so the intended path is visible.
TokenSimulation.prototype._offerChoice = function (el) {
  if (this._deciding.has(el.id)) return;
  const inclusive = isInclusive(el);
  const dec = { flows: new Set(), armed: new Set(), inclusive };
  this._deciding.set(el.id, dec);
  const def = defaultFlowId(el);
  for (const f of outFlows(el)) {
    dec.flows.add(f.id);
    this._flowOwner.set(f.id, el.id);
    this._addMarker(f.id, "atlas-sim-choice");
    if (f.id === def) this._addMarker(f.id, "atlas-sim-default");
  }
  this._addMarker(el.id, "atlas-sim-deciding");
  this._notify();
};

// _clearDeciding drops one gateway's choice highlighting and bookkeeping.
TokenSimulation.prototype._clearDeciding = function (gwId) {
  const dec = this._deciding.get(gwId);
  if (!dec) return;
  for (const fid of dec.flows) {
    this._removeMarker(fid, "atlas-sim-choice");
    this._removeMarker(fid, "atlas-sim-default");
    this._removeMarker(fid, "atlas-sim-armed");
    this._flowOwner.delete(fid);
  }
  this._removeMarker(gwId, "atlas-sim-deciding");
  this._deciding.delete(gwId);
};

TokenSimulation.prototype._clearAllDeciding = function () {
  for (const gwId of Array.from(this._deciding.keys())) this._clearDeciding(gwId);
};

// _takeSingle sends the gateway's token down one chosen flow (exclusive / event-based). If
// another token still rests on the gateway, the choice stays offered for it.
TokenSimulation.prototype._takeSingle = function (gw, flow) {
  if ((this._resting.get(gw.id) || 0) <= 0) return;
  this._rest(gw.id, -1);
  if ((this._resting.get(gw.id) || 0) <= 0) this._clearDeciding(gw.id);
  this._render();
  this._notify();
  this._travel(flow);
};

// _fireInclusive forks the gateway's token onto the armed subset of branches (one or more)
// — the inclusive split. The paired OR-join later fires on quiescence, so activating only
// some branches still converges cleanly.
TokenSimulation.prototype._fireInclusive = function (gw, flowIds) {
  if ((this._resting.get(gw.id) || 0) <= 0 || flowIds.length === 0) return;
  this._rest(gw.id, -1);
  this._clearDeciding(gw.id);
  // Another token still waiting on this gateway re-opens the choice for its own decision.
  if ((this._resting.get(gw.id) || 0) > 0) this._offerChoice(gw);
  this._render();
  this._notify();
  for (const fid of flowIds) {
    const f = this._registry.get(fid);
    if (f) this._travel(f);
  }
};

// _fireBoundary fires an event attached to an activity that currently holds a token.
// Interrupting cancels the activity (the token leaves via the boundary); non-interrupting
// spawns a parallel token out the boundary and leaves the activity running.
TokenSimulation.prototype._fireBoundary = function (b) {
  const host = b.host || (b.businessObject && b.businessObject.attachedToRef);
  if (!host || (this._resting.get(host.id) || 0) <= 0) return;
  const interrupting = b.businessObject.cancelActivity !== false;
  if (interrupting) {
    this._rest(host.id, -1);
    this._clearDeciding(host.id);
  }
  this._flash(b);
  const outs = outFlows(b);
  this._render();
  this._notify();
  if (outs.length === 0) {
    this._completed++;
  } else {
    for (const f of outs) this._travel(f);
  }
  this._settleJoins();
};

// _fireEventSub triggers an event subprocess by dropping a token on its start event
// (ADR-0082). An interrupting trigger first terminates the enclosing scope — every other
// live token, join, and in-flight animation — because the handler pre-empts the process; a
// non-interrupting trigger simply runs alongside. Note: the simulation is flat, so it
// treats the scope as the whole process; a nested event subprocess reads as process-scoped.
TokenSimulation.prototype._fireEventSub = function (start) {
  if ((this._resting.get(start.id) || 0) > 0) return; // already running
  if (isInterruptingSub(start)) {
    this._epoch++; // abort in-flight dots — the scope is being torn down
    this._resting.clear();
    this._joinWait.clear();
    this._inflight.clear();
    this._miRemaining.clear();
    this._clearAllDeciding();
  }
  this._flash(start);
  this.spawnAt(start);
};

// _travel animates a dot along a sequence flow, then delivers the token to its target.
TokenSimulation.prototype._travel = function (flow) {
  const target = flow.target;
  if (!target || !flow.waypoints || flow.waypoints.length < 2) return;
  const epoch = this._epoch;
  this._addMarker(flow.id, "atlas-sim-flow");
  this._inflight.set(target.id, (this._inflight.get(target.id) || 0) + 1);
  this._animateDot(flow.waypoints, () => this._epoch !== epoch, "atlas-sim-dot").then(() => {
    this._removeMarker(flow.id, "atlas-sim-flow");
    const n = (this._inflight.get(target.id) || 0) - 1;
    if (n > 0) this._inflight.set(target.id, n);
    else this._inflight.delete(target.id);
    if (this._epoch !== epoch) return; // superseded by a reset
    this._arrive(target, flow);
  });
};

// _arrive delivers a token to an element: it joins at a converging gateway, completes at an
// end event (throwing first if it is a message/signal end), or comes to rest and settles.
TokenSimulation.prototype._arrive = function (target, viaFlow) {
  if (isParallelJoin(target)) {
    const wait = this._joinWait.get(target.id) || new Map();
    wait.set(viaFlow.id, (wait.get(viaFlow.id) || 0) + 1);
    this._joinWait.set(target.id, wait);
    const incoming = inFlows(target);
    const ready = incoming.every((f) => (wait.get(f.id) || 0) > 0);
    if (ready) {
      for (const f of incoming) wait.set(f.id, wait.get(f.id) - 1);
      this._rest(target.id, 1);
      this._land(target);
    } else {
      this._render();
      this._notify();
      this._settleJoins(); // a newly parked branch may quiesce a downstream OR-join
    }
    return;
  }
  if (isInclusiveJoin(target)) {
    // Record the arrival; the OR-join fires later, once no token can still reach it.
    const wait = this._joinWait.get(target.id) || new Map();
    wait.set(viaFlow.id, (wait.get(viaFlow.id) || 0) + 1);
    this._joinWait.set(target.id, wait);
    this._render();
    this._notify();
    this._settleJoins();
    return;
  }
  if (isEnd(target)) {
    if (isThrow(target)) this._throwEvent(target);
    this._completed++;
    this._flash(target);
    this._render();
    this._notify();
    this._settleJoins();
    return;
  }
  // A multi-instance activity runs its body several times before the token moves on; seed
  // the instance counter so the badge shows the multiplicity from the moment it arrives.
  if (isMultiInstance(target) && !this._miRemaining.has(target.id)) {
    const total = this._miInstancesFor(target);
    this._miRemaining.set(target.id, { left: total, total });
  }
  this._rest(target.id, 1);
  this._land(target);
};

// _settleJoins fires every inclusive OR-join that has become quiescent: it has at least one
// arrived token and no still-active token can reach it, so nothing more is coming. The
// arrived tokens merge into a single token that continues. Firing a join re-lands a token,
// which re-enters this pass — so re-read _joinWait each step and skip anything already
// consumed, to never fire a join twice.
TokenSimulation.prototype._settleJoins = function () {
  for (const gwId of Array.from(this._joinWait.keys())) {
    const wait = this._joinWait.get(gwId);
    if (!wait) continue; // already fired by a re-entrant settle
    const el = this._registry.get(gwId);
    if (!el || !isInclusiveJoin(el)) continue;
    if (!this._orJoinReady(el, wait)) continue;
    this._joinWait.delete(gwId);
    this._rest(gwId, 1); // the merged token now sits on the gateway
    this._land(el);
  }
};

// _orJoinReady is the quiescence test: fire once ≥1 token has arrived and no still-active
// token could still reach the join over the sequence-flow graph. "Active" means a resting
// token, an in-flight token, or a token parked in another gateway's join-wait — any of
// which could later travel into this join.
TokenSimulation.prototype._orJoinReady = function (join, wait) {
  let arrived = 0;
  for (const c of wait.values()) arrived += c;
  if (arrived < 1) return false;
  for (const [id, n] of this._resting) {
    if (n > 0 && id !== join.id && this._canReach(id, join.id)) return false;
  }
  for (const [id, n] of this._inflight) {
    if (n <= 0) continue;
    if (id === join.id) return false; // a token is still on its way in
    if (this._canReach(id, join.id)) return false;
  }
  for (const [gwId, w] of this._joinWait) {
    if (gwId === join.id) continue; // the arrivals we are testing
    let held = 0;
    for (const c of w.values()) held += c;
    if (held > 0 && this._canReach(gwId, join.id)) return false;
  }
  return true;
};

// _canReach reports whether `fromId` can reach `toId` following outgoing sequence flows.
// Memoised per pair; the cache is cleared on import (the graph is otherwise static).
TokenSimulation.prototype._canReach = function (fromId, toId) {
  const key = fromId + "→" + toId;
  const cached = this._reach.get(key);
  if (cached !== undefined) return cached;
  const seen = new Set();
  const stack = [fromId];
  let found = false;
  while (stack.length) {
    const cur = stack.pop();
    if (cur === toId) {
      found = true;
      break;
    }
    if (seen.has(cur)) continue;
    seen.add(cur);
    const el = this._registry.get(cur);
    if (!el) continue;
    for (const f of outFlows(el)) if (f.target) stack.push(f.target.id);
  }
  this._reach.set(key, found);
  return found;
};

// _pump auto-advances the flow while playing: on each tick it moves one eligible token and
// re-arms itself if work remains. In auto mode a "movable" token also includes a gateway
// choice (resolved to the default / a branch) and a parked catch (fired), so Play runs
// end-to-end; in manual mode those wait for a click and the pump idles.
TokenSimulation.prototype._pump = function () {
  if (this._pumpTimer) return; // a tick is already scheduled
  if (!this._playing || !this._active) return;
  const delay = 650 / this._speed;
  this._pumpTimer = setTimeout(() => {
    this._pumpTimer = null;
    if (!this._playing || !this._active) return;
    const moved = this._advanceOne();
    if (moved || this._anyPumpable()) this._pump();
  }, delay);
};

// _advanceOne performs a single pump step and reports whether it moved anything.
TokenSimulation.prototype._advanceOne = function () {
  for (const [id, n] of Array.from(this._resting)) {
    if (n <= 0) continue;
    const el = this._registry.get(id);
    if (!el) continue;
    if (needsChoice(el)) {
      if (this._auto) {
        this._autoDecide(el);
        return true;
      }
      continue;
    }
    if (needsTrigger(el)) {
      if (this._auto) {
        this._fireTrigger(el);
        return true;
      }
      continue;
    }
    this._emit(el);
    return true;
  }
  return false;
};

// _anyPumpable is true if some resting token could move on the next tick (respecting auto
// mode). When only choice/trigger tokens remain in manual mode, the pump idles.
TokenSimulation.prototype._anyPumpable = function () {
  for (const [id, n] of this._resting) {
    if (n <= 0) continue;
    const el = this._registry.get(id);
    if (!el) continue;
    if (needsChoice(el) || needsTrigger(el)) {
      if (this._auto) return true;
      continue;
    }
    return true;
  }
  return false;
};

// _autoDecide resolves a diverging gateway without a click: prefer the modelled default,
// else take every branch for an inclusive gateway or the first branch otherwise.
TokenSimulation.prototype._autoDecide = function (gw) {
  const outs = outFlows(gw);
  const def = defaultFlowId(gw);
  const defFlow = def && outs.find((f) => f.id === def);
  if (isInclusive(gw)) {
    const chosen = defFlow ? [defFlow.id] : outs.map((f) => f.id);
    this._fireInclusive(gw, chosen);
  } else {
    this._takeSingle(gw, defFlow || outs[0]);
  }
};

// --- Rendering ---------------------------------------------------------------------

// _render repaints the resting-token badges, the partial-join indicators, and the fire
// affordances for parked catch/boundary events.
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
    const el = this._registry.get(id);
    if (el && needsTrigger(el)) this._drawFire(el, this._triggerGlyph(el), () => this._fireTrigger(el));
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
    // Offer any boundary events attached to an activity that now holds a token.
    for (const b of this._boundaries.get(id) || []) {
      this._drawFire(b, "&#9889;", () => this._fireBoundary(b), this._boundaryTitle(b));
    }
  }
  // Remove "here" glow from elements that no longer hold a token.
  for (const mid of Array.from(this._markers)) {
    if (!mid.endsWith("::atlas-sim-here")) continue;
    const elId = mid.slice(0, -"::atlas-sim-here".length);
    if ((this._resting.get(elId) || 0) <= 0) this._removeMarker(elId, "atlas-sim-here");
  }
  // Partial-join indicators: how many branches have arrived at a converging gateway.
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
  // Multi-instance badge: how many instances are still to run of the total for this activity
  // (from a modelled cardinality, else the configured default).
  for (const [id, mi] of this._miRemaining) {
    if (!mi || mi.left <= 0) continue;
    const el = this._registry.get(id);
    if (!el) continue;
    const seq = isSequentialMI(el);
    const modelled = literalCardinality(el) != null;
    const src = modelled ? "modelled cardinality" : "simulated";
    try {
      this._overlayIds.push(
        this._overlays.add(id, "atlas-sim-mi", {
          position: { bottom: 4, left: 4 },
          html: `<span class="atlas-sim-mi" title="${seq ? "sequential" : "parallel"} multi-instance — ${mi.left} of ${mi.total} left (${src})">${seq ? "≡" : "‖"} ${mi.left}/${mi.total}</span>`,
        }),
      );
    } catch {
      /* skip */
    }
  }
  // Event-subprocess triggers: while the process is running, each event-sub start offers a
  // fire affordance (unless it already holds a token — the handler is running).
  if (this._totalLive() > 0) {
    for (const start of this._eventSubStarts) {
      if ((this._resting.get(start.id) || 0) > 0) continue;
      this._drawFire(start, this._triggerGlyph(start), () => this._fireEventSub(start), this._eventSubTitle(start));
    }
  }
};

// _triggerGlyph picks an icon for a parked catch event, hinting at what fires it.
TokenSimulation.prototype._triggerGlyph = function (el) {
  if (isTimerEvent(el)) return "&#9203;"; // hourglass
  if (isMessageEvent(el)) return "&#9993;"; // envelope
  if (isSignalEvent(el)) return "&#9889;"; // spark
  return "&#9654;"; // receive task / conditional — a plain "go"
};

TokenSimulation.prototype._boundaryTitle = function (b) {
  const kind = isTimerEvent(b) ? "timer" : isMessageEvent(b) ? "message" : isSignalEvent(b) ? "signal" : "event";
  const mode = b.businessObject.cancelActivity !== false ? "interrupting" : "non-interrupting";
  return `Fire this ${mode} ${kind} boundary event`;
};

TokenSimulation.prototype._eventSubTitle = function (start) {
  const kind = isTimerEvent(start) ? "timer" : isMessageEvent(start) ? "message" : isSignalEvent(start) ? "signal" : "event";
  const mode = isInterruptingSub(start) ? "interrupting" : "non-interrupting";
  return `Trigger this ${mode} ${kind} event subprocess`;
};

// _drawFire adds a clickable "fire this event" affordance on an element. Like the spawn
// glyph it lives above the canvas, so it needs its own DOM listener rather than an
// element.click. The overlay id is tracked in _overlayIds and cleared on the next render.
TokenSimulation.prototype._drawFire = function (el, glyph, onFire, title) {
  const btn = document.createElement("span");
  btn.className = "atlas-sim-fire";
  btn.title = title || "Fire this event — release the waiting token";
  btn.innerHTML = glyph;
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    onFire();
  });
  try {
    this._overlayIds.push(
      this._overlays.add(el.id, "atlas-sim-fire", { position: { top: -14, right: -14 }, html: btn }),
    );
  } catch {
    /* skip */
  }
};

// _drawStartAffordances puts a "spawn a token here" play glyph on every process start
// event, so where to begin is obvious the moment simulation turns on. Event-subprocess
// starts are excluded — they are triggered by their event (a fire affordance), not spawned.
TokenSimulation.prototype._drawStartAffordances = function () {
  this._clearStartAffordances();
  this._registry.forEach((el) => {
    if (!isProcessStart(el)) return;
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
        this._overlays.add(el.id, "atlas-sim-spawn", { position: { top: -14, left: -14 }, html: btn }),
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

// _ping pulses a catch-like element that a thrown message/signal just reached — the visual
// link between throw and catch, without firing the catch itself.
TokenSimulation.prototype._ping = function (el) {
  this._addMarker(el.id, "atlas-sim-ping");
  setTimeout(() => this._removeMarker(el.id, "atlas-sim-ping"), 900);
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

// _animateDot moves a token dot along a list of waypoints over a speed-scaled duration in a
// dedicated SVG layer (diagram coordinates, so it tracks pan/zoom). cancelled() aborts
// mid-flight (a reset or deactivate bumps the epoch). Resolves when the dot arrives. `cls`
// styles the dot — control-flow tokens and thrown-message dots look different.
TokenSimulation.prototype._animateDot = function (waypoints, cancelled, cls) {
  const wps =
    waypoints && waypoints.length >= 2 ? waypoints.map((w) => ({ x: w.x, y: w.y })) : null;
  if (!wps) return Promise.resolve();
  const layer = this._canvas.getLayer("atlas-sim", 780);
  const NS = "http://www.w3.org/2000/svg";
  const g = document.createElementNS(NS, "g");
  const halo = document.createElementNS(NS, "circle");
  halo.setAttribute("r", "12");
  halo.setAttribute("class", "atlas-sim-halo");
  const dot = document.createElementNS(NS, "circle");
  dot.setAttribute("r", "7");
  dot.setAttribute("class", cls || "atlas-sim-dot");
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

// _notify tells the toolbar (via the eventBus) that state changed, so it can refresh its
// counts and button states without polling.
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
