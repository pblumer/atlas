package api

import (
	"net/http"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/panorama"
	"github.com/pblumer/atlas/compiler"
)

// collectBindingCatalog gathers the Atlas resources a Panorama document's bindings
// resolve against (ADR-0189 §4), filtered for the caller.
//
// Run-loop goroutine only: it reads the project, worker, release and target
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
		Runtimes:     map[string]panorama.ResourceRef{},
		JobTypes:     map[string]panorama.ResourceRef{},
	}

	// Job types are the last kind this catalog could not answer, and the reason it
	// gave was wrong: a job type *is* registered here. The engine keeps an
	// engine-wide table of them (ADR-0007), because a job's index on disk has to
	// mean the same thing to every definition — so "does this server know that job
	// type" is a lookup, and it has been all along. The Workers view has listed the
	// whole table for as long as it has existed.
	//
	// The whole table, reserved and model-authored alike, which is the same set that
	// view shows. A type nothing currently uses is still one the engine knows: an
	// index is never recycled, and whether anything is *doing* that work is a
	// question for an observation rather than for resolution. With the map present, a
	// binding to a type this engine has never seen is *missing* — which is a real
	// finding, and one a model can act on — instead of unsupported.
	//
	// Every caller may see them. A job type is engine-wide infrastructure with no
	// sharing scope of its own, like a deployment target; and here resolution
	// discloses nothing new even in principle, because a job type's id is its name —
	// the caller already has it, in their own document.
	for _, e := range s.jobTypes.All() {
		catalog.JobTypes[e.Name] = panorama.ResourceRef{
			ID: e.Name, Name: jobTypeDisplayName(e.Index), CanView: true,
		}
	}

	// Runtimes are the Atlas nodes a model may bind to. This server knows exactly
	// one of them with certainty — itself (ADR-0189 §6) — so the map is present and
	// holds one entry. That is the honest shape: a binding to this node resolves, a
	// binding to any other id is *missing* here rather than unsupported, which is
	// the true answer until remote descriptors are collected through deployment
	// targets. Every caller may see it: the descriptor is what this server tells any
	// signed-in identity about itself, and a name it already serves on a route of
	// its own is not a secret here.
	node, err := s.nodeIdentity()
	if err != nil {
		return panorama.Catalog{}, err
	}
	catalog.Runtimes[node.ID] = panorama.ResourceRef{
		ID: node.ID, Name: nodeDisplayName(node), CanView: true,
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

// jobTypeDisplayName says what sort of job type this is, rather than repeating the
// id back at the reader.
//
// A job type's id *is* its name, so a panel that showed the name beside the id would
// print one string twice and tell nobody anything. What a reader gains instead is
// where the type comes from: one Atlas ships with, or one somebody wrote into a
// model. It is the same distinction the Workers view draws, from the same boundary —
// the reserved count rather than the dynamic floor, because a legacy assignment sits
// between the two and is model-authored, not built in.
func jobTypeDisplayName(index int32) string {
	if index < compiler.ReservedJobTypeCount() {
		return "Built-in job type"
	}
	return "Model-authored job type"
}

// releaseDisplayName names a release the way a person would: the application it
// belongs to and the version it shipped.
func releaseDisplayName(application string, version int32) string {
	return application + " v" + strconv.FormatInt(int64(version), 10)
}

// nodeDisplayName names a runtime the way an architect binding to it would
// recognise it: the operator's own name for the node, qualified by its
// environment. It falls back to the product name rather than to the id, because an
// id repeated as its own label tells a reader nothing they did not already have.
func nodeDisplayName(n nodeIdentity) string {
	name := n.Name
	if name == "" {
		name = "Atlas"
	}
	if n.Environment != "" {
		return name + " (" + n.Environment + ")"
	}
	return name
}
