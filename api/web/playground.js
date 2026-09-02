// Playground tab: run the diagram on the real engine, in a throwaway sandbox.
//
// The Design view's token simulation (ADR-0078) is engine-free on purpose — it
// answers "where can a token go". This answers "what does the process do with
// this data": the server compiles the diagram on screen and executes it on the
// real processor over a virtual clock, so FEEL, gateways, DMN and timers behave
// exactly as they will in production. Nothing is deployed, nothing durable is
// written, and the sandbox holds no connectors, so no task can reach the network.
//
// The tab has two ways to drive that sandbox, and they share it:
//   Step  — one case, one occurrence at a time, with the person at the keyboard
//           answering the human tasks. "What does this do?"
//   Batch — a dataset spread over simulated time, then a report: outcomes,
//           durations, a bottleneck ranking, the run over time, and a heat map
//           of what the data used and what it never reached. "Does this hold up?"
//
// See ADR-0215.

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// STUB_DURATIONS are the answering speeds offered for every job the diagram
// creates. "Instant" is for walking the control flow; the longer ones make the
// simulated clock move so timers and deadlines become visible.
const STUB_DURATIONS = [
  { label: "instant", ms: 0 },
  { label: "1 min", ms: 60_000 },
  { label: "1 h", ms: 3_600_000 },
  { label: "4 h", ms: 14_400_000 },
];

// HUMAN_DURATIONS are the same for user tasks, plus the one answer a stub cannot
// give: leave it parked for the person at the keyboard. That is what Step mode
// wants and what a batch cannot use, which is why the choice is offered rather
// than assumed.
const HUMAN_DURATIONS = [{ label: "leave for me", ms: -1 }, ...STUB_DURATIONS];

