package compiler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pblumer/atlas/expr"
)

// entraAttributesExpr compiles an inline attributes JSON template into a single FEEL
// context expression (ADR-0172, amended). The template is authored in the modeler's
// JSON editor; a string value with a leading '=' is a FEEL expression evaluated over the
// instance's variables at call time (e.g. "displayName": "=vorname + \" \" + nachname"),
// and every other value — string, number, bool, null, nested object or array — is a
// literal. Turning the whole template into one FEEL context means it is parsed and
// compiled once at deploy (invariant I5) and evaluated to the request body once at
// runtime, reusing the same RestExpr the worker's other authored values use.
//
// An empty template returns the zero RestExpr: the task then falls back to its
// attributesVariable, so the two ways of supplying a body stay mutually exclusive.
func entraAttributesExpr(taskID, raw string) (RestExpr, error) {
	if strings.TrimSpace(raw) == "" {
		return RestExpr{}, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return RestExpr{}, fmt.Errorf("compiler: entra task %q has an invalid attributes JSON: %w", taskID, err)
	}
	if _, ok := v.(map[string]any); !ok {
		return RestExpr{}, fmt.Errorf("compiler: entra task %q attributes must be a JSON object of directory properties, not a %T", taskID, v)
	}
	feel, err := jsonTemplateToFeel(v)
	if err != nil {
		return RestExpr{}, fmt.Errorf("compiler: entra task %q attributes: %w", taskID, err)
	}
	e, err := expr.CompileAuto(feel)
	if err != nil {
		return RestExpr{}, fmt.Errorf("compiler: entra task %q attributes did not compile (a value's =expression may be malformed): %w", taskID, err)
	}
	return RestExpr{Expr: e}, nil
}

// jsonTemplateToFeel renders a decoded JSON template as FEEL source. Objects become FEEL
// contexts with string-literal keys (which sidestep every question of whether a
// property name is a valid FEEL identifier), arrays become FEEL lists, and a string
// leaf is either a FEEL sub-expression (leading '=') or a FEEL string literal. Keys are
// emitted in sorted order so the compiled expression is deterministic.
func jsonTemplateToFeel(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "null", nil
	case bool:
		if t {
			return "true", nil
		}
		return "false", nil
	case json.Number:
		return t.String(), nil
	case string:
		if s := strings.TrimSpace(t); strings.HasPrefix(s, "=") {
			inner := strings.TrimSpace(s[1:])
			if inner == "" {
				return "", fmt.Errorf("a value has an empty =expression")
			}
			// Parenthesized so the sub-expression stays one term inside the context,
			// whatever operators it contains.
			return "(" + inner + ")", nil
		}
		return feelStringLiteral(t), nil
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			p, err := jsonTemplateToFeel(e)
			if err != nil {
				return "", err
			}
			parts[i] = p
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			p, err := jsonTemplateToFeel(t[k])
			if err != nil {
				return "", err
			}
			parts = append(parts, feelStringLiteral(k)+": "+p)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	default:
		return "", fmt.Errorf("unsupported JSON value of type %T", v)
	}
}

// feelStringLiteral quotes a Go string as a FEEL string literal, escaping the characters
// that would otherwise end or break it.
func feelStringLiteral(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}
