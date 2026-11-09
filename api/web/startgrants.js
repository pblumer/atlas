// Start grants, for administrators (ADR-0516, MFE1A).
//
// A start grant lets named accounts and groups start one process of one application
// themselves, without the operator role. This page is where an administrator sees and
// changes them: per application, every process with who may start it and whether it
// would be offered at all, the audience picked by name, a check for one person or
// group, and the audited history.
//
// The page is a convenience over routes that already refuse everyone but an
// administrator — and changes from anything but an administrator's own session. It
// adds no authority, and hiding it from the menu is not the check. It sends ids,
// never names: names are only shown. A write that loses a race (409) is never
// retried; the administrator is shown the current audience and decides again.

import { fmtDateTime } from "./datefmt.js";
import { t } from "./i18n.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// Timestamps from the server are Unix seconds.
const when = (unix) => (unix ? fmtDateTime(unix * 1000) : "—");

const enc = encodeURIComponent;

// blockerText says one blocker code in words; an unknown code is shown as it is, so
// a newer server's reason is never hidden.
function blockerText(code) {
  const text = t("startgrants.blocker." + code);
  return text === "startgrants.blocker." + code ? code : text;
}

function issueText(code) {
  const text = t("startgrants.issue." + code);
  return text === "startgrants.issue." + code ? code : text;
}

// directory resolves the ids a grant names to the names a person reads. An id that
// resolves to nobody is shown as removed — never hidden, so it can still be revoked.
function directory(users, groups) {
  const u = new Map(users.map((x) => [x.id, x.displayName || x.username || x.id]));
  const g = new Map(groups.map((x) => [x.id, x.name || x.id]));
  return {
    user: (id) => u.has(id) ? u.get(id) : t("startgrants.removedUser", { id }),
    group: (id) => g.has(id) ? g.get(id) : t("startgrants.removedGroup", { id }),
    users, groups,
  };
}

function audienceText(dir, grant) {
  const names = [...(grant.groupIds || []).map(dir.group), ...(grant.userIds || []).map(dir.user)];
  return names.length ? names.join(", ") : "—";
}

// rows merges the application's current processes with its grant records: a process
// nobody granted is a row too (to grant it), and a grant whose process is no longer
// deployed here stays a row (to revoke it).
function rows(definitions, grants) {
  const byId = new Map();
  for (const d of definitions || []) {
    if (!d.current) continue;
    byId.set(d.processId, { processId: d.processId, name: d.name || d.processId, version: d.version, deployed: true, grant: null });
  }
  for (const g of grants || []) {
    const row = byId.get(g.processId) || { processId: g.processId, name: g.processId, version: g.version, deployed: false, grant: null };
    row.grant = g;
    byId.set(g.processId, row);
  }
  return [...byId.values()].sort((a, b) => a.name.localeCompare(b.name) || a.processId.localeCompare(b.processId));
}

function statusCell(row) {
  const g = row.grant;
  if (!g || !g.granted) return `<span class="pill">${esc(t("startgrants.status.notGranted"))}</span>`;
  const blockers = g.blockers || [];
  if (!blockers.length) return `<span class="pill ok">${esc(t("startgrants.status.offered"))}</span>`;
  return `<span class="pill warn">${esc(t("startgrants.status.notOffered"))}</span>
    <div class="muted" style="font-size:12px">${blockers.map((b) => esc(blockerText(b))).join("<br>")}</div>`;
}

