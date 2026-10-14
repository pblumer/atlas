package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/capability"
	"github.com/pblumer/atlas/api/httpapi"
)

// collectCapabilityLandscape is what the business-architecture area is told about this
// installation: which processes are deployed and under which application key, which
// Workers are configured, and which call activities resolve where.
//
// Run-loop goroutine only. It reads the application store, the deployment registry,
// the worker store, the call-override store and the ADR-0080 per-definition counters —
// all of which only the single writer may touch (I3).
//
// Everything it returns is a value, and none of it is written down. A capability record
// holds a portable key and nothing else; the version, the instance count and whether a
// process still exists are resolved here, each time somebody reads. A record that
// carried them would be a second, stale copy of the deployment registry within a week,
// which is the rule ADR-0189 §4 set for Panorama's bindings.
//
// Three things it deliberately does not do:
//
//   - It invents no visibility rule. A deployment defers to its application's sharing
//     scope, falling back to its deployer — canViewArtifact, the rule every other
//     filtered read of a deployment already applies (ADR-0071).
//   - It drops nothing the caller may not see. A process outside the caller's scope
//     travels as a placeholder with CanView false, so the comparison can report it as
//     restricted rather than as missing. Telling somebody their architecture is broken
//     when they merely lack access to one application is the worst answer available.
//   - It reads no instance. The running count is the maintained per-definition counter
//     (ADR-0080), so this whole read is design-time size however many instances exist —
//     which is what lets it run on the loop at all (ADR-0239).
func (s *Server) collectCapabilityLandscape(r *http.Request) (capability.Landscape, error) {
	projs, err := s.projectsByID()
	if err != nil {
		return capability.Landscape{}, err
	}
	// The portable application key is what a realization names, so it has to be
	// resolved rather than assumed: an application created before ADR-0134 carries none
	// until something needs one, and applicationKeyFor derives and *saves* it.
	//
	// Only the applications something is actually deployed under. That backfill is
	// meant to happen when something needs the key, and here what needs it is a
	// deployment a realization could name — deriving one for an application with nothing
	// deployed would rewrite a record this read has no use for, and would pay
	// deriveApplicationKey's store scan for it.
	inUse := map[string]bool{}
	for pid := range s.versions {
		if d := s.latestDeploymentByProcessID(pid); d != nil && d.cp != nil {
			inUse[d.ProjectID] = true
		}
	}
	keyByProject := make(map[string]string, len(inUse))
	nameByProject := make(map[string]string, len(inUse))
	for id := range inUse {
		p, ok := projs[id]
		if !ok {
			// An ungrouped deployment (empty id), or an application deleted out from
			// under one. Either way there is no key, and the process still belongs on
			// the landscape — with an empty application key, which is a realization
			// nothing can name and which the gap report reports as unclaimed.
			continue
		}
		key, err := s.applicationKeyFor(p)
		if err != nil {
			return capability.Landscape{}, err
		}
		keyByProject[id] = key
		nameByProject[id] = p.Name
	}

	overrides, err := s.callOverrides.LoadAll()
	if err != nil {
		return capability.Landscape{}, err
	}
	ovByPID := make(map[string]callOverride, len(overrides))
	for _, rec := range overrides {
		ovByPID[rec.CalledProcessID] = rec
	}

	land := capability.Landscape{}

	// One entry per process id at its current version. A realization names a process,
	// not a version, so enumerating the deploy history would say nothing extra and
	// would grow with it.
	for pid := range s.versions {
		d := s.latestDeploymentByProcessID(pid)
		if d == nil || d.cp == nil {
			continue
		}
		proc := capability.Process{
			ApplicationKey:  keyByProject[d.ProjectID],
			ApplicationName: nameByProject[d.ProjectID],
			ProcessID:       d.ProcessID,
			Name:            d.Name,
			Version:         d.Version,
			Inactive:        d.inactive,
			CanView:         s.canViewArtifact(r, d.ProjectID, d.DeployedBy, projs),
		}
		if n, err := s.store.DefInstanceCount(d.Key); err == nil {
			proc.ActiveInstances = n
		}
		land.Processes = append(land.Processes, proc)

		for _, ref := range d.cp.CallActivities() {
			call := capability.Call{
				CallerProcessID: d.ProcessID,
				ElementID:       ref.ElementId,
				CalledProcessID: ref.CalledProcessId,
			}
			// Resolution mirrors the call-activity management view exactly, overrides
			// included: an edge that ignored a redirect or a pin would report a
			// dependency the engine would never take (ADR-0076/0105).
			var ovPtr *callOverride
			if ov, ok := ovByPID[ref.CalledProcessId]; ok {
				ovCopy := ov
				ovPtr = &ovCopy
			}
			if target := s.resolveEffectiveTarget(ref.CalledProcessId, ovPtr); target != nil {
				call.Resolved = true
				// The *effective* target's process id, not the one the model names: a
				// redirect makes those differ, and the dependency that exists is the one
				// the engine would take.
				call.CalledProcessID = target.ProcessID
			}
			land.Calls = append(land.Calls, call)
		}
	}

	// Configured Workers. The store record still spells the Worker Type "Kind" — one
	// of the names ADR-0203 leaves in place until the packages move — and what this
	// emits is Worker vocabulary.
	confWorkers, err := s.connectors.LoadAll()
	if err != nil {
		return capability.Landscape{}, err
	}
	principal := httpapi.PrincipalFrom(r.Context())
	for _, w := range confWorkers {
		land.Workers = append(land.Workers, capability.Worker{
			Ref: w.Name, Name: w.Name, Type: w.Kind,
			CanView: scopeRank(connectorRole(w, principal, s.authEnabled)) >= scopeRank(ScopeRoleViewer),
		})
	}
	return land, nil
}

// confirmationHorizon reads how many months a business-architecture confirmation stays
// fresh for. Run-loop goroutine only: it reads the settings store.
//
// An installation that has said nothing gets the default, which is the twelve months
// ADR-0289 and ADR-0293 already use for the two other things this repository dates and
// cannot verify.
func (s *Server) confirmationHorizon() (int, error) {
	setting, stored, err := s.settings.getConfirmation()
	if err != nil {
		return 0, err
	}
	if !stored || setting.HorizonMonths == 0 {
		return capability.DefaultHorizonMonths, nil
	}
	return setting.HorizonMonths, nil
}
