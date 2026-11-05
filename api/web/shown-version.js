// What the Modeler has open, said in its bar (ADR-0452).
//
// A draft and a deployed version are two records that share a process id and nothing
// else: Save writes the draft, Deploy mints a version, and neither touches the other.
// An application opens the draft; Operations and the Modeler home open a version. So
// two diagrams of "the same process" can be two different models, and a change can be
// visible in one and gone from the other with nothing on screen to say which is which.
// That is what was reported: a deployed change missing from the diagram an application
// opened, and no way to tell whether the Modeler had picked up an old version.
//
// The chip says which record this is — the draft, or version N of M — and how it
// stands against the other: the same model, changed since, or (the case that loses
// work) a newer deployed version that differs from this draft. Its panel names the
// people: who created the draft and who last saved it; who deployed the version and who
// adjusted its layout. The server compares the two models (GET
// /api/v1/drafts/{id}/comparison), because only it can say "same model" without a
// formatting difference reading as a change.
//
// The panel also opens the full list of differences — every element the draft adds,
// removes or changes against the version, which the comparison now carries — and, on
// a version, the comparison with any other version of the process
// (ADR-0530). A standing names those as actions; the chip
// opens them.
//
// The chip also walks the process's records: an arrow on either side of it opens the
// version before or after the one on screen, and the panel lists every version with
// the draft above them. A version on screen offers "Restore as draft", which makes it
// the draft again — after saying what of the draft that loses — and then opens the
// draft, so the next Deploy runs it as a new version
// (ADR-0533).
//
// draftStanding, versionStanding, versionSteps and restoreModel are pure functions of
// what the server answered, so a browser test drives them without an editor around
// them — the split frozen-decisions.js already makes.

import { fmtDateTime } from "./datefmt.js";
import { t } from "./i18n.js";
import { compareVersionsFlow, countsText, showChangesDialog, versionLabel } from "./version-comparison.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const when = (sec) => (sec ? fmtDateTime(sec * 1000) : "");

// stamp is "when · who", or what to say when neither is known. A draft saved before
// "last saved by" was recorded, or on a server without accounts, has a time and no
// name; that is said as it is rather than filled in.
function stamp(at, name) {
  const parts = [when(at), name].filter(Boolean);
  return parts.length ? parts.join(" · ") : t("shown.notRecorded");
}

// draftStanding is the chip for a session that addresses a draft. cmp is the server's
// comparison of the draft with the newest deployed version, or null when it could not
// be read — then the chip still says "Draft", and only that.
//
// versions are the deployed versions of the draft's process, for the arrows and the
// list; none when there are none, or none could be read.
export function draftStanding(cmp, versions) {
  if (!cmp) {
    return {
      label: t("shown.draft"), tone: "", title: t("shown.draftTitle"),
      rows: [[t("shown.row.showing"), t("shown.showingDraft")]],
      note: t("shown.noCompare"), links: [], actions: [], steps: [], at: -1,
    };
  }
  const { steps, at } = versionSteps(versions, { key: null, processId: cmp.processId, draft: cmp, cmp });
  const rows = [
    [t("shown.row.showing"), t("shown.showingDraft")],
    [t("shown.row.createdBy"), cmp.ownerName || t("shown.notRecorded")],
    [t("shown.row.lastSaved"), stamp(cmp.savedAt, cmp.savedByName)],
  ];
  if (!cmp.deployed) {
    rows.push([t("shown.row.deployed"), t("shown.noVersion")]);
    return {
      label: t("shown.notDeployed"), tone: "off",
      title: t("shown.notDeployedTitle"),
      rows, note: "", links: [], actions: [], steps, at,
    };
  }
  const v = `v${cmp.version}`;
  rows.push([t("shown.row.newest"), t("shown.newestRow", { v, stamp: stamp(cmp.deployedAt, cmp.deployedByName) })]);
  const links = [{ href: `#/modeler/d/${encodeURIComponent(cmp.key)}`, text: t("shown.open", { v }) }];
  if (cmp.sameModel) {
    return {
      label: t("shown.same", { v }), tone: "ok",
      title: t("shown.sameTitle", { v }),
      rows, links, actions: [], steps, at,
      note: t("shown.sameNote", { v }),
    };
  }
  const diff = cmp.difference ? t("shown.firstDiff", { difference: cmp.difference }) : "";
  const actions = changesAction(cmp, v);
  // Deployed after the draft's last save and not the same model: the version holds
  // something this draft does not. That is the case that loses work — editing this
  // draft and publishing its application deploys without it — so it is the warning.
  // An equal second counts as after: missing the warning costs more than showing it.
  if (cmp.deployedAt >= cmp.savedAt) {
    return {
      label: t("shown.behind", { v }), tone: "warn",
      title: t("shown.behindTitle", { v }),
      rows, links, actions, steps, at,
      note: t("shown.behindNote", { v, diff }),
    };
  }
  return {
    label: t("shown.ahead", { v }), tone: "",
    title: t("shown.aheadTitle", { v }),
    rows, links, actions, steps, at,
    note: t("shown.aheadNote", { diff }),
  };
}

