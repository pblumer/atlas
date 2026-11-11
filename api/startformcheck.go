package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pblumer/atlas/api/formdecision"
	"github.com/pblumer/atlas/api/formlookup"
	"github.com/pblumer/atlas/model"
)

// The self-service start checks what its start form declares, on the server
// (ADR-0516).
//
// Every other door a form's answer comes through leaves required fields, types and
// bounds to form-js in the browser and holds only the shape on the server
// (publicstartfields.go): an operator is trusted with the start API, and a public
// link's visitor cannot be told anything useful. A person starting a granted process
// is neither. Their browser is not a boundary — the same POST can be sent by hand —
// and what they submit becomes the instance's variables, which the model then trusts
// without anybody having checked them. So this path refuses, before anything is
// sealed or started, a submission the form itself would not have let through.
//
// It reads the constraints form-js declares and nothing it computes. Deliberately
// not checked here, and said so rather than half-done:
//   - a field shown only under a condition (form-js `conditional`) is never
//     required, because whether it is shown is a FEEL expression over the other
//     values, which only the form evaluates;
//   - a `pattern`, because form-js tests it as a JavaScript regular expression and
//     Go's would accept and refuse different strings;
//   - fields inside a repeating dynamic list, beyond their shape: their rows are
//     checked by notOnForm, their values by the browser;
//   - options a form reads from a variable or an expression, which do not exist
//     before the instance does. A decision field's options are checked by
//     refuseDecisionValues, against the decision itself.

// formRule is what one keyed, submitted field of a start form declares.
type formRule struct {
	path              []string
	typ               string
	required          bool
	minLen, maxLen    *float64
	min, max          *float64
	options           map[string]bool // canonical JSON of the allowed values; nil when not a closed list
	serializeToString bool
}

// startFormRules reads the rules of every keyed field a schema submits. A schema
// that does not parse declares none; the shape check refuses every value then.
func startFormRules(schema []byte) []formRule {
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(schema))
	dec.UseNumber()
	if dec.Decode(&doc) != nil {
		return nil
	}
	var out []formRule
	collectFormRules(doc["components"], nil, false, &out)
	return out
}

func collectFormRules(components any, prefix []string, conditional bool, out *[]formRule) {
	list, _ := components.([]any)
	for _, c := range list {
		comp, ok := c.(map[string]any)
		if !ok {
			continue
		}
		cond := conditional || comp["conditional"] != nil
		switch typ, _ := comp["type"].(string); typ {
		case "group", "dynamiclist":
			at := prefix
			if p, _ := comp["path"].(string); p != "" {
				if repeating, _ := comp["isRepeating"].(bool); repeating {
					continue
				}
				at = append(append([]string(nil), prefix...), strings.Split(p, ".")...)
			}
			collectFormRules(comp["components"], at, cond, out)
		case mailViewType, chartType:
			// Shows a variable, submits nothing.
		default:
			key, _ := comp["key"].(string)
			if key == "" || comp["disabled"] == true {
				// A disabled field is never submitted; the shape check refuses a value for it.
				collectFormRules(comp["components"], prefix, cond, out)
				continue
			}
			r := formRule{path: append(append([]string(nil), prefix...), strings.Split(key, ".")...), typ: typ}
			r.serializeToString, _ = comp["serializeToString"].(bool)
			if v, ok := comp["validate"].(map[string]any); ok {
				r.required = v["required"] == true && !cond
				r.minLen, r.maxLen = number(v["minLength"]), number(v["maxLength"])
				r.min, r.max = number(v["min"]), number(v["max"])
			}
			r.options = staticOptions(comp)
			*out = append(*out, r)
		}
	}
}

// number reads a numeric constraint, nil when it is absent or not a number.
func number(v any) *float64 {
	n, ok := v.(json.Number)
	if !ok {
		return nil
	}
	f, err := n.Float64()
	if err != nil {
		return nil
	}
	return &f
}

// optionTypes are the components whose value is chosen from a list.
var optionTypes = map[string]bool{"radio": true, "select": true, "checklist": true, "taglist": true}

