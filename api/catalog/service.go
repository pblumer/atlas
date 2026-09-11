package catalog

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/limits"
)

// The catalogue's HTTP surface (ADR-0147: an API area is a service, not more
// Server methods).
//
// The single-writer boundary is the [runloop.Loop] this service holds. Every
// store access goes through it and nothing else, which is invariant I3 — and it
// is the one thing a review of this package has to check line by line. Nothing
// here is engine state: a catalogue is authored, never executed, so none of it
// reaches the event log, the processor or recovery.

// Service is the catalogue API area.
type Service struct {
	loop  *runloop.Loop
	store *Store
	// now is injected so tests are deterministic and so the one place a timestamp
	// is read stays visible.
	now func() int64
	// admin answers whether a principal administers the instance. It is a function
	// rather than a role name so this package never carries a second copy of the
	// role vocabulary — a list kept in two places is a list that eventually
	// disagrees with itself.
	admin func(*httpapi.Principal) bool

	// Limits are the installation's resource budgets. New sets them to
	// [limits.Default]; the server overwrites them with its own once it has read
	// the environment.
	Limits limits.Limits
}

// New builds the service. Every dependency is an explicit argument (ADR-0147).
func New(loop *runloop.Loop, store *Store, now func() int64, admin func(*httpapi.Principal) bool) *Service {
	return &Service{loop: loop, store: store, now: now, admin: admin, Limits: limits.Default()}
}

// budgets is how this service reads a ceiling. It defaults a Service built as a
// struct literal to [limits.Default], because the zero Limits is every ceiling at
// zero and a ceiling of zero admits nothing — a failure that looks like a bad
// request rather than like missing configuration. New always sets them.
func (s *Service) budgets() limits.Limits {
	if s.Limits == (limits.Limits{}) {
		return limits.Default()
	}
	return s.Limits
}

// mayEdit reports whether p may change this catalogue: the object axis of
// ADR-0278, checked where the action happens rather than at the boundary, because
// the boundary knows the role and not the catalogue.
//
// It reads nothing. A principal carries its roles and its group membership from
// login (ADR-0180), so this is a pure check — which is what lets it run inside the
// same run-loop closure as the write it guards, rather than needing a second
// rendezvous the loop cannot give it.
func (s *Service) mayEdit(c Catalog, p *httpapi.Principal) bool {
	// Fail closed: a request that arrived without an identity is not the owner of
	// a catalogue that happens to have none.
	if p == nil {
		return false
	}
	if s.admin(p) {
		return true
	}
	if c.OwnerID != "" && c.OwnerID == p.UserID {
		return true
	}
	return grantedRole(c, p) == RoleEditor
}

// grantedRole returns the strongest member role this principal holds on the
// catalogue, or "" for none. Editor outranks viewer, so a person granted both
// directly and through a group gets the stronger of the two rather than whichever
// entry happens to come first.
func grantedRole(c Catalog, p *httpapi.Principal) MemberRole {
	if p == nil {
		return ""
	}
	var best MemberRole
	for _, m := range c.Members {
		matches := false
		switch m.Ref.Type {
		case "user":
			matches = m.Ref.ID == p.UserID
		case "group":
			matches = p.InGroup(m.Ref.ID)
		}
		if !matches {
			continue
		}
		if m.Role == RoleEditor {
			return RoleEditor
		}
		if m.Role == RoleViewer {
			best = RoleViewer
		}
	}
	return best
}

// newID mints an opaque id with a self-describing prefix, so a bare id in a log
// or a URL says what kind of thing it is.
func newID(prefix string) (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b), nil
}

func decodeBody(r *http.Request, into any) error {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		return fmt.Errorf("malformed JSON body: %w", err)
	}
	return nil
}

// HandleListCatalogs lists the catalogues the caller maintains, lowest rank
// first. It is the maintenance sight, not the portal one: being the audience for
// a catalogue puts nothing in this list, and GET /portal/catalog answers that
// question instead. A customer must not learn from a listing which other
// customers exist.
func (s *Service) HandleListCatalogs(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	out := []Catalog{}
	var loadErr error
	s.loop.Do(func() {
		var all []Catalog
		if all, loadErr = s.store.Catalogs(); loadErr != nil {
			return
		}
		for _, c := range all {
			if s.mayEdit(c, p) {
				out = append(out, c)
			}
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list catalogues: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// HandleCreateCatalog creates one.
func (s *Service) HandleCreateCatalog(w http.ResponseWriter, r *http.Request) {
	var in Catalog
	if err := decodeBody(r, &in); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := newID("cat")
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "mint id: "+err.Error())
		return
	}
	in.ID = id
	in.CreatedAt, in.UpdatedAt = s.now(), s.now()
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		in.OwnerID = p.UserID
	}

	var saveErr error
	s.loop.Do(func() { saveErr = s.store.SaveCatalog(in) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save catalogue: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusCreated, in)
}

// HandleGetCatalog reads one. A missing catalogue is 404 rather than an empty
// object: a reader has to be able to tell "no such catalogue" from "a catalogue
// with nothing in it".
func (s *Service) HandleGetCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())
	var (
		got     Catalog
		found   bool
		loadErr error
	)
	s.loop.Do(func() {
		if got, found, loadErr = s.store.Catalog(id); loadErr != nil || !found {
			return
		}
		// A catalogue somebody may not see reads as absent. A refusal that
		// distinguishes "not yours" from "no such thing" answers the question it
		// was meant to withhold.
		found = s.mayRead(got, p)
	})
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read catalogue: "+loadErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no catalogue "+id)
	default:
		httpapi.JSON(w, http.StatusOK, got)
	}
}

