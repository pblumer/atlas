package dmn

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tdmn "github.com/pblumer/temis/dmn"
)

// ValidationResult reports the outcome of resolving a DMN reference and checking
// it against temis (ADR-0034 Phase 2). It is a pure preflight result: no engine
// or registry state is mutated by producing it.
type ValidationResult struct {
	Resolved  bool     // the modelRef resolved to an actual model
	Valid     bool     // the resolved model compiled without errors in temis
	ModelName string   // the DMN <definitions name>, when resolved
	Decisions []string // decision names the model exposes, when valid
	Message   string   // human-readable reason when unresolved or invalid
}

// Validator resolves DMN references and validates them against temis. It owns a
// temis engine used only to compile (nothing is deployed) and a Resolver for
// fetching model XML. Safe for concurrent use once constructed.
type Validator struct {
	resolver Resolver
	engine   *tdmn.Engine
}

// NewValidator builds a Validator over a resolver and a fresh temis engine.
func NewValidator(resolver Resolver) *Validator {
	return &Validator{resolver: resolver, engine: tdmn.New()}
}

// Validate resolves modelRef and compiles it with temis, reporting whether it
// resolved and whether it is a valid DMN model — the check a deploy runs before
// trusting a reference. It returns a non-nil error ONLY for an infrastructure
// failure (e.g. the model source is unreadable); an unresolved handle or an
// invalid model is a normal, reportable result, not an error, so a caller can
// surface it to the user rather than as a 500.
func (v *Validator) Validate(ctx context.Context, modelRef string) (ValidationResult, error) {
	xml, err := v.resolver.Resolve(ctx, modelRef)
	if errors.Is(err, ErrNotFound) {
		return ValidationResult{Message: "no temis model matches this reference"}, nil
	}
	if err != nil {
		return ValidationResult{}, err
	}
	defs, diags, err := v.engine.Compile(ctx, xml)
	if err != nil {
		return ValidationResult{Resolved: true, Message: err.Error()}, nil
	}
	if diags.HasErrors() {
		return ValidationResult{Resolved: true, Message: formatDiagnostics(diags)}, nil
	}
	idx := defs.Index()
	return ValidationResult{
		Resolved:  true,
		Valid:     true,
		ModelName: defs.ModelName(),
		Decisions: idx.Decisions,
	}, nil
}

// DecisionField is one input or output of a decision, for authoring tooling: a
// name and its declared FEEL type (empty when the model declares none).
type DecisionField struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// DecisionInfo is a decision's self-description for the Modeler's decision picker
// (ADR-0050): the id to reference it by, its display name, the input data it
// consumes (so a business rule task's input mappings can be auto-filled), and its
// output (the process variable a result naturally lands in).
type DecisionInfo struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Inputs []DecisionField `json:"inputs"`
	Output DecisionField   `json:"output"`
}

// Describe resolves modelRef, compiles it, and returns its model name and the
// self-description of each evaluable decision (inputs + output). Like Validate it
// returns a non-nil error only for an infrastructure failure; an unresolved handle
// or an invalid model yields an empty result (a best-effort catalog entry), not an
// error, so one broken reference does not blank the whole picker.
func (v *Validator) Describe(ctx context.Context, modelRef string) (string, []DecisionInfo, error) {
	xml, err := v.resolver.Resolve(ctx, modelRef)
	if errors.Is(err, ErrNotFound) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	defs, diags, err := v.engine.Compile(ctx, xml)
	if err != nil || diags.HasErrors() {
		return "", nil, nil
	}
	return defs.ModelName(), describeDecisions(defs), nil
}

// describeDecisions turns a compiled model's decision requirements graph into a
// per-decision I/O description. A decision's inputs are the input data it consumes
// transitively (through any sub-decisions it depends on), in the model's node
// order for a stable picker; its output is its result variable and declared type.
// Only named decisions with executable logic are listed — the same set Index
// exposes, so the picker and the deploy gate agree.
func describeDecisions(defs *tdmn.Definitions) []DecisionInfo {
	g := defs.Graph()
	// requires: a requiring node id → the ids it directly requires. A DRG
	// information-requirement edge points from the required (upstream) element to
	// the one that requires it.
	requires := map[string][]string{}
	for _, e := range g.Edges {
		if e.Type == "informationRequirement" {
			requires[e.Target] = append(requires[e.Target], e.Source)
		}
	}
	var out []DecisionInfo
	for _, n := range g.Nodes {
		if n.Type != "decision" || n.Name == "" || !n.HasLogic {
			continue
		}
		feeding := reachableUpstream(n.ID, requires)
		var inputs []DecisionField
		for _, m := range g.Nodes { // model order → stable UI
			if m.Type == "inputData" && feeding[m.ID] {
				inputs = append(inputs, DecisionField{Name: m.Name, Type: m.DataType})
			}
		}
		outName := n.VarName
		if outName == "" {
			outName = n.Name
		}
		out = append(out, DecisionInfo{
			ID:     n.Name, // temis Decision() resolves by name; matches Index().Decisions
			Name:   n.Name,
			Inputs: inputs,
			Output: DecisionField{Name: outName, Type: n.DataType},
		})
	}
	return out
}

// reachableUpstream returns the set of node ids reachable from start by following
// requirement edges upstream, so a decision's inputs include the input data
// feeding the sub-decisions it depends on.
func reachableUpstream(start string, requires map[string][]string) map[string]bool {
	seen := map[string]bool{}
	stack := append([]string(nil), requires[start]...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		stack = append(stack, requires[id]...)
	}
	return seen
}

// ValidateXML compiles a DMN model already in hand (not resolved by a handle) and
// reports whether it is valid, with its name and decisions — the check the upload
// path runs before storing a model. Resolved is always true (the bytes are
// present), and no infrastructure failure is possible, so it returns no error.
func (v *Validator) ValidateXML(ctx context.Context, xml []byte) ValidationResult {
	defs, diags, err := v.engine.Compile(ctx, xml)
	if err != nil {
		return ValidationResult{Resolved: true, Message: err.Error()}
	}
	if diags.HasErrors() {
		return ValidationResult{Resolved: true, Message: formatDiagnostics(diags)}
	}
	idx := defs.Index()
	return ValidationResult{Resolved: true, Valid: true, ModelName: defs.ModelName(), Decisions: idx.Decisions}
}

// formatDiagnostics renders the error-severity diagnostics into one line for the
// UI. Diagnostic messages are human-readable but not a stable API (temis warns
// against parsing them), so this is display-only.
func formatDiagnostics(diags tdmn.Diagnostics) string {
	var b strings.Builder
	for _, d := range diags {
		if d.Severity != tdmn.SevError {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		if d.DecisionID != "" {
			fmt.Fprintf(&b, "%s: ", d.DecisionID)
		}
		b.WriteString(d.Message)
	}
	if b.Len() == 0 {
		return "model has errors"
	}
	return b.String()
}
