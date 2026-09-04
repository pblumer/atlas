package layout

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// Transplanting a diagram onto a model that already exists.
//
// Generating a layout (Ensure, Regenerate) answers "this model has no usable
// picture". This file answers the other half: a picture somebody drew by hand is
// to replace the one a stored model carries, and *nothing else about that model
// may change*. That is what lets a deployed definition's diagram be adjusted
// without redeploying it — the compiled process behind it is not recompiled, not
// re-keyed and not re-versioned, because it is not touched at all.
//
// The guarantee is structural rather than promised. Transplant keeps the stored
// document's bytes for everything outside the diagram and takes only the diagram
// interchange from the incoming one, so a semantic edit hidden in the incoming
// document cannot reach the store even in principle: those bytes are discarded.
// The digest check on top of that is a different question — "is this picture even
// of this model" — and refusing a mismatch is what keeps an operator from grafting
// one process's layout onto another's shapes.

// Errors Transplant returns. They are distinguished because the HTTP surface maps
// them to different statuses: a model whose meaning differs is the caller's
// mistake to fix (409), a body carrying no diagram is a malformed request (400).
var (
	// ErrNoDiagram means the incoming document carries no diagram interchange to
	// take — an empty <BPMNDiagram> included, since a plane with no shapes on it
	// would replace the stored picture with a blank canvas.
	ErrNoDiagram = errors.New("the model carries no diagram interchange")
	// ErrDifferentModel means the two documents do not describe the same process:
	// their semantic halves differ in some element, attribute or text. The diagram
	// is not transplanted, because a layout only means anything against the shapes
	// it was drawn for.
	ErrDifferentModel = errors.New("the model's semantic content differs from the stored one")
)

// Transplant returns the stored document with the incoming document's diagram
// interchange in place of its own. Only the picture moves: every byte of stored
// outside its <BPMNDiagram> blocks comes back unchanged, so the model the
// compiler already turned into a CompiledProcess is bit-for-bit the model it
// still holds — script bodies, documentation text, formatting and all.
//
// It refuses with ErrDifferentModel unless the two documents' semantic halves
// are equal as XML (see semanticDigest), and with ErrNoDiagram if the incoming
// document has no shapes to give.
//
// The incoming diagram is made self-contained on the way over: the namespace
// prefixes it uses are re-declared on the <BPMNDiagram> element itself, from the
// declarations its own document bound them to. Without that, a diagram written
// with prefixes the stored document's root does not declare would splice in as
// well-formed nonsense.
func Transplant(stored, incoming []byte) ([]byte, error) {
	storedDigest, err := semanticDigest(stored)
	if err != nil {
		return nil, fmt.Errorf("read the stored model: %w", err)
	}
	incomingDigest, err := semanticDigest(incoming)
	if err != nil {
		return nil, fmt.Errorf("read the submitted model: %w", err)
	}
	if storedDigest != incomingDigest {
		return nil, ErrDifferentModel
	}
	di := diagramOf(incoming)
	if di == "" {
		return nil, ErrNoDiagram
	}
	return injectBeforeDefinitionsClose(stripDiagram(stored), di), nil
}

// SameModel reports whether two BPMN documents describe the same process — equal
// everywhere the engine looks, whatever their diagrams say. It is the question
// Transplant asks before it moves anything, exposed on its own so a caller can
// tell "you changed the model" from "the model won't parse" before offering to
// save a layout.
func SameModel(a, b []byte) (bool, error) {
	da, err := semanticDigest(a)
	if err != nil {
		return false, err
	}
	db, err := semanticDigest(b)
	if err != nil {
		return false, err
	}
	return da == db, nil
}

