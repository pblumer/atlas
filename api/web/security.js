// Console → Administration → Security: who acts as whom, and by which policy
// (ADR-0475 §10, §12, §13, §17).
//
// Five parts, in the order an administrator migrates through them:
//
//   - **Account linking.** The app registration people link their Microsoft
//     accounts through. Start flags outrank it and are only shown; otherwise it is
//     set up here. The half at Microsoft cannot be done from a browser tab of this
//     server, so the card says what to make there and shows the redirect URI to copy.
//   - **Readiness.** Every configured Worker, whether its actions are governed and
//     how, and where its credential is. Shadow is said to be what it is — decisions
//     recorded, nothing blocked — every time it is shown, because a page that let
//     shadow read like protection would be the most dangerous thing on it.
//   - **Policies.** Drafts are edited as their JSON document and checked by the
//     server on save; publishing makes the draft the next immutable version.
//   - **Simulator.** Asks the published set, or one draft in place of its version,
//     a question an administrator imagines. Its answer is labelled a simulation:
//     nothing is recorded and nothing is enforced.
//   - **Evidence.** The decisions and approvals an instance recorded, read back from
//     the log.
//
// The server decides every rule; this page only asks and shows.

import { copyText } from "./clipboard.js";
import { fmtDateTime } from "./datefmt.js";
import { t } from "./i18n.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// Timestamps from the server are Unix nanoseconds.
const when = (ns) => (ns ? fmtDateTime(Math.floor(ns / 1e6)) : "—");

// MODES and ENFORCEMENTS are the identity policy's words (api/workeridentity.go).
export const MODES = ["service", "delegated-fixed", "delegated-per-job"];
export const ENFORCEMENTS = ["shadow", "enforce"];

// EFFECTS are authz's four answers. Only Permit lets an action run.
const effectPill = (effect) => {
  const cls = effect === "Permit" ? "ok" : effect === "Deny" ? "err" : "warn";
  return `<span class="pill ${cls}" data-effect="${esc(effect)}"><span class="dot"></span>${esc(effect)}</span>`;
};

const enforcementPill = (row) => {
  if (!row.governed) return `<span class="pill off" data-enforcement="off"><span class="dot"></span>${esc(t("security.enforcement.off"))}</span>`;
  if (row.enforcement === "shadow") return `<span class="pill warn" data-enforcement="shadow"><span class="dot"></span>${esc(t("security.enforcement.shadow"))}</span>`;
  return `<span class="pill ok" data-enforcement="enforce"><span class="dot"></span>${esc(t("security.enforcement.enforce"))}</span>`;
};

// noteOf says what a row's setting means, in the reader's language. The server says
// the same in English (readinessRow.note); the cases are its cases.
function noteOf(row) {
  if (!row.governed) return row.governable ? t("security.note.legacy") : t("security.note.notGovernable");
  if (row.enforcement === "shadow") return t("security.note.shadow");
  if (row.mode === "service") return t("security.note.cooperative");
  return t("security.note.credential");
}

const credentialOf = (row) => {
  if (row.credentialKind === "brokered") return t("security.credential.brokered");
  if (row.credentialKind === "worker") return t("security.credential.worker", { ref: row.credentialRef || "" });
  return t("security.credential.none");
};

// ---------- Account linking ----------

const LINK_PATH = "/api/v1/security/linking/microsoft";

function linkStatus(v) {
  const pill = (cls, text) => `<span class="pill ${cls}" id="link-status" data-source="${esc(v.source)}"><span class="dot"></span>${esc(text)}</span>`;
  if (!v.available) return pill("off", t("security.link.status.unavailable"));
  if (v.active) return pill("ok", v.source === "flags" ? t("security.link.status.flags") : t("security.link.status.console"));
  if (v.source === "flags") return pill("warn", t("security.link.status.flagsIncomplete"));
  return pill("off", t("security.link.status.off"));
}

function linkRedirect(v) {
  return `<div style="margin:10px 0">
    <div class="muted" style="font-size:12px">${esc(t("security.link.redirect"))}</div>
    <div class="row" style="gap:8px; align-items:center; flex-wrap:wrap">
      <code id="link-redirect">${esc(v.redirectUri || "—")}</code>
      ${v.redirectUri ? `<button class="btn ghost" type="button" data-act="copy-redirect">${esc(t("security.link.copy"))}</button>` : ""}
    </div>
    ${v.redirectUriDerived ? `<p class="muted" style="font-size:12px; margin:4px 0 0">${esc(t("security.link.redirectDerived"))}</p>` : ""}
  </div>`;
}

