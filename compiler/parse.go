package compiler

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
)

// defaultRetries is used when a service task's task definition omits retries.
const defaultRetries = 3

// Parse reads a BPMN 2.0 XML model and compiles the first <process> into an
// immutable CompiledProcess keyed by key at the given version. It is the front
// end to the linearizer (compiler.md stages 1–2 and 6): it parses the XML,
// resolves string element ids to integer indices, and pours the result into the
// shared Builder. Validation beyond reference integrity (reachability, gateway
// coverage) is a later stage.
//
// Service-task job types come from the Zeebe task-definition extension element
// (<zeebe:taskDefinition type="..." retries="..."/>), the de-facto standard for
// executable BPMN.
func Parse(key uint64, version int32, r io.Reader) (*CompiledProcess, error) {
	var defs xmlDefinitions
	if err := xml.NewDecoder(r).Decode(&defs); err != nil {
		return nil, fmt.Errorf("compiler: parse BPMN: %w", err)
	}
	if len(defs.Processes) == 0 {
		return nil, fmt.Errorf("compiler: no <process> element in definitions")
	}
	proc := defs.Processes[0]

	b := NewBuilder(key, proc.Id, version)
	ids := make(map[string]int32, len(proc.StartEvents)+len(proc.ServiceTasks)+len(proc.EndEvents))
	register := func(id string, nodeID int32) error {
		if id == "" {
			return fmt.Errorf("compiler: element with empty id")
		}
		if _, dup := ids[id]; dup {
			return fmt.Errorf("compiler: duplicate element id %q", id)
		}
		ids[id] = nodeID
		b.SetElementBpmnId(nodeID, id) // retain for the live diagram overlay
		return nil
	}

	for _, s := range proc.StartEvents {
		if err := register(s.Id, b.AddStartEvent()); err != nil {
			return nil, err
		}
	}
	for _, st := range proc.ServiceTasks {
		if st.TaskDefinition.Type == "" {
			return nil, fmt.Errorf("compiler: service task %q has no task definition type", st.Id)
		}
		retries := int32(defaultRetries)
		if r := st.TaskDefinition.Retries; r != "" {
			n, err := strconv.Atoi(r)
			if err != nil {
				return nil, fmt.Errorf("compiler: service task %q has invalid retries %q: %w", st.Id, r, err)
			}
			retries = int32(n)
		}
		if err := register(st.Id, b.AddServiceTask(st.TaskDefinition.Type, retries)); err != nil {
			return nil, err
		}
	}
	for _, brt := range proc.BusinessRuleTasks {
		if brt.CalledDecision.DecisionId == "" {
			return nil, fmt.Errorf("compiler: business rule task %q has no calledDecision decisionId", brt.Id)
		}
		retries := int32(defaultRetries)
		if r := brt.CalledDecision.Retries; r != "" {
			n, err := strconv.Atoi(r)
			if err != nil {
				return nil, fmt.Errorf("compiler: business rule task %q has invalid retries %q: %w", brt.Id, r, err)
			}
			retries = int32(n)
		}
		statics, mappings, err := decisionInputs(brt.Inputs)
		if err != nil {
			return nil, fmt.Errorf("compiler: business rule task %q: %w", brt.Id, err)
		}
		node, err := b.AddBusinessRuleTask(BusinessRule{
			DecisionId:     brt.CalledDecision.DecisionId,
			StaticInputs:   statics,
			InputMappings:  mappings,
			ResultVariable: brt.CalledDecision.ResultVariable,
			Retries:        retries,
		})
		if err != nil {
			return nil, err
		}
		if err := register(brt.Id, node); err != nil {
			return nil, err
		}
	}
	for _, e := range proc.EndEvents {
		if err := register(e.Id, b.AddEndEvent()); err != nil {
			return nil, err
		}
	}

	if !b.hasStartEvent() {
		return nil, fmt.Errorf("compiler: process %q has no start event", proc.Id)
	}

	for _, f := range proc.Flows {
		src, ok := ids[f.SourceRef]
		if !ok {
			return nil, fmt.Errorf("compiler: flow %q references unknown sourceRef %q", f.Id, f.SourceRef)
		}
		tgt, ok := ids[f.TargetRef]
		if !ok {
			return nil, fmt.Errorf("compiler: flow %q references unknown targetRef %q", f.Id, f.TargetRef)
		}
		b.Connect(src, tgt)
	}

	return b.Build()
}

