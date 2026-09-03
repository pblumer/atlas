// Exporting the landscape (ADR-0211 §10).
//
// §10 names two export classes and forbids treating them alike: a *model* export
// carries authored structure and is safe, a *live* export carries observation data
// and is a disclosure surface. The derived landscape is only ever the second one.
// Nothing on it was drawn — its structure is read off this server's resources and
// every node carries a state — so there is no version of this picture that is a
// model export, and this module does not offer one.
//
// Two rules follow from that, and they are the whole design here:
//
//   - **Redaction is inherited, not re-applied.** What is exported is the picture
//     the server already built for this principal: scope-filtered, with restricted
//     placeholders where a scope cut a path. There is no second walk over an
//     unfiltered graph and no export endpoint to authorize, so there is nothing
//     that could disclose more than the screen it came from. This is the same
//     argument impact analysis makes for running on the delivered graph.
//   - **The artifact carries its own provenance.** An undated "all green" picture
//     circulates inside an organization long after it stopped being true, and is
//     believed because it looks like evidence. So the observation time, the source
//     instance, and everything the picture is *not* showing — a filter, a
//     drilldown, hidden nodes, a collapsed landscape, the states this build cannot
//     observe at all — are rendered into the image, not into the filename. A
//     filename is lost on the first paste into a chat window.
//
// What is exported is the landscape as currently narrowed — filtered, drilled into,
// or all of it — drawn at full extent rather than as framed. Pan and zoom are
// reading aids, so a file cropped to the current window would drop nodes and say
// nothing about having dropped them; the narrowing, by contrast, is a question
// somebody asked, so it is kept and named in the stamp.

// EXPORT_WIDTH is the exported picture's width in pixels. Everything else — the
// height, the stamp's type sizes — is derived from it, so one number decides how
// large the artifact is.
export const EXPORT_WIDTH = 1600;

// PNG_SCALE and PNG_MAX_PIXELS bound the raster. Two device pixels per exported
// pixel is the difference between a legible node name and a grey smudge when the
// image is pasted at half size; the cap keeps a very wide landscape from asking the
// browser for a canvas it will refuse to allocate.
export const PNG_SCALE = 2;
export const PNG_MAX_PIXELS = 8000;

const XMLNS = "http://www.w3.org/2000/svg";

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&apos;" })[c]);
}

// pad2 keeps the timestamp fixed-width, because it is read as a stamp rather than
// as prose and a wandering column is harder to compare between two exports.
const pad2 = (n) => String(n).padStart(2, "0");