function linkBody(v) {
  if (!v.available) return `<p class="muted" id="link-unavailable">${esc(t("security.link.unavailable"))}</p>`;
  const stranded = v.stranded
    ? `<div class="warn-note" id="link-stranded" role="status">${esc(t("security.link.stranded", { n: v.stranded }))}</div>` : "";
  if (v.source === "flags") {
    const row = (label, value) => `<tr><td class="muted">${esc(label)}</td><td><code>${esc(value || "—")}</code></td></tr>`;
    return `${stranded}<p class="muted" id="link-flags">${esc(t("security.link.flags"))}</p>
      ${v.active ? "" : `<p class="muted" id="link-flags-incomplete">${esc(t("security.link.flagsIncompleteHint"))}</p>`}
      <table><tbody>
        ${row(t("security.link.tenant"), v.tenantId)}
        ${row(t("security.link.client"), v.clientId)}
        ${row(t("security.link.scopes"), v.scopes)}
        ${row(t("security.link.secret"), v.clientSecretSet ? t("security.link.secretSet") : t("security.link.secretUnset"))}
      </tbody></table>
      ${linkRedirect(v)}`;
  }
  const input = (name, label, value, attrs) => `<label class="field" style="min-width:320px">${esc(label)}
      <input name="${name}" value="${esc(value || "")}" autocomplete="off" spellcheck="false" ${attrs || ""} /></label>`;
  return `${stranded}
    <p style="margin:10px 0 4px"><b>${esc(t("security.link.steps"))}</b></p>
    <ol id="link-steps" class="muted" style="margin:0 0 6px 18px; padding:0">
      <li>${esc(t("security.link.step1"))}</li>
      <li>${esc(t("security.link.step2"))}</li>
      <li>${esc(t("security.link.step3"))}</li>
      <li>${esc(t("security.link.step4"))}</li>
    </ol>
    ${linkRedirect(v)}
    <form id="link-form" autocomplete="off">
      <div class="row" style="gap:12px; flex-wrap:wrap; align-items:flex-end">
        ${input("tenantId", t("security.link.tenant"), v.tenantId, `placeholder="00000000-0000-0000-0000-000000000000" required`)}
        ${input("clientId", t("security.link.client"), v.clientId, `placeholder="00000000-0000-0000-0000-000000000000" required`)}
      </div>
      <div class="row" style="gap:12px; flex-wrap:wrap; align-items:flex-end; margin-top:8px">
        ${input("scopes", t("security.link.scopes"), v.scopes, `placeholder="https://graph.microsoft.com/Sites.ReadWrite.All" required`)}
        <label class="field" style="min-width:320px">${esc(t("security.link.secret"))}
          <input name="clientSecret" type="password" autocomplete="new-password" spellcheck="false"
            placeholder="${esc(v.clientSecretSet ? t("security.link.secretKept") : "")}" ${v.clientSecretSet ? "" : "required"} /></label>
      </div>
      <p class="muted" style="font-size:12px; margin:6px 0">${esc(t("security.link.scopesHint"))} ${esc(t("security.link.secretHint"))}</p>
      <div class="row" style="gap:8px">
        <button class="btn" type="submit">${esc(t("security.link.save"))}</button>
        ${v.source === "console" ? `<button class="btn ghost danger" type="button" data-act="remove-link">${esc(t("security.link.remove"))}</button>` : ""}
      </div>
    </form>`;
}

function paintLinkSetup(box, v) {
  box._setup = v;
  box.querySelector("#link-status-slot").innerHTML = linkStatus(v);
  box.querySelector("#link-body").innerHTML = linkBody(v);
}

async function loadLinkSetup(api, root) {
  const box = root.querySelector("#sec-linking");
  let v;
  try { v = await api("GET", LINK_PATH); } catch (e) {
    box.querySelector("#link-body").innerHTML = `<div class="warn-note">${esc(e.message)}</div>`;
    return;
  }
  // A server from before this card answers with something else; say nothing rather
  // than paint a form it would refuse.
  if (!v || typeof v.source !== "string") { box.hidden = true; return; }
  paintLinkSetup(box, v);
}

function wireLinkSetup(api, toast, root, onLinkingChanged) {
  const box = root.querySelector("#sec-linking");
  const changed = () => { if (onLinkingChanged) onLinkingChanged(); };
  box.addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-act]");
    if (!btn) return;
    const v = box._setup || {};
    if (btn.dataset.act === "copy-redirect") {
      if (await copyText(v.redirectUri || "")) toast(t("security.link.copied"));
      else toast(t("security.link.copyFailed"), "warn");
      return;
    }
    if (btn.dataset.act !== "remove-link") return;
    const ask = v.connections ? t("security.link.removeConfirmConnections", { n: v.connections }) : t("security.link.removeConfirm");
    if (!window.confirm(ask)) return;
    btn.disabled = true;
    try {
      paintLinkSetup(box, await api("DELETE", LINK_PATH));
      toast(t("security.link.removed"));
      changed();
    } catch (err) {
      btn.disabled = false;
      toast(err.message, "err");
    }
  });
  box.addEventListener("submit", async (e) => {
    const form = e.target.closest("#link-form");
    if (!form) return;
    e.preventDefault();
    const v = box._setup || {};
    const f = form.elements;
    const body = { tenantId: f.tenantId.value.trim(), clientId: f.clientId.value.trim(), scopes: f.scopes.value.trim() };
    // The secret travels only when one was typed: empty keeps the one held.
    if (f.clientSecret.value) body.clientSecret = f.clientSecret.value;
    const moved = (v.tenantId && body.tenantId.toLowerCase() !== v.tenantId) || (v.clientId && body.clientId.toLowerCase() !== v.clientId);
    if (moved && v.connections && !window.confirm(t("security.link.changeConfirm", { n: v.connections }))) return;
    const btn = form.querySelector("button[type=submit]");
    btn.disabled = true;
    try {
      paintLinkSetup(box, await api("PUT", LINK_PATH, body));
      toast(t("security.link.saved"));
      changed();
    } catch (err) {
      btn.disabled = false;
      toast(err.message, "err");
    }
  });
}

