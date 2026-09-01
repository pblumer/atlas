package panorama

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Atlas bindings (ADR-0189 §4) say which Atlas resource an ArchiMate element
// refers to. They are ordinary ArchiMate properties carried by the exchange
// document, not an Atlas-specific carrier, so a bound model stays a standard model
// and its bindings travel with it into any conformant tool.
//
// A binding holds a stable, opaque Atlas id and nothing else. Names, health,
// versions and every other mutable fact are resolved by the server at read time —
// a drawing that stored them would be a second, stale runtime database, which is
// the thing ADR-0189 exists to prevent.

// BindingContractVersion is the version of this key set. It is a small public
// contract: the keys below are what another tool can rely on finding, so a change
// to their meaning is a version bump rather than a quiet redefinition.
const BindingContractVersion = 1

// The binding keys. This is an allowlist, and that is load-bearing: ADR-0189 §4
// forbids credential references, tokens, passwords and secret values in a binding,
// and an allowlist makes that structural. atlas.credentialRef is refused because it
// was never permitted, not because somebody remembered to ban it.
const (
	KeyApplicationID      = "atlas.applicationId"
	KeyProcessID          = "atlas.processId"
	KeyConnectorID        = "atlas.connectorId"
	KeyJobType            = "atlas.jobType"
	KeyRuntimeID          = "atlas.runtimeId"
	KeyDeploymentTargetID = "atlas.deploymentTargetId"
	KeyReleaseID          = "atlas.releaseId"
)

// bindingKeyPrefix is the namespace this contract owns. A property outside it
// belongs to somebody else and is neither read nor reported — it round-trips
// untouched because a read never rewrites the document.
const bindingKeyPrefix = "atlas."

// allowedOn maps each key to the ArchiMate element types it is meaningful on.
// An application id on a Node means nothing, and carrying it would let a nonsense
// binding travel to another tool looking official — so the pairing is part of the
// contract rather than a convention.
var allowedOn = map[string][]string{
	KeyApplicationID:      {"ApplicationComponent"},
	KeyProcessID:          {"BusinessProcess"},
	KeyConnectorID:        {"ApplicationService", "ApplicationInterface"},
	KeyJobType:            {"ApplicationService", "ApplicationInterface"},
	KeyRuntimeID:          {"Node"},
	KeyDeploymentTargetID: {"Node"},
	KeyReleaseID:          {"Artifact"},
}

// Binding is every id bound under one key on one element. Values is a list because
// the exchange format expresses many-to-many by repeating the property, and
// ADR-0189 §4 requires that: one ArchiMate component can be implemented by several
// Atlas process applications, and one process application can contribute to
// several components.
type Binding struct {
	ElementID   string `json:"elementId"`
	ElementType string `json:"elementType"`
	// ElementName is what the architect called it. The mesh overlay shows it, and
	// for an element Atlas has no resource for it is the only name there is.
	ElementName string   `json:"elementName,omitempty"`
	Key         string   `json:"key"`
	Values      []string `json:"values"`
}

// BindingSet is what one document declares, plus every reason a declaration was
// refused. Problems are carried rather than returned as an error: a model with one
// bad binding still has good ones, and hiding them all behind a single failure
// would make a typo look like an empty landscape.
type BindingSet struct {
	ContractVersion int       `json:"contractVersion"`
	Bindings        []Binding `json:"bindings"`
	Problems        []Problem `json:"problems"`
}

// BindingKeys lists the contract's keys in a stable order, for an API that has to
// tell a client what it may set.
func BindingKeys() []string {
	return []string{
		KeyApplicationID, KeyProcessID, KeyConnectorID, KeyJobType,
		KeyRuntimeID, KeyDeploymentTargetID, KeyReleaseID,
	}
}

