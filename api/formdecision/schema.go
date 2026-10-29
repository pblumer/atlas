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
//	{"type": "select", "key": "mitglied", "label": "Mitglied",
//	 "atlasDecision": {"version": 1, "decisionId": "Mitglieder",
//	   "inputs": [{"name": "gruppe", "field": "gruppe"}, {"name": "land", "value": "CH"}]}}
//
// The block stands where the field's `values`, `valuesKey` or `valuesExpression` would:
// it is a fifth options source beside form-js's three and the directory field's remote
// one (ADR-0473), and a field takes its options from one source. form-js keeps a member
// it does not know on export, so the form stays a form-js document.
//
// Each of the decision's inputs is either bound to another field of the form — what the
// person chose there is the input, so choosing a group decides which members the next
// field offers — or fixed. There is no expression language in the block: the only input
// at run time is the person's own form state, and the binding says everything an
// expression over it would, without an evaluation surface (ADR-0473 decided the same for
// its filters).

// Member is the component member that makes a field a decision field.
const Member = "atlasDecision"

// Version is the one version of the block this server reads. A form written for a newer
// one is refused at save with that reason, rather than half-understood.
const Version = 1

// Bounds on what a block may say. Product rules, not installation budgets.
const (
	// maxDecisionID bounds a decision name. temis has no limit of its own; a name
	// longer than this is not one anybody typed.
	maxDecisionID = 256
	// maxInputs bounds the inputs one field binds; a decision taking more is not one
	// whose answer is a list of choices.
	maxInputs = 16
	// maxValueBytes bounds a fixed input's JSON, and a bound field's value in a request.
	maxValueBytes = 4096
)

// FieldTypes are the form-js fields that take their options from a source. A
// select and a radio group submit one value; a checklist and a tag list submit a list.
var FieldTypes = []string{"select", "radio", "checklist", "taglist"}

var multiTypes = []string{"checklist", "taglist"}

// otherSources are the members by which a field is given its options some other way:
// form-js's own three, and the directory field's remote data source (ADR-0473).
var otherSources = []string{"values", "valuesKey", "valuesExpression", "atlasDataSource"}

// Source is the atlasDecision block as a form states it.
type Source struct {
	Version    int     `json:"version"`
	DecisionID string  `json:"decisionId"`
	Inputs     []Input `json:"inputs,omitempty"`
}

