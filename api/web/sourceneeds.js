// The two dialogs around moving an application's source to another installation
// (ADR-0467, amended 2026-10-21).
//
// A download carries the application's processes, forms and decisions, and its
// constant values only when that is ticked: they belong to this installation, and a
// tree kept in a repository should not carry INT's values. What the processes read
// from the installation and no download carries — server constants, application
// constants without a value here, configured workers — is listed before the download,
// so whoever moves the application knows what the target has to have. The import
// answers the same list as the target stands, and names what is still missing there
// before anybody publishes.
//
// It lives in its own module for the reason the pick dialog does: app.js boots the
// whole console on import, so anything left inside it is only ever exercised by hand.

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// KINDS are the kinds of need, in the order the dialogs list them, with where an
// installation sets each up.
const KINDS = [
  { kind: "server-constant", title: "Server constants", where: "Admin → Engine → Server constants" },
  { kind: "application-constant", title: "Application constants", where: "the application's Constants tab" },
  { kind: "worker", title: "Workers", where: "Workers, as a worker of this name and Worker Type" },
];

// STATES says what each state means where it is reported.
const STATES = {
  set: "set",
  missing: "missing",
  fallback: "not set; the model's fallback applies",
  "type-mismatch": "held with another type",
  configured: "configured",
  "other-type": "configured as another Worker Type",
  unusable: "configured, but not usable",
};

// OPEN are the states that leave a need unanswered: an instance would run without
// the value, with another type, or park at a worker that is not there.
const OPEN = new Set(["missing", "type-mismatch", "other-type", "unusable"]);

// openNeeds lists the needs an import left unanswered.
export function openNeeds(needs) {
  return (needs || []).filter((n) => OPEN.has(n.state));
}

// needLine renders one need: its name and type, which processes read it, and, when
// state is given, how the installation in question answers it.
function needLine(n, { state = true, note = "" } = {}) {
  const type = n.type ? ` <span class="muted">${esc(n.type)}</span>` : "";
  const fallback = n.fallback && n.kind !== "worker" ? ` <span class="muted">(has a fallback)</span>` : "";
  const processes = (n.processes || []).length
    ? ` <span class="muted small">— ${esc(n.processes.join(", "))}</span>` : "";
  const st = state && n.state
    ? ` <span class="need-state" data-state="${esc(n.state)}">${esc(STATES[n.state] || n.state)}${n.detail ? `: ${esc(n.detail)}` : ""}</span>`
    : "";
  return `<li data-need="${esc(n.kind)}/${esc(n.name)}"><code>${esc(n.name)}</code>${type}${fallback}${processes}${st}${note}</li>`;
}

// needGroups renders the needs grouped by kind; line renders each.
function needGroups(needs, line) {
  return KINDS.map((k) => {
    const items = needs.filter((n) => n.kind === k.kind);
    if (!items.length) return "";
    return `<section class="need-group" data-kind="${k.kind}">
        <h3>${esc(k.title)} <span class="muted">(${items.length})</span></h3>
        <p class="muted small">Set up under ${esc(k.where)}.</p>
        <ul>${items.map(line).join("")}</ul>
      </section>`;
  }).join("");
}

// mountModal shows a dialog and wires closing it: the close button, Escape, a click
// outside and any [data-cancel] finish it with null. finish(result) removes it, and
// done resolves to the result.
function mountModal(className, title, body, foot) {
  const ov = document.createElement("div");
  ov.className = "modal-ov";
  ov.innerHTML = `
    <div class="modal ${className}" role="dialog" aria-modal="true" aria-label="${esc(title)}">
      <div class="modal-head">
        <h2>${esc(title)}</h2>
        <button type="button" class="icon-btn" data-x aria-label="Close" title="Close">✕</button>
      </div>
      <div class="modal-body">${body}</div>
      <div class="modal-foot"><span></span><span class="modal-actions">${foot}</span></div>
    </div>`;
  let resolveDone;
  const done = new Promise((resolve) => { resolveDone = resolve; });
  let settled = false;
  const onKey = (e) => { if (e.key === "Escape") finish(null); };
  const finish = (result) => {
    if (settled) return;
    settled = true;
    ov.remove();
    document.removeEventListener("keydown", onKey);
    resolveDone(result);
  };
  document.body.appendChild(ov);
  document.addEventListener("keydown", onKey);
  ov.addEventListener("mousedown", (e) => { if (e.target === ov) finish(null); });
  ov.querySelector("[data-x]").addEventListener("click", () => finish(null));
  ov.querySelectorAll("[data-cancel]").forEach((b) => b.addEventListener("click", () => finish(null)));
  return { ov, done, finish };
}

