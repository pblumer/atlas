// What Atlas and the target systems disagree about, and the three things a person
// may do about it (ADR-0334).
//
// The comparison was built before this screen, and for a few days the findings were
// reachable only by constructing an HTTP POST with an id read out of a JSON array.
// That is not "a person decides", it is "an engineer decides" — and the whole
// posture of that slice is that nothing happens to somebody's access without a
// person having read the finding. This is where they read it.
//
// # Why the three actions are not styled alike
//
// They differ in what can be undone, and the interface says so before the click
// rather than after it:
//
//   - **Adopt** writes an entitlement. Undone by revoking it, or by the next
//     comparison. One click.
//   - **Revoke** removes a record Atlas could not substantiate. If the target system
//     does have the right after all, the next comparison finds it again as
//     unmanaged. One click.
//   - **Deprovision** runs a process that takes access away in a real system. Atlas
//     cannot undo it — the person has to order the thing again, with its approval.
//     It asks first, and it is styled as what it is.
//
// # The explanations here are the interface's own
//
// The server sends a `why` with a finding in a *run report*; the journal stores none,
// and this page writes its own two sentences. That is deliberate rather than
// duplication: there are exactly two kinds, an interface explains in its own words,
// and storing a sentence on every record to avoid writing two here would be paying
// per row for prose.

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const fmtWhen = (unixSeconds) =>
  unixSeconds ? new Date(unixSeconds * 1000).toLocaleString() : "—";

// sinceWhen says how long a disagreement has stood, which is the thing a list of
// findings is otherwise bad at conveying: "three weeks" and "since this morning"
// call for different reactions and look identical as two timestamps.
function sinceWhen(openedAt, now = Date.now() / 1000) {
  if (!openedAt) return "";
  const days = Math.floor((now - openedAt) / 86400);
  if (days >= 1) return `${days} day${days === 1 ? "" : "s"}`;
  const hours = Math.floor((now - openedAt) / 3600);
  return hours >= 1 ? `${hours}h` : "just now";
}

// KINDS is what each direction means and what may be done about it. The two are
// not symmetrical and the screen must not make them look it: one is somebody
// holding what nobody granted, the other is Atlas asserting something untrue.
const KINDS = {
  unmanaged: {
    name: "Unmanaged",
    pill: "warn",
    what: "Held in the target system and not recorded here — somebody has this and " +
      "nothing in Atlas decided to give it to them.",
    acts: [
      { act: "adopt", label: "Adopt", cls: "btn sm",
        title: "Record it as held, with origin “adopted” — Atlas did not grant it and will not say it did" },
      { act: "deprovision", label: "Deprovision…", cls: "btn sm danger",
        title: "Run the product's deprovisioning process and take the access away" },
    ],
  },
  missing: {
    name: "Missing",
    pill: "err",
    what: "Recorded here and not held in the target system — Atlas is asserting " +
      "something the target system does not agree with, and will keep asserting it.",
    acts: [
      { act: "revoke", label: "Revoke record", cls: "btn sm",
        title: "Stop asserting it. The target system is not touched — there is nothing there to touch" },
    ],
  },
};

// originPill marks where a *missing* right came from, because it decides how
// alarming the finding is: an ordered right that vanished is a provisioning that
// came undone; a legacy one is quite possibly a group somebody tidied up years ago.
function originPill(origin) {
  if (!origin) return "";
  const tone = origin === "ordered" ? "err" : "";
  return ` <span class="pill ${tone}" title="Where the inventory's knowledge came from">${esc(origin)}</span>`;
}

// episodePill is shown only past the first, because a disagreement somebody keeps
// re-creating is itself a finding and one record per identity hides it perfectly.
function episodePill(rec) {
  if (!rec.episodes || rec.episodes < 2) return "";
  const back = rec.previousClosedHow ? ` after being ${esc(rec.previousClosedHow)}` : "";
  return ` <span class="pill warn" title="This has opened ${rec.episodes} times${back} — something keeps re-creating it">×${rec.episodes}</span>`;
}

