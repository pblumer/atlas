// Importing a class diagram somebody already drew (ADR-0232).
//
// A data model is normally drawn in a UML tool long before anybody opens Atlas, and
// retyping one by hand is both the slowest way to start and the way a business key
// quietly goes missing. Two documents are read: Atlas's own JSON — how a model moves
// between applications and installations — and the XMI 2.5.1 a UML tool exports.
//
// The import is deliberately two steps. Reading a foreign notation into a declared
// subset is lossy, and the report the first step returns is the substance of it: what
// arrived, and what the subset would not take, element by element. The same call
// makes both — the second one only drops the dryRun flag — so what the report
// promises is exactly what gets stored.
//
// It lives in its own module for the reason pickmodal.js and connectordialog.js do:
// app.js boots the whole console on import, so anything left inside it is only ever
// exercised by hand. This flow earned the move the hard way — its first cut called a
// helper that a later change to the Console had removed, so the button did nothing at
// all, and nothing anywhere said so.

import { openDialog } from "./dialog.js";
const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// runImport reads one file into one application and resolves to the stored model's
// summary, or to null if the document could not be read or the reader cancelled.
//
// Every collaborator it needs is passed in — the API caller, the toast, where to go
// afterwards — so the flow can be driven by a test without booting the Console.
export async function runImport({ app, file, api, toast, navigate }) {
  let text;
  try {
    text = await file.text();
  } catch (e) {
    toast("Import failed: " + e.message, "err");
    return null;
  }

  let preview;
  try {
    preview = await api("POST", "/api/v1/infomodel/import",
      { applicationId: app.id, document: text, dryRun: true });
  } catch (e) {
    toast("Import failed: " + e.message, "err");
    return null;
  }

  return showImportReport({
    app, api, toast, navigate, text,
    fileName: file.name,
    fallbackName: file.name.replace(/\.[^.]+$/, ""),
    preview,
  });
}

// showImportReport is the report and the confirmation in one: the counts, the name the
// model will carry, and every note the reader has to see before deciding. It resolves
// when the reader has decided — to the stored model, or to null.
export function showImportReport({ app, api, toast, navigate, text, fileName, fallbackName, preview }) {
  const model = preview.preview || { classes: [], associations: [], stores: [] };
  const notes = preview.notes || [];
  const level = (l) => ({ dropped: "#b42318", adjusted: "#9a6700", info: "#6a737d" }[l] || "#6a737d");
  const badge = (l) => `<span class="im-note-level" style="display:inline-block;padding:1px 8px;border-radius:10px;font-size:11px;color:#fff;white-space:nowrap;background:${level(l)}">${esc(l)}</span>`;
  const rows = notes.map((n) =>
    `<tr data-level="${esc(n.level)}"><td>${badge(n.level)}</td><td>${n.element ? `<code>${esc(n.element)}</code>` : `<span class="muted">the model</span>`}</td>
     <td class="muted">${esc(n.message)}</td></tr>`).join("");
  const counted = (n, one, many) => `${n} ${n === 1 ? one : many}`;

  return new Promise((resolve) => {
    const body = document.createElement("div");
    body.innerHTML = `
      <p class="muted" style="margin:0 0 10px" id="im-import-counts">Read as <b>${esc(preview.format === "xmi" ? "UML XMI" : "Atlas JSON")}</b> —
        ${counted((model.classes || []).length, "class", "classes")},
        ${counted((model.associations || []).length, "relationship", "relationships")},
        ${counted((model.stores || []).length, "data store", "data stores")}.
        Nothing is stored until you import.</p>
      <label class="field" style="max-width:380px"><span>Model name</span>
        <input id="im-import-name" value="${esc(model.name || fallbackName)}"/></label>
      <p class="muted" style="margin:10px 0 6px">Atlas authors a declared subset of the UML class diagram, so a
        document from another tool routinely says things it has no place for. Every one of them is listed here:
        <b>dropped</b> is not in the model, <b>adjusted</b> is in it saying something slightly different.</p>
      <div style="max-height:44vh; overflow:auto">
        <table><thead><tr><th style="width:90px">What</th><th style="width:190px">Element</th><th>Detail</th></tr></thead>
          <tbody>${rows || `<tr><td colspan="3" class="muted">Nothing was lost: the document fits the subset as it stands.</td></tr>`}</tbody></table>
      </div>`;

    let settled = false;
    const finish = (result) => {
      if (settled) return;
      settled = true;
      resolve(result);
    };

    // The shared dialog (ADR-draft-shared-ui-primitives). It also fixes what this
    // one got wrong on its own: the report used to open with the focus still behind
    // it, so a keyboard reader landed nowhere. openDialog puts the focus on the name
    // field, which is the one thing here anybody edits.
    const dlg = openDialog({
      title: `Import ${fileName}`,
      label: "Import report",
      body,
      width: 880,
      overlayId: "im-import-report",
      onClose: (v) => finish(v),
      actions: [
        { label: "Cancel", kind: "neutral", value: null, attrs: { "data-close": "" },
          title: "Close without importing" },
        { label: `Import into ${app.name}`, keepOpen: true, attrs: { "data-import": "" },
          title: `Store this as an information model of ${app.name}`,
          onSelect: async () => {
            const name = (body.querySelector("#im-import-name").value || "").trim();
            try {
              const created = await api("POST", "/api/v1/infomodel/import",
                { applicationId: app.id, document: text, name });
              dlg.close(created.model || null);
              const dropped = (created.notes || []).filter((n) => n.level === "dropped").length;
              toast(dropped
                ? `Imported “${created.model.name}” — ${dropped} element${dropped === 1 ? "" : "s"} the subset does not author were left out`
                : `Imported “${created.model.name}”`, "ok");
              if (navigate) navigate(created.model.id);
            } catch (e) {
              toast("Import failed: " + e.message, "err");
            }
          } },
      ],
    });
  });
}
