package mimimport

import (
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
	Inner   string     `xml:",innerxml"`
	Kids    []xnode    `xml:",any"`
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
func (n xnode) displayName() string {
	if v, ok := n.attr("DisplayName", "Description", "Title", "Name"); ok {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return n.local()
}

// raw re-serialises the node to a compact XML string used to preserve the
// original activity inside an <atlas:mimSource> element. It reproduces the tag,
// its attributes and its inner markup; prefix normalisation aside, the activity
// survives the round trip so no modelling information is lost.
func (n xnode) raw() string {
	var b strings.Builder
	b.WriteByte('<')
	b.WriteString(n.local())
	for _, a := range n.Attrs {
		name := a.Name.Local
		if a.Name.Space != "" {
			// Keep a readable prefix for xmlns-qualified attributes (e.g. x:Name).
			if p := lastSegment(a.Name.Space); p != "" {
				name = p + ":" + a.Name.Local
			}
		}
		fmt.Fprintf(&b, ` %s="%s"`, name, attr(a.Value))
	}
	inner := strings.TrimSpace(n.Inner)
	if inner == "" {
		b.WriteString("/>")
		return b.String()
	}
	b.WriteByte('>')
	b.WriteString(inner)
	fmt.Fprintf(&b, "</%s>", n.local())
	return b.String()
}

func lastSegment(ns string) string {
	ns = strings.TrimRight(ns, "/")
	if i := strings.LastIndexAny(ns, "/:#"); i >= 0 {
		return ns[i+1:]
	}
	return ns
}

// parseXOML reads an XOML document (or a FIMAutomation export that embeds one)
// and returns its root activity element. When the input is a wrapper rather than
// the workflow itself, the embedded XOML is located and re-parsed.
func parseXOML(r io.Reader) (xnode, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return xnode{}, err
	}
	root, err := decodeNode(data)
	if err != nil {
		return xnode{}, err
	}
	if isWorkflowRoot(root) {
		return root, nil
	}
	// Not a workflow root: this is likely an Export-FIMConfig resource graph that
	// carries the XOML as an attribute value or a nested element's text.
	if embedded, ok := findEmbeddedXOML(root); ok {
		inner, err := decodeNode([]byte(embedded))
		if err != nil {
			return xnode{}, fmt.Errorf("embedded XOML did not parse: %w", err)
		}
		return inner, nil
	}
	// Fall back to treating whatever we decoded as the workflow body; the caller
	// still produces a (mostly manual-review) process rather than failing outright.
	return root, nil
}

func decodeNode(data []byte) (xnode, error) {
	var n xnode
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = false // XOML in the wild is not always namespace-clean
	if err := dec.Decode(&n); err != nil {
		return xnode{}, err
	}
	if n.XMLName.Local == "" {
		return xnode{}, fmt.Errorf("input is not XML")
	}
	return n, nil
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

// findEmbeddedXOML walks a resource-graph wrapper looking for the workflow
// markup MIM stores under its "XOML" attribute. It handles the three shapes seen
// in the wild — a literal <XOML> element, an attribute literally named XOML, and
// the Export-FIMConfig form where an element names the attribute (AttributeName=
// "XOML") and carries the escaped value in a <Value> child — and returns the
// first non-empty candidate, unescaped and CDATA-stripped so it re-parses.
func findEmbeddedXOML(n xnode) (string, bool) {
	if strings.EqualFold(n.local(), "XOML") {
		if s := strings.TrimSpace(n.Inner); s != "" {
			return cleanEmbedded(s), true
		}
	}
	for _, a := range n.Attrs {
		if strings.EqualFold(a.Name.Local, "XOML") && strings.TrimSpace(a.Value) != "" {
			return cleanEmbedded(a.Value), true
		}
	}
	// Export-FIMConfig: <AttributeType AttributeName="XOML"><Value>…</Value>…
	if v, ok := n.attr("AttributeName", "Name"); ok && strings.EqualFold(strings.TrimSpace(v), "XOML") {
		if s := valueText(n); s != "" {
			return cleanEmbedded(s), true
		}
	}
	for _, k := range n.Kids {
		if s, ok := findEmbeddedXOML(k); ok {
			return s, true
		}
	}
	return "", false
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
