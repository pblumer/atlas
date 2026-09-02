package infomodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The object diagram: this instance's data, as objects.
//
// UML draws types and instances as two different diagrams, and that distinction is
// the reason a class diagram was the right notation for Atlas at all — it maps onto
// the design-time/run-time line the engine already has. The class diagram next door
// says what an Order *is*; this says which orders are here and how they hang
// together.
//
// It is **derived and read-only**, the posture ADR-0211 gives Panorama's landscape
// mesh. Nobody arranges an object diagram: it is a projection of what one instance
// holds, recomputed from the values every time, and it states where it stopped.
//
// Two things become a line, and they are different kinds of fact:
//
//   - **Containment.** A composition means the parts live inside the whole's value,
//     so the parts are objects of this instance and the line is a fact about the
//     value itself. The same rule the JSON Schema projection uses (jsonschema.go).
//   - **A business key.** An association or an aggregation is a *reference* between
//     things that exist separately, so it is drawn only when one object's member
//     holds another object's business key. This is what the key is for, and without
//     one the line simply cannot be drawn — which the graph says rather than guesses.
//
// A reference matching nothing here is not an error. It is the edge of what one
// instance can see: the Customer is in another instance, or in a data store. Saying
// so is what makes the picture trustworthy, and it is precisely the boundary a
// worker-backed data store removes.

// The guards that keep a derived picture finite. A class that owns its own kind
// recurses forever without the first; an instance carrying a large list becomes an
// unreadable hairball without the second.
const (
	maxObjectDepth = 5
	maxObjectNodes = 200
)

// How a link was established. It travels to the reader because the two are
// different claims: one is read off the value, the other is inferred from a key.
const (
	ViaContainment = "containment"
	ViaKey         = "key"
)

// ObjectValue is one data object of an instance, as the graph reads it.
type ObjectValue struct {
	Name string
	// Class is the resolved declared type, "" when the object is untyped or its
	// itemSubjectRef resolves to nothing.
	Class string
	State string
	// Value is the object's value as canonical JSON, nil when it is unset.
	Value json.RawMessage
}

// ObjectAttribute is one member of an object node, in the order its class declares
// them — so two objects of one class read alike rather than in whatever order their
// JSON happened to be written.
type ObjectAttribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Key   bool   `json:"key,omitempty"`
	// Absent distinguishes "this object does not carry that member" from "it carries
	// it, empty" — different facts about a datum, and the first is usually the one
	// worth noticing.
	Absent bool `json:"absent,omitempty"`
}

// ObjectNode is one object on the diagram.
type ObjectNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Name  string `json:"name"`
	Class string `json:"class,omitempty"`
	State string `json:"state,omitempty"`
	// Key is the object's business key, rendered — what makes this object *this*
	// one, and what another object's reference has to match to become a line.
	Key        string            `json:"key,omitempty"`
	Attributes []ObjectAttribute `json:"attributes"`
	// Value is what the object holds, rendered as an attribute's value is (a scalar
	// as itself, a structure as its JSON). Carried for an object whose class is
	// unknown or whose value is not a structure — there is no attribute list to show
	// instead, and an object with neither would be an empty box.
	Value string `json:"value,omitempty"`
	// Nested marks a node that came out of a whole's value rather than being a data
	// object of the instance in its own right.
	Nested bool `json:"nested,omitempty"`
	Unset  bool `json:"unset,omitempty"`
}

// ObjectLink is one line.
type ObjectLink struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
	Via   string `json:"via"`
}

// UnresolvedRef is a reference this instance cannot satisfy: a member holding a
// business key that names no object here.
type UnresolvedRef struct {
	From  string `json:"from"`
	Role  string `json:"role"`
	Class string `json:"class"`
	Value string `json:"value"`
}

// Graph is the whole derived picture, with what it could not show.
type Graph struct {
	Nodes      []ObjectNode    `json:"nodes"`
	Links      []ObjectLink    `json:"links"`
	Unresolved []UnresolvedRef `json:"unresolved"`
	// Degraded says the application models nothing, so this is the instance's data
	// without the structure a model would give it.
	Degraded bool `json:"degraded"`
	// Truncated says a guard stopped the walk, so the picture is a part of one.
	Truncated bool `json:"truncated"`
}

