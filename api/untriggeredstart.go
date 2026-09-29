package api

import (
	"strings"

	"github.com/pblumer/atlas/compiler"
)

// Starting by hand a process that only its triggers can start (ADR-0426).
//
// A create nobody triggered seeds the process's none start events, and where it has
// none, every start event it has — which is right for a process with exactly one,
// the message-only process a modeller tests by pressing Start (ADR-0035). A process
// with several start events and no none start is a different shape: each start is
// a trigger, and a create by hand fired none of them. Seeding all of them runs
// every branch at once, and for a product's lifecycle process that is provisioning
// and deprovisioning in the same instant (ADR-0425). Nothing about the instance
// would look wrong; the damage would be in the target systems.
//
// So every door that creates an instance by hand asks this first and refuses. The
// engine's own door — a call activity — raises an incident for the same reason,
// because it has nobody to answer to.

// untriggeredStartRefusal is the reason an untriggered create of cp must be
// refused, or "" when it may go ahead. It names the start events, because what the
// person who pressed Start needs to learn is which triggers the process has.
func untriggeredStartRefusal(cp *compiler.CompiledProcess) string {
	if cp == nil || !cp.UntriggeredStartAmbiguous() {
		return ""
	}
	starts := cp.StartEvents()
	names := make([]string, 0, len(starts))
	for _, id := range starts {
		names = append(names, cp.ElementBpmnId(id))
	}
	return "process " + cp.ProcessId() + " has no none start event and " +
		"several start events (" + strings.Join(names, ", ") + "); each is a trigger, " +
		"and starting it by hand fires none of them — it would run every branch at " +
		"once. Start it through one of its triggers instead (ADR-0426)"
}

// ruleCallUntriggeredStart is the Problems-panel rule for a call activity whose
// target only its triggers can start. A warning, not an error: the target is
// resolved per server and per version (ADR-0076, ADR-0105), so what the modeller
// sees here is how it resolves on this server now, and the incident at runtime
// stays the authority (ADR-0426).
const ruleCallUntriggeredStart = "call.untriggered-start"

// ambiguousCallTargetsOnLoop finds the call activities in cp whose target, as it
// resolves right now, has no none start and several start events. A target defined
// beside the caller — another pool in the same model — is read from there, because
// that is the version the model will deploy with; otherwise the newest deployed
// version of the id answers. Reads the deployment registry, so it runs on the run
// loop (invariant I3).
func (s *Server) ambiguousCallTargetsOnLoop(cp *compiler.CompiledProcess, siblings []*compiler.CompiledProcess) []compiler.Problem {
	var out []compiler.Problem
	for id := int32(0); int(id) < cp.NodeCount(); id++ {
		node := cp.Node(id)
		if node.Type != compiler.TypeCallActivity {
			continue
		}
		called := cp.Intern(cp.CallActivity(node.Detail).CalledProcessId)
		var target *compiler.CompiledProcess
		for _, sib := range siblings {
			if sib != nil && sib.ProcessId() == called {
				target = sib
				break
			}
		}
		if target == nil {
			if d := s.latestDeploymentOf(called); d != nil {
				target = d.cp
			}
		}
		if reason := untriggeredStartRefusal(target); reason != "" {
			out = append(out, compiler.Problem{
				Element:  cp.ElementBpmnId(id),
				Severity: compiler.SeverityWarning,
				Rule:     ruleCallUntriggeredStart,
				Message: "this call activity will park on an incident instead of calling: " +
					reason,
			})
		}
	}
	return out
}
