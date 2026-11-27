package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// Output is one field the model's answer must carry, as the model is told it
// (ADR-0567): the shape of <atlas:agentOutput>.
type Output struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required"`
	Values      []string `json:"values,omitempty"`
}

// outputsOf translates the compiled answer fields into what travels to a worker.
func outputsOf(cp *compiler.CompiledProcess, fields []compiler.AgentOutput) []Output {
	if len(fields) == 0 {
		return nil
	}
	out := make([]Output, 0, len(fields))
	for _, f := range fields {
		o := Output{Name: cp.Intern(f.Name), Type: cp.Intern(f.Type), Required: f.Required}
		if f.Description >= 0 {
			o.Description = cp.Intern(f.Description)
		}
		for _, v := range f.Values {
			o.Values = append(o.Values, cp.Intern(v))
		}
		out = append(out, o)
	}
	return out
}

// outputsFromPayload reads the answer fields back, in either shape they can arrive in.
func outputsFromPayload(v any) []Output {
	switch raw := v.(type) {
	case []Output:
		return raw
	case []any:
		// Over JSON: decode through the struct's own tags rather than field by field,
		// so the two ends cannot come to disagree about a name.
		data, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		var out []Output
		if json.Unmarshal(data, &out) != nil {
			return nil
		}
		return out
	}
	return nil
}

// answerSchema is the JSON Schema of the answer object: every declared field, closed
// against any other. strict reports whether every field is required, which is the
// condition under which the Chat Completions format can enforce the schema exactly.
func answerSchema(outputs []Output) (schema map[string]any, strict bool) {
	props := map[string]any{}
	required := []string{}
	strict = true
	for _, o := range outputs {
		prop := map[string]any{"type": o.Type}
		if o.Description != "" {
			prop["description"] = o.Description
		}
		if len(o.Values) > 0 {
			prop["enum"] = o.Values
		}
		props[o.Name] = prop
		if o.Required {
			required = append(required, o.Name)
		} else {
			strict = false
		}
	}
	return map[string]any{
		"type": "object", "properties": props, "required": required, "additionalProperties": false,
	}, strict
}

// outputInstruction says in words what the schema says in JSON. An endpoint that
// enforces the schema does not need it; one that speaks the wire format without that
// extension has nothing else to go on, and the answer is checked either way.
func outputInstruction(outputs []Output) string {
	if len(outputs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nWhen you answer, answer with one JSON object and nothing else — no prose around it, no code fence. Its fields:")
	for _, o := range outputs {
		fmt.Fprintf(&b, "\n- %s (%s", o.Name, o.Type)
		if o.Required {
			b.WriteString(", required")
		}
		b.WriteString(")")
		if len(o.Values) > 0 {
			fmt.Fprintf(&b, ", one of: %s", strings.Join(o.Values, ", "))
		}
		if o.Description != "" {
			fmt.Fprintf(&b, " — %s", o.Description)
		}
	}
	return b.String()
}

// structuredAnswer holds an answer to its declared fields and returns it as canonical
// JSON. This check is the guarantee, not the provider's: a schema sent with the request
// makes a conforming answer likely, and only an answer that passes here is written into
// the process, where a gateway will route on it.
func structuredAnswer(outputs []Output, text string) (string, error) {
	text = strings.TrimSpace(text)
	// A model told to answer in JSON sometimes fences it anyway. The fence is not the
	// answer, and refusing a correct object over it would fail a round for nothing.
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "```"))
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.UseNumber() // a decimal stays exactly what the model wrote
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return "", fmt.Errorf("the answer is not a JSON object: %v", err)
	}
	if dec.More() {
		return "", fmt.Errorf("the answer carries text after its JSON object")
	}
	declared := make(map[string]bool, len(outputs))
	for _, o := range outputs {
		declared[o.Name] = true
		v, present := obj[o.Name]
		if !present || v == nil {
			if o.Required {
				return "", fmt.Errorf("the answer lacks the required field %q", o.Name)
			}
			delete(obj, o.Name) // an optional field answered null is an absent one
			continue
		}
		ok := false
		switch o.Type {
		case "string":
			var s string
			if s, ok = v.(string); ok && len(o.Values) > 0 && !slices.Contains(o.Values, s) {
				return "", fmt.Errorf("the answer's %q is %q, which is not one of %s", o.Name, s, strings.Join(o.Values, ", "))
			}
		case "number":
			_, ok = v.(json.Number)
		case "boolean":
			_, ok = v.(bool)
		}
		if !ok {
			return "", fmt.Errorf("the answer's %q is not a %s", o.Name, o.Type)
		}
	}
	for name := range obj {
		if !declared[name] {
			return "", fmt.Errorf("the answer carries the field %q, which is not declared", name)
		}
	}
	canonical, err := json.Marshal(obj) // sorted keys: the canonical form (ADR-0037)
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

// applyOutputs turns a decision's free-text answer into the declared object. A decision
// that calls tools is not an answer yet and is left alone.
func applyOutputs(req Request, d *Decision) error {
	if len(req.Outputs) == 0 || len(d.ToolCalls) > 0 || len(d.Outputs) != 1 {
		return nil
	}
	canonical, err := structuredAnswer(req.Outputs, d.Outputs[0].Text)
	if err != nil {
		return err
	}
	d.Outputs[0] = model.VariableValue{Name: d.Outputs[0].Name, Kind: model.VarJSON, Text: canonical}
	return nil
}
