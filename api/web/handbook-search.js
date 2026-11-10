// Full-text search for the handbooks (handbuch.html, shop-handbuch.html).
//
// The handbooks are single static pages, so the browser's own find (Ctrl+F) already
// covers them — for readers who know it. This puts a search field in the top bar for
// everyone else: it lists the matching passages with their chapter and heading, and
// choosing one scrolls there, opens a folded <details> around it and marks the words.
//
// Everything happens in the page: the index is the page's own text, read on the first
// search and again after the language toggle or a change to <main>. Nothing is sent to
// the server and nothing is stored.
//
// Both languages live in the DOM and the root data-lang hides one (the [data-l] rule in
// each page's CSS). The index only holds the visible half, so a reader is never sent to a
// passage they cannot see.

const root = document.documentElement;
const main = document.querySelector("main");
const topbar = document.querySelector(".topbar");

// The blocks a hit points at. A text node belongs to the closest of these around it, so
// a paragraph inside a list item is its own hit and the item keeps only its own words.
const BLOCK = "h1,h2,h3,h4,h5,h6,p,li,dt,dd,th,td,pre,summary,figcaption,blockquote,label,.gc";
const HEADING = /^(H[1-6]|SUMMARY)$/;
// Never part of the text a reader searches: code that runs, drawings, and this UI.
const SKIP = "script,style,template,noscript,svg,canvas,textarea,input,select,button,.hsearch";
const MAX_RESULTS = 40;
const SNIPPET = 70; // characters of context either side of the first hit

const T = {
  label: { de: "Handbuch durchsuchen", en: "Search the handbook" },
  placeholder: { de: "Suchen …", en: "Search …" },
  none: { de: "Keine Treffer.", en: "No matches." },
  short: { de: "Mindestens zwei Zeichen eingeben.", en: "Type at least two characters." },
  count: { de: (n, more) => more ? `Die ersten ${n} Treffer – genauer suchen grenzt ein.` : (n === 1 ? "1 Treffer" : `${n} Treffer`),
           en: (n, more) => more ? `The first ${n} matches – a more specific search narrows them.` : (n === 1 ? "1 match" : `${n} matches`) },
  hint: { de: "Tipp: «/» öffnet die Suche.", en: "Tip: press “/” to search." },
};
const lang = () => (root.dataset.lang === "en" ? "en" : "de");
const tr = (k) => T[k][lang()];

// fold lowercases and drops accents so "uberblick" finds "Überblick", and spells ß as ss.
// It returns the folded text and, for each folded character, the index of the original
// one, so a hit in the folded text maps back onto the page.
function fold(s) {
  let out = "";
  const map = [];
  for (let i = 0; i < s.length; i++) {
    let f = s[i].toLowerCase();
    if (f === "ß") f = "ss";
    else if (f.charCodeAt(0) > 127) f = f.normalize("NFD").replace(/[̀-ͯ]/g, "");
    if (/\s/.test(f)) f = " ";
    for (const c of f) { out += c; map.push(i); }
  }
  return { text: out, map };
}
const foldQuery = (q) => fold(q).text.split(" ").filter(Boolean);

function visible(el) {
  const l = el.closest("[data-l]");
  return !l || l.dataset.l === lang();
}

// ---- index ----
let index = null;
// A change to the text drops the index; a diagram drawn into its box (the recipes draw
// theirs as they come near the screen) adds nothing anyone searches and keeps it.
const drawn = (n) => n.nodeType === 1 && n.matches(SKIP);
function textChanged(r) {
  const t = r.target.nodeType === 1 ? r.target : r.target.parentElement;
  if (t && t.closest(SKIP)) return false;
  if (r.type !== "childList") return true;
  return [...r.addedNodes, ...r.removedNodes].some((n) => !drawn(n));
}
if (main) new MutationObserver((recs) => { if (recs.some(textChanged)) index = null; })
  .observe(main, { childList: true, subtree: true, characterData: true });

