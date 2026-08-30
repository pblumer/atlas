// Package panorama owns Atlas's design-time ArchiMate models (ADR-0189).
// It deliberately has no dependency on the engine, processor, WAL, or runtime
// state: Panorama documents are architecture source, not execution facts.
package panorama

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

const (
	// NotationArchiMate32 is the stable notation id carried by Panorama resources.
	// ArchiMate 3.2 uses the 3.1 Open Exchange schema, whose XML namespace stayed
	// at /3.0/; the notation and the transport schema are related but not identical
	// version numbers.
	NotationArchiMate32 = "archimate-3.2"

	// ExchangeNamespace is the target namespace of The Open Group ArchiMate 3.1
	// Model Exchange File Format. It is also the namespace used by Atlas's existing
	// generated ArchiMate 3.2 model under docs/architecture/model.
	ExchangeNamespace = "http://www.opengroup.org/xsd/archimate/3.0/"

	// MaxXMLBytes bounds a persisted Panorama document and every validation pass.
	// Architecture models are design-time input; they must never become an
	// unbounded parser workload merely because the API accepted a large body.
	MaxXMLBytes = 4 << 20 // 4 MiB

	maxXMLDepth           = 256
	maxValidationProblems = 100
)

// Problem is one precise reason an Open Exchange document was rejected.
type Problem struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// ValidationResult is the bounded summary returned by Panorama validation.
// Counts describe reusable semantic elements and relationships plus diagram
// views; they do not duplicate the model into an Atlas-specific metamodel.
type ValidationResult struct {
	Valid           bool      `json:"valid"`
	Notation        string    `json:"notation"`
	Namespace       string    `json:"namespace,omitempty"`
	ModelIdentifier string    `json:"modelIdentifier,omitempty"`
	Name            string    `json:"name,omitempty"`
	Elements        int       `json:"elements"`
	Relationships   int       `json:"relationships"`
	Views           int       `json:"views"`
	Problems        []Problem `json:"problems"`
}

type pendingReference struct {
	kind  string
	value string
}

// Validate checks the Open Exchange envelope, the standard ArchiMate type
// vocabulary, global identifier uniqueness, and the references used by
// relationships and diagram objects. It never rewrites the input: a model that
// passes is stored and exported byte-for-byte so unsupported-but-standard
// properties and visual metadata cannot disappear in a round trip.
//
// Full XSD conformance fixtures and the ArchiMate relationship matrix remain a
// separate interoperability layer. This validator is the safe runtime gate: it
// is pure Go, bounded, rejects DTD/directive input, and performs no network or
// external-entity resolution.
func Validate(data []byte) ValidationResult {
	result := ValidationResult{
		Notation: NotationArchiMate32,
		Problems: []Problem{},
	}
	add := func(format string, args ...any) {
		if len(result.Problems) >= maxValidationProblems {
			return
		}
		result.Problems = append(result.Problems, Problem{
			Severity: "error",
			Message:  fmt.Sprintf(format, args...),
		})
	}
	if len(data) == 0 {
		add("XML document is empty")
		return result
	}
	if len(data) > MaxXMLBytes {
		add("XML document exceeds the %d byte limit", MaxXMLBytes)
		return result
	}

	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true

	identifiers := map[string]string{}
	elements := map[string]bool{}
	relationships := map[string]bool{}
	concepts := map[string]bool{}
	var refs []pendingReference
	depth := 0
	rootSeen := false
	rootClosed := false
	captureModelName := false
	var modelName strings.Builder

	register := func(id, kind string) {
		if strings.TrimSpace(id) == "" {
			add("%s identifier is required", kind)
			return
		}
		if previous, exists := identifiers[id]; exists {
			add("duplicate identifier %q (%s and %s)", id, previous, kind)
			return
		}
		identifiers[id] = kind
	}

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			add("invalid XML: %v", err)
			break
		}

		switch tok := token.(type) {
		case xml.Directive:
			add("XML directives are not allowed")
		case xml.StartElement:
			depth++
			if depth > maxXMLDepth {
				add("XML exceeds the maximum XML depth of %d", maxXMLDepth)
				return result
			}
			if rootClosed {
				add("XML contains content after the model root element")
				continue
			}
			if !rootSeen {
				rootSeen = true
				result.Namespace = tok.Name.Space
				if tok.Name.Local != "model" || tok.Name.Space != ExchangeNamespace {
					add("unsupported root element {%s}%s; expected {%s}model",
						tok.Name.Space, tok.Name.Local, ExchangeNamespace)
				}
				result.ModelIdentifier = attribute(tok, "identifier")
				if strings.TrimSpace(result.ModelIdentifier) == "" {
					add("model identifier is required")
				}
				continue
			}

			if tok.Name.Space != ExchangeNamespace {
				continue
			}
			switch tok.Name.Local {
			case "name":
				if depth == 2 && result.Name == "" {
					captureModelName = true
					modelName.Reset()
				}
			case "element":
				id := attribute(tok, "identifier")
				register(id, "element")
				typeName := attribute(tok, "type")
				if typeName == "" {
					add("element %q has no xsi:type", id)
				} else if !archiMateElementTypes[typeName] {
					add("unknown ArchiMate element type %q", typeName)
				}
				if id != "" {
					elements[id] = true
					concepts[id] = true
				}
				result.Elements++
			case "relationship":
				id := attribute(tok, "identifier")
				register(id, "relationship")
				typeName := attribute(tok, "type")
				if typeName == "" {
					add("relationship %q has no xsi:type", id)
				} else if !archiMateRelationshipTypes[typeName] {
					add("unknown ArchiMate relationship type %q", typeName)
				}
				if id != "" {
					relationships[id] = true
					concepts[id] = true
				}
				for _, kind := range []string{"source", "target"} {
					value := attribute(tok, kind)
					if value == "" {
						add("relationship %q %s is required", id, kind)
					} else {
						refs = append(refs, pendingReference{kind: kind, value: value})
					}
				}
				result.Relationships++
			case "view":
				register(attribute(tok, "identifier"), "view")
				result.Views++
			case "node":
				register(attribute(tok, "identifier"), "view node")
				if ref := attribute(tok, "elementRef"); ref != "" {
					refs = append(refs, pendingReference{kind: "elementRef", value: ref})
				}
			case "connection":
				register(attribute(tok, "identifier"), "view connection")
				if ref := attribute(tok, "relationshipRef"); ref != "" {
					refs = append(refs, pendingReference{kind: "relationshipRef", value: ref})
				}
			}
		case xml.CharData:
			if captureModelName {
				modelName.Write(tok)
			}
		case xml.EndElement:
			if captureModelName && tok.Name.Space == ExchangeNamespace && tok.Name.Local == "name" && depth == 2 {
				result.Name = strings.TrimSpace(modelName.String())
				captureModelName = false
			}
			if depth == 1 {
				rootClosed = true
			}
			depth--
		}
	}

	if !rootSeen {
		add("XML document has no root element")
	}
	if strings.TrimSpace(result.Name) == "" {
		add("model name is required")
	}
	for _, ref := range refs {
		switch ref.kind {
		case "elementRef":
			if !elements[ref.value] {
				add("elementRef %q does not exist", ref.value)
			}
		case "relationshipRef":
			if !relationships[ref.value] {
				add("relationshipRef %q does not exist", ref.value)
			}
		default:
			// Open Exchange relationships may target another relationship, so
			// source/target resolve against all globally registered concepts.
			if !concepts[ref.value] {
				if _, exists := identifiers[ref.value]; exists {
					add("%s %q is not an ArchiMate concept", ref.kind, ref.value)
					continue
				}
				add("%s %q does not exist", ref.kind, ref.value)
			}
		}
	}
	result.Valid = len(result.Problems) == 0
	return result
}

