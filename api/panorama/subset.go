package panorama

// The authoring subset: which ArchiMate elements Atlas can create, and which
// relationships it will let you draw between them (ADR-0189 §2, P2b).
//
// ADR-0189 is unusually specific here, and every line of it shapes this file:
//
//	The relationship matrix is semantic validation, not just a visual hint.
//	Invalid connections are blocked while authoring and reported during
//	import/validation. […] The UI states the implemented subset and must not
//	claim complete ArchiMate 3.2 authoring until the full conformance scope is
//	tested.
//
// So three things are true of what follows.
//
// **It is one table, not two.** The canvas has to refuse a connection while it is
// being dragged, and the server has to refuse it on write. Two copies of a
// relationship matrix is how you get a canvas that lets somebody draw an arrow the
// server then rejects — so the browser is served this table rather than carrying
// its own, and there is exactly one place a rule can be wrong.
//
// **It is a subset, and says so.** ArchiMate 3.2's full matrix is roughly forty
// element types against eleven relationship types, with derivation rules on top.
// This covers the elements the record names — Capability, Business Process, the
// core Application layer, and the Technology elements needed for artifacts, nodes,
// services and networks — and the relationships that are meaningful between them.
// Everything else is refused *as out of subset*, which is a different answer from
// "ArchiMate forbids this", and the two are kept apart because one is a limit of
// this build and the other is a fact about the notation.
//
// **A document may contain more than this can author.** Reading and round-tripping
// are unaffected: §2 requires that unsupported-but-standard content survives, and
// this table constrains only what Atlas will *create*.

// SubsetVersion is the version of the table below. It is a public contract — a
// palette and a rule the browser enforces — so a change to what is permitted is a
// version bump rather than a quiet redefinition.
const SubsetVersion = 1

// The ArchiMate layers this subset spans, in the order the palette shows them:
// motivation-free, top-down, the way the standard's own layer diagram reads.
const (
	LayerStrategy    = "strategy"
	LayerBusiness    = "business"
	LayerApplication = "application"
	LayerTechnology  = "technology"
)

// ElementKind is one authorable element type.
type ElementKind struct {
	// Type is the xsi:type written into the document — the standard's own name,
	// because the document is an interchange format and not Atlas's private store.
	Type string `json:"type"`
	// Label is what a person reads in the palette; Layer groups it there.
	Label string `json:"label"`
	Layer string `json:"layer"`
	// Aspect is the element's role in the standard's structure/behaviour split, and
	// it is what most of the relationship rules below are actually about. Carrying
	// it makes those rules readable as the sentences the specification states,
	// rather than as a list of type pairs somebody has to trust.
	Aspect string `json:"aspect"`
}

// The three aspects this subset needs. ArchiMate's fourth, motivation, is not in
// the authoring subset at all: a Goal or a Requirement relates to everything else
// through a different part of the matrix, and offering the elements without those
// rules would be offering a palette that cannot be connected to anything.
const (
	AspectActive   = "active"   // who or what performs behaviour
	AspectBehavior = "behavior" // what happens
	AspectPassive  = "passive"  // what is acted upon
)

// authorable is the subset, in palette order.
var authorable = []ElementKind{
	{Type: "Capability", Label: "Capability", Layer: LayerStrategy, Aspect: AspectBehavior},

	{Type: "BusinessActor", Label: "Business actor", Layer: LayerBusiness, Aspect: AspectActive},
	{Type: "BusinessRole", Label: "Business role", Layer: LayerBusiness, Aspect: AspectActive},
	{Type: "BusinessProcess", Label: "Business process", Layer: LayerBusiness, Aspect: AspectBehavior},
	{Type: "BusinessService", Label: "Business service", Layer: LayerBusiness, Aspect: AspectBehavior},
	{Type: "BusinessObject", Label: "Business object", Layer: LayerBusiness, Aspect: AspectPassive},

	{Type: "ApplicationComponent", Label: "Application component", Layer: LayerApplication, Aspect: AspectActive},
	{Type: "ApplicationCollaboration", Label: "Application collaboration", Layer: LayerApplication, Aspect: AspectActive},
	{Type: "ApplicationInterface", Label: "Application interface", Layer: LayerApplication, Aspect: AspectActive},
	{Type: "ApplicationFunction", Label: "Application function", Layer: LayerApplication, Aspect: AspectBehavior},
	{Type: "ApplicationProcess", Label: "Application process", Layer: LayerApplication, Aspect: AspectBehavior},
	{Type: "ApplicationService", Label: "Application service", Layer: LayerApplication, Aspect: AspectBehavior},
	{Type: "DataObject", Label: "Data object", Layer: LayerApplication, Aspect: AspectPassive},

	{Type: "Node", Label: "Node", Layer: LayerTechnology, Aspect: AspectActive},
	{Type: "Device", Label: "Device", Layer: LayerTechnology, Aspect: AspectActive},
	{Type: "SystemSoftware", Label: "System software", Layer: LayerTechnology, Aspect: AspectActive},
	{Type: "TechnologyInterface", Label: "Technology interface", Layer: LayerTechnology, Aspect: AspectActive},
	{Type: "CommunicationNetwork", Label: "Communication network", Layer: LayerTechnology, Aspect: AspectActive},
	{Type: "TechnologyService", Label: "Technology service", Layer: LayerTechnology, Aspect: AspectBehavior},
	{Type: "Artifact", Label: "Artifact", Layer: LayerTechnology, Aspect: AspectPassive},
}

