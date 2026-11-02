// The Operations "Mockups" view's content (ADR-0216): one card per mockup, drawn by a
// renderer chosen by the kind it mocks, and a plain one for a kind nobody has written a
// renderer for — visible the day it reports, well drawn once somebody writes its card.
//
// The first kind here is SharePoint (ADR-0484). Its card sits
// between the two mockups that still have pages of their own: the mock directory
// (ADR-0213) draws state, because that mockup has state; the mock database (ADR-0224)
// draws a sequence, because that one has none. A SharePoint mockup has both — a site
// whose lists and libraries a run fills, and the operations that filled them — so its
// card draws both: the site as tables, one per list or library, and the journal
// underneath, where a refusal is the row an author came to find.
//
// The rendering lives in this module rather than in app.js because app.js boots the
// whole console on import and cannot be exercised in isolation; this can, and the e2e
// harness renders it against a fixture.

import { fmtDateTime } from "./datefmt.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// cell renders one column value. Text is shown as text; anything else — a number, a
// yes/no, a multi-choice list — as JSON, so the string "17" and the number 17 can be
// told apart, which is the question when a filter found nothing.
function cell(v) {
  if (v === undefined || v === null) return `<span class="muted">—</span>`;
  if (typeof v === "string") return esc(v);
  return `<code>${esc(JSON.stringify(v))}</code>`;
}

