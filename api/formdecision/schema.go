package formdecision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A decision field is a form-js select, radio group, checklist or tag list whose
// options are the list a deployed DMN decision answers with, named by its atlasDecision
// block (ADR-draft-form-options-from-a-decision):
//
//	{"type": "select", "key": "kostenstelle", "label": "Kostenstelle",
//	 "atlasDecision": {"version": 1, "decisionId": "Kostenstellen"}}
//
// The block stands where the field's `values`, `valuesKey` or `valuesExpression` would:
// it is a fifth options source beside form-js's three and the directory field's remote
// one (ADR-0473), and a field takes its options from one source. form-js keeps a member
// it does not know on export, so the form stays a form-js document.

// Member is the component member that makes a field a decision field.
const Member = "atlasDecision"

// Version is the one version of the block this server reads. A form written for a newer
// one is refused at save with that reason, rather than half-understood.
const Version = 1

// maxDecisionID bounds a decision name. temis has no limit of its own; a name longer
// than this is not one anybody typed.
const maxDecisionID = 256

// FieldTypes are the form-js fields that take their options from a source. A
// select and a radio group submit one value; a checklist and a tag list submit a list.
var FieldTypes = []string{"select", "radio", "checklist", "taglist"}

var multiTypes = []string{"checklist", "taglist"}

// otherSources are the members by which a field is given its options some other way:
// form-js's own three, and the directory field's remote data source (ADR-0473).
var otherSources = []string{"values", "valuesKey", "valuesExpression", "atlasDataSource"}

// Source is the atlasDecision block as a form states it.
type Source struct {
	Version    int    `json:"version"`
	DecisionID string `json:"decisionId"`
}

// Step is one hop from the submitted variables to a field's value. Each marks a list
// whose every element holds the rest of the path — a dynamic list's rows.
type Step struct {
	Name string
	Each bool
}

// Field is one decision field of a form, checked.
type Field struct {
	Key        string
	Label      string
	Type       string
	DecisionID string
	// Multi is true for a field that submits a list of values.
	Multi bool
	// Path is where the field's value sits in what the form submits: the paths of the
	// groups and dynamic lists around it, then its own key, both split at their dots as
	// form-js splits them.
	Path        []Step
	Required    bool
	Disabled    bool
	Conditional bool
}

// Form is what a schema says about its decision fields.
type Form struct {
	Fields []Field
}

// DecisionIDs are the decisions the form's fields name, each once, sorted.
func (f *Form) DecisionIDs() []string {
	var out []string
	for _, fl := range f.Fields {
		if !slices.Contains(out, fl.DecisionID) {
			out = append(out, fl.DecisionID)
		}
	}
	slices.Sort(out)
	return out
}

// Parse reads a form-js schema's decision fields and checks every one of them. A schema
// without any parses to none; a malformed atlasDecision is refused with the field and
// the reason.
//
// What it does not check is whether the decision is deployed, takes no inputs and
// answers with a list. That is the server's state, not the form's: it changes after the
// form is saved, and an import may well bring a form in before the decision it names.
// The form editor shows it while the field is edited, and a form rendered against a
// decision that cannot answer says so in the field.
func Parse(schema []byte) (*Form, error) {
	var doc any
	dec := json.NewDecoder(bytes.NewReader(schema))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("the form is not JSON: %w", err)
	}
	form := &Form{}
	var walkErr error
	var walk func(v any, prefix []Step)
	walk = func(v any, prefix []Step) {
		if walkErr != nil {
			return
		}
		switch t := v.(type) {
		case map[string]any:
			if _, ok := t[Member]; ok {
				fl, err := field(t, prefix)
				if err != nil {
					walkErr = err
					return
				}
				form.Fields = append(form.Fields, fl)
			}
			walk(t["components"], childPrefix(t, prefix))
		case []any:
			for _, e := range t {
				walk(e, prefix)
			}
		}
	}
	walk(doc, nil)
	if walkErr != nil {
		return nil, walkErr
	}
	return form, nil
}