// chapterOf names the section a block sits in by its visible title; `seen` caches it per
// section for one build of the index.
function chapterOf(el, seen) {
  const sec = el.closest("section[id]");
  if (!sec) return "";
  if (seen.has(sec)) return seen.get(sec);
  let name = "";
  for (const h of sec.querySelectorAll("h1,h2")) {
    if (h.closest("section[id]") === sec && visible(h)) { name = h.textContent.replace(/\s+/g, " ").trim(); break; }
  }
  seen.set(sec, name);
  return name;
}

function buildIndex() {
  const blocks = new Map(); // element → its text nodes, in document order
  if (main) {
    const walker = document.createTreeWalker(main, NodeFilter.SHOW_TEXT, {
      acceptNode(n) {
        const p = n.parentElement;
        if (!p || !n.nodeValue.trim() || p.closest(SKIP) || !visible(p)) return NodeFilter.FILTER_REJECT;
        return NodeFilter.FILTER_ACCEPT;
      },
    });
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      const b = n.parentElement.closest(BLOCK) || n.parentElement.closest("div,section") || n.parentElement;
      if (!main.contains(b)) continue;
      if (!blocks.has(b)) blocks.set(b, []);
      blocks.get(b).push(n);
    }
  }
  const out = [];
  const seen = new Map();
  let heading = "";
  let headingChapter = "";
  for (const [el, nodes] of blocks) {
    const raw = nodes.map((n) => n.nodeValue).join("");
    const text = raw.replace(/\s+/g, " ").trim();
    if (!text) continue;
    const isHeading = HEADING.test(el.tagName);
    const chapter = chapterOf(el, seen);
    if (isHeading && el.tagName !== "SUMMARY") { heading = text; headingChapter = chapter; }
    // A heading is the context of the blocks after it only within its own chapter.
    if (headingChapter !== chapter) { heading = ""; headingChapter = chapter; }
    out.push({
      el, nodes, text, chapter,
      heading: isHeading ? "" : heading,
      folded: fold(text).text,
      context: fold(heading + " " + chapter).text,
      isHeading,
      isCode: el.tagName === "PRE",
    });
  }
  return out;
}

function search(q) {
  const terms = foldQuery(q);
  if (!terms.length) return { terms, hits: [] };
  if (!index) index = buildIndex();
  const phrase = terms.join(" ");
  const hits = [];
  index.forEach((b, order) => {
    let inBlock = 0;
    for (const t of terms) {
      if (b.folded.includes(t)) inBlock++;
      else if (!b.context.includes(t)) return; // every word must be here or in its heading
    }
    if (!inBlock) return;
    let score = inBlock * 10;
    if (terms.length > 1 && b.folded.includes(phrase)) score += 15;
    if (b.isHeading) score += 25;
    if (terms.some((t) => new RegExp("(^|[^a-z0-9])" + t.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).test(b.folded))) score += 5;
    if (b.isCode) score -= 12; // the XML of a recipe matches nearly everything
    hits.push({ b, score, order });
  });
  hits.sort((x, y) => y.score - x.score || x.order - y.order);
  return { terms, hits };
}

// ---- rendering ----
const esc = (s) => s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// ranges returns the [start, end) spans of every term in text, in original positions.
function ranges(text, terms) {
  const { text: f, map } = fold(text);
  const out = [];
  for (const t of terms) {
    for (let i = f.indexOf(t); i !== -1; i = f.indexOf(t, i + t.length)) {
      out.push([map[i], map[i + t.length - 1] + 1]);
    }
  }
  out.sort((a, b) => a[0] - b[0]);
  const merged = [];
  for (const r of out) {
    const last = merged[merged.length - 1];
    if (last && r[0] <= last[1]) last[1] = Math.max(last[1], r[1]);
    else merged.push(r.slice());
  }
  return merged;
}

function marked(text, terms) {
  let html = "";
  let at = 0;
  for (const [s, e] of ranges(text, terms)) {
    html += esc(text.slice(at, s)) + "<mark>" + esc(text.slice(s, e)) + "</mark>";
    at = e;
  }
  return html + esc(text.slice(at));
}