// ---------- Readiness ----------

function readinessRow(row) {
  const edit = row.governable
    ? `<button class="btn ghost" type="button" data-act="identity">${esc(t("security.readiness.edit"))}</button>`
    : "";
  return `<tr data-worker="${esc(row.id)}" data-name="${esc(row.worker)}">
    <td><b>${esc(row.worker)}</b><div class="muted" style="font-size:12px">${esc(row.kind)}</div></td>
    <td>${row.governed ? `<code>${esc(row.mode)}</code>${row.actAs ? `<div class="muted" style="font-size:12px">${esc(t("security.readiness.actAs", { who: row.actAs }))}</div>` : ""}${row.connectionId ? `<div class="muted" style="font-size:12px"><code>${esc(row.connectionId)}</code></div>` : ""}` : `<span class="muted">${esc(t("security.readiness.legacy"))}</span>`}</td>
    <td>${enforcementPill(row)}</td>
    <td>${esc(credentialOf(row))}</td>
    <td class="muted" style="font-size:13px" data-note>${esc(noteOf(row))}</td>
    <td style="text-align:right">${edit}</td>
  </tr>`;
}

function identityForm(row) {
  const opt = (v, cur) => `<option value="${esc(v)}"${v === cur ? " selected" : ""}>${esc(v)}</option>`;
  return `<tr data-identity-form="${esc(row.id)}"><td colspan="6">
    <form class="sec-identity" style="padding:8px 0">
      <div class="row" style="gap:12px; flex-wrap:wrap; align-items:flex-end">
        <label class="field">${esc(t("security.identity.mode"))}
          <select name="mode">${MODES.map((m) => opt(m, row.governed ? row.mode : "delegated-per-job")).join("")}</select></label>
        <label class="field">${esc(t("security.identity.enforcement"))}
          <select name="enforcement">${ENFORCEMENTS.map((m) => opt(m, row.governed ? row.enforcement : "enforce")).join("")}</select></label>
        <label class="field" data-for="delegated-fixed">${esc(t("security.identity.connection"))}
          <input name="connectionId" value="${esc(row.connectionId || "")}" placeholder="con_…" autocomplete="off" spellcheck="false" /></label>
      </div>
      <p class="muted" data-mode-hint style="margin:6px 0"></p>
      <div class="row" style="gap:8px">
        <button class="btn" type="submit">${esc(t("security.identity.save"))}</button>
        ${row.governed ? `<button class="btn ghost danger" type="button" data-act="clear">${esc(t("security.identity.clear"))}</button>` : ""}
        <button class="btn ghost" type="button" data-act="cancel">${esc(t("security.identity.cancel"))}</button>
      </div>
    </form></td></tr>`;
}

function paintIdentityForm(form) {
  const mode = form.elements.mode.value;
  form.querySelector("[data-for='delegated-fixed']").hidden = mode !== "delegated-fixed";
  // A delegated Worker is enforced or nothing: the server refuses shadow for it.
  const enf = form.elements.enforcement;
  if (mode !== "service") enf.value = "enforce";
  enf.disabled = mode !== "service";
  form.querySelector("[data-mode-hint]").textContent = t("security.identity.hint." + mode);
}

async function loadReadiness(api, root) {
  const box = root.querySelector("#sec-readiness");
  let r;
  try { r = await api("GET", "/api/v1/security/readiness"); } catch (e) {
    box.innerHTML = `<div class="warn-note">${esc(e.message)}</div>`;
    return;
  }
  const rows = r.workers || [];
  const governedShadow = rows.filter((w) => w.governed && w.enforcement === "shadow").length;
  box.innerHTML = `${r.policySetError ? `<div class="warn-note" id="sec-policy-error" role="alert">${esc(t("security.readiness.policyError", { error: r.policySetError }))}</div>` : ""}
    ${governedShadow ? `<div class="warn-note" id="sec-shadow-warning" role="status">${esc(t("security.readiness.shadowWarning"))}</div>` : ""}
    <table data-dt-key="security-readiness">
      <thead><tr><th>${esc(t("security.col.worker"))}</th><th>${esc(t("security.col.mode"))}</th>
        <th>${esc(t("security.col.enforcement"))}</th><th>${esc(t("security.col.credential"))}</th>
        <th>${esc(t("security.col.meaning"))}</th><th></th></tr></thead>
      <tbody id="sec-readiness-rows">${rows.map(readinessRow).join("")
        || `<tr><td colspan="6" class="muted" style="padding:14px 18px">${esc(t("security.readiness.empty"))}</td></tr>`}</tbody>
    </table>`;
  box._rows = rows;
}

