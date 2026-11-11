// decision-logic.js — what one DRG element computes, drawn from the engine's own view.
//
// The Modeler's decision peek (ADR-draft-decision-peek) shows a business rule task's decision as a
// graph the author can walk: pick a node and its logic shows underneath. That logic
// is not parsed here. The server hands each decision's and knowledge model's boxed
// expression over as temis's typed view (GET /api/v1/dmnrefs/{id}/logic) — the same
// views temis serves its own editors — so this module only draws shapes it is given:
// a decision table, a literal expression, a context, a list, a relation, an
// invocation, a conditional, a filter, an iterator. That is DMN 1.5's set of boxed
// expressions as the engine that evaluates them knows it, rather than the subset a
// browser-side XML reader happened to be taught.
//
// It draws and decides nothing else, so it can be driven from a test with a view
// and nothing mounted.

import { t } from "./i18n.js";

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// cell renders a FEEL cell. An empty entry and the DMN "-" both mean "this column
// does not constrain this rule", and read as one dash, as in the trace matrix.
const cell = (s) => {
  const v = String(s ?? "").trim();
  return v === "" || v === "-" ? "–" : esc(v);
};

const code = (s) => `<code class="drg-feel">${cell(s)}</code>`;
const typed = (typeRef) => (typeRef ? ` <span class="drg-type">${esc(typeRef)}</span>` : "");

// KIND_KEYS name a logic kind for a reader. The key is the kind temis reports.
const KIND_KEYS = {
  table: "peek.kind.table",
  literal: "peek.kind.literal",
  context: "peek.kind.context",
  list: "peek.kind.list",
  relation: "peek.kind.relation",
  invocation: "peek.kind.invocation",
  conditional: "peek.kind.conditional",
  filter: "peek.kind.filter",
  iterator: "peek.kind.iterator",
  function: "peek.kind.function",
};

// logicKindLabel names a logic kind, or says that it is one the peek does not know.
export function logicKindLabel(kind) {
  return KIND_KEYS[kind] ? t(KIND_KEYS[kind]) : kind;
}

function tableHTML(v) {
  const ins = v.inputs || [], outs = v.outputs || [], rules = v.rules || [];
  const notes = rules.some((r) => (r.annotations || []).some((a) => String(a).trim()));
  const policy = (v.hitPolicy || "U") + (v.aggregation ? " " + v.aggregation : "");
  const head = `<tr><th class="mcol-idx">#</th>${ins.map((i) =>
    `<th>${esc(i.label || i.expression)}${typed(i.typeRef)}</th>`).join("")}${outs.map((o) =>
    `<th class="mout">&rarr; ${esc(o.label || o.name)}${typed(o.typeRef)}</th>`).join("")}${notes ? `<th>${esc(t("peek.table.note"))}</th>` : ""}</tr>`;
  const body = rules.map((r, i) => `<tr class="mrule"><td class="mcol-idx">${i + 1}</td>${ins.map((_, k) =>
    `<td>${cell((r.inputEntries || [])[k])}</td>`).join("")}${outs.map((_, k) =>
    `<td class="mout">${cell((r.outputEntries || [])[k])}</td>`).join("")}${notes ? `<td>${esc((r.annotations || []).join(" · "))}</td>` : ""}</tr>`).join("");
  return `<div class="mtable"><div class="mtable-head">${esc(t("peek.table.rules", { n: rules.length }))}<span class="mtable-policy" title="${esc(t("peek.table.policy"))}">${esc(policy)}</span></div>` +
    `<table class="mgrid">${head}${body}</table></div>`;
}

// pairs draws a two-column table of a name and its FEEL, which is what a context, an
// invocation's bindings, a conditional and a filter all are when read.
function pairs(rows) {
  return `<table class="mgrid drg-pairs">${rows.map(([k, v]) =>
    `<tr><th>${k}</th><td>${v}</td></tr>`).join("")}</table>`;
}

// renderLogic draws one element's logic from its ElementLogic record. A kind the
// engine names but has no view for, and an element with no logic at all, are said
// in words rather than drawn as an empty box.
export function renderLogic(el) {
  if (!el) return `<p class="muted">${esc(t("peek.logic.none"))}</p>`;
  const v = el.view;
  const params = (el.params || []).length
    ? `<p class="drg-params">${esc(t("peek.bkm.params"))} ${(el.params || []).map((p) =>
      `<code class="drg-feel">${esc(p.name)}</code>${typed(p.typeRef)}`).join(", ")}</p>`
    : "";
  if (!el.kind) return params + `<p class="muted">${esc(t("peek.logic.none"))}</p>`;
  if (!v) return params + `<p class="muted">${esc(t("peek.logic.noView", { kind: logicKindLabel(el.kind) }))}</p>`;
  switch (el.kind) {
    case "table":
      return params + tableHTML(v);
    case "literal":
      return params + `<pre class="drg-code">${esc(v.text)}</pre>${v.typeRef ? `<p class="drg-params">${esc(t("peek.literal.type"))}${typed(v.typeRef)}</p>` : ""}`;
    case "context": {
      const rows = (v.entries || []).map((e) => [
        esc(e.name) + typed(e.typeRef),
        e.childKind ? `<span class="drg-tag">${esc(logicKindLabel(e.childKind))}</span> ${code(e.text)}` : code(e.text),
      ]);
      if (v.result) rows.push([`<i>${esc(t("peek.context.result"))}</i>${typed(v.resultTypeRef)}`, code(v.result)]);
      return params + pairs(rows);
    }
    case "list":
      return params + `<ol class="drg-list">${(v.items || []).map((i) => `<li>${code(i)}</li>`).join("")}</ol>`;
    case "relation":
      return params + `<table class="mgrid"><tr>${(v.columns || []).map((c) => `<th>${esc(c)}</th>`).join("")}</tr>${(v.rows || []).map((r) =>
        `<tr>${(v.columns || []).map((_, k) => `<td>${cell(r[k])}</td>`).join("")}</tr>`).join("")}</table>`;
    case "invocation":
      return params + `<p class="drg-params">${esc(t("peek.invocation.calls"))} <code class="drg-feel">${esc(v.called)}</code></p>` +
        pairs((v.bindings || []).map((b) => [esc(b.parameter), code(b.value)]));
    case "conditional":
      return params + pairs([["if", code(v.if)], ["then", code(v.then)], ["else", code(v.else)]]);
    case "filter":
      return params + pairs([[esc(t("peek.filter.in")), code(v.in)], [esc(t("peek.filter.match")), code(v.match)]]);
    case "iterator":
      return params + pairs([
        [esc(v.kind || "for"), `<code class="drg-feel">${esc(v.variable)}</code> in ${code(v.in)}`],
        [v.kind === "for" ? "return" : "satisfies", code(v.body)],
      ]);
    default:
      return params + `<p class="muted">${esc(t("peek.logic.noView", { kind: logicKindLabel(el.kind) }))}</p>`;
  }
}
