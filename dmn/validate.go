package dmn

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tdmn "github.com/pblumer/temis/dmn"
)

// ValidationResult reports the outcome of resolving a DMN reference and checking
// it against temis (ADR-0024 Phase 2). It is a pure preflight result: no engine
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
