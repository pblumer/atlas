package mimimport

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// xnode is a namespace-agnostic view of an XOML element. XOML is open-ended —
// FIM ships stock activities and customers add their own (MIMWAL, custom WF
// activities) — so the parser cannot enumerate types up front. It captures the
// local name, every attribute, the parsed children, and the raw inner markup so
// an unrecognised activity can still be preserved verbatim (see xnode.raw).
type xnode struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	// Text is the element's own character data, without that of its children.
	// A serialised .NET collection puts a cell's value there and its key in an
	// <x:Key> child, so decoding a MIMWAL table needs the two apart.
	Text  string  `xml:",chardata"`
	Inner string  `xml:",innerxml"`
	Kids  []xnode `xml:",any"`
}

// local is the element's namespace-stripped tag name (e.g. "IfElseActivity").
func (n xnode) local() string { return n.XMLName.Local }

// attr returns the value of the first attribute whose local name matches any of
// names (case-insensitive), and whether one was found.
func (n xnode) attr(names ...string) (string, bool) {
	for _, want := range names {
		for _, a := range n.Attrs {
			if strings.EqualFold(a.Name.Local, want) {
				return a.Value, true
			}
		}
	}
	return "", false
}

// displayName picks the most human-readable label MIM/WF activities carry,
// falling back to the element's local name so a node is never nameless.
//
// ActivityDisplayName comes first because it is what the MIMWAL activity
// library writes: every MIMWAL activity carries the author's label there and a
// WF designer id (actionActivity6) in x:Name, so matching "Name" first would
// name every node after the designer id and leave the diagram unreadable.
func (n xnode) displayName() string {
	if v, ok := n.attr("ActivityDisplayName", "DisplayName", "Description", "Title", "Name"); ok {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return n.local()
}

// parseXOML reads an XOML document (or a FIMAutomation export that embeds one)
// and returns its root activity element. When the input is a wrapper rather than
// the workflow itself, the embedded XOML is located and re-parsed. Any repair
// the input needed before it would parse is returned as a warning, so a
// conversion never silently rests on a rewritten document.
func parseInput(r io.Reader) ([]workflowInput, []string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	var warnings []string
	root, repaired, err := decodeNode(data)
	if err != nil {
		return nil, nil, err
	}
	if repaired {
		warnings = append(warnings, warnUnquoted)
	}
	if isWorkflowRoot(root) {
		return []workflowInput{{root: root}}, warnings, nil
	}

	// Not a workflow root: an Export-FIMConfig resource graph, which carries one
	// WorkflowDefinition per workflow and the XOML as an attribute on each. Taking
	// only the first is how an export of a whole MIM installation used to arrive as
	// a single process, the rest gone without a word.
	found := exportedWorkflows(root)
	var (
		out      []workflowInput
		firstErr error
	)
	for _, w := range found {
		inner, innerRepaired, err := decodeNode([]byte(w.xoml))
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("embedded XOML did not parse: %w", err)
			}
			warnings = append(warnings, "a WorkflowDefinition was skipped because its XOML did not parse: "+
				sourceLabel(w.source))
			continue
		}
		if innerRepaired && !repaired {
			repaired = true
			warnings = append(warnings, warnUnquoted)
		}
		out = append(out, workflowInput{root: inner, source: w.source})
	}
	switch {
	case len(out) > 0:
		return out, warnings, nil
	case firstErr != nil:
		return nil, nil, firstErr
	}
	// Fall back to treating whatever we decoded as the workflow body; the caller
	// still produces a (mostly manual-review) process rather than failing outright.
	return []workflowInput{{root: root}}, warnings, nil
}

// workflowInput is one workflow to convert: its root activity and, when it came
// from an export wrapper, the resource that carried it.
type workflowInput struct {
	root   xnode
	source SourceInfo
}

// sourceLabel names a resource for a message, falling back to its id.
func sourceLabel(s SourceInfo) string {
	switch {
	case s.DisplayName != "":
		return s.DisplayName
	case s.ObjectID != "":
		return s.ObjectID
	default:
		return "unnamed"
	}
}

// warnUnquoted is reported when the input only parsed after quoteAttrValues
// rewrote it, so a reviewer knows the source document was not well-formed. Like
// every Note detail it is English: the server has no idea who is reading it
// (ADR-0267), and the console decides how to present it.
const warnUnquoted = "input was not well-formed XML: unquoted attribute values were quoted before parsing"

// decodeNode parses one XOML document. MIM does not always emit well-formed
// XML — it writes the xmlns declarations of a workflow root without quotes
// around the value — and Go's decoder rejects that outright even with
// Strict=false, which only relaxes namespace handling. So a failed parse is
// retried once against a quoted copy; repaired reports whether that was needed.
func decodeNode(data []byte) (n xnode, repaired bool, err error) {
	n, err = decodeStrict(data)
	if err == nil {
		return n, false, nil
	}
	fixed, changed := quoteAttrValues(data)
	if !changed {
		return xnode{}, false, err
	}
	n, retryErr := decodeStrict(fixed)
	if retryErr != nil {
		return xnode{}, false, err // report the original failure, not the repair's
	}
	return n, true, nil
}

