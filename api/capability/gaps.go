package capability

import (
	"fmt"
	"sort"
	"strings"
)

// The gap report: the reverse of the realization edge, computed rather than stored.
//
// The map says what the organisation must be able to do. The installation says what
// it actually runs. This compares the two and reports every place they disagree, and
// it is the reason the registry is worth maintaining at all: without it a capability
// map is a document that ages, and with it the adoption journey is a list that
// shrinks.
//
// Two rules hold the whole file together.
//
// It is a comparison and never a merge. The derived call graph can raise a finding
// about a declared dependency; it can never rewrite one. The method's black box is
// normally a service task or a message, so the graph sees a minority of the real
// dependencies, and letting it edit Requires would let the incomplete half overwrite
// the complete one.
//
// It never reports the limits of Atlas's eyesight as somebody's defect. A capability
// realised by a purchased system or by a person is not checked, because there is
// nothing here to check it against; a realization pointing at something the caller
// may not see is reported as restricted rather than missing, and the report says how
// many such things it met.

// Finding kinds. The shape is <subject>.<problem>, so a UI can group them without a
// second table.
const (
	// FindingUnrealized: a capability nothing is doing. The most useful row in the
	// report — the work still done by hand, or by a system nobody wrote down.
	FindingUnrealized = "capability.unrealized"
	// FindingRealizationMissing: a realization pointing at a process or Worker that is
	// not here. Either the map is ahead of the installation, or something was deleted
	// underneath it.
	FindingRealizationMissing = "realization.missing"
	// FindingProcessUnclaimed: a deployed process no capability claims. The other
	// direction, and the one that catches a map quietly going stale as delivery moves.
	FindingProcessUnclaimed = "process.unclaimed"
	// FindingRequiresUnknown: a dependency naming a capability that does not exist.
	FindingRequiresUnknown = "requires.unknown"
	// FindingStageEmpty: a value-stream stage no capability performs.
	FindingStageEmpty = "stage.empty"
	// FindingStageUnknown: a stage naming a capability that does not exist.
	FindingStageUnknown = "stage.unknown"
	// FindingCallUndeclared: one capability's process calls another capability's
	// process, and the caller never declared the dependency.
	FindingCallUndeclared = "call.undeclared"
	// FindingProcessShared: two capabilities claim the same deployed process. The map
	// exists to make ownership clear, and one implementation with two owners is the
	// opposite — so it is reported rather than resolved by whichever record happened to
	// be read last.
	FindingProcessShared = "process.shared"
)

// FindingKinds is every kind, in report order. Served in the authoring subset so a
// client can render an empty state per kind rather than inferring the set from
// whatever a particular report happened to contain.
func FindingKinds() []string {
	return []string{
		FindingUnrealized,
		FindingRealizationMissing,
		FindingProcessUnclaimed,
		FindingRequiresUnknown,
		FindingStageEmpty,
		FindingStageUnknown,
		FindingProcessShared,
		FindingCallUndeclared,
	}
}

// Finding is one disagreement between the map and the installation. The fields are
// the coordinates of the thing it is about; a finding fills the ones that apply and
// leaves the rest empty.
//
// There is no severity. A severity scale would be this package deciding how much
// somebody else's architecture gap matters, which it cannot know: an unrealized
// capability is a triumph of honesty in one organisation and an emergency in
// another. The kind says what it is; what it is worth is the reader's.
type Finding struct {
	Kind           string `json:"kind"`
	CapabilityKey  string `json:"capabilityKey,omitempty"`
	RequiredKey    string `json:"requiredKey,omitempty"`
	ValueStreamKey string `json:"valueStreamKey,omitempty"`
	StageKey       string `json:"stageKey,omitempty"`
	ApplicationKey string `json:"applicationKey,omitempty"`
	ProcessID      string `json:"processId,omitempty"`
	ElementID      string `json:"elementId,omitempty"`
	// Detail says what to do about it in one sentence. A finding that only names a
	// coordinate makes the reader guess.
	Detail string `json:"detail"`
}

// GapReport is the whole comparison.
type GapReport struct {
	// Counts carries every kind, including the zeroes. A report that listed only the
	// kinds it happened to find would leave a UI unable to say "nothing wrong here".
	Counts   map[string]int `json:"counts"`
	Findings []Finding      `json:"findings"`
	// Restricted is how many things in the landscape the caller's access hid from this
	// comparison — counted once each, however many ways the comparison met them.
	// Published rather than swallowed: a report that quietly checks less than it
	// claims is worse than one that admits its blind spot.
	Restricted int `json:"restricted"`
	// Checked says what the comparison ran over, so a suspiciously empty report can be
	// told apart from an empty installation.
	Checked Checked `json:"checked"`
}

// Checked is the size of what the report looked at.
type Checked struct {
	Capabilities int `json:"capabilities"`
	ValueStreams int `json:"valueStreams"`
	Processes    int `json:"processes"`
	Calls        int `json:"calls"`
}

