// The incident interaction, in one place (ADR-0151). An incident is the same fact
// wherever an operator meets it — the Operations incidents table, the live view's
// side panel, the replay's Details tab — so the row, the pill, and the two resolve
// actions live here rather than being re-implemented (and drifting) per surface.
//
// Two resolve actions, deliberately: beside a diagram the operator has just read the
// message and wants the job to try again, so it is one click with a single attempt
// (ADR-0150); in the incidents table they are triaging a list and may want a bigger
// budget, so it asks — which also gives room to say that a timer incident re-arms and
// ignores the count, something window.prompt had nowhere to put.

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

// incidentRowHTML is one incident beside a diagram: where it is parked, why, and the
// one action that resumes it. `label` names the element (a caller holding the
// rendered diagram knows its label; the raw id is the fallback), and `showInstance`
// adds the instance it belongs to — which the live view's all-instances scope needs
// and a single-instance panel does not.
export function incidentRowHTML(inc, { label = "", showInstance = true } = {}) {
  const where = showInstance
    ? `<span class="muted">· instance ${esc(String(inc.processInstanceKey))}</span>`
    : "";
  return `<div class="inc-row">
      <div class="inc-where"><b>${esc(label || inc.elementId || "")}</b>
        ${where}
        ${inc.raisedAt ? `<span class="muted">· ${esc(fmtRaised(inc.raisedAt))}</span>` : ""}</div>
      <div class="inc-msg">${esc(inc.message || "(no message)")}</div>
      <div class="inc-actions"><button class="btn neutral sm" data-resolve="${esc(String(inc.elementInstanceKey))}"
        title="Clear the incident and hand the job one more attempt">&#8635; Resolve &amp; retry</button></div>
    </div>`;
}

// incidentPanelHTML wraps those rows in the block that leads the live view's
// variables panel and the replay's Details tab: a count, a way to the full list, and
// every incident in view.
export function incidentPanelHTML(list, { truncated = false, rows = "" } = {}) {
  if (!list.length) return "";
  return `<div class="vp-incidents">
    <div class="vp-head"><span class="vp-title">&#9888; ${list.length}${truncated ? "+" : ""} incident${list.length === 1 ? "" : "s"}</span>
      <span class="vp-actions"><a class="replay-link" href="#/operations/incidents" title="Every unresolved incident on this server">All incidents &#8599;</a></span></div>
    ${rows}</div>`;
}

// resolveIncidentQuick is the diagram-side action behind every "↻ Resolve & retry":
// clear the incident and re-activate its job with a fresh single attempt. One attempt
// is the right default from here — the operator has just read the message and fixed
// (or is testing) the cause, and a failure parks the token again with the new reason
// rather than burning a budget on the old one. Resolves true when it worked.
export async function resolveIncidentQuick({ api, toast, key }) {
  try {
    await api("POST", `/api/v1/incidents/${encodeURIComponent(key)}/resolve`, { retries: 1 });
    toast("Incident resolved — retrying the job", "ok");
    return true;
  } catch (e) {
    toast("Resolve failed: " + (e && e.message ? e.message : e), "err");
    return false;
  }
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

// resolveIncidentFlow is the triage action behind the incidents table's "Resolve…":
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
