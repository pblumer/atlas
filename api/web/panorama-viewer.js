// Read-only ArchiMate 3.2 diagram surface (ADR-0189). The canonical XML stays
// untouched on the server; this module only projects its Diagram views into the
// Atlas-owned diagram-js renderer vendored under vendor/archimate.

// An element's documentation is prose, and prose in Atlas is Markdown
// (ADR-0250). It is rendered with the shared module rather
// than escaped into one paragraph, which also matters here in a way it does not
// elsewhere: this text comes out of a foreign modelling tool, so it is exactly the kind
// of string that must be inert. renderMarkdown escapes before it parses.
import { renderMarkdown } from "./markdown.js";

const esc = (value) => String(value ?? "").replace(/[&<>"']/g, (character) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character]);

let vendorPromise;
function loadVendor() {
  if (globalThis.AtlasArchiMate) return Promise.resolve(globalThis.AtlasArchiMate);
  if (vendorPromise) return vendorPromise;
  const link = document.createElement("link");
  link.rel = "stylesheet";
  link.href = "vendor/archimate/diagram-js.css";
  document.head.appendChild(link);
  vendorPromise = new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = "vendor/archimate/archimate-viewer.js";
    script.onload = () => resolve(globalThis.AtlasArchiMate);
    script.onerror = () => reject(new Error("Could not load the ArchiMate renderer"));
    document.head.appendChild(script);
  });
  return vendorPromise;
}

const prettyType = (type) => String(type || "Element").replace(/([a-z])([A-Z])/g, "$1 $2");

// Atlas bindings (ADR-0189 §4) say which Atlas resource an element refers to. The
// document stores an opaque id and nothing else; the name below always comes from
// the server, so a drawing can never hold a stale copy of it.

// KEYS maps each binding key to the element types it is valid on and the label the
// panel shows. It mirrors the server's contract; a key the server would refuse must
// not be offerable here, or the panel would invite an error.
const BINDING_KEYS = [
  { key: "atlas.applicationId", label: "Process application", on: ["ApplicationComponent"] },
  { key: "atlas.processId", label: "BPMN process", on: ["BusinessProcess"] },
  { key: "atlas.connectorId", label: "Worker", on: ["ApplicationService", "ApplicationInterface"] },
  { key: "atlas.jobType", label: "Job type", on: ["ApplicationService", "ApplicationInterface"] },
  { key: "atlas.runtimeId", label: "Atlas runtime", on: ["Node"] },
  { key: "atlas.deploymentTargetId", label: "Deployment target", on: ["Node"] },
  { key: "atlas.releaseId", label: "Release", on: ["Artifact"] },
];

// STATUS_TEXT turns a resolution status into a sentence. The four are deliberately
// distinct: "you may not see it" is not "it is gone", and neither is "this server
// cannot resolve that kind yet".
const STATUS_TEXT = {
  forbidden: "outside your access",
  missing: "no longer on this server",
  unsupported: "this server cannot resolve this kind yet",
};

function bindingValueHTML(value) {
  if (value.status === "resolved") {
    return `<li class="panorama-bound"><b>${esc(value.name)}</b>
      <code class="muted">${esc(value.value)}</code></li>`;
  }
  // An unresolved binding is shown, never hidden: dropping it would make a broken
  // binding look like an absent one, and the model would then look correct.
  return `<li class="panorama-bound unresolved"><code>${esc(value.value)}</code>
    <span class="panorama-unresolved">${esc(STATUS_TEXT[value.status] || value.status)}</span></li>`;
}

function bindingsHTML(item, resolution, canEdit) {
  if (!item || item.kind === "relationship") return "";
  const applicable = BINDING_KEYS.filter((entry) => entry.on.includes(item.type));
  if (!applicable.length) {
    return `<section class="psec"><h3>Atlas bindings</h3>
      <p class="muted">No Atlas resource kind binds to a ${esc(prettyType(item.type))}.</p></section>`;
  }
  const byKey = new Map();
  for (const binding of resolution?.bindings || []) {
    if (binding.elementId === item.id) byKey.set(binding.key, binding.values);
  }
  const rows = applicable.map((entry) => {
    const values = byKey.get(entry.key) || [];
    return `<div class="panorama-binding" data-key="${esc(entry.key)}">
      <div class="panorama-binding-head">
        <span>${esc(entry.label)}</span>
        ${canEdit ? `<button class="btn ghost small" data-bind-edit="${esc(entry.key)}">${values.length ? "Change" : "Bind"}</button>` : ""}
      </div>
      ${values.length
        ? `<ul class="panorama-bound-list">${values.map(bindingValueHTML).join("")}</ul>`
        : `<p class="muted">Not bound.</p>`}
    </div>`;
  }).join("");
  return `<section class="psec"><h3>Atlas bindings</h3>${rows}</section>`;
}

// SEVERITY_TEXT is how an observation's class reads in the panel. It matches the
// landscape mesh's wording deliberately: the same finding, seen from the drawing
// and from the derived graph, must not be described in two different vocabularies.
const SEVERITY_TEXT = {
  ok: "OK",
  attention: "Attention",
  critical: "Critical",
  unknown: "Not watched",
};

// STATE_TEXT names the observation state under the class (ADR-0189 §6). The class
// is a reading aid; the state is what somebody acts on, so both are shown.
const STATE_TEXT = {
  healthy: "healthy",
  degraded: "degraded",
  "not-ready": "not ready",
  unreachable: "unreachable",
  stale: "stale",
  unbound: "nothing here observes it",
};

