// A product's content from the wiki, on the catalogue screen
// (ADR-draft-service-content-from-an-external-source, § 5–§ 9).
//
// The backend binds a product to one wiki page per catalogue language, copies the
// description, keywords and picture into the draft, rechecks the pages' protection
// on its own and withholds the content when a recheck fails. This module is the
// product manager's half of it: link, preview, refresh, unlink, and what the recheck
// found. Three rules shape it.
//
//   - **Nothing appears without a source.** The panel asks the product's content
//     status first and draws nothing when no source is configured, so an
//     installation that never configured one sees exactly the screen it had (§ 3).
//   - **Wiki text is text.** Every title, label, reason and the converted
//     description come from somebody else's system. They reach the page through
//     el(), which only ever creates text nodes, and never through innerHTML — the
//     record's "foreign content is never markup" held on this side too.
//   - **Every refusal is shown whole.** A link the server refuses answers with every
//     problem of every slot at once, and so does the preview: the list is the work,
//     as it is for a refused publish.

import { t } from "./i18n.js";
import { fmtDateTime } from "./datefmt.js";

const enc = encodeURIComponent;

// el builds an element from text. Attributes are set as attributes and children are
// appended as nodes or as text nodes — there is no path through it to markup, which
// is the point: everything this module draws may carry a wiki page's words.
export function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === "class") node.className = v;
    else if (k.startsWith("on") && typeof v === "function") node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v === true ? "" : String(v));
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    node.append(c.nodeType ? c : document.createTextNode(String(c)));
  }
  return node;
}

// fmtNs prints one of the catalogue's timestamps, which are Unix nanoseconds.
export const fmtNs = (ns) => (ns ? fmtDateTime(Math.floor(Number(ns) / 1e6)) : "—");

// RECHECK_INTERVAL and STALE_AFTER mirror api/catalog/contentstate.go: a recheck
// runs every fifteen minutes, publishing refuses past one interval, and content
// whose last passed recheck is older than two is withheld at read time.
const RECHECK_INTERVAL = 15 * 60 * 1e9;
const STALE_AFTER = 2 * RECHECK_INTERVAL;

// wikiHref is a page address the screen may link to. The adapter builds it from the
// source's https base URL, and this is the second half of that rule: an address that
// is not https is not a link here, whatever put it in the record.
export function wikiHref(url) {
  const s = String(url || "");
  return /^https:\/\/[^\s]+$/i.test(s) ? s : "";
}

// wikiLink is a link out to a wiki page: a new tab, and no window.opener for the page
// it opens.
function wikiLink(url, label) {
  const href = wikiHref(url);
  if (!href) return el("span", {}, label);
  return el("a", { href, target: "_blank", rel: "noopener noreferrer" }, label);
}

// problemList renders the slot problems a refused link or refresh answers with.
function problemList(problems) {
  return el("ul", { class: "content-problems" }, (problems || []).map((p) =>
    el("li", {}, [p.lang ? `${p.lang}: ` : "", p.page ? `${p.page} — ` : "", p.message || ""].join(""))));
}

// failure is a refused write as the panel shows it: every problem the server named,
// or its message when it named none.
function failure(head, err) {
  const problems = ((err && err.body) || {}).problems || [];
  return el("div", { class: "content-failure", role: "alert" },
    el("b", {}, head),
    problems.length ? problemList(problems)
      : el("p", { class: "muted" }, (err && err.message) || `HTTP ${(err && err.status) || "?"}`));
}