function snippet(text, terms) {
  const first = ranges(text, terms)[0];
  if (!first || text.length <= SNIPPET * 2) return marked(text, terms);
  let s = Math.max(0, first[0] - SNIPPET);
  let e = Math.min(text.length, first[1] + SNIPPET);
  if (s > 0) { const sp = text.indexOf(" ", s); if (sp !== -1 && sp < first[0]) s = sp + 1; }
  if (e < text.length) { const sp = text.lastIndexOf(" ", e); if (sp > first[1]) e = sp; }
  return (s > 0 ? "… " : "") + marked(text.slice(s, e), terms) + (e < text.length ? " …" : "");
}

// ---- UI ----
const style = document.createElement("style");
style.textContent = `
.hsearch{position:relative;flex:0 1 340px;min-width:0}
.hsearch input{width:100%;font:inherit;font-size:14px;color:var(--fg);background:var(--bg);
  border:1px solid var(--border);border-radius:999px;padding:6px 14px 6px 34px;outline:none}
.hsearch input:focus{border-color:var(--accent);box-shadow:0 0 0 3px color-mix(in srgb,var(--accent) 22%,transparent)}
.hsearch input::-webkit-search-cancel-button{cursor:pointer}
.hsearch .hs-icon{position:absolute;left:12px;top:50%;transform:translateY(-50%);width:15px;height:15px;
  color:var(--muted);pointer-events:none}
.hsearch .hs-panel{position:absolute;top:calc(100% + 8px);right:0;width:min(560px,calc(100vw - 32px));
  max-height:min(70vh,560px);overflow:auto;background:var(--card);border:1px solid var(--border);
  border-radius:12px;box-shadow:var(--shadow);padding:6px;z-index:30}
.hsearch .hs-panel[hidden]{display:none}
.hsearch .hs-meta{padding:6px 10px;font-size:12.5px;color:var(--muted)}
.hsearch .hs-list{list-style:none;margin:0;padding:0}
.hsearch .hs-item{display:block;padding:8px 10px;border-radius:8px;cursor:pointer;line-height:1.4}
.hsearch .hs-item[aria-selected="true"],.hsearch .hs-item:hover{background:var(--chip)}
.hsearch .hs-where{display:block;font-size:12px;color:var(--muted);font-weight:600;
  white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.hsearch .hs-text{display:block;font-size:13.5px;color:var(--fg);overflow-wrap:anywhere}
.hsearch mark{background:#ffe27a;color:#1c2024;border-radius:2px}
::highlight(handbook-search){background:#ffe27a;color:#1c2024}
.hs-flash{outline:2px solid var(--accent);outline-offset:4px;border-radius:4px;transition:outline-color 1.2s}
.hs-flash.hs-fade{outline-color:transparent}
.hs-vh{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap}
/* On a phone the field gets a row of its own under the bar. A wrapping line breaks on the
   items' natural widths before it shrinks any of them, so the brand gives up its width
   (flex-basis 0) and the link and the language toggle stay on the first row with it. */
@media (max-width:720px){
  .topbar{flex-wrap:wrap;row-gap:10px}
  .topbar .spacer{display:none}
  .topbar .brand{flex:1 1 0;min-width:0;overflow:hidden;white-space:nowrap;text-overflow:ellipsis}
  .topbar .brand .mark{flex:none}
  .topbar .brand > span:not(.mark){min-width:0;overflow:hidden;text-overflow:ellipsis}
  .hsearch{order:10;flex:1 0 100%}
  .hsearch .hs-panel{left:0;right:0;width:auto}
}`;
document.head.appendChild(style);

const box = document.createElement("div");
box.className = "hsearch";
box.setAttribute("role", "search");
box.innerHTML = `
  <svg class="hs-icon" viewBox="0 0 16 16" aria-hidden="true"><circle cx="7" cy="7" r="5" fill="none" stroke="currentColor" stroke-width="1.7"/><path d="M10.8 10.8L14.5 14.5" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/></svg>
  <label class="hs-vh" for="hs-input"></label>
  <input id="hs-input" type="search" autocomplete="off" spellcheck="false" enterkeyhint="search"
    role="combobox" aria-autocomplete="list" aria-expanded="false" aria-controls="hs-list">
  <div class="hs-panel" hidden>
    <div class="hs-meta" aria-live="polite"></div>
    <ul class="hs-list" id="hs-list" role="listbox"></ul>
  </div>`;
