// Package infomodel holds Atlas's process information model: a UML class-diagram
// subset that gives BPMN's data objects a type they can share across processes
// (ADR-draft-process-information-model).
//
// BPMN scopes a data object to one process definition and leaves its type slot —
// `itemSubjectRef` → `<itemDefinition structureRef="…">` — deliberately opaque,
// pointing "into some other schema language". So two processes that both handle an
// order have two unrelated strings named `order`, and nothing anywhere says they
// mean the same thing. This package is the other schema language: an
// application-owned document of classes, their attributes, and the associations
// between them, against which `itemSubjectRef` resolves.
//
// It models *concepts, not storage.* An entity-relationship diagram's whole
// vocabulary — entities, columns, foreign keys — is persistence, and drawing one
// would assert a decision nobody has made. Where a datum is persisted is the data
// store's question (ADR-0036), settled per store; this is about what a datum *is*.
package infomodel

// Model is one application-owned information model. Unlike a Panorama document —
// whose canonical form is the standard's own interchange XML — this one is stored
// in its native shape and *projected* to its notations: a UML class diagram to
// read, and a JSON Schema to validate against. A projection is derived, never
// authored, and states what it dropped.
type Model struct {
	ID            string `json:"id"`
	ApplicationID string `json:"applicationId"`
	Name          string `json:"name"`
	Documentation string `json:"documentation,omitempty"`
	// Revision guards a concurrent overwrite: a write states the revision it read,
	// and a write against a stale one is refused rather than silently winning.
	Revision     int64         `json:"revision"`
	Classes      []Class       `json:"classes"`
	Associations []Association `json:"associations"`
	CreatedAt    int64         `json:"createdAt"`
	CreatedBy    string        `json:"createdBy,omitempty"`
	UpdatedAt    int64         `json:"updatedAt"`
	UpdatedBy    string        `json:"updatedBy,omitempty"`
}

// Class is one business object type — the thing a BPMN data object's
// `itemSubjectRef` names.
type Class struct {
	// ID is stable across renames, so an association survives its class being
	// renamed; Name is what a model and a diagram refer to, and what
	// `itemSubjectRef` matches.
	ID            string `json:"id"`
	Name          string `json:"name"`
	Documentation string `json:"documentation,omitempty"`
	// Stereotype says what kind of type this is, and it is what most of the
	// relationship rules in subset.go are actually about — see the constants there.
	Stereotype string      `json:"stereotype"`
	Attributes []Attribute `json:"attributes"`
	// Literals are the members of an «enumeration», which has literals where every
	// other stereotype has attributes. Empty otherwise.
	Literals []string `json:"literals,omitempty"`
	// Identity names the attributes that together form the business key: the fact
	// that makes Order#ORD-1 the same order in three processes and in a data store.
	// It is the part BPMN has no equivalent for, and every cross-process capability
	// rests on it. Empty for a class whose instances have no identity of their own.
	Identity []string `json:"identity,omitempty"`
	// X and Y place the class on the canvas. Layout is part of the document because
	// a diagram a person arranged is a diagram they can read again.
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Attribute is one typed member of a class.
type Attribute struct {
	Name string `json:"name"`
	// Type is a primitive (see PrimitiveTypes) or the name of another class in the
	// same model — including an «enumeration», which is how a closed set of values
	// is declared.
	Type          string `json:"type"`
	Multiplicity  string `json:"multiplicity"`
	Documentation string `json:"documentation,omitempty"`
}

// Association relates two classes. Its Kind carries the meaning; the ends carry
// the reading — a role name and a multiplicity each, so the diagram states
// "a Customer places 0..* Orders" rather than only that a line exists.
type Association struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Kind string `json:"kind"`
	// From is the source end. For a generalization it is the *specific* class ("an
	// ExpressOrder is an Order"); for a composition or aggregation it is the whole.
	From End `json:"from"`
	To   End `json:"to"`
}

// End is one side of an association: which class, what it is called from the other
// side, and how many of it there are.
type End struct {
	ClassID      string `json:"classId"`
	Role         string `json:"role,omitempty"`
	Multiplicity string `json:"multiplicity,omitempty"`
}

// Summary is the listing representation: everything a library row shows, without
// the classes and associations a canvas needs.
type Summary struct {
	ID            string `json:"id"`
	ApplicationID string `json:"applicationId"`
	Name          string `json:"name"`
	Documentation string `json:"documentation,omitempty"`
	Revision      int64  `json:"revision"`
	Classes       int    `json:"classes"`
	Associations  int    `json:"associations"`
	CreatedAt     int64  `json:"createdAt"`
	CreatedBy     string `json:"createdBy,omitempty"`
	UpdatedAt     int64  `json:"updatedAt"`
	UpdatedBy     string `json:"updatedBy,omitempty"`
}

func summarize(m Model) Summary {
	return Summary{
		ID: m.ID, ApplicationID: m.ApplicationID, Name: m.Name,
		Documentation: m.Documentation, Revision: m.Revision,
		Classes: len(m.Classes), Associations: len(m.Associations),
		CreatedAt: m.CreatedAt, CreatedBy: m.CreatedBy,
		UpdatedAt: m.UpdatedAt, UpdatedBy: m.UpdatedBy,
	}
}

// ClassByName finds a class by the name a model refers to it with, which is what
// a BPMN `itemSubjectRef` carries. Names are unique within a model (validation.go
// enforces it), so the first match is the only one.
func (m *Model) ClassByName(name string) (*Class, bool) {
	for i := range m.Classes {
		if m.Classes[i].Name == name {
			return &m.Classes[i], true
		}
	}
	return nil, false
}

// ClassByID finds a class by its stable id — how an association names its ends.
func (m *Model) ClassByID(id string) (*Class, bool) {
	for i := range m.Classes {
		if m.Classes[i].ID == id {
			return &m.Classes[i], true
		}
	}
	return nil, false
}
