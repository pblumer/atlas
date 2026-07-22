package compiler

import (
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
	Id           string            `xml:"id,attr"`
	StartEvents  []xmlNode         `xml:"startEvent"`
	EndEvents    []xmlNode         `xml:"endEvent"`
	ServiceTasks []xmlServiceTask  `xml:"serviceTask"`
	Flows        []xmlSequenceFlow `xml:"sequenceFlow"`
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

type xmlSequenceFlow struct {
	Id        string `xml:"id,attr"`
	SourceRef string `xml:"sourceRef,attr"`
	TargetRef string `xml:"targetRef,attr"`
}
