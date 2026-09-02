package infomodel

// The authoring subset: which class kinds Atlas can create, and which associations
// it will let you draw between them.
//
// This follows the discipline ADR-0189 §2 set for Panorama's ArchiMate subset, and
// for the same three reasons.
//
// **It is one table, not two.** The canvas has to refuse a connection while it is
// being dragged, and the server has to refuse it on write. Two copies of a rule
// matrix is how you get a canvas that lets somebody draw an arrow the server then
// rejects — so the browser is served this table rather than carrying its own.
//
// **It is a subset, and says so.** UML's class diagram has interfaces, operations,
// n-ary associations, association classes, qualifiers, profiles and derivation. This
// covers what a *process* information model needs: types, their typed attributes,
// an identity, and the four relationships that carry meaning between business
// objects. Everything else is refused *as out of subset*, which is a different
// answer from "UML forbids this" — one is a limit of this build, the other is a fact
// about the notation, and a user acts on them differently.
//
// **It models concepts, not storage.** There is deliberately no notion of a table, a
// column, a foreign key or a nullable — those belong to where a datum is persisted,
// which is a data store's question (ADR-0036) and not this document's.

// SubsetVersion is the version of the tables below. It is a public contract — a
// palette and a rule the browser enforces — so a change to what is permitted is a
// version bump rather than a quiet redefinition.
const SubsetVersion = 1

// The stereotypes a class may carry. They are not decoration: almost every rule in
// the matrix below is about which of these three a class is.
const (
	// StereotypeBusinessObject is a thing the business tracks and that has an
	// identity of its own: an Order, a Customer, a Claim. The default.
	StereotypeBusinessObject = "businessObject"
	// StereotypeValueType is a structured value with no independent existence: an
	// Address, a Money amount. Two of them with equal contents *are* the same value,
	// which is why one cannot declare a business key and cannot own parts.
	StereotypeValueType = "valueType"
	// StereotypeEnumeration is a closed set of literals: an OrderStatus. It has
	// literals where the others have attributes, and it appears only as an
	// attribute's type — never at the end of an association.
	StereotypeEnumeration = "enumeration"
)

// The association kinds. Four, because these are the four that say something a
// reader can act on; UML's dependency, realization and usage say things about code
// rather than about a business record.
const (
	KindAssociation    = "association"
	KindAggregation    = "aggregation"
	KindComposition    = "composition"
	KindGeneralization = "generalization"
)

// The multiplicities an attribute or an association end may carry.
const (
	MultOptional = "0..1"
	MultOne      = "1"
	MultMany     = "0..*"
	MultAtLeast1 = "1..*"
)

// The primitive attribute types. Deliberately few and deliberately abstract: a
// process information model says a delivery date is a date, not that it is a
// DATETIME2(7) — the moment it says the latter it has become a storage schema.
const (
	TypeString   = "string"
	TypeNumber   = "number"
	TypeBoolean  = "boolean"
	TypeDate     = "date"
	TypeDateTime = "dateTime"
	TypeTime     = "time"
	TypeDuration = "duration"
)

// Why something was refused. The distinction is the point: RefusedOutOfSubset is a
// limit of this build and reads as "not yet"; RefusedByNotation is a fact about
// what these things are and reads as "no".
const (
	RefusedOutOfSubset = "out-of-subset"
	RefusedByNotation  = "notation"
)

// Refusal explains a rejected connection in words a modeler can act on.
type Refusal struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// StereotypeKind is one authorable class kind, as the palette offers it.
type StereotypeKind struct {
	Stereotype string `json:"stereotype"`
	Label      string `json:"label"`
	// Meaning is the sentence a modeler needs to choose correctly. It travels to the
	// browser because the difference between a business object and a value type is
	// the single most consequential choice in this metamodel, and a palette that only
	// lists three words makes it by accident.
	Meaning string `json:"meaning"`
	// HasIdentity reports whether this kind may declare a business key. Only a
	// business object may: identity is what makes Order#ORD-1 the same order in three
	// processes, and a value with no independent existence has none to declare.
	HasIdentity bool `json:"hasIdentity"`
	// HasAttributes is false for an enumeration, which carries literals instead.
	HasAttributes bool `json:"hasAttributes"`
}

var stereotypes = []StereotypeKind{
	{Stereotype: StereotypeBusinessObject, Label: "Business object", HasIdentity: true, HasAttributes: true,
		Meaning: "Something the business tracks and can point at: an order, a customer, a claim. " +
			"Two of them with identical contents are still two different things, which is why it " +
			"is the only kind that declares a business key."},
	{Stereotype: StereotypeValueType, Label: "Value type", HasIdentity: false, HasAttributes: true,
		Meaning: "A structured value with no existence of its own: an address, an amount with its " +
			"currency. Two with equal contents are the same value, so it has nothing to identify " +
			"and cannot own parts."},
	{Stereotype: StereotypeEnumeration, Label: "Enumeration", HasIdentity: false, HasAttributes: false,
		Meaning: "A closed set of named values: draft, approved, rejected. It is what an attribute " +
			"is typed as, never something a relationship points at."},
}

