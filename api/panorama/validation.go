// Package panorama owns Atlas's design-time ArchiMate models (ADR-0189).
// It deliberately has no dependency on the engine, processor, WAL, or runtime
// state: Panorama documents are architecture source, not execution facts.
package panorama

import (
	"bytes"
	"encoding/xml"
	"errors"
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

// unsupportedEncoding is what the decoder is handed when a document declares an
// encoding other than UTF-8.
//
// The alternative is leaving Decoder.CharsetReader nil, which is what this did: the
// standard library then fails with "encoding \"UTF-16\" declared but
// Decoder.CharsetReader is nil", and an operator reading that has been told about a
// field on a Go struct. The refusal itself stays — Panorama stores and re-exports a
// document byte-for-byte (ADR-0189 §2) and its writers splice into those bytes, so
// accepting a UTF-16 document at the gate would corrupt it at the first edit — but a
// refusal has to be a sentence somebody can act on.
type unsupportedEncoding struct{ label string }

func (e unsupportedEncoding) Error() string { return "unsupported XML encoding " + e.label }

// describeRoot says what a document with the wrong root element *is*, and what to do
// with it instead.
//
// This is the whole of the fix. The refusal was already correct — none of these are
// ArchiMate Open Exchange documents — but it was written in Clark notation for
// whoever wrote the parser, and the person holding the file got two namespace URIs
// and no remedy. The cases below are the files people actually arrive with: a
// modelling tool's own format, a different notation, an office document, a page.
//
// An empty answer means the document is not one this list knows, and the caller
// falls back to naming the format Atlas does read.
func describeRoot(space, local string) string {
	ns := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(space), "/"))
	switch {
	case ns == "http://www.opengroup.org/xsd/archimate":
		// The 2.x exchange namespace. Close enough to the 3.x one that the old
		// message showed two nearly identical URIs and left the reader to spot the
		// difference.
		return "it is an ArchiMate 2.x Model Exchange file, and Atlas reads the 3.x " +
			"format. Re-export the model as ArchiMate 3 Open Exchange."
	case strings.Contains(ns, "archimatetool.com"):
		// Archi's native save format, and the single most likely mistake an
		// ArchiMate user makes — the file picker even offers .archimate.
		return "it is Archi's own model file rather than the exchange format. In " +
			"Archi: File → Export → Model To Open Exchange File, and import the .xml " +
			"that produces."
	case strings.Contains(ns, "omg.org/spec/bpmn"):
		return "it is a BPMN process. Those belong under Modeler; Panorama holds " +
			"architecture models."
	case strings.Contains(ns, "omg.org/spec/dmn"):
		return "it is a DMN decision model. Those are deployed under Decisions."
	case strings.Contains(ns, "omg.org/spec/xmi") || local == "XMI":
		return "it is an XMI export. Atlas reads The Open Group's ArchiMate Model " +
			"Exchange File Format, which most tools offer as a separate export."
	case strings.Contains(ns, "sparxsystems"):
		return "it is an Enterprise Architect export in Sparx's own format. Atlas " +
			"reads The Open Group's ArchiMate Model Exchange File Format."
	case strings.Contains(ns, "mid.de"):
		return "it is an export from a MID tool in MID's own format — this one wraps " +
			"a spreadsheet. Atlas reads The Open Group's ArchiMate Model Exchange " +
			"File Format, which is a separate export."
	case strings.Contains(ns, "openxmlformats.org"),
		strings.Contains(ns, "urn:schemas-microsoft-com:office"),
		strings.Contains(ns, "spreadsheetml"):
		return "it is a Microsoft Office document, not an architecture model."
	case ns == "http://www.w3.org/1999/xhtml", strings.EqualFold(local, "html"):
		return "it is an HTML page. A login page or an error page saved from a " +
			"browser is the usual way one of these arrives here."
	case local == "model" && space == "":
		// The one that reads as "model; expected model" and looks like a bug in Atlas.
		return "its root element is <model>, but the document declares no XML " +
			"namespace at all. An Open Exchange file opens with " +
			`<model xmlns="` + ExchangeNamespace + `" …>, and a file that has lost ` +
			"that declaration is no longer one."
	case local == "model":
		return "its <model> root belongs to another notation."
	}
	return ""
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
	// Not a transcoder — a name for the refusal. See unsupportedEncoding.
	decoder.CharsetReader = func(label string, _ io.Reader) (io.Reader, error) {
		return nil, unsupportedEncoding{label: label}
	}

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
			var encoding unsupportedEncoding
			if errors.As(err, &encoding) {
				add("Atlas reads UTF-8 XML, and this document declares %q. Re-save or "+
					"re-export it as UTF-8 and import it again.", encoding.label)
				// Nothing was read, so "no root element" and "model name is required"
				// below would be two more complaints about the same one fact.
				return result
			}
			add("invalid XML: %v", err)
			break
		}

		switch tok := token.(type) {
		case xml.Directive:
			// The rule stays — a DOCTYPE can name external content, and this validator
			// resolves nothing — but "not allowed" left the reader with no idea what
			// they had handed over. An HTML page is by far the most common one.
			if head := directiveHead(tok); strings.EqualFold(head, "DOCTYPE html") {
				add("This is an HTML page, not an ArchiMate Open Exchange document. A " +
					"login page or an error page saved from a browser is the usual way " +
					"one of these arrives here.")
				return result
			}
			add("XML directives are not accepted: a DOCTYPE can name content outside " +
				"the document, and Panorama resolves nothing. Remove it and import the " +
				"model itself.")
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
					what := describeRoot(tok.Name.Space, tok.Name.Local)
					if what == "" {
						what = "Atlas reads The Open Group's ArchiMate Model Exchange " +
							"File Format; export the model in that format and import the " +
							"XML it produces."
					}
					where := tok.Name.Space
					if where == "" {
						where = "none"
					}
					add("This is not an ArchiMate Open Exchange document: %s "+
						"(root element %q, namespace %s)", what, tok.Name.Local, where)
					// And nothing else. Every check past this one is about a model, and
					// this document is not one: "model identifier is required" against a
					// spreadsheet is noise stacked on the one problem worth reading.
					return result
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

// directiveHead is the first two words of a directive, which is enough to tell a
// DOCTYPE for an HTML page from any other one and short enough that a hostile
// document cannot turn the message into a payload.
func directiveHead(directive xml.Directive) string {
	fields := strings.Fields(string(directive))
	if len(fields) > 2 {
		fields = fields[:2]
	}
	return strings.Join(fields, " ")
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
