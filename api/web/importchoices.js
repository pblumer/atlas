// The dialog an import shows before it replaces anything
// (ADR-0467).
//
// An import compares every artifact it carries with what the server holds and with
// what the last import brought: new, unchanged, behind (as the last import left it,
// and newer in the file), ahead (changed here, and as before in the file) or changed
// (changed on both sides, or never imported). New is written and unchanged left
// alone, so the same file imported twice changes nothing the second time. Behind is
// replaced and ahead kept unless the person says otherwise; changed is work done on
// this server that the file would overwrite, and is kept until the person ticks it.
// This dialog lists all three, with a box each, set to what happens without a word.
//
// It lives in its own module for the reason the pick dialog does: app.js boots the
// whole console on import, so anything left inside it is only ever exercised by hand.

import { t } from "./i18n.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// The statuses the dialog lists, in its order, with what each box starts as; title and
// hint are catalogue keys.
const GROUPS = [
  { status: "changed", ticked: false, title: "choices.changed", hint: "choices.changedHint" },
  { status: "behind", ticked: true, title: "choices.behind", hint: "choices.behindHint" },
  { status: "ahead", ticked: false, title: "choices.ahead", hint: "choices.aheadHint" },
];

// decisionQuery is the decisions as the import routes take them. Every changed
// artifact is named, under overwrite or keep, rather than with *: one that changed
// after the dialog was shown is then refused instead of replaced on the strength of a
// decision about something else. Behind and ahead are named only where the person
// moved a box: left as it started, the server settles them again where it writes.
export function decisionQuery({ overwrite = [], keep = [], adopt = [], map = [], omit = [] }) {
  const q = new URLSearchParams();
  if (overwrite.length) q.set("overwrite", overwrite.join(","));
  if (keep.length) q.set("keep", keep.join(","));
  if (adopt.length) q.set("adopt", adopt.join(","));
  // A gap in the capability map (ADR-draft-application-bundle-architecture): mapped
  // as ref=key, or left out.
  if (map.length) q.set("map", map.join(","));
  if (omit.length) q.set("omit", omit.join(","));
  return q.toString();
}

// needsAsking reports whether a plan has anything the dialog lists.
export function needsAsking(artifacts) {
  return (artifacts || []).some((a) => a.status === "elsewhere" || a.status === "gap" ||
    GROUPS.some((g) => g.status === a.status));
}

// gapChoices reads the dialog's gap rows: each adopted, mapped onto a key or left
// out. A row set to map with no key is left out — nothing is filed under a key
// nobody named.
export function gapChoices(rows) {
  const adopt = [], map = [], omit = [];
  for (const { ref, choice, key } of rows) {
    const k = String(key || "").trim();
    if (choice === "adopt") adopt.push(ref);
    else if (choice === "map" && k) map.push(`${ref}=${k}`);
    else omit.push(ref);
  }
  return { adopt, map, omit };
}

