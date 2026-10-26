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

import { t, plural } from "./i18n.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// nameList renders up to a handful of names and says how many more there are, so a
// preview of a large application stays one screen.
function nameList(items, label = (x) => x.name || x.id) {
  const MAX = 8;
  const shown = items.slice(0, MAX).map((x) => `<li>${esc(label(x))}</li>`).join("");
  const more = items.length > MAX ? `<li class="muted">${esc(t("appDelete.more", { n: items.length - MAX }))}</li>` : "";
  return `<ul class="purge-names">${shown}${more}</ul>`;
}

// purgeSummaryHTML renders a deletion preview: what the purge removes, then what
// outside the application depends on it. Exported for the test.
export function purgeSummaryHTML(p) {
  const rows = [];
  const versions = p.processes.reduce((n, pr) => n + pr.versions.length, 0);
  if (p.processes.length) {
    const running = p.runningInstances
      ? ` — <b>${esc(plural("appDelete.running", p.runningInstances))}</b>`
      : "";
    const counted = t("appDelete.processes", {
      processes: plural("appDelete.process", p.processes.length),
      versions: plural("appDelete.version", versions),
    });
    rows.push(`<li>${esc(counted)}${running}
      ${nameList(p.processes, (pr) => pr.name ? `${pr.name} (${pr.processId})` : pr.processId)}</li>`);
  }
  const simple = [
    [p.decisionDeployments, "appDelete.decisionDeployment", (d) => `${d.name || d.key}: ${d.decisions.join(", ")}`],
    [p.drafts, "appDelete.draft"],
    [p.decisionDrafts, "appDelete.decisionDraft"],
    [p.decisionReferences, "appDelete.reference"],
    [p.forms, "appDelete.form"],
    [p.scenarios, "appDelete.scenario"],
    [p.panoramaModels, "appDelete.panorama"],
    [p.informationModels, "appDelete.infoModel"],
  ];
  for (const [items, key, label] of simple) {
    if (items && items.length) rows.push(`<li>${esc(plural(key, items.length))}${nameList(items, label)}</li>`);
  }
  const removed = rows.length
    ? `<ul class="purge-list">${rows.join("")}</ul>`
    : `<p class="muted">${esc(t("appDelete.nothingElse"))}</p>`;

  const outside = [];
  for (const b of p.catalogueProducts || []) {
    outside.push(`<li>${plural("appDelete.product", b.products.length, { products: esc(b.products.join(", ")), process: esc(b.processId) })}</li>`);
  }
  for (const o of p.orderLines || []) {
    outside.push(`<li>${plural("appDelete.orderLine", o.lines, { process: esc(o.processId) })}</li>`);
  }
  for (const d of p.dependentProcesses || []) {
    outside.push(`<li>${t("appDelete.dependent", { process: esc(d.processId), version: d.version, decision: esc(d.decisionId) })}</li>`);
  }
  const breaks = outside.length
    ? `<h3 class="purge-h">${esc(t("appDelete.outside"))}</h3><ul class="purge-list">${outside.join("")}</ul>`
    : "";
  return `<h3 class="purge-h">${esc(t("appDelete.removed"))}</h3>${removed}${breaks}`;
}

// openApplicationDeleteDialog asks how to delete an application. loadPreview fetches
// GET .../deletion-preview and is called only when the person picks "everything".
// Resolves to "keep", "purge", or null on cancel, Escape or a click outside.
export function openApplicationDeleteDialog({ name, loadPreview }) {
  return new Promise((resolve) => {
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal purge-modal" role="dialog" aria-modal="true" aria-label="${esc(t("appDelete.ok"))}">
        <div class="modal-head">
          <h2>${esc(t("appDelete.title", { name }))}</h2>
          <button type="button" class="icon-btn" data-x aria-label="${esc(t("common.close"))}" title="${esc(t("common.close"))}">✕</button>
        </div>
        <div class="modal-body">
          <label class="purge-choice">
            <input type="radio" name="purge-mode" value="keep" checked>
            <span><b>${esc(t("appDelete.keep"))}</b><br>
            <span class="muted small">${esc(t("appDelete.keepHint"))}</span></span>
          </label>
          <label class="purge-choice">
            <input type="radio" name="purge-mode" value="purge">
            <span><b>${esc(t("appDelete.purge"))}</b><br>
            <span class="muted small">${esc(t("appDelete.purgeHint"))}</span></span>
          </label>
          <div id="purge-detail" hidden></div>
        </div>
        <div class="modal-foot">
          <span></span>
          <span class="modal-actions">
            <button type="button" class="btn ghost" data-cancel>${esc(t("common.cancel"))}</button>
            <button type="button" class="btn danger" data-ok>${esc(t("appDelete.ok"))}</button>
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
        ok.textContent = t("appDelete.ok");
        return;
      }
      ok.textContent = t("appDelete.okPurge");
      ok.disabled = !preview || !!preview.refusal || preview.needsOperator || typed.trim() !== name.trim();
    };

    const showPreview = () => {
      if (preview.refusal) {
        detail.innerHTML = `<p class="purge-refusal">${esc(preview.refusal)}</p>`;
        return;
      }
      const role = preview.needsOperator
        ? `<p class="purge-refusal">${esc(t("appDelete.needsOperator"))}</p>`
        : "";
      detail.innerHTML = `${purgeSummaryHTML(preview)}${role}
        <p class="purge-warning">${esc(t("appDelete.warning"))}</p>
        <label class="field"><span>${t("appDelete.confirm", { name: esc(name) })}</span>
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
      detail.innerHTML = `<p class="muted">${esc(t("appDelete.reading"))}</p>`;
      try {
        preview = await loadPreview();
        showPreview();
      } catch (e) {
        detail.innerHTML = `<p class="purge-refusal">${esc(t("appDelete.readFailed", { error: e.message }))}</p>`;
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
