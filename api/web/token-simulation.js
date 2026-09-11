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
// A message name lives in one of two places, depending on what carries it: an *event*
// references the message through its <messageEventDefinition>, while a receive or send *task*
// carries a messageRef of its own and has no event definition at all. The compiler reads both,
// so this does too — reading only the event definition left a send task throwing nothing and a
// receive task no throw could ever reach, which are the two halves of one omission.
const messageName = (el) => {
  const fromDef = defName(el, "bpmn:MessageEventDefinition", "messageRef");
  if (fromDef) return fromDef;
  const ref = el.businessObject && el.businessObject.messageRef;
  return (ref && ref.name) || null;
};
const signalName = (el) => defName(el, "bpmn:SignalEventDefinition", "signalRef");

// A link event (ADR-0132) is BPMN's off-page connector, and the one throw/catch pair with no
// execution semantics of its own: reaching a link throw is identical to having taken a sequence
// flow straight to the link catch of the same name in the same scope, which then flows on. The
// pair stands in for a line the author chose not to draw, so a token must cross it — and a link
// catch is emphatically not an event anyone waits for or fires.
const LINK_DEF = "bpmn:LinkEventDefinition";
const isLinkThrow = (el) => el.type === "bpmn:IntermediateThrowEvent" && hasDef(el, LINK_DEF);
const isLinkCatch = (el) => el.type === "bpmn:IntermediateCatchEvent" && hasDef(el, LINK_DEF);
// A link is matched by the name on its definition, trimmed, within one flow scope — the BPMN
// container the events are drawn in, which is what the compiler pairs them by.
const linkName = (el) => {
  const d = eventDefs(el).find((x) => x.$type === LINK_DEF);
  return ((d && d.name) || "").trim();
};

// A conditional event (ADR-0137) is the catch triggered by data: it fires when a boolean FEEL
// condition over the process's variables becomes true. The simulation evaluates no FEEL, so
// firing it is the person saying the condition now holds — but the element has to say that is
// what it waits for, rather than offering itself as one more event arriving from outside.
const CONDITIONAL_DEF = "bpmn:ConditionalEventDefinition";

// A catch parks the token until the modelled event "occurs" — the user fires it. Message,
// timer, signal and conditional intermediate catch events and receive tasks all wait. A link
// catch does not: nothing occurs there, it is where a jump lands.
const isCatch = (el) =>
  (el.type === "bpmn:IntermediateCatchEvent" && !hasDef(el, LINK_DEF)) ||
  el.type === "bpmn:ReceiveTask";

// A throw emits a message/signal and the token continues: a send task, a message/signal
// intermediate throw, or a message/signal end event. A plain throw with no event
// definition is just a pass-through marker.
const isThrow = (el) =>
  el.type === "bpmn:SendTask" ||
  (el.type === "bpmn:IntermediateThrowEvent" && (isMessageEvent(el) || isSignalEvent(el))) ||
  (isEnd(el) && (isMessageEvent(el) || isSignalEvent(el)));

// A terminate end event is the BPMN "abort" end (ADR-0116): reaching it ends the enclosing
// flow scope *at once*, taking every other live token in that scope with it — where a plain
// end event completes only the one token that arrived. Getting this wrong is not a cosmetic
// difference: the whole point of the element is that the other branches stop.
const isTerminateEnd = (el) => isEnd(el) && hasDef(el, "bpmn:TerminateEventDefinition");

// Errors (ADR-0089) and escalations (ADR-0125) are *faults*: they are not broadcast the way a
// message or signal is, they travel structurally up the scope chain to the nearest enclosing
// handler. The two are siblings that differ in exactly two places, and the simulation has to
// keep them apart: an error catch always interrupts and an uncaught error is a failure (the
// engine raises an incident and the instance parks), while an escalation catch may run
// alongside and an uncaught escalation is benign.
const ERROR_DEF = "bpmn:ErrorEventDefinition";
const ESCALATION_DEF = "bpmn:EscalationEventDefinition";
const isErrorEnd = (el) => isEnd(el) && hasDef(el, ERROR_DEF);
const isEscalationEnd = (el) => isEnd(el) && hasDef(el, ESCALATION_DEF);
// BPMN has no error intermediate throw — an error can only be thrown by an end event — but an
// escalation has one, and it raises and then carries on down its outgoing flow.
const isEscalationThrow = (el) =>
  el.type === "bpmn:IntermediateThrowEvent" && hasDef(el, ESCALATION_DEF);
const isFaultThrow = (el) => isErrorEnd(el) || isEscalationEnd(el) || isEscalationThrow(el);

// faultCode reads the code a fault throw and a fault catch are matched on. BPMN keeps it on
// the referenced <error>/<escalation>, not on the event, and a reference to nothing — or no
// reference at all — is the empty code. This is what the compiler resolves too, so a diagram
// that matches here matches when it runs.
const faultCode = (el, defType) => {
  const d = eventDefs(el).find((x) => x.$type === defType);
  if (!d) return "";
  const ref = defType === ERROR_DEF ? d.errorRef : d.escalationRef;
  if (!ref) return "";
  return (defType === ERROR_DEF ? ref.errorCode : ref.escalationCode) || "";
};
// A catch carrying no code at all is a catch-all; otherwise the codes must be equal. Same rule
// as the engine's errorCodeMatches.
const codeCatches = (catchCode, thrown) => catchCode === "" || catchCode === thrown;

// Compensation (ADR-0103) and transactions (ADR-0108) are the other two things an end event
// can do instead of completing. A *compensation* throw or end runs the handlers of the
// activities that already completed in its scope, newest first — undoing work in the reverse
// of the order it was done. A *cancel* end sits inside a transaction and rolls the whole
// transaction back: compensate everything it completed, then leave by the cancel boundary
// instead of the normal exit.
const COMPENSATE_DEF = "bpmn:CompensateEventDefinition";
const CANCEL_DEF = "bpmn:CancelEventDefinition";
const isTransaction = (el) => el.type === "bpmn:Transaction";
const isCancelEnd = (el) => isEnd(el) && hasDef(el, CANCEL_DEF);
const isCompensationThrow = (el) =>
  (el.type === "bpmn:IntermediateThrowEvent" || isEnd(el)) && hasDef(el, COMPENSATE_DEF);
// A compensation boundary is inert: it never fires on its own and has no sequence flow out of
// it — it names, through a BPMN <association>, the handler that runs when its activity is
// compensated. A cancel boundary is inert for the same kind of reason: only its transaction's
// own rollback fires it. Neither may be offered as "fire this event", and firing one by hand
// would destroy the host's token and take a flow that does not exist.
const isInertBoundary = (el) =>
  isBoundary(el) && (hasDef(el, COMPENSATE_DEF) || hasDef(el, CANCEL_DEF));
