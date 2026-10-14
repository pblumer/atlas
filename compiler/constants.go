package compiler

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/pblumer/atlas/expr"
)

// Process constants (ADR-0454).
//
// A constant is a value every instance of a process starts with and nobody is asked for:
// declared on the process as <atlas:constant name type value/> inside <atlas:constants>,
// parsed here once into the canonical form any other variable takes, and written by the
// engine as an ordinary process-scope variable when it creates an instance — on every
// start path, before the start variables. Afterwards nothing in the run can replace it;
// only an operator's audited correction does. Its name is written in capitals
// (ValidConstantName). A constant whose value the installation holds is not a variable of
// its own: it is a member of a namespace — application.NAME for the application's
// constants, server.NAME for the installation's (ADR-draft-constant-namespaces) — and the
// same namespaces carry a few facts Atlas itself knows, in lower case: application.name,
// server.environment.
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
	// attribute), the application the process is filed under on the installation where an
	// instance starts ("application", ADR-0453), or that installation itself ("server",
	// ADR-draft-constant-namespaces). For the last two Value is an optional fallback.
	Source string `xml:"source,attr"`
}

// Constant sources (ADR-0453, ADR-draft-constant-namespaces).
const (
	ConstantSourceModel       = "model"
	ConstantSourceApplication = "application"
	ConstantSourceServer      = "server"
)

// ApplicationNamespace and ServerNamespace are the variables an instance holds the
// constants in whose values the installation holds (ADR-draft-constant-namespaces): one
// object each, so a model reads application.TENANT_ID or server.ENVIRONMENT_URL in FEEL
// and $application.TENANT_ID in PowerShell, and the source of every value is in the name
// it is read by. A namespace exists only in an instance of a process that declares one of
// its members, and is then a constant itself — nothing in the run may replace it.
const (
	ApplicationNamespace = "application"
	ServerNamespace      = "server"
)

// namespaceInfo are the members of each namespace Atlas fills itself rather than an
// operator: facts about the application and the installation an instance starts on. They
// are written in lower case, which no constant can be, so a fact and a constant never share
// a name. A model declares the ones it reads like a constant (<atlas:constant
// name="environment" source="server"/>), so a namespace still carries only what its model
// asks for.
var namespaceInfo = map[string][]string{
	ApplicationNamespace: {"id", "key", "name"},
	ServerNamespace:      {"id", "name", "environment"},
}

// InfoFields lists the facts a namespace carries besides its constants, in a stable order.
func InfoFields(namespace string) []string { return namespaceInfo[namespace] }

func isInfoField(namespace, name string) bool {
	for _, f := range namespaceInfo[namespace] {
		if f == name {
			return true
		}
	}
	return false
}

// reservedConstantNames are the names a constant may not take, because they are the
// namespaces constants are read under. Compared after upper-casing, the only form
// a constant name has: PowerShell does not tell $APPLICATION from $application, so a
// constant named APPLICATION would silently shadow the namespace in every script.
var reservedConstantNames = map[string]bool{"APPLICATION": true, "SERVER": true}

// ValidConstantName says why name cannot name a constant, or nil. A constant's name is
// written in capitals — letters A–Z, digits and underscores, starting with a letter, such
// as TENANT_ID (ADR-0454, amended) — so a reader tells a constant from a variable at a
// glance in every expression and script, and the API applies the same rule to an
// application's constants so the two names a binding joins can never differ in case.
func ValidConstantName(name string) error {
	if name == "" {
		return fmt.Errorf("a constant has no name")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '_'):
		default:
			return fmt.Errorf("constant %q: a constant's name is written in capitals — letters A–Z, digits and underscores, starting with a letter, such as TENANT_ID", name)
		}
	}
	if reservedConstantNames[name] {
		return fmt.Errorf("constant %q: the name is reserved for a namespace constants are read under (application.NAME, server.NAME)", name)
	}
	return nil
}

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

	// Namespace is where the value comes from and is read: "" for a process constant,
	// which is a root variable of its own, ApplicationNamespace or ServerNamespace for one
	// the installation holds, which is a member of that namespace (Bound).
	Namespace string
	// Info marks a member Atlas fills itself — application.name, server.environment —
	// rather than one an operator holds.
	Info        bool
	HasFallback bool // a bound constant declares a value to use when the installation holds none
}

// Bound reports whether the constant's value comes from the installation rather than the
// model.
func (k *Constant) Bound() bool { return k.Namespace != "" }

