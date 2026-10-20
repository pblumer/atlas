// The dialog an import shows before it replaces anything
// (ADR-0467).
//
// An import compares every artifact it carries with what the server holds: new,
// unchanged, or changed. New is written and unchanged left alone, so the same file
// imported twice changes nothing the second time. Changed is work done on this
// server — a process edited here, a product renamed here — and the server writes over
// it only when told so, artifact by artifact. This is where the person is asked:
// each changed artifact with a box, unticked (keep this server's version) until they
// tick it.
//
// It lives in its own module for the reason the pick dialog does: app.js boots the
// whole console on import, so anything left inside it is only ever exercised by hand.

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// decisionQuery is the decisions as the import routes take them. Every changed
// artifact is named, under overwrite or keep, rather than with *: one that changed
// after the dialog was shown is then refused instead of replaced on the strength of a
// decision about something else.
export function decisionQuery({ overwrite = [], keep = [] }) {
  const q = new URLSearchParams();
  if (overwrite.length) q.set("overwrite", overwrite.join(","));
  if (keep.length) q.set("keep", keep.join(","));
  return q.toString();
}

// openImportChoices shows the plan and asks which changed artifacts to replace.
// Resolves to { overwrite, keep } — refs — or null on cancel, Escape or a click
// outside.
export function openImportChoices({ title, artifacts }) {
  return new Promise((resolve) => {
    const count = (status) => artifacts.filter((a) => a.status === status).length;
    const changed = artifacts.filter((a) => a.status === "changed");
    const fresh = artifacts.filter((a) => a.status === "new");
    const label = (a) => `<code>${esc(a.ref)}</code>${a.name && a.name !== a.ref.split(":").slice(1).join(":")
      ? ` ${esc(a.name)}` : ""}`;
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal import-choices" role="dialog" aria-modal="true" aria-label="${esc(title)}">
        <div class="modal-head">
          <h2>${esc(title)}</h2>
          <button type="button" class="icon-btn" data-x aria-label="Close" title="Close">✕</button>
        </div>
        <div class="modal-body">
          <p class="import-counts">${count("new")} new, ${count("unchanged")} unchanged,
            <b>${changed.length} differ from this server</b>.</p>
          <p class="muted small">What differs was changed here. Tick what the file should replace;
            what stays unticked is kept as this server has it.</p>
          <div class="import-bulk">
            <button type="button" class="btn ghost small" data-all title="Replace every one with the file's version">Tick all</button>
            <button type="button" class="btn ghost small" data-none title="Keep every one as this server has it">Tick none</button>
          </div>
          <ul class="import-changed">${changed.map((a) => `
            <li><label><input type="checkbox" class="import-pick" value="${esc(a.ref)}">
              Replace ${label(a)}</label></li>`).join("")}</ul>
          ${fresh.length ? `<details class="import-new"><summary>${fresh.length} new</summary>
            <ul>${fresh.map((a) => `<li>${label(a)}</li>`).join("")}</ul></details>` : ""}
        </div>
        <div class="modal-foot">
          <span></span>
          <span class="modal-actions">
            <button type="button" class="btn ghost" data-cancel>Cancel</button>
            <button type="button" class="btn" data-ok>Import</button>
          </span>
        </div>
      </div>`;
    document.body.appendChild(ov);
    const picks = () => [...ov.querySelectorAll(".import-pick")];

    let settled = false;
    const finish = (result) => {
      if (settled) return;
      settled = true;
      ov.remove();
      document.removeEventListener("keydown", onKey);
      resolve(result);
    };
    const onKey = (e) => { if (e.key === "Escape") finish(null); };
    document.addEventListener("keydown", onKey);
    ov.addEventListener("mousedown", (e) => { if (e.target === ov) finish(null); });
    ov.querySelector("[data-x]").addEventListener("click", () => finish(null));
    ov.querySelector("[data-cancel]").addEventListener("click", () => finish(null));
    ov.querySelector("[data-all]").addEventListener("click", () => picks().forEach((p) => { p.checked = true; }));
    ov.querySelector("[data-none]").addEventListener("click", () => picks().forEach((p) => { p.checked = false; }));
    ov.querySelector("[data-ok]").addEventListener("click", () => finish({
      overwrite: picks().filter((p) => p.checked).map((p) => p.value),
      keep: picks().filter((p) => !p.checked).map((p) => p.value),
    }));
    ov.querySelector("[data-ok]").focus();
  });
}

// importAsking runs an import the way the routes ask for it: the plan first, the
// dialog for whatever differs, then the import with the decisions. send(query) posts
// the import with a query string ("" for none) and resolves to the answer. Resolves
// to that answer, or null when the person cancelled; nothing is written then.
export async function importAsking(send, { title }) {
  const plan = await send("dryRun=true");
  const artifacts = (plan && plan.artifacts) || [];
  if (!artifacts.some((a) => a.status === "changed")) return send("");
  const choice = await openImportChoices({ title, artifacts });
  if (!choice) return null;
  return send(decisionQuery(choice));
}

// outcomeSummary says in a few words what an import did with its artifacts.
export function outcomeSummary(artifacts) {
  const n = (outcome) => (artifacts || []).filter((a) => a.outcome === outcome).length;
  const parts = [`${n("created")} new`, `${n("updated")} replaced`];
  if (n("kept")) parts.push(`${n("kept")} kept as they were`);
  parts.push(`${n("unchanged")} unchanged`);
  return parts.join(", ");
}
