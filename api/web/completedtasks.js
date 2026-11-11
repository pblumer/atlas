// The Tasks app's "Completed" view (ADR-0554): the tasks the signed-in person
// completed, newest first, each shown again read-only in the revision of its form it
// was answered through, with the values as they stood once it was answered.
//
// It is a view of its own rather than a folder: folders are filters over open tasks
// (ADR-0268), and a completed task has no inbox semantics left — nothing to claim,
// nothing to complete. app.js hands it the list and detail panes while the view is
// selected and takes them back when another folder is chosen.

import { fmtDateTime } from "./datefmt.js";
import { loadFormViewer } from "./formviewer.js";
import { printTaskDetail } from "./printtask.js";
import { t as tr } from "./i18n.js";

const PAGE = 50;

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// when renders the completion instant in the time zone the reader chose, as every
// other timestamp of the Console does.
function when(iso) {
  return fmtDateTime(iso) || iso;
}

// createCompletedView keeps the view's own state — the loaded rows, the cursor, the
// selection — so switching folders and back does not refetch, and the inbox's periodic
// refresh does not reset what the person is reading.
export function createCompletedView({ api, listEl, detailEl, titleEl, me = () => "" }) {
  const state = { items: null, next: "", selected: null, loading: false, error: "" };
  let mounted = null;
  // rendered is the task the detail pane shows, so a redraw the inbox asks for — its
  // periodic refresh — leaves a task being read alone instead of drawing it again.
  let rendered = null;

  function destroy() {
    if (mounted) {
      try { mounted.destroy(); } catch { /* already gone */ }
      mounted = null;
    }
    rendered = null;
  }

  async function load({ more = false } = {}) {
    if (state.loading) return;
    state.loading = true;
    state.error = "";
    try {
      const q = `/api/v1/tasks/completed?limit=${PAGE}` + (more && state.next ? "&cursor=" + encodeURIComponent(state.next) : "");
      const page = await api("GET", q);
      state.items = more ? (state.items || []).concat(page.items || []) : (page.items || []);
      state.next = page.nextCursor || "";
    } catch (err) {
      state.error = err.message || String(err);
      if (!more) state.items = [];
    } finally {
      state.loading = false;
    }
  }

  function renderList(query = "") {
    titleEl.textContent = tr("tasks.completed.title");
    if (state.items === null) {
      listEl.innerHTML = `<li class="tasks-empty muted">${esc(tr("tasks.completed.loading"))}</li>`;
      return;
    }
    const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
    const rows = state.items.filter((it) => {
      const hay = [it.name, it.processName, it.processId, it.elementId].filter(Boolean).join(" ").toLowerCase();
      return words.every((w) => hay.includes(w));
    });
    const note = `<li class="tasks-completed-note muted">${esc(tr("tasks.completed.retention"))}</li>`;
    const error = state.error ? `<li class="tasks-empty err">${esc(state.error)}</li>` : "";
    if (!rows.length) {
      listEl.innerHTML = error + `<li class="tasks-empty muted">${esc(tr(words.length ? "tasks.completed.noMatch" : "tasks.completed.empty"))}</li>` + note;
      return;
    }
    listEl.innerHTML = error + rows.map((it) => `
      <li class="tasks-item${it.key === state.selected ? " selected" : ""}" data-key="${esc(it.key)}" tabindex="0">
        <div class="tasks-item-title">${esc(it.name || it.elementId || it.key)}</div>
        <div class="tasks-item-meta muted">${esc(it.processName || it.processId || "")} · ${esc(tr("tasks.completed.at", { when: when(it.completedAt) }))}</div>
      </li>`).join("") +
      (state.next ? `<li class="tasks-more"><button class="btn ghost small" id="task-completed-more">${esc(tr("tasks.completed.more"))}</button></li>` : "") +
      note;
    listEl.querySelectorAll(".tasks-item").forEach((li) => {
      const pick = () => { state.selected = li.dataset.key; renderList(query); renderDetail({ force: true }); };
      li.addEventListener("click", pick);
      li.addEventListener("keydown", (e) => { if (e.key === "Enter") pick(); });
    });
    const more = listEl.querySelector("#task-completed-more");
    if (more) more.addEventListener("click", async () => { await load({ more: true }); renderList(query); });
  }

  // answersList shows a task's answers as plain label / value pairs: the view of a task
  // whose form was deleted, and the fallback when its form cannot be drawn.
  function answersList(values) {
    const rows = Object.entries(values || {}).sort(([a], [b]) => a.localeCompare(b));
    if (!rows.length) return `<p class="muted">${esc(tr("tasks.completed.noValues"))}</p>`;
    return `<dl class="tasks-completed-values">${rows.map(([k, v]) =>
      `<dt>${esc(k)}</dt><dd>${esc(typeof v === "object" ? JSON.stringify(v) : v)}</dd>`).join("")}</dl>`;
  }

  async function renderDetail({ force = false } = {}) {
    if (!force && state.selected && rendered === state.selected) return;
    destroy();
    rendered = state.selected;
    const grid = detailEl.closest(".tasks");
    if (grid) grid.classList.toggle("has-selection", !!state.selected);
    if (!state.selected) {
      detailEl.innerHTML = `<div class="tasks-form-placeholder"><p class="muted">${esc(tr("tasks.completed.pick"))}</p></div>`;
      return;
    }
    const key = state.selected;
    detailEl.innerHTML = `<p class="muted">${esc(tr("tasks.completed.loading"))}</p>`;
    let view;
    try {
      view = await api("GET", "/api/v1/tasks/completed/" + encodeURIComponent(key));
    } catch (err) {
      if (state.selected === key) detailEl.innerHTML = `<p class="muted err">${esc(err.message || String(err))}</p>`;
      return;
    }
    if (state.selected !== key) return;
    const task = view.task || {};
    const erased = (view.erased || []).length
      ? `<p class="muted">${esc(tr("tasks.completed.erased", { fields: view.erased.join(", ") }))}</p>` : "";
    const missing = view.formMissing ? `<p class="muted">${esc(tr("tasks.completed.formMissing"))}</p>` : "";
    const row = (label, val) =>
      `<div class="tasks-field"><span class="tasks-field-label muted">${esc(label)}</span><span>${val}</span></div>`;
    detailEl.innerHTML = `
      <header class="tasks-detail-head">
        <h1>${esc(task.name || task.elementId || task.key)}</h1>
        <div class="tasks-detail-actions">
          <span class="tasks-completed-badge">${esc(tr("tasks.completed.byYou", { when: when(task.completedAt) }))}</span>
          <button class="btn ghost" id="task-completed-print" title="${esc(tr("tasks.print.title"))}">${esc(tr("tasks.print"))}</button>
        </div>
      </header>
      <div class="tasks-fields">
        ${row("Process", esc((task.processName || task.processId || "—") + (task.version ? " · v" + task.version : "")))}
        ${row("Element", `<span class="chip">${esc(task.elementId || "—")}</span>`)}
        ${row(tr("tasks.completed.folder"), esc(when(task.completedAt)))}
        ${row("Instance", `<span class="chip">${esc(task.processInstanceKey)}</span>`)}
        ${row("Task key", `<span class="chip">${esc(task.key)}</span>`)}
      </div>
      ${missing}${erased}
      <div id="task-completed-form"></div>
      <p class="muted tasks-completed-help" data-print="omit">${esc(tr("tasks.completed.help"))}</p>`;
    // Printed, the actions and the badge stay off the paper; the completion is still
    // there in its own row above (ADR-0555 §6).
    const printBtn = detailEl.querySelector("#task-completed-print");
    if (printBtn) printBtn.addEventListener("click", () => printTaskDetail(detailEl, { who: me() }));
    const host = detailEl.querySelector("#task-completed-form");
    if (!view.form) {
      host.innerHTML = answersList(view.values);
      return;
    }
    try {
      const { Form } = await loadFormViewer();
      if (state.selected !== key) return;
      const form = new Form({ container: host, properties: { readOnly: true } });
      await form.importSchema(view.form.schema, view.values || {});
      if (state.selected !== key) { try { form.destroy(); } catch { /* noop */ } return; }
      mounted = form;
    } catch {
      // A form that cannot be drawn read-only still has answers to show.
      host.innerHTML = answersList(view.values);
    }
  }

  return {
    // show draws the view, loading its first page the first time or when asked.
    async show({ reload = false, query = "" } = {}) {
      if (reload || state.items === null) {
        state.items = state.items && !reload ? state.items : null;
        renderList(query);
        await load();
      }
      renderList(query);
      renderDetail({ force: reload });
    },
    renderList,
    renderDetail,
    hide() { destroy(); state.selected = null; },
  };
}
