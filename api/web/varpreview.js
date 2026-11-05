// How Operations shows a variable value that is longer than the place it is shown in.
//
// A process may hold a whole uploaded file in one string variable, or a list of every
// record it read. Printed whole, one such value fills the instance card of the
// overview, pushes the next instance a screen further down, and is still unreadable:
// a card is for recognising an instance, not for reading a file. So every surface that
// lists variables shows a long value as its beginning plus its size, and opens the
// whole of it in a window on request (editor.js, openValueWindow).
//
// The functions here are the pure part of that — what counts as long, what the preview
// is, how the size is said, and in which order a card lists its values — so the three
// surfaces (the overview card, the single-instance view and the replay's Variables tab)
// cut a value the same way.

import { fmtBytes } from "./storage.js";
import { plural } from "./i18n.js";

// CARD_CHARS is the preview of a value on an overview card: one line in a side panel.
export const CARD_CHARS = 80;
// FIELD_CHARS and FIELD_LINES bound a text value in the single-instance view and in the
// replay's table, which have more room than a card but are still a list of values.
export const FIELD_CHARS = 300;
export const FIELD_LINES = 3;
// CARD_VARS is how many values an overview card lists before "+ N more".
export const CARD_VARS = 5;

const lineCount = (text) => {
  let n = 1;
  for (let i = text.indexOf("\n"); i !== -1; i = text.indexOf("\n", i + 1)) n++;
  return n;
};

// isLongText reports whether a text exceeds maxChars characters or maxLines lines.
export function isLongText(text, maxChars, maxLines = 1) {
  const s = String(text ?? "");
  if (s.length > maxChars) return true;
  if (maxLines < 1) return false;
  return lineCount(s) > maxLines;
}

// previewText is the beginning of a text: at most maxLines lines and maxChars
// characters, ending in an ellipsis when anything was left out. A carriage return is
// dropped so a file saved on Windows previews like any other.
export function previewText(text, maxChars, maxLines = 1) {
  const s = String(text ?? "").replace(/\r/g, "");
  const lines = s.split("\n");
  let out = lines.slice(0, Math.max(1, maxLines)).join("\n");
  let cut = lines.length > maxLines;
  if (out.length > maxChars) {
    out = out.slice(0, maxChars);
    cut = true;
  }
  return cut ? out.replace(/\s+$/, "") + "…" : out;
}

// sizeText says how big a text is: its size in bytes as stored (UTF-8), and its line
// count when it has more than one line — "1.2 KiB · 16 lines". It is what tells a reader
// of a preview how much the preview left out.
export function sizeText(text) {
  const s = String(text ?? "");
  const bytes = new TextEncoder().encode(s).length;
  const lines = lineCount(s);
  const parts = [fmtBytes(bytes)];
  if (lines > 1) parts.push(plural("ops.vars.lines", lines));
  return parts.join(" · ");
}

// searchableNames reads a process's atlas:searchable declaration (ADR-0244): a
// comma-separated list of variable names, in the order the modeler declared them.
export function searchableNames(declaration) {
  return String(declaration || "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

// cardOrder is the order an overview card lists variables in. The variables the model
// declares searchable come first, in their declared order: they are the business values
// a case is found by (ADR-0244), and so the ones an operator recognises it by. The rest
// keep the order the server listed them in.
export function cardOrder(list, searchable) {
  const vars = Array.isArray(list) ? list : [];
  const names = Array.isArray(searchable) ? searchable : [];
  if (!names.length) return vars.slice();
  const rank = new Map(names.map((n, i) => [n, i]));
  const first = vars.filter((v) => rank.has(v.name) && !v.scope)
    .sort((a, b) => rank.get(a.name) - rank.get(b.name));
  const rest = vars.filter((v) => !(rank.has(v.name) && !v.scope));
  return first.concat(rest);
}

// downloadName is the file name a value is saved under: the variable's name, with
// anything a file system would refuse replaced, and an extension saying what is inside.
export function downloadName(name, isJSON) {
  const base = String(name || "value").replace(/[\\/:*?"<>|\s]+/g, "_") || "value";
  return `${base}.${isJSON ? "json" : "txt"}`;
}

// A listing asked for with ?values=preview (api/variablepreview.go) cuts a long value
// to its beginning and says what the whole is: truncated, size in bytes, lines for a
// text, shape and count for a structure. The two functions below read a variable the
// same way whether it arrived whole or cut, so a surface does not have to know which.

// variableSizeText is sizeText for a variable: from the server's figures when the
// value was cut, from the value itself when it is whole.
export function variableSizeText(v) {
  if (!v || !v.truncated) return sizeText(v ? v.value : "");
  const parts = [fmtBytes(v.size)];
  if (v.lines > 1) parts.push(plural("ops.vars.lines", v.lines));
  return parts.join(" · ");
}

// variableShape is a structure's shape and element count — {shape: "array"|"object",
// count} — or null for anything else. A cut JSON value cannot be parsed, so it is read
// off the server's figures; a whole one off the value.
export function variableShape(v) {
  if (!v || v.kind !== "json") return null;
  if (v.truncated) return v.shape ? { shape: v.shape, count: v.count || 0 } : null;
  try {
    const parsed = JSON.parse(v.value);
    if (Array.isArray(parsed)) return { shape: "array", count: parsed.length };
    if (parsed && typeof parsed === "object") return { shape: "object", count: Object.keys(parsed).length };
  } catch { /* not a structure */ }
  return null;
}

// A variable that names a document (ADR-draft-a-file-is-a-document-not-a-variable) holds
// a small JSON object, {"$document": "doc_…", name, contentType, size, sha256}, and the
// file itself is in the document store. documentRef reads one, or answers null for any
// other value. It is never cut by a preview: a reference is far below the 512 bytes a
// listing keeps.
const DOC_ID = /^doc_[a-z2-7]{26}$/;
export function documentRef(v) {
  if (!v || v.kind !== "json" || v.truncated) return null;
  let parsed;
  try { parsed = JSON.parse(v.value); } catch { return null; }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null;
  const id = parsed.$document;
  if (typeof id !== "string" || !DOC_ID.test(id)) return null;
  return {
    id,
    name: typeof parsed.name === "string" && parsed.name ? parsed.name : id,
    contentType: typeof parsed.contentType === "string" ? parsed.contentType : "",
    size: Number(parsed.size) || 0,
  };
}

// documentHref is where a document is downloaded from. The server decides whether the
// person asking may read it; the link carries no permission of its own.
export function documentHref(id) {
  return `/api/v1/documents/${encodeURIComponent(id)}`;
}

// documentSizeText says how big a referenced document is.
export function documentSizeText(ref) {
  return ref && ref.size ? fmtBytes(ref.size) : "";
}
