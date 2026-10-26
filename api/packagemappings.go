package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/apppkg"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/sidecar"
)

// The table of names an installation keeps for the packages it imports (ADR-0467,
// amended 2026-10-21).
//
// A package names a catalogue's groups, people and approvers by the names the
// exporting server gives them, and the importing server finds them by name. When INT
// calls a group "Fachbereich Bau" and PROD calls it "FB Bau", nothing is found, and
// every import asks the person again. This table is PROD's answer, kept on PROD: the
// package's name of a kind, and the principal it means here. It is filled from the
// choices people make when a name is not found — the Console's dialog, `atlas import
// --set` — and read before the directory, so a question is asked once per target and
// not once per import. It is this installation's, like the bases an import compares
// with, and means nothing on another.

// packageMapping is one entry: apppkg.Mapping, with who set it and when.
type packageMapping struct {
	apppkg.Mapping
	UpdatedAt int64  `json:"updatedAt"`
	UpdatedBy string `json:"updatedBy,omitempty"`
}

// packageMappingStore keeps one JSON file per entry, keyed by kind and name — the
// sidecar approach every operator store uses. Owned by the run-loop goroutine.
type packageMappingStore = sidecar.Store[packageMapping]

func newPackageMappingStore(dir string) (*packageMappingStore, error) {
	return sidecar.NewStore(dir, "packagemappingstore", func(rec packageMapping) string { return mappingKey(rec.Kind, rec.Answer) })
}

func mappingKey(kind, answer string) string { return kind + "\x00" + answer }

// packageMappingsOnLoop is the table, sorted by kind and name. Run loop only.
func (s *Server) packageMappingsOnLoop() ([]packageMapping, error) {
	recs, err := s.mappings.LoadAll()
	if err != nil {
		return nil, err
	}
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Kind != recs[j].Kind {
			return recs[i].Kind < recs[j].Kind
		}
		return recs[i].Answer < recs[j].Answer
	})
	return recs, nil
}

// packageMappings is the table as apppkg reads it.
func (s *Server) packageMappings() ([]apppkg.Mapping, error) {
	var (
		recs []packageMapping
		err  error
	)
	s.do(func() { recs, err = s.packageMappingsOnLoop() })
	out := make([]apppkg.Mapping, 0, len(recs))
	for _, rec := range recs {
		out = append(out, rec.Mapping)
	}
	return out, err
}

// packageMappingView is an entry as GET answers it: whether the principal it names
// is still here, and its name here now.
type packageMappingView struct {
	packageMapping
	// Here says the principal still exists on this server; an entry naming one that
	// does not is skipped by every import until it is corrected or removed.
	Here bool `json:"here"`
}

// handleListPackageMappings serves GET /api/v1/catalogs/import/mappings.
func (s *Server) handleListPackageMappings(w http.ResponseWriter, _ *http.Request) {
	var (
		recs []packageMapping
		err  error
	)
	s.do(func() { recs, err = s.packageMappingsOnLoop() })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read the table: "+err.Error())
		return
	}
	dir, err := s.packageDirectory()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read the directory: "+err.Error())
		return
	}
	out := make([]packageMappingView, 0, len(recs))
	for _, rec := range recs {
		view := packageMappingView{packageMapping: rec}
		if id, _, why := apppkg.FindPrincipal(dir, rec.Kind, rec.ID); why == "" && id == rec.ID {
			view.Here = true
			for _, e := range dir {
				if e.Type == rec.Kind && e.ID == rec.ID {
					view.Name = e.Name
				}
			}
		}
		out = append(out, view)
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleSetPackageMappings serves POST /api/v1/catalogs/import/mappings: body
// {"mappings": [{kind, answer, id}]}, each a group, user or account. Every entry is
// checked before any is written: the principal must exist here — a group or user by
// its id, an account by its username, ignoring case, which is stored as this server
// spells it — so the table never sends an import to nobody. An entry of the same kind
// and name is replaced.
func (s *Server) handleSetPackageMappings(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Settings))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var body struct {
		Mappings []apppkg.Mapping `json:"mappings"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	dir, err := s.packageDirectory()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read the directory: "+err.Error())
		return
	}
	updatedBy := ""
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		updatedBy = p.UserID
	}
	now := time.Now().Unix()
	recs := make([]packageMapping, 0, len(body.Mappings))
	for _, m := range body.Mappings {
		m.Kind, m.Answer, m.ID = strings.TrimSpace(m.Kind), strings.TrimSpace(m.Answer), strings.TrimSpace(m.ID)
		if m.Kind != apppkg.KindGroup && m.Kind != apppkg.KindUser && m.Kind != apppkg.KindAccount {
			httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf("kind %q is not group, user or account", m.Kind))
			return
		}
		if m.Answer == "" {
			httpapi.Error(w, http.StatusBadRequest, "an entry names no name from the package")
			return
		}
		var found *apppkg.DirectoryEntry
		for i, e := range dir {
			if e.Type == m.Kind && (e.ID == m.ID || m.Kind == apppkg.KindAccount && strings.EqualFold(e.ID, m.ID)) {
				found = &dir[i]
			}
		}
		if found == nil {
			httpapi.Error(w, http.StatusUnprocessableEntity, fmt.Sprintf("no %s %q on this server for %q", m.Kind, m.ID, m.Answer))
			return
		}
		m.ID, m.Name = found.ID, found.Name
		recs = append(recs, packageMapping{Mapping: m, UpdatedAt: now, UpdatedBy: updatedBy})
	}
	var saveErr error
	s.do(func() {
		for _, rec := range recs {
			if saveErr = s.mappings.Save(rec); saveErr != nil {
				return
			}
		}
	})
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "write the table: "+saveErr.Error())
		return
	}
	s.handleListPackageMappings(w, r)
}

// handleDeletePackageMapping serves DELETE /api/v1/catalogs/import/mappings?kind=&answer=.
func (s *Server) handleDeletePackageMapping(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key := mappingKey(q.Get("kind"), q.Get("answer"))
	var (
		found  bool
		delErr error
	)
	s.do(func() {
		if _, found, delErr = s.mappings.Get(key); delErr != nil || !found {
			return
		}
		delErr = s.mappings.Delete(key)
	})
	switch {
	case delErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "write the table: "+delErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, fmt.Sprintf("the table has no %s entry for %q", q.Get("kind"), q.Get("answer")))
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