// catalogueLabel is a catalogue as a person reads it: its name, else its id.
const catalogueLabel = (c) => (c.texts && (c.texts.en || c.texts.de || Object.values(c.texts)[0])) || c.id;

// openSourceDownload asks what the application's package carries — its constant
// values, and the catalogues that sell it — and shows what the target needs. info is
// GET /api/v1/applications/{id}/needs. With catalogues ticked the download is the
// package (GET …/package), asked first for a preview whose warnings are shown before
// anything is downloaded; preview(url) answers it. Resolves to the download's URL, or
// null on cancel.
export function openSourceDownload({ id, info, preview }) {
  const held = (info && info.constants) || 0;
  const needs = (info && info.needs) || [];
  const unread = (info && info.unread) || [];
  const catalogs = (info && info.catalogs) || [];
  const app = encodeURIComponent(id);
  const appConstantNote = (n, carried) => {
    if (n.kind !== "application-constant") return "";
    return n.state === "set" && carried
      ? ` <span class="need-carried">its value travels with the download</span>`
      : ` <span class="need-carried">set it on the target</span>`;
  };
  const list = (carried) => needs.length
    ? needGroups(needs, (n) => needLine(n, { state: false, note: appConstantNote(n, carried) }))
    : `<p class="muted">The processes read nothing from the installation and name no worker.</p>`;
  const catalogList = catalogs.length
    ? `<ul class="package-catalogs">${catalogs.map((c) => `
        <li><label><input type="checkbox" name="catalog" value="${esc(c.id)}"${c.bound ? " checked" : ""}>
          ${esc(catalogueLabel(c))} <code>${esc(c.id)}</code>${c.bound
            ? ` <span class="need-carried">sells this application</span>` : ""}</label></li>`).join("")}</ul>`
    : `<p class="muted small">No catalogue to choose: you maintain none here, or this server has none.</p>`;
  const body = `
    <p>The download carries the application's processes, forms and decisions.</p>
    <label class="source-constants"><input type="checkbox" name="withConstants"${held ? " checked" : " disabled"}>
      ${held ? `Include the ${held} constant value${held === 1 ? "" : "s"} this application holds here`
        : "This application holds no constant values here"}</label>
    <p class="muted small">The target offers them like any other artifact: a value it lacks is set, one an
      administrator set there is kept unless somebody ticks it. Leave this off for a tree kept in a
      repository; the values belong to this installation.</p>
    <h3 class="needs-title">Catalogues</h3>
    <p class="muted small">Ticked ones travel with the application. Their groups and people travel by name,
      and the target finds them in its own directory; it asks for any it cannot find.</p>
    ${catalogList}
    <div class="package-warnings" hidden></div>
    <h3 class="needs-title">What the target needs, and no download carries</h3>
    <div class="source-needs">${list(held > 0)}</div>
    ${unread.length ? `<p class="muted small source-unread">Not read, because ${unread.length === 1 ? "it does" : "they do"} not compile yet:
      ${unread.map((u) => `<code>${esc(u)}</code>`).join(", ")}.</p>` : ""}`;
  const foot = `<button type="button" class="btn ghost" data-cancel>Cancel</button>
    <button type="button" class="btn" data-ok>Download</button>`;
  const { ov, done, finish } = mountModal("source-download", "Export application", body, foot);
  const box = ov.querySelector('[name="withConstants"]');
  const ok = ov.querySelector("[data-ok]");
  const warnings = ov.querySelector(".package-warnings");
  // A selection the warnings were shown for; downloading it again needs no second look.
  let warned = null;
  const url = () => {
    const ticked = [...ov.querySelectorAll('[name="catalog"]:checked')].map((b) => b.value);
    const q = new URLSearchParams();
    if (box.checked) q.set("constants", "true");
    ticked.forEach((c) => q.append("catalog", c));
    const query = q.toString();
    return `/api/v1/applications/${app}/${ticked.length ? "package" : "source"}${query ? "?" + query : ""}`;
  };
  const reset = () => {
    warned = null;
    warnings.hidden = true;
    ok.textContent = "Download";
  };
  box.addEventListener("change", () => {
    ov.querySelector(".source-needs").innerHTML = list(box.checked);
  });
  ov.querySelectorAll('[name="catalog"]').forEach((b) => b.addEventListener("change", reset));
  ok.addEventListener("click", async () => {
    const target = url();
    if (!target.includes("/package") || !preview || warned === target) return finish(target);
    ok.disabled = true;
    let p;
    try {
      p = await preview(target + "&preview=true");
    } catch (e) {
      warnings.hidden = false;
      warnings.innerHTML = `<div class="deploy-warnings"><b>&#9888; The package cannot be written:</b>
        <ul><li>${esc((e && e.message) || e)}</li></ul></div>`;
      ok.disabled = false;
      return;
    }
    ok.disabled = false;
    if (!(p.warnings || []).length) return finish(target);
    warned = target;
    warnings.hidden = false;
    warnings.innerHTML = `<div class="deploy-warnings"><b>&#9888; On the target, this will not work as it does here:</b>
      <ul>${p.warnings.map((w) => `<li>${esc(w)}</li>`).join("")}</ul></div>`;
    ok.textContent = "Download anyway";
  });
  ok.focus();
  return done;
}