function wireReadiness(api, toast, root) {
  const box = root.querySelector("#sec-readiness");
  box.addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-act]");
    if (!btn) return;
    const act = btn.dataset.act;
    if (act === "identity") {
      const tr = btn.closest("tr");
      const row = (box._rows || []).find((w) => w.id === tr.dataset.worker);
      if (!row || box.querySelector(`[data-identity-form="${CSS.escape(row.id)}"]`)) return;
      tr.insertAdjacentHTML("afterend", identityForm(row));
      const form = box.querySelector(`[data-identity-form="${CSS.escape(row.id)}"] form`);
      paintIdentityForm(form);
      form.addEventListener("change", () => paintIdentityForm(form));
      return;
    }
    const formRow = btn.closest("[data-identity-form]");
    if (!formRow) return;
    if (act === "cancel") { formRow.remove(); return; }
    if (act === "clear") {
      if (!window.confirm(t("security.identity.clearConfirm"))) return;
      try {
        await api("DELETE", `/api/v1/configured-workers/${encodeURIComponent(formRow.dataset.identityForm)}/identity`);
        toast(t("security.identity.cleared"));
        await loadReadiness(api, root);
      } catch (err) { toast(err.message, "err"); }
    }
  });
  box.addEventListener("submit", async (e) => {
    const form = e.target.closest("form.sec-identity");
    if (!form) return;
    e.preventDefault();
    const id = form.closest("[data-identity-form]").dataset.identityForm;
    const mode = form.elements.mode.value;
    const body = { mode, enforcement: mode === "service" ? form.elements.enforcement.value : "enforce" };
    if (mode === "delegated-fixed") body.connectionId = form.elements.connectionId.value.trim();
    if (mode === "delegated-per-job") body.actAs = "initiator";
    try {
      await api("PUT", `/api/v1/configured-workers/${encodeURIComponent(id)}/identity`, body);
      toast(body.enforcement === "shadow" ? t("security.identity.savedShadow") : t("security.identity.saved"));
      await loadReadiness(api, root);
    } catch (err) { toast(err.message, "err"); }
  });
}

// ---------- Policies ----------

// policyTemplate is where a new policy starts: the smallest document the server
// accepts, permitting one harmless operation, so the reader edits a working policy
// rather than guessing the format.
const policyTemplate = (id) => JSON.stringify({
  id,
  description: "",
  target: { workerTypes: ["sharepoint"] },
  ruleCombining: "first-applicable",
  rules: [{ id: "read", effect: "Permit", reasonCode: "READ", when: { operations: ["get-file"] } }],
}, null, 2);

function policyRow(p) {
  const state = !p.current
    ? `<span class="pill off" data-state="draft"><span class="dot"></span>${esc(t("security.policies.unpublished"))}</span>`
    : p.enabled
      ? `<span class="pill ok" data-state="enabled"><span class="dot"></span>${esc(t("security.policies.enabled"))}</span>`
      : `<span class="pill off" data-state="disabled"><span class="dot"></span>${esc(t("security.policies.disabled"))}</span>`;
  return `<tr data-policy="${esc(p.id)}">
    <td><b>${esc(p.id)}</b></td>
    <td>${p.current ? `v${esc(p.current)}` : "—"}</td>
    <td>${state}</td>
    <td>${p.draft ? `<span class="chip" data-draft>${esc(t("security.policies.hasDraft"))}</span>` : ""}
      ${(p.draftWarnings || []).length ? `<span class="pill warn"><span class="dot"></span>${esc(t("security.policies.warnings", { n: p.draftWarnings.length }))}</span>` : ""}</td>
    <td class="muted" style="font-size:13px">${esc(when(p.updatedAt))}${p.updatedBy ? ` · ${esc(p.updatedBy)}` : ""}</td>
    <td style="text-align:right; white-space:nowrap">
      <button class="btn ghost" type="button" data-act="edit">${esc(t("security.policies.edit"))}</button>
      ${p.current ? `<button class="btn ghost" type="button" data-act="toggle">${esc(p.enabled ? t("security.policies.disable") : t("security.policies.enable"))}</button>` : ""}
      ${p.current ? `<button class="btn ghost" type="button" data-act="versions">${esc(t("security.policies.versions"))}</button>` : ""}
    </td>
  </tr>`;
}

async function loadPolicies(api, root) {
  const box = root.querySelector("#sec-policies-rows");
  let list;
  try { list = (await api("GET", "/api/v1/security/policies")) || []; } catch (e) {
    box.innerHTML = `<tr><td colspan="6"><div class="warn-note">${esc(e.message)}</div></td></tr>`;
    return [];
  }
  box.innerHTML = list.map(policyRow).join("")
    || `<tr><td colspan="6" class="muted" style="padding:14px 18px">${esc(t("security.policies.empty"))}</td></tr>`;
  root._policies = list;
  const sel = root.querySelector("#sim-candidate");
  if (sel) {
    sel.innerHTML = `<option value="">${esc(t("security.sim.candidateNone"))}</option>` +
      list.filter((p) => p.draft).map((p) => `<option value="${esc(p.id)}">${esc(t("security.sim.candidateDraft", { id: p.id }))}</option>`).join("");
  }
  return list;
}