// askReason is the dialog every unlink goes through: the server requires a reason
// and records it in the audit log with who and when, and the dialog says so before
// anybody types. Resolves to the reason, or null on cancel.
export function askReason({ title, what }) {
  return new Promise((resolve) => {
    const area = el("textarea", { rows: "3", required: true, "aria-label": t("content.unlink.reason") });
    const hint = el("p", { class: "content-reason-missing", hidden: true }, t("content.unlink.reasonNeeded"));
    const ok = el("button", { type: "button", class: "btn danger" }, t("content.unlink.ok"));
    const cancel = el("button", { type: "button", class: "btn ghost" }, t("common.cancel"));
    const close = el("button", { type: "button", class: "icon-btn", "aria-label": t("common.close") }, "✕");
    const ov = el("div", { class: "modal-ov" },
      el("div", { class: "modal", role: "dialog", "aria-modal": "true", "aria-label": title },
        el("div", { class: "modal-head" }, el("h2", {}, title), close),
        el("div", { class: "modal-body" },
          el("p", {}, what),
          el("label", { class: "field" }, el("span", {}, t("content.unlink.reason")), area),
          hint,
          el("p", { class: "muted small" }, t("content.unlink.audit"))),
        el("div", { class: "modal-foot" }, el("span"), el("span", { class: "modal-actions" }, cancel, ok))));
    document.body.appendChild(ov);
    let settled = false;
    const finish = (v) => {
      if (settled) return;
      settled = true;
      ov.remove();
      document.removeEventListener("keydown", onKey);
      resolve(v);
    };
    const submit = () => {
      const reason = area.value.trim();
      if (!reason) { hint.hidden = false; area.focus(); return; }
      finish(reason);
    };
    const onKey = (e) => { if (e.key === "Escape") { e.preventDefault(); finish(null); } };
    document.addEventListener("keydown", onKey);
    ov.addEventListener("mousedown", (e) => { if (e.target === ov) finish(null); });
    close.addEventListener("click", () => finish(null));
    cancel.addEventListener("click", () => finish(null));
    ok.addEventListener("click", submit);
    area.focus();
  });
}

// droppedLine says what one dropped thing was, in the reader's words.
function droppedLine(d) {
  const what = d.what === "page" ? t("content.dropped.page")
    : d.what === "passage" ? t("content.dropped.passage")
      : t("content.dropped.block", { what: d.what || "?" });
  const reasons = (d.reasons || []).length ? ` — ${d.reasons.join("; ")}` : "";
  return `${d.page ? `${d.page}: ` : ""}${what}${reasons}`;
}

// previewBox draws one page as linking would take it: what it resolved to, the text
// the shop would show, and everything the rules drop or refuse.
export function previewBox(pv) {
  const page = pv.page || {};
  const refusals = pv.refusals || [];
  const dropped = pv.dropped || [];
  const children = pv.children || [];
  return el("div", { class: `content-preview${refusals.length || pv.boundTo ? " refused" : ""}` },
    el("div", { class: "content-preview-head" },
      el("b", {}, wikiLink(page.url, page.title || page.key || page.ref || pv.input || "")),
      el("span", { class: "muted" }, [
        page.key || page.ref || "",
        pv.space ? ` · ${pv.space}` : "",
        pv.state ? ` · ${t("content.state", { state: pv.state })}` : "",
        page.version ? ` · ${t("content.version", { n: page.version })}` : "",
      ].join(""))),
    refusals.length
      ? el("div", { class: "content-refusals", role: "alert" },
        el("b", {}, t("content.refusals")),
        el("ul", {}, refusals.map((r) => el("li", {}, r))))
      : null,
    pv.boundTo ? el("p", { class: "content-refusals", role: "alert" }, t("content.boundTo", { item: pv.boundTo })) : null,
    !refusals.length && !pv.boundTo ? el("p", { class: "content-passes" }, t("content.passes")) : null,
    dropped.length
      ? el("div", { class: "content-dropped" },
        el("b", {}, t("content.dropped")),
        el("ul", {}, dropped.map((d) => el("li", {}, droppedLine(d)))))
      : null,
    children.length
      ? el("p", { class: "muted" }, `${t("content.children")}: ${children.map((c) => c.title || c.key || c.ref).join(", ")}`)
      : null,
    (pv.keywords || []).length
      ? el("p", { class: "muted" }, `${t("content.keywords")}: ${pv.keywords.join(", ")}`) : null,
    el("div", { class: "content-text-label muted" }, t("content.text")),
    String(pv.text || "").trim()
      ? el("pre", { class: "content-text" }, pv.text)
      : el("p", { class: "muted" }, t("content.noText")));
}