// formatObserved renders the moment the server read this landscape.
//
// The offset is part of it. An export travels: "14:31" means nothing to somebody
// three time zones away, and the reader who has to decide whether a picture is
// current is exactly the reader who did not take it. The local rendering is kept
// beside the offset rather than replaced by UTC, because the person who *did* take
// it recognises their own wall clock and would have to convert otherwise.
//
// A missing timestamp is said out loud rather than filled in from this browser's
// clock: that clock dates the export, not the reading, and the two are the same
// number only if nobody left the tab open.
export function formatObserved(seconds) {
  if (!Number.isFinite(seconds) || seconds <= 0) {
    return "Observation time not reported by this server";
  }
  const d = new Date(seconds * 1000);
  const off = -d.getTimezoneOffset();
  const sign = off < 0 ? "-" : "+";
  const abs = Math.abs(off);
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())} ` +
    `${pad2(d.getHours())}:${pad2(d.getMinutes())} UTC${sign}${pad2(Math.floor(abs / 60))}:${pad2(abs % 60)}`;
}

// scopeText names which landscape this is, in the reader's terms. "The whole
// landscape" is a claim, so it is only made when nothing narrowed the picture.
export function scopeText(scope = {}) {
  if (scope.kind === "filter" && scope.term) return `filtered by “${scope.term}”`;
  if (scope.kind === "drill") {
    const hops = scope.hops === "all" || scope.hops === Infinity ? "any" : scope.hops;
    return `drilled into ${scope.name || "one node"}, within ${hops} hop(s)`;
  }
  return "the whole landscape";
}

// exportName is the file's name. Sortable date first, because the second thing
// anybody does with these is put two of them side by side.
export function exportName(extension, at = new Date()) {
  const stamp = `${at.getFullYear()}${pad2(at.getMonth() + 1)}${pad2(at.getDate())}-` +
    `${pad2(at.getHours())}${pad2(at.getMinutes())}`;
  return `atlas-landscape-${stamp}.${extension}`;
}

// stampLines is what §10 requires rendered into the artifact, in the order somebody
// reads it: what this is, when and where it came from, and then everything it is
// not showing.
//
// Every note is about the picture in the file rather than about the app it was
// taken in. A reader who was not there has no other source for any of it.
export function stampLines(meta = {}) {
  const lines = [{ weight: "bold", text: `Atlas landscape — ${scopeText(meta.scope)}` }];

  const drawn = meta.drawn || {};
  const facts = [
    `Observed ${formatObserved(meta.observedAt)}`,
    `Source ${meta.source || "unknown instance"}`,
  ];
  // Counted against the whole only when the whole is a different number: "7 of 7"
  // reads as a narrowing that did not happen.
  if (Number.isFinite(drawn.nodes)) {
    facts.push(Number.isFinite(meta.total) && meta.total !== drawn.nodes
      ? `${drawn.nodes} of ${meta.total} node(s) drawn`
      : `${drawn.nodes} node(s) drawn`);
  }
  lines.push({ text: facts.join("  ·  ") });

  if (meta.restricted > 0) {
    lines.push({ text: `${meta.restricted} node(s) in this landscape are hidden by your ` +
      `access. Their dependencies are drawn, their identities are not — this picture is ` +
      `filtered, and says so rather than looking complete.` });
  }
  if (meta.clustered) {
    lines.push({ text: `This landscape exceeded its size budget and is collapsed to ` +
      `applications; each one states how many nodes it stands for.` });
  }
  if (meta.partial) {
    lines.push({ text: `Counting parked work stopped at its bound, so a node shown as OK ` +
      `here is a floor rather than a verdict.` });
  }
  const unavailable = Array.isArray(meta.unavailable) ? meta.unavailable : [];
  if (unavailable.length) {
    const states = unavailable.map((u) => u.label || u.state).join(", ");
    lines.push({ text: `Not watched here: ${states}. ${unavailable[0].reason || ""}`.trim() });
  }
  return lines;
}

// wrap breaks a line at whole words to a character budget. Text in SVG does not
// reflow, so a note long enough to run past the edge would simply be cut off — and
// the notes are the honest half of the stamp, so they are the last thing that may
// be lost.
export function wrap(text, max) {
  const words = String(text).split(/\s+/).filter(Boolean);
  const out = [];
  let line = "";
  for (const word of words) {
    if (line && line.length + 1 + word.length > max) {
      out.push(line);
      line = word;
    } else {
      line = line ? `${line} ${word}` : word;
    }
  }
  if (line) out.push(line);
  return out.length ? out : [""];
}

// layoutLegend flows the key across the artifact's band, wrapping when a row runs
// out of width.
//
// Widths are estimated from the label rather than measured, because measuring means
// laying the text out in the document first and the answer would still be an
// estimate the moment the file is opened in a viewer with a different font. Erring
// wide costs a row; erring narrow overlaps two labels, which is the failure that
// makes a key unreadable rather than merely loose.
export function layoutLegend(entries, { width, pad, size, lead, mark = 16, gap = 28 }) {
  const placed = [];
  let x = pad;
  let row = 0;
  for (const entry of entries) {
    const w = mark + 6 + Math.ceil(String(entry.label).length * size * 0.52) + gap;
    if (x > pad && x + w > width - pad) {
      row += 1;
      x = pad;
    }
    placed.push({ ...entry, x, row });
    x += w;
  }
  return { placed, rows: placed.length ? row + 1 : 0, lead };
}

// meshRules harvests the landscape's own styling out of the live stylesheet.
//
// The alternative is a second stylesheet written for exports, and it would drift:
// a colour changed in app.css would go on being right on screen and quietly wrong
// in every file anybody saved. Harvesting cannot drift, for the same reason the
// legend is drawn by the function that draws the nodes.
//
// Rules are matched on a ".mesh-" class appearing in the selector, which is how
// every rule the landscape uses is written. Sweeping in a few panel rules that can
// never match inside an SVG costs bytes and nothing else; missing one that can
// would cost the picture. The leading dot is what keeps it from also taking any
// unrelated class that happens to contain the letters.
export function meshRules(doc = document) {
  const rules = [];
  for (const sheet of doc.styleSheets || []) {
    let list;
    try {
      list = sheet.cssRules;
    } catch {
      continue; // a cross-origin sheet cannot be read, and holds none of ours
    }
    for (const rule of list || []) {
      // selectorText is absent on @media, @keyframes and @font-face. Skipping them
      // is deliberate rather than incidental: a media query about screen width
      // means nothing in a fixed-size artifact, and the keyframes belong to the
      // heartbeat, which an export must not carry (see standaloneSVG).
      if (!rule.selectorText || !rule.selectorText.includes(".mesh-")) continue;
      rules.push(rule.cssText);
    }
  }
  return rules.join("\n");
}

// resolveTokens turns every custom property the picture references into a literal.
//
// The nodes carry `fill="var(--accent-soft)"` and the like, and the theme those
// resolve against lives on the page, not in the file. Left unresolved they would
// render as nothing at all once the file is opened anywhere else. Resolving them
// against the live root is also what makes an export match the theme the reader
// was actually looking at.
export function resolveTokens(text, root = document.documentElement) {
  const style = root && root.ownerDocument.defaultView
    ? root.ownerDocument.defaultView.getComputedStyle(root)
    : null;
  if (!style) return "";
  const found = new Map();
  let pending = [text];
  // A token's value may itself name another token, so this settles rather than
  // reads once. The bound is a guard against a cycle in the stylesheet, not a
  // depth anybody is expected to reach.
  for (let pass = 0; pass < 8 && pending.length; pass++) {
    const next = [];
    for (const chunk of pending) {
      for (const [, name] of String(chunk).matchAll(/var\(\s*(--[\w-]+)/g)) {
        if (found.has(name)) continue;
        const value = style.getPropertyValue(name).trim();
        // A name nothing defines is left out rather than written as an empty
        // declaration: the reference is already broken, and an empty custom property
        // is one parser away from taking the whole block with it.
        if (!value) continue;
        found.set(name, value);
        if (value.includes("var(")) next.push(value);
      }
    }
    pending = next;
  }
  return [...found].map(([name, value]) => `${name}:${value};`).join("");
}

// EXPORT_OVERRIDES are the three differences between the picture on screen and the
// picture in the file. Each is a consequence of the file being *rendered once* and
// never pointed at, and every one of them is a way a correct-looking export could
// come out wrong.
const EXPORT_OVERRIDES = `
/* The heartbeat is stilled. An animation rasterizes at whatever phase the encoder
   happened to catch, so a critical node could come out mid-fade — and severity is
   already carried by the ring, the badge and its glyph, none of which move. */