// changesAction offers the full list of what the draft changes against version v,
// when the comparison carries one — a server from before the list does not.
function changesAction(cmp, v) {
  if (!cmp || cmp.sameModel || !Array.isArray(cmp.changes)) return [];
  return [{
    act: "changes", text: t("shown.showChanges"),
    title: t("shown.changesDraftTitle", { v }), intro: t("shown.changesDraftIntro", { v }), cmp,
  }];
}

// versionStanding is the chip for a session opened on a deployed version. here is its
// entry in GET /api/v1/processes, versions every entry of the same process id, and cmp
// the comparison of the process's draft with this version — null when there is no
// draft, or none this caller may read. draftUnread says the comparison failed for
// another reason, so "no draft" would be a guess. restorable says the editor can make
// this version the draft, which it offers unless the draft already is this model.
export function versionStanding(here, versions, cmp, draftUnread, restorable) {
  const newest = (versions || []).reduce((a, b) => (b.version > a.version ? b : a), here);
  const latest = newest.version === here.version;
  const v = `v${here.version}`;
  const rows = [
    [t("shown.row.showing"), t("shown.showingVersion", { version: here.version, newest: newest.version })],
    [t("shown.row.deployed"), stamp(here.deployedAt, here.deployedByName)],
  ];
  if (here.diagramUpdatedAt) {
    rows.push([t("shown.row.layout"), stamp(here.diagramUpdatedAt, here.diagramUpdatedByName || here.diagramUpdatedBy)]);
  }
  const links = [];
  if (!latest) {
    rows.push([t("shown.row.newest"), t("shown.newestRow", { v: `v${newest.version}`, stamp: stamp(newest.deployedAt, newest.deployedByName) })]);
    links.push({ href: `#/modeler/d/${encodeURIComponent(newest.key)}`, text: t("shown.open", { v: `v${newest.version}` }) });
  }
  let note = "";
  const actions = [];
  if (restorable && !(cmp && cmp.sameModel)) {
    actions.push({ act: "restore", primary: true, text: t("shown.restore", { v }), here });
  }
  if ((versions || []).length > 1) {
    actions.push({ act: "compare", text: t("shown.compareVersions"), here, versions });
  }
  if (cmp) {
    actions.push(...changesAction(cmp, v));
    const same = t(cmp.sameModel ? "shown.sameModel" : "shown.otherModel", { v });
    rows.push([t("shown.row.draft"), t("shown.draftSaved", { stamp: stamp(cmp.savedAt, cmp.savedByName), same })]);
    links.push({ href: `#/modeler/draft/${encodeURIComponent(here.processId)}`, text: t("shown.openDraft") });
    if (!cmp.sameModel) {
      note = t("shown.draftDiffers", {
        v, diff: cmp.difference ? t("shown.draftDiffersFirst", { difference: cmp.difference }) : "",
      });
    }
  } else if (draftUnread) {
    rows.push([t("shown.row.draft"), t("shown.draftUnread")]);
  } else {
    rows.push([t("shown.row.draft"), t("shown.noDraft")]);
  }
  const { steps, at } = versionSteps(versions && versions.length ? versions : [here],
    { key: here.key, processId: here.processId, draft: cmp, cmp });
  return {
    label: latest ? t("shown.newestLabel", { v }) : t("shown.ofLabel", { v, newest: newest.version }),
    tone: latest ? "ok" : "warn",
    title: t("shown.versionTitle", { version: here.version, newest: newest.version, stamp: stamp(here.deployedAt, here.deployedByName) }),
    rows, note, links, actions, steps, at,
  };
}