// mountContentPanel draws the panel for one product into host, or nothing when no
// content source is configured. ctx carries the page's api, toast and reload; item
// is the product as the page read it, whose revision every write states.
export async function mountContentPanel(host, ctx, item) {
  if (!host || !item) return;
  let status;
  try {
    status = await ctx.api("GET", `/api/v1/catalog-products/${enc(item.id)}/content`);
  } catch (err) {
    // A product somebody else maintains answers 403 and one this caller cannot
    // read 404: the panel is theirs to show, not this one's. Anything else is a
    // failure worth a line.
    if (err.status !== 403 && err.status !== 404) {
      host.replaceChildren(el("p", { class: "muted" }, t("content.loadFailed", { error: err.message })));
    }
    return;
  }
  const sources = (status && status.sources) || [];
  // No source configured: the binding does not exist on this installation (§ 3).
  if (!sources.length && !(status && status.binding)) { host.replaceChildren(); return; }
  host.replaceChildren(el("section", { class: "card content-panel-card" },
    el("h4", { class: "content-title" }, t("content.title")),
    el("p", { class: "muted content-intro" }, t("content.intro")),
    status.binding ? boundView(ctx, item, status) : linkForm(ctx, item, status)));
}

// written handles a write's answer the same way for every action: a 409 about the
// product having moved reloads the page with the current record, anything else is
// shown where the action was asked.
function stale(ctx, err) {
  if (err && err.status === 409 && /changed (since|while)/.test(err.message || "")) {
    ctx.toast(t("content.stale409"), "err");
    ctx.reload();
    return true;
  }
  return false;
}

// ---------- not bound yet: choose a source, one page per language ----------

function linkForm(ctx, item, status) {
  const sources = status.sources || [];
  const langs = (status.languages || []).length ? status.languages : [];
  const sourceSel = el("select", { "aria-label": t("content.source") },
    sources.map((s) => el("option", { value: s.id }, s.name ? `${s.name} (${s.id})` : s.id)));
  const report = el("div", { class: "content-report", "aria-live": "polite" });
  const primaryGroup = `content-primary-${item.id}`;
  const slots = langs.map((lang, n) => slotRow(ctx, item, lang, n === 0, primaryGroup, () => sourceSel.value));
  const link = el("button", { type: "button", class: "btn" }, t("content.link"));
  link.addEventListener("click", async () => {
    report.replaceChildren();
    const pages = {};
    let primary = "";
    for (const s of slots) {
      const v = s.input.value.trim();
      if (v) pages[s.lang] = v;
      if (s.radio.checked) primary = s.lang;
    }
    if (!Object.keys(pages).length) {
      report.replaceChildren(el("p", { class: "content-failure" }, t("content.noPages")));
      return;
    }
    if (!pages[primary]) {
      report.replaceChildren(el("p", { class: "content-failure" }, t("content.primaryEmpty")));
      return;
    }
    link.disabled = true;
    link.textContent = t("content.linking");
    try {
      await ctx.api("POST", `/api/v1/catalog-products/${enc(item.id)}/content`,
        { source: sourceSel.value, primary, pages, revision: item.revision || 0 });
      ctx.toast(t("content.linked"));
      ctx.reload();
    } catch (err) {
      if (stale(ctx, err)) return;
      report.replaceChildren(failure(t("content.linkFailed"), err));
    } finally {
      link.disabled = false;
      link.textContent = t("content.link");
    }
  });
  return el("div", { class: "content-link" },
    el("label", { class: "field" }, el("span", {}, t("content.source")), sourceSel),
    langs.length ? null : el("p", { class: "content-failure" }, t("content.noLanguages")),
    el("p", { class: "muted" }, t("content.slotsHint")),
    slots.map((s) => s.node),
    el("div", { class: "row" }, link),
    report);
}

