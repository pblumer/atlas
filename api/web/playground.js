// Playground tab: run the diagram on the real engine, in a throwaway sandbox.
//
// The Design view's token simulation (ADR-0078) is engine-free on purpose — it
// answers "where can a token go". This answers "what does the process do with
// this data": the server compiles the diagram on screen and executes it on the
// real processor over a virtual clock, so FEEL, gateways, DMN and timers behave
// exactly as they will in production. Nothing is deployed, nothing durable is
// written, and the sandbox holds no workers, so no task can reach the network.
//
// The tab has two ways to drive that sandbox, and they share it:
//   Step  — one case, one occurrence at a time, with the person at the keyboard
//           answering the human tasks. "What does this do?"
//   Batch — a dataset spread over simulated time, then a report: outcomes,
//           durations, a bottleneck ranking, the run over time, and a heat map
//           of what the data used and what it never reached. "Does this hold up?"
//
// See ADR-0215.

// A batch run's counts reach five and six digits, and they are printed in the same
// badges the runtime views use — so they are grouped the same way (numfmt.js).
import { fmtCount } from "./numfmt.js";
import { t, plural } from "./i18n.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// STUB_DURATIONS are the answering speeds offered for every job the diagram
// creates. "Instant" is for walking the control flow; the longer ones make the
// simulated clock move so timers and deadlines become visible.
//
// This table and the ones below hold catalogue keys (ADR-0267) rather than words, so
// the labels are read when they are drawn, in the language the console speaks then.
const STUB_DURATIONS = [
  { label: "pg.dur.instant", ms: 0 },
  { label: "pg.dur.1m", ms: 60_000 },
  { label: "pg.dur.1h", ms: 3_600_000 },
  { label: "pg.dur.4h", ms: 14_400_000 },
];

// HUMAN_DURATIONS are the same for user tasks, plus the one answer a stub cannot
// give: leave it parked for the person at the keyboard. That is what Step mode
// wants and what a batch cannot use, which is why the choice is offered rather
// than assumed.
const HUMAN_DURATIONS = [{ label: "pg.dur.leave", ms: -1 }, ...STUB_DURATIONS];

// ARRIVALS is how a dataset is spread over simulated time. The parameter each
// mode needs is asked for only when that mode is chosen.
const ARRIVALS = [
  { mode: "allAtOnce", label: "pg.arrival.allAtOnce", param: null },
  { mode: "sequential", label: "pg.arrival.sequential", param: null },
  { mode: "every", label: "pg.arrival.every", param: "pg.arrival.everyParam" },
  { mode: "poisson", label: "pg.arrival.poisson", param: "pg.arrival.poissonParam" },
];

// TASK_TYPES are the elements a pool can be attached to: the ones that hold a
// token while somebody or something works on them.
const TASK_TYPES = new Set([
  "bpmn:Task", "bpmn:UserTask", "bpmn:ServiceTask", "bpmn:ScriptTask",
  "bpmn:BusinessRuleTask", "bpmn:SendTask", "bpmn:ReceiveTask", "bpmn:ManualTask",
  "bpmn:CallActivity",
]);

// HEAT_LEVELS is how many shades the heat map has above "never reached". Five is
// enough to read a ranking off the diagram and few enough that each step is a
// visible difference.
const HEAT_LEVELS = 5;

// OVERLAYS are the four things a run can shade the diagram by. They are one
// picture at a time on purpose: a diagram shaded by two quantities at once is two
// answers to a question nobody asked, and the reader cannot tell which colour
// belongs to which.
//
// Only the token counts exist for a sequence flow — an edge has no work time, no
// waiting and nothing to fail on — so the other three leave the flows unshaded
// rather than colouring them from a different quantity than the shapes.
// `cold` says whether zero is worth drawing as its own state. It is true only for
// the token counts, where zero means "the data never got here" — the coverage
// question, and the reason the map is built from the model's shape rather than from
// the counters. On the other three, zero means "no waiting here", "no work here",
// "nothing failed here", which is the ordinary case: drawing those dashed and faded
// says "never reached" about most of a healthy diagram, which is a lie.
const OVERLAYS = [
  { key: "runs", label: "pg.overlay.runs", title: "pg.overlay.runsTitle", flows: true, cold: true },
  { key: "work", label: "pg.overlay.work", title: "pg.overlay.workTitle", flows: false, cold: false },
  { key: "wait", label: "pg.overlay.wait", title: "pg.overlay.waitTitle", flows: false, cold: false },
  { key: "incidents", label: "pg.overlay.incidents", title: "pg.overlay.incidentsTitle", flows: false, cold: false },
];

// FIELD_KINDS is what a generated field can be. The labels say what the field
// produces rather than naming a type: somebody describing a dataset is thinking
// about orders and amounts, not about int64.
const FIELD_KINDS = [
  { kind: "int", label: "pg.kind.int" },
  { kind: "decimal", label: "pg.kind.decimal" },
  { kind: "bool", label: "pg.kind.bool" },
  { kind: "choice", label: "pg.kind.choice" },
  { kind: "constant", label: "pg.kind.constant" },
  { kind: "sequence", label: "pg.kind.sequence" },
  { kind: "timestamp", label: "pg.kind.timestamp" },
];

const DAY_MS = 86_400_000;

// BUSINESS_HOURS is one working day, applied to every pool and to the arrival
// stream alike. One calendar rather than one per pool: an author asking "do three
// clerks suffice" is not also modelling shift patterns, and the API keeps the
// richer per-pool calendar available for when they are.
const BUSINESS_HOURS = {
  open: [{ fromMinutes: 8 * 60, toMinutes: 17 * 60 }],
  days: [1, 2, 3, 4, 5],
};

// fmtDur turns milliseconds of *simulated* time into something a person reads at
// a glance. Simulated runs span minutes or months, so the unit is chosen per
// value rather than fixed.
function fmtDur(ms) {
  if (ms == null) return "—";
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 ? `${h}h ${m % 60}m` : `${h}h`;
  const d = Math.floor(h / 24);
  return h % 24 ? `${d}d ${h % 24}h` : `${d}d`;
}

// fmtWhen shortens an RFC 3339 instant to what a chart axis or a status line has
// room for. The simulated seconds are rarely the interesting part.
const fmtWhen = (iso) => (iso || "").replace("T", " ").replace(/(:\d\d)?Z$/, "");

// barHTML is the control strip. It borrows the token simulation's bar styling so
// the two play modes look like siblings rather than two unrelated toolbars.
function barHTML() {
  const opts = STUB_DURATIONS
    .map((d, i) => `<option value="${d.ms}"${i === 1 ? " selected" : ""}>${esc(t(d.label))}</option>`)
    .join("");
  const human = HUMAN_DURATIONS
    .map((d, i) => `<option value="${d.ms}"${i === 0 ? " selected" : ""}>${esc(t(d.label))}</option>`)
    .join("");
  return `
    <button class="btn play" id="pg-start" title="${esc(t("pg.startTitle"))}">${esc(t("pg.start"))}</button>
    <button class="btn neutral" id="pg-case" title="${esc(t("pg.caseTitle"))}" hidden>${esc(t("pg.case"))}</button>
    <button class="btn neutral" id="pg-step" title="${esc(t("pg.stepTitle"))}" hidden>${esc(t("pg.step"))}</button>
    <button class="btn neutral" id="pg-run" title="${esc(t("pg.runTitle"))}" hidden>${esc(t("pg.run"))}</button>
    <button class="btn neutral" id="pg-clock" title="${esc(t("pg.clockTitle"))}" hidden>${esc(t("pg.clock"))}</button>
    <button class="btn play" id="pg-batch" title="${esc(t("pg.batchTitle"))}" hidden>${esc(t("pg.batch"))}</button>
    <button class="btn neutral" id="pg-cancel" title="${esc(t("pg.cancelTitle"))}" hidden>${esc(t("pg.cancel"))}</button>
    <button class="btn neutral" id="pg-stop" title="${esc(t("pg.discardTitle"))}" hidden>${esc(t("pg.discard"))}</button>
    <label class="sim-speed" id="pg-dur-wrap" title="${esc(t("pg.durTitle"))}">${esc(t("pg.dur"))}
      <select class="speed" id="pg-dur">${opts}</select>
    </label>
    <label class="sim-speed" id="pg-human-wrap" title="${esc(t("pg.humanTitle"))}">${esc(t("pg.human"))}
      <select class="speed" id="pg-human">${human}</select>
    </label>
    <span class="sim-hint" id="pg-hint"></span>
    <span style="flex:1"></span>
    <span class="sim-stats" id="pg-stats"></span>`;
}

// The Playground takes three columns and a strip, because it is asking three
// questions at once and they are not the same question.
//
// What the run *will* be — the dataset, the timing, the policy, what it has to show
// — is decided before it starts and read back afterwards to see what produced a
// number. What the run *did* is read while it happens and after. And the cases
// themselves are a table, which is a shape neither column has room for. Stacked into
// one 300 px panel, as this began, the report pushed the dataset off the screen the
// moment a run finished, and going back to change one figure meant scrolling past
// everything the last run produced.
//
// setupPanelHTML is the left column: everything that decides what runs.
function setupPanelHTML() {
  return `
    <div class="vars-head"><b>${esc(t("pg.setup"))}</b></div>
    <div class="vars-list" id="pg-setup-body"></div>`;
}

// panelHTML is the right column: what the run did.
function panelHTML() {
  return `
    <div class="vars-head"><b>${esc(t("pg.analysis"))}</b>
      <button class="icon-btn" id="pg-panel-close" title="${esc(t("pg.backToDesign"))}" aria-label="${esc(t("pg.backToDesign"))}">&#10005;</button></div>
    <div class="vars-list" id="pg-body"></div>`;
}

// overlayBarHTML is the strip over the canvas: which measure shades the diagram,
// and the scale that shading means. It sits on the diagram rather than in a column
// because it is a control for the diagram — and the legend has to be beside the
// thing it explains, or it is a key to a map on another page.
function overlayBarHTML() {
  return `<div id="pg-overlay-body"></div>`;
}

// resultsHTML is the strip under the canvas: the cases themselves, a page at a time.
// It is under the diagram rather than in a column because it is a table — the one
// shape a 300 px column cannot hold.
function resultsHTML() {
  return `<div id="pg-results-body"></div>`;
}

