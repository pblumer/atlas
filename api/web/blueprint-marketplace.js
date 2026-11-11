// atlas hub: design-time browsing of signed application Blueprints (ADR-0499).
// The registry is configured and
// verified by the server. We never use a URL or signing key supplied by a
// catalogue, and never install before the operator confirms the preview.
//
// The actual import is ADR-0467's existing packageimport.js workflow — including
// dry-run conflict decisions, caller authorization, optional publish and needs.
import { importPackage, packageSummary } from "./packageimport.js";
import { connectToHub, explainStop, openRoute, routeSummary } from "./hub-connection.js";
import { t } from "./i18n.js";

const esc = (value) => String(value == null ? "" : value).replace(/[&<>"']/g,
  (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const route = (id, version) => "/api/v1/blueprints/" + encodeURIComponent(id) +
  "/releases/" + encodeURIComponent(version);

// refused is the error a refused registry request throws. When the registry could
// not be read, the server names the station the way ended at; the error carries it,
// so the dialog can say where and why instead of the server's one English sentence.
function refused(data) {
  const err = new Error(data.error || t("blueprint.failed"));
  if (data.station && data.station.stage) {
    err.station = data.station;
    err.host = data.registryHost || "";
  }
  return err;
}

async function trustedJSON(url) {
  const res = await fetch(url, { cache: "no-store" });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw refused(data);
  return data;
}

// installations reads what this server installed from the hub, by Blueprint, with
// each installation's components as the server holds them now (ADR-0499 amendment
// h). A server that cannot answer — an older one, or a refusal — shows nothing.
async function installations() {
  try {
    const res = await fetch("/api/v1/blueprints/installations", { cache: "no-store" });
    if (!res.ok) return new Map();
    const list = await res.json();
    return new Map((Array.isArray(list) ? list : []).map((i) => [i.blueprintId, i]));
  } catch {
    return new Map();
  }
}

const installedStatus = (inst) => t("blueprint.installed.status." + inst.status);

// installedSection says what of a release this server holds: which version from
// where, its status, and each component as it is here.
function installedSection(inst) {
  const rows = (inst.components || []).map((c) => {
    const ok = c.status === "present" || c.status === "published";
    return `<li data-component="${esc(c.kind + ":" + c.id)}" data-state="${esc(c.status)}">
      ${esc(t("blueprint.installed.kind." + c.kind))} <code>${esc(c.id)}</code>${c.version ? " v" + esc(c.version) : ""}:
      <span class="${ok ? "muted" : "chip small sv-warn"}">${esc(t("blueprint.installed.state." + c.status))}</span></li>`;
  }).join("");
  const hint = inst.status === "complete" ? "" :
    `<p class="small" data-installed-hint>${esc(t("blueprint.installed.hint." + inst.status))}</p>`;
  return `<section data-installation="${esc(inst.status)}">
      <p><b>${esc(t("blueprint.installed.title"))}</b></p>
      <p class="small">${esc(t("blueprint.installed.line", { version: inst.version, registry: inst.registryId,
        digest: inst.digest, status: installedStatus(inst) }))}</p>
      ${hint}
      ${rows ? `<ul class="small">${rows}</ul>` : ""}
    </section>`;
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
  // What a preview holds beyond its markup: the sketch's object URL and the diagram
  // viewer. Released whenever the preview is left, so neither outlives what it shows.
  let sketchURL = null;
  let viewer = null;
  const releaseExtras = () => {
    if (sketchURL) { URL.revokeObjectURL(sketchURL); sketchURL = null; }
    if (viewer) { viewer.destroy(); viewer = null; }
  };
  const close = () => {
    if (closed || busy) return;
    closed = true;
    releaseExtras();
    document.removeEventListener("keydown", onEscape);
    ov.remove();
  };
  const onEscape = (e) => { if (e.key === "Escape") close(); };
  document.addEventListener("keydown", onEscape);
  ov.querySelectorAll("[data-close]").forEach((b) => b.addEventListener("click", close));
  ov.addEventListener("mousedown", (e) => { if (e.target === ov) close(); });

  // error replaces the dialog's content with what went wrong. Where the registry
  // could not be read, it says at which station the way ended and what that cause
  // usually means, as the route does, and offers to check the whole way again.
  const error = (err) => {
    if (closed) return;
    if (err && err.station) {
      const stop = explainStop(err.station, err.host);
      body.innerHTML = `<p class="muted" data-stopped>${esc(t("blueprint.failed"))}. ${esc(stop.line)}</p>
        ${stop.html}
        <div class="between" style="margin-top:10px">
          <button type="button" class="btn ghost" data-back>${esc(t("blueprint.back"))}</button>
          <button type="button" class="btn ghost" data-retry>${esc(t("blueprint.route.retry"))}</button>
        </div>`;
      body.querySelector("[data-retry]").addEventListener("click", () => { void list(true); });
    } else {
      body.innerHTML = `<p class="muted">${esc(t("blueprint.failed"))}: ${esc(err.message || err)}</p>
        <button type="button" class="btn ghost" data-back>${esc(t("blueprint.back"))}</button>`;
    }
    body.querySelector("[data-back]")?.addEventListener("click", () => { void list(); });
  };

  // The tag a person filters the listing by, kept while the dialog is open.
  // Tags only steer what is shown; they are signed with the index, and the
  // server decides nothing by them.
  let activeTag = "";
  // What this server installed from the hub, by Blueprint, read with each listing.
  let installed = new Map();
  const chips = (tags) => (tags || []).map((tag) =>
    `<span class="chip small" data-tag-chip="${esc(tag)}">${esc(tag)}</span>`).join(" ");

  // The way to the hub, as the last connection passed it: its lines, replayed
  // into the folded route above the listing, and its done line with the index.
  let connection = null;

  // connect draws the route live while the server loads the signed index, and
  // leaves it standing, with what the stop usually means, where the way ends.
  const connect = async () => {
    body.innerHTML = `<div data-route></div>`;
    const route = openRoute(body.querySelector("[data-route]"));
    const events = [];
    try {
      const done = await connectToHub((event) => {
        events.push(event);
        if (!closed) route.update(event);
      });
      if (closed) return false;
      if (!done.ok) {
        body.insertAdjacentHTML("beforeend", `<div class="between" style="margin-top:10px"><span></span>
          <button type="button" class="btn ghost" data-retry>${esc(t("blueprint.route.retry"))}</button></div>`);
        body.querySelector("[data-retry]").addEventListener("click", () => { void list(true); });
        return false;
      }
      const start = events.find((e) => e.type === "start") || {};
      connection = { events, done, host: start.host || "", steps: events.filter((e) => e.type === "step") };
      return true;
    } catch (err) {
      route.stop();
      if (!closed) error(err);
      return false;
    }
  };

  const list = async (reconnect = false) => {
    releaseExtras();
    if ((!connection || reconnect) && !(await connect())) return;
    try {
      if (closed) return;
      const index = connection.done.index || {};
      const tooOld = new Map((connection.done.incompatible || []).map((r) => [r.blueprintId + "@" + r.version, r.minAtlas]));
      const releases = index.releases || [];
      installed = await installations();
      if (closed) return;
      const allTags = [...new Set(releases.flatMap((r) => r.tags || []))].sort();
      if (activeTag && !allTags.includes(activeTag)) activeTag = "";
      const shown = releases
        .map((r, i) => ({ r, i }))
        .filter(({ r }) => !activeTag || (r.tags || []).includes(activeTag));
      const filter = allTags.length ? `<div class="blueprint-tags" role="group" aria-label="${esc(t("blueprint.tags"))}"
          style="display:flex;flex-wrap:wrap;gap:6px;margin:6px 0 10px">
          <button type="button" class="btn ghost small toggle" data-tag="" aria-pressed="${activeTag === ""}">${esc(t("blueprint.allTags"))}</button>
          ${allTags.map((tag) => `<button type="button" class="btn ghost small toggle" data-tag="${esc(tag)}" aria-pressed="${activeTag === tag}">${esc(tag)}</button>`).join("")}
        </div>` : "";
      body.innerHTML = `<details class="fold-sub hub-connected">
          <summary class="small">${esc(routeSummary(connection.done, connection.steps, connection.host))}</summary>
          <div data-route></div>
          <div class="between"><span></span><button type="button" class="btn ghost small" data-retry>${esc(t("blueprint.route.retry"))}</button></div>
        </details>
        <p class="muted">${esc(t("blueprint.registry"))}: ${esc(index.registryId)}</p>
        <p>${esc(t("blueprint.intro"))}</p>
        ${filter}
        ${shown.length ? `<div class="package-list">${shown.map(({ r, i }) => `
          <div class="between" style="padding:9px 0;border-bottom:1px solid var(--border)">
            <div><b>${esc(r.title)}</b> <span class="muted">v${esc(r.version)}</span>
              <div class="muted small">${esc(r.publisherId)} · ${esc(r.license)}</div>
              <div class="small">${esc(r.description)}</div>
              ${(r.tags || []).length ? `<div class="small" style="margin-top:4px">${chips(r.tags)}</div>` : ""}
              ${installed.has(r.blueprintId) ? `<div class="small" style="margin-top:4px"><span class="chip small${installed.get(r.blueprintId).status === "complete" ? "" : " sv-warn"}" data-installed="${esc(installed.get(r.blueprintId).status)}">${esc(t("blueprint.installed.chip", { version: installed.get(r.blueprintId).version, status: installedStatus(installed.get(r.blueprintId)) }))}</span></div>` : ""}
              ${tooOld.has(r.blueprintId + "@" + r.version) ? `<div class="small" style="margin-top:4px"><span class="chip small sv-warn" data-too-old>${esc(t("blueprint.route.needsAtlas", { min: tooOld.get(r.blueprintId + "@" + r.version) || "?", version: connection.done.atlasVersion }))}</span></div>` : ""}</div>
            <button class="btn ghost" type="button" data-preview="${i}"${tooOld.has(r.blueprintId + "@" + r.version) ? " disabled" : ""}>${esc(t("blueprint.preview"))}</button>
          </div>`).join("")}</div>` : `<p class="muted">${esc(t("blueprint.empty"))}</p>`}`;
      const folded = openRoute(body.querySelector(".hub-connected [data-route]"));
      connection.events.forEach((event) => folded.update(event));
      body.querySelector(".hub-connected [data-retry]").addEventListener("click", () => { void list(true); });
      body.querySelectorAll("[data-tag]").forEach((b) => {
        b.addEventListener("click", () => {
          activeTag = b.dataset.tag || "";
          void list();
        });
      });
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
        ${installed.has(inspected.blueprintId) ? installedSection(installed.get(inspected.blueprintId)) : ""}
        <section data-architecture>
          <p><b>${esc(t("blueprint.architecture.title"))}</b></p>
          <p class="muted small">${esc(t("blueprint.architecture.hint"))}</p>
          <div data-sketch><p class="muted small">${esc(t("blueprint.architecture.loading"))}</p></div>
        </section>
        <section data-diagrams>
          <p><b>${esc(t("blueprint.diagrams.title"))}</b></p>
          <p class="muted small">${esc(t("blueprint.diagrams.hint"))}</p>
          <div data-diagram-picker><button type="button" class="btn ghost small" data-show-diagrams>${esc(t("blueprint.diagrams.show"))}</button></div>
          <div class="tp-canvas" data-diagram hidden></div>
        </section>
        <p><b>${esc(t("blueprint.files"))}:</b></p><ul>${fileRows}</ul>
        <p>${esc(t("blueprint.warning"))}</p>
        <div class="between"><button type="button" class="btn ghost" data-back>${esc(t("blueprint.back"))}</button>
          <button type="button" class="btn" data-install>${esc(t("blueprint.import"))}</button></div>`;
      body.querySelector("[data-back]").addEventListener("click", () => { void list(); });
      void showSketch(inspected);
      body.querySelector("[data-show-diagrams]").addEventListener("click", () => { void showDiagrams(inspected); });
      const install = body.querySelector("[data-install]");
      install.addEventListener("click", async () => {
        if (busy || !window.confirm(t("blueprint.confirm", { name: inspected.applicationName }))) return;
        busy = true;
        install.disabled = true;
        try {
          const res = await fetch(route(inspected.blueprintId, inspected.version) + "/package", {
            cache: "no-store", headers: { "If-Match": '"' + inspected.digest + '"' },
          });
          if (!res.ok) throw refused(await res.json().catch(() => ({})));
          // The server rechecks its pinned index and inspected digest. Importing
          // a package is still a separate authorized user action, not a GET effect.
          const file = new File([await res.blob()], "blueprint-" + inspected.version + ".tar.gz", {
            type: "application/gzip",
          });
          // The application itself is installed by the server's hub import, not from
          // the bytes this page holds: the server fetches the release again, holds it
          // to the previewed digest and records where the application came from. The
          // package read here supplies the constants and catalogues beside it.
          const out = await importPackage(file, {
            call, title: t("blueprint.title"),
            importSource: (query) => call("POST", pinned(inspected, "/import", query ? "&" + query : "")),
          });
          if (out) {
            closeAfterImport();
            notify(t("blueprint.imported") + (packageSummary(out) ? " · " + packageSummary(out) : ""), "ok");
            await reload();
          }
        } catch (err) {
          const reason = err && err.station ? explainStop(err.station, err.host).line : (err.message || err);
          if (!closed) notify(t("blueprint.importFailed") + ": " + reason, "err");
        } finally {
          busy = false;
          if (!closed) install.disabled = false;
        }
      });
    } catch (err) {
      if (!closed) error(err);
    }
  };

  // The sketch and the models are read for the digest this preview reported, so what
  // they show is the package the person is looking at (ADR-0539).
  const pinned = (inspected, tail, extra = "") => route(inspected.blueprintId, inspected.version) + tail +
    "?digest=" + encodeURIComponent(inspected.digest) + extra;

  // showSketch draws the server's sketch of the package as an image: the SVG is the
  // server's own rendering, carries no script and is shown through an <img>, which runs
  // none either. A failure says so in place and leaves the rest of the preview usable.
  const showSketch = async (inspected) => {
    const slot = body.querySelector("[data-sketch]");
    try {
      const res = await fetch(pinned(inspected, "/architecture.svg", "&layers=application,business,data"), { cache: "no-store" });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || res.statusText);
      }
      const svg = await res.blob();
      if (closed || !slot.isConnected) return;
      if (sketchURL) URL.revokeObjectURL(sketchURL);
      sketchURL = URL.createObjectURL(new Blob([svg], { type: "image/svg+xml" }));
      const file = "architecture-" + String(inspected.blueprintId).replace(/[^A-Za-z0-9._-]+/g, "-") + "-" +
        String(inspected.version).replace(/[^A-Za-z0-9._-]+/g, "-") + ".svg";
      slot.innerHTML = `<div style="overflow:auto; background:#fff; border:1px solid var(--border); border-radius:var(--radius)">
          <img data-sketch-img alt="${esc(t("blueprint.architecture.alt", { name: inspected.applicationName }))}" style="display:block; max-width:100%; height:auto">
        </div>
        <div class="between" style="margin-top:6px"><span></span>
          <a class="btn ghost small" data-sketch-save download="${esc(file)}">${esc(t("blueprint.architecture.save"))}</a></div>`;
      slot.querySelector("[data-sketch-img]").src = sketchURL;
      slot.querySelector("[data-sketch-save]").href = sketchURL;
    } catch (err) {
      if (!closed && slot.isConnected) {
        slot.innerHTML = `<p class="muted small" data-sketch-failed>${esc(t("blueprint.architecture.failed"))}: ${esc(err.message || err)}</p>`;
      }
    }
  };

  // showDiagrams loads the package's models on request — a further download of the
  // package, so not before somebody asks — and draws the chosen one read-only.
  const showDiagrams = async (inspected) => {
    const picker = body.querySelector("[data-diagram-picker]");
    const canvas = body.querySelector("[data-diagram]");
    picker.innerHTML = `<p class="muted small">${esc(t("blueprint.diagrams.loading"))}</p>`;
    let processes;
    try {
      processes = (await trustedJSON(pinned(inspected, "/diagrams"))).processes || [];
    } catch (err) {
      if (!closed && picker.isConnected) picker.innerHTML = `<p class="muted small">${esc(t("blueprint.diagrams.failed"))}: ${esc(err.message || err)}</p>`;
      return;
    }
    if (closed || !picker.isConnected) return;
    if (!processes.length) {
      picker.innerHTML = `<p class="muted small">${esc(t("blueprint.diagrams.none"))}</p>`;
      return;
    }
    picker.innerHTML = `<div role="group" style="display:flex;flex-wrap:wrap;gap:6px;margin:0 0 8px">${processes.map((p, i) =>
      `<button type="button" class="btn ghost small toggle" data-diagram-pick="${i}" aria-pressed="false">${esc(p.name || p.processId)}</button>`).join("")}</div>`;
    const draw = async (i) => {
      picker.querySelectorAll("[data-diagram-pick]").forEach((b) => b.setAttribute("aria-pressed", String(Number(b.dataset.diagramPick) === i)));
      if (viewer) { viewer.destroy(); viewer = null; }
      canvas.hidden = false;
      canvas.innerHTML = `<p class="tp-msg muted">${esc(t("blueprint.diagrams.loading"))}</p>`;
      try {
        const { mountDiagramPreview } = await import("./editor.js");
        const handle = await mountDiagramPreview(canvas, processes[i].xml);
        if (closed || !canvas.isConnected) { handle.destroy(); return; }
        viewer = handle;
      } catch (err) {
        if (!closed && canvas.isConnected) canvas.innerHTML = `<p class="tp-msg muted" data-diagram-failed>${esc(t("blueprint.diagrams.failed"))}: ${esc(err.message || err)}</p>`;
      }
    };
    picker.querySelectorAll("[data-diagram-pick]").forEach((b) => b.addEventListener("click", () => { void draw(Number(b.dataset.diagramPick)); }));
    await draw(0);
  };

  const closeAfterImport = () => {
    busy = false;
    close();
  };
  void list();
}
