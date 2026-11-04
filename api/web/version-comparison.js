// What changed between two versions of a process, as a list
// (ADR-draft-compare-process-versions).
//
// A version is immutable and every deploy mints one, so "what is different in v7"
// is a question every process with a history raises: before migrating instances
// onto it, when a run behaves differently from last week's, or to see what a publish
// deployed. The server compares the two models element by element
// (GET /api/v1/processes/{key}/comparison) — the diagram, formatting and attribute
// order ignored — and this renders its answer: every element added, removed or
// changed, and for a changed one each field before and after.
//
// A list rather than a marked-up drawing, first: it says which version each value
// belongs to, which a drawing can only imply, and it carries a changed condition or
// script, which a drawing does not show at all. The drawing can build on it.
//
// The same list renders a draft's comparison with a version (the Modeler's chip,
// ADR-0452), which carries changes in the same form.
//
// changesHTML is a pure function of the server's answer, so a browser test drives it
// without a dialog around it — the split migrationdialog.js makes.

import { t as tr, plural as trPlural } from "./i18n.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// versionLabel names a deployed version the way an operator reads one.
export const versionLabel = (v) => `v${v.version}${v.versionTag ? ` · ${v.versionTag}` : ""}`;

// countsText says how many elements changed, by kind: "2 neu · 1 geändert". Kinds
// with nothing in them are left out; no change at all says so.
export function countsText(counts) {
  const c = counts || {};
  const parts = [];
  if (c.added) parts.push(trPlural("cmp.count.added", c.added));
  if (c.removed) parts.push(trPlural("cmp.count.removed", c.removed));
  if (c.changed) parts.push(trPlural("cmp.count.changed", c.changed));
  return parts.length ? parts.join(" · ") : tr("cmp.count.none");
}

// A value longer than this, or one with a line break in it, is shown as a block:
// a script or a long condition read inline is unreadable.
const INLINE_MAX = 80;

// valueHTML renders one side of a field. A side that does not state the field is a
// dash, said as absent; a side that states it with nothing in it — <zeebe:userTask/> —
// is "(leer)", because Kind tells the two apart and the reader must be able to too.
function valueHTML(value, present) {
  if (!present) return `<span class="muted" title="${esc(tr("cmp.absentTitle"))}">—</span>`;
  if (value === "" || value == null) return `<span class="muted">${esc(tr("cmp.empty"))}</span>`;
  const s = String(value);
  if (s.length > INLINE_MAX || s.includes("\n")) return `<pre class="vc-pre">${esc(s)}</pre>`;
  return `<code class="vc-val">${esc(s)}</code>`;
}

// detailRows renders the fields a changed element states differently.
function detailRows(details) {
  return (details || []).map((d) => {
    const before = d.kind !== "added";
    const after = d.kind !== "removed";
    return `<tr class="vc-d-${esc(d.kind)}">
        <td class="vc-field mono">${esc(d.field)}</td>
        <td>${valueHTML(d.before, before)}</td>
        <td>${valueHTML(d.after, after)}</td>
      </tr>`;
  }).join("");
}

// changeHTML renders one element of the list: what happened to it, which element it
// is — its name when it has one, its id always, since the id is what the two versions
// were matched by — and where it sits.
function changeHTML(c) {
  const kind = esc(tr(`cmp.kind.${c.kind}`));
  const who = c.id
    ? `${c.name ? `<b>${esc(c.name)}</b> ` : ""}<code class="vc-id">${esc(c.id)}</code>`
    : `<b>${esc(tr("cmp.document"))}</b>`;
  const where = c.parent ? ` <span class="muted vc-parent">${esc(tr("cmp.in", { parent: c.parent }))}</span>` : "";
  const details = c.details && c.details.length
    ? `<table class="vc-details">
        <thead><tr><th>${esc(tr("cmp.col.field"))}</th><th>${esc(tr("cmp.col.before"))}</th><th>${esc(tr("cmp.col.after"))}</th></tr></thead>
        <tbody>${detailRows(c.details)}</tbody>
      </table>`
    : "";
  return `<li class="vc-item vc-${esc(c.kind)}" data-element="${esc(c.id || "")}">
      <div class="vc-head"><span class="vc-kind">${kind}</span> ${who} <span class="muted vc-type">${esc(c.type)}</span>${where}</div>
      ${details}
    </li>`;
}

// changesHTML renders a comparison's answer: the counts, then every change. Two
// models the server calls the same say so, and nothing else. Two that differ with an
// empty list differ only in what the list leaves out — the incoming/outgoing hints —
// and that is said rather than shown as "no changes", which would be a claim the
// server did not make.
export function changesHTML(cmp) {
  if (!cmp) return "";
  const changes = Array.isArray(cmp.changes) ? cmp.changes : [];
  if (cmp.sameModel && !changes.length) {
    return `<p class="vc-summary vc-same">${esc(tr("cmp.same"))}</p>`;
  }
  if (!changes.length) {
    return `<p class="vc-summary">${esc(tr("cmp.onlyHints", { difference: cmp.difference || "" }))}</p>`;
  }
  return `<p class="vc-summary">${esc(countsText(cmp.counts))}</p>
    <ul class="vc-list">${changes.map(changeHTML).join("")}</ul>`;
}

