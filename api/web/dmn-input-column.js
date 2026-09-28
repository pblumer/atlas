// dmn-input-column.js — a decision table's input columns, read and written against
// the requirements graph that feeds them.
//
// A decision table's input column carries a FEEL *expression*, not a reference to the
// requirement that feeds it, and DMN keeps it that way on purpose: one requirement
// can feed several columns ("applicant.age", "applicant.income"), one column can
// combine several ("amount / income"), and a requirement may be read only in an
// output. So there is no rule that says a requirement has a column.
//
// There is a sensible default, though, and it is the one an author writes out by hand
// nine times in ten: a column whose expression is the name the required element's
// value arrives under, typed as that element is typed. Offering it is what keeps the
// graph and the table from drifting apart in the first place — the other half being
// dmn-warnings.js, which reports the drift once it is there.
//
// Written against the moddle rather than the decision-table editor, because the
// requirement is drawn in the requirements graph and that editor is not loaded there.
// The elements it builds are the ones DmnFactory builds (an InputClause carrying a
// LiteralExpression; one empty UnaryTests per rule), so a column added here and one
// added from the table's own plus button are the same column.
//
// The default is only the first moment. A graph is edited after it is drawn, and each
// of those edits can walk the two apart again:
//
//   - **A provider is renamed.** The column keeps reading a name that no longer
//     exists. Followed automatically — the author renamed one thing, not two, and the
//     follow-up is queued into their own command so one undo takes both back.
//   - **A provider's type changes.** Same, on the column's typeRef.
//   - **A requirement is removed**, by deleting the arrow or the element at its far
//     end. The column is now unbound, and this is the one case that is *asked* about
//     rather than done: the column owns a cell in every rule, each of them a unary
//     test somebody wrote, so removing it silently would throw away logic.
//
// The reverse direction — a column typed in the table that the graph does not provide
// — is deliberately not written back here. A name typed into a table is also exactly
// what a typo looks like, so it is reported by dmn-warnings.js with a one-click repair
// rather than acted on.

// tableOf returns a decision's table, or null when its logic is something else or is
// not written yet — in which case there is no column to add and nothing to do.
export function tableOf(decision) {
  const logic = decision && decision.decisionLogic;
  return logic && logic.$type === "dmn:DecisionTable" ? logic : null;
}

// nameOf is the identifier a decision's logic reads an element's value under: its
// <variable name> where it declares one, its label otherwise. The rule the engine
// applies (dmn/validate.go), so the column this writes is one the engine resolves.
export const nameOf = (el) => (el && el.variable && el.variable.name) || (el && el.name) || "";

// declaredTypeOf is the type an element declares on its variable, or "" where it
// declares none. Kept apart from typeOf because the two questions differ: a column
// being *written* needs some type, a column being *followed* must not have its typing
// reset to a default by an element that never said anything about types.
const declaredTypeOf = (el) => (el && el.variable && el.variable.typeRef) || "";

// typeOf is the element's declared type, defaulted the way dmn-js defaults it for a
// column added by hand.
const typeOf = (el) => declaredTypeOf(el) || "string";

// expressionText is an input column's whole expression, trimmed. The one reading of
// "which name does this column read" used everywhere in this module.
const expressionText = (input) =>
  ((input && input.inputExpression && input.inputExpression.text) || "").trim();

// refId reads the id out of a moddle reference. dmn-moddle keeps a requirement's
// target unresolved — `{ href: "#input_1" }` — rather than pointing at the element, so
// the id has to come off the href; a reference into another model ("other.dmn#x")
// names nothing here. The same six lines as dmn-warnings.js's refId, kept separate
// because that module is the pure findings half and this one does the editing: neither
// should have to load the other to read a moddle reference.
function refId(ref) {
  if (!ref) return "";
  if (ref.id) return ref.id;
  const href = ref.href || "";
  return href.startsWith("#") ? href.slice(1) : "";
}

// requires reports whether the decision is given the provider's value, by the graph.
function requires(decision, provider) {
  for (const ir of (decision && decision.informationRequirement) || []) {
    const id = refId(ir.requiredInput) || refId(ir.requiredDecision);
    if (id && id === provider.id) return true;
  }
  return false;
}

