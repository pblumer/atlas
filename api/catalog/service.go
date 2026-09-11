package catalog

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
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
}

// New builds the service. Every dependency is an explicit argument (ADR-0147).
func New(loop *runloop.Loop, store *Store, now func() int64, admin func(*httpapi.Principal) bool) *Service {
	return &Service{loop: loop, store: store, now: now, admin: admin}
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
	for _, m := range c.Members {
		if m.Role != RoleEditor {
			continue
		}
		switch m.Ref.Type {
		case "user":
			if m.Ref.ID == p.UserID {
				return true
			}
		case "group":
			if p.InGroup(m.Ref.ID) {
				return true
			}
		}
	}
	return false
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

// HandleListCatalogs lists every catalogue, lowest rank first.
func (s *Service) HandleListCatalogs(w http.ResponseWriter, r *http.Request) {
	out := []Catalog{}
	var loadErr error
	s.loop.Do(func() { out, loadErr = s.store.Catalogs() })
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
	var (
		got     Catalog
		found   bool
		loadErr error
	)
	s.loop.Do(func() { got, found, loadErr = s.store.Catalog(id) })
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

// HandleListItems lists every product, by id.
func (s *Service) HandleListItems(w http.ResponseWriter, r *http.Request) {
	out := []Item{}
	var loadErr error
	s.loop.Do(func() { out, loadErr = s.store.Items() })
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
		if allowed = s.mayEdit(home, p); !allowed {
			return
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
	out := []Release{}
	var loadErr error
	s.loop.Do(func() { out, loadErr = s.store.ReleasesOf(id) })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list releases: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}
