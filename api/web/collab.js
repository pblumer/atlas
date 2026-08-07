// collab.js — live collaborative-modeling client (ADR-0103).
//
// Binds a Modeler canvas to a draft's Server-Sent Events session so co-editors
// appear in real time: their presence (who is here, what they have selected) and
// their per-element edit locks are drawn live on the canvas, while this editor's
// own selection, locks, and element changes are broadcast back to them. It is a
// thin client over the design-time session endpoints (GET .../session for the
// stream, POST .../session/{presence,lock,change} for actions) — the engine and
// its invariants are never involved.
//
// Concurrency follows the server's first cut (ADR-0103): selecting an element
// acquires a soft lock on it; an element another participant holds is refused
// (409) and surfaced as a hint, not enforced in the canvas. Applying a peer's
// structural change into the local canvas is deliberately out of scope here (the
// op-log/CRDT upgrade); a remote change is surfaced as a brief awareness pulse.

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// hueFor derives a stable, well-spread hue from a participant id, so each person
// gets a consistent color across everyone's screens without any coordination.
function hueFor(id) {
  let h = 0;
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
  return h % 360;
}

// initials abbreviates a display name to two characters for an avatar disc.
function initials(name) {
  const s = String(name || "").trim();
  if (!s) return "?";
  const p = s.split(/\s+/);
  return (p.length > 1 ? p[0][0] + p[1][0] : s.slice(0, 2)).toUpperCase();
}

// guestName invents a friendly per-tab label for a session with auth off, where
// there is no signed-in identity to name a participant by.
function guestName() {
  const animals = ["Fox", "Owl", "Elk", "Wren", "Lynx", "Hare", "Crane", "Ibis"];
  const a = animals[Math.floor(Math.random() * animals.length)];
  return "Guest " + a;
}

// isLockable reports whether a selected element is one we broadcast a lock for.
// Labels and the diagram root are not editable shapes in this sense.
function isLockable(el) {
  return !!(el && el.id && el.type && el.type !== "label" &&
    el.type !== "bpmn:Process" && el.type !== "bpmn:Collaboration");
}