// alreadyReads reports whether the table has a column for this name already. Compared
// on the expression's exact text, because that is what "a column for it" means here:
// a column that reads the name among other things is the author's own expression and
// not something to duplicate either.
function alreadyReads(table, name) {
  return (table.input || []).some((input) => expressionText(input) === name);
}

// nextId asks the moddle for an id in its own sequence, so nothing collides with what
// the editors generate. A moddle without the counter (an older dmn-moddle) still gets
// a unique id rather than an exception.
function nextId(model, prefix) {
  try {
    if (model && model.ids && typeof model.ids.nextPrefixed === "function") {
      return model.ids.nextPrefixed(prefix + "_");
    }
  } catch { /* fall through to the local counter */ }
  return prefix + "_" + Math.random().toString(36).slice(2, 10);
}

/**
 * The columns a requirement feeds: the ones whose whole expression is the name that
 * requirement provides.
 *
 * Two narrowings, and both are the same refusal to guess. Only the decisions that
 * actually require the element are looked at, so an unrelated decision whose table
 * happens to read a column of the same name is left alone — sharing a name is not
 * being fed by it. And only an expression that *is* the name counts: a column that
 * reads the name among other things ("amount * 2") is somebody's own FEEL, and
 * rewriting inside it would be this module implementing a FEEL parser. Those are
 * left to dmn-warnings.js to report once the name stops resolving.
 *
 * @param {ModdleElement} definitions
 * @param {ModdleElement} provider the element the requirement comes from
 * @param {string} name the name to match, which on a rename is the *old* one
 *
 * @return {Array<{ decision: ModdleElement, table: ModdleElement, input: ModdleElement }>}
 */
export function columnsFedBy(definitions, provider, name) {
  const out = [];
  if (!provider || !name) return out;
  for (const el of (definitions && definitions.drgElement) || []) {
    const table = tableOf(el);
    if (!table || el === provider || !requires(el, provider)) continue;
    for (const input of table.input || []) {
      if (expressionText(input) === name) out.push({ decision: el, table, input });
    }
  }
  return out;
}

/**
 * The columns of one table whose whole expression is a name.
 *
 * The same match as columnsFedBy, for the caller that already holds the requirement
 * being removed and so has no graph left to look it up in.
 *
 * @param {ModdleElement} table
 * @param {string} name
 *
 * @return {Array<ModdleElement>} the input clauses
 */
export function columnsNamed(table, name) {
  if (!table || !name) return [];
  return (table.input || []).filter((input) => expressionText(input) === name);
}

/**
 * The type a decision's table declares for the column that reads a name, or "" when no
 * column reads it or the column says nothing about its type.
 *
 * Read when an element is being created *from* a column: the author already said what
 * the thing is when they wrote the cells under it, and a new element typed "Any" would
 * make them say it twice.
 *
 * @param {ModdleElement} decision
 * @param {string} name
 *
 * @return {string}
 */
export function columnTypeIn(decision, name) {
  const [input] = columnsNamed(tableOf(decision), name);
  return (input && input.inputExpression && input.inputExpression.typeRef) || "";
}

/**
 * The names a decision is currently given, by the graph's own edges.
 *
 * Used to ask whether a column is still fed after a requirement was removed: a second
 * requirement may provide the same name, in which case nothing was lost and there is
 * nothing to ask about.
 *
 * @param {ModdleElement} definitions
 * @param {ModdleElement} decision
 *
 * @return {Set<string>}
 */
export function namesGivenTo(definitions, decision) {
  const drg = (definitions && definitions.drgElement) || [];
  const byId = new Map(drg.map((el) => [el.id, el]));
  const out = new Set();
  for (const ir of (decision && decision.informationRequirement) || []) {
    const name = nameOf(byId.get(refId(ir.requiredInput) || refId(ir.requiredDecision)));
    if (name) out.add(name);
  }
  return out;
}

/**
 * What every element in the graph provides, right now: the name its value arrives
 * under and the type it declares.
 *
 * Taken before an edit and compared after it, this is how a rename is noticed without
 * enumerating the commands that can perform one — and there are more of them than is
 * obvious (the canvas renames with `element.updateLabel`, the properties panel with
 * `element.updateProperties` for the label and `element.updateModdleProperties` for
 * the variable, and a peer's change arrives through neither). Keyed by the element
 * itself rather than by its id, so changing an id is a rename like any other and not
 * an element disappearing.
 *
 * @param {ModdleElement} definitions
 *
 * The label and the variable's name are kept apart from the name they resolve to,
 * because the gap between them is itself a thing to act on: a DRG element is created
 * with no variable at all, and the properties panel creates one — named after the
 * element as it is called at that moment — the first time a type is picked. After
 * that the label is decorative, and only a reader that can see both can tell a
 * renamed label that moved the variable with it from one that was left behind.
 *
 * @param {ModdleElement} definitions
 *
 * @return {Map<ModdleElement, { name: string, label: string, varName: string,
 *   typeRef: string }>}
 */
