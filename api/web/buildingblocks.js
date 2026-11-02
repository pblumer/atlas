// The catalogue app's Building blocks page (tshopper phase A;
// ADR-0515).
//
// The services a content source describes, searched from the catalogue's side, each
// with the products that already use it. A page nobody uses is a service described
// and not yet offered; "Create product" makes a draft product from it in a
// catalogue the reader maintains and binds the page to it, in two calls the server
// already has.

import { t } from "./i18n.js";

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// productIdFor answers a product id for a page: its title in lower case, ASCII,
// words joined by hyphens, at most 48 characters — the shape product ids already have.
export function productIdFor(title, key) {
  const base = String(title || "")
    .replace(/ä/gi, "ae").replace(/ö/gi, "oe").replace(/ü/gi, "ue").replace(/ß/g, "ss")
    .normalize("NFKD").replace(/[̀-ͯ]/g, "")
    .toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 48)
    .replace(/-+$/g, "");
  return base || String(key || "baustein").toLowerCase().replace(/[^a-z0-9]+/g, "-");
}

function rows(blocks) {
  if (!blocks.length) return `<p class="muted">${esc(t("blocks.none"))}</p>`;
  return `<table class="table"><thead><tr>
      <th>${esc(t("blocks.col.page"))}</th><th>${esc(t("blocks.col.space"))}</th>
      <th>${esc(t("blocks.col.used"))}</th><th></th></tr></thead><tbody>
    ${blocks.map((b, i) => {
    const used = [
      ...(b.usedBy || []).map((id) => `<code>${esc(id)}</code>`),
      b.usedElsewhere ? esc(t("blocks.elsewhere", { n: b.usedElsewhere })) : "",
    ].filter(Boolean).join(", ");
    return `<tr>
      <td>${b.url ? `<a href="${esc(b.url)}" target="_blank" rel="noopener">${esc(b.title)}</a>` : esc(b.title)}
        <div class="muted">${esc(b.key)}${b.state ? ` · ${esc(b.state)}` : ""}</div></td>
      <td>${esc(b.space)}</td>
      <td>${used || `<span class="muted">${esc(t("blocks.unused"))}</span>`}</td>
      <td><button class="btn ghost" type="button" data-create="${i}">${esc(t("blocks.create"))}</button></td>
    </tr>`;
  }).join("")}</tbody></table>`;
}

export async function viewBuildingBlocks({ api, toast, view, isSuperseded }) {
  let sources = [];
  let cats = [];
  try {
    sources = ((await api("GET", "/api/v1/catalog-building-blocks").catch(() => null)) || {}).sources || [];
    cats = (await api("GET", "/api/v1/catalogs")) || [];
  } catch (e) {
    if (isSuperseded()) return;
    throw e;
  }
  if (isSuperseded()) return;
  const list = sources;
  view.innerHTML = `<div class="row"><h2 style="margin:0">${esc(t("blocks.title"))}</h2></div>
    <p class="muted" style="max-width:70ch">${esc(t("blocks.intro"))}</p>
    ${list.length ? `<form class="bb-search card" style="display:flex; gap:10px; flex-wrap:wrap; align-items:flex-end">
      <label class="field" style="margin:0">${esc(t("blocks.source"))}<select name="source">
        ${list.map((s) => `<option value="${esc(s.id)}">${esc(s.name || s.id)}</option>`).join("")}</select></label>
      <label class="field" style="margin:0; flex:1">${esc(t("blocks.query"))}<input name="q" required autocomplete="off"></label>
      <label class="field" style="margin:0">${esc(t("blocks.into"))}<select name="catalog">
        ${cats.map((c) => `<option value="${esc(c.id)}">${esc(Object.values(c.texts || {})[0] || c.id)}</option>`).join("")}</select></label>
      <button class="btn" type="submit">${esc(t("blocks.search"))}</button>
    </form>
    <div data-blocks style="margin-top:12px"></div>`
    : `<div class="empty"><p>${esc(t("blocks.noSource"))}</p></div>`}`;
  const form = view.querySelector(".bb-search");
  if (!form) return;
  const out = view.querySelector("[data-blocks]");
  let found = [];
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = new FormData(form);
    out.innerHTML = `<p class="muted">${esc(t("blocks.searching"))}</p>`;
    try {
      const q = new URLSearchParams({ source: f.get("source"), q: f.get("q") });
      const got = await api("GET", `/api/v1/catalog-building-blocks?${q}`);
      found = got.blocks || [];
      out.innerHTML = rows(found);
    } catch (err) {
      out.innerHTML = `<p class="muted">${esc(err.message)}</p>`;
    }
  });
  out.addEventListener("click", async (e) => {
    const b = e.target.closest("button[data-create]");
    if (!b) return;
    const page = found[Number(b.dataset.create)];
    const f = new FormData(form);
    const cat = cats.find((c) => c.id === f.get("catalog"));
    if (!page || !cat) return;
    const lang = (cat.languages || ["de"])[0];
    const id = productIdFor(page.title, page.key);
    try {
      await api("POST", "/api/v1/catalog-products", {
        id, homeCatalog: cat.id, state: "draft", texts: { [lang]: page.title },
        approval: { kind: "none" },
      });
      await api("POST", `/api/v1/catalog-products/${encodeURIComponent(id)}/content`,
        { source: f.get("source"), primary: lang, pages: { [lang]: page.id } });
      toast(t("blocks.created", { id }));
      location.hash = `#/catalog/c/${encodeURIComponent(cat.id)}`;
    } catch (err) {
      toast(err.message, "err");
    }
  });
}
