package panorama

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Adding an element or a relationship to a document (ADR-0189 §2, P2b).
//
// Same discipline as the two writers beside it: splice, never re-serialise. What is
// new here is that this *inserts* rather than replacing a value, which brings two
// problems the geometry writer did not have.
//
// **The document's own namespace prefix.** A model written as `<am:element>` must
// gain an `<am:element>`, not an `<element>` — the second would be in no namespace
// and would not be the same document. So the prefix is read off the block being
// inserted into rather than assumed.
//
// **The document's own indentation.** An insert lands in the middle of somebody's
// formatting, and a line that does not match the ones around it is a diff nobody
// asked for. The whitespace before the closing tag is reused as the new line's
// indent, which is what an author would have typed.

// maxMintAttempts bounds the search for an unused identifier. A document that
// already holds this many colliding ids is not one to keep guessing at.
const maxMintAttempts = 10000

// NewElement is one element to add, and where it goes on a view.
type NewElement struct {
	// Type is an ArchiMate element type from the authoring subset. Anything else is
	// refused: this writer will not create what the canvas cannot draw rules for.
	Type string `json:"type"`
	Name string `json:"name"`
	// ViewID is the view the new shape is placed on, and the geometry is where.
	ViewID string `json:"viewId"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	W      int    `json:"w"`
	H      int    `json:"h"`
}

// NewRelationship is one relationship to add, and the view to draw it on.
type NewRelationship struct {
	Type   string `json:"type"`
	Source string `json:"source"`
	Target string `json:"target"`
	ViewID string `json:"viewId"`
}

// AddElement writes a new element into the model and places it on a view.
//
// Both halves happen or neither does. An element with no shape is invisible in the
// editor that just created it, and a shape referencing an element that is not there
// is what the parser reports as a broken view — either alone is a worse document
// than the one we started with.
func AddElement(data []byte, add NewElement) ([]byte, string, error) {
	kind, known := kindByType[add.Type]
	if !known {
		return nil, "", fmt.Errorf("panorama: Atlas does not author %s elements; "+
			"the subset is %s", add.Type, subsetSummary())
	}
	if strings.TrimSpace(add.Name) == "" {
		return nil, "", fmt.Errorf("panorama: a new %s needs a name", kind.Label)
	}
	if add.W < 1 || add.H < 1 {
		return nil, "", fmt.Errorf("panorama: a new %s needs a positive width and height", kind.Label)
	}
	index, err := indexContent(data)
	if err != nil {
		return nil, "", err
	}
	view, found := index.views[add.ViewID]
	if !found {
		return nil, "", fmt.Errorf("panorama: the document has no view %q", add.ViewID)
	}
	if index.elementsClose < 0 {
		return nil, "", fmt.Errorf("panorama: the document has no <elements> block to add to")
	}

	minted, err := mintIDs(index.taken, "element", "node")
	if err != nil {
		return nil, "", err
	}
	elementID, nodeID := minted[0], minted[1]

	element := fmt.Sprintf("%s<%selement identifier=%q xsi:type=%q><%sname>%s</%sname></%selement>",
		index.elementsIndent, index.prefix, elementID, add.Type,
		index.prefix, escapeXML(add.Name), index.prefix, index.prefix)
	node := fmt.Sprintf("%s<%snode identifier=%q elementRef=%q x=\"%d\" y=\"%d\" w=\"%d\" h=\"%d\"/>",
		view.indent, index.prefix, nodeID, elementID, add.X, add.Y, add.W, add.H)

	return applyEdits(data, []edit{
		{from: index.elementsClose, to: index.elementsClose, text: element},
		{from: view.close, to: view.close, text: node},
	}), nodeID, nil
}

// AddRelationship writes a new relationship into the model and draws it on a view.
//
// The subset decides whether it may exist at all, through the same function the
// canvas asked before it let the arrow be drawn — so a connection the canvas
// offered is one this accepts, and a connection it refused never gets here.
func AddRelationship(data []byte, add NewRelationship) ([]byte, string, error) {
	index, err := indexContent(data)
	if err != nil {
		return nil, "", err
	}
	// Checked before the ends are looked up, because it is the more useful answer:
	// a document with no <elements> block has no elements either, and "there is
	// nowhere to put a relationship" says what is wrong with the document rather
	// than blaming the caller for naming an element that could not have existed.
	if index.relationshipsClose < 0 && index.elementsBlockEnd < 0 {
		return nil, "", fmt.Errorf("panorama: the document has no <elements> block, " +
			"so there is nowhere to put a relationship")
	}
	sourceType, sourceKnown := index.elementTypes[add.Source]
	targetType, targetKnown := index.elementTypes[add.Target]
	switch {
	case !sourceKnown:
		return nil, "", fmt.Errorf("panorama: the document has no element %q", add.Source)
	case !targetKnown:
		return nil, "", fmt.Errorf("panorama: the document has no element %q", add.Target)
	}
	if ok, refusal := MayConnectElements(add.Type, add.Source, sourceType, add.Target, targetType); !ok {
		return nil, "", fmt.Errorf("panorama: %s", refusal.Message)
	}
	view, found := index.views[add.ViewID]
	if !found {
		return nil, "", fmt.Errorf("panorama: the document has no view %q", add.ViewID)
	}
	// The two ends have to be *on this view*, not merely in the model. A connection
	// between shapes that are not drawn here is a line from nowhere to nowhere.
	sourceNode, drawnSource := view.nodeFor[add.Source]
	targetNode, drawnTarget := view.nodeFor[add.Target]
	switch {
	case !drawnSource:
		return nil, "", fmt.Errorf("panorama: element %q is not on this view", add.Source)
	case !drawnTarget:
		return nil, "", fmt.Errorf("panorama: element %q is not on this view", add.Target)
	}
	// A model with no relationships yet has no block to put one in. Refusing would
	// mean nobody could ever draw their first relationship, so the block is created
	// where the schema puts it — immediately after <elements>, which is the sequence
	// the exchange format requires.
	insertAt, indent, wrap := index.relationshipsClose, index.relationshipsIndent, false
	if insertAt < 0 {
		insertAt, indent, wrap = index.elementsBlockEnd, index.elementsIndent, true
	}

	minted, err := mintIDs(index.taken, "rel", "conn")
	if err != nil {
		return nil, "", err
	}
	relationshipID, connectionID := minted[0], minted[1]

	relationship := fmt.Sprintf("%s<%srelationship identifier=%q source=%q target=%q xsi:type=%q/>",
		indent, index.prefix, relationshipID, add.Source, add.Target, add.Type)
	if wrap {
		// The whole block, indented like <elements> was, with the relationship inside.
		blockIndent := indentBefore(data, index.elementsBlockEnd)
		relationship = fmt.Sprintf("%s<%srelationships>%s%s</%srelationships>",
			blockIndent, index.prefix, relationship, blockIndent, index.prefix)
	}
	connection := fmt.Sprintf("%s<%sconnection identifier=%q relationshipRef=%q source=%q target=%q/>",
		view.indent, index.prefix, connectionID, relationshipID, sourceNode, targetNode)

	return applyEdits(data, []edit{
		{from: insertAt, to: insertAt, text: relationship},
		{from: view.close, to: view.close, text: connection},
	}), relationshipID, nil
}

// subsetSummary names what may be created, for a refusal that tells somebody what
// they can do rather than only what they cannot.
func subsetSummary() string {
	names := make([]string, 0, len(authorable))
	for _, kind := range authorable {
		names = append(names, kind.Type)
	}
	return strings.Join(names, ", ")
}

// viewSpan is one view: where its content ends, how its children are indented, and
// which node draws each element on it.
type viewSpan struct {
	close   int
	indent  string
	nodeFor map[string]string
}

// contentIndex is what one pass over the document finds for an insert.
type contentIndex struct {
	// prefix is the namespace prefix the document writes its ArchiMate elements
	// with, including the colon, or empty for a default namespace.
	prefix string
	// taken is every identifier already in use, so a minted one cannot collide.
	taken        map[string]bool
	elementTypes map[string]string
	views        map[string]*viewSpan

	elementsClose  int
	elementsIndent string
	// elementsBlockEnd is just past </elements>, where a <relationships> block goes
	// when the document has none — the position the exchange schema's sequence puts
	// it in.
	elementsBlockEnd    int
	relationshipsClose  int
	relationshipsIndent string
}

// indexContent walks the document once and records where an insert goes.
func indexContent(data []byte) (contentIndex, error) {
	index := contentIndex{
		taken: map[string]bool{}, elementTypes: map[string]string{},
		views: map[string]*viewSpan{}, elementsClose: -1, relationshipsClose: -1,
		elementsBlockEnd: -1,
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth, inViews := 0, 0
	var currentView *viewSpan
	var currentElement string
	// lastOpen remembers the most recent start tag's offsets per depth, so the
	// indentation before a closing tag can be measured.
	prev := 0

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return index, fmt.Errorf("panorama: read document: %w", err)
		}
		start, end := prev, int(decoder.InputOffset())
		prev = end

		switch tok := token.(type) {
		case xml.StartElement:
			depth++
			if id := attrValue(tok, "identifier"); id != "" {
				index.taken[id] = true
			}
			if depth == 1 {
				// The root element is where the prefix is read from. Every ArchiMate
				// element in the document shares it, and the root is the one tag
				// guaranteed to be there — reading it off <elements> would leave a
				// document with an empty block, or none, with no prefix at all.
				index.prefix = prefixAt(data, tokenStart(data, start, end))
			}
			switch tok.Name.Local {
			case "views":
				inViews = depth
			case "element":
				if inViews == 0 {
					currentElement = attrValue(tok, "identifier")
					index.elementTypes[currentElement] = localType(attrValue(tok, "type"))
					if index.elementsIndent == "" {
						index.elementsIndent = indentBefore(data, tokenStart(data, start, end))
					}
				}
			case "relationship":
				if inViews == 0 && index.relationshipsIndent == "" {
					index.relationshipsIndent = indentBefore(data, tokenStart(data, start, end))
				}
			case "view":
				if inViews > 0 {
					currentView = &viewSpan{close: -1, nodeFor: map[string]string{}}
					index.views[attrValue(tok, "identifier")] = currentView
				}
			case "node":
				if currentView != nil {
					if ref := attrValue(tok, "elementRef"); ref != "" {
						currentView.nodeFor[ref] = attrValue(tok, "identifier")
					}
					if currentView.indent == "" {
						currentView.indent = indentBefore(data, tokenStart(data, start, end))
					}
				}
			}
		case xml.EndElement:
			at := tokenStart(data, start, end)
			switch tok.Name.Local {
			case "elements":
				index.elementsClose = insertBefore(data, at)
				index.elementsBlockEnd = end
				if index.elementsIndent == "" {
					index.elementsIndent = indentBefore(data, at) + "  "
				}
			case "relationships":
				index.relationshipsClose = insertBefore(data, at)
				if index.relationshipsIndent == "" {
					index.relationshipsIndent = indentBefore(data, at) + "  "
				}
			case "view":
				if currentView != nil {
					currentView.close = insertBefore(data, at)
					if currentView.indent == "" {
						// A view with no shapes yet: indent its first child one step in from
						// the view's own closing tag, which is what an author would type.
						currentView.indent = indentBefore(data, at) + "  "
					}
					currentView = nil
				}
			case "views":
				inViews = 0
			}
			depth--
		}
	}
	return index, nil
}

// prefixAt reads the namespace prefix off the tag beginning at an offset,
// including the colon, for both `<am:elements>` and `</am:elements>`.
//
// It reads the raw bytes rather than asking the decoder, and that is not laziness:
// encoding/xml resolves a prefix to its namespace *URI* and does not report the
// prefix at all. Taking Name.Space for a prefix yields "http:" out of
// "http://www.opengroup.org/...", which is exactly what a first attempt at this
// wrote into the document.
func prefixAt(data []byte, at int) string {
	rest := data[at:]
	if len(rest) < 2 || rest[0] != '<' {
		return ""
	}
	rest = rest[1:]
	if rest[0] == '/' {
		rest = rest[1:]
	}
	end := bytes.IndexAny(rest, " \t\r\n/>")
	if end < 0 {
		return ""
	}
	name := string(rest[:end])
	if colon := strings.IndexByte(name, ':'); colon >= 0 {
		return name[:colon+1]
	}
	return ""
}

// insertBefore is where a new last child goes: at the start of the whitespace run
// that indents the closing tag, so the new line lands between the previous child
// and that tag rather than between the tag's indentation and the tag itself.
func insertBefore(data []byte, close int) int {
	return close - len(indentBefore(data, close))
}

// indentBefore is the whitespace run ending at an offset, including the newline
// that starts it. Reusing it is what makes an inserted line look like the ones
// around it instead of like a diff somebody will reformat.
func indentBefore(data []byte, at int) string {
	i := at
	for i > 0 && (data[i-1] == ' ' || data[i-1] == '\t') {
		i--
	}
	if i == 0 || data[i-1] != '\n' {
		// Not on its own line — the block is written inline, so match it by adding
		// nothing rather than by inventing a line break the author did not have.
		return ""
	}
	return string(data[i-1 : at])
}

// mintID produces an identifier no other element in the document uses, and records
// it so two inserts in one call cannot collide with each other.
//
// Past the bound it fails rather than guessing. An earlier version fell back to a
// derived number and called it collision-free by construction, which was simply
// untrue — nothing stopped that number being taken as well. A duplicate identifier
// is a document that no longer says what it meant, and it would be discovered long
// after the save that caused it, so "I could not mint one" is the only honest
// answer left at that point.
// mintIDs mints several at once, so a caller that needs two has one failure to
// handle rather than two identical ones.
func mintIDs(taken map[string]bool, prefixes ...string) ([]string, error) {
	minted := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		id, err := mintID(taken, prefix)
		if err != nil {
			return nil, err
		}
		minted = append(minted, id)
	}
	return minted, nil
}

func mintID(taken map[string]bool, prefix string) (string, error) {
	for i := 1; i <= maxMintAttempts; i++ {
		candidate := prefix + "-" + strconv.Itoa(i)
		if !taken[candidate] {
			taken[candidate] = true
			return candidate, nil
		}
	}
	return "", fmt.Errorf("panorama: the document already uses %d %s identifiers; "+
		"no free one could be minted", maxMintAttempts, prefix)
}