// RelationshipKind is one authorable relationship type.
type RelationshipKind struct {
	Type  string `json:"type"`
	Label string `json:"label"`
	// Rule is the sentence from the specification this implements, in plain words.
	// It travels to the browser so a refusal can say *why* rather than only that it
	// was refused: "you cannot draw that" teaches nobody the notation.
	Rule string `json:"rule"`
}

// drawable is the relationship subset, in the order the connect menu offers them:
// the structural ones first, then the dynamic ones, then the two that relate
// anything to anything.
var drawable = []RelationshipKind{
	{Type: "Composition", Label: "Composition",
		Rule: "A whole is composed of parts of the same kind of thing; a part belongs to one whole."},
	{Type: "Aggregation", Label: "Aggregation",
		Rule: "A whole groups parts of the same kind of thing, which may belong to several wholes."},
	{Type: "Assignment", Label: "Assignment",
		Rule: "Something active performs behaviour, or holds what is deployed on it."},
	{Type: "Realization", Label: "Realization",
		Rule: "Something concrete makes something more abstract real."},
	{Type: "Serving", Label: "Serving",
		Rule: "Behaviour or an interface provides its services to something that uses them."},
	{Type: "Access", Label: "Access",
		Rule: "Behaviour reads or writes something passive."},
	{Type: "Triggering", Label: "Triggering",
		Rule: "One piece of behaviour causes another to happen."},
	{Type: "Flow", Label: "Flow",
		Rule: "Something passes from one piece of behaviour to another."},
	{Type: "Specialization", Label: "Specialization",
		Rule: "One element is a particular kind of another element of the same type."},
	{Type: "Association", Label: "Association",
		Rule: "Two elements are related in a way the other relationships do not capture."},
}

// AuthorableElements returns the element subset, in palette order.
func AuthorableElements() []ElementKind {
	return append([]ElementKind(nil), authorable...)
}

// DrawableRelationships returns the relationship subset, in menu order.
func DrawableRelationships() []RelationshipKind {
	return append([]RelationshipKind(nil), drawable...)
}

// kindByType indexes the element subset.
var kindByType = func() map[string]ElementKind {
	byType := make(map[string]ElementKind, len(authorable))
	for _, kind := range authorable {
		byType[kind.Type] = kind
	}
	return byType
}()

// relationshipRule is the predicate for one relationship type.
type relationshipRule func(source, target ElementKind) bool

// sameLayer is the structural constraint composition and aggregation carry: a
// business process is not part of an application component, however much a diagram
// might want to draw it that way. Crossing layers is what realization and serving
// are for, and offering composition instead would let somebody express a
// containment the standard does not have.
func sameLayer(source, target ElementKind) bool { return source.Layer == target.Layer }