function bytes(n) {
  n = Number(n) || 0;
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / 1024 / 1024).toFixed(1)} MiB`;
}

function plural(n, one, many) {
  return `${n} ${n === 1 ? one : many}`;
}

function listTable(l) {
  const items = l.items || [];
  if (!items.length) return `<p class="empty">No items: every one written to it was deleted.</p>`;
  const cols = ["id", ...(l.columns || []).filter((c) => c !== "id")];
  return `<table class="admock-attrs spmock-table">
    <thead><tr>${cols.map((c) => `<th>${esc(c)}</th>`).join("")}</tr></thead>
    <tbody>${items.map((it) => `<tr>${cols.map((c) => `<td>${cell(it[c])}</td>`).join("")}</tr>`).join("")}</tbody>
  </table>`;
}

function libraryTable(l) {
  const files = l.files || [];
  if (!files.length) return `<p class="empty">Empty: nothing has been uploaded, or everything was deleted.</p>`;
  return `<table class="admock-attrs spmock-table">
    <thead><tr><th>Path</th><th>Size</th><th>Properties</th></tr></thead>
    <tbody>${files.map((f) => `<tr class="${f.folder ? "spmock-folder" : ""}">
      <td><code>${esc(f.path)}${f.folder ? "/" : ""}</code></td>
      <td class="spmock-size">${f.folder ? "" : esc(bytes(f.size))}</td>
      <td>${f.fields && Object.keys(f.fields).length
        ? Object.keys(f.fields).sort().map((k) => `${esc(k)} = ${cell(f.fields[k])}`).join(", ")
        : ""}</td>
    </tr>`).join("")}</tbody>
  </table>`;
}

function listSection(l) {
  const shown = l.library ? (l.files || []).length : (l.items || []).length;
  const held = l.held || 0;
  const what = l.library
    ? plural(held, "file or folder", "files and folders")
    : plural(held, "item", "items");
  return `<details class="spmock-list" data-k="list:${esc(l.title)}" open>
    <summary>
      <b>${esc(l.title)}</b>
      <span class="muted">${l.library ? "document library" : "list"} · ${what}${
        (l.columns || []).length ? ` · columns ${(l.columns || []).map(esc).join(", ")}` : ""}</span>
    </summary>
    ${shown < held ? `<p class="muted">Showing the first ${shown} of ${held}.</p>` : ""}
    ${l.library ? libraryTable(l) : listTable(l)}
  </details>`;
}

function journalTable(journal) {
  if (!journal.length) return `<p class="empty">Nothing asked since the site was last emptied.</p>`;
  return `<table class="admock-attrs sqlmock-table spmock-journal"><tbody>${journal.map((op) => `
    <tr class="${op.error ? "sqlmock-failed" : ""}">
      <td class="sqlmock-seq muted">${esc(op.seq)}</td>
      <td class="muted spmock-at">${esc(op.at ? fmtDateTime(op.at) : "")}</td>
      <td><b>${esc(op.operation || "")}</b> <code>${esc(op.target || "")}</code>
        ${op.error ? `<div class="sqlmock-detail">${esc(op.error)}</div>` : ""}</td>
    </tr>`).join("")}</tbody></table>`;
}

// sharePointCard draws one SharePoint mockup: its report's data is the Worker's site.
function sharePointCard(r) {
  const w = r.data || {};
  const sites = w.sites || [];
  const journal = w.journal || [];
  const lists = sites.reduce((n, s) => n + (s.lists || []).length, 0);
  const refused = journal.filter((op) => op.error).length;
  return `<div class="card admock-worker" data-worker="${esc(r.target)}" data-kind="sharepoint" data-fold="${esc(r.target)}">
      <div class="admock-head">
        <h2>${esc(r.target)}</h2>
        <span class="tag">SharePoint</span>
        <span class="muted">${plural(sites.length, "site", "sites")} · ${plural(lists, "list or library", "lists and libraries")} ·
        ${esc(bytes(w.storedBytes))} of file content${refused ? ` · <b>${refused}</b> refused` : ""}</span>
        <span class="admock-card-state">
          <span class="pill warn"><span class="dot"></span>mockup</span>
          <button class="btn ghost danger" data-spmock-clear="${esc(r.target)}"
            title="Empty this Worker's site and its journal, for the next trial run">Empty site</button>
        </span>
      </div>
      ${sites.length
        ? sites.map((s) => `<div class="admock-forest">
            <h3>${esc(s.url)}</h3>
            ${(s.lists || []).map(listSection).join("")}
          </div>`).join("")
        : `<p class="empty">The site is empty. A list or a library appears here the first time a task
           writes to it — create-item, upload-file or create-folder — as it would have been made by a
           person on a real site.</p>`}
      <details class="admock-ops" data-k="ops" ${refused ? "open" : ""}>
        <summary>Operations (${journal.length}${journal.length >= 200 ? ", the newest 200" : ""})</summary>
        ${journalTable(journal)}
      </details>
  </div>`;
}

// openAPICard draws one mock REST API (ADR-draft-console-managed-openapi-mock): the
// operation table the document describes, with how often each was actually called, and
// the journal of calls underneath.
//
// This card is where the journal lives, and that is deliberate rather than incidental.
// The served mock refuses /__mock/calls because a request body comes from a real
// instance of a real draft; it is read here, behind the login, instead. So the column
// an author comes for is "Calls" at zero — the operation they thought they were
// calling and were not.
function openAPICard(r) {
  const d = r.data || {};
  const ops = d.operations || [];
  const calls = d.calls || [];
  const uncalled = ops.filter((o) => !o.calls).length;
  const opsTable = !ops.length
    ? `<p class="empty">The document describes no operation this mock can serve.</p>`
    : `<table class="admock-attrs spmock-table">
        <thead><tr><th>Operation</th><th>Path</th><th style="text-align:right">Calls</th></tr></thead>
        <tbody>${ops.map((o) => `<tr class="${o.calls ? "" : "muted"}">
          <td><b>${esc(o.method)}</b>${o.id ? ` <span class="muted">${esc(o.id)}</span>` : ""}</td>
          <td><code>${esc(o.path)}</code></td>
          <td style="text-align:right">${o.calls ? esc(o.calls) : "—"}</td>
        </tr>`).join("")}</tbody>
      </table>`;
  const callsTable = !calls.length
    ? `<p class="empty">Nothing called it yet. A REST task reaches it once its URL names this
       mock's base path instead of the real API's.</p>`
    : `<table class="admock-attrs sqlmock-table spmock-journal"><tbody>${calls.map((c) => `
        <tr class="${c.status >= 400 ? "sqlmock-failed" : ""}">
          <td class="sqlmock-seq muted">${esc(c.seq)}</td>
          <td><b>${esc(c.method)}</b> <code>${esc(c.path)}</code>
            <span class="muted">&rarr; ${esc(c.status)}${
              c.operation ? ` · ${esc(c.operation)}` : ""}${
              c.requestId ? ` · job ${esc(c.requestId)}` : ""}</span>
            ${c.body !== undefined && c.body !== null
              ? `<div class="sqlmock-detail"><code>${esc(JSON.stringify(c.body))}</code></div>` : ""}</td>
        </tr>`).join("")}</tbody></table>`;
  const title = d.title || r.target || "an untitled API";
  return `<div class="card admock-worker" data-worker="${esc(r.source || "")}" data-kind="openapi" data-fold="${esc(r.source || title)}">
      <div class="admock-head">
        <h2>${esc(r.source || title)}</h2>
        <span class="tag">OpenAPI</span>
        <span class="muted">${esc(title)}${d.version ? ` ${esc(d.version)}` : ""} ·
        ${plural(ops.length, "operation", "operations")} · ${plural(calls.length, "call", "calls")}${
          uncalled ? ` · <b>${uncalled}</b> never called` : ""}</span>
        <span class="admock-card-state"><span class="pill warn"><span class="dot"></span>mockup</span></span>
      </div>
      <details class="admock-ops" data-k="openapi-ops" open>
        <summary>Operations (${ops.length})</summary>
        ${opsTable}
      </details>
      <details class="admock-ops" data-k="openapi-calls" ${calls.length ? "open" : ""}>
        <summary>Calls (${calls.length}${calls.length >= 200 ? ", the newest 200" : ""})</summary>
        ${callsTable}
      </details>
  </div>`;
}

// genericCard draws a mockup of a kind with no renderer of its own: its summary, and its
// data as the server served it. Plainer than a written card, which is the honest sign
// that nobody has written one yet (ADR-0216).
function genericCard(r) {
  return `<div class="card admock-worker" data-worker="${esc(r.target || "")}" data-kind="${esc(r.kind || "")}" data-fold="${esc(r.target || r.kind || "?")}">
      <div class="admock-head">
        <h2>${esc(r.target || r.kind || "?")}</h2>
        <span class="tag">${esc(r.kind || "?")}</span>
        <span class="muted">${esc(r.summary || "")}</span>
        <span class="admock-card-state"><span class="pill warn"><span class="dot"></span>mockup</span></span>
      </div>
      <pre class="mockups-data">${esc(JSON.stringify(r.data, null, 2))}</pre>
  </div>`;
}

// RENDERERS maps a mocked kind to the card that draws it.
const RENDERERS = { sharepoint: sharePointCard, openapi: openAPICard };

// mockupsHTML renders the view's list from GET /api/v1/mocks.
export function mockupsHTML(data) {
  const reports = (data && data.reports) || [];
  if (!reports.length) {
    return `<div class="card"><p class="empty">No mockup is running. A SharePoint Worker becomes one
      with the provider <b>Mockup</b> under Organization &rsaquo; Workers; every SharePoint task that
      names it then runs against a site shown here. A <b>mock REST API</b> becomes one on the same
      page: upload the API's OpenAPI document under Mock REST APIs, and every call a task makes to it
      is listed here.</p></div>`;
  }
  return reports.map((r) => (RENDERERS[r.kind] || genericCard)(r)).join("");
}
