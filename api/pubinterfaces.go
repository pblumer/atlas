package api

import (
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/pubinterface"
)

// The server's half of the published process interfaces (ADR-0373): what the
// pubinterface service asks of the deployment registry, of the applications and of
// the identity directory. Definition, Rank, UserExists and GroupExists read state
// the run loop owns and are called only from inside the service's loop turn; Names
// dispatches onto the loop itself and is called outside it.
func (s *Server) pubInterfaceHost() pubinterface.Host {
	return pubinterface.Host{
		Definition: func(key uint64) (pubinterface.Definition, bool) {
			d, ok := s.deployments[key]
			if !ok {
				return pubinterface.Definition{}, false
			}
			return pubinterface.Definition{
				Key: d.Key, ProcessID: d.ProcessID, Version: d.Version, ApplicationID: d.ProjectID,
				Inactive: d.inactive, Process: d.cp,
			}, true
		},
		// The caller's role on the application is the sharing scope's (ADR-0071), the
		// same one every other per-application route reads: an administrator resolves
		// as owner, and a server without authentication makes everybody one.
		Rank: func(p *httpapi.Principal, applicationID string) (int, error) {
			app, ok, err := s.projects.Get(applicationID)
			if err != nil || !ok {
				return pubinterface.RankNone, err
			}
			return scopeRank(app.effectiveRole(p, s.authEnabled)), nil
		},
		UserExists: func(id string) (bool, error) {
			_, ok, err := s.users.Get(id)
			return ok, err
		},
		GroupExists: func(id string) (bool, error) {
			_, ok, err := s.groups.Get(id)
			return ok, err
		},
		Names: s.elementNamesOf,
	}
}