// liveHTML is what this element is *doing*, beside what it is (ADR-0189 §6).
//
// It is a section of the properties panel rather than an overlay on the diagram,
// and that is the ADR's own constraint reaching the UI: ArchiMate layer colours
// stay intact, so runtime state is carried by text and badges rather than by
// recolouring an element's fill out from under its semantics.
//
// A model with no observations for the selected element says so. The alternative —
// rendering nothing — reads as an element that is fine, and the difference between
// "nothing is wrong" and "nothing is watching" is the one this whole projection
// exists to keep.
function liveHTML(item, observations) {
  if (!item || !observations) return "";
  const mine = (observations.observations || []).filter((o) => o.elementId === item.id);
  if (!mine.length) {
    return `<section class="psec"><h3>Live</h3>
      <p class="muted">This element binds nothing, so there is nothing to observe.</p></section>`;
  }
  const rows = mine.map((o) => `<div class="panorama-obs panorama-sev-${esc(o.severity)}">
      <div class="panorama-obs-head">
        <b>${esc(SEVERITY_TEXT[o.severity] || o.severity)}</b>
        <span class="muted">${esc(STATE_TEXT[o.state] || o.state)} · ${esc(o.source)}</span>
      </div>
      <code>${esc(o.key)} = ${esc(o.value)}</code>
      ${o.reason ? `<p>${esc(o.reason)}</p>` : ""}
      ${sinceHTML(o)}
      ${detailHTML(o.detail)}
    </div>`).join("");
  // What the view cannot see, stated beside what it can. Without it a model
  // nothing observes renders as an architecture where everything is fine.
  const unwatched = (observations.unavailable || []).map((u) => esc(STATE_TEXT[u.state] || u.state));
  return `<section class="psec"><h3>Live</h3>${rows}
    ${unwatched.length ? `<p class="muted panorama-unwatched">Not watched here: ${unwatched.join(", ")}.</p>` : ""}
    <p class="muted">Read at ${esc(new Date((observations.observedAt || 0) * 1000).toLocaleTimeString())};
    nothing here is stored on the model.</p></section>`;
}

// CANVAS_MARK is how a severity is drawn *on the diagram* (ADR-0189 §6, P4d).
//
// The record's constraint is the whole design here: "ArchiMate layer colors remain
// intact. Runtime state is shown with borders, badges, icons, and an accessible
// text legend rather than recoloring semantic element fills." A layer colour is
// semantics — it says whether an element is business, application or technology —
// and painting health over it would destroy one meaning to show another. So the
// element's own fill is never touched; a mark sits *around* and *beside* it.
//
// Three classes are drawn and one is not:
//
//   - critical and attention carry a glyph, because they are what somebody is
//     scanning the diagram for;
//   - ok carries a small unobtrusive dot rather than nothing, because "observed and
//     fine" and "not observed at all" must not look the same — that conflation is
//     the one this whole projection exists to prevent;
//   - unknown carries nothing. Most elements of a young model are unbound, and a
//     badge on every one of them would make the diagram a wall of marks.
const CANVAS_MARK = {
  critical: { glyph: "!", label: "Critical — it cannot do work" },
  attention: { glyph: "•", label: "Attention — something inside it went wrong" },
  ok: { glyph: "", label: "OK — observed, nothing wrong" },
};

// SEVERITY_RANK orders the classes so an element bound to several resources shows
// the worst of them. An element that is fine in one binding and broken in another
// is not fine, and showing the first answer found would depend on document order.
const SEVERITY_RANK = { unknown: 0, ok: 1, attention: 2, critical: 3 };

// worstByElement reduces a document to one finding per ArchiMate element.
export function worstByElement(observations) {
  const worst = new Map();
  for (const o of (observations && observations.observations) || []) {
    const held = worst.get(o.elementId);
    if (!held || (SEVERITY_RANK[o.severity] || 0) > (SEVERITY_RANK[held.severity] || 0)) {
      worst.set(o.elementId, o);
    }
  }
  return worst;
}

// sinceHTML says when this value last changed, if anything has been seen to change
// it (ADR-0189 P5).
//
// "Degraded" and "degraded since nine this morning" are different findings, and the
// second is the one somebody acts on: a state that has held for a week is a
// standing condition, one that turned over five minutes ago is an incident. It
// comes on the observation itself rather than from a second request.
//
// Absent when nothing has been *seen* to change — which is not the same as nothing
// having changed, and is why the journal publishes its limits beside this.
function sinceHTML(o) {
  if (!o.changedAt) return "";
  const when = new Date(o.changedAt * 1000);
  const was = o.previousState ? ` (was ${esc(STATE_TEXT[o.previousState] || o.previousState)})` : "";
  return `<p class="panorama-obs-since muted">Changed ${esc(when.toLocaleString())}${was}</p>`;
}

// maxDriftRows bounds what one panel renders. The journal is already bounded on the
// server; this is the second, smaller bound of what a person reads in a side panel
// without scrolling past the element they selected it for.
const maxDriftRows = 25;