// AssociationKind is one drawable relationship.
type AssociationKind struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Rule is the sentence this implements, in plain words, so a refusal can say
	// *why* rather than only that it was refused.
	Rule string `json:"rule"`
	// Directed reports whether the two ends mean different things, so the canvas
	// knows whether reversing the arrow changes the statement.
	Directed bool `json:"directed"`
}

var associationKinds = []AssociationKind{
	{Kind: KindAssociation, Label: "Association", Directed: false,
		Rule: "Two things are related, and each end says how many of the other it sees."},
	{Kind: KindAggregation, Label: "Aggregation", Directed: true,
		Rule: "A whole groups parts that go on existing without it."},
	{Kind: KindComposition, Label: "Composition", Directed: true,
		Rule: "A whole owns its parts; delete the whole and the parts go with it."},
	{Kind: KindGeneralization, Label: "Generalization", Directed: true,
		Rule: "The specific thing is a kind of the general thing, and can be used wherever it can."},
}

// AssociationKinds exposes the relationship table. Everything else about the subset
// reaches a caller through AuthoringSubset, which is the payload the browser is
// served; this one is separate because the matrix is built from it. It returns the
// package's own slice; callers must not mutate it.
func AssociationKinds() []AssociationKind { return associationKinds }

// PrimitiveType is one built-in attribute type, with the JSON Schema it projects to
// (jsonschema.go reads this rather than keeping a second list).
type PrimitiveType struct {
	Type  string `json:"type"`
	Label string `json:"label"`
	// JSONType and JSONFormat are the projection: the draft 2020-12 type and the
	// format keyword this primitive becomes. A primitive with no format projects to
	// its bare type.
	JSONType   string `json:"jsonType"`
	JSONFormat string `json:"jsonFormat,omitempty"`
}

var primitives = []PrimitiveType{
	{Type: TypeString, Label: "Text", JSONType: "string"},
	{Type: TypeNumber, Label: "Number", JSONType: "number"},
	{Type: TypeBoolean, Label: "Yes/no", JSONType: "boolean"},
	{Type: TypeDate, Label: "Date", JSONType: "string", JSONFormat: "date"},
	{Type: TypeDateTime, Label: "Date and time", JSONType: "string", JSONFormat: "date-time"},
	{Type: TypeTime, Label: "Time of day", JSONType: "string", JSONFormat: "time"},
	{Type: TypeDuration, Label: "Duration", JSONType: "string", JSONFormat: "duration"},
}

// MultiplicityOption is one multiplicity, with what it means for a value.
type MultiplicityOption struct {
	Multiplicity string `json:"multiplicity"`
	Label        string `json:"label"`
	// Required and Collection are what the JSON Schema projection and the canvas
	// both need, derived here once rather than by parsing the string in two places.
	Required   bool `json:"required"`
	Collection bool `json:"collection"`
}

var multiplicities = []MultiplicityOption{
	{Multiplicity: MultOptional, Label: "Optional", Required: false, Collection: false},
	{Multiplicity: MultOne, Label: "Exactly one", Required: true, Collection: false},
	{Multiplicity: MultMany, Label: "Any number", Required: false, Collection: true},
	{Multiplicity: MultAtLeast1, Label: "One or more", Required: true, Collection: true},
}

// MultiplicityOf returns one multiplicity's meaning, and whether it is in the
// subset at all.
func MultiplicityOf(m string) (MultiplicityOption, bool) {
	for _, o := range multiplicities {
		if o.Multiplicity == m {
			return o, true
		}
	}
	return MultiplicityOption{}, false
}

// PrimitiveOf returns one primitive's projection, and whether the name is one.
func PrimitiveOf(t string) (PrimitiveType, bool) {
	for _, p := range primitives {
		if p.Type == t {
			return p, true
		}
	}
	return PrimitiveType{}, false
}

func knownStereotype(s string) bool {
	for _, k := range stereotypes {
		if k.Stereotype == s {
			return true
		}
	}
	return false
}

func stereotypeOf(s string) (StereotypeKind, bool) {
	for _, k := range stereotypes {
		if k.Stereotype == s {
			return k, true
		}
	}
	return StereotypeKind{}, false
}

func knownKind(k string) bool {
	for _, a := range associationKinds {
		if a.Kind == k {
			return true
		}
	}
	return false
}

