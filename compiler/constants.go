package compiler

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/pblumer/atlas/expr"
)

// Process constants (ADR-draft-process-constants).
//
// A constant is a value every instance of a process starts with and nobody is asked for:
// declared on the process as <atlas:constant name type value/> inside <atlas:constants>,
// parsed here once into the canonical form any other variable takes, and written by the
// engine as an ordinary process-scope variable when it creates an instance — on every
// start path, before the start variables. Afterwards nothing in the run can replace it;
// only an operator's audited correction does.
//
// The deploy is the place a constant is checked, because a constant is new: unlike the
// start-variable declaration, which had always been accepted as written, no stored model
// carries one, so refusing a malformed constant can never turn an accepted model into one
// that no longer loads.

// xmlConstant is one <atlas:constant name type value source/>.
type xmlConstant struct {
	Name  string `xml:"name,attr"`
	Type  string `xml:"type,attr"`
	Value string `xml:"value,attr"`
	// Source is where the value comes from: the model ("" or "model", the value
	// attribute) or the application the process is filed under on the installation
	// where an instance starts ("application", ADR-draft-application-values-per-installation),
	// in which case Value is an optional fallback.
	Source string `xml:"source,attr"`
}

// Constant sources (ADR-draft-application-values-per-installation).
const (
	ConstantSourceModel       = "model"
	ConstantSourceApplication = "application"
)

// Constant is one process constant as the engine seeds it: its name, the type and value
// the model declares, and that value in the canonical (kind, bool, text) form a variable
// is stored as — computed once at deploy so the engine writes it without parsing (I5).
//
// A constant bound to its application (FromApplication) takes the application's value
// of the same name on the installation where the instance starts; Kind, Bool and Text
// are then its fallback, meaningful only when HasFallback is set.
type Constant struct {
	Name  string
	Type  string // declared type: string, number, boolean or json
	Value string // the declared value (a bound constant's fallback), as authored
	Kind  expr.ValueKind
	Bool  bool
	Text  string

	FromApplication bool // the value is the application's, per installation
	HasFallback     bool // a bound constant declares a value to use when the application holds none
}

// constantsOf reads a process's constant declarations, refusing any the engine could not
// seed as written: a nameless one, a name declared twice, a type outside the four a start
// variable takes, or a value that is not a literal of its type.
func constantsOf(procID string, decl []xmlConstant) ([]Constant, error) {
	var out []Constant
	seen := map[string]bool{}
	for _, d := range decl {
		name := strings.TrimSpace(d.Name)
		if name == "" {
			return nil, fmt.Errorf("compiler: process %q: a constant has no name", procID)
		}
		if seen[name] {
			return nil, fmt.Errorf("compiler: process %q: constant %q is declared twice", procID, name)
		}
		seen[name] = true
		typ := strings.TrimSpace(d.Type)
		if typ == "" {
			typ = "string"
		}
		k := Constant{Name: name, Type: typ, Value: d.Value}
		switch src := strings.TrimSpace(d.Source); src {
		case "", ConstantSourceModel:
		case ConstantSourceApplication:
			k.FromApplication = true
		default:
			return nil, fmt.Errorf("compiler: process %q: constant %q: source %q is not one of model, application", procID, name, src)
		}
		// A bound constant's value is optional — it is the fallback — so an empty one
		// means "none". A model constant's empty value is the empty string it says.
		if k.FromApplication && strings.TrimSpace(d.Value) == "" {
			out = append(out, k)
			continue
		}
		var err error
		k.Kind, k.Bool, k.Text, err = ParseConstantValue(typ, d.Value)
		if err != nil {
			return nil, fmt.Errorf("compiler: process %q: constant %q: %w", procID, name, err)
		}
		k.HasFallback = k.FromApplication
		out = append(out, k)
	}
	return out, nil
}

// ParseConstantValue turns a value declared with one of the four constant types into the
// canonical (kind, bool, text) form a variable is stored in, or says why it is not a value
// of that type. The API parses an application's values with it when they are saved
// (ADR-draft-application-values-per-installation), so they are held in exactly the form a
// model constant is and the engine never parses either.
func ParseConstantValue(typ, raw string) (expr.ValueKind, bool, string, error) {
	v, err := constantValue(typ, raw)
	if err != nil {
		return 0, false, "", err
	}
	kind, b, text := expr.Classify(v)
	return kind, b, text, nil
}

// constantValue turns a declared value into the FEEL value its type says it is. A string
// is taken as written; the others must be a literal of their type and nothing else, so a
// typo fails the deploy rather than seeding null into every instance.
func constantValue(typ, raw string) (expr.Value, error) {
	switch typ {
	case "string":
		return expr.FromJSON(raw), nil
	case "boolean":
		switch strings.TrimSpace(raw) {
		case "true":
			return expr.FromJSON(true), nil
		case "false":
			return expr.FromJSON(false), nil
		}
		return nil, fmt.Errorf("value %q is not a boolean (true or false)", raw)
	case "number":
		v, err := decodeOneJSON(raw)
		if _, isNum := v.(json.Number); err != nil || !isNum {
			return nil, fmt.Errorf("value %q is not a number", raw)
		}
		return expr.FromJSON(v), nil
	case "json":
		v, err := decodeOneJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("value is not valid JSON: %w", err)
		}
		return expr.FromJSON(v), nil
	}
	return nil, fmt.Errorf("type %q is not one of string, number, boolean, json", typ)
}

