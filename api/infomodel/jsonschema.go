package infomodel

import (
	"fmt"
	"sort"
)

// The JSON Schema projection.
//
// The class diagram is what a person reads; this is what a *value* is checked
// against. They are the same model seen from two sides, and only one of them is
// authored: a projection is derived, never edited, and — the discipline ADR-0211
// set for Panorama's C4 projection — it states what it could not carry.
//
// What it cannot carry is real, and worth naming rather than hiding. A JSON
// document is a **tree**. A class model is a **graph**: a Customer places Orders and
// an Order belongs to a Customer, and neither contains the other. Only composition —
// a whole that owns parts that die with it — is containment, so only composition
// projects into the document. Everything else is reported.

const schemaDialect = "https://json-schema.org/draft/2020-12/schema"

// LossNote is one thing the projection could not express, and why. The reason
// matters more than the fact: "JSON Schema has no keyword for this" is a property
// of the target notation, not a shortcoming of the model it came from.
type LossNote struct {
	Area   string `json:"area"`
	Reason string `json:"reason"`
}

// Projection is a derived schema together with what deriving it cost.
type Projection struct {
	Class  string         `json:"class"`
	Schema map[string]any `json:"schema"`
	Loss   []LossNote     `json:"loss"`
}

// SchemaFor projects one class of a model to a JSON Schema.
//
// It validates the model first and refuses an invalid one. A schema derived from a
// model that does not yet make sense would be a confident, checkable, wrong
// statement about what a value must look like — worse than no schema.
func SchemaFor(m Model, className string) (Projection, error) {
	if res := Validate(m); !res.Valid {
		return Projection{}, fmt.Errorf("model is not valid: %s", res.Findings[0].Message)
	}
	root, ok := m.ClassByName(className)
	if !ok {
		return Projection{}, fmt.Errorf("no class named %q in this model", className)
	}

	p := &projector{model: m, defs: map[string]any{}, pending: map[string]bool{}}
	schema := p.class(root)
	schema["$schema"] = schemaDialect
	if len(p.defs) > 0 {
		schema["$defs"] = p.defs
	}
	return Projection{Class: root.Name, Schema: schema, Loss: p.lossNotes(root)}, nil
}

type projector struct {
	model Model
	// defs holds every class the root reaches, so a reference — including a class
	// reaching itself — is a $ref rather than an expansion that never terminates.
	defs    map[string]any
	pending map[string]bool
	// seen records which relationship kinds the projection actually met, so the loss
	// report names what this model has rather than reciting the whole list.
	sawAssociation    bool
	sawAggregation    bool
	sawGeneralization bool
}

