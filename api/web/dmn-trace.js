// dmn-trace.js — how a temis decision trace is drawn (ADR-0066).
//
// A trace says which decision tables ran, which rules matched, and why. Two places
// ask that question: **Operations → Decisions**, about an evaluation a running
// process made, and the decision editor's **Test** panel, about the model on screen
// (ADR-0326). They draw one picture, from here,
// so an author who learns to read a trace in the Modeler can read the same trace in
// Operations.
//
// The picture is the rule matrix temis's own Operate view uses: a row per rule,
// the input columns tinted by whether each condition held, and the matched rule
// highlighted. A rule that never matches — a string compared against a number, a
// stray space, a wrong type — shows its condition in red, which is the thing an
// author is usually looking for.

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// fmtVal renders one evaluated value the way a reader needs to see it: a string as
// itself, everything else as JSON, so a number that is really a string ("250" vs
// 250) is visible rather than hidden by the rendering.
export const fmtVal = (v) =>
  (v === null || v === undefined ? "null" : typeof v === "string" ? v : JSON.stringify(v));

// cellText renders a rule cell. An empty condition and the DMN "-" both mean "this
// column does not constrain this rule", so both read as one dash.
const cellText = (t) => { const s = (t ?? "").trim(); return s === "" || s === "-" ? "–" : s; };

// tablesOf pulls the decision tables out of whatever shape the trace arrived in.
// A decision with no table logic — a literal expression — traces to nothing, which
// is a valid answer and not an error.
export function tablesOf(trace) {
  return trace && Array.isArray(trace.tables) ? trace.tables : [];
}

// matchedRuleNumbers lists the 1-based rule numbers that fired across a trace, for
// the "Rule 2 fired" badge beside a result.
export function matchedRuleNumbers(trace) {
  const nums = [];
  for (const t of tablesOf(trace)) {
    for (const rule of (t.rules || [])) if (rule.matched) nums.push(rule.index + 1);
  }
  return [...new Set(nums)];
}

// renderTraceTable draws one decision table as a compact matrix. `n` numbers the
// table when a trace holds more than one; pass 0 for a single table.
export function renderTraceTable(tt, n) {
  const matched = (tt.rules || []).filter((r) => r.matched).map((r) => r.index + 1);
  const policy = (tt.hitPolicy || "U") + (tt.aggregation ? " " + tt.aggregation : "");
  const head = matched.length ? `Rule ${matched.join(", ")} fired` : "no rule fired";
  const ins = tt.inputs || [];
  const hr = `<tr><th class="mcol-idx">#</th>${ins.map((i) =>
    `<th>${esc(i.expression)} <code>= ${esc(fmtVal(i.value))}</code></th>`).join("")}<th>&rarr;</th></tr>`;
  const body = (tt.rules || []).map((r) => {
    const cells = ins.map((_, k) => {
      const c = r.conditions && r.conditions[k];
      const cls = c ? (c.matched ? "mcell is-ok" : "mcell is-no") : "mcell is-skip";
      return `<td class="${cls}">${c ? esc(cellText(c.entry)) : ""}</td>`;
    }).join("");
    const out = r.matched && r.outputs ? esc(r.outputs.map(fmtVal).join(", ")) : "";
    return `<tr class="mrule${r.matched ? " is-hit" : ""}"><td class="mcol-idx">${r.index + 1}</td>${cells}<td class="mout">${out}</td></tr>`;
  }).join("");
  return `<div class="mtable"><div class="mtable-head">${n ? `Table ${n} · ` : ""}${esc(head)}<span class="mtable-policy">${esc(policy)}</span></div>` +
    `<table class="mgrid">${hr}${body}</table></div>`;
}

// renderTrace draws every table in a trace, numbering them when there is more than
// one. A decision whose logic is a literal expression has no table to draw, and
// says so rather than rendering an empty frame.
export function renderTrace(trace) {
  const tables = tablesOf(trace);
  if (!tables.length) {
    return `<p class="muted">This decision has no table logic, so there are no rules to trace.</p>`;
  }
  return tables.map((tt, i) => renderTraceTable(tt, tables.length > 1 ? i + 1 : 0)).join("");
}
