// The questions a recertification campaign asks, and the two answers
// (ADR-0341).
//
// # Why this is in Tasks and not in Operations or the shop
//
// The reconciliation screen is the operator's: a finding is repair. This is not.
// It asks a line manager whether somebody on their team still needs something, and
// a line manager is an ordinary user who has never opened Operations.
//
// Nor is it one of the two shop pages. Those carry the *catalogue's* brand and
// are written for people outside the tooling — somebody ordering a laptop. The
// audience here is internal and it is exactly the Tasks audience: work addressed to
// you, waiting for a decision. It is a second kind of inbox, so it sits beside the
// first.
//
// # What this screen deliberately does not have
//
// **There is no way to answer more than one row.** No select-all, no "certify the
// rest", no keyboard shortcut that walks the list saying yes. That is the whole
// record in one sentence: a campaign answered in bulk is a signature with no
// reading behind it, and an attestation nobody read is worse than none, because an
// auditor believes it. The API has no bulk route either — this is not a screen that
// declines to offer what the server allows.
//
// **Undecided is shown as a number, not as a remainder.** A progress bar invites
// the reader to treat what is left as work in hand. "41 unanswered" is the finding.

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const fmtWhen = (unixSeconds) =>
  unixSeconds ? new Date(unixSeconds * 1000).toLocaleString() : "—";

// heldSince renders a nanosecond hold start as something a person can weigh. A
// right held for four years and one granted last Tuesday are the same row without
// it, and they are not the same question.
function heldSince(nanos) {
  if (!nanos) return "—";
  const days = Math.floor((Date.now() - nanos / 1e6) / 86_400_000);
  if (days >= 730) return `${Math.floor(days / 365)} years`;
  if (days >= 60) return `${Math.floor(days / 30)} months`;
  if (days >= 1) return `${days} days`;
  return "today";
}

// ORIGINS is what the inventory's knowledge is worth, in the reviewer's terms. It
// is the single most useful thing on a row: "somebody ordered this and it was
// approved" and "we found this in the estate and nobody knows why" deserve
// completely different answers to the same question.
const ORIGINS = {
  ordered: { pill: "", what: "Ordered through the catalogue, with whatever approval it carries." },
  adopted: { pill: "warn", what: "Found in the target system and accepted by a person — Atlas did not grant it." },
  legacy: { pill: "warn", what: "Found in place when the inventory was first taken. Nobody here decided it." },
};

function originPill(origin) {
  if (!origin) return "";
  const o = ORIGINS[origin] || { pill: "", what: "" };
  return `<span class="pill ${o.pill}" title="${esc(o.what)}">${esc(origin)}</span>`;
}

// disputePill is the warning that has to be on the row rather than in a footnote.
// Certifying a right the target system currently denies is signing a statement
// about something contested, and the reviewer has to see that before they answer,
// not after.
// endsPill is the good news on a row: this right ends by itself, so the question
// did not really need asking. Shown rather than used to hide the row — a reviewer
// who is not shown a row cannot notice that its end is wrong.
function endsPill(row) {
  if (!row.until) return "";
  const when = fmtWhen(Math.floor(row.until / 1e9));
  return ` <span class="pill" title="This right ends on its own. Confirming it does not extend it — extending access is ordering it again, with whatever approval it carries.">ends ${esc(when)}</span>`;
}

function disputePill(row) {
  if (!row.disputed) return "";
  const what = row.disputeKind === "missing"
    ? "Atlas records this right and the target system does not report it. Certifying it attests to something the target system denies."
    : "A reconciliation finding stands against this right. Certifying it attests to something two systems disagree about.";
  return ` <span class="pill err" title="${esc(what)}">disputed</span>`;
}