const isCancelBoundary = (el) => isBoundary(el) && hasDef(el, CANCEL_DEF);
// activityRef narrows a compensation throw to a single activity; without one it compensates
// every completed compensable activity in its scope.
const compensationRef = (el) => {
  const d = eventDefs(el).find((x) => x.$type === COMPENSATE_DEF);
  return (d && d.activityRef && d.activityRef.id) || null;
};

// A "special" end does something instead of completing its path — it throws, cancels, or
// compensates — and each needs its token resting on the element while it does so.
const isSpecialEnd = (el) =>
  isErrorEnd(el) || isEscalationEnd(el) || isCancelEnd(el) || isCompensationThrow(el);

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
// isInterruptingCatch answers the same question for either kind of fault handler — a boundary
// event says so with cancelActivity, an event-subprocess start with isInterrupting — because a
// caught escalation behaves completely differently depending on the answer.
const isInterruptingCatch = (el) =>
  isBoundary(el) ? el.businessObject.cancelActivity !== false : isInterruptingSub(el);

// An embedded subprocess (or transaction) holds a nested flow the token descends into. An
// *event* subprocess is not one of these — it is a handler triggered by its event, not
// entered along a sequence flow — so it is excluded here. Whether a given subprocess can
// actually be walked also depends on it being expanded with a rendered inner start event;
// that check lives in _isEnterableScope (it needs the element registry).
const isSubProcessShape = (el) => el.type === "bpmn:SubProcess" || el.type === "bpmn:Transaction";
const isEventSubProcess = (el) => !!(el.businessObject && el.businessObject.triggeredByEvent === true);

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
// A standard loop (ADR-0133) is the other repetition marker: it repeats its activity while a
// condition holds, one run at a time. The simulation does not evaluate the condition either,
// so it visualises the loop the same way — a counted repetition, sequential by definition,
// bounded by the modelled loopMaximum when there is one.
const isStandardLoop = (el) => {
  const lc = loopChars(el);
  return !!(lc && lc.$type === "bpmn:StandardLoopCharacteristics");
};
// isRepeating covers both loop markers: an activity whose body runs several times before the
// token moves on. Every simulation step that has to count runs goes through it, so an
// activity with a loop icon repeats in the simulation whichever marker drew the icon.
const isRepeating = (el) => isMultiInstance(el) || isStandardLoop(el);
const isSequentialMI = (el) => {
  const lc = loopChars(el);
  return !!(lc && (lc.isSequential === true || lc.$type === "bpmn:StandardLoopCharacteristics"));
};
// literalCardinality reads a modelled fixed repetition count — a `<loopCardinality>` that is
// a plain positive integer (optionally FEEL-prefixed `=3`), or a standard loop's
// `loopMaximum`. A data-driven or expression cardinality returns null, so the caller falls
// back to the configurable default.
const literalCardinality = (el) => {
  const lc = loopChars(el);
  if (!lc) return null;
  if (isStandardLoop(el)) {
    const n = parseInt(lc.loopMaximum, 10);
    return n > 0 ? n : null;
  }
  const body = lc.loopCardinality && lc.loopCardinality.body;
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
  // scopes: embedded subprocesses the flow has entered, id → { count }. A held token rests
  // on the subprocess shape (the badge shows the scope is running) and is excluded from the
  // normal move/pump; the scope completes — releasing the held token onward — once no token
  // remains inside it (quiescence, the same idea as the OR-join).
  this._scopes = new Map();
  // scopeGen: a per-scope teardown generation. Aborting a running animation is otherwise
  // all-or-nothing (the global _epoch below), which is right for a reset or a terminate at
  // the process root but wrong for a *scoped* teardown: cancelling a subprocess must not
  // abort a dot flying in the enclosing flow. A token travelling to a target inside scope S
  // carries S's generation, so bumping it aborts exactly the dots inside S.
  this._scopeGen = new Map();
  // throwing: elements whose thrown error/escalation is travelling to its handler. The token
  // waits on the element until the fault lands, so the scope it is leaving cannot quiesce and
  // complete out from under it, and nothing may move that token in the meantime.
  this._throwing = new Set();
  // compensables: per scope (the process root keyed by ""), the compensable activities that
  // have completed there, in completion order. This is the simulation's stand-in for the
  // engine's cfCompensable index, and the reason compensation can run backwards: the newest
  // record is undone first. A record is consumed when its handler runs.
  this._compensables = new Map();
  this._compHandlers = new Map(); // compensation boundary id → its handler element, from <association>
  this._compensating = new Set(); // handler activities running right now — they retire, not complete
  // stuck: tokens the diagram gives nowhere to go — a link throw whose catch is missing. The
  // model does not deploy, but it is drawn often enough while authoring, and a token that
  // simply vanished there would be counted as a process that completed.
  this._stuck = new Set();
  this._cancelling = new Set(); // transaction scopes rolling back, waiting for their handlers to drain
  // incidents: error ends whose error no handler caught. The engine raises an incident and
  // parks the instance there (ADR-0089/0061); the simulation parks the token the same way,
  // because the one reading it must not be given is "the process completed".
  this._incidents = new Set();
  this._reach = new Map(); // memoised "can `from` reach `to`?" over sequence flows
  this._completed = 0; // tokens that reached an end event / ran off the graph
  this._terminated = 0; // tokens killed by a terminate end event / an interrupting handler

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

// setSpeed scales the animation and dwell timings. 1× is the normal (deliberately calm)
// baseline — a ~1.3s dwell per step — and the selector offers two steps in each direction
// (0.25× / 0.5× / 1× / 2× / 4×); higher/lower values divide that base tempo up or down.
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
    if (n <= 0 || this._scopes.has(id) || this._isHeld(id)) continue;
    const el = this._registry.get(id);
    if (el && !needsChoice(el) && !needsTrigger(el)) {
      this._emit(el);
      return;
    }
  }
  for (const [id, n] of this._resting) {
    if (n <= 0 || this._scopes.has(id) || this._isHeld(id)) continue;
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
  this._clearAllScopes();
  this._scopeGen.clear();
  this._throwing.clear();
  this._clearAllCompensation();
  this._clearIncidents();
  this._clearStuck();
  this._completed = 0;
  this._terminated = 0;
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
    terminated: this._terminated,
    incidents: this._incidents.size,
    stuck: this._stuck.size,
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
  // A compensation boundary names its handler with a BPMN <association>, not a sequence flow,
  // and either end of the association may be the boundary. Resolving it once here is what lets
  // a compensation throw find the activity to run — the compiler does the same post-pass.
  this._compHandlers.clear();
  this._registry.forEach((el) => {
    if (el.type !== "bpmn:Association" || !el.source || !el.target) return;
    if (isInertBoundary(el.source)) this._compHandlers.set(el.source.id, el.target);
    else if (isInertBoundary(el.target)) this._compHandlers.set(el.target.id, el.source);
  });
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
  // A running subprocess only advances when its inner flow quiesces — clicking it is a no-op
  // (its held token must not be hand-advanced out).
  if (this._scopes.has(el.id)) return;
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
  this._settleScopes();
};

