package api

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/model"
)

// A position's answers reach its processes
// (ADR-0441).
//
// What the orderer answered on a product's configuration form is on the order line
// (ADR-0358). The processes the product binds — its provisioning, its lifecycle, its
// return, an approval model of the installation's own — receive each answer as a
// variable of its own, under its field's key, so a task form shows the field and a
// model may declare it personal data (ADR-0314): the start act seals what the model
// declares, under the recipient's key. Atlas's own approval processes do not
// receive them. They declare nothing, so the answers would sit in their history in
// the clear; the approver reads them on the approval itself, from the order.

// orderAnswerVars are a line's answers as process variables, as the order holds
// them now — an amended answer included. Only a key the product's form still asks
// for goes in: the answers are the orderer's input, and a key the form does not
// name is one nobody asked for. A key already among taken, or one of the order's
// own variables, stays out, so an answer can never stand in for who the position is
// for or which approval applies. A form this server no longer holds passes nothing:
// without it there is no telling which keys were asked.
func (s *Server) orderAnswerVars(l order.Line, taken []model.VariableValue) ([]model.VariableValue, error) {
	id := strings.TrimSpace(l.ConfigForm)
	if id == "" || len(l.Config) == 0 {
		return nil, nil
	}
	rec, found, err := s.forms.Get(id)
	if err != nil || !found {
		return nil, err
	}
	asked := map[string]bool{}
	collectFormFieldKeys([]byte(rec.Schema), asked)
	have := make(map[string]bool, len(taken))
	for _, v := range taken {
		have[v.Name] = true
	}
	var out []model.VariableValue
	for _, k := range slices.Sorted(maps.Keys(l.Config)) {
		if !asked[k] || have[k] || catalog.OrderVariables[k] {
			continue
		}
		out = append(out, model.VariableValue{Name: k, Kind: model.VarString, Text: l.Config[k]})
	}
	return out, nil
}

// FormFields names the variables a form's fields write, for the catalogue's publish
// check (catalog.OrderFormProblems).
func (l processLookup) FormFields(formID string) ([]string, bool) {
	rec, found, err := l.s.forms.Get(strings.TrimSpace(formID))
	if err != nil || !found {
		return nil, false
	}
	keys := map[string]bool{}
	collectFormFieldKeys([]byte(rec.Schema), keys)
	return slices.Sorted(maps.Keys(keys)), true
}

// approvalAnswer is one answer as an approver reads it: the field's label where the
// form still has the field, its key where it does not.
type approvalAnswer struct {
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
	Value string `json:"value"`
}

// answersFor lays a line's answers out for its approver: in the order the form asks
// them, labelled as the form labels them, and after those any answer the form no
// longer asks for — the order is the record of what was answered, and a field
// removed since is still what the orderer said.
func (s *Server) answersFor(l order.Line) []approvalAnswer {
	if len(l.Config) == 0 {
		return nil
	}
	var fields []formField
	if id := strings.TrimSpace(l.ConfigForm); id != "" {
		if rec, found, err := s.forms.Get(id); err == nil && found {
			fields = formFieldsOf([]byte(rec.Schema))
		}
	}
	out := make([]approvalAnswer, 0, len(l.Config))
	shown := map[string]bool{}
	for _, f := range fields {
		if v, ok := l.Config[f.Key]; ok && !shown[f.Key] {
			out = append(out, approvalAnswer{Key: f.Key, Label: f.Label, Value: v})
			shown[f.Key] = true
		}
	}
	for _, k := range slices.Sorted(maps.Keys(l.Config)) {
		if !shown[k] {
			out = append(out, approvalAnswer{Key: k, Value: l.Config[k]})
		}
	}
	return out
}

// formField is one field of a form-js schema: its key and its label.
type formField struct {
	Key   string
	Label string
}

// formFieldsOf walks a form-js schema in the order it shows its fields, nested
// groups included. A schema that will not parse has no fields.
func formFieldsOf(schema []byte) []formField {
	var doc any
	if err := json.Unmarshal(schema, &doc); err != nil {
		return nil
	}
	var out []formField
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if k, ok := t["key"].(string); ok && k != "" {
				label, _ := t["label"].(string)
				out = append(out, formField{Key: k, Label: label})
			}
			walk(t["components"])
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(doc)
	return out
}