function openEditor(root, p) {
  const ed = root.querySelector("#sec-editor");
  const doc = p && p.draft ? JSON.stringify(p.draft, null, 2) : null;
  ed.hidden = false;
  ed.innerHTML = `<h3>${esc(p ? t("security.editor.titleOf", { id: p.id }) : t("security.editor.titleNew"))}</h3>
    <form id="sec-editor-form">
      <label class="field">${esc(t("security.editor.id"))}
        <input name="id" value="${esc(p ? p.id : "")}" ${p ? "readonly" : ""} autocomplete="off" spellcheck="false" placeholder="hr-documents" /></label>
      <label class="field">${esc(t("security.editor.document"))}
        <textarea name="doc" rows="18" spellcheck="false" style="font-family:var(--mono, monospace); font-size:13px">${esc(doc || "")}</textarea></label>
      <p class="muted" style="margin-top:-4px">${esc(t("security.editor.hint"))}</p>
      <div id="sec-editor-notes"></div>
      <div class="row" style="gap:8px; margin-top:10px">
        <button class="btn" type="submit">${esc(t("security.editor.save"))}</button>
        <button class="btn" type="button" data-act="publish" ${p && p.draft ? "" : "disabled"}>${esc(t("security.editor.publish"))}</button>
        <button class="btn ghost" type="button" data-act="close">${esc(t("security.editor.close"))}</button>
      </div>
    </form>`;
  const form = ed.querySelector("#sec-editor-form");
  if (!form.elements.doc.value) {
    // A versioned policy without a draft starts from its current version; a new one
    // from a skeleton. Either way the reader edits a document, never a blank page.
    form.elements.doc.value = policyTemplate(p ? p.id : "");
  }
  ed.scrollIntoView({ block: "nearest" });
  return form;
}

function wirePolicies(api, toast, root) {
  const listBox = root.querySelector("#sec-policies");
  const ed = root.querySelector("#sec-editor");
  root.querySelector("#sec-policy-new").addEventListener("click", () => openEditor(root, null));

  listBox.addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-act]");
    if (!btn) return;
    const id = btn.closest("tr").dataset.policy;
    const p = (root._policies || []).find((x) => x.id === id);
    if (!p) return;
    if (btn.dataset.act === "edit") {
      const form = openEditor(root, p);
      if (!p.draft && p.current) {
        try {
          const full = await api("GET", `/api/v1/security/policies/${encodeURIComponent(id)}`);
          const cur = (full.versions || []).find((v) => v.version === p.current);
          if (cur) form.elements.doc.value = JSON.stringify(cur.document, null, 2);
        } catch { /* the skeleton stands */ }
      }
    } else if (btn.dataset.act === "toggle") {
      if (p.enabled && !window.confirm(t("security.policies.disableConfirm", { id }))) return;
      try {
        await api("POST", `/api/v1/security/policies/${encodeURIComponent(id)}/enabled`, { enabled: !p.enabled });
        toast(p.enabled ? t("security.policies.disabledToast") : t("security.policies.enabledToast"));
        await loadPolicies(api, root);
      } catch (err) { toast(err.message, "err"); }
    } else if (btn.dataset.act === "versions") {
      await showVersions(api, root, id);
    }
  });

  ed.addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-act]");
    if (!btn) return;
    const form = ed.querySelector("#sec-editor-form");
    if (btn.dataset.act === "close") { ed.hidden = true; ed.innerHTML = ""; return; }
    if (btn.dataset.act === "publish") {
      const id = form.elements.id.value.trim();
      if (!window.confirm(t("security.editor.publishConfirm", { id }))) return;
      try {
        const out = await api("POST", `/api/v1/security/policies/${encodeURIComponent(id)}/publish`);
        toast(t("security.editor.published", { id, v: out.current }));
        ed.hidden = true; ed.innerHTML = "";
        await loadPolicies(api, root);
      } catch (err) { toast(err.message, "err"); }
    }
  });
  ed.addEventListener("submit", async (e) => {
    e.preventDefault();
    const form = e.target;
    const id = form.elements.id.value.trim();
    const notes = ed.querySelector("#sec-editor-notes");
    if (!id) { toast(t("security.editor.idRequired"), "warn"); return; }
    let doc;
    try { doc = JSON.parse(form.elements.doc.value); } catch (err) {
      notes.innerHTML = `<div class="warn-note">${esc(t("security.editor.badJSON", { error: err.message }))}</div>`;
      return;
    }
    // The document names its policy too; a skeleton left without one takes the
    // field's, and a different one is the server's to refuse.
    if (doc && typeof doc === "object" && !doc.id) doc.id = id;
    try {
      const out = await api("PUT", `/api/v1/security/policies/${encodeURIComponent(id)}/draft`, doc);
      const warnings = out.draftWarnings || [];
      notes.innerHTML = warnings.length
        ? `<div class="warn-note" data-warnings><b>${esc(t("security.editor.warningsTitle"))}</b><ul>${warnings.map((w) => `<li>${esc(w)}</li>`).join("")}</ul></div>`
        : `<p class="muted" data-saved>${esc(t("security.editor.savedClean"))}</p>`;
      form.elements.id.readOnly = true;
      ed.querySelector("button[data-act='publish']").disabled = false;
      toast(t("security.editor.saved"));
      await loadPolicies(api, root);
    } catch (err) {
      notes.innerHTML = `<div class="warn-note" data-refused><b>${esc(t("security.editor.refused"))}</b><div>${esc(err.message)}</div></div>`;
    }
  });
}