// AllowAssociation is the matrix: may an association of this kind run from a class
// of one stereotype to a class of another? It is the single authority — the canvas
// is served it, the server applies it on write.
//
// It rules on *stereotypes*, not on individual classes, because every rule here is
// about what kind of thing each end is. Whether the two ends are the same class,
// and whether a generalization closes a cycle, are properties of a particular model
// rather than of the notation, so validation.go answers those.
func AllowAssociation(kind, fromStereotype, toStereotype string) (bool, *Refusal) {
	if !knownKind(kind) {
		return false, &Refusal{Reason: RefusedOutOfSubset, Message: "Atlas does not author " +
			kind + " relationships. This build draws associations, aggregations, compositions " +
			"and generalizations."}
	}
	from, ok := stereotypeOf(fromStereotype)
	if !ok {
		return false, &Refusal{Reason: RefusedOutOfSubset,
			Message: "Atlas does not author classes of kind " + fromStereotype + "."}
	}
	to, ok := stereotypeOf(toStereotype)
	if !ok {
		return false, &Refusal{Reason: RefusedOutOfSubset,
			Message: "Atlas does not author classes of kind " + toStereotype + "."}
	}

	// An enumeration is a set of values, so nothing relates *to* it and it relates to
	// nothing: it is what an attribute is typed as. This is the notation, not us.
	if from.Stereotype == StereotypeEnumeration || to.Stereotype == StereotypeEnumeration {
		return false, &Refusal{Reason: RefusedByNotation,
			Message: "An enumeration is a closed set of values, not something a relationship can " +
				"point at. Give one of these classes an attribute typed as the enumeration instead."}
	}

	switch kind {
	case KindGeneralization:
		// "Is-a" means the specific thing is usable wherever the general one is, so the
		// two have to be the same kind of thing. A value type that specialized a
		// business object would inherit an identity a value cannot have.
		if from.Stereotype != to.Stereotype {
			return false, &Refusal{Reason: RefusedByNotation,
				Message: "A " + from.Label + " cannot be a kind of a " + to.Label +
					". A specialization has to be usable everywhere the thing it specializes is, " +
					"so both ends must be the same kind of class."}
		}
	case KindComposition, KindAggregation:
		// The whole owns or groups parts, which presumes it is a thing you can point at
		// — a value has no independent existence to own anything with.
		if from.Stereotype != StereotypeBusinessObject {
			return false, &Refusal{Reason: RefusedByNotation,
				Message: "A " + from.Label + " has no existence of its own, so it cannot be the " +
					"whole that owns or groups parts. Make it a business object, or draw the " +
					"relationship the other way round."}
		}
	}
	return true, nil
}

// AllowedFrom lists the association kinds that may run from one stereotype to
// another — what the canvas offers in its connect menu, so it never offers a line
// the server would refuse.
func AllowedFrom(fromStereotype, toStereotype string) []string {
	out := []string{}
	for _, k := range associationKinds {
		if ok, _ := AllowAssociation(k.Kind, fromStereotype, toStereotype); ok {
			out = append(out, k.Kind)
		}
	}
	return out
}

// Subset is the whole authoring contract, as the browser is served it.
type Subset struct {
	Version          int                  `json:"version"`
	Notation         string               `json:"notation"`
	Stereotypes      []StereotypeKind     `json:"stereotypes"`
	AssociationKinds []AssociationKind    `json:"associationKinds"`
	Primitives       []PrimitiveType      `json:"primitives"`
	Multiplicities   []MultiplicityOption `json:"multiplicities"`
	// Matrix is AllowAssociation precomputed for every stereotype pair, so the canvas
	// can grey out a connection while it is being dragged without a round trip. Keyed
	// "from>to".
	Matrix map[string][]string `json:"matrix"`
	Limits []SubsetLimit       `json:"limits"`
}

// SubsetLimit is one thing this build does not author, and why. Stating them is
// what keeps "we do not do this yet" apart from "this is not a thing".
type SubsetLimit struct {
	Area   string `json:"area"`
	Reason string `json:"reason"`
}

var limits = []SubsetLimit{
	{
		Area: "Interfaces, operations and visibility",
		Reason: "A process information model describes the records a process moves, not the " +
			"software that handles them. Behaviour belongs to the BPMN model beside it.",
	},
	{
		Area: "N-ary associations, association classes and qualifiers",
		Reason: "Every relationship here has exactly two ends. A relationship that carries its " +
			"own attributes is modeled as a class between the two.",
	},
	{
		Area: "Profiles, stereotyped extensions and derived attributes",
		Reason: "The three stereotypes above are fixed, so the rules that follow from them can " +
			"be enforced. An open stereotype vocabulary would make the relationship matrix " +
			"unenforceable.",
	},
	{
		Area: "Storage detail — lengths, precision, nullability, keys, indexes",
		Reason: "Deliberately absent. Where a datum lives is the data store's question and is " +
			"settled per store; stating it here would turn a model of the business into a " +
			"schema for one database.",
	},
}

// AuthoringSubset builds the payload served to the browser.
func AuthoringSubset() Subset {
	matrix := map[string][]string{}
	for _, from := range stereotypes {
		for _, to := range stereotypes {
			matrix[from.Stereotype+">"+to.Stereotype] = AllowedFrom(from.Stereotype, to.Stereotype)
		}
	}
	return Subset{
		Version:          SubsetVersion,
		Notation:         "UML 2.5 class diagram (subset)",
		Stereotypes:      stereotypes,
		AssociationKinds: associationKinds,
		Primitives:       primitives,
		Multiplicities:   multiplicities,
		Matrix:           matrix,
		Limits:           limits,
	}
}