func attribute(start xml.StartElement, local string) string {
	for _, attr := range start.Attr {
		if attr.Name.Local == local {
			return attr.Value
		}
	}
	return ""
}

// The type vocabularies below are the enumerations in The Open Group ArchiMate
// 3.1 Open Exchange model schema. ArchiMate 3.2 did not introduce a new exchange
// namespace or a different element vocabulary.
var archiMateElementTypes = map[string]bool{
	"BusinessActor": true, "BusinessRole": true, "BusinessCollaboration": true,
	"BusinessInterface": true, "BusinessProcess": true, "BusinessFunction": true,
	"BusinessInteraction": true, "BusinessEvent": true, "BusinessService": true,
	"BusinessObject": true, "Contract": true, "Representation": true, "Product": true,
	"ApplicationComponent": true, "ApplicationCollaboration": true,
	"ApplicationInterface": true, "ApplicationFunction": true,
	"ApplicationInteraction": true, "ApplicationProcess": true,
	"ApplicationEvent": true, "ApplicationService": true, "DataObject": true,
	"Node": true, "Device": true, "SystemSoftware": true,
	"TechnologyCollaboration": true, "TechnologyInterface": true, "Path": true,
	"CommunicationNetwork": true, "TechnologyFunction": true,
	"TechnologyProcess": true, "TechnologyInteraction": true,
	"TechnologyEvent": true, "TechnologyService": true, "Artifact": true,
	"Equipment": true, "Facility": true, "DistributionNetwork": true, "Material": true,
	"Stakeholder": true, "Driver": true, "Assessment": true, "Goal": true,
	"Outcome": true, "Principle": true, "Requirement": true, "Constraint": true,
	"Meaning": true, "Value": true, "Resource": true, "Capability": true,
	"CourseOfAction": true, "ValueStream": true, "WorkPackage": true,
	"Deliverable": true, "ImplementationEvent": true, "Plateau": true, "Gap": true,
	"Grouping": true, "Location": true, "AndJunction": true, "OrJunction": true,
}

var archiMateRelationshipTypes = map[string]bool{
	"Composition": true, "Aggregation": true, "Assignment": true,
	"Realization": true, "Serving": true, "Access": true, "Influence": true,
	"Triggering": true, "Flow": true, "Specialization": true, "Association": true,
}