// class builds one class's object schema: its own attributes, the attributes it
// inherits, and the parts it owns.
func (p *projector) class(c *Class) map[string]any {
	if c.Stereotype == StereotypeEnumeration {
		lits := make([]any, 0, len(c.Literals))
		for _, l := range c.Literals {
			lits = append(lits, l)
		}
		out := map[string]any{"enum": lits}
		if c.Documentation != "" {
			out["description"] = c.Documentation
		}
		return out
	}

	props := map[string]any{}
	required := []string{}
	// Inherited members come first so a specialization reads as "everything the
	// general thing has, plus these" — the order a person expects.
	for _, ancestor := range p.ancestors(c) {
		p.addAttributes(ancestor, props, &required)
		p.addOwnedParts(ancestor, props, &required)
	}
	p.addAttributes(c, props, &required)
	p.addOwnedParts(c, props, &required)

	out := map[string]any{"title": c.Name, "type": "object", "properties": props}
	if c.Documentation != "" {
		out["description"] = c.Documentation
	}
	sort.Strings(required)
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// ancestors returns a class's generalization chain, most general first. Validation
// has already ruled out cycles, so this terminates.
func (p *projector) ancestors(c *Class) []*Class {
	var chain []*Class
	seen := map[string]bool{c.ID: true}
	cur := c
	for {
		var parent *Class
		for _, a := range p.model.Associations {
			if a.Kind != KindGeneralization || a.From.ClassID != cur.ID {
				continue
			}
			if found, ok := p.model.ClassByID(a.To.ClassID); ok && !seen[found.ID] {
				parent = found
			}
			break
		}
		if parent == nil {
			break
		}
		p.sawGeneralization = true
		seen[parent.ID] = true
		chain = append([]*Class{parent}, chain...)
		cur = parent
	}
	return chain
}

func (p *projector) addAttributes(c *Class, props map[string]any, required *[]string) {
	for _, a := range c.Attributes {
		mult, _ := MultiplicityOf(a.Multiplicity)
		props[a.Name] = p.wrap(p.typeSchema(a), mult, a.Documentation)
		if mult.Required {
			*required = append(*required, a.Name)
		}
	}
}

// addOwnedParts projects the compositions this class is the whole of. A part is a
// member of the whole's value because that is what composition means: delete the
// whole and the parts go with it, so they were never separately addressable.
func (p *projector) addOwnedParts(c *Class, props map[string]any, required *[]string) {
	for _, a := range p.model.Associations {
		switch a.Kind {
		case KindAssociation:
			if a.From.ClassID == c.ID || a.To.ClassID == c.ID {
				p.sawAssociation = true
			}
			continue
		case KindAggregation:
			if a.From.ClassID == c.ID || a.To.ClassID == c.ID {
				p.sawAggregation = true
			}
			continue
		case KindComposition:
			if a.From.ClassID != c.ID {
				continue
			}
		default:
			continue
		}
		part, ok := p.model.ClassByID(a.To.ClassID)
		if !ok {
			continue
		}
		name := a.To.Role
		if name == "" {
			name = a.Name
		}
		if name == "" {
			continue // an unnamed containment has no property to be
		}
		mult, known := MultiplicityOf(a.To.Multiplicity)
		if !known {
			mult = MultiplicityOption{Multiplicity: MultOptional}
		}
		props[name] = p.wrap(p.ref(part), mult, "")
		if mult.Required {
			*required = append(*required, name)
		}
	}
}

// typeSchema is an attribute's type: a primitive inline, a class as a $ref.
func (p *projector) typeSchema(a Attribute) map[string]any {
	if prim, ok := PrimitiveOf(a.Type); ok {
		out := map[string]any{"type": prim.JSONType}
		if prim.JSONFormat != "" {
			out["format"] = prim.JSONFormat
		}
		return out
	}
	if target, ok := p.model.ClassByName(a.Type); ok {
		return p.ref(target)
	}
	return map[string]any{} // unreachable: validation resolved every type
}

// ref emits a class into $defs once and returns a reference to it. Emitting before
// recursing is what makes a class that contains its own kind terminate.
func (p *projector) ref(c *Class) map[string]any {
	if !p.pending[c.Name] {
		p.pending[c.Name] = true
		p.defs[c.Name] = p.class(c)
	}
	return map[string]any{"$ref": "#/$defs/" + c.Name}
}

// wrap applies a multiplicity to a schema: a collection becomes an array, and
// 1..* an array with at least one member. Documentation rides along as the
// standard's own description keyword.
func (p *projector) wrap(schema map[string]any, mult MultiplicityOption, doc string) map[string]any {
	if mult.Collection {
		out := map[string]any{"type": "array", "items": schema}
		if mult.Required {
			out["minItems"] = 1
		}
		if doc != "" {
			out["description"] = doc
		}
		return out
	}
	if doc != "" && schema["$ref"] == nil {
		schema["description"] = doc
	}
	return schema
}

// lossNotes states what the projection could not carry. It reports only what this
// model actually has: a note about aggregations on a model with none would be
// noise, and noise is how a report stops being read.
func (p *projector) lossNotes(root *Class) []LossNote {
	out := []LossNote{}
	if p.sawAssociation {
		out = append(out, LossNote{
			Area: "Association",
			Reason: "A JSON document is a tree and an association is a reference between two " +
				"things that exist separately. The related object is not part of this value, so " +
				"nothing here describes it.",
		})
	}
	if p.sawAggregation {
		out = append(out, LossNote{
			Area: "Aggregation",
			Reason: "The grouped parts go on existing without the whole, so they are not members " +
				"of its value. Only composition — parts that die with the whole — becomes containment.",
		})
	}
	if p.sawGeneralization {
		out = append(out, LossNote{
			Area: "Generalization",
			Reason: "The inherited members are inlined, so the hierarchy is flattened: a validator " +
				"accepts any value carrying the right members and cannot tell a specialization from " +
				"the general thing.",
		})
	}
	if len(root.Identity) > 0 {
		out = append(out, LossNote{
			Area: "Business key",
			Reason: "JSON Schema has no keyword for identity, so nothing here says that " +
				keyList(root) + " is what makes two of these the same one. That is the model's " +
				"most important statement about this class and it does not survive the projection.",
		})
	}
	out = append(out, LossNote{
		Area: "Stereotype",
		Reason: "Whether a class is a business object, a value type or an enumeration decides " +
			"what may relate to it. A schema only describes a value's shape, so the distinction " +
			"is not carried.",
	})
	return out
}

func keyList(c *Class) string {
	switch len(c.Identity) {
	case 0:
		return ""
	case 1:
		return c.Name + "." + c.Identity[0]
	default:
		s := ""
		for i, k := range c.Identity {
			if i > 0 {
				s += " + "
			}
			s += c.Name + "." + k
		}
		return s
	}
}
