// The dialog an import shows before it replaces anything
// (ADR-0467).
//
// An import compares every artifact it carries with what the server holds and with
// what the last import brought: new, unchanged, behind (as the last import left it,
// and newer in the file), ahead (changed here, and as before in the file) or changed
// (changed on both sides, or never imported). New is written and unchanged left
// alone, so the same file imported twice changes nothing the second time. Behind is
// replaced and ahead kept unless the person says otherwise; changed is work done on
// this server that the file would overwrite, and is kept until the person ticks it.
// This dialog lists all three, with a box each, set to what happens without a word.
//
// It lives in its own module for the reason the pick dialog does: app.js boots the
// whole console on import, so anything left inside it is only ever exercised by hand.

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// The statuses the dialog lists, in its order, with what each box starts as.
const GROUPS = [
  { status: "changed", ticked: false, title: "Changed here and in the file",
    hint: "Ticked, the file's version replaces the one changed here." },
  { status: "behind", ticked: true, title: "Newer in the file, unchanged here",
    hint: "Ticked, as they start, they are updated; untick one to keep this server's version." },
  { status: "ahead", ticked: false, title: "Changed here only",
    hint: "The file carries them as the last import did. Tick one to put the file's version back." },
];

// decisionQuery is the decisions as the import routes take them. Every changed
// artifact is named, under overwrite or keep, rather than with *: one that changed
// after the dialog was shown is then refused instead of replaced on the strength of a
// decision about something else. Behind and ahead are named only where the person
// moved a box: left as it started, the server settles them again where it writes.
export function decisionQuery({ overwrite = [], keep = [] }) {
  const q = new URLSearchParams();
  if (overwrite.length) q.set("overwrite", overwrite.join(","));
  if (keep.length) q.set("keep", keep.join(","));
  return q.toString();
}

// needsAsking reports whether a plan has anything the dialog lists.
export function needsAsking(artifacts) {
  return (artifacts || []).some((a) => GROUPS.some((g) => g.status === a.status));
}

// openImportChoices shows the plan and asks what to replace. Resolves to
// { overwrite, keep } — refs — or null on cancel, Escape or a click outside.
export function openImportChoices({ title, artifacts }) {
  return new Promise((resolve) => {
    const count = (status) => artifacts.filter((a) => a.status === status).length;
    const fresh = artifacts.filter((a) => a.status === "new");
    const label = (a) => `<code>${esc(a.ref)}</code>${a.name && a.name !== a.ref.split(":").slice(1).join(":")
      ? ` ${esc(a.name)}` : ""}`;
    const group = (g) => {
      const items = artifacts.filter((a) => a.status === g.status);
      if (!items.length) return "";
      return `<section class="import-group" data-status="${g.status}">
          <h3>${esc(g.title)} <span class="muted">(${items.length})</span></h3>
          <p class="muted small">${esc(g.hint)}</p>
          <ul class="import-changed">${items.map((a) => `
            <li><label><input type="checkbox" class="import-pick" value="${esc(a.ref)}"
              data-status="${g.status}"${g.ticked ? " checked" : ""}> Replace ${label(a)}</label></li>`).join("")}</ul>
        </section>`;
    };
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
            ${count("behind")} newer in the file, ${count("ahead")} changed here only,
            <b>${count("changed")} changed on both sides</b>.</p>
          <div class="import-bulk">
            <button type="button" class="btn ghost small" data-all title="Replace every one with the file's version">Tick all</button>
            <button type="button" class="btn ghost small" data-none title="Keep every one as this server has it">Tick none</button>
          </div>
          ${GROUPS.map(group).join("")}
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
    const startsTicked = (p) => GROUPS.find((g) => g.status === p.dataset.status).ticked;

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
    ov.querySelector("[data-ok]").addEventListener("click", () => {
      // Changed is always named; behind and ahead only where a box was moved.
      const named = picks().filter((p) => p.dataset.status === "changed" || p.checked !== startsTicked(p));
      finish({
        overwrite: named.filter((p) => p.checked).map((p) => p.value),
        keep: named.filter((p) => !p.checked).map((p) => p.value),
      });
    });
    ov.querySelector("[data-ok]").focus();
  });
}

// importAsking runs an import the way the routes ask for it: the plan first, the
// dialog for whatever would be replaced or kept, then the import with the decisions.
// send(query) posts the import with a query string ("" for none) and resolves to the
// answer. Resolves to that answer, or null when the person cancelled; nothing is
// written then.
export async function importAsking(send, { title }) {
  const plan = await send("dryRun=true");
  const artifacts = (plan && plan.artifacts) || [];
  if (!needsAsking(artifacts)) return send("");
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