const input = box.querySelector("input");
const label = box.querySelector("label");
const panel = box.querySelector(".hs-panel");
const meta = box.querySelector(".hs-meta");
const list = box.querySelector(".hs-list");

if (topbar) {
  // Right after the spacer: at the right, beside the link back to the app and the language toggle.
  const spacer = topbar.querySelector(".spacer");
  topbar.insertBefore(box, spacer ? spacer.nextSibling : null);
}

function relabel() {
  label.textContent = tr("label");
  input.placeholder = tr("placeholder");
  input.title = tr("label") + " – " + tr("hint").replace(/^[^:]+:\s*/, "");
}
relabel();

let hits = [];
let terms = [];
let active = -1;

function open(show) {
  panel.hidden = !show;
  input.setAttribute("aria-expanded", String(show));
}

function select(i) {
  active = i;
  list.querySelectorAll(".hs-item").forEach((li, j) => li.setAttribute("aria-selected", String(j === i)));
  const li = list.children[i];
  if (li) { input.setAttribute("aria-activedescendant", li.id); li.scrollIntoView({ block: "nearest" }); }
  else input.removeAttribute("aria-activedescendant");
}

function run() {
  const q = input.value.trim();
  if (!q) { hits = []; open(false); clearMarks(); return; }
  if (q.replace(/\s/g, "").length < 2) {
    hits = []; list.innerHTML = ""; meta.textContent = tr("short"); select(-1); open(true); return;
  }
  const r = search(q);
  terms = r.terms;
  hits = r.hits.slice(0, MAX_RESULTS);
  meta.textContent = hits.length ? T.count[lang()](hits.length, r.hits.length > MAX_RESULTS) : tr("none");
  list.innerHTML = hits.map(({ b }, i) => {
    const where = [b.chapter, b.heading && b.heading !== b.chapter ? b.heading : ""].filter(Boolean).join(" › ");
    return `<li class="hs-item" role="option" id="hs-opt-${i}" data-i="${i}" aria-selected="false">` +
      (where ? `<span class="hs-where">${esc(where)}</span>` : "") +
      `<span class="hs-text">${snippet(b.text, terms)}</span></li>`;
  }).join("");
  select(hits.length ? 0 : -1);
  open(true);
}

function clearMarks() {
  if (window.CSS && CSS.highlights) CSS.highlights.delete("handbook-search");
}

// markIn highlights every occurrence of the terms inside one block, across the text nodes
// it is made of (a word may be split by <b> or <code>).
function markIn(b) {
  if (!(window.CSS && CSS.highlights && window.Highlight)) return;
  const starts = [];
  let raw = "";
  for (const n of b.nodes) { starts.push(raw.length); raw += n.nodeValue; }
  const locate = (pos) => {
    let k = starts.length - 1;
    while (k > 0 && starts[k] > pos) k--;
    return [b.nodes[k], pos - starts[k]];
  };
  const hl = new Highlight();
  for (const [s, e] of ranges(raw, terms)) {
    const r = document.createRange();
    const [sn, so] = locate(s);
    const [en, eo] = locate(e - 1);
    try { r.setStart(sn, so); r.setEnd(en, eo + 1); hl.add(r); } catch (err) { /* node changed */ }
  }
  CSS.highlights.set("handbook-search", hl);
}

