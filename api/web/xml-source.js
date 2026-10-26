// The Modeler's source view: the document behind a process diagram or a decision,
// shown as text and, once corrected there, applied back onto the canvas
// (ADR-0457).
//
// A BPMN diagram and a DMN decision are XML underneath, and some corrections are
// easier to make there than on the canvas: an id that has to change in every place
// that names it, an attribute the properties panel does not offer, a reference a
// hand-written or generated model got wrong. Export XML hands the document over as a
// file; this puts it in front of the author in place and takes it back, without a
// round trip through the file system and an upload.
//
// The editors own what "apply" means — each knows how to import into its modeler, how
// to put the canvas back when that fails, and what has to learn that the canvas
// changed — so this module is only the dialog: it reads the document through the
// caller, checks that an edit is well-formed XML before anything else sees it, and
// hands the text to the caller's apply. Three rules keep it from costing work:
//
//   - A malformed edit never reaches the modeler. It is refused with the line the
//     parser stopped on, while typing and again on Apply, and the text stays the
//     author's to fix.
//   - A refused import leaves the canvas as it was. That is the caller's half of the
//     contract: apply either succeeds or throws with the canvas unchanged.
//   - An unapplied edit is not thrown away on a stray Escape: closing asks first.
//
// The text surface is code-editor.js, the one every code field in the Modeler uses, so
// it highlights, numbers lines and marks the one in error the same way. Buildless and
// self-contained (ADR-0012, ADR-0013).

import { attachCodeEditor } from "./code-editor.js";
import { t, plural } from "./i18n.js";
import { copyText } from "./clipboard.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// tokenizeXML splits an XML document into spans covering the whole source, so joining
// the values reproduces it exactly (the code-editor backdrop contract). It is a
// highlighter, not a parser — checkWellFormed below is the parser — but it reads the
// three things dev-lang.js's HTML highlighter does not, because an HTML fragment never
// holds them and a BPMN or DMN document routinely does: the <?xml?> declaration,
// CDATA sections (scripts and FEEL are often wrapped in one), and a `>` inside a
// quoted attribute value, which is legal XML and must not end the tag.
export function tokenizeXML(src) {
  const s = String(src);
  const n = s.length;
  const out = [];
  const push = (type, value) => { if (value) out.push({ type, value }); };
  // until returns the index just past `end`, searching from `from`, or the end of the
  // source for a construct that is still open while somebody types it.
  const until = (from, end) => {
    const k = s.indexOf(end, from);
    return k === -1 ? n : k + end.length;
  };

  let i = 0;
  while (i < n) {
    if (s.startsWith("<!--", i)) {
      const j = until(i + 4, "-->");
      push("comment", s.slice(i, j));
      i = j;
    } else if (s.startsWith("<![CDATA[", i)) {
      const close = s.indexOf("]]>", i + 9);
      push("punct", "<![CDATA[");
      if (close === -1) { push("text", s.slice(i + 9)); i = n; continue; }
      push("text", s.slice(i + 9, close));
      push("punct", "]]>");
      i = close + 3;
    } else if (s.startsWith("<?", i) || s.startsWith("<!", i)) {
      // A declaration, a processing instruction or a DOCTYPE: structure, not content.
      const j = s.startsWith("<?", i) ? until(i + 2, "?>") : until(i + 2, ">");
      push("punct", s.slice(i, j));
      i = j;
    } else if (s[i] === "<") {
      i = tag(s, i, push);
    } else {
      const next = s.indexOf("<", i);
      const j = next === -1 ? n : next;
      text(s.slice(i, j), push);
      i = j;
    }
  }
  return out;
}