export async function viewReconciliation({ api, toast, view, isSuperseded }) {
  view.innerHTML = `
    <div class="between">
      <h1>Reconciliation</h1>
      <button class="btn neutral" id="rec-refresh" title="Reload the findings">Refresh</button>
    </div>
    <p class="muted">Where Atlas and a target system disagree about who holds what.
    An entitlement asserts that a right exists somewhere else, and target systems are
    changed from outside Atlas — so the inventory decays, and this is what checking it
    turns up. <b>Nothing here happened automatically and nothing here will:</b> each
    finding is acted on by a person, one at a time.</p>
    <p class="muted">Findings are produced by a reconciliation run, which a modelled
    process performs — see <code>examples/abgleich.bpmn</code>. A run names the
    references it read <i>completely</i>, and concludes nothing outside them.</p>
    <div class="row" style="margin:0 0 10px">
      <label class="field inline" style="margin:0">System
        <select id="rec-system" style="margin-left:6px"><option value="">all</option></select></label>
    </div>
    <div class="card" style="padding:0">
      <table data-dt-key="reconciliation">
        <thead><tr>
          <th>Finding</th><th>Person</th><th>Product</th><th>Reference</th>
          <th>Standing since</th><th></th>
        </tr></thead>
        <tbody id="rec-rows"><tr><td colspan="6" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>`;

  const tbody = view.querySelector("#rec-rows");
  const picker = view.querySelector("#rec-system");
  let current = [];

  const load = async () => {
    let rows;
    try {
      rows = (await api("GET", "/api/v1/reconciliation")) || [];
    } catch (e) {
      tbody.innerHTML = `<tr><td colspan="6" class="empty">${esc(e.message)}</td></tr>`;
      return;
    }
    if (isSuperseded && isSuperseded()) return;
    current = rows;

    // The picker is rebuilt from what is actually there rather than from a fixed
    // list: the systems are an installation's own vocabulary, and offering one
    // nothing has ever reported would be offering an empty filter.
    const chosen = picker.value;
    const systems = [...new Set(rows.map((r) => r.system).filter(Boolean))].sort();
    picker.innerHTML = `<option value="">all</option>` +
      systems.map((s) => `<option value="${esc(s)}"${s === chosen ? " selected" : ""}>${esc(s)}</option>`).join("");

    const shown = chosen ? rows.filter((r) => r.system === chosen) : rows;
    if (!shown.length) {
      // The empty state says which of the two empties it is. "Nothing disagrees" and
      // "nothing has ever been compared" look identical in an empty table, and they
      // call for opposite reactions.
      tbody.innerHTML = `<tr><td colspan="6" class="empty">${rows.length
        ? "No findings for this system."
        : "No open findings. Either everything compared so far agrees — or nothing has been compared yet: a reconciliation run is what produces these."}</td></tr>`;
      return;
    }

    tbody.innerHTML = shown.map((r) => {
      const kind = KINDS[r.kind] || { name: r.kind, pill: "", what: "", acts: [] };
      const acts = kind.acts.map((a) =>
        `<button class="${a.cls}" data-act="${a.act}" data-id="${esc(r.id)}" title="${esc(a.title)}">${esc(a.label)}</button>`
      ).join(" ");
      return `<tr>
        <td><span class="pill ${kind.pill}"><span class="dot"></span>${esc(kind.name)}</span>${
          originPill(r.origin)}${episodePill(r)}
          <div class="muted" style="font-size:12px;margin-top:3px;max-width:34em">${esc(kind.what)}</div></td>
        <td style="font-family:ui-monospace,monospace">${esc(r.principal)}</td>
        <td style="font-family:ui-monospace,monospace">${esc(r.itemId)}</td>
        <td><span class="muted">${esc(r.system)}</span>${
          r.ref ? ` <span style="font-family:ui-monospace,monospace">${esc(r.ref)}</span>` : ""}</td>
        <td data-sort="${r.openedAt || 0}" title="First seen ${esc(fmtWhen(r.openedAt))}, last confirmed ${esc(fmtWhen(r.lastSeenAt))}">${
          esc(sinceWhen(r.openedAt))}</td>
        <td class="row-actions">${acts}</td>
      </tr>`;
    }).join("");
  };

  // One delegated handler: the rows are replaced on every load, so a listener per
  // button would be a listener per row that outlives its row.
  tbody.addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-act]");
    if (!btn) return;
    const id = btn.dataset.id;
    const act = btn.dataset.act;
    const rec = current.find((r) => r.id === id);

    // Only the one that reaches outside Atlas asks. Adopting and revoking are
    // recoverable — the next comparison finds the truth again either way — and a
    // confirmation on every action is a confirmation nobody reads by the third one.
    if (act === "deprovision" && !window.confirm(
      `Run the deprovisioning process for “${rec?.itemId}” against ${rec?.principal}?\n\n` +
      `This takes the access away in ${rec?.system}. Atlas cannot undo it: getting it ` +
      `back means ordering the product again, with whatever approval it carries.`)) {
      return;
    }

    btn.disabled = true;
    try {
      await api("POST", `/api/v1/reconciliation/${encodeURIComponent(id)}/${act}`, {});
      toast(act === "adopt" ? "Adopted into the inventory"
        : act === "revoke" ? "Record removed"
        : "Deprovisioning started", "ok");
      await load();
    } catch (err) {
      // The server's refusals say why — a finding already closed names how it was
      // closed — so they are shown rather than replaced with a generic failure.
      toast(err.message || "That did not work", "err");
      btn.disabled = false;
    }
  });

  picker.addEventListener("change", load);
  view.querySelector("#rec-refresh").addEventListener("click", load);
  await load();
}
