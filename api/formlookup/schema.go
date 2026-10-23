package formlookup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/pblumer/atlas/connector/lookup"
)

// A directory field is a form-js select whose options come from a remote data source,
// named by its atlasDataSource block (ADR-0473). The select holds what form-js holds for
// any input — the submitted value, its label, required, its description and its errors —
// and the block says where its options come from, in place of the select's own `values`,
// `valuesKey` or `valuesExpression`. form-js keeps a member it does not know on export,
// so the form stays a form-js document; the vendored bundle renders the block as a
// search, and form-js rendered anywhere else shows a box for the chosen id.
//
// Directory fields were first saved as textfields carrying the same block. Those are
// read as they were: the form editor offers to convert one, nothing converts it behind
// its author's back.

// DataSourceMember is the component member that makes a select a directory field.
const DataSourceMember = "atlasDataSource"

// optionSources are the members by which form-js gives a select its options. A directory
// field takes its options from its data source, and from nothing else.
var optionSources = []string{"values", "valuesKey", "valuesExpression"}

// Version is the one version of the block this server reads. A form written for a newer
// one is refused at save with that reason, rather than half-understood.
const Version = 1

// The ways a submitted value is checked before its task completes.
const (
	// VerifyLookup looks the value up once more, under the field's filters.
	VerifyLookup = "lookup"
	// VerifyFormat checks only that the value has the shape of one.
	VerifyFormat = "format"
)

// Bounds on what a field may say. Product rules, not installation budgets.
const (
	maxDetails         = 4
	maxMinSearchLength = 10
	defaultMinSearch   = 2
	maxWorkerName      = 128
)

// Source is the atlasDataSource block as a form states it.
type Source struct {
	Version      int      `json:"version"`
	WorkerType   string   `json:"workerType"`
	Worker       string   `json:"worker"`
	Resource     string   `json:"resource"`
	SearchFields []string `json:"searchFields,omitempty"`
	Filters      []Filter `json:"filters,omitempty"`
	Option       Option   `json:"option,omitempty"`
	PageSize     int      `json:"pageSize,omitempty"`
	// MinSearchLength is how many characters a term needs before it is searched; 0
	// lists what the filters admit without one. Absent means 2.
	MinSearchLength *int `json:"minSearchLength,omitempty"`
	// ActiveOnly leaves disabled accounts out. Absent means true: a person picked for
	// a process is normally somebody who can still sign in, and a leaver process that
	// wants the disabled ones says so.
	ActiveOnly *bool  `json:"activeOnly,omitempty"`
	Verify     string `json:"verify,omitempty"`
}

// Filter narrows a search by equality. Field binds it to the value another field of the
// same form holds; Value fixes it, whatever a request says.
type Filter struct {
	Property string `json:"property"`
	Field    string `json:"field,omitempty"`
	Value    string `json:"value,omitempty"`
	// Required makes a bound filter a precondition: no search runs until its field has
	// a value. Without it, an empty field leaves the search unnarrowed.
	Required bool `json:"required,omitempty"`
}

// Option says what a result shows: the label a person reads, the line under it, and up
// to four details beside it. The value is always the source's own identifier.
type Option struct {
	Label       string   `json:"label,omitempty"`
	Description string   `json:"description,omitempty"`
	Details     []string `json:"details,omitempty"`
}

// Field is one directory field of a form, checked.
type Field struct {
	Key      string
	Label    string
	Required bool
	// Conditional is set when form-js may hide the field. The server cannot evaluate
	// form-js's condition, so it does not insist on a value a hidden field never sends.
	Conditional bool
	// Disabled fields are not submitted by form-js, and not checked.
	Disabled bool
	Source   Source
	Provider Provider
}

// PageSize is how many options one answer carries.
func (f Field) PageSize() int {
	if f.Source.PageSize == 0 {
		return lookup.DefaultPageSize
	}
	return f.Source.PageSize
}

// MinSearchLength is how many characters a term needs.
func (f Field) MinSearchLength() int {
	if f.Source.MinSearchLength == nil {
		return defaultMinSearch
	}
	return *f.Source.MinSearchLength
}

// ActiveOnly reports whether disabled records are left out.
func (f Field) ActiveOnly() bool { return f.Source.ActiveOnly == nil || *f.Source.ActiveOnly }

// Verify is how a submitted value is checked.
func (f Field) Verify() string {
	if f.Source.Verify == "" {
		return VerifyLookup
	}
	return f.Source.Verify
}

