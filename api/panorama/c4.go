package panorama

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

// The C4 projection (ADR-0211 §8).
//
// C4 is offered as a *projection*, never as a second authored notation. ADR-0189 §7
// forbids treating notations as renderer themes over one model, and it is right to:
// a C4 Container and an ArchiMate Application Component are not the same concept,
// and a toggle that pretended otherwise would export semantically false diagrams
// under a standard's name.
//
// What makes a projection honest where a theme is not:
//
//   - it is one-directional. There is no import, no round trip, and no C4 source
//     document. ArchiMate stays the only thing anybody authors.
//   - the mapping is small, explicit and versioned, so a consumer can tell what a
//     given projection meant.
//   - loss is reported rather than hidden. Everything the mapping cannot express is
//     listed, with the reason. A projection that silently dropped what it could not
//     say would look complete and be wrong, which is the exact failure the ban on
//     notation-as-a-theme exists to prevent.

// NotationC4Projection is the projection's own id. It is deliberately not a peer of
// archimate-3.2 in the notation list: nothing is authored in it.
const NotationC4Projection = "c4-projection"

// C4MappingVersion versions the mapping below. It is a small public contract: a
// change in what an ArchiMate type projects to is a version bump, not a quiet
// redefinition, because exported pictures outlive the code that made them.
const C4MappingVersion = 1

// The C4 vocabulary this projection uses.
const (
	C4Person         = "Person"
	C4SoftwareSystem = "SoftwareSystem"
	C4Container      = "Container"
)

// c4ElementTypes is the whole element mapping. Anything absent is reported as loss
// rather than approximated: C4 is notation-poor, and inventing a nearest neighbour
// for a concept it does not have is how a projection starts lying.
var c4ElementTypes = map[string]string{
	"BusinessActor":            C4Person,
	"BusinessRole":             C4Person,
	"ApplicationComponent":     C4SoftwareSystem,
	"ApplicationCollaboration": C4SoftwareSystem,
	"Node":                     C4Container,
	"SystemSoftware":           C4Container,
}

// c4Relationships are the ArchiMate relationships that become a C4 arrow. C4 has
// one relationship kind, so several ArchiMate kinds collapse onto it — the source
// kind is carried so a reader can see which.
var c4Relationships = map[string]bool{
	"Serving":     true,
	"Triggering":  true,
	"Flow":        true,
	"Access":      true,
	"Association": true,
}

// c4Nesting are the relationships C4 expresses by containment instead of an arrow.
// Drawing a composition as a relationship would misstate the structure: in C4 a
// container sits inside its system.
var c4Nesting = map[string]bool{
	"Composition": true,
	"Aggregation": true,
}

// C4Element is one projected element. Source is the ArchiMate identifier it came
// from, so a reader can always get back to the authored model.
type C4Element struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	// Parent is the enclosing element, from a composition or aggregation.
	Parent     string `json:"parent,omitempty"`
	SourceType string `json:"sourceType"`
}

// C4Relationship is one projected arrow. SourceType names the ArchiMate
// relationship it came from, because C4 has only one kind and the distinction is
// otherwise lost.
type C4Relationship struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	Name       string `json:"name,omitempty"`
	SourceType string `json:"sourceType"`
}

// C4Loss is one thing the projection could not express, and why.
type C4Loss struct {
	ID         string `json:"id"`
	SourceType string `json:"sourceType"`
	Name       string `json:"name,omitempty"`
	Reason     string `json:"reason"`
}

// C4Projection is a read-only view of an ArchiMate model in C4 terms.
type C4Projection struct {
	Notation       string `json:"notation"`
	SourceNotation string `json:"sourceNotation"`
	SourceModelID  string `json:"sourceModelId"`
	SourceRevision int64  `json:"sourceRevision"`
	MappingVersion int    `json:"mappingVersion"`
	// ReadOnly is always true and is in the payload on purpose: a consumer must not
	// be able to mistake a projection for a source document.
	ReadOnly      bool             `json:"readOnly"`
	Elements      []C4Element      `json:"elements"`
	Relationships []C4Relationship `json:"relationships"`
	Dropped       []C4Loss         `json:"dropped"`
}

// archiConcept is one element or relationship as read from the document.
type archiConcept struct {
	id, typ, name, doc, source, target string
}