async function showVersions(api, root, id) {
  const ed = root.querySelector("#sec-editor");
  let full;
  try { full = await api("GET", `/api/v1/security/policies/${encodeURIComponent(id)}`); } catch (err) {
    ed.hidden = false;
    ed.innerHTML = `<div class="warn-note">${esc(err.message)}</div>`;
    return;
  }
  ed.hidden = false;
  ed.innerHTML = `<h3>${esc(t("security.versions.title", { id }))}</h3>
    <p class="muted">${esc(t("security.versions.immutable"))}</p>
    ${(full.versions || []).slice().reverse().map((v) => `<details data-version="${esc(v.version)}">
      <summary>v${esc(v.version)} · ${esc(when(v.publishedAt))}${v.publishedBy ? ` · ${esc(v.publishedBy)}` : ""}</summary>
      <pre style="white-space:pre-wrap; font-size:12px">${esc(JSON.stringify(v.document, null, 2))}</pre></details>`).join("")}
    <button class="btn ghost" type="button" data-act="close" style="margin-top:10px">${esc(t("security.editor.close"))}</button>`;
}

// ---------- Simulator ----------

const splitList = (s) => s.split(",").map((x) => x.trim()).filter(Boolean);

function simForm() {
  return `<form id="sim-form" novalidate>
    <div class="row" style="gap:12px; flex-wrap:wrap">
      <label class="field">${esc(t("security.sim.subject"))}
        <input name="subject" placeholder="usr_…" autocomplete="off" spellcheck="false" /></label>
      <label class="field">${esc(t("security.sim.roles"))}
        <input name="roles" placeholder="manager" autocomplete="off" spellcheck="false" /></label>
      <label class="field">${esc(t("security.sim.groups"))}
        <input name="groups" placeholder="grp_documents" autocomplete="off" spellcheck="false" /></label>
    </div>
    <p class="muted" style="margin-top:-4px">${esc(t("security.sim.subjectHint"))}</p>
    <div class="row" style="gap:12px; flex-wrap:wrap">
      <label class="field">${esc(t("security.sim.worker"))}
        <input name="worker" placeholder="sp-prod" autocomplete="off" spellcheck="false" /></label>
      <label class="field">${esc(t("security.sim.mode"))}
        <select name="mode">${MODES.map((m) => `<option${m === "delegated-per-job" ? " selected" : ""}>${esc(m)}</option>`).join("")}</select></label>
      <label class="field">${esc(t("security.sim.connection"))}
        <select name="connection">
          <option value="active">${esc(t("security.sim.connection.active"))}</option>
          <option value="reauth_required">${esc(t("security.sim.connection.reauth"))}</option>
          <option value="revoked">${esc(t("security.sim.connection.revoked"))}</option>
          <option value="">${esc(t("security.sim.connection.none"))}</option>
        </select></label>
    </div>
    <div class="row" style="gap:12px; flex-wrap:wrap">
      <label class="field">${esc(t("security.sim.operation"))}
        <input name="operation" placeholder="delete-file" autocomplete="off" spellcheck="false" /></label>
      <label class="field" style="flex:1; min-width:280px">${esc(t("security.sim.resource"))}
        <input name="resource" placeholder="sharepoint:site=contoso.sharepoint.com:/sites/hr;list=Documents;path=/a.docx" autocomplete="off" spellcheck="false" /></label>
    </div>
    <label class="field">${esc(t("security.sim.approvals"))}
      <textarea name="approvals" rows="3" spellcheck="false" placeholder='[{"id":"a1","approverId":"usr_bob","approverGroups":["grp_managers"],"outcome":"granted","decidedAt":0}]'></textarea></label>
    <p class="muted" style="margin-top:-4px">${esc(t("security.sim.approvalsHint"))}</p>
    <label class="field">${esc(t("security.sim.candidate"))}
      <select name="candidate" id="sim-candidate"><option value="">${esc(t("security.sim.candidateNone"))}</option></select></label>
    <button class="btn" type="submit" style="margin-top:10px">${esc(t("security.sim.run"))}</button>
  </form>
  <div id="sim-result"></div>`;
}

function simResult(r) {
  const obligations = (r.obligations || []).map((o) => `<li data-obligation="${esc(o.id)}">
      <b>${esc(o.type)}</b> (${esc(o.policyId)} v${esc(o.policyVersion)}) —
      ${o.satisfied ? esc(t("security.sim.obligationMet")) : esc(t("security.sim.obligationOpen", { reason: o.reason || "" }))}
      ${o.approverGroup ? ` · ${esc(t("security.sim.approverGroup", { group: o.approverGroup, n: o.approvals || 1 }))}` : ""}</li>`).join("");
  const trace = (r.trace || []).map((s) => `<tr><td><code>${esc(s.policyId)}</code> v${esc(s.policyVersion)}</td>
      <td>${esc(s.ruleId || "—")}</td><td>${effectPill(s.effect)}</td><td>${esc(s.reasonCode || "")}</td>
      <td class="muted" style="font-size:12px">${esc(s.note || "")}</td></tr>`).join("");
  return `<div class="card" id="sim-answer" data-simulation="${r.simulation ? "true" : "false"}" style="margin-top:14px; border-style:dashed">
    <div class="warn-note" id="sim-label" role="status"><b>${esc(t("security.sim.label"))}</b> ${esc(t("security.sim.labelText"))}</div>
    <div class="row" style="gap:12px; align-items:center; margin-top:10px">
      ${effectPill(r.effect)} <code data-reason>${esc(r.reasonCode)}</code>
      ${r.policyId ? `<span class="muted">${esc(t("security.sim.decidedBy", { id: r.policyId, v: r.policyVersion }))}</span>` : ""}
    </div>
    ${r.subjectResolved ? `<p class="muted" style="margin-top:8px">${esc(t("security.sim.resolved"))}</p>` : ""}
    ${(r.ignored || []).length ? `<div class="warn-note" data-ignored style="margin-top:8px">${esc(t("security.sim.ignored", { fields: r.ignored.join(", ") }))}</div>` : ""}
    ${obligations ? `<h4>${esc(t("security.sim.obligations"))}</h4><ul>${obligations}</ul>` : ""}
    ${trace ? `<h4>${esc(t("security.sim.trace"))}</h4><table><thead><tr><th>${esc(t("security.col.policy"))}</th><th>${esc(t("security.col.rule"))}</th>
      <th>${esc(t("security.col.effect"))}</th><th>${esc(t("security.col.reason"))}</th><th></th></tr></thead><tbody>${trace}</tbody></table>` : ""}
    <p class="muted" style="font-size:12px; margin-top:8px">${esc(t("security.sim.digest"))}: <code>${esc(r.digest || "")}</code></p>
  </div>`;
}

