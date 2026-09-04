// Rendering documentation prose, in one place.
//
// `<bpmn:documentation>` is the one field every element carries (ADR-0025), and the
// people who read it are the ones furthest from the model: the assignee working a user
// task, an operator in the instance replay, an auditor reading the exported document
// (ADR-0143). Authors have been writing it as Markdown all along — the Modeler's field
// declares itself Markdown to the developer view (`editor.js`, `dev-lang.js`) — but
// every reading surface printed it as literal text, so a heading arrived as a hash and
// an emphasised "do not" arrived as asterisks.
//
// This module is the renderer they were missing (ADR-0250).
// It is deliberately small and closed: it parses a subset and *builds* every tag
// itself, so no fragment of the source is ever passed through as markup. That matters
// more here than in most Markdown. The author and the reader are different people with
// different roles — a modeller writes, a caseworker reads — and a documentation PDF can
// be handed to readers with no account at all. Two rules keep it closed:
//
//   1. every character of the source is HTML-escaped *first*, before any parsing, so
//      raw HTML in a documentation text renders as visible text and never as markup;
//   2. a link's destination must pass an allowlist of schemes, or the link renders as
//      its label alone. `javascript:` and `data:` never reach an href.
//
// What it supports is what documentation actually uses: headings, paragraphs, bullet
// and numbered lists, block quotes, fenced and inline code, bold, italic,
// strikethrough, links and thematic breaks. It deliberately does not support tables,
// images or raw HTML: each is a decision of its own — a table has to survive the PDF
// exporter's line breaker, an image has to be embedded or fetched from somewhere — and
// leaving them out keeps this a module a reader can hold in their head.
//
// **A soft line break becomes `<br>`.** That is not CommonMark, which joins
// consecutive lines into one paragraph, and it is the one deliberate departure. Every
// documentation text written before this module existed was displayed as plain text
// under `white-space: pre-wrap`, where a line break is a line break. Reflowing that
// prose years later, silently, would be a worse answer than diverging from the spec.
// For the same reason there are no indented code blocks: four spaces at the start of a
// line is something people do in plain text, and it must not turn their sentence into
// code.

const HTML_ESCAPES = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };

// escapeHTML is this module's first act on every piece of source. Everything after it
// works on text that can no longer become markup by accident, which is what lets the
// rest of the file build tags by string concatenation without a second thought.
function escapeHTML(s) {
  return String(s == null ? "" : s).replace(/[&<>"']/g, (c) => HTML_ESCAPES[c]);
}

// normalize gives the parser one shape of input: Unix line endings, tabs as spaces
// (indentation decides what a list item is, so a tab cannot be left ambiguous), and no
// control characters — both because they have no business in prose and because the
// inline pass uses NUL as its placeholder sentinel and must own it alone.
function normalize(src) {
  return String(src == null ? "" : src)
    .replace(/\r\n?/g, "\n")
    .replace(/\t/g, "    ")
    .replace(/[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F]/g, "");
}

const FENCE = /^ {0,3}(`{3,}|~{3,})\s*([A-Za-z0-9_+-]*)\s*$/;
const HEADING = /^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$/;
const THEMATIC_BREAK = /^ {0,3}([-*_])[ \t]*(?:\1[ \t]*){2,}$/;
const QUOTE = /^ {0,3}>/;
// A list item's marker must be followed by a space (or end the line), which is what
// keeps `**bold**` from opening a bullet list and `1.5 hours` from opening a numbered
// one.
const ITEM = /^( {0,7})([-*+]|\d{1,9}[.)])( +|$)(.*)$/;

// renderMarkdown turns one documentation text into HTML. The result is safe to insert
// with innerHTML — that is the module's whole contract — and is "" for a blank text, so
// a caller can decide whether an empty documentation means an empty block or no block.
export function renderMarkdown(src) {
  const text = normalize(src);
  if (!text.trim()) return "";
  return renderBlocks(text.split("\n"));
}

// renderBlocks walks lines and dispatches on what each one opens. It recurses for the
// containers — a list item and a block quote hold blocks of their own — so a nested
// list or a quoted code fence needs no special case anywhere.
function renderBlocks(lines) {
  const out = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { i++; continue; }

    const fence = FENCE.exec(line);
    if (fence) { i = codeFence(lines, i, fence, out); continue; }

    // Before the list: `---` is a thematic break, not a bullet with nothing after it.
    if (THEMATIC_BREAK.test(line)) { out.push("<hr>"); i++; continue; }

    const heading = HEADING.exec(line);
    if (heading) {
      const level = heading[1].length;
      out.push(`<h${level}>${inline(heading[2])}</h${level}>`);
      i++;
      continue;
    }

    if (QUOTE.test(line)) { i = quote(lines, i, out); continue; }
    if (itemAt(line)) { i = list(lines, i, out); continue; }

    i = paragraph(lines, i, out);
  }
  return out.join("\n");
}

// opensBlock reports whether a line starts something other than a paragraph. It is what
// stops a paragraph, and what stops a lazy continuation from swallowing the heading or
// the list that follows it.
function opensBlock(line) {
  return FENCE.test(line) || THEMATIC_BREAK.test(line) || HEADING.test(line)
    || QUOTE.test(line) || !!itemAt(line);
}

// itemAt describes a list item's opening line, or returns null. `width` is the column
// its content starts at — the marker plus the space after it — which is the indentation
// a following line must reach to belong to this item rather than end the list.
function itemAt(line) {
  const m = ITEM.exec(line);
  if (!m) return null;
  const [, lead, marker, spaces, content] = m;
  const ordered = /\d/.test(marker[0]);
  return {
    ordered,
    start: ordered ? Number(marker.slice(0, -1)) : 1,
    indent: lead.length,
    // An item written as "-   text" indents its continuation to the text, so the
    // spacing counts; an item with nothing after the marker still opens one column.
    width: lead.length + marker.length + Math.max(1, spaces.length),
    content,
  };
}

// paragraph collects the run of lines up to a blank line or the next block, and renders
// them as one paragraph whose internal line breaks survive as <br> (see the header).
function paragraph(lines, start, out) {
  const buf = [];
  let i = start;
  while (i < lines.length && lines[i].trim() && !(i > start && opensBlock(lines[i]))) {
    buf.push(lines[i].trim());
    i++;
  }
  out.push(`<p>${inline(buf.join("\n"))}</p>`);
  return i;
}

// codeFence renders ``` / ~~~ blocks. The body is escaped and otherwise untouched: its
// indentation and its asterisks are the point of writing it as code.
function codeFence(lines, start, m, out) {
  const marker = m[1][0];
  const closing = new RegExp(`^ {0,3}[${marker}]{${m[1].length},}\\s*$`);
  const lang = m[2] || "";
  const body = [];
  let i = start + 1;
  for (; i < lines.length; i++) {
    if (closing.test(lines[i])) { i++; break; }
    body.push(lines[i]);
  }
  const cls = lang ? ` class="language-${lang}"` : "";
  out.push(`<pre><code${cls}>${escapeHTML(body.join("\n"))}</code></pre>`);
  return i;
}

// quote strips one level of "> " and renders what is left as blocks. A plain line
// directly under a quoted one continues the quote, the way a reader expects when they
// wrapped a long sentence without repeating the marker.
function quote(lines, start, out) {
  const inner = [];
  let i = start;
  while (i < lines.length) {
    if (QUOTE.test(lines[i])) {
      inner.push(lines[i].replace(/^ {0,3}> ?/, ""));
      i++;
      continue;
    }
    if (inner.length && lines[i].trim() && !opensBlock(lines[i])) {
      inner.push(lines[i].trim());
      i++;
      continue;
    }
    break;
  }
  out.push(`<blockquote>${renderBlocks(inner)}</blockquote>`);
  return i;
}

// list collects consecutive items of one kind and renders each item's content as blocks
// of its own. An item that runs over several lines keeps them if they are indented to
// its content column, which is also how a nested list gets found: it is simply a list
// inside the item's lines.
//
// A blank line between items makes the list *loose*, and a loose list keeps the <p>
// around each item's text — the same distinction CommonMark draws, and the one that
// decides whether the items read as a tight enumeration or as spaced-out prose.
function list(lines, start, out) {
  const first = itemAt(lines[start]);
  const items = [];
  let i = start;
  let loose = false;
  let blank = false;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { blank = true; i++; continue; }

    const item = itemAt(line);
    if (item && item.ordered === first.ordered && item.indent <= first.indent + 3) {
      if (blank && items.length) loose = true;
      blank = false;
      items.push([item.content]);
      i++;
      continue;
    }
    if (!items.length) break;

    const indent = line.length - line.trimStart().length;
    if (indent >= first.width) {
      if (blank) items[items.length - 1].push("");
      blank = false;
      items[items.length - 1].push(line.slice(first.width));
      i++;
      continue;
    }
    // A lazy continuation: an unindented wrapped line, but only while the item is
    // still open — a blank line ends the list rather than pulling the next paragraph in.
    if (!blank && !opensBlock(line)) {
      items[items.length - 1].push(line.trim());
      i++;
      continue;
    }
    break;
  }

  const body = items.map((item) => {
    const inner = renderBlocks(item);
    return `<li>${loose ? inner : unwrapParagraph(inner)}</li>`;
  }).join("");
  const startAttr = first.ordered && first.start !== 1 ? ` start="${first.start}"` : "";
  out.push(first.ordered ? `<ol${startAttr}>${body}</ol>` : `<ul>${body}</ul>`);
  return i;
}

// unwrapParagraph drops the <p> around a tight item's own text — that is what makes a
// tight list tight — while leaving whatever follows it alone, so an item that carries a
// nested list reads as one line with a list under it rather than as a spaced-out block.
function unwrapParagraph(html) {
  const m = /^<p>((?:(?!<p>)[\s\S])*)<\/p>(\n[\s\S]*)?$/.exec(html);
  return m ? m[1] + (m[2] || "") : html;
}

// ---------- inline ----------

// The inline pass escapes once, then works on the escaped string. That is safe because
// escaping only ever produces `&…;` sequences, and none of the delimiters below —
// backtick, asterisk, underscore, tilde, bracket, paren — can appear inside one.
//
// Code spans and link tags are lifted out into placeholders before emphasis runs, so an
// underscore inside `snake_case` code or inside a URL cannot become italics. The
// placeholder is a NUL-delimited index, and normalize() has already guaranteed the
// source carries no NUL of its own.
function inline(raw) {
  const held = [];
  const hold = (html) => `\u0000${held.push(html) - 1}\u0000`;

  let text = escapeHTML(raw);

  // Code spans first: their content is literal, so nothing that follows may touch it.
  text = text.replace(/(`+)([\s\S]+?)\1/g, (all, ticks, code) =>
    hold(`<code>${code.replace(/^ (.*) $/, "$1")}</code>`));

  // Backslash escapes: the author asking for a literal asterisk, hash or bracket. The
  // entity alternative catches the characters escaping already turned into entities, so
  // `\<` is the author's angle bracket rather than a stray backslash in front of one.
  text = text.replace(/\\(&(?:amp|lt|gt|quot|#39);|[\\`*_{}[\]()#+\-.!~])/g, (all, ch) => hold(ch));

  // Links, and images rendered as links to the image. Embedding a picture into the
  // console, the PDF and a public page are three different decisions and none of them
  // is made here; a link to it is the honest thing this module can do today.
  text = text.replace(/(!?)\[([^\]]*)\]\(([^()\s]*)\)/g, (all, bang, label, url) => {
    const href = safeHref(url);
    const shown = label || (bang ? "image" : url);
    if (!href) return shown;
    const external = /^https?:\/\//i.test(href);
    const attrs = external ? ` target="_blank" rel="noopener noreferrer"` : "";
    // Only the tags are held: the label stays in the flow so emphasis inside it works.
    return `${hold(`<a href="${href}"${attrs}>`)}${shown}${hold("</a>")}`;
  });

  // Emphasis, strongest first so `**x**` is never read as two `*x*`.
  text = text
    .replace(/\*\*(\S|\S[\s\S]*?\S)\*\*/g, "<strong>$1</strong>")
    .replace(/(^|[^A-Za-z0-9_])__(\S|\S[\s\S]*?\S)__(?![A-Za-z0-9_])/g, "$1<strong>$2</strong>")
    .replace(/~~(\S|\S[\s\S]*?\S)~~/g, "<del>$1</del>")
    .replace(/\*(\S|\S[\s\S]*?\S)\*/g, "<em>$1</em>")
    // An underscore inside a word is a name — `order_id`, `MAX_RETRIES` — not emphasis.
    // Asterisks carry no such risk, so only this form is guarded.
    .replace(/(^|[^A-Za-z0-9_])_(\S|\S[\s\S]*?\S)_(?![A-Za-z0-9_])/g, "$1<em>$2</em>");

  text = text.replace(/\n/g, "<br>");

  return text.replace(/\u0000(\d+)\u0000/g, (all, n) => held[Number(n)]);
}

// safeHref decides what may become an href. It is an allowlist, not a blocklist: http,
// https and mailto, plus links that stay on this origin (a `#/…` route or a path). Every
// other scheme — javascript:, data:, vbscript:, file: — and every protocol-relative
// `//host` URL returns null, and the caller renders the label as plain text instead.
//
// The url arrives HTML-escaped, which is correct for an attribute value and harmless
// here: an escaped `&` cannot form a scheme, so nothing can smuggle one past this.
function safeHref(url) {
  const u = url.trim();
  if (!u) return null;
  if (/^(https?:\/\/|mailto:)/i.test(u)) return u;
  if (/^\/\//.test(u)) return null;
  if (/^[#/]/.test(u)) return u;
  if (/^[A-Za-z][A-Za-z0-9+.-]*:/.test(u)) return null;
  return u;
}

// markdownToPlain flattens a documentation text to one line of prose: the markers
// removed, the words kept. It is for the places that show documentation *about* an
// element rather than the documentation itself — a table's subtitle, a tooltip, a
// title attribute — where markup has nowhere to render and would read as noise.
// The result is plain text, not HTML: a caller still escapes it.
export function markdownToPlain(src) {
  return normalize(src)
    .replace(/^ {0,3}(?:`{3,}|~{3,}).*$/gm, "")
    .replace(/^ {0,3}([-*_])[ \t]*(?:\1[ \t]*){2,}$/gm, "")
    .replace(/^ {0,3}#{1,6}\s+/gm, "")
    .replace(/^ {0,3}>\s?/gm, "")
    .replace(/^ {0,3}(?:[-*+]|\d{1,9}[.)])\s+/gm, "")
    .replace(/!?\[([^\]]*)\]\(([^()\s]*)\)/g, (all, label, url) => label || url)
    .replace(/`+([^`]*)`+/g, "$1")
    // The same delimiter rules the renderer applies, for the same reasons: an escaped
    // marker stays literal, and an underscore inside a word is part of a name.
    .replace(/(^|[^\\])(\*\*|~~)(\S|\S[\s\S]*?\S)\2/g, "$1$3")
    .replace(/(^|[^A-Za-z0-9_\\])__(\S|\S[\s\S]*?\S)__(?![A-Za-z0-9_])/g, "$1$2")
    .replace(/(^|[^\\])\*(\S|\S[\s\S]*?\S)\*/g, "$1$2")
    .replace(/(^|[^A-Za-z0-9_\\])_(\S|\S[\s\S]*?\S)_(?![A-Za-z0-9_])/g, "$1$2")
    .replace(/\\([\\`*_{}[\]()#+\-.!~>])/g, "$1")
    .replace(/\s+/g, " ")
    .trim();
}
