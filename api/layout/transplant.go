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
		// Name what differs. A refusal that only says "the model differs" leaves the
		// caller to diff two documents by eye, which is exactly the position somebody
		// is in when they believe they changed nothing — and they are sometimes right,
		// because an editor round-trip is not a byte-for-byte round-trip.
		return nil, fmt.Errorf("%w (first difference: %s)", ErrDifferentModel, describeDifference(stored, incoming))
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

// step is one token of the canonical walk, kept in a form a human can be pointed at.
type step struct {
	key   string // what the digest compares
	label string // how to name it in a refusal
}

// canonicalSteps is semanticDigest's walk, retained rather than hashed, so the two
// streams can be lined up and the first divergence named. Only the refusal path
// calls it — the digest itself stays a single pass that keeps nothing.
func canonicalSteps(src []byte) ([]step, error) {
	dec := xml.NewDecoder(bytes.NewReader(stripDiagram(src)))
	var out []step
	var open []string // enclosing elements, as labels
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			label := t.Name.Local
			for _, a := range t.Attr {
				if a.Name.Space == "" && a.Name.Local == "id" {
					label += ` id="` + a.Value + `"`
					break
				}
			}
			key := "<" + t.Name.Space + "|" + t.Name.Local
			if depth > 0 {
				key += " " + canonicalAttrs(t.Attr)
			}
			out = append(out, step{key: key, label: "<" + label + ">"})
			open = append(open, label)
			depth++
		case xml.EndElement:
			depth--
			if len(open) > 0 {
				open = open[:len(open)-1]
			}
			out = append(out, step{key: ">" + t.Name.Space + "|" + t.Name.Local, label: "the end of <" + t.Name.Local + ">"})
		case xml.CharData:
			if s := strings.TrimSpace(string(t)); s != "" {
				where := "text"
				if len(open) > 0 {
					where = "the text in <" + open[len(open)-1] + ">"
				}
				out = append(out, step{key: "=" + s, label: where})
			}
		}
	}
	return out, nil
}

// describeDifference names the first place two documents' semantic halves diverge,
// for a refusal message. Best-effort by construction: it runs only once the digests
// have already disagreed, so "somewhere" is still a true answer when the streams
// cannot be lined up.
func describeDifference(stored, incoming []byte) string {
	a, err := canonicalSteps(stored)
	if err != nil {
		return "the stored model could not be walked"
	}
	b, err := canonicalSteps(incoming)
	if err != nil {
		return "the submitted model could not be walked"
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i].key == b[i].key {
			continue
		}
		if a[i].label == b[i].label {
			// Same element, different content: an attribute changed.
			return a[i].label + " differs"
		}
		return "at " + a[i].label + ", where the submitted model has " + b[i].label
	}
	switch {
	case len(a) > len(b):
		return "the submitted model stops at " + b[len(b)-1].label + " and the deployed one continues"
	case len(b) > len(a):
		return "the submitted model continues past " + a[len(a)-1].label
	}
	return "somewhere the two documents disagree"
}

// bpmnDefaults are the BPMN attributes the schema gives a default value, keyed by
// name. Writing one at its default and leaving it out are the same statement, so
// the digest has to read them as the same statement too.
//
// This is not a nicety: bpmn-js omits exactly these when their value equals the
// default, so a model that spells `cancelActivity="true"` out — which the BPMN
// examples do, and which every interrupting boundary event in a hand-written model
// carries — comes back from the editor without it. Compared literally, that reads as
// a changed model and the layout save is refused, on a document nobody edited. It is
// the failure this table exists to stop.
//
// The list is the set the bundled moddle declares a default for (every property
// object carrying `default` and `isAttr`), which is precisely the set an editor may
// drop. Each name has one default across every type that declares it, so keying by
// name alone is unambiguous.
//
// It applies to unprefixed attributes only — the BPMN ones. An extension attribute
// (zeebe:, atlas:) that happens to share a name is a different attribute with
// different rules, and the walk keys those by namespace, so they never reach here.
//
// The seven Atlas reads are checked against the compiler rather than assumed:
// cancelActivity, isInterrupting and cancelRemainingInstances are read as
// `!= "false"` (scope_compile.go), triggeredByEvent, isSequential and testBefore as
// `== "true"` (parse.go, scope_compile.go), and isCollection as a bool attribute
// whose absence is false. All seven agree with the schema. The rest the compiler does
// not read at all, so normalising them can change nothing it sees.
var bpmnDefaults = map[string]string{
	"cancelActivity":           "true",
	"cancelRemainingInstances": "true",
	"isInterrupting":           "true",
	"isUnlimited":              "true",
	"instantiate":              "false",
	"isCollection":             "false",
	"isForCompensation":        "false",
	"isReference":              "false",
	"isSequential":             "false",
	"mustUnderstand":           "false",
	"testBefore":               "false",
	"triggeredByEvent":         "false",
	"completionQuantity":       "1",
	"startQuantity":            "1",
	"maximum":                  "1",
	"minimum":                  "0",
	"expressionLanguage":       "http://www.w3.org/1999/XPath",
	"typeLanguage":             "http://www.w3.org/2001/XMLSchema",
	"textFormat":               "text/plain",
}

// canonicalAttrs renders an element's attributes in a fixed order, dropping the
// namespace declarations and any attribute sitting on its schema default. A prefix
// rename is a spelling change: encoding/xml has already resolved every name to its
// namespace URI by the time we see it, so the declarations themselves carry no
// meaning the walk has not already recorded — and an attribute at its default says
// what its absence says.
func canonicalAttrs(attrs []xml.Attr) string {
	kept := make([]string, 0, len(attrs))
	for _, a := range attrs {
		if a.Name.Local == "xmlns" || a.Name.Space == "xmlns" {
			continue
		}
		if a.Name.Space == "" && bpmnDefaults[a.Name.Local] == a.Value {
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
