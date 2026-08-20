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
      <div class="inc-actions">
        <button class="btn neutral sm" data-fix-vars="${esc(String(inc.processInstanceKey))}" data-inc="${esc(String(inc.elementInstanceKey))}"
          title="Correct the instance's variables before retrying — a retry alone repeats whatever failed">&#9998; Fix variables&hellip;</button>
        <button class="btn neutral sm" data-resolve="${esc(String(inc.elementInstanceKey))}"
          title="Clear the incident and hand the job one more attempt">&#8635; Resolve &amp; retry</button>
        <button class="btn neutral sm" data-complete="${esc(String(inc.elementInstanceKey))}"
          title="Finish this task by hand and let the process continue — recorded with your name and the reason you give">&#10003; Complete manually&hellip;</button></div>
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

// fixVariablesFlow is the other half of resolving: a retry alone repeats whatever
// failed, so an operator who has read the message usually has to correct the data
// first. It opens the instance's variables, writes what was changed through the
// audited operator override (ADR-0098 — every write records who made it, which the
// replay then shows beside the value), and optionally resolves in the same step.
//
// The variables are edited as JSON rather than as a form: the endpoint sets or
// overwrites exactly the keys it is given, and a value can be an object or an array,
// which no flat field grid represents honestly. Removing a key here leaves it
// untouched — this corrects values, it does not delete them.
//
// Resolves "resolved" when the incident was also cleared, "saved" when only the
// variables were written, and null when nothing happened.
export async function fixVariablesFlow({ api, toast, incident }) {
  const instance = incident.processInstanceKey;
  let current = {};
  try {
    current = (await api("GET", `/api/v1/instances/${encodeURIComponent(instance)}/variables`)) || {};
  } catch (e) {
    toast("Could not read the instance's variables: " + (e && e.message ? e.message : e), "warn");
    return null;
  }
  const choice = await askVariables(incident, current);
  if (!choice) return null;

  try {
    await api("POST", `/api/v1/instances/${encodeURIComponent(instance)}/variables`, { variables: choice.variables });
  } catch (e) {
    // Writing live process state is admin-only when auth is on (ADR-0098); say that
    // rather than leaving a bare 403 to be interpreted.
    const msg = (e && e.message ? e.message : String(e));
    toast(/403|forbidden/i.test(msg) ? "Changing variables needs an admin account" : "Could not set the variables: " + msg, "warn");
    return null;
  }
  if (!choice.retry) {
    toast("Variables updated — the incident is still open", "ok");
    return "saved";
  }
  return (await resolveIncidentQuick({ api, toast, key: incident.elementInstanceKey })) ? "resolved" : "saved";
}