// ARRIVALS is how a dataset is spread over simulated time. The parameter each
// mode needs is asked for only when that mode is chosen.
const ARRIVALS = [
  { mode: "allAtOnce", label: "all at once", param: null },
  { mode: "sequential", label: "one after another", param: null },
  { mode: "every", label: "one every", param: "minutes" },
  { mode: "poisson", label: "a stream of", param: "per hour" },
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

// FIELD_KINDS is what a generated field can be. The labels say what the field
// produces rather than naming a type: somebody describing a dataset is thinking
// about orders and amounts, not about int64.
const FIELD_KINDS = [
  { kind: "int", label: "whole number" },
  { kind: "decimal", label: "decimal" },
  { kind: "bool", label: "true or false" },
  { kind: "choice", label: "one of a list" },
  { kind: "constant", label: "the same value" },
  { kind: "sequence", label: "case number" },
  { kind: "timestamp", label: "a date" },
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
    .map((d, i) => `<option value="${d.ms}"${i === 1 ? " selected" : ""}>${esc(d.label)}</option>`)
    .join("");
  const human = HUMAN_DURATIONS
    .map((d, i) => `<option value="${d.ms}"${i === 0 ? " selected" : ""}>${esc(d.label)}</option>`)
    .join("");
  return `
    <button class="btn play" id="pg-start" title="Compile the diagram on screen and open a sandbox for it">&#9654; Start sandbox</button>
    <button class="btn neutral" id="pg-case" title="Start one case with the start variables below" hidden>+ Case</button>
    <button class="btn neutral" id="pg-step" title="Carry out exactly one thing: a job answered, or a timer fired" hidden>Step</button>
    <button class="btn neutral" id="pg-run" title="Run until the case comes to rest" hidden>Run</button>
    <button class="btn neutral" id="pg-clock" title="Jump the simulated clock forward one hour and fire what came due" hidden>&#9201; +1 h</button>
    <button class="btn play" id="pg-batch" title="Run the dataset in the panel" hidden>&#9654; Run batch</button>
    <button class="btn neutral" id="pg-cancel" title="Stop the batch, leaving what it did readable" hidden>Stop</button>
    <button class="btn neutral" id="pg-heat" title="Shade the diagram by how much the run used each part" hidden>Heat map</button>
    <button class="btn neutral" id="pg-stop" title="Discard the sandbox" hidden>Discard</button>
    <label class="sim-speed" id="pg-dur-wrap" title="How long every stubbed job takes in simulated time">Tasks
      <select class="speed" id="pg-dur">${opts}</select>
    </label>
    <label class="sim-speed" id="pg-human-wrap" title="How user tasks are answered. Left for you they park, which is Step mode; a batch needs a duration">User tasks
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
    <div class="vars-head"><b>Run setup</b></div>
    <div class="vars-list" id="pg-setup-body"></div>`;
}

// panelHTML is the right column: what the run did.
function panelHTML() {
  return `
    <div class="vars-head"><b>Analysis</b>
      <button class="icon-btn" id="pg-panel-close" title="Back to Design" aria-label="Back to Design">&#10005;</button></div>
    <div class="vars-list" id="pg-body"></div>`;
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
  const panes = [setupPanel, results, panel];
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
    run: null,       // the last run status
    report: null,
    results: null,   // one page of the results table: {total, offset, rows}
    heat: null,      // the heat map, once a run has produced one
    showHeat: false,
    polling: 0,
    // What the run has to show, and what it showed. Expectations turn a report
    // somebody reads into a verdict something can act on — the same ones the
    // `atlas playground` runner exits a build on.
    expect: { allFinish: true, noIncidents: true, p90Hours: "", mustReach: "", queue: {} },
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
      toast(`${what}: ${e.message}`, "err");
    } finally {
      state.busy = false;
      render();
    }
  }

  // tasksInDiagram lists the elements a pool can be put on, straight off the
  // canvas: the author configures capacity against the tasks they drew, not
  // against a list they have to type out again.
  function tasksInDiagram() {
    let registry;
    try { registry = modeler.get("elementRegistry"); } catch { return []; }
    const out = [];
    registry.forEach((e) => {
      const bo = e.businessObject;
      if (!bo || !TASK_TYPES.has(bo.$type) || !bo.id) return;
      out.push({ id: bo.id, name: bo.name || bo.id });
    });
    return out;
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
    state.session = { id: s.id, processId: s.processId, seed: s.seed, stubLabel };
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
    state.showHeat = false;
    state.verdict = null;
    state.comparison = null;
    state.openRequest = null;
    clearCanvas();
  }

  async function startCase() {
    const c = await api("POST", path("/cases"), { variables: parseJSON(state.startVars, "start variables") });
    state.caseKey = c.instanceKey;
    state.result = c;
    await refresh();
  }

  async function step() {
    const occ = await api("POST", path("/step"));
    state.simTime = occ.simTime;
    if (!occ.happened) toast("Nothing left to do — the case has come to rest.", "");
    await refresh();
  }

  async function run() {
    const prog = await api("POST", path("/run"));
    state.simTime = prog.simTime;
    if (!prog.quiescent) toast("Stopped on the run budget — press Run again to carry on.", "");
    await refresh();
  }

  async function advance() {
    const out = await api("POST", path("/clock"), { millis: 3_600_000 });
    state.simTime = out.simTime;
    await refresh();
  }

  async function complete(jobKey) {
    await api("POST", path(`/tasks/${encodeURIComponent(jobKey)}/complete`),
      { variables: parseJSON(state.outputs, "output variables") });
    await refresh();
  }

  // parseJSON reports the mistake against the field it was made in, so "not valid
  // JSON" names which box to look at.
  function parseJSON(text, what) {
    try {
      return JSON.parse(text || (what === "the dataset" ? "[]" : "{}"));
    } catch (e) {
      throw new Error(`the ${what} are not valid JSON (${e.message})`);
    }
  }

  // runBody is the batch request this panel would send: the dataset and the
  // arrival profile. It is one function so that what is run and what is saved as a
  // scenario cannot differ.
  function runBody() {
    if (state.source === "generated") return { generate: genBody(), arrival: arrivalBody() };
    return { cases: parseJSON(state.cases, "the dataset"), arrival: arrivalBody() };
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
    return e;
  }

  // expectFromBody is expectBody read back, so a saved scenario fills the same
  // boxes it was built from.
  function expectFromBody(e) {
    const out = { allFinish: !!e.minCompleted, noIncidents: e.maxIncidents === 0,
      p90Hours: e.maxP90Millis ? String(e.maxP90Millis / 3_600_000) : "",
      mustReach: Object.keys(e.minVisits || {}).join(", "), queue: {} };
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
    state.showHeat = false;
    state.verdict = null;
    state.comparison = null;
    state.results = null;
    if (state.source === "csv") {
      if (!state.csv) throw new Error("choose a CSV file, or take the dataset from the list or the generator");
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
        throw new Error("the dataset is a list of cases, and it needs at least one");
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

  // resultsPageSize is how many cases the strip shows at a time. A page is read by a
  // person: enough to scroll, few enough that the fifty-thousandth case costs the
  // same as the fiftieth.
  const resultsPageSize = 50;

  // readResults fetches one page. The rows are never held whole — a run of fifty
  // thousand is fifty thousand rows in the sandbox's store and one page in here.
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
      state.showHeat = true;
      // The verdict is asked for after the report because "every case finishes"
      // means the cases this run had, which is a thing only the finished run knows.
      state.verdict = await api("POST", path("/verdict"), expectBody());
      if (state.baseline) {
        state.comparison = await api("POST", path("/compare"), { baseline: state.baseline });
      }
      await readResults(0);
      drawCanvas();
    } catch (e) {
      toast(`read the report: ${e.message}`, "err");
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
    state.session = { id: s.id, processId: s.processId, seed: s.seed, stubLabel: "from the scenario" };
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
    await refresh();
  }

  // saveScenario stores the run this panel would start, so somebody else — or a
  // build — can run exactly it. What is stored is the requests themselves, which
  // is why there is nothing here that has to be kept in step with the endpoints.
  async function saveScenario() {
    if (state.source === "csv") {
      throw new Error("a run from an uploaded CSV cannot be saved as a scenario: its rows are parsed on the server and are not in the browser to store — describe the dataset instead and it can be");
    }
    const name = (state.scenarioName || "").trim();
    if (!name) throw new Error("give the scenario a name");
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
    toast(`Saved the scenario "${saved.name}".`, "ok");
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
      throw new Error("this run did not pass, and a failing baseline would hide the failure from every run after it");
    }
    await api("PUT", `/api/v1/playground/scenarios/${encodeURIComponent(state.scenarioId)}/baseline`, state.report);
    state.baseline = state.report;
    state.comparison = null;
    await loadScenarios();
    toast("Kept as this scenario's baseline.", "ok");
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
    if (state.showHeat && state.heat) drawHeatMap(canvas, overlays, registry);
    else drawRun(canvas, overlays, registry);
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
          html: `<div class="token-badges"><div class="token-badge history" title="${count} token(s) passed through">${count}</div></div>`,
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
  function drawHeatMap(canvas, overlays, registry) {
    const max = state.heat.maxCount || 0;
    const mark = (id, count) => {
      if (!id || !registry.get(id)) return;
      const cls = `pg-heat-${heatLevel(count, max)}`;
      canvas.addMarker(id, cls);
      drawn.markers.push([id, cls]);
    };
    for (const e of state.heat.elements || []) {
      mark(e.id, e.count);
      if (!registry.get(e.id)) continue;
      try {
        // Above the shape, not below it: an event's own label sits underneath, and
        // a count that hides the name of the end somebody is looking for costs more
        // than it tells them.
        drawn.overlays.push(overlays.add(e.id, "pg-heat", {
          position: { top: -10, right: -6 },
          html: `<div class="token-badges"><div class="token-badge heat" title="${e.count} token(s)">${e.count}</div></div>`,
        }));
      } catch { /* shape without graphics */ }
    }
    const conns = connectionIndex(registry);
    for (const f of state.heat.flows || []) {
      mark(conns.get(f.from + " " + f.to), f.count);
    }
  }

  // ---- panel ----------------------------------------------------------------

  function taskRowHTML(t) {
    const kind = t.human ? "user task" : "job";
    return `<div class="pg-task">
      <div><b>${esc(t.element)}</b> <span class="muted">${esc(kind)}</span></div>
      <button class="btn neutral small" data-job="${esc(t.jobKey)}"
        title="Complete this task with the output variables below">Complete</button>
    </div>`;
  }

  function resultHTML() {
    const c = state.result;
    if (!c) return `<p class="muted">No case yet. Press <b>+ Case</b> to start one.</p>`;
    const vars = Object.entries(c.variables || {})
      .map(([k, v]) => `<tr><td>${esc(k)}</td><td class="mono">${esc(v)}</td></tr>`).join("");
    return `
      <div class="pg-result">
        <div><span class="muted">state</span> <b>${esc(c.state)}</b>
          ${c.incidents ? `<span class="pg-inc">${c.incidents} incident(s)</span>` : ""}</div>
        <div><span class="muted">path</span> <span class="mono">${esc((c.path || []).join(" → ")) || "—"}</span></div>
        ${vars ? `<table class="pg-vars">${vars}</table>` : `<p class="muted">No variables yet.</p>`}
      </div>`;
  }

  // setupHTML is what the run will be made of: how each job is answered and what
  // it has to queue for. It is asked before the sandbox opens because the policy
  // is fixed for its life — a run is only comparable with another run if the
  // policy behind them is the same.
  function setupHTML() {
    const tasks = tasksInDiagram();
    const rows = tasks.map((t) => {
      const cfg = state.pools[t.id] || {};
      return `<div class="pg-pool">
        <label title="${esc(t.id)}">${esc(t.name)}</label>
        <input type="text" data-pool="${esc(t.id)}" value="${esc(cfg.pool || "")}"
          placeholder="pool" title="Which pool this task draws on. Two tasks with the same name share one." />
        <input type="number" min="1" step="1" data-seats="${esc(t.id)}" value="${esc(cfg.seats || "")}"
          placeholder="seats" title="How many can be worked at once. Empty means no limit." />
      </div>`;
    }).join("");
    return `
      <p class="muted">Runs the diagram on screen with the real compiler and the real
        processor, on a clock the sandbox owns — a three-day timer takes no time at all.
        Nothing is deployed and no connector can be called: the sandbox has none.</p>
      ${scenarioPickerHTML()}
      <div class="pg-sec"><b>Resources</b> <span class="muted">optional</span></div>
      ${tasks.length
        ? `${rows}<label class="pg-check"><input type="checkbox" id="pg-hours" ${state.hours ? "checked" : ""} />
             Business hours only (08:00-17:00, Mon-Fri)</label>`
        : `<p class="muted">The diagram has no tasks to put a pool on.</p>`}
      <p class="muted">Without a pool every case is worked the moment it arrives, and the
        report's waiting time is zero by construction.</p>`;
  }

  // scenarioPickerHTML offers this diagram's saved runs. Opening one replaces the
  // sandbox, because the stub and pool policy travels with it and that policy is
  // fixed for a sandbox's life — which is also why the picker lives here, before
  // one is open, rather than beside the dataset.
  function scenarioPickerHTML() {
    if (!state.scenarios.length) return "";
    const opts = state.scenarios.map((sc) =>
      `<option value="${esc(sc.id)}"${sc.id === state.pickedScenario ? " selected" : ""}>${esc(sc.name)}${
        sc.hasBaseline ? " · has a baseline" : ""}</option>`).join("");
    return `
      <div class="pg-sec"><b>Saved scenarios</b> <span class="muted">${state.scenarios.length}</span></div>
      <div class="pg-timing">
        <select id="pg-scenario-pick"><option value="">pick one…</option>${opts}</select>
        <button class="btn neutral small" id="pg-scenario-open">Open</button>
      </div>
      <p class="muted">A scenario carries its own stub and pool policy, so opening one
        starts a fresh sandbox with it.</p>`;
  }

  // expectHTML is what the run has to show. It is the same set the `atlas
  // playground` runner exits a build on, which is the point: what an author checks
  // by eye here is what a build checks without them.
  function expectHTML() {
    const pools = Object.values(state.pools)
      .map((c) => (c.pool || "").trim()).filter(Boolean);
    const seen = new Set();
    const queueRows = pools.filter((p) => !seen.has(p) && seen.add(p)).map((p) => `
      <div class="pg-timing"><span class="muted" style="flex:1">queue at ${esc(p)} at most</span>
        <input type="number" min="0" step="1" data-queue="${esc(p)}" value="${esc(state.expect.queue[p] || "")}" /></div>`).join("");
    return `
      <div class="pg-sec"><b>Expectations</b> <span class="muted">optional</span></div>
      <label class="pg-check"><input type="checkbox" id="pg-x-finish" ${state.expect.allFinish ? "checked" : ""} />
        every case finishes</label>
      <label class="pg-check"><input type="checkbox" id="pg-x-inc" ${state.expect.noIncidents ? "checked" : ""} />
        no incidents</label>
      <div class="pg-timing"><span class="muted" style="flex:1">p90 under</span>
        <input type="number" min="0" step="0.5" id="pg-x-p90" value="${esc(state.expect.p90Hours)}" />
        <span class="muted">hours</span></div>
      <label class="field"><span>Must reach (element ids, comma separated)</span>
        <textarea id="pg-x-reach" rows="1" spellcheck="false">${esc(state.expect.mustReach)}</textarea></label>
      ${queueRows}`;
  }

  // scenarioSaveHTML is how a run becomes repeatable by somebody who is not here.
  function scenarioSaveHTML() {
    if (state.source === "csv") {
      return `<div class="pg-sec"><b>Scenario</b></div>
        <p class="muted">A run from an uploaded CSV cannot be saved as a scenario: its
          rows are parsed on the server, by the same code a real import uses, and are
          not in the browser to store. A described dataset can be — that is what
          <b>Generated</b> is for.</p>`;
    }
    return `
      <div class="pg-sec"><b>Scenario</b>
        <span class="muted">${state.scenarioId ? esc(state.scenarioId) : "unsaved"}</span></div>
      <div class="pg-timing">
        <input type="text" id="pg-scenario-name" placeholder="name it" value="${esc(state.scenarioName)}" style="flex:1" />
        <button class="btn neutral small" id="pg-scenario-save">Save</button>
        ${state.scenarioId ? `<button class="btn neutral small" id="pg-scenario-delete">Delete</button>` : ""}
      </div>
      ${state.baseline
        ? `<p class="muted">A baseline is stored: the next run is set beside it.</p>`
        : `<p class="muted">Run it, then keep the run as a baseline to compare the next one against.</p>`}`;
  }

  // verdictHTML is the run judged. A verdict with no checks is a pass, and says so
  // quietly: "I have not said what I expect yet" must not read as a problem.
  function verdictHTML() {
    const v = state.verdict;
    if (!v) return "";
    if (!v.checks || !v.checks.length) {
      return `<div class="pg-sec"><b>Verdict</b></div>
        <p class="muted">Nothing was expected of this run, so there was nothing to check.</p>`;
    }
    return `
      <div class="pg-sec"><b>Verdict</b>
        <span class="pg-verdict ${v.passed ? "ok" : "bad"}">${v.passed ? "passed" : "failed"}</span></div>
      <table class="pg-table pg-checks"><tbody>${v.checks.map((c) => `<tr class="${c.passed ? "" : "pg-bad"}">
        <td>${c.passed ? "&#10003;" : "&#10007;"}</td><td>${esc(c.name)}</td>
        <td class="muted">${esc(c.want)}</td><td>${esc(c.got)}</td></tr>`).join("")}</tbody></table>
      ${state.scenarioId ? `<div class="pg-actions">
        <button class="btn neutral small" id="pg-keep-baseline"${v.passed ? "" : " disabled"}
          title="${v.passed ? "Keep this run as what the next one is measured against"
            : "A failing baseline would hide the failure from every run after it"}">Keep as baseline</button>
      </div>` : ""}`;
  }

  // comparisonHTML is this run beside the stored baseline. Only what moved: a
  // table of unchanged numbers is where the two that did move go to hide.
  function comparisonHTML() {
    const c = state.comparison;
    if (!c || !c.deltas) return "";
    const moved = c.deltas.filter((d) => d.before !== d.after);
    if (!moved.length) {
      return `<div class="pg-sec"><b>Against the baseline</b></div>
        <p class="muted">Nothing moved. Same dataset, same policy, same seed — the run is reproducible.</p>`;
    }
    const cell = (unit, v) => unit === "millis" ? fmtDur(v) : unit === "percent" ? `${v}%` : String(v);
    return `
      <div class="pg-sec"><b>Against the baseline</b> <span class="muted">${moved.length} changed</span></div>
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
        : `<label class="field"><span>Cases (JSON list)</span>
             <textarea id="pg-cases" rows="5" spellcheck="false">${esc(state.cases)}</textarea></label>`;
    return `
      <div class="pg-sec"><b>Dataset</b></div>
      <div class="pg-source">${tab("list", "A list")}${tab("generated", "Generated")}${tab("csv", "A CSV file")}</div>
      ${body}`;
  }

  function csvHTML() {
    return state.csv
      ? `<div class="pg-file"><span class="mono">${esc(state.csv.name)}</span>
           <button class="btn neutral small" id="pg-csv-clear">Choose another</button></div>`
      : `<div class="pg-file"><input type="file" id="pg-csv" accept=".csv,text/csv" />
           <span class="muted">its header names the variables</span></div>`;
  }

  // generatorHTML describes the dataset rather than listing it: a count, and a row
  // per variable saying how its value is drawn.
  function generatorHTML() {
    return `
      <div class="pg-timing"><span class="muted" style="flex:1">how many cases</span>
        <input type="number" min="1" step="1" id="pg-gen-count" value="${esc(state.gen.count)}" /></div>
      <div class="pg-fields">${state.gen.fields.map(fieldRowHTML).join("")}</div>
      <div class="pg-timing">
        <button class="btn neutral small" id="pg-gen-add">Add a field</button>
        <button class="btn neutral small" id="pg-gen-preview">Preview</button>
      </div>
      ${previewHTML()}`;
  }

  // A field is two lines: what it is called and what kind it is, then that kind's
  // parameters. One line was tried first and does not fit — in a panel this narrow
  // a name and three boxes leave "amount" showing as "amour", and a date field with
  // two offsets and a checkbox is unreadable.
  function fieldRowHTML(f, i) {
    const kinds = FIELD_KINDS.map((k) =>
      `<option value="${k.kind}"${k.kind === f.kind ? " selected" : ""}>${esc(k.label)}</option>`).join("");
    return `
      <div class="pg-field">
        <div class="pg-field-head">
          <input type="text" data-gen="name" data-i="${i}" value="${esc(f.name)}"
            placeholder="variable" aria-label="Variable name" />
          <select data-gen="kind" data-i="${i}" aria-label="How it is drawn">${kinds}</select>
          <button class="icon-btn" data-gen-del="${i}" title="Remove this field"
            aria-label="Remove this field">&#10005;</button>
        </div>
        <div class="pg-field-args">${fieldParamsHTML(f, i)}</div>
      </div>`;
  }

  function fieldParamsHTML(f, i) {
    const num = (key, hint) =>
      `<input type="number" data-gen="${key}" data-i="${i}" value="${esc(f[key])}"
         placeholder="${hint}" aria-label="${hint}" />`;
    const text = (key, hint) =>
      `<input type="text" data-gen="${key}" data-i="${i}" value="${esc(f[key])}"
         placeholder="${hint}" aria-label="${hint}" />`;
    switch (f.kind) {
      case "int": return num("min", "min") + num("max", "max");
      case "decimal": return num("min", "min") + num("max", "max") + num("decimals", "places");
      case "bool": return num("percentTrue", "% true");
      case "choice": return text("choices", "gold:1, silver:3, standard:6");
      case "constant": return text("value", "value");
      case "sequence": return text("prefix", "ORDER-");
      default: return num("fromDays", "from (days)") + num("toDays", "to (days)") +
        `<label class="pg-check"><input type="checkbox" data-gen="onlyDate" data-i="${i}"
           ${f.onlyDate ? "checked" : ""} /> date only</label>`;
    }
  }

  // previewHTML shows the first cases of the described dataset. They are the cases
  // the run will carry, which is the only reason showing them is worth anything.
  function previewHTML() {
    const p = state.genPreview;
    if (!p) return "";
    if (!p.rows.length || !p.columns.length) {
      return `<p class="muted">Nothing described yet: a field needs a name.</p>`;
    }
    const cell = (v) => esc(typeof v === "boolean" ? String(v) : v);
    return `
      <div class="pg-preview"><table>
        <thead><tr>${p.columns.map((c) => `<th>${esc(c)}</th>`).join("")}</tr></thead>
        <tbody>${p.rows.map((r) =>
          `<tr>${p.columns.map((c) => `<td>${cell(r[c])}</td>`).join("")}</tr>`).join("")}</tbody>
      </table></div>
      <p class="muted">The first ${p.rows.length} of ${p.total} — these are the cases the run carries.</p>`;
  }

  // batchSetupHTML is what decides the run: the data, when it arrives, what it has
  // to show, and the scenario all three are stored in.
  function batchSetupHTML() {
    const modes = ARRIVALS.map((a) =>
      `<option value="${a.mode}"${a.mode === state.arrival ? " selected" : ""}>${esc(a.label)}</option>`).join("");
    const arrival = ARRIVALS.find((a) => a.mode === state.arrival);
    const param = arrival ? arrival.param : null;
    return `
      ${datasetHTML()}
      <div class="pg-sec"><b>Timing</b></div>
      <div class="pg-timing">
        <select id="pg-arrival">${modes}</select>
        ${param ? `<input type="number" min="1" step="1" id="pg-arrival-n" value="${esc(state.arrivalN)}" />
                   <span class="muted">${esc(param)}</span>` : ""}
      </div>
      ${expectHTML()}
      ${scenarioSaveHTML()}`;
  }

  // batchAnalysisHTML is what the run did — read while it runs and after it stops.
  function batchAnalysisHTML() {
    if (!state.run) {
      return `<p class="muted">Nothing has run yet. Set the dataset and the timing on the
        left, then press <b>Run batch</b>.</p>`;
    }
    return `
      ${runStatusHTML()}
      ${verdictHTML()}
      ${comparisonHTML()}
      ${reportHTML()}`;
  }

  function runStatusHTML() {
    const r = state.run;
    if (!r) return "";
    const done = r.state !== "running";
    const pct = r.cases ? Math.round(100 * (r.completed || 0) / r.cases) : 0;
    return `
      <div class="pg-sec"><b>Run</b> <span class="muted">${esc(r.state)}</span></div>
      <div class="pg-progress"><div class="pg-progress-fill" style="width:${done ? 100 : pct}%"></div></div>
      <div class="pg-run-line">
        <span>${r.completed || 0} of ${r.cases || 0} finished</span>
        ${r.simTime ? `<span class="muted">simulated ${esc(fmtWhen(r.simTime))}</span>` : ""}
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
    const cold = coldPaths();
    return `
      <div class="pg-sec"><b>Outcomes</b></div>
      <div class="pg-facts">
        <div><b>${rep.completed}</b><span>of ${rep.cases} finished</span></div>
        <div><b class="${rep.incidents ? "bad" : ""}">${rep.incidents}</b><span>incidents</span></div>
        <div><b>${rep.maxInFlight}</b><span>peak in flight</span></div>
      </div>
      <div class="pg-sec"><b>Durations</b> <span class="muted">per case, simulated</span></div>
      <div class="pg-facts">
        <div><b>${esc(fmtDur(d.minMillis))}</b><span>fastest</span></div>
        <div><b>${esc(fmtDur(d.p50Millis))}</b><span>median</span></div>
        <div><b>${esc(fmtDur(d.p90Millis))}</b><span>p90</span></div>
        <div><b>${esc(fmtDur(d.maxMillis))}</b><span>slowest</span></div>
      </div>
      ${bottlenecks.length ? `
        <div class="pg-sec"><b>Bottlenecks</b> <span class="muted">by total waiting</span></div>
        <table class="pg-table pg-bottlenecks"><thead><tr><th>element</th><th>runs</th><th>waiting</th><th>longest</th><th>work</th></tr></thead>
        <tbody>${bottlenecks.map((b) => `<tr>
          <td class="mono">${esc(b.id)}</td><td>${b.runs}</td>
          <td>${esc(fmtDur(b.waitMillis))}</td><td>${esc(fmtDur(b.maxWaitMillis))}</td>
          <td>${esc(fmtDur(b.workMillis))}</td></tr>`).join("")}</tbody></table>` : ""}
      ${pools.length ? `
        <div class="pg-sec"><b>Pools</b></div>
        <table class="pg-table pg-pools"><thead><tr><th>pool</th><th>seats</th><th>used</th><th>served</th><th>longest queue</th></tr></thead>
        <tbody>${pools.map(([name, p]) => `<tr>
          <td class="mono">${esc(name)}</td><td>${p.capacity}</td>
          <td>${p.utilisationPercent}%</td><td>${p.served}</td><td>${p.maxQueue}</td></tr>`).join("")}</tbody></table>` : ""}
      ${timelineHTML(rep.timeline)}
      ${state.heat ? `
        <div class="pg-sec"><b>Coverage</b>
          <span class="muted">${cold.length ? `${cold.length} never reached` : "every path used"}</span></div>
        ${cold.length
          ? `<div class="pg-cold" id="pg-cold">${cold.map((c) => `<span class="mono">${esc(c)}</span>`).join("")}</div>`
          : `<p class="muted">The data exercised every element and every sequence flow.</p>`}` : ""}
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
    return `
      <div class="pg-results-head">
        <b>Results</b>
        <span class="muted">${shown ? `${offset + 1}\u2013${offset + shown} of ${total} cases` : "no cases"}</span>
        <span style="flex:1"></span>
        <button class="btn neutral small" id="pg-page-prev"${offset > 0 ? "" : " disabled"}>Previous</button>
        <button class="btn neutral small" id="pg-page-next"${offset + shown < total ? "" : " disabled"}>Next</button>
        <button class="btn neutral small" id="pg-csv-out">&#8615; CSV</button>
      </div>
      ${shown ? `
        <div class="pg-results-scroll">
          <table class="pg-table pg-cases">
            <thead><tr><th>case</th><th>outcome</th><th>duration</th><th>incidents</th>
              ${names.map((n) => `<th>${esc(n)}</th>`).join("")}</tr></thead>
            <tbody>${rows.map((row) => `<tr${row.incidents ? ' class="pg-bad"' : ""}>
              <td>${row.index + 1}</td>
              <td class="mono">${esc(row.end || (row.state === "completed" ? "" : row.state))}</td>
              <td>${row.ended ? esc(fmtDur(row.durationMillis)) : "\u2014"}</td>
              <td>${row.incidents || ""}</td>
              ${names.map((n) => `<td class="mono">${esc((row.variables || {})[n] == null ? "" : row.variables[n])}</td>`).join("")}
            </tr>`).join("")}</tbody>
          </table>
        </div>` : `<p class="muted">The run produced no rows to show.</p>`}`;
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
      <div class="pg-sec"><b>Over time</b>
        <span class="muted">${esc(fmtDur(tl.widthMillis || 0))} per slice</span></div>
      <svg class="pg-chart" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" role="img"
        aria-label="Arrivals, completions and work in progress over simulated time">
        ${bars}<polyline class="pg-line" points="${line}"/>
        <line class="pg-base" x1="0" y1="${H}" x2="${W}" y2="${H}"/>
      </svg>
      <div class="pg-legend">
        <span><i class="pg-key-in"></i>arrived</span>
        <span><i class="pg-key-out"></i>finished</span>
        <span><i class="pg-key-flight"></i>in flight (peak ${maxFlight})</span>
      </div>
      <div class="pg-axis"><span>${esc(fmtWhen(b[0].at))}</span><span>${esc(fmtWhen(b[b.length - 1].at))}</span></div>`;
  }

  function modeTabsHTML() {
    const label = { step: "Step", batch: "Batch" };
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
    show("pg-heat", batching && !!state.heat);
    show("pg-stop", open);
    show("pg-start", !open);
    // The policy is fixed for the life of a sandbox, so the selects go away once
    // one is open rather than sitting there implying otherwise.
    el("pg-dur-wrap").hidden = open;
    el("pg-human-wrap").hidden = open;
    const heatBtn = el("pg-heat");
    if (heatBtn) heatBtn.classList.toggle("on", state.showHeat);

    el("pg-hint").textContent = !open
      ? "Nothing is deployed and no connector can be called — the sandbox has none."
      : batching
        ? running ? "Running the dataset. Stop leaves what it did readable."
          : state.report ? "The report is in the panel; the heat map shades the diagram."
            : "Give it a dataset and a timing profile, then run it."
        : state.tasks.length
          ? "A task is waiting for you: complete it in the panel."
          : "Step one occurrence at a time, or run the case to rest.";
    el("pg-stats").textContent = open
      ? `simulated ${fmtWhen(state.simTime)} · tasks ${state.session.stubLabel} · seed ${state.session.seed}`
      : "";

    // The results strip is there only when there are results to put in it: an empty
    // band under the diagram is a promise the panel has not kept.
    results.hidden = !(batching && state.report);
    el("pg-setup-body").innerHTML = setupColumnHTML();
    el("pg-body").innerHTML = analysisColumnHTML();
    if (!results.hidden) el("pg-results-body").innerHTML = resultsStripHTML();
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
      <label class="field"><span>Start variables (JSON)</span>
        <textarea id="pg-startvars" rows="3" spellcheck="false">${esc(state.startVars)}</textarea></label>
      <div class="pg-sec"><b>Waiting for you</b> <span class="muted">${state.tasks.length}</span></div>
      ${state.tasks.length ? state.tasks.map(taskRowHTML).join("") : `<p class="muted">Nothing is waiting.</p>`}
      <label class="field"><span>Output variables (JSON)</span>
        <textarea id="pg-outputs" rows="2" spellcheck="false">${esc(state.outputs)}</textarea></label>`;
  }

  // analysisColumnHTML is the right column: what came back, in both modes.
  function analysisColumnHTML() {
    if (!state.session) {
      return `<p class="muted">Start the sandbox and what it does shows up here — the
        outcomes, the durations, where the cases waited, and the run over simulated time.</p>`;
    }
    if (state.mode === "batch") return batchAnalysisHTML();
    return `<div class="pg-sec"><b>Case</b></div>${resultHTML()}`;
  }

  // ---- events ---------------------------------------------------------------

  bar.addEventListener("click", (e) => {
    const btn = e.target.closest("button");
    if (!btn) return;
    switch (btn.id) {
      case "pg-start": guard("start sandbox", start); break;
      case "pg-case": guard("start case", startCase); break;
      case "pg-step": guard("step", step); break;
      case "pg-run": guard("run", run); break;
      case "pg-clock": guard("advance the clock", advance); break;
      case "pg-batch": guard("run the batch", startBatch); break;
      case "pg-cancel": guard("stop the batch", cancelBatch); break;
      case "pg-stop": guard("discard sandbox", stop); break;
      case "pg-heat":
        state.showHeat = !state.showHeat;
        drawCanvas();
        render();
        break;
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
      state.showHeat = state.mode === "batch" && !!state.heat;
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
    if (e.target.closest("#pg-gen-preview")) {
      guard("preview the dataset", previewDataset);
      return;
    }
    if (e.target.closest("#pg-scenario-open")) {
      const id = el("pg-scenario-pick")?.value;
      if (id) guard("open the scenario", () => openScenario(id));
      return;
    }
    if (e.target.closest("#pg-scenario-save")) {
      guard("save the scenario", saveScenario);
      return;
    }
    if (e.target.closest("#pg-scenario-delete")) {
      guard("delete the scenario", deleteScenario);
      return;
    }
    if (e.target.closest("#pg-keep-baseline")) {
      guard("keep the baseline", keepBaseline);
      return;
    }
    const page = e.target.closest("#pg-page-prev, #pg-page-next");
    if (page) {
      const at = state.results ? state.results.offset : 0;
      const to = page.id === "pg-page-prev" ? at - resultsPageSize : at + resultsPageSize;
      guard("read the results", () => readResults(to));
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
    if (btn) guard("complete task", () => complete(btn.dataset.job));
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
    el("pg-hint").textContent = "The diagram changed — discard the sandbox and start it again to run what is on screen.";
  };
  modeler.on("elements.changed", onEdit);

  function setActive(on) {
    bar.hidden = !on;
    panel.hidden = !on;
    setupPanel.hidden = !on;
    if (!on) results.hidden = true;
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
      if (state.session) {
        const id = state.session.id;
        state.session = null;
        // Best effort on teardown: the page may be unloading.
        api("DELETE", `/api/v1/playground/sessions/${encodeURIComponent(id)}`).catch(() => {});
      }
      bar.remove();
      setupPanel.remove();
      results.remove();
      panel.remove();
    },
  };
}
