// The dialog behind deleting an application
// (ADR-0468).
//
// Deleting an application used to be a window.confirm that said its artifacts would
// be kept — and that was the only thing it could do. Somebody who wanted the
// application gone with everything it brought found fourteen processes, their forms
// and decisions under Not assigned, and had to remove them one at a time.
//
// So the dialog offers both: keep the contents (the ADR-0034 default), or delete
// everything. The second is irreversible and cancels running instances, so it shows
// the server's own preview of what goes and what outside the application stops
// working, and asks for the application's name to be typed before it runs — the
// friction the bulk terminate already asks for once the blast radius is large.
//
// It lives in its own module because app.js boots the whole console on import; here
// a test can drive it with a preview of its own.

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const count = (n, one, many) => `${n} ${n === 1 ? one : many}`;

// nameList renders up to a handful of names and says how many more there are, so a
// preview of a large application stays one screen.
function nameList(items, label = (x) => x.name || x.id) {
  const MAX = 8;
  const shown = items.slice(0, MAX).map((x) => `<li>${esc(label(x))}</li>`).join("");
  const more = items.length > MAX ? `<li class="muted">and ${items.length - MAX} more</li>` : "";
  return `<ul class="purge-names">${shown}${more}</ul>`;
}

// purgeSummaryHTML renders a deletion preview: what the purge removes, then what
// outside the application depends on it. Exported for the test.
export function purgeSummaryHTML(p) {
  const rows = [];
  const versions = p.processes.reduce((n, pr) => n + pr.versions.length, 0);
  if (p.processes.length) {
    const running = p.runningInstances
      ? ` — <b>${count(p.runningInstances, "running instance", "running instances")} will be cancelled</b>`
      : "";
    rows.push(`<li>${count(p.processes.length, "deployed process", "deployed processes")} (${count(versions, "version", "versions")})${running}
      ${nameList(p.processes, (pr) => pr.name ? `${pr.name} (${pr.processId})` : pr.processId)}</li>`);
  }
  const simple = [
    [p.decisionDeployments, "decision deployment", "decision deployments", (d) => `${d.name || d.key}: ${d.decisions.join(", ")}`],
    [p.drafts, "BPMN draft", "BPMN drafts"],
    [p.decisionDrafts, "decision draft", "decision drafts"],
    [p.decisionReferences, "DMN reference", "DMN references"],
    [p.forms, "form", "forms"],
    [p.scenarios, "test scenario", "test scenarios"],
    [p.panoramaModels, "Panorama model", "Panorama models"],
    [p.informationModels, "information model", "information models"],
  ];
  for (const [items, one, many, label] of simple) {
    if (items && items.length) rows.push(`<li>${count(items.length, one, many)}${nameList(items, label)}</li>`);
  }
  const removed = rows.length
    ? `<ul class="purge-list">${rows.join("")}</ul>`
    : `<p class="muted">The application holds nothing else; only the application itself is deleted.</p>`;

  const outside = [];
  for (const b of p.catalogueProducts || []) {
    outside.push(`<li>Catalogue ${b.products.length === 1 ? "product" : "products"} <b>${esc(b.products.join(", "))}</b> ${b.products.length === 1 ? "binds" : "bind"} <b>${esc(b.processId)}</b>: ordering ${b.products.length === 1 ? "it" : "them"} will fail.</li>`);
  }
  for (const o of p.orderLines || []) {
    outside.push(`<li>${count(o.lines, "order line", "order lines")} can still start <b>${esc(o.processId)}</b> (a return or a change): ${o.lines === 1 ? "it" : "they"} will fail.</li>`);
  }
  for (const d of p.dependentProcesses || []) {
    outside.push(`<li>Process <b>${esc(d.processId)}</b> v${d.version} of another application evaluates decision <b>${esc(d.decisionId)}</b>: its business rule task will fail.</li>`);
  }
  const breaks = outside.length
    ? `<h3 class="purge-h">Outside this application, this stops working</h3><ul class="purge-list">${outside.join("")}</ul>`
    : "";
  return `<h3 class="purge-h">Deleted permanently</h3>${removed}${breaks}`;
}

