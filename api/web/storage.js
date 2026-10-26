// The disk under the data directory, as the Console's dashboard shows it to an
// administrator (ADR-0481).
//
// Two pieces from one request. The card is always there for an administrator: each file
// system the server writes to, how full it is, where the reserve begins, and how soon the
// consumption reaches it. The alert above the dashboard appears only when there is
// something to do — the room running low, the server frozen below its reserve, or the
// engine halted after a failed write — and says what that is. Both are silent when the
// request fails: the dashboard works without them.
//
// The words are the Console's, keyed by the state the server reports (ADR-0267); the
// server sends numbers and ids only.
//
// Below the disk, the card says how Atlas gives space back and offers it
// (ADR-0486): whether the WAL is compacted on
// every checkpoint, what the last pass did, a checkpoint now and — on a server not
// started with --compact-wal — one compaction pass after a confirmation. It reads the
// checkpoint status with ?verify=false: the full read re-hashes every checkpoint, which
// on a large store is minutes of disk for every dashboard load.

import { fmtCount } from "./numfmt.js";
import { fmtDateTime } from "./datefmt.js";
import { plural, t } from "./i18n.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// The states the server reports, and the pill each wears. Kept in step with
// api/storage by TestTheStorageCardHasWordsForEveryState.
export const STATES = ["ok", "warn", "frozen", "halted", "unknown"];
const PILL = { ok: "ok", warn: "warn", frozen: "err", halted: "err", unknown: "off" };

// fmtBytes prints a size in binary units, one decimal below a hundred, with the decimal
// mark of the region this person chose.
export function fmtBytes(n) {
  const v = Number(n);
  if (!Number.isFinite(v) || v < 0) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let i = 0;
  let x = v;
  while (x >= 1024 && i < units.length - 1) { x /= 1024; i++; }
  const shown = i === 0 || x >= 100 ? Math.round(x) : Math.round(x * 10) / 10;
  return `${fmtCount(shown)} ${units[i]}`;
}

// whenText says how long until something, in the coarsest unit that still says it: a
// forecast smoothed over six hours is not precise to the minute, and pretending it is
// would be read as a promise.
export function whenText(seconds) {
  const s = Number(seconds);
  if (!Number.isFinite(s) || s < 0) return "";
  if (s > 365 * 86400) return t("storage.when.year");
  if (s < 2 * 86400) return plural("storage.when.hours", Math.max(1, Math.round(s / 3600)));
  return plural("storage.when.days", Math.round(s / 86400));
}

// cadenceText says how often checkpoints are taken: minutes below an hour, hours
// above. A cadence is configured in whole minutes in practice, so nothing finer is said.
export function cadenceText(seconds) {
  const s = Number(seconds);
  if (!Number.isFinite(s) || s <= 0) return "";
  if (s < 3600) return plural("storage.every.minutes", Math.max(1, Math.round(s / 60)));
  return plural("storage.every.hours", Math.round(s / 3600));
}

// renderStorage fills the dashboard's alert slot and card slot from one reading.
export async function renderStorage({ alertSlot, cardSlot, api, confirm = (m) => window.confirm(m) }) {
  let s;
  try { s = await api("GET", "/api/v1/storage"); } catch { return; }
  if (!s || !STATES.includes(s.state)) return;
  if (alertSlot) alertSlot.innerHTML = alertHTML(s);
  if (!cardSlot) return;
  // The disk first, on its own: it is what the card is for, and it must not wait on the
  // checkpoint status, which is a second request that can be slow or fail. The reclaim
  // section fills its slot when that answer arrives, and stays empty when it does not.
  cardSlot.innerHTML = cardHTML(s);
  let cp;
  try { cp = await api("GET", "/api/v1/checkpoints?verify=false"); } catch { return; }
  const slot = cardSlot.querySelector(".storage-reclaim-slot");
  if (!slot) return;
  slot.innerHTML = reclaimHTML(cp);
  wireReclaim(cardSlot, { api, confirm, rerender: () => renderStorage({ alertSlot, cardSlot, api, confirm }) });
}

