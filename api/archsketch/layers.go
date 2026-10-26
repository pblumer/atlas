package archsketch

import (
	"fmt"
	"strings"
)

// The layers of a release's architecture (ADR-0495 §1,
// §3).
//
// A layer is a band of the picture and a part of what a read returns. The application
// layer is the snapshot's logical part, what the release's content fixes; the
// technology layer is its physical part, what the recording installation had in place.
// Strategy, business and data are named here so that a selection can already ask for
// them and a link written today keeps meaning the same thing; no snapshot records
// them yet, and a read that asks only for layers a snapshot does not hold is refused
// in words rather than answered with an empty picture.
//
// A public start link is an application interface that only the installation
// records, so it belongs to both layers and is drawn only when both are shown.

// Layer names one layer, as a selection spells it.
type Layer string

// The layers, in the order they are drawn from top to bottom.
const (
	LayerStrategy    Layer = "strategy"
	LayerBusiness    Layer = "business"
	LayerApplication Layer = "application"
	LayerData        Layer = "data"
	LayerTechnology  Layer = "technology"
)

var layerOrder = []Layer{LayerStrategy, LayerBusiness, LayerApplication, LayerData, LayerTechnology}

// Selection is a set of layers.
type Selection uint8

// DefaultSelection is what a read that names no layers shows: the two layers every
// sketch was drawn with before layers existed, so a reader or an embedded picture that
// chose nothing sees exactly what it saw before.
var DefaultSelection = SelectionOf(LayerApplication, LayerTechnology)

// SelectionOf is the set of the layers named.
func SelectionOf(layers ...Layer) Selection {
	var s Selection
	for _, l := range layers {
		for i, known := range layerOrder {
			if l == known {
				s |= 1 << i
			}
		}
	}
	return s
}

// Has reports whether the layer is in the selection.
func (s Selection) Has(l Layer) bool { return s&SelectionOf(l) != 0 }

// Layers lists the selection in drawing order.
func (s Selection) Layers() []Layer {
	out := []Layer{}
	for i, l := range layerOrder {
		if s&(1<<i) != 0 {
			out = append(out, l)
		}
	}
	return out
}

// String writes the selection the way a layers= value spells it.
func (s Selection) String() string {
	names := make([]string, 0, len(layerOrder))
	for _, l := range s.Layers() {
		names = append(names, string(l))
	}
	return strings.Join(names, ",")
}

// ParseSelection reads a layers= value: layer names separated by commas, in any order
// and any case. An empty value is the default selection. A name that is not a layer
// is an error naming it, because a link that quietly drops a layer it asked for draws
// a picture its author did not mean.
func ParseSelection(raw string) (Selection, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultSelection, nil
	}
	var s Selection
	for _, part := range strings.Split(raw, ",") {
		name := Layer(strings.ToLower(strings.TrimSpace(part)))
		if name == "" {
			continue
		}
		bit := SelectionOf(name)
		if bit == 0 {
			return 0, fmt.Errorf("unknown layer %q: the layers are %s", part, Selection(1<<len(layerOrder)-1))
		}
		s |= bit
	}
	if s == 0 {
		return DefaultSelection, nil
	}
	return s, nil
}

// Recorded is the layers this snapshot holds something for: the application layer
// always, the technology layer when the snapshot carries a physical part. On a value
// [Snapshot.Select] returned it is still the stored snapshot's answer, so a picture
// can say which recorded layers were switched off.
func (s Snapshot) Recorded() Selection {
	if s.recorded != 0 {
		return s.recorded
	}
	out := SelectionOf(LayerApplication)
	if s.Physical != nil {
		out |= SelectionOf(LayerTechnology)
	}
	return out
}

// Select is the snapshot as a read of these layers sees it. Without the technology
// layer it has no physical part and is drawn as a logical view; without the
// application layer it keeps its processes, so a reader still learns what the release
// shipped, but none of its ways in or dependencies, and none of the public start
// links. The snapshot it is called on is not changed.
func (s Snapshot) Select(sel Selection) Snapshot {
	out := s
	out.shown, out.recorded = sel, s.Recorded()
	if !sel.Has(LayerTechnology) {
		out.Physical = nil
	}
	if !sel.Has(LayerApplication) {
		out.EntryPoints = nil
		out.Dependencies = nil
		if out.Physical != nil {
			p := *out.Physical
			p.PublicStartLinks = nil
			out.Physical = &p
		}
	}
	return out
}

// Shown is the selection a rendering of this value draws.
func (s Snapshot) Shown() Selection {
	if s.shown == 0 {
		return DefaultSelection
	}
	return s.shown
}

// technologyOnly reports whether this value is drawn as the installation alone: the
// technology layer without the application layer.
func (s Snapshot) technologyOnly() bool {
	sel := s.Shown()
	return !sel.Has(LayerApplication) && sel.Has(LayerTechnology) && s.Physical != nil
}

// layerNote is what a rendering says about its layers when they are not the default:
// which it shows, which were switched off, and which the reader asked for that this
// snapshot does not record. Empty for the default selection, so a picture nobody
// narrowed carries no line about narrowing.
func (s Snapshot) layerNote() string {
	sel := s.Shown()
	if sel == DefaultSelection {
		return ""
	}
	recorded := s.Recorded()
	var off, missing []string
	for _, l := range layerOrder {
		switch {
		case recorded.Has(l) && !sel.Has(l):
			off = append(off, string(l))
		case sel.Has(l) && !recorded.Has(l):
			missing = append(missing, string(l))
		}
	}
	note := "Layers shown: " + (sel & recorded).String() + "."
	if len(off) > 0 {
		note += " Switched off for this picture: " + strings.Join(off, ", ") + "."
	}
	if len(missing) > 0 {
		note += " Asked for, but not recorded in this snapshot: " + strings.Join(missing, ", ") + "."
	}
	return note
}