// attachPlayground wires the Playground tab into an open editor. It creates its
// own bar and panel so the editor's markup stays as it is, and returns the handle
// the tab toggle drives.
export function attachPlayground(root, { api, toast, modeler }) {
  const editor = root.querySelector(".editor");
  const body = root.querySelector(".editor-body");
  if (!editor || !body) return { setActive() {}, destroy() {} };

  const bar = document.createElement("div");
  bar.className = "sim-bar";
  bar.id = "pg-bar";
  bar.hidden = true;
  bar.innerHTML = barHTML();
  editor.insertBefore(bar, body);

  // Left of the canvas, so the reading order down the page is: what to run, what it
  // ran on, what came out.
  const setupPanel = document.createElement("aside");
  setupPanel.className = "vars-panel";
  setupPanel.id = "pg-setup";
  setupPanel.hidden = true;
  setupPanel.innerHTML = setupPanelHTML();
  body.insertBefore(setupPanel, body.firstChild);

  const overlayBar = document.createElement("section");
  overlayBar.className = "pg-overlay";
  overlayBar.id = "pg-overlay";
  overlayBar.hidden = true;
  overlayBar.innerHTML = overlayBarHTML();
  body.insertBefore(overlayBar, root.querySelector("#canvas"));

  const results = document.createElement("section");
  results.className = "pg-results";
  results.id = "pg-results";
  results.hidden = true;
  results.innerHTML = resultsHTML();
  body.insertBefore(results, root.querySelector("#props-resizer"));

  const panel = document.createElement("aside");
  panel.className = "vars-panel";
  panel.id = "pg-panel";
  panel.hidden = true;
  panel.innerHTML = panelHTML();
  body.insertBefore(panel, root.querySelector("#props-resizer"));

  // The three pieces answer to the same handlers and the same lookups: they are one
  // panel that happens to be laid out in three places.
  const panes = [setupPanel, overlayBar, results, panel];
  const el = (id) => {
    const found = bar.querySelector("#" + id);
    if (found) return found;
    for (const p of panes) {
      const hit = p.querySelector("#" + id);
      if (hit) return hit;
    }
    return null;
  };
  const state = {
    session: null,   // {id, processId, seed, stubLabel}
    mode: "step",    // which of the two ways of driving the sandbox is on screen
    simTime: "",
    caseKey: "",     // the case being watched in Step mode
    result: null,    // its last read
    tasks: [],
    visits: {},
    startVars: "{}",
    outputs: "{}",
    busy: false,
    // The run's configuration, gathered before the sandbox opens because the stub
    // and pool policy is fixed for its life: two runs are only comparable if the
    // policy behind them is the same.
    pools: {},       // element id -> {pool, seats}
    hours: false,    // confine pools and arrivals to business hours
    // The batch: its dataset, its arrival profile, and what came back.
    // A dataset comes from one of three places, and the panel shows one at a time:
    // a list typed in, a description drawn from, or a file the server parses.
    source: "list",  // "list" | "generated" | "csv"
    cases: '[{"amount": 1200}, {"amount": 90}]',
    csv: null,       // a File, when the dataset came from one
    // The description: how many cases, and what each one carries. It starts on the
    // dataset everybody wants first — a few hundred cases with a random amount.
    gen: { count: 300, fields: [{ name: "amount", kind: "int", min: 100, max: 5000 }] },
    genPreview: null,
    arrival: "allAtOnce",
    arrivalN: 10,
    // The shape of the arrival stream, drawn before the run. The server lays the
    // arrivals out with the code the run uses, so the sparkline is the schedule
    // rather than a picture of one; the key is the request it answers, so a redraw
    // costs a call only when the timing itself changed.
    profile: null,
    profileKey: "",
    profileError: "",
    run: null,       // the last run status
    report: null,
    results: null,   // one page of the results table: {total, offset, rows}
    // One case from that table, opened to be read rather than driven. It is a field
    // of its own rather than Step mode's: the two are different questions about the
    // same sandbox, and leaving one behind in the other's view shows a case nobody
    // asked for.
    inspect: null,   // {index, key, end, durationMillis} — the row that was clicked
    inspected: null, // what the server said about it
    heat: null,      // the heat map, once a run has produced one
    // Which measure shades the diagram, or "off". One at a time: see OVERLAYS.
    overlay: "off",
    polling: 0,
    // What the run has to show, and what it showed. Expectations turn a report
    // somebody reads into a verdict something can act on — the same ones the
    // `atlas playground` runner exits a build on.
    // Rules are the half of the expectations a run-wide bound cannot state: they are
    // judged case by case, so "small applications are paid out" is a thing the panel
    // can say and a build can exit on.
    expect: { allFinish: true, noIncidents: true, p90Hours: "", mustReach: "", queue: {}, rules: [] },
    verdict: null,
    comparison: null,
    // The saved scenarios of this diagram, and the one this session came from.
    // A scenario carries the stub and pool policy, which is fixed for a sandbox's
    // life, so loading one replaces the sandbox rather than editing it.
    scenarios: [],
    scenarioId: "",
    scenarioName: "",
    baseline: null,
    pickedScenario: "",
  };
  // Overlay handles we added, so a redraw removes ours and leaves anything else
  // on the canvas alone.
  let drawn = { markers: [], overlays: [] };

  // ---- server calls ---------------------------------------------------------

  const path = (suffix) => `/api/v1/playground/sessions/${encodeURIComponent(state.session.id)}${suffix || ""}`;

  // guard runs one server call with the buttons disabled, so a slow step cannot be
  // double-fired, and turns a failure into a toast rather than a dead panel.
  async function guard(what, fn) {
    if (state.busy) return;
    state.busy = true;
    render();
    try {
      await fn();
    } catch (e) {
      toast(t("pg.failed", { what, error: e.message }), "err");
    } finally {
      state.busy = false;
      render();
    }
  }

  // drawnElements lists what the author drew, of one kind, straight off the canvas —
  // so the panel configures and reports against the diagram rather than against a
  // list somebody has to retype from it.
  //
  // A label is skipped. bpmn-js registers an element's *external* label as an
  // element of its own carrying the same business object, so anything that has one —
  // an event, a gateway — is in the registry twice. That is invisible in a picker,
  // where the second option looks like the first, and wrong in a breakdown, where an
  // outcome would be counted twice and every share halved.
  function drawnElements(wanted) {
    let registry;
    try { registry = modeler.get("elementRegistry"); } catch { return []; }
    const out = [];
    registry.forEach((e) => {
      const bo = e.businessObject;
      if (e.labelTarget || !bo || !bo.id || !wanted(bo.$type)) return;
      out.push({ id: bo.id, name: bo.name || bo.id });
    });
    return out;
  }

  // tasksInDiagram lists the elements a pool can be put on: the author configures
  // capacity against the tasks they drew.
  function tasksInDiagram() {
    return drawnElements((type) => TASK_TYPES.has(type));
  }

  // stubPolicy is the whole run configuration in the shape the open endpoint
  // takes: what answers each job, and which pools the work competes for.
  function stubPolicy() {
    const ms = Number(el("pg-dur").value);
    const humanMs = Number(el("pg-human").value);
    const stubs = { default: { minMillis: ms, maxMillis: ms } };
    if (humanMs >= 0) stubs.human = { minMillis: humanMs, maxMillis: humanMs };
    const pools = {}, poolOf = {};
    for (const [element, cfg] of Object.entries(state.pools)) {
      const name = (cfg.pool || "").trim();
      const seats = Number(cfg.seats);
      if (!name || !(seats > 0)) continue;
      pools[name] = { capacity: seats, calendar: state.hours ? BUSINESS_HOURS : {} };
      poolOf[element] = name;
    }
    if (Object.keys(pools).length) { stubs.pools = pools; stubs.poolOf = poolOf; }
    return stubs;
  }

  async function start() {
    const { xml } = await modeler.saveXML({ format: true });
    const sel = el("pg-dur");
    const stubLabel = sel.options[sel.selectedIndex].text;
    // Kept, because a scenario is the request that made the sandbox: saving one
    // later must store the policy this run actually used, not the boxes as they
    // stand by then.
    state.openRequest = {
      source: "xml",
      xml,
      startTime: new Date().toISOString().replace(/\.\d+Z$/, "Z"),
      stubs: stubPolicy(),
    };
    const s = await api("POST", "/api/v1/playground/sessions", state.openRequest);
    // xml is what this sandbox runs, so an import can be told apart from a change
    // (onImport below): a diagram put back is not a diagram that changed.
    state.session = { id: s.id, processId: s.processId, seed: s.seed, stubLabel, xml };
    state.simTime = s.simTime;
    state.caseKey = "";
    state.result = null;
    state.run = null;
    state.report = null;
    state.heat = null;
    await refresh();
  }

  async function stop() {
    if (!state.session) return;
    stopPolling();
    try { await api("DELETE", path()); } catch { /* already gone; the panel resets either way */ }
    state.session = null;
    state.tasks = [];
    state.visits = {};
    state.result = null;
    state.caseKey = "";
    state.run = null;
    state.report = null;
    state.heat = null;
    state.overlay = "off";
    state.verdict = null;
    state.comparison = null;
    state.openRequest = null;
    clearCanvas();
  }

  async function startCase() {
    const c = await api("POST", path("/cases"), { variables: parseJSON(state.startVars, "start") });
    state.caseKey = c.instanceKey;
    state.result = c;
    await refresh();
  }

  async function step() {
    const occ = await api("POST", path("/step"));
    state.simTime = occ.simTime;
    if (!occ.happened) toast(t("pg.restNothing"), "");
    await refresh();
  }

  async function run() {
    const prog = await api("POST", path("/run"));
    state.simTime = prog.simTime;
    if (!prog.quiescent) toast(t("pg.budget"), "");
    await refresh();
  }

  async function advance() {
    const out = await api("POST", path("/clock"), { millis: 3_600_000 });
    state.simTime = out.simTime;
    await refresh();
  }

  async function complete(jobKey) {
    await api("POST", path(`/tasks/${encodeURIComponent(jobKey)}/complete`),
      { variables: parseJSON(state.outputs, "outputs") });
    await refresh();
  }

  // parseJSON reports the mistake against the field it was made in, so "not valid
  // JSON" names which box to look at.
  // parseJSON reads one of the three JSON boxes; `which` names the box, and with it
  // what an empty one means and how a broken one is reported.
  const JSON_BOXES = { start: "pg.json.start", outputs: "pg.json.outputs", dataset: "pg.json.dataset" };
  function parseJSON(text, which) {
    try {
      return JSON.parse(text || (which === "dataset" ? "[]" : "{}"));
    } catch (e) {
      throw new Error(t(JSON_BOXES[which], { error: e.message }));
    }
  }

  // runBody is the batch request this panel would send: the dataset and the
  // arrival profile. It is one function so that what is run and what is saved as a
  // scenario cannot differ.
  function runBody() {
    if (state.source === "generated") return { generate: genBody(), arrival: arrivalBody() };
    return { cases: parseJSON(state.cases, "dataset"), arrival: arrivalBody() };
  }

  // typedValue reads what was typed as the value it looks like: 1200 as a number,
  // so the model can compare it against a threshold rather than against text, and
  // true as a boolean. A box that only ever produced strings would make every
  // generated amount fail the one comparison the dataset exists to exercise.
  function typedValue(text) {
    const s = String(text == null ? "" : text).trim();
    if (s === "true") return true;
    if (s === "false") return false;
    if (s !== "" && /^-?\d+(\.\d+)?$/.test(s)) return Number(s);
    return s;
  }

  // parseChoices reads "gold:1, silver:3, standard:6" — or the same list without
  // the weights, which makes the options equally likely. One box rather than a
  // row per option: a list of three is the common case, and it is a list.
  function parseChoices(text) {
    return String(text || "").split(",").map((s) => s.trim()).filter(Boolean).map((s) => {
      const m = /^(.*):(\d+)$/.exec(s);
      if (m) return { value: typedValue(m[1]), weight: Number(m[2]) };
      return { value: typedValue(s) };
    });
  }

  // genBody is the dataset description in the shape the run and preview endpoints
  // take. Fields with no name are left out rather than refused: an empty row is a
  // row somebody has not filled in yet.
  function genBody() {
    const num = (v) => Number(v) || 0;
    const fields = state.gen.fields.filter((f) => (f.name || "").trim()).map((f) => {
      const out = { name: f.name.trim(), kind: f.kind };
      switch (f.kind) {
        case "int":
        case "decimal":
          out.min = num(f.min);
          out.max = num(f.max);
          if (f.kind === "decimal") out.decimals = num(f.decimals);
          break;
        case "bool": out.percentTrue = num(f.percentTrue); break;
        case "choice": out.choices = parseChoices(f.choices); break;
        case "constant": out.value = typedValue(f.value); break;
        case "sequence": out.prefix = String(f.prefix || ""); break;
        case "timestamp":
          out.fromMillis = Math.round(num(f.fromDays) * DAY_MS);
          out.toMillis = Math.round(num(f.toDays) * DAY_MS);
          out.onlyDate = !!f.onlyDate;
          break;
      }
      return out;
    });
    return { count: Math.max(1, num(state.gen.count)), fields };
  }

  // genFromBody is genBody read back, so a saved scenario fills the same boxes it
  // was built from.
  function genFromBody(g) {
    const at = (v) => (v == null ? "" : v);
    return {
      count: g.count || 1,
      fields: (g.fields || []).map((f) => ({
        name: f.name || "", kind: f.kind || "int",
        min: at(f.min), max: at(f.max), decimals: at(f.decimals),
        percentTrue: at(f.percentTrue),
        choices: (f.choices || []).map((c) => (c.weight ? `${c.value}:${c.weight}` : String(c.value))).join(", "),
        value: at(f.value), prefix: f.prefix || "",
        fromDays: (f.fromMillis || 0) / DAY_MS, toDays: (f.toMillis || 0) / DAY_MS,
        onlyDate: !!f.onlyDate,
      })),
    };
  }

  // expectBody is what the run has to show, in the shape the verdict endpoint
  // takes. "Every case finishes" is resolved against the run that actually
  // happened rather than against the dataset in the box, so it means the same
  // thing for a list typed in and for a file the server parsed.
  function expectBody() {
    const e = {};
    const cases = (state.run && state.run.cases) || 0;
    if (state.expect.allFinish && cases > 0) e.minCompleted = cases;
    if (state.expect.noIncidents) e.maxIncidents = 0;
    const hours = Number(state.expect.p90Hours);
    if (hours > 0) e.maxP90Millis = Math.round(hours * 3_600_000);
    const reach = String(state.expect.mustReach || "").split(",").map((x) => x.trim()).filter(Boolean);
    if (reach.length) {
      e.minVisits = {};
      for (const id of reach) e.minVisits[id] = 1;
    }
    const queue = {};
    for (const [pool, v] of Object.entries(state.expect.queue || {})) {
      if (String(v).trim() !== "" && Number(v) >= 0) queue[pool] = Number(v);
    }
    if (Object.keys(queue).length) e.maxQueue = queue;
    const rules = (state.expect.rules || [])
      .filter((r) => String(r.then || "").trim())
      .map((r) => {
        const out = { then: r.then.trim() };
        if (String(r.when || "").trim()) out.when = r.when.trim();
        if (String(r.name || "").trim()) out.name = r.name.trim();
        return out;
      });
    if (rules.length) e.rules = rules;
    return e;
  }

  // expectFromBody is expectBody read back, so a saved scenario fills the same
  // boxes it was built from.
  function expectFromBody(e) {
    const out = { allFinish: !!e.minCompleted, noIncidents: e.maxIncidents === 0,
      p90Hours: e.maxP90Millis ? String(e.maxP90Millis / 3_600_000) : "",
      mustReach: Object.keys(e.minVisits || {}).join(", "), queue: {},
      rules: (e.rules || []).map((r) => ({ name: r.name || "", when: r.when || "", then: r.then || "" })) };
    for (const [pool, v] of Object.entries(e.maxQueue || {})) out.queue[pool] = String(v);
    return out;
  }

  // arrivalBody is the timing half of a batch request.
  function arrivalBody() {
    const a = { mode: state.arrival };
    if (state.arrival === "every") a.intervalMillis = Math.max(1, Number(state.arrivalN)) * 60_000;
    if (state.arrival === "poisson") a.perHour = Math.max(0.01, Number(state.arrivalN));
    if (state.hours) a.calendar = BUSINESS_HOURS;
    return a;
  }

  // startBatch sends the dataset — typed in or uploaded — and then watches it. A
  // CSV goes as a file rather than being parsed here: the server parses it with
  // the same code a real CSV import uses (ADR-0084/0139), so what the Playground
  // reads from a file is what production would read from it.
  async function startBatch() {
    state.report = null;
    state.heat = null;
    state.overlay = "off";
    state.verdict = null;
    state.comparison = null;
    state.results = null;
    closeCase();
    if (state.source === "csv") {
      if (!state.csv) throw new Error(t("pg.csvMissing"));
      const form = new FormData();
      form.append("file", state.csv);
      form.append("arrival", JSON.stringify(arrivalBody()));
      const res = await fetch(path("/runs/csv"), { method: "POST", body: form });
      const data = await res.json().catch(() => null);
      if (!res.ok) throw new Error((data && data.error) || res.statusText);
      state.run = data;
    } else {
      const body = runBody();
      if (body.cases && !body.cases.length) {
        throw new Error(t("pg.emptyDataset"));
      }
      state.run = await api("POST", path("/runs"), body);
    }
    startPolling();
  }

  // previewDataset shows the first cases the description would produce — the
  // cases the run will carry, drawn on this sandbox's own seed, rather than an
  // illustration of what one might look like.
  async function previewDataset() {
    state.genPreview = await api("POST", path("/generate?limit=8"), genBody());
  }

  // plannedCases is how many cases the run would carry, from whichever of the three
  // sources is driving it. A CSV's size is not one of them: the file is parsed on
  // the server, so the browser cannot count its rows without reading it twice.
  function plannedCases() {
    if (state.source === "generated") return Math.max(0, Math.round(Number(state.gen.count)) || 0);
    if (state.source === "list") {
      try {
        const rows = JSON.parse(state.cases);
        return Array.isArray(rows) ? rows.length : 0;
      } catch { return 0; }
    }
    return 0;
  }

  // profileKey is the request the drawn profile answers: the count and the timing,
  // and nothing else on the panel. Everything the shape depends on is in here, so a
  // key that has not changed is a picture that does not need fetching again.
  function profileKey() {
    const count = plannedCases();
    if (!state.session || state.mode !== "batch" || count < 1) return "";
    return JSON.stringify({ count, arrival: arrivalBody() });
  }

  // refreshProfile fetches the shape of the stream the timing describes.
  //
  // It runs off the render rather than off each box, so every way of changing the
  // timing — the mode, its parameter, the business-hours box, the size of the
  // dataset — redraws it without a handler of its own. It is deliberately outside
  // guard(): a preview is not something to disable the panel's buttons for, and a
  // stream the planner refuses is shown where the numbers were typed rather than as
  // a toast that outlives them.
  async function refreshProfile() {
    const key = profileKey();
    if (key === state.profileKey) return;
    state.profileKey = key;
    state.profile = null;
    state.profileError = "";
    if (!key) { drawProfile(); return; }
    try {
      const out = await api("POST", path("/arrivals"), JSON.parse(key));
      // A later change may have overtaken this answer. Only the current one is
      // drawn: a sparkline of the timing somebody has already replaced is worse
      // than none, because nothing on screen says it is stale.
      if (key !== state.profileKey) return;
      state.profile = out;
    } catch (e) {
      if (key !== state.profileKey) return;
      state.profileError = e.message;
    }
    drawProfile();
  }

  // drawProfile repaints the sparkline and nothing else.
  //
  // The answer lands while somebody is still typing in the column beside it, and a
  // full render would replace the box under their caret — and, worse, the button
  // their click is halfway through. The picture owns one element; it redraws that.
  function drawProfile() {
    const slot = el("pg-profile");
    if (slot) slot.innerHTML = arrivalProfileHTML();
    // A stream the planner refuses is step 2's readout as much as it is the missing
    // picture, so the two are never out of step with each other.
    drawSteps();
  }

  // profileTimer debounces the boxes that feed the sparkline. A count is typed a
  // digit at a time and "5", "50" and "500" are three different pictures; only the
  // one somebody stopped on is worth fetching.
  let profileTimer = 0;
  function profileSoon() {
    clearTimeout(profileTimer);
    profileTimer = setTimeout(refreshProfile, 300);
  }

  // resultsPageSize is how many cases the strip shows at a time. A page is read by a
  // person: enough to scroll, few enough that the fifty-thousandth case costs the
  // same as the fiftieth.
  const resultsPageSize = 50;

  // readResults fetches one page. The rows are never held whole — a run of fifty
  // thousand is fifty thousand rows in the sandbox's store and one page in here.
  // openCase reads one case of a finished batch, so the diagram shows the path *that
  // case* took rather than what all of them did together.
  //
  // It reads rather than drives: the sandbox's Step controls act on the whole
  // sandbox, and offering them here would invite stepping a run that is over.
  async function openCase(row) {
    state.inspect = { index: row.index, key: row.instanceKey, end: row.end, durationMillis: row.durationMillis };
    state.inspected = await api("GET", path(`/cases/${encodeURIComponent(row.instanceKey)}`));
    drawCanvas();
  }

  function closeCase() {
    if (!state.inspect) return;
    state.inspect = null;
    state.inspected = null;
    drawCanvas();
  }

  async function readResults(offset) {
    state.results = await api("GET", path(`/results?offset=${Math.max(0, offset)}&limit=${resultsPageSize}`));
  }

  async function cancelBatch() {
    state.run = await api("POST", path("/runs/cancel"));
    await finishBatch();
  }

  // startPolling watches a batch. Only the status endpoint is asked while it runs
  // — it is O(1) in the number of cases, which is what makes it safe to ask every
  // second of a fifty-thousand-case run.
  function startPolling() {
    stopPolling();
    state.polling = setInterval(async () => {
      if (!state.session) { stopPolling(); return; }
      try {
        state.run = await api("GET", path("/runs"));
      } catch {
        stopPolling(); // the session went away; the panel says so on the next render
        return;
      }
      if (state.run.state === "running") { render(); return; }
      stopPolling();
      await finishBatch();
    }, 700);
    render();
  }

  function stopPolling() {
    if (state.polling) clearInterval(state.polling);
    state.polling = 0;
  }

  // finishBatch reads what the run produced. The report and the heat map are asked
  // for once, at the end, rather than polled: both fold the whole run.
  async function finishBatch() {
    stopPolling();
    try {
      const [report, heat] = await Promise.all([
        api("GET", path("/report")),
        api("GET", path("/heatmap")),
      ]);
      state.report = report;
      state.heat = heat;
      state.simTime = report.simEnd || state.simTime;
      state.overlay = "runs";
      // The verdict is asked for after the report because "every case finishes"
      // means the cases this run had, which is a thing only the finished run knows.
      state.verdict = await api("POST", path("/verdict"), expectBody());
      if (state.baseline) {
        state.comparison = await api("POST", path("/compare"), { baseline: state.baseline });
      }
      await readResults(0);
      drawCanvas();
    } catch (e) {
      toast(t("pg.reportFailed", { error: e.message }), "err");
    }
    render();
  }

  // ---- scenarios ------------------------------------------------------------

  // diagramProcessId is which diagram a scenario belongs to. The session knows it
  // once one is open; before that the canvas does, which is what lets the setup
  // view offer the scenarios of the diagram on screen.
  function diagramProcessId() {
    if (state.session) return state.session.processId;
    try {
      return modeler.get("canvas").getRootElement().businessObject.id || "";
    } catch { return ""; }
  }

  async function loadScenarios() {
    const pid = diagramProcessId();
    if (!pid) { state.scenarios = []; return; }
    state.scenarios = await api("GET", `/api/v1/playground/scenarios?processId=${encodeURIComponent(pid)}`) || [];
  }

  // openScenario replaces the sandbox with one the scenario describes, and fills
  // the panel in from it. It replaces rather than edits because the stub and pool
  // policy is fixed for a sandbox's life: a run is only comparable with another
  // run if the policy behind them is the same.
  async function openScenario(id) {
    const sc = await api("GET", `/api/v1/playground/scenarios/${encodeURIComponent(id)}`);
    if (state.session) await stop();
    const s = await api("POST", "/api/v1/playground/sessions", sc.spec.open);
    state.session = {
      id: s.id, processId: s.processId, seed: s.seed, stubLabel: t("pg.fromScenario"),
      xml: (sc.spec.open && sc.spec.open.xml) || "",
    };
    state.simTime = s.simTime;
    state.mode = "batch";
    state.scenarioId = sc.id;
    state.scenarioName = sc.name;
    state.baseline = sc.baseline || null;
    state.pickedScenario = sc.id;
    state.csv = null;
    state.genPreview = null;
    const run = sc.spec.run || {};
    state.source = run.generate ? "generated" : "list";
    if (run.generate) state.gen = genFromBody(run.generate);
    state.cases = JSON.stringify(run.cases || [], null, 1);
    const arrival = run.arrival || {};
    state.arrival = arrival.mode || "allAtOnce";
    if (arrival.intervalMillis) state.arrivalN = Math.round(arrival.intervalMillis / 60000);
    if (arrival.perHour) state.arrivalN = arrival.perHour;
    state.expect = expectFromBody(sc.spec.expect || {});
    state.run = null;
    state.report = null;
    state.heat = null;
    state.verdict = null;
    state.comparison = null;
    state.results = null;
    closeCase();
    await refresh();
  }

  // saveScenario stores the run this panel would start, so somebody else — or a
  // build — can run exactly it. What is stored is the requests themselves, which
  // is why there is nothing here that has to be kept in step with the endpoints.
  async function saveScenario() {
    if (state.source === "csv") {
      throw new Error(t("pg.scenario.csv"));
    }
    const name = (state.scenarioName || "").trim();
    if (!name) throw new Error(t("pg.scenario.noName"));
    const id = state.scenarioId || name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
    // The seed the sandbox actually used is pinned into what is stored. Without it
    // the scenario would be re-seeded from the clock every time it is opened, and a
    // "reproducible" run would come back with different numbers — which is the one
    // thing a saved scenario exists to prevent. The open response carries the seed
    // precisely so it can be written down.
    const open = { ...(state.openRequest || {}), seed: state.session.seed };
    const saved = await api("POST", "/api/v1/playground/scenarios", {
      id, name, processId: diagramProcessId(),
      spec: { open, run: runBody(), expect: expectBody() },
    });
    state.scenarioId = saved.id;
    state.pickedScenario = saved.id;
    await loadScenarios();
    toast(t("pg.scenario.saved", { name: saved.name }), "ok");
  }

  async function deleteScenario() {
    if (!state.scenarioId) return;
    await api("DELETE", `/api/v1/playground/scenarios/${encodeURIComponent(state.scenarioId)}`);
    state.scenarioId = "";
    state.pickedScenario = "";
    state.baseline = null;
    await loadScenarios();
  }

  // keepBaseline records this run as what the next one is measured against. Only
  // a run that passed: a baseline is the thing to beat, so keeping a failing one
  // would hide the failure from every run after it.
  async function keepBaseline() {
    if (!state.scenarioId || !state.report) return;
    if (state.verdict && !state.verdict.passed) {
      throw new Error(t("pg.baseline.failing"));
    }
    await api("PUT", `/api/v1/playground/scenarios/${encodeURIComponent(state.scenarioId)}/baseline`, state.report);
    state.baseline = state.report;
    state.comparison = null;
    await loadScenarios();
    toast(t("pg.baseline.kept"), "ok");
  }

  // refresh re-reads what the Step view shows. One round trip per view; the
  // sandbox is local to the server, so this is cheap.
  async function refresh() {
    if (!state.session) return;
    const [tasks, visits] = await Promise.all([
      api("GET", path("/tasks")),
      api("GET", path("/overlay")),
    ]);
    state.tasks = tasks || [];
    state.visits = visits || {};
    if (state.caseKey) {
      state.result = await api("GET", path(`/cases/${encodeURIComponent(state.caseKey)}`));
    }
    const s = await api("GET", path());
    state.simTime = s.simTime;
    drawCanvas();
  }

  // ---- canvas ---------------------------------------------------------------

  function clearCanvas() {
    let canvas, overlays;
    try { canvas = modeler.get("canvas"); overlays = modeler.get("overlays"); } catch { drawn = { markers: [], overlays: [] }; return; }
    for (const [id, marker] of drawn.markers) { try { canvas.removeMarker(id, marker); } catch { /* shape gone */ } }
    for (const id of drawn.overlays) { try { overlays.remove(id); } catch { /* gone */ } }
    drawn = { markers: [], overlays: [] };
  }

  // connectionIndex maps a source-to-target pair to the sequence flow that joins
  // them. The server names a flow by its ends rather than by a BPMN id, because a
  // compiled flow does not carry one — and the client holding the diagram is
  // exactly the place that can resolve it.
  function connectionIndex(registry) {
    const out = new Map();
    registry.forEach((e) => {
      const bo = e.businessObject;
      if (bo && bo.$type === "bpmn:SequenceFlow" && bo.sourceRef && bo.targetRef) {
        out.set(bo.sourceRef.id + " " + bo.targetRef.id, e.id);
      }
    });
    return out;
  }

  // heatLevel places a count on the shading scale. Zero is its own level: "never
  // reached" is a different statement from "reached least", and a heat map that
  // renders them alike cannot answer a coverage question.
  const heatLevel = (count, max) =>
    count <= 0 ? 0 : Math.max(1, Math.ceil(HEAT_LEVELS * count / Math.max(1, max)));

  function drawCanvas() {
    clearCanvas();
    let canvas, overlays, registry;
    try {
      canvas = modeler.get("canvas");
      overlays = modeler.get("overlays");
      registry = modeler.get("elementRegistry");
    } catch { return; }
    if (state.inspect && state.inspected) drawCase(canvas, overlays, registry);
    else if (state.overlay !== "off" && state.heat) drawOverlay(canvas, overlays, registry);
    else drawRun(canvas, overlays, registry);
  }

  // drawCase paints one case's own path: the elements it went through, numbered in
  // the order it reached them.
  //
  // The number is what makes this a replay rather than a second coverage map. An
  // element the case looped through carries every step it was, because "3, 7" is the
  // loop — and a single count would hide the thing somebody opened the case to see.
  function drawCase(canvas, overlays, registry) {
    const steps = new Map();
    (state.inspected.path || []).forEach((id, i) => {
      if (!steps.has(id)) steps.set(id, []);
      steps.get(id).push(i + 1);
    });
    const last = (state.inspected.path || [])[(state.inspected.path || []).length - 1];
    const running = state.inspected.state !== "completed";
    for (const [id, at] of steps) {
      if (!registry.get(id)) continue;
      // The last element of an unfinished case is where it stands now, not somewhere
      // it has been: a parked case reads as stuck there, which is the point.
      const marker = running && id === last ? "atlas-active" : "atlas-visited";
      canvas.addMarker(id, marker);
      drawn.markers.push([id, marker]);
      try {
        drawn.overlays.push(overlays.add(id, "pg-visits", {
          position: { bottom: 4, right: 4 },
          html: `<div class="token-badges"><div class="token-badge history"
            title="${esc(t("pg.mark.steps", { steps: at.map((n) => fmtCount(n)).join(", ") }))}">${at.map((n) => fmtCount(n)).join(", ")}</div></div>`,
        }));
      } catch { /* shape without graphics */ }
    }
  }

  // drawRun paints a single case onto the diagram: every element a token has
  // passed through is marked and carries its count, and an element with a job
  // waiting for a person is marked live. It reuses the runtime view's markers, so
  // a playground run reads exactly like a real instance does.
  function drawRun(canvas, overlays, registry) {
    const waiting = new Set(state.tasks.map((t) => t.element));
    for (const [id, count] of Object.entries(state.visits)) {
      if (!registry.get(id)) continue;
      const live = waiting.has(id);
      const marker = live ? "atlas-active" : "atlas-visited";
      canvas.addMarker(id, marker);
      drawn.markers.push([id, marker]);
      try {
        drawn.overlays.push(overlays.add(id, "pg-visits", {
          position: { bottom: 4, right: 4 },
          html: `<div class="token-badges"><div class="token-badge history" title="${esc(plural("pg.mark.passed", count, { n: fmtCount(count) }))}">${fmtCount(count)}</div></div>`,
        }));
      } catch { /* shape without graphics */ }
    }
    // A task waiting on an element the token has not "visited" yet cannot happen —
    // a visit is recorded on activation — so the loop above covers every marker.
  }

  // drawHeatMap shades the whole diagram by how much the run used each part, edges
  // included. The cold parts are the reason it draws every element rather than
  // only the ones with a count: an author looking for the branch their data never
  // exercised needs to see it marked, not merely left plain.
  // measure is the chosen overlay's value for every element the model has, and the
  // largest of them.
  //
  // Every element the heat map knows about is in it, at zero when the run measured
  // nothing there. That is the coverage half of the picture and it holds for all
  // four measures: "no case waited here" is a different statement from "this element
  // is not in the report", and a map that dropped the second would answer neither.
  function measure(key) {
    const values = new Map();
    for (const e of (state.heat && state.heat.elements) || []) values.set(e.id, 0);
    if (key === "runs") {
      for (const e of (state.heat && state.heat.elements) || []) values.set(e.id, e.count);
    } else {
      const field = { work: "workMillis", wait: "waitMillis", incidents: "incidents" }[key];
      for (const [id, st] of Object.entries((state.report && state.report.elements) || {})) {
        values.set(id, st[field] || 0);
      }
    }
    let max = 0;
    for (const v of values.values()) max = Math.max(max, v);
    return { values, max };
  }

  // overlayFormat renders one of a measure's values for a badge and for the legend.
  const overlayFormat = (key, v) => (key === "work" || key === "wait" ? fmtDur(v) : fmtCount(v));

  function drawOverlay(canvas, overlays, registry) {
    const key = state.overlay;
    const spec = OVERLAYS.find((o) => o.key === key);
    if (!spec) return;
    const { values, max } = measure(key);
    // A zero the measure has nothing to say about is left alone, shape and badge
    // alike: an untouched element and a "0s" badge on every event are the same
    // statement, and the second one covers the diagram to make it.
    const shows = (v) => v !== 0 || spec.cold;
    const mark = (id, v) => {
      if (!id || !registry.get(id) || !shows(v)) return;
      const cls = `pg-heat-${heatLevel(v, max)}`;
      canvas.addMarker(id, cls);
      drawn.markers.push([id, cls]);
    };
    for (const [id, v] of values) {
      mark(id, v);
      if (!registry.get(id) || !shows(v)) continue;
      try {
        // Above the shape, not below it: an event's own label sits underneath, and
        // a badge that hides the name of the end somebody is looking for costs more
        // than it tells them.
        const text = overlayFormat(key, v);
        drawn.overlays.push(overlays.add(id, "pg-heat", {
          position: { top: -10, right: -6 },
          html: `<div class="token-badges"><div class="token-badge heat"
            title="${esc(t("pg.mark.heat", { measure: t(spec.title), value: text }))}">${esc(text)}</div></div>`,
        }));
      } catch { /* shape without graphics */ }
    }
    if (!spec.flows) return;
    const conns = connectionIndex(registry);
    for (const f of state.heat.flows || []) {
      mark(conns.get(f.from + " " + f.to), f.count);
    }
  }

  // ---- panel ----------------------------------------------------------------

  function taskRowHTML(task) {
    const kind = t(task.human ? "pg.task.user" : "pg.task.job");
    return `<div class="pg-task">
      <div><b>${esc(task.element)}</b> <span class="muted">${esc(kind)}</span></div>
      <button class="btn neutral small" data-job="${esc(task.jobKey)}"
        title="${esc(t("pg.task.completeTitle"))}">${esc(t("pg.task.complete"))}</button>
    </div>`;
  }

  function resultHTML(c) {
    if (!c) return `<p class="muted">${t("pg.case.none")}</p>`;
    const vars = Object.entries(c.variables || {})
      .map(([k, v]) => `<tr><td>${esc(k)}</td><td class="mono">${esc(v)}</td></tr>`).join("");
    return `
      <div class="pg-result">
        <div><span class="muted">${esc(t("pg.case.state"))}</span> <b>${esc(c.state)}</b>
          ${c.incidents ? `<span class="pg-inc">${esc(plural("pg.case.incidents", c.incidents))}</span>` : ""}</div>
        <div><span class="muted">${esc(t("pg.case.path"))}</span> <span class="mono">${esc((c.path || []).join(" → ")) || "—"}</span></div>
        ${vars ? `<table class="pg-vars">${vars}</table>` : `<p class="muted">${esc(t("pg.case.noVars"))}</p>`}
      </div>`;
  }

  // setupHTML is what the run will be made of: how each job is answered and what
  // it has to queue for. It is asked before the sandbox opens because the policy
  // is fixed for its life — a run is only comparable with another run if the
  // policy behind them is the same.
  function setupHTML() {
    const tasks = tasksInDiagram();
    const rows = tasks.map((task) => {
      const cfg = state.pools[task.id] || {};
      return `<div class="pg-pool">
        <label title="${esc(task.id)}">${esc(task.name)}</label>
        <input type="text" data-pool="${esc(task.id)}" value="${esc(cfg.pool || "")}"
          placeholder="${esc(t("pg.pool.placeholder"))}" title="${esc(t("pg.pool.title"))}" />
        <input type="number" min="1" step="1" data-seats="${esc(task.id)}" value="${esc(cfg.seats || "")}"
          placeholder="${esc(t("pg.seats.placeholder"))}" title="${esc(t("pg.seats.title"))}" />
      </div>`;
    }).join("");
    return `
      <p class="muted">${esc(t("pg.setup.intro"))}</p>
      ${scenarioPickerHTML()}
      <div class="pg-sec"><b>${esc(t("pg.resources"))}</b> <span class="muted">${esc(t("pg.optional"))}</span></div>
      ${tasks.length
        ? `${rows}<label class="pg-check"><input type="checkbox" id="pg-hours" ${state.hours ? "checked" : ""} />
             ${esc(t("pg.businessHours"))}</label>`
        : `<p class="muted">${esc(t("pg.noTasks"))}</p>`}
      <p class="muted">${esc(t("pg.noPoolNote"))}</p>`;
  }

  // scenarioPickerHTML offers this diagram's saved runs. Opening one replaces the
  // sandbox, because the stub and pool policy travels with it and that policy is
  // fixed for a sandbox's life — which is also why the picker lives here, before
  // one is open, rather than beside the dataset.
  function scenarioPickerHTML() {
    if (!state.scenarios.length) return "";
    const opts = state.scenarios.map((sc) =>
      `<option value="${esc(sc.id)}"${sc.id === state.pickedScenario ? " selected" : ""}>${esc(sc.name)}${
        sc.hasBaseline ? esc(t("pg.scenario.hasBaseline")) : ""}</option>`).join("");
    return `
      <div class="pg-sec"><b>${esc(t("pg.scenario.saved.heading"))}</b> <span class="muted">${state.scenarios.length}</span></div>
      <div class="pg-timing">
        <select id="pg-scenario-pick"><option value="">${esc(t("pg.scenario.pick"))}</option>${opts}</select>
        <button class="btn neutral small" id="pg-scenario-open">${esc(t("pg.scenario.open"))}</button>
      </div>
      <p class="muted">${esc(t("pg.scenario.openNote"))}</p>`;
  }

  // expectHTML is what the run has to show. It is the same set the `atlas
  // playground` runner exits a build on, which is the point: what an author checks
  // by eye here is what a build checks without them.
  function expectHTML() {
    const pools = Object.values(state.pools)
      .map((c) => (c.pool || "").trim()).filter(Boolean);
    const seen = new Set();
    const queueRows = pools.filter((p) => !seen.has(p) && seen.add(p)).map((p) => `
      <div class="pg-timing"><span class="muted" style="flex:1">${esc(t("pg.x.queue", { pool: p }))}</span>
        <input type="number" min="0" step="1" data-queue="${esc(p)}" value="${esc(state.expect.queue[p] || "")}" /></div>`).join("");
    return `
      ${stepHeadHTML(3)}
      <label class="pg-check"><input type="checkbox" id="pg-x-finish" ${state.expect.allFinish ? "checked" : ""} />
        ${esc(t("pg.x.finish"))}</label>
      <label class="pg-check"><input type="checkbox" id="pg-x-inc" ${state.expect.noIncidents ? "checked" : ""} />
        ${esc(t("pg.x.noIncidents"))}</label>
      <div class="pg-timing"><span class="muted" style="flex:1">${esc(t("pg.x.p90"))}</span>
        <input type="number" min="0" step="0.5" id="pg-x-p90" value="${esc(state.expect.p90Hours)}" />
        <span class="muted">${esc(t("pg.x.hours"))}</span></div>
      <label class="field"><span>${esc(t("pg.x.reach"))}</span>
        <textarea id="pg-x-reach" rows="1" spellcheck="false">${esc(state.expect.mustReach)}</textarea></label>
      ${queueRows}
      ${rulesHTML()}`;
  }

  // rulesHTML is the per-case half of the expectations: a row per rule, in the
  // language the diagram's own gateways are written in.
  //
  // The end events are offered off the canvas rather than typed, the way the pool
  // rows are — an author asserts against the outcomes they drew, and the box stays
  // editable for the assertions that are not about an end event.
  function rulesHTML() {
    const rows = (state.expect.rules || []).map(ruleRowHTML).join("");
    return `
      <div class="pg-sec"><b>${esc(t("pg.rules.heading"))}</b> <span class="muted">${esc(t("pg.rules.feel"))}</span></div>
      ${rows || `<p class="muted">${t("pg.rules.intro")}</p>`}
      <div class="pg-timing">
        <button class="btn neutral small" id="pg-rule-add">${esc(t("pg.rules.add"))}</button>
      </div>`;
  }

  function ruleRowHTML(r, i) {
    const ends = endEventsInDiagram();
    const options = ends.map((e) =>
      `<option value="${esc(e.id)}">${esc(e.name)}</option>`).join("");
    return `
      <div class="pg-rule">
        <div class="pg-rule-head">
          <span class="muted">${esc(t("pg.rule.when"))}</span>
          <input type="text" data-rule="when" data-i="${i}" value="${esc(r.when)}"
            placeholder="${esc(t("pg.rule.whenPlaceholder"))}" aria-label="${esc(t("pg.rule.whenLabel"))}" />
          <button class="icon-btn" data-rule-del="${i}" title="${esc(t("pg.rule.remove"))}"
            aria-label="${esc(t("pg.rule.remove"))}">&#10005;</button>
        </div>
        <div class="pg-rule-head">
          <span class="muted">${esc(t("pg.rule.then"))}</span>
          <input type="text" data-rule="then" data-i="${i}" value="${esc(r.then)}"
            placeholder="end = &quot;approved&quot;" aria-label="${esc(t("pg.rule.thenLabel"))}" />
          ${ends.length ? `<select data-rule-end="${i}" aria-label="${esc(t("pg.rule.insertEnd"))}">
            <option value="">${esc(t("pg.rule.endsAt"))}</option>${options}</select>` : ""}
        </div>
      </div>`;
  }

  // endEventsInDiagram lists the outcomes the author drew, so a rule is written —
  // and a run broken down — against the diagram rather than against a list of ids
  // retyped from it.
  function endEventsInDiagram() {
    return drawnElements((type) => type === "bpmn:EndEvent");
  }

  // scenarioSaveHTML is how a run becomes repeatable by somebody who is not here.
  function scenarioSaveHTML() {
    if (state.source === "csv") {
      return `<div class="pg-sec"><b>${esc(t("pg.scenario"))}</b></div>
        <p class="muted">${t("pg.scenario.csvNote")}</p>`;
    }
    return `
      <div class="pg-sec"><b>${esc(t("pg.scenario"))}</b>
        <span class="muted">${state.scenarioId ? esc(state.scenarioId) : esc(t("pg.scenario.unsaved"))}</span></div>
      <div class="pg-timing">
        <input type="text" id="pg-scenario-name" placeholder="${esc(t("pg.scenario.namePlaceholder"))}" value="${esc(state.scenarioName)}" style="flex:1" />
        <button class="btn neutral small" id="pg-scenario-save">${esc(t("pg.scenario.save"))}</button>
        ${state.scenarioId ? `<button class="btn neutral small" id="pg-scenario-delete">${esc(t("pg.scenario.delete"))}</button>` : ""}
      </div>
      ${state.baseline
        ? `<p class="muted">${esc(t("pg.baseline.stored"))}</p>`
        : `<p class="muted">${esc(t("pg.baseline.hint"))}</p>`}`;
  }

  // verdictHTML is the run judged. A verdict with no checks is a pass, and says so
  // quietly: "I have not said what I expect yet" must not read as a problem.
  function verdictHTML() {
    const v = state.verdict;
    if (!v) return "";
    if (!v.checks || !v.checks.length) {
      return `<div class="pg-sec"><b>${esc(t("pg.verdict"))}</b></div>
        <p class="muted">${esc(t("pg.verdict.nothing"))}</p>`;
    }
    // The per-case rules are left out of this table and shown as their own
    // breakdown below: a rule's statement is a sentence, and four columns of a 330 px
    // panel wrap it to four lines and then truncate what it did.
    const bounds = v.checks.filter((c) => !c.rule);
    return `
      <div class="pg-sec"><b>${esc(t("pg.verdict"))}</b>
        <span class="pg-verdict ${v.passed ? "ok" : "bad"}">${esc(t(v.passed ? "pg.verdict.passed" : "pg.verdict.failed"))}</span></div>
      <table class="pg-table pg-checks"><tbody>${bounds.map((c) => `<tr class="${c.passed ? "" : "pg-bad"}">
        <td>${c.passed ? "&#10003;" : "&#10007;"}</td><td>${esc(c.name)}</td>
        <td class="muted">${esc(c.want)}</td><td>${esc(c.got)}</td></tr>`).join("")}</tbody></table>
      ${ruleBreakdownHTML(v.rules)}
      ${state.scenarioId ? `<div class="pg-actions">
        <button class="btn neutral small" id="pg-keep-baseline"${v.passed ? "" : " disabled"}
          title="${esc(t(v.passed ? "pg.baseline.keepTitle" : "pg.baseline.failingTitle"))}">${esc(t("pg.baseline.keep"))}</button>
      </div>` : ""}`;
  }

  // comparisonHTML is this run beside the stored baseline. Only what moved: a
  // table of unchanged numbers is where the two that did move go to hide.
  // ruleBreakdownHTML is how a rule went, case by case: the split a single check
  // line cannot carry, and the cases that broke it — a number sends somebody
  // looking, the case numbers send them to the rows that did it.
  function ruleBreakdownHTML(rules) {
    if (!rules || !rules.length) return "";
    return `
      <div class="pg-sec"><b>${esc(t("pg.rules.heading"))}</b> <span class="muted">${esc(plural("pg.rules.count", rules.length))}</span></div>
      ${rules.map((r) => `
        <div class="pg-rule-result${r.passed ? "" : " bad"}">
          <div class="pg-rule-name">${esc(r.name)}</div>
          <div class="pg-rule-split">
            <span>${t("pg.rule.held", { n: r.satisfied })}</span>
            ${r.violated ? `<span class="bad">${t("pg.rule.broke", { n: r.violated })}</span>` : ""}
            ${r.undecided ? `<span class="muted">${t("pg.rule.unfinished", { n: r.undecided })}</span>` : ""}
            <span class="muted">${esc(t("pg.rule.of", { matched: r.matched, cases: r.cases }))}</span>
          </div>
          ${r.examples && r.examples.length ? `<div class="pg-rule-cases muted">${esc(t("pg.rule.cases", { list:
            r.examples.slice(0, 12).map((i) => i + 1).join(", ") + (
            r.truncated || r.examples.length > 12 ? t("pg.rule.andMore") : "") }))}</div>` : ""}
        </div>`).join("")}`;
  }

  // violatingCases is every case a rule broke on, as a set the results strip marks.
  // The server sends a bounded sample, so a row past the sample is simply not
  // marked; the breakdown above says the sample was cut.
  function violatingCases() {
    const out = new Set();
    for (const r of (state.verdict && state.verdict.rules) || []) {
      for (const i of r.examples || []) out.add(i);
    }
    return out;
  }

  // deviations are the cases worth looking at, counted over the whole run: the ones
  // a rule caught, the ones that never finished, and the incidents.
  //
  // Over the run rather than over the page, because that is the question somebody
  // asks of this line — whether there is anything here to page towards at all. A
  // count of what is on screen would answer a question nobody has, and would read as
  // the run's total next to "1–50 of 300".
  //
  // The rule count is distinct cases rather than a sum over the rules, so a case
  // that broke two is counted once; it comes from the same set the rows are marked
  // from, so the number and the marks cannot disagree. The server samples a rule's
  // offenders, so when a sample was cut the number is a floor and says so. Incidents
  // are counted as incidents, not as cases: a case can carry several, and the run
  // knows the one number and not the other.
  function deviations() {
    const rep = state.report || {};
    const rules = (state.verdict && state.verdict.rules) || [];
    const broke = violatingCases();
    const out = [];
    if (broke.size) {
      out.push(plural(rules.some((r) => r.truncated) ? "pg.dev.brokeAtLeast" : "pg.dev.broke", broke.size));
    }
    const unfinished = Math.max(0, (rep.cases || 0) - (rep.completed || 0));
    if (unfinished) out.push(t("pg.dev.unfinished", { n: unfinished }));
    if (rep.incidents) out.push(plural("pg.dev.incidents", rep.incidents));
    return out;
  }

  function comparisonHTML() {
    const c = state.comparison;
    if (!c || !c.deltas) return "";
    const moved = c.deltas.filter((d) => d.before !== d.after);
    if (!moved.length) {
      return `<div class="pg-sec"><b>${esc(t("pg.cmp.heading"))}</b></div>
        <p class="muted">${esc(t("pg.cmp.none"))}</p>`;
    }
    const cell = (unit, v) => unit === "millis" ? fmtDur(v) : unit === "percent" ? `${v}%` : String(v);
    return `
      <div class="pg-sec"><b>${esc(t("pg.cmp.heading"))}</b> <span class="muted">${esc(t("pg.cmp.changed", { n: moved.length }))}</span></div>
      <table class="pg-table pg-deltas"><tbody>${moved.map((d) => `<tr>
        <td>${esc(d.name)}</td>
        <td class="muted">${esc(cell(d.unit, d.before))}</td>
        <td class="${d.better ? "pg-better" : d.worse ? "pg-worse" : ""}">${esc(cell(d.unit, d.after))}</td>
      </tr>`).join("")}</tbody></table>`;
  }

  // batchHTML is the dataset, the timing, and what came back.
  // datasetHTML is the three ways data gets into a run, one at a time. They are
  // tabs rather than three boxes down the page because only one of them drives the
  // run, and a panel showing all three invites filling in two of them.
  function datasetHTML() {
    const tab = (id, label) =>
      `<button data-source="${id}"${state.source === id ? ' class="active"' : ""}>${label}</button>`;
    const body = state.source === "csv" ? csvHTML()
      : state.source === "generated" ? generatorHTML()
        : `<label class="field"><span>${esc(t("pg.data.cases"))}</span>
             <textarea id="pg-cases" rows="5" spellcheck="false">${esc(state.cases)}</textarea></label>`;
    return `
      ${stepHeadHTML(1)}
      <div class="pg-source">${tab("list", esc(t("pg.data.list")))}${tab("generated", esc(t("pg.data.generated")))}${tab("csv", esc(t("pg.data.csv")))}</div>
      ${body}`;
  }

  function csvHTML() {
    return state.csv
      ? `<div class="pg-file"><span class="mono">${esc(state.csv.name)}</span>
           <button class="btn neutral small" id="pg-csv-clear">${esc(t("pg.csv.other"))}</button></div>`
      : `<div class="pg-file"><input type="file" id="pg-csv" accept=".csv,text/csv" />
           <span class="muted">${esc(t("pg.csv.header"))}</span></div>`;
  }

  // generatorHTML describes the dataset rather than listing it: a count, and a row
  // per variable saying how its value is drawn.
  function generatorHTML() {
    return `
      <div class="pg-timing"><span class="muted" style="flex:1">${esc(t("pg.gen.count"))}</span>
        <input type="number" min="1" step="1" id="pg-gen-count" value="${esc(state.gen.count)}" /></div>
      <div class="pg-fields">${state.gen.fields.map(fieldRowHTML).join("")}</div>
      <div class="pg-timing">
        <button class="btn neutral small" id="pg-gen-add">${esc(t("pg.gen.add"))}</button>
        <button class="btn neutral small" id="pg-gen-preview">${esc(t("pg.gen.preview"))}</button>
      </div>
      ${previewHTML()}`;
  }

  // A field is two lines: what it is called and what kind it is, then that kind's
  // parameters. One line was tried first and does not fit — in a panel this narrow
  // a name and three boxes leave "amount" showing as "amour", and a date field with
  // two offsets and a checkbox is unreadable.
  function fieldRowHTML(f, i) {
    const kinds = FIELD_KINDS.map((k) =>
      `<option value="${k.kind}"${k.kind === f.kind ? " selected" : ""}>${esc(t(k.label))}</option>`).join("");
    return `
      <div class="pg-field">
        <div class="pg-field-head">
          <input type="text" data-gen="name" data-i="${i}" value="${esc(f.name)}"
            placeholder="${esc(t("pg.field.placeholder"))}" aria-label="${esc(t("pg.field.name"))}" />
          <select data-gen="kind" data-i="${i}" aria-label="${esc(t("pg.field.kind"))}">${kinds}</select>
          <button class="icon-btn" data-gen-del="${i}" title="${esc(t("pg.field.remove"))}"
            aria-label="${esc(t("pg.field.remove"))}">&#10005;</button>
        </div>
        <div class="pg-field-args">${fieldParamsHTML(f, i)}</div>
      </div>`;
  }

  function fieldParamsHTML(f, i) {
    const num = (key, hint) =>
      `<input type="number" data-gen="${key}" data-i="${i}" value="${esc(f[key])}"
         placeholder="${esc(hint)}" aria-label="${esc(hint)}" />`;
    const text = (key, hint) =>
      `<input type="text" data-gen="${key}" data-i="${i}" value="${esc(f[key])}"
         placeholder="${hint}" aria-label="${hint}" />`;
    switch (f.kind) {
      case "int": return num("min", t("pg.arg.min")) + num("max", t("pg.arg.max"));
      case "decimal": return num("min", t("pg.arg.min")) + num("max", t("pg.arg.max")) + num("decimals", t("pg.arg.places"));
      case "bool": return num("percentTrue", t("pg.arg.percentTrue"));
      case "choice": return text("choices", "gold:1, silver:3, standard:6");
      case "constant": return text("value", t("pg.arg.value"));
      case "sequence": return text("prefix", "ORDER-");
      default: return num("fromDays", t("pg.arg.fromDays")) + num("toDays", t("pg.arg.toDays")) +
        `<label class="pg-check"><input type="checkbox" data-gen="onlyDate" data-i="${i}"
           ${f.onlyDate ? "checked" : ""} /> ${esc(t("pg.arg.dateOnly"))}</label>`;
    }
  }

  // previewHTML shows the first cases of the described dataset. They are the cases
  // the run will carry, which is the only reason showing them is worth anything.
  function previewHTML() {
    const p = state.genPreview;
    if (!p) return "";
    if (!p.rows.length || !p.columns.length) {
      return `<p class="muted">${esc(t("pg.preview.empty"))}</p>`;
    }
    const cell = (v) => esc(typeof v === "boolean" ? String(v) : v);
    return `
      <div class="pg-preview"><table>
        <thead><tr>${p.columns.map((c) => `<th>${esc(c)}</th>`).join("")}</tr></thead>
        <tbody>${p.rows.map((r) =>
          `<tr>${p.columns.map((c) => `<td>${cell(r[c])}</td>`).join("")}</tr>`).join("")}</tbody>
      </table></div>
      <p class="muted">${esc(t("pg.preview.first", { n: p.rows.length, total: p.total }))}</p>`;
  }

  // The setup is three steps, and each of them says where it stands.
  //
  // The panel is one column somebody scrolls, not a wizard that hides its later
  // pages — every step is always visible and editable, because the third one is
  // usually the reason to go back and change the first. What the numbering buys is
  // an answer to "is this ready?" without reading three sections of boxes: each step
  // carries a one-line readout of what it will contribute to the run, or of what is
  // wrong with it.
  //
  // A step reports { ok, note, bad }: ok means it will contribute what the note says,
  // bad means the note is a fault rather than a summary, and neither means it is
  // simply not filled in yet. The first two must be ok before a run can start; the
  // third never has to be, because a run nobody asserts anything about is a run
  // somebody reads rather than a mistake.
  const STEPS = [
    { n: 1, title: "pg.step.dataset", read: () => dataStep() },
    { n: 2, title: "pg.step.timing", read: () => timingStep() },
    { n: 3, title: "pg.step.expect", read: () => expectStep() },
  ];

  function stepHeadHTML(n) {
    const spec = STEPS[n - 1];
    const step = spec.read();
    return `<div class="pg-sec pg-step" id="pg-step-${n}">
      <span class="pg-step-n${step.ok ? " done" : ""}">${n}</span>
      <b>${esc(t(spec.title))}</b>
      <span class="pg-step-note${step.bad ? " bad" : ""}">${esc(step.note)}</span>
    </div>`;
  }

  // drawSteps repaints the three heads and re-gates the button, and nothing else.
  //
  // Every box in the column feeds one of them, and most of those boxes are typed in
  // — so this runs on input, where a full render would take the caret out of the box
  // being typed in. A head carries no input of its own, so replacing one while
  // somebody types into the section below it is safe.
  function drawSteps() {
    for (const spec of STEPS) {
      const head = el(`pg-step-${spec.n}`);
      if (head) head.outerHTML = stepHeadHTML(spec.n);
    }
    gateRun();
  }

  // gateRun stops the button offering a run the setup cannot start. What is missing
  // is named in the step that is missing it, so the button says only that.
  function gateRun() {
    const batch = el("pg-batch");
    if (!batch || batch.hidden) return;
    const blocked = setupBlocked();
    batch.disabled = state.busy || blocked;
    batch.title = t(blocked ? "pg.gate.blocked" : "pg.batchTitle");
  }

  // dataStep is what the run will carry, from whichever of the three sources is
  // driving it — and what is wrong with it, which for a typed list is a question the
  // panel can answer without asking the server.
  function dataStep() {
    if (state.source === "csv") {
      return state.csv ? { ok: true, note: state.csv.name } : { note: t("pg.data.chooseFile") };
    }
    if (state.source === "generated") {
      const count = plannedCases();
      if (count < 1) return { bad: true, note: t("pg.data.howMany") };
      // The same two rules the generator itself applies. A row with no name is not
      // one of them: it is a row somebody has not filled in yet, and it is left out
      // of the request rather than refused. A name used twice is a mistake — a case
      // carries one value per name — and it is the one a copied row makes.
      const named = state.gen.fields.map((f) => (f.name || "").trim()).filter(Boolean);
      const twice = named.find((name, i) => named.indexOf(name) !== i);
      if (twice) return { bad: true, note: t("pg.data.twice", { name: twice }) };
      return {
        ok: true,
        note: t("pg.data.generatedNote", {
          n: count,
          fields: named.length ? plural("pg.data.fields", named.length) : t("pg.data.noVars"),
        }),
      };
    }
    let rows;
    try { rows = JSON.parse(state.cases); } catch { return { bad: true, note: t("pg.data.badJSON") }; }
    if (!Array.isArray(rows)) return { bad: true, note: t("pg.data.notList") };
    if (!rows.length) return { bad: true, note: t("pg.data.emptyList") };
    return { ok: true, note: plural("pg.data.listed", rows.length) };
  }

  // timingStep is the arrival stream in words. Its validity is the server's answer
  // rather than a second opinion about it: the profile call the sparkline is drawn
  // from refuses exactly what the run would refuse, so a stream that cannot be
  // planned is flagged here by the planner itself.
  function timingStep() {
    if (state.profileError) return { bad: true, note: state.profileError };
    const a = ARRIVALS.find((x) => x.mode === state.arrival);
    // The calendar is not in the note. It is fixed for the sandbox's life, so it is
    // not what a readout is watching — and the sparkline underneath shows it better
    // than words do, as the flat stretch where the stream stops overnight. Naming it
    // here only cost the line a wrap, which orphaned the last word of it.
    const param = a && a.param ? ` ${state.arrivalN} ${t(a.param)}` : "";
    return { ok: true, note: `${a ? t(a.label) : state.arrival}${param}` };
  }

  // expectStep counts what the run will be judged on.
  //
  // Asserting nothing is not a fault — it is a run somebody is going to read — so it
  // is stated rather than flagged, and it does not stop the run. A rule with nothing
  // after "then" is a fault: the verdict refuses it, and saying so here beats saying
  // it once the run is over.
  function expectStep() {
    const e = state.expect;
    const rules = (e.rules || []).filter((r) => (r.when || "").trim() || (r.then || "").trim());
    if (rules.some((r) => !(r.then || "").trim())) {
      return { bad: true, note: t("pg.x.ruleEmpty") };
    }
    let checks = 0;
    for (const on of [e.allFinish, e.noIncidents]) if (on) checks++;
    for (const text of [e.p90Hours, e.mustReach]) if (String(text).trim()) checks++;
    for (const bound of Object.values(e.queue)) if (String(bound).trim()) checks++;
    if (!checks && !rules.length) return { note: t("pg.x.nothing") };
    const parts = [];
    if (checks) parts.push(plural("pg.x.checks", checks));
    if (rules.length) parts.push(plural("pg.rules.count", rules.length));
    return { ok: true, note: parts.join(" · ") };
  }

  // setupBlocked reports whether the run has something to run at all. The
  // expectations are not in it: a run with none is the ordinary case.
  function setupBlocked() {
    return !dataStep().ok || !timingStep().ok;
  }

  // batchSetupHTML is what decides the run: the data, when it arrives, what it has
  // to show, and the scenario all three are stored in.
  function batchSetupHTML() {
    const modes = ARRIVALS.map((a) =>
      `<option value="${a.mode}"${a.mode === state.arrival ? " selected" : ""}>${esc(t(a.label))}</option>`).join("");
    const arrival = ARRIVALS.find((a) => a.mode === state.arrival);
    const param = arrival ? arrival.param : null;
    return `
      ${datasetHTML()}
      ${stepHeadHTML(2)}
      <div class="pg-timing">
        <select id="pg-arrival">${modes}</select>
        ${param ? `<input type="number" min="1" step="1" id="pg-arrival-n" value="${esc(state.arrivalN)}" />
                   <span class="muted">${esc(t(param))}</span>` : ""}
      </div>
      <div id="pg-profile">${arrivalProfileHTML()}</div>
      ${expectHTML()}
      ${scenarioSaveHTML()}`;
  }

  // arrivalProfileHTML draws the stream the timing describes, before anything runs.
  //
  // The shape comes from the server because the server plans the run: one piece of
  // code lays the arrivals out either way, so what is on screen is the schedule the
  // cases will actually get rather than a browser's second guess at it. Which is the
  // whole point of drawing it — a Poisson stream of three hundred is bursty in a way
  // no wording of "a stream of 10 per hour" conveys, and the burst is what the pools
  // downstream will feel.
  function arrivalProfileHTML() {
    if (state.profileError) return `<p class="pg-inc">${esc(state.profileError)}</p>`;
    const p = state.profile;
    if (!p) return "";
    if (!p.scheduled) {
      return `<p class="muted">${esc(t("pg.profile.sequential"))}</p>`;
    }
    const b = p.buckets || [];
    if (b.length < 2) return "";
    const W = 300, H = 34, peak = Math.max(1, p.peak);
    // The line stops a hair short of the top edge: drawn flush against the frame the
    // fullest slice reads as a border rather than as the measurement it is.
    const x = (i) => (i * W) / (b.length - 1);
    const y = (v) => H - (v / peak) * (H - 2);
    const points = b.map((v, i) => `${x(i).toFixed(2)},${y(v).toFixed(2)}`).join(" ");
    const startMs = Date.parse(p.start);
    const slice = (p.spanMillis || 0) / b.length;
    const bw = W / (b.length - 1);
    // A band per slice carrying its own title: the tooltip is the browser's, which on
    // a strip this size beats a crosshair widget nobody asked the panel to grow.
    const hits = b.map((v, i) => {
      const at = fmtWhen(`${new Date(startMs + i * slice).toISOString().slice(0, 19)}Z`);
      return `<rect class="pg-spark-hit" x="${Math.max(0, x(i) - bw / 2).toFixed(2)}" y="0"
        width="${bw.toFixed(2)}" height="${H}"><title>${esc(plural("pg.spark.slice", v, { at }))}</title></rect>`;
    }).join("");
    return `
      <svg class="pg-spark" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" role="img"
        aria-label="${esc(t("pg.spark.label", { n: p.cases, span: fmtDur(p.spanMillis), peak }))}">
        <polygon class="pg-spark-fill" points="0,${H} ${points} ${W},${H}"/>
        <polyline class="pg-spark-line" points="${points}"/>
        <line class="pg-base" x1="0" y1="${H}" x2="${W}" y2="${H}"/>
        ${hits}
      </svg>
      <div class="pg-axis"><span>${esc(fmtWhen(p.start))}</span><span>${esc(fmtWhen(p.end))}</span></div>
      <p class="muted pg-spark-note">${esc(plural("pg.spark.cases", p.cases))} ${
        esc(p.spanMillis ? t("pg.spark.over", { span: fmtDur(p.spanMillis), peak }) : t("pg.spark.atOnce"))}</p>`;
  }

  // batchAnalysisHTML is what the run did — read while it runs and after it stops.
  function batchAnalysisHTML() {
    if (!state.run) {
      return `<p class="muted">${t("pg.analysis.nothing")}</p>`;
    }
    return `
      ${inspectedCaseHTML()}
      ${runStatusHTML()}
      ${verdictHTML()}
      ${comparisonHTML()}
      ${reportHTML()}`;
  }

  // inspectedCaseHTML is the case a reader clicked, above the report rather than
  // instead of it: they came here from the run, and going back to it should not cost
  // them the numbers they were reading.
  function inspectedCaseHTML() {
    if (!state.inspect) return "";
    return `
      <div class="pg-sec"><b>${esc(t("pg.inspect.case", { n: state.inspect.index + 1 }))}</b>
        <span class="muted">${esc(t("pg.inspect.of", { total: state.results ? state.results.total : 0 }))}</span></div>
      ${resultHTML(state.inspected)}`;
  }

  function runStatusHTML() {
    const r = state.run;
    if (!r) return "";
    const done = r.state !== "running";
    const pct = r.cases ? Math.round(100 * (r.completed || 0) / r.cases) : 0;
    return `
      <div class="pg-sec"><b>${esc(t("pg.run.heading"))}</b> <span class="muted">${esc(r.state)}</span></div>
      <div class="pg-progress"><div class="pg-progress-fill" style="width:${done ? 100 : pct}%"></div></div>
      <div class="pg-run-line">
        <span>${esc(t("pg.run.finished", { done: r.completed || 0, cases: r.cases || 0 }))}</span>
        ${r.simTime ? `<span class="muted">${esc(t("pg.run.simulated", { at: fmtWhen(r.simTime) }))}</span>` : ""}
      </div>
      ${r.error ? `<p class="pg-inc">${esc(r.error)}</p>` : ""}`;
  }

  // coldPaths is every element and sequence flow the data never reached. It is the
  // half of a heat map that a coverage question asks, and it reads better as a
  // list than as a colour somebody has to hunt for on the canvas.
  function coldPaths() {
    if (!state.heat) return [];
    return [
      ...(state.heat.elements || []).filter((e) => !e.count).map((e) => e.id),
      ...(state.heat.flows || []).filter((f) => !f.count).map((f) => `${f.from} → ${f.to}`),
    ];
  }

  // trackHTML is the gauge: a track filled to a value's share of the scale its
  // column is read against.
  //
  // The empty part is drawn too, and that is the whole of why this is a function
  // rather than a styled bar. A fill with no track behind it underlines its own
  // number when it is full and reads as a stray mark when it is small; with the
  // track, one glance down a column says which row is the big one.
  function trackHTML(value, max) {
    const pct = max > 0 ? Math.max(0, Math.min(100, (value / max) * 100)) : 0;
    return `<span class="pg-track"><i style="width:${pct.toFixed(1)}%"></i></span>`;
  }

  // meterHTML is a table cell whose number carries its own magnitude under it.
  //
  // Both, rather than either. The gauge answers the question a column of formatted
  // durations is slowest at — which row is the big one — and the number beside it
  // keeps the fact exact, so nothing here is read off a length alone.
  function meterHTML(value, max, text, title) {
    return `<td class="pg-meter"${title ? ` title="${esc(title)}"` : ""}>${esc(text)}
      ${trackHTML(value, max)}</td>`;
  }

  // factBarHTML is one of the duration tiles with its own share of the slowest case
  // under it. The four sit on one axis, so four lengths against one scale say what
  // four numbers cannot: whether the median is near the fastest and the p90 out on
  // its own, or the whole run is bunched together.
  function factBarHTML(value, max, text, label) {
    return `<div><b>${esc(text)}</b><span>${esc(label)}</span>${trackHTML(value, max)}</div>`;
  }

  // outcomesHTML is where the cases came out, one row per end event.
  //
  // It is the question a run is actually asked — how many were approved, how many
  // were rejected — and the one "482 of 500 finished" cannot answer. The counts are
  // the run's own token counts, folded over every case rather than over the page of
  // results on screen, and the names come off the canvas, so the rows read like the
  // diagram somebody drew. An end event nothing reached keeps its row at zero: a
  // branch the data never took is the finding, and a missing row would hide it.
  //
  // A diagram with a single end event gets no table. Its one row would say what the
  // line above it already said, and a lone bar at a hundred percent is not a
  // comparison.
  function outcomesHTML(rep) {
    const ends = endEventsInDiagram();
    if (ends.length < 2) return "";
    const visits = rep.visits || {};
    const rows = ends.map((e) => ({ ...e, count: Number(visits[e.id]) || 0 }))
      .sort((a, b) => b.count - a.count);
    const max = Math.max(0, ...rows.map((r) => r.count));
    // The total names the share column's denominator, and says how many cases never
    // came out anywhere. It can also exceed the case count, because these are token
    // counts and a case with a parallel branch ends twice — which is worth showing
    // rather than hiding, since it is the thing somebody would otherwise misread the
    // percentages by.
    const total = rows.reduce((n, r) => n + r.count, 0);
    return `
      <div class="pg-sec"><b>${esc(t("pg.ends"))}</b>
        <span class="muted">${esc(t("pg.ends.reached", { n: total, cases: rep.cases }))}</span></div>
      <table class="pg-table pg-ends"><thead><tr><th>${esc(t("pg.ends.outcome"))}</th><th>${esc(t("pg.ends.reachedCol"))}</th><th>${esc(t("pg.ends.share"))}</th></tr></thead>
      <tbody>${rows.map((r) => `<tr${r.count ? "" : ' class="pg-unreached"'}>
        <td title="${esc(r.id)}">${esc(r.name)}</td>
        ${meterHTML(r.count, max, String(r.count), t("pg.ends.meter"))}
        <td>${total ? Math.round((100 * r.count) / total) : 0}%</td>
      </tr>`).join("")}</tbody></table>`;
  }

  // reportHTML is the analysis: what came out, how long it took, where it waited,
  // and when. Every number here is a fold of the whole run rather than a sample.
  function reportHTML() {
    const rep = state.report;
    if (!rep) return "";
    const d = rep.duration || {};
    const bottlenecks = Object.entries(rep.elements || {})
      .map(([id, e]) => ({ id, ...e }))
      // Ranked by total waiting: elapsed time that is all queue is a capacity
      // problem, and it is the one a pool size can fix. Work time sits beside it so
      // a task that is simply slow is not mistaken for a bottleneck.
      .sort((a, b) => (b.waitMillis - a.waitMillis) || (b.workMillis - a.workMillis))
      .slice(0, 8);
    const pools = Object.entries(rep.pools || {});
    // A column of bars is scaled to its own largest value, so a length is read
    // against the rows beside it and never against a run that is not on screen.
    // Utilisation is the exception: it is scaled to a full hundred, because the
    // question there is how full a pool was rather than which of them was fullest,
    // and a bar that filled at the busiest would read as saturated at forty percent.
    const maxWait = Math.max(0, ...bottlenecks.map((b) => b.waitMillis || 0));
    const cold = coldPaths();
    return `
      <div class="pg-sec"><b>${esc(t("pg.rep.outcomes"))}</b></div>
      <div class="pg-facts">
        <div><b>${rep.completed}</b><span>${esc(t("pg.rep.finished", { cases: rep.cases }))}</span></div>
        <div><b class="${rep.incidents ? "bad" : ""}">${rep.incidents}</b><span>${esc(t("pg.rep.incidents"))}</span></div>
        <div><b>${rep.maxInFlight}</b><span>${esc(t("pg.rep.peak"))}</span></div>
      </div>
      ${outcomesHTML(rep)}
      <div class="pg-sec"><b>${esc(t("pg.rep.durations"))}</b> <span class="muted">${esc(t("pg.rep.perCase"))}</span></div>
      <div class="pg-facts">
        ${factBarHTML(d.minMillis, d.maxMillis, fmtDur(d.minMillis), t("pg.rep.fastest"))}
        ${factBarHTML(d.p50Millis, d.maxMillis, fmtDur(d.p50Millis), t("pg.rep.median"))}
        ${factBarHTML(d.p90Millis, d.maxMillis, fmtDur(d.p90Millis), t("pg.rep.p90"))}
        ${factBarHTML(d.maxMillis, d.maxMillis, fmtDur(d.maxMillis), t("pg.rep.slowest"))}
      </div>
      ${bottlenecks.length ? `
        <div class="pg-sec"><b>${esc(t("pg.rep.bottlenecks"))}</b> <span class="muted">${esc(t("pg.rep.byWaiting"))}</span></div>
        <table class="pg-table pg-bottlenecks"><thead><tr><th>${esc(t("pg.rep.element"))}</th><th>${esc(t("pg.rep.runs"))}</th><th>${esc(t("pg.rep.waiting"))}</th><th>${esc(t("pg.rep.longest"))}</th><th>${esc(t("pg.rep.work"))}</th></tr></thead>
        <tbody>${bottlenecks.map((b) => `<tr>
          <td class="mono">${esc(b.id)}</td><td>${b.runs}</td>
          ${meterHTML(b.waitMillis, maxWait, fmtDur(b.waitMillis), t("pg.rep.waitMeter"))}
          <td>${esc(fmtDur(b.maxWaitMillis))}</td>
          <td>${esc(fmtDur(b.workMillis))}</td></tr>`).join("")}</tbody></table>` : ""}
      ${pools.length ? `
        <div class="pg-sec"><b>${esc(t("pg.rep.pools"))}</b></div>
        <table class="pg-table pg-pools"><thead><tr><th>${esc(t("pg.rep.pool"))}</th><th>${esc(t("pg.rep.seats"))}</th><th>${esc(t("pg.rep.used"))}</th><th>${esc(t("pg.rep.served"))}</th><th>${esc(t("pg.rep.longestQueue"))}</th></tr></thead>
        <tbody>${pools.map(([name, p]) => `<tr>
          <td class="mono">${esc(name)}</td><td>${p.capacity}</td>
          ${meterHTML(p.utilisationPercent, 100, `${p.utilisationPercent}%`, t("pg.rep.useMeter"))}
          <td>${p.served}</td><td>${p.maxQueue}</td></tr>`).join("")}</tbody></table>` : ""}
      ${timelineHTML(rep.timeline)}
      ${state.heat ? `
        <div class="pg-sec"><b>${esc(t("pg.rep.coverage"))}</b>
          <span class="muted">${esc(cold.length ? t("pg.rep.neverReached", { n: cold.length }) : t("pg.rep.allUsed"))}</span></div>
        ${cold.length
          ? `<div class="pg-cold" id="pg-cold">${cold.map((c) => `<span class="mono">${esc(c)}</span>`).join("")}</div>`
          : `<p class="muted">${esc(t("pg.rep.allCovered"))}</p>`}` : ""}
`;
  }

  // resultsStripHTML is the run's own cases, a page at a time. A total says what a
  // run cost and the heat map says where; only the rows say which case it was — the
  // one that took eleven hours, the four that never finished.
  function resultsStripHTML() {
    // Read defensively: this renders whatever came back, and a page that is missing
    // its rows must show an empty table rather than take the panel down with it.
    const r = state.results || {};
    const rows = Array.isArray(r.rows) ? r.rows : [];
    const offset = r.offset || 0;
    const total = r.total || 0;
    const shown = rows.length;
    const names = variableColumns(rows);
    const broke = violatingCases();
    // The bars are scaled to the slowest case on this page rather than to the run's
    // own slowest. A page is what a reader is comparing — which of these fifty took
    // the longest — and a column scaled to a case on page nine would draw all fifty
    // of them as short.
    const slowest = Math.max(0, ...rows.map((r) => (r.ended ? r.durationMillis || 0 : 0)));
    const flagged = deviations();
    return `
      <div class="pg-results-head">
        <b>${esc(t("pg.res.heading"))}</b>
        <span class="muted">${esc(shown ? t("pg.res.range", { from: offset + 1, to: offset + shown, total }) : t("pg.res.none"))}</span>
        <span class="pg-dev${flagged.length ? " bad" : ""}">${
          flagged.length ? `&#10007; ${esc(flagged.join(" \u00b7 "))}` : esc(t("pg.res.clean"))}</span>
        <span style="flex:1"></span>
        <button class="btn neutral small" id="pg-page-prev"${offset > 0 ? "" : " disabled"}>${esc(t("pg.res.prev"))}</button>
        <button class="btn neutral small" id="pg-page-next"${offset + shown < total ? "" : " disabled"}>${esc(t("pg.res.next"))}</button>
        <button class="btn neutral small" id="pg-csv-out">&#8615; CSV</button>
      </div>
      ${shown ? `
        <div class="pg-results-scroll">
          <table class="pg-table pg-cases">
            <thead><tr><th>${esc(t("pg.res.case"))}</th><th>${esc(t("pg.res.outcome"))}</th><th>${esc(t("pg.res.duration"))}</th><th>${esc(t("pg.res.incidents"))}</th>
              ${names.map((n) => `<th>${esc(n)}</th>`).join("")}</tr></thead>
            <tbody>${rows.map((row) => `<tr data-case="${esc(row.index)}" title="${esc(t("pg.res.showCase"))}"
              class="${row.incidents || broke.has(row.index) ? "pg-bad " : ""}${
                state.inspect && state.inspect.index === row.index ? "pg-open" : ""}">
              <td>${row.index + 1}${broke.has(row.index) ? ` <span title="${esc(t("pg.res.brokeRule"))}">&#10007;</span>` : ""}</td>
              <td class="mono">${esc(row.end || (row.state === "completed" ? "" : row.state))}</td>
              ${row.ended
                ? meterHTML(row.durationMillis, slowest, fmtDur(row.durationMillis), t("pg.res.meter"))
                : `<td>\u2014</td>`}
              <td>${row.incidents || ""}</td>
              ${names.map((n) => `<td class="mono">${esc((row.variables || {})[n] == null ? "" : row.variables[n])}</td>`).join("")}
            </tr>`).join("")}</tbody>
          </table>
        </div>` : `<p class="muted">${esc(t("pg.res.empty"))}</p>`}`;
  }

  // variableColumns picks the columns the table shows, from the page in hand. A CSV
  // has one header and so has this: choosing it from the rows on screen beats reading
  // the whole table twice to find a name only the last page carries.
  function variableColumns(rows) {
    const seen = [];
    for (const row of rows || []) {
      for (const name of Object.keys(row.variables || {})) {
        if (!seen.includes(name)) seen.push(name);
      }
    }
    return seen.sort();
  }

  // timelineHTML draws the run over simulated time: bars for what arrived and what
  // finished in each slice, and a line for what was still in flight. A total says
  // what a run cost; this says when — that the queue built up on Tuesday morning
  // and drained by Thursday is not something a mean and a p90 can tell anybody.
  function timelineHTML(tl) {
    const b = (tl && tl.buckets) || [];
    if (b.length < 2) return "";
    const W = 300, H = 56, gap = 0.5;
    const bw = W / b.length;
    const maxBar = Math.max(1, ...b.map((x) => Math.max(x.started, x.completed)));
    const maxFlight = Math.max(1, ...b.map((x) => x.inFlight));
    // A slice holding one case out of a thousand would otherwise draw a bar too
    // short to see, and a chart that renders "one" and "none" alike is worse than
    // no chart: the reader cannot tell an empty stretch from a quiet one.
    const bar = (v) => (v ? Math.max(1.5, (v / maxBar) * H) : 0);
    const bars = b.map((x, i) => {
      const sh = bar(x.started), ch = bar(x.completed);
      return `<rect x="${(i * bw + gap).toFixed(2)}" y="${(H - sh).toFixed(2)}" width="${(bw / 2 - gap).toFixed(2)}" height="${sh.toFixed(2)}" class="pg-bar-in"/>` +
        `<rect x="${(i * bw + bw / 2).toFixed(2)}" y="${(H - ch).toFixed(2)}" width="${(bw / 2 - gap).toFixed(2)}" height="${ch.toFixed(2)}" class="pg-bar-out"/>`;
    }).join("");
    // The line stops short of the top edge: at full height a saturated run draws it
    // flush against the frame, where it reads as a border rather than as the
    // measurement it is.
    const line = b.map((x, i) =>
      `${(i * bw + bw / 2).toFixed(2)},${(H - 3 - (x.inFlight / maxFlight) * (H - 3)).toFixed(2)}`).join(" ");
    return `
      <div class="pg-sec"><b>${esc(t("pg.time.heading"))}</b>
        <span class="muted">${esc(t("pg.time.slice", { width: fmtDur(tl.widthMillis || 0) }))}</span></div>
      <svg class="pg-chart" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" role="img"
        aria-label="${esc(t("pg.time.label"))}">
        ${bars}<polyline class="pg-line" points="${line}"/>
        <line class="pg-base" x1="0" y1="${H}" x2="${W}" y2="${H}"/>
      </svg>
      <div class="pg-legend">
        <span><i class="pg-key-in"></i>${esc(t("pg.time.arrived"))}</span>
        <span><i class="pg-key-out"></i>${esc(t("pg.time.finished"))}</span>
        <span><i class="pg-key-flight"></i>${esc(t("pg.time.inFlight", { peak: maxFlight }))}</span>
      </div>
      <div class="pg-axis"><span>${esc(fmtWhen(b[0].at))}</span><span>${esc(fmtWhen(b[b.length - 1].at))}</span></div>`;
  }

  function modeTabsHTML() {
    const label = { step: esc(t("pg.mode.step")), batch: esc(t("pg.mode.batch")) };
    return `<div class="pg-modes">${["step", "batch"].map((m) =>
      `<button data-mode="${m}"${state.mode === m ? ' class="active"' : ""}>${label[m]}</button>`).join("")}</div>`;
  }

  function render() {
    const open = !!state.session;
    const running = !!(state.run && state.run.state === "running");
    const show = (id, on) => {
      const b = el(id);
      if (b) { b.hidden = !on; b.disabled = state.busy; }
    };
    const stepping = open && state.mode === "step";
    const batching = open && state.mode === "batch";
    for (const id of ["pg-case", "pg-step", "pg-run", "pg-clock"]) show(id, stepping);
    show("pg-batch", batching && !running);
    show("pg-cancel", batching && running);
    show("pg-stop", open);
    show("pg-start", !open);
    // The policy is fixed for the life of a sandbox, so the selects go away once
    // one is open rather than sitting there implying otherwise.
    el("pg-dur-wrap").hidden = open;
    el("pg-human-wrap").hidden = open;

    el("pg-hint").textContent = t(!open
      ? "pg.hint.closed"
      : batching
        ? running ? "pg.hint.running"
          : state.inspect ? "pg.hint.inspect"
            : state.report ? "pg.hint.report"
              : "pg.hint.setup"
        : state.tasks.length
          ? "pg.hint.task"
          : "pg.hint.step");
    el("pg-stats").textContent = open
      ? t("pg.stats", { at: fmtWhen(state.simTime), tasks: state.session.stubLabel, seed: state.session.seed })
      : "";

    // The results strip is there only when there are results to put in it: an empty
    // band under the diagram is a promise the panel has not kept.
    results.hidden = !(batching && state.report);
    // The switcher is over the diagram only once there is a run to shade it by.
    overlayBar.hidden = !(batching && !!state.heat);
    el("pg-setup-body").innerHTML = setupColumnHTML();
    el("pg-body").innerHTML = analysisColumnHTML();
    if (!results.hidden) el("pg-results-body").innerHTML = resultsStripHTML();
    if (!overlayBar.hidden) el("pg-overlay-body").innerHTML = overlayStripHTML();
    // A setup that cannot run says so before the button is pressed rather than after.
    gateRun();
    // The sparkline is fetched off the render rather than off each box that feeds
    // it. It is a no-op unless the timing actually changed, and it re-renders once
    // the answer lands.
    refreshProfile();
  }

  // overlayStripHTML is the switcher and its legend: which of the run's four
  // measures shades the diagram, and what the darkest shade is worth.
  //
  // A legend rather than a colour alone, because a shade only means something
  // against a scale — "dark" says nothing until it says "dark is 322 cases".
  function overlayStripHTML() {
    // While a case is open the canvas shows that case, not the run, so the measures
    // are not offered: a button that says "Waiting" over a diagram drawing one case's
    // path would be naming something that is not on screen.
    if (state.inspect) {
      const c = state.inspected || {};
      const steps = (c.path || []).length;
      return `
        <span class="muted">${esc(t("pg.ov.showing"))}</span>
        <b>${esc(t("pg.ov.case", { n: state.inspect.index + 1 }))}</b>
        <span class="muted">${esc(c.state || "")}${
          state.inspect.end ? esc(t("pg.ov.at", { end: state.inspect.end })) : ""} · ${esc(plural("pg.ov.steps", steps))}${
          state.inspect.durationMillis ? ` · ${esc(fmtDur(state.inspect.durationMillis))}` : ""}</span>
        <span style="flex:1"></span>
        <button id="pg-case-close">${esc(t("pg.ov.back"))}</button>`;
    }
    const key = state.overlay;
    const tab = (k, label, title) =>
      `<button data-overlay="${k}"${k === key ? ' class="active"' : ""} title="${esc(t(title))}">${esc(t(label))}</button>`;
    const spec = OVERLAYS.find((o) => o.key === key);
    const steps = Array.from({ length: HEAT_LEVELS }, (_, i) =>
      `<i class="pg-heat-key-${i + 1}"></i>`).join("");
    return `
      <span class="muted">${esc(t("pg.ov.overlay"))}</span>
      ${tab("off", "pg.ov.off", "pg.ov.offTitle")}
      ${OVERLAYS.map((o) => tab(o.key, o.label, o.title)).join("")}
      <span style="flex:1"></span>
      ${spec ? `
        <span class="pg-scale" title="${esc(t(spec.title))}">
          <span class="muted">0</span>${steps}<span class="muted">${esc(overlayFormat(key, measure(key).max))}</span>
        </span>
        ${spec.flows ? "" : `<span class="muted">${esc(t("pg.ov.shapesOnly"))}</span>`}` : ""}`;
  }

  // setupColumnHTML is the left column. Before a sandbox exists it is the whole
  // preflight — the policy is fixed for a sandbox's life, so it is decided here or
  // not at all.
  function setupColumnHTML() {
    if (!state.session) return setupHTML();
    return modeTabsHTML() + (state.mode === "batch" ? batchSetupHTML() : stepSetupHTML());
  }

  function stepSetupHTML() {
    return `
      <label class="field"><span>${esc(t("pg.step.startVars"))}</span>
        <textarea id="pg-startvars" rows="3" spellcheck="false">${esc(state.startVars)}</textarea></label>
      <div class="pg-sec"><b>${esc(t("pg.step.waiting"))}</b> <span class="muted">${state.tasks.length}</span></div>
      ${state.tasks.length ? state.tasks.map(taskRowHTML).join("") : `<p class="muted">${esc(t("pg.step.nothingWaiting"))}</p>`}
      <label class="field"><span>${esc(t("pg.step.outputs"))}</span>
        <textarea id="pg-outputs" rows="2" spellcheck="false">${esc(state.outputs)}</textarea></label>`;
  }

  // analysisColumnHTML is the right column: what came back, in both modes.
  function analysisColumnHTML() {
    if (!state.session) {
      return `<p class="muted">${esc(t("pg.analysis.closed"))}</p>`;
    }
    if (state.mode === "batch") return batchAnalysisHTML();
    return `<div class="pg-sec"><b>${esc(t("pg.analysis.case"))}</b></div>${resultHTML(state.result)}`;
  }

  // ---- events ---------------------------------------------------------------

  bar.addEventListener("click", (e) => {
    const btn = e.target.closest("button");
    if (!btn) return;
    switch (btn.id) {
      case "pg-start": guard(t("pg.act.startSandbox"), start); break;
      case "pg-case": guard(t("pg.act.startCase"), startCase); break;
      case "pg-step": guard(t("pg.act.step"), step); break;
      case "pg-run": guard(t("pg.act.run"), run); break;
      case "pg-clock": guard(t("pg.act.advance"), advance); break;
      case "pg-batch": guard(t("pg.act.batch"), startBatch); break;
      case "pg-cancel": guard(t("pg.act.cancelBatch"), cancelBatch); break;
      case "pg-stop": guard(t("pg.act.discard"), stop); break;
    }
  });

  const onPaneClick = (e) => {
    // Closing the panel means leaving the mode, so it goes through the tab that
    // owns it — otherwise the tab bar would still say Playground with no panel.
    if (e.target.closest("#pg-panel-close")) {
      root.querySelector('.etabs button[data-tab="design"]')?.click();
      return;
    }
    const mode = e.target.closest("button[data-mode]");
    if (mode) {
      state.mode = mode.dataset.mode;
      // Each mode paints the canvas its own way, and the heat map belongs to the
      // batch: stepping through a case while the diagram is shaded by a previous
      // run would be two answers on one picture.
      closeCase();
      if (state.mode !== "batch") state.overlay = "off";
      else if (state.overlay === "off" && state.heat) state.overlay = "runs";
      drawCanvas();
      render();
      return;
    }
    const pick = e.target.closest("button[data-overlay]");
    if (pick) {
      state.overlay = pick.dataset.overlay;
      drawCanvas();
      render();
      return;
    }
    const source = e.target.closest("button[data-source]");
    if (source) {
      state.source = source.dataset.source;
      render();
      return;
    }
    if (e.target.closest("#pg-csv-clear")) {
      state.csv = null;
      render();
      return;
    }
    if (e.target.closest("#pg-gen-add")) {
      state.gen.fields.push({ name: "", kind: "int", min: 0, max: 100 });
      render();
      return;
    }
    const del = e.target.closest("[data-gen-del]");
    if (del) {
      state.gen.fields.splice(Number(del.dataset.genDel), 1);
      state.genPreview = null;
      render();
      return;
    }
    if (e.target.closest("#pg-rule-add")) {
      state.expect.rules.push({ name: "", when: "", then: "" });
      render();
      return;
    }
    const ruleDel = e.target.closest("[data-rule-del]");
    if (ruleDel) {
      state.expect.rules.splice(Number(ruleDel.dataset.ruleDel), 1);
      render();
      return;
    }
    if (e.target.closest("#pg-gen-preview")) {
      guard(t("pg.act.preview"), previewDataset);
      return;
    }
    if (e.target.closest("#pg-scenario-open")) {
      const id = el("pg-scenario-pick")?.value;
      if (id) guard(t("pg.act.openScenario"), () => openScenario(id));
      return;
    }
    if (e.target.closest("#pg-scenario-save")) {
      guard(t("pg.act.saveScenario"), saveScenario);
      return;
    }
    if (e.target.closest("#pg-scenario-delete")) {
      guard(t("pg.act.deleteScenario"), deleteScenario);
      return;
    }
    if (e.target.closest("#pg-keep-baseline")) {
      guard(t("pg.act.keepBaseline"), keepBaseline);
      return;
    }
    if (e.target.closest("#pg-case-close")) {
      closeCase();
      render();
      return;
    }
    const caseRow = e.target.closest("tr[data-case]");
    if (caseRow) {
      const row = ((state.results && state.results.rows) || [])
        .find((r) => r.index === Number(caseRow.dataset.case));
      if (row) guard(t("pg.act.showCase"), () => openCase(row));
      return;
    }
    const page = e.target.closest("#pg-page-prev, #pg-page-next");
    if (page) {
      const at = state.results ? state.results.offset : 0;
      const to = page.id === "pg-page-prev" ? at - resultsPageSize : at + resultsPageSize;
      guard(t("pg.act.readResults"), () => readResults(to));
      return;
    }
    if (e.target.closest("#pg-csv-out")) {
      // A plain same-origin navigation, so the session cookie authenticates it,
      // exactly as the Console's downloads do — and the file is streamed rather
      // than assembled in the browser, which a fifty-thousand-row result needs.
      window.location.href = path("/results.csv");
      return;
    }
    const btn = e.target.closest("button[data-job]");
    if (btn) guard(t("pg.act.complete"), () => complete(btn.dataset.job));
  };

  // Keep what is typed in state, without re-rendering: a render would move the
  // caret to the end of whatever box has focus.
  const onPaneInput = (e) => {
    const t = e.target;
    if (t.id === "pg-startvars") state.startVars = t.value;
    if (t.id === "pg-outputs") state.outputs = t.value;
    if (t.id === "pg-cases") state.cases = t.value;
    if (t.id === "pg-arrival-n") state.arrivalN = t.value;
    if (t.id === "pg-x-p90") state.expect.p90Hours = t.value;
    if (t.id === "pg-x-reach") state.expect.mustReach = t.value;
    if (t.id === "pg-scenario-name") state.scenarioName = t.value;
    if (t.id === "pg-gen-count") state.gen.count = t.value;
    // Three of these boxes decide the arrival sparkline: how many cases there are
    // and how fast they come. They redraw it on their own rather than through a
    // render, which would take the caret out of the box being typed in.
    if (t.id === "pg-arrival-n" || t.id === "pg-gen-count" || t.id === "pg-cases") profileSoon();
    if (t.dataset.rule) state.expect.rules[Number(t.dataset.i)][t.dataset.rule] = t.value;
    // A generated field's boxes are kept without re-rendering, like every other
    // box here: a render would move the caret to the end of the one being typed in.
    if (t.dataset.gen && t.dataset.gen !== "kind") {
      state.gen.fields[Number(t.dataset.i)][t.dataset.gen] = t.value;
    }
    if (t.dataset.queue != null) state.expect.queue[t.dataset.queue] = t.value;
    if (t.dataset.pool != null) {
      state.pools[t.dataset.pool] = { ...state.pools[t.dataset.pool], pool: t.value };
    }
    if (t.dataset.seats != null) {
      state.pools[t.dataset.seats] = { ...state.pools[t.dataset.seats], seats: t.value };
    }
    // The heads read what was just typed. They are repainted rather than re-rendered
    // for the reason nothing else here re-renders on input: the caret.
    drawSteps();
  };

  const onPaneChange = (e) => {
    const t = e.target;
    if (t.id === "pg-hours") state.hours = t.checked;
    if (t.id === "pg-x-finish") state.expect.allFinish = t.checked;
    if (t.id === "pg-x-inc") state.expect.noIncidents = t.checked;
    if (t.id === "pg-scenario-pick") state.pickedScenario = t.value;
    if (t.id === "pg-arrival") { state.arrival = t.value; render(); }

    if (t.id === "pg-csv" && t.files && t.files[0]) { state.csv = t.files[0]; render(); }
    if (t.dataset.gen === "kind") {
      // The kind decides which parameters the row shows, so this one does redraw.
      state.gen.fields[Number(t.dataset.i)].kind = t.value;
      state.genPreview = null;
      render();
    }
    if (t.dataset.gen === "onlyDate") state.gen.fields[Number(t.dataset.i)].onlyDate = t.checked;
    if (t.dataset.ruleEnd != null && t.value) {
      // The picker writes the assertion rather than being it: an author starts from
      // the outcome they drew and is left with an expression they can edit into
      // anything else FEEL can say.
      state.expect.rules[Number(t.dataset.ruleEnd)].then = `end = ${JSON.stringify(t.value)}`;
      render();
    }
    drawSteps();
  };

  // One panel in three places: the same handlers, bound to each of them.
  for (const pane of panes) {
    pane.addEventListener("click", onPaneClick);
    pane.addEventListener("input", onPaneInput);
    pane.addEventListener("change", onPaneChange);
  }

  // A sandbox runs the diagram as it was when it started. Editing after that means
  // the canvas and the run no longer agree, so say so rather than quietly lying.
  const onEdit = () => {
    if (!state.session) return;
    el("pg-hint").textContent = t("pg.changed");
  };
  modeler.on("elements.changed", onEdit);

  // An import replaces the canvas whole: an applied XML source, an auto-layout, a
  // collaborator's draft pulled in by the live session, or the editor putting the
  // diagram back after one of those was refused. None of them fires elements.changed,
  // and every one takes the run's markers off the shapes it replaces. So the run is
  // painted again onto the new shapes, and the hint is given when what is on the
  // canvas is no longer what the sandbox runs — which a diagram put back is not.
  let shown = false; // the Playground tab is the active one (setActive)
  const onImport = async (e) => {
    if (e && e.error) return; // a refused import; the one that follows puts it back
    if (shown) drawCanvas();
    const session = state.session;
    if (!session) return;
    let now = "";
    try { ({ xml: now } = await modeler.saveXML({ format: true })); } catch { /* cannot tell: say so */ }
    if (state.session !== session) return; // the sandbox went away while asking
    if (!now || now !== session.xml) onEdit();
  };
  modeler.on("import.done", onImport);

  function setActive(on) {
    shown = on;
    bar.hidden = !on;
    panel.hidden = !on;
    setupPanel.hidden = !on;
    if (!on) {
      results.hidden = true;
      overlayBar.hidden = true;
    }
    editor.classList.toggle("pg-active", on);
    if (on) {
      render();
      drawCanvas();
      // The saved scenarios of the diagram on screen, read when the tab is opened
      // rather than when the editor mounts: most visits to a diagram never come
      // here, and a listing nobody looks at is a request nobody needed.
      loadScenarios().then(render).catch(() => { /* a listing that fails leaves the panel usable */ });
    } else clearCanvas();
  }

  render();
  return {
    setActive,
    // destroy releases the server-side sandbox when the editor goes away. Without
    // it the session would sit there until its TTL, holding an engine.
    destroy() {
      stopPolling();
      modeler.off?.("elements.changed", onEdit);
      modeler.off?.("import.done", onImport);
      if (state.session) {
        const id = state.session.id;
        state.session = null;
        // Best effort on teardown: the page may be unloading.
        api("DELETE", `/api/v1/playground/sessions/${encodeURIComponent(id)}`).catch(() => {});
      }
      bar.remove();
      setupPanel.remove();
      overlayBar.remove();
      results.remove();
      panel.remove();
    },
  };
}
