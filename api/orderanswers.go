package api

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/formlookup"
	"github.com/pblumer/atlas/api/httpapi"
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

// FormFields names the fields of a form, for the catalogue's publish check and its
// warning (catalog.OrderFormProblems, catalog.AnswerWarnings): the variable each
// writes, and whether the form says its answer names nobody.
func (l processLookup) FormFields(formID string) ([]catalog.FormField, bool) {
	rec, found, err := l.s.forms.Get(strings.TrimSpace(formID))
	if err != nil || !found {
		return nil, false
	}
	var out []catalog.FormField
	for _, f := range formFieldsOf([]byte(rec.Schema)) {
		key := f.Key
		// A key addresses a nested value by path; the variable it writes is the root.
		if i := strings.IndexByte(key, '.'); i > 0 {
			key = key[:i]
		}
		out = append(out, catalog.FormField{Key: key, NotPersonal: f.NotPersonal})
	}
	return out, true
}

// PersonalVariables names what the newest deployed version of a process declares
// personal data, for the catalogue's publish warning.
func (l processLookup) PersonalVariables(processID string) (names []string, deployed bool) {
	processID = strings.TrimSpace(processID)
	if processID == "" {
		return nil, false
	}
	l.s.do(func() {
		if d := l.s.latestDeploymentOf(processID); d != nil && d.cp != nil {
			names, deployed = slices.Clone(d.cp.PersonalVariables()), true
		}
	})
	return names, deployed
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

// formField is one field of a form-js schema: its key, its label, and whether the
// form says its answer is not personal data — the custom property personal=false,
// which form-js keeps under "properties" as the editor's Custom properties write it.
type formField struct {
	Key         string
	Label       string
	NotPersonal bool
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
				f := formField{Key: k, Label: label}
				if props, ok := t["properties"].(map[string]any); ok {
					switch v := props["personal"].(type) {
					case string:
						f.NotPersonal = strings.EqualFold(strings.TrimSpace(v), "false")
					case bool:
						f.NotPersonal = !v
					}
				}
				out = append(out, f)
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

// The order form's directory fields (ADR-0473, amended) live here rather than in
// formlookups.go, beside the other readers of an order's answers, so the lookup area
// itself stays outside the shop (ADR-0515).

// lookupOrderForm asks the question placing an order asks — may this caller order from
// the release's catalogue — and returns the order form the release's item names, as the
// form store holds it now: the form the shop renders.
func (s *Server) lookupOrderForm(r *http.Request, releaseID, itemID string) (formlookup.Task, error) {
	rel, ok, err := s.catalogStore.Release(releaseID)
	if err != nil {
		return formlookup.Task{}, err
	}
	if !ok {
		return formlookup.Task{}, formlookup.ErrNoTask
	}
	allowed, err := s.catalogs.MayOrderFrom(httpapi.PrincipalFrom(r.Context()), rel.CatalogID)
	if err != nil {
		return formlookup.Task{}, err
	}
	if !allowed {
		return formlookup.Task{}, formlookup.ErrForbidden
	}
	schema, found, err := s.orderFormSchema(rel, itemID)
	if err != nil {
		return formlookup.Task{}, err
	}
	if !found {
		return formlookup.Task{}, formlookup.ErrNoTask
	}
	return formlookup.Task{Schema: schema}, nil
}

// orderFormSchema reads the order form a release's item names. found is false for an
// item the release does not hold, one without a form, or a form since deleted.
func (s *Server) orderFormSchema(rel catalog.Release, itemID string) (schema []byte, found bool, err error) {
	for _, it := range rel.Items {
		if it.ID != itemID {
			continue
		}
		formID := strings.TrimSpace(it.ConfigForm)
		if formID == "" {
			return nil, false, nil
		}
		rec, ok, err := s.forms.Get(formID)
		if err != nil || !ok {
			return nil, false, err
		}
		return []byte(rec.Schema), true, nil
	}
	return nil, false, nil
}

// checkOrderAnswers holds a basket's — or one corrected position's — answers to the
// directory fields of the order forms they answer, the check a task completion makes
// (checkTaskSubmission). The answers arrive as text; a directory field's value is text.
// It answers true when it wrote a refusal.
func (s *Server) checkOrderAnswers(w http.ResponseWriter, r *http.Request, rel catalog.Release, config map[string]map[string]string) bool {
	for itemID, answers := range config {
		schema, found, err := s.orderFormSchema(rel, itemID)
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "read order form: "+err.Error())
			return true
		}
		if !found || !strings.Contains(string(schema), formlookup.DataSourceMember) {
			continue
		}
		vars := make(map[string]any, len(answers))
		for k, v := range answers {
			vars[k] = v
		}
		if refusal := s.formLookup.CheckSubmission(r.Context(), schema, vars); refusal != nil {
			refusal.Write(w)
			return true
		}
	}
	return false
}