// semanticDigest fingerprints everything in a BPMN document except its diagram:
// element names resolved to their namespaces, their attributes sorted so document
// order cannot change the answer, and the text between them with surrounding
// whitespace removed.
//
// Three deliberate omissions, each because the transplant does not carry the
// thing across:
//
//   - The <BPMNDiagram> blocks, stripped before the walk. They are the half being
//     replaced; comparing them would refuse every adjustment.
//   - The root <definitions> element's own attributes (its id, targetNamespace,
//     and whatever exporter stamped it). The result keeps the stored root, so an
//     editor that re-stamps those on export has changed nothing that lands.
//   - Whitespace between elements, and comments. A round-trip through an editor
//     reformats a document freely; since the transplant keeps the stored bytes,
//     indentation inside a script task survives regardless of what the digest
//     forgives.
//
// So this is not a proof that two documents mean the same thing to the compiler —
// the transplant provides that by construction. It is the narrower check the
// transplant still needs: that the incoming picture was drawn for these shapes.
func semanticDigest(src []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(stripDiagram(src)))
	h := sha256.New()
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			// The root's attributes are the document's own metadata, not the model's.
			if depth == 0 {
				fmt.Fprintf(h, "<%s|%s\n", t.Name.Space, t.Name.Local)
			} else {
				fmt.Fprintf(h, "<%s|%s %s\n", t.Name.Space, t.Name.Local, canonicalAttrs(t.Attr))
			}
			depth++
		case xml.EndElement:
			depth--
			fmt.Fprintf(h, ">%s|%s\n", t.Name.Space, t.Name.Local)
		case xml.CharData:
			if s := strings.TrimSpace(string(t)); s != "" {
				fmt.Fprintf(h, "=%s\n", s)
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// canonicalAttrs renders an element's attributes in a fixed order, dropping the
// namespace declarations. A prefix rename is a spelling change: encoding/xml has
// already resolved every name to its namespace URI by the time we see it, so the
// declarations themselves carry no meaning the walk has not already recorded.
func canonicalAttrs(attrs []xml.Attr) string {
	kept := make([]string, 0, len(attrs))
	for _, a := range attrs {
		if a.Name.Local == "xmlns" || a.Name.Space == "xmlns" {
			continue
		}
		kept = append(kept, a.Name.Space+"|"+a.Name.Local+"="+a.Value)
	}
	sort.Strings(kept)
	return strings.Join(kept, " ")
}

// definitionsOpen matches a BPMN document's root start tag, where the namespace
// prefixes its diagram uses are declared. Like the package's other patterns it
// stops at the first '>', which is sound because a root attribute value holding
// one — a targetNamespace URI, an exporter name — does not occur.
var definitionsOpen = regexp.MustCompile(`(?is)<\s*([a-z0-9_.]+:)?definitions\b[^>]*>`)

// xmlnsDecl matches one prefixed namespace declaration in a start tag.
var xmlnsDecl = regexp.MustCompile(`xmlns:([A-Za-z_][\w.-]*)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// elemPrefix and attrPrefix find the namespace prefixes a fragment of XML uses —
// on its elements and on its attributes respectively. Attribute *values* are not
// scanned: a URI in one holds a colon and no prefix.
var (
	elemPrefix = regexp.MustCompile(`</?\s*([A-Za-z_][\w.-]*):`)
	attrPrefix = regexp.MustCompile(`[\s]([A-Za-z_][\w.-]*):[\w.-]+\s*=`)
)

// diagramOf extracts a document's diagram interchange as a self-contained
// fragment: the <BPMNDiagram> blocks it carries, each with the namespace
// declarations its contents need re-declared on it.
//
// It returns "" when there is nothing worth transplanting — no block, or blocks
// with no shape or edge in them, which as a replacement would blank the diagram
// rather than adjust it.
func diagramOf(src []byte) string {
	blocks := bpmnDiagramBlock.FindAll(src, -1)
	if len(blocks) == 0 {
		return ""
	}
	declared := map[string]string{}
	if root := definitionsOpen.Find(src); root != nil {
		for _, m := range xmlnsDecl.FindAllSubmatch(root, -1) {
			uri := string(m[2])
			if uri == "" {
				uri = string(m[3])
			}
			declared[string(m[1])] = uri
		}
	}
	var out strings.Builder
	drew := false
	for _, block := range blocks {
		if bytes.Contains(block, []byte("BPMNShape")) || bytes.Contains(block, []byte("BPMNEdge")) {
			drew = true
		}
		out.WriteString("\n  ")
		out.WriteString(selfContained(string(block), declared))
	}
	out.WriteString("\n")
	if !drew {
		return ""
	}
	return out.String()
}

// selfContained re-declares on a block's own start tag every namespace prefix it
// uses and does not itself declare, reading the binding from the declarations of
// the document the block came out of. Re-declaring a prefix to the URI it already
// has is valid XML and harmless, so no attempt is made to work out whether the
// destination would have bound it anyway — the fragment simply stops depending on
// the answer.
func selfContained(block string, declared map[string]string) string {
	used := map[string]bool{}
	for _, m := range elemPrefix.FindAllStringSubmatch(block, -1) {
		used[m[1]] = true
	}
	for _, m := range attrPrefix.FindAllStringSubmatch(block, -1) {
		if m[1] != "xmlns" {
			used[m[1]] = true
		}
	}
	// A prefix the block declares for itself is already carried by its own text.
	for _, m := range xmlnsDecl.FindAllStringSubmatch(block, -1) {
		delete(used, m[1])
	}
	add := make([]string, 0, len(used))
	for prefix := range used {
		if uri, ok := declared[prefix]; ok {
			add = append(add, fmt.Sprintf(` xmlns:%s="%s"`, prefix, attr(uri)))
		}
	}
	if len(add) == 0 {
		return block
	}
	sort.Strings(add)
	at := startTagNameEnd(block)
	return block[:at] + strings.Join(add, "") + block[at:]
}

// startTagNameEnd returns the offset just past the element name in a start tag,
// which is where an attribute may be inserted.
func startTagNameEnd(block string) int {
	for i := 1; i < len(block); i++ {
		switch block[i] {
		case ' ', '\t', '\r', '\n', '/', '>':
			return i
		}
	}
	return len(block)
}
