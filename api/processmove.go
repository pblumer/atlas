package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
)

// handleMoveProcess files a deployed definition under an application, or takes it
// out of one (ADR-0034). Body: {"projectId": "..."}; empty means Ungrouped.
//
// A deployment carries its own project id, stamped once at deploy time — from the
// ?projectId= the editor sends, or inherited from the matching draft at that moment,
// or empty. Until now nothing could change it afterwards. Moving the *draft* moved
// the draft; the deployment kept whatever it was filed under, so a definition
// deployed through the raw API, or before its application existed, stayed Ungrouped
// for good — visible on the Modeler home and on the Starmap as a process belonging
// to nothing, with a redeploy the only way out and a version bump the price of it.
//
// It is deliberately the smallest possible change: the record's project id and
// nothing else. Not the version, not the model, not the active flag, and nothing the
// engine holds — filing is design-time metadata (the Modeler's grouping and the
// landscape's containment edge) and the processor never reads it. A move that
// bumped a version would be a deploy wearing a smaller name, and an operator would
// have no reason to run it against a system with work in flight.
//
// Two things it moves that the caller did not name, both because the alternative is
// an estate that cannot be put back together:
//
//   - Every version of the definition. Filing belongs to the process, not to one of
//     its versions; leaving v1 behind would show one process in two folders on the
//     Modeler home, and would leave the Starmap — which draws the current version —
//     looking fixed while its history disagreed.
//   - The other pools of a collaboration. They are one drawing, deployed together
//     and listed as one row, so there is no way to address the others separately;
//     moving the row has to move the drawing.
func (s *Server) handleMoveProcess(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var payload struct {
		ProjectID string `json:"projectId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, `invalid body: expected {"projectId": "..."}`)
		return
	}

	// First loop turn: name what would move and what it currently belongs to. The
	// authorization below calls onto the loop itself, so it cannot run inside this —
	// Loop.Do must not recurse (I3).
	var (
		found     bool
		processID string
		keys      []uint64
		// scopes is every distinct (project, deployer) this move would take something
		// out of. Normally one — a drawing and its versions are filed together — but
		// it is collected rather than assumed: if v1 sits in an application this
		// caller may not edit and v3 is Ungrouped, addressing v3 must not become a way
		// to empty that application.
		scopes [][2]string
	)
	s.do(func() {
		d, ok := s.deployments[key]
		if !ok {
			return
		}
		found = true
		processID = d.ProcessID
		pools := map[string]bool{}
		for _, sib := range s.poolSiblings(d) {
			pools[sib.ProcessID] = true
		}
		seen := map[[2]string]bool{}
		for _, k := range s.order {
			dep := s.deployments[k]
			if !pools[dep.ProcessID] {
				continue
			}
			keys = append(keys, k)
			scope := [2]string{dep.ProjectID, dep.DeployedBy}
			if !seen[scope] {
				seen[scope] = true
				scopes = append(scopes, scope)
			}
		}
	})
	if !found {
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
		return
	}

	// Editor on both ends, as moving a draft already requires (ADR-0071): every scope
	// this takes something out of, and — when it names one — the application it joins.
	for _, scope := range scopes {
		if code, msg := s.authorizeArtifact(r, scope[0], scope[1], ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
	}
	if payload.ProjectID != "" {
		if code, msg := s.authorizeTargetProject(r, payload.ProjectID, ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
		// A protected system application's content is platform-managed (ADR-0122):
		// refuse for any caller, backstopping the scope check above exactly as the
		// draft move does. Two doors into one room and only one of them locked is the
		// same as no lock.
		var (
			protected bool
			readErr   error
		)
		s.do(func() {
			if proj, ok, e := s.projects.Get(payload.ProjectID); e != nil {
				readErr = e
			} else if ok && proj.Protected {
				protected = true
			}
		})
		if readErr != nil {
			httpapi.Error(w, http.StatusInternalServerError, "read application: "+readErr.Error())
			return
		}
		if protected {
			httpapi.Error(w, http.StatusForbidden, "protected system application cannot be modified")
			return
		}
	}

	// Second loop turn: the write. Durable before visible (I2, ADR-0019) per record —
	// the display copy is swapped only after its own record is on disk, so a persist
	// that fails halfway leaves the store and the registry agreeing on what happened.
	var (
		loadErr    error
		persistErr error
		moved      []uint64
	)
	s.do(func() {
		for _, k := range keys {
			d, ok := s.deployments[k]
			if !ok {
				// Deleted between the two turns. Skipping is the honest answer: there is
				// nothing left to file, and inventing a record for it would be worse.
				continue
			}
			if d.ProjectID == payload.ProjectID {
				continue // Already there. An idempotent move rewrites nothing.
			}
			// The full record, so the rewrite preserves the XML and the DMN models the
			// in-memory deployment does not hold.
			rec, ok, err := s.deploys.load(k)
			if err != nil {
				loadErr = err
				return
			}
			if !ok {
				// The registry and the sidecar have diverged (should not happen). Skip
				// rather than write a record this server invented.
				continue
			}
			rec.ProjectID = payload.ProjectID
			if err := s.deploys.Save(rec); err != nil {
				persistErr = err
				return
			}
			d.ProjectID = payload.ProjectID
			moved = append(moved, k)
		}
	})
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read deployment: "+loadErr.Error())
	case persistErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "persist deployment: "+persistErr.Error())
	default:
		sort.Slice(moved, func(i, j int) bool { return moved[i] < moved[j] })
		httpapi.JSON(w, http.StatusOK, map[string]any{
			"key": key, "processId": processID, "projectId": payload.ProjectID,
			// Which records actually changed, so a caller moving an estate can see that
			// a definition's whole history came with it — and that an already-filed one
			// cost nothing.
			"moved": moved,
		})
	}
}