// versionSteps lays a process's records out in the order the arrows walk them: the
// oldest version first, the newest last, and the draft — when there is one this
// caller can read — after it, because the draft is where the next version comes
// from. key is the version on screen, null when the draft is; at is its index, -1
// when it is not among them. cmp is a comparison of the draft with one version: when
// it says "same model", that version is marked as what the draft holds.
export function versionSteps(versions, { key, processId, draft, cmp }) {
  const byOldest = (versions || []).slice().sort((a, b) => a.version - b.version);
  const newest = byOldest.length ? byOldest[byOldest.length - 1].version : 0;
  const sameKey = cmp && cmp.sameModel && cmp.key != null ? String(cmp.key) : "";
  const steps = byOldest.map((p) => ({
    href: `#/modeler/d/${encodeURIComponent(p.key)}`,
    label: versionLabel(p),
    short: `v${p.version}`,
    sub: stamp(p.deployedAt, p.deployedByName),
    newest: p.version === newest,
    holdsDraft: sameKey !== "" && String(p.key) === sameKey,
    draft: false,
    current: key != null && String(p.key) === String(key),
  }));
  if (draft && processId) {
    steps.push({
      href: `#/modeler/draft/${encodeURIComponent(processId)}`,
      label: t("shown.draft"), short: t("shown.draft"),
      sub: t("shown.savedStamp", { stamp: stamp(draft.savedAt, draft.savedByName) }),
      newest: false, holdsDraft: false, draft: true, current: key == null,
    });
  }
  return { steps, at: steps.findIndex((x) => x.current) };
}

// restoreModel is what the "Restore as draft" dialog says, from what the server
// answered: here is the version to restore; newest is the comparison of the draft
// with the newest version, null when there is no draft (or, with unread, when it
// could not be read); dirty says the canvas holds edits of its own, which go into the
// draft with it.
//
// The warning is about what the draft holds that no deployed version does, and the
// server can say that only against one version at a time — so it compares with the
// newest and says exactly that: the draft is not that model, and what it holds
// beyond it is lost. A draft keeps no history (ADR-0021), so that is the whole truth
// of it; the dialog warns, and the author decides.
export function restoreModel({ here, newest, unread, dirty }) {
  const v = `v${here.version}`;
  const rows = [[t("shown.restore.row.restoring"), `${versionLabel(here)} · ${t("shown.restore.deployed", { stamp: stamp(here.deployedAt, here.deployedByName) })}`]];
  const m = {
    title: t("shown.restore.title", { v }),
    intro: t("shown.restore.intro", { v }),
    confirm: t("shown.restore.ok", { v }),
    rows, warn: "", counts: "", changes: null, tone: "",
    dirty: dirty ? t("shown.restore.dirty") : "",
    after: t("shown.restore.after"),
  };
  if (unread) {
    rows.push([t("shown.restore.row.draft"), t("shown.draftUnread")]);
    m.warn = t("shown.restore.unread");
    m.tone = "warn";
    return m;
  }
  if (!newest) {
    rows.push([t("shown.restore.row.draft"), t("shown.restore.none")]);
    m.intro = t("shown.restore.introNew", { v });
    m.confirm = t("shown.restore.okNew", { v });
    return m;
  }
  const saved = stamp(newest.savedAt, newest.savedByName);
  if (!newest.deployed) {
    rows.push([t("shown.restore.row.draft"), t("shown.savedStamp", { stamp: saved })]);
    m.warn = t("shown.restore.warnUndeployed");
    m.tone = "warn";
    return m;
  }
  const nv = `v${newest.version}`;
  const same = t(newest.sameModel ? "shown.sameModel" : "shown.otherModel", { v: nv });
  rows.push([t("shown.restore.row.draft"), t("shown.draftSaved", { stamp: saved, same })]);
  if (!newest.sameModel) {
    m.warn = t("shown.restore.warn", {
      newest: nv,
      diff: newest.difference ? t("shown.firstDiff", { difference: newest.difference }) : "",
    });
    m.tone = "warn";
    if (newest.counts) m.counts = countsText(newest.counts);
    if (Array.isArray(newest.changes)) m.changes = newest;
  }
  return m;
}

// unsavedStanding is the chip for a diagram that has never been saved.
export function unsavedStanding() {
  return {
    label: t("shown.unsaved"), tone: "off", title: t("shown.unsavedTitle"),
    rows: [[t("shown.row.showing"), t("shown.unsavedRow")]], note: "", links: [], actions: [],
  };
}

