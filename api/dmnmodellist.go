package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// The model store, listed (ADR-0330).
//
// A DMN reference (ADR-0034) is what puts a stored model in front of an author:
// the decision catalog reads references, the picker reads the catalog, the Modeler
// lists references. The model *file* is a rung below that and was never listed
// anywhere — so deleting the last reference to a model left the file on disk and
// out of reach of everything except a caller who still remembered its handle.
//
// This lists what is actually in the store, and says which entries nothing points
// at. It is the only route that reads the model folder as a folder; everything else
// resolves one handle at a time.

// dmnModelResp is one model in the store: the handle it resolves under, what the
// model says it is, and whether any DMN reference points at it.
type dmnModelResp struct {
	Handle    string   `json:"handle"`
	ModelName string   `json:"modelName,omitempty"`
	Decisions []string `json:"decisions"`
	// Referenced is computed over *every* reference, including ones the caller
	// cannot see (ADR-0071). Reporting a model as unreferenced because of who is
	// looking would invite a second reference to a model that already has one.
	Referenced bool `json:"referenced"`
	// References are the pointing references the caller may see, so a model that is
	// referenced can say by what. Empty on a model the caller cannot see the
	// references of, which Referenced still reports honestly.
	References []dmnRefResp `json:"references"`
	// Valid is false for a file in the store that does not compile. It is listed
	// anyway: an author who has to fix it has to find it first.
	Valid bool `json:"valid"`
}

// handleListDmnModels lists the local DMN model store, newest handle order aside —
// sorted by handle, because that is the name a reference resolves by and the order
// a reader scans.
//
// Reference records are run-loop state and are read there; resolving and compiling
// each model is I/O and CPU and runs off it, the way the decision catalog already
// works. A model that does not compile is listed as invalid rather than skipped.
func (s *Server) handleListDmnModels(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.dmnModelDir()
	if !ok {
		// A remote temis service owns its own catalog; Atlas has no folder to read.
		httpapi.Error(w, http.StatusConflict,
			"DMN models are served by a remote temis service (ATLAS_DMN_RESOLVER_URL); list them there")
		return
	}
	handles, err := storedModelHandles(dir)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read model store: "+err.Error())
		return
	}

	var (
		refs    []dmnRef
		projs   map[string]project
		loadErr error
	)
	s.do(func() {
		if refs, loadErr = s.dmnrefs.LoadAll(); loadErr != nil {
			return
		}
		projs, loadErr = s.projectsByID()
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list dmn references: "+loadErr.Error())
		return
	}
	pointing := map[string][]dmnRef{}
	for _, rec := range refs {
		pointing[rec.ModelRef] = append(pointing[rec.ModelRef], rec)
	}

	out := make([]dmnModelResp, 0, len(handles))
	for _, h := range handles {
		row := dmnModelResp{Handle: h, Decisions: []string{}, References: []dmnRefResp{}}
		res, err := s.dmnValidator.Validate(r.Context(), h)
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "resolve dmn model: "+err.Error())
			return
		}
		row.Valid = res.Valid
		row.ModelName = res.ModelName
		if res.Decisions != nil {
			row.Decisions = res.Decisions
		}
		for _, rec := range pointing[h] {
			row.Referenced = true
			if s.canViewArtifact(r, rec.ProjectID, rec.OwnerID, projs) {
				row.References = append(row.References, toDmnRefResp(rec))
			}
		}
		out = append(out, row)
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// storedModelHandles is the inverse of the write path's naming: every .dmn/.xml
// file in the store, as the handle a reference resolves by. Sorted and
// de-duplicated, so a model stored under both extensions is one entry — which is
// what Resolve would answer for it.
func storedModelHandles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil // nothing uploaded yet is an empty store, not a failure
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".dmn" && ext != ".xml" {
			continue
		}
		h := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

// handleDeleteDmnModel removes a stored DMN model file
// (ADR-0336). It is the other half of a store that
// only ever grew: every upload stayed, and ADR-0330 made the unreferenced ones
// visible without making them removable.
//
// One thing has to be true first: no DMN reference may point at the handle. A
// reference whose model is gone is an unresolved reference — a state Atlas
// tolerates and reports, but not one a deletion should create behind an author's
// back, and the remedy (delete the reference, or point it somewhere) is the
// author's to choose.
//
// A decision deployment's modelRef deliberately does **not** block it. That field
// is provenance: the record carries its own XML and is rebuilt from it at startup
// (ADR-0319), so a deployed decision keeps evaluating exactly what it was deployed
// with whether or not the file it came from still exists. Refusing here would tie
// a runtime artifact's lifetime to a design-time file it does not read.
func (s *Server) handleDeleteDmnModel(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.dmnModelDir()
	if !ok {
		httpapi.Error(w, http.StatusConflict,
			"DMN models are served by a remote temis service (ATLAS_DMN_RESOLVER_URL); remove them there")
		return
	}
	handle := sanitizeHandle(r.PathValue("ref"))
	if handle == "" {
		httpapi.Error(w, http.StatusBadRequest, "invalid model handle")
		return
	}

	var (
		pointing []dmnRef
		loadErr  error
	)
	s.do(func() {
		var refs []dmnRef
		if refs, loadErr = s.dmnrefs.LoadAll(); loadErr != nil {
			return
		}
		for _, rec := range refs {
			if rec.ModelRef == handle {
				pointing = append(pointing, rec)
			}
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list dmn references: "+loadErr.Error())
		return
	}
	if len(pointing) > 0 {
		// Named, not counted: a reference the caller cannot see still blocks, and
		// saying so without naming it would leave them with no way forward. The names
		// are a display name and a handle, which the model store listing already shows
		// to any modeler.
		names := make([]string, 0, len(pointing))
		for _, rec := range pointing {
			names = append(names, rec.Name)
		}
		httpapi.Error(w, http.StatusConflict, fmt.Sprintf(
			"%d DMN reference(s) point at %q: %v. Deleting the model would leave them unresolved — delete the reference first, or point it at another model",
			len(pointing), handle, names))
		return
	}

	removed, err := removeStoredModel(dir, handle)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete model: "+err.Error())
		return
	}
	if !removed {
		// Already gone is the outcome the caller asked for.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// removeStoredModel deletes the file (or files) a handle resolves through, and
// reports whether anything was there. Both extensions the resolver accepts are
// removed, because both are the same handle as far as a reference is concerned —
// leaving one behind would make a deleted model come back.
func removeStoredModel(dir, handle string) (bool, error) {
	removed := false
	for _, ext := range []string{".dmn", ".xml"} {
		err := os.Remove(filepath.Join(dir, handle+ext))
		switch {
		case err == nil:
			removed = true
		case os.IsNotExist(err):
		default:
			return removed, err
		}
	}
	return removed, nil
}
