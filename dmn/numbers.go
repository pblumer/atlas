package dmn

import (
	"encoding/json"

	tdmn "github.com/pblumer/temis/dmn"
)

// Restoring the numbers a decision result carries.
//
// temis hands a FEEL number back as its exact decimal **string**. That is a
// documented, deliberate contract (temis ADR-0007): temis holds numbers as
// arbitrary-precision decimals, and a float64 would round an amount on the way
// out. Atlas has a lossless home for exactly that shape — [model.VarNumber]'s
// Text *is* "the canonical decimal string" — so nothing needs rounding or
// reparsing to store a decision's number as a number.
//
// What was missing is only the knowledge of *which* strings are numbers, and the
// model itself is what says so: DMN declares a type for a decision's result and
// for each member of a structured one. So the declaration decides, and nothing
// else does. A string that merely looks like a decimal is left alone — a policy
// number, an article code, a Swiss postcode and `"0800"` all parse as decimals,
// and turning those into numbers would trade a visible wrong type for an
// invisible wrong value.
//
// The conversion happens here, inside the package that holds the compiled model,
// rather than at the seam that writes the variable: [evalDecision] is the one
// place every caller passes through — the business rule task, the try-a-decision
// endpoint, and the retained evaluation record (ADR-0066) — so they cannot come
// to disagree about what a result is.
//
// See ADR-draft-a-decision-result-keeps-its-declared-type.

// feelNumber is the canonical type name DMN and FEEL give the number type. temis
// canonicalizes the model's spelling (`feel:number` and a bare `number` both
// arrive as this) before it reaches any of the views read below.
const feelNumber = "number"

// declaredNumbers reads the model's own type declarations for one decision's
// result: whole is true when the result *itself* is declared a number, and members
// holds the names of a structured result's number-typed parts.
//
// Which accessor answers depends on the shape of the logic, and all three are
// needed because each shape declares its types somewhere else:
//
//   - the DRG node's data type covers a decision with a `<variable typeRef>`, and
//     temis already falls back there to a lone decision-table output column or a
//     literal expression's own type;
//   - a decision table's output columns cover the multi-output table, whose result
//     is a context keyed by column name;
//   - a boxed context's entries cover its members, and its result cell covers the
//     case where the context evaluates to that cell's value instead.
func declaredNumbers(defs *tdmn.Definitions, nodes []tdmn.GraphNode, decisionId string) (whole bool, members map[string]bool) {
	members = map[string]bool{}
	for _, n := range nodes {
		// Every name the evaluation accepts, this accepts: temis resolves a decision
		// by its id, its label or its FEEL identifier, so matching fewer of them would
		// mean the decision evaluates while its declared type is not found, and the
		// number quietly stays a string.
		if n.Type == "decision" && (n.ID == decisionId || n.Name == decisionId || n.VarName == decisionId) {
			whole = n.DataType == feelNumber
			break
		}
	}
	if tv, ok := defs.DecisionTable(decisionId); ok {
		for _, out := range tv.Outputs {
			if out.Name != "" && out.TypeRef == feelNumber {
				members[out.Name] = true
			}
		}
	}
	if cv, ok := defs.BoxedContext(decisionId); ok {
		// A result cell makes the context evaluate to that cell rather than to a
		// context of its entries, so its type is the whole result's.
		if cv.Result != "" && cv.ResultTypeRef == feelNumber {
			whole = true
		}
		for _, e := range cv.Entries {
			if e.Name != "" && e.TypeRef == feelNumber {
				members[e.Name] = true
			}
		}
	}
	return whole, members
}

// restoreNumbers puts back the numbers the conversion out of temis left as
// decimal strings, for exactly those values the model declares as numbers.
//
// The carrier is [json.Number]: a string underneath, so the exact decimal
// survives, and a distinct type, so nothing downstream has to guess again.
// [expr.FromJSON] already maps it to a FEEL number wherever it appears —
// including nested inside a list or a context — so the variable, the condition
// that reads it and the retained record all follow from this one step.
//
// It is deliberately shallow: it converts the result, the elements of a list
// result, and the members of a structured one. A number nested deeper than that
// keeps its string, which is stated rather than papered over — see the ADR.
func restoreNumbers(defs *tdmn.Definitions, nodes []tdmn.GraphNode, decisionId string, outputs map[string]any) map[string]any {
	if len(outputs) == 0 {
		return outputs
	}
	whole, members := declaredNumbers(defs, nodes, decisionId)
	if !whole && len(members) == 0 {
		return outputs // nothing is declared a number; there is nothing to restore
	}
	out := make(map[string]any, len(outputs))
	for name, v := range outputs {
		switch val := v.(type) {
		case string:
			if whole {
				out[name] = json.Number(val)
				continue
			}
			out[name] = v
		case []any:
			// A COLLECT table with no aggregation answers with a list, and the
			// output column's declared type is each element's.
			if !whole {
				out[name] = v
				continue
			}
			list := make([]any, len(val))
			for i, e := range val {
				if s, ok := e.(string); ok {
					list[i] = json.Number(s)
					continue
				}
				list[i] = e
			}
			out[name] = list
		case map[string]any:
			ctx := make(map[string]any, len(val))
			for k, mv := range val {
				if s, ok := mv.(string); ok && members[k] {
					ctx[k] = json.Number(s)
					continue
				}
				ctx[k] = mv
			}
			out[name] = ctx
		default:
			out[name] = v
		}
	}
	return out
}