// standingHTML renders a standing into the chip and its panel.
// The arrows and the list appear once there is somewhere to go: a draft that was
// never deployed, or a version that is all there is, shows the chip alone. The list
// comes after the actions, so Restore is in reach however many versions there are.
export function standingHTML(s) {
  const rows = s.rows.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(v)}</dd>`).join("");
  const note = s.note ? `<p class="sv-note${s.tone === "warn" ? " warn" : ""}">${esc(s.note)}</p>` : "";
  const actions = s.actions || [];
  const links = s.links.length || actions.length
    ? `<div class="sv-links">${actions.filter((a) => a.primary).map((a) => actionHTML(a, actions)).join("")}${
      s.links.map((l) => `<a class="btn neutral small" href="${esc(l.href)}" data-sv-go>${esc(l.text)}</a>`).join("")}${
      actions.filter((a) => !a.primary).map((a) => actionHTML(a, actions)).join("")}</div>`
    : "";
  const steps = s.steps || [];
  const walk = steps.length > 1 && s.at >= 0;
  const chip = `<button type="button" class="pill sv-chip${s.tone ? " " + s.tone : ""}" id="sv-chip"
      aria-haspopup="true" aria-expanded="false" aria-controls="sv-pop" title="${esc(s.title)}">${esc(s.label)}</button>`;
  return `${walk ? stepHTML(steps, s.at, -1) : ""}${chip}${walk ? stepHTML(steps, s.at, 1) : ""}
    <div class="sv-pop" id="sv-pop" role="dialog" aria-label="${esc(t("shown.popLabel"))}" hidden>
      <dl class="sv-rows">${rows}</dl>${note}${links}${walk ? listHTML(steps) : ""}
    </div>`;
}

function actionHTML(a, actions) {
  return `<button type="button" class="btn ${a.primary ? "" : "neutral "}small" data-sv-act="${actions.indexOf(a)}">${esc(a.text)}</button>`;
}

const CHEVRON = { "-1": "M15 18l-6-6 6-6", "1": "M9 18l6-6-6-6" };

// stepHTML is the arrow before (dir -1) or after (dir 1) the chip: a link to the
// record next in line, or a disabled button at either end, so the chip does not move
// when the arrow has nowhere to go.
function stepHTML(steps, at, dir) {
  const cls = `sv-step ${dir < 0 ? "sv-older" : "sv-newer"}`;
  const icon = `<svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true" fill="none" stroke="currentColor"
      stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="${CHEVRON[dir]}"/></svg>`;
  const to = steps[at + dir];
  if (!to) {
    const none = t(dir < 0 ? "shown.step.noOlder" : "shown.step.noNewer");
    return `<button type="button" class="${cls}" disabled aria-label="${esc(none)}" title="${esc(none)}">${icon}</button>`;
  }
  const label = to.draft ? t("shown.step.toDraft")
    : t(dir < 0 ? "shown.step.older" : "shown.step.newer", { v: to.short });
  return `<a class="${cls}" href="${esc(to.href)}" data-sv-go aria-label="${esc(label)}" title="${esc(label)}">${icon}</a>`;
}

// listHTML lists every record of the process, newest first and the draft on top: the
// way back to any version, not only to the one beside this.
function listHTML(steps) {
  const tag = (text, cls) => `<span class="pill sv-tag${cls ? " " + cls : ""}">${esc(text)}</span>`;
  const items = steps.slice().reverse().map((x) => {
    const tags = [
      x.newest ? tag(t("shown.tag.newest"), "ok") : "",
      x.holdsDraft ? tag(t("shown.tag.holdsDraft"), "") : "",
      x.current ? tag(t("shown.tag.showing"), "sv-here") : "",
    ].join("");
    return `<li><a class="sv-v${x.current ? " current" : ""}" href="${esc(x.href)}" data-sv-go${x.current ? ` aria-current="page"` : ""}>
      <span class="sv-vl">${esc(x.label)}</span><span class="sv-vs">${esc(x.sub)}</span><span class="sv-tags">${tags}</span></a></li>`;
  }).join("");
  return `<p class="sv-head">${esc(t("shown.versionsHead"))}</p><ol class="sv-vlist">${items}</ol>`;
}

// attachShownVersion mounts the chip into host and keeps it current. which() is read
// on every refresh, because what a session addresses changes under it: the first Save
// of a new diagram, or of a version opened read-only, makes it address a draft.
//
// Every read is best-effort. A listing that cannot be read leaves the chip out rather
// than saying something it does not know; the editor never depends on it.
//
// The editor hands in what moving between records needs: leave(href) opens another
// record once this diagram's work is safe (the same question every door out of the
// editor asks), restore() saves the version on screen as the draft and opens the
// draft, answering whether it did, and dirty() says whether the canvas holds edits of
// its own. Without them the arrows navigate directly and Restore is not offered.
export function attachShownVersion(host, { api, which, leave, restore, dirty }) {
  const go = leave || ((href) => { location.hash = href; });
  let seq = 0;
  let closed = false;
  let shown = null; // the standing on screen, whose actions the panel's buttons name
  const setOpen = (open) => {
    const chip = host.querySelector("#sv-chip");
    const pop = host.querySelector("#sv-pop");
    if (!chip || !pop) return;
    pop.hidden = !open;
    chip.setAttribute("aria-expanded", open ? "true" : "false");
  };
  // The panel is not a .dropdown-menu, so the Console's menu handler (app.js) leaves it
  // alone, and the chip's click is let through: it reaches that handler and the bar
  // menu's (editor.js wireBarMenu), which close their menus — only one panel stands at
  // a time. This one closes on a click anywhere outside it, read in the capture phase
  // because the bar menu's trigger stops its click before it would bubble up to here.
  const onHostClick = (e) => {
    // A link to another record walks through leave, so an unsaved diagram is kept or
    // given up knowingly. A click that asks for a new tab is the browser's.
    const link = e.target.closest && e.target.closest("a[data-sv-go]");
    if (link) {
      if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
      e.preventDefault();
      setOpen(false);
      go(link.getAttribute("href"));
      return;
    }
    const act = e.target.closest && e.target.closest("[data-sv-act]");
    if (act && shown) {
      const a = (shown.actions || [])[Number(act.dataset.svAct)];
      if (a) {
        setOpen(false);
        runAction(a);
      }
      return;
    }
    if (!(e.target.closest && e.target.closest("#sv-chip"))) return;
    const pop = host.querySelector("#sv-pop");
    setOpen(pop ? pop.hidden : false);
  };
  const onDocClick = (e) => { if (!host.contains(e.target)) setOpen(false); };
  const onKey = (e) => {
    if (e.key !== "Escape") return;
    const pop = host.querySelector("#sv-pop");
    if (!pop || pop.hidden) return;
    setOpen(false);
    const chip = host.querySelector("#sv-chip");
    if (chip) chip.focus();
  };
  host.addEventListener("click", onHostClick);
  host.addEventListener("keydown", onKey);
  document.addEventListener("click", onDocClick, true);

  const runAction = (a) => {
    if (a.act === "changes") {
      showChangesDialog({ title: a.title, intro: a.intro, cmp: a.cmp });
    } else if (a.act === "compare") {
      compareVersionsFlow({
        api, processId: a.here.processId, processName: a.here.name || a.here.processId,
        versions: a.versions, toKey: a.here.key,
      });
    } else if (a.act === "restore" && restore) {
      restoreFlow({ api, here: a.here, restore, dirty });
    }
  };

  const read = async (method, url) => {
    try { return await api(method, url); } catch { return null; }
  };

  const standingNow = async () => {
    const { key, draftId } = which();
    if (draftId != null) {
      const got = await read("GET", `/api/v1/drafts/${encodeURIComponent(draftId)}/comparison`);
      const cmp = got && typeof got === "object" && got.processId ? got : null;
      // The versions are read only when there are some: a draft never deployed has
      // nowhere to step to, and the listing is the whole server's.
      let versions = [];
      if (cmp && cmp.deployed) {
        const procs = await read("GET", "/api/v1/processes");
        if (Array.isArray(procs)) versions = procs.filter((p) => p.processId === cmp.processId);
      }
      return draftStanding(cmp, versions);
    }
    if (key == null) return unsavedStanding();
    const procs = await read("GET", "/api/v1/processes");
    if (!Array.isArray(procs)) return null;
    const here = procs.find((p) => String(p.key) === String(key));
    if (!here) return null;
    const versions = procs.filter((p) => p.processId === here.processId);
    // A 404 is the answer "no draft" (or none this caller may see); anything else is
    // a failure to read one, and is said as such.
    let cmp = null, unread = false;
    try {
      cmp = await api("GET",
        `/api/v1/drafts/${encodeURIComponent(here.processId)}/comparison?key=${encodeURIComponent(here.key)}`);
    } catch (e) {
      unread = !(e && e.status === 404);
    }
    return versionStanding(here, versions, cmp && typeof cmp === "object" && cmp.processId ? cmp : null, unread, !!restore);
  };

  const refresh = async () => {
    const mine = ++seq;
    const s = await standingNow();
    if (closed || mine !== seq) return; // superseded, or the editor is gone
    const wasOpen = !!host.querySelector("#sv-pop:not([hidden])");
    shown = s;
    host.innerHTML = s ? standingHTML(s) : "";
    host.hidden = !s;
    if (wasOpen) setOpen(true);
  };

  return {
    refresh,
    destroy() {
      closed = true;
      host.removeEventListener("click", onHostClick);
      host.removeEventListener("keydown", onKey);
      document.removeEventListener("click", onDocClick, true);
    },
  };
}

// restoreFlow asks before a version replaces the draft. It reads the draft against the
// newest version first — the comparison the chip holds is against the version on
// screen, which says nothing about what the draft holds that is not deployed — and then
// says what goes. Confirming hands over to restore(), which saves and opens the draft;
// a save that fails leaves the dialog open, with the editor's toast saying why.
export async function restoreFlow({ api, here, restore, dirty }) {
  let newest = null, unread = false;
  try {
    const got = await api("GET", `/api/v1/drafts/${encodeURIComponent(here.processId)}/comparison`);
    newest = got && typeof got === "object" && got.processId ? got : null;
  } catch (e) {
    // A 404 is "no draft"; anything else is a draft that could not be read, which is
    // not the same answer and is not passed off as it.
    unread = !(e && e.status === 404);
  }
  let isDirty = false;
  try { isDirty = !!(dirty && dirty()); } catch { /* no stack, nothing of its own */ }
  const m = restoreModel({ here, newest, unread, dirty: isDirty });
  openRestoreDialog(m, restore);
}

function openRestoreDialog(m, restore) {
  const ov = document.createElement("div");
  ov.className = "modal-ov";
  const rows = m.rows.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(v)}</dd>`).join("");
  const warn = m.warn
    ? `<div class="sv-note warn sv-restore-warn" id="sv-restore-warn"><p>${esc(m.warn)}</p>${
      m.counts ? `<p class="sv-restore-counts">${esc(m.counts)}</p>` : ""}${
      m.changes ? `<button type="button" class="btn neutral small" data-sv-restore-changes>${esc(t("shown.showChanges"))}</button>` : ""}</div>`
    : "";
  ov.innerHTML = `
    <div class="modal confirm-modal sv-restore" role="dialog" aria-modal="true" aria-labelledby="sv-restore-title">
      <div class="modal-head"><h2 id="sv-restore-title">${esc(m.title)}</h2></div>
      <div class="modal-body">
        <p>${esc(m.intro)}</p>
        <dl class="sv-rows">${rows}</dl>${warn}
        ${m.dirty ? `<p class="sv-note">${esc(m.dirty)}</p>` : ""}
        <p class="sv-note muted">${esc(m.after)}</p>
      </div>
      <div class="modal-foot">
        <button type="button" class="btn neutral" data-sv-restore-cancel>${esc(t("shown.restore.cancel"))}</button>
        <button type="button" class="btn${m.tone === "warn" ? " danger" : ""}" data-sv-restore-ok>${esc(m.confirm)}</button>
      </div>
    </div>`;
  document.body.appendChild(ov);
  const ok = ov.querySelector("[data-sv-restore-ok]");
  const close = () => {
    ov.remove();
    document.removeEventListener("keydown", onKey, true);
  };
  // The list of differences opens over this dialog; Escape closes that one first.
  const onKey = (e) => {
    if (e.key !== "Escape") return;
    const all = document.querySelectorAll(".modal-ov");
    if (all[all.length - 1] !== ov) return;
    e.stopPropagation();
    close();
  };
  ov.querySelector("[data-sv-restore-cancel]").addEventListener("click", close);
  ov.addEventListener("click", (e) => { if (e.target === ov) close(); });
  const changes = ov.querySelector("[data-sv-restore-changes]");
  if (changes) {
    changes.addEventListener("click", () => showChangesDialog({
      title: t("shown.changesDraftTitle", { v: `v${m.changes.version}` }),
      intro: t("shown.changesDraftIntro", { v: `v${m.changes.version}` }),
      cmp: m.changes,
    }));
  }
  ok.addEventListener("click", async () => {
    ok.disabled = true;
    let done = false;
    try { done = await restore(); } catch { done = false; }
    if (done) close();
    else ok.disabled = false;
  });
  document.addEventListener("keydown", onKey, true);
  setTimeout(() => ov.querySelector("[data-sv-restore-cancel]").focus(), 0);
}