// driftHTML is what has been seen to change about the selected element, newest
// first (ADR-0189 P5).
//
// It is scoped to the selection rather than to the model, because the panel it sits
// in is: a reader who selected one component and got the whole landscape's history
// has to work out which lines are about the thing in front of them. The rest of the
// model is not hidden — the count says how many other changes the journal holds, so
// the narrowing is visible rather than silent.
//
// It is a section rather than a chart, because this is not a time series and must
// not look like one — ADR-0189 is explicit that Panorama stays a correlation
// surface. What it holds is transitions: a hundred identical readings produce
// nothing, one release going stale produces one line.
//
// The limits are rendered with it, never under a fold. A history that hides what it
// cannot see is worse than no history: without them a reader cannot tell "nothing
// happened" from "nobody looked", and this journal only ever sees what was looked at.
function driftHTML(drift, item) {
  if (!item || item.kind === "relationship") return "";
  // A body that is not a journal is treated as one that never arrived. Rendering
  // "nothing has been seen to change" from something this code cannot read would
  // turn a transport or contract fault into a finding about the architecture,
  // which is the one mistake this whole section exists to avoid.
  if (!drift || !Array.isArray(drift.entries)) return "";
  const all = drift.entries;
  const mine = all.filter((e) => e.elementId === item.id);
  const elsewhere = all.length - mine.length;
  const limits = (Array.isArray(drift.limits) ? drift.limits : []).map((l) =>
    `<li><b>${esc(l.limit)}</b> — ${esc(l.reason)}</li>`).join("");
  const entries = mine.slice(0, maxDriftRows).map((e) => `
    <li class="panorama-drift-entry">
      <span class="muted">${esc(new Date(e.at * 1000).toLocaleString())}</span>
      <code>${esc(e.value)}</code>
      <span>${esc(STATE_TEXT[e.from] || e.from)} → <b>${esc(STATE_TEXT[e.to] || e.to)}</b></span>
      ${e.reason ? `<p class="muted">${esc(e.reason)}</p>` : ""}
    </li>`).join("");
  return `<section class="psec"><h3>What changed</h3>
    ${entries
      ? `<ul class="panorama-drift">${entries}</ul>
         ${mine.length > maxDriftRows ? `<p class="muted">${mine.length - maxDriftRows} older change(s) for this element are not shown.</p>` : ""}`
      : `<p class="muted">Nothing has been seen to change about this element.</p>`}
    ${elsewhere ? `<p class="muted">${elsewhere} other recorded change(s) in this model.</p>` : ""}
    ${drift.since ? `<p class="muted">Recorded since ${esc(new Date(drift.since * 1000).toLocaleString())}.</p>` : ""}
    ${drift.truncated ? `<p class="muted">Older changes were dropped to stay inside the journal's bound.</p>` : ""}
    <details class="panorama-drift-limits"><summary>What this history cannot see</summary>
      <ul>${limits}</ul></details>
  </section>`;
}

// CONTEXT_STATE is what each of the six context states means to a reader. They are
// six rather than two because every one of them sends somebody somewhere different,
// and a panel that rendered them all as "no data" would undo the distinction the
// whole surface exists to make.
const CONTEXT_STATE = {
  "not-configured": "No such store is wired here, so nothing was asked.",
  unidentifiable: "This store cannot name a thing of this kind.",
  unreachable: "The store could not be reached, so nothing is known about this window.",
  refused: "The store declined the query.",
  empty: "The store holds nothing for this in this window.",
  available: "",
};

// CONTEXT_SOURCE is the store behind an answer. An operator who disagrees with a
// number needs to know which system to go and argue with.
const CONTEXT_SOURCE = { events: "Event log", metrics: "Metrics" };

// sparkHTML draws one measure as a bar strip.
//
// It is a strip rather than a chart because this is not a time series and must not
// look like one: ADR-0189 keeps Panorama a correlation surface, and a smooth line
// with axes would promise a continuity that a query against somebody else's
// retention does not have. What it shows is shape — quiet, then busy, then quiet —
// which is the whole of what "has it been like this" needs.
//
// Every bar carries its own count as a title, so the shape is readable without a
// hover and exact without a legend.
function sparkHTML(measure) {
  const buckets = Array.isArray(measure.buckets) ? measure.buckets : [];
  if (!buckets.length) {
    return `<p class="muted">No buckets in this window.</p>`;
  }
  const peak = buckets.reduce((max, b) => Math.max(max, Number(b.value) || 0), 0);
  const bars = buckets.map((b) => {
    const value = Number(b.value) || 0;
    // A zero bucket keeps a hairline rather than vanishing: an empty interval is a
    // reading, and a gap where one should be reads as missing data.
    const height = peak > 0 ? Math.max(2, Math.round((value / peak) * 100)) : 2;
    const when = new Date((b.at || 0) * 1000).toLocaleString();
    return `<span class="panorama-spark-bar" style="height:${height}%" title="${esc(when)}: ${esc(String(value))}"></span>`;
  }).join("");
  return `<div class="panorama-spark" role="img"
    aria-label="${esc(measure.label || measure.name)}: ${esc(String(measure.total ?? 0))} over the window">${bars}</div>`;
}

// contextHTML is what the stores outside Atlas say about the selected element.
//
// It is fetched on demand rather than with the view: every bound value costs a
// query against somebody else's cluster, and a panel that fired those on every
// selection would make browsing a model expensive for a system that did not agree
// to it.
function contextHTML(ctx, loading) {
  if (loading) {
    return `<section class="psec"><h3>History</h3>
      <p class="muted">Asking the stores outside Atlas…</p></section>`;
  }
  if (!ctx) {
    return `<section class="psec"><h3>History</h3>
      <button class="btn ghost small" data-tool="context">Look up history</button>
      <p class="muted">Asks the stores outside Atlas what they hold about this element.
      Nothing is stored here.</p></section>`;
  }
  // A lookup that failed is its own state, distinct from one nobody has run. They
  // arrived here as the same absent value once, and the panel then re-offered the
  // button as though the request had never happened \u2014 which tells a reader that
  // nothing is wrong. It is the same conflation the six states below exist to
  // prevent, one layer up.
  if (ctx.failed || !Array.isArray(ctx.results)) {
    return `<section class="psec"><h3>History</h3>
      <button class="btn ghost small" data-tool="context">Try again</button>
      <p class="muted">The history could not be read, so nothing is known about this
      element\u2019s past \u2014 which is not the same as nothing having happened.</p></section>`;
  }
  const limits = (Array.isArray(ctx.limits) ? ctx.limits : []).map((l) =>
    `<li><b>${esc(l.limit)}</b> — ${esc(l.reason)}</li>`).join("");
  const rows = ctx.results.map((r) => {
    const measures = (r.measures || []).map((m) => `
      <div class="panorama-measure">
        <div class="panorama-measure-head">
          <span>${esc(m.label || m.name)}</span><b>${esc(String(m.total ?? 0))}</b>
        </div>
        ${sparkHTML(m)}
      </div>`).join("");
    const notCounted = r.detail && r.detail.notCounted
      ? `<p class="muted">Not counted: ${esc(r.detail.notCounted)}.</p>` : "";
    return `<div class="panorama-ctx panorama-ctx-${esc(r.state)}">
      <div class="panorama-ctx-head">
        <b>${esc(CONTEXT_SOURCE[r.source] || r.source)}</b>
        <span class="muted">${esc(r.state)}</span>
      </div>
      <code>${esc(r.key)} = ${esc(r.value)}</code>
      ${r.reason || CONTEXT_STATE[r.state]
        ? `<p class="muted">${esc(r.reason || CONTEXT_STATE[r.state])}</p>` : ""}
      ${measures}${notCounted}
    </div>`;
  }).join("");
  const window = ctx.window || {};
  return `<section class="psec"><h3>History</h3>
    ${rows || `<p class="muted">This element binds nothing, so there is nothing to ask about.</p>`}
    ${ctx.truncated ? `<p class="muted">Some answers were dropped to keep this bounded.</p>` : ""}
    <p class="muted">Window ${esc(window.window || "")}, read at
      ${esc(new Date((ctx.observedAt || 0) * 1000).toLocaleTimeString())}. Nothing here is stored.</p>
    <details class="panorama-ctx-limits"><summary>What this history cannot do</summary>
      <ul>${limits}</ul></details>
  </section>`;
}

// paletteHTML is what may be created, grouped by ArchiMate layer.
//
// It is built from the subset the server served, not from a list in this file. A
// palette that offered an element type the server will not write is a promise the
// server breaks, and the only way to be sure it cannot happen is to have one list.
//
// The subset's own limits are shown with it, under a fold. ADR-0189 forbids
// claiming complete ArchiMate 3.2 authoring, and a palette is the one thing
// somebody reads without reading anything else.
function paletteHTML(subset, canEdit) {
  if (!canEdit) return "";
  if (!subset || !Array.isArray(subset.elements)) {
    // The subset could not be read, so nothing is offered. Offering a guess would
    // be offering what the server may refuse.
    return `<p class="muted panorama-palette-empty">The palette could not be loaded.</p>`;
  }
  const byLayer = new Map();
  for (const kind of subset.elements) {
    if (!byLayer.has(kind.layer)) byLayer.set(kind.layer, []);
    byLayer.get(kind.layer).push(kind);
  }
  const groups = [...byLayer].map(([layer, kinds]) => `
    <div class="panorama-palette-group">
      <h4>${esc(layer)}</h4>
      ${kinds.map((kind) => `<button class="panorama-palette-item" type="button"
        data-add-type="${esc(kind.type)}" title="${esc(kind.label)}">${esc(kind.label)}</button>`).join("")}
    </div>`).join("");
  const limits = (subset.limits || []).map((l) =>
    `<li><b>${esc(l.limit)}</b> — ${esc(l.reason)}</li>`).join("");
  return `<h3>Add</h3>${groups}
    <details class="panorama-subset-limits"><summary>What can be authored</summary>
      <ul>${limits}</ul></details>`;
}

// connectHTML offers the relationships that may be drawn from the selected element
// to the others on this view.
//
// Every entry comes from the served matrix, so the menu contains only choices the
// write path accepts — somebody authoring a model should meet ArchiMate's rules by
// seeing what is offered, not by being refused after the fact.
function connectHTML(item, subset, views, canEdit) {
  if (!canEdit || !item || item.kind === "relationship" || !subset) return "";
  const others = (views || []).filter((other) => other.id !== item.id);
  if (!others.length) {
    return `<section class="psec"><h3>Connect</h3>
      <p class="muted">Nothing else is on this view yet.</p></section>`;
  }
  const rows = others.map((other) => {
    const allowed = (subset.matrix || {})[`${item.type}>${other.type}`] || [];
    if (!allowed.length) {
      // The subset says nothing may be drawn between these two. Shown rather than
      // hidden, so the absence is a statement instead of a gap.
      return `<div class="panorama-connect-row">
        <span>${esc(other.name || other.id)}</span>
        <span class="muted">nothing may be drawn</span></div>`;
    }
    return `<div class="panorama-connect-row">
      <span>${esc(other.name || other.id)}</span>
      <select data-connect-to="${esc(other.id)}" aria-label="Relationship to ${esc(other.name || other.id)}">
        ${allowed.map((type) => `<option value="${esc(type)}">${esc(type)}</option>`).join("")}
      </select>
      <button class="btn ghost small" data-connect="${esc(other.id)}">Draw</button>
    </div>`;
  }).join("");
  return `<section class="psec"><h3>Connect</h3>${rows}
    <p class="muted">Only relationships ArchiMate permits between these elements are offered.</p>
  </section>`;
}

// detailHTML renders the numbers behind the sentence — a version, a count — for a
// reader who wants them. Sorted, because this is something people compare between
// two servers.
function detailHTML(detail) {
  const entries = Object.entries(detail || {}).sort(([a], [b]) => a.localeCompare(b));
  if (!entries.length) return "";
  return `<div class="panorama-obs-detail">${entries
    .map(([k, v]) => `<span><span class="muted">${esc(k)}</span> ${esc(v)}</span>`).join("")}</div>`;
}

function propertiesHTML(item, resolution, canEdit, observations, drift, ctx, ctxLoading, subset, onView) {
  if (!item) return `<div class="panorama-props-empty">
    <div class="panorama-selection-icon">◇</div>
    <b>Nothing selected</b>
    <p>Select an element or relationship in the diagram to inspect its ArchiMate properties.</p>
  </div>`;
  const relationship = item.kind === "relationship";
  return `<div class="phead">
      <span class="ptype" aria-hidden="true">${relationship ? "↗" : "◇"}</span>
      <div><b>${esc(item.name || prettyType(item.type))}</b><div class="muted">${relationship ? "Relationship" : "Element"}</div></div>
    </div>
    <section class="psec"><h3>ArchiMate</h3>
      <div class="panorama-kv"><span>Type</span><b>${esc(prettyType(item.type))}</b></div>
      <div class="panorama-kv"><span>Identifier</span><code>${esc(item.id)}</code></div>
      ${relationship ? `<div class="panorama-kv"><span>Source</span><code>${esc(item.source)}</code></div>
        <div class="panorama-kv"><span>Target</span><code>${esc(item.target)}</code></div>` : ""}
    </section>
    ${item.documentation ? `<section class="psec"><h3>Documentation</h3>
      <div class="md">${renderMarkdown(item.documentation)}</div></section>` : ""}
    ${bindingsHTML(item, resolution, canEdit)}
    ${liveHTML(item, observations)}
    ${driftHTML(drift, item)}
    ${connectHTML(item, subset, onView, canEdit)}
    ${item.kind === "relationship" ? "" : contextHTML(ctx, ctxLoading)}`;
}

// pickBinding is the picker ADR-0189 §4 asks for: a user selects from resources
// they may see rather than typing an opaque id. Multi-select, because a binding is
// many-to-many — one ArchiMate component can be implemented by several Atlas
// process applications, and the picker must not quietly make that a single choice.
//
// Built on the shell's modal pattern rather than a new one. Resolves to the chosen
// ids, or null when cancelled — an empty array is a real answer here, since it is
// how a binding is cleared.
function pickBinding(list, current, key) {
  const chosen = new Set((current?.values || []).map((value) => value.value));
  return new Promise((resolve) => {
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal" role="dialog" aria-modal="true" aria-label="Bind Atlas resource">
        <div class="modal-head"><h2>Bind ${esc(key)}</h2></div>
        <div class="modal-body">
          <p class="muted" style="margin:0 0 10px">Only resources you may see are listed.
            Selecting none clears the binding.</p>
          <ul class="panorama-pick">
            ${list.candidates.map((candidate) => `<li><label>
              <input type="checkbox" value="${esc(candidate.id)}" ${chosen.has(candidate.id) ? "checked" : ""}/>
              <span><b>${esc(candidate.name)}</b> <code class="muted">${esc(candidate.id)}</code></span>
            </label></li>`).join("")}
          </ul>
        </div>
        <div class="modal-foot">
          <button class="btn neutral" data-cancel>Cancel</button>
          <button class="btn" data-confirm>Save binding</button>
        </div>
      </div>`;
    document.body.appendChild(ov);
    const close = (result) => { ov.remove(); document.removeEventListener("keydown", onKey); resolve(result); };
    const onKey = (event) => { if (event.key === "Escape") close(null); };
    document.addEventListener("keydown", onKey);
    ov.querySelector("[data-cancel]").addEventListener("click", () => close(null));
    ov.querySelector("[data-confirm]").addEventListener("click", () => {
      close([...ov.querySelectorAll("input:checked")].map((input) => input.value));
    });
    ov.addEventListener("click", (event) => { if (event.target === ov) close(null); });
    ov.querySelector("input, [data-confirm]")?.focus();
  });
}

