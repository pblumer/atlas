// dmn-input-column.js — giving a decision table the column an information
// requirement implies.
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

// typeOf is the element's declared type, defaulted the way dmn-js defaults it for a
// column added by hand.
const typeOf = (el) => (el && el.variable && el.variable.typeRef) || "string";

// alreadyReads reports whether the table has a column for this name already. Compared
// on the expression's exact text, because that is what "a column for it" means here:
// a column that reads the name among other things is the author's own expression and
// not something to duplicate either.
function alreadyReads(table, name) {
  return (table.input || []).some((input) => {
    const text = ((input.inputExpression && input.inputExpression.text) || "").trim();
    return text === name;
  });
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