func decodeStrict(data []byte) (xnode, error) {
	var n xnode
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false // XOML in the wild is not always namespace-clean
	if err := dec.Decode(&n); err != nil {
		return xnode{}, err
	}
	if n.XMLName.Local == "" {
		return xnode{}, fmt.Errorf("input is not XML")
	}
	return n, nil
}

// quoteAttrValues puts quotes around attribute values that were serialised
// without them, and reports whether it changed anything. It scans tags rather
// than using a regexp so that an "=" inside an already-quoted value, inside
// element text, or inside a comment or CDATA section is left alone.
func quoteAttrValues(data []byte) ([]byte, bool) {
	out := make([]byte, 0, len(data)+64)
	changed := false
	for i := 0; i < len(data); {
		if data[i] != '<' {
			out = append(out, data[i])
			i++
			continue
		}
		// Comments, CDATA sections and processing instructions carry no
		// attributes: copy them through verbatim.
		if end, ok := skipVerbatim(data, i); ok {
			out = append(out, data[i:end]...)
			i = end
			continue
		}
		end, fixedTag, tagChanged := quoteTagAttrValues(data, i)
		out = append(out, fixedTag...)
		changed = changed || tagChanged
		i = end
	}
	return out, changed
}

// skipVerbatim reports the end offset of a comment, CDATA section or processing
// instruction starting at i, and whether data[i:] begins with one.
func skipVerbatim(data []byte, i int) (int, bool) {
	for _, m := range []struct{ open, close string }{
		{"<!--", "-->"},
		{"<![CDATA[", "]]>"},
		{"<?", "?>"},
		{"<!", ">"}, // doctype and other declarations
	} {
		if !bytes.HasPrefix(data[i:], []byte(m.open)) {
			continue
		}
		if j := bytes.Index(data[i+len(m.open):], []byte(m.close)); j >= 0 {
			return i + len(m.open) + j + len(m.close), true
		}
		return len(data), true
	}
	return 0, false
}

// quoteTagAttrValues rewrites one tag starting at i (data[i] == '<'), returning
// the offset just past it, the tag's markup with every attribute value quoted,
// and whether a value had to be quoted.
func quoteTagAttrValues(data []byte, i int) (int, []byte, bool) {
	out := []byte{data[i]}
	changed := false
	for i++; i < len(data); {
		switch c := data[i]; {
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(data) && data[j] != c {
				j++
			}
			if j < len(data) {
				j++ // the closing quote
			}
			out = append(out, data[i:j]...)
			i = j
		case c == '>':
			return i + 1, append(out, c), changed
		case c == '=':
			out = append(out, c)
			i++
			for i < len(data) && isXMLSpace(data[i]) {
				out = append(out, data[i])
				i++
			}
			if i >= len(data) || data[i] == '"' || data[i] == '\'' {
				continue // already quoted (or truncated input): nothing to repair
			}
			start := i
			for i < len(data) && !isXMLSpace(data[i]) && data[i] != '>' &&
				!(data[i] == '/' && i+1 < len(data) && data[i+1] == '>') {
				i++
			}
			out = append(out, quoteValue(data[start:i])...)
			changed = true
		default:
			out = append(out, c)
			i++
		}
	}
	return len(data), out, changed
}

// quoteValue wraps a bare attribute value in whichever quote character it does
// not itself contain, escaping as a last resort.
func quoteValue(v []byte) []byte {
	switch {
	case !bytes.ContainsRune(v, '"'):
		return []byte(`"` + string(v) + `"`)
	case !bytes.ContainsRune(v, '\''):
		return []byte("'" + string(v) + "'")
	default:
		return []byte(`"` + strings.ReplaceAll(string(v), `"`, "&quot;") + `"`)
	}
}

func isXMLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// isWorkflowRoot reports whether n looks like a WF workflow definition rather
// than an export wrapper: a *Workflow* element, or any element that already is
// (or contains) recognisable activities.
func isWorkflowRoot(n xnode) bool {
	l := strings.ToLower(n.local())
	if strings.Contains(l, "workflow") {
		return true
	}
	if strings.HasSuffix(l, "activity") {
		return true
	}
	for _, k := range n.Kids {
		kl := strings.ToLower(k.local())
		if strings.HasSuffix(kl, "activity") || strings.Contains(kl, "workflow") {
			return true
		}
	}
	return false
}

// valueText returns the payload of a FIM attribute element: the text of its
// <Value> child if present, otherwise the element's own inner content.
func valueText(n xnode) string {
	for _, k := range n.Kids {
		if strings.EqualFold(k.local(), "Value") {
			if s := strings.TrimSpace(k.Inner); s != "" {
				return s
			}
		}
	}
	return strings.TrimSpace(n.Inner)
}

func cleanEmbedded(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<![CDATA[") && strings.HasSuffix(s, "]]>") {
		return strings.TrimSuffix(strings.TrimPrefix(s, "<![CDATA["), "]]>")
	}
	if strings.Contains(s, "&lt;") && !strings.Contains(s, "<") {
		s = xmlUnescape(s)
	}
	return s
}

func xmlUnescape(s string) string {
	r := strings.NewReplacer(
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&apos;", "'",
		"&amp;", "&",
	)
	return r.Replace(s)
}
