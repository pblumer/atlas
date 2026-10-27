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
// The data layer, from format version 5, is what the processes keep and read and the
// personal data they declare; the business and strategy layers, from version 6, the
// work, roles and products and the capabilities and value streams the release serves.
// A read that asks only for layers a snapshot does not hold is refused in words rather
// than answered with an empty picture.
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
// always, the technology layer when the snapshot carries a physical part, the data
// layer when it carries a data part — even an empty one, which says the release
// declares no data. On a value
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
	if s.Data != nil {
		out |= SelectionOf(LayerData)
	}
	if s.Business != nil {
		out |= SelectionOf(LayerBusiness)
	}
	if s.Strategy != nil {
		out |= SelectionOf(LayerStrategy)
	}
	return out
}

// Select is the snapshot as a read of these layers sees it. Without the technology
// layer it has no physical part and is drawn as a logical view; without the
// application layer it keeps its processes, so a reader still learns what the release
// shipped, but none of its ways in or dependencies, and none of the public start
// links. Without the data layer it has no data part. The snapshot it is called on is
// not changed.
func (s Snapshot) Select(sel Selection) Snapshot {
	out := s
	out.shown, out.recorded = sel, s.Recorded()
	if !sel.Has(LayerTechnology) {
		out.Physical = nil
	}
	if !sel.Has(LayerData) {
		out.Data = nil
	}
	if !sel.Has(LayerBusiness) {
		out.Business = nil
	}
	if !sel.Has(LayerStrategy) {
		out.Strategy = nil
	}
	if !sel.Has(LayerApplication) {
		out.EntryPoints = nil
		out.Dependencies = nil
		out.Coupling = nil
		if out.Physical != nil {
			p := *out.Physical
			p.PublicStartLinks = nil
			out.Physical = &p
		}
	}
	return out
}

// selected is the value a rendering draws: itself when [Snapshot.Select] made it, and
// otherwise the default selection of it, so a snapshot rendered as stored draws what a
// read that names no layers draws.
func (s Snapshot) selected() Snapshot {
	if s.shown != 0 {
		return s
	}
	return s.Select(DefaultSelection)
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

// bandsOnly reports whether this value is drawn as bands alone: a strategy, business
// or data layer without the application layer, and without a technology layer drawn
// as the installation.
func (s Snapshot) bandsOnly() bool {
	return (s.Data != nil || s.Business != nil || s.Strategy != nil) && !s.Shown().Has(LayerApplication) && !s.technologyOnly()
}

// bandsName names the layers a value drawn as bands alone shows, the way a stamp says
// "Data view".
func (s Snapshot) bandsName() string {
	var names []string
	for _, l := range []struct {
		on   bool
		name string
	}{{s.Strategy != nil, "strategy"}, {s.Business != nil, "business"}, {s.Data != nil, "data"}} {
		if l.on {
			names = append(names, l.name)
		}
	}
	switch len(names) {
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// layerNote is what a rendering says about its layers when they are not the default:
// which it shows, which were switched off, and which the reader asked for that this
// snapshot does not record. Empty for the default selection of a snapshot that records
// nothing beyond it, so a picture nobody narrowed carries no line about narrowing; a
// snapshot that records more says which of its layers the default leaves off.
func (s Snapshot) layerNote() string {
	sel, recorded := s.Shown(), s.Recorded()
	if sel == DefaultSelection && recorded&^sel == 0 {
		return ""
	}
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