function wireSimulator(api, toast, root) {
  const form = root.querySelector("#sim-form");
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = form.elements;
    const subject = f.subject.value.trim();
    const worker = f.worker.value.trim();
    const body = {
      subject: { id: subject },
      identityMode: f.mode.value,
      worker,
      workerType: "sharepoint",
      operation: f.operation.value.trim(),
      resource: f.resource.value.trim(),
      initiatedBy: subject,
    };
    // Roles or groups typed here are taken as given; with neither, the server reads
    // the subject's real ones from the user store and says so.
    const roles = splitList(f.roles.value);
    const groups = splitList(f.groups.value);
    if (roles.length || groups.length) {
      body.subject.roles = roles;
      body.subject.groups = groups;
    }
    if (f.mode.value !== "service" && f.connection.value) {
      body.connection = {
        id: "con_simulated", ownerId: subject, status: f.connection.value,
        allowedWorkers: worker ? [worker] : [], lastInteractiveAuth: Date.now() * 1e6,
      };
    }
    if (f.approvals.value.trim()) {
      try { body.approvals = JSON.parse(f.approvals.value); } catch (err) {
        toast(t("security.sim.badApprovals", { error: err.message }), "warn");
        return;
      }
    }
    const candidate = f.candidate.value;
    if (candidate) {
      const p = (root._policies || []).find((x) => x.id === candidate);
      if (p && p.draft) body.policy = p.draft;
    }
    try {
      const r = await api("POST", "/api/v1/security/policies/simulate", body);
      root.querySelector("#sim-result").innerHTML = simResult(r);
    } catch (err) { toast(err.message, "err"); }
  });
}

// ---------- Evidence ----------

function evidenceTables(decisions, approvals) {
  const drows = decisions.map((d) => `<tr data-decision="${esc(d.decisionKey)}">
      <td data-sort="${d.decidedAt}">${esc(when(d.decidedAt))}</td>
      <td>${effectPill(d.effect)}${d.enforced ? "" : ` <span class="pill warn" data-shadow><span class="dot"></span>${esc(t("security.enforcement.shadow"))}</span>`}
        <div><code style="font-size:12px">${esc(d.reasonCode)}</code></div></td>
      <td>${esc(d.worker)}<div class="muted" style="font-size:12px">${esc(d.operation)} · ${esc(d.identityMode)}</div></td>
      <td style="font-size:12px"><code>${esc(d.resource)}</code></td>
      <td style="font-size:12px">${esc(t("security.evidence.who", { initiated: d.initiatedBy || "—", for: d.executedFor || "—", as: d.executedAs || "—" }))}</td>
      <td>${d.policyId ? `<code>${esc(d.policyId)}</code> v${esc(d.policyVersion)}` : "—"}</td>
      <td class="muted" style="font-size:12px">${esc(t("security.evidence.job", { job: d.jobKey, epoch: d.leaseEpoch }))}</td>
    </tr>`).join("");
  const arows = approvals.map((a) => `<tr data-approval="${esc(a.approvalKey)}">
      <td data-sort="${a.requestedAt}">${esc(when(a.requestedAt))}</td>
      <td>${esc(a.requestedBy)}</td>
      <td>${esc(a.approverGroup)} (${esc(a.approvals)})</td>
      <td>${esc(a.operation)}<div style="font-size:12px"><code>${esc(a.resource)}</code></div></td>
      <td>${(a.answers || []).map((x) => `<div>${esc(x.principal)}: ${esc(x.outcome || x.kind)} · ${esc(when(x.at))}</div>`).join("") || "—"}</td>
      <td>${a.open ? `<span class="pill warn"><span class="dot"></span>${esc(t("security.evidence.open"))}</span>` : `<span class="pill off"><span class="dot"></span>${esc(t("security.evidence.closed"))}</span>`}
        <div class="muted" style="font-size:12px">${esc(t("security.evidence.expires", { when: when(a.expiresAt) }))}</div></td>
    </tr>`).join("");
  return `<h3>${esc(t("security.evidence.decisions"))}</h3>
    ${drows ? `<table data-dt-key="security-decisions"><thead><tr><th>${esc(t("security.col.when"))}</th><th>${esc(t("security.col.effect"))}</th>
      <th>${esc(t("security.col.worker"))}</th><th>${esc(t("security.col.resource"))}</th><th>${esc(t("security.col.who"))}</th>
      <th>${esc(t("security.col.policy"))}</th><th>${esc(t("security.col.job"))}</th></tr></thead><tbody id="sec-decisions">${drows}</tbody></table>`
    : `<p class="muted" id="sec-no-decisions">${esc(t("security.evidence.noDecisions"))}</p>`}
    <h3 style="margin-top:16px">${esc(t("security.evidence.approvals"))}</h3>
    ${arows ? `<table data-dt-key="security-approvals"><thead><tr><th>${esc(t("security.col.when"))}</th><th>${esc(t("security.col.requestedBy"))}</th>
      <th>${esc(t("security.col.approvers"))}</th><th>${esc(t("security.col.action"))}</th><th>${esc(t("security.col.answers"))}</th><th></th></tr></thead>
      <tbody id="sec-approvals">${arows}</tbody></table>`
    : `<p class="muted" id="sec-no-approvals">${esc(t("security.evidence.noApprovals"))}</p>`}`;
}