// needsReport says whether an import answer has anything for showImportNeeds.
export function needsReport(result) {
  return !!result && (openNeeds(result.needs).length > 0 || (result.constantsNotSet || []).length > 0 ||
    (result.needsUnread || []).length > 0 || (result.warnings || []).length > 0);
}

// showImportNeeds tells what an import left for this installation to set up: the needs
// it does not answer, the constants it did not set because the caller is not an
// administrator, the processes whose needs it could not read, and what the publish
// and the catalogue import warned about (warnings). Resolves when it is closed.
export function showImportNeeds(result) {
  const open = openNeeds(result.needs);
  const notSet = result.constantsNotSet || [];
  const unread = result.needsUnread || [];
  const body = `
    <p>The application is in. Before it runs here as it does where it came from, this server needs:</p>
    ${needGroups(open, (n) => needLine(n))}
    ${notSet.length ? `<section class="need-group" data-kind="not-set">
        <h3>Constants not set <span class="muted">(${notSet.length})</span></h3>
        <p class="muted small">Setting a constant is an administrator's; an administrator sets these on the
          application's Constants tab.</p>
        <ul>${notSet.map((n) => `<li><code>${esc(n)}</code></li>`).join("")}</ul>
      </section>` : ""}
    ${unread.length ? `<p class="muted small source-unread">Not read, because ${unread.length === 1 ? "it does" : "they do"} not compile:
      ${unread.map((u) => `<code>${esc(u)}</code>`).join(", ")}.</p>` : ""}
    ${(result.warnings || []).length ? `<section class="need-group" data-kind="warnings">
        <h3>Warnings <span class="muted">(${result.warnings.length})</span></h3>
        <ul>${result.warnings.map((w) => `<li>${esc(w)}</li>`).join("")}</ul>
      </section>` : ""}
    <p class="muted small">Publishing does not wait for them; it warns about each, and an instance started
      without them runs without the value or parks at the worker.</p>`;
  const { ov, done, finish } = mountModal("import-needs", `Before ${result.name || "it"} runs here`, body,
    `<button type="button" class="btn" data-done>Close</button>`);
  ov.querySelector("[data-done]").addEventListener("click", () => finish(true));
  ov.querySelector("[data-done]").focus();
  return done;
}
