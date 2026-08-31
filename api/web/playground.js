// Playground tab: run the diagram on the real engine, in a throwaway sandbox.
//
// The Design view's token simulation (ADR-0078) is engine-free on purpose — it
// answers "where can a token go". This answers "what does the process do with
// this data": the server compiles the diagram on screen and executes it on the
// real processor over a virtual clock, so FEEL, gateways, DMN and timers behave
// exactly as they will in production. Nothing is deployed, nothing durable is
// written, and the sandbox holds no connectors, so no task can reach the network.
//
// This module is the first stage: one case at a time, stepped by hand, with the
// human tasks answered by the person at the keyboard. Datasets, arrival profiles,
// resource pools and the analysis come next (ADR-draft-modeler-playground).

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

// barHTML is the control strip. It borrows the token simulation's bar styling so
// the two play modes look like siblings rather than two unrelated toolbars.
function barHTML() {
  const opts = STUB_DURATIONS
    .map((d, i) => `<option value="${d.ms}"${i === 1 ? " selected" : ""}>${esc(d.label)}</option>`)
    .join("");
  return `
    <button class="btn play" id="pg-start" title="Compile the diagram on screen and open a sandbox for it">&#9654; Start sandbox</button>
    <button class="btn neutral" id="pg-case" title="Start one case with the start variables below" hidden>+ Case</button>
    <button class="btn neutral" id="pg-step" title="Carry out exactly one thing: a job answered, or a timer fired" hidden>Step</button>
    <button class="btn neutral" id="pg-run" title="Run until the case comes to rest" hidden>Run</button>
    <button class="btn neutral" id="pg-clock" title="Jump the simulated clock forward one hour and fire what came due" hidden>&#9201; +1 h</button>
    <button class="btn neutral" id="pg-stop" title="Discard the sandbox" hidden>Discard</button>
    <label class="sim-speed" id="pg-dur-wrap" title="How long every stubbed job takes in simulated time">Stub duration
      <select class="speed" id="pg-dur">${opts}</select>
    </label>
    <span class="sim-hint" id="pg-hint"></span>
    <span style="flex:1"></span>
    <span class="sim-stats" id="pg-stats"></span>`;
}

// panelHTML is the side panel: what to start a case with, what is waiting for a
// person, and what became of the case.
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
    session: null,   // {id, processId, seed}
    simTime: "",
    caseKey: "",     // the case being watched
    result: null,    // its last read
    tasks: [],
    visits: {},
    startVars: "{}",
    outputs: "{}",
    busy: false,
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

  async function start() {
    const { xml } = await modeler.saveXML({ format: true });
    const sel = el("pg-dur");
    const ms = Number(sel.value);
    const stubLabel = sel.options[sel.selectedIndex].text;
    const s = await api("POST", "/api/v1/playground/sessions", {
      source: "xml",
      xml,
      startTime: new Date().toISOString().replace(/\.\d+Z$/, "Z"),
      // One policy for every job the model creates. Human tasks are deliberately
      // left out: they park, and the person at the keyboard answers them.
      stubs: { default: { minMillis: ms, maxMillis: ms } },
    });
    state.session = { id: s.id, processId: s.processId, seed: s.seed, stubLabel };
    state.simTime = s.simTime;
    state.caseKey = "";
    state.result = null;
    await refresh();
  }

  async function stop() {
    if (!state.session) return;
    try { await api("DELETE", path()); } catch { /* already gone; the panel resets either way */ }
    state.session = null;
    state.tasks = [];
    state.visits = {};
    state.result = null;
    state.caseKey = "";
    clearCanvas();
  }

  async function startCase() {
    let vars;
    try {
      vars = JSON.parse(state.startVars || "{}");
    } catch (e) {
      throw new Error(`start variables are not valid JSON (${e.message})`);
    }
    const c = await api("POST", path("/cases"), { variables: vars });
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
    let vars;
    try {
      vars = JSON.parse(state.outputs || "{}");
    } catch (e) {
      throw new Error(`output variables are not valid JSON (${e.message})`);
    }
    await api("POST", path(`/tasks/${encodeURIComponent(jobKey)}/complete`), { variables: vars });
    await refresh();
  }

  // refresh re-reads everything the panel and the canvas show. One round trip per
  // view; the sandbox is local to the server, so this is cheap.
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

  // drawCanvas paints the run onto the diagram: every element a token has passed
  // through is marked and carries its count, and an element with a job waiting for
  // a person is marked live. It reuses the runtime view's markers, so a playground
  // run reads exactly like a real instance does.
  function drawCanvas() {
    clearCanvas();
    let canvas, overlays, registry;
    try {
      canvas = modeler.get("canvas");
      overlays = modeler.get("overlays");
      registry = modeler.get("elementRegistry");
    } catch { return; }

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

  function render() {
    const open = !!state.session;
    for (const id of ["pg-case", "pg-step", "pg-run", "pg-clock", "pg-stop"]) {
      const b = el(id);
      if (b) { b.hidden = !open; b.disabled = state.busy; }
    }
    const startBtn = el("pg-start");
    startBtn.hidden = open;
    startBtn.disabled = state.busy;
    el("pg-dur-wrap").hidden = open; // the policy is fixed for the life of a sandbox

    el("pg-hint").textContent = !open
      ? "Nothing is deployed and no connector can be called — the sandbox has none."
      : state.tasks.length
        ? "A task is waiting for you: complete it in the panel."
        : "Step one occurrence at a time, or run the case to rest.";
    el("pg-stats").textContent = open
      ? `simulated ${esc(state.simTime || "")} · stubs ${esc(state.session.stubLabel)} · seed ${state.session.seed}`
      : "";

    const b = el("pg-body");
    if (!open) {
      b.innerHTML = `<p class="muted">Start a sandbox to run the diagram on screen. It compiles with the
        real compiler and runs on the real processor, on a clock the sandbox owns — a three-day timer
        takes no time at all.</p>`;
      return;
    }
    b.innerHTML = `
      <label class="field"><span>Start variables (JSON)</span>
        <textarea id="pg-startvars" rows="3" spellcheck="false">${esc(state.startVars)}</textarea></label>
      <div class="pg-sec"><b>Waiting for you</b> <span class="muted">${state.tasks.length}</span></div>
      ${state.tasks.length ? state.tasks.map(taskRowHTML).join("") : `<p class="muted">Nothing is waiting.</p>`}
      <label class="field"><span>Output variables (JSON)</span>
        <textarea id="pg-outputs" rows="2" spellcheck="false">${esc(state.outputs)}</textarea></label>
      <div class="pg-sec"><b>Case</b></div>
      ${resultHTML()}`;
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
      case "pg-stop": guard("discard sandbox", stop); break;
    }
  });

  panel.addEventListener("click", (e) => {
    // Closing the panel means leaving the mode, so it goes through the tab that
    // owns it — otherwise the tab bar would still say Playground with no panel.
    if (e.target.closest("#pg-panel-close")) {
      root.querySelector('.etabs button[data-tab="design"]')?.click();
      return;
    }
    const btn = e.target.closest("button[data-job]");
    if (btn) guard("complete task", () => complete(btn.dataset.job));
  });

  // Keep the textareas in state so a re-render does not throw away what is typed.
  panel.addEventListener("input", (e) => {
    if (e.target.id === "pg-startvars") state.startVars = e.target.value;
    if (e.target.id === "pg-outputs") state.outputs = e.target.value;
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
    if (on) render();
    else clearCanvas();
  }

  render();
  return {
    setActive,
    // destroy releases the server-side sandbox when the editor goes away. Without
    // it the session would sit there until its TTL, holding an engine.
    destroy() {
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
