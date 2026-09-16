// dmn-warnings.js — author-time findings for a decision model.
//
// Both of the findings here are about the same thing: the two ways a model's
// requirements graph and its logic can disagree about a **knowledge model**.
//
// A knowledge model is a reusable FEEL function. DMN says a decision that invokes
// one declares a knowledge requirement for it, and the DRG draws that requirement as
// an arrow. temis does not enforce it: a decision whose expression calls a knowledge
// model by name evaluates correctly with no arrow at all. That was measured, not
// assumed — `dmn/knowledgemodel_test.go` pins the invocation, and the same harness
// says the edge is optional to the engine.
//
// Which is exactly why both cases are worth saying while the author is looking at
// the model, and why neither can be left to Deploy:
//
//   - **Nothing invokes this knowledge model.** It is dead weight. The model is
//     valid, its decisions deploy, the engine never complains — so there is no later
//     moment at which anybody finds out. A knowledge model that nothing calls looks
//     exactly like one that is called: the only difference is an arrow that is not
//     there.
//   - **A decision calls one without requiring it.** This runs, and draws a diagram
//     that is wrong. The diagram is what gets reviewed, and what goes into the
//     decision's published documentation, so a dependency missing from it is a
//     dependency nobody reviews.
//
// Both are warnings and never errors, because each describes a model that deploys
// and runs. And both are biased towards silence: an invocation is *anything* that
// looks like the name followed by `(`, anywhere in any expression of any other
// element, so an unusual way of calling one costs a missed warning rather than a
// false one. A warning an author learns to ignore is worse than no warning.

// refId reads the id out of a moddle reference. dmn-moddle keeps a requirement's
// target unresolved — `{ href: "#bkm_fee" }` — rather than pointing at the element,
// so the id has to come off the href. A reference into another model
// ("other.dmn#bkm") names nothing here, and is treated as reaching nothing local.
function refId(ref) {
  if (!ref) return "";
  if (ref.id) return ref.id;
  const href = ref.href || "";
  if (!href.startsWith("#")) return "";
  return href.slice(1);
}

// expressionTexts walks the DRG elements and returns every expression body with the
// element it belongs to. The walk is generic rather than a list of the places an
// expression can sit — a literal expression, a decision table's input expressions,
// its unary tests and its output entries all carry `text`, and so will whatever the
// pinned dmn-js fork supports next.
const REQUIREMENT_KEYS = new Set([
  "knowledgeRequirement", "informationRequirement", "authorityRequirement",
]);

function expressionTexts(drg) {
  const out = [];
  const seen = new Set();
  const walk = (node, owner) => {
    if (!node || typeof node !== "object" || seen.has(node)) return;
    seen.add(node);
    if (Array.isArray(node)) {
      for (const n of node) walk(n, owner);
      return;
    }
    if (typeof node.text === "string" && node.text) out.push({ owner, text: node.text });
    for (const key of Object.keys(node)) {
      // $parent would walk back up into the whole model, and the other $-keys are
      // moddle's own descriptors rather than model content.
      if (key.startsWith("$")) continue;
      // A requirement holds a reference and never an expression. It is skipped because
      // of what a reference might be: dmn-moddle leaves it as `{ href }` here, but were
      // it ever resolved to the element it names, descending into it would file another
      // element's expressions under this one — and a decision would then appear to make
      // every call its required knowledge model makes.
      if (REQUIREMENT_KEYS.has(key)) continue;
      const value = node[key];
      if (value && typeof value === "object") walk(value, owner);
    }
  };
  for (const el of drg) walk(el, el.id);
  return out;
}

const escapeRe = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

// withoutStrings blanks FEEL string literals. It matters in one direction only: the
// missing-requirement finding is the one that can be *wrong* rather than merely
// absent, and the way it would be wrong is a message like "fee(x) is not allowed
// here" sitting in a decision's own text and reading as a call. A blanked literal
// cannot produce that.
const withoutStrings = (text) => text.replace(/"(?:[^"\\]|\\.)*"/g, '""');

// callsByName reports whether text invokes name. A FEEL name may contain spaces
// ("base rate"), so the whole name is matched literally, followed by the open
// parenthesis that makes it a call rather than a mention.
function callsByName(text, name) {
  if (!name) return false;
  return new RegExp(escapeRe(name) + "\\s*\\(").test(withoutStrings(text));
}

// labelFor names an element for a message: its name, or its id when it has none —
// a knowledge model drawn and not yet named still has to be pointed at.
const labelFor = (el) => el.name || el.id || "this element";

// knowledgeModelFindings returns the warnings for a model, newest concern first:
// knowledge models nothing invokes, then decisions that invoke one without saying
// so. Each finding names the element it is about so the caller can point at it.
//
// definitions is dmn-js's `getDefinitions()` — the moddle model, which is available
// whichever view is open, so the findings do not go quiet when the author steps into
// a decision's table.
export function knowledgeModelFindings(definitions) {
  const drg = (definitions && definitions.drgElement) || [];
  const knowledgeModels = drg.filter((el) => el.$type === "dmn:BusinessKnowledgeModel");
  if (!knowledgeModels.length) return [];

  // Who requires what, by the graph's own edges.
  const requires = new Map(); // requiring element id → Set of required knowledge ids
  for (const el of drg) {
    const ids = new Set();
    for (const kr of el.knowledgeRequirement || []) {
      const id = refId(kr.requiredKnowledge);
      if (id) ids.add(id);
    }
    requires.set(el.id, ids);
  }
  const requiredAnywhere = new Set();
  for (const ids of requires.values()) for (const id of ids) requiredAnywhere.add(id);

  const texts = expressionTexts(drg);
  // callers: knowledge model id → the ids of the elements whose expressions call it.
  // An element calling itself does not count as something invoking it: a knowledge
  // model that only its own body mentions is still one nothing reaches.
  const callers = new Map();
  for (const bkm of knowledgeModels) {
    const found = new Set();
    for (const { owner, text } of texts) {
      if (owner !== bkm.id && callsByName(text, bkm.name)) found.add(owner);
    }
    callers.set(bkm.id, found);
  }

  const byId = new Map(drg.map((el) => [el.id, el]));
  const findings = [];

  for (const bkm of knowledgeModels) {
    if (requiredAnywhere.has(bkm.id) || callers.get(bkm.id).size) continue;
    findings.push({
      severity: "warning",
      rule: "knowledge-model-unused",
      element: bkm.id,
      label: labelFor(bkm),
      message: `Nothing invokes the knowledge model “${labelFor(bkm)}”. A knowledge model runs `
        + `only when a decision calls it, so this one is never evaluated — and nothing later `
        + `will say so: the model is valid, and its decisions deploy.`,
    });
  }

  for (const bkm of knowledgeModels) {
    for (const callerId of callers.get(bkm.id)) {
      if ((requires.get(callerId) || new Set()).has(bkm.id)) continue;
      const caller = byId.get(callerId);
      if (!caller) continue;
      findings.push({
        severity: "warning",
        rule: "knowledge-requirement-missing",
        element: callerId,
        label: labelFor(caller),
        message: `“${labelFor(caller)}” calls the knowledge model “${labelFor(bkm)}” but does not `
          + `require it. It will run, and the requirements graph will not show the dependency — `
          + `draw a knowledge requirement from “${labelFor(bkm)}” to it.`,
      });
    }
  }

  return findings;
}