// tag emits one element tag starting at s[i] === "<" and returns the index after it.
function tag(s, i, push) {
  const n = s.length;
  let j = i + 1;
  if (s[j] === "/") j++;
  push("punct", s.slice(i, j));
  const name = /^[A-Za-z_][\w:.-]*/.exec(s.slice(j, j + 200));
  if (name) { push("html-tag", name[0]); j += name[0].length; }

  // Attributes run to the first `>` that is not inside a quoted value.
  let expectValue = false;
  while (j < n) {
    const c = s[j];
    if (c === ">") { push("punct", ">"); return j + 1; }
    if (c === "/" && s[j + 1] === ">") { push("punct", "/>"); return j + 2; }
    if (c === "<") return j; // an unfinished tag: let the next one start here
    if (/\s/.test(c)) {
      const ws = /^\s+/.exec(s.slice(j, j + 400))[0];
      push("ws", ws);
      j += ws.length;
    } else if (c === "=") {
      push("punct", "=");
      expectValue = true;
      j++;
    } else if (c === '"' || c === "'") {
      const close = s.indexOf(c, j + 1);
      const k = close === -1 ? n : close + 1;
      push("string", s.slice(j, k));
      expectValue = false;
      j = k;
    } else {
      const word = /^[^\s=>"'<\/]+/.exec(s.slice(j, j + 400));
      const w = word ? word[0] : c;
      push(expectValue ? "string" : "html-attr", w);
      expectValue = false;
      j += w.length;
    }
  }
  return n;
}

// text colours &amp;-style character references inside text content.
function text(t, push) {
  const re = /&[#A-Za-z0-9]+;/g;
  let last = 0;
  let m;
  while ((m = re.exec(t)) !== null) {
    push("text", t.slice(last, m.index));
    push("html-entity", m[0]);
    last = m.index + m[0].length;
  }
  push("text", t.slice(last));
}

// xml is the code-editor.js language module for a whole document. It completes
// nothing: the names that could be offered are the BPMN and DMN schemas', and a list
// of a few hundred element names is not help.
export const xml = {
  tokenize: tokenizeXML,
  completions() { return []; },
  tooltip() { return null; },
};

// checkWellFormed asks the browser's own XML parser whether the text is a document at
// all, and where it is not, on which line. It answers { ok: true } or
// { ok: false, line, column, message }; line is 0 when the parser did not say.
//
// The parser reports in prose, and the prose differs by engine: Chromium and WebKit
// write "error on line 5 at column 12: …" inside an HTML-ish <parsererror>, Firefox
// writes "XML Parsing Error: … Line Number 5, Column 12:". Both are read here; anything
// else is shown as the parser worded it, without a line.
export function checkWellFormed(src) {
  let doc;
  try {
    doc = new DOMParser().parseFromString(String(src), "application/xml");
  } catch (e) {
    return { ok: false, line: 0, column: 0, message: e.message || "not well-formed XML" };
  }
  const err = doc.getElementsByTagName("parsererror")[0];
  if (!err) return { ok: true };
  const raw = err.textContent || "";
  let m = /error on line (\d+) at column (\d+):\s*([^\n]*)/i.exec(raw);
  if (m) return { ok: false, line: +m[1], column: +m[2], message: m[3].trim() || "not well-formed XML" };
  m = /Line Number (\d+), Column (\d+)/i.exec(raw);
  const first = raw.split("\n")[0].replace(/^XML Parsing Error:\s*/i, "").trim();
  return { ok: false, line: m ? +m[1] : 0, column: m ? +m[2] : 0, message: first || "not well-formed XML" };
}

// lineOf finds the line a modeler's import message points at. bpmn-js and dmn-js word
// a parse failure as "unparsable content <x> detected; …\n\tline: 3\n\tcolumn: 5".
function lineOf(message) {
  const m = /\bline:?\s*(\d+)/i.exec(String(message || ""));
  return m ? +m[1] : 0;
}

// firstLine keeps a modeler's message to the sentence a person reads; the rest is a
// stack of tab-indented coordinates that lineOf has already taken what it needs from.
const firstLine = (message) => String(message || "").split("\n")[0].trim();

// The dialog currently open, so an editor being torn down can take it with it — a
// navigation by the browser's Back button leaves no other way to close it.
let openDialog = null;

// closeXmlSource closes the source view without asking, for an editor that is going
// away: whatever was on its canvas is going with it.
export function closeXmlSource() {
  if (openDialog) openDialog(true);
}

// openXmlSource opens the source view over an editor.
//
//   kind     what the document is, for the badge: "BPMN", "DMN 1.3", …
//   subject  the noun for what is on the canvas — "diagram", "decision" — so a refusal
//            can say what it left alone
//   read     () => Promise<string>, the document as the editor holds it now
//   apply    (text) => Promise<{ warnings }>, imports the text onto the canvas, or
//            throws with the canvas unchanged
//   toast    the Console's toast, for the one thing said after the dialog has closed
//
// Resolves once the dialog is open (or failed to open); the dialog closes itself.
export async function openXmlSource({ kind, subject = "diagram", read, apply, toast }) {
  closeXmlSource();
  let source;
  try {
    source = await read();
  } catch (e) {
    if (toast) toast(t("xml.readFailed", { error: e.message }), "err");
    return;
  }
  // baseline is the text that matches the canvas: what was read, or what was last
  // applied. An edit is unapplied while the text differs from it.
  let baseline = String(source);

  const ov = document.createElement("div");
  ov.className = "modal-ov xml-ov";
  ov.innerHTML = `
    <div class="modal xml-modal" role="dialog" aria-modal="true" aria-label="${esc(t("xml.title"))}">
      <div class="modal-head">
        <h2>${esc(t("xml.title"))} <span class="chip xml-kind">${esc(kind)}</span></h2>
        <button type="button" class="icon-btn" data-x aria-label="${esc(t("common.close"))}" title="${esc(t("common.close"))}">✕</button>
      </div>
      <div class="xml-body">
        <p class="muted xml-hint">${t("xml.hint." + subject)}</p>
        <div class="xml-editor"><textarea class="xml-ta" spellcheck="false" aria-label="${esc(t("xml.title"))}"></textarea></div>
      </div>
      <div class="modal-foot">
        <span class="xml-status" role="status" aria-live="polite"></span>
        <span class="modal-actions">
          <button type="button" class="btn neutral" data-copy title="${esc(t("xml.copyTitle"))}">${esc(t("xml.copy"))}</button>
          <button type="button" class="btn ghost" data-cancel>${esc(t("common.cancel"))}</button>
          <button type="button" class="btn" data-apply title="${esc(t("xml.applyTitle." + subject))}">${esc(t("xml.apply"))}</button>
        </span>
      </div>
    </div>`;
  document.body.appendChild(ov);

  const ta = ov.querySelector(".xml-ta");
  const statusEl = ov.querySelector(".xml-status");
  const applyBtn = ov.querySelector("[data-apply]");
  ta.value = baseline;
  const ed = attachCodeEditor(ta, { lang: xml, gutter: true, wrap: false, virtual: true });

  const setStatus = (msg, kind = "") => {
    statusEl.textContent = msg || "";
    statusEl.className = "xml-status" + (kind ? " " + kind : "");
  };
  const mark = (line, message) => {
    if (!ed) return;
    ed.setMarkers(line > 0 ? [{ line, message }] : []);
    if (line > 0) ed.focusLine(line);
  };
  const where = (r) => (r.line
    ? (r.column ? t("xml.whereColumn", { line: r.line, column: r.column }) : t("xml.where", { line: r.line }))
    : "");

  // The well-formedness check runs while typing, so the line in error is marked before
  // Apply is ever pressed — debounced, because a document is reparsed whole.
  let checkTimer = null;
  const check = () => {
    const r = checkWellFormed(ta.value);
    if (r.ok) {
      if (ed) ed.setMarkers([]);
      setStatus(ta.value === baseline ? "" : t("xml.wellFormed"), "");
      return r;
    }
    if (ed) ed.setMarkers(r.line > 0 ? [{ line: r.line, message: r.message }] : []);
    setStatus(t("xml.notWellFormed", { where: where(r), error: r.message }), "err");
    return r;
  };
  const onInput = () => {
    clearTimeout(checkTimer);
    checkTimer = setTimeout(check, 250);
  };
  ta.addEventListener("input", onInput);

  let closed = false;
  const finish = (force) => {
    if (closed) return;
    if (!force && ta.value !== baseline &&
        !window.confirm(t("xml.confirmDiscard"))) {
      return;
    }
    closed = true;
    clearTimeout(checkTimer);
    window.removeEventListener("keydown", onKey, true);
    ta.removeEventListener("input", onInput);
    if (ed) ed.destroy();
    ov.remove();
    if (openDialog === finish) openDialog = null;
  };
  openDialog = finish;

  let applying = false;
  const doApply = async () => {
    if (applying) return;
    clearTimeout(checkTimer);
    const text = ta.value;
    if (text === baseline) { finish(true); return; } // nothing to apply
    const r = check();
    if (!r.ok) { mark(r.line, r.message); return; }
    applying = true;
    applyBtn.disabled = true;
    setStatus(t("xml.applying"));
    let res;
    try {
      res = (await apply(text)) || {};
    } catch (e) {
      if (closed) return;
      const msg = firstLine(e && e.message) || t("xml.importFailed");
      const line = lineOf(e && e.message);
      mark(line, msg);
      setStatus(t("xml.notApplied." + subject, { where: line ? t("xml.where", { line }) : "", error: msg }), "err");
      return;
    } finally {
      applying = false;
      applyBtn.disabled = false;
    }
    if (closed) return;
    baseline = text;
    const warnings = (res.warnings || []).map((w) => (w && w.message) || String(w)).filter(Boolean);
    if (!warnings.length) {
      finish(true);
      if (toast) toast(t("xml.applied"), "ok");
      return;
    }
    // Applied, but the modeler set something aside — an unresolved reference, an
    // element it does not know. That is exactly what an author correcting the XML
    // needs to see, so the dialog stays open on it rather than closing over it.
    const line = lineOf(warnings[0]);
    mark(line, firstLine(warnings[0]));
    const more = warnings.length > 1 ? t("xml.more", { n: warnings.length - 1 }) : "";
    setStatus(plural("xml.withWarnings." + subject, warnings.length, {
      where: line ? t("xml.lineLower", { line }) : "", first: firstLine(warnings[0]), more,
    }), "warn");
  };

  // Bound on the window in the capture phase, the first stop of every key event, because
  // the editor underneath binds its own shortcuts (F4 Variables, F8 Auto-layout) on the
  // document in the capture phase and the Console binds Ctrl+K and the FEEL assistant's
  // Ctrl+Shift+E there too — a listener on the document would run after them, however
  // it stopped the event. A modal is modal: what is typed into it does not act on the
  // canvas it covers.
  //
  // The text area's own keys — Tab, Enter's indent, brackets — are code-editor.js's
  // handlers on the text area itself, so a key aimed there is only stopped when it is
  // one of those shortcuts. A key aimed anywhere else (a button, the backdrop) is
  // stopped outright; stopping it does not cancel its default, so Enter and Space still
  // press a focused button.
  const isShortcut = (e) => /^F\d+$/.test(e.key) ||
    ((e.ctrlKey || e.metaKey) && (e.key === "k" || e.key === "K" || (e.shiftKey && (e.key === "e" || e.key === "E"))));
  const onKey = (e) => {
    if (!ov.isConnected) { finish(true); return; }
    if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); finish(false); return; }
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); e.stopPropagation(); doApply(); return; }
    if (e.target !== ta || isShortcut(e)) e.stopPropagation();
  };
  window.addEventListener("keydown", onKey, true);

  ov.querySelector("[data-x]").addEventListener("click", () => finish(false));
  ov.querySelector("[data-cancel]").addEventListener("click", () => finish(false));
  applyBtn.addEventListener("click", doApply);
  ov.querySelector("[data-copy]").addEventListener("click", async () => {
    const ok = await copyText(ta.value);
    if (!closed) setStatus(t(ok ? "xml.copied" : "xml.copyFailed"), ok ? "ok" : "err");
  });

  ta.focus();
  ta.setSelectionRange(0, 0);
  ta.scrollTop = 0;
}
