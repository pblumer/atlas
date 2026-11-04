package order

import (
	"slices"

	"github.com/pblumer/atlas/api/catalog"
)

// directServiceIn reports a service the selection asks for on its own, or "" when
// there is none. In a structured release the shop offers services only inside a
// Marktleistung (ADR-0524), so a selection may name one
// only beside a Marktleistung it is part of — included or optional — or as an option
// of a Marktleistung the recipient already holds, which is how a sleeve not ticked
// with the laptop is ordered later (ADR-0312, amendment of 2026-10-28). A release
// that is not structured is not asked: its levels are drawn from the graph and any
// item may be ordered.
func directServiceIn(rel catalog.Release, selected []string, held map[string]bool) string {
	if !rel.Structured {
		return ""
	}
	kind := make(map[string]catalog.ItemKind, len(rel.Items))
	for _, it := range rel.Items {
		kind[it.ID] = it.Kind
	}
	asked := make(map[string]bool, len(selected))
	for _, id := range selected {
		asked[id] = true
	}
	for _, id := range selected {
		if kind[id] != catalog.ItemService {
			continue
		}
		if partOfAsked(rel, id, asked) || optionOfHeld(rel, id, held) {
			continue
		}
		return id + " is a service, and a service is ordered only as part of a Marktleistung: " +
			"order the Marktleistung it belongs to, with this as one of its options"
	}
	return ""
}

func partOfAsked(rel catalog.Release, id string, asked map[string]bool) bool {
	for whole := range asked {
		if slices.Contains(rel.Includes[whole], id) || slices.Contains(rel.Options[whole], id) {
			return true
		}
	}
	return false
}

func optionOfHeld(rel catalog.Release, id string, held map[string]bool) bool {
	for whole, options := range rel.Options {
		if held[whole] && slices.Contains(options, id) {
			return true
		}
	}
	return false
}