// openImportChoices shows the plan and asks what to replace and what to take from
// another application. Resolves to { overwrite, keep, adopt } — refs — or null on
// cancel, Escape or a click outside.
export function openImportChoices({ title, artifacts }) {
  return new Promise((resolve) => {
    const count = (status) => artifacts.filter((a) => a.status === status).length;
    const fresh = artifacts.filter((a) => a.status === "new");
    const label = (a) => `<code>${esc(a.ref)}</code>${a.name && a.name !== a.ref.split(":").slice(1).join(":")
      ? ` ${esc(a.name)}` : ""}`;
    const group = (g) => {
      const items = artifacts.filter((a) => a.status === g.status);
      if (!items.length) return "";
      return `<section class="import-group" data-status="${g.status}">
          <h3>${esc(t(g.title))} <span class="muted">(${items.length})</span></h3>
          <p class="muted small">${esc(t(g.hint))}</p>
          <ul class="import-changed">${items.map((a) => `
            <li><label><input type="checkbox" class="import-pick" value="${esc(a.ref)}"
              data-status="${g.status}"${g.ticked ? " checked" : ""}> ${t("choices.replace", { label: label(a) })}</label></li>`).join("")}</ul>
        </section>`;
    };
    // Artifacts another application holds (ADR-0467 amendment 2026-11-03): taken
    // only when the person ticks them, and then, if their content differs, kept as
    // this server has it unless the second box says to replace it.
    const elsewhere = () => {
      const items = artifacts.filter((a) => a.status === "elsewhere");
      if (!items.length) return "";
      const holder = (a) => a.heldBy ? a.heldBy.name : t("choices.elsewhereHidden");
      return `<section class="import-group" data-status="elsewhere">
          <h3>${esc(t("choices.elsewhere"))} <span class="muted">(${items.length})</span></h3>
          <p class="muted small">${esc(t("choices.elsewhereHint"))}</p>
          <ul class="import-changed">${items.map((a) => `
            <li><label><input type="checkbox" class="import-adopt" value="${esc(a.ref)}">
              ${t("choices.adopt", { label: label(a), holder: esc(holder(a)) })}</label>
              ${a.compared === "changed" ? `<br><label class="small" style="margin-left:1.6em"><input type="checkbox"
                class="import-adopt-content" value="${esc(a.ref)}"> ${esc(t("choices.adoptContent"))}</label>` : ""}</li>`).join("")}</ul>
        </section>`;
    };
    // Capabilities and value streams the server's map lacks: adopted, mapped onto a
    // key the map has, or left out. Each starts as the package proposes it when it
    // proposes one, and as left out otherwise.
    const gaps = () => {
      const items = artifacts.filter((a) => a.status === "gap");
      if (!items.length) return "";
      return `<section class="import-group" data-status="gap">
          <h3>${esc(t("choices.gap"))} <span class="muted">(${items.length})</span></h3>
          <p class="muted small">${esc(t("choices.gapHint"))}</p>
          <ul class="import-changed">${items.map((a) => {
            const isCap = a.ref.startsWith("capability:");
            return `
            <li class="import-gap" data-ref="${esc(a.ref)}">${label(a)}<br>
              <select class="import-gap-choice" style="margin-left:1.6em">
                ${a.proposed ? `<option value="adopt" selected>${esc(t("choices.gapAdopt"))}</option>` : ""}
                ${isCap ? `<option value="map">${esc(t("choices.gapMap"))}</option>` : ""}
                <option value="omit"${a.proposed ? "" : " selected"}>${esc(t("choices.gapOmit"))}</option>
              </select>
              ${isCap ? `<input type="text" class="import-gap-key small" placeholder="${esc(t("choices.gapKey"))}" hidden>` : ""}</li>`;
          }).join("")}</ul>
        </section>`;
    };
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal import-choices" role="dialog" aria-modal="true" aria-label="${esc(title)}">
        <div class="modal-head">
          <h2>${esc(title)}</h2>
          <button type="button" class="icon-btn" data-x aria-label="${esc(t("common.close"))}" title="${esc(t("common.close"))}">✕</button>
        </div>
        <div class="modal-body">
          <p class="import-counts">${t("choices.counts", {
            created: count("new"), unchanged: count("unchanged"), behind: count("behind"),
            ahead: count("ahead"), changed: count("changed"),
          })}</p>
          <div class="import-bulk">
            <button type="button" class="btn ghost small" data-all title="${esc(t("choices.tickAllTitle"))}">${esc(t("choices.tickAll"))}</button>
            <button type="button" class="btn ghost small" data-none title="${esc(t("choices.tickNoneTitle"))}">${esc(t("choices.tickNone"))}</button>
          </div>
          ${elsewhere()}
          ${gaps()}
          ${GROUPS.map(group).join("")}
          ${fresh.length ? `<details class="import-new fold-sub"><summary>${esc(t("choices.fresh", { n: fresh.length }))}</summary>
            <ul>${fresh.map((a) => `<li>${label(a)}</li>`).join("")}</ul></details>` : ""}
        </div>
        <div class="modal-foot">
          <span></span>
          <span class="modal-actions">
            <button type="button" class="btn ghost" data-cancel>${esc(t("common.cancel"))}</button>
            <button type="button" class="btn" data-ok>${esc(t("choices.import"))}</button>
          </span>
        </div>
      </div>`;
    document.body.appendChild(ov);
    const picks = () => [...ov.querySelectorAll(".import-pick")];
    ov.querySelectorAll(".import-gap").forEach((row) => {
      const sel = row.querySelector(".import-gap-choice");
      const key = row.querySelector(".import-gap-key");
      if (sel && key) sel.addEventListener("change", () => { key.hidden = sel.value !== "map"; });
    });
    const startsTicked = (p) => GROUPS.find((g) => g.status === p.dataset.status).ticked;

    let settled = false;
    const finish = (result) => {
      if (settled) return;
      settled = true;
      ov.remove();
      document.removeEventListener("keydown", onKey);
      resolve(result);
    };
    const onKey = (e) => { if (e.key === "Escape") finish(null); };
    document.addEventListener("keydown", onKey);
    ov.addEventListener("mousedown", (e) => { if (e.target === ov) finish(null); });
    ov.querySelector("[data-x]").addEventListener("click", () => finish(null));
    ov.querySelector("[data-cancel]").addEventListener("click", () => finish(null));
    ov.querySelector("[data-all]").addEventListener("click", () => picks().forEach((p) => { p.checked = true; }));
    ov.querySelector("[data-none]").addEventListener("click", () => picks().forEach((p) => { p.checked = false; }));
    ov.querySelector("[data-ok]").addEventListener("click", () => {
      // Changed is always named; behind and ahead only where a box was moved.
      const named = picks().filter((p) => p.dataset.status === "changed" || p.checked !== startsTicked(p));
      const adopt = [...ov.querySelectorAll(".import-adopt")].filter((p) => p.checked).map((p) => p.value);
      // An adopted artifact whose content differs is named too: replaced where the
      // second box is ticked, kept as this server has it where it is not.
      const contentBox = (ref) => [...ov.querySelectorAll(".import-adopt-content")].find((p) => p.value === ref);
      const replaced = adopt.filter((ref) => contentBox(ref) && contentBox(ref).checked);
      const keptHere = adopt.filter((ref) => contentBox(ref) && !contentBox(ref).checked);
      const gapped = gapChoices([...ov.querySelectorAll(".import-gap")].map((row) => ({
        ref: row.dataset.ref,
        choice: row.querySelector(".import-gap-choice").value,
        key: row.querySelector(".import-gap-key") ? row.querySelector(".import-gap-key").value : "",
      })));
      finish({
        overwrite: [...named.filter((p) => p.checked).map((p) => p.value), ...replaced],
        keep: [...named.filter((p) => !p.checked).map((p) => p.value), ...keptHere],
        adopt: [...adopt, ...gapped.adopt],
        map: gapped.map,
        omit: gapped.omit,
      });
    });
    ov.querySelector("[data-ok]").focus();
  });
}

// importAsking runs an import the way the routes ask for it: the plan first, the
// dialog for whatever would be replaced or kept, then the import with the decisions.
// send(query) posts the import with a query string ("" for none) and resolves to the
// answer. Resolves to that answer, or null when the person cancelled; nothing is
// written then.
export async function importAsking(send, { title }) {
  const plan = await send("dryRun=true");
  const artifacts = (plan && plan.artifacts) || [];
  if (!needsAsking(artifacts)) return send("");
  const choice = await openImportChoices({ title, artifacts });
  if (!choice) return null;
  return send(decisionQuery(choice));
}

// outcomeSummary says in a few words what an import did with its artifacts.
export function outcomeSummary(artifacts) {
  const n = (outcome) => (artifacts || []).filter((a) => a.outcome === outcome).length;
  const parts = [t("choices.outcome.created", { n: n("created") }), t("choices.outcome.updated", { n: n("updated") })];
  if (n("kept")) parts.push(t("choices.outcome.kept", { n: n("kept") }));
  if (n("mapped")) parts.push(t("choices.mapped", { n: n("mapped") }));
  if (n("omitted")) parts.push(t("choices.omitted", { n: n("omitted") }));
  parts.push(t("choices.outcome.unchanged", { n: n("unchanged") }));
  return parts.join(", ");
}