// rules is the matrix, written as the specification's own sentences rather than as
// a table of type pairs. A pair table for twenty element types is four hundred
// cells nobody can check; a predicate over aspect and layer is the rule itself, and
// a reader can tell whether it matches the standard by reading it.
var rules = map[string]relationshipRule{
	// Structural. Composition and aggregation group like with like; assignment and
	// realization are the two that legitimately cross.
	"Composition": func(source, target ElementKind) bool {
		return sameLayer(source, target) && source.Aspect == target.Aspect
	},
	"Aggregation": func(source, target ElementKind) bool {
		return sameLayer(source, target) && source.Aspect == target.Aspect
	},
	"Assignment": func(source, target ElementKind) bool {
		if source.Aspect != AspectActive {
			return false
		}
		// Active structure performs behaviour — and, in the technology layer, holds
		// what is deployed on it: a node assigned to an artifact is how the standard
		// says "this runs here".
		if target.Aspect == AspectBehavior {
			return true
		}
		return source.Layer == LayerTechnology && target.Aspect == AspectPassive
	},
	"Realization": func(source, target ElementKind) bool {
		// More concrete realizes more abstract, which in this subset means a lower
		// layer realizing a higher one, or active structure realizing behaviour
		// within a layer — an application component realizing a service.
		if layerRank(source.Layer) > layerRank(target.Layer) {
			return true
		}
		return source.Layer == target.Layer &&
			source.Aspect != AspectBehavior && target.Aspect == AspectBehavior
	},
	"Serving": func(source, target ElementKind) bool {
		// A service or an interface serves whatever uses it. Neither end may be
		// passive — something acted upon does not serve, and is not served.
		if source.Aspect == AspectPassive || target.Aspect == AspectPassive {
			return false
		}
		// And it does not run downhill. Technology serves the application layer and
		// the application layer serves the business, never the other way round: a
		// business process serving a node is not a statement the standard has, and
		// allowing it would let Atlas author ArchiMate that is not valid — which is
		// the failure this matrix exists to prevent (ADR-0189 §2).
		return layerRank(source.Layer) >= layerRank(target.Layer)
	},
	"Access": func(source, target ElementKind) bool {
		return source.Aspect == AspectBehavior && target.Aspect == AspectPassive
	},
	// Dynamic. Both ends must be behaviour: an application component does not
	// trigger another component, its behaviour does.
	"Triggering": func(source, target ElementKind) bool {
		return source.Aspect == AspectBehavior && target.Aspect == AspectBehavior
	},
	"Flow": func(source, target ElementKind) bool {
		return source.Aspect == AspectBehavior && target.Aspect == AspectBehavior
	},
	// Specialization is the one rule with no room in it: a thing can only be a
	// particular kind of the same thing.
	"Specialization": func(source, target ElementKind) bool { return source.Type == target.Type },
	// And association relates anything to anything, which is what it is for.
	"Association": func(ElementKind, ElementKind) bool { return true },
}

// layerRank orders the layers from most concrete to most abstract, which is the
// direction realization runs in.
func layerRank(layer string) int {
	switch layer {
	case LayerTechnology:
		return 3
	case LayerApplication:
		return 2
	case LayerBusiness:
		return 1
	case LayerStrategy:
		return 0
	}
	return -1
}

// The reasons a connection is refused. They are told apart because they send
// somebody to different places: one is a gap in this build, the others are facts
// about the notation and about the document.
const (
	// RefusedOutOfSubset: Atlas does not author this element or relationship type
	// yet. The document may legitimately contain it, and reading one is unaffected.
	RefusedOutOfSubset = "out-of-subset"
	// RefusedByNotation: ArchiMate does not permit this relationship between these
	// two elements. No version of Atlas will allow it.
	RefusedByNotation = "not-in-archimate"
	// RefusedSelfReference: an element related to itself. The standard permits it
	// for a few relationships; Atlas does not author it, because on a canvas it is
	// almost always a mis-drop rather than a statement.
	RefusedSelfReference = "self-reference"
)

// Refusal is why a connection may not be drawn, in a form a canvas can show.
type Refusal struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// MayConnect reports whether this subset will author a relationship of this type
// between these two element types, and why not when it will not.
//
// Both the canvas and the write path call it, which is the point: a rule that lives
// in one place cannot disagree with itself.
func MayConnect(relationship, sourceType, targetType string) (bool, *Refusal) {
	rule, known := rules[relationship]
	if !known {
		return false, &Refusal{Reason: RefusedOutOfSubset, Message: "Atlas does not author " +
			relationship + " relationships yet. A document may contain them, and reading one is unaffected."}
	}
	source, sourceKnown := kindByType[sourceType]
	target, targetKnown := kindByType[targetType]
	switch {
	case !sourceKnown:
		return false, &Refusal{Reason: RefusedOutOfSubset,
			Message: "Atlas does not author " + sourceType + " elements yet, so it will not draw relationships from one."}
	case !targetKnown:
		return false, &Refusal{Reason: RefusedOutOfSubset,
			Message: "Atlas does not author " + targetType + " elements yet, so it will not draw relationships to one."}
	}
	if !rule(source, target) {
		return false, &Refusal{Reason: RefusedByNotation, Message: notationMessage(relationship, source, target)}
	}
	return true, nil
}

// MayConnectElements is MayConnect with the two concrete elements, which is what a
// canvas actually has. It adds the one rule that is about identity rather than
// type: nothing is related to itself.
func MayConnectElements(relationship, sourceID, sourceType, targetID, targetType string) (bool, *Refusal) {
	if sourceID != "" && sourceID == targetID {
		return false, &Refusal{Reason: RefusedSelfReference,
			Message: "An element cannot be related to itself."}
	}
	return MayConnect(relationship, sourceType, targetType)
}

