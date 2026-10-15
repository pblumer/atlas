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
// draftStanding and versionStanding are pure functions of what the server answered, so
// a browser test drives them without an editor around them — the split
// frozen-decisions.js already makes.

import { fmtDateTime } from "./datefmt.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const when = (sec) => (sec ? fmtDateTime(sec * 1000) : "");

// stamp is "when · who", or what to say when neither is known. A draft saved before
// "last saved by" was recorded, or on a server without accounts, has a time and no
// name; that is said as it is rather than filled in.
function stamp(at, name) {
  const parts = [when(at), name].filter(Boolean);
  return parts.length ? parts.join(" · ") : "not recorded";
}

// draftStanding is the chip for a session that addresses a draft. cmp is the server's
// comparison of the draft with the newest deployed version, or null when it could not
// be read — then the chip still says "Draft", and only that.
export function draftStanding(cmp) {
  if (!cmp) {
    return {
      label: "Draft", tone: "", title: "The draft of this process",
      rows: [["Showing", "The draft — the editable copy an application publishes"]],
      note: "Could not compare this draft with what is deployed.", links: [],
    };
  }
  const rows = [
    ["Showing", "The draft — the editable copy an application publishes"],
    ["Created by", cmp.ownerName || "not recorded"],
    ["Last saved", stamp(cmp.savedAt, cmp.savedByName)],
  ];
  if (!cmp.deployed) {
    rows.push(["Deployed", "No version yet"]);
    return {
      label: "Draft · not deployed", tone: "off",
      title: "The draft of this process. No version of it is deployed yet.",
      rows, note: "", links: [],
    };
  }
  const v = `v${cmp.version}`;
  rows.push(["Newest version", `${v} · deployed ${stamp(cmp.deployedAt, cmp.deployedByName)}`]);
  const links = [{ href: `#/modeler/d/${encodeURIComponent(cmp.key)}`, text: `Open ${v}` }];
  if (cmp.sameModel) {
    return {
      label: `Draft = ${v}`, tone: "ok",
      title: `The draft of this process — the same model as the deployed ${v}.`,
      rows, links,
      note: `This draft is the model ${v} runs. The diagram's layout is not compared.`,
    };
  }
  const diff = cmp.difference ? ` First difference: ${cmp.difference}.` : "";
  // Deployed after the draft's last save and not the same model: the version holds
  // something this draft does not. That is the case that loses work — editing this
  // draft and publishing its application deploys without it — so it is the warning.
  // An equal second counts as after: missing the warning costs more than showing it.
  if (cmp.deployedAt >= cmp.savedAt) {
    return {
      label: `Draft ≠ ${v}`, tone: "warn",
      title: `${v} was deployed after this draft was last saved, and it is a different model. ` +
        "This draft may be missing changes that are deployed.",
      rows, links,
      note: `${v} was deployed after this draft was last saved, and it is not the model shown here.${diff} ` +
        `A change deployed in ${v} is not in this draft: publishing the application from this draft would ` +
        `replace it. Open ${v} and Save it to make it the draft again, or carry the change over here.`,
    };
  }
  return {
    label: `Draft · ${v} + changes`, tone: "",
    title: `The draft of this process, changed since ${v} was deployed.`,
    rows, links,
    note: `This draft has changes that are not deployed yet.${diff} Deploy it, or publish its application, to run them.`,
  };
}

