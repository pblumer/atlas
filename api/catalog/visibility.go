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
	return Highest(reached)
}

// Highest is the top-ranked catalogue of a set, and whether there was one.
//
// It is what "which catalogue" means when there is no audience question to ask —
// see [Service.HandleMyCatalog] for the one mode where that is the case. Rank is
// the tie-break the product already uses everywhere else, and publishing refuses a
// rank tie, which is what makes "highest" an answer rather than a coin toss.
func Highest(catalogs []Catalog) (Catalog, bool) {
	if len(catalogs) == 0 {
		return Catalog{}, false
	}
	out := make([]Catalog, len(catalogs))
	copy(out, catalogs)
	sort.Slice(out, func(a, b int) bool {
		if out[a].Rank != out[b].Rank {
			return out[a].Rank > out[b].Rank
		}
		// Ranks are unique by the time a catalogue is published; this only keeps
		// the answer stable for one that is not yet.
		return out[a].ID < out[b].ID
	})
	return out[0], true
}

// mayRead reports whether p may see this catalogue at all.
//
// Two sights of the same store. The portal one is the catalogue you are the
// audience for; the maintenance one is a catalogue you maintain. A catalogue
// carries its audience, its approval rules, its product list and its process
// bindings, so at ten of them named after customers the listing alone tells one
// customer who the others are.
func (s *Service) mayRead(c Catalog, p *httpapi.Principal) bool {
	// Same order and the same reason as mayEdit: enforcement off means there is
	// nobody to be, not nobody who may.
	if s.admin(p) {
		return true
	}
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

// nobodyToBe reports whether this installation has no identities at all: the
// documented single-user mode, `--auth=false`.
//
// It asks the admin predicate with a nil principal rather than carrying a flag of
// its own, and that is deliberate. The server builds that predicate as
// `!authEnabled || p.HasRole(admin)`, so a nil principal passing it *is* the
// statement "enforcement is off" — where with enforcement on it answers false, as
// every gate in this package relies on. A second copy of one fact is a second copy
// that eventually disagrees with the first.
func (s *Service) nobodyToBe() bool { return s.admin(nil) }

// HandleMyCatalog answers the first question of every portal session: which
// catalogue is mine.
//
// With enforcement on, it is the highest-ranked catalogue the caller's groups
// reach, and reaching none is 404 rather than an arbitrary catalogue — showing
// somebody a shop they are not the audience for is worse than showing them
// nothing.
//
// # The single-user mode
//
// With `--auth=false` there is no principal, so there are no groups, so
// [Catalog.ReachedBy] answers false for every catalogue and the portal could never
// resolve one. The documented development and demo mode showed an empty page and
// said a catalogue had not been assigned — to somebody there is no "you" to assign
// one to (ADR-0377).
//
// So when there is nobody to be, the audience question is not asked and the
// highest-ranked catalogue is the answer. That relaxes a rule this package is
// otherwise strict about — a catalogue with no audience reaches nobody, fail-closed
// on purpose — and the reason it is safe to relax *here* and nowhere else is that
// the guard protects nothing in this mode: with enforcement off every catalogue is
// already readable through the administration routes by anybody who can reach the
// port. It withholds a catalogue from a person who does not exist, at the cost of
// the one screen the mode is for.
//
// It is confined to a **nil** principal. A signed-in administrator still gets their
// own audience's catalogue and not the top-ranked one: being allowed to read every
// catalogue is not the same as being the audience for one, and the portal answers
// the second question.
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
	if !ok && p == nil && s.nobodyToBe() {
		got, ok = Highest(all)
	}
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "no catalogue is assigned to you")
		return
	}
	httpapi.JSON(w, http.StatusOK, got)
}

// MayMaintain reports whether p may see this catalogue as somebody who maintains it,
// rather than as somebody it is offered to.
//
// It is [mayRead] without the audience, and the difference is the whole reason it
// exists. A customer reaches a catalogue to order from it; that tells them nothing
// about the estate behind it, and a surface that drew the catalogue's products, their
// provisioning processes and the engine around them for everyone the catalogue
// reaches would be using this store to answer a question it never agreed to answer.
// Maintainers, and the people a maintainer shared it with, are who that picture is
// for (ADR-0211 §3).
func (s *Service) MayMaintain(c Catalog, p *httpapi.Principal) bool {
	return s.mayEdit(c, p) || grantedRole(c, p) == RoleViewer
}
