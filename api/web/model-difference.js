// The difference between what is built and what is planned
// (ADR-0310).
//
// ADR-0301 settled that Atlas holds two statements about the same subject and must not
// merge them: the derived model is what is *built*, the authored one is what is
// *wanted*, and their difference is the work not yet done. This reads that difference.
//
// A list rather than a marked-up drawing, and deliberately: ADR-0301's own negative
// consequence is that the two pictures must not be confused, and painting one with the
// other's absences is that confusion made visual. A list can say which *document* each
// row is about, which is the thing a drawing cannot.
//
// Nothing here writes. It is a reading over a derivation and a vocabulary, exactly as
// the "as built" view beside it is a reading over the processes.

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// What a finding is about, as one short phrase. The note carries the sentence; this is
// the label that lets a reader scan a column.
const WHAT = { class: "class", member: "member", state: "state", transition: "transition" };

const where = (f) => f.kind === "transition" ? `${f.from} → ${f.to}` : (f.name || f.class);

// One side's rows, grouped by class. Grouping is the whole reason this is readable: a
// class with six unbuilt states is one subject, not six.
function sideHTML(findings, { empty }) {
  if (!findings.length) return `<p class="md-none">${esc(empty)}</p>`;
  const byClass = new Map();
  for (const f of findings) {
    if (!byClass.has(f.class)) byClass.set(f.class, []);
    byClass.get(f.class).push(f);
  }
  return [...byClass.entries()].map(([cls, rows]) => `<div class="md-class">
    <div class="md-class-head"><b>${esc(cls)}</b>
      <span class="muted">${rows.length} ${rows.length === 1 ? "finding" : "findings"}</span></div>
    <ul class="md-rows">${rows.map((f) => `<li>
      <span class="md-what">${esc(WHAT[f.kind] || f.kind)}</span>
      <span class="md-where">${esc(where(f))}</span>
      <span class="md-note">${esc(f.note)}</span>
    </li>`).join("")}</ul></div>`).join("");
}

export async function mountModelDifference(root, { api, applicationId, application }) {
  const appName = (application && application.name) || applicationId;

  root.innerHTML = `<div class="md-root">
    <div class="between">
      <div>
        <h1>Planned against built · ${esc(appName)}</h1>
        <p class="muted" style="margin:0">What this application's processes carry, against what its
          information model says they should. The difference is the work not yet done — it is not drift,
          and nothing here changes either document.</p>
      </div>
      <div style="display:flex; gap:8px">
        <a class="btn neutral" href="#/data/derived/${encodeURIComponent(applicationId)}">As built →</a>
        <a class="btn neutral" href="#/data">Information model →</a>
      </div>
    </div>
    <div id="md-body"><p class="muted">Reading both…</p></div>
  </div>`;

  const body = root.querySelector("#md-body");
  let diff;
  try {
    diff = await api("GET", `/api/v1/infomodel/difference?applicationId=${encodeURIComponent(applicationId)}`);
  } catch (e) {
    body.innerHTML = `<div class="card empty"><p>Could not read it: ${esc(e.message)}</p></div>`;
    return;
  }
  if (!root.isConnected) return; // navigated away while it was read

  // Nothing has been planned, so nothing is missing from the plan. A first-time reader
  // would otherwise meet a wall of findings that are only the absence of a document
  // they have not started.
  if (!diff.modeled) {
    body.innerHTML = `<div class="card empty"><h2>Nothing is planned yet</h2>
      <p class="muted">This application has no information model, so there is nothing for its processes
      to differ from. Draw one under <a href="#/data">Data</a> — or read
      <a href="#/data/derived/${encodeURIComponent(applicationId)}">what the processes already carry</a>
      and start from that.</p></div>`;
    return;
  }

  const planned = diff.planned || [];
  const built = diff.built || [];

  body.innerHTML = `
    <div class="md-cols">
      <section class="md-col planned">
        <h2>Planned, not built <span class="md-count">${planned.length}</span></h2>
        <p class="md-meaning">In the model and in no process. This is the backlog: a decision taken and
          not yet implemented. It is the direction the pair exists for, and it is <b>not</b> a defect.</p>
        ${sideHTML(planned, { empty: "Everything the model plans, the processes build." })}
      </section>
      <section class="md-col built">
        <h2>Built, not described <span class="md-count">${built.length}</span></h2>
        <p class="md-meaning">In the processes and in no model. Usually that means write it down —
          occasionally it means a process is doing something nobody agreed to, which is the more
          interesting reading.</p>
        ${sideHTML(built, { empty: "Everything the processes build, the model describes." })}
      </section>
    </div>
    ${(diff.excluded || []).length ? `<div class="md-excluded">
      <b>What this comparison never looked at</b>
      <p class="muted">Derivation cannot see these, so a difference in them would be a fact about
        derivation rather than about your system — and each would sit on every class for ever. A short
        list above is therefore not a clean bill.</p>
      <ul>${diff.excluded.map((x) => `<li>${esc(x)}</li>`).join("")}</ul>
    </div>` : ""}`;
}
