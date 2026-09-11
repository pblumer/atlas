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
//
// Serving the relationship table did not bump it, and that is a judgment rather than
// an oversight. No row changed meaning: the three relationship types were already
// what the exporter wrote at version 1, and a document generated then is what a
// document generated now would be, relationship for relationship. What changed is
// who can read the table — the canvas as well as the exporter — and a version that
// moved for that would say a document might differ when it cannot.
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

// NotationRelation is what one notation calls one derived edge kind, and which way
// round it runs.
//
// The same one-row rule as NotationType, for the same reason and one more: the edge
// is now drawn as well as written. A picture that put Triggering's filled arrowhead
// on an edge the exported document calls Serving would be two answers to one
// question, and the reader would have no way of telling which was the true one.
//
// Flip says the notation's relationship runs opposite to the derived edge. It is a
// fact about the vocabulary rather than about the drawing — ArchiMate's Serving runs
// from the provider to the consumer and the landscape's `uses` edge runs from the
// consumer to the provider — so it belongs in this table, and the export and the
// canvas each read it rather than each deciding it.
type NotationRelation struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	Flip bool   `json:"flip,omitempty"`
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
	// Relations is the same for the edges between those kinds. A kind absent from it
	// is drawn as the plain derived line and named in the loss list, exactly as an
	// absent node kind keeps its derived outline.
	Relations map[string]NotationRelation `json:"relations"`
	Loss      []string                    `json:"loss"`
}

