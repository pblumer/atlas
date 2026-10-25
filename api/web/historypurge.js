// Deleting a process's finished instances for good, from the Operations overview
// (ADR-0486).
//
// The server answers what a purge would consider without changing anything
// (GET …/history-purge) and deletes in bounded batches (POST, repeated while it says
// more remain). This is the surface that makes both usable: the administrator picks a
// version or all of them and a cut-off, reads how many instances finished before it,
// types the process id, and watches the batches run, which can be stopped between two.
//
// It also says the three things a person is least likely to expect: that deleting
// frees no disk until the WAL is compacted, that the overview's finished count does
// not go down, and that a process run again and again is better served by
// atlas:historyTtl than by this dialog.
//
// It lives in its own module for the reason the migration dialog does: app.js boots
// the whole console on import, so anything left in it is only ever exercised by hand.

import { fmtCount } from "./numfmt.js";
import { plural, t } from "./i18n.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// How many batches one version may take before the walk gives up. A version of five
// million instances is five thousand batches at the server's default; the guard is a
// backstop against a server that never stops saying "remaining", not the expected exit.
const BATCH_GUARD = 20000;

// localInputValue writes a moment the way a datetime-local input holds it — the
// viewer's own clock, to the second — so the cut-off a person reads is the one they
// see on every other screen.
export function localInputValue(date) {
  const p = (n) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${p(date.getMonth() + 1)}-${p(date.getDate())}T${p(date.getHours())}:${p(date.getMinutes())}:${p(date.getSeconds())}`;
}

// cutOffParam turns the input's local value into the RFC 3339 moment both the preview
// and every batch carry, so they mean the same instances. "" when the input holds no
// moment.
export function cutOffParam(value) {
  if (!value) return "";
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? "" : d.toISOString();
}

// purgeTotals sums the previews of the versions chosen.
export function purgeTotals(plans) {
  const out = { finished: 0, running: 0, enabled: plans.length > 0, exporter: false, ttl: false };
  for (const p of plans) {
    out.finished += p.finished || 0;
    out.running += p.running || 0;
    if (!p.enabled) out.enabled = false;
    if (p.gate === "exporter") out.exporter = true;
    if (p.historyTtlSeconds > 0) out.ttl = true;
  }
  return out;
}

const versionLabel = (v) => `v${v.version}${v.versionTag ? ` · ${v.versionTag}` : ""}`;

// purgeFinishedFlow opens the dialog and runs the purge the administrator confirms.
// It resolves with how many instances were deleted (0 when nothing was), after onDone
// has re-read whatever the caller shows.
export function purgeFinishedFlow({ api, toast, processId, processName, versions, onDone }) {
  const deployed = (versions || []).slice().sort((a, b) => b.version - a.version);
  return new Promise((resolve) => {
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal confirm-modal purge-modal" role="dialog" aria-modal="true" aria-labelledby="purge-title">
        <div class="modal-head"><h2 id="purge-title">${esc(t("purge.title", { name: processName || processId }))}</h2></div>
        <div class="modal-body">
          <p class="muted" style="margin:0 0 10px">${esc(t("purge.intro"))}</p>
          <div class="row" style="gap:10px;flex-wrap:wrap">
            <label class="field" style="flex:1 1 160px"><span>${esc(t("purge.version"))}</span>
              <select id="purge-version">
                ${deployed.length > 1 ? `<option value="all">${esc(t("purge.version.all"))}</option>` : ""}
                ${deployed.map((v) => `<option value="${esc(String(v.key))}">${esc(versionLabel(v))}</option>`).join("")}
              </select>
            </label>
            <label class="field" style="flex:1 1 220px"><span>${esc(t("purge.before"))}</span>
              <input id="purge-before" type="datetime-local" step="1"/>
            </label>
          </div>
          <div class="purge-preview" aria-live="polite"></div>
          <ul class="purge-notes muted">
            <li>${esc(t("purge.disk"))}</li>
            <li>${esc(t("purge.counter"))}</li>
          </ul>
          <label class="field purge-confirm-field"><span>${esc(t("purge.confirm", { id: processId }))}</span>
            <input id="purge-confirm" type="text" autocomplete="off" spellcheck="false" placeholder="${esc(processId)}"/>
          </label>
          <div class="purge-progress" aria-live="polite" hidden></div>
        </div>
        <div class="modal-foot">
          <button class="btn neutral" data-purge-close>${esc(t("common.cancel"))}</button>
          <button class="btn neutral" data-purge-stop hidden>${esc(t("purge.stop"))}</button>
          <button class="btn danger" data-purge-go disabled>${esc(t("purge.go"))}</button>
        </div>
      </div>`;
    document.body.appendChild(ov);

    const versionSel = ov.querySelector("#purge-version");
    const beforeIn = ov.querySelector("#purge-before");
    const confirmIn = ov.querySelector("#purge-confirm");
    const previewEl = ov.querySelector(".purge-preview");
    const progressEl = ov.querySelector(".purge-progress");
    const closeBtn = ov.querySelector("[data-purge-close]");
    const stopBtn = ov.querySelector("[data-purge-stop]");
    const goBtn = ov.querySelector("[data-purge-go]");
    beforeIn.value = localInputValue(new Date());

    let plans = []; // the previews of the versions chosen, in the order they run
    let reading = 0; // which preview request is current: an older answer is dropped
    let busy = false;
    let stopped = false;
    let purged = 0;

    const chosen = () => (versionSel.value === "all" ? deployed : deployed.filter((v) => String(v.key) === versionSel.value));
    const sync = () => {
      const tot = purgeTotals(plans);
      goBtn.disabled = busy || !tot.enabled || tot.finished === 0 || !cutOffParam(beforeIn.value) ||
        confirmIn.value.trim() !== processId;
    };
    const finish = async () => {
      ov.remove();
      document.removeEventListener("keydown", onKey);
      if (purged && onDone) await onDone();
      resolve(purged);
    };
    const onKey = (e) => { if (e.key === "Escape" && !busy) finish(); };
    document.addEventListener("keydown", onKey);

    const renderPreview = () => {
      const tot = purgeTotals(plans);
      const lines = [];
      lines.push(`<p class="purge-count"><b>${esc(tot.finished
        ? plural("purge.count", tot.finished, { n: fmtCount(tot.finished) })
        : t("purge.none"))}</b></p>`);
      if (plans.length > 1 && tot.finished) {
        lines.push(`<ul class="purge-versions">${plans.filter((p) => p.finished).map((p) =>
          `<li>v${esc(String(p.version))}: ${esc(fmtCount(p.finished))}</li>`).join("")}</ul>`);
      }
      if (tot.running) lines.push(`<p class="muted">${esc(plural("purge.running", tot.running, { n: fmtCount(tot.running) }))}</p>`);
      if (tot.exporter) lines.push(`<p class="muted">${esc(t("purge.gate.exporter"))}</p>`);
      if (!tot.ttl) lines.push(`<p class="muted">${esc(t("purge.ttl"))}</p>`);
      if (!tot.enabled && plans.length) lines.push(`<p class="purge-off">${esc(t("purge.disabled"))}</p>`);
      previewEl.innerHTML = lines.join("");
      sync();
    };

    const readPreview = async () => {
      const before = cutOffParam(beforeIn.value);
      const mine = ++reading;
      plans = [];
      sync();
      if (!before) { previewEl.innerHTML = ""; return; }
      previewEl.innerHTML = `<p class="muted">${esc(t("purge.loading"))}</p>`;
      try {
        const got = await Promise.all(chosen().map((v) =>
          api("GET", `/api/v1/processes/${encodeURIComponent(v.key)}/history-purge?before=${encodeURIComponent(before)}`)));
        if (mine !== reading) return;
        plans = got;
        renderPreview();
      } catch (e) {
        if (mine !== reading) return;
        previewEl.innerHTML = `<p class="purge-off">${esc(t("purge.readFailed", { error: e.message }))}</p>`;
      }
    };

    const showProgress = (total, held, note) => {
      progressEl.hidden = false;
      const lines = [`<p><b>${esc(t("purge.progress", { done: fmtCount(purged), total: fmtCount(total) }))}</b></p>`];
      if (held) lines.push(`<p class="muted">${esc(t("purge.held", { n: fmtCount(held) }))}</p>`);
      if (note) lines.push(note);
      progressEl.innerHTML = lines.join("");
    };

    const run = async () => {
      const before = cutOffParam(beforeIn.value);
      const total = purgeTotals(plans).finished;
      busy = true;
      stopped = false;
      for (const el of [versionSel, beforeIn, confirmIn]) el.disabled = true;
      goBtn.disabled = true;
      closeBtn.disabled = true;
      stopBtn.hidden = false;
      let held = 0;
      let failure = "";
      showProgress(total, held);
      try {
        for (const plan of plans) {
          if (stopped) break;
          if (!plan.finished) continue;
          const route = `/api/v1/processes/${encodeURIComponent(plan.processDefKey)}/history-purge` +
            `?confirm=${encodeURIComponent(processId)}&before=${encodeURIComponent(before)}`;
          let after = "";
          for (let guard = 0; guard < BATCH_GUARD && !stopped; guard++) {
            const res = await api("POST", after ? `${route}&after=${encodeURIComponent(after)}` : route);
            purged += res.purged || 0;
            held += res.held || 0;
            showProgress(total, held);
            if (!res.remaining) break;
            // A batch that says more remain without saying where to continue cannot be
            // repeated safely: from the cut-off it would meet the same held instances.
            if (!res.nextCursor || res.nextCursor === after) break;
            after = res.nextCursor;
          }
        }
      } catch (e) {
        failure = e.message;
      }
      busy = false;
      stopBtn.hidden = true;
      // One purge per dialog: what it did is on screen, and a second run would start
      // from a cut-off and a count that no longer describe the history.
      goBtn.hidden = true;
      closeBtn.disabled = false;
      closeBtn.textContent = t("common.close");
      const notes = [];
      if (failure) {
        notes.push(`<p class="purge-off">${esc(t("purge.failed", { error: failure }))}</p>`);
      } else if (stopped) {
        notes.push(`<p>${esc(t("purge.stopped"))}</p>`);
      } else {
        notes.push(`<p>${esc(plural("purge.done", purged, { n: fmtCount(purged) }))}</p>`);
      }
      if (purged) notes.push(`<p class="muted">${esc(t("purge.compactHint"))}</p>`);
      showProgress(total, held, notes.join(""));
      if (toast) {
        toast(failure ? t("purge.failed", { error: failure }) : plural("purge.done", purged, { n: fmtCount(purged) }),
          failure ? "err" : "ok");
      }
    };

    versionSel.addEventListener("change", readPreview);
    beforeIn.addEventListener("change", readPreview);
    confirmIn.addEventListener("input", sync);
    closeBtn.addEventListener("click", () => { if (!busy) finish(); });
    stopBtn.addEventListener("click", () => { stopped = true; stopBtn.disabled = true; });
    ov.addEventListener("click", (e) => { if (e.target === ov && !busy) finish(); });
    goBtn.addEventListener("click", () => { if (!goBtn.disabled) run(); });

    readPreview();
    setTimeout(() => confirmIn.focus(), 0);
  });
}