// ObjectGraph derives one instance's object diagram from its data objects and the
// application's vocabulary.
func ObjectGraph(objects []ObjectValue, vocab *Vocabulary) Graph {
	g := &Graph{Nodes: []ObjectNode{}, Links: []ObjectLink{}, Unresolved: []UnresolvedRef{}}
	g.Degraded = !vocab.Modeled()

	b := &objectBuilder{vocab: vocab, graph: g}
	for _, o := range objects {
		b.addRoot(o)
	}
	// Links between roots are resolved once every node exists, because a reference
	// may point at an object that comes later in the list.
	b.linkReferences()

	sort.SliceStable(g.Links, func(a, c int) bool {
		if g.Links[a].From != g.Links[c].From {
			return g.Links[a].From < g.Links[c].From
		}
		return g.Links[a].To < g.Links[c].To
	})
	sort.SliceStable(g.Unresolved, func(a, c int) bool {
		if g.Unresolved[a].From != g.Unresolved[c].From {
			return g.Unresolved[a].From < g.Unresolved[c].From
		}
		return g.Unresolved[a].Role < g.Unresolved[c].Role
	})
	return *g
}

type objectBuilder struct {
	vocab *Vocabulary
	graph *Graph
	// pending holds the reference members found while walking, resolved after every
	// node exists.
	pending []pendingRef
}

type pendingRef struct {
	from   string // node id holding the reference
	role   string
	kind   string
	class  string // the class the reference should point at
	value  string
	nodeID string
}

// addRoot places one data object. A collection-valued object becomes one node per
// element, because a list of things is a list of objects and an object diagram
// shows objects.
func (b *objectBuilder) addRoot(o ObjectValue) {
	class, classed := b.vocab.Class(o.Class)
	decoded, ok := decodeValue(o.Value)
	if !ok {
		b.graph.Nodes = append(b.graph.Nodes, ObjectNode{
			ID: o.Name, Name: o.Name, Label: labelFor(o.Name, o.Class, classed),
			Class: classOf(o.Class, classed), State: o.State, Unset: true, Attributes: []ObjectAttribute{},
		})
		return
	}
	if list, isList := decoded.([]any); isList && classed {
		for i, el := range list {
			b.addNode(fmt.Sprintf("%s[%d]", o.Name, i), o.Name, class, el, o.State, false, 0)
		}
		return
	}
	if !classed {
		b.graph.Nodes = append(b.graph.Nodes, ObjectNode{
			ID: o.Name, Name: o.Name, Label: o.Name, State: o.State,
			Value: renderValue(decoded), Attributes: []ObjectAttribute{},
		})
		return
	}
	b.addNode(o.Name, o.Name, class, decoded, o.State, false, 0)
}

// addNode places one object of a known class and walks what it contains.
func (b *objectBuilder) addNode(id, name string, class Class, value any, state string, nested bool, depth int) {
	if len(b.graph.Nodes) >= maxObjectNodes {
		b.graph.Truncated = true
		return
	}
	node := ObjectNode{
		ID: id, Name: name, Label: id + " : " + class.Name, Class: class.Name,
		State: state, Nested: nested, Attributes: []ObjectAttribute{},
	}
	fields, isObject := value.(map[string]any)
	if !isObject {
		// A class-typed object whose value is not a structure: show what it holds
		// rather than an attribute list it does not have.
		node.Value = renderValue(value)
		b.graph.Nodes = append(b.graph.Nodes, node)
		return
	}
	// The key identifies a root object. A composed part has none of its own — it is
	// identified by the whole that owns it, which is what composition means.
	if !nested {
		node.Key = keyValue(class, fields)
	}
	keys := map[string]bool{}
	for _, k := range class.Identity {
		keys[k] = true
	}
	for _, a := range b.vocab.members[class.Name] {
		raw, present := fields[a.Name]
		node.Attributes = append(node.Attributes, ObjectAttribute{
			Name: a.Name, Value: renderValue(raw), Key: keys[a.Name], Absent: !present,
		})
	}
	b.graph.Nodes = append(b.graph.Nodes, node)

	if depth >= maxObjectDepth {
		// A deeper part exists in the value but is not drawn; say so rather than
		// letting the picture look complete.
		if b.hasDeeperParts(class, fields) {
			b.graph.Truncated = true
		}
		return
	}
	b.walkRelations(id, class, fields, depth)
}

