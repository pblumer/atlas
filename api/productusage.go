package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Reading the catalogue graph backwards
// (ADR-draft-product-usage).
//
// Everything the catalogue answers today runs forwards: a product names what it
// contains, what it needs, what it excludes. That is the question an *order*
// asks, and the portal, the basket and the fulfilment schedule are all built on
// it.
//
// The person who maintains a service asks the opposite question, and could not
// ask it at all: **where is this used, what depends on it, who has it.** A
// product manager about to retire a service, change its provisioning or move it
// between catalogues has no way to find out what they are about to break.
//
// # Why this needs no new data
//
// It reads the same edges the release already froze, walked the other way. That
// is the whole of it, and it is why this is a read route rather than a model
// change: the answer has been in the store since the first release was
// published, with nothing to ask it.
//
// # What it deliberately does not answer
//
// **Who holds it, by name.** The count is here and the list is not. A list of
// people holding one service is the inventory filtered to the interesting part,
// which is the disclosure the inventory routes are gated for — and a product
// manager maintaining a service does not need to know who Ada is to know that
// forty people would be affected.

// usageIn is one place a product is used: the whole that carries it, and how.
type usageIn struct {
	ItemID string `json:"itemId"`
	// Kind is "composition" for a part that is integral to the whole and
	// "aggregation" for one offered alongside it. They are kept apart because
	// retiring the two has different consequences: an integral part cannot be
	// removed without changing what the whole *is*, and an optional one can.
	Kind string `json:"kind"`
	// CatalogID names where this containment was declared. The same product can be
	// carried by different wholes in different catalogues, and a maintainer
	// changing it needs to know which estates they reach.
	CatalogID string `json:"catalogId"`
}

// usageReport is what one read answers.
type usageReport struct {
	ItemID string `json:"itemId"`
	// OfferedBy names every catalogue whose newest release carries this product,
	// whether or not anything contains it.
	OfferedBy []string `json:"offeredBy"`
	// PartOf is where it is used: the wholes that carry it. Empty for a product
	// that is offered on its own, which is an answer and not an omission.
	PartOf []usageIn `json:"partOf"`
	// Needs is what must be provisioned before this can be, NeededBy what cannot be
	// provisioned before this is. The second is the one nobody can derive by
	// reading a catalogue screen, and it is the one that matters when a service is
	// about to be retired.
	Needs    []string `json:"needs"`
	NeededBy []string `json:"neededBy"`
	// ExcludedWith is what this may never be held together with. Symmetric, so it
	// reads the same from either side.
	ExcludedWith []string `json:"excludedWith"`
	// Held counts what the inventory records, by origin. A count and never a list —
	// see the note at the top of this file.
	Held usageHeld `json:"held"`
	// Note explains an answer that is empty for a reason a reader would otherwise
	// have to guess at.
	Note string `json:"note,omitempty"`
}

type usageHeld struct {
	Total int `json:"total"`
	// ByOrigin separates what this portal granted from what it merely found. The
	// difference decides what a maintainer can do: an ordered right can be returned
	// through its order, an adopted or legacy one cannot.
	ByOrigin map[string]int `json:"byOrigin,omitempty"`
}

// handleProductUsage answers where one product is used.
func (s *Server) handleProductUsage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		httpapi.Error(w, http.StatusBadRequest, "usage: name a product")
		return
	}

	var (
		rep     usageReport
		known   bool
		ran     bool
		loadErr error
	)
	s.do(func() {
		ran = true
		rep, known, loadErr = usageOf(s, id)
	})
	if !ran {
		httpapi.Error(w, http.StatusServiceUnavailable, "usage: this server is shutting down")
		return
	}
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "usage: "+loadErr.Error())
		return
	}
	if !known {
		// 404 rather than an empty report: "no catalogue offers this" and "this
		// product does not exist" are different answers, and a maintainer who
		// mistyped an id must not read the first as the second.
		httpapi.Error(w, http.StatusNotFound,
			"no released catalogue carries the product "+id+
				". A product exists in this answer once a catalogue that offers it has been published")
		return
	}

	// The inventory, off the loop — one product's holders are a scan of everybody's
	// holdings, because the entitlement key starts with the principal (ADR-0239).
	counts := map[string]int{}
	total := 0
	if err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		return rv.Entitlements(func(v *model.EntitlementValue) error {
			if v.ItemID != id {
				return nil
			}
			total++
			counts[v.Origin.String()]++
			return nil
		})
	}); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "usage: read the inventory: "+err.Error())
		return
	}
	rep.Held = usageHeld{Total: total}
	if total > 0 {
		rep.Held.ByOrigin = counts
	}

	if len(rep.PartOf) == 0 && len(rep.NeededBy) == 0 && total == 0 {
		rep.Note = "nothing in any published catalogue carries or requires this product, and " +
			"nobody is recorded as holding it. That is what a service safe to retire looks " +
			"like — and it is also what one looks like that was never published"
	}
	httpapi.JSON(w, http.StatusOK, rep)
}