// The C4 projection (ADR-0211 §8). It is shown as a structure with its loss report,
// not as a second canvas: what makes a projection trustworthy is that it says what
// it could not express, and a picture that merely omitted those would look complete
// and be wrong. ArchiMate stays the only thing anybody authors here — there is no
// write counterpart to this view and there is not meant to be one.
function c4PanelHTML(projection) {
  const byParent = new Map();
  for (const element of projection.elements) {
    const key = element.parent || "";
    if (!byParent.has(key)) byParent.set(key, []);
    byParent.get(key).push(element);
  }
  const render = (element) => {
    const children = byParent.get(element.id) || [];
    return `<li>
      <span class="c4-type">${esc(element.type)}</span>
      <b>${esc(element.name || element.id)}</b>
      <code class="muted">${esc(element.sourceType)}</code>
      ${element.description ? `<p class="muted">${esc(element.description)}</p>` : ""}
      ${children.length ? `<ul class="c4-tree">${children.map(render).join("")}</ul>` : ""}
    </li>`;
  };
  const roots = (byParent.get("") || []).map(render).join("");

  const relationships = projection.relationships.map((rel) => `<li>
    <b>${esc(rel.source)}</b> → <b>${esc(rel.target)}</b>
    ${rel.name ? `<span>${esc(rel.name)}</span>` : ""}
    <code class="muted">${esc(rel.sourceType)}</code></li>`).join("");

  // The loss report is the contractual half of this view, so it is never collapsed
  // away and never rendered as a footnote.
  const dropped = projection.dropped.map((loss) => `<li>
    <b>${esc(loss.name || loss.id)}</b>
    <code class="muted">${esc(loss.sourceType)}</code>
    <span class="c4-reason">${esc(loss.reason)}</span></li>`).join("");

  return `<div class="c4-panel">
    <div class="c4-head">
      <h2>C4 projection</h2>
      <p class="muted">A read-only projection of this ArchiMate model at revision
        ${esc(projection.sourceRevision)}, using mapping version
        ${esc(projection.mappingVersion)}. Nothing here is authored: edit the ArchiMate
        model and project again.</p>
    </div>
    <section class="psec"><h3>Structure</h3>
      ${roots ? `<ul class="c4-tree">${roots}</ul>` : `<p class="muted">Nothing in this model projects into C4.</p>`}
    </section>
    <section class="psec"><h3>Relationships</h3>
      ${relationships ? `<ul class="c4-list">${relationships}</ul>` : `<p class="muted">None.</p>`}
    </section>
    <section class="psec c4-loss"><h3>Not projected (${projection.dropped.length})</h3>
      ${dropped
        ? `<p class="muted">C4 cannot express these. They are listed rather than
             dropped quietly — a projection that hid them would look complete and be
             wrong.</p><ul class="c4-list">${dropped}</ul>`
        : `<p class="muted">Everything in this model projects into C4.</p>`}
    </section>
  </div>`;
}

