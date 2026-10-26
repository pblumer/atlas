// The approvals a person may answer, and the ones asked on their behalf
// (ADR-0475 §9).
//
// An approval is asked for one exact action — a Worker, an operation, a resource,
// bound by the action's digest — when a policy says that action needs somebody's
// yes. It is answered here by a person, in their own session: a member of the
// approver group who is not the person the action would run for. The server checks
// all of that again when the answer is given, and the policy checks it once more
// when the action is next authorized; this page only offers what is likely to pass.
//
// Answering does not run the action. The policy reads the answers when the action is
// tried again, and when that is depends on the process — the shipped example waits on
// a user task. The page says so rather than suggesting a click starts something.
//
// Nothing on this page is a credential, and it keeps nothing in storage, the URL or
// the console; TestTheApprovalsPageHoldsNothing holds it to that.

import { fmtDateTime } from "./datefmt.js";
import { t } from "./i18n.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// Timestamps from the server are Unix nanoseconds.
const when = (ns) => (ns ? fmtDateTime(Math.floor(ns / 1e6)) : "—");

// sections sorts one page of the worklist by what the reader can do about each item.
export function sections(items) {
  const out = { waiting: [], answered: [], requested: [] };
  for (const a of items) {
    if (a.role === "requester") out.requested.push(a);
    else if (a.myAnswer) out.answered.push(a);
    else out.waiting.push(a);
  }
  return out;
}

// standing is where an approval's answers stand, as a pill: rejected wins, as it does
// in the policy; then whether the grants reach what the obligation asks.
function standing(a) {
  if (a.rejected) return `<span class="pill off" data-standing="rejected"><span class="dot"></span>${esc(t("approvals.standing.rejected"))}</span>`;
  const enough = a.granted >= (a.approvals || 1);
  return `<span class="pill ${enough ? "ok" : "warn"}" data-standing="${enough ? "granted" : "open"}"><span class="dot"></span>${esc(t("approvals.standing.count", { granted: a.granted, needed: a.approvals || 1 }))}</span>`;
}

function answerRow(x) {
  const what = x.kind === "revoked" ? t("approvals.answer.withdrawn")
    : x.outcome === "granted" ? t("approvals.answer.granted") : t("approvals.answer.rejected");
  return `<li data-answer-kind="${esc(x.kind)}">${esc(x.name || x.principal)} — ${esc(what)} · ${esc(when(x.at))}${x.reason ? ` — ${esc(t("approvals.answer.reason", { reason: x.reason }))}` : ""}</li>`;
}

function actions(a) {
  if (a.role === "requester") {
    return `<p class="muted" data-self style="margin-top:10px">${esc(t("approvals.self", { group: a.approverGroupName || a.approverGroup }))}</p>`;
  }
  if (a.myAnswer) {
    const mine = a.myAnswer.outcome === "granted" ? t("approvals.mine.granted") : t("approvals.mine.rejected");
    return `<div class="row" style="margin-top:12px; gap:8px; align-items:center">
      <span data-mine="${esc(a.myAnswer.outcome)}">${esc(mine)} · ${esc(when(a.myAnswer.at))}</span>
      <button class="btn ghost" type="button" data-act="withdrawn">${esc(t("approvals.withdraw"))}</button>
    </div>`;
  }
  return `<div style="margin-top:12px">
      <label class="field" style="margin-bottom:6px">${esc(t("approvals.reason"))}
        <textarea name="reason" rows="2" maxlength="500" placeholder="${esc(t("approvals.reason.placeholder"))}"></textarea></label>
      <div class="row" style="gap:8px">
        <button class="btn" type="button" data-act="granted">${esc(t("approvals.grant"))}</button>
        <button class="btn ghost danger" type="button" data-act="rejected">${esc(t("approvals.reject"))}</button>
      </div>
    </div>`;
}

function approvalCard(a, ctx) {
  const instance = ctx.canOperate
    ? `<a href="#/operations/i/${esc(a.processInstanceKey)}" data-instance-link>${esc(a.processId || t("approvals.instance"))} · ${esc(a.processInstanceKey)}</a>`
    : `${esc(a.processId || t("approvals.instance"))} · ${esc(a.processInstanceKey)}`;
  const digest = String(a.actionDigest || "");
  return `<div class="card" data-approval="${esc(a.approvalKey)}" data-instance="${esc(a.processInstanceKey)}" style="margin-top:12px">
    <div class="between">
      <div><span class="chip" data-operation>${esc(a.operation)}</span> <code data-resource style="word-break:break-all">${esc(a.resource)}</code></div>
      <div>${standing(a)}</div>
    </div>
    <div class="muted" style="font-size:13px; margin-top:8px; line-height:1.6">
      ${esc(t("approvals.for", { who: a.requestedByName || a.requestedBy }))} ·
      ${esc(t("approvals.worker", { worker: a.worker }))} ·
      ${esc(t("approvals.group", { group: a.approverGroupName || a.approverGroup }))}<br>
      ${esc(t("approvals.process"))}: ${instance} ·
      ${esc(t("approvals.policy", { policy: a.policyId, version: a.policyVersion }))}<br>
      ${esc(t("approvals.requestedAt", { when: when(a.requestedAt) }))} ·
      <b>${esc(t("approvals.expires", { when: when(a.expiresAt) }))}</b> ·
      <span title="${esc(t("approvals.digest.title"))}">${esc(t("approvals.digest"))} <code>${esc(digest.slice(0, 12))}</code></span>
    </div>
    ${(a.answers || []).length ? `<ul class="muted" data-answers style="font-size:13px; margin:8px 0 0 18px">${a.answers.map(answerRow).join("")}</ul>` : ""}
    ${actions(a)}
  </div>`;
}