// usageOf loads every catalogue's newest release and hands them to the walk.
//
// It must run on the loop — it reads the catalogue store.
func usageOf(s *Server, id string) (usageReport, bool, error) {
	cats, err := s.catalogStore.Catalogs()
	if err != nil {
		return usageReport{}, false, err
	}
	current := map[string]catalog.Release{}
	for _, c := range cats {
		rels, err := s.catalogStore.ReleasesOf(c.ID)
		if err != nil {
			return usageReport{}, false, err
		}
		if len(rels) == 0 {
			continue
		}
		// The newest, and only the newest, for the reason the conflict report reads
		// it that way: an older release is what the catalogue used to say, and a
		// maintainer planning a change needs what it says now.
		current[c.ID] = rels[0]
	}
	rep, known := usageAcross(id, current)
	return rep, known, nil
}

// usageAcross walks every catalogue backwards for one product.
//
// Pure, and separate from the load for the reason conflictsFor is: the walk is
// where every decision in this file lives, and a test that had to stand up a
// store to reach it would be testing the store.
//
// Merged across catalogues rather than answered per catalogue, because the
// question is about a *service* and a service does not belong to a catalogue: the
// same product carried by two catalogues is one thing a maintainer is about to
// change. A per-catalogue answer would let them fix one estate and break another
// without ever seeing the second.
func usageAcross(id string, releases map[string]catalog.Release) (usageReport, bool) {
	rep := usageReport{
		ItemID: id, OfferedBy: []string{}, PartOf: []usageIn{},
		Needs: []string{}, NeededBy: []string{}, ExcludedWith: []string{},
	}
	needs, neededBy, excluded := map[string]bool{}, map[string]bool{}, map[string]bool{}
	seenPart := map[string]bool{}
	known := false

	for catID, rel := range releases {
		if !carries(rel, id) {
			continue
		}
		known = true
		rep.OfferedBy = append(rep.OfferedBy, catID)

		for whole, parts := range rel.Includes {
			if contains(parts, id) && !seenPart[whole+"\x00"+catID] {
				seenPart[whole+"\x00"+catID] = true
				rep.PartOf = append(rep.PartOf,
					usageIn{ItemID: whole, Kind: string(catalog.EdgeComposition), CatalogID: catID})
			}
		}
		for whole, parts := range rel.Options {
			if contains(parts, id) && !seenPart[whole+"\x00"+catID] {
				seenPart[whole+"\x00"+catID] = true
				rep.PartOf = append(rep.PartOf,
					usageIn{ItemID: whole, Kind: string(catalog.EdgeAggregation), CatalogID: catID})
			}
		}
		for _, need := range rel.Requires[id] {
			needs[need] = true
		}
		for dependent, wants := range rel.Requires {
			if dependent != id && contains(wants, id) {
				neededBy[dependent] = true
			}
		}
		for _, other := range rel.Excludes[id] {
			excluded[other] = true
		}
	}

	rep.Needs = usageList(needs)
	rep.NeededBy = usageList(neededBy)
	rep.ExcludedWith = usageList(excluded)
	// Sorted throughout, because a map has no order: two reads of an unchanged
	// catalogue that disagreed would read as a change nobody made.
	sort.Strings(rep.OfferedBy)
	sort.Slice(rep.PartOf, func(i, j int) bool {
		if rep.PartOf[i].ItemID != rep.PartOf[j].ItemID {
			return rep.PartOf[i].ItemID < rep.PartOf[j].ItemID
		}
		return rep.PartOf[i].CatalogID < rep.PartOf[j].CatalogID
	})
	return rep, known
}

// carries reports whether a release offers this product at all.
func carries(rel catalog.Release, id string) bool {
	for _, it := range rel.Items {
		if it.ID == id {
			return true
		}
	}
	return false
}

func contains(haystack []string, want string) bool {
	for _, h := range haystack {
		if h == want {
			return true
		}
	}
	return false
}

// usageList renders a set as a stable list. Stable because two reads of an
// unchanged catalogue that disagreed about order would look like a change.
func usageList(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
