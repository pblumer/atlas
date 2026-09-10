// The derived information model — what an application's processes imply about its
// data, read without anyone having modelled anything
// (ADR-0301, §3).
//
// This is the *as built* reading. The model somebody authors under Data is a
// different statement — a target, not yet reality — and nothing here writes into it.
// Read the two together and their difference is the work not yet done, which is why
// the two are kept apart rather than reconciled.
//
// Everything on this page is read-only, and that is the point rather than a
// limitation: it is evidence about the processes, not a document about the business.
// The drawings are the same two canvases the authored model and the run-time overlay
// use, in their read-only mode, so one notation means one thing everywhere.

import { loadCanvasBundle } from "./canvas-bundle.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// Live canvases, module-level for the reason the replay's are: leaving the view has
// to take them down rather than leave them bound to markup that is gone.
let canvases = null;
function destroyCanvases() {
  if (!canvases) return;
  for (const c of [canvases.classes, canvases.lifecycle]) {
    if (c) { try { c.destroy(); } catch { /* already gone */ } }
  }
  canvases = null;
}

export async function mountDerivedModel(root, { api, applicationId, application }) {
  destroyCanvases();
  const appName = (application && application.name) || applicationId;

  root.innerHTML = `<div class="dm-root">
    <div class="between">
      <div>
        <h1>As built · ${esc(appName)}</h1>
        <p class="muted" style="margin:0">The classes, members and states this application's processes
          actually carry — read from the processes themselves, with nothing modelled by hand. The model
          under <a href="#/data">Data</a> is the other statement: what you <i>want</i> it to be.</p>
      </div>
      <a class="btn neutral" href="#/data">Information model →</a>
    </div>
    <div id="dm-body"><p class="muted">Reading the processes…</p></div>
  </div>`;

  const body = root.querySelector("#dm-body");
  let derived;
  try {
    derived = await api("GET", `/api/v1/infomodel/derived?applicationId=${encodeURIComponent(applicationId)}`);
  } catch (e) {
    body.innerHTML = `<div class="card empty"><p>Could not read the processes: ${esc(e.message)}</p></div>`;
    return;
  }
  if (!root.isConnected) return; // navigated away while it was read

  const classes = (derived && derived.model && derived.model.classes) || [];
  if (!classes.length) {
    body.innerHTML = `<div class="card empty"><h2>Nothing to read yet</h2>
      <p class="muted">No process in this application carries a data object, so there is nothing to derive.
      Draw a data object on a process and give an activity a data association, and the class it implies
      appears here.</p></div>`;
    return;
  }

  // The qualifications, above the drawing rather than under it. A derived picture
  // mistaken for a complete one is worse than no picture, so what could not be read is
  // said before the reader has formed a view — not in a footnote afterwards.
  const gaps = (derived && derived.gaps) || [];
  const general = gaps.filter((g) => !g.class);
  const perClass = gaps.filter((g) => g.class);

  body.innerHTML = `
    ${general.length ? `<div class="dm-gaps">
      <b>What this reading cannot see</b>
      <ul>${general.map((g) => `<li>${esc(g.note)}</li>`).join("")}</ul>
    </div>` : ""}
    <div class="dm-stage">
      <div class="og-canvas" id="dm-classes"></div>
      <div class="og-tools">
        <button type="button" class="icon-btn" data-dmtool="zoom-in" title="Zoom in" aria-label="Zoom in">+</button>
        <button type="button" class="icon-btn" data-dmtool="zoom-out" title="Zoom out" aria-label="Zoom out">−</button>
        <button type="button" class="icon-btn" data-dmtool="fit" title="Fit diagram" aria-label="Fit diagram">⊡</button>
      </div>
    </div>
    <div class="dm-side" id="dm-side"></div>`;

  const el = body.querySelector("#dm-classes");
  let uml;
  try {
    uml = (await loadCanvasBundle()).uml;
  } catch {
    el.innerHTML = `<p class="ops-empty err">Could not load the diagram canvas.</p>`;
    return;
  }
  if (!root.isConnected || !body.contains(el)) return;

  const side = body.querySelector("#dm-side");
  const byName = new Map(classes.map((c) => [c.name, c]));
  let shown = null;

  // Not editable: there is no document behind this to write to, and pretending
  // otherwise would invite an edit that goes nowhere.
  const classCanvas = new uml.ClassCanvas(el, {
    editable: false,
    onSelection: (bo) => renderSide(bo && bo.name),
  });
  canvases = { classes: classCanvas, lifecycle: null };
  classCanvas.render(derived.model);

  const stage = el.parentElement;
  stage.querySelector('[data-dmtool="zoom-in"]').addEventListener("click", () => canvases?.classes.zoom(1.2));
  stage.querySelector('[data-dmtool="zoom-out"]').addEventListener("click", () => canvases?.classes.zoom(1 / 1.2));
  stage.querySelector('[data-dmtool="fit"]').addEventListener("click", () => canvases?.classes.fit());

  renderSide(classes[0].name);

  // The side reads one class: what its processes write into it, what it cannot say
  // about that, and — the half no class diagram carries — the states it moves through.
  function renderSide(name) {
    const c = name && byName.get(name);
    if (!c) { side.innerHTML = ""; return; }
    if (shown === c.name) return; // already showing it; leave the drawing alone
    shown = c.name;
    if (canvases && canvases.lifecycle) {
      try { canvases.lifecycle.destroy(); } catch { /* gone */ }
      canvases.lifecycle = null;
    }

    const mine = perClass.filter((g) => g.class === c.name);
    const states = (c.lifecycle && c.lifecycle.states) || [];
    side.innerHTML = `
      <div class="dm-card">
        <div class="dm-card-head"><b>${esc(c.name)}</b>
          <span class="muted">${c.attributes.length} member${c.attributes.length === 1 ? "" : "s"} ·
            ${states.length} state${states.length === 1 ? "" : "s"}</span></div>
        ${mine.length ? `<ul class="dm-gaps-inline">${mine.map((g) => `<li>${esc(g.note)}</li>`).join("")}</ul>` : ""}
        ${states.length ? `<div class="dm-stage small">
          <div class="og-canvas" id="dm-lifecycle"></div>
        </div>` : `<p class="muted">No process writes a data state on this class, so it has no life to draw.
          A data object's <b>Data state</b> in the Modeler is what makes one.</p>`}
      </div>`;
    if (!states.length) return;

    const lcEl = side.querySelector("#dm-lifecycle");
    const lc = new uml.StateCanvas(lcEl, { editable: false });
    if (canvases) canvases.lifecycle = lc;
    lc.render(c.lifecycle);
  }
}

export { destroyCanvases as cleanupDerivedModel };
