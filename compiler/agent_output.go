package compiler

import (
	"fmt"
	"slices"
	"strings"
)

// RuleAgentOutput marks an <atlas:agentOutput> the answer cannot be held to: a field
// without a name, a name declared twice, a type that is not a scalar, or a set of values
// on a field that is not a string.
const RuleAgentOutput = "agent.output"

// agentOutputTypes is the closed set an answer field may declare. It is narrower than a
// tool parameter's on purpose: a provider's structured output needs every object closed,
// and an "object" or "array" declared by name alone says nothing about what is inside.
// A gateway routes on a scalar; that is what this is for.
var agentOutputTypes = []string{"string", "number", "boolean"}

// agentOutputSpec is one <atlas:agentOutput> as authored.
type agentOutputSpec struct {
	Name, Type, Description, Values string
	Required                        bool
}

// AgentOutput is one field the model's answer must carry
// (ADR-draft-agent-transcript-and-answer): <atlas:agentParam>'s shape turned round. With
// any declared, the answer is one JSON object holding exactly these fields, and the
// worker refuses an answer that is not — which is what lets a gateway route on it.
// Values, when present, is the set a string field is limited to.
type AgentOutput struct {
	Name        int32
	Type        int32
	Description int32 // -1 when the field states none
	Required    bool
	Values      []int32
}

// compileAgentOutputs interns the answer fields declared on one element, in document
// order, and reports what was wrong with any of them. A faulty field is left out rather
// than failing the build, for parseAgentLimits' reason: the model must still load, and
// Validate refuses its deploy.
func (b *Builder) compileAgentOutputs(ownerBpmnID string) ([]AgentOutput, []string) {
	specs := b.agentOutputs[ownerBpmnID]
	if len(specs) == 0 {
		return nil, nil
	}
	var (
		out      []AgentOutput
		problems []string
		seen     = make(map[string]bool, len(specs))
	)
	for _, sp := range specs {
		name := strings.TrimSpace(sp.Name)
		typ := strings.TrimSpace(sp.Type)
		switch {
		case name == "":
			problems = append(problems, "declares an agentOutput without a name")
			continue
		case seen[name]:
			problems = append(problems, fmt.Sprintf("declares the answer field %q twice", name))
			continue
		case !slices.Contains(agentOutputTypes, typ):
			problems = append(problems, fmt.Sprintf("answer field %q has type %q, which is not one of %s",
				name, typ, strings.Join(agentOutputTypes, ", ")))
			continue
		}
		seen[name] = true
		o := AgentOutput{Name: b.intern(name), Type: b.intern(typ), Description: -1, Required: sp.Required}
		if d := strings.TrimSpace(sp.Description); d != "" {
			o.Description = b.intern(d)
		}
		for _, v := range strings.Split(sp.Values, ",") {
			if v = strings.TrimSpace(v); v != "" {
				o.Values = append(o.Values, b.intern(v))
			}
		}
		if len(o.Values) > 0 && typ != "string" {
			problems = append(problems, fmt.Sprintf("answer field %q limits its values but is a %s; only a string field can", name, typ))
			continue
		}
		out = append(out, o)
	}
	return out, problems
}