export async function viewStartGrants({ api, view, isSuperseded, isAdmin, authEnabled }) {
  const shell = (body) => `<div id="start-grants">
      <div class="card">
        <h1>${esc(t("startgrants.title"))}</h1>
        <p class="muted">${esc(t("startgrants.intro"))}</p>
      </div>${body}</div>`;
  if (!authEnabled) {
    view.innerHTML = shell(`<p class="muted" id="sg-denied" style="margin-top:18px">${esc(t("startgrants.noAuth"))}</p>`);
    return;
  }
  if (!isAdmin) {
    view.innerHTML = shell(`<p class="muted" id="sg-denied" style="margin-top:18px">${esc(t("startgrants.adminOnly"))}</p>`);
    return;
  }
  view.innerHTML = shell(`<p class="muted" id="sg-loading" style="margin-top:18px">${esc(t("startgrants.loading"))}</p>`);

  let apps, dir;
  try {
    const [a, u, g] = await Promise.all([
      api("GET", "/api/v1/applications"),
      api("GET", "/api/v1/users"),
      api("GET", "/api/v1/groups"),
    ]);
    apps = (a || []).slice().sort((x, y) => String(x.name || x.id).localeCompare(String(y.name || y.id)));
    dir = directory(u || [], g || []);
  } catch (e) {
    if (isSuperseded()) return;
    view.innerHTML = shell(`<p class="muted err" id="sg-error" style="margin-top:18px">${esc(t("startgrants.loadFailed", { error: e.message }))}</p>`);
    return;
  }
  if (isSuperseded()) return;

  const params = new URLSearchParams(location.hash.split("?")[1] || "");
  const state = {
    app: apps.some((x) => x.id === params.get("app")) ? params.get("app") : (apps[0] && apps[0].id) || "",
    focus: params.get("process") || "",
    rows: [], locked: false, panel: null,
  };

  view.innerHTML = shell(`
    <div class="card" style="margin-top:12px">
      <label class="field"><span>${esc(t("startgrants.application"))}</span>
        <select id="sg-app">${apps.map((a) => `<option value="${esc(a.id)}"${a.id === state.app ? " selected" : ""}${a.protected ? " disabled" : ""}>${esc(a.name || a.id)}${a.protected ? " — " + esc(t("startgrants.protectedApp")) : ""}</option>`).join("")}</select>
      </label>
      ${apps.length ? "" : `<p class="muted" id="sg-no-apps">${esc(t("startgrants.noApps"))}</p>`}
    </div>
    <div class="selfstart-status" id="sg-status" role="status" aria-live="polite" hidden></div>
    <div id="sg-table"></div>
    <div id="sg-panel"></div>`);
  const tableEl = view.querySelector("#sg-table");
  const panelEl = view.querySelector("#sg-panel");

  // say writes the page's one status line; kind is ok, err or info.
  function say(kind, text) {
    const el = view.querySelector("#sg-status");
    el.className = "selfstart-status " + kind;
    el.hidden = !text;
    el.textContent = text || "";
  }

  // failure turns a refused request into what it means for the administrator.
  function failure(err) {
    if (err.status === 401) return t("startgrants.session");
    if (err.status === 503) { state.locked = true; return t("startgrants.uncertain"); }
    if (err.status === 403) return t("startgrants.adminOnly");
    return t("startgrants.failed", { error: err.message });
  }

  function renderTable() {
    if (!state.app) { tableEl.innerHTML = ""; return; }
    if (!state.rows.length) {
      tableEl.innerHTML = `<p class="muted" id="sg-empty" style="margin-top:18px">${esc(t("startgrants.noProcesses"))}</p>`;
      return;
    }
    const disabled = state.locked ? " disabled" : "";
    tableEl.innerHTML = `<div class="card" style="padding:0; margin-top:12px; overflow-x:auto">
      <table data-dt-key="start-grants">
        <thead><tr>
          <th>${esc(t("startgrants.col.process"))}</th>
          <th>${esc(t("startgrants.col.audience"))}</th>
          <th>${esc(t("startgrants.col.status"))}</th>
          <th>${esc(t("startgrants.col.changed"))}</th>
          <th></th>
        </tr></thead>
        <tbody>${state.rows.map((r) => {
          const g = r.grant;
          const granted = !!(g && g.granted);
          return `<tr data-process="${esc(r.processId)}"${r.processId === state.focus ? ' class="selected"' : ""}>
            <td><b>${esc(r.name)}</b>${r.version ? ` <span class="chip">v${esc(r.version)}</span>` : ""}
              <div class="muted" style="font-size:12px">${esc(r.processId)}${r.deployed ? "" : " · " + esc(t("startgrants.notDeployedHere"))}</div></td>
            <td>${esc(granted ? audienceText(dir, g) : "—")}</td>
            <td>${statusCell(r)}</td>
            <td class="muted" style="font-size:12px">${g && g.revision ? esc(t("startgrants.changedBy", { who: dir.user(g.changedBy), when: when(g.changedAt) })) : "—"}</td>
            <td class="row-actions" style="white-space:nowrap">
              ${r.deployed ? `<button class="btn ghost sm" data-act="edit"${disabled}>${esc(t(granted ? "startgrants.edit" : "startgrants.grant"))}</button>` : ""}
              ${granted ? `<button class="btn ghost sm" data-act="check">${esc(t("startgrants.check"))}</button>` : ""}
              ${g && g.revision ? `<button class="btn ghost sm" data-act="history">${esc(t("startgrants.history"))}</button>` : ""}
              ${granted ? `<button class="btn ghost sm danger" data-act="revoke"${disabled}>${esc(t("startgrants.revoke"))}</button>` : ""}
            </td></tr>`;
        }).join("")}</tbody>
      </table></div>`;
  }

  async function load() {
    say("", "");
    panelEl.innerHTML = "";
    if (!state.app) { renderTable(); return; }
    tableEl.innerHTML = `<p class="muted" style="margin-top:18px">${esc(t("startgrants.loading"))}</p>`;
    try {
      const [deps, grants] = await Promise.all([
        api("GET", `/api/v1/applications/${enc(state.app)}/deployments`),
        api("GET", `/api/v1/applications/${enc(state.app)}/start-grants`),
      ]);
      if (isSuperseded()) return;
      state.rows = rows(deps && deps.definitions, grants && grants.items);
      renderTable();
    } catch (e) {
      if (isSuperseded()) return;
      tableEl.innerHTML = "";
      say("err", failure(e));
    }
  }

  const grantPath = (processId) => `/api/v1/applications/${enc(state.app)}/start-grants/${enc(processId)}`;

  // picker is the search box that adds users and groups to an audience by name.
  function pickerHTML(id) {
    return `<label class="field"><span>${esc(t("startgrants.find"))}</span>
      <input type="search" id="${id}" autocomplete="off" placeholder="${esc(t("startgrants.findPlaceholder"))}"/></label>
      <ul class="sg-matches" id="${id}-matches" role="listbox"></ul>`;
  }

  function matches(q) {
    const needle = q.trim().toLowerCase();
    if (!needle) return [];
    const out = [];
    for (const g of dir.groups) if (String(g.name || "").toLowerCase().includes(needle)) out.push({ kind: "group", id: g.id, label: g.name });
    for (const u of dir.users) {
      const hay = [u.displayName, u.username, u.email].filter(Boolean).join(" ").toLowerCase();
      if (hay.includes(needle)) out.push({ kind: "user", id: u.id, label: u.displayName || u.username });
    }
    return out.slice(0, 20);
  }

  function wirePicker(input, onPick) {
    const list = view.querySelector(`#${input.id}-matches`);
    input.addEventListener("input", () => {
      list.innerHTML = matches(input.value).map((m) =>
        `<li><button type="button" class="btn ghost sm" data-kind="${m.kind}" data-id="${esc(m.id)}">${esc(t(m.kind === "group" ? "startgrants.kind.group" : "startgrants.kind.user"))}: ${esc(m.label)}</button></li>`).join("");
    });
    list.addEventListener("click", (e) => {
      const b = e.target.closest("button[data-id]");
      if (!b) return;
      onPick(b.dataset.kind, b.dataset.id);
      input.value = "";
      list.innerHTML = "";
      input.focus();
    });
  }

  async function openEditor(processId) {
    let current;
    try { current = await api("GET", grantPath(processId)); }
    catch (e) { say("err", failure(e)); return; }
    const row = state.rows.find((r) => r.processId === processId);
    const sel = { users: new Set(current.granted ? current.userIds : []), groups: new Set(current.granted ? current.groupIds : []) };
    const chips = () => [...[...sel.groups].map((id) => ({ kind: "group", id, label: dir.group(id) })),
      ...[...sel.users].map((id) => ({ kind: "user", id, label: dir.user(id) }))]
      .map((c) => `<span class="chip">${esc(c.label)} <button type="button" class="icon-btn" data-remove-kind="${c.kind}" data-remove-id="${esc(c.id)}" aria-label="${esc(t("startgrants.remove", { name: c.label }))}">×</button></span>`).join(" ")
      || `<span class="muted">${esc(t("startgrants.audienceEmpty"))}</span>`;
    panelEl.innerHTML = `<div class="card" id="sg-editor" style="margin-top:12px">
      <h2>${esc(t("startgrants.editTitle", { process: row ? row.name : processId }))}</h2>
      <p class="muted">${esc(t("startgrants.revision", { n: current.revision }))}</p>
      <div id="sg-chips" style="margin:8px 0">${chips()}</div>
      ${pickerHTML("sg-find")}
      <div class="row" style="gap:8px; margin-top:12px">
        <button class="btn" type="button" id="sg-save">${esc(t("startgrants.save"))}</button>
        <button class="btn ghost" type="button" id="sg-cancel">${esc(t("startgrants.cancel"))}</button>
      </div></div>`;
    const redraw = () => { panelEl.querySelector("#sg-chips").innerHTML = chips(); };
    wirePicker(panelEl.querySelector("#sg-find"), (kind, id) => { (kind === "group" ? sel.groups : sel.users).add(id); redraw(); });
    panelEl.querySelector("#sg-chips").addEventListener("click", (e) => {
      const b = e.target.closest("button[data-remove-id]");
      if (!b) return;
      (b.dataset.removeKind === "group" ? sel.groups : sel.users).delete(b.dataset.removeId);
      redraw();
    });
    panelEl.querySelector("#sg-cancel").addEventListener("click", () => { panelEl.innerHTML = ""; });
    const save = panelEl.querySelector("#sg-save");
    save.addEventListener("click", async () => {
      if (!sel.users.size && !sel.groups.size) { say("err", t("startgrants.audienceRequired")); return; }
      save.disabled = true;
      try {
        const res = await api("PUT", grantPath(processId), {
          userIds: [...sel.users], groupIds: [...sel.groups], expectedRevision: current.revision,
        });
        await load();
        say("ok", t(res && res.changed === false ? "startgrants.unchanged" : "startgrants.saved"));
      } catch (e) {
        save.disabled = false;
        if (e.status === 409) {
          await load();
          say("err", t("startgrants.conflict"));
          return;
        }
        say("err", failure(e));
      }
    });
  }

  async function revoke(processId) {
    const row = state.rows.find((r) => r.processId === processId);
    const g = row && row.grant;
    if (!g) return;
    if (!window.confirm(t("startgrants.revokeConfirm", { process: row.name, audience: audienceText(dir, g) }))) return;
    try {
      await api("DELETE", `${grantPath(processId)}?expectedRevision=${enc(g.revision)}`);
      await load();
      say("ok", t("startgrants.revoked"));
    } catch (e) {
      if (e.status === 409) { await load(); say("err", t("startgrants.conflict")); return; }
      say("err", failure(e));
    }
  }

  function openCheck(processId) {
    const row = state.rows.find((r) => r.processId === processId);
    panelEl.innerHTML = `<div class="card" id="sg-check" style="margin-top:12px">
      <h2>${esc(t("startgrants.checkTitle", { process: row ? row.name : processId }))}</h2>
      ${pickerHTML("sg-check-find")}
      <div id="sg-check-result" role="status" aria-live="polite"></div></div>`;
    const out = panelEl.querySelector("#sg-check-result");
    wirePicker(panelEl.querySelector("#sg-check-find"), async (kind, id) => {
      out.textContent = t("startgrants.loading");
      try {
        const p = await api("GET", `${grantPath(processId)}/preview?${kind === "group" ? "groupId" : "userId"}=${enc(id)}`);
        const name = kind === "group" ? dir.group(id) : dir.user(id);
        const reasons = [
          ...(p.granted ? [] : [t("startgrants.reason.notGranted")]),
          ...(p.accountIssues || []).map(issueText),
          ...(p.blockers || []).map(blockerText),
        ];
        const via = (p.matchedBy || []).map((m) => m === "user" ? t("startgrants.via.user")
          : t("startgrants.via.group", { group: dir.group(m.replace(/^group:/, "")) }));
        out.innerHTML = `<p><b>${esc(t(p.wouldBeOffered ? "startgrants.checkYes" : "startgrants.checkNo", { name }))}</b></p>
          ${via.length ? `<p class="muted">${esc(t("startgrants.via", { via: via.join(", ") }))}</p>` : ""}
          ${reasons.length ? `<ul>${reasons.map((r) => `<li>${esc(r)}</li>`).join("")}</ul>` : ""}`;
      } catch (e) {
        out.textContent = failure(e);
      }
    });
  }

  async function openHistory(processId) {
    const row = state.rows.find((r) => r.processId === processId);
    let h;
    try { h = await api("GET", `${grantPath(processId)}/history`); }
    catch (e) { say("err", failure(e)); return; }
    const items = (h && h.items) || [];
    panelEl.innerHTML = `<div class="card" id="sg-history" style="margin-top:12px">
      <h2>${esc(t("startgrants.historyTitle", { process: row ? row.name : processId }))}</h2>
      ${items.length ? `<ol>${items.map((c) => `<li>${esc(t(c.action === "revoke" ? "startgrants.historyRevoke" : "startgrants.historyGrant",
        { who: dir.user(c.actorId), when: when(c.at), audience: audienceText(dir, c) }))}</li>`).join("")}</ol>`
        : `<p class="muted">${esc(t("startgrants.historyEmpty"))}</p>`}</div>`;
  }

  view.querySelector("#sg-app").addEventListener("change", (e) => { state.app = e.target.value; state.focus = ""; load(); });
  tableEl.addEventListener("click", (e) => {
    const b = e.target.closest("button[data-act]");
    if (!b) return;
    const processId = b.closest("tr[data-process]").dataset.process;
    if (b.dataset.act === "edit") openEditor(processId);
    else if (b.dataset.act === "revoke") revoke(processId);
    else if (b.dataset.act === "check") openCheck(processId);
    else if (b.dataset.act === "history") openHistory(processId);
  });
  await load();
}

// grantHint is the read-only mark the application's Deployments tab shows an
// administrator beside a process that has a start grant, linking to this page. It
// offers nothing to change: owners and editors work in that view, and a grant is not
// theirs (ADR-0516). Exported for app.js.
export function grantHint(applicationId, grant) {
  if (!grant || !grant.granted) return "";
  const groups = (grant.groupIds || []).length, users = (grant.userIds || []).length;
  const label = t("startgrants.hint", { groups, users });
  return ` <a class="pill grant-hint" href="#/console/start-grants?app=${enc(applicationId)}&process=${enc(grant.processId)}" title="${esc(t("startgrants.hintTitle"))}">${esc(label)}</a>`;
}