.mesh-beat{animation:none!important;}
/* A transition runs when the file is opened, from the property's initial value.
   The names fade in over 80ms, which is nothing on screen and is most of the time a
   rasterizer spends: without this the export can come out with faded labels. */
.mesh-label-ink{transition:none;}
/* The adjacency halo belongs to the pointer, and a file has none. It is invisible
   unless the canvas is in its hover state, so this only fixes the case where the
   export was taken while one node was lit — where it would have been the single
   node with a ring around it and nothing to explain why. */
.mesh-halo{display:none;}`;

// exportStyles is the stylesheet the artifact carries: the resolved theme, the
// landscape's own rules, and the few overrides above.
export function exportStyles(markup, { doc = document, root = document.documentElement } = {}) {
  const rules = meshRules(doc);
  const tokens = resolveTokens(`${rules}\n${markup}`, root);
  const view = root && root.ownerDocument.defaultView;
  // The font follows the page. A file that fell back to the browser's default
  // serif would be recognisably not the thing the reader exported.
  const font = view ? view.getComputedStyle(doc.body || root).fontFamily : "";
  return `:root{${tokens}}\n${rules}\n${font ? `text{font-family:${font};}` : ""}${EXPORT_OVERRIDES}`;
}

// standaloneSVG builds the whole artifact: the landscape at full extent, the stamp
// beneath it, and the stylesheet that makes both render outside this app.
//
// The source SVG is cloned rather than moved — the picture on screen is still being
// read — and the clone is stripped of everything that carries behaviour. Nothing in
// this view puts a script or an event handler on the canvas today; the strip is
// here because an exported file is opened by people who did not make it, and "there
// is nothing to strip" is a property of the current code rather than of the format.
export function standaloneSVG(source, {
  stamp = [],
  legend = [],
  css = "",
  extent = "",
  width = EXPORT_WIDTH,
  background = "#ffffff",
  ink = "#111111",
  muted = "#666666",
  rule = "#dddddd",
} = {}) {
  // extent is the whole landscape, and it is passed in rather than read off the
  // element because the element's own viewBox is wherever the reader has zoomed to.
  // Exporting that would produce a picture of a scroll position: the nodes outside
  // the window would be gone from the file, and nothing in it would say they had
  // been. Zoom and pan are for reading; the artifact carries all of what was drawn.
  const raw = (extent || source.getAttribute("viewBox") || "").trim().split(/[\s,]+/).map(Number);
  const box = raw.length === 4 && raw.every(Number.isFinite) ? raw : [0, 0, 1200, 720];
  const [, , vw, vh] = box;
  const picture = Math.max(1, Math.round(width * (vh / Math.max(vw, 1))));

  const clone = source.cloneNode(true);
  for (const el of [clone, ...clone.querySelectorAll("*")]) {
    if (el.tagName && el.tagName.toLowerCase() === "script") {
      el.remove();
      continue;
    }
    for (const attr of [...el.attributes]) {
      if (/^on/i.test(attr.name)) el.removeAttribute(attr.name);
    }
    el.removeAttribute("tabindex");
  }
  clone.classList.remove("mesh-zoomed", "mesh-beating", "mesh-names-anchors");
  // Every name is painted. Which names appear on screen is decided by magnification
  // — how large the text is against the reader's window — and an artifact has no
  // magnification: it is zoomed by whatever opens it. A file that hid the names it
  // happened to be hiding at the moment of export would be a picture of a scroll
  // position rather than of a landscape.
  clone.classList.add("mesh-names-all");
  clone.setAttribute("viewBox", box.join(" "));
  clone.setAttribute("x", "0");
  clone.setAttribute("y", "0");
  clone.setAttribute("width", String(width));
  clone.setAttribute("height", String(picture));

  const u = width / EXPORT_WIDTH;         // one number scales the stamp with the page
  const pad = Math.round(24 * u);
  const size = { bold: 20 * u, plain: 16 * u };
  const lead = Math.round(24 * u);
  // Roughly the average glyph width of the page font at this size; it decides where
  // a note wraps, and being a little conservative costs a short line rather than a
  // sentence running off the edge.
  const perLine = Math.max(24, Math.floor((width - 2 * pad) / (size.plain * 0.5)));

  const rows = [];
  for (const line of stamp) {
    for (const part of wrap(line.text, perLine)) rows.push({ ...line, text: part });
  }

  // The key goes under the notes rather than over them. Both are things the reader
  // of a file cannot get anywhere else, and of the two the notes are the ones that
  // change what the picture means.
  const markSize = Math.round(16 * u);
  const keySize = size.plain * 0.9;
  const key = layoutLegend(legend, { width, pad, size: keySize, lead, mark: markSize });
  const keyTop = picture + pad + rows.length * lead + (key.rows ? lead * 0.6 : 0);
  const stampHeight = pad + rows.length * lead + (key.rows ? key.rows * lead + lead * 0.6 : 0) + pad / 2;
  const height = picture + stampHeight;

  const text = rows.map((row, i) => `<text x="${pad}" y="${(picture + pad + i * lead).toFixed(1)}"
    font-size="${(row.weight === "bold" ? size.bold : size.plain).toFixed(1)}"
    font-weight="${row.weight === "bold" ? 600 : 400}"
    fill="${row.weight === "bold" ? esc(ink) : esc(muted)}">${esc(row.text)}</text>`).join("");

  // Each swatch is drawn in the 16x16 box the page's legend uses and scaled from
  // there, so the mark in the file is the mark beside the canvas — and both are the
  // shape the node itself was drawn with.
  const marks = key.placed.map((entry) => {
    const y = keyTop + entry.row * lead;
    return `<g class="${esc(entry.tone || "")}">
      <g transform="translate(${entry.x.toFixed(1)},${(y - markSize * 0.8).toFixed(1)}) scale(${(markSize / 16).toFixed(3)})">${entry.mark}</g>
      <text x="${(entry.x + markSize + 6).toFixed(1)}" y="${y.toFixed(1)}"
        font-size="${keySize.toFixed(1)}" fill="${esc(muted)}">${esc(entry.label)}</text>
    </g>`;
  }).join("");

  const svg = `<svg xmlns="${XMLNS}" width="${width}" height="${height}"
  viewBox="0 0 ${width} ${height}" role="img" aria-label="Atlas derived landscape">
  <style><![CDATA[${css}]]></style>
  <rect width="${width}" height="${height}" fill="${esc(background)}"/>
  ${new XMLSerializer().serializeToString(clone)}
  <line x1="0" y1="${picture}" x2="${width}" y2="${picture}" stroke="${esc(rule)}" stroke-width="1"/>
  ${text}${marks}
