// The connected systems, for administrators (ADR-0512 §4).
//
// A connected system is approved once, on the consent screen, by an administrator who
// started the connection in the other system. This page is where that approval is seen
// afterwards and ended: per system the host its client_id is on, the name it claims, who
// approved it and when, what it may read and how far, whether its people connect
// without being asked, how many are connected, and when it last used Atlas.
//
// Two things are done here, and one deliberately is not. Pre-consent is turned off
// here, and a system is disconnected here — its grant withdrawn and, if chosen, every
// person's grant of it. What a system reads is not edited here: it is changed by
// approving the system again, started from the system, so the administrator sees the
// whole question on the consent screen rather than a part of it on a form.
//
// Nothing on this page holds a credential: the route behind it answers grant ids and
// what they reach, never a token.

import { fmtDateTime } from "./datefmt.js";
import { t } from "./i18n.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// Timestamps from the server are Unix seconds.
const when = (unix) => (unix ? fmtDateTime(unix * 1000) : "—");

function capabilities(row) {
  if (!(row.capabilities || []).length) return "—";
  return row.capabilities.map((c) => {
    const reach = (c.projects || []).length
      ? t("systems.reach.projects", { projects: c.projects.join(", ") })
      : t("systems.reach.all");
    return `<span class="chip" data-cap="${esc(c.scope)}">${esc(c.scope)} · ${esc(reach)}</span>`;
  }).join(" ");
}

function systemCard(row) {
  const pill = row.connected
    ? `<span class="pill ok"><span class="dot"></span>${esc(t("systems.status.connected"))}</span>`
    : `<span class="pill off"><span class="dot"></span>${esc(t("systems.status.disconnected"))}</span>`;
  const facts = row.connected
    ? `${esc(t("systems.approved", { who: row.approvedBy || "—", when: when(row.approvedAt) }))}<br>
       ${esc(t("systems.capabilities"))}: ${capabilities(row)}<br>
       ${esc(t(row.preconsent ? "systems.preconsent.on" : "systems.preconsent.off"))} ·
       ${esc(t("systems.people", { n: row.people }))} ·
       ${esc(t("systems.lastUsed", { when: when(row.lastUsed) }))}`
    : `${esc(t("systems.disconnectedAt", { when: when(row.disconnectedAt) }))}${row.disconnectReason ? ` — ${esc(row.disconnectReason)}` : ""}<br>
       ${esc(t("systems.reconnect"))}`;
  const actions = row.connected
    ? `<div class="row" style="margin-top:10px; gap:8px; flex-wrap:wrap">
        ${row.preconsent ? `<button class="btn ghost" type="button" data-act="preconsent-off">${esc(t("systems.preconsent.turnOff"))}</button>` : ""}
        <button class="btn ghost danger" type="button" data-act="disconnect">${esc(t("systems.disconnect"))}</button>
        <label class="field inline" style="margin:0"><input type="checkbox" data-people />
          <span>${esc(t("systems.disconnect.people"))}</span></label>
      </div>`
    : "";
  return `<div class="card" data-system="${esc(row.id)}" data-client="${esc(row.clientId)}" style="margin-top:12px">
    <div class="between">
      <div>${row.host ? `<div class="muted" style="font-size:12px"><code>${esc(row.host)}</code></div>` : ""}
        <b>${esc(row.name || row.clientId)}</b>
        <div class="muted" style="font-size:12px"><code>${esc(row.clientId)}</code></div></div>
      <div>${pill}</div>
    </div>
    <div class="muted" style="font-size:13px; margin-top:8px">${facts}</div>
    ${actions}
  </div>`;
}

