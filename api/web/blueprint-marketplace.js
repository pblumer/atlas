// atlas hub: design-time browsing of signed application Blueprints (ADR-0499).
// The registry is configured and
// verified by the server. We never use a URL or signing key supplied by a
// catalogue, and never install before the operator confirms the preview.
//
// The actual import is ADR-0467's existing packageimport.js workflow — including
// dry-run conflict decisions, caller authorization, optional publish and needs.
import { importPackage, packageSummary } from "./packageimport.js";
import { t } from "./i18n.js";

const esc = (value) => String(value == null ? "" : value).replace(/[&<>"']/g,
  (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const route = (id, version) => "/api/v1/blueprints/" + encodeURIComponent(id) +
  "/releases/" + encodeURIComponent(version);

async function trustedJSON(url) {
  const res = await fetch(url, { cache: "no-store" });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || t("blueprint.failed"));
  return data;
}

// This atlas hub view presents an operator-pinned registry listing and a
// safe inspected package preview, then hands an approved package to the same
// importer as the ordinary Console file-upload action. No second installer.
export function openBlueprintMarketplace({ call, reload, notify }) {
  const ov = document.createElement("div");
  ov.className = "modal-ov";
  ov.innerHTML = `<div class="modal" role="dialog" aria-modal="true" aria-label="${esc(t("blueprint.title"))}">
      <div class="modal-head">
        <h2>${esc(t("blueprint.title"))}</h2>
        <button type="button" class="icon-btn" data-close aria-label="${esc(t("common.close"))}">✕</button>
      </div>
      <div class="modal-body" data-body><p class="muted">${esc(t("blueprint.loading"))}</p></div>
      <div class="modal-foot"><span></span><button type="button" class="btn ghost" data-close>${esc(t("common.close"))}</button></div>
    </div>`;
  document.body.appendChild(ov);
  const body = ov.querySelector("[data-body]");
  let closed = false;
  let busy = false;
  const close = () => {
    if (closed || busy) return;
    closed = true;
    document.removeEventListener("keydown", onEscape);
    ov.remove();
  };
  const onEscape = (e) => { if (e.key === "Escape") close(); };
  document.addEventListener("keydown", onEscape);
  ov.querySelectorAll("[data-close]").forEach((b) => b.addEventListener("click", close));
  ov.addEventListener("mousedown", (e) => { if (e.target === ov) close(); });

  const error = (err) => {
    if (!closed) body.innerHTML = `<p class="muted">${esc(t("blueprint.failed"))}: ${esc(err.message || err)}</p>
      <button type="button" class="btn ghost" data-back>${esc(t("blueprint.back"))}</button>`;
    body.querySelector("[data-back]")?.addEventListener("click", () => { void list(); });
  };

  const list = async () => {
    body.innerHTML = `<p class="muted">${esc(t("blueprint.loading"))}</p>`;
    try {
      const index = await trustedJSON("/api/v1/blueprints");
      if (closed) return;
      const releases = index.releases || [];
      body.innerHTML = `<p class="muted">${esc(t("blueprint.registry"))}: ${esc(index.registryId)}</p>
        <p>${esc(t("blueprint.intro"))}</p>
        ${releases.length ? `<div class="package-list">${releases.map((r, i) => `
          <div class="between" style="padding:9px 0;border-bottom:1px solid var(--border)">
            <div><b>${esc(r.title)}</b> <span class="muted">v${esc(r.version)}</span>
              <div class="muted small">${esc(r.publisherId)} · ${esc(r.license)}</div>
              <div class="small">${esc(r.description)}</div></div>
            <button class="btn ghost" type="button" data-preview="${i}">${esc(t("blueprint.preview"))}</button>
          </div>`).join("")}</div>` : `<p class="muted">${esc(t("blueprint.empty"))}</p>`}`;
      body.querySelectorAll("[data-preview]").forEach((b) => {
        b.addEventListener("click", () => {
          const r = releases[Number(b.dataset.preview)];
          if (r) void preview(r);
        });
      });
    } catch (err) {
      if (!closed) error(err);
    }
  };

  const preview = async (release) => {
    body.innerHTML = `<p class="muted">${esc(t("blueprint.loading"))}</p>`;
    try {
      const inspected = await trustedJSON(route(release.blueprintId, release.version) + "/preview");
      if (closed) return;
      const fileRows = (inspected.files || []).map((file) =>
        `<li>${esc(file.Path ?? file.path)} (${esc(file.SizeBytes ?? file.sizeBytes)} B)</li>`).join("");
      const needs = [
        ...(inspected.needs?.workerTypes || []),
        ...(inspected.needs?.serverConstants || []),
      ];
      // The signed package describes catalogue offers, not import outcomes.
      // Conflicts, local edits and publication are checked later by ADR-0467.
      const products = new Map((inspected.products || []).map((p) => [p.id, p.homeCatalog]));
      const catalogues = (inspected.catalogues || []).map((c) => `
        <li><b>${esc(c.id)}</b>${(c.items || []).length ? `<ul>${c.items.map((id) =>
          `<li>${esc(id)}${products.has(id) && products.get(id) !== c.id ? ` (${esc(products.get(id))})` : ""}</li>`).join("")}</ul>` : ""}</li>`).join("");
      body.innerHTML = `<h3>${esc(inspected.applicationName)}</h3>
        <p class="muted">${esc(inspected.blueprintId)} · v${esc(inspected.version)} · ${esc(inspected.license)}</p>
        <p>${esc(t("blueprint.digest"))}: <code>${esc(inspected.digest)}</code></p>
        <p><b>${esc(t("blueprint.needs"))}:</b> ${esc(needs.join(", ") || t("blueprint.none"))}</p>
        ${catalogues ? `<p><b>${esc(t("transfer.catalogues"))}:</b></p><ul>${catalogues}</ul>` : ""}
        <p><b>${esc(t("blueprint.files"))}:</b></p><ul>${fileRows}</ul>
        <p>${esc(t("blueprint.warning"))}</p>
        <div class="between"><button type="button" class="btn ghost" data-back>${esc(t("blueprint.back"))}</button>
          <button type="button" class="btn" data-install>${esc(t("blueprint.import"))}</button></div>`;
      body.querySelector("[data-back]").addEventListener("click", () => { void list(); });
      const install = body.querySelector("[data-install]");
      install.addEventListener("click", async () => {
        if (busy || !window.confirm(t("blueprint.confirm", { name: inspected.applicationName }))) return;
        busy = true;
        install.disabled = true;
        try {
          const res = await fetch(route(inspected.blueprintId, inspected.version) + "/package", {
            cache: "no-store", headers: { "If-Match": '"' + inspected.digest + '"' },
          });
          if (!res.ok) {
            const data = await res.json().catch(() => ({}));
            throw new Error(data.error || t("blueprint.failed"));
          }
          // The server rechecks its pinned index and inspected digest. Importing
          // a package is still a separate authorized user action, not a GET effect.
          const file = new File([await res.blob()], "blueprint-" + inspected.version + ".tar.gz", {
            type: "application/gzip",
          });
          const out = await importPackage(file, { call, title: t("blueprint.title") });
          if (out) {
            closeAfterImport();
            notify(t("blueprint.imported") + (packageSummary(out) ? " · " + packageSummary(out) : ""), "ok");
            await reload();
          }
        } catch (err) {
          if (!closed) notify(t("blueprint.failed") + ": " + (err.message || err), "err");
        } finally {
          busy = false;
          if (!closed) install.disabled = false;
        }
      });
    } catch (err) {
      if (!closed) error(err);
    }
  };

  const closeAfterImport = () => {
    busy = false;
    close();
  };
  void list();
}
