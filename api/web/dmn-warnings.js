// dmn-warnings.js — author-time findings for a decision model.
//
// Every finding here is about one thing: the ways a model's requirements graph and
// its logic can disagree. The graph is what gets reviewed and what goes into the
// decision's published documentation; the logic is what runs. When they drift, the
// drawing is a claim nobody checked.
//
// Two families. The first is about a **knowledge model**, below. The second is about
// an **information requirement** — what a decision is given — and is the one an
// author meets first: a decision table's input column carries a FEEL expression, not
// a reference to the requirement, so nothing in DMN makes the two agree. That is
// deliberate (one requirement can feed several columns, one column can combine
// several requirements), which is why this reports the disagreement instead of
// forbidding it.
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
          + `require it. It will run, and the requirements graph will not show the dependency.`,
        // This one finding has a determinate repair, so it carries it: the missing edge
        // runs from the knowledge model to the element that calls it, and there is
        // nothing to choose. The other finding has no fix here on purpose — which
        // decision ought to call an uninvoked knowledge model is the author's to decide,
        // and a button that guessed would be writing their model for them.
        //
        // It is declared rather than performed: this module knows the model, not the
        // canvas, and the editor is what owns dmn-js.
        fix: { kind: "connect", source: bkm.id, target: callerId, label: "Draw the requirement" },
      });
    }
  }

  return findings;
}


// providedName is the identifier a decision's logic reads an element's value under:
// its <variable name> where it declares one, and its label otherwise. The same rule
// the engine applies (dmn/validate.go describeDecisions reads VarName, then Name), so
// a finding here and a diagnostic there are about the same name.
const providedName = (el) => (el && el.variable && el.variable.name) || (el && el.name) || "";

// SIMPLE_NAME matches a FEEL name as the decision-table editor writes one: a plain
// identifier, possibly with spaces in it ("input 1", "Decision 2"). It is how the
// unbound-input finding stays narrow. An input expression that is anything more — a
// comparison, a call, a path, arithmetic — may read a name this module cannot resolve
// without being a FEEL implementation, so those are passed over in silence rather
// than guessed at.
const SIMPLE_NAME = /^[A-Za-z_][A-Za-z0-9_]*(?: +[A-Za-z0-9_]+)*$/;

// mentionsName reports whether text reads name, as opposed to calling it. Bounded by
// what a FEEL name may not contain, so "amount" is not found inside "amount due".
//
// It errs towards finding a mention: "input" *is* found inside "input 1", which is
// wrong, and wrong in the safe direction — a requirement that looks used stays quiet,
// and a warning an author learns to ignore is worse than no warning.
function mentionsName(text, name) {
  if (!name) return false;
  const re = new RegExp("(^|[^A-Za-z0-9_])" + escapeRe(name) + "($|[^A-Za-z0-9_])");
  return re.test(withoutStrings(text));
}

// decisionTableOf returns a decision's table, or null when its logic is something
// else (a literal expression, an invocation) or is not written yet.
function decisionTableOf(decision) {
  const logic = decision && decision.decisionLogic;
  return logic && logic.$type === "dmn:DecisionTable" ? logic : null;
}

// requirementsOf lists what a decision is given: the required element, the name its
// value arrives under, and the requirement itself.
function requirementsOf(decision, byId) {
  const out = [];
  for (const ir of decision.informationRequirement || []) {
    const id = refId(ir.requiredInput) || refId(ir.requiredDecision);
    const provider = id && byId.get(id);
    if (!provider) continue; // a reference into another model names nothing here
    out.push({ requirement: ir, provider, name: providedName(provider) });
  }
  return out;
}