var notations = []Notation{
	{
		ID: NotationAtlas, Label: "Atlas (derived)", Short: "Atlas",
		Projection: false, MappingVersion: NotationMappingVersion,
		Types: map[string]NotationType{}, Relations: map[string]NotationRelation{}, Loss: []string{},
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
		// Derived from archiRelations rather than written out again: the export picks
		// the relationship type from that table and the canvas draws its notation from
		// this one, and one table feeding both is what stops the file and the picture
		// from disagreeing about which relationship an edge is.
		Relations: archiNotationRelations(),
		Loss: []string{
			"Nothing here was modelled. This is Atlas's own resources in ArchiMate's vocabulary — a picture, and a document generated from it, neither of which anybody drew.",
			"Relationships are derived from two facts, and three is all there can ever be. ArchiMate tells eleven relationships apart; the starmap knows only that an application holds a process, that a process calls another, and that a process uses a worker or a decision. Those three are drawn and exported as Assignment, Triggering and Serving. Nothing here is a Flow, an Access, a Realization, an Aggregation or a Composition, because the facts that would distinguish them are not held — so an absent relationship type means Atlas cannot see one, never that there is none.",
			"A Serving relationship is drawn pointing the other way from the fact it comes from. ArchiMate's Serving runs from the provider to the consumer, and the landscape's edge runs from the process to the worker it needs. The arrowhead is therefore on the process, so that the picture says \"the mail worker serves the invoice process\" — the same reversal the exported document makes.",
			"A worker becomes an Application Service with nothing behind it. Atlas holds the worker's name and type and never what is on the other side, so there is no Technology Service to realize it.",
			"Restricted placeholders have no ArchiMate element — they stand for resources this reader may not see, which is a fact about the reader rather than about the architecture — and are absent from the exported document.",
			"Drafts are absent too. The document describes what this server runs; a saved diagram nobody has deployed is a plan, and giving it the same element type as a deployed process would put an intention into a file that reads as a record.",
			"The picture uses ArchiMate's icon-only notation — the element's own symbol at full size with the name beneath it — rather than the boxed notation with the type icon in the corner. Both are the standard's; at the size a node is drawn here a corner icon would be a pixel or two, so the icon is the node and the type is written under the name as well.",
			"The layer colours are convention, not standard. ArchiMate 3.2 states that colour carries no formal semantics and defines none; the fills here are the ones the specification's own figures and the Archi tool use, so a reader recognises the layers at a glance. Nothing in the exported document depends on them.",
			"Only the five kinds this table names are drawn as ArchiMate elements. A draft, a restricted placeholder and an unresolved dependency keep Atlas's own shape and colour, because ArchiMate has no element for them and dressing them as one would be a claim the notation does not make.",
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
		// C4 has no relationship types to map to. Its arrow is one arrow, and what
		// tells two of them apart is the technology label Atlas does not hold — which
		// the loss list already says.
		Relations: map[string]NotationRelation{},
		Loss: []string{
			"C4 separates its levels onto different diagrams. This canvas shows containers and components together, which no C4 level does.",
			"External systems are absent. C4 puts the thing a component talks to on the diagram; Atlas holds no model of what is behind a worker, only its name and type.",
			"Relationships carry no technology or protocol label, which is most of what a C4 arrow is for.",
			"Restricted and unresolved placeholders have no C4 element and keep their own shape. So does a draft: C4 describes a system that exists, and a diagram nobody has deployed is not part of one.",
			"There is no Person and no Software System: the starmap is derived from what this server runs, and neither is a thing Atlas holds.",
		},
	},
}

// Notations returns every vocabulary the landscape can be drawn in, in the order the
// picker offers them. Copied, not shared: a caller must not be able to edit the
// mapping the next request is answered from.
func Notations() []Notation {
	out := make([]Notation, len(notations))
	for i, n := range notations {
		out[i] = n.clone()
	}
	return out
}

// NotationByID resolves a notation id. An unknown id is a stale saved view or a
// hand-edited URL, and the derived vocabulary is the answer that cannot mislead.
func NotationByID(id string) (Notation, bool) {
	for _, n := range notations {
		if n.ID == id {
			return n.clone(), true
		}
	}
	return notations[0].clone(), false
}

// clone is what makes "copied" true of the whole row rather than of its header.
//
// Copying the struct copies the maps' and the slice's headers, so a caller handed one
// could reach through and edit the table every later request is answered from — and
// the edit would be invisible, because nothing here reads the table back to check it.
// The rows are three and small; a deep copy per call costs nothing a request notices
// and removes a class of bug that would present as the mapping being wrong.
func (n Notation) clone() Notation {
	out := n
	out.Types = make(map[string]NotationType, len(n.Types))
	for kind, t := range n.Types {
		out.Types[kind] = t
	}
	out.Relations = make(map[string]NotationRelation, len(n.Relations))
	for edge, r := range n.Relations {
		out.Relations[edge] = r
	}
	out.Loss = append([]string(nil), n.Loss...)
	return out
}

// archiRelation is how one derived edge is written as an ArchiMate relationship.
//
// Flip is the detail that matters and the one a reader would otherwise find
// surprising: ArchiMate's Serving runs from the provider to the consumer, and the
// landscape's `uses` edge runs the other way — a process points at the worker it
// needs. The exported relationship is therefore reversed, so that "the mail worker
// serves the invoice process" is what the document says.
type archiRelation struct {
	// Name is what a person is shown — the relationship spelled the way the standard
	// prints it — and Type is the xsi:type an exchange document carries. Two fields
	// for the same two readers NotationType has.
	Name string
	Type string
	Flip bool
}

var archiRelations = map[string]archiRelation{
	// An application component is *assigned to* the behaviour it performs. It does
	// not compose it: composition is for elements of one kind, and a component and a
	// process are not.
	EdgeContains: {Name: "Assignment", Type: "Assignment"},
	// A call activity is one behaviour invoking another, which is Triggering.
	EdgeCalls: {Name: "Triggering", Type: "Triggering"},
	EdgeUses:  {Name: "Serving", Type: "Serving", Flip: true},
}

// archiNotationRelations is archiRelations as the served table.
//
// A second shape of the same rows rather than a second list of them: the exporter
// reads archiRelations and the canvas is served this, and a test holds the two to
// being the same table. Writing the relationships out twice is how the picture and
// the file would eventually come to call one edge two things.
func archiNotationRelations() map[string]NotationRelation {
	out := make(map[string]NotationRelation, len(archiRelations))
	for edge, rel := range archiRelations {
		out[edge] = NotationRelation{Name: rel.Name, Type: rel.Type, Flip: rel.Flip}
	}
	return out
}