function section(id, title, intro, items, empty, ctx) {
  return `<section id="${id}" style="margin-top:22px">
    <h2>${esc(title)}</h2>
    <p class="muted">${esc(intro)}</p>
    ${items.map((a) => approvalCard(a, ctx)).join("") || `<p class="muted" data-empty>${esc(empty)}</p>`}
  </section>`;
}

// confirmText names the exact action an answer is about, so nobody approves a
// different deletion than the one they think they are approving.
function confirmText(a, outcome) {
  const key = outcome === "granted" ? "approvals.grant.confirm"
    : outcome === "rejected" ? "approvals.reject.confirm" : "approvals.withdraw.confirm";
  return t(key, { operation: a.operation, resource: a.resource, who: a.requestedByName || a.requestedBy, worker: a.worker });
}

export async function viewApprovals({ api, toast, view, isSuperseded, signedIn, canOperate }) {
  if (!signedIn) {
    view.innerHTML = `<div class="card"><h1>${esc(t("approvals.title"))}</h1>
      <p class="muted" id="apr-signin">${esc(t("approvals.signIn"))}</p></div>`;
    return;
  }
  const ctx = { canOperate: !!canOperate };
  let page;
  try {
    page = await api("GET", "/api/v1/me/action-approvals");
  } catch (e) {
    if (isSuperseded()) return;
    if (e.status === 403 || e.status === 401) {
      view.innerHTML = `<div class="card"><h1>${esc(t("approvals.title"))}</h1>
        <p class="muted" id="apr-denied">${esc(t("approvals.denied"))}</p></div>`;
      return;
    }
    throw e;
  }
  if (isSuperseded()) return;
  const items = (page && page.items) || [];
  const byKey = new Map(items.map((a) => [`${a.processInstanceKey}:${a.approvalKey}`, a]));
  const s = sections(items);

  view.innerHTML = `<div id="approvals">
    <div class="card">
      <h1>${esc(t("approvals.title"))}</h1>
      <p class="muted">${esc(t("approvals.intro"))}</p>
      <p class="muted" style="font-size:12px; margin-top:6px">${esc(t("approvals.after"))}</p>
    </div>
    ${page && page.truncated ? `<div class="warn-note" id="apr-truncated" role="status" style="margin-top:18px">${esc(t("approvals.truncated", { n: items.length }))}</div>` : ""}
    ${section("apr-waiting", t("approvals.waiting.title"), t("approvals.waiting.intro"), s.waiting, t("approvals.waiting.empty"), ctx)}
    ${section("apr-answered", t("approvals.answered.title"), t("approvals.answered.intro"), s.answered, t("approvals.answered.empty"), ctx)}
    ${s.requested.length ? section("apr-requested", t("approvals.onBehalf.title"), t("approvals.onBehalf.intro"), s.requested, "", ctx) : ""}
  </div>`;

  const root = document.getElementById("approvals");
  root.addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-act]");
    if (!btn) return;
    const card = btn.closest("[data-approval]");
    const a = byKey.get(`${card.dataset.instance}:${card.dataset.approval}`);
    if (!a) return;
    const outcome = btn.dataset.act;
    if (!window.confirm(confirmText(a, outcome))) return;
    const reasonField = card.querySelector("textarea[name=reason]");
    const reason = reasonField ? reasonField.value.trim() : "";
    card.querySelectorAll("button").forEach((b) => { b.disabled = true; });
    try {
      await api("POST", `/api/v1/instances/${encodeURIComponent(a.processInstanceKey)}/action-approvals/${encodeURIComponent(a.approvalKey)}/decide`,
        { outcome, reason });
      toast(outcome === "granted" ? t("approvals.done.granted") : outcome === "rejected" ? t("approvals.done.rejected") : t("approvals.done.withdrawn"));
    } catch (err) {
      toast(err.message, "err");
    }
    // The server holds the answers; the page is read again rather than patched, so
    // what it shows is what was recorded — including an answer that was refused.
    if (isSuperseded()) return;
    try {
      await viewApprovals({ api, toast, view, isSuperseded, signedIn, canOperate });
    } catch (err) {
      toast(err.message, "err");
    }
  });
}

// renderWaitingApprovals tells a signed-in person, on the dashboard, that approvals
// wait for their answer. Silent when none do and on failure: it is an addition to a
// page that works without it.
export async function renderWaitingApprovals(slot, { api }) {
  if (!slot) return;
  let page;
  try { page = await api("GET", "/api/v1/me/action-approvals"); } catch { return; }
  const waiting = sections((page && page.items) || []).waiting;
  if (!waiting.length) return;
  const n = waiting.length;
  const intro = page.truncated ? t("approvals.dashboard.introMore", { n })
    : n === 1 ? t("approvals.dashboard.intro.one") : t("approvals.dashboard.intro.other", { n });
  slot.innerHTML = `<div class="card approvals-waiting" id="approvals-waiting" role="status">
    <h2>${esc(t("approvals.dashboard.title"))}</h2>
    <p>${esc(intro)}</p>
    <div class="row"><a class="btn" href="#/console/approvals">${esc(t("approvals.dashboard.open"))}</a></div>
  </div>`;
}