// BPMN XML is matched by element/attribute local name, so namespace prefixes
// (bpmn:, zeebe:) are handled transparently by encoding/xml.

type xmlDefinitions struct {
	Processes []xmlProcess `xml:"process"`
}

type xmlProcess struct {
	Id                string                `xml:"id,attr"`
	StartEvents       []xmlNode             `xml:"startEvent"`
	EndEvents         []xmlNode             `xml:"endEvent"`
	ServiceTasks      []xmlServiceTask      `xml:"serviceTask"`
	BusinessRuleTasks []xmlBusinessRuleTask `xml:"businessRuleTask"`
	Flows             []xmlSequenceFlow     `xml:"sequenceFlow"`
}

type xmlNode struct {
	Id string `xml:"id,attr"`
}

type xmlServiceTask struct {
	Id             string            `xml:"id,attr"`
	TaskDefinition xmlTaskDefinition `xml:"extensionElements>taskDefinition"`
}

type xmlTaskDefinition struct {
	Type    string `xml:"type,attr"`
	Retries string `xml:"retries,attr"`
}

// A business rule task references a DMN decision via the Zeebe calledDecision
// extension (<zeebe:calledDecision decisionId="..." resultVariable="..."/>) and
// binds the decision's inputs with Atlas decisionInput extension elements. A
// decisionInput is either static or a variable binding:
//
//	<atlas:decisionInput name="Season" value="Winter"/>   static constant
//	<atlas:decisionInput name="Guests" variable="guests"/> read from a variable
//
// A static value is parsed as JSON when it parses (so numbers and booleans keep
// their FEEL types), else used verbatim as a string. resultVariable, if set,
// names the variable the decision's outputs are written back into.
type xmlBusinessRuleTask struct {
	Id             string             `xml:"id,attr"`
	CalledDecision xmlCalledDecision  `xml:"extensionElements>calledDecision"`
	Inputs         []xmlDecisionInput `xml:"extensionElements>decisionInput"`
}

type xmlCalledDecision struct {
	DecisionId     string `xml:"decisionId,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	Retries        string `xml:"retries,attr"`
}

type xmlDecisionInput struct {
	Name     string `xml:"name,attr"`
	Value    string `xml:"value,attr"`
	Variable string `xml:"variable,attr"`
}

// decisionInputs splits parsed <decisionInput> elements into static constants
// (name→value) and variable bindings (decision input name→variable name). A
// decisionInput naming a variable is a binding; otherwise it is a static value,
// parsed as JSON when possible. A name may not be declared twice, in either form.
func decisionInputs(in []xmlDecisionInput) (statics map[string]any, mappings map[string]string, err error) {
	seen := map[string]bool{}
	for _, di := range in {
		if di.Name == "" {
			return nil, nil, fmt.Errorf("decisionInput with empty name")
		}
		if seen[di.Name] {
			return nil, nil, fmt.Errorf("duplicate decisionInput name %q", di.Name)
		}
		seen[di.Name] = true
		if di.Variable != "" {
			if mappings == nil {
				mappings = map[string]string{}
			}
			mappings[di.Name] = di.Variable
			continue
		}
		var v any
		if jerr := json.Unmarshal([]byte(di.Value), &v); jerr != nil {
			v = di.Value // not JSON: treat as a literal string
		}
		if statics == nil {
			statics = map[string]any{}
		}
		statics[di.Name] = v
	}
	return statics, mappings, nil
}

type xmlSequenceFlow struct {
	Id        string `xml:"id,attr"`
	SourceRef string `xml:"sourceRef,attr"`
	TargetRef string `xml:"targetRef,attr"`
}
