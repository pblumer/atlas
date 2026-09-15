package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/sidecar"
)

// What somebody marked to find again (ADR-draft-favourites).
//
// The smallest thing in the portal and the one with the least to argue about,
// which is exactly why the two decisions it does carry are worth writing down.
//
// # It is a bookmark and never an entitlement
//
// A favourite stores a catalogue item id and nothing else — no release, no
// catalogue, no variant. It says "show me this again", not "I may have this".
// Everything that decides whether the person may still order it is asked at read
// time, by the same routes that decide it for the catalogue screen, and a
// favourite pointing at something they can no longer see changes none of those
// answers.
//
// That is what keeps it from going stale in a way anybody has to repair. A
// catalogue reassignment, a product withdrawn, a release that no longer carries
// it: the stored id is unaffected, and the portal simply resolves fewer of them.
// Storing a resolved product instead would have made a favourite a copy of a
// catalogue row, ageing the moment the catalogue moved.
//
// # Yours only, with no way to ask about anybody else
//
// Every other read in the portal has a `?principal=` for an operator, because
// somebody administering an estate legitimately needs to see what another person
// holds or owes. Nothing needs to see what another person *bookmarked*, and a
// parameter nobody needs is a surface to keep closed rather than a convenience.

// favourites is one person's marked products.
type favourites struct {
	// Principal owns the list, as a principal id. It is the record's key: one
	// person has one list, and there is no id of the list itself to leak or guess.
	Principal string `json:"principal"`
	// ItemIDs are catalogue item ids, sorted. Sorted on write rather than on read
	// so two saves of the same set produce the same file — a store whose bytes
	// changed without its meaning changing makes a backup diff unreadable.
	ItemIDs   []string `json:"itemIds"`
	UpdatedAt int64    `json:"updatedAt"`
}

type favouriteStore = sidecar.Store[favourites]

func newFavouriteStore(dir string) (*favouriteStore, error) {
	return sidecar.NewStore(dir, "favouritestore",
		func(rec favourites) string { return rec.Principal })
}

// favouritesOf reads one person's list, answering an empty one where nothing is
// stored. Absent and empty are deliberately the same thing here: "I have marked
// nothing" and "I have never marked anything" are the same state to everybody
// who asks, unlike the registration setting next door where they are not.
func (s *Server) favouritesOf(principal string) (favourites, error) {
	rec, ok, err := s.favourites.Get(principal)
	if err != nil {
		return favourites{}, err
	}
	if !ok {
		return favourites{Principal: principal, ItemIDs: []string{}}, nil
	}
	if rec.ItemIDs == nil {
		rec.ItemIDs = []string{}
	}
	return rec, nil
}

// whoseFavourites answers which list this request may touch, writing the refusal
// itself when there is none.
//
// Always the caller's own. There is no ?principal= here — see the note at the top
// of this file.
func whoseFavourites(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil || p.UserID == "" {
		httpapi.Error(w, http.StatusBadRequest,
			"favourites belong to an account, and nobody is signed in")
		return "", false
	}
	return p.UserID, true
}

// handleListFavourites answers what the caller has marked.
func (s *Server) handleListFavourites(w http.ResponseWriter, r *http.Request) {
	principal, ok := whoseFavourites(w, r)
	if !ok {
		return
	}
	var (
		rec     favourites
		ran     bool
		loadErr error
	)
	s.do(func() { ran = true; rec, loadErr = s.favouritesOf(principal) })
	if !ran {
		httpapi.Error(w, http.StatusServiceUnavailable, "favourites: this server is shutting down")
		return
	}
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "favourites: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, rec)
}

// handleSetFavourite marks one product, and handleClearFavourite unmarks it.
//
// One product per call rather than a whole list per call, which is the shape a
// star actually has — and it is what keeps two open tabs from losing a mark: a
// replace-the-list write would silently drop whatever the other tab added
// between its read and this write.
func (s *Server) handleSetFavourite(w http.ResponseWriter, r *http.Request) {
	s.changeFavourite(w, r, true)
}

func (s *Server) handleClearFavourite(w http.ResponseWriter, r *http.Request) {
	s.changeFavourite(w, r, false)
}

func (s *Server) changeFavourite(w http.ResponseWriter, r *http.Request, mark bool) {
	principal, ok := whoseFavourites(w, r)
	if !ok {
		return
	}
	item := strings.TrimSpace(r.PathValue("itemId"))
	if item == "" {
		httpapi.Error(w, http.StatusBadRequest, "favourites: name a product")
		return
	}

	var (
		rec     favourites
		ran     bool
		full    bool
		opErr   error
		changed bool
	)
	s.do(func() {
		ran = true
		rec, opErr = s.favouritesOf(principal)
		if opErr != nil {
			return
		}
		next, did := withFavourite(rec.ItemIDs, item, mark)
		if !did {
			// Marking what is already marked, or clearing what is not, is the state
			// the caller asked for rather than an error — the same reading the
			// inventory's revocation takes. Nothing is written, so a star pressed
			// twice does not churn the store.
			rec.ItemIDs = next
			return
		}
		if mark && len(next) > int(s.budgets().Favourites) {
			full = true
			return
		}
		changed = true
		rec.ItemIDs, rec.UpdatedAt = next, s.now()
		opErr = s.favourites.Save(rec)
	})
	switch {
	case !ran:
		httpapi.Error(w, http.StatusServiceUnavailable, "favourites: this server is shutting down")
	case full:
		httpapi.Error(w, http.StatusUnprocessableEntity,
			"that is more favourites than one account may keep. A list nobody can scan is "+
				"not a shortcut, so clear one before marking another")
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "favourites: "+opErr.Error())
	default:
		_ = changed
		httpapi.JSON(w, http.StatusOK, rec)
	}
}

// withFavourite returns the list with this product marked or cleared, and
// whether that changed anything.
//
// Sorted, so the stored file is a function of the set rather than of the order
// somebody pressed things in.
func withFavourite(have []string, item string, mark bool) ([]string, bool) {
	out := make([]string, 0, len(have)+1)
	found := false
	for _, id := range have {
		if id == item {
			found = true
			if !mark {
				continue
			}
		}
		out = append(out, id)
	}
	if mark && !found {
		out = append(out, item)
	}
	sort.Strings(out)
	return out, mark != found
}