// openModal puts a dialog on the page and answers its parts. It closes on the close
// button, a click beside it and Escape.
function openModal({ title, head = "", wide = true }) {
  const ov = document.createElement("div");
  ov.className = "modal-ov";
  ov.innerHTML = `
    <div class="modal confirm-modal vc-modal${wide ? " vc-wide" : ""}" role="dialog" aria-modal="true" aria-labelledby="vc-title">
      <div class="modal-head"><h2 id="vc-title">${esc(title)}</h2></div>
      <div class="modal-body">${head}<div class="vc-result" aria-live="polite"></div></div>
      <div class="modal-foot"><span></span><button class="btn neutral" data-vc-close>${esc(tr("cmp.close"))}</button></div>
    </div>`;
  document.body.appendChild(ov);
  let onClose = null;
  const close = () => {
    ov.remove();
    document.removeEventListener("keydown", onKey, true);
    if (onClose) onClose();
  };
  const onKey = (e) => { if (e.key === "Escape") { e.stopPropagation(); close(); } };
  ov.querySelector("[data-vc-close]").addEventListener("click", close);
  ov.addEventListener("click", (e) => { if (e.target === ov) close(); });
  document.addEventListener("keydown", onKey, true);
  setTimeout(() => ov.querySelector("[data-vc-close]").focus(), 0);
  return {
    ov,
    result: ov.querySelector(".vc-result"),
    closed: new Promise((resolve) => { onClose = resolve; }),
  };
}

// defaultPair picks what to compare when the caller named only one side, or none:
// the version named (else the newest) against the one before it — the question a new
// version raises. The oldest version has none before it, so it is compared with the
// one after it instead, the other way round.
export function defaultPair(versions, toKey, fromKey) {
  const byNewest = (versions || []).slice().sort((a, b) => b.version - a.version);
  if (byNewest.length < 2) return null;
  const find = (k) => k != null && byNewest.find((v) => String(v.key) === String(k));
  const to = find(toKey) || byNewest[0];
  let from = find(fromKey);
  if (!from || from === to) {
    from = byNewest.find((v) => v.version < to.version) || byNewest.find((v) => v !== to);
  }
  return { to, from, versions: byNewest };
}

// compareVersionsFlow opens the comparison of two versions of one process. versions
// is the process's deployed versions as GET /api/v1/processes lists them; toKey and
// fromKey preselect the two sides, by default the newest and the one before.
export async function compareVersionsFlow({ api, processId, processName, versions, toKey, fromKey }) {
  const pair = defaultPair(versions, toKey, fromKey);
  if (!pair) return false;
  const option = (v) => `<option value="${esc(String(v.key))}">${esc(versionLabel(v))}</option>`;
  const head = `
    <p class="muted" style="margin:0 0 10px">${esc(tr("cmp.intro"))}</p>
    <div class="row vc-pick" style="gap:10px;flex-wrap:wrap;align-items:flex-end">
      <label class="field" style="flex:1 1 180px"><span>${esc(tr("cmp.from"))}</span>
        <select id="vc-from">${pair.versions.map(option).join("")}</select></label>
      <button type="button" class="btn neutral small" id="vc-swap" title="${esc(tr("cmp.swapTitle"))}">&#8646;</button>
      <label class="field" style="flex:1 1 180px"><span>${esc(tr("cmp.to"))}</span>
        <select id="vc-to">${pair.versions.map(option).join("")}</select></label>
    </div>`;
  const m = openModal({ title: tr("cmp.title", { process: processName || processId || "" }), head });
  const fromSel = m.ov.querySelector("#vc-from");
  const toSel = m.ov.querySelector("#vc-to");
  fromSel.value = String(pair.from.key);
  toSel.value = String(pair.to.key);

  let seq = 0;
  const load = async () => {
    const mine = ++seq;
    if (fromSel.value === toSel.value) {
      m.result.innerHTML = `<p class="vc-summary">${esc(tr("cmp.sameVersion"))}</p>`;
      return;
    }
    m.result.innerHTML = `<p class="muted">${esc(tr("cmp.loading"))}</p>`;
    let html;
    try {
      const cmp = await api("GET",
        `/api/v1/processes/${encodeURIComponent(toSel.value)}/comparison?with=${encodeURIComponent(fromSel.value)}`);
      html = changesHTML(cmp);
    } catch (e) {
      html = `<p class="vc-error">${esc(tr("cmp.failed", { error: (e && e.message) || String(e) }))}</p>`;
    }
    if (mine === seq && m.ov.isConnected) m.result.innerHTML = html;
  };
  fromSel.addEventListener("change", load);
  toSel.addEventListener("change", load);
  m.ov.querySelector("#vc-swap").addEventListener("click", () => {
    const f = fromSel.value;
    fromSel.value = toSel.value;
    toSel.value = f;
    load();
  });
  load();
  await m.closed;
  return true;
}

// showChangesDialog shows a comparison the caller already holds — a draft's with a
// version — under a title and a sentence that say which two records it compares.
export async function showChangesDialog({ title, intro, cmp }) {
  const head = intro ? `<p class="muted" style="margin:0 0 10px">${esc(intro)}</p>` : "";
  const m = openModal({ title, head });
  m.result.innerHTML = changesHTML(cmp);
  await m.closed;
}
