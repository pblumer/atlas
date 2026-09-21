package dmn

import (
	"encoding/xml"

	tdmn "github.com/pblumer/temis/dmn"
)

// A decision service is DMN's published interface over a part of the DRD
// (DMN §10.4). It names four sets of elements and holds no logic of its own:
// the decisions it returns, the decisions it evaluates internally, the decisions
// whose results the *caller* supplies as a boundary, and the input data it takes.
// What it buys is the one thing a bare decision cannot express — a caller
// addresses the service and never learns which decision inside computes what, so
// the inside can be rearranged without breaking anyone (ADR-0398).
//
// temis compiles and evaluates one completely, honouring the caller-supplied
// boundary and coercing the declared output type. What it does not do is *list*
// them: `Index()` covers decisions and input data, `Graph()` has no node for a
// service, and `Functions()` lists business knowledge models only — measured
// against the pinned version. So Atlas reads the names out of the document and
// then asks the engine for each one, which keeps the engine the single authority
// on what exists: a service the document declares but temis could not compile
// resolves to nothing and is dropped here.

// xmlServiceDefs is the decision-service half of a DMN document — the only part
// of the XML this file reads.
type xmlServiceDefs struct {
	Services []xmlService `xml:"decisionService"`
}

type xmlService struct {
	ID      string   `xml:"id,attr"`
	Name    string   `xml:"name,attr"`
	Outputs []xmlRef `xml:"outputDecision"`
	// Encapsulated is read only by the layout generator, which has to know which
	// compartment of the service box a decision belongs in (layout.go). Describing
	// a service needs only its name and what it takes, so nothing here reads it.
	Encapsulated   []xmlRef `xml:"encapsulatedDecision"`
	InputDecisions []xmlRef `xml:"inputDecision"`
	InputData      []xmlRef `xml:"inputData"`
}

// servicesPublishingNothing names the decision services a document declares with no
// output decision, in document order.
//
// DMN gives a service one or more of them (1.5 Table 17: outputDecisions [1..*]):
// they are what it answers with, and the whole reason to address a service rather
// than the decision inside it. One with none is not an incomplete model that still
// half works — temis compiles it, Atlas lists it, the decision picker offers it, a
// business rule task calls it, and the answer is empty.
//
// It is read from the document rather than from the compiled model because the
// engine does not object: a service with nothing to return is, to a compiler, a
// service with nothing to do.
func servicesPublishingNothing(src []byte) []string {
	var parsed xmlServiceDefs
	if err := xml.Unmarshal(src, &parsed); err != nil {
		return nil
	}
	var out []string
	for _, s := range parsed.Services {
		if len(s.Outputs) > 0 {
			continue
		}
		switch {
		case s.Name != "":
			out = append(out, s.Name)
		case s.ID != "":
			out = append(out, s.ID)
		default:
			// A service with neither is refused all the same: it is the document that
			// is wrong, and saying so without a name beats letting it through.
			out = append(out, "(unnamed)")
		}
	}
	return out
}

// serviceNames reduces described services to the names they answer to, in
// document order and each name once. A model that declares one name twice is
// refused by [nameCollisions], so the repetition only reaches here through a
// reload, which does not re-apply the gate (ADR-0177) — and there the name still
// has to appear once.
func serviceNames(infos []DecisionInfo) []string {
	if len(infos) == 0 {
		return nil
	}
	out := make([]string, 0, len(infos))
	seen := map[string]bool{}
	for _, info := range infos {
		if seen[info.ID] {
			continue
		}
		seen[info.ID] = true
		out = append(out, info.ID)
	}
	return out
}

// nameCollisions returns the names this model gives to more than one thing: a
// name held by both a decision and a decision service, or by two services. They are the reason the two share one addressing vocabulary
// at all: a `decisionId` is one string, so a model in which the string means two
// things has no answer, and the deploy gate refuses it rather than picking one
// silently.
func nameCollisions(defs *tdmn.Definitions, src []byte) []string {
	declared := describeServices(defs, src)
	if len(declared) == 0 {
		return nil
	}
	taken := map[string]bool{}
	for _, d := range addressableDecisions(defs) {
		taken[d] = true
	}
	var out []string
	reported := map[string]bool{}
	for _, info := range declared {
		// Either kind of ambiguity, because a decisionId cannot express either: a name
		// a decision already holds, or a name a second service declares again.
		if !taken[info.ID] || reported[info.ID] {
			taken[info.ID] = true
			continue
		}
		reported[info.ID] = true
		out = append(out, info.ID)
	}
	return out
}

// describeServices describes each decision service the way describeDecisions
// describes a decision. It is the one place the document is read for services —
// every other answer about them is derived from what it returns — so the picker and the try-a-decision panel can offer one:
// its inputs are the names a caller must supply — its input data and its input
// decisions, under the FEEL identifiers the evaluation binds them to, in the
// order temis registers them as the service's parameters.
//
// A service with exactly one output decision reports that decision's result
// variable and declared type as its output. With several, the result is a
// context keyed by output-decision name, which this shape cannot express, so the
// output is left as the service's own name with no type.
//
// One entry per declaration: a document that declares two services under one
// name yields two, so [nameCollisions] can see it. Such a model is refused, and
// nothing downstream has to decide which of the two it meant.
func describeServices(defs *tdmn.Definitions, src []byte) []DecisionInfo {
	var parsed xmlServiceDefs
	if err := xml.Unmarshal(src, &parsed); err != nil {
		return nil
	}
	g := defs.Graph()
	byID := make(map[string]tdmn.GraphNode, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	field := func(ref xmlRef) (DecisionField, bool) {
		n, ok := byID[localHref(ref.Href)]
		if !ok {
			return DecisionField{}, false
		}
		name := n.VarName
		if name == "" {
			name = n.Name
		}
		if name == "" {
			return DecisionField{}, false
		}
		return DecisionField{Name: name, Type: n.DataType}, true
	}

	var out []DecisionInfo
	for _, s := range parsed.Services {
		name := s.Name
		if name == "" {
			name = s.ID
		}
		if name == "" {
			continue
		}
		if _, err := defs.Service(name); err != nil {
			continue
		}
		info := DecisionInfo{ID: name, Name: name, Service: true, Output: DecisionField{Name: name}}
		for _, ref := range append(append([]xmlRef{}, s.InputData...), s.InputDecisions...) {
			if f, ok := field(ref); ok {
				info.Inputs = append(info.Inputs, f)
			}
		}
		if len(s.Outputs) == 1 {
			if f, ok := field(s.Outputs[0]); ok {
				info.Output = f
			}
		}
		out = append(out, info)
	}
	return out
}

// localHref turns a same-document reference ("#id") into the element id. A href
// into another document names nothing here, so it yields "".
func localHref(href string) string {
	if len(href) < 2 || href[0] != '#' {
		return ""
	}
	return href[1:]
}
