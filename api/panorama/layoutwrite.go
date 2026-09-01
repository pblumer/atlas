package panorama

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
)

// Moving a shape on the canvas edits the document in place, at the byte level, for
// exactly the reason writing a binding does (see bindingwrite.go).
//
// ADR-0189 §2 requires that unsupported-but-standard content round-trips without
// loss. A decode/encode cycle discards what the decoder does not model and
// normalises what it does — comments, indentation, attribute order, namespace
// prefixes, every part of the schema Panorama has no reason to know about. So this
// splices four attributes and reads nothing else.
//
// It is also why the *browser* does not write this document. The canvas knows the
// new geometry, and it would be one line there to serialise the DOM it parsed and
// PUT the result — but `XMLSerializer` normalises, so that one line would quietly
// rewrite somebody's model every time a box was nudged. The canvas therefore sends
// what it actually knows, which is a list of moved shapes, and the guarantee stays
// on the server that holds the document.

// maxLayoutChanges bounds one save. It is a person dragging boxes on a view, not a
// bulk import: a view large enough to exceed this is one the editor should not have
// opened, and a request that claims to move more than this is not a canvas.
const maxLayoutChanges = 2000

// LayoutChange is one view node's new geometry, in the document's own units.
type LayoutChange struct {
	// NodeID is the view node's `identifier`, not the element it references. A view
	// may place the same element more than once, and each placement moves on its own.
	NodeID string `json:"nodeId"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	W      int    `json:"w"`
	H      int    `json:"h"`
}

// SetLayout writes new geometry for the named view nodes and leaves every other
// byte of the document alone.
//
// A change naming a node the document does not have is refused rather than
// ignored. The canvas can only move what it drew, so an unknown id means the two
// have disagreed about what the document contains — and quietly dropping it would
// save a layout that is not the one on screen.
func SetLayout(data []byte, changes []LayoutChange) ([]byte, error) {
	if len(changes) > maxLayoutChanges {
		return nil, fmt.Errorf("layout: %d changes exceeds the bound of %d", len(changes), maxLayoutChanges)
	}
	spans, err := indexViewNodes(data)
	if err != nil {
		return nil, err
	}

	var edits []edit
	for _, change := range changes {
		span, ok := spans[change.NodeID]
		if !ok {
			return nil, fmt.Errorf("layout: the document has no view node %q", change.NodeID)
		}
		if change.W < 1 || change.H < 1 {
			// A zero-sized shape is not a shape. It would round-trip, render as nothing,
			// and look like a lost element rather than a bad save.
			return nil, fmt.Errorf("layout: view node %q needs a positive width and height", change.NodeID)
		}
		for name, value := range map[string]int{
			"x": change.X, "y": change.Y, "w": change.W, "h": change.H,
		} {
			at, ok := span.attrs[name]
			if !ok {
				return nil, fmt.Errorf("layout: view node %q has no %s attribute to write", change.NodeID, name)
			}
			edits = append(edits, edit{from: at.from, to: at.to, text: strconv.Itoa(value)})
		}
	}
	return applyEdits(data, edits), nil
}

// attrSpan is where one attribute's value sits, between its quotes.
type attrSpan struct{ from, to int }

// nodeSpan is one view node and the attributes this writer may replace.
type nodeSpan struct {
	attrs map[string]attrSpan
}

// indexViewNodes walks the document once and records, for every `<node>` inside a
// view, where its geometry attributes' values are.
//
// It uses the decoder to find the elements and then scans the raw bytes of each
// start tag for the attribute values, because the decoder reports parsed
// attributes without telling you where they were. Scanning the tag itself is what
// keeps the rest of it — order, quoting, spacing, anything else it carries —
// exactly as the author wrote it.
func indexViewNodes(data []byte) (map[string]nodeSpan, error) {
	spans := map[string]nodeSpan{}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	inViews := 0
	for {
		before := int(decoder.InputOffset())
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("layout: read document: %w", err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "views" {
				inViews = depth
			}
			// Only nodes inside a view carry geometry. An `<element>` may be named
			// `node` in some other namespace, and a diagram node is the only thing whose
			// x/y/w/h this writer owns.
			if inViews > 0 && t.Name.Local == "node" {
				id := attrValue(t, "identifier")
				if id == "" {
					break
				}
				start := tokenStart(data, before, int(decoder.InputOffset()))
				attrs := scanAttrSpans(data[start:int(decoder.InputOffset())], start)
				spans[id] = nodeSpan{attrs: attrs}
			}
		case xml.EndElement:
			if inViews == depth {
				inViews = 0
			}
			depth--
		}
	}
	return spans, nil
}

// attrValue reads one attribute off a start element, ignoring its namespace. The
// exchange format writes `identifier` unprefixed, but a document that prefixes it
// is still the same document.
func attrValue(element xml.StartElement, name string) string {
	for _, attr := range element.Attr {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}

// scanAttrSpans finds where each geometry attribute's value sits inside a start
// tag, as absolute offsets into the document.
//
// It looks only for the four names it will write. Anything else in the tag is not
// parsed, not recorded, and therefore cannot be damaged.
func scanAttrSpans(tag []byte, base int) map[string]attrSpan {
	found := map[string]attrSpan{}
	for _, name := range []string{"x", "y", "w", "h"} {
		at := findAttr(tag, name)
		if at.to > at.from {
			found[name] = attrSpan{from: base + at.from, to: base + at.to}
		}
	}
	return found
}

// findAttr locates `name="value"` in a start tag and returns where the value is.
//
// The scan requires the name to be preceded by whitespace and followed by an
// equals sign, so `w=` is not found inside `sw="2"` and an attribute named in some
// other element's text cannot match. A tag with the same attribute twice is not
// well-formed XML and the decoder would already have refused the document.
func findAttr(tag []byte, name string) attrSpan {
	for i := 1; i < len(tag); i++ {
		if !isXMLSpace(tag[i-1]) {
			continue
		}
		rest := tag[i:]
		if len(rest) < len(name)+2 || string(rest[:len(name)]) != name {
			continue
		}
		j := len(name)
		for j < len(rest) && isXMLSpace(rest[j]) {
			j++
		}
		if j >= len(rest) || rest[j] != '=' {
			continue
		}
		j++
		for j < len(rest) && isXMLSpace(rest[j]) {
			j++
		}
		if j >= len(rest) || (rest[j] != '"' && rest[j] != '\'') {
			continue
		}
		quote := rest[j]
		j++
		end := bytes.IndexByte(rest[j:], quote)
		if end < 0 {
			continue
		}
		return attrSpan{from: i + j, to: i + j + end}
	}
	return attrSpan{}
}

func isXMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
