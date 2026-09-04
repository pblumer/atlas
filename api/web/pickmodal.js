// The dialog behind every "pick one of these, and name it" in the console: which
// application an information model or an architecture model belongs to, which target
// a release is promoted to.
//
// It lives in its own module for the reason the worker and migration dialogs do:
// app.js boots the whole console on import, so anything left inside it is only ever
// exercised by hand. Here it is reachable from a test — which matters for this one,
// because what it replaces failed in a way no one could see.

import { openDialog } from "./dialog.js";
const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// openPickModal is "choose one of these, and name it" as a dialog rather than as a
// riddle. It replaces a window.prompt that printed the choices as a numbered list
// and asked for the number back — a pattern that failed twice over.
//
// It failed silently first: a browser truncates a prompt body once it grows past a
// handful of lines and marks the cut with an ellipsis, so on a server with a dozen
// applications the newest ones — which sort last, being newest — were not in the
// list somebody was being told to choose from. The application was there, the
// dialog just could not show it, and nothing said so. And it failed openly second:
// counting entries and typing an ordinal is not how anyone expects to pick from a
// list, and a mistyped number picked the wrong thing rather than nothing.
//
// A <select> has no length limit and needs no counting. Resolves to
// { option, name } on confirm, or null on cancel, Escape or a click outside.
export function openPickModal({ title, label, options, hint, nameLabel, nameFor, okLabel = "Create" }) {
  return new Promise((resolve) => {
    const body = document.createElement("div");
    body.innerHTML = `
      <label class="field"><span>${esc(label)}</span>
        <select id="pick-opt">${options.map((o) =>
          `<option value="${esc(o.value)}">${esc(o.label)}</option>`).join("")}</select></label>
      ${nameFor ? `<label class="field"><span>${esc(nameLabel || "Name")}</span>
        <input type="text" id="pick-name" autocomplete="off"></label>` : ""}
      ${hint ? `<p class="muted small">${esc(hint)}</p>` : ""}`;

    const sel = body.querySelector("#pick-opt");
    const nameEl = body.querySelector("#pick-name");
    const optionFor = (value) => options.find((o) => String(o.value) === String(value));
    // The suggested name follows the picker until somebody types their own, so
    // changing the application still offers a name that belongs to it.
    let nameEdited = false;
    const syncName = () => {
      if (!nameEl || nameEdited) return;
      nameEl.value = nameFor(optionFor(sel.value)) || "";
    };
    if (nameEl) {
      syncName();
      nameEl.addEventListener("input", () => { nameEdited = true; });
    }
    sel.addEventListener("change", syncName);

    let settled = false;
    const finish = (result) => {
      if (settled) return;
      settled = true;
      resolve(result);
    };
    const submit = () => {
      const option = optionFor(sel.value);
      if (!option) return dlg.close(null);
      const name = nameEl ? nameEl.value.trim() : "";
      if (nameEl && !name) { nameEl.focus(); return; } // an unnamed thing is not created
      dlg.close({ option, name });
    };

    const dlg = openDialog({
      title, label: title, body, onClose: finish,
      actions: [
        { label: "Cancel", kind: "ghost", value: null, attrs: { "data-cancel": "" } },
        { label: okLabel, keepOpen: true, attrs: { "data-ok": "" }, onSelect: submit },
      ],
    });

    // Enter is submit here: the dialog is one picker and one name, and reaching for
    // the mouse to confirm two fields is the kind of friction this dialog exists to
    // remove. openDialog owns Escape; this is the caller's own key.
    dlg.el.addEventListener("keydown", (e) => {
      if (e.key === "Enter") { e.preventDefault(); submit(); }
    });
  });
}