function wireEvidence(api, toast, root) {
  const form = root.querySelector("#sec-evidence-form");
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const key = form.elements.instance.value.trim();
    if (!/^\d+$/.test(key)) { toast(t("security.evidence.keyRequired"), "warn"); return; }
    const out = root.querySelector("#sec-evidence");
    try {
      const [decisions, approvals] = await Promise.all([
        api("GET", `/api/v1/instances/${key}/authorizations`),
        api("GET", `/api/v1/instances/${key}/action-approvals`),
      ]);
      out.innerHTML = evidenceTables(decisions || [], approvals || []);
    } catch (err) { toast(err.message, "err"); }
  });
}

// ---------- The page ----------

export async function viewSecurity({ api, toast, view, isSuperseded, onLinkingChanged }) {
  // One call decides whether the page is for this reader at all: the readiness view
  // is an administrator's, like every route below it.
  try {
    await api("GET", "/api/v1/security/readiness");
  } catch (e) {
    if (isSuperseded()) return;
    if (e.status === 403 || e.status === 401) {
      view.innerHTML = `<div class="card"><h1>${esc(t("security.title"))}</h1>
        <p class="muted" id="sec-denied">${esc(t("security.denied"))}</p></div>`;
      return;
    }
    throw e;
  }
  if (isSuperseded()) return;
  view.innerHTML = `<div id="security">
    <div class="card">
      <h1>${esc(t("security.title"))}</h1>
      <p class="muted">${esc(t("security.intro"))}</p>
    </div>
    <div class="card" style="margin-top:18px" id="sec-linking">
      <div class="between"><h2>${esc(t("security.link.title"))}</h2><span id="link-status-slot"></span></div>
      <p class="muted">${esc(t("security.link.intro"))}</p>
      <div id="link-body"></div>
    </div>
    <div class="card" style="margin-top:18px">
      <h2>${esc(t("security.readiness.title"))}</h2>
      <p class="muted">${esc(t("security.readiness.intro"))}</p>
      <div id="sec-readiness"></div>
    </div>
    <div class="card" style="margin-top:18px" id="sec-policies">
      <div class="between"><h2>${esc(t("security.policies.title"))}</h2>
        <button class="btn" type="button" id="sec-policy-new">${esc(t("security.policies.new"))}</button></div>
      <p class="muted">${esc(t("security.policies.intro"))}</p>
      <table data-dt-key="security-policies">
        <thead><tr><th>${esc(t("security.col.policy"))}</th><th>${esc(t("security.col.version"))}</th>
          <th>${esc(t("security.col.state"))}</th><th>${esc(t("security.col.draft"))}</th>
          <th>${esc(t("security.col.updated"))}</th><th></th></tr></thead>
        <tbody id="sec-policies-rows"></tbody>
      </table>
      <div id="sec-editor" style="margin-top:14px" hidden></div>
    </div>
    <div class="card" style="margin-top:18px">
      <h2>${esc(t("security.sim.title"))}</h2>
      <p class="muted">${esc(t("security.sim.intro"))}</p>
      ${simForm()}
    </div>
    <div class="card" style="margin-top:18px">
      <h2>${esc(t("security.evidence.title"))}</h2>
      <p class="muted">${esc(t("security.evidence.intro"))}</p>
      <form id="sec-evidence-form" class="row" style="gap:8px; align-items:flex-end">
        <label class="field">${esc(t("security.evidence.instance"))}
          <input name="instance" inputmode="numeric" autocomplete="off" /></label>
        <button class="btn" type="submit">${esc(t("security.evidence.show"))}</button>
      </form>
      <div id="sec-evidence" style="margin-top:12px"></div>
    </div>
  </div>`;
  const root = document.getElementById("security");
  await Promise.all([loadLinkSetup(api, root), loadReadiness(api, root), loadPolicies(api, root)]);
  if (isSuperseded()) return;
  wireLinkSetup(api, toast, root, onLinkingChanged);
  wireReadiness(api, toast, root);
  wirePolicies(api, toast, root);
  wireSimulator(api, toast, root);
  wireEvidence(api, toast, root);
}