// askVariables opens the editor and resolves to {variables, retry}, or null if the
// operator cancelled. It refuses to submit invalid JSON rather than letting the
// request fail server-side with the change already typed and lost.
function askVariables(inc, current) {
  return new Promise((resolve) => {
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal confirm-modal inc-vars-modal" role="dialog" aria-modal="true" aria-labelledby="inc-vars-title">
        <div class="modal-head"><h2 id="inc-vars-title">Fix variables &middot; instance ${esc(String(inc.processInstanceKey))}</h2></div>
        <div class="modal-body">
          ${inc.message ? `<p class="inc-modal-msg">${esc(inc.message)}</p>` : ""}
          <p class="muted" style="margin:0 0 8px">Correct the values the retry will run against. Keys you leave out stay as they are; this sets and overwrites, it never deletes. Every change is recorded with your name and shown in the replay.</p>
          <textarea id="inc-vars" class="inc-vars-json" spellcheck="false" rows="12"></textarea>
          <p class="err" id="inc-vars-err" style="margin:6px 0 0"></p>
        </div>
        <div class="modal-foot">
          <button class="btn neutral" data-vars-cancel>Cancel</button>
          <button class="btn neutral" data-vars-save>Save only</button>
          <button class="btn" data-vars-go>Save &amp; retry</button>
        </div>
      </div>`;
    document.body.appendChild(ov);
    const ta = ov.querySelector("#inc-vars");
    const err = ov.querySelector("#inc-vars-err");
    ta.value = JSON.stringify(current, null, 2);
    const close = (value) => { ov.remove(); document.removeEventListener("keydown", onKey); resolve(value); };
    const onKey = (e) => { if (e.key === "Escape") close(null); };
    const submit = (retry) => {
      let parsed;
      try { parsed = JSON.parse(ta.value); }
      catch (e) { err.textContent = "Not valid JSON: " + (e && e.message ? e.message : e); return; }
      if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
        err.textContent = "Expected an object of variable names to values.";
        return;
      }
      close({ variables: parsed, retry });
    };
    document.addEventListener("keydown", onKey);
    ov.querySelector("[data-vars-cancel]").addEventListener("click", () => close(null));
    ov.querySelector("[data-vars-save]").addEventListener("click", () => submit(false));
    ov.querySelector("[data-vars-go]").addEventListener("click", () => submit(true));
    ov.addEventListener("click", (e) => { if (e.target === ov) close(null); });
    ta.focus();
  });
}

// completeManuallyFlow finishes a parked task by hand so the process can continue
// (ADR-0159). It is the escape hatch for work that was carried out outside the engine —
// the account really was created, the mail really was sent — where retrying only repeats
// whatever cannot succeed here. Because it forces a step the engine would not have taken
// on its own, the server requires a reason: the completion is recorded as an operator
// action (who, when, which element, why) in append-only audit history that the instance
// timeline and the replay surface, and any outputs entered here are written as the job's
// result, carrying the same attribution the "Fix variables" path does. Resolves true when
// the task was completed.
export async function completeManuallyFlow({ api, toast, incident }) {
  // The audit record hangs off the job. An incident on a task carries its job key
  // directly; fall back to the instance's job list for one that does not.
  let jobKey = incident.jobKey || 0;
  if (!jobKey) {
    try {
      const jobs = (await api("GET", `/api/v1/instances/${incident.processInstanceKey}/jobs`)) || [];
      const mine = jobs.find((j) => String(j.elementInstanceKey) === String(incident.elementInstanceKey));
      jobKey = mine ? mine.key : 0;
    } catch (e) {
      toast("could not read the task: " + e.message, "err");
      return false;
    }
  }
  if (!jobKey) {
    toast("This incident has no job to complete — it is not parked on a task.", "err");
    return false;
  }
  const answer = await askCompletion(incident);
  if (!answer) return false;
  try {
    await api("POST", `/api/v1/jobs/${jobKey}/complete`, { reason: answer.reason, variables: answer.variables });
    toast("Task completed manually — recorded in the instance's audit trail", "ok");
    return true;
  } catch (e) {
    toast("complete failed: " + e.message, "err");
    return false;
  }
}

// askCompletion opens the manual-completion dialog and resolves to {reason, variables},
// or null if the operator cancelled. The reason is mandatory here as well as on the
// server, so the requirement is visible before the request rather than as a rejection
// after it; the outputs are optional and must be a JSON object when given.
function askCompletion(inc) {
  return new Promise((resolve) => {
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal confirm-modal inc-vars-modal" role="dialog" aria-modal="true" aria-labelledby="inc-done-title">
        <div class="modal-head"><h2 id="inc-done-title">Complete manually &middot; ${esc(inc.elementId || "task")}</h2></div>
        <div class="modal-body">
          ${inc.message ? `<p class="inc-modal-msg">${esc(inc.message)}</p>` : ""}
          <p class="muted" style="margin:0 0 8px">This finishes the task as a worker would and lets the process continue.
            It is recorded as a manual completion — your name, the time, and the reason below — and shown on this step in the replay.</p>
          <label class="field"><span>Reason <b>(required)</b></span>
            <input type="text" id="inc-done-reason" spellcheck="false" placeholder="e.g. account created by hand in AD, ticket INC-4711"/></label>
          <label class="field"><span>Output variables (optional JSON)</span>
            <textarea id="inc-done-vars" class="inc-vars-json" spellcheck="false" rows="7">{}</textarea></label>
          <p class="err" id="inc-done-err" style="margin:6px 0 0"></p>
        </div>
        <div class="modal-foot">
          <button class="btn neutral" data-done-cancel>Cancel</button>
          <button class="btn" data-done-go>Complete task</button>
        </div>
      </div>`;
    document.body.appendChild(ov);
    const reasonEl = ov.querySelector("#inc-done-reason");
    const varsEl = ov.querySelector("#inc-done-vars");
    const err = ov.querySelector("#inc-done-err");
    const close = (value) => { ov.remove(); document.removeEventListener("keydown", onKey); resolve(value); };
    const onKey = (e) => { if (e.key === "Escape") close(null); };
    const submit = () => {
      const reason = (reasonEl.value || "").trim();
      if (!reason) {
        err.textContent = "A reason is required — it is what makes the manual completion auditable.";
        reasonEl.focus();
        return;
      }
      let parsed = {};
      const raw = (varsEl.value || "").trim();
      if (raw) {
        try { parsed = JSON.parse(raw); }
        catch (e) { err.textContent = "Output variables are not valid JSON: " + (e && e.message ? e.message : e); return; }
        if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
          err.textContent = "Expected an object of variable names to values.";
          return;
        }
      }
      close({ reason, variables: parsed });
    };
    document.addEventListener("keydown", onKey);
    ov.querySelector("[data-done-cancel]").addEventListener("click", () => close(null));
    ov.querySelector("[data-done-go]").addEventListener("click", submit);
    ov.addEventListener("click", (e) => { if (e.target === ov) close(null); });
    reasonEl.focus();
  });
}

// bindIncidentActions wires one surface's incident block: the two actions on every
// row, delegated on a container that re-renders under them. `resolve` picks which
// resolve this surface offers — "quick" beside a diagram (one click, one attempt,
// ADR-0150), "ask" in the incidents table, where an operator triaging a list may want
// a bigger budget. onChanged runs after anything actually changed, so the caller can
// re-poll. `incidents()` returns the rows currently on screen.
export function bindIncidentActions(root, { api, toast, incidents, onChanged, resolve = "quick", within = "" }) {
  root.addEventListener("click", async (ev) => {
    const sel = "[data-resolve], [data-fix-vars], [data-complete]";
    const btn = ev.target.closest(within ? sel.split(", ").map((x) => `${within} ${x}`).join(", ") : sel);
    if (!btn) return;
    ev.preventDefault();
    const key = btn.dataset.resolve || btn.dataset.inc || btn.dataset.complete;
    const incident = (incidents() || []).find((i) => String(i.elementInstanceKey) === String(key));
    if (!incident) return;
    btn.disabled = true;
    try {
      const changed = btn.dataset.complete
        ? await completeManuallyFlow({ api, toast, incident })
        : btn.dataset.fixVars
        ? !!(await fixVariablesFlow({ api, toast, incident }))
        : resolve === "ask"
          ? await resolveIncidentFlow({ api, toast, incident })
          : await resolveIncidentQuick({ api, toast, key: incident.elementInstanceKey });
      if (changed && onChanged) await onChanged();
    } finally {
      btn.disabled = false;
    }
  });
}