// markCanvas draws each element's worst finding onto the rendered diagram.
//
// It works on the DOM the renderer produced rather than inside the renderer,
// which is a deliberate choice and not a shortcut. The ArchiMate canvas is a
// pre-built bundle Atlas ships (ADR-0012: no build step, no CDN, no Node
// toolchain at run time), so a change that needed a rebuild would put a
// toolchain between an operator and a bug fix. Decorating afterwards keeps the
// bundle byte-identical and its checksum meaningful.
//
// The mark is a border *around* the element and a badge beside it. Nothing here
// writes fill: an ArchiMate layer colour says what kind of element it is, and
// overwriting it with health would destroy one meaning to show another
// (ADR-0189 §6).
function markCanvas(canvas, view, observations) {
  canvas.querySelectorAll(".panorama-canvas-mark").forEach((mark) => mark.remove());
  canvas.querySelectorAll(".djs-element").forEach((group) => {
    group.classList.remove("panorama-marked-ok", "panorama-marked-attention", "panorama-marked-critical");
  });
  if (!observations) return 0;

  const worst = worstByElement(observations);
  let marked = 0;
  for (const shape of view.shapes || []) {
    const finding = worst.get(shape.elementRef);
    const mark = finding && CANVAS_MARK[finding.severity];
    if (!mark) continue;
    // CSS.escape: a view node id comes from somebody's exchange document, so it
    // may hold anything a selector would otherwise read as syntax.
    const group = canvas.querySelector(`.djs-element[data-element-id="${CSS.escape(shape.id)}"]`);
    if (!group) continue;

    group.classList.add(`panorama-marked-${finding.severity}`);
    // The finding joins the element's accessible name rather than replacing it: a
    // screen reader has to hear what the element *is* before what it is doing.
    const spoken = group.getAttribute("aria-label") || shape.semantic?.name || shape.elementRef;
    group.setAttribute("aria-label",
      `${spoken} — ${mark.label.split(" — ")[0]}${finding.reason ? ": " + finding.reason : ""}`);

    const svg = document.createElementNS("http://www.w3.org/2000/svg", "g");
    svg.setAttribute("class", `panorama-canvas-mark panorama-mark-${finding.severity}`);
    svg.innerHTML = `<rect class="panorama-mark-border" x="-4" y="-4"
        width="${shape.width + 8}" height="${shape.height + 8}" rx="4"/>
      <circle class="panorama-mark-dot" cx="${shape.width - 6}" cy="6" r="${mark.glyph ? 8 : 4}"/>
      ${mark.glyph ? `<text class="panorama-mark-glyph" x="${shape.width - 6}" y="6" dy="3.5"
        text-anchor="middle">${esc(mark.glyph)}</text>` : ""}`;
    group.appendChild(svg);
    marked++;
  }
  return marked;
}

