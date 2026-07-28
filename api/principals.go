package api

import "net/http"

// This file serves the principals directory (ADR-0073): the minimal, id-keyed
// identity list that member and assignee pickers need. A scope member is a
// principalRef{ type, id } (ADR-0071), so a picker must reference the stable,
// opaque User.ID — never a mutable username. The admin-only management list
// (GET /users) cannot serve this, so a non-admin owner gets it here instead.

// principalDirEntry is one directory entry: the stable id a scope grant (or a
// future assignee-by-id flow) points at, tagged with a type so groups can join
// later as type "group" (ADR-0071), plus a display name for the picker. It
// carries nothing sensitive — no email, roles, disabled flag, source, or hash —
// so it is safe to expose to any authenticated caller.
type principalDirEntry struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// handleListPrincipals returns the principals directory: every enabled user as a
// { type, id, name } entry. Like the assignable-users list (ADR-0045) it is not
// admin-gated — sharing a project or picking an assignee is an everyday action,
// not user administration — so any authenticated caller may read it, and it is
// open when auth is off. It only reads the user sidecar on the run loop, so it
// touches none of the six invariants.
func (s *Server) handleListPrincipals(w http.ResponseWriter, _ *http.Request) {
	list := []principalDirEntry{}
	var loadErr error
	s.do(func() {
		var recs []User
		recs, loadErr = s.users.loadAll()
		for _, u := range recs {
			if u.Disabled {
				continue
			}
			name := u.DisplayName
			if name == "" {
				name = u.Username
			}
			list = append(list, principalDirEntry{Type: PrincipalTypeUser, ID: u.ID, Name: name})
		}
	})
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list principals: "+loadErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}