// ProjectToC4 projects an Open Exchange document into C4 terms.
func ProjectToC4(data []byte, modelID string, revision int64) (C4Projection, error) {
	out := C4Projection{
		Notation: NotationC4Projection, SourceNotation: NotationArchiMate32,
		SourceModelID: modelID, SourceRevision: revision,
		MappingVersion: C4MappingVersion, ReadOnly: true,
		Elements: []C4Element{}, Relationships: []C4Relationship{}, Dropped: []C4Loss{},
	}
	elements, relationships, err := readConcepts(data)
	if err != nil {
		return out, err
	}

	projected := map[string]bool{}
	for _, e := range elements {
		c4type, mapped := c4ElementTypes[e.typ]
		if !mapped {
			out.Dropped = append(out.Dropped, C4Loss{
				ID: e.id, SourceType: e.typ, Name: e.name,
				Reason: fmt.Sprintf("C4 has no concept for an ArchiMate %s", e.typ),
			})
			continue
		}
		projected[e.id] = true
		out.Elements = append(out.Elements, C4Element{
			ID: e.id, Type: c4type, Name: e.name, Description: e.doc, SourceType: e.typ,
		})
	}

	parentOf := map[string]string{}
	for _, r := range relationships {
		// A relationship whose end was not projected cannot be drawn, and saying so
		// is more useful than an arrow into nothing.
		if !projected[r.source] || !projected[r.target] {
			missing := r.source
			if projected[r.source] {
				missing = r.target
			}
			out.Dropped = append(out.Dropped, C4Loss{
				ID: r.id, SourceType: r.typ, Name: r.name,
				Reason: fmt.Sprintf("its end %q is not projected", missing),
			})
			continue
		}
		switch {
		case c4Nesting[r.typ]:
			parentOf[r.target] = r.source
		case c4Relationships[r.typ]:
			out.Relationships = append(out.Relationships, C4Relationship{
				ID: r.id, Source: r.source, Target: r.target, Name: r.name, SourceType: r.typ,
			})
		default:
			out.Dropped = append(out.Dropped, C4Loss{
				ID: r.id, SourceType: r.typ, Name: r.name,
				Reason: fmt.Sprintf("C4 has no relationship for an ArchiMate %s", r.typ),
			})
		}
	}
	for i := range out.Elements {
		out.Elements[i].Parent = parentOf[out.Elements[i].ID]
	}

	// Stable order throughout: an export that churned between identical runs could
	// not be diffed, and a projection is something people commit.
	sort.SliceStable(out.Elements, func(i, j int) bool { return out.Elements[i].ID < out.Elements[j].ID })
	sort.SliceStable(out.Relationships, func(i, j int) bool { return out.Relationships[i].ID < out.Relationships[j].ID })
	sort.SliceStable(out.Dropped, func(i, j int) bool { return out.Dropped[i].ID < out.Dropped[j].ID })
	return out, nil
}

// readConcepts walks the document once for the elements and relationships the
// projection needs. It reads nothing else and rewrites nothing.
func readConcepts(data []byte) ([]archiConcept, []archiConcept, error) {
	if len(data) > MaxXMLBytes {
		return nil, nil, fmt.Errorf("XML document exceeds the %d byte limit", MaxXMLBytes)
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true

	var elements, relationships []archiConcept
	var current *archiConcept
	var into *[]archiConcept
	var capture *strings.Builder
	var field *string
	depth := 0

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("invalid XML: %w", err)
		}
		switch tok := token.(type) {
		case xml.Directive:
			return nil, nil, fmt.Errorf("XML directives are not allowed")
		case xml.StartElement:
			depth++
			if depth > maxXMLDepth {
				return nil, nil, fmt.Errorf("XML exceeds the maximum XML depth of %d", maxXMLDepth)
			}
			switch tok.Name.Local {
			case "element", "relationship":
				current = &archiConcept{
					id: attribute(tok, "identifier"), typ: localType(attribute(tok, "type")),
					source: attribute(tok, "source"), target: attribute(tok, "target"),
				}
				if tok.Name.Local == "element" {
					into = &elements
				} else {
					into = &relationships
				}
			case "name", "documentation":
				if current != nil {
					capture = &strings.Builder{}
					if tok.Name.Local == "name" {
						field = &current.name
					} else {
						field = &current.doc
					}
				}
			}
		case xml.CharData:
			if capture != nil {
				capture.Write(tok)
			}
		case xml.EndElement:
			depth--
			switch tok.Name.Local {
			case "name", "documentation":
				if capture != nil && field != nil && *field == "" {
					*field = strings.TrimSpace(capture.String())
				}
				capture, field = nil, nil
			case "element", "relationship":
				if current != nil && into != nil {
					*into = append(*into, *current)
				}
				current, into = nil, nil
			}
		}
	}
	return elements, relationships, nil
}