// notationMessage says why the standard refuses this pair, in the terms the rule
// was written in. A refusal that only says "not allowed" teaches nobody the
// notation, and this canvas is the place most people will meet it.
func notationMessage(relationship string, source, target ElementKind) string {
	subject := source.Label + " → " + target.Label + ": "
	switch relationship {
	case "Composition", "Aggregation":
		return subject + "a whole and its parts are the same kind of thing in the same layer. " +
			"Use realization or serving to cross between layers."
	case "Assignment":
		return subject + "assignment goes from something active to the behaviour it performs, " +
			"or from technology to what is deployed on it."
	case "Realization":
		return subject + "realization runs from the more concrete to the more abstract."
	case "Serving":
		return subject + "serving runs from what offers a capability to what uses it — " +
			"technology serves applications, applications serve the business — and never " +
			"to or from something passive."
	case "Access":
		return subject + "access runs from behaviour to something passive that it reads or writes."
	case "Triggering", "Flow":
		return subject + relationship + " relates one piece of behaviour to another. " +
			"A component does not trigger a component; its behaviour does."
	case "Specialization":
		return subject + "an element can only specialize another element of the same type."
	}
	return subject + "ArchiMate does not permit this relationship between these elements."
}

// AllowedBetween lists the relationship types this subset will author between two
// element types, in menu order. It is what a connect menu offers, so that the menu
// only ever contains choices that will be accepted.
func AllowedBetween(sourceType, targetType string) []string {
	var allowed []string
	for _, kind := range drawable {
		if ok, _ := MayConnect(kind.Type, sourceType, targetType); ok {
			allowed = append(allowed, kind.Type)
		}
	}
	return allowed
}

// Subset is the whole contract, as one document for the browser: what may be
// created, what may be drawn, and the statement that it is a subset.
type Subset struct {
	Version       int                `json:"version"`
	Elements      []ElementKind      `json:"elements"`
	Relationships []RelationshipKind `json:"relationships"`
	// Matrix is the permitted relationship types for every ordered pair of element
	// types in the subset, keyed "Source>Target". It is precomputed and sent whole
	// rather than asked for pair by pair: the canvas needs an answer during a drag,
	// and a network round trip per pointer move is not an answer.
	Matrix map[string][]string `json:"matrix"`
	// Limits says what this subset is not, in the same shape every other Panorama
	// answer publishes its limits.
	Limits []SubsetLimit `json:"limits"`
}

// SubsetLimit is one thing this subset does not do, with the reason.
type SubsetLimit struct {
	Limit  string `json:"limit"`
	Reason string `json:"reason"`
}

// subsetLimits is what every copy of the contract carries. ADR-0189 requires the
// UI to state the implemented subset and forbids claiming complete ArchiMate 3.2
// authoring; publishing the limits with the table is how that survives somebody
// reading only the palette.
var subsetLimits = []SubsetLimit{
	{
		Limit: "an authoring subset, not all of ArchiMate 3.2",
		Reason: "The standard has around forty element types and eleven relationship types " +
			"with derivation rules on top. Atlas authors the elements and relationships " +
			"listed here and refuses the rest as out of subset — which is not the same as " +
			"the notation forbidding them.",
	},
	{
		Limit: "reading is not restricted by it",
		Reason: "A document may contain anything the standard allows. This constrains only " +
			"what Atlas creates; unsupported content round-trips untouched.",
	},
	{
		Limit: "no motivation elements",
		Reason: "A goal or a requirement relates to the rest of a model through a different " +
			"part of the matrix. Offering the elements without those rules would be a " +
			"palette that cannot be connected to anything.",
	},
	{
		Limit: "no derived relationships",
		Reason: "The standard permits a relationship implied by a chain of others. Atlas " +
			"authors only relationships that are directly permitted, so nothing is drawn " +
			"whose justification is somewhere else in the model.",
	},
}

// AuthoringSubset builds the contract document.
func AuthoringSubset() Subset {
	matrix := map[string][]string{}
	for _, source := range authorable {
		for _, target := range authorable {
			if allowed := AllowedBetween(source.Type, target.Type); len(allowed) > 0 {
				matrix[source.Type+">"+target.Type] = allowed
			}
		}
	}
	return Subset{
		Version:       SubsetVersion,
		Elements:      AuthorableElements(),
		Relationships: DrawableRelationships(),
		Matrix:        matrix,
		Limits:        subsetLimits,
	}
}