export async function viewRecertification({ api, toast, view, isSuperseded }) {
  view.innerHTML = `
    <div class="between">
      <h1>Access review</h1>
      <button class="btn neutral" id="rct-refresh" title="Reload">Refresh</button>
    </div>
    <p class="muted">Whether the access people already have is still access they
    need. A right that was properly approved and properly provisioned can still be
    wrong — people change roles and keep what the old one needed — and this is the
    only question that catches it.</p>
    <p class="muted"><b>One row at a time, and only yours.</b> There is deliberately
    no way to answer several at once: an attestation is worth exactly the reading
    behind it. A row you leave alone stays <i>unanswered</i> and is never recorded as
    certified.</p>
    <div class="row" style="margin:0 0 10px">
      <label class="field inline" style="margin:0">Campaign
        <select id="rct-campaign" style="margin-left:6px"></select></label>
      <label class="field inline" style="margin:0 0 0 14px">
        <input type="checkbox" id="rct-mine" checked /> Only rows I can answer</label>
    </div>
    <div id="rct-summary" class="muted" style="margin:0 0 10px"></div>
    <div class="card" style="padding:0">
      <table data-dt-key="recertification">
        <thead><tr>
          <th>Person</th><th>Product</th><th>Where it came from</th>
          <th>Held for</th><th>Decision</th><th></th>
        </tr></thead>
        <tbody id="rct-rows"><tr><td colspan="6" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>`;

  const tbody = view.querySelector("#rct-rows");
  const picker = view.querySelector("#rct-campaign");
  const mine = view.querySelector("#rct-mine");
  const summary = view.querySelector("#rct-summary");
  let campaigns = [];

  const loadCampaigns = async () => {
    try {
      campaigns = (await api("GET", "/api/v1/recertification")) || [];
    } catch (e) {
      tbody.innerHTML = `<tr><td colspan="6" class="empty">${esc(e.message)}</td></tr>`;
      return false;
    }
    if (isSuperseded && isSuperseded()) return false;
    if (!campaigns.length) {
      picker.innerHTML = "";
      summary.textContent = "";
      // Which empty it is. "Nothing to review" and "nobody has ever opened a
      // campaign" look identical in an empty table and call for opposite reactions.
      tbody.innerHTML = `<tr><td colspan="6" class="empty">No campaigns. A campaign is opened by a
        modelled process — see <code>examples/rezertifizierung.bpmn</code> — which resolves who
        reviews whom and asks Atlas for the questions.</td></tr>`;
      return false;
    }
    const chosen = picker.value;
    picker.innerHTML = campaigns.map((c) => {
      const label = c.closedAt ? `${c.name} (closed)` : c.name;
      return `<option value="${esc(c.id)}"${c.id === chosen ? " selected" : ""}>${esc(label)}</option>`;
    }).join("");
    return true;
  };

  const load = async () => {
    const id = picker.value;
    if (!id) return;
    let rep;
    try {
      rep = await api("GET",
        `/api/v1/recertification/${encodeURIComponent(id)}${mine.checked ? "?mine=true" : ""}`);
    } catch (e) {
      tbody.innerHTML = `<tr><td colspan="6" class="empty">${esc(e.message)}</td></tr>`;
      return;
    }
    if (isSuperseded && isSuperseded()) return;

    const c = rep.counts || {};
    const closed = !!rep.closedAt;
    // The four numbers, undecided among them rather than implied by them. The
    // wording is chosen so that the unanswered rows cannot be read as progress.
    summary.innerHTML = [
      `<b>${c.rows || 0}</b> right(s) in this campaign`,
      `<b>${c.kept || 0}</b> confirmed`,
      `<b>${c.revoked || 0}</b> withdrawn`,
      `<b>${c.undecided || 0}</b> <span title="Nobody answered these. They are not certified, and closing the campaign will not make them so.">unanswered</span>`,
      c.disputed ? `<b>${c.disputed}</b> disputed` : "",
      c.ending ? `<b>${c.ending}</b> <span title="These end on their own. Questions that did not need asking — which is what a ceiling on the product buys.">ending anyway</span>` : "",
      c.unassigned ? `<b>${c.unassigned}</b> addressed to nobody` : "",
      rep.dueAt ? `due ${esc(fmtWhen(rep.dueAt))}` : "",
      closed ? `<b>closed ${esc(fmtWhen(rep.closedAt))}</b>` : "",
    ].filter(Boolean).join(" · ");

    const rows = rep.rows || [];
    if (!rows.length) {
      tbody.innerHTML = `<tr><td colspan="6" class="empty">${mine.checked
        ? "Nothing in this campaign is addressed to you."
        : (rep.reason ? esc(rep.reason) : "This campaign asks nothing.")}</td></tr>`;
      return;
    }

    tbody.innerHTML = rows.map((r) => {
      const decided = !!r.decision;
      const acts = decided || closed ? "" :
        `<button class="btn sm" data-act="keep" data-row="${esc(r.id)}"
           title="Attest that this right is still needed. Nothing changes except the record that you said so">Still needed</button>
         <button class="btn sm danger" data-act="revoke" data-row="${esc(r.id)}"
           title="Attest that it is not, and run the product's deprovisioning process">Withdraw…</button>`;
      const answer = decided
        ? `<span class="pill ${r.decision === "keep" ? "" : "err"}">${esc(r.decision === "keep" ? "confirmed" : "withdrawn")}</span>
           <div class="muted" style="font-size:12px;margin-top:3px">${esc(r.decidedBy || "")} · ${esc(fmtWhen(r.decidedAt))}${
             r.outcome === "already-gone" ? " · the right was already gone" : ""}</div>
           ${r.note ? `<div class="muted" style="font-size:12px;max-width:26em">${esc(r.note)}</div>` : ""}`
        // Not "pending", not "open", and no spinner: the word has to say that
        // nothing has been decided, because that is what will be recorded.
        : `<span class="muted">unanswered</span>`;
      return `<tr>
        <td style="font-family:ui-monospace,monospace">${esc(r.principal)}</td>
        <td style="font-family:ui-monospace,monospace">${esc(r.itemId)}${
          r.variantId ? ` <span class="muted">${esc(r.variantId)}</span>` : ""}${disputePill(r)}${endsPill(r)}</td>
        <td>${originPill(r.origin)}${
          r.orderId ? ` <span class="muted" style="font-size:12px">${esc(r.orderId)}</span>` : ""}</td>
        <td data-sort="${r.since || 0}" title="Held since ${esc(fmtWhen(Math.floor((r.since || 0) / 1e9)))}">${
          esc(heldSince(r.since))}</td>
        <td>${answer}</td>
        <td class="row-actions">${acts}</td>
      </tr>`;
    }).join("");
  };

  // One delegated handler: the rows are replaced on every load, so a listener per
  // button would outlive its row.
  tbody.addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-act]");
    if (!btn) return;
    const act = btn.dataset.act;
    const row = btn.dataset.row;
    const id = picker.value;

    // Only the answer that takes access away asks, for the reason the
    // reconciliation screen gives: confirming a right is recoverable — the next
    // campaign asks again — and a confirmation on every action is one nobody reads
    // by the third row. A note is offered on both, because the reason for keeping
    // something is as worth recording as the reason for removing it.
    let note = window.prompt(act === "keep"
      ? "Why is this still needed? (optional)"
      : "Why is this no longer needed? (optional)", "");
    if (note === null) return;
    if (act === "revoke" && !window.confirm(
      "Withdraw this right?\n\nThis runs the product's deprovisioning process and takes the " +
      "access away in the target system. Atlas cannot undo it: getting it back means ordering " +
      "the product again, with whatever approval it carries.")) {
      return;
    }

    btn.disabled = true;
    try {
      await api("POST",
        `/api/v1/recertification/${encodeURIComponent(id)}/rows/${encodeURIComponent(row)}/${act}`,
        { note });
      toast(act === "keep" ? "Confirmed as still needed" : "Withdrawn, deprovisioning started", "ok");
      await load();
    } catch (err) {
      // The server's refusals explain themselves — a row already decided names who
      // decided it, one addressed elsewhere says so — and replacing them with a
      // generic failure would send a reviewer looking for a fault that is not theirs.
      toast(err.message || "That did not work", "err");
      btn.disabled = false;
    }
  });

  picker.addEventListener("change", load);
  mine.addEventListener("change", load);
  view.querySelector("#rct-refresh").addEventListener("click", async () => {
    if (await loadCampaigns()) await load();
  });
  if (await loadCampaigns()) await load();
}
