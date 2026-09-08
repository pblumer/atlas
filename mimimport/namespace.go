package mimimport

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// XOML binds every activity library it uses to a prefix on the workflow root:
//
//	xmlns:ns1="clr-namespace:MicrosoftServices.IdentityManagement.WorkflowActivityLibrary.Activities;Assembly=…WorkflowActivityLibrary, Version=2.20.723.0, …"
//
// That binding is not decoration. It is what distinguishes a stock MIM activity
// from a MIMWAL one of the same local name, and it names the assembly and
// version an activity was authored against — the two facts a developer needs to
// find the implementation being replaced.
//
// Go's decoder resolves prefixes away: an element arrives with the namespace
// URI in XMLName.Space and no memory of which prefix carried it. Preserved
// markup used to be written from the local name alone, which lost the binding
// twice over — the qualified type was gone, and the fragment's own inner markup
// still referred to prefixes nothing declared, so it did not parse on its own.
//
// nsTable rebuilds the mapping from the root's xmlns attributes, which the
// decoder does surface, so a preserved fragment can carry its prefixes and
// declare them.

// xamlNS is the XAML namespace that carries x:Name and x:Key.
const xamlNS = "http://schemas.microsoft.com/winfx/2006/xaml"

// nsTable maps namespace URIs back to the prefixes the source document bound
// them to, and keeps those declarations so a preserved fragment can repeat them.
type nsTable struct {
	prefix map[string]string // namespace URI → prefix ("" is the default namespace)
	decls  []xml.Attr        // the root's xmlns declarations, in document order
}

// namespaces reads the prefix bindings declared on a workflow root.
func namespaces(root xnode) *nsTable {
	t := &nsTable{prefix: map[string]string{}}
	for _, a := range root.Attrs {
		p, ok := declaredPrefix(a)
		if !ok {
			continue
		}
		t.prefix[a.Value] = p
		t.decls = append(t.decls, a)
	}
	return t
}

// declaredPrefix reports the prefix an attribute declares, if it is an xmlns
// declaration at all: xmlns:p="…" binds p, and xmlns="…" binds the default.
func declaredPrefix(a xml.Attr) (string, bool) {
	switch {
	case a.Name.Space == "xmlns":
		return a.Name.Local, true
	case a.Name.Space == "" && a.Name.Local == "xmlns":
		return "", true
	}
	return "", false
}

// qname renders an element or attribute name with the prefix its namespace was
// bound to. An unqualified name (space "") stays unqualified — which is also the
// right answer for an attribute, since a default namespace never applies to one.
func (t *nsTable) qname(space, local string) string {
	if space == "" {
		return local
	}
	if p, ok := t.prefix[space]; ok {
		if p == "" {
			return local
		}
		return p + ":" + local
	}
	return local
}

// fragmentDecls returns the xmlns declarations a preserved fragment of n must
// carry to stand on its own — the ones n declares itself, then the root's for
// every prefix n did not, then a minted one for any namespace n uses that
// neither bound — together with the table to render n's names with.
//
// That table is built from the fragment's own declarations rather than from t,
// because only the fragment says what a prefix means inside it: an element that
// rebinds a prefix the root also declared would otherwise have its own name
// rendered with a prefix the fragment points somewhere else.
//
// Declarations nested deeper in n travel inside its raw inner markup, so only
// n's own names have to be covered here.
func (t *nsTable) fragmentDecls(n xnode) ([]xml.Attr, *nsTable) {
	var decls []xml.Attr
	seen := map[string]bool{}    // prefixes declared on the fragment
	bound := map[string]string{} // namespace URI → the prefix the fragment binds it to
	declare := func(a xml.Attr) {
		p, _ := declaredPrefix(a)
		decls = append(decls, a)
		seen[p] = true
		if _, ok := bound[a.Value]; !ok {
			bound[a.Value] = p // the innermost declaration of a URI wins
		}
	}
	for _, a := range n.Attrs {
		if p, ok := declaredPrefix(a); ok && !seen[p] {
			declare(a)
		}
	}
	for _, d := range t.decls {
		if p, _ := declaredPrefix(d); !seen[p] {
			declare(d)
		}
	}

	// Every namespace the fragment's own names use must now resolve.
	spaces := []string{n.XMLName.Space}
	for _, a := range n.Attrs {
		if _, isDecl := declaredPrefix(a); !isDecl {
			spaces = append(spaces, a.Name.Space)
		}
	}
	for _, space := range spaces {
		if space == "" || space == "xmlns" {
			continue // unqualified, and an attribute never takes a default namespace
		}
		if _, ok := bound[space]; ok {
			continue
		}
		p := mintPrefix(seen)
		declare(xml.Attr{Name: xml.Name{Space: "xmlns", Local: p}, Value: space})
	}
	return decls, &nsTable{prefix: bound}
}

// mintPrefix returns a prefix no declaration on the fragment uses yet, for a
// namespace the source document referenced without binding.
func mintPrefix(seen map[string]bool) string {
	for i := 0; ; i++ {
		p := "mim" + strconv.Itoa(i)
		if !seen[p] {
			return p
		}
	}
}

// clrType resolves a XAML clr-namespace URI and a local name into the .NET type
// and the assembly it lives in:
//
//	clr-namespace:Foo.Bar;Assembly=Baz, Version=1.0.0.0, …  +  Widget
//	→ "Foo.Bar.Widget", "Baz, Version=1.0.0.0, …"
//
// Both are empty for a namespace that is not a clr-namespace — the XAML
// workflow namespace of a stock WF element, say — because there is no .NET type
// to name there.
func clrType(space, local string) (typeName, assembly string) {
	const marker = "clr-namespace:"
	if !strings.HasPrefix(space, marker) {
		return "", ""
	}
	body := space[len(marker):]
	ns := body
	if i := strings.IndexByte(body, ';'); i >= 0 {
		ns = body[:i]
		for _, part := range strings.Split(body[i+1:], ";") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(part), "Assembly="); ok {
				assembly = strings.TrimSpace(v)
			}
		}
	}
	if ns = strings.TrimSpace(ns); ns == "" {
		return "", assembly
	}
	return ns + "." + local, assembly
}

// raw re-serialises the node as a standalone XML fragment, used to preserve the
// original activity inside an <atlas:mimSource> element. It reproduces the tag
// with the prefix the source bound it to, the xmlns declarations that prefix and
// its attributes need, the attributes themselves, and the inner markup verbatim
// — so the activity survives the round trip and the fragment parses on its own.
func (n xnode) raw(ns *nsTable) string {
	decls, names := ns.fragmentDecls(n)
	tag := names.qname(n.XMLName.Space, n.local())

	var b strings.Builder
	b.WriteByte('<')
	b.WriteString(tag)
	for _, d := range decls {
		name := "xmlns"
		if p, _ := declaredPrefix(d); p != "" {
			name = "xmlns:" + p
		}
		fmt.Fprintf(&b, ` %s="%s"`, name, attr(d.Value))
	}
	for _, a := range n.Attrs {
		if _, isDecl := declaredPrefix(a); isDecl {
			continue // already written above, in the order the fragment needs
		}
		fmt.Fprintf(&b, ` %s="%s"`, names.qname(a.Name.Space, a.Name.Local), attr(a.Value))
	}
	inner := strings.TrimSpace(n.Inner)
	if inner == "" {
		b.WriteString("/>")
		return b.String()
	}
	b.WriteByte('>')
	b.WriteString(inner)
	fmt.Fprintf(&b, "</%s>", tag)
	return b.String()
}
