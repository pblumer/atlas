// varuse.js — which variables one element of a diagram reads, for the Modeler's
// Variables panel when an element is selected.
//
// What an element *writes* is declared: a result variable, an output mapping's target,
// a multi-instance's output collection. What it *reads* is not declared anywhere; it is
// whatever its expressions name. So this reads the expressions: every FEEL value the
// element carries (Atlas and Zeebe store one with a leading "="), the formal expressions
// that are FEEL by definition (a branch condition, a loop's completion condition), the
// {{ … }} placeholders of a template text (ADR-0487) and an agent's context list.
//
// It is a static reading of the model and says so: a name is reported because an
// expression mentions it, not because a run will reach it. Names an expression binds
// itself — `for x in`, `some x in`, a function's parameters, a filter's `item` — are not
// reads, nor is a word inside a string literal, a context key or a property after a dot.

import { tokenize } from "./feel.js";

// The formal expressions that hold FEEL whether or not they start with "=". A timer's
// duration (PT5M) or date is a literal unless it is "="-prefixed, so it is not here.
const FEEL_EXPRESSIONS = new Set(["conditionExpression", "completionCondition", "loopCardinality", "loopCondition", "condition"]);

// The words a FEEL expression binds for itself.
const BINDERS = new Set(["for", "some", "every"]);

// feelReads returns the variable paths a FEEL expression reads, in the order they first
// appear. A path is the name and, where one follows, its first member (`order.total` →
// "order.total"), so a caller can match a namespaced constant (application.NAME) as well
// as a plain variable; `rootOf` gives back the variable it starts at.
export function feelReads(src) {
  const out = [];
  const seen = new Set();
  if (typeof src !== "string" || !src.trim()) return out;
  const toks = tokenize(src.replace(/^\s*=/, "")).filter((t) => t.type !== "ws" && t.type !== "comment");
  const bound = new Set(["item"]);
  for (let i = 0; i < toks.length; i++) {
    const tk = toks[i];
    if (tk.type === "keyword" && BINDERS.has(tk.value)) {
      // for a in xs, b in ys return … — every name directly before an `in` is bound.
      for (let j = i + 1; j < toks.length - 1; j++) {
        if (toks[j].type === "name" && toks[j + 1].type === "keyword" && toks[j + 1].value === "in") bound.add(toks[j].value);
        if (toks[j].type === "keyword" && (toks[j].value === "return" || toks[j].value === "satisfies")) break;
      }
      continue;
    }
    if (tk.type === "keyword" && tk.value === "function") {
      // function(a, b) body — the parameters are the function's own.
      for (let j = i + 2; j < toks.length && toks[j].value !== ")"; j++) {
        if (toks[j].type === "name") bound.add(toks[j].value);
      }
      continue;
    }
    if (tk.type !== "name") continue;
    const prev = toks[i - 1];
    const next = toks[i + 1];
    if (prev && prev.type === "punct" && prev.value === ".") continue; // a member, not a variable
    if (next && next.type === "punct" && (next.value === ":" || next.value === "(")) continue; // a context key, a call
    if (bound.has(tk.value)) continue;
    let path = tk.value;
    const after = toks[i + 2];
    if (next && next.type === "punct" && next.value === "." && after && after.type === "name") path += "." + after.value;
    if (!seen.has(path)) { seen.add(path); out.push(path); }
  }
  return out;
}

// templateReads returns what the {{ … }} placeholders of a template text read: each
// placeholder is a FEEL expression, after the block word that opens it ({{#if x}},
// {{#loop xs}}) — and a {{/if}} or {{else}} reads nothing.
export function templateReads(text) {
  const out = [];
  if (typeof text !== "string" || text.indexOf("{{") < 0) return out;
  const re = /\{\{\s*([^}]*?)\s*\}\}/g;
  let m;
  while ((m = re.exec(text))) {
    let body = m[1];
    if (/^(\/|else\b)/.test(body)) continue;
    body = body.replace(/^#\s*\w+\s*/, "");
    for (const p of feelReads(body)) if (!out.includes(p)) out.push(p);
  }
  return out;
}

// rootOf is the variable a path starts at.
export function rootOf(path) {
  const i = path.indexOf(".");
  return i < 0 ? path : path.slice(0, i);
}

// expressionReads walks one moddle object — an element's extension elements, its loop
// characteristics, a condition — and returns the paths its expressions read. propName
// names the property obj was found under, which decides whether a formal expression
// without "=" is FEEL (a branch condition) or a literal (a timer's duration). It follows
// containment only: a reference ($parent, a sourceRef, a messageRef) leads to another
// object of the model, which the caller decides about.
export function expressionReads(obj, propName = "") {
  const out = [];
  const add = (paths) => { for (const p of paths) if (!out.includes(p)) out.push(p); };
  const visit = (o, propName, depth) => {
    if (!o || typeof o !== "object" || depth > 12) return;
    if (Array.isArray(o)) { for (const x of o) visit(x, propName, depth + 1); return; }
    // A description is prose for the reader; a word in it is not a read.
    if (o.$type === "bpmn:Documentation" || o.$type === "bpmn:TextAnnotation") return;
    if (o.$type === "bpmn:FormalExpression" || o.$type === "bpmn:Expression") {
      const body = o.body || "";
      if (/^\s*=/.test(body) || FEEL_EXPRESSIONS.has(propName)) add(feelReads(body));
      else add(templateReads(body));
      return;
    }
    const props = (o.$descriptor && o.$descriptor.properties) || [];
    for (const p of props) {
      if (p.isReference || p.name === "$parent") continue;
      const v = o[p.name];
      if (typeof v === "string") readValue(o, p.name, v, add);
      else if (v && typeof v === "object") visit(v, p.name, depth + 1);
    }
    // Attributes the schema does not describe still carry values the engine reads.
    for (const [k, v] of Object.entries(o.$attrs || {})) {
      if (typeof v === "string" && !/^xmlns(:|$)/.test(k)) readValue(o, k, v, add);
    }
  };
  visit(obj, propName, 0);
  return out;
}

function readValue(o, name, v, add) {
  if (/^\s*=/.test(v)) { add(feelReads(v)); return; }
  // An agent-driven container is handed the variables its context attribute lists by
  // name (ADR-0257); that is a read of each of them.
  if (name === "context" && o.$type === "atlas:AgentConnector") {
    add(v.split(",").map((s) => s.trim()).filter(Boolean));
    return;
  }
  add(templateReads(v));
}
