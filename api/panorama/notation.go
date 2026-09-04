package panorama

// The notations the derived landscape can be spoken in (ADR-0211 §8).
//
// §8 allows a read-only projection with an explicit versioned mapping and reported
// loss, and forbids the renderer toggle it would otherwise become. The tables below
// are that mapping, and they live here — in Go, served to the browser — rather than
// in the view that draws with them, for the reason ADR-0189's connection subset
// already gives: a table the server and the browser each keep a copy of is a table
// that eventually disagrees with itself, and here the disagreement would be a
// picture calling a node an Application Process beside a file calling it something
// else.
//
// Three consumers read one table: the landscape's labels and legend, the stamp on
// its SVG/PNG export, and the ArchiMate document generated from it.

// NotationMappingVersion identifies the tables below. §8 requires a projection's
// mapping to be explicit *and* versioned: a reader who saw a picture last quarter
// has to be able to tell whether it would be drawn the same way today. Bump it
// whenever a row changes meaning.
const NotationMappingVersion = 1

const (
	// NotationAtlas is the landscape drawn as itself: Atlas's own kinds, no
	// projection, nothing to declare.
	NotationAtlas = "atlas"
	// NotationC4 is the C4 projection. It is not a peer notation and never an
	// authoring one — ADR-0211 §8's "projection id that is not a peer notation".
	NotationC4 = "c4-projection"
)

// NotationType is what one notation calls one mesh kind.
//
// Two fields because the two readers need different things from the same row. Name
// is what a person is shown — "Application Component", spaced the way the standard
// prints it — and Type is the notation's own machine token, which for ArchiMate is
// the xsi:type an exchange document must carry and for C4 does not exist, because C4
// has no interchange format here. One row, so a picture and a file can never call
// the same node two different things.
type NotationType struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// Notation is one vocabulary the landscape can be drawn in.
//
// Types maps a mesh node kind to what this notation calls it. A kind that is
// *absent* from the map has no counterpart, and that absence is deliberate: the node
// keeps its derived shape and the loss list names it. Inventing a row would be the
// silent drop §8's theme ban exists to prevent.
type Notation struct {
	ID             string                  `json:"id"`
	Label          string                  `json:"label"`
	Short          string                  `json:"short"`
	Projection     bool                    `json:"projection"`
	MappingVersion int                     `json:"mappingVersion"`
	Types          map[string]NotationType `json:"types"`
	Loss           []string                `json:"loss"`
}

var notations = []Notation{
	{
		ID: NotationAtlas, Label: "Atlas (derived)", Short: "Atlas",
		Projection: false, MappingVersion: NotationMappingVersion,
		Types: map[string]NotationType{}, Loss: []string{},
	},
	{
		ID: NotationArchiMate32, Label: "ArchiMate 3.2", Short: "ArchiMate",
		Projection: true, MappingVersion: NotationMappingVersion,
		Types: map[string]NotationType{
			KindApplication: {Name: "Application Component", Type: "ApplicationComponent"},
			KindProcess:     {Name: "Application Process", Type: "ApplicationProcess"},
			KindWorker:      {Name: "Application Service", Type: "ApplicationService"},
			KindDecision:    {Name: "Application Function", Type: "ApplicationFunction"},
			KindTarget:      {Name: "Node", Type: "Node"},
		},
		Loss: []string{
			"Nothing here was modelled. This is Atlas's own resources in ArchiMate's vocabulary — a picture, and a document generated from it, neither of which anybody drew.",
			"Relationships are derived from two facts. ArchiMate tells serving from triggering from assignment; the starmap knows only that a process calls another and that it uses a worker or a decision, and the export picks one ArchiMate type per fact.",
			"A worker becomes an Application Service with nothing behind it. Atlas holds the worker's name and type and never what is on the other side, so there is no Technology Service to realize it.",
			"Restricted placeholders have no ArchiMate element — they stand for resources this reader may not see, which is a fact about the reader rather than about the architecture — and are absent from the exported document.",
			"The type is written out rather than drawn as ArchiMate's corner icon.",
		},
	},
	{
		ID: NotationC4, Label: "C4 (projection)", Short: "C4",
		Projection: true, MappingVersion: NotationMappingVersion,
		Types: map[string]NotationType{
			KindApplication: {Name: "Container"},
			KindProcess:     {Name: "Component"},
			KindWorker:      {Name: "Component"},
			KindDecision:    {Name: "Component"},
			KindTarget:      {Name: "Deployment Node"},
		},
		Loss: []string{
			"C4 separates its levels onto different diagrams. This canvas shows containers and components together, which no C4 level does.",
			"External systems are absent. C4 puts the thing a component talks to on the diagram; Atlas holds no model of what is behind a worker, only its name and type.",
			"Relationships carry no technology or protocol label, which is most of what a C4 arrow is for.",
			"Restricted and unresolved placeholders have no C4 element and keep their own shape.",
			"There is no Person and no Software System: the starmap is derived from what this server runs, and neither is a thing Atlas holds.",
		},
	},
}

// Notations returns every vocabulary the landscape can be drawn in, in the order the
// picker offers them. The slice is copied: a caller must not be able to edit the
// mapping the next request is answered from.
func Notations() []Notation {
	out := make([]Notation, len(notations))
	copy(out, notations)
	return out
}

// NotationByID resolves a notation id. An unknown id is a stale saved view or a
// hand-edited URL, and the derived vocabulary is the answer that cannot mislead.
func NotationByID(id string) (Notation, bool) {
	for _, n := range notations {
		if n.ID == id {
			return n, true
		}
	}
	return notations[0], false
}

// archiRelation is how one derived edge is written as an ArchiMate relationship.
//
// Flip is the detail that matters and the one a reader would otherwise find
// surprising: ArchiMate's Serving runs from the provider to the consumer, and the
// landscape's `uses` edge runs the other way — a process points at the worker it
// needs. The exported relationship is therefore reversed, so that "the mail worker
// serves the invoice process" is what the document says.
type archiRelation struct {
	Type string
	Flip bool
}

var archiRelations = map[string]archiRelation{
	// An application component is *assigned to* the behaviour it performs. It does
	// not compose it: composition is for elements of one kind, and a component and a
	// process are not.
	EdgeContains: {Type: "Assignment"},
	// A call activity is one behaviour invoking another, which is Triggering.
	EdgeCalls: {Type: "Triggering"},
	EdgeUses:  {Type: "Serving", Flip: true},
}
