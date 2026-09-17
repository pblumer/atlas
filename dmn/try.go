package dmn

import (
	"context"
	"encoding/json"
)

// Trying a decision before anything is deployed
// (ADR-0326).
//
// A decision table is a program, and the first question its author asks is whether
// it does what they meant. Answering that used to mean saving the model, deploying
// it, deploying a process that calls it, starting an instance and reading the
// result back — five steps, three of them about processes, to answer a question
// about one table.
//
// Try answers it directly: compile these bytes, run this decision over these
// inputs, hand back what came out and the temis trace saying which rules fired.
// It is a pure function of the request. Nothing is keyed, stored, registered or
// cleaned up, and the DMN registry — what deployed processes are bound to — is
// neither read nor written, so trying a decision cannot move anything that is
// running.

// Trial is the answer to "what does this model do with these inputs": what it can
// run and what that wants, and — when a decision was named — what it produced.
//
// OK is false for a model that does not compile, a decision id the model does not
// provide, and an evaluation that errored, with Message saying which. Those are the
// normal output of authoring rather than a caller's mistake, so they come back as a
// result to render, not as an HTTP error (the grammar POST /api/v1/feel/evaluate
// already uses).
type Trial struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`

	// What the model offers, whenever it compiled: the panel's form is built from
	// this, so the fields are temis's own view of the model rather than something a
	// client re-derived from the XML.
	ModelName string         `json:"modelName,omitempty"`
	Decisions []DecisionInfo `json:"decisions"`

	// What one decision produced. Present only when a decision was named and ran.
	DecisionID string          `json:"decisionId,omitempty"`
	Outputs    map[string]any  `json:"outputs,omitempty"`
	Trace      json.RawMessage `json:"trace,omitempty"`
}

// Try compiles src with the validator's own temis engine — the one that exists to
// compile without deploying — and, when decisionID names a decision the model
// provides, evaluates it over inputs and returns its outputs with the trace.
//
// With decisionID empty it evaluates nothing and only describes the model. That is
// one question asked twice over, not two modes: the panel needs to know what it may
// run and what that wants before it can ask for a run, and both answers come out of
// the same compile.
//
// inputs carries decoded JSON, the same value shapes a business rule task's static
// inputs arrive as, so a decision tried here sees what it would see at runtime.
func (v *Validator) Try(ctx context.Context, src []byte, decisionID string, inputs map[string]any) Trial {
	out := Trial{Decisions: []DecisionInfo{}}
	defs, diags, err := v.engine.Compile(ctx, src)
	if err != nil {
		out.Message = err.Error()
		return out
	}
	if diags.HasErrors() {
		out.Message = formatDiagnostics(diags)
		return out
	}
	out.ModelName = defs.ModelName()
	if described := describeDecisions(defs); described != nil {
		out.Decisions = described
	}
	if decisionID == "" {
		out.OK = true
		return out
	}
	// Membership is checked against the model's index — the same set the deploy gate
	// and the picker use — so "this model does not provide that decision" is one
	// answer everywhere rather than three.
	found := false
	for _, id := range addressableDecisions(defs) {
		if id == decisionID {
			found = true
			break
		}
	}
	if !found {
		out.Message = "this model provides no decision called " + decisionID
		return out
	}
	out.DecisionID = decisionID
	outputs, trace, err := evalDecision(ctx, defs, decisionID, inputs, "this model")
	if err != nil {
		out.Message = err.Error()
		return out
	}
	out.OK, out.Outputs, out.Trace = true, outputs, trace
	return out
}
