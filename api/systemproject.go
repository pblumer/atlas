package api

import "net/http"

// This file backs ADR-0122: a protected, platform-managed "system" project that
// holds Atlas's own operating processes (user intake, access review, offboarding).
// It is created at startup and cannot be renamed, deleted, reshared, or written
// into through the design-time API — by any caller, admins included.
//
// This slice establishes the protected project and its enforcement. Embedding the
// process/form bundle and bootstrap-*deploying* it into this project (idempotent
// by content checksum) is the ADR's follow-up slice; the protection surface is
// the part the ADR calls out as expensive to change later, so it lands first.

const (
	// systemProjectID is the stable, reserved id of the protected system project.
	// It is a fixed string (not a random newID) so the bootstrap is idempotent by
	// lookup and never scans; user-created projects use random hex ids and can
	// never collide with it.
	systemProjectID = "system"
	// systemOwnerID is the reserved owner principal of the system project. It is
	// intentionally distinct from servicePrincipalName ("system:mcp", ADR-0049),
	// which stays non-admin: the bootstrap owner exists only to own this project.
	systemOwnerID = "system"
	// systemProjectName is the display name shown in the Modeler.
	systemProjectName = "Atlas System"
)

// ensureSystemProject makes the protected system project exist and stay protected
// (ADR-0122). It is idempotent: a second run is a no-op, and a record that has
// lost its protection/owner (e.g. hand-edited on disk) is repaired in place with
// its timestamps preserved. It runs on the constructing goroutine before the run
// loop serves traffic — the same single-writer discipline bootstrapAdmin uses — so
// it touches the project store directly rather than through s.do.
func (s *Server) ensureSystemProject(now int64) error {
	existing, ok, err := s.projects.get(systemProjectID)
	if err != nil {
		return err
	}
	if ok {
		if existing.Protected && existing.OwnerID == systemOwnerID {
			return nil
		}
		existing.Protected = true
		existing.OwnerID = systemOwnerID
		existing.UpdatedAt = now
		return s.projects.save(existing)
	}
	return s.projects.save(project{
		ID:         systemProjectID,
		Name:       systemProjectName,
		OwnerID:    systemOwnerID,
		Visibility: VisibilityShared,
		Protected:  true,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
}

// protectedGuard returns a 403 status and message when a project is protected — a
// platform-managed system project (ADR-0122) that no caller, admin included, may
// modify through the design-time API — and 0 otherwise. Handlers call it after
// their normal role check, so protection is an additional refusal layered on top
// of authorization, not a replacement for it.
func protectedGuard(p project) (int, string) {
	if p.Protected {
		return http.StatusForbidden, "protected system project cannot be modified"
	}
	return 0, ""
}