// informationRequirementFindings returns the ways a decision's graph and its table
// disagree about what it is given.
//
// Two findings, and they are not the same severity because they do not end the same
// way:
//
//   - **The table reads a name nothing provides.** This does not deploy: temis
//     answers `unknown variable`, at error severity, and Atlas's deploy gate refuses
//     the model (dmn/registry.go Deploy). So the author does find out — at Deploy or
//     Test, phrased as a FEEL variable, about a diagram they drew minutes ago. Said
//     here it is about the diagram, at the moment it stops being true.
//   - **A requirement is drawn that the table never reads.** This deploys, and runs.
//     Nothing anywhere says a word, which is exactly why it is worth saying: the
//     graph claims a dependency the decision does not have, and the graph is what is
//     reviewed.
//
// Both are silent while the author is still building the decision, and the two ways of
// being silent are mirror images of the same rule — a disagreement needs two sides.
// An arrow drawn before the logic is not a disagreement, and neither is a table
// written before the arrows: a decision with no information requirement at all has not
// yet said what it is given, so nothing its table reads can be said to be missing.
//
// What that costs, stated rather than discovered: a decision that has a table, reads a
// name and requires nothing does not deploy either, and is not reported here. It is
// also the state every decision passes through a minute after it is drawn — the table
// arrives with a default column called "input" — so reporting it would put a finding
// on the screen for every new decision, which is how an author learns to stop reading
// the strip. Deploy and Test still say so.
//
// definitions is dmn-js's `getDefinitions()` — the moddle model, available whichever
// view is open, so the findings do not go quiet inside a decision's own table.
export function informationRequirementFindings(definitions) {
  const drg = (definitions && definitions.drgElement) || [];
  const decisions = drg.filter((el) => el.$type === "dmn:Decision");
  if (!decisions.length) return [];

  const byId = new Map(drg.map((el) => [el.id, el]));
  const texts = expressionTexts(drg);
  const findings = [];

  // Every name the model has to offer, for the repair below. A name two elements
  // share resolves to neither: there would be nothing to choose between them.
  const providers = new Map(); // name → element, or null where it is ambiguous
  for (const el of drg) {
    const name = providedName(el);
    if (!name || el.$type === "dmn:DecisionService") continue;
    providers.set(name, providers.has(name) ? null : el);
  }

  for (const decision of decisions) {
    // Logic of some kind: a literal expression reads its requirements as much as a
    // table does, and an unused one is as wrong there.
    if (!decision.decisionLogic) continue;

    const table = decisionTableOf(decision);
    const requirements = requirementsOf(decision, byId);
    const given = new Set(requirements.map((r) => r.name).filter(Boolean));
    const mine = texts.filter((t) => t.owner === decision.id);

    // The table reads a name nothing gives it — once there is a graph to disagree with.
    for (const input of (table && requirements.length ? table.input : []) || []) {
      const text = ((input.inputExpression && input.inputExpression.text) || "").trim();
      if (!text || !SIMPLE_NAME.test(text) || given.has(text)) continue;

      // A repair only where there is one: exactly one element in the model answers to
      // that name, and it is not already required. Where none or several do, which
      // element ought to feed this decision is the author's to say, and a button that
      // guessed would be writing their model for them.
      const source = providers.get(text);
      const fix = source && source.id !== decision.id && !given.has(providedName(source))
        ? { kind: "connect", source: source.id, target: decision.id, label: "Draw the requirement" }
        : undefined;

      findings.push({
        severity: "error",
        rule: "decision-input-unbound",
        element: decision.id,
        label: labelFor(decision),
        message: `“${labelFor(decision)}” reads “${text}”, and nothing gives it that. `
          + `A decision is given what its requirements provide and nothing else, so this `
          + `column is empty at every evaluation — and the model will not deploy: the `
          + `engine reports it as an unknown variable.`,
        ...(fix ? { fix } : {}),
      });
    }

    // A requirement is drawn that the table never reads.
    for (const { provider, name } of requirements) {
      if (!name || mine.some(({ text }) => mentionsName(text, name))) continue;
      findings.push({
        severity: "warning",
        rule: "information-requirement-unused",
        element: decision.id,
        label: labelFor(decision),
        message: `“${labelFor(decision)}” requires “${labelFor(provider)}” and never reads it. `
          + `It deploys and it runs, so nothing later will say so — but the graph shows a `
          + `dependency the decision does not have, and the graph is what gets reviewed.`,
        // The repair is the column that would use it, which is the same edit drawing
        // the requirement offers to make. Declared, not performed: this module knows
        // the model, not the canvas.
        ...(table ? {
          fix: {
            kind: "add-input", source: provider.id, target: decision.id,
            label: "Add the input column",
          },
        } : {}),
      });
    }
  }

  return findings;
}