// childPrefix is where a container puts its children's values: under its path, as
// rows of a list for a dynamic list, and where it is itself for a container without one.
func childPrefix(comp map[string]any, prefix []Step) []Step {
	path, _ := comp["path"].(string)
	steps := splitPath(path)
	if len(steps) == 0 {
		return prefix
	}
	if typ, _ := comp["type"].(string); typ == "dynamiclist" {
		steps[len(steps)-1].Each = true
	}
	return append(slices.Clone(prefix), steps...)
}

func splitPath(p string) []Step {
	var out []Step
	for _, seg := range strings.Split(strings.TrimSpace(p), ".") {
		if seg = strings.TrimSpace(seg); seg != "" {
			out = append(out, Step{Name: seg})
		}
	}
	return out
}

// field checks one component carrying an atlasDecision.
func field(comp map[string]any, prefix []Step) (Field, error) {
	key, _ := comp["key"].(string)
	key = strings.TrimSpace(key)
	name := key
	if name == "" {
		name = "(no key)"
	}
	fail := func(format string, a ...any) (Field, error) {
		return Field{}, fmt.Errorf("field %q: %s: %s", name, Member, fmt.Sprintf(format, a...))
	}
	if key == "" {
		return fail("a field whose options come from a decision needs a key: it is the variable the chosen value is submitted as")
	}
	typ, _ := comp["type"].(string)
	if !slices.Contains(FieldTypes, typ) {
		return fail("options come from a decision on a %s; this one is a %q", strings.Join(FieldTypes, ", "), typ)
	}
	for _, m := range otherSources {
		if _, ok := comp[m]; ok {
			return fail("a field takes its options from one source: this one names %s as well as the decision", m)
		}
	}
	raw, err := json.Marshal(comp[Member])
	if err != nil {
		return fail("%v", err)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return fail("%s must be an object", Member)
	}
	var src Source
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&src); err != nil {
		return fail("%v", err)
	}
	switch {
	case src.Version == 0:
		return fail("version is missing: this server reads version %d", Version)
	case src.Version > Version:
		return fail("version %d is newer than this server reads (%d)", src.Version, Version)
	case src.Version != Version:
		return fail("version %d is not one this server reads (%d)", src.Version, Version)
	}
	id := strings.TrimSpace(src.DecisionID)
	switch {
	case id == "":
		return fail("decisionId is missing: name the deployed decision whose list the options are")
	case strings.HasPrefix(id, "="):
		return fail("decisionId must be a plain decision name: it is resolved on the server, never evaluated from the form")
	case utf8.RuneCountInString(id) > maxDecisionID || strings.IndexFunc(id, unicode.IsControl) >= 0:
		return fail("decisionId %q is not a decision name", id)
	}
	fl := Field{
		Key:        key,
		Type:       typ,
		DecisionID: id,
		Multi:      slices.Contains(multiTypes, typ),
		Path:       append(slices.Clone(prefix), splitPath(key)...),
	}
	fl.Label, _ = comp["label"].(string)
	if v, ok := comp["validate"].(map[string]any); ok {
		fl.Required, _ = v["required"].(bool)
	}
	_, fl.Conditional = comp["conditional"]
	fl.Disabled, _ = comp["disabled"].(bool)
	return fl, nil
}

// Check is what a form save asks: may this schema be stored?
func Check(schema []byte) error {
	_, err := Parse(schema)
	return err
}

// valuesAt is every value the submitted data holds at path: one for a field outside a
// dynamic list, one per row inside one. An absent or null value is no value.
func valuesAt(v any, path []Step) []any {
	if len(path) == 0 {
		if v == nil {
			return nil
		}
		return []any{v}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	next, ok := m[path[0].Name]
	if !ok {
		return nil
	}
	if !path[0].Each {
		return valuesAt(next, path[1:])
	}
	rows, ok := next.([]any)
	if !ok {
		return nil
	}
	var out []any
	for _, row := range rows {
		out = append(out, valuesAt(row, path[1:])...)
	}
	return out
}
