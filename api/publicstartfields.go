package api

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/model"
)

// The public start link accepts only what its form can submit (ADR-0029). The link is
// the one route an anonymous caller writes into an instance through, and a start
// variable the form never shows is a value the model trusts without anyone having been
// asked for it: a gateway that reads `approved` before any task has set it routes on
// whatever the open internet sent.
//
// What the form can submit is a tree, not a list of names. form-js writes each keyed
// field at its value path (pathRegistry.getValuePath in the vendored viewer): the
// field's key split at its dots, behind the paths of the groups and dynamic lists that
// enclose it, with an item index after a repeating list's path. Checking the root
// names only would let a caller add `address.verified` beside the form's
// `address.street`.
//
// The check is about shape. What a field's value is, its type, whether it is required,
// its pattern, stays the form's own validation in the browser, as it is on every other
// path a form's answer takes.

// formShape is one level of the value paths a form-js schema can submit.
type formShape struct {
	// field is set where a keyed field takes the whole value: a checklist's array or a
	// file picker's object are its value, not structure to check.
	field bool
	// list is set at the path of a repeating dynamic list: its value is an array, each
	// element shaped by children.
	list     bool
	children map[string]*formShape
}

// formShapeOf reads the submittable paths of a form-js schema. A schema that does not
// parse has none, so a form that cannot be rendered accepts no field either.
func formShapeOf(schema []byte) *formShape {
	root := &formShape{}
	var doc map[string]any
	if json.Unmarshal(schema, &doc) == nil {
		root.add(doc["components"])
	}
	return root
}

// add records one container level's components under s.
func (s *formShape) add(components any) {
	list, _ := components.([]any)
	for _, c := range list {
		comp, ok := c.(map[string]any)
		if !ok {
			continue
		}
		switch typ, _ := comp["type"].(string); typ {
		case "group", "dynamiclist":
			// A container with a path nests its fields' values under that path; one
			// without leaves them at its own level.
			at := s
			if p, _ := comp["path"].(string); p != "" {
				at = s.at(p)
				if repeating, _ := comp["isRepeating"].(bool); repeating {
					at.list = true
				}
			}
			at.add(comp["components"])
		default:
			if k, _ := comp["key"].(string); k != "" {
				s.at(k).field = true
			}
			s.add(comp["components"])
		}
	}
}

// at returns the node at a dotted path below s, creating the levels on the way.
func (s *formShape) at(path string) *formShape {
	n := s
	for _, seg := range strings.Split(path, ".") {
		if n.children == nil {
			n.children = map[string]*formShape{}
		}
		c, ok := n.children[seg]
		if !ok {
			c = &formShape{}
			n.children[seg] = c
		}
		n = c
	}
	return n
}

// notOnForm names every submitted path the form cannot have produced, sorted, so a
// refusal lists all of them at once. It reads the start variables as they will be
// stored, before anything is sealed.
func (s *formShape) notOnForm(vars []model.VariableValue) []string {
	var bad []string
	for _, v := range vars {
		n, ok := s.children[v.Name]
		switch {
		case !ok:
			bad = append(bad, v.Name)
		case n.field, v.Kind == model.VarNull:
		case v.Kind != model.VarJSON:
			// A plain value where the form nests fields.
			bad = append(bad, v.Name)
		default:
			var val any
			dec := json.NewDecoder(strings.NewReader(v.Text))
			dec.UseNumber()
			if dec.Decode(&val) != nil {
				bad = append(bad, v.Name)
				continue
			}
			bad = n.check(val, v.Name, bad)
		}
	}
	slices.Sort(bad)
	return bad
}

// check appends the paths in val, found at path, that s does not hold.
func (s *formShape) check(val any, path string, bad []string) []string {
	if s.field || val == nil {
		return bad
	}
	if !s.list {
		return s.checkObject(val, path, bad)
	}
	items, ok := val.([]any)
	if !ok {
		return append(bad, path)
	}
	for i, item := range items {
		bad = s.checkObject(item, path+"["+strconv.Itoa(i)+"]", bad)
	}
	return bad
}

// checkObject appends the members of an object val that s has no field for.
func (s *formShape) checkObject(val any, path string, bad []string) []string {
	if val == nil {
		return bad
	}
	obj, ok := val.(map[string]any)
	if !ok {
		return append(bad, path)
	}
	for k, v := range obj {
		p := path + "." + k
		c, ok := s.children[k]
		if !ok {
			bad = append(bad, p)
			continue
		}
		bad = c.check(v, p, bad)
	}
	return bad
}
