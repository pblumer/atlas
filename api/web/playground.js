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
// See ADR-draft-modeler-playground.

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

// panelHTML is the side panel: the run's configuration before it starts, and what
// it did afterwards.
function panelHTML() {
  return `
    <div class="vars-head"><b>Playground</b>
      <button class="icon-btn" id="pg-panel-close" title="Back to Design" aria-label="Back to Design">&#10005;</button></div>
    <div class="vars-list" id="pg-body"></div>`;
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

  const panel = document.createElement("aside");
  panel.className = "vars-panel";
  panel.id = "pg-panel";
  panel.hidden = true;
  panel.innerHTML = panelHTML();
  body.insertBefore(panel, root.querySelector("#props-resizer"));

  const el = (id) => bar.querySelector("#" + id) || panel.querySelector("#" + id);
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
    cases: '[{"amount": 1200}, {"amount": 90}]',
    csv: null,       // a File, when the dataset came from one
    arrival: "allAtOnce",
    arrivalN: 10,
    run: null,       // the last run status
    report: null,
    heat: null,      // the heat map, once a run has produced one
    showHeat: false,
    polling: 0,
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
    const s = await api("POST", "/api/v1/playground/sessions", {
      source: "xml",
      xml,
      startTime: new Date().toISOString().replace(/\.\d+Z$/, "Z"),
      stubs: stubPolicy(),
    });
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
    if (state.csv) {
      const form = new FormData();
      form.append("file", state.csv);
      form.append("arrival", JSON.stringify(arrivalBody()));
      const res = await fetch(path("/runs/csv"), { method: "POST", body: form });
      const data = await res.json().catch(() => null);
      if (!res.ok) throw new Error((data && data.error) || res.statusText);
      state.run = data;
    } else {
      const cases = parseJSON(state.cases, "the dataset");
      if (!Array.isArray(cases) || !cases.length) {
        throw new Error("the dataset is a list of cases, and it needs at least one");
      }
      state.run = await api("POST", path("/runs"), { cases, arrival: arrivalBody() });
    }
    startPolling();
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
      drawCanvas();
    } catch (e) {
      toast(`read the report: ${e.message}`, "err");
    }
    render();
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
      <div class="pg-sec"><b>Resources</b> <span class="muted">optional</span></div>
      ${tasks.length
        ? `${rows}<label class="pg-check"><input type="checkbox" id="pg-hours" ${state.hours ? "checked" : ""} />
             Business hours only (08:00-17:00, Mon-Fri)</label>`
        : `<p class="muted">The diagram has no tasks to put a pool on.</p>`}
      <p class="muted">Without a pool every case is worked the moment it arrives, and the
        report's waiting time is zero by construction.</p>`;
  }

  // batchHTML is the dataset, the timing, and what came back.
  function batchHTML() {
    const modes = ARRIVALS.map((a) =>
      `<option value="${a.mode}"${a.mode === state.arrival ? " selected" : ""}>${esc(a.label)}</option>`).join("");
    const arrival = ARRIVALS.find((a) => a.mode === state.arrival);
    const param = arrival ? arrival.param : null;
    return `
      <div class="pg-sec"><b>Dataset</b>
        <span class="muted">${state.csv ? "from a file" : "one entry per case"}</span></div>
      ${state.csv
        ? `<div class="pg-file"><span class="mono">${esc(state.csv.name)}</span>
             <button class="btn neutral small" id="pg-csv-clear">Use the list instead</button></div>`
        : `<label class="field"><span>Cases (JSON list)</span>
             <textarea id="pg-cases" rows="5" spellcheck="false">${esc(state.cases)}</textarea></label>
           <div class="pg-file"><input type="file" id="pg-csv" accept=".csv,text/csv" />
             <span class="muted">or a CSV, whose header names the variables</span></div>`}
      <div class="pg-sec"><b>Timing</b></div>
      <div class="pg-timing">
        <select id="pg-arrival">${modes}</select>
        ${param ? `<input type="number" min="1" step="1" id="pg-arrival-n" value="${esc(state.arrivalN)}" />
                   <span class="muted">${esc(param)}</span>` : ""}
      </div>
      ${runStatusHTML()}
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
        <table class="pg-table"><thead><tr><th>element</th><th>runs</th><th>waiting</th><th>longest</th><th>work</th></tr></thead>
        <tbody>${bottlenecks.map((b) => `<tr>
          <td class="mono">${esc(b.id)}</td><td>${b.runs}</td>
          <td>${esc(fmtDur(b.waitMillis))}</td><td>${esc(fmtDur(b.maxWaitMillis))}</td>
          <td>${esc(fmtDur(b.workMillis))}</td></tr>`).join("")}</tbody></table>` : ""}
      ${pools.length ? `
        <div class="pg-sec"><b>Pools</b></div>
        <table class="pg-table"><thead><tr><th>pool</th><th>seats</th><th>used</th><th>served</th><th>longest queue</th></tr></thead>
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
      <div class="pg-actions">
        <button class="btn neutral small" id="pg-csv-out">&#8615; Results as CSV</button>
      </div>`;
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

    const b = el("pg-body");
    if (!open) { b.innerHTML = setupHTML(); return; }
    b.innerHTML = modeTabsHTML() + (state.mode === "batch" ? batchHTML() : `
      <label class="field"><span>Start variables (JSON)</span>
        <textarea id="pg-startvars" rows="3" spellcheck="false">${esc(state.startVars)}</textarea></label>
      <div class="pg-sec"><b>Waiting for you</b> <span class="muted">${state.tasks.length}</span></div>
      ${state.tasks.length ? state.tasks.map(taskRowHTML).join("") : `<p class="muted">Nothing is waiting.</p>`}
      <label class="field"><span>Output variables (JSON)</span>
        <textarea id="pg-outputs" rows="2" spellcheck="false">${esc(state.outputs)}</textarea></label>
      <div class="pg-sec"><b>Case</b></div>
      ${resultHTML()}`);
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

  panel.addEventListener("click", (e) => {
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
    if (e.target.closest("#pg-csv-clear")) {
      state.csv = null;
      render();
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
  });

  // Keep what is typed in state, without re-rendering: a render would move the
  // caret to the end of whatever box has focus.
  panel.addEventListener("input", (e) => {
    const t = e.target;
    if (t.id === "pg-startvars") state.startVars = t.value;
    if (t.id === "pg-outputs") state.outputs = t.value;
    if (t.id === "pg-cases") state.cases = t.value;
    if (t.id === "pg-arrival-n") state.arrivalN = t.value;
    if (t.dataset.pool != null) {
      state.pools[t.dataset.pool] = { ...state.pools[t.dataset.pool], pool: t.value };
    }
    if (t.dataset.seats != null) {
      state.pools[t.dataset.seats] = { ...state.pools[t.dataset.seats], seats: t.value };
    }
  });

  panel.addEventListener("change", (e) => {
    const t = e.target;
    if (t.id === "pg-hours") state.hours = t.checked;
    if (t.id === "pg-arrival") { state.arrival = t.value; render(); }
    if (t.id === "pg-csv" && t.files && t.files[0]) { state.csv = t.files[0]; render(); }
  });

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
    editor.classList.toggle("pg-active", on);
    if (on) { render(); drawCanvas(); }
    else clearCanvas();
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
      panel.remove();
    },
  };
}