// catalogUpdate is what may be changed about an existing catalogue. Its id, and
// when it was created, are not among them.
type catalogUpdate struct {
	Texts     map[string]string `json:"texts,omitempty"`
	Rank      *int              `json:"rank,omitempty"`
	Languages []string          `json:"languages,omitempty"`
	Items     []string          `json:"items,omitempty"`
	Groups    []string          `json:"groups,omitempty"`
	Members   []Member          `json:"members,omitempty"`
	// Edges are the structure and precedence between the items this catalogue
	// offers. They live with the catalogue because they are what its release is
	// computed from.
	Edges []Edge `json:"edges,omitempty"`
}

// HandleUpdateCatalog changes what a catalogue offers.
func (s *Service) HandleUpdateCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in catalogUpdate
	if err := decodeBody(r, &in); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	p := httpapi.PrincipalFrom(r.Context())
	var (
		got     Catalog
		found   bool
		allowed bool
		loadErr error
	)
	s.loop.Do(func() {
		got, found, loadErr = s.store.Catalog(id)
		if loadErr != nil || !found {
			return
		}
		// Not being allowed to see it hides it; being allowed to see but not
		// change it is a plain refusal, because by then its existence is no longer
		// the secret — only the authority is.
		if found = s.mayRead(got, p); !found {
			return
		}
		if allowed = s.mayEdit(got, p); !allowed {
			return
		}
		if in.Texts != nil {
			got.Texts = in.Texts
		}
		if in.Rank != nil {
			got.Rank = *in.Rank
		}
		if in.Languages != nil {
			got.Languages = in.Languages
		}
		if in.Items != nil {
			got.Items = in.Items
		}
		if in.Groups != nil {
			got.Groups = in.Groups
		}
		if in.Members != nil {
			got.Members = in.Members
		}
		if in.Edges != nil {
			got.Edges = in.Edges
		}
		got.UpdatedAt = s.now()
		loadErr = s.store.SaveCatalog(got)
	})
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "update catalogue: "+loadErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no catalogue "+id)
	case !allowed:
		httpapi.Error(w, http.StatusForbidden, "not an editor of catalogue "+id)
	default:
		httpapi.JSON(w, http.StatusOK, got)
	}
}