// slotRow is one language's slot: the page for it, how to find it, and its preview.
function slotRow(ctx, item, lang, primary, group, source) {
  const input = el("input", { type: "text", autocomplete: "off", spellcheck: "false",
    placeholder: t("content.pagePlaceholder"), "aria-label": t("content.slot", { lang }) });
  const radio = el("input", { type: "radio", name: group, value: lang, checked: primary });
  const results = el("div", { class: "content-hits" });
  const preview = el("div", { class: "content-preview-slot", "aria-live": "polite" });
  const search = el("button", { type: "button", class: "btn ghost" }, t("content.search"));
  const show = el("button", { type: "button", class: "btn ghost" }, t("content.preview"));

  const runPreview = async (page) => {
    preview.replaceChildren(el("p", { class: "muted" }, t("content.reading")));
    try {
      const pv = await ctx.api("GET", `/api/v1/catalog-products/${enc(item.id)}/content/preview?source=${
        enc(source())}&page=${enc(page)}`);
      preview.replaceChildren(previewBox(pv || {}));
    } catch (err) {
      preview.replaceChildren(failure(t("content.previewFailed"), err));
    }
  };
  search.addEventListener("click", async () => {
    const q = input.value.trim();
    if (!q) { input.focus(); return; }
    results.replaceChildren(el("p", { class: "muted" }, t("content.reading")));
    try {
      const hits = (await ctx.api("GET", `/api/v1/catalog-products/${enc(item.id)}/content/search?source=${
        enc(source())}&q=${enc(q)}`)) || [];
      if (!hits.length) { results.replaceChildren(el("p", { class: "muted" }, t("content.noHits"))); return; }
      results.replaceChildren(el("ul", { class: "content-hit-list" }, hits.map((h) => {
        const choose = el("button", { type: "button", class: "btn ghost small" }, t("content.choose"));
        choose.addEventListener("click", () => {
          // The stable id where the hit has one: a key changes when a page moves.
          input.value = h.id || h.key || "";
          results.replaceChildren();
          runPreview(input.value);
        });
        return el("li", {},
          el("b", {}, h.title || h.key || h.id || ""),
          el("span", { class: "muted" }, [
            h.space ? ` · ${h.space}` : "", h.key ? ` · ${h.key}` : "",
            h.state ? ` · ${t("content.state", { state: h.state })}` : ""].join("")),
          " ", choose);
      })));
    } catch (err) {
      results.replaceChildren(failure(t("content.searchFailed"), err));
    }
  });
  show.addEventListener("click", () => {
    const v = input.value.trim();
    if (!v) { input.focus(); return; }
    runPreview(v);
  });
  const node = el("div", { class: "content-slot" },
    el("div", { class: "content-slot-head" },
      el("b", {}, t("content.slot", { lang })),
      el("label", { class: "content-primary", title: t("content.primaryHint") }, radio, " ", t("content.primary"))),
    el("div", { class: "row" }, input, search, show),
    results, preview);
  return { lang, input, radio, node };
}

// ---------- bound: what was copied, what the recheck found, and the actions ----------

// recheckLines says what the recheck record says, including the two ways content is
// withheld without a quarantine: a copy older than a lifted quarantine, and a last
// passed recheck too old to vouch for anything.
export function recheckLines(binding, state, shown, now) {
  const out = [];
  const st = state || null;
  if (!st || !st.lastChecked) out.push({ kind: "warn", text: t("content.recheck.never") });
  else {
    out.push({ kind: "", text: t("content.recheck.lastChecked", { at: fmtNs(st.lastChecked) }) });
    out.push({ kind: "", text: st.lastPassed
      ? t("content.recheck.lastPassed", { at: fmtNs(st.lastPassed) }) : t("content.recheck.neverPassed") });
  }
  if (st && st.quarantine) {
    out.push({ kind: "err", text: t("content.quarantine", { since: fmtNs(st.quarantine.since), reason: st.quarantine.reason || "" }) });
    out.push({ kind: "", text: t("content.quarantineHint") });
  } else if (st && st.distrusted) {
    const oldest = Object.values((binding || {}).pages || {})
      .reduce((m, p) => (m === 0 || (p.fetchedAt && p.fetchedAt < m) ? p.fetchedAt || m : m), 0);
    out.push({ kind: oldest && oldest <= st.distrusted ? "err" : "",
      text: oldest && oldest <= st.distrusted
        ? t("content.distrustedCopy", { since: fmtNs(st.distrusted) })
        : t("content.distrusted", { since: fmtNs(st.distrusted) }) });
  }
  if (!shown && st && !st.quarantine && st.lastPassed && now - st.lastPassed > STALE_AFTER) {
    out.push({ kind: "warn", text: t("content.stale") });
  }
  return out;
}

