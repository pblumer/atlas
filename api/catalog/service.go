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
}

// New builds the service. Every dependency is an explicit argument (ADR-0147).
func New(loop *runloop.Loop, store *Store, now func() int64) *Service {
	return &Service{loop: loop, store: store, now: now}
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

	var (
		got     Catalog
		found   bool
		loadErr error
	)
	s.loop.Do(func() {
		got, found, loadErr = s.store.Catalog(id)
		if loadErr != nil || !found {
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

	var saveErr error
	s.loop.Do(func() { saveErr = s.store.SaveItem(in) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save product: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, in)
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

	var (
		rel      Release
		problems []Problem
		found    bool
		opErr    error
	)
	s.loop.Do(func() {
		var in Input
		in, found, opErr = s.store.InputFor(id, nil)
		if opErr != nil || !found {
			return
		}
		var cat Catalog
		if cat, found, opErr = s.store.Catalog(id); opErr != nil || !found {
			return
		}
		in.Edges = cat.Edges

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