// canvasLegendHTML says what the marks mean, in words.
//
// ADR-0189 §6 asks for an accessible text legend in the same breath as the badges,
// and the reason is that a mark nobody can decode is decoration. It lists only the
// classes actually on the diagram — a legend describing findings the picture does
// not contain is a legend nobody reads twice — and states what an *unmarked*
// element means, which is the half a legend usually leaves out.
function canvasLegendHTML(observations, marked) {
  if (!observations) return "";
  const present = new Set();
  for (const finding of worstByElement(observations).values()) {
    if (CANVAS_MARK[finding.severity]) present.add(finding.severity);
  }
  if (!present.size) {
    return `<div class="panorama-live-legend"><span class="muted">Nothing on this view is
      observed: no element here binds a resource this server can see.</span></div>`;
  }
  const swatches = ["critical", "attention", "ok"].filter((key) => present.has(key)).map((key) => `
    <span class="panorama-live-swatch panorama-mark-${key}">
      <svg width="16" height="16" aria-hidden="true">
        <circle cx="8" cy="8" r="${CANVAS_MARK[key].glyph ? 7 : 4}" class="panorama-mark-dot"/>
        ${CANVAS_MARK[key].glyph ? `<text x="8" y="8" dy="3.5" text-anchor="middle"
          class="panorama-mark-glyph">${esc(CANVAS_MARK[key].glyph)}</text>` : ""}
      </svg>${esc(CANVAS_MARK[key].label)}</span>`).join("");
  const unwatched = (observations.unavailable || [])
    .map((u) => esc(STATE_TEXT[u.state] || u.state));
  return `<div class="panorama-live-legend">
    ${swatches}
    <span class="muted">${marked} of ${(view0Count(observations))} bound element(s) marked;
    an unmarked element binds nothing this server observes.</span>
    ${unwatched.length ? `<span class="muted">Not watched here: ${unwatched.join(", ")}.</span>` : ""}
  </div>`;
}

// view0Count is how many distinct elements the document has an observation for. It
// is the denominator the legend needs so "3 marked" is a proportion rather than a
// number floating on its own.
function view0Count(observations) {
  return worstByElement(observations).size;
}

