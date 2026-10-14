package capability

// What this area needs to know about the running installation, and nothing more.
//
// The service owns the map; it owns none of the landscape. Everything below is
// resolved by the server on the run loop and handed over as values, which is what
// keeps this package free of the deployment registry, the project store and the
// worker registry — and what makes coverage and the gap report testable against a
// landscape written by hand.
//
// Nothing here is stored. A realization holds a portable key and nothing else, and
// every mutable fact about it — does the application exist, is the process deployed,
// at which version, how many instances are running — is resolved when the record is
// read. A record that stored those would be a second, stale copy of the deployment
// registry within a week, which is the discipline ADR-0189 §4 set for Panorama's
// bindings and the reason it set it.

// Process is one deployed process as the registry sees it.
type Process struct {
	// ApplicationKey is the portable application key (ADR-0134), empty for a process
	// deployed outside any application. A realization addresses this, never the local
	// application id, so the reference survives the map being moved.
	ApplicationKey  string `json:"applicationKey,omitempty"`
	ApplicationName string `json:"applicationName,omitempty"`
	ProcessID       string `json:"processId"`
	Name            string `json:"name,omitempty"`
	Version         int32  `json:"version"`
	// Inactive marks a deployment an operator has switched off (ADR-0119). It still
	// exists, so it is not a missing realization — a different finding entirely.
	Inactive bool `json:"inactive"`
	// ActiveInstances is the O(1) maintained per-definition counter (ADR-0080), never
	// a scan: this read must not grow with the instance population.
	ActiveInstances int `json:"activeInstances"`
	// CanView is whether the requesting principal may see this process at all. A
	// realization pointing at one they may not see is reported as restricted rather
	// than as missing — the difference between "you cannot see this" and "this is not
	// there" is the whole value of the answer.
	CanView bool `json:"-"`
}

// Worker is one configured Worker: a target and identity of a Worker Type
// (ADR-0203).
type Worker struct {
	Ref     string `json:"ref"`
	Name    string `json:"name,omitempty"`
	Type    string `json:"type,omitempty"`
	CanView bool   `json:"-"`
}

// Call is one call activity between deployed processes, with the target it resolves
// to on this server.
//
// It is the input to the one finding worth the most: a call crossing from one
// capability's process into another's that the caller never declared. It is compared
// against Requires and never merged into it — a declared dependency with no call is
// the normal case, since the method's black box is usually a REST call rather than a
// call activity.
type Call struct {
	CallerProcessID string `json:"callerProcessId"`
	ElementID       string `json:"elementId"`
	CalledProcessID string `json:"calledProcessId"`
	// Resolved says the call would start a child on this server rather than park.
	// An unresolved call is somebody else's finding (the call-activity inventory
	// already reports it), so this area only reads the resolved ones.
	Resolved bool `json:"resolved"`
}

// Landscape is the whole of what this area is told about the installation. Built on
// the run loop, filtered for the requesting principal, and read off it.
type Landscape struct {
	Processes []Process
	Workers   []Worker
	Calls     []Call
}

// processIndex is the landscape's processes by (applicationKey, processId), which is
// how a realization addresses one.
func (l Landscape) processIndex() map[realizationRef]Process {
	out := make(map[realizationRef]Process, len(l.Processes))
	for _, p := range l.Processes {
		ref := realizationRef{app: p.ApplicationKey, process: p.ProcessID}
		// Several versions of one process are one entry: a realization names the
		// process, not a version, so the newest wins and the version travels with it.
		if prev, ok := out[ref]; ok && prev.Version >= p.Version {
			continue
		}
		out[ref] = p
	}
	return out
}

// realizationRef is the pair a process realization names.
type realizationRef struct{ app, process string }

func (l Landscape) workerIndex() map[string]Worker {
	out := make(map[string]Worker, len(l.Workers))
	for _, w := range l.Workers {
		out[w.Ref] = w
	}
	return out
}
