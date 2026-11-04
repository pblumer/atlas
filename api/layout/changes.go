package layout

import (
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

// Every difference between two versions of a model, element by element.
//
// Difference answers "are these the same model, and if not, where do they first
// part" — enough for a chip that says whether a draft is the version that runs,
// and for a refusal that says why a layout was not saved. It is not enough for the
// question a person brings to two versions of a process: *what changed*. A first
// difference is one line of an answer whose other lines the reader cannot see, and
// after a reordering it is not even the most important line.
//
// Changes answers that question. It reads both documents the way the digest does —
// the diagram stripped, attributes at their schema default dropped, whitespace
// between elements and comments ignored — and lines them up by element id rather
// than by position, so an element moved within the document is the same element,
// and an inserted task does not make every element after it read as changed.
//
// What counts as an element. A BPMN element with an id is one when it stands on
// its own in the model: a child of the root <definitions> (a process, a message, a
// signal, an error), or a child of a container (a process, sub-process, ad-hoc
// sub-process, transaction, collaboration, lane set or lane) — the flow nodes,
// sequence flows, data objects, artifacts, lanes, participants and message flows.
// Everything else inside an element belongs to it: its documentation, its event
// definition, its condition, its extension elements with their task definition,
// headers, input and output mappings, script and form. An event definition carries
// an id of its own in a bpmn-js document, but it is a part of its event; listing it
// as a second element would report one change twice, under a name nobody chose.
//
// Three things are deliberately not compared, each because it says nothing the
// compiler reads:
//
//   - <incoming> and <outgoing>. They are hints bpmn-js writes beside every flow
//     node; the compiler reads a sequence flow's sourceRef and targetRef and never
//     these (compiler/parse.go). A rewired flow is reported once, on the flow,
//     instead of again on both of its ends — and a hand-written model that leaves
//     them out does not read as changed after a round-trip through the editor.
//   - The ids of parts. An event definition or an input mapping that bpmn-js gave
//     an id is compared by what it says, not by that id.
//   - xsi:type. Its value spells a type with a prefix local to its document
//     ("bpmn:tFormalExpression" against "tFormalExpression"), so comparing it would
//     report every condition of a hand-written model as changed by the editor.
//
// So the list can be empty for two models whose digests differ — a changed
// <outgoing> list and nothing else. A caller that must say "same model" reads
// SameModel, and the list as what it is: everything that changed in what the model
// states.

// The kinds of change Changes reports, and of difference inside one.
const (
	ChangeAdded   = "added"
	ChangeRemoved = "removed"
	ChangeChanged = "changed"
)

// Change is one element that differs between the two versions.
type Change struct {
	// Kind is ChangeAdded (only in the newer document), ChangeRemoved (only in
	// the older) or ChangeChanged (in both, stating something different).
	Kind string `json:"kind"`
	// ID is the element's id. Empty for the document itself: what the root
	// <definitions> holds beside its elements, such as an import.
	ID string `json:"id"`
	// Type is the element's kind as the document spells it — "userTask",
	// "sequenceFlow" — in the newer document when it is in both.
	Type string `json:"type"`
	// Name is the element's name, the newer one when it is in both and has one.
	Name string `json:"name,omitempty"`
	// Parent is the id of the element it sits in — its process, sub-process or
	// lane set — in the newer document when it is in both.
	Parent string `json:"parent,omitempty"`
	// Details are what differs, for a changed element, in the order the newer
	// document states them. Empty for an added or a removed one.
	Details []Detail `json:"details,omitempty"`
}

// Detail is one thing a changed element states differently.
type Detail struct {
	// Field names what differs, as a path inside the element: "name",
	// "conditionExpression", "extensionElements/zeebe:taskDefinition@type",
	// "extensionElements/zeebe:ioMapping/zeebe:input[total]@source". Two
	// fields belong to the element itself: "type", when the same id is now a
	// different kind of element, and "parent", when it moved to another container.
	Field string `json:"field"`
	// Kind is ChangeAdded when only the newer document states the field,
	// ChangeRemoved when only the older one does, ChangeChanged when both do.
	Kind string `json:"kind"`
	// Before and After are the field's values; the side that does not state it is
	// empty. A part with no text — <zeebe:userTask/> — has the empty value on the
	// side that has it, which Kind tells from absent.
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// Changes lists every element in which two BPMN documents differ as models: added
// in b, removed from a, or stating something else. a is the older side and b the
// newer, so "added" means "in b and not in a". The changes come in b's document
// order, the removed ones after them in a's.
//
// An error means one of the documents is not well-formed XML.
func Changes(a, b []byte) ([]Change, error) {
	ma, err := parseModel(a)
	if err != nil {
		return nil, err
	}
	mb, err := parseModel(b)
	if err != nil {
		return nil, err
	}
	// How repeated parts are named is settled for both documents at once: a name
	// that depended on one document alone would change with the number of parts,
	// and every part would then read as removed and added again.
	naming := partNaming(ma.elements, mb.elements)
	ma.collect(naming)
	mb.collect(naming)
	ea, eb := ma.elements, mb.elements

	before := make(map[string]*modelElement, len(ea))
	for _, e := range ea {
		before[e.id] = e
	}
	out := []Change{}
	seen := make(map[string]bool, len(eb))
	for _, nb := range eb {
		seen[nb.id] = true
		na, ok := before[nb.id]
		if !ok {
			if nb.root {
				// The document's own parts appeared — an import, a root extension.
				// They are reported as a change of the document, never as an
				// element "added" with no id.
				if d := diffFacets(&modelElement{}, nb); len(d) > 0 {
					out = append(out, Change{Kind: ChangeChanged, Type: nb.typ, Details: d})
				}
				continue
			}
			out = append(out, Change{Kind: ChangeAdded, ID: nb.id, Type: nb.typ, Name: nb.name, Parent: nb.parent})
			continue
		}
		if d := diffFacets(na, nb); len(d) > 0 {
			name := nb.name
			if name == "" {
				name = na.name
			}
			out = append(out, Change{Kind: ChangeChanged, ID: nb.id, Type: nb.typ, Name: name, Parent: nb.parent, Details: d})
		}
	}
	for _, na := range ea {
		if seen[na.id] {
			continue
		}
		if na.root {
			if d := diffFacets(na, &modelElement{}); len(d) > 0 {
				out = append(out, Change{Kind: ChangeChanged, Type: na.typ, Details: d})
			}
			continue
		}
		out = append(out, Change{Kind: ChangeRemoved, ID: na.id, Type: na.typ, Name: na.name, Parent: na.parent})
	}
	return out, nil
}

// Compare answers both questions about two versions of a model at once: where they
// first part, as Difference names it ("" when they are the same model), and every
// element that differs, as Changes lists them. aName and bName name the two sides in
// the first difference. An error means one of the documents is not well-formed XML.
func Compare(a, b []byte, aName, bName string) (string, []Change, error) {
	diff, err := Difference(a, b, aName, bName)
	if err != nil {
		return "", nil, err
	}
	changes, err := Changes(a, b)
	return diff, changes, err
}

// diffFacets lists the fields two states of one element disagree on: the element's
// kind and container first, then its own fields in b's order, then those only a has.
func diffFacets(a, b *modelElement) []Detail {
	var out []Detail
	if a.typKey != "" && b.typKey != "" && a.typKey != b.typKey {
		out = append(out, Detail{Field: "type", Kind: ChangeChanged, Before: a.typ, After: b.typ})
	}
	if a.typKey != "" && b.typKey != "" && a.parent != b.parent {
		out = append(out, Detail{Field: "parent", Kind: ChangeChanged, Before: a.parent, After: b.parent})
	}
	for _, k := range b.order {
		fb := b.facets[k]
		fa, ok := a.facets[k]
		switch {
		case !ok:
			out = append(out, Detail{Field: fb.label, Kind: ChangeAdded, After: fb.value})
		case fa.value != fb.value:
			out = append(out, Detail{Field: fb.label, Kind: ChangeChanged, Before: fa.value, After: fb.value})
		}
	}
	for _, k := range a.order {
		if _, ok := b.facets[k]; ok {
			continue
		}
		fa := a.facets[k]
		out = append(out, Detail{Field: fa.label, Kind: ChangeRemoved, Before: fa.value})
	}
	return out
}

// bpmnModelNS is the namespace of BPMN's semantic elements.
const bpmnModelNS = "http://www.omg.org/spec/BPMN/20100524/MODEL"

// xsiNS is the XML Schema instance namespace, whose type attribute is not compared.
const xsiNS = "http://www.w3.org/2001/XMLSchema-instance"

// knownPrefixes spells the namespaces every Atlas model uses the same way in every
// answer, whatever prefix a document bound them to: BPMN's own elements bare, the
// two extension vocabularies by the prefix their moddle declares.
var knownPrefixes = map[string]string{
	bpmnModelNS:                           "",
	"http://camunda.org/schema/zeebe/1.0": "zeebe",
	"http://atlas/schema/1.0":             "atlas",
	xsiNS:                                 "xsi",
}

// containers are the BPMN elements whose children with an id are elements of their
// own rather than parts of them.
var containers = map[string]bool{
	"process":         true,
	"subProcess":      true,
	"adHocSubProcess": true,
	"transaction":     true,
	"collaboration":   true,
	"laneSet":         true,
	"lane":            true,
	"childLaneSet":    true,
}

// discriminators are the attributes that tell repeated parts of one kind apart, in
// the order they are tried: an input or output mapping by its target, a header by
// its key, a property, constant or start variable by its name. A part identified
// this way keeps its field name when another is inserted before it.
var discriminators = []string{"target", "key", "name"}

// xnode is one element of a parsed document, the diagram already removed.
type xnode struct {
	name     xml.Name
	attrs    []xml.Attr
	text     []string // trimmed, non-empty character data directly inside it
	children []*xnode
}

func (n *xnode) attr(local string) string {
	for _, a := range n.attrs {
		if a.Name.Space == "" && a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

func (n *xnode) value() string { return strings.Join(n.text, "\n") }

// facet is one field of an element: how to name it, and what it says.
type facet struct {
	label string
	value string
}

// modelElement is one element of a document with every field it states, keyed
// by a namespace-qualified path so that two documents binding different prefixes
// to one namespace still agree.
type modelElement struct {
	id     string
	typ    string // as shown
	typKey string // namespace-qualified, for comparing
	name   string
	parent string
	root   bool     // the document's own parts beside its elements
	node   *xnode   // the element, whose attributes are its own fields; nil for the root
	parts  []*xnode // what sits inside it and is not an element of its own
	order  []string
	facets map[string]facet
}

// put records one field. Every key is put once: a repeated part's name is unique
// among its kind (partNaming), and an attribute's key extends its part's.
func (e *modelElement) put(key, label, value string) {
	if e.facets == nil {
		e.facets = map[string]facet{}
	}
	e.order = append(e.order, key)
	e.facets[key] = facet{label: label, value: value}
}

// parsedModel is a document's elements, in document order, and how to spell the
// names in it.
type parsedModel struct {
	elements []*modelElement
	label    func(xml.Name) string
}

// collect records every element's fields, naming repeated parts as naming says.
func (m *parsedModel) collect(naming map[string]partName) {
	for _, e := range m.elements {
		if e.node != nil {
			ownAttrs(e, e.node, "", "", m.label)
		}
		partFacets(e, e.parts, "", "", "", naming, m.label)
	}
}

// parseModel parses a document and lists its elements in document order. The
// document itself comes first as a root element holding what <definitions> carries
// beside its elements; it has no fields when there is nothing there.
func parseModel(src []byte) (*parsedModel, error) {
	root, prefixes, err := parseTree(src)
	if err != nil {
		return nil, err
	}
	label := func(n xml.Name) string {
		p, ok := knownPrefixes[n.Space]
		if !ok {
			p = prefixes[n.Space]
		}
		if p == "" {
			return n.Local
		}
		return p + ":" + n.Local
	}
	m := &parsedModel{label: label}
	if root == nil {
		return m, nil
	}
	doc := &modelElement{root: true, typ: label(root.name), typKey: qkey(root.name)}
	m.elements = append(m.elements, doc)
	var walk func(n *xnode, owner *modelElement, container bool, parentID string)
	walk = func(n *xnode, owner *modelElement, container bool, parentID string) {
		for _, c := range n.children {
			id := c.attr("id")
			if container && id != "" {
				e := &modelElement{
					id: id, typ: label(c.name), typKey: qkey(c.name),
					name: c.attr("name"), parent: parentID, node: c,
				}
				m.elements = append(m.elements, e)
				walk(c, e, c.name.Space == bpmnModelNS && containers[c.name.Local], id)
				continue
			}
			if c.name.Space == bpmnModelNS && (c.name.Local == "incoming" || c.name.Local == "outgoing") {
				continue
			}
			owner.parts = append(owner.parts, c)
		}
	}
	walk(root, doc, true, "")
	return m, nil
}

// ownAttrs records an element's or a part's attributes under path. The id is not
// a field: an element's id is what it is matched by, and a part's id is a name
// nobody chose. Namespace declarations, xsi:type and attributes at their schema
// default say nothing the walk has not already recorded.
func ownAttrs(e *modelElement, n *xnode, key, path string, label func(xml.Name) string) {
	for _, a := range n.attrs {
		switch {
		case a.Name.Local == "xmlns" || a.Name.Space == "xmlns":
			continue
		case a.Name.Space == "" && a.Name.Local == "id":
			continue
		case a.Name.Space == xsiNS && a.Name.Local == "type":
			continue
		case a.Name.Space == "" && bpmnDefaults[a.Name.Local] == a.Value:
			continue
		}
		field := label(a.Name)
		if path != "" {
			field = path + "@" + field
		}
		e.put(key+"@"+qkey(a.Name), field, a.Value)
	}
}

// How the parts of one kind at one place in an element are told apart.
type partName struct {
	by   int    // nameNone, nameAttr, nameText or namePosition
	attr string // the discriminating attribute, for nameAttr
}

const (
	nameNone     = iota // never more than one: the part's kind is its name
	nameAttr            // "[total]": by an attribute every one carries distinctly
	nameText            // "[t2]": by their text — a lane's flowNodeRef
	namePosition        // "[2]": nothing they say tells them apart
)

// partGroups calls visit for every group of parts of one kind at one place in an
// element, with the key naming that place: the element's id and the path of part
// kinds down to it. The key carries no discriminator, so it is the same in both
// documents whatever their parts say.
func partGroups(e *modelElement, parts []*xnode, place string, visit func(key string, group []*xnode)) {
	var kinds []string
	byKind := map[string][]*xnode{}
	for _, c := range parts {
		k := qkey(c.name)
		if _, ok := byKind[k]; !ok {
			kinds = append(kinds, k)
		}
		byKind[k] = append(byKind[k], c)
	}
	for _, k := range kinds {
		visit(e.id+"\x00"+place+"/"+k, byKind[k])
		for _, c := range byKind[k] {
			partGroups(e, c.children, place+"/"+k, visit)
		}
	}
}

// partNaming settles, for every place parts repeat in either document, how they are
// named in both: not at all when neither document has more than one; else by the
// first discriminating attribute that tells them apart in both, by their text when
// that does, and by position when nothing does.
func partNaming(a, b []*modelElement) map[string]partName {
	type stats struct {
		max  int
		attr map[string]bool // still distinct in every group seen
		text bool
	}
	read := func(els []*modelElement) map[string]*stats {
		out := map[string]*stats{}
		for _, e := range els {
			partGroups(e, e.parts, "", func(key string, group []*xnode) {
				s := out[key]
				if s == nil {
					s = &stats{attr: map[string]bool{}, text: true}
					for _, d := range discriminators {
						s.attr[d] = true
					}
					out[key] = s
				}
				if len(group) > s.max {
					s.max = len(group)
				}
				for _, d := range discriminators {
					d := d
					if !distinct(group, func(n *xnode) string { return n.attr(d) }) {
						s.attr[d] = false
					}
				}
				if !distinct(group, shortText) {
					s.text = false
				}
			})
		}
		return out
	}
	sa, sb := read(a), read(b)
	out := map[string]partName{}
	decide := func(key string) {
		if _, done := out[key]; done {
			return
		}
		x, y := sa[key], sb[key]
		most := 0
		for _, s := range []*stats{x, y} {
			if s != nil && s.max > most {
				most = s.max
			}
		}
		if most <= 1 {
			out[key] = partName{by: nameNone}
			return
		}
		for _, d := range discriminators {
			if (x == nil || x.attr[d]) && (y == nil || y.attr[d]) {
				out[key] = partName{by: nameAttr, attr: d}
				return
			}
		}
		if (x == nil || x.text) && (y == nil || y.text) {
			out[key] = partName{by: nameText}
			return
		}
		out[key] = partName{by: namePosition}
	}
	for k := range sa {
		decide(k)
	}
	for k := range sb {
		decide(k)
	}
	return out
}

// partFacets records the parts inside an element under the element, each at a path
// naming where it sits, repeated parts named as naming settled.
func partFacets(e *modelElement, parts []*xnode, key, path, place string, naming map[string]partName, label func(xml.Name) string) {
	position := map[string]int{}
	for _, c := range parts {
		k := qkey(c.name)
		position[k]++
		tag := ""
		switch n := naming[e.id+"\x00"+place+"/"+k]; n.by {
		case nameAttr:
			tag = "[" + c.attr(n.attr) + "]"
		case nameText:
			tag = "[" + c.value() + "]"
		case namePosition:
			tag = "[" + strconv.Itoa(position[k]) + "]"
		}
		ckey := key + "/" + k + tag
		cpath := label(c.name) + tag
		if path != "" {
			cpath = path + "/" + cpath
		}
		e.put(ckey, cpath, c.value())
		ownAttrs(e, c, ckey, cpath, label)
		partFacets(e, c.children, ckey, cpath, place+"/"+k, naming, label)
	}
}

// shortText is a part's text when it is short enough to name the part by — a
// reference, a variable name — and "" when it is a paragraph or a script, which
// would make a field name nobody can read.
func shortText(n *xnode) string {
	v := n.value()
	if len(v) > 64 || strings.ContainsAny(v, "\n[]") {
		return ""
	}
	return v
}

// distinct reports whether every node has a non-empty value of f and no two share
// one.
func distinct(group []*xnode, f func(*xnode) string) bool {
	seen := make(map[string]bool, len(group))
	for _, n := range group {
		v := f(n)
		if v == "" || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}

// qkey is a name with its namespace, the form fields are matched by.
func qkey(n xml.Name) string { return "{" + n.Space + "}" + n.Local }

// parseTree reads a document without its diagram into a tree, and the prefixes it
// binds to each namespace — the first binding, for naming a namespace Atlas does
// not know.
func parseTree(src []byte) (*xnode, map[string]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(stripDiagram(src)))
	prefixes := map[string]string{}
	var root *xnode
	var stack []*xnode
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" {
					if _, ok := prefixes[a.Value]; !ok {
						prefixes[a.Value] = a.Name.Local
					}
				}
			}
			n := &xnode{name: t.Name, attrs: append([]xml.Attr(nil), t.Attr...)}
			if len(stack) == 0 {
				if root == nil {
					root = n
				}
			} else {
				p := stack[len(stack)-1]
				p.children = append(p.children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			if s := strings.TrimSpace(string(t)); s != "" {
				top := stack[len(stack)-1]
				top.text = append(top.text, s)
			}
		}
	}
	return root, prefixes, nil
}
