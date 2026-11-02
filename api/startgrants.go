package api

import (
	"slices"

	"github.com/pblumer/atlas/api/startgrant"
)

// The server's half of the start-grant administration (ADR-0516): what the
// startgrant service asks of the identity directory and of the deployment registry.
// Every function here reads state the run loop owns and is called only from inside
// the service's loop turn.

// startGrantDirectory resolves the identities a grant names, by id only.
func (s *Server) startGrantDirectory() startgrant.Directory {
	return startgrant.Directory{
		User: func(id string) (startgrant.Account, bool, error) {
			u, ok, err := s.users.Get(id)
			if err != nil || !ok {
				return startgrant.Account{}, ok, err
			}
			return startgrant.Account{
				Disabled: u.Disabled,
				// The self-service routes are declared for the user role, which admin
				// reaches too (mayReach). The grant is the second key, never the first.
				SelfService: slices.Contains(u.Roles, RoleUser) || slices.Contains(u.Roles, RoleAdmin),
			}, true, nil
		},
		Group: func(id string) (bool, error) {
			_, ok, err := s.groups.Get(id)
			return ok, err
		},
		GroupsOf: s.groups.idsForUser,
	}
}

// startGrantTarget is what the server knows about one application/process pair
// now. An empty processID asks about the application alone.
func (s *Server) startGrantTarget(applicationID, processID string) (startgrant.Target, error) {
	app, found, err := s.projects.Get(applicationID)
	if err != nil {
		return startgrant.Target{}, err
	}
	t := startgrant.Target{ApplicationFound: found, ApplicationProtected: found && app.Protected}
	if processID == "" {
		return t, nil
	}
	d := s.latestDeploymentByProcessID(processID)
	if d != nil {
		t.DefinitionKey, t.Version = d.Key, d.Version
	}
	t.ProcessInApplication = found && d != nil && d.ProjectID == applicationID
	t.Blockers = s.selfServiceBlockers(applicationID, t, d)
	return t, nil
}

// selfServiceBlockers says why the newest deployed version of a process would not
// be offered to a person holding a grant for it. The rule is one function so the
// administrator's preview and the self-service routes cannot disagree about it.
//
// It is the newest version or nothing. A grant follows the process within its
// application, so a version superseded by a newer deployment is never offered,
// and neither is any version once the newest one was filed under another
// application: a process id reused elsewhere is not this grant's to start
// (ADR-0516).
func (s *Server) selfServiceBlockers(applicationID string, t startgrant.Target, d *deployment) []string {
	var out []string
	switch {
	case !t.ApplicationFound:
		out = append(out, startgrant.BlockApplicationMissing)
	case t.ApplicationProtected:
		out = append(out, startgrant.BlockApplicationProtected)
	}
	switch {
	case d == nil:
		return append(out, startgrant.BlockNotDeployed)
	case d.ProjectID != applicationID:
		return append(out, startgrant.BlockOtherApplication)
	}
	if d.cp == nil || !d.cp.IsExecutable() {
		out = append(out, startgrant.BlockNotExecutable)
	}
	if d.inactive {
		out = append(out, startgrant.BlockInactive)
	}
	if untriggeredStartRefusal(d.cp) != "" {
		out = append(out, startgrant.BlockTriggerOnly)
	}
	if s.boundConstantRefusalOnLoop(d) != "" {
		out = append(out, startgrant.BlockConstantsMissing)
	}
	return out
}