// staticOptions is the closed list of values a choice field offers, when the schema
// states one itself. A list read from a variable, an expression, a decision or a
// directory is not this check's to hold.
func staticOptions(comp map[string]any) map[string]bool {
	typ, _ := comp["type"].(string)
	if !optionTypes[typ] {
		return nil
	}
	for _, dynamic := range []string{"valuesKey", "valuesExpression", formdecision.Member, formlookup.DataSourceMember} {
		if _, ok := comp[dynamic]; ok {
			return nil
		}
	}
	values, ok := comp["values"].([]any)
	if !ok {
		return nil
	}
	out := map[string]bool{}
	for _, v := range values {
		if m, ok := v.(map[string]any); ok {
			if c, ok := canonical(m["value"]); ok {
				out[c] = true
			}
		}
	}
	return out
}

// canonical is a scalar's JSON text, the form both sides of an option compare in.
func canonical(v any) (string, bool) {
	switch v.(type) {
	case string, json.Number, bool:
		b, err := json.Marshal(v)
		return string(b), err == nil
	}
	return "", false
}

// startValueTree is the submitted variables as one JSON value tree, numbers kept as
// their text.
func startValueTree(vars []model.VariableValue) map[string]any {
	out := make(map[string]any, len(vars))
	for _, v := range vars {
		switch v.Kind {
		case model.VarString:
			out[v.Name] = v.Text
		case model.VarNumber:
			out[v.Name] = json.Number(v.Text)
		case model.VarBool:
			out[v.Name] = v.Bool
		case model.VarJSON:
			var x any
			dec := json.NewDecoder(strings.NewReader(v.Text))
			dec.UseNumber()
			if dec.Decode(&x) == nil {
				out[v.Name] = x
			}
		default:
			out[v.Name] = nil
		}
	}
	return out
}

func valueAtPath(tree map[string]any, path []string) (any, bool) {
	var cur any = tree
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[seg]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func blank(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	}
	return false
}

// startFormProblems names, sorted, every declared constraint a submission breaks.
func startFormProblems(rules []formRule, vars []model.VariableValue) []string {
	tree := startValueTree(vars)
	var out []string
	for _, r := range rules {
		name := strings.Join(r.path, ".")
		v, present := valueAtPath(tree, r.path)
		if !present || blank(v) {
			if r.required {
				out = append(out, name+" is required")
			}
			continue
		}
		out = append(out, r.problems(name, v)...)
	}
	sort.Strings(out)
	return out
}

func (r formRule) problems(name string, v any) []string {
	var out []string
	bound := func(f *float64) string { return strconv.FormatFloat(*f, 'f', -1, 64) }
	switch r.typ {
	case "textfield", "textarea", "datetime":
		s, ok := v.(string)
		if !ok {
			return []string{name + " must be text"}
		}
		n := float64(utf8.RuneCountInString(s))
		if r.minLen != nil && n < *r.minLen {
			out = append(out, fmt.Sprintf("%s must be at least %s characters", name, bound(r.minLen)))
		}
		if r.maxLen != nil && n > *r.maxLen {
			out = append(out, fmt.Sprintf("%s must be at most %s characters", name, bound(r.maxLen)))
		}
	case "number":
		var text string
		switch x := v.(type) {
		case json.Number:
			text = x.String()
		case string:
			if !r.serializeToString {
				return []string{name + " must be a number"}
			}
			text = x
		default:
			return []string{name + " must be a number"}
		}
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return []string{name + " must be a number"}
		}
		if r.min != nil && f < *r.min {
			out = append(out, fmt.Sprintf("%s must be at least %s", name, bound(r.min)))
		}
		if r.max != nil && f > *r.max {
			out = append(out, fmt.Sprintf("%s must be at most %s", name, bound(r.max)))
		}
	case "checkbox":
		if _, ok := v.(bool); !ok {
			return []string{name + " must be true or false"}
		}
	case "radio", "select":
		c, ok := canonical(v)
		if !ok {
			return []string{name + " must be one of its options"}
		}
		if r.options != nil && !r.options[c] {
			out = append(out, name+" is not one of its options")
		}
	case "checklist", "taglist":
		list, ok := v.([]any)
		if !ok {
			return []string{name + " must be a list of its options"}
		}
		for _, item := range list {
			c, ok := canonical(item)
			if !ok || (r.options != nil && !r.options[c]) {
				return []string{name + " holds a value that is not one of its options"}
			}
		}
	}
	return out
}