function boundView(ctx, item, status) {
  const b = status.binding;
  const st = status.state || null;
  const source = (status.sources || []).find((s) => s.id === b.source);
  const pages = b.pages || {};
  const langs = Object.keys(pages).sort((x, y) => (x === b.primary ? -1 : y === b.primary ? 1 : x.localeCompare(y)));
  const report = el("div", { class: "content-report", "aria-live": "polite" });
  // Date.now() is milliseconds; the record is nanoseconds.
  const now = Date.now() * 1e6;

  const unlink = async (lang) => {
    const reason = await askReason({
      title: lang ? t("content.unlink.titleLang", { lang }) : t("content.unlink.title"),
      what: lang ? t("content.unlink.whatLang", { lang }) : t("content.unlink.what"),
    });
    if (!reason) return;
    report.replaceChildren();
    try {
      await ctx.api("POST", `/api/v1/catalog-products/${enc(item.id)}/content/unlink`,
        { ...(lang ? { lang } : {}), reason, revision: item.revision || 0 });
      ctx.toast(t("content.unlinked"));
      ctx.reload();
    } catch (err) {
      if (stale(ctx, err)) return;
      report.replaceChildren(failure(t("content.unlinkFailed"), err));
    }
  };

  const rows = langs.map((lang) => {
    const p = pages[lang] || {};
    const isPrimary = lang === b.primary;
    // The primary page names the picture, so it goes only with the whole binding;
    // the server refuses it, and the button says so rather than inviting it.
    const lone = langs.length === 1;
    const action = el("button", {
      type: "button", class: "btn ghost small danger",
      disabled: isPrimary && !lone,
      title: isPrimary && !lone ? t("content.unlinkPrimary") : null,
    }, t("content.unlinkLang"));
    action.addEventListener("click", () => unlink(lone ? "" : lang));
    return el("tr", {},
      el("td", {}, lang, isPrimary ? el("span", { class: "pill" }, t("content.primary")) : null),
      el("td", {}, wikiLink(p.url, p.title || p.ref || ""), el("div", { class: "muted" }, p.ref || "")),
      el("td", {}, String(p.version || "—")),
      el("td", {}, fmtNs(p.fetchedAt)),
      el("td", {}, (p.children || []).length
        ? (p.children || []).map((c) => c.title || c.ref).join(", ") : "—"),
      el("td", { class: "row-actions" }, action));
  });

  const refresh = el("button", { type: "button", class: "btn" }, t("content.refresh"));
  refresh.addEventListener("click", async () => {
    report.replaceChildren();
    refresh.disabled = true;
    refresh.textContent = t("content.refreshing");
    try {
      await ctx.api("POST", `/api/v1/catalog-products/${enc(item.id)}/content/refresh`, { revision: item.revision || 0 });
      ctx.toast(t("content.refreshed"));
      ctx.reload();
    } catch (err) {
      if (stale(ctx, err)) return;
      report.replaceChildren(failure(t("content.refreshFailed"), err));
    } finally {
      refresh.disabled = false;
      refresh.textContent = t("content.refresh");
    }
  });
  const unlinkAll = el("button", { type: "button", class: "btn ghost danger" }, t("content.unlink"));
  unlinkAll.addEventListener("click", () => unlink(""));

  return el("div", { class: "content-bound" },
    el("p", {},
      t("content.boundSource", { source: source ? (source.name || source.id) : b.source }), " ",
      el("span", { class: `pill ${status.shown ? "ok" : "warn"}` },
        status.shown ? t("content.shown") : t("content.withheld"))),
    el("table", { class: "table no-enhance content-pages" },
      el("thead", {}, el("tr", {},
        el("th", {}, t("content.th.lang")), el("th", {}, t("content.th.page")),
        el("th", {}, t("content.th.version")), el("th", {}, t("content.th.fetched")),
        el("th", {}, t("content.th.children")), el("th", {}))),
      el("tbody", {}, rows)),
    el("div", { class: "content-recheck" },
      el("b", {}, t("content.recheck.title")),
      el("ul", {}, recheckLines(b, st, status.shown, now).map((l) =>
        el("li", { class: l.kind ? `content-${l.kind}` : null }, l.text)))),
    el("div", { class: "row" }, refresh, unlinkAll),
    report);
}

