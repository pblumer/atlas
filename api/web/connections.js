// A person's linked accounts and the delegated connections they authorize
// (ADR-0475 §5, §6).
//
// This page is where consent lives. Linking an account is signing in at the provider
// — Atlas never sees the password, and the provider's own MFA and Conditional Access
// apply — and a new connection allows no Worker at all: the person chooses, here,
// which Workers may act as them. Revoking deletes the credential for good.
//
// Nothing on this page ever holds a credential. The routes behind it answer with
// the public half of a connection only, and the page keeps nothing in storage, the
// URL or the console; TestTheConnectionsPageHoldsNoCredential holds it to that.

import { fmtDateTime } from "./datefmt.js";
import { t } from "./i18n.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// GOVERNABLE_KINDS are the Worker Types a connection can be used by in this build
// (governableKinds in api/workeridentity.go). The server decides regardless; the copy
// only keeps the page from offering a Worker that can never act through a connection.
export const GOVERNABLE_KINDS = ["sharepoint"];

// Timestamps from the server are Unix nanoseconds.
const when = (ns) => (ns ? fmtDateTime(Math.floor(ns / 1e6)) : "—");

const statusPill = (status) => {
  const cls = status === "active" ? "ok" : status === "revoked" ? "off" : "warn";
  return `<span class="pill ${cls}" data-status="${esc(status)}"><span class="dot"></span>${esc(t("connections.status." + status))}</span>`;
};

const providerName = (p) => (p === "microsoft" ? t("connections.provider.microsoft") : p);

// banner reads what the link flow's landing page was told (?linked= or ?link=failed).
// It reads the hash and never writes it: the query says what happened, nothing more.
function banner() {
  const q = (location.hash.split("?")[1] || "");
  const params = new URLSearchParams(q);
  if (params.get("linked")) {
    return `<div class="card" id="con-banner" data-banner="linked" role="status" style="margin-top:18px">
      <b>${esc(t("connections.linked.title"))}</b>
      <p class="muted" style="margin:6px 0 0">${esc(t("connections.linked.next"))}</p></div>`;
  }
  if (params.get("link") === "failed") {
    return `<div class="warn-note" id="con-banner" data-banner="failed" role="alert" style="margin-top:18px">
      <b>${esc(t("connections.failed.title"))}</b><div>${esc(t("connections.failed.hint"))}</div></div>`;
  }
  return "";
}

function identityRow(x) {
  return `<tr data-identity="${esc(x.id)}">
    <td><b>${esc(x.displayName || x.username || x.subject)}</b>
      ${x.username ? `<div class="muted" style="font-size:12px">${esc(x.username)}</div>` : ""}</td>
    <td>${esc(providerName(x.provider))}</td>
    <td><code title="${esc(t("connections.identity.keyTitle"))}">${esc(x.provider + ":" + (x.tenantId || "") + ":" + x.subject)}</code></td>
    <td data-sort="${x.linkedAt || 0}">${esc(when(x.linkedAt))}</td>
    <td data-sort="${x.lastAuthenticatedAt || 0}">${esc(when(x.lastAuthenticatedAt))}</td>
  </tr>`;
}

// workerPicker lists the Workers a person may allow. Workers already allowed but no
// longer offered (renamed, deleted, another kind) stay listed and checked, so saving
// never drops a consent the person did not see.
function workerPicker(conn, workers) {
  const offered = workers.filter((w) => GOVERNABLE_KINDS.includes(w.kind)).map((w) => w.name);
  const names = [...new Set([...offered, ...(conn.allowedWorkers || [])])].sort();
  if (!names.length) return `<div class="muted" data-workers-none>${esc(t("connections.workers.none"))}</div>`;
  const allowed = new Set(conn.allowedWorkers || []);
  return names.map((n) => `<label class="field inline" style="margin:2px 0">
      <input type="checkbox" name="worker" value="${esc(n)}"${allowed.has(n) ? " checked" : ""} />
      <span>${esc(n)}${offered.includes(n) ? "" : ` <span class="muted" style="font-size:12px">${esc(t("connections.workers.unknown"))}</span>`}</span></label>`).join("");
}

function connectionCard(conn, ctx) {
  const ident = ctx.identities.get(conn.identityId);
  const who = ident ? ident.displayName || ident.username || ident.subject : conn.identityKey;
  const revoked = conn.status === "revoked";
  const reauth = conn.status === "reauth_required";
  const linkStart = ctx.linkable.find((l) => l.provider === conn.provider);
  return `<div class="card" data-connection="${esc(conn.id)}" style="margin-top:12px">
    <div class="between">
      <div><b>${esc(providerName(conn.provider))}</b> · ${esc(who)}
        <div class="muted" style="font-size:12px"><code>${esc(conn.id)}</code></div></div>
      <div>${statusPill(conn.status)}</div>
    </div>
    ${reauth ? `<div class="warn-note" data-reauth style="margin-top:10px">${esc(t("connections.reauth.note"))}
      ${linkStart ? `<div style="margin-top:6px"><a class="btn" href="${esc(linkStart.start)}">${esc(t("connections.reauth.action"))}</a></div>` : ""}</div>` : ""}
    <div class="muted" style="font-size:13px; margin-top:8px">
      ${esc(t("connections.scopes"))}: ${(conn.scopes || []).map((s) => `<span class="chip">${esc(s)}</span>`).join(" ") || "—"}<br>
      ${esc(t("connections.created", { when: when(conn.createdAt) }))} ·
      ${esc(t("connections.lastSignIn", { when: when(conn.lastInteractiveAuthAt) }))}
      ${conn.lastRefreshAt ? ` · ${esc(t("connections.lastUse", { when: when(conn.lastRefreshAt) }))}` : ""}
    </div>
    ${revoked
    ? `<p class="muted" data-revoked style="margin-top:10px">${esc(t("connections.revokedAt", { when: when(conn.revokedAt) }))}${conn.revokeReason ? ` — ${esc(conn.revokeReason)}` : ""}</p>`
    : `<form data-workers-form style="margin-top:12px">
        <div class="field" style="margin-bottom:4px">${esc(t("connections.workers.title"))}</div>
        <p class="muted" style="margin:0 0 6px">${esc(t("connections.workers.hint"))}</p>
        ${workerPicker(conn, ctx.workers)}
        <div class="row" style="margin-top:10px; gap:8px">
          <button class="btn" type="submit">${esc(t("connections.workers.save"))}</button>
          <button class="btn ghost danger" type="button" data-act="revoke">${esc(t("connections.revoke"))}</button>
        </div>
      </form>`}
  </div>`;
}