// Gaps compares a capability map against the landscape.
//
// Pure: it reads its three arguments and touches nothing else, which is what lets it
// be tested against a landscape written by hand and what keeps it off the run loop.
func Gaps(caps []Capability, streams []ValueStream, land Landscape) GapReport {
	rep := GapReport{
		Counts: make(map[string]int, len(FindingKinds())),
		Checked: Checked{
			Capabilities: len(caps), ValueStreams: len(streams),
			Processes: len(land.Processes), Calls: len(land.Calls),
		},
	}
	for _, kind := range FindingKinds() {
		rep.Counts[kind] = 0
	}
	// Counted once, here, from the landscape itself. The comparison below meets a
	// hidden process twice — as a realization it cannot verify and as a process it
	// cannot claim — and incrementing at each sighting would report one inaccessible
	// application as two blind spots.
	for _, p := range land.Processes {
		if !p.CanView {
			rep.Restricted++
		}
	}
	for _, w := range land.Workers {
		if !w.CanView {
			rep.Restricted++
		}
	}

	byKey := make(map[string]Capability, len(caps))
	for _, c := range caps {
		byKey[c.Key] = c
	}
	processes := land.processIndex()
	workers := land.workerIndex()

	// claimedBy maps a deployed process id to the capability that claims it, and
	// claimants keeps every claimant so a contested process can be reported rather than
	// silently resolved to whichever record was read last.
	//
	// Keyed by process id rather than by (application, process) because a call activity
	// names only the called process id — the callee's application is not in the model —
	// and because a process id identifies a definition uniquely on one server anyway.
	claimedBy := map[string]string{}
	claimants := map[string][]string{}
	for _, c := range caps {
		for _, r := range c.Realizations {
			if r.Kind != RealizationProcess {
				continue
			}
			// Lowest key wins, not first read. A contested process is reported as a
			// finding of its own; until somebody resolves it the comparison still has to
			// attribute the calls out of that process to *some* capability, and doing it
			// by argument order would make two identical reports disagree.
			if seen, ok := claimedBy[r.ProcessID]; !ok || c.Key < seen {
				claimedBy[r.ProcessID] = c.Key
			}
			claimants[r.ProcessID] = append(claimants[r.ProcessID], c.Key)
		}
	}

	var findings []Finding
	add := func(f Finding) { findings = append(findings, f) }

	for _, c := range caps {
		for _, r := range c.Realizations {
			switch r.Kind {
			case RealizationProcess:
				// A realization the caller may not see is present in the landscape as a
				// placeholder, so it resolves and raises nothing: restricted is not
				// missing, and telling somebody their architecture is broken when they
				// merely lack access to one application is the worst available answer.
				if _, ok := processes[realizationRef{app: r.ApplicationKey, process: r.ProcessID}]; !ok {
					add(Finding{Kind: FindingRealizationMissing, CapabilityKey: c.Key,
						ApplicationKey: r.ApplicationKey, ProcessID: r.ProcessID,
						Detail: fmt.Sprintf("no process %q is deployed in application %q on this server; "+
							"either it has not been deployed here yet, or the realization names the wrong one",
							r.ProcessID, r.ApplicationKey)})
				}
			case RealizationWorker:
				if _, ok := workers[r.WorkerRef]; !ok {
					add(Finding{Kind: FindingRealizationMissing, CapabilityKey: c.Key,
						Detail: fmt.Sprintf("no Worker %q is configured on this server", r.WorkerRef)})
				}
			}
			// A system or manual realization is not checked at all. Atlas cannot see a
			// purchased SaaS or a clerk, and reporting them would report its own
			// eyesight as somebody's architecture defect.
		}

		// Unrealized is about having no realization at all, not about a broken one:
		// a capability whose single process realization is missing has a realization
		// and a problem with it, and saying both would count one trouble twice.
		if len(c.Realizations) == 0 && c.State != StateDeprecated {
			add(Finding{Kind: FindingUnrealized, CapabilityKey: c.Key,
				Detail: "nothing here realizes this capability: it is done manually, done by a system " +
					"nobody has recorded, or not done at all. Add a realization saying which."})
		}

		for _, key := range c.Requires {
			if _, ok := byKey[key]; !ok {
				add(Finding{Kind: FindingRequiresUnknown, CapabilityKey: c.Key, RequiredKey: key,
					Detail: fmt.Sprintf("this capability requires %q, which is not in the map", key)})
			}
		}
	}

	// The other direction: what is running that nobody claims.
	claimedRefs := map[realizationRef]bool{}
	for _, c := range caps {
		for _, r := range c.Realizations {
			if r.Kind == RealizationProcess {
				claimedRefs[realizationRef{app: r.ApplicationKey, process: r.ProcessID}] = true
			}
		}
	}
	for ref, p := range processes {
		if !p.CanView {
			continue // already counted as a blind spot; it is not evidence of anything
		}
		if claimedRefs[ref] {
			continue
		}
		add(Finding{Kind: FindingProcessUnclaimed, ApplicationKey: p.ApplicationKey, ProcessID: p.ProcessID,
			Detail: "this process is deployed but no capability claims it: either it realizes one that " +
				"is not recorded, or it is a supporting process that belongs inside one"})
	}

	for _, v := range streams {
		for _, st := range v.Stages {
			if len(st.Capabilities) == 0 {
				add(Finding{Kind: FindingStageEmpty, ValueStreamKey: v.Key, StageKey: st.Key,
					Detail: "no capability performs this stage, so the value stream has a step nobody owns"})
				continue
			}
			for _, key := range st.Capabilities {
				if _, ok := byKey[key]; !ok {
					add(Finding{Kind: FindingStageUnknown, ValueStreamKey: v.Key, StageKey: st.Key,
						RequiredKey: key,
						Detail:      fmt.Sprintf("this stage names capability %q, which is not in the map", key)})
				}
			}
		}
	}

	// One implementation, two owners. Reported once for the process rather than once per
	// claimant, because it is one ambiguity and the answer is a decision about which
	// capability owns it.
	sharedProcesses := make([]string, 0)
	for pid, owners := range claimants {
		if len(owners) > 1 {
			sharedProcesses = append(sharedProcesses, pid)
		}
	}
	sort.Strings(sharedProcesses)
	for _, pid := range sharedProcesses {
		owners := append([]string(nil), claimants[pid]...)
		sort.Strings(owners)
		add(Finding{Kind: FindingProcessShared, CapabilityKey: owners[0], ProcessID: pid,
			Detail: fmt.Sprintf("process %q is claimed by %s — one implementation with two owners is "+
				"the ambiguity the map exists to remove; decide which capability owns it, and let the "+
				"other require it", pid, strings.Join(owners, " and "))})
	}

	// The comparison worth the most: a call activity crossing a capability boundary
	// the caller never declared.
	for _, call := range land.Calls {
		if !call.Resolved {
			continue // an unresolved call is the call inventory's finding, not this one's
		}
		callerKey, ok := claimedBy[call.CallerProcessID]
		if !ok {
			continue // the caller belongs to no capability; process.unclaimed already says so
		}
		calleeKey, ok := claimedBy[call.CalledProcessID]
		if !ok || calleeKey == callerKey {
			continue // unclaimed callee, or a call inside one capability's own black box
		}
		caller := byKey[callerKey]
		declared := false
		for _, key := range caller.Requires {
			if key == calleeKey {
				declared = true
				break
			}
		}
		if declared {
			continue
		}
		add(Finding{Kind: FindingCallUndeclared, CapabilityKey: callerKey, RequiredKey: calleeKey,
			ProcessID: call.CalledProcessID, ElementID: call.ElementID,
			Detail: fmt.Sprintf("%q calls %q's process from element %q, but does not declare it as a "+
				"required capability. Declare it, or make the call go through the capability's interface",
				callerKey, calleeKey, call.ElementID)})
	}

	sortFindings(findings)
	rep.Findings = findings
	for _, f := range findings {
		rep.Counts[f.Kind]++
	}
	return rep
}

