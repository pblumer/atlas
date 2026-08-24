// The org-wide "repair form per connector kind" panel in the Console
// (ADR-draft-repair-forms-without-authoring).
//
// A connector's failure is the same failure in every model that uses it: a mail task
// that parks on a rejected recipient parks on a rejected recipient everywhere. So the
// form worth showing an operator is worth writing *once*, for the kind, rather than
// binding to each of forty tasks — which is the difference between a feature that pays
// off and one that needs per-task work nobody ever does.
//
// It lives in its own module for the reason the connector and migration dialogs do:
// app.js boots the whole console on import, so anything left in it is only ever
// exercised by hand. Here it is reachable from a test.

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// wireRepairForms fills the panel: one row per kind the server says exists, each a
// picker over the deployed forms.
//
// The kinds come from the server rather than a list in here. A hardcoded copy would
// silently omit every kind added after it was written, so the newest integration — the
// one operators have had the least practice with — would be the one nobody could give
// guidance for.
export async function wireRepairForms({ api, toast }) {
  const body = document.getElementById("repair-forms-body");
  if (!body) return;
  let cfg, forms;
  try {
    [cfg, forms] = await Promise.all([
      api("GET", "/api/v1/settings/repair-forms"),
      api("GET", "/api/v1/forms").catch(() => []),
    ]);
  } catch (e) {
    body.innerHTML = `<p class="muted">${esc(e.message)}</p>`;
    return;
  }
  const byKind = (cfg && cfg.byKind) || {};
  const kinds = (cfg && cfg.kinds) || [];
  if (!kinds.length) {
    body.innerHTML = `<p class="muted">No connector kinds are available on this server.</p>`;
    return;
  }
  const option = (f, cur) => `<option value="${esc(f.id)}"${f.id === cur ? " selected" : ""}>${esc(f.name || f.id)}</option>`;
  body.innerHTML = `<table><tbody>${kinds.map((k) => {
    const cur = byKind[k] || "";
    // A form deleted after it was bound is still the binding the server holds. Dropping
    // it silently from the picker would show "none" for a kind that is not none; naming
    // it as missing is what lets someone tell a stale binding from an unset one.
    const missing = cur && !(forms || []).some((f) => f.id === cur);
    return `<tr>
      <td style="width:180px"><span class="chip">${esc(k)}</span></td>
      <td><select data-repair-kind="${esc(k)}" title="The form shown when a ${esc(k)} task parks behind an incident">
        <option value="">&mdash; none &mdash;</option>
        ${(forms || []).map((f) => option(f, cur)).join("")}
        ${missing ? `<option value="${esc(cur)}" selected>${esc(cur)} (missing)</option>` : ""}
      </select></td>
    </tr>`;
  }).join("")}</tbody></table>
  <p class="muted" style="font-size:12px;margin:10px 0 0">Changes save immediately. A form listed as <b>missing</b> was deleted
  after it was bound — the incident falls back to the raw variable editor until another is picked.</p>`;

  for (const sel of body.querySelectorAll("select[data-repair-kind]")) {
    sel.addEventListener("change", async () => {
      // The whole table is written at once, so two kinds changed in quick succession
      // cannot interleave into a partial state.
      const next = {};
      for (const s of body.querySelectorAll("select[data-repair-kind]")) {
        if (s.value) next[s.dataset.repairKind] = s.value;
      }
      try {
        await api("PUT", "/api/v1/settings/repair-forms", { byKind: next });
        toast(sel.value ? `Repair form set for ${sel.dataset.repairKind}` : `Repair form cleared for ${sel.dataset.repairKind}`, "ok");
      } catch (e) {
        toast(/403|admin/i.test(e.message) ? "Binding a repair form needs an admin account" : "Could not save: " + e.message, "warn");
        // Re-read rather than leave the picker showing a change the server refused: the
        // panel is the operator's only view of what is actually bound.
        await wireRepairForms({ api, toast });
      }
    });
  }
}