// Input is one of the decision's inputs as a block states it: bound to another field of
// the form by its key, or a fixed value.
type Input struct {
	Name  string          `json:"name"`
	Field string          `json:"field,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// Binding is one input of a decision field, checked: the input's name, and either the
// key of the field it is bound to or its fixed value.
type Binding struct {
	Name  string
	Field string
	Value any
}

// Fixed reports whether the input has a fixed value rather than a field's.
func (b Binding) Fixed() bool { return b.Field == "" }

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
	Inputs     []Binding
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

// BoundFields reports whether any input of the field is bound to another field — which
// is what makes its options depend on what the person chooses.
func (f Field) BoundFields() bool {
	for _, b := range f.Inputs {
		if !b.Fixed() {
			return true
		}
	}
	return false
}

// keyed is what the form says about a field another field's input may be bound to.
type keyed struct {
	Path []Step
	// Static holds the values of the field's own static options; HasStatic is false for
	// a field that has none.
	Static    []Option
	HasStatic bool
	// Decision is true for a field whose own options come from a decision.
	Decision   bool
	Default    any
	HasDefault bool
}

// Form is what a schema says about its decision fields and the fields they are bound to.
type Form struct {
	Fields []Field
	keys   map[string]keyed
	counts map[string]int
}

// Field finds the decision field with this key.
func (f *Form) Field(key string) (Field, bool) {
	for _, fl := range f.Fields {
		if fl.Key == key {
			return fl, true
		}
	}
	return Field{}, false
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

// closure is every field whose value the options of the decision field depend on: the
// fields its inputs are bound to, and theirs in turn where those are decision fields.
// The save refuses a cycle, so the walk ends.
func (f *Form) closure(fl Field) []string {
	var out []string
	var visit func(Field)
	visit = func(x Field) {
		for _, b := range x.Inputs {
			if b.Fixed() || slices.Contains(out, b.Field) {
				continue
			}
			out = append(out, b.Field)
			if next, ok := f.Field(b.Field); ok {
				visit(next)
			}
		}
	}
	visit(fl)
	return out
}

// Parse reads a form-js schema's decision fields and checks every one of them. A schema
// without any parses to none; a malformed atlasDecision is refused with the field and
// the reason.
//
// What it does not check is whether the decision is deployed, which inputs it takes and
// whether it answers with a list. That is the server's state, not the form's: it changes
// after the form is saved, and an import may well bring a form in before the decision it
// names. The form editor shows it while the field is edited, and a form rendered against
// a decision that cannot answer says so in the field.
func Parse(schema []byte) (*Form, error) {
	var doc any
	dec := json.NewDecoder(bytes.NewReader(schema))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("the form is not JSON: %w", err)
	}
	form := &Form{keys: map[string]keyed{}, counts: map[string]int{}}
	type pending struct {
		comp   map[string]any
		prefix []Step
	}
	var found []pending
	var walk func(v any, prefix []Step)
	walk = func(v any, prefix []Step) {
		switch t := v.(type) {
		case map[string]any:
			if k, ok := t["key"].(string); ok && strings.TrimSpace(k) != "" {
				k = strings.TrimSpace(k)
				form.counts[k]++
				form.keys[k] = keyedOf(t, append(slices.Clone(prefix), splitPath(k)...))
			}
			if _, ok := t[Member]; ok {
				found = append(found, pending{comp: t, prefix: prefix})
			}
			walk(t["components"], childPrefix(t, prefix))
		case []any:
			for _, e := range t {
				walk(e, prefix)
			}
		}
	}
	walk(doc, nil)

	for _, p := range found {
		fl, err := field(p.comp, p.prefix, form)
		if err != nil {
			return nil, err
		}
		form.Fields = append(form.Fields, fl)
	}
	if err := form.refuseCycles(); err != nil {
		return nil, err
	}
	return form, nil
}

// keyedOf reads what a keyed component says about the value another field may be bound
// to: where it is, the options it fixes itself, whether its own options come from a
// decision, and the value it starts with.
func keyedOf(comp map[string]any, path []Step) keyed {
	k := keyed{Path: path}
	if vals, ok := comp["values"].([]any); ok {
		k.HasStatic = true
		k.Static, _, _ = OptionsOf(vals)
	}
	_, k.Decision = comp[Member]
	if d, ok := comp["defaultValue"]; ok {
		k.Default, k.HasDefault = d, true
	}
	return k
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

// rowsPrefix is the part of a path down to its innermost dynamic list: the rows a value
// at that path belongs to.
func rowsPrefix(path []Step) []Step {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].Each {
			return path[:i+1]
		}
	}
	return nil
}

// eachCount is how many dynamic lists a path passes through.
func eachCount(path []Step) int {
	n := 0
	for _, s := range path {
		if s.Each {
			n++
		}
	}
	return n
}

// field checks one component carrying an atlasDecision.
func field(comp map[string]any, prefix []Step, form *Form) (Field, error) {
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
	if form.counts[key] > 1 {
		return fail("another field of this form has the key %q too, so which one the options are asked for is undefined", key)
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
	case !plainName(id):
		return fail("decisionId %q is not a decision name", id)
	}
	path := append(slices.Clone(prefix), splitPath(key)...)
	if len(src.Inputs) > maxInputs {
		return fail("inputs names %d inputs; a field binds at most %d", len(src.Inputs), maxInputs)
	}
	var bindings []Binding
	for _, in := range src.Inputs {
		b, msg := binding(in, key, path, form)
		if msg != "" {
			return fail("inputs: %s", msg)
		}
		if slices.ContainsFunc(bindings, func(x Binding) bool { return x.Name == b.Name }) {
			return fail("inputs: %q is given twice", b.Name)
		}
		bindings = append(bindings, b)
	}
	fl := Field{
		Key:        key,
		Type:       typ,
		DecisionID: id,
		Inputs:     bindings,
		Multi:      slices.Contains(multiTypes, typ),
		Path:       path,
	}
	fl.Label, _ = comp["label"].(string)
	if v, ok := comp["validate"].(map[string]any); ok {
		fl.Required, _ = v["required"].(bool)
	}
	_, fl.Conditional = comp["conditional"]
	fl.Disabled, _ = comp["disabled"].(bool)
	return fl, nil
}

// binding checks one input of a field, and answers why not where it does not hold.
func binding(in Input, own string, path []Step, form *Form) (Binding, string) {
	name := strings.TrimSpace(in.Name)
	if name == "" || !plainName(name) {
		return Binding{}, fmt.Sprintf("%q is not the name of a decision input", in.Name)
	}
	bound := strings.TrimSpace(in.Field)
	if (bound == "") == (in.Value == nil) {
		return Binding{}, fmt.Sprintf("%q needs exactly one of field (bound to another field) and value (fixed)", name)
	}
	if bound == "" {
		if len(in.Value) > maxValueBytes {
			return Binding{}, fmt.Sprintf("the fixed value of %q is larger than %d bytes", name, maxValueBytes)
		}
		var v any
		if err := json.Unmarshal(in.Value, &v); err != nil {
			return Binding{}, fmt.Sprintf("the fixed value of %q is not JSON: %v", name, err)
		}
		return Binding{Name: name, Value: v}, ""
	}
	switch {
	case bound == own:
		return Binding{}, fmt.Sprintf("%q is bound to the field itself", name)
	case form.counts[bound] == 0:
		return Binding{}, fmt.Sprintf("%q is bound to %q, which is not a field of this form", name, bound)
	case form.counts[bound] > 1:
		return Binding{}, fmt.Sprintf("%q is bound to %q, which several fields of this form are called", name, bound)
	}
	// The bound field is read from the row the field is in, or from outside every
	// dynamic list around it — never from a row of a list the field is not in, which
	// has no one value for it.
	rows := rowsPrefix(form.keys[bound].Path)
	if len(rows) > len(path) || !slices.Equal(rows, path[:len(rows)]) {
		return Binding{}, fmt.Sprintf("%q is bound to %q, which repeats in a dynamic list this field is not in", name, bound)
	}
	return Binding{Name: name, Field: bound}, ""
}

// refuseCycles refuses decision fields whose options depend on each other: neither
// could be offered before the other was chosen.
func (f *Form) refuseCycles() error {
	const (
		unseen = iota
		open
		done
	)
	state := map[string]int{}
	var visit func(key string, trail []string) error
	visit = func(key string, trail []string) error {
		switch state[key] {
		case open:
			return fmt.Errorf("field %q: %s: inputs: the options of %s depend on each other", key, Member, strings.Join(append(trail, key), " → "))
		case done:
			return nil
		}
		state[key] = open
		if fl, ok := f.Field(key); ok {
			for _, b := range fl.Inputs {
				if b.Fixed() {
					continue
				}
				if _, isDecision := f.Field(b.Field); !isDecision {
					continue
				}
				if err := visit(b.Field, append(trail, key)); err != nil {
					return err
				}
			}
		}
		state[key] = done
		return nil
	}
	for _, fl := range f.Fields {
		if err := visit(fl.Key, nil); err != nil {
			return err
		}
	}
	return nil
}

func plainName(s string) bool {
	return utf8.RuneCountInString(s) <= maxDecisionID && strings.IndexFunc(s, unicode.IsControl) < 0
}

// Check is what a form save asks: may this schema be stored?
func Check(schema []byte) error {
	_, err := Parse(schema)
	return err
}

// occurrence is one value a field holds in submitted data, and the rows of the dynamic
// lists it sits in.
type occurrence struct {
	Value any
	Rows  []int
}

// occurrences is every value the submitted data holds at path: one for a field outside
// a dynamic list, one per row inside one. An absent or null value is no value.
func occurrences(v any, path []Step) []occurrence {
	var out []occurrence
	var walk func(v any, path []Step, rows []int)
	walk = func(v any, path []Step, rows []int) {
		if len(path) == 0 {
			if v != nil {
				out = append(out, occurrence{Value: v, Rows: slices.Clone(rows)})
			}
			return
		}
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		next, ok := m[path[0].Name]
		if !ok {
			return
		}
		if !path[0].Each {
			walk(next, path[1:], rows)
			return
		}
		list, ok := next.([]any)
		if !ok {
			return
		}
		for i, row := range list {
			walk(row, path[1:], append(rows, i))
		}
	}
	walk(v, path, nil)
	return out
}

// valueAt is the value at path in the given rows of the dynamic lists it passes, and
// whether the data holds the path at all — a null is held, an absent key is not.
func valueAt(v any, path []Step, rows []int) (any, bool) {
	for _, step := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[step.Name]; !ok {
			return nil, false
		}
		if step.Each {
			list, ok := v.([]any)
			if !ok || len(rows) == 0 || rows[0] >= len(list) {
				return nil, false
			}
			v, rows = list[rows[0]], rows[1:]
		}
	}
	return v, true
}

// valuesAt is every value the submitted data holds at path, without the rows.
func valuesAt(v any, path []Step) []any {
	var out []any
	for _, o := range occurrences(v, path) {
		out = append(out, o.Value)
	}
	return out
}
