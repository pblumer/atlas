// The table of names this installation keeps for the application packages it
// imports (ADR-0467, amended 2026-10-21).
//
// A package names its catalogues' groups, people and approvers as the exporting
// server does. When a name is not found here, the person importing chooses who it
// means, and the choice is kept in this table, so the next import of a package from
// the same place does not ask again. This card shows the table on the catalogue list,
// where the imports are, and lets a product manager correct an entry, remove one, or
// add one before the first import.
//
// It lives in its own module for the reason the other dialogs do: app.js boots the
// whole console on import, so anything left inside it is only ever exercised by hand.

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// KINDS are what the table keeps, as a person reads them.
const KINDS = { group: "Group", user: "Person", account: "Approver (account)" };

// targets lists what a kind may mean here: groups and people by id from the
// directory, accounts by username.
function targets(kind, dir, accounts) {
  if (kind === "account") {
    return (accounts || []).map((a) => ({ id: a.username, name: a.displayName || a.name || a.username }));
  }
  return (dir || []).filter((e) => e.type === kind).map((e) => ({ id: e.id, name: e.name }));
}

// mappingsCard renders the table. mappings is GET /api/v1/catalogs/import/mappings,
// anything but a list when it could not be read.
export function mappingsCard(mappings, dir, accounts) {
  const intro = `<p class="muted" style="max-width:62ch">When an application package names a group, a
      person or an approver this server does not know by that name, the choice made on import is kept
      here, and the next import takes it without asking. Correct or remove an entry, or add one before
      the first import.</p>`;
  if (!Array.isArray(mappings)) {
    return `<section class="cat-mappings" style="margin-top:26px">
      <h3 style="margin:0 0 10px">Names from other installations</h3>${intro}
      <p class="muted">The table could not be read.</p></section>`;
  }
  const rows = mappings.map((m) => `<tr data-kind="${esc(m.kind)}" data-answer="${esc(m.answer)}">
      <td>${esc(KINDS[m.kind] || m.kind)}</td>
      <td>${esc(m.answer)}</td>
      <td>${esc(m.name || m.id)} <code>${esc(m.id)}</code>${m.here ? ""
        : ` <span class="need-state" data-state="missing">no longer here; imports look the name up again</span>`}</td>
      <td><button type="button" class="btn ghost small" data-act="mapping-remove" title="Remove this entry">Remove</button></td>
    </tr>`).join("");
  const kindOptions = Object.entries(KINDS).map(([k, label]) => `<option value="${k}">${esc(label)}</option>`).join("");
  const first = targets("group", dir, accounts);
  return `<section class="cat-mappings" style="margin-top:26px">
    <h3 style="margin:0 0 10px">Names from other installations</h3>${intro}
    <div class="card" style="max-width:860px">
      ${mappings.length ? `<table class="mapping-table">
        <thead><tr><th>Kind</th><th>Name in the package</th><th>Means here</th><th></th></tr></thead>
        <tbody>${rows}</tbody></table>`
        : `<p class="muted mapping-empty">No entries yet: every name a package gave was found here.</p>`}
      <form class="mapping-add">
        <label class="field">Kind<select name="kind">${kindOptions}</select></label>
        <label class="field">Name in the package<input name="answer" placeholder="Fachbereich Bau" autocomplete="off" required></label>
        <label class="field">Means here<select name="id">${first.map((t) =>
          `<option value="${esc(t.id)}">${esc(t.name)}</option>`).join("")}</select></label>
        <button class="btn neutral" type="submit">Add</button>
      </form>
    </div></section>`;
}

// wireMappings makes the card work: remove an entry, add one. Each change is the
// server's to check — an entry naming nobody here is refused there — and the page is
// drawn again from what it answers.
export function wireMappings({ api, toast, view, dir, accounts, redraw }) {
  const card = view.querySelector(".cat-mappings");
  if (!card) return;
  const form = card.querySelector(".mapping-add");
  if (form) {
    form.kind.addEventListener("change", () => {
      form.id.innerHTML = targets(form.kind.value, dir, accounts)
        .map((t) => `<option value="${esc(t.id)}">${esc(t.name)}</option>`).join("");
    });
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      try {
        await api("POST", "/api/v1/catalogs/import/mappings",
          { mappings: [{ kind: form.kind.value, answer: form.answer.value.trim(), id: form.id.value }] });
        toast("Kept: " + form.answer.value.trim());
        redraw();
      } catch (err) {
        toast((err && err.message) || String(err), "err");
      }
    });
  }
  card.addEventListener("click", async (e) => {
    const b = e.target.closest('button[data-act="mapping-remove"]');
    if (!b) return;
    const row = b.closest("tr");
    const q = new URLSearchParams({ kind: row.dataset.kind, answer: row.dataset.answer });
    try {
      await api("DELETE", "/api/v1/catalogs/import/mappings?" + q.toString());
      toast("Removed: " + row.dataset.answer);
      redraw();
    } catch (err) {
      toast((err && err.message) || String(err), "err");
    }
  });
}