// walkRelations turns a class's associations into lines: the compositions into
// nested nodes now, the references into pending matches for later.
func (b *objectBuilder) walkRelations(id string, class Class, fields map[string]any, depth int) {
	for _, rel := range b.vocab.relations(class.Name) {
		raw, present := fields[rel.member]
		if !present || raw == nil {
			continue
		}
		if rel.kind == KindComposition {
			b.addParts(id, rel, raw, depth)
			continue
		}
		// An association or an aggregation relates things that exist separately, so
		// the member holds a key rather than the object itself.
		for _, v := range asList(raw) {
			b.pending = append(b.pending, pendingRef{
				from: id, role: rel.member, kind: rel.kind, class: rel.target, value: renderValue(v),
			})
		}
	}
}

func (b *objectBuilder) addParts(ownerID string, rel relation, raw any, depth int) {
	part, ok := b.vocab.Class(rel.target)
	if !ok {
		return
	}
	list, isList := raw.([]any)
	if !isList {
		childID := ownerID + "." + rel.member
		b.addNode(childID, rel.member, part, raw, "", true, depth+1)
		b.link(ownerID, childID, KindComposition, rel.member, ViaContainment)
		return
	}
	for i, el := range list {
		childID := fmt.Sprintf("%s.%s[%d]", ownerID, rel.member, i)
		b.addNode(childID, rel.member, part, el, "", true, depth+1)
		b.link(ownerID, childID, KindComposition, rel.member, ViaContainment)
	}
}

// hasDeeperParts reports whether the value carries a composition member the depth
// guard is about to leave undrawn.
func (b *objectBuilder) hasDeeperParts(class Class, fields map[string]any) bool {
	for _, rel := range b.vocab.relations(class.Name) {
		if rel.kind != KindComposition {
			continue
		}
		if v, ok := fields[rel.member]; ok && v != nil {
			return true
		}
	}
	return false
}

// linkReferences matches every held key against the objects that exist, once they
// all do. What matches becomes a line; what does not becomes a stated boundary.
func (b *objectBuilder) linkReferences() {
	byClassKey := map[string]string{}
	for _, n := range b.graph.Nodes {
		if n.Nested || n.Class == "" || n.Key == "" {
			continue
		}
		byClassKey[n.Class+"\x00"+n.Key] = n.ID
	}
	for _, p := range b.pending {
		if target, ok := byClassKey[p.class+"\x00"+p.value]; ok {
			b.link(p.from, target, p.kind, p.role, ViaKey)
			continue
		}
		b.graph.Unresolved = append(b.graph.Unresolved, UnresolvedRef{
			From: p.from, Role: p.role, Class: p.class, Value: p.value,
		})
	}
}

func (b *objectBuilder) link(from, to, kind, label, via string) {
	b.graph.Links = append(b.graph.Links, ObjectLink{From: from, To: to, Kind: kind, Label: label, Via: via})
}

// relation is one association seen from one end: the member on this class, the
// class at the other end, and what kind of relationship it is.
type relation struct {
	member string
	target string
	kind   string
}

// relations lists the associations a class participates in, from its own side.
// Both directions are read: an association end's role names the member on the
// *opposite* class, which is the same reading the class canvas and the JSON Schema
// projection use.
func (v *Vocabulary) relations(className string) []relation {
	if v == nil {
		return nil
	}
	return v.rels[className]
}

// keyValue renders a class's business key from an object's fields, "" when the
// class declares none or the value does not carry all of it — a partial key
// identifies nothing, so it is not offered as one.
func keyValue(class Class, fields map[string]any) string {
	if len(class.Identity) == 0 {
		return ""
	}
	parts := make([]string, 0, len(class.Identity))
	for _, k := range class.Identity {
		raw, ok := fields[k]
		if !ok || raw == nil {
			return ""
		}
		parts = append(parts, renderValue(raw))
	}
	return strings.Join(parts, " · ")
}

// decodeValue decodes a data object's canonical JSON, reporting whether there was
// a value at all. Numbers stay exact, because a business key is routinely a number
// and matching it as a float would lose digits.
func decodeValue(raw json.RawMessage) (any, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, false
	}
	if out == nil {
		return nil, false
	}
	return out, true
}

// renderValue is one value as the diagram shows it: a scalar as itself, a structure
// as its JSON — so a reference held as the number 7 matches a key rendered as 7.
func renderValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		out, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(out)
	}
}

// asList reads a member that may hold one reference or several.
func asList(v any) []any {
	if list, ok := v.([]any); ok {
		return list
	}
	return []any{v}
}

func classOf(name string, known bool) string {
	if known {
		return name
	}
	return ""
}

func labelFor(name, class string, known bool) string {
	if known {
		return name + " : " + class
	}
	return name
}
