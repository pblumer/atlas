// The dialog every view opens.
//
// Twenty-two of them were built by hand across ten files, each with its own overlay
// markup and its own copy of the same behaviour: role and aria-modal, Escape, the
// click on the backdrop, the initial focus. Most of them got most of it right, which
// is why the gaps were invisible — `infomodel-import.js` opened its import report
// with the focus still behind it, and nothing in a test run said so. The parts that
// are easy to forget belong in one place, tested once
// (ADR-draft-shared-ui-primitives).
//
// It builds rather than decorates, unlike groupifyPanel and attachDiagramZoom: a
// dialog has no markup until it is opened, so there is nothing to decorate. Callers
// hand it a body element and get the frame, the behaviour and the teardown.

// FOCUSABLE is what the keyboard can reach. Deliberately not [tabindex] in general:
// an element with tabindex="-1" is focusable by script (the dialog itself is) but is
// not a tab stop, and treating it as one would put a stop in the loop the Tab key
// then appears to skip.
const FOCUSABLE = [
  "a[href]", "button:not([disabled])", "input:not([disabled]):not([type=hidden])",
  "select:not([disabled])", "textarea:not([disabled])", "[tabindex]:not([tabindex='-1'])",
].join(",");

// visibleFocusable lists the tab stops inside `root`, in document order, skipping
// anything hidden — a collapsed section's fields are in the DOM and are not stops.
function visibleFocusable(root) {
  return [...root.querySelectorAll(FOCUSABLE)].filter((el) =>
    el.offsetWidth > 0 || el.offsetHeight > 0 || el === document.activeElement);
}

// openDialog puts a modal dialog on screen and returns { el, body, close }.
//
//   title      the heading, shown in the head
//   label      what assistive technology announces (defaults to the title)
//   body       an element, or an HTML string, for the dialog's content
//   actions    [{ label, kind, value, keepOpen, attrs, disabled }] — the foot's
//              buttons, left to right. Pressing one closes with its `value` unless
//              keepOpen is set; `kind` is "primary" (filled), "neutral", "ghost" or
//              "danger"; `attrs` puts caller-chosen attributes on the button, which
//              is how a caller keeps addressing its own buttons after moving here.
//   width      a max-width for the dialog box
//   className  an extra class on the box, for a dialog that styles its own body
//   overlayId  an id for the overlay element, for a caller that addresses it
//   onClose    called with the value the dialog closed on; a dismissal passes null
//   dismissible  false to refuse Escape and the backdrop, for a dialog whose
//              actions are the only way out (a destructive confirm)
//
// Dismissal — Escape, the backdrop, the close button — is not an action: it passes
// null, so a caller can tell "cancel" from "the third button".
export function openDialog(opts = {}) {
  const { title = "", label, body, actions = [], width, className, onClose, dismissible = true } = opts;

  const returnFocusTo = document.activeElement;

  const ov = document.createElement("div");
  ov.className = "modal-ov";
  if (opts.overlayId) ov.id = opts.overlayId;
  const box = document.createElement("div");
  box.className = "modal" + (className ? " " + className : "");
  box.setAttribute("role", "dialog");
  box.setAttribute("aria-modal", "true");
  box.setAttribute("aria-label", label || title);
  box.tabIndex = -1; // focusable by script, not a tab stop: the fallback holder
  if (width) box.style.maxWidth = typeof width === "number" ? width + "px" : width;

  const head = document.createElement("div");
  head.className = "modal-head";
  const h = document.createElement("h2");
  h.textContent = title;
  const x = document.createElement("button");
  x.type = "button";
  x.className = "icon-btn";
  x.setAttribute("aria-label", "Close");
  x.title = "Close";
  x.textContent = "✕";
  head.append(h, x);

  const bodyEl = document.createElement("div");
  bodyEl.className = "modal-body";
  if (body instanceof Node) bodyEl.appendChild(body);
  else if (typeof body === "string") bodyEl.innerHTML = body;

  box.append(head, bodyEl);

  let foot = null;
  if (actions.length) {
    foot = document.createElement("div");
    foot.className = "modal-foot";
    // .modal-foot is space-between, so the buttons need something on the left to be
    // pushed away from — an empty span when the caller has nothing to say there.
    // Without it two buttons sit at opposite ends of the dialog.
    const note = document.createElement("span");
    note.className = "muted small";
    const spacer = actions.find((a) => a.spacer);
    if (spacer) note.textContent = spacer.spacer;
    foot.appendChild(note);
    const group = document.createElement("span");
    group.className = "modal-actions";
    foot.appendChild(group);
    for (const a of actions) {
      if (a.spacer) continue; // already placed on the left
      const b = document.createElement("button");
      b.type = "button";
      b.className = "btn" + (a.kind && a.kind !== "primary" ? " " + a.kind.replace("danger", "ghost danger") : "");
      b.textContent = a.label;
      if (a.title) b.title = a.title;
      if (a.disabled) b.disabled = true;
      for (const [k, v] of Object.entries(a.attrs || {})) b.setAttribute(k, v);
      b.addEventListener("click", () => {
        if (b.disabled) return;
        if (a.onSelect) a.onSelect();
        if (!a.keepOpen) close(a.value === undefined ? a.label : a.value);
      });
      group.appendChild(b);
    }
    box.appendChild(foot);
  }

  ov.appendChild(box);
  document.body.appendChild(ov);

  let closed = false;
  function close(value = null) {
    if (closed) return;
    closed = true;
    document.removeEventListener("keydown", onKey, true);
    ov.remove();
    // Back where it came from. A dialog that drops the focus leaves a keyboard user
    // at the top of the page with no idea where they were.
    if (returnFocusTo && returnFocusTo.isConnected && typeof returnFocusTo.focus === "function") {
      returnFocusTo.focus();
    }
    if (onClose) onClose(value);
  }

  function onKey(e) {
    if (e.key === "Escape" && dismissible) {
      e.stopPropagation();
      close(null);
      return;
    }
    if (e.key !== "Tab") return;
    // Keep the focus in the dialog. Without this the tab order walks out into the
    // page behind, which is still there and still clickable to a keyboard.
    const stops = visibleFocusable(box);
    if (!stops.length) { e.preventDefault(); box.focus(); return; }
    const first = stops[0], last = stops[stops.length - 1];
    const on = document.activeElement;
    if (!box.contains(on)) { e.preventDefault(); (e.shiftKey ? last : first).focus(); return; }
    if (!e.shiftKey && on === last) { e.preventDefault(); first.focus(); }
    else if (e.shiftKey && on === first) { e.preventDefault(); last.focus(); }
  }
  // Capture, so a dialog opened over a view that listens for Escape itself (the
  // editor does) is the one that answers it.
  document.addEventListener("keydown", onKey, true);

  x.addEventListener("click", () => close(null));

  // The backdrop closes only when the gesture both starts and ends on it: selecting
  // text in a field and releasing outside the box is not "close without saving".
  let downOnBackdrop = false;
  ov.addEventListener("mousedown", (e) => { downOnBackdrop = e.target === ov; });
  ov.addEventListener("click", (e) => {
    if (dismissible && e.target === ov && downOnBackdrop) close(null);
    downOnBackdrop = false;
  });

  // The first thing a person would use: a field to type in, else the first action,
  // else the close button, else the box itself — never nothing.
  const stops = visibleFocusable(bodyEl);
  (stops[0] || (foot && foot.querySelector("button")) || x || box).focus();

  return { el: box, body: bodyEl, close };
}
