// Package startgrant evaluates the independent, least-privilege mayStart
// audience of ADR-0516. It is not a route guard: callers must also validate
// human authentication, publication, version and start form on the server.
package startgrant

import "strings"

// Grant is a process-scoped audience configured independently of app sharing.
// Persistence and admin audit belong to the design-time sidecar, not the WAL.
type Grant struct {
	ApplicationID string   `json:"applicationId"`
	ProcessID     string   `json:"processId"`
	UserIDs       []string `json:"userIds,omitempty"`
	GroupIDs      []string `json:"groupIds,omitempty"`
}

// Principal is the authenticated user's current identity and group membership.
// A zero UserID cannot authorize a start even when groups match.
type Principal struct {
	UserID   string
	GroupIDs []string
}

// Allowed evaluates an explicit grant for exactly one application/process pair.
// The caller supplies current group membership and a current durable grant
// snapshot; no cached positive decision may survive a persisted revocation.
// It deliberately does not infer permission from viewer/editor or operator.
func Allowed(grants []Grant, applicationID, processID string, p Principal) bool {
	if strings.TrimSpace(p.UserID) == "" || strings.TrimSpace(applicationID) == "" || strings.TrimSpace(processID) == "" {
		return false
	}
	for _, g := range grants {
		if g.ApplicationID != applicationID || g.ProcessID != processID {
			continue
		}
		for _, userID := range g.UserIDs {
			if userID != "" && userID == p.UserID {
				return true
			}
		}
		for _, groupID := range g.GroupIDs {
			if groupID == "" {
				continue
			}
			for _, member := range p.GroupIDs {
				if member == groupID {
					return true
				}
			}
		}
	}
	return false
}
