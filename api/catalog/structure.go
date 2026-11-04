package catalog

import (
	"sort"
	"strings"
)

// Declared structure (ADR-0524).
//
// A catalogue drew its levels from the graph: an item nothing contained was a
// product, anything below one a service. That made the distinction a picture rather
// than a fact — the catalogue held products only, and a single account with no parts
// was offered as a Marktleistung because nothing happened to contain it. A structured
// catalogue states the kind on every item and a publish proves the graph agrees:
//
//   - a Marktleistung is the whole: contained by nothing, including at least one
//     service, containing services only, and filed under a category and a product
//     group;
//   - a service is one brick: it has no parts, carries no heading, and is contained
//     by at least one Marktleistung, because the shop offers it nowhere else.
//
// Every item keeps owing its own lifecycle binding (checkBindings), which is what
// makes a service provisionable on its own. The Marktleistung owes one too: it holds
// the orchestration that completes and dismantles the whole.

// checkStructure holds a structured catalogue to its declarations and reports
// whether the catalogue being published is one. An unknown kind is refused whether
// or not the catalogue opted in, because a value nobody defined is a typo that would
// otherwise travel silently until the switch is turned on.
func checkStructure(in Input, byID map[string]Item, add func(Problem)) bool {
	items := append([]Item(nil), in.Items...)
	sort.Slice(items, func(a, b int) bool { return items[a].ID < items[b].ID })

	for _, it := range items {
		switch it.Kind {
		case "", ItemOffering, ItemService:
		default:
			add(Problem{Item: it.ID, Message: "names kind " + quote(string(it.Kind)) +
				"; an item is a Marktleistung (\"offering\") or a service (\"service\")"})
		}
	}

	subject, ok := subjectCatalog(in)
	if !ok || !subject.Structured {
		return false
	}

	// The structure, read once: who contains whom, and which wholes include at
	// least one part integrally. Edges to an unknown item were reported by
	// checkEdges and are skipped here, so one defect is not named twice.
	wholes := map[string][]string{}
	parts := map[string][]string{}
	includes := map[string]bool{}
	for _, e := range in.Edges {
		if !e.Kind.Structural() {
			continue
		}
		if _, known := byID[e.From]; !known {
			continue
		}
		if _, known := byID[e.To]; !known {
			continue
		}
		parts[e.From] = appendOnce(parts[e.From], e.To)
		wholes[e.To] = appendOnce(wholes[e.To], e.From)
		if e.Kind == EdgeComposition {
			includes[e.From] = true
		}
	}

	for _, it := range items {
		switch it.Kind {
		case "":
			add(Problem{Item: it.ID, Message: "is neither a Marktleistung nor a service; " +
				"a structured catalogue declares which every product is"})
		case ItemOffering:
			checkOffering(it, byID, wholes[it.ID], parts[it.ID], includes[it.ID], add)
		case ItemService:
			checkService(it, byID, wholes[it.ID], parts[it.ID], add)
		}
	}
	return true
}

func checkOffering(it Item, byID map[string]Item, wholes, parts []string, includes bool, add func(Problem)) {
	if len(wholes) > 0 {
		add(Problem{Item: it.ID, Message: "is a Marktleistung and is contained by " +
			join(wholes) + "; a Marktleistung is the whole a person orders and is part of nothing"})
	}
	if !includes {
		add(Problem{Item: it.ID, Message: "is a Marktleistung that includes no service; " +
			"it is assembled from at least one service it always comes with, or ordering " +
			"it would provision nothing of its own"})
	}
	for _, p := range parts {
		if byID[p].Kind != ItemService {
			add(Problem{Item: it.ID, Message: "contains " + p + ", which is not a service; " +
				"a Marktleistung is built from services only"})
		}
	}
	if strings.TrimSpace(it.Category) == "" {
		add(Problem{Item: it.ID, Message: "is a Marktleistung that names no category; " +
			"the shop files every Marktleistung under a category and a product group"})
	}
	if strings.TrimSpace(it.ProductGroup) == "" {
		add(Problem{Item: it.ID, Message: "is a Marktleistung that names no product group; " +
			"the shop files every Marktleistung under a category and a product group"})
	}
}

func checkService(it Item, byID map[string]Item, wholes, parts []string, add func(Problem)) {
	if len(parts) > 0 {
		add(Problem{Item: it.ID, Message: "is a service and contains " + join(parts) +
			"; a service is one building block and has no parts"})
	}
	offered := false
	for _, w := range wholes {
		if byID[w].Kind == ItemOffering {
			offered = true
			break
		}
	}
	if !offered {
		add(Problem{Item: it.ID, Message: "is a service and no Marktleistung in this catalogue " +
			"contains it; the shop offers a service only as part of a Marktleistung, so " +
			"nobody could order it — leave it out of the catalogue until one does"})
	}
	if strings.TrimSpace(it.Category) != "" || strings.TrimSpace(it.ProductGroup) != "" ||
		described(it.CategoryTexts) || described(it.ProductGroupTexts) {
		add(Problem{Item: it.ID, Message: "is a service and carries a category or product " +
			"group; headings belong to the Marktleistung, and the shop never reads them on a part"})
	}
}

// subjectCatalog is the catalogue a publish is of: the one [Input.CatalogID] names,
// or the only one there is. checkSubject refuses every other reading.
func subjectCatalog(in Input) (Catalog, bool) {
	if in.CatalogID == "" {
		if len(in.Catalogs) == 1 {
			return in.Catalogs[0], true
		}
		return Catalog{}, false
	}
	for _, c := range in.Catalogs {
		if c.ID == in.CatalogID {
			return c, true
		}
	}
	return Catalog{}, false
}

func appendOnce(list []string, id string) []string {
	for _, have := range list {
		if have == id {
			return list
		}
	}
	list = append(list, id)
	sort.Strings(list)
	return list
}