// HandleListItems lists the products whose home catalogue the caller maintains.
// A product's rules — its approval, its process bindings — belong to whoever is
// responsible for it, and the portal reads what it may order from a release
// rather than from here.
func (s *Service) HandleListItems(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	out := []Item{}
	var loadErr error
	s.loop.Do(func() {
		var all []Item
		if all, loadErr = s.store.Items(); loadErr != nil {
			return
		}
		// One lookup per home catalogue rather than per product: a maintainer's
		// products share a handful of homes.
		seen := map[string]bool{}
		for _, it := range all {
			ok, known := seen[it.HomeCatalog]
			if !known {
				cat, found, e := s.store.Catalog(it.HomeCatalog)
				if e != nil {
					loadErr = e
					return
				}
				ok = found && s.mayEdit(cat, p)
				seen[it.HomeCatalog] = ok
			}
			if ok {
				out = append(out, it)
			}
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list products: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// HandleSaveItem writes a product, creating or replacing it.
//
// The id comes from the caller rather than being minted here: a product is
// referred to by name in a model, an import and a catalogue's item list, so it
// is a chosen identifier and not an opaque one.
func (s *Service) HandleSaveItem(w http.ResponseWriter, r *http.Request) {
	var in Item
	if err := decodeBody(r, &in); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.ID == "" {
		// The id is the key the store files it under, so a blank one would
		// overwrite whatever else has a blank one.
		httpapi.Error(w, http.StatusBadRequest, "a product needs an id")
		return
	}
	now := s.now()
	if in.CreatedAt == 0 {
		in.CreatedAt = now
	}
	in.UpdatedAt = now

	// A product is referenced by several catalogues and changed through exactly
	// one — its home — so that a catalogue cannot alter a product another
	// catalogue depends on (ADR-draft-portal-roles-and-responsibilities). A home
	// that does not exist is refused rather than tolerated: a product belonging to
	// nothing is a product nobody is responsible for.
	//
	// Both sides are checked, and the losing one is the half that is easy to miss.
	// Changing a product is a write on the catalogue responsible for it *now*, not
	// on the one the request would like it to belong to — otherwise a product is
	// taken by writing it back under somebody else's home, while whoever was
	// responsible for it keeps carrying it and is never told.
	p := httpapi.PrincipalFrom(r.Context())
	var (
		home    Catalog
		homeOK  bool
		allowed bool
		opErr   error
	)
	s.loop.Do(func() {
		home, homeOK, opErr = s.store.Catalog(in.HomeCatalog)
		if opErr != nil || !homeOK {
			return
		}
		// A home the caller cannot see reads as absent, as everywhere else: a
		// refusal that says "it exists, just not for you" answers the question the
		// gate withholds.
		if homeOK = s.mayRead(home, p); !homeOK {
			return
		}
		if allowed = s.mayEdit(home, p); !allowed {
			return
		}

		// The losing side, when there is one. A product whose home no longer
		// resolves has nobody responsible for it, so demanding that side's
		// permission would freeze it forever — nobody can satisfy a check against
		// a catalogue that is not there. The gaining side is still checked, so
		// that is an adoption rather than a free-for-all.
		var (
			prev   Item
			prevOK bool
		)
		if prev, prevOK, opErr = s.store.Item(in.ID); opErr != nil {
			return
		}
		if prevOK && prev.HomeCatalog != in.HomeCatalog {
			was, wasOK, e := s.store.Catalog(prev.HomeCatalog)
			if e != nil {
				opErr = e
				return
			}
			if wasOK {
				// The losing side hides itself the same way: somebody who cannot
				// see the catalogue a product belongs to is told the product is
				// not there, not that it is somebody else's.
				if homeOK = s.mayRead(was, p); !homeOK {
					return
				}
				if allowed = s.mayEdit(was, p); !allowed {
					return
				}
			}
		}

		opErr = s.store.SaveItem(in)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save product: "+opErr.Error())
	case !homeOK:
		httpapi.Error(w, http.StatusNotFound, "no home catalogue "+in.HomeCatalog)
	case !allowed:
		httpapi.Error(w, http.StatusForbidden, "not an editor of catalogue "+in.HomeCatalog)
	default:
		httpapi.JSON(w, http.StatusOK, in)
	}
}

// HandlePublish validates a catalogue and writes the release.
//
// A refused publish writes nothing: the problems are the response, and a release
// is only created once every one of them is gone. Reporting them all at once is
// deliberate — an author who fixes one refusal per attempt is an author who
// eventually works around the gate.
func (s *Service) HandlePublish(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	relID, err := newID("rel")
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "mint id: "+err.Error())
		return
	}

	p := httpapi.PrincipalFrom(r.Context())
	var (
		rel      Release
		problems []Problem
		found    bool
		allowed  bool
		opErr    error
	)
	s.loop.Do(func() {
		var cat Catalog
		if cat, found, opErr = s.store.Catalog(id); opErr != nil || !found {
			return
		}
		if found = s.mayRead(cat, p); !found {
			return
		}
		// Publishing is what makes a catalogue orderable, so it is a write on it
		// and needs the same authority as changing it.
		if allowed = s.mayEdit(cat, p); !allowed {
			return
		}
		var in Input
		if in, found, opErr = s.store.InputFor(id, cat.Edges); opErr != nil || !found {
			return
		}

		if rel, problems = Publish(in); len(problems) > 0 {
			return
		}
		rel.ID, rel.CatalogID, rel.CreatedAt = relID, id, s.now()
		opErr = s.store.SaveRelease(rel)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "publish: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no catalogue "+id)
	case !allowed:
		httpapi.Error(w, http.StatusForbidden, "not an editor of catalogue "+id)
	case len(problems) > 0:
		httpapi.JSON(w, http.StatusUnprocessableEntity, map[string]any{"problems": problems})
	default:
		httpapi.JSON(w, http.StatusCreated, rel)
	}
}

// HandleListReleases lists a catalogue's releases, newest first.
func (s *Service) HandleListReleases(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())
	out := []Release{}
	var (
		visible bool
		loadErr error
	)
	s.loop.Do(func() {
		cat, found, e := s.store.Catalog(id)
		if e != nil {
			loadErr = e
			return
		}
		if visible = found && s.mayRead(cat, p); !visible {
			return
		}
		out, loadErr = s.store.ReleasesOf(id)
	})
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "list releases: "+loadErr.Error())
	case !visible:
		httpapi.Error(w, http.StatusNotFound, "no catalogue "+id)
	default:
		httpapi.JSON(w, http.StatusOK, out)
	}
}
