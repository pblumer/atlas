package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/panorama"
)

// meshMaxNodes is the landscape mesh's size budget (ADR-0211 §7). Over it the
// graph collapses to applications and says so, rather than handing the browser a
// graph it cannot lay out. The number is a starting point to be replaced by a
// measured one; it is deliberately a single constant so the measurement has one
// place to land.
const meshMaxNodes = 400

// collectLandscape reads this server's resources and returns them filtered for the
// caller, as the input the derived mesh is computed from (ADR-0211 §1).
//
// Run-loop goroutine only: it reads the project store, the call-override store and
// the deployment registry.
//
// Two things it deliberately does not do. It does not invent a visibility rule:
// applications defer to their sharing scope (ADR-0071) and a deployment defers to
// its project, falling back to its deployer, which is exactly the rule
// canViewArtifact already applies everywhere else a deployment is filtered. And it
// does not enumerate every deployed version: the landscape is the current state, so
// one node per process id at its current version, which is also what keeps a server
// with a long deploy history inside the size budget.
func (s *Server) collectLandscape(r *http.Request) (panorama.Landscape, error) {
	projs, err := s.projectsByID()
	if err != nil {
		return panorama.Landscape{}, err
	}
	overrides, err := s.callOverrides.LoadAll()
	if err != nil {
		return panorama.Landscape{}, err
	}
	ovByPID := make(map[string]callOverride, len(overrides))
	for _, rec := range overrides {
		ovByPID[rec.CalledProcessID] = rec
	}

	land := panorama.Landscape{}
	for _, p := range projs {
		land.Applications = append(land.Applications, panorama.Application{
			ID: p.ID, Name: p.Name,
			CanView: s.canViewArtifact(r, p.ID, p.OwnerID, projs),
		})
	}

	for pid := range s.versions {
		d := s.latestDeploymentByProcessID(pid)
		if d == nil || d.cp == nil {
			continue
		}
		proc := panorama.Process{
			Key: d.Key, ProcessID: d.ProcessID, Name: d.Name, Version: d.Version,
			ApplicationID: d.ProjectID,
			CanView:       s.canViewArtifact(r, d.ProjectID, d.DeployedBy, projs),
		}
		for _, ref := range d.cp.CallActivities() {
			call := panorama.Call{ElementID: ref.ElementId, CalledProcessID: ref.CalledProcessId}
			// Resolution mirrors the call-activity management view exactly, overrides
			// included: an edge that ignored a redirect or a pin would draw a
			// dependency the engine would not take (ADR-0076/0105).
			var ovPtr *callOverride
			if ov, ok := ovByPID[ref.CalledProcessId]; ok {
				ovCopy := ov
				ovPtr = &ovCopy
			}
			if target := s.resolveEffectiveTarget(ref.CalledProcessId, ovPtr); target != nil {
				call.TargetKey = target.Key
			}
			proc.Calls = append(proc.Calls, call)
		}
		land.Processes = append(land.Processes, proc)
	}
	return land, nil
}
