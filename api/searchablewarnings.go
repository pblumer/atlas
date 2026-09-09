package api

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
)

// searchableStructuredTypes are the declared types the value index cannot hold. It
// answers equality and prefix over a byte string, so a structured value is refused
// rather than stored under its exact encoding (ADR-0244).
var searchableStructuredTypes = map[string]bool{"json": true, "object": true, "array": true}

// searchableDeclarationWarnings reports an atlas:searchable declaration the model
// cannot honour — one sentence per name, per process.
//
// The failure it exists for is silent in a way the others here are not. A declared name
// the deploy accepts and nothing ever writes indexes nothing: the search stays empty
// forever, the operator concludes the instance is not there, and no screen anywhere says
// why. The Modeler marks it while the name is being typed, but a model deployed from a
// pipeline or over the API never passes through the Modeler, so the same reading belongs
// in the deploy's own answer.
//
// It reads the model's bytes rather than the compiled process on purpose. The question
// is about what the model *says* — its declared start variables and their types, its
// output mappings, its result variables — and the raw document answers it generically,
// by attribute name, without this having to know one Worker Type from another. A kind
// added tomorrow writes its result into a resultVariable like every other, and is
// counted here without anybody remembering to add it.
//
// Two findings, and they differ in how sure they are:
//
//   - A name the model itself declares as a structured start variable can never be
//     indexed. That is settled by the declaration, so it is reported for any process.
//   - A name nothing in the model writes is a question, not a verdict: a worker's own
//     output, a message payload or an operator's write through the variables API can all
//     produce a name the document never mentions. So it is reported only where the model
//     has stated its inputs — it declares start variables and links no form, whose fields
//     are a separate resource this cannot see — and the sentence says plainly that an
//     outside writer makes it fine.
//
// Best-effort, like the namespace warnings beside it: a token error ends the walk with
// what was found, because this describes a deploy that has already succeeded.
func searchableDeclarationWarnings(model []byte) []string {
	var out []string
	var cur *searchableScan
	dec := xml.NewDecoder(bytes.NewReader(model))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "process" {
				cur = newSearchableScan(t)
				continue
			}
			if cur != nil {
				cur.read(t)
			}
		case xml.EndElement:
			if t.Name.Local == "process" && cur != nil {
				out = append(out, cur.warnings()...)
				cur = nil
			}
		}
	}
	if cur != nil {
		out = append(out, cur.warnings()...) // a truncated document still says what it said
	}
	return out
}

// searchableScan is what one <bpmn:process> said about its searchable declaration and
// about the variables it writes.
type searchableScan struct {
	id        string
	declared  []string
	startVars map[string]string // declared start variable → its declared type
	writers   map[string]bool   // names an output mapping or a result variable produces
	linksForm bool              // a form's fields are variables this walk cannot see
}

// newSearchableScan begins a process scan, or returns nil for a process that declares
// nothing searchable — which is most of them, and which must cost one attribute read.
func newSearchableScan(t xml.StartElement) *searchableScan {
	s := &searchableScan{startVars: map[string]string{}, writers: map[string]bool{}}
	for _, a := range t.Attr {
		switch a.Name.Local {
		case "id":
			s.id = a.Value
		case "searchable":
			for _, part := range strings.Split(a.Value, ",") {
				if name := strings.TrimSpace(part); name != "" {
					s.declared = append(s.declared, name)
				}
			}
		}
	}
	if len(s.declared) == 0 {
		return nil
	}
	return s
}

// read takes one element of the process: what it declares as an input, and what it
// writes. Matched by local name, the way the compiler matches its own attributes, so a
// document that binds the prefixes differently reads the same.
func (s *searchableScan) read(t xml.StartElement) {
	switch t.Name.Local {
	case "startVariable":
		var name, typ string
		for _, a := range t.Attr {
			switch a.Name.Local {
			case "name":
				name = strings.TrimSpace(a.Value)
			case "type":
				typ = strings.TrimSpace(a.Value)
			}
		}
		if name != "" {
			s.startVars[name] = typ
		}
	case "output": // <zeebe:output source="…" target="…"/>
		for _, a := range t.Attr {
			if a.Name.Local == "target" {
				if name := strings.TrimSpace(a.Value); name != "" {
					s.writers[name] = true
				}
			}
		}
	case "formDefinition":
		s.linksForm = true
	}
	// Every Worker Type, every script and every decision writes its result into a
	// resultVariable, so one attribute name covers all of them — including the ones
	// added after this was written.
	for _, a := range t.Attr {
		if a.Name.Local == "resultVariable" {
			if name := strings.TrimSpace(a.Value); name != "" {
				s.writers[name] = true
			}
		}
	}
}

// warnings turns one process's scan into sentences, in the order the names were
// declared, so a deploy response reads the same way twice.
func (s *searchableScan) warnings() []string {
	// The model has stated its inputs only if it declares start variables and links no
	// form; without that, "nothing writes this name" says nothing about the name.
	stated := len(s.startVars) > 0 && !s.linksForm
	var out []string
	seen := map[string]bool{}
	for _, name := range s.declared {
		if seen[name] {
			continue // the repeated name is the compiler's to refuse, not this one's to explain twice
		}
		seen[name] = true
		typ, declared := s.startVars[name]
		switch {
		case declared && searchableStructuredTypes[typ]:
			out = append(out, fmt.Sprintf(
				"process %q declares %q searchable, but declares it as %s: the value index holds only text, "+
					"a number or true/false, so a search for %q will never match. Index a scalar the model "+
					"carries instead, or drop the name from atlas:searchable.", s.id, name, typ, name))
		case !declared && stated && !s.writers[name]:
			out = append(out, fmt.Sprintf(
				"process %q declares %q searchable, but nothing in the model produces that name: it is not "+
					"one of the declared start variables, and no output mapping or result variable writes it. "+
					"If a worker writes it, or it is set through the variables API, this is fine; otherwise it "+
					"is a spelling that will never be found.", s.id, name))
		}
	}
	return out
}
