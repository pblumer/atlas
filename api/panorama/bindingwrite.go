package panorama

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Writing a binding edits the document in place, at the byte level, rather than
// decoding it into a model and re-encoding.
//
// That is not an optimisation. ADR-0189 §2 requires that unsupported-but-standard
// content either round-trips without loss or fails with a precise diagnostic —
// Atlas must never silently discard it. A decode/encode cycle through a Go struct
// discards exactly what the struct does not model, and normalises what it does:
// comments, indentation, attribute order, namespace prefixes, and any element of
// the schema Panorama has no reason to know about. Splicing bytes keeps every one
// of them, because it never looks at them.
//
// The cost is that this file must find its insertion points itself, using the
// decoder's input offsets. That is the trade this file exists to make.

// edit is one splice: replace [from,to) with text. Applied back to front so
// earlier offsets stay valid.
type edit struct {
	from, to int
	text     string
}

// docIndex is what one pass over the document finds: where each element's body
// ends, where its properties block is, where each Atlas property sits, and where a
// definitions block is or would go.
type docIndex struct {
	elementType   map[string]string
	elementEnd    map[string]int // offset of the element's closing tag
	propsOpenEnd  map[string]int // offset just after <properties>, -1 when absent
	atlasProps    []atlasProp
	defIDByName   map[string]string
	defsInsertAt  int // just after <propertyDefinitions>, -1 when the block is absent
	defsBlockAt   int // where a whole new block goes when absent
	defsBlockNext string
}

// atlasProp is one existing <property> that names an Atlas key, with the byte range
// covering the whole element so it can be removed cleanly.
type atlasProp struct {
	elementID, key string
	from, to       int
}

// SetBinding sets one binding key on one element to the given values, replacing
// whatever that key held before. No values clears it. Properties that are not this
// key — Atlas's other keys and anybody else's properties alike — are untouched.
func SetBinding(data []byte, elementID, key string, values []string) ([]byte, error) {
	if len(data) > MaxXMLBytes {
		return nil, fmt.Errorf("XML document exceeds the %d byte limit", MaxXMLBytes)
	}
	types, allowed := allowedOn[key]
	if !allowed {
		return nil, fmt.Errorf("unknown Atlas binding key %q; contract version %d defines %s",
			key, BindingContractVersion, strings.Join(BindingKeys(), ", "))
	}
	clean := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, fmt.Errorf("binding value for %q must not be empty", key)
		}
		clean = append(clean, trimmed)
	}

	index, err := indexDocument(data)
	if err != nil {
		return nil, err
	}
	elementType, known := index.elementType[elementID]
	if !known {
		return nil, fmt.Errorf("no element %q in this model", elementID)
	}
	if !contains(types, elementType) {
		return nil, fmt.Errorf("binding key %q is not valid on a %s element; it belongs on %s",
			key, elementType, strings.Join(types, " or "))
	}

	var edits []edit

	// Remove every existing occurrence of this key on this element. Other keys and
	// foreign properties are not in this list and so are never considered.
	for _, prop := range index.atlasProps {
		if prop.elementID == elementID && prop.key == key {
			edits = append(edits, edit{from: prop.from, to: prop.to})
		}
	}

	if len(clean) > 0 {
		defID, haveDef := index.defIDByName[key]
		if !haveDef {
			defID = definitionID(key, index.defIDByName)
			definition := fmt.Sprintf("\n    <propertyDefinition identifier=%q type=\"string\"><name>%s</name></propertyDefinition>",
				defID, key)
			if index.defsInsertAt >= 0 {
				edits = append(edits, edit{from: index.defsInsertAt, to: index.defsInsertAt, text: definition})
			} else {
				edits = append(edits, edit{
					from: index.defsBlockAt, to: index.defsBlockAt,
					text: "  <propertyDefinitions>" + definition + "\n  </propertyDefinitions>\n",
				})
			}
		}

		var written strings.Builder
		for _, value := range clean {
			written.WriteString(fmt.Sprintf(
				"\n        <property propertyDefinitionRef=%q><value>%s</value></property>", defID, escapeXML(value)))
		}
		if at := index.propsOpenEnd[elementID]; at >= 0 {
			edits = append(edits, edit{from: at, to: at, text: written.String()})
		} else {
			// The schema puts properties last in an element, so a new block goes
			// immediately before the closing tag.
			at := index.elementEnd[elementID]
			edits = append(edits, edit{
				from: at, to: at,
				text: "  <properties>" + written.String() + "\n      </properties>\n    ",
			})
		}
	}
	return applyEdits(data, edits), nil
}