// bringIntoView centres el and keeps it there while the page settles. A jump passes
// chapters that draw their diagrams only once they come near the screen, so the content
// above the target can still grow after the scroll; a smooth scroll would aim at where the
// target was when it started. Any input from the reader ends the correction.
function bringIntoView(el) {
  el.scrollIntoView({ block: "center", behavior: "instant" });
  let want = el.getBoundingClientRect().top;
  const until = performance.now() + 1500;
  let stop = false;
  const cancel = () => { stop = true; };
  const evs = ["wheel", "touchstart", "keydown", "mousedown"];
  evs.forEach((e) => addEventListener(e, cancel, { once: true, passive: true, capture: true }));
  const tick = () => {
    if (stop || !el.isConnected || performance.now() > until) {
      evs.forEach((e) => removeEventListener(e, cancel, { capture: true }));
      return;
    }
    const top = el.getBoundingClientRect().top;
    if (Math.abs(top - want) > 2) { window.scrollBy({ top: top - want, behavior: "instant" }); want = el.getBoundingClientRect().top; }
    requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
}

function go(i) {
  const h = hits[i];
  if (!h) return;
  const el = h.b.el;
  if (!el.isConnected) { index = null; run(); return; }
  // A hit inside a folded block is no use while it stays folded.
  for (let d = el.parentElement; d; d = d.parentElement) if (d.tagName === "DETAILS") d.open = true;
  open(false);
  markIn(h.b);
  bringIntoView(el);
  el.classList.remove("hs-flash", "hs-fade");
  void el.offsetWidth;
  el.classList.add("hs-flash");
  setTimeout(() => el.classList.add("hs-fade"), 1400);
  setTimeout(() => el.classList.remove("hs-flash", "hs-fade"), 2700);
}

let timer = 0;
input.addEventListener("input", () => { clearTimeout(timer); timer = setTimeout(() => { timer = 0; run(); }, 120); });
input.addEventListener("focus", () => {
  if (input.value.trim()) run();
  // Reading the page takes a moment on the first search; do it while the reader types.
  else if (!index) (window.requestIdleCallback || setTimeout)(() => { if (!index) index = buildIndex(); });
});
// After a jump the field keeps the focus, so a click on it brings the list back.
input.addEventListener("click", () => { if (panel.hidden && input.value.trim()) run(); });
input.addEventListener("keydown", (ev) => {
  if (ev.key === "ArrowDown" || ev.key === "ArrowUp") {
    ev.preventDefault();
    if (panel.hidden) { run(); return; }
    if (!hits.length) return;
    const step = ev.key === "ArrowDown" ? 1 : -1;
    select((active + step + hits.length) % hits.length);
  } else if (ev.key === "Enter") {
    ev.preventDefault();
    // Enter typed before the list caught up with the last keystroke goes to its first hit.
    const stale = timer !== 0;
    clearTimeout(timer);
    timer = 0;
    if (stale) { run(); go(0); }
    else if (panel.hidden) run();
    else go(active < 0 ? 0 : active);
  } else if (ev.key === "Escape") {
    if (!panel.hidden) { ev.preventDefault(); open(false); }
    else if (input.value) { ev.preventDefault(); input.value = ""; clearMarks(); }
    else input.blur();
  }
});
list.addEventListener("mousedown", (ev) => ev.preventDefault()); // keep focus in the field
list.addEventListener("click", (ev) => {
  const li = ev.target.closest(".hs-item");
  if (li) go(Number(li.dataset.i));
});
document.addEventListener("click", (ev) => { if (!box.contains(ev.target)) open(false); });

// "/" (as on many documentation sites) and Ctrl/Cmd+K open the search; the browser's own
// Ctrl+F is left alone for the readers who use it.
document.addEventListener("keydown", (ev) => {
  if (ev.defaultPrevented || ev.altKey) return;
  const t = ev.target;
  const typing = t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName));
  if ((ev.key === "/" && !typing && !ev.ctrlKey && !ev.metaKey) ||
      ((ev.ctrlKey || ev.metaKey) && (ev.key === "k" || ev.key === "K"))) {
    ev.preventDefault();
    input.focus();
    input.select();
  }
});

// The language toggle swaps which half of the page is visible: the index, the labels and
// an open result list follow it.
new MutationObserver(() => {
  index = null;
  relabel();
  clearMarks();
  if (!panel.hidden) run();
}).observe(root, { attributes: true, attributeFilter: ["data-lang"] });

// /handbuch.html?q=timer opens the page with that search, so a link can point at one.
const q0 = new URLSearchParams(location.search).get("q");
if (q0) { input.value = q0; run(); input.focus(); }