// versionStanding is the chip for a session opened on a deployed version. here is its
// entry in GET /api/v1/processes, versions every entry of the same process id, and cmp
// the comparison of the process's draft with this version — null when there is no
// draft, or none this caller may read. draftUnread says the comparison failed for
// another reason, so "no draft" would be a guess.
export function versionStanding(here, versions, cmp, draftUnread) {
  const newest = (versions || []).reduce((a, b) => (b.version > a.version ? b : a), here);
  const latest = newest.version === here.version;
  const v = `v${here.version}`;
  const rows = [
    ["Showing", `Deployed version ${here.version} of ${newest.version} — it does not change; Deploy makes a new one`],
    ["Deployed", stamp(here.deployedAt, here.deployedByName)],
  ];
  if (here.diagramUpdatedAt) {
    rows.push(["Layout adjusted", stamp(here.diagramUpdatedAt, here.diagramUpdatedByName || here.diagramUpdatedBy)]);
  }
  const links = [];
  if (!latest) {
    rows.push(["Newest version", `v${newest.version} · deployed ${stamp(newest.deployedAt, newest.deployedByName)}`]);
    links.push({ href: `#/modeler/d/${encodeURIComponent(newest.key)}`, text: `Open v${newest.version}` });
  }
  let note = "";
  if (cmp) {
    const same = cmp.sameModel ? `the same model as ${v}` : `a different model from ${v}`;
    rows.push(["Draft", `Last saved ${stamp(cmp.savedAt, cmp.savedByName)} — ${same}`]);
    links.push({ href: `#/modeler/draft/${encodeURIComponent(here.processId)}`, text: "Open the draft" });
    if (!cmp.sameModel) {
      note = `The draft of this process is not ${v}` + (cmp.difference ? ` (first difference: ${cmp.difference})` : "") +
        ". Save here to replace the draft with this version.";
    }
  } else if (draftUnread) {
    rows.push(["Draft", "Could not be read"]);
  } else {
    rows.push(["Draft", "None — Save turns this version into one"]);
  }
  return {
    label: latest ? `${v} · newest` : `${v} of ${newest.version}`,
    tone: latest ? "ok" : "warn",
    title: `Deployed version ${here.version} of ${newest.version}, deployed ${stamp(here.deployedAt, here.deployedByName)}.`,
    rows, note, links,
  };
}

// unsavedStanding is the chip for a diagram that has never been saved.
export function unsavedStanding() {
  return {
    label: "Not saved", tone: "off", title: "A new diagram, not saved as a draft yet.",
    rows: [["Showing", "A new diagram, not saved as a draft yet"]], note: "", links: [],
  };
}

// standingHTML renders a standing into the chip and its panel.
export function standingHTML(s) {
  const rows = s.rows.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(v)}</dd>`).join("");
  const note = s.note ? `<p class="sv-note${s.tone === "warn" ? " warn" : ""}">${esc(s.note)}</p>` : "";
  const links = s.links.length
    ? `<div class="sv-links">${s.links.map((l) => `<a class="btn neutral small" href="${esc(l.href)}">${esc(l.text)}</a>`).join("")}</div>`
    : "";
  return `<button type="button" class="pill sv-chip${s.tone ? " " + s.tone : ""}" id="sv-chip"
      aria-haspopup="true" aria-expanded="false" aria-controls="sv-pop" title="${esc(s.title)}">${esc(s.label)}</button>
    <div class="sv-pop" id="sv-pop" role="dialog" aria-label="What is shown" hidden>
      <dl class="sv-rows">${rows}</dl>${note}${links}
    </div>`;
}

// attachShownVersion mounts the chip into host and keeps it current. which() is read
// on every refresh, because what a session addresses changes under it: the first Save
// of a new diagram, or of a version opened read-only, makes it address a draft.
//
// Every read is best-effort. A listing that cannot be read leaves the chip out rather
// than saying something it does not know; the editor never depends on it.
export function attachShownVersion(host, { api, which }) {
  let seq = 0;
  let closed = false;
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

  const read = async (method, url) => {
    try { return await api(method, url); } catch { return null; }
  };

  const standingNow = async () => {
    const { key, draftId } = which();
    if (draftId != null) {
      const cmp = await read("GET", `/api/v1/drafts/${encodeURIComponent(draftId)}/comparison`);
      return draftStanding(cmp && typeof cmp === "object" && cmp.processId ? cmp : null);
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
    return versionStanding(here, versions, cmp && typeof cmp === "object" && cmp.processId ? cmp : null, unread);
  };

  const refresh = async () => {
    const mine = ++seq;
    const s = await standingNow();
    if (closed || mine !== seq) return; // superseded, or the editor is gone
    const wasOpen = !!host.querySelector("#sv-pop:not([hidden])");
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
