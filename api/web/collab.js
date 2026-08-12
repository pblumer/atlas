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
// (409) and surfaced as a hint, not enforced in the canvas. A peer's change is
// applied by re-importing the saved draft into the live canvas (viewport and
// selection preserved) so co-editors stay in sync without a manual reload — the
// durable draft is the source of truth (the server relays changes, it does not
// merge them), so this reflects saved state; an unsaved in-flight peer edit and
// true per-element merging await the op-log/CRDT upgrade. A re-import never runs
// over unsaved local work: a dirty canvas defers the sync until the next save.

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
    canEdit: true,         // project role permits editing; a viewer joins read-only (ADR-0071)
    name: guestName(),
    participants: [],
    locks: [],
    myLocks: new Set(),
    overlayIds: [],
    closed: false,
    es: null,
    applyingRemote: false, // true while we re-import a peer's change (see reimport)
    dirty: false,          // local canvas has unsaved edits — don't clobber them
    pendingRemote: false,  // a peer change arrived while dirty; sync after next save
    warnedPending: false,  // deferral hint already shown for the current pending sync
  };

  const base = `/api/v1/drafts/${encodeURIComponent(draftId)}/session`;

  // Fire-and-forget POST of a session action; a 409 on a lock is expected (the
  // element is held by someone else) and surfaced as a hint rather than an error.
  const send = (suffix, body, onConflict) => {
    if (!state.self || state.closed) return;
    // A viewer (read-only project role) never sends a mutating action — the
    // server would 403 it; skipping keeps a viewer's canvas a clean watch view.
    if (!state.canEdit) return;
    api("POST", base + suffix, { participantId: state.self, ...body })
      .catch((e) => { if (onConflict) onConflict(e); });
  };

  // --- Presence bar (a floating roster on the canvas) ---
  const container = modeler.get("canvas").getContainer();
  const bar = document.createElement("div");
  bar.className = "collab-presence";
  container.appendChild(bar);

  const renderPresence = () => {
    // Collapse multiple streams of the same signed-in identity into one avatar, so
    // a second tab or a lingering reconnect never shows the same person twice.
    // Each open editor stream is its own server-side participant; the roster keys
    // by userId (when authenticated) so those streams fold into one, with a ×N
    // count. Guests (no userId — e.g. auth off) stay distinct: they really are
    // different people. Color and identity key off the group, not the stream id.
    const groups = new Map();
    for (const p of state.participants) {
      const key = p.userId ? "u:" + p.userId : "p:" + p.id;
      let g = groups.get(key);
      if (!g) { g = { key, name: p.name, selection: p.selection, count: 0, me: false }; groups.set(key, g); }
      g.count++;
      if (p.id === state.self) { g.me = true; g.selection = p.selection; } // prefer my own stream's view
      else if (!g.selection) { g.selection = p.selection; }
    }
    bar.innerHTML = Array.from(groups.values()).map((g) => {
      const where = g.selection ? ` · ${esc(g.selection)}` : "";
      const many = g.count > 1 ? ` (×${g.count})` : "";
      const badge = g.count > 1 ? `<i class="collab-count">${g.count}</i>` : "";
      return `<span class="collab-avatar${g.me ? " me" : ""}" style="--h:${hueFor(g.key)}" ` +
        `title="${esc(g.name)}${g.me ? " (you)" : ""}${many}${where}">${esc(initials(g.name))}${badge}</span>`;
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

  // --- Remote change → seamless re-import of the saved draft ---
  // A `change` frame names the element a peer touched but is not a merge payload:
  // the durable truth is the saved draft (the server relays changes, it does not
  // persist them — api/collabsession.go). So on a peer's edit we re-fetch the draft
  // and re-import it, preserving viewport and selection, instead of forcing a
  // reload. It reflects *saved* state (an agent's saved edits, a collaborator's
  // Save) and never overwrites unsaved local work — a dirty canvas defers the sync
  // until the next save (markSaved), so a co-editor's arriving change can't discard
  // what this editor has typed but not yet saved.
  let reimportTimer = null;
  let heartbeatTimer = null;

  const reimport = async () => {
    if (state.closed || state.applyingRemote) return;
    if (state.dirty) { // don't clobber unsaved local edits — sync after the next save
      state.pendingRemote = true;
      if (!state.warnedPending) {
        state.warnedPending = true;
        toast("A collaborator changed the diagram — save to merge their changes", "warn");
      }
      return;
    }
    let xml;
    try { xml = await api("GET", `/api/v1/drafts/${encodeURIComponent(draftId)}/xml`); }
    catch { return; } // transient fetch failure: the next change frame retries
    if (state.closed || state.dirty) return; // a local edit landed while we fetched
    let vb = null, sel = [];
    try {
      vb = modeler.get("canvas").viewbox();
      sel = modeler.get("selection").get().map((el) => el.id);
    } catch { /* modeler torn down mid-flight */ }
    state.applyingRemote = true; // suppress our own change/selection broadcasts below
    try {
      await modeler.importXML(typeof xml === "string" ? xml : String(xml));
      if (vb) { try { modeler.get("canvas").viewbox(vb); } catch { /* ignore */ } }
      // Re-select what we had so our locks and presence are re-announced for the
      // elements that survived the import (onSelection runs once we clear the flag).
      try {
        const reg = modeler.get("elementRegistry");
        const still = sel.map((id) => reg.get(id)).filter(Boolean);
        modeler.get("selection").select(still.length ? still : null);
      } catch { /* ignore */ }
    } catch { /* malformed draft: leave the current canvas untouched */ }
    finally { state.applyingRemote = false; }
    state.pendingRemote = false;
    state.warnedPending = false;
    renderLocks(); // importXML wiped the canvas overlays — redraw peers' lock badges
  };

  // scheduleReimport coalesces a burst of change frames (e.g. an agent rewiring
  // several elements at once) into a single re-import shortly after they settle.
  const scheduleReimport = () => {
    if (reimportTimer) clearTimeout(reimportTimer);
    reimportTimer = setTimeout(() => { reimportTimer = null; reimport(); }, 300);
  };

  // --- Incoming stream ---
  const applySync = (d) => {
    state.self = d.self;
    state.canEdit = d.canEdit !== false; // absent (older server) defaults to editable
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
      if (c.by === state.self) return; // our own edit, echoed back
      if (c.elementId) pulse(c.elementId); // brief awareness flash on the touched shape
      scheduleReimport();                  // then pull the saved draft into our canvas
    } catch { /* ignore */ }
  });

  // Heartbeat: our locks are freed the instant this stream drops (the server's
  // keepalive write fails), but a half-open connection can hide a dead tab far
  // longer. Re-announce presence on a fixed interval — well inside the server's
  // participant TTL — so its reaper can evict a truly gone browser (and free its
  // locks) as a backstop, while this live editor keeps its session by heartbeating.
  heartbeatTimer = setInterval(() => {
    let sel = [];
    try { sel = modeler.get("selection").get() || []; } catch { /* torn down */ }
    send("/presence", { selection: sel.length ? sel[0].id : "" });
  }, 20000);

  // --- Outgoing: selection drives presence + locks ---
  const onSelection = (ev) => {
    if (state.applyingRemote) return; // selection churn from our own re-import
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

  // --- Outgoing: relay this editor's edits and mark the canvas unsaved ---
  const onChange = (ev) => {
    if (state.applyingRemote) return; // our own re-import, not a user edit — never echo it
    const el = ev && ev.element;
    if (el && el.id) { state.dirty = true; send("/change", { elementId: el.id }); }
  };
  modeler.on("element.changed", onChange);

  return {
    // markSaved clears the unsaved-work guard after the editor persists the draft,
    // so a peer change that was deferred (to avoid clobbering local edits) can now
    // sync in. The editor's Save handler calls this on a successful save.
    markSaved() {
      state.dirty = false;
      state.warnedPending = false;
      if (state.pendingRemote) scheduleReimport();
    },
    close() {
      state.closed = true;
      if (reimportTimer) { clearTimeout(reimportTimer); reimportTimer = null; }
      if (heartbeatTimer) { clearInterval(heartbeatTimer); heartbeatTimer = null; }
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
