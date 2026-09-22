package rungraph

import (
	"fmt"
	"sort"

	"github.com/pblumer/atlas/model"
)

// Axis is what a cloud groups by. ADR-0404 §5 names five dimensions the nodes "already
// carry" — definition, element, worker, incident state, time bucket — and measured against
// [model.ElementInstanceValue] the node carries two of them, plus the BPMN element type as a
// refinement of the second. Those three are here. The other three are a join and a per-node
// array of 4 bytes — 420 MB at 110 M nodes, the same budget unit whose doubling §5's
// membership decision refused — so they are a cost for the record to weigh rather than a
// default to ship.
//
// The zero value is not an axis: a cloud grouped by whatever a switch's default happens to
// be would report a density over something the caller did not ask for.
type Axis uint8

const (
	// ByDefinition groups by ProcessDefKey: how much of the graph belongs to each
	// deployed process definition.
	ByDefinition Axis = iota + 1
	// ByElement groups by an element *within* its definition. ElementId is an index into
	// the compiled graph rather than a global identifier, so the definition is part of the
	// cell: without it, element 1 of two processes would be one cell describing neither.
	ByElement
	// ByElementType groups by BpmnElementType — how much of the graph is service tasks,
	// how much is timers, and so on, across every definition.
	ByElementType
)

func (a Axis) String() string {
	switch a {
	case ByDefinition:
		return "definition"
	case ByElement:
		return "element"
	case ByElementType:
		return "element type"
	default:
		return fmt.Sprintf("Axis(%d)", uint8(a))
	}
}

// Cell is one group of a cloud and the number of nodes in it. Which fields are meaningful
// follows from the cloud's axis: [ByDefinition] sets Definition, [ByElement] sets Definition
// and Element, [ByElementType] sets ElementType. The rest stay zero rather than being
// filled with a value that would read as a claim.
type Cell struct {
	Definition  uint64
	Element     int32
	ElementType uint8
	Nodes       int
}

// Cloud is a density: cells with counts, the axis they are cells of, and the log position
// the count is true as of.
//
// **It is an aggregation, not a clustering** (§5), and the consequence is what makes the
// cloud affordable where the walk is not: a group-by is not a graph operation. It costs one
// scan of the store and a map sized by the number of *cells*, so it needs no ordinal map, no
// CSR and no union-find — none of the 2,448 MB the structure costs at 110 M nodes, and none
// of §9's budget. An installation whose whole-graph walk §9 refuses can still be drawn as a
// cloud.
//
// Axis and Position are fields rather than documentation because §5's own rendering rule
// forbids a picture that cannot say what it is dense *in* and *as of when*, and a renderer
// cannot add either honestly if the number does not carry them.
type Cloud struct {
	Axis     Axis
	Position uint64

	cells []Cell
	nodes int
}

// Cells returns the cloud's cells in ascending order of definition, element and element
// type. The order is imposed rather than inherited: a Go map iterates randomly, and a cloud
// whose cells reshuffle between rebuilds breaks the same promise §5 kept by choosing
// components over a Louvain partition — that a saved view keeps its meaning.
func (c *Cloud) Cells() []Cell { return c.cells }

// Len is the number of cells, which is the size of the answer. Nodes is the number of
// element instances behind it, which is the size of the data. They differ by orders of
// magnitude, and confusing them is how a cloud gets mistaken for a graph.
func (c *Cloud) Len() int { return len(c.cells) }

// Nodes is how many element instances the cloud counted, across every cell.
func (c *Cloud) Nodes() int { return c.nodes }

// cellKey is the map key one scan accumulates into. All three fields are present whatever
// the axis; the unused ones stay zero, which is what collapses a definition's many elements
// into one cell under [ByDefinition].
type cellKey struct {
	definition  uint64
	element     int32
	elementType uint8
}

func (a Axis) keyOf(v *model.ElementInstanceValue) cellKey {
	switch a {
	case ByDefinition:
		return cellKey{definition: v.ProcessDefKey}
	case ByElement:
		return cellKey{definition: v.ProcessDefKey, element: v.ElementId}
	default: // ByElementType, the only remaining axis BuildCloud admits
		return cellKey{elementType: v.BpmnElementType}
	}
}

// BuildCloud counts the live element instances of src into cells of the given axis.
//
// It reads the position first, so a source that cannot say when its snapshot is true fails
// before the scan rather than producing a density nobody can date. A scan that fails
// part-way is an error and not a thin cloud: a count is plausible at every size, so a
// partial one has nothing in the shape of its answer to say it is wrong.
func BuildCloud(src Source, axis Axis) (*Cloud, error) {
	switch axis {
	case ByDefinition, ByElement, ByElementType:
	default:
		return nil, fmt.Errorf("rungraph: unknown cloud axis %s", axis)
	}
	pos, err := src.LastAppliedPosition()
	if err != nil {
		return nil, fmt.Errorf("rungraph: read position: %w", err)
	}
	counts := map[cellKey]int{}
	nodes := 0
	if err := src.ActiveElementInstances(func(_ uint64, v *model.ElementInstanceValue) error {
		counts[axis.keyOf(v)]++
		nodes++
		return nil
	}); err != nil {
		return nil, fmt.Errorf("rungraph: scan element instances: %w", err)
	}
	cells := make([]Cell, 0, len(counts))
	for k, n := range counts {
		cells = append(cells, Cell{
			Definition:  k.definition,
			Element:     k.element,
			ElementType: k.elementType,
			Nodes:       n,
		})
	}
	sort.Slice(cells, func(i, j int) bool {
		a, b := cells[i], cells[j]
		if a.Definition != b.Definition {
			return a.Definition < b.Definition
		}
		if a.Element != b.Element {
			return a.Element < b.Element
		}
		return a.ElementType < b.ElementType
	})
	return &Cloud{Axis: axis, Position: pos, cells: cells, nodes: nodes}, nil
}
