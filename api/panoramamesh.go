package api

import (
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/panorama"
	"github.com/pblumer/atlas/compiler"
)

// meshMaxNodes is the landscape mesh's size budget (ADR-0211 §7). Over it the
// graph collapses to applications and says so, rather than handing the browser a
// graph it cannot lay out.
//
// The number is measured, not guessed. The browser-side layout is an all-pairs
// force simulation, so its cost is quadratic in node count; timed in Chromium
// (e2e/panorama-mesh.spec.mjs, "stays inside its size budget"):
//
//	200 nodes / 370 edges   148 ms layout,  486 ms to first paint
//	400 nodes / 740 edges   438 ms layout, 1036 ms to first paint
//	800 nodes / 1480 edges 1385 ms layout, 2203 ms to first paint
//
// 400 is the last size that still paints in about a second on a warm machine.
// Raising it is a decision about the layout algorithm — Barnes-Hut, or the
// server-side pipeline in api/layout (ADR-0124/0127) — not about this constant.
const meshMaxNodes = 400

// collectLandscape reads this server's resources and returns them filtered for the
// caller, as the input the derived mesh is computed from (ADR-0211 §1).
//
// Run-loop goroutine only: it reads the project store, the call-override store, the
// deployment registry, the unresolved incidents and the worker registry — the last
// two being the observations severity is computed from (ADR-0211 §4).
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

	// Configured workers still live in the connector store, whose record still spells
	// the Worker Type Kind — names ADR-0203 leaves in place until the packages move.
	// What the mesh emits is Worker vocabulary.
	confWorkers, err := s.connectors.LoadAll()
	if err != nil {
		return panorama.Landscape{}, err
	}
	principal := httpapi.PrincipalFrom(r.Context())
	held, polled := s.workerHoldings()
	workersByName := make(map[string]connector, len(confWorkers))
	land := panorama.Landscape{}
	for _, w := range confWorkers {
		workersByName[w.Name] = w
		state, reason := s.workerStatus(w.Kind, w.Name, polled, held)
		land.Workers = append(land.Workers, panorama.Worker{
			ID: w.ID, Name: w.Name, Type: w.Kind,
			CanView: scopeRank(connectorRole(w, principal, s.authEnabled)) >= scopeRank(ScopeRoleViewer),
			// Carried so the derivation can be tested for never emitting them.
			Endpoint: w.Endpoint, CredentialsRef: w.CredentialsRef,
			State: state, Reason: reason,
		})
	}

	// Parked work is counted once for the whole landscape rather than per process:
	// the alternative is one scan per definition, which on an instance with a few
	// hundred processes is the difference between a view and an outage.
	parked, partial, err := s.incidentsByDefinition()
	if err != nil {
		return panorama.Landscape{}, err
	}
	land.PartialStatus = partial

	// Deployed decisions are engine-wide rather than owned by any application
	// (ADR-0034), so there is no scope to apply and CanView is simply true. Saying
	// that here is better than leaving a reader to wonder which filter was forgotten.
	for _, d := range s.dmnRegistry.DeployedDecisions() {
		land.Decisions = append(land.Decisions, panorama.Decision{
			ID: d.ID, Name: d.Name, CanView: true,
		})
	}

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
		state, reason := processStatus(parked[d.Key])
		proc := panorama.Process{
			Key: d.Key, ProcessID: d.ProcessID, Name: d.Name, Version: d.Version,
			ApplicationID: d.ProjectID,
			CanView:       s.canViewArtifact(r, d.ProjectID, d.DeployedBy, projs),
			State:         state, Reason: reason, Incidents: parked[d.Key],
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
		proc.Workers = workerUses(d.cp, workersByName)
		proc.Decisions = d.cp.BusinessRuleDecisions()
		land.Processes = append(land.Processes, proc)
	}
	return land, nil
}

// workerUses resolves a process's worker references — the names its tasks state in
// connector="…" — against the configured workers, mirroring the deploy-time check in
// connectorWarnings, including the two references that are deliberately *not*
// findings there, because treating them as findings here would put false "not
// configured" nodes on the landscape:
//
//   - a reference whose job type no managed Worker Type claims is not a worker
//     reference at all (a local decision names its connector field the same way); and
//   - a name authored as a FEEL expression (entra, ADR-0172) names no fixed worker —
//     which one it reaches is known only at call time, so there is nothing on this
//     server to resolve it against.
//
// Run-loop goroutine only, via its caller.
func workerUses(cp *compiler.CompiledProcess, byName map[string]connector) []panorama.WorkerUse {
	var out []panorama.WorkerUse
	for _, ref := range cp.ConnectorRefs() {
		if connectorKindOfJobType(ref.JobType) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(ref.Connector), "=") {
			continue
		}
		use := panorama.WorkerUse{ElementID: ref.ElementId, Name: ref.Connector}
		if rec, ok := byName[ref.Connector]; ok {
			use.TargetID = rec.ID
		}
		out = append(out, use)
	}
	return out
}
