// varrefs.js — what a variable's value names, for the replay's Variables tab.
//
// A process carries people, processes and products as identifiers. For people that is
// a rule rather than a habit (ADR-0314): an order, an entitlement and a variable name a
// person by principal id and never by name, so the history holds nothing an erasure
// would have to reach. The tab printed those ids as stored, and an operator reading
// `orderer = usr_3b0e…` had to look the account up somewhere else.
//
// This answers the id when the tab is drawn — the moment ADR-0314 names for resolving
// one — and keeps the answer in memory for as long as the view is open. Nothing it
// resolves is written anywhere: not into a variable, not into the browser's storage.
//
// Each lookup reads a directory the server already serves to the caller, so what
// resolves is exactly what the caller may read: the principals any signed-in person
// reads (ADR-0073), the deployed processes, and the products of the catalogues the
// caller maintains. An id outside that is left as it was. The server stays the
// authority on every link, too: one is offered only for a role that reaches its page.
//
// Only a whole value is a reference. A filter that contains an id, or a sentence that
// mentions one, is a string about an id, and naming it would read as the variable
// holding a person.

import { t } from "./i18n.js";

// A principal id as the server mints it: a kind prefix and random hex
// (newUserID, newGroupID). Recognised by shape because the shape is the contract — the
// prefix exists to make an id self-describing in logs and URLs.
const PRINCIPAL = /^(usr|grp)_[0-9a-f]{8,64}$/;