// sortFindings gives the report a stable order: by the thing it is about, then by
// kind, then by the rest. A report whose order changes between two identical reads
// cannot be diffed, and diffing it against yesterday's is how anybody notices the map
// drifting.
func sortFindings(f []Finding) {
	sort.SliceStable(f, func(i, j int) bool {
		a, b := f[i], f[j]
		if a.subject() != b.subject() {
			return a.subject() < b.subject()
		}
		if a.Kind != b.Kind {
			// By the declared order of FindingKinds, not alphabetically: "capability has
			// no realization" is the row somebody acts on and "call.undeclared" is a
			// detail, and sorting by the spelling of the constants would put the detail
			// first for no reason a reader could infer.
			return kindRank(a.Kind) < kindRank(b.Kind)
		}
		if a.RequiredKey != b.RequiredKey {
			return a.RequiredKey < b.RequiredKey
		}
		if a.StageKey != b.StageKey {
			return a.StageKey < b.StageKey
		}
		if a.ProcessID != b.ProcessID {
			return a.ProcessID < b.ProcessID
		}
		return a.ElementID < b.ElementID
	})
}

// kindRank is a finding kind's position in [FindingKinds]. An unknown kind sorts last
// rather than first, so a kind added without a place in that list is visible at the
// bottom instead of silently displacing the ones that matter.
func kindRank(kind string) int {
	for i, k := range FindingKinds() {
		if k == kind {
			return i
		}
	}
	return len(FindingKinds())
}

// subject is what a finding is about, for ordering: the capability where there is
// one, the value stream next, and the process for the findings that have neither.
func (f Finding) subject() string {
	switch {
	case f.CapabilityKey != "":
		return f.CapabilityKey
	case f.ValueStreamKey != "":
		return f.ValueStreamKey
	default:
		return f.ApplicationKey + "/" + f.ProcessID
	}
}