// applyEdits splices back to front so each edit's offsets are still valid when it
// is applied.
func applyEdits(data []byte, edits []edit) []byte {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].from > edits[j].from })
	out := append([]byte(nil), data...)
	for _, e := range edits {
		out = append(out[:e.from], append([]byte(e.text), out[e.to:]...)...)
	}
	return out
}

// definitionID mints an identifier that no existing definition uses.
func definitionID(key string, taken map[string]string) string {
	used := map[string]bool{}
	for _, id := range taken {
		used[id] = true
	}
	base := "atlas-" + strings.TrimPrefix(key, bindingKeyPrefix)
	candidate := base
	for i := 2; used[candidate]; i++ {
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return candidate
}

func escapeXML(value string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}

// indexDocument walks the document once and records the byte offsets a splice
// needs. It reads nothing it does not need and rewrites nothing at all.
func indexDocument(data []byte) (docIndex, error) {
	index := docIndex{
		elementType:  map[string]string{},
		elementEnd:   map[string]int{},
		propsOpenEnd: map[string]int{},
		defIDByName:  map[string]string{},
		defsInsertAt: -1,
		defsBlockAt:  -1,
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true

	var elementID string
	var defID, defName string
	var capture *strings.Builder
	var propFrom int
	var propDefRef string
	depth := 0
	// Offsets of the first <views> and of </model>, so a missing definitions block
	// can be inserted where the schema sequence requires.
	viewsStart, modelClose := -1, -1
	prev := 0

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return index, fmt.Errorf("invalid XML: %w", err)
		}
		start, end := prev, int(decoder.InputOffset())
		prev = end

		switch tok := token.(type) {
		case xml.Directive:
			return index, fmt.Errorf("XML directives are not allowed")
		case xml.StartElement:
			depth++
			if depth > maxXMLDepth {
				return index, fmt.Errorf("XML exceeds the maximum XML depth of %d", maxXMLDepth)
			}
			switch tok.Name.Local {
			case "element":
				elementID = attribute(tok, "identifier")
				index.elementType[elementID] = localType(attribute(tok, "type"))
				index.propsOpenEnd[elementID] = -1
			case "properties":
				if elementID != "" {
					index.propsOpenEnd[elementID] = end
				}
			case "property":
				propFrom = tokenStart(data, start, end)
				propDefRef = attribute(tok, "propertyDefinitionRef")
			case "propertyDefinitions":
				index.defsInsertAt = end
			case "propertyDefinition":
				defID = attribute(tok, "identifier")
			case "views":
				if viewsStart < 0 {
					viewsStart = tokenStart(data, start, end)
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
				if defID != "" && capture != nil {
					defName = strings.TrimSpace(capture.String())
				}
				capture = nil
			case "value":
				capture = nil
			case "property":
				index.atlasProps = append(index.atlasProps, atlasProp{
					elementID: elementID, key: "", from: propFrom, to: end,
				})
				// The key is resolved after the pass; store the reference for now.
				index.atlasProps[len(index.atlasProps)-1].key = "\x00" + propDefRef
			case "propertyDefinition":
				if defID != "" && defName != "" {
					index.defIDByName[defName] = defID
				}
				defID, defName = "", ""
			case "element":
				index.elementEnd[elementID] = tokenStart(data, start, end)
				elementID = ""
			case "model":
				modelClose = tokenStart(data, start, end)
			}
		}
	}
	// Resolve each property's definition name now that every definition is known,
	// and drop the ones that are not Atlas keys: those are somebody else's and must
	// never appear in a removal list.
	nameByID := map[string]string{}
	for name, id := range index.defIDByName {
		nameByID[id] = name
	}
	kept := index.atlasProps[:0]
	for _, prop := range index.atlasProps {
		name := nameByID[strings.TrimPrefix(prop.key, "\x00")]
		if _, isBinding := allowedOn[name]; !isBinding {
			continue
		}
		prop.key = name
		kept = append(kept, prop)
	}
	index.atlasProps = kept

	if viewsStart >= 0 {
		index.defsBlockAt = viewsStart
	} else {
		index.defsBlockAt = modelClose
	}
	return index, nil
}

// tokenStart finds where a token actually begins. The decoder reports the offset
// after a token, and the gap before it may hold whitespace, so the opening angle
// bracket is located by scanning back from the token's end.
func tokenStart(data []byte, from, to int) int {
	if to > len(data) {
		to = len(data)
	}
	if at := bytes.LastIndexByte(data[from:to], '<'); at >= 0 {
		return from + at
	}
	return from
}