// wireReclaim binds the card's buttons. A pass can take minutes on a large store —
// verifying a checkpoint reads all of it — and a proxy in front of the server may give
// up first, so a missing answer is said as one, not as a failed pass.
function wireReclaim(slot, { api, confirm, rerender }) {
  const buttons = [...slot.querySelectorAll("[data-reclaim]")];
  for (const b of buttons) {
    b.addEventListener("click", async () => {
      const compact = b.dataset.reclaim === "compact";
      if (compact && !confirm(t("storage.reclaim.confirm"))) return;
      const label = b.textContent;
      for (const x of buttons) x.disabled = true;
      b.textContent = t("storage.reclaim.running");
      let msg;
      try {
        msg = passText(await api("POST", compact ? "/api/v1/checkpoints?compact=true" : "/api/v1/checkpoints"));
        // The card is drawn again from a fresh reading, so the disk and the last pass
        // it shows are the ones this pass left; the answer goes under the buttons.
        await rerender();
      } catch (e) {
        msg = !e.status || e.status >= 500
          ? t("storage.reclaim.noAnswer", { error: e.message })
          : t("storage.reclaim.failed", { error: e.message });
      }
      // Still on screen means the card was not drawn again — the pass failed, or the
      // fresh reading did — so the buttons are given back.
      if (b.isConnected) {
        for (const x of buttons) x.disabled = false;
        b.textContent = label;
      }
      const out = slot.querySelector(".storage-reclaim-result");
      if (out) out.textContent = msg;
    });
  }
}

// passText says what one checkpoint pass did, in the words a person reads after pressing
// the button.
export function passText(p) {
  if (!p) return "";
  if (p.checkpointError) return t("storage.reclaim.checkpointFailed", { error: p.checkpointError });
  const parts = [p.position ? t("storage.reclaim.checkpointAt", { position: fmtCount(p.position) }) : t("storage.reclaim.checkpointNone")];
  if (p.compaction) {
    if (p.compactionError) parts.push(t("storage.reclaim.compactionFailed", { error: p.compactionError }));
    else if (p.segmentsRemoved > 0) parts.push(plural("storage.reclaim.removed", p.segmentsRemoved, { n: fmtCount(p.segmentsRemoved) }));
    else parts.push(p.note ? t("storage.reclaim.skipped", { note: p.note }) : t("storage.reclaim.nothing"));
  }
  return parts.join(" ");
}

// reclaimHTML is how this server gives disk back, from the checkpoint status. Nothing
// when the status could not be read.
export function reclaimHTML(cp) {
  if (!cp) return "";
  const lines = [`<h3 class="storage-reclaim-title">${esc(t("storage.reclaim.title"))}</h3>`];
  if (!cp.enabled) {
    lines.push(`<p class="muted">${esc(t("storage.reclaim.noCheckpoints"))}</p>`);
    return `<div class="storage-reclaim">${lines.join("\n      ")}</div>`;
  }
  const every = cadenceText(cp.intervalSeconds || 0);
  lines.push(`<p>${esc(cp.compaction ? t("storage.reclaim.on", { every }) : t("storage.reclaim.off"))}</p>`);
  if (cp.lastPass) {
    const when = fmtDateTime(new Date(Number(cp.lastPass.at) / 1e6));
    lines.push(`<p class="muted">${esc(t("storage.reclaim.last", { when }))} ${esc(passText(cp.lastPass))}</p>`);
  }
  const buttons = cp.compaction
    ? [`<button class="btn" data-reclaim="checkpoint">${esc(t("storage.reclaim.checkpointCompact"))}</button>`]
    : [`<button class="btn neutral" data-reclaim="checkpoint">${esc(t("storage.reclaim.checkpoint"))}</button>`,
      `<button class="btn danger" data-reclaim="compact">${esc(t("storage.reclaim.compactOnce"))}</button>`];
  lines.push(`<div class="row storage-reclaim-actions">${buttons.join("")}</div>`);
  lines.push(`<p class="storage-reclaim-result" aria-live="polite"></p>`);
  lines.push(`<p class="muted">${esc(t("storage.reclaim.purgeHint"))}</p>`);
  return `<div class="storage-reclaim">${lines.join("\n      ")}</div>`;
}

function deciding(s) {
  const fss = Array.isArray(s.fileSystems) ? s.fileSystems : [];
  return fss.find((fs) => fs.decides) || fss[0] || null;
}

// alertHTML is the notice above the dashboard, or nothing when all is well.
export function alertHTML(s) {
  if (s.state !== "warn" && s.state !== "frozen" && s.state !== "halted") return "";
  const fs = deciding(s);
  const params = {
    available: fs ? fmtBytes(fs.availableBytes) : "—",
    reserve: fmtBytes(s.reserveBytes),
    resume: fmtBytes(s.resumeBytes),
  };
  const until = s.forecast && s.forecast.secondsUntilFreeze != null ? whenText(s.forecast.secondsUntilFreeze) : "";
  const lines = [`<p>${esc(t(`storage.alert.${s.state}.text`, params))}</p>`];
  if (s.state === "warn" && until) lines.push(`<p>${esc(t("storage.alert.warn.forecast", { when: until }))}</p>`);
  if (s.state !== "warn") lines.push(`<p class="muted">${esc(t(`storage.alert.${s.state}.hint`, params))}</p>`);
  const role = s.state === "warn" ? "status" : "alert";
  return `<div class="card storage-alert storage-${esc(s.state)}" id="storage-alert" role="${role}">
    <h2>${esc(t(`storage.alert.${s.state}.title`))}</h2>
    ${lines.join("\n    ")}
    <div class="row"><a class="btn ghost" href="/handbuch.html#speicher" target="_blank" rel="noopener">${esc(t("storage.handbook"))} ↗</a></div>
  </div>`;
}