// ExtractBindings reads every Atlas binding an Open Exchange document declares.
//
// It never rewrites the input. Reading a model must not be able to change it, and
// the byte-for-byte guarantee is what keeps unsupported-but-standard content
// (ADR-0189 §2) safe across a round trip.
//
// The pass is two-phase because the schema puts propertyDefinitions near the end of
// the model, after the elements that reference them: a property cannot be resolved
// on sight, so references are collected and resolved once the document is read.
func ExtractBindings(data []byte) (BindingSet, error) {
	set := BindingSet{ContractVersion: BindingContractVersion, Bindings: []Binding{}, Problems: []Problem{}}
	if len(data) > MaxXMLBytes {
		return set, fmt.Errorf("XML document exceeds the %d byte limit", MaxXMLBytes)
	}
	add := func(format string, args ...any) {
		if len(set.Problems) >= maxValidationProblems {
			return
		}
		set.Problems = append(set.Problems, Problem{Severity: "error", Message: fmt.Sprintf(format, args...)})
	}

	// One raw property occurrence, before its definition is known.
	type occurrence struct {
		elementID, elementType, defRef, value string
	}
	elementNames := map[string]string{}
	var occurrences []occurrence
	defNames := map[string]string{}

	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true

	var elementID, elementType string
	var defID string
	var capture *strings.Builder
	var pending *occurrence
	depth := 0

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return set, fmt.Errorf("invalid XML: %w", err)
		}
		switch tok := token.(type) {
		case xml.Directive:
			return set, fmt.Errorf("XML directives are not allowed")
		case xml.StartElement:
			depth++
			if depth > maxXMLDepth {
				return set, fmt.Errorf("XML exceeds the maximum XML depth of %d", maxXMLDepth)
			}
			switch tok.Name.Local {
			case "element":
				elementID = attribute(tok, "identifier")
				elementType = localType(attribute(tok, "type"))
			case "propertyDefinition":
				defID = attribute(tok, "identifier")
			case "property":
				pending = &occurrence{
					elementID: elementID, elementType: elementType,
					defRef: attribute(tok, "propertyDefinitionRef"),
				}
			case "name", "value":
				capture = &strings.Builder{}
			}
		case xml.CharData:
			if capture != nil {
				capture.Write(tok)
			}
		case xml.EndElement:
			depth--
			switch tok.Name.Local {
			case "name":
				// The same <name> tag names the model, a property definition, an
				// element, a relationship and a view. Which one this is depends on
				// what is open around it, so both arms are guarded rather than
				// assuming the nearest ancestor.
				if capture != nil {
					text := strings.TrimSpace(capture.String())
					switch {
					case defID != "":
						defNames[defID] = text
					case elementID != "":
						elementNames[elementID] = text
					}
				}
				capture = nil
			case "value":
				if pending != nil && capture != nil {
					pending.value = strings.TrimSpace(capture.String())
				}
				capture = nil
			case "property":
				if pending != nil {
					occurrences = append(occurrences, *pending)
					pending = nil
				}
			case "propertyDefinition":
				defID = ""
			case "element":
				elementID, elementType = "", ""
			}
		}
	}

	// Second phase: now that every definition is known, decide what each occurrence
	// was. Order is preserved so a many-to-many binding reads in document order.
	index := map[string]int{}
	for _, occ := range occurrences {
		name, known := defNames[occ.defRef]
		if !known {
			add("property on %q references undeclared property definition %q", occ.elementID, occ.defRef)
			continue
		}
		if !strings.HasPrefix(name, bindingKeyPrefix) {
			continue // somebody else's property; not ours to read or report
		}
		types, allowed := allowedOn[name]
		if !allowed {
			// Deliberately does not echo the value: a rejected secret is still a
			// secret, and this message reaches logs and an API response.
			add("unknown Atlas binding key %q on %q is not part of contract version %d",
				name, occ.elementID, BindingContractVersion)
			continue
		}
		if !contains(types, occ.elementType) {
			add("binding key %q is not valid on a %s element (%q); it belongs on %s",
				name, occ.elementType, occ.elementID, strings.Join(types, " or "))
			continue
		}
		if occ.value == "" {
			add("binding key %q on %q has an empty value", name, occ.elementID)
			continue
		}
		at, seen := index[occ.elementID+"\x00"+name]
		if !seen {
			set.Bindings = append(set.Bindings, Binding{
				ElementID: occ.elementID, ElementType: occ.elementType,
				ElementName: elementNames[occ.elementID], Key: name,
			})
			at = len(set.Bindings) - 1
			index[occ.elementID+"\x00"+name] = at
		}
		set.Bindings[at].Values = append(set.Bindings[at].Values, occ.value)
	}
	return set, nil
}

// localType strips the xsi:type prefix, so "archimate:Node" and "Node" read alike.
func localType(value string) string {
	if i := strings.LastIndex(value, ":"); i >= 0 {
		return value[i+1:]
	}
	return value
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
