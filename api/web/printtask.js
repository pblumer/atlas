// Printing a task (ADR-0555 §6): an open task as it is being filled in, or a completed
// one as it was answered (ADR-0554). The browser's own "Print → Save as PDF" makes the
// PDF from the same page.
//
// The detail pane is not printed where it stands. It sits in a grid whose panes have a
// fixed height and scroll on their own, so a print of the page would cut the task off at
// the first sheet and carry the list and the folders along. Instead the pane is copied
// into a root of its own at the end of <body>, and while the print dialog is open
// everything else is display:none (app.css, @media print). The copy carries what was
// typed into the form: a cloned input keeps its attributes, not what was typed since.

import { fmtDateTime } from "./datefmt.js";
import { t as tr } from "./i18n.js";

// What a printed task leaves out: the way back, the buttons, the tabs, the process
// diagram and the form library's badge. Nothing on paper can be pressed.
const OMIT = [
  ".tasks-back", ".tasks-detail-actions", ".tasks-detail-tabs", "#pane-process",
  ".fjs-powered-by", "button", "[data-print='omit']",
].join(", ");

// carryValues writes what the person typed into the copy's attributes, element by
// element: the two trees have the same shape, so the n-th field of one is the n-th of
// the other.
function carryValues(from, to) {
  const a = from.querySelectorAll("input, textarea, select");
  const b = to.querySelectorAll("input, textarea, select");
  a.forEach((el, i) => {
    const c = b[i];
    if (!c) return;
    if (el.type === "checkbox" || el.type === "radio") {
      if (el.checked) c.setAttribute("checked", "");
      else c.removeAttribute("checked");
    } else if (el.tagName === "TEXTAREA") {
      c.textContent = el.value;
    } else if (el.tagName === "SELECT") {
      Array.from(c.options).forEach((o, j) => {
        if (el.options[j] && el.options[j].selected) o.setAttribute("selected", "");
        else o.removeAttribute("selected");
      });
    } else {
      c.setAttribute("value", el.value);
    }
  });
}

// buildPrintCopy returns the printable copy of a task's detail pane: the fields as they
// stand, without anything that only works on screen, with no element ids (they would
// repeat the page's own) and with the form shown even when another tab was open.
export function buildPrintCopy(source, { who = "", now = Date.now() } = {}) {
  const copy = source.cloneNode(true);
  carryValues(source, copy);
  const form = copy.querySelector("#pane-form");
  if (form) form.removeAttribute("hidden");
  copy.querySelectorAll(OMIT).forEach((n) => n.remove());
  copy.removeAttribute("id");
  copy.querySelectorAll("[id]").forEach((n) => n.removeAttribute("id"));
  const foot = document.createElement("p");
  foot.className = "tasks-print-foot";
  foot.textContent = who
    ? tr("tasks.print.footer", { when: fmtDateTime(now), who })
    : tr("tasks.print.footerAnon", { when: fmtDateTime(now) });
  copy.append(foot);
  return copy;
}

// printTaskDetail prints a task's detail pane and puts the page back as it was once
// the print dialog closes.
export function printTaskDetail(source, opts = {}) {
  document.querySelectorAll(".task-print-root").forEach((n) => n.remove());
  const root = document.createElement("div");
  root.className = "task-print-root";
  root.append(buildPrintCopy(source, opts));
  document.body.append(root);
  document.body.classList.add("printing-task");
  const done = () => {
    window.removeEventListener("afterprint", done);
    document.body.classList.remove("printing-task");
    root.remove();
  };
  window.addEventListener("afterprint", done);
  window.print();
}