// decodeOneJSON decodes exactly one JSON value, keeping numbers exact, and refuses
// anything after it — "1 2" is not a number, and "{} x" is not JSON.
func decodeOneJSON(raw string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after the value")
	}
	return v, nil
}

// SetConstants records the process's constants. The engine seeds them into every
// instance it creates.
func (b *Builder) SetConstants(cs []Constant) { b.constants = cs }

// Constants returns the process's constants in the order they were declared, nil when it
// declares none. The slice is shared and must not be modified.
func (p *CompiledProcess) Constants() []Constant { return p.constants }

// HasConstants reports whether the process declares any. The engine asks it first on
// every variable write into an instance root, so a process that declares none pays one
// length check rather than a lookup — the same shape as HasSearchableVariables.
func (p *CompiledProcess) HasConstants() bool { return len(p.constantSet) > 0 }

// IsConstant reports whether name is one of the process's constants.
func (p *CompiledProcess) IsConstant(name string) bool {
	_, ok := p.constantSet[name]
	return ok
}

// constantSetOf indexes the constants by name.
func constantSetOf(cs []Constant) map[string]struct{} {
	if len(cs) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(cs))
	for _, c := range cs {
		set[c.Name] = struct{}{}
	}
	return set
}

// refuseConstantConflicts refuses a process whose constants contradict the rest of its
// declarations, or that writes one of them itself.
//
// A name cannot be a constant and a start variable: one is fixed, the other is asked for.
// It cannot be a constant and personal data (ADR-0314): a value stated in the model is in
// the clear in every copy of the model, which is the opposite of what that declaration
// promises — and the data subject, being the same for every instance, would name nobody.
//
// And the model may not write a constant: the deploy is where such a mistake is cheapest
// to see, where at run time the engine would only drop the write (see the engine's
// keepsConstant). A mapping whose source is the constant itself hands the value on
// unchanged — the way a call activity passes a constant into its child — and is allowed.
// As with the personal-data refusal, a write target is an interned index no reflection can
// tell from any other int32, so this list has no completeness guard beyond reading it; the
// engine's drop is the backstop for anything it misses.
func (p *CompiledProcess) refuseConstantConflicts() error {
	if len(p.constantSet) == 0 {
		return nil
	}
	for _, sv := range p.startVars {
		if p.IsConstant(sv.Name) {
			return fmt.Errorf("compiler: %q is declared both as a start variable and as a constant — a start variable is asked for when an instance starts, a constant is fixed by the model; declare it as one or the other (ADR-draft-process-constants)", sv.Name)
		}
	}
	for _, name := range p.personalVars {
		if p.IsConstant(name) {
			return fmt.Errorf("compiler: %q is declared both personal and as a constant — a constant's value is written in the model, in the clear, so it cannot be personal data (ADR-0314, ADR-draft-process-constants)", name)
		}
	}
	if p.dataSubjectVar != "" && p.IsConstant(p.dataSubjectVar) {
		return fmt.Errorf("compiler: %q is the data subject and a constant — the data subject names the person an instance is about, and a constant is the same for every instance (ADR-0314, ADR-draft-process-constants)", p.dataSubjectVar)
	}
	refuse := func(kind, name string) error {
		if name == "" || !p.IsConstant(name) {
			return nil
		}
		return fmt.Errorf("compiler: %s writes %q, which the process declares as a constant — a constant is set when an instance starts and nothing in the run may replace it; write the value under another name (ADR-draft-process-constants)", kind, name)
	}
	for i := range p.ioInputs {
		m := &p.ioInputs[i]
		target := p.Intern(m.Target)
		if passesThrough(m.Source, target) {
			continue
		}
		if err := refuse("an input mapping", target); err != nil {
			return err
		}
	}
	for i := range p.ioOutputs {
		m := &p.ioOutputs[i]
		target := p.Intern(m.Target)
		if passesThrough(m.Source, target) {
			continue
		}
		if err := refuse("an output mapping", target); err != nil {
			return err
		}
	}
	for i := range p.scriptTasks {
		if err := refuse("a script task's result variable", p.scriptTasks[i].ResultVar); err != nil {
			return err
		}
	}
	for i := range p.scriptJobTasks {
		if err := refuse("a script task's result variable", p.Intern(p.scriptJobTasks[i].ResultVar)); err != nil {
			return err
		}
	}
	for i := range p.businessRuleTasks {
		if err := refuse("a business rule task's result variable", p.Intern(p.businessRuleTasks[i].ResultVar)); err != nil {
			return err
		}
	}
	for i := range p.connectorTasks {
		if err := refuse("a service task's result variable", p.Intern(p.connectorTasks[i].ResultVar)); err != nil {
			return err
		}
		if err := refuse("a shop task's result variable", p.connectorTasks[i].ShopResultVar); err != nil {
			return err
		}
	}
	for i := range p.mockupTasks {
		if err := refuse("a mockup task's result variable", p.mockupTasks[i].ResultVar); err != nil {
			return err
		}
	}
	for i := range p.multiInstances {
		if err := refuse("a multi-instance output collection", p.Intern(p.multiInstances[i].OutputCollection)); err != nil {
			return err
		}
		if err := refuse("a multi-instance element variable", p.Intern(p.multiInstances[i].InputElement)); err != nil {
			return err
		}
	}
	for i := range p.adHocs {
		if err := refuse("an ad-hoc subprocess's result collection", p.Intern(p.adHocs[i].ResultCollection)); err != nil {
			return err
		}
	}
	return nil
}