// SearchFields are the properties a term is a prefix of.
func (f Field) SearchFields() []string {
	if len(f.Source.SearchFields) == 0 {
		return slices.Clone(f.Provider.Searchable)
	}
	return f.Source.SearchFields
}

// LabelProperty is what an option's label reads.
func (f Field) LabelProperty() string {
	if f.Source.Option.Label == "" {
		return f.Provider.DefaultLabel
	}
	return f.Source.Option.Label
}

// DescriptionProperty is what the line under an option's label reads.
func (f Field) DescriptionProperty() string {
	if f.Source.Option.Description == "" {
		return f.Provider.DefaultDescription
	}
	return f.Source.Option.Description
}

// Properties are everything a lookup for this field reads, and nothing more: the
// value, what the option shows, and the fallback for a record that leaves either empty.
func (f Field) Properties() []string {
	var out []string
	add := func(p string) {
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	add(f.Provider.ValueProperty)
	add(f.LabelProperty())
	add(f.DescriptionProperty())
	add(f.Provider.Fallback)
	for _, d := range f.Source.Option.Details {
		add(d)
	}
	return out
}

// Form is what a schema says about its directory fields and about the fields they
// may be bound to.
type Form struct {
	Fields []Field
	keys   map[string]keyed
	// labels are the fields' labels by key, for naming a field in a refusal.
	labels map[string]string
}

type keyed struct {
	static []string
	hasSet bool
}

// Field finds the directory field with this key.
func (f *Form) Field(key string) (Field, bool) {
	for _, fl := range f.Fields {
		if fl.Key == key {
			return fl, true
		}
	}
	return Field{}, false
}

// StaticOptions are the option values a field fixes in the form itself — a select's
// `values` — and false when it has none (no such field, or options from a variable or
// an expression, which the server cannot know).
func (f *Form) StaticOptions(key string) ([]string, bool) {
	k, ok := f.keys[key]
	if !ok || !k.hasSet {
		return nil, false
	}
	return k.static, true
}

// Parse reads a form-js schema's directory fields and checks every one of them against
// what this server can honour. A schema without any is every form that existed before
// them and parses to none; a malformed atlasDataSource is refused with the field and
// the reason.
func Parse(schema []byte) (*Form, error) {
	var doc any
	dec := json.NewDecoder(bytes.NewReader(schema))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("the form is not JSON: %w", err)
	}
	form := &Form{keys: map[string]keyed{}, labels: map[string]string{}}
	type pending struct {
		comp map[string]any
		path bool
	}
	var found []pending
	var walk func(v any, inPath bool)
	walk = func(v any, inPath bool) {
		switch t := v.(type) {
		case map[string]any:
			if k, ok := t["key"].(string); ok && strings.TrimSpace(k) != "" {
				k = strings.TrimSpace(k)
				if l, ok := t["label"].(string); ok && strings.TrimSpace(l) != "" {
					form.labels[k] = strings.TrimSpace(l)
				}
				entry := form.keys[k]
				if vals, ok := t["values"].([]any); ok {
					entry.hasSet = true
					for _, o := range vals {
						if m, ok := o.(map[string]any); ok {
							if s, ok := m["value"]; ok && s != nil {
								entry.static = append(entry.static, fmt.Sprint(s))
							}
						}
					}
				}
				if _, dup := form.keys[k]; dup {
					entry.static, entry.hasSet = nil, false
				}
				form.keys[k] = entry
			}
			if _, ok := t[DataSourceMember]; ok {
				found = append(found, pending{comp: t, path: inPath})
			}
			_, repeats := t["path"]
			walk(t["components"], inPath || repeats)
		case []any:
			for _, e := range t {
				walk(e, inPath)
			}
		}
	}
	walk(doc, false)

	counts := map[string]int{}
	var count func(v any)
	count = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if k, ok := t["key"].(string); ok && strings.TrimSpace(k) != "" {
				counts[strings.TrimSpace(k)]++
			}
			count(t["components"])
		case []any:
			for _, e := range t {
				count(e)
			}
		}
	}
	count(doc)

	for _, p := range found {
		fl, err := field(p.comp, p.path, form, counts)
		if err != nil {
			return nil, err
		}
		form.Fields = append(form.Fields, fl)
	}
	return form, nil
}