// barHTML draws one file system: what is used against what this server can ever have
// (used + available, as df counts it), and a mark where the reserve begins.
function barHTML(fs, s) {
  const room = Number(fs.usedBytes) + Number(fs.availableBytes);
  const used = room > 0 ? Math.min(100, (100 * fs.usedBytes) / room) : 0;
  const mark = room > 0 && s.reserveBytes > 0 ? Math.max(0, 100 - (100 * s.reserveBytes) / room) : null;
  const percent = fmtCount(fs.usedPercent);
  return `<div class="storage-bar" role="img" aria-label="${esc(t("storage.bar.label", { percent }))}">
      <span class="storage-used storage-${esc(s.state)}" style="width:${used.toFixed(1)}%"></span>
      ${mark == null ? "" : `<span class="storage-reserve" style="left:${mark.toFixed(1)}%" title="${esc(t("storage.fs.reserve", { reserve: fmtBytes(s.reserveBytes) }))}"></span>`}
    </div>`;
}

function fileSystemHTML(fs, s) {
  const paths = (fs.paths || []).map((p) => `<code>${esc(p)}</code>`).join(" ");
  const nums = [
    t("storage.fs.used", { used: fmtBytes(fs.usedBytes) }) + ` (${fmtCount(fs.usedPercent)} %)`,
    t("storage.fs.available", { available: fmtBytes(fs.availableBytes) }),
    t("storage.fs.total", { total: fmtBytes(fs.totalBytes) }),
  ];
  if (s.reserveBytes > 0) nums.push(t("storage.fs.reserve", { reserve: fmtBytes(s.reserveBytes) }));
  return `<div class="storage-fs">
    <div class="storage-paths">${paths}</div>
    ${barHTML(fs, s)}
    <div class="storage-nums muted">${nums.map(esc).join(" · ")}</div>
  </div>`;
}

function forecastHTML(s) {
  const f = s.forecast;
  if (!f) return `<p class="muted storage-forecast">${esc(t("storage.forecast.pending"))}</p>`;
  if (!(f.bytesPerSecond > 0) || f.secondsUntilFreeze == null) {
    return `<p class="storage-forecast">${esc(t("storage.forecast.none"))}</p>`;
  }
  const rate = t("storage.forecast.rate", { perDay: fmtBytes(f.bytesPerDay) });
  const until = t("storage.forecast.until", { when: whenText(f.secondsUntilFreeze) });
  return `<p class="storage-forecast">${esc(rate)} · ${esc(until)}
    <span class="muted">(${esc(t("storage.forecast.basis"))})</span></p>`;
}

// cardHTML is the administrator's card: the disk at a glance, then what it is used for,
// then how to give it back when the checkpoint status could be read.
export function cardHTML(s, cp) {
  const fss = Array.isArray(s.fileSystems) ? s.fileSystems : [];
  const atlas = s.atlas || {};
  const parts = [];
  if (s.error) parts.push(`<p class="storage-error">${esc(t("storage.unreadable", { error: s.error }))}</p>`);
  parts.push(...fss.map((fs) => fileSystemHTML(fs, s)));
  if (fss.length) parts.push(forecastHTML(s));
  parts.push(`<p class="muted">${esc(plural("storage.atlas", atlas.walSegments || 0, {
    wal: fmtBytes(atlas.walBytes), segments: fmtCount(atlas.walSegments || 0), state: fmtBytes(atlas.stateBytes),
  }))}</p>`);
  if (!(s.reserveBytes > 0)) parts.push(`<p class="muted">${esc(t("storage.freezeOff"))}</p>`);
  parts.push(`<div class="storage-reclaim-slot">${reclaimHTML(cp)}</div>`);
  parts.push(`<p class="muted storage-note">${esc(t("storage.note"))}
    <a href="/handbuch.html#speicher" target="_blank" rel="noopener">${esc(t("storage.handbook"))} ↗</a></p>`);
  return `<div class="card storage-card" id="storage-card">
    <div class="between"><h2>${esc(t("storage.title"))}</h2>
      <span class="pill ${PILL[s.state]}"><span class="dot"></span>${esc(t(`storage.state.${s.state}`))}</span></div>
    ${parts.join("\n    ")}
  </div>`;
}