// constantsOf reads a process's constant declarations, refusing any the engine could not
// seed as written: a nameless one, a name declared twice, a type outside the four a start
// variable takes, or a value that is not a literal of its type.
//
// A name not written in capitals is refused through the deploy gate g: a deploy refuses
// it, while a reload of a definition deployed before the rule keeps the constant under the
// name it has and reports it — a spelling is no reason for a server not to start.
func constantsOf(procID string, decl []xmlConstant, g *feelGate) ([]Constant, error) {
	var out []Constant
	seen := map[string]bool{}
	for _, d := range decl {
		name := strings.TrimSpace(d.Name)
		k := Constant{Name: name, Value: d.Value}
		switch src := strings.TrimSpace(d.Source); src {
		case "", ConstantSourceModel:
		case ConstantSourceApplication:
			k.Namespace = ApplicationNamespace
		case ConstantSourceServer:
			k.Namespace = ServerNamespace
		default:
			return nil, fmt.Errorf("compiler: process %q: constant %q: source %q is not one of model, application, server", procID, name, src)
		}
		k.Info = k.Bound() && isInfoField(k.Namespace, name)
		if !k.Info && name == "" {
			return nil, fmt.Errorf("compiler: process %q: a constant has no name", procID)
		}
		if !k.Info {
			if err := ValidConstantName(name); err != nil {
				if !g.tolerate {
					return nil, fmt.Errorf("compiler: process %q: %w (ADR-0454)", procID, err)
				}
				g.found = append(g.found, Problem{Severity: SeverityError, Rule: RuleConstantName,
					Message: fmt.Sprintf("process %q: %s", procID, err)})
			}
		}
		key := k.Namespace + "." + name
		if seen[key] {
			return nil, fmt.Errorf("compiler: process %q: constant %q is declared twice", procID, name)
		}
		seen[key] = true
		k.Type = strings.TrimSpace(d.Type)
		if k.Type == "" {
			k.Type = "string"
		}
		if k.Info && k.Type != "string" {
			return nil, fmt.Errorf("compiler: process %q: %s.%s is text Atlas fills in; declare it with type string", procID, k.Namespace, name)
		}
		// A bound constant's value is optional — it is the fallback — so an empty one
		// means "none". A model constant's empty value is the empty string it says.
		if k.Bound() && strings.TrimSpace(d.Value) == "" {
			out = append(out, k)
			continue
		}
		var err error
		k.Kind, k.Bool, k.Text, err = ParseConstantValue(k.Type, d.Value)
		if err != nil {
			return nil, fmt.Errorf("compiler: process %q: constant %q: %w", procID, name, err)
		}
		k.HasFallback = k.Bound()
		out = append(out, k)
	}
	return out, nil
}

// ParseConstantValue turns a value declared with one of the four constant types into the
// canonical (kind, bool, text) form a variable is stored in, or says why it is not a value
// of that type. The API parses an application's values with it when they are saved
// (ADR-0453), so they are held in exactly the form a
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

// IsConstant reports whether name is a variable the process's constants occupy in an
// instance root: a process constant's own name, or a namespace when the process declares
// one of its members. A bound constant's own name is not one — it is a member of the
// namespace, and a variable of that name is the process's to write.
func (p *CompiledProcess) IsConstant(name string) bool {
	_, ok := p.constantSet[name]
	return ok
}

// Binds reports whether the process declares a member of namespace, and so whether its
// instances carry that namespace. A scan of a handful of declarations — no allocation (I1).
func (p *CompiledProcess) Binds(namespace string) bool {
	for i := range p.constants {
		if p.constants[i].Namespace == namespace {
			return true
		}
	}
	return false
}

// constantSetOf indexes the root variables the constants occupy: each process constant by
// name, and each namespace once when any of its members is declared.
func constantSetOf(cs []Constant) map[string]struct{} {
	if len(cs) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(cs))
	for _, c := range cs {
		if c.Bound() {
			set[c.Namespace] = struct{}{}
			continue
		}
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
		if (sv.Name == ApplicationNamespace || sv.Name == ServerNamespace) && p.IsConstant(sv.Name) {
			return fmt.Errorf("compiler: %q is declared as a start variable, but it is the namespace this process's %s constants are read under; name the start variable differently (ADR-draft-constant-namespaces)", sv.Name, sv.Name)
		}
		if p.IsConstant(sv.Name) {
			return fmt.Errorf("compiler: %q is declared both as a start variable and as a constant — a start variable is asked for when an instance starts, a constant is fixed by the model; declare it as one or the other (ADR-0454)", sv.Name)
		}
	}
	for _, name := range p.personalVars {
		if p.IsConstant(name) {
			return fmt.Errorf("compiler: %q is declared both personal and as a constant — a constant's value is written in the model, in the clear, so it cannot be personal data (ADR-0314, ADR-0454)", name)
		}
	}
	if p.dataSubjectVar != "" && p.IsConstant(p.dataSubjectVar) {
		return fmt.Errorf("compiler: %q is the data subject and a constant — the data subject names the person an instance is about, and a constant is the same for every instance (ADR-0314, ADR-0454)", p.dataSubjectVar)
	}
	refuse := func(kind, name string) error {
		if name == "" || !p.IsConstant(name) {
			return nil
		}
		if name == ApplicationNamespace || name == ServerNamespace {
			return fmt.Errorf("compiler: %s writes %q, the namespace this process's %s constants are read under — it is set when an instance starts and nothing in the run may replace it; write the value under another name (ADR-draft-constant-namespaces)", kind, name, name)
		}
		return fmt.Errorf("compiler: %s writes %q, which the process declares as a constant — a constant is set when an instance starts and nothing in the run may replace it; write the value under another name (ADR-0454)", kind, name)
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
