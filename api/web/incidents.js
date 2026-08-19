// Incident presentation, shared by every operator surface that shows one
// (ADR-0150). An incident is the same fact wherever it appears — the Operations
// incidents table, the live view's diagram and side panel, the replay's Details
// panel — so the pill, the card, and above all the *resolve* interaction live here
// rather than being re-invented (and drifting) three times.
//
// The one behaviour worth stating: resolving asks for the retry budget the
// re-activated job gets. A timer incident has no job and re-arms against the
// instance's current variables, so the count is meaningless for it — the dialog
// says so instead of pretending otherwise.

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// incidentKind is what parked: a service-task job whose retries ran out, or a
// job-less timer whose FEEL schedule stopped resolving (ADR-0064/0111). The server
// says so directly; the jobKey fallback keeps an older response readable.
const incidentKind = (inc) => inc.type || (inc.jobKey ? "job" : "timer");

// incidentPill renders the job/timer cause as the colored pill the incidents table
// established: red for a parked job, amber for a timer that stopped resolving.
export function incidentPill(inc) {
  return incidentKind(inc) === "job"
    ? `<span class="pill err"><span class="dot"></span>job</span>`
    : `<span class="pill warn"><span class="dot"></span>timer</span>`;
}

// fmtRaised renders an incident's raisedAt (unix nanoseconds, frozen into the
// event) as a local timestamp.
export const fmtRaised = (ns) => (ns ? new Date(ns / 1e6).toLocaleString() : "—");

// incidentCardHTML is one incident as a block: what stalled, why, and the two
// actions an operator wants — resolve it, or open the instance it belongs to.
// `label` is the element's diagram label (the caller has the rendered diagram and
// knows it better than the API does); `instance` adds the instance key and its
// replay link, which the live view's "all instances" scope needs and a
// single-instance panel does not.
export function incidentCardHTML(inc, { label = "", instance = false } = {}) {
  const name = label || inc.elementId || `element ${inc.elementInstanceKey}`;
  const where = instance
    ? `<a class="inc-inst" href="#/operations/i/${inc.processInstanceKey}" title="Replay this instance step by step">&#9654; ${esc(String(inc.processInstanceKey))}</a>`
    : "";
  return `<div class="inc-card">
    <div class="inc-card-head">
      <span class="inc-card-name" title="${esc(inc.elementId || "")}">${esc(name)}</span>
      ${incidentPill(inc)}${where}
    </div>
    <div class="inc-card-msg">${esc(inc.message || "No message recorded.")}</div>
    <div class="inc-card-foot">
      <span class="inc-card-when" title="When the incident was raised">${esc(fmtRaised(inc.raisedAt))}</span>
      <button class="btn sm" type="button" data-resolve-inc="${inc.elementInstanceKey}">Resolve&hellip;</button>
    </div>
  </div>`;
}

// askResolveRetries opens the resolve dialog and resolves to the retry budget the
// operator granted, or null if they cancelled. A modal rather than window.prompt:
// the count needs the explanation that a timer incident ignores it, and prompt has
// nowhere to put one.
function askResolveRetries(inc) {
  return new Promise((resolve) => {
    const timer = incidentKind(inc) === "timer";
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal confirm-modal" role="dialog" aria-modal="true" aria-label="Resolve incident">
        <div class="modal-head"><h2>Resolve incident</h2></div>
        <div class="modal-body">
          <p class="muted" style="margin:0 0 10px">${timer
            ? "This timer re-arms against the instance's current variables. If its schedule still doesn't resolve, the incident is raised again."
            : "The parked job goes back on the activatable index and a worker retries it. If the cause is unfixed it fails again and a new incident is raised."}</p>
          ${inc.message ? `<p class="inc-modal-msg">${esc(inc.message)}</p>` : ""}
          <label class="field"><span>Retries to grant${timer ? " (ignored by a timer incident)" : ""}</span>
            <input id="inc-retries" type="number" min="1" step="1" value="1" ${timer ? "disabled" : ""}/></label>
        </div>
        <div class="modal-foot">
          <button class="btn neutral" data-inc-cancel>Cancel</button>
          <button class="btn" data-inc-go>Resolve</button>
        </div>
      </div>`;
    document.body.appendChild(ov);
    const input = ov.querySelector("#inc-retries");
    const close = (value) => { ov.remove(); document.removeEventListener("keydown", onKey); resolve(value); };
    const onKey = (e) => {
      if (e.key === "Escape") close(null);
      if (e.key === "Enter" && document.body.contains(ov)) go();
    };
    const go = () => {
      const n = timer ? 1 : parseInt(input.value, 10);
      if (!Number.isInteger(n) || n <= 0) { input.focus(); return; }
      close(n);
    };
    document.addEventListener("keydown", onKey);
    ov.querySelector("[data-inc-cancel]").addEventListener("click", () => close(null));
    ov.querySelector("[data-inc-go]").addEventListener("click", go);
    ov.addEventListener("click", (e) => { if (e.target === ov) close(null); });
    (timer ? ov.querySelector("[data-inc-go]") : input).focus();
    if (input && !timer) input.select();
  });
}

// resolveIncidentFlow is the whole operator action behind every "Resolve…" button:
// ask for the budget, POST it, report the outcome. Resolves true when the incident
// was resolved (so the caller can refresh), false when it was cancelled or failed.
export async function resolveIncidentFlow({ api, toast, incident }) {
  const retries = await askResolveRetries(incident);
  if (retries === null) return false;
  try {
    await api("POST", `/api/v1/incidents/${encodeURIComponent(incident.elementInstanceKey)}/resolve`, { retries });
    toast("Incident resolved", "ok");
    return true;
  } catch (e) {
    toast(e.message || "Resolve failed", "warn");
    return false;
  }
}