export async function mountPanoramaViewer(container, { api, toast, id }) {
  container.innerHTML = `<div class="card empty"><p class="muted">Loading architecture view…</p></div>`;
  const [vendor, model, xml, applications, bindings, observations, subset] = await Promise.all([
    loadVendor(),
    api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}`),
    api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/xml`),
    api("GET", "/api/v1/applications").catch(() => []),
    // Bindings are additive: a model whose bindings cannot be read is still a
    // model worth opening, so this failure degrades the panel rather than the view.
    api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/bindings`).catch(() => null),
    // Observations are additive in exactly the same way, and on a server that
    // observes nothing this route refuses outright — a model is still worth
    // opening either way, so the panel loses its Live section rather than the
    // view losing the diagram.
    api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/observations`).catch(() => null),
    // What may be authored. It is a property of the build rather than of this
    // model, and the palette and connect menu are both built from it — so the
    // canvas can only ever offer what the write path accepts.
    api("GET", "/api/v1/panorama/subset").catch(() => null),
  ]);
  let parsed = vendor.parseOpenExchange(xml);
  const application = applications.find((item) => item.id === model.applicationId);

  container.innerHTML = `<div class="editor live panorama-editor">
    <div class="editor-bar">
      <nav class="crumbs" aria-label="Breadcrumb">
        <a href="#/panorama">Models</a><span class="crumb-sep">/</span>
        <span class="crumb-current">${esc(model.name)}</span>
      </nav>
      <div class="panorama-view-tabs" role="tablist" aria-label="Architecture views">
        ${parsed.views.map((item, index) => `<button class="panorama-view-tab${index === 0 ? " active" : ""}" role="tab" aria-selected="${index === 0}" data-view="${esc(item.id)}">${esc(item.name)}</button>`).join("")}
      </div>
      <span class="spacer"></span>
      <span class="panorama-status"></span>
      <button class="btn ghost small" data-tool="c4" aria-pressed="false">C4 projection</button>
      <a class="btn ghost small" href="/api/v1/panorama/models/${encodeURIComponent(id)}/xml">Export XML</a>
    </div>
    <div class="editor-body">
      <div class="panorama-palette" aria-label="Add an element"></div>
      <div class="panorama-stage">
        <div class="panorama-tools" aria-label="Canvas controls">
          <button class="icon-btn" data-tool="zoom-in" title="Zoom in" aria-label="Zoom in">+</button>
          <button class="icon-btn" data-tool="zoom-out" title="Zoom out" aria-label="Zoom out">−</button>
          <button class="icon-btn" data-tool="fit" title="Fit diagram" aria-label="Fit diagram">⊡</button>
          <span class="panorama-tool-sep" aria-hidden="true"></span>
          <button class="icon-btn" data-tool="undo" title="Undo" aria-label="Undo" disabled>↺</button>
          <button class="icon-btn" data-tool="redo" title="Redo" aria-label="Redo" disabled>↻</button>
          <button class="btn small" data-tool="save" disabled>Save layout</button>
        </div>
        <div class="panorama-canvas" role="tabpanel" aria-label="ArchiMate diagram"></div>
        <div class="panorama-live-legend-slot" aria-label="What the runtime marks mean"></div>
      </div>
      <aside class="props panorama-properties" aria-label="Properties">${propertiesHTML(null)}</aside>
    </div>
    <div class="problems panorama-problems" tabindex="0">
      <span>Problems</span><span class="badge">${parsed.problems.length}</span>
      <span class="prob-summary">${parsed.problems.length ? `${parsed.problems.length} import warning${parsed.problems.length === 1 ? "" : "s"}` : "Open Exchange view loaded"}</span>
      <span class="spacer"></span><span>ArchiMate 3.2 · revision ${esc(model.revision)}${application ? ` · ${esc(application.name)}` : ""}</span>
    </div>
  </div>`;

  const canvas = container.querySelector(".panorama-canvas");
  const legend = container.querySelector(".panorama-live-legend-slot");
  const properties = container.querySelector(".panorama-properties");
  // The application's role travels with the listing; editing a binding is authoring
  // the model, so it needs the same rights as any other write to it.
  const canEdit = ["owner", "editor"].includes(application?.myRole);
  let resolution = bindings;
  let live = observations;
  // The journal is read after the observations, not alongside them: reading the
  // observations is what records a transition, so a history fetched in the same
  // batch would always be one read behind what the panel is about to show.
  // Additive like the rest — a model is worth opening without it.
  const drift = await api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/drift`)
    .catch(() => null);
  let revision = model.revision;
  const status = container.querySelector(".panorama-status");
  const undoButton = container.querySelector('[data-tool="undo"]');
  const redoButton = container.querySelector('[data-tool="redo"]');
  const saveButton = container.querySelector('[data-tool="save"]');

  // paintEditState is what the toolbar says about unsaved work. It reads the
  // canvas rather than counting drags, because dragging a box away and back is not
  // a change and a counter would call it one — and then save a revision that moved
  // nothing, conflicting every other open editor for it.
  function paintEditState() {
    if (!canEdit) {
      status.innerHTML = `<span class="panorama-lock" aria-hidden="true">▣</span> Read only`;
      return;
    }
    const pending = viewer ? viewer.moved().length : 0;
    undoButton.disabled = !viewer?.canUndo();
    redoButton.disabled = !viewer?.canRedo();
    saveButton.disabled = pending === 0;
    status.textContent = pending
      ? `${pending} shape${pending === 1 ? "" : "s"} moved`
      : "No unsaved changes";
    status.classList.toggle("panorama-dirty", pending > 0);
  }

  // Leaving with work in progress is the one thing the canvas cannot undo for
  // somebody, so it is the one thing worth interrupting a navigation for.
  const guard = (event) => {
    if (canEdit && viewer && viewer.moved().length) event.preventDefault();
  };
  window.addEventListener("beforeunload", guard);
  let selected = null;

  // elementsOnView is what the connect menu may offer to draw to: the elements this
  // view actually shows. A relationship to something that is not on the view would
  // be a line from nowhere, which the server refuses — so it is not offered.
  const elementsOnView = () => {
    const view = currentView();
    if (!view) return [];
    return view.shapes.map((shape) => shape.semantic).filter(Boolean);
  };

  // currentView is the view the tabs say is open, which is what a save has to
  // re-render after re-reading the document.
  const currentView = () =>
    parsed.views.find((v) => v.id === container.querySelector(".panorama-view-tab.active")?.dataset.view)
    || parsed.views[0];

  // Context is per element and fetched on demand, so it is cleared whenever the
  // selection moves: showing one element's history under another's name would be
  // the worst thing this panel could do.
  let ctx = null;
  let ctxLoading = false;
  const paintProperties = () => {
    properties.innerHTML = propertiesHTML(selected, resolution, canEdit, live, drift, ctx, ctxLoading, subset, elementsOnView());
  };

  // The lookup is a button rather than an automatic fetch: every bound value costs
  // a query against a system that did not agree to be browsed.
  // Drawing a relationship from the panel. The menu already offered only what the
  // subset permits, so a refusal here means the document moved underneath — which
  // is worth showing rather than swallowing.
  properties.addEventListener("click", async (event) => {
    const draw = event.target.closest("[data-connect]");
    if (!draw || !selected) return;
    const target = draw.dataset.connect;
    const choice = properties.querySelector(`[data-connect-to="${CSS.escape(target)}"]`);
    if (!choice) return;
    try {
      const made = await api("POST",
        `/api/v1/panorama/models/${encodeURIComponent(id)}/relationships`,
        {
          expectedRevision: revision, type: choice.value,
          source: selected.id, target, viewId: currentView()?.id,
        });
      revision = made.revision;
      await reload();
      toast(`${choice.value} drawn.`);
    } catch (e) {
      toast(e.message);
    }
  });

  properties.addEventListener("click", async (event) => {
    if (!event.target.closest('[data-tool="context"]') || !selected) return;
    const forElement = selected.id;
    ctxLoading = true;
    paintProperties();
    const answer = await api("GET",
      `/api/v1/panorama/models/${encodeURIComponent(id)}/context?element=${encodeURIComponent(forElement)}`)
      .catch(() => ({ failed: true }));
    // The selection may have moved while the stores were being asked. A late answer
    // is dropped rather than painted under whatever is selected now.
    if (!selected || selected.id !== forElement) return;
    ctxLoading = false;
    ctx = answer;
    paintProperties();
  });

  let viewer = null;
  const select = (diagramView) => {
    if (!diagramView) {
      canvas.innerHTML = `<div class="panorama-no-view"><div>◇</div><h2>No diagram views</h2><p>This model contains reusable ArchiMate elements, but no Diagram view yet.</p></div>`;
      return;
    }
    if (!viewer) {
      viewer = new vendor.Viewer(canvas, (item) => {
        selected = item;
        ctx = null;
        ctxLoading = false;
        paintProperties();
      }, { editable: canEdit, onChange: paintEditState });
    }
    viewer.render(diagramView);
    // The renderer adds its shapes synchronously, so the DOM is there to decorate
    // the moment render returns. Re-marking on every view switch is not an
    // optimisation to skip: each render rebuilds the canvas, and a mark left over
    // from the previous view would sit on whatever element inherited its id.
    const marked = markCanvas(canvas, diagramView, live);
    legend.innerHTML = canvasLegendHTML(live, marked);
  };
  select(parsed.views[0]);
  paintEditState();

  container.querySelectorAll(".panorama-view-tab").forEach((button) => button.addEventListener("click", () => {
    container.querySelectorAll(".panorama-view-tab").forEach((tab) => {
      const active = tab === button;
      tab.classList.toggle("active", active);
      tab.setAttribute("aria-selected", String(active));
    });
    selected = null;
    paintProperties();
    select(parsed.views.find((item) => item.id === button.dataset.view));
    paintEditState();
  }));
  // Binding edits are delegated from the panel, which is re-rendered on every
  // selection: a listener bound to a button would not survive the next repaint.
  properties.addEventListener("click", async (event) => {
    const button = event.target.closest("[data-bind-edit]");
    if (!button || !selected) return;
    const key = button.getAttribute("data-bind-edit");
    let list;
    try {
      list = await api("GET",
        `/api/v1/panorama/models/${encodeURIComponent(id)}/bindings/candidates?key=${encodeURIComponent(key)}`);
    } catch (e) {
      toast(e.message);
      return;
    }
    if (!list.supported) {
      toast("This Atlas version cannot resolve that kind of binding yet.");
      return;
    }
    if (!list.candidates.length) {
      toast("You have access to no resource of that kind to bind.");
      return;
    }
    const current = (resolution?.bindings || [])
      .find((b) => b.elementId === selected.id && b.key === key);
    const chosen = await pickBinding(list, current, key);
    if (chosen === null) return; // cancelled

    try {
      const updated = await api("PUT", `/api/v1/panorama/models/${encodeURIComponent(id)}/bindings`, {
        expectedRevision: revision, elementId: selected.id, key, values: chosen,
      });
      revision = updated.revision;
      // Re-read rather than patching locally: the server resolves names, and a
      // panel that invented them would be showing something the model does not say.
      resolution = await api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/bindings`);
      paintProperties();
      toast(chosen.length ? "Binding saved." : "Binding cleared.");
    } catch (e) {
      toast(e.message);
    }
  });

  const c4Button = container.querySelector('[data-tool="c4"]');
  let c4Open = false;
  c4Button.addEventListener("click", async () => {
    if (c4Open) {
      c4Open = false;
      c4Button.setAttribute("aria-pressed", "false");
      canvas.querySelector(".c4-panel")?.remove();
      select(currentView());
      return;
    }
    let projection;
    try {
      projection = await api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/c4`);
    } catch (e) {
      toast(e.message);
      return;
    }
    viewer?.destroy();
    viewer = null;
    c4Open = true;
    c4Button.setAttribute("aria-pressed", "true");
    canvas.innerHTML = c4PanelHTML(projection);
  });

  container.querySelector('[data-tool="zoom-in"]').addEventListener("click", () => viewer?.zoom(1.2));
  container.querySelector('[data-tool="zoom-out"]').addEventListener("click", () => viewer?.zoom(1 / 1.2));
  container.querySelector('[data-tool="fit"]').addEventListener("click", () => viewer?.fit());
  // reload re-reads the document after the server has changed it. The canvas never
  // creates content itself, so this is how what was written becomes what is drawn —
  // and it is why there can never be a shape on screen the document does not have.
  async function reload() {
    const fresh = await api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/xml`);
    parsed = vendor.parseOpenExchange(fresh);
    select(currentView());
    paintProperties();
    paintEditState();
  }

  // The palette writes through the server and re-reads. A new element lands at a
  // fixed spot on the canvas rather than where a pointer was: this is a click, not
  // a drag, and pretending otherwise would put boxes where nobody aimed.
  const palette = container.querySelector(".panorama-palette");
  palette.innerHTML = paletteHTML(subset, canEdit);
  palette.addEventListener("click", async (event) => {
    const button = event.target.closest("[data-add-type]");
    if (!button) return;
    const type = button.dataset.addType;
    const name = window.prompt(`Name for the new ${type}`, type);
    if (name === null) return;
    try {
      const made = await api("POST", `/api/v1/panorama/models/${encodeURIComponent(id)}/elements`,
        {
          expectedRevision: revision, type, name,
          viewId: currentView()?.id, x: 60, y: 60, w: 170, h: 70,
        });
      revision = made.revision;
      await reload();
      toast(`${type} added.`);
    } catch (e) {
      toast(e.message);
    }
  });

  undoButton.addEventListener("click", () => { viewer?.undo(); paintEditState(); });
  redoButton.addEventListener("click", () => { viewer?.redo(); paintEditState(); });

  // Saving sends the shapes that moved, not the document. The canvas has a parsed
  // copy and could serialise it, but a browser's XMLSerializer normalises — and
  // ADR-0189 §2 requires that nothing outside the edit changes. The server splices
  // four numbers per shape instead, so a comment somebody left in their model
  // survives being nudged.
  saveButton.addEventListener("click", async () => {
    const changes = viewer?.moved() || [];
    if (!changes.length) return;
    saveButton.disabled = true;
    try {
      const updated = await api("PUT", `/api/v1/panorama/models/${encodeURIComponent(id)}/layout`,
        { expectedRevision: revision, changes });
      revision = updated.revision;
      // Re-read the view so the canvas's baseline is the document again. Without it
      // the next save would resend shapes that are already stored, and a second
      // editor's conflict would be reported against work that had landed.
      const xml = await api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/xml`);
      parsed = vendor.parseOpenExchange(xml);
      select(currentView());
      toast(`Layout saved (revision ${revision}).`);
    } catch (e) {
      toast(e.message);
    }
    paintEditState();
  });

  window.__atlasCleanup = () => {
    viewer?.destroy();
    viewer = null;
    window.__atlasCleanup = null;
  };
}