// field checks one component carrying an atlasDataSource.
func field(comp map[string]any, inPath bool, form *Form, counts map[string]int) (Field, error) {
	key, _ := comp["key"].(string)
	key = strings.TrimSpace(key)
	name := key
	if name == "" {
		name = "(no key)"
	}
	fail := func(format string, a ...any) (Field, error) {
		return Field{}, fmt.Errorf("field %q: %s: %s", name, DataSourceMember, fmt.Sprintf(format, a...))
	}
	if key == "" {
		return fail("a directory field needs a key: it is the variable the chosen value is submitted as")
	}
	switch typ, _ := comp["type"].(string); typ {
	case "select":
		for _, m := range optionSources {
			if _, ok := comp[m]; ok {
				return fail("a select takes its options from one source: this one names %s as well as the remote data source", m)
			}
		}
	case "textfield":
		// The shape directory fields were saved in first; still read.
	default:
		return fail("a directory field is a select, which holds the submitted value; this one is a %q", typ)
	}
	if inPath {
		return fail("a directory field cannot repeat or sit inside a group with a path: it submits one value under its own key")
	}
	if counts[key] > 1 {
		return fail("another field of this form has the key %q too, so which one is submitted is undefined", key)
	}
	raw, err := json.Marshal(comp[DataSourceMember])
	if err != nil {
		return fail("%v", err)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return fail("%s must be an object", DataSourceMember)
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
	prov, ok := ProviderFor(src.WorkerType, src.Resource)
	if !ok {
		return fail("there is no remote data source %q/%q on this server", src.WorkerType, src.Resource)
	}
	src.Worker = strings.TrimSpace(src.Worker)
	switch {
	case src.Worker == "":
		return fail("worker is missing: name the Worker whose credential answers the search")
	case strings.HasPrefix(src.Worker, "="):
		return fail("worker must be a plain name: it is resolved on the server when a person searches, never evaluated from the form")
	case utf8.RuneCountInString(src.Worker) > maxWorkerName || !lookup.CleanText(src.Worker):
		return fail("worker %q is not a Worker name", src.Worker)
	}
	for _, p := range src.SearchFields {
		if !slices.Contains(prov.Searchable, p) {
			return fail("searchFields: %q cannot be searched on %s (allowed: %s)", p, prov.Label, strings.Join(prov.Searchable, ", "))
		}
	}
	seen := map[string]bool{}
	for _, f := range src.Filters {
		if !slices.Contains(prov.Filterable, f.Property) {
			return fail("filters: %q cannot be filtered on %s (allowed: %s)", f.Property, prov.Label, strings.Join(prov.Filterable, ", "))
		}
		if seen[f.Property] {
			return fail("filters: %q is filtered twice", f.Property)
		}
		seen[f.Property] = true
		if (f.Field == "") == (f.Value == "") {
			return fail("filters: %q needs exactly one of field (bound to another field) and value (fixed)", f.Property)
		}
		if f.Field != "" {
			if f.Field == key {
				return fail("filters: %q is bound to the field itself", f.Property)
			}
			if _, ok := form.keys[f.Field]; !ok {
				return fail("filters: %q is bound to %q, which is not a field of this form", f.Property, f.Field)
			}
		}
		if utf8.RuneCountInString(f.Value) > lookup.MaxValueLength || !lookup.CleanText(f.Value) {
			return fail("filters: the value of %q is not one a filter can carry", f.Property)
		}
		if f.Value != "" && f.Required {
			return fail("filters: %q is fixed, so it cannot be required of the person", f.Property)
		}
	}
	for _, p := range []string{src.Option.Label, src.Option.Description} {
		if p != "" && !slices.Contains(prov.Readable, p) {
			return fail("option: %q is not readable on %s (allowed: %s)", p, prov.Label, strings.Join(prov.Readable, ", "))
		}
	}
	if len(src.Option.Details) > maxDetails {
		return fail("option: details names %d properties; an option shows at most %d", len(src.Option.Details), maxDetails)
	}
	for _, p := range src.Option.Details {
		if !slices.Contains(prov.Readable, p) {
			return fail("option: details: %q is not readable on %s (allowed: %s)", p, prov.Label, strings.Join(prov.Readable, ", "))
		}
	}
	if src.PageSize < 0 || src.PageSize > lookup.MaxPageSize {
		return fail("pageSize %d is outside 1..%d", src.PageSize, lookup.MaxPageSize)
	}
	if m := src.MinSearchLength; m != nil && (*m < 0 || *m > maxMinSearchLength) {
		return fail("minSearchLength %d is outside 0..%d", *m, maxMinSearchLength)
	}
	switch src.Verify {
	case "", VerifyLookup, VerifyFormat:
	default:
		return fail("verify %q is neither %q nor %q", src.Verify, VerifyLookup, VerifyFormat)
	}
	fl := Field{Key: key, Source: src, Provider: prov}
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