// _emit moves one token out of an element along its outgoing flow(s): a throw first fires
// its message/signal, then the token forks onto every outgoing flow (the trivial case is a
// single flow). No outgoing flow means the token leaves the graph (completed).
TokenSimulation.prototype._emit = function (el) {
  if ((this._resting.get(el.id) || 0) <= 0) return;
  if (this._scopes.has(el.id)) return; // a running subprocess's held token waits for quiescence
  if (this._isHeld(el.id)) return; // a fault is travelling from here, or parked here uncaught
  // Multi-instance: consume one instance per move, keeping the token on the activity until
  // the last instance is done — so a step / Play visibly counts the body running N times.
  if (isRepeating(el)) {
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
  // A terminate end does not "move on" — it ends the enclosing scope, so the token leaves
  // the element and the scope goes with it (a token can rest here after being spawned onto
  // it or hand-advanced; one that arrives along a flow is handled in _arrive).
  if (isTerminateEnd(el)) {
    this._rest(el.id, -1);
    this._terminate(el);
    return;
  }
  // An element that throws, cancels or compensates hands its token to _throwFault rather than
  // simply moving on: an error end throws to the nearest catch and never completes, an
  // escalation raises and carries on unless an interrupting catch takes the scope, a cancel end
  // rolls its transaction back, a compensation throw runs the handlers of what already
  // completed. The token stays put while that happens, so _throwFault owns it from here.
  if (this._throwFault(el)) return;
  if (isThrow(el)) this._throwEvent(el);
  this._departFrom(el);
};

// _departFrom consumes one token resting on an element and moves it on: onto every outgoing
// flow, or — with none — off the graph as a completion. It is the tail that every "the token
// leaves here" path shares, whether the token simply moved on or first raised an escalation.
TokenSimulation.prototype._departFrom = function (el) {
  // A compensation handler retires when it is done: it is not on the normal flow, so it never
  // counts as a completion of the process. Anything else leaving a compensable activity makes
  // that activity compensable from now on — which is what a later throw undoes.
  const compensating = this._compensating.delete(el.id);
  if (!compensating) this._recordCompensable(el);
  const outs = outFlows(el);
  this._rest(el.id, -1);
  if (outs.length === 0) {
    if (!compensating) this._creditCompletion(el);
    this._flash(el);
    this._render();
    this._notify();
    this._settleJoins();
    this._settleScopes();
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
  const signalled = [];
  const messaged = [];
  this._registry.forEach((t) => {
    if (t === el || !isCatchLike(t)) return;
    if (sName && signalName(t) === sName) signalled.push(t);
    else if (mName && messageName(t) === mName) messaged.push(t);
  });
  // A signal is a broadcast and a message is not: the engine delivers a signal to every
  // subscription of that name and correlates a message to exactly one. Names are all the
  // simulation matches on — correlation keys are the engine's job — but *how many* catches a
  // throw reaches is the one property that tells the two apart, so showing a message fanning
  // out to three pools would teach the opposite of what the element means.
  const targets = signalled;
  const one = this._correlate(messaged);
  if (one) targets.push(one);
  const from = centerOf(el);
  for (const t of targets) {
    const epoch = this._epoch;
    this._animateDot([from, centerOf(t)], () => this._epoch !== epoch, "atlas-sim-msg-dot").then(
      () => {
        if (this._epoch !== epoch || !this._active) return;
        this._deliverToCatch(t);
      },
    );
  }
};

// _correlate picks the single catch a thrown message goes to, standing in for the correlation
// the engine does on keys the simulation never evaluates. It prefers whoever is actually
// waiting — a parked catch or receive task first, then an armed boundary on a running activity,
// then a handler whose scope is live — and falls back to a start event, which is what a message
// with no one waiting does: it begins an instance. With none of those it is the first match,
// which can only be pinged.
TokenSimulation.prototype._correlate = function (candidates) {
  if (candidates.length <= 1) return candidates[0] || null;
  const rank = (t) => {
    if (isCatch(t)) return (this._resting.get(t.id) || 0) > 0 ? 0 : 4;
    if (isBoundary(t)) {
      const host = t.host || (t.businessObject && t.businessObject.attachedToRef);
      return host && (this._resting.get(host.id) || 0) > 0 ? 1 : 4;
    }
    if (isEventSubStart(t)) return this._totalLive() > 0 ? 2 : 4;
    return 3; // a process start: no one is waiting, so the message begins an instance
  };
  let best = candidates[0];
  let bestRank = rank(best);
  for (const t of candidates.slice(1)) {
    const r = rank(t);
    if (r < bestRank) {
      best = t;
      bestRank = r;
    }
  }
  return best;
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
  if (isInertBoundary(b)) return; // a compensation / cancel boundary is fired by a rollback, never by hand
  const host = b.host || (b.businessObject && b.businessObject.attachedToRef);
  if (!host || (this._resting.get(host.id) || 0) <= 0) return;
  const interrupting = b.businessObject.cancelActivity !== false;
  if (interrupting) {
    if (this._scopes.has(host.id)) {
      // Cancel the whole running subprocess: its inner tokens go with it, and its dots are
      // aborted per scope, so a token flying in the enclosing flow is left alone. One held
      // token leaves through the boundary below; any other entered token dies with the scope.
      const held = (this._scopes.get(host.id) || {}).count || 1;
      this._terminated += this._tokensInScope(host) + Math.max(0, held - 1);
      this._teardownScope(host);
    } else {
      this._rest(host.id, -1);
    }
    this._clearDeciding(host.id);
  }
  this._flash(b);
  const outs = outFlows(b);
  this._render();
  this._notify();
  if (outs.length === 0) {
    this._creditCompletion(b);
  } else {
    for (const f of outs) this._travel(f);
  }
  this._settleJoins();
  this._settleScopes();
};

// _fireEventSub triggers an event subprocess by dropping a token on its start event
// (ADR-0082). An interrupting trigger first terminates the enclosing scope — every other
// live token, join, and in-flight animation — because the handler pre-empts the process; a
// non-interrupting trigger simply runs alongside. Note: the simulation is flat, so it
// treats the scope as the whole process; a nested event subprocess reads as process-scoped.
TokenSimulation.prototype._fireEventSub = function (start) {
  if ((this._resting.get(start.id) || 0) > 0) return; // already running
  if (isInterruptingSub(start)) {
    // The handler pre-empts the scope it is *declared in* — the enclosing subprocess when it
    // sits in one, otherwise the whole process. That scope itself keeps running: the handler
    // runs inside it, which is why its contents go but its own held token does not.
    const scope = this._scopeOf(start);
    if (scope) {
      this._terminated += this._tokensInScope(scope);
      this._clearScopeContents(scope);
    } else {
      this._epoch++; // abort in-flight dots — the whole process is being torn down
      this._terminated += this._totalLive();
      this._resting.clear();
      this._joinWait.clear();
      this._inflight.clear();
      this._miRemaining.clear();
      this._clearAllScopes();
      this._clearAllDeciding();
      this._clearIncidents();
      this._clearStuck();
      this._clearAllCompensation();
    }
  }
  this._flash(start);
  this.spawnAt(start);
};

// _travel animates a dot along a sequence flow, then delivers the token to its target.
TokenSimulation.prototype._travel = function (flow) {
  const target = flow.target;
  if (!target || !flow.waypoints || flow.waypoints.length < 2) return;
  this._addMarker(flow.id, "atlas-sim-flow");
  this._flyToken(flow.waypoints, target, "atlas-sim-dot", (aborted) => {
    this._removeMarker(flow.id, "atlas-sim-flow");
    if (aborted) return;
    this._arrive(target, flow);
  });
};

// _flyToken carries one token through the air to `target` and reports on arrival whether the
// flight was superseded. It is the single place that counts a token as "still coming" — which
// the OR-join's quiescence test depends on — and the single place that decides a token in
// flight no longer belongs to the run: a reset, or the teardown of the scope it was flying
// into. A token flying *into* a scope belongs to that scope; one flying toward the subprocess
// shape itself still belongs to the enclosing flow, which is why the generation it carries is
// that of the scope it lands in.
TokenSimulation.prototype._flyToken = function (waypoints, target, cls, onArrive) {
  const epoch = this._epoch;
  const scope = this._scopeOf(target);
  const scopeId = scope ? scope.id : null;
  const gen = scopeId ? this._scopeGen.get(scopeId) || 0 : 0;
  const aborted = () =>
    this._epoch !== epoch || (scopeId !== null && (this._scopeGen.get(scopeId) || 0) !== gen);
  this._inflight.set(target.id, (this._inflight.get(target.id) || 0) + 1);
  this._animateDot(waypoints, aborted, cls).then(() => {
    const n = (this._inflight.get(target.id) || 0) - 1;
    if (n > 0) this._inflight.set(target.id, n);
    else this._inflight.delete(target.id);
    onArrive(aborted() || !this._active);
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
    if (isTerminateEnd(target)) {
      this._terminate(target);
      return;
    }
    // A special end comes to rest first and only then acts: a fault end holds its token while
    // the fault travels, and a cancel or compensation end needs its scope to stay live while
    // the handlers it starts appear inside it.
    if (isSpecialEnd(target)) {
      this._rest(target.id, 1);
      this._render();
      this._notify();
      this._throwFault(target);
      return;
    }
    if (isThrow(target)) this._throwEvent(target);
    this._creditCompletion(target);
    this._flash(target);
    this._render();
    this._notify();
    this._settleJoins();
    this._settleScopes(); // an inner end may have quiesced its subprocess
    return;
  }
  // An embedded subprocess is *entered*, not passed over: a token spawns on each inner start
  // and runs the nested flow; the outer token continues only once the subprocess completes.
  if (this._isEnterableScope(target)) {
    this._enterScope(target);
    return;
  }
  // A multi-instance activity runs its body several times before the token moves on; seed
  // the instance counter so the badge shows the multiplicity from the moment it arrives.
  if (isRepeating(target) && !this._miRemaining.has(target.id)) {
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

// --- Embedded subprocess scopes ----------------------------------------------------

// _isEnterableScope reports whether a token arriving at this element should descend into it
// rather than pass over it. Only an *expanded* embedded subprocess with a rendered plain
// start event can be walked: a collapsed one has no inner shapes in the registry (so
// _innerStartsOf comes back empty) and a multi-instance subprocess keeps its instance-count
// visualisation instead — both fall through to the pass-over path.
TokenSimulation.prototype._isEnterableScope = function (el) {
  if (!isSubProcessShape(el) || isEventSubProcess(el) || isRepeating(el)) return false;
  return this._innerStartsOf(el).length > 0;
};

// _innerStartsOf returns the plain (non-event) start events that sit directly inside a
// subprocess — where the flow begins when the token enters. A nested subprocess's own start
// belongs to that nested scope, not this one, so only direct children count.
TokenSimulation.prototype._innerStartsOf = function (sub) {
  const out = [];
  this._registry.forEach((el) => {
    if (isStart(el) && !isEventSubStart(el) && el.parent && el.parent.id === sub.id) out.push(el);
  });
  return out;
};

// _within reports whether the element `elId` sits anywhere inside the subprocess `subId`
// (any nesting depth), by walking its parent chain.
TokenSimulation.prototype._within = function (elId, subId) {
  let el = this._registry.get(elId);
  el = el && el.parent;
  while (el) {
    if (el.id === subId) return true;
    el = el.parent;
  }
  return false;
};

// _scopeOf returns the nearest ancestor subprocess of `el` that is currently a running
// scope, or null. A token that runs off the graph inside such a scope retires an inner
// token rather than completing the process.
TokenSimulation.prototype._scopeOf = function (el) {
  let p = el && el.parent;
  while (p) {
    if (this._scopes.has(p.id)) return p;
    p = p.parent;
  }
  return null;
};

// _creditCompletion counts a token that ran off the graph as a process completion — unless
// it did so inside a running subprocess, where it only retires an inner token (the scope
// completes, and the outer token continues, once the whole subprocess quiesces).
TokenSimulation.prototype._creditCompletion = function (el) {
  if (this._scopeOf(el)) return;
  this._completed++;
};

// _enterScope descends the token into an embedded subprocess: a held token rests on the
// subprocess shape (marking it "running") and a fresh token spawns on each inner start. A
// second token arriving while the scope already runs just adds another held token; they all
// leave together when the subprocess completes.
TokenSimulation.prototype._enterScope = function (sub) {
  const existing = this._scopes.get(sub.id);
  this._rest(sub.id, 1);
  if (existing) {
    existing.count++;
    this._render();
    this._notify();
    return;
  }
  this._scopes.set(sub.id, { count: 1 });
  this._addMarker(sub.id, "atlas-sim-scope");
  this._flash(sub);
  const starts = this._innerStartsOf(sub);
  this._render();
  this._notify();
  for (const s of starts) this.spawnAt(s);
};

// _settleScopes completes every running subprocess whose inner flow has quiesced — no token
// rests, animates, or waits in a join anywhere inside it. Completing one releases its held
// token(s) onward, which may in turn quiesce an enclosing scope, so re-read the map each
// pass (the same re-entrant pattern as _settleJoins).
TokenSimulation.prototype._settleScopes = function () {
  for (const sid of Array.from(this._scopes.keys())) {
    if (!this._scopes.has(sid)) continue;
    const sub = this._registry.get(sid);
    if (!sub) {
      this._scopes.delete(sid);
      continue;
    }
    if (this._scopeLive(sub)) continue;
    if (this._cancelling.has(sid)) this._completeCancelledScope(sub);
    else this._completeScope(sub);
  }
};

// _scopeLive reports whether any token is still inside the subprocess `sub` — resting,
// in flight toward an inner element, or parked in an inner join. The subprocess's own held
// token (keyed by its id) is not "inside" and so does not keep the scope alive.
TokenSimulation.prototype._scopeLive = function (sub) {
  for (const [id, n] of this._resting) {
    if (n > 0 && id !== sub.id && this._within(id, sub.id)) return true;
  }
  for (const [id, n] of this._inflight) {
    if (n > 0 && this._within(id, sub.id)) return true;
  }
  for (const [gwId, w] of this._joinWait) {
    let held = 0;
    for (const c of w.values()) held += c;
    if (held > 0 && this._within(gwId, sub.id)) return true;
  }
  return false;
};

// _completeScope ends a quiesced subprocess: it releases the held token(s) out of the
// subprocess's outgoing flow(s), or — if it has none — credits their completion (respecting
// a further enclosing scope). One token leaves for each that entered.
TokenSimulation.prototype._completeScope = function (sub) {
  const rec = this._scopes.get(sub.id);
  const count = (rec && rec.count) || 1;
  this._scopes.delete(sub.id);
  this._forgetCompensables(sub.id); // a finished scope leaves nothing to compensate
  this._removeMarker(sub.id, "atlas-sim-scope");
  this._rest(sub.id, -count);
  this._flash(sub);
  const outs = outFlows(sub);
  this._render();
  this._notify();
  if (outs.length === 0) {
    for (let i = 0; i < count; i++) this._creditCompletion(sub);
    this._settleScopes();
    return;
  }
  for (let i = 0; i < count; i++) for (const f of outs) this._travel(f);
};

// _abortScope invalidates the dots still flying *inside* a scope (and inside any scope
// nested in it), so tearing that scope down does not have to bump the global epoch — which
// would also abort dots belonging to the enclosing flow, silently losing tokens that the
// teardown must not touch.
TokenSimulation.prototype._abortScope = function (subId) {
  const bump = (id) => this._scopeGen.set(id, (this._scopeGen.get(id) || 0) + 1);
  bump(subId);
  for (const sid of this._scopes.keys()) if (this._within(sid, subId)) bump(sid);
};

// _tokensInScope counts the live tokens inside a subprocess — resting on an inner element,
// flying toward one, or parked in an inner join. It is _scopeLive's counting twin: what a
// teardown of that scope takes with it. The subprocess's own held token is not "inside".
TokenSimulation.prototype._tokensInScope = function (sub) {
  let n = 0;
  for (const [id, c] of this._resting) {
    if (c > 0 && id !== sub.id && this._within(id, sub.id)) n += c;
  }
  for (const [id, c] of this._inflight) {
    if (c > 0 && this._within(id, sub.id)) n += c;
  }
  for (const [gwId, w] of this._joinWait) {
    if (!this._within(gwId, sub.id)) continue;
    for (const c of w.values()) n += c;
  }
  return n;
};

// _clearScopeContents removes every token *inside* a subprocess — resting, flying, parked in
// an inner join, deciding at an inner gateway, counting repetitions, or held by a nested
// scope. The subprocess's own held token and its scope record are deliberately left alone:
// the caller decides what becomes of them — a terminate end completes the scope normally so
// the held token continues, an interrupting boundary tears it down with everything else.
TokenSimulation.prototype._clearScopeContents = function (sub) {
  this._abortScope(sub.id);
  for (const id of Array.from(this._resting.keys())) {
    if (id !== sub.id && this._within(id, sub.id)) this._resting.delete(id);
  }
  for (const gwId of Array.from(this._joinWait.keys())) {
    if (this._within(gwId, sub.id)) this._joinWait.delete(gwId);
  }
  for (const gwId of Array.from(this._deciding.keys())) {
    if (this._within(gwId, sub.id)) this._clearDeciding(gwId);
  }
  for (const sid of Array.from(this._scopes.keys())) {
    if (sid === sub.id || !this._within(sid, sub.id)) continue;
    this._scopes.delete(sid);
    this._forgetCompensables(sid);
    this._cancelling.delete(sid);
    this._removeMarker(sid, "atlas-sim-cancelling");
    this._removeMarker(sid, "atlas-sim-scope"); // a nested scope stops running with its parent
  }
  for (const id of Array.from(this._compensating)) {
    if (this._within(id, sub.id)) this._compensating.delete(id); // a handler stops with its scope
  }
  for (const id of Array.from(this._miRemaining.keys())) {
    if (this._within(id, sub.id)) this._miRemaining.delete(id);
  }
  for (const id of Array.from(this._incidents)) {
    if (this._within(id, sub.id)) {
      this._incidents.delete(id); // the parked incident goes with the scope it was parked in
      this._removeMarker(id, "atlas-sim-incident");
    }
  }
  for (const id of Array.from(this._stuck)) {
    if (this._within(id, sub.id)) {
      this._stuck.delete(id);
      this._removeMarker(id, "atlas-sim-stuck");
    }
  }
};

// _teardownScope cancels a running subprocess outright (an interrupting boundary fired):
// everything inside it goes, and so do its held token, its scope record, and its highlight.
TokenSimulation.prototype._teardownScope = function (sub) {
  this._clearScopeContents(sub);
  this._resting.delete(sub.id);
  this._scopes.delete(sub.id);
  this._forgetCompensables(sub.id);
  this._cancelling.delete(sub.id);
  this._removeMarker(sub.id, "atlas-sim-cancelling");
  this._removeMarker(sub.id, "atlas-sim-scope");
};

// --- Faults: errors and escalations -------------------------------------------------

// _isHeld reports whether a token resting here must not be moved by a click, a step or the
// pump: a fault it threw is still travelling to its handler, or an uncaught error has parked
// it. Both are tokens the run no longer owns — moving one would throw the fault twice.
TokenSimulation.prototype._isHeld = function (id) {
  return this._throwing.has(id) || this._incidents.has(id) || this._stuck.has(id);
};

// _throwFault raises the fault an element carries and reports whether it took the token over.
// The token must already rest on the element: it waits there while the fault travels, which is
// what stops the scope it belongs to from quiescing and completing underneath it.
TokenSimulation.prototype._throwFault = function (el) {
  if (isErrorEnd(el)) {
    this._throwError(el);
    return true;
  }
  if (isEscalationEnd(el) || isEscalationThrow(el)) {
    this._throwEscalation(el);
    return true;
  }
  if (isCancelEnd(el)) {
    this._cancelTransaction(el);
    return true;
  }
  if (isCompensationThrow(el)) {
    this._compensateFrom(el);
    return true;
  }
  if (isLinkThrow(el)) {
    this._jumpLink(el);
    return true;
  }
  return false;
};

// _throwError runs an error end event (ADR-0089). An error end does not complete its path: it
// throws a coded error that travels up the live scope chain to the nearest matching error
// boundary or error event subprocess. That catch is *always* interrupting — the scope below it
// is torn down and the flow leaves through the handler. When nothing catches it the engine
// raises an incident and parks the instance, so the token parks here too, marked — because
// "the process completed" is the one reading of an uncaught error that must never be given.
TokenSimulation.prototype._throwError = function (el) {
  const handler = this._findHandler(el, ERROR_DEF, faultCode(el, ERROR_DEF));
  if (!handler) {
    this._incidents.add(el.id);
    this._addMarker(el.id, "atlas-sim-incident");
    this._flashAbort(el);
    this._render();
    this._notify();
    return;
  }
  this._flashAbort(el);
  this._sendFault(el, handler, () => {
    this._rest(el.id, -1); // the throwing token goes with the scope the catch tears down
    this._fireCatch(handler);
  });
};

// _throwEscalation runs an escalation end event or escalation intermediate throw (ADR-0125) —
// the error's benign sibling. It raises a coded escalation to the nearest matching handler and
// differs from an error in exactly the two ways that define escalation: the catch may be
// NON-interrupting, in which case the handler runs alongside and the raising token carries on
// as if nothing had happened; and an escalation nobody catches is not a failure — the throw's
// own flow semantics simply apply. Either way _departFrom is what "carries on" means: an end
// event runs off the graph, an intermediate throw takes its outgoing flow.
TokenSimulation.prototype._throwEscalation = function (el) {
  const handler = this._findHandler(el, ESCALATION_DEF, faultCode(el, ESCALATION_DEF));
  if (!handler) {
    this._departFrom(el); // uncaught is benign: the token carries on
    return;
  }
  const interrupting = isInterruptingCatch(handler);
  this._sendFault(el, handler, () => {
    if (interrupting) {
      this._rest(el.id, -1); // an interrupting catch tears this token's scope down with it
      this._fireCatch(handler);
      return;
    }
    this._fireCatch(handler); // the handler runs beside the scope, which keeps going
    this._departFrom(el);
  });
};

// _sendFault animates the fault from a throw to the handler that catches it, then hands over.
// Unlike a message/signal dot — a broadcast to every same-named catch — this one goes to
// exactly one element: the nearest enclosing handler the scope walk found. If the throwing
// token is gone by the time it lands, the throw was torn down mid-flight and nothing happens.
TokenSimulation.prototype._sendFault = function (from, to, onArrive) {
  this._throwing.add(from.id);
  const epoch = this._epoch;
  this._render();
  this._notify();
  this._animateDot(
    [centerOf(from), centerOf(to)],
    () => this._epoch !== epoch,
    "atlas-sim-fault-dot",
  ).then(() => {
    this._throwing.delete(from.id);
    if (this._epoch !== epoch || !this._active) return;
    if ((this._resting.get(from.id) || 0) <= 0) return; // superseded by a teardown
    this._ping(to);
    onArrive();
  });
};

// _fireCatch runs the handler a fault reached — a boundary event on the scope it left, or an
// event subprocess declared in one. Both already know how to fire interrupting or not.
TokenSimulation.prototype._fireCatch = function (handler) {
  if (isBoundary(handler)) this._fireBoundary(handler);
  else this._fireEventSub(handler);
};

// _findHandler walks outward from a fault throw to the handler that catches it, mirroring the
// engine's scope walk: at each enclosing scope an event subprocess declared *in* that scope
// catches before a boundary *on* it (the event sub catches the fault inside the scope, the
// boundary catches it leaving), and the process root's own event subprocesses are checked last.
// The nearest match wins; nothing matching means uncaught.
TokenSimulation.prototype._findHandler = function (el, defType, code) {
  for (const scope of this._scopeChainOf(el)) {
    const sub = this._eventSubIn(scope, defType, code);
    if (sub) return sub;
    const boundary = this._boundaryOn(scope, defType, code);
    if (boundary) return boundary;
  }
  return this._eventSubIn(null, defType, code); // the process root's own handlers
};

// _scopeChainOf lists the running subprocess scopes enclosing an element, nearest first. It is
// the simulation's stand-in for the engine's FlowScopeKey chain; the process root is the null
// at the end of it, and is handled by the caller.
TokenSimulation.prototype._scopeChainOf = function (el) {
  const chain = [];
  for (let cur = this._scopeOf(el); cur; cur = this._scopeOf(cur)) chain.push(cur);
  return chain;
};

// _eventSubIn finds an event-subprocess trigger declared directly in `scope` (null for the
// process root) that catches this fault. One already holding a token is running, not armed.
TokenSimulation.prototype._eventSubIn = function (scope, defType, code) {
  const wanted = scope ? scope.id : null;
  for (const start of this._eventSubStarts) {
    if (!hasDef(start, defType) || !codeCatches(faultCode(start, defType), code)) continue;
    const own = this._scopeOf(start);
    if ((own ? own.id : null) !== wanted) continue;
    if ((this._resting.get(start.id) || 0) > 0) continue;
    return start;
  }
  return null;
};

// _boundaryOn finds a boundary event on a scope's subprocess shape that catches this fault.
TokenSimulation.prototype._boundaryOn = function (scope, defType, code) {
  for (const b of this._boundaries.get(scope.id) || []) {
    if (hasDef(b, defType) && codeCatches(faultCode(b, defType), code)) return b;
  }
  return null;
};

// --- Link events ---------------------------------------------------------------------

// _jumpLink runs a link intermediate throw event (ADR-0132). A link is a goto, not a wait:
// reaching the throw is identical to having taken a sequence flow straight to the link catch of
// the same name in the same scope, which then carries on by its own outgoing flow. The compiler
// resolves the pair into a synthetic sequence flow; here the token flies the same jump, so the
// two halves of a flow the author split up read as the one line they stand for.
TokenSimulation.prototype._jumpLink = function (el) {
  const target = this._linkCatchFor(el);
  if (!target) {
    // Nowhere to jump to. Such a model does not deploy, and the token must not quietly run off
    // the graph as though the process had finished — it is stranded, and says so.
    this._stuck.add(el.id);
    this._addMarker(el.id, "atlas-sim-stuck");
    this._flashAbort(el);
    this._render();
    this._notify();
    return;
  }
  this._rest(el.id, -1);
  this._flash(el);
  this._render();
  this._notify();
  this._flyToken([centerOf(el), centerOf(target)], target, "atlas-sim-link-dot", (aborted) => {
    if (aborted) return;
    this._flash(target);
    this._rest(target.id, 1);
    this._land(target);
  });
};

// _linkCatchFor finds the link catch a throw jumps to: the same trimmed name, in the same flow
// scope — the BPMN container both are drawn in, which is how the compiler pairs them, so a
// throw and a catch in different subprocesses do not pair.
TokenSimulation.prototype._linkCatchFor = function (el) {
  const name = linkName(el);
  if (!name) return null;
  const scopeId = el.parent && el.parent.id;
  let found = null;
  this._registry.forEach((t) => {
    if (found || !isLinkCatch(t) || linkName(t) !== name) return;
    if ((t.parent && t.parent.id) !== scopeId) return;
    found = t;
  });
  return found;
};

// --- Compensation and transactions ---------------------------------------------------

// _handlerFor returns the compensation handler armed on an activity — the activity its
// compensation boundary points at — or null when it carries none and is not compensable.
TokenSimulation.prototype._handlerFor = function (el) {
  for (const b of this._boundaries.get(el.id) || []) {
    const handler = this._compHandlers.get(b.id);
    if (handler) return handler;
  }
  return null;
};

// _scopeKeyOf keys the compensable index: a running subprocess scope by its id, the process
// root by the empty string (no element can own that id).
TokenSimulation.prototype._scopeKeyOf = function (el) {
  const scope = this._scopeOf(el);
  return scope ? scope.id : "";
};

// _recordCompensable notes that a compensable activity has completed. From here on a
// compensation throw in the same scope can undo it, and the order they are recorded in is the
// order compensation walks backwards through.
TokenSimulation.prototype._recordCompensable = function (el) {
  const handler = this._handlerFor(el);
  if (!handler) return;
  const key = this._scopeKeyOf(el);
  const list = this._compensables.get(key) || [];
  list.push({ activityId: el.id, handlerId: handler.id });
  this._compensables.set(key, list);
  this._addMarker(el.id, "atlas-sim-compensable");
};

// _runCompensations starts the compensation handlers of the completed compensable activities
// in a scope — the one named by `activityId`, or all of them — newest first, and consumes their
// records: an activity is compensated once. It returns how many handlers it started. The
// handlers run *inside* the scope, so the scope cannot finish until they are done, which is
// what makes a rollback wait for itself.
TokenSimulation.prototype._runCompensations = function (scopeKey, activityId) {
  const list = this._compensables.get(scopeKey) || [];
  const keep = [];
  const run = [];
  for (const rec of list) (activityId && rec.activityId !== activityId ? keep : run).push(rec);
  if (keep.length) this._compensables.set(scopeKey, keep);
  else this._compensables.delete(scopeKey);
  run.reverse(); // reverse completion order: the last thing done is the first thing undone
  for (const rec of run) {
    this._removeMarker(rec.activityId, "atlas-sim-compensable");
    const handler = this._registry.get(rec.handlerId);
    if (!handler) continue;
    this._compensating.add(handler.id);
    this._ping(handler);
    this._rest(handler.id, 1);
    this._land(handler);
  }
  return run.length;
};

// _compensateFrom runs a compensation throw or compensation end event (ADR-0103). It starts the
// handlers of what already completed in its own scope and then goes on its way — a throw takes
// its outgoing flow, an end ends its path — because compensation runs alongside rather than
// blocking the thrower. Compensation is scope-confined: a throw undoes what completed in its
// own scope, never what completed elsewhere.
TokenSimulation.prototype._compensateFrom = function (el) {
  if (this._runCompensations(this._scopeKeyOf(el), compensationRef(el))) this._flash(el);
  this._departFrom(el);
};

// _cancelTransaction runs a cancel end event (ADR-0108): the enclosing transaction is rolled
// back. Its other live tokens are terminated, everything it completed is compensated newest
// first, and the transaction is marked cancelling — so when those handlers drain it leaves by
// its cancel boundary instead of its normal outgoing flow. A cancel end that is not inside a
// transaction can roll nothing back (the compiler rejects that model); the simulation walks it
// as the plain end the diagram actually drew rather than inventing a rollback.
TokenSimulation.prototype._cancelTransaction = function (el) {
  const scope = this._scopeOf(el);
  if (!scope || !isTransaction(scope)) {
    this._departFrom(el);
    return;
  }
  this._rest(el.id, -1); // the cancel end's own token goes with the rollback
  this._cancelling.add(scope.id);
  this._addMarker(scope.id, "atlas-sim-cancelling");
  this._terminated += this._tokensInScope(scope);
  this._clearScopeContents(scope); // the transaction's other work stops before it is undone
  this._flashAbort(el);
  this._runCompensations(scope.id, null);
  this._render();
  this._notify();
  this._settleScopes(); // with nothing to compensate, the rollback finishes here
};

// _completeCancelledScope finishes a rolled-back transaction once its compensation handlers
// have drained: the transaction is torn down and its cancel boundary takes the recovery flow.
// A transaction drawn without a cancel boundary has nowhere to send it — the compiler warns
// about that model, and here the tokens that entered simply end with it.
TokenSimulation.prototype._completeCancelledScope = function (sub) {
  this._cancelling.delete(sub.id);
  this._removeMarker(sub.id, "atlas-sim-cancelling");
  const boundary = (this._boundaries.get(sub.id) || []).find(isCancelBoundary);
  const held = (this._scopes.get(sub.id) || {}).count || 1;
  this._teardownScope(sub);
  this._flash(sub);
  this._render();
  this._notify();
  if (!boundary) {
    this._terminated += held;
    this._settleScopes();
    return;
  }
  if (held > 1) this._terminated += held - 1; // only one token leaves by the boundary
  this._flash(boundary);
  const outs = outFlows(boundary);
  if (outs.length === 0) this._creditCompletion(boundary);
  else for (const f of outs) this._travel(f);
  this._settleJoins();
  this._settleScopes();
};

// _clearAllCompensation drops every compensable record, running handler and rollback: the whole
// process has been torn down, so there is nothing left that could be undone.
TokenSimulation.prototype._clearAllCompensation = function () {
  for (const key of Array.from(this._compensables.keys())) this._forgetCompensables(key);
  this._compensating.clear();
  this._clearCancelling();
};

// _clearCancelling drops every rollback in progress and its marking — the scopes are gone.
TokenSimulation.prototype._clearCancelling = function () {
  for (const id of this._cancelling) this._removeMarker(id, "atlas-sim-cancelling");
  this._cancelling.clear();
};

// _forgetCompensables drops a scope's compensable records and the markings that went with
// them. A scope that is gone — completed, cancelled or torn down — leaves nothing to undo, the
// same teardown the engine does on the scope's own Completed/Terminated event.
TokenSimulation.prototype._forgetCompensables = function (scopeKey) {
  for (const rec of this._compensables.get(scopeKey) || []) {
    this._removeMarker(rec.activityId, "atlas-sim-compensable");
  }
  this._compensables.delete(scopeKey);
};

// _clearStuck drops every token the diagram had stranded, and its marking.
TokenSimulation.prototype._clearStuck = function () {
  for (const id of this._stuck) this._removeMarker(id, "atlas-sim-stuck");
  this._stuck.clear();
};

// _clearIncidents drops every parked incident and its marking — the tokens they belonged to
// are gone (a reset, or a teardown that took them).
TokenSimulation.prototype._clearIncidents = function () {
  for (const id of this._incidents) this._removeMarker(id, "atlas-sim-incident");
  this._incidents.clear();
};

// _clearAllScopes drops every running scope and the "running" highlight that goes with it —
// the whole process is being torn down (a reset, an interrupting event subprocess, or a
// terminate end at the process root). Clearing the map alone would leave the tint behind on
// a subprocess that is no longer running.
TokenSimulation.prototype._clearAllScopes = function () {
  for (const sid of this._scopes.keys()) this._removeMarker(sid, "atlas-sim-scope");
  this._scopes.clear();
};

// _terminate runs a terminate end event — the BPMN "abort" end (ADR-0116), and the one end
// event that is about the tokens it does *not* own. It ends the enclosing flow scope at
// once: every other live token in that scope goes with it, wherever it sits — resting, in
// flight, parked in a join, deciding at a gateway, counting repetitions, or inside a nested
// subprocess. Then the scope completes. At the process root that ends the simulated
// instance; inside an embedded subprocess it completes that subprocess, so the parent token
// continues on the subprocess's outgoing flow and the rest of the process runs on. This
// mirrors the engine's terminateEndEventBehavior, and like the engine it runs no
// compensation and no event handling (BPMN 13.4.6).
//
// The simulation is flat outside embedded subprocesses, so a terminate inside an event
// subprocess reads as process-scoped — the same simplification _fireEventSub makes.
TokenSimulation.prototype._terminate = function (el) {
  this._flashAbort(el);
  const scope = this._scopeOf(el);
  if (scope) {
    this._terminated += this._tokensInScope(scope);
    this._clearScopeContents(scope);
    this._render();
    this._notify();
    this._completeScope(scope); // the held token(s) leave on the subprocess's outgoing flow
    this._settleJoins();
    this._settleScopes();
    return;
  }
  this._epoch++; // at the root nothing survives — abort every dot still in flight
  this._terminated += this._totalLive();
  this._resting.clear();
  this._joinWait.clear();
  this._inflight.clear();
  this._miRemaining.clear();
  this._clearAllScopes();
  this._clearAllDeciding();
  this._clearIncidents();
  this._clearStuck();
  this._clearAllCompensation();
  this._completed++; // the token that reached the terminate end is the one completion
  this._render();
  this._notify();
};

// _pump auto-advances the flow while playing: on each tick it moves one eligible token and
// re-arms itself if work remains. In auto mode a "movable" token also includes a gateway
// choice (resolved to the default / a branch) and a parked catch (fired), so Play runs
// end-to-end; in manual mode those wait for a click and the pump idles.
TokenSimulation.prototype._pump = function () {
  if (this._pumpTimer) return; // a tick is already scheduled
  if (!this._playing || !this._active) return;
  const delay = 1300 / this._speed;
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
    if (this._scopes.has(id)) continue; // a running subprocess's held token never pumps
    if (this._isHeld(id)) continue; // a fault in flight, or an error parked on an incident
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
    if (this._scopes.has(id)) continue; // held subprocess token — not movable on its own
    if (this._isHeld(id)) continue; // a fault in flight, or an error parked on an incident
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
    if (el && needsTrigger(el)) {
      this._drawFire(el, this._triggerGlyph(el), () => this._fireTrigger(el), this._triggerTitle(el));
    }
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
    // Offer any boundary events attached to an activity that now holds a token. An inert one
    // is not offered: nothing a person does fires a compensation or cancel boundary.
    for (const b of this._boundaries.get(id) || []) {
      if (isInertBoundary(b)) continue;
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
  // Repetition badge: how many runs are still to go of the total for this activity (from a
  // modelled cardinality or loop maximum, else the configured default). The glyph mirrors
  // the marker on the shape — ↻ for a standard loop, ≡ / ‖ for a multi-instance.
  for (const [id, mi] of this._miRemaining) {
    if (!mi || mi.left <= 0) continue;
    const el = this._registry.get(id);
    if (!el) continue;
    const std = isStandardLoop(el);
    const seq = isSequentialMI(el);
    const modelled = literalCardinality(el) != null;
    const src = std
      ? (modelled ? "modelled loop maximum" : "simulated — the loop condition is not evaluated")
      : (modelled ? "modelled cardinality" : "simulated");
    const what = std ? "loop" : (seq ? "sequential multi-instance" : "parallel multi-instance");
    try {
      this._overlayIds.push(
        this._overlays.add(id, "atlas-sim-mi", {
          position: { bottom: 4, left: 4 },
          html: `<span class="atlas-sim-mi" title="${what} — ${mi.left} of ${mi.total} left (${src})">${std ? "↻" : (seq ? "≡" : "‖")} ${mi.left}/${mi.total}</span>`,
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
  if (hasDef(el, ERROR_DEF)) return "&#9888;"; // warning sign
  if (hasDef(el, ESCALATION_DEF)) return "&#8599;"; // up-right arrow — raised up the chain
  if (hasDef(el, CONDITIONAL_DEF)) return "&#9776;"; // lines, as the conditional marker draws them
  return "&#9654;"; // receive task — a plain "go"
};

// _triggerTitle says what a person is asserting when they fire a parked catch. A conditional
// event is the one that is not an event arriving from outside: it waits on the process's own
// data, and the simulation evaluates no FEEL, so firing it means "the condition now holds".
TokenSimulation.prototype._triggerTitle = function (el) {
  if (hasDef(el, CONDITIONAL_DEF)) return "The condition now holds — release the waiting token";
  const kind = eventKindOf(el);
  return `Fire this ${kind ? kind + " " : ""}event — release the waiting token`;
};

// _faultKind names what a handler catches, for the affordance titles. A fault handler is
// normally reached by a throw rather than a click, but the affordance stays: firing it by hand
// is how a person asks "and what if this fails here?".
const eventKindOf = (el) =>
  isTimerEvent(el)
    ? "timer"
    : isMessageEvent(el)
      ? "message"
      : isSignalEvent(el)
        ? "signal"
        : hasDef(el, ERROR_DEF)
          ? "error"
          : hasDef(el, ESCALATION_DEF)
            ? "escalation"
            : hasDef(el, CONDITIONAL_DEF)
              ? "conditional"
              : ""; // an event carrying no definition has no kind to name

TokenSimulation.prototype._boundaryTitle = function (b) {
  const mode = isInterruptingCatch(b) ? "interrupting" : "non-interrupting";
  const kind = eventKindOf(b);
  return `Fire this ${mode} ${kind ? kind + " " : ""}boundary event`;
};

TokenSimulation.prototype._eventSubTitle = function (start) {
  const mode = isInterruptingSub(start) ? "interrupting" : "non-interrupting";
  const kind = eventKindOf(start);
  return `Trigger this ${mode} ${kind ? kind + " " : ""}event subprocess`;
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

// _flashAbort pulses a terminate end event in the danger colour. The green _flash reads as
// "a token passed through here"; this end did something else — it stopped the scope and took
// the other tokens with it, and the badges that just vanished should have a visible cause.
TokenSimulation.prototype._flashAbort = function (el) {
  this._addMarker(el.id, "atlas-sim-abort");
  setTimeout(() => this._removeMarker(el.id, "atlas-sim-abort"), 900);
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
  const dur = Math.max(440, Math.min(2800, 80 + total * 4.4)) / this._speed;

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