export function providedNames(definitions) {
  const out = new Map();
  for (const el of (definitions && definitions.drgElement) || []) {
    // A decision service provides nothing to a table: it is a boundary drawn around
    // decisions, and what it publishes is invoked, not read as a variable.
    if (el.$type === "dmn:DecisionService") continue;
    out.set(el, {
      name: nameOf(el),
      label: el.name || "",
      varName: (el.variable && el.variable.name) || "",
      typeRef: declaredTypeOf(el),
    });
  }
  return out;
}

/**
 * Point a column at a new name, and at the type that name now carries.
 *
 * @param {Object} modeling
 * @param {Shape} shape the decision's shape, for the change notification
 * @param {ModdleElement} input the input clause
 * @param {string} name
 * @param {string} [typeRef] left as it is when empty, so an element that declares no
 *   type does not push the table's own typing back to the default
 */
export function renameInputColumn(modeling, shape, input, name, typeRef) {
  const properties = { text: name };
  if (typeRef) properties.typeRef = typeRef;
  modeling.updateModdleProperties(shape, input.inputExpression, properties);
}

/**
 * Take a column out of a table, with the cell it owns in every rule.
 *
 * The cells are the reason this is asked about rather than done: each one is a unary
 * test somebody wrote, and a column removed silently takes all of them with it.
 *
 * @param {Object} modeling
 * @param {Shape} shape
 * @param {ModdleElement} table
 * @param {ModdleElement} input
 */
export function removeInputColumn(modeling, shape, table, input) {
  const index = (table.input || []).indexOf(input);
  if (index < 0) return;

  // The column goes last, for the same reason it arrives last in addInputColumn: a
  // table whose columns outnumber a rule's cells is a table the editor draws wrong and
  // the XML does not mean, so it must not exist between two commands.
  for (const rule of table.rule || []) {
    const entries = rule.inputEntry || [];
    if (index >= entries.length) continue;
    modeling.updateModdleProperties(shape, rule, {
      inputEntry: entries.filter((_, i) => i !== index),
    });
  }
  modeling.updateModdleProperties(shape, table, {
    input: (table.input || []).filter((i) => i !== input),
  });
}

/**
 * Add the column an information requirement implies, as part of whatever command is
 * running — so one undo takes the requirement and its column back together.
 *
 * @param {Object} modeling the requirements graph's modeling service
 * @param {Object} shape the decision's shape, for the change notification
 * @param {Object} decision the decision's business object
 * @param {Object} provider the business object of the element it requires
 *
 * @return {string} the name the column reads, or "" when nothing was added
 */
export function addInputColumn(modeling, shape, decision, provider) {
  const table = tableOf(decision);
  const name = nameOf(provider);
  if (!table || !name || alreadyReads(table, name)) return "";

  const model = decision.$model;
  if (!model || typeof model.create !== "function") return "";

  const expression = model.create("dmn:LiteralExpression", {
    id: nextId(model, "LiteralExpression"),
    text: name,
    typeRef: typeOf(provider),
  });
  const input = model.create("dmn:InputClause", {
    id: nextId(model, "InputClause"),
    inputExpression: expression,
  });
  expression.$parent = input;
  input.$parent = table;

  // The rules go first. A table whose columns outnumber a rule's cells is a table the
  // editor draws wrong and the XML does not mean, so it must not exist between two
  // commands — and commands queued here run in the order they are queued.
  for (const rule of table.rule || []) {
    const cell = model.create("dmn:UnaryTests", { id: nextId(model, "UnaryTests"), text: "" });
    cell.$parent = rule;
    modeling.updateModdleProperties(shape, rule, {
      inputEntry: (rule.inputEntry || []).concat(cell),
    });
  }
  modeling.updateModdleProperties(shape, table, {
    input: (table.input || []).concat(input),
  });
  return name;
}
