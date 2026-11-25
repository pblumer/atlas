// Which version of a decision is deployed, said in the decision editor's bar the way
// the BPMN editor says it of a process (ADR-0452, shown-version.js).
//
// A decision is versioned on its own (ADR-0319): every deploy of a model gives each
// decision in it the next version under its published name, and a form field or a
// latest-bound business rule task evaluates the newest. The editor's bar used to name
// only that newest version and its key. That left three questions without an answer
// on the page where they are asked: which versions exist, who deployed each and when,
// and whether the newest even came from the model on screen — two models that publish
// a decision under the same name share its version line, so a deploy of either
// replaces what the other's users evaluate.
//
// The chip still says "Deployed vN · key K". Its panel lists every version, newest
// first, with when, who and the model it was deployed from, marks the newest, says
// how many deployed processes still evaluate each version, and warns when the newest
// came from another model than this one.
//
// decisionStanding is a pure function of what the server answered, so a browser test
// drives it without an editor around it — the split shown-version.js makes.

import { fmtDateTime } from "./datefmt.js";
import { t } from "./i18n.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// stamp is "when · who", or what to say when neither is known.
function stamp(at, name, by) {
  const parts = [at ? fmtDateTime(at * 1000) : "", name || by || ""].filter(Boolean);
  return parts.length ? parts.join(" · ") : t("shown.notRecorded");
}

// modelOf names the model a deployment came from: its name and the file it is stored
// as, whichever of the two the record carries.
function modelOf(r) {
  const parts = [r.modelName, r.resourceName].filter(Boolean);
  return parts.length ? parts.join(" · ") : t("shown.notRecorded");
}

// fromElsewhere says a deployment came from another model than the one on screen.
// Only a record and an editor that both know their model handle can say so; a
// decision deployed from an unsaved diagram, or opened as a draft, is not suspected.
function fromElsewhere(r, modelRef) {
  return !!(modelRef && r.modelRef && r.modelRef !== modelRef);
}

// decisionStanding is the chip and its panel for the decision named name, from the
// rows GET /api/v1/decision-deployments?decisionId= answered for it. modelRef is the
// handle of the model the editor has open, "" when it has none. Nothing deployed is
// null: the chip is left out rather than saying "v0".
export function decisionStanding(rows, { name, modelRef = "" } = {}) {
  const versions = (rows || []).filter((r) => r && r.decisionId === name)
    .slice().sort((a, b) => b.version - a.version);
  if (!versions.length) return null;
  const newest = versions.find((r) => r.current) || versions[0];
  const v = `v${newest.version}`;
  const foreign = fromElsewhere(newest, modelRef);
  const details = [
    [t("dmnv.row.decision"), name],
    [t("dmnv.row.newest"), `${v} · ${stamp(newest.deployedAt, newest.deployedByName, newest.deployedBy)}`],
    [t("dmnv.row.key"), String(newest.key)],
    [t("dmnv.row.model"), modelOf(newest)],
  ];
  const items = versions.map((r) => {
    const pins = Array.isArray(r.pinnedBy) ? r.pinnedBy : [];
    return {
      label: `v${r.version}`,
      // The file alone: the row is narrow, and the model's full name is in the title.
      sub: [stamp(r.deployedAt, r.deployedByName, r.deployedBy), r.resourceName || r.modelName].filter(Boolean).join(" · "),
      title: t("dmnv.itemTitle", { v: `v${r.version}`, key: r.key, model: modelOf(r) }),
      newest: r === newest,
      foreign: fromElsewhere(r, modelRef),
      pinned: pins.length,
      pinnedTitle: pins.map((p) => `${p.name || p.processId} v${p.version}`).join(", "),
    };
  });
  return {
    label: t("dmn.deployed", { version: newest.version, key: newest.key }),
    tone: foreign ? "warn" : "ok",
    title: t("dmn.deployedTitle", { id: name, version: newest.version, key: newest.key }),
    rows: details,
    note: foreign ? t("dmnv.foreignNote", { v, model: newest.resourceName || newest.modelRef }) : "",
    items,
  };
}

// standingHTML renders a standing into the chip and its panel, in the markup and the
// classes the BPMN editor's chip uses, so the two read as one thing.
export function standingHTML(s) {
  const rows = s.rows.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(v)}</dd>`).join("");
  const note = s.note ? `<p class="sv-note warn" id="dmnv-note">${esc(s.note)}</p>` : "";
  const tag = (text, cls, title) =>
    `<span class="pill sv-tag${cls ? " " + cls : ""}"${title ? ` title="${esc(title)}"` : ""}>${esc(text)}</span>`;
  const items = s.items.map((x) => {
    const tags = [
      x.newest ? tag(t("shown.tag.newest"), "ok") : "",
      x.foreign ? tag(t("dmnv.tag.foreign"), "warn") : "",
      x.pinned ? tag(t("dmnv.tag.pinned", { count: x.pinned }), "", x.pinnedTitle) : "",
    ].join("");
    return `<li><div class="sv-v${x.newest ? " current" : ""}" title="${esc(x.title)}">
      <span class="sv-vl">${esc(x.label)}</span><span class="sv-vs">${esc(x.sub)}</span><span class="sv-tags">${tags}</span></div></li>`;
  }).join("");
  return `<button type="button" class="pill sv-chip ${s.tone}" id="dmn-deployed-chip"
      aria-haspopup="true" aria-expanded="false" aria-controls="dmnv-pop" title="${esc(s.title)}">${esc(s.label)}</button>
    <div class="sv-pop" id="dmnv-pop" role="dialog" aria-label="${esc(t("dmnv.popLabel"))}" hidden>
      <dl class="sv-rows">${rows}</dl>${note}
      <p class="sv-head">${esc(t("shown.versionsHead"))}</p><ol class="sv-vlist" id="dmnv-list">${items}</ol>
      <p class="sv-note muted">${esc(t("dmnv.latestNote"))}</p>
    </div>`;
}

// attachDecisionVersions mounts the chip into host. show(standing) replaces what it
// says — null hides it — and keeps the panel open if it was. The panel opens on the
// chip and closes on Escape or a click anywhere outside it.
export function attachDecisionVersions(host) {
  const setOpen = (open) => {
    const chip = host.querySelector("#dmn-deployed-chip");
    const pop = host.querySelector("#dmnv-pop");
    if (!chip || !pop) return;
    pop.hidden = !open;
    chip.setAttribute("aria-expanded", open ? "true" : "false");
  };
  const onHostClick = (e) => {
    if (!(e.target.closest && e.target.closest("#dmn-deployed-chip"))) return;
    const pop = host.querySelector("#dmnv-pop");
    setOpen(pop ? pop.hidden : false);
  };
  const onDocClick = (e) => { if (!host.contains(e.target)) setOpen(false); };
  const onKey = (e) => {
    if (e.key !== "Escape") return;
    const pop = host.querySelector("#dmnv-pop");
    if (!pop || pop.hidden) return;
    setOpen(false);
    const chip = host.querySelector("#dmn-deployed-chip");
    if (chip) chip.focus();
  };
  host.addEventListener("click", onHostClick);
  host.addEventListener("keydown", onKey);
  document.addEventListener("click", onDocClick, true);
  return {
    show(s) {
      const wasOpen = !!host.querySelector("#dmnv-pop:not([hidden])");
      host.innerHTML = s ? standingHTML(s) : "";
      host.hidden = !s;
      if (s && wasOpen) setOpen(true);
    },
    destroy() {
      host.removeEventListener("click", onHostClick);
      host.removeEventListener("keydown", onKey);
      document.removeEventListener("click", onDocClick, true);
    },
  };
}
