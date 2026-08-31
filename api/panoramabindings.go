package api

import (
	"net/http"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/panorama"
)

// collectBindingCatalog gathers the Atlas resources a Panorama document's bindings
// resolve against (ADR-0189 §4), filtered for the caller.
//
// Run-loop goroutine only: it reads the project, connector, release and target
// stores and the deployment registry.
//
// A map that is present but empty means "looked, holds none"; a map left absent
// means "this server cannot resolve that kind yet", which the resolver reports as
// unsupported rather than missing. The difference matters: missing is the claim
// that nothing here has that id, and for a kind nobody looked up it would be false.
func (s *Server) collectBindingCatalog(r *http.Request) (panorama.Catalog, error) {
	projs, err := s.projectsByID()
	if err != nil {
		return panorama.Catalog{}, err
	}
	catalog := panorama.Catalog{
		Applications: map[string]panorama.ResourceRef{},
		Processes:    map[string]panorama.ResourceRef{},
		Connectors:   map[string]panorama.ResourceRef{},
		Targets:      map[string]panorama.ResourceRef{},
		Releases:     map[string]panorama.ResourceRef{},
		// JobTypes and Runtimes are deliberately absent. A job type is authored in a
		// model rather than registered as a resource, and a stable runtime id is the
		// node descriptor ADR-0189 §6 defines and P4 delivers. Supplying an empty map
		// for either would turn "not built yet" into "no such resource".
	}

	for _, p := range projs {
		catalog.Applications[p.ID] = panorama.ResourceRef{
			ID: p.ID, Name: p.Name,
			CanView: s.canViewArtifact(r, p.ID, p.OwnerID, projs),
		}
		releases, err := s.releases.forApplication(p.ID)
		if err != nil {
			return panorama.Catalog{}, err
		}
		for _, rel := range releases {
			// A release inherits its application's scope: it is that application's
			// content, and ADR-0071 governs it through the project like every other
			// artifact.
			catalog.Releases[rel.ID] = panorama.ResourceRef{
				ID: rel.ID, Name: releaseDisplayName(p.Name, rel.Version),
				CanView: catalog.Applications[p.ID].CanView,
			}
		}
	}

	// A BPMN process is bound by its process id, not by a deployment key: the model
	// names the process, and which version is deployed is a runtime fact the binding
	// must not freeze.
	for pid := range s.versions {
		d := s.latestDeploymentByProcessID(pid)
		if d == nil {
			continue
		}
		catalog.Processes[pid] = panorama.ResourceRef{
			ID: pid, Name: d.Name,
			CanView: s.canViewArtifact(r, d.ProjectID, d.DeployedBy, projs),
		}
	}

	conns, err := s.connectors.LoadAll()
	if err != nil {
		return panorama.Catalog{}, err
	}
	principal := httpapi.PrincipalFrom(r.Context())
	for _, c := range conns {
		// Name and kind only; a catalog entry never carries an endpoint or a
		// credential reference (ADR-0189 §4, I6).
		catalog.Connectors[c.ID] = panorama.ResourceRef{
			ID: c.ID, Name: c.Name,
			CanView: scopeRank(connectorRole(c, principal, s.authEnabled)) >= scopeRank(ScopeRoleViewer),
		}
	}

	targets, err := s.targets.LoadAll()
	if err != nil {
		return panorama.Catalog{}, err
	}
	for _, t := range targets {
		// A deployment target is org-wide infrastructure with no sharing scope of its
		// own, so there is nothing to filter on. Its base URL and credential
		// reference stay out regardless.
		catalog.Targets[t.ID] = panorama.ResourceRef{ID: t.ID, Name: t.Name, CanView: true}
	}
	return catalog, nil
}

// releaseDisplayName names a release the way a person would: the application it
// belongs to and the version it shipped.
func releaseDisplayName(application string, version int32) string {
	return application + " v" + strconv.FormatInt(int64(version), 10)
}
