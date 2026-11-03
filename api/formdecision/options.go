package formdecision

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// MaxOptions bounds the options one decision gives a field. A choice among more than
// this is not made from a list a person scrolls through; it wants a search, which is
// what a directory field is (ADR-0473). A list over the bound is refused rather than
// cut, because a cut list would hide entries without saying which.
const MaxOptions = 1000

// Option is one choice as the form shows it: what a person reads, and what the form
// submits when it is chosen.
type Option struct {
	Label string `json:"label"`
	Value any    `json:"value"`
}

// OptionsOf turns a decision's result into options, the way form-js turns a select's
// `values` into options (normalizeOptionsData in @bpmn-io/form-js-viewer 1.24), so
// what the server checks a submission against is what the person was shown:
//
//   - a string, number or boolean is both the label and the value; an empty string is
//     no option;
//   - a context with a non-empty string `label` and a `value` that is such a primitive
//     or a non-empty context is taken as it is;
//   - a context with only a primitive `value` is labelled with that value;
//   - anything else — null, a list, a context without a usable value — is skipped and
//     counted, so the editor can say how many entries the form will not show.
//
// A result that is not a list at all is refused: the decision answers something else.
func OptionsOf(result any) ([]Option, int, error) {
	list, ok := result.([]any)
	if !ok {
		return nil, 0, fmt.Errorf("its result is %s, not a list", describe(result))
	}
	if len(list) > MaxOptions {
		return nil, 0, fmt.Errorf("its list has %d entries; a field offers at most %d", len(list), MaxOptions)
	}
	out := make([]Option, 0, len(list))
	skipped := 0
	for _, item := range list {
		if o, ok := option(item); ok {
			out = append(out, o)
		} else {
			skipped++
		}
	}
	return out, skipped, nil
}

func option(item any) (Option, bool) {
	if label, ok := primitiveLabel(item); ok {
		return Option{Label: label, Value: item}, true
	}
	ctx, ok := item.(map[string]any)
	if !ok {
		return Option{}, false
	}
	value := ctx["value"]
	if label, ok := ctx["label"].(string); ok && label != "" {
		if _, primitive := primitiveLabel(value); primitive {
			return Option{Label: label, Value: value}, true
		}
		if m, ok := value.(map[string]any); ok && len(m) > 0 {
			return Option{Label: label, Value: value}, true
		}
		return Option{}, false
	}
	if label, ok := primitiveLabel(value); ok {
		return Option{Label: label, Value: value}, true
	}
	return Option{}, false
}

// primitiveLabel is how form-js writes a primitive option's label (`${option}`), and
// false for anything that is not an allowed primitive.
func primitiveLabel(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, t != ""
	case bool:
		return strconv.FormatBool(t), true
	case json.Number:
		return t.String(), true
	case float64:
		// A number decoded without UseNumber; the registry hands numbers back as
		// json.Number (dmn/numbers.go) and every reader here decodes with it.
		return strconv.FormatFloat(t, 'f', -1, 64), true
	}
	return "", false
}

func describe(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "a context"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	}
	if _, ok := primitiveLabel(v); ok {
		return "a number"
	}
	return fmt.Sprintf("a %T", v)
}

// offers reports whether v is the value of one of the options. Values are compared as
// the JSON they travel as, so a number decoded from a request equals the same number
// from the decision whatever Go type each arrived in, and a context equals one with the
// same members.
func offers(options []Option, v any) bool {
	want, err := json.Marshal(v)
	if err != nil {
		return false
	}
	for _, o := range options {
		if got, err := json.Marshal(o.Value); err == nil && string(got) == string(want) {
			return true
		}
	}
	return false
}
