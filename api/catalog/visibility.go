package catalog

import (
	"net/http"
	"sort"

	"github.com/pblumer/atlas/api/httpapi"
)

// Which catalogue is whose.
//
// One catalogue per person (decision 11), resolved from the groups they carry
// and the rank the catalogues carry (decision 4). Rank is what breaks the
// many-to-many: a person is in dozens of directory groups and several of them may
// point at catalogues, so the highest rank they reach wins. Publishing refuses a
// rank tie, which is what makes "highest" an answer rather than a coin toss.
//
// The resolution reads only the groups a principal already carries from login
// (ADR-0180), so it needs no directory call and no store read beyond the
// catalogues themselves.

// ReachedBy reports whether somebody in these groups is in this catalogue's
// audience.
//
// A catalogue with no audience reaches nobody. That is fail-closed on purpose: a
// freshly created catalogue has no groups yet, and the dangerous default is the
// one where it is visible to everybody while somebody is still filling it.
func (c Catalog) ReachedBy(groups []string) bool {
	for _, want := range c.Groups {
		for _, has := range groups {
			if want == has {
				return true
			}
		}
	}
	return false
}

// Resolve returns the catalogue somebody in these groups sees: the highest-ranked
// one they reach. The second return is false when they reach none, which is an
// answer and not an error — it has to be distinguishable from reaching an empty
// catalogue.
func Resolve(catalogs []Catalog, groups []string) (Catalog, bool) {
	reached := make([]Catalog, 0, len(catalogs))
	for _, c := range catalogs {
		if c.ReachedBy(groups) {
			reached = append(reached, c)
		}
	}
	if len(reached) == 0 {
		return Catalog{}, false
	}
	sort.Slice(reached, func(a, b int) bool {
		if reached[a].Rank != reached[b].Rank {
			return reached[a].Rank > reached[b].Rank
		}
		// Ranks are unique by the time a catalogue is published; this only keeps
		// the answer stable for one that is not yet.
		return reached[a].ID < reached[b].ID
	})
	return reached[0], true
}

// mayRead reports whether p may see this catalogue at all.
//
// Two sights of the same store. The portal one is the catalogue you are the
// audience for; the maintenance one is a catalogue you maintain. A catalogue
// carries its audience, its approval rules, its product list and its process
// bindings, so at ten of them named after customers the listing alone tells one
// customer who the others are.
func (s *Service) mayRead(c Catalog, p *httpapi.Principal) bool {
	if p == nil {
		return false
	}
	// A viewer is by definition somebody who may read it — the first cut of this
	// check asked only for edit rights and hid the catalogue from the very people
	// it had been shared with.
	return c.ReachedBy(p.GroupIDs) || s.mayEdit(c, p) || grantedRole(c, p) == RoleViewer
}

// MayOrderFrom reports whether this principal may place an order against the
// given catalogue.
//
// Two ways in, and they answer different questions. The audience is the ordinary
// one: which catalogue somebody orders from is a fact about *them* — their
// groups — rather than about their authority, so no role opens a customer's
// catalogue to somebody outside it. The second is whoever maintains the
// catalogue, because otherwise nobody can check what they built and the first
// real order is the test.
//
// It runs on the loop, so callers must not already hold it.
func (s *Service) MayOrderFrom(p *httpapi.Principal, catalogID string) (bool, error) {
	if p == nil {
		return false, nil
	}
	var (
		cat     Catalog
		found   bool
		loadErr error
	)
	s.loop.Do(func() { cat, found, loadErr = s.store.Catalog(catalogID) })
	if loadErr != nil || !found {
		return false, loadErr
	}
	return cat.ReachedBy(p.GroupIDs) || s.mayEdit(cat, p), nil
}

// HandleMyCatalog answers the first question of every portal session: which
// catalogue is mine. Reaching none is 404 rather than an arbitrary catalogue —
// showing somebody a shop they are not the audience for is worse than showing
// them nothing.
func (s *Service) HandleMyCatalog(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	var groups []string
	if p != nil {
		groups = p.GroupIDs
	}

	var (
		all     []Catalog
		loadErr error
	)
	s.loop.Do(func() { all, loadErr = s.store.Catalogs() })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "resolve catalogue: "+loadErr.Error())
		return
	}

	got, ok := Resolve(all, groups)
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "no catalogue is assigned to you")
		return
	}
	httpapi.JSON(w, http.StatusOK, got)
}