export async function viewConnections({ api, toast, view, isSuperseded }) {
  let mine;
  try {
    mine = await api("GET", "/api/v1/me/connections");
  } catch (e) {
    if (isSuperseded()) return;
    const key = e.status === 503 ? "connections.unavailable" : e.status === 401 ? "connections.signIn" : "";
    if (key) {
      view.innerHTML = `<div class="card"><h1>${esc(t("connections.title"))}</h1>
        <p class="muted" id="con-unavailable">${esc(t(key))}</p></div>`;
      return;
    }
    throw e;
  }
  // The Workers a person may allow. Best-effort: without the list the page still
  // shows and keeps what is already allowed.
  let workers = [];
  try { workers = (await api("GET", "/api/v1/configured-workers")) || []; } catch { /* keep [] */ }
  if (isSuperseded()) return;

  const ctx = {
    identities: new Map((mine.identities || []).map((x) => [x.id, x])),
    linkable: mine.linkable || [],
    workers,
  };
  const conns = mine.connections || [];
  const link = ctx.linkable.length
    ? ctx.linkable.map((l) => `<a class="btn" data-link="${esc(l.provider)}" href="${esc(l.start)}">${esc(t("connections.link", { provider: providerName(l.provider) }))}</a>`).join(" ")
    : `<p class="muted" id="con-no-link">${esc(t("connections.link.unconfigured"))}</p>`;

  view.innerHTML = `<div id="connections">
    <div class="card">
      <h1>${esc(t("connections.title"))}</h1>
      <p class="muted">${esc(t("connections.intro"))}</p>
      <div class="row" style="margin-top:10px">${link}</div>
      <p class="muted" style="font-size:12px; margin-top:8px">${esc(t("connections.link.privacy"))}</p>
    </div>
    ${banner()}
    <div class="card" style="padding:0; margin-top:18px">
      <div style="padding:16px 18px 0"><h2>${esc(t("connections.identities.title"))}</h2></div>
      <table data-dt-key="identities">
        <thead><tr><th>${esc(t("connections.col.account"))}</th><th>${esc(t("connections.col.provider"))}</th>
          <th>${esc(t("connections.col.key"))}</th><th>${esc(t("connections.col.linked"))}</th>
          <th>${esc(t("connections.col.lastSignIn"))}</th></tr></thead>
        <tbody>${(mine.identities || []).map(identityRow).join("")
          || `<tr><td colspan="5" class="muted" style="padding:14px 18px">${esc(t("connections.identities.empty"))}</td></tr>`}</tbody>
      </table>
    </div>
    <h2 style="margin-top:22px">${esc(t("connections.list.title"))}</h2>
    <p class="muted">${esc(t("connections.list.intro"))}</p>
    <div id="con-list">${conns.map((c) => connectionCard(c, ctx)).join("")
      || `<p class="muted" id="con-empty">${esc(t("connections.list.empty"))}</p>`}</div>
  </div>`;

  const root = document.getElementById("connections");
  root.addEventListener("submit", async (e) => {
    const form = e.target.closest("form[data-workers-form]");
    if (!form) return;
    e.preventDefault();
    const card = form.closest("[data-connection]");
    const id = card.dataset.connection;
    const chosen = [...form.querySelectorAll("input[name=worker]:checked")].map((i) => i.value);
    const btn = form.querySelector("button[type=submit]");
    btn.disabled = true;
    try {
      const updated = await api("PUT", `/api/v1/connections/${encodeURIComponent(id)}/workers`, { workers: chosen });
      const at = conns.findIndex((c) => c.id === id);
      if (at >= 0) conns[at] = updated;
      card.outerHTML = connectionCard(updated, ctx);
      toast(chosen.length ? t("connections.workers.saved") : t("connections.workers.savedNone"));
    } catch (err) {
      btn.disabled = false;
      toast(err.message, "err");
    }
  });
  root.addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-act='revoke']");
    if (!btn) return;
    const card = btn.closest("[data-connection]");
    const id = card.dataset.connection;
    if (!window.confirm(t("connections.revoke.confirm"))) return;
    btn.disabled = true;
    try {
      const updated = await api("POST", `/api/v1/connections/${encodeURIComponent(id)}/revoke`, { reason: "revoked by its owner in the Console" });
      const at = conns.findIndex((c) => c.id === id);
      if (at >= 0) conns[at] = updated;
      card.outerHTML = connectionCard(updated, ctx);
      toast(t("connections.revoked"));
    } catch (err) {
      btn.disabled = false;
      toast(err.message, "err");
    }
  });
}