// openApplicationDeleteDialog asks how to delete an application. loadPreview fetches
// GET .../deletion-preview and is called only when the person picks "everything".
// Resolves to "keep", "purge", or null on cancel, Escape or a click outside.
export function openApplicationDeleteDialog({ name, loadPreview }) {
  return new Promise((resolve) => {
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal purge-modal" role="dialog" aria-modal="true" aria-label="Delete application">
        <div class="modal-head">
          <h2>Delete application “${esc(name)}”</h2>
          <button type="button" class="icon-btn" data-x aria-label="Close" title="Close">✕</button>
        </div>
        <div class="modal-body">
          <label class="purge-choice">
            <input type="radio" name="purge-mode" value="keep" checked>
            <span><b>Delete the application only</b><br>
            <span class="muted small">Its BPMN, DMN and form artifacts and its deployments are kept and become Not assigned. Panorama models must be deleted first.</span></span>
          </label>
          <label class="purge-choice">
            <input type="radio" name="purge-mode" value="purge">
            <span><b>Delete the application and everything in it</b><br>
            <span class="muted small">Deployments, running instances, decisions, drafts, forms and models — permanently.</span></span>
          </label>
          <div id="purge-detail" hidden></div>
        </div>
        <div class="modal-foot">
          <span></span>
          <span class="modal-actions">
            <button type="button" class="btn ghost" data-cancel>Cancel</button>
            <button type="button" class="btn danger" data-ok>Delete application</button>
          </span>
        </div>
      </div>`;
    document.body.appendChild(ov);

    const detail = ov.querySelector("#purge-detail");
    const ok = ov.querySelector("[data-ok]");
    let mode = "keep";
    let preview = null;
    let typed = "";

    // The button is armed only when the chosen mode can go through: always for keep;
    // for purge, once the preview has loaded, names no refusal, needs no role the
    // caller lacks, and the application's name has been typed.
    const arm = () => {
      if (mode === "keep") {
        ok.disabled = false;
        ok.textContent = "Delete application";
        return;
      }
      ok.textContent = "Delete everything permanently";
      ok.disabled = !preview || !!preview.refusal || preview.needsOperator || typed.trim() !== name.trim();
    };

    const showPreview = () => {
      if (preview.refusal) {
        detail.innerHTML = `<p class="purge-refusal">${esc(preview.refusal)}</p>`;
        return;
      }
      const role = preview.needsOperator
        ? `<p class="purge-refusal">Cancelling the running instances needs the operator role, which you do not hold. Ask an operator or an administrator to do this.</p>`
        : "";
      detail.innerHTML = `${purgeSummaryHTML(preview)}${role}
        <p class="purge-warning">This cannot be undone. Nothing listed above can be restored afterwards.</p>
        <label class="field"><span>Type <b>${esc(name)}</b> to confirm</span>
          <input id="purge-confirm" type="text" autocomplete="off" spellcheck="false"></label>`;
      const input = detail.querySelector("#purge-confirm");
      input.addEventListener("input", () => { typed = input.value; arm(); });
      input.focus();
    };

    const choose = async (value) => {
      mode = value;
      detail.hidden = mode !== "purge";
      arm();
      if (mode !== "purge" || preview) return;
      detail.innerHTML = `<p class="muted">Reading what the application holds…</p>`;
      try {
        preview = await loadPreview();
        showPreview();
      } catch (e) {
        detail.innerHTML = `<p class="purge-refusal">Could not read what the application holds: ${esc(e.message)}</p>`;
      }
      arm();
    };

    let settled = false;
    const finish = (result) => {
      if (settled) return;
      settled = true;
      ov.remove();
      document.removeEventListener("keydown", onKey);
      resolve(result);
    };
    const onKey = (e) => { if (e.key === "Escape") { e.preventDefault(); finish(null); } };
    document.addEventListener("keydown", onKey);
    ov.addEventListener("mousedown", (e) => { if (e.target === ov) finish(null); });
    ov.querySelector("[data-x]").addEventListener("click", () => finish(null));
    ov.querySelector("[data-cancel]").addEventListener("click", () => finish(null));
    for (const radio of ov.querySelectorAll('input[name="purge-mode"]')) {
      radio.addEventListener("change", () => choose(radio.value));
    }
    ok.addEventListener("click", () => { if (!ok.disabled) finish(mode); });
    arm();
    ok.focus();
  });
}

// forceReason strips the server's pointer to the force parameter from a refusal: the
// dialog that shows it offers the forced delete itself, in words rather than as a
// query parameter.
export function forceReason(message) {
  return String(message || "").split(" — or delete with force=true")[0];
}