export async function viewConnectedSystems({ api, toast, view, isSuperseded }) {
  let rows;
  try {
    rows = await api("GET", "/api/v1/connected-systems");
  } catch (e) {
    if (isSuperseded()) return;
    if (e.status === 403) {
      view.innerHTML = `<div class="card"><h1>${esc(t("systems.title"))}</h1>
        <p class="muted" id="sys-denied">${esc(t("systems.denied"))}</p></div>`;
      return;
    }
    throw e;
  }
  if (isSuperseded()) return;
  const render = () => {
    view.innerHTML = `<div id="connected-systems">
      <div class="card">
        <h1>${esc(t("systems.title"))}</h1>
        <p class="muted">${esc(t("systems.intro"))}</p>
      </div>
      <div id="sys-list">${rows.map(systemCard).join("")
        || `<p class="muted" id="sys-empty" style="margin-top:18px">${esc(t("systems.empty"))}</p>`}</div>
    </div>`;
  };
  render();
  const reload = async () => {
    rows = await api("GET", "/api/v1/connected-systems");
    if (!isSuperseded()) render();
  };
  view.addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-act]");
    if (!btn) return;
    const card = btn.closest("[data-system]");
    const id = card.dataset.system;
    try {
      if (btn.dataset.act === "preconsent-off") {
        if (!window.confirm(t("systems.preconsent.confirm"))) return;
        btn.disabled = true;
        await api("PUT", `/api/v1/connected-systems/${encodeURIComponent(id)}/preconsent`, { preconsent: false });
        toast(t("systems.preconsent.done"));
      } else if (btn.dataset.act === "disconnect") {
        const people = card.querySelector("input[data-people]")?.checked;
        if (!window.confirm(t(people ? "systems.disconnect.confirmPeople" : "systems.disconnect.confirm"))) return;
        btn.disabled = true;
        await api("DELETE", `/api/v1/connected-systems/${encodeURIComponent(id)}${people ? "?people=withdraw" : ""}`);
        toast(t("systems.disconnected"));
      }
      await reload();
    } catch (err) {
      btn.disabled = false;
      toast(err.message, "err");
    }
  });
}

// mySystemsSection is the part of Connected accounts that lists the connected systems
// a person is connected to and those pre-approved for them, each with a withdrawal
// (ADR-0512 §4). It is exported for connections.js, which renders it whether or not the
// vault behind the rest of that page is on.
export async function mySystemsSection({ api, toast }) {
  const none = { html: "", wire: () => {} };
  let mine = [];
  try { mine = (await api("GET", "/api/v1/me/connected-systems")) || []; } catch { return none; }
  if (!mine.length) return none;
  const row = (s) => {
    const status = s.declined ? t("systems.mine.declined")
      : (s.grants || []).length ? t("systems.mine.connected")
        : s.preApproved ? t("systems.mine.preApproved") : t("systems.mine.notConnected");
    const canWithdraw = (s.grants || []).length || s.preApproved;
    return `<tr data-my-system="${esc(s.clientId)}">
      <td>${s.host ? `<div class="muted" style="font-size:12px"><code>${esc(s.host)}</code></div>` : ""}<b>${esc(s.name || s.clientId)}</b></td>
      <td>${esc(status)}</td>
      <td>${canWithdraw ? `<button class="btn ghost danger" type="button" data-act="withdraw-system">${esc(t("systems.mine.withdraw"))}</button>` : ""}</td>
    </tr>`;
  };
  const html = `<div class="card" id="my-systems" style="padding:0; margin-top:18px">
    <div style="padding:16px 18px 0"><h2>${esc(t("systems.mine.title"))}</h2>
      <p class="muted">${esc(t("systems.mine.intro"))}</p></div>
    <table data-dt-key="my-systems">
      <thead><tr><th>${esc(t("systems.mine.col.system"))}</th><th>${esc(t("systems.mine.col.status"))}</th><th></th></tr></thead>
      <tbody>${mine.map(row).join("")}</tbody>
    </table>
  </div>`;
  const wire = (root) => {
    root.addEventListener("click", async (e) => {
      const btn = e.target.closest("button[data-act='withdraw-system']");
      if (!btn) return;
      const clientId = btn.closest("[data-my-system]").dataset.mySystem;
      if (!window.confirm(t("systems.mine.confirm"))) return;
      btn.disabled = true;
      try {
        await api("POST", "/api/v1/me/connected-systems/withdraw", { clientId });
        btn.closest("tr").children[1].textContent = t("systems.mine.declined");
        btn.remove();
        toast(t("systems.mine.withdrawn"));
      } catch (err) {
        btn.disabled = false;
        toast(err.message, "err");
      }
    });
  };
  return { html, wire };
}