// What a process or product id can look like. Both are chosen identifiers rather than
// minted ones, so the shape only narrows what is worth looking up; the directory
// decides. `#` is the separator of a position id, <product>#<variant>.
const IDENTIFIER = /^[A-Za-z0-9][A-Za-z0-9_.:#-]{1,199}$/;

// readerLanguages is what a reader has a chance with, best first: their own, then the
// two the Console itself is written in — the order the inbox names products in.
const readerLanguages = () => [(navigator.language || "en").slice(0, 2).toLowerCase(), "en", "de"];

// textIn picks a catalogue text by language and not by the whole tag: a catalogue
// keyed by de-CH answers a German reader.
function textIn(texts) {
  if (!texts) return "";
  for (const base of readerLanguages()) {
    for (const [tag, said] of Object.entries(texts)) {
      if (said && String(tag).toLowerCase().split("-")[0] === base) return said;
    }
  }
  return Object.values(texts).find((v) => v) || "";
}

// createRefResolver returns the lookups one view shares.
//
// refOf(value) answers synchronously from what has been read so far, and null when the
// value names nothing known — including while a directory is still being read.
// want(values) starts reading the directories those values could be found in, each at
// most once per resolver; onLoad runs whenever one of them arrives, so the view can
// draw again. A directory that cannot be read answers nothing for the rest of the view
// rather than being asked again on every redraw.
//
// mayUse(role) is the Console's own role check (app.js): it decides whether a link is
// offered, never whether a name is shown — the directory already decided that.
export function createRefResolver({ api, mayUse = () => true, onLoad = () => {} }) {
  let principals = null; // Map id → {type, name}, or false once a read failed
  let processes = null;  // Map processId → newest deployment
  let products = null;   // Map item id → product
  let principalsAsked = false;
  let catalogueAsked = false;

  const read = (url, build, set) => api("GET", url)
    .then((list) => set(build(Array.isArray(list) ? list : [])))
    .catch(() => set(false))
    .then(() => onLoad());

  function want(values) {
    const strings = values.filter((v) => typeof v === "string");
    if (!principalsAsked && strings.some((v) => PRINCIPAL.test(v))) {
      principalsAsked = true;
      read("/api/v1/principals",
        (list) => new Map(list.map((p) => [p.id, p])),
        (m) => { principals = m; });
    }
    if (!catalogueAsked && strings.some((v) => !PRINCIPAL.test(v) && IDENTIFIER.test(v))) {
      catalogueAsked = true;
      read("/api/v1/processes", newestByProcess, (m) => { processes = m; });
      read("/api/v1/catalog-products",
        (list) => new Map(list.map((it) => [it.id, it])),
        (m) => { products = m; });
    }
  }

  function refOf(value) {
    if (typeof value !== "string") return null;
    if (PRINCIPAL.test(value)) {
      if (!principals) return null; // not read yet, or unreadable: nothing is claimed
      const group = value.startsWith("grp_");
      const p = principals.get(value);
      // The directory lists enabled accounts only, so a user id it does not answer is an
      // account deleted since or disabled — it cannot tell which, and says both. A group
      // has no disabled state: one it does not list was deleted.
      if (!p) return { kind: group ? "group" : "user", id: value, missing: true };
      return { kind: p.type === "group" ? "group" : "user", id: value, name: p.name || value };
    }
    if (!IDENTIFIER.test(value)) return null;
    const dep = processes && processes.get(value);
    if (dep) {
      return {
        kind: "process", id: value, name: dep.name || value, version: dep.version,
        href: mayUse("operator") ? `#/operations/p/${dep.key}` : "",
      };
    }
    if (products) {
      const cut = value.indexOf("#");
      const itemId = cut < 0 ? value : value.slice(0, cut);
      const it = products.get(itemId);
      if (it) {
        const variantId = cut < 0 ? "" : value.slice(cut + 1);
        const variant = variantId ? (it.variants || []).find((x) => x.id === variantId) : null;
        return {
          kind: "product", id: value, name: textIn(it.texts) || itemId,
          variant: variantId ? (variant && textIn(variant.texts)) || variantId : "",
          catalog: it.homeCatalog || "",
          href: it.homeCatalog && mayUse("productmanager") ? `#/catalog/c/${encodeURIComponent(it.homeCatalog)}` : "",
        };
      }
    }
    return null;
  }

  return { want, refOf };
}

// newestByProcess keeps one deployment per process id, the highest version: a model
// names a process by its id and starts whatever is deployed newest, so that is the
// version a reference to it means.
function newestByProcess(list) {
  const out = new Map();
  for (const d of list) {
    if (!d || !d.processId) continue;
    const had = out.get(d.processId);
    if (!had || (d.version || 0) > (had.version || 0)) out.set(d.processId, d);
  }
  return out;
}

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const KIND_LABEL = { user: "pnl.feel.ref.person", group: "pnl.feel.ref.group", process: "pnl.feel.ref.process", product: "pnl.feel.ref.product" };

const missingLabel = (ref) => (ref.kind === "group" ? t("pnl.feel.ref.noGroup") : t("pnl.feel.ref.noAccount"));

const ICON = {
  user: '<path d="M8 8a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5Z"/><path d="M3 14c.6-2.6 2.6-4 5-4s4.4 1.4 5 4"/>',
  group: '<path d="M6 7.5a2 2 0 1 0 0-4 2 2 0 0 0 0 4Z"/><path d="M1.8 13c.5-2.2 2.1-3.4 4.2-3.4s3.7 1.2 4.2 3.4"/><path d="M10.5 3.6a2 2 0 0 1 0 3.8M11.8 9.7c1.3.4 2.1 1.5 2.4 3.3"/>',
  process: '<rect x="1.5" y="4.5" width="13" height="7" rx="2"/><path d="M5 8h6M9 6l2 2-2 2"/>',
  product: '<path d="M8 1.8 14 5v6l-6 3.2L2 11V5l6-3.2Z"/><path d="M2 5l6 3.2L14 5M8 8.2v6"/>',
};

// refChipHTML is the mark a reference gets beside its value: an icon and what it is
// called, a link where the reader may open it. data-ref carries the value back to the
// popover, which reads the resolver again rather than trusting markup.
export function refChipHTML(ref) {
  if (!ref) return "";
  const icon = `<svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">${ICON[ref.kind] || ""}</svg>`;
  const label = ref.missing
    ? missingLabel(ref)
    : ref.variant ? `${ref.name} · ${ref.variant}` : ref.name;
  const cls = `c-ref c-ref-${ref.kind}${ref.missing ? " missing" : ""}`;
  const inner = `${icon}<span class="c-ref-name">${esc(label)}</span>`;
  return ref.href
    ? ` <a class="${cls}" href="${esc(ref.href)}" data-ref="${esc(ref.id)}">${inner}</a>`
    : ` <span class="${cls}" tabindex="0" data-ref="${esc(ref.id)}">${inner}</span>`;
}

// refPopHTML is what hovering a reference explains: what kind of thing the id names,
// the id itself, and the one caveat that matters on a replay — the name is read now,
// while the step it sits in is past.
export function refPopHTML(ref) {
  if (!ref) return "";
  const kind = esc(t(KIND_LABEL[ref.kind] || "pnl.feel.ref.reference"));
  const id = `<div class="ref-pop-id">${esc(ref.id)}</div>`;
  if (ref.missing) {
    const why = ref.kind === "group"
      ? t("pnl.feel.ref.groupGone")
      : t("pnl.feel.ref.accountGone");
    return `<div class="pop-title">${kind} · ${esc(missingLabel(ref))}</div>${id}
      <p class="ref-pop-note">${esc(why)}</p>`;
  }
  let facts = "";
  if (ref.kind === "process") facts = esc(t("pnl.feel.ref.version", { version: String(ref.version || "—") }));
  if (ref.kind === "product") {
    facts = [ref.variant ? esc(t("pnl.feel.ref.variant", { variant: ref.variant })) : "", ref.catalog ? esc(t("pnl.feel.ref.catalogue", { catalog: ref.catalog })) : ""]
      .filter(Boolean).join(" · ");
  }
  const note = ref.kind === "user" || ref.kind === "group"
    ? t("pnl.feel.ref.noteAccount")
    : ref.kind === "process"
      ? t("pnl.feel.ref.noteProcess")
      : t("pnl.feel.ref.noteProduct");
  const go = ref.href ? `<p class="ref-pop-go">${esc(t("pnl.feel.ref.open"))}</p>` : "";
  return `<div class="pop-title">${esc(ref.name)}</div>
    <div class="ref-pop-kind">${kind}${facts ? ` · ${facts}` : ""}</div>${id}
    <p class="ref-pop-note">${esc(note)}</p>${go}`;
}