</svg>`;
  return { svg, width, height };
}

// rasterise encodes the artifact as a PNG, for the places an SVG cannot go — a
// chat window, a slide, a ticket that renders images and not markup.
//
// PNG rather than JPEG: this is line art on a flat background, where JPEG's ringing
// lands exactly on the node outlines and the names.
export async function rasterise({ svg, width, height }, {
  scale = PNG_SCALE, maxPixels = PNG_MAX_PIXELS, background = "#ffffff",
} = {}) {
  let w = Math.max(1, Math.round(width * scale));
  let h = Math.max(1, Math.round(height * scale));
  const cap = Math.max(w, h);
  if (cap > maxPixels) {
    const shrink = maxPixels / cap;
    w = Math.max(1, Math.round(w * shrink));
    h = Math.max(1, Math.round(h * shrink));
  }
  const url = URL.createObjectURL(new Blob([svg], { type: "image/svg+xml;charset=utf-8" }));
  try {
    const img = await new Promise((resolve, reject) => {
      const el = new Image();
      el.onload = () => resolve(el);
      el.onerror = () => reject(new Error("the picture could not be rendered"));
      el.src = url;
    });
    const canvas = document.createElement("canvas");
    canvas.width = w;
    canvas.height = h;
    const ctx = canvas.getContext("2d");
    // PNG has alpha, so an unpainted backdrop would be transparent — which reads as
    // white in most viewers and as black in a few, and the second is where a dark
    // export's own background would have been.
    ctx.fillStyle = background;
    ctx.fillRect(0, 0, w, h);
    ctx.drawImage(img, 0, 0, w, h);
    return await new Promise((resolve, reject) => {
      canvas.toBlob((blob) => blob ? resolve(blob) : reject(new Error("the image could not be encoded")),
        "image/png");
    });
  } finally {
    URL.revokeObjectURL(url);
  }
}

// save hands the file to the browser. The object URL is released on the next turn
// rather than immediately: revoking it in the same tick can beat the download the
// click started, and the failure is a file that silently never arrives.
export function save(blob, name) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.rel = "noopener";
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