// attachCollab wires the modeler into the draft's live session and returns a
// handle whose close() tears everything down (the editor's cleanup() calls it).
export function attachCollab(modeler, api, draftId, toast) {
  if (typeof EventSource === "undefined") return { close() {} };

  const state = {
    self: null,
    name: guestName(),
    participants: [],
    locks: [],
    myLocks: new Set(),
    overlayIds: [],
    closed: false,
    es: null,
  };

  const base = `/api/v1/drafts/${encodeURIComponent(draftId)}/session`;

  // Fire-and-forget POST of a session action; a 409 on a lock is expected (the
  // element is held by someone else) and surfaced as a hint rather than an error.
  const send = (suffix, body, onConflict) => {
    if (!state.self || state.closed) return;
    api("POST", base + suffix, { participantId: state.self, ...body })
      .catch((e) => { if (onConflict) onConflict(e); });
  };

  // --- Presence bar (a floating roster on the canvas) ---
  const container = modeler.get("canvas").getContainer();
  const bar = document.createElement("div");
  bar.className = "collab-presence";
  container.appendChild(bar);

  const renderPresence = () => {
    bar.innerHTML = state.participants.map((p) => {
      const me = p.id === state.self;
      const where = p.selection ? ` · ${esc(p.selection)}` : "";
      return `<span class="collab-avatar${me ? " me" : ""}" style="--h:${hueFor(p.id)}" ` +
        `title="${esc(p.name)}${me ? " (you)" : ""}${where}">${esc(initials(p.name))}</span>`;
    }).join("");
  };

  // --- Lock badges (a marker on each element another participant is editing) ---
  const renderLocks = () => {
    let overlays;
    try { overlays = modeler.get("overlays"); } catch { return; }
    for (const id of state.overlayIds) { try { overlays.remove(id); } catch { /* gone */ } }
    state.overlayIds = [];
    for (const l of state.locks) {
      if (l.holderId === state.self) continue; // my own locks need no badge
      try {
        state.overlayIds.push(overlays.add(l.elementId, "collab-lock", {
          position: { top: -12, right: 12 },
          html: `<span class="collab-lock" style="--h:${hueFor(l.holderId)}" ` +
            `title="Locked by ${esc(l.holderName)}">🔒 ${esc(initials(l.holderName))}</span>`,
        }));
      } catch { /* shape not on canvas (mid-import) — skip */ }
    }
  };

  // isLockedByOther reports whether an element is held by someone other than us.
  const isLockedByOther = (id) =>
    state.locks.some((l) => l.elementId === id && l.holderId !== state.self);

  // --- Remote change awareness: a brief pulse on the touched element ---
  const pulse = (elementId) => {
    let canvas;
    try { canvas = modeler.get("canvas"); } catch { return; }
    try {
      canvas.addMarker(elementId, "collab-touched");
      setTimeout(() => { try { canvas.removeMarker(elementId, "collab-touched"); } catch { /* gone */ } }, 1500);
    } catch { /* not on canvas */ }
  };

  // --- Incoming stream ---
  const applySync = (d) => {
    state.self = d.self;
    state.participants = d.participants || [];
    state.locks = d.locks || [];
    renderPresence();
    renderLocks();
  };
  const es = new EventSource(base + `?name=${encodeURIComponent(state.name)}`);
  state.es = es;
  es.addEventListener("sync", (e) => { try { applySync(JSON.parse(e.data)); } catch { /* ignore */ } });
  es.addEventListener("presence", (e) => {
    try { state.participants = JSON.parse(e.data).participants || []; renderPresence(); } catch { /* ignore */ }
  });
  es.addEventListener("lock", (e) => {
    try { state.locks = JSON.parse(e.data).locks || []; renderLocks(); } catch { /* ignore */ }
  });
  es.addEventListener("change", (e) => {
    try {
      const c = JSON.parse(e.data);
      if (c.by !== state.self && c.elementId) pulse(c.elementId);
    } catch { /* ignore */ }
  });

  // --- Outgoing: selection drives presence + locks ---
  const onSelection = (ev) => {
    const sel = (ev && ev.newSelection) || [];
    const wanted = new Set(sel.filter(isLockable).map((el) => el.id));

    // Release locks we held but no longer have selected.
    for (const id of Array.from(state.myLocks)) {
      if (!wanted.has(id)) { state.myLocks.delete(id); send("/lock", { elementId: id, action: "release" }); }
    }
    // Acquire locks for newly selected elements not already ours.
    for (const id of wanted) {
      if (state.myLocks.has(id)) continue;
      if (isLockedByOther(id)) { toast(`${id} is being edited by someone else`, "warn"); continue; }
      state.myLocks.add(id);
      send("/lock", { elementId: id, action: "acquire" }, (e) => {
        state.myLocks.delete(id);
        if (String(e.message || "").includes("locked")) toast(`${id} is being edited by someone else`, "warn");
      });
    }
    // Announce where we are looking.
    send("/presence", { selection: sel.length ? sel[0].id : "" });
  };
  modeler.on("selection.changed", onSelection);

  // --- Outgoing: relay element edits so peers get an awareness pulse ---
  const onChange = (ev) => {
    const el = ev && ev.element;
    if (el && el.id) send("/change", { elementId: el.id });
  };
  modeler.on("element.changed", onChange);

  return {
    close() {
      state.closed = true;
      try { modeler.off("selection.changed", onSelection); } catch { /* torn down */ }
      try { modeler.off("element.changed", onChange); } catch { /* torn down */ }
      try { es.close(); } catch { /* already closed */ }
      // Best-effort release of our locks and departure; keepalive lets it finish
      // even as the page unloads.
      if (state.self) {
        const body = JSON.stringify({ participantId: state.self });
        try { navigator.sendBeacon?.(base + "/leave", body); } catch { /* ignore */ }
      }
      try { bar.remove(); } catch { /* already gone */ }
    },
  };
}
