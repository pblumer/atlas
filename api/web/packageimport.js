// Installing an application package from the Console
// (ADR-0467, amended 2026-10-21).
//
// A package is what "Export application…" downloads and `atlas export` writes: the
// application's source tree, and optionally katalog.json with the catalogues that sell
// it, fragen.json with one question per group and person they name, and antworten.json
// with the names the exporting server gives them. A plain source tree is a package
// without a shop.
//
// The import takes the steps `atlas import` takes, through the same routes and under
// the authority each asks: the plan first — the application's, its constants', the
// catalogues' — with the questions answered from this server's directory, one dialog
// for whatever differs from what is here, then the application, its constants, a
// publish when the package brings a shop (its products are bound to the processes the
// publish deploys), and the catalogues. The rules — which artifact is new, behind,
// ahead or changed; which group a name means here — are the server's; this module
// only asks the person and calls the routes in order.
//
// It lives in its own module for the reason the other dialogs do: app.js boots the
// whole console on import, so anything left inside it is only ever exercised by hand.

import { decisionQuery, needsAsking, openImportChoices } from "./importchoices.js";
import { t } from "./i18n.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const text = (bytes) => new TextDecoder().decode(bytes);

// readArchive reads a gzip tar into its regular files, by path. Only the root files a
// package adds are read by name; the source tree goes to the server as the archive it
// came in, so an entry this reader does not understand costs nothing but its name.
export async function readArchive(bytes) {
  const stream = new Blob([bytes]).stream().pipeThrough(new DecompressionStream("gzip"));
  const tar = new Uint8Array(await new Response(stream).arrayBuffer());
  const field = (h, at, len) => text(h.subarray(at, at + len)).replace(/\0.*$/s, "");
  const files = new Map();
  for (let at = 0; at + 512 <= tar.length;) {
    const header = tar.subarray(at, at + 512);
    if (header.every((b) => b === 0)) break;
    const size = parseInt(field(header, 124, 12).trim() || "0", 8);
    const type = String.fromCharCode(header[156] || 48);
    const prefix = field(header, 345, 155);
    const name = (prefix ? prefix + "/" : "") + field(header, 0, 100);
    at += 512;
    if (type === "0" || type === "\0") files.set(name.replace(/^\.\//, ""), tar.subarray(at, at + size));
    at += Math.ceil(size / 512) * 512;
  }
  return files;
}

// openAnswers asks for what this server's directory could not answer: a group or a
// person nobody here carries by that name, or a name several people carry. Each is a
// choice among the principals of its kind (the ones an ambiguous name matches first) —
// a group or a user by id, an account (a fixed approver) by username; a question of
// another kind takes text. directory is GET /api/v1/principals with the accounts of
// GET /api/v1/users/assignable as { type: "account", id: username, name }. Resolves
// to { name: answer }, or null.
export function openAnswers(problems, directory) {
  return new Promise((resolve) => {
    const field = (p) => {
      if (p.kind !== "group" && p.kind !== "user" && p.kind !== "account") {
        return `<input type="text" class="package-answer" data-name="${esc(p.name)}" value="${esc(p.answer || "")}">`;
      }
      const all = directory.filter((e) => e.type === p.kind);
      const first = all.filter((e) => (p.candidates || []).includes(e.id));
      const rest = all.filter((e) => !(p.candidates || []).includes(e.id));
      const opt = (e) => `<option value="${esc(e.id)}">${esc(e.name)} (${esc(e.id)})</option>`;
      return `<select class="package-answer" data-name="${esc(p.name)}">
          <option value="">${esc(t("package.answers.choose"))}</option>${first.map(opt).join("")}${rest.map(opt).join("")}</select>`;
    };
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal package-answers" role="dialog" aria-modal="true" aria-label="${esc(t("package.answers.title"))}">
        <div class="modal-head">
          <h2>${esc(t("package.answers.title"))}</h2>
          <button type="button" class="icon-btn" data-x aria-label="${esc(t("common.close"))}" title="${esc(t("common.close"))}">✕</button>
        </div>
        <div class="modal-body">
          <p>${esc(t("package.answers.intro"))}</p>
          ${problems.map((p) => `<div class="package-question" data-name="${esc(p.name)}">
            <label><b>${esc(p.question || p.name)}</b>
              <span class="muted small">${esc(p.answer ? t("package.answers.detail", { answer: p.answer, detail: p.detail }) : p.detail)}</span>
              ${field(p)}</label></div>`).join("")}
        </div>
        <div class="modal-foot"><span></span><span class="modal-actions">
          <button type="button" class="btn ghost" data-cancel>${esc(t("common.cancel"))}</button>
          <button type="button" class="btn" data-ok>${esc(t("package.answers.continue"))}</button></span></div>
      </div>`;
    document.body.appendChild(ov);
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
    ov.querySelector("[data-ok]").addEventListener("click", () => {
      const out = {};
      for (const f of ov.querySelectorAll(".package-answer")) {
        if (f.value.trim()) out[f.dataset.name] = f.value.trim();
      }
      finish(out);
    });
  });
}

// importPackage installs a package file. call(method, path, body, type) sends one
// request — body raw bytes with a type, or anything else as JSON — and resolves to
// { status, data }, the answer decoded. Resolves to what each step did, or null when
// the person cancelled; nothing is written before the last question is answered.
export async function importPackage(file, { call, title = t("package.title"), beforeWrite }) {
  const bytes = new Uint8Array(await file.arrayBuffer());
  const files = await readArchive(bytes);
  const json = (name) => (files.has(name) ? JSON.parse(text(files.get(name))) : null);
  const shop = files.has("katalog.json") ? {
    document: text(files.get("katalog.json")),
    questions: json("fragen.json") || {},
    answers: json("antworten.json") || {},
  } : null;
  const constants = json("konstanten.json");
  const ok = (res, what) => {
    if (res.status >= 200 && res.status < 300) return res.data;
    const d = res.data || {};
    const problems = (d.problems || []).map((p) => (p.subject ? `${p.subject}: ${p.problem}` : p.problem || p.detail));
    throw new Error(`${what}: ${d.error || "HTTP " + res.status}${problems.length ? " — " + problems.join("; ") : ""}`);
  };
  const q = (query, sep = "?") => (query ? sep + query : "");
  const source = async (query) => ok(await call("POST", "/api/v1/applications/source" + q(query), bytes, "application/gzip"),
    t("package.what.application"));
  const constantsPath = (appId, query) => `/api/v1/applications/${encodeURIComponent(appId)}/constants/import${q(query)}`;

  // The plans.
  const plan = await source("dryRun=true");
  let constantsPlan = null;
  if (constants && constants.constants && constants.constants.length) {
    // An application this server has never seen holds no constants: every one is new.
    constantsPlan = plan.created || !plan.applicationId
      ? { artifacts: constants.constants.map((c) => ({ ref: "constant:" + c.name, name: c.name, status: "new", outcome: "created" })) }
      : ok(await call("POST", constantsPath(plan.applicationId, "dryRun=true"), constants), t("package.what.constants"));
  }
  let filled = null;
  let shopPlan = null;
  // The person's choices go beside the package's answers, not over them: the server
  // remembers a choice against the name the package gave (ADR-0467), and answers
  // which choices are worth remembering (learned) and which questions its table of
  // names answered (remembered).
  const chosen = {};
  let learned = [];
  let remembered = [];
  if (shop) {
    let directory = null;
    for (;;) {
      const res = await call("POST", "/api/v1/catalogs/import/answers",
        { document: shop.document, questions: shop.questions, answers: shop.answers, chosen });
      if (res.status === 200) {
        filled = res.data.document;
        learned = res.data.learned || [];
        remembered = res.data.remembered || [];
        break;
      }
      if (res.status !== 422) ok(res, t("package.what.questions"));
      if (!directory) {
        const accounts = ok(await call("GET", "/api/v1/users/assignable"), t("package.what.accounts")).map((u) =>
          ({ type: "account", id: u.username, name: u.displayName || u.username }));
        directory = [...ok(await call("GET", "/api/v1/principals"), t("package.what.directory")), ...accounts];
      }
      const picked = await openAnswers(res.data.problems || [], directory);
      if (!picked) return null;
      Object.assign(chosen, picked);
    }
    shopPlan = ok(await call("POST", "/api/v1/catalogs/import?dryRun=true", filled), t("package.what.catalogues"));
  }

  // One dialog for everything that differs from what is here.
  const artifacts = [...(plan.artifacts || []), ...((constantsPlan && constantsPlan.artifacts) || []),
    ...((shopPlan && shopPlan.artifacts) || [])];
  let query = "";
  if (needsAsking(artifacts)) {
    const choice = await openImportChoices({ title, artifacts });
    if (!choice) return null;
    query = decisionQuery(choice);
  }

  // No native write (source, constants, publish, catalogue or mappings) may
  // precede this optional durability gate. A cancelled conflict/answers dialog
  // never creates a pending Marketplace journal. Ordinary file imports do not
  // provide the hook and keep their existing behaviour.
  if (beforeWrite) await beforeWrite();

  // The steps, in the order `atlas import` takes them.
  const out = { source: await source(query), constants: null, published: null, catalogue: null, warnings: [],
    remembered, learned: [] };
  const appId = out.source.applicationId;
  if (constantsPlan) {
    out.constants = ok(await call("POST", constantsPath(appId, query), constants), t("package.what.constants"));
    if ((out.constants.notSet || []).length) {
      out.source.constantsNotSet = [...(out.source.constantsNotSet || []), ...out.constants.notSet];
    }
  }
  if (shop) {
    const releases = ok(await call("GET", `/api/v1/applications/${encodeURIComponent(appId)}/releases`), t("package.what.releases"));
    const changed = out.source.created || (out.source.artifacts || []).some((a) =>
      (a.outcome === "created" || a.outcome === "updated") && !String(a.ref).startsWith("constant:"));
    if (!releases.length || changed) {
      const pub = ok(await call("POST", `/api/v1/applications/${encodeURIComponent(appId)}/publish`,
        { note: "Console package import" }), t("package.what.publish"));
      if (!pub.deployed) throw new Error(t("package.notPublished", { reason: pub.reason || t("package.refused") }));
      out.published = pub;
      out.warnings.push(...(pub.warnings || []));
    }
    out.catalogue = ok(await call("POST", "/api/v1/catalogs/import" + q(query), filled), t("package.what.catalogues"));
    // Only now, with the catalogues in, are the choices kept for the next import.
    if (learned.length) {
      ok(await call("POST", "/api/v1/catalogs/import/mappings", { mappings: learned }), t("package.what.mappings"));
      out.learned = learned;
    }
    out.warnings.push(...(out.catalogue.warnings || []).map((w) => (w.subject ? `${w.subject}: ${w.problem}` : String(w.problem || w))));
  }
  return out;
}

// packageSummary says in a few words what a package import did.
export function packageSummary(out) {
  const parts = [];
  if (out.published) parts.push(t("package.summary.published", { version: (out.published.release || {}).version }));
  if (out.catalogue) {
    const n = (k) => (out.catalogue[k] || []).length;
    parts.push(t("package.summary.shop", { created: n("created"), updated: n("updated"), releases: n("releases") }));
  }
  if ((out.remembered || []).length) parts.push(t("package.summary.remembered", { n: out.remembered.length }));
  if ((out.learned || []).length) parts.push(t("package.summary.learned", { n: out.learned.length }));
  return parts.join(" · ");
}
