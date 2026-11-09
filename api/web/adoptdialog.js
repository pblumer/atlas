// The dialog that files what belongs to no application under one
// (ADR-draft-artifacts-belong-to-an-application).
//
// Everything new belongs to an application, so what is listed under Not assigned is
// what was written before that rule: it is not filed anywhere silently, because which
// application a process belongs to is its owner's decision. This is where that
// decision is made, for many artifacts at once — one request to
// POST /api/v1/applications/{id}/adopt, which moves nothing unless it can move all.
//
// It lives in its own module, as the pick dialog does, so a test can reach it
// without booting the console.

import { t, plural } from "./i18n.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// KINDS is the order the dialog lists the kinds in, each with how a row of it reads.
// The key is both the field of GET /api/v1/unassigned and of the adopt request.
export const KINDS = [
  { key: "processes", id: (p) => p.processId, label: (p) => p.name || p.processId,
    sub: (p) => plural("adopt.versions", (p.versions || []).length) +
      (p.filedVersions ? " · " + plural("adopt.filedVersions", p.filedVersions) : ""),
    blocked: (p) => !!p.filedVersions },
  { key: "drafts", id: (d) => d.id, label: (d) => d.name || d.id, sub: (d) => d.id },
  { key: "forms", id: (f) => f.id, label: (f) => f.name || f.id, sub: (f) => f.id },
  { key: "decisionReferences", id: (r) => r.id, label: (r) => r.name || r.id, sub: () => "" },
  { key: "decisionDrafts", id: (d) => d.id, label: (d) => d.name || d.id, sub: () => "" },
  { key: "decisionDeployments", id: (d) => d.key, label: (d) => (d.decisions || []).join(", ") || String(d.key),
    sub: (d) => "#" + d.key },
];

// adoptRequest is the body of the adopt request for what is checked in root.
export function adoptRequest(root) {
  const body = {};
  for (const k of KINDS) {
    const ids = [...root.querySelectorAll(`input[data-kind="${k.key}"]:checked`)].map((el) =>
      k.key === "decisionDeployments" ? Number(el.value) : el.value);
    if (ids.length) body[k.key] = ids;
  }
  return body;
}

// countUnassigned is how many rows GET /api/v1/unassigned listed.
export function countUnassigned(unassigned) {
  return KINDS.reduce((n, k) => n + ((unassigned && unassigned[k.key]) || []).length, 0);
}

function sectionHTML(kind, rows) {
  if (!rows || !rows.length) return "";
  const items = rows.map((r) => {
    const id = kind.id(r);
    const blocked = kind.blocked ? kind.blocked(r) : false;
    return `<li><label class="adopt-row${blocked ? " muted" : ""}">
      <input type="checkbox" data-kind="${esc(kind.key)}" value="${esc(id)}"${blocked ? " disabled" : ""}>
      <span><b>${esc(kind.label(r))}</b> <span class="muted small">${esc(kind.sub(r))}</span></span></label></li>`;
  }).join("");
  return `<fieldset class="adopt-kind">
      <legend><label><input type="checkbox" data-all="${esc(kind.key)}"> ${esc(t("adopt.kind." + kind.key))} (${rows.length})</label></legend>
      <ul class="adopt-list">${items}</ul>
    </fieldset>`;
}

// openAdoptDialog shows what belongs to no application and files the checked part of
// it under the chosen one. apps are the applications the caller may write to, as
// GET /api/v1/applications lists them. Resolves to true once something was adopted,
// false when the dialog was closed without.
export function openAdoptDialog({ api, apps, unassigned, toast }) {
  return new Promise((resolve) => {
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    const total = countUnassigned(unassigned);
    ov.innerHTML = `
      <div class="modal" role="dialog" aria-modal="true" aria-label="${esc(t("adopt.title"))}" style="max-width:640px">
        <div class="modal-head">
          <h2>${esc(t("adopt.title"))}</h2>
          <button type="button" class="icon-btn" data-x aria-label="${esc(t("common.close"))}" title="${esc(t("common.close"))}">✕</button>
        </div>
        <div class="modal-body">
          <p class="muted">${esc(t("adopt.intro"))}</p>
          ${apps.length ? `<label class="field"><span>${esc(t("adopt.application"))}</span>
            <select id="adopt-app">${apps.map((p) => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join("")}</select></label>`
            : `<p class="warn">${esc(t("application.pick.none"))}</p>`}
          <div class="adopt-kinds" style="max-height:50vh;overflow:auto">
            ${total ? KINDS.map((k) => sectionHTML(k, unassigned[k.key])).join("") : `<p class="muted">${esc(t("adopt.nothing"))}</p>`}
          </div>
          <div class="adopt-problems" aria-live="polite"></div>
        </div>
        <div class="modal-foot">
          <span class="muted small" id="adopt-count"></span>
          <span class="modal-actions">
            <button type="button" class="btn ghost" data-cancel>${esc(t("common.cancel"))}</button>
            <button type="button" class="btn" data-ok disabled>${esc(t("adopt.ok"))}</button>
          </span>
        </div>
      </div>`;
    document.body.appendChild(ov);

    const ok = ov.querySelector("[data-ok]");
    const count = ov.querySelector("#adopt-count");
    const problems = ov.querySelector(".adopt-problems");
    const refresh = () => {
      const n = ov.querySelectorAll("input[data-kind]:checked").length;
      count.textContent = n ? plural("adopt.selected", n) : "";
      ok.disabled = !n || !apps.length;
    };
    ov.addEventListener("change", (e) => {
      const all = e.target.closest("input[data-all]");
      if (all) {
        for (const el of ov.querySelectorAll(`input[data-kind="${all.dataset.all}"]:not(:disabled)`)) el.checked = all.checked;
      }
      refresh();
    });

    let settled = false;
    const finish = (result) => {
      if (settled) return;
      settled = true;
      ov.remove();
      document.removeEventListener("keydown", onKey);
      resolve(result);
    };
    const onKey = (e) => { if (e.key === "Escape") { e.preventDefault(); finish(false); } };
    document.addEventListener("keydown", onKey);
    ov.addEventListener("mousedown", (e) => { if (e.target === ov) finish(false); });
    ov.querySelector("[data-x]").addEventListener("click", () => finish(false));
    ov.querySelector("[data-cancel]").addEventListener("click", () => finish(false));
    ok.addEventListener("click", async () => {
      const appId = ov.querySelector("#adopt-app").value;
      const body = adoptRequest(ov);
      ok.disabled = true;
      problems.innerHTML = "";
      try {
        const res = await api("POST", `/api/v1/applications/${encodeURIComponent(appId)}/adopt`, body);
        const moved = KINDS.reduce((n, k) => n + ((res && res[k.key]) || []).length, 0);
        const app = apps.find((p) => p.id === appId);
        if (toast) toast(t("adopt.done", { n: moved, application: app ? app.name : appId }), "ok");
        finish(true);
      } catch (e) {
        // A 409 names every artifact that could not be adopted; nothing was moved.
        const list = (e && e.body && e.body.problems) || [];
        problems.innerHTML = `<p class="warn">${esc(t("adopt.failed", { error: e.message || String(e) }))}</p>` +
          (list.length ? `<ul>${list.map((p) => `<li>${esc(p.kind)} ${esc(p.id)}: ${esc(p.problem)}</li>`).join("")}</ul>` : "");
        refresh();
      }
    });
    refresh();
    (ov.querySelector("#adopt-app") || ov.querySelector("[data-cancel]")).focus();
  });
}