// ---------- the catalogue's refresh of every bound product ----------

// mountCatalogRefresh draws "Refresh all wiki content" for a catalogue that offers at
// least one bound product, and the per-product answer after it ran. Nothing is drawn
// for a catalogue without one: there is nothing to refresh, and on an installation
// without a source no product can be bound.
export function mountCatalogRefresh(host, ctx, catalogID, boundCount) {
  if (!host || !boundCount) return;
  const result = el("div", { class: "content-catalog-result", "aria-live": "polite" });
  const go = el("button", { type: "button", class: "btn" }, t("content.catalog.refresh"));
  go.addEventListener("click", async () => {
    go.disabled = true;
    go.textContent = t("content.catalog.refreshing");
    result.replaceChildren();
    try {
      const lines = (await ctx.api("POST", `/api/v1/catalogs/${enc(catalogID)}/content/refresh`)) || [];
      result.replaceChildren(catalogRefreshReport(lines, ctx));
    } catch (err) {
      result.replaceChildren(failure(t("content.catalog.failed"), err));
    } finally {
      go.disabled = false;
      go.textContent = t("content.catalog.refresh");
    }
  });
  host.replaceChildren(
    el("h3", { style: "margin-top:26px" }, t("content.catalog.title")),
    el("p", { class: "muted", style: "max-width:62ch" }, t("content.catalog.intro")),
    el("div", { class: "row" }, go),
    result);
}

// statusLabel names a refresh line's outcome. Written out per status, so every key
// the screen can ask for is a literal the message-catalogue test can find.
function statusLabel(status) {
  if (status === "refused") return t("content.catalog.status.refused");
  if (status === "skipped") return t("content.catalog.status.skipped");
  return t("content.catalog.status.refreshed");
}

// catalogRefreshReport lists what the refresh did per product: refreshed, refused
// with every problem, or skipped and why.
export function catalogRefreshReport(lines, ctx) {
  const count = (s) => lines.filter((l) => l.status === s).length;
  const again = el("button", { type: "button", class: "btn ghost" }, t("content.catalog.reload"));
  again.addEventListener("click", () => ctx.reload());
  return el("div", { class: "card content-catalog-report" },
    el("b", {}, t("content.catalog.summary", {
      refreshed: count("refreshed"), refused: count("refused"), skipped: count("skipped") })),
    lines.length ? el("ul", {}, lines.map((l) => el("li", { class: `content-line-${l.status}` },
      el("b", {}, l.item), " — ",
      el("span", { class: `pill ${l.status === "refreshed" ? "ok" : l.status === "refused" ? "err" : "off"}` },
        statusLabel(l.status)),
      l.message ? ` ${l.message}` : "",
      (l.problems || []).length ? problemList(l.problems) : null)))
      : el("p", { class: "muted" }, t("content.catalog.none")),
    el("div", { class: "row" }, again));
}
