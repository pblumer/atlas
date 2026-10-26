package catalog

import "sort"

// StripProducts returns c without the products in gone: they leave its offer and
// every edge that names one of them, and the ids it lost are answered sorted.
//
// It is the one change an administrator's purge (ADR-0469) makes
// to a catalogue it does not delete. A product is referenced by catalogues rather
// than owned by one, so the catalogue being purged can be the home of a product
// another catalogue offers; deleting the product and leaving the other offer
// would publish a reference to nothing the next time somebody publishes there.
//
// The caller advances the revision and the time when it writes the result. This
// function only says what the catalogue would look like: the plan the purge
// shows before it acts is computed with it too.
func StripProducts(c Catalog, gone map[string]bool) (Catalog, []string) {
	lost := map[string]bool{}
	items := make([]string, 0, len(c.Items))
	for _, id := range c.Items {
		if gone[id] {
			lost[id] = true
			continue
		}
		items = append(items, id)
	}
	var edges []Edge
	for _, e := range c.Edges {
		if gone[e.From] || gone[e.To] {
			for _, id := range []string{e.From, e.To} {
				if gone[id] {
					lost[id] = true
				}
			}
			continue
		}
		edges = append(edges, e)
	}
	if len(lost) == 0 {
		return c, nil
	}
	c.Items, c.Edges = items, edges
	out := make([]string, 0, len(lost))
	for id := range lost {
		out = append(out, id)
	}
	sort.Strings(out)
	return c, out
}
