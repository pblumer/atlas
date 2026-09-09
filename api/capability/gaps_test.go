package capability

import (
	"sort"
	"strings"
	"testing"
)

// kindsOf reduces a report to the finding kinds it raised, for tests that care about
// what was found rather than how it was worded.
func kindsOf(rep GapReport) []string {
	var out []string
	for _, f := range rep.Findings {
		out = append(out, f.Kind)
	}
	sort.Strings(out)
	return out
}

func has(rep GapReport, kind string) bool {
	for _, f := range rep.Findings {
		if f.Kind == kind {
			return true
		}
	}
	return false
}

func findingsOf(rep GapReport, kind string) []Finding {
	var out []Finding
	for _, f := range rep.Findings {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

func TestGapsOnACompleteMapFindsNothing(t *testing.T) {
	caps := []Capability{
		{Key: "onboarding", Name: "Customer Onboarding", State: StateActive,
			Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "crm", ProcessID: "onboarding"}},
			Requires:     []string{"identity"}},
		{Key: "identity", Name: "Identity Verification", State: StateActive,
			Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "crm", ProcessID: "identity"}}},
	}
	streams := []ValueStream{{Key: "consumer-loan", Name: "Consumer Loan", Stages: []Stage{
		{Key: "apply", Name: "Apply", Capabilities: []string{"onboarding"}},
	}}}
	land := Landscape{
		Processes: []Process{
			{ApplicationKey: "crm", ProcessID: "onboarding", Name: "Onboarding", Version: 1, CanView: true},
			{ApplicationKey: "crm", ProcessID: "identity", Name: "Identity", Version: 3, CanView: true},
		},
		Calls: []Call{{CallerProcessID: "onboarding", ElementID: "call-1", CalledProcessID: "identity", Resolved: true}},
	}
	rep := Gaps(caps, streams, land)
	if len(rep.Findings) != 0 {
		t.Fatalf("a complete map reported %d findings: %+v", len(rep.Findings), rep.Findings)
	}
	if rep.Counts[FindingUnrealized] != 0 {
		t.Errorf("counts should carry a zero for every kind, got %v", rep.Counts)
	}
	if _, ok := rep.Counts[FindingUnrealized]; !ok {
		t.Error("a report with no findings still has to say so per kind, or a UI cannot render an empty state")
	}
}

func TestGapsReportsAnUnrealizedCapability(t *testing.T) {
	// The most useful finding in the whole report: the work still done by hand, or by
	// a system nobody has written down.
	caps := []Capability{{Key: "underwriting", Name: "Loan Underwriting", State: StateActive}}
	rep := Gaps(caps, nil, Landscape{})
	fs := findingsOf(rep, FindingUnrealized)
	if len(fs) != 1 {
		t.Fatalf("findings = %+v, want exactly one unrealized capability", rep.Findings)
	}
	if fs[0].CapabilityKey != "underwriting" {
		t.Errorf("finding names %q", fs[0].CapabilityKey)
	}
	if fs[0].Detail == "" {
		t.Error("a finding with no detail makes the reader guess what to do about it")
	}
}

func TestGapsDoesNotReportADeprecatedCapabilityAsUnrealized(t *testing.T) {
	// A capability on its way out is *supposed* to have nothing doing it. Reporting it
	// would fill the backlog with rows nobody intends to act on, which is how a report
	// stops being read.
	caps := []Capability{{Key: "fax-intake", Name: "Fax Intake", State: StateDeprecated}}
	rep := Gaps(caps, nil, Landscape{})
	if has(rep, FindingUnrealized) {
		t.Errorf("a deprecated capability was reported as unrealized: %+v", rep.Findings)
	}
}

func TestGapsReportsAMissingRealization(t *testing.T) {
	caps := []Capability{{Key: "billing", Name: "Billing", State: StateActive,
		Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "fin", ProcessID: "invoice"}}}}
	rep := Gaps(caps, nil, Landscape{})
	fs := findingsOf(rep, FindingRealizationMissing)
	if len(fs) != 1 {
		t.Fatalf("findings = %+v, want one missing realization", rep.Findings)
	}
	if fs[0].ProcessID != "invoice" || fs[0].ApplicationKey != "fin" {
		t.Errorf("finding = %+v, want it to name the application and process it could not find", fs[0])
	}
	// A capability whose only realization is missing is not *also* unrealized: it has
	// one, it is broken, and saying both would double-count the same problem.
	if has(rep, FindingUnrealized) {
		t.Error("a broken realization was also counted as no realization")
	}
}

func TestGapsReportsAMissingWorkerRealization(t *testing.T) {
	caps := []Capability{{Key: "notify", Name: "Notify", State: StateActive,
		Realizations: []Realization{{Kind: RealizationWorker, WorkerRef: "mail-service-desk"}}}}
	rep := Gaps(caps, nil, Landscape{Workers: []Worker{{Ref: "mail-other", CanView: true}}})
	if !has(rep, FindingRealizationMissing) {
		t.Fatalf("findings = %+v, want the unknown worker reported", rep.Findings)
	}
}

func TestGapsNeverChecksASystemOrManualRealization(t *testing.T) {
	// Atlas has no way to know whether a purchased system or a clerk is doing the
	// work. Reporting them as missing would be reporting the limits of Atlas's
	// eyesight as a defect in somebody's architecture.
	caps := []Capability{{Key: "kyc", Name: "KYC", State: StateActive, Realizations: []Realization{
		{Kind: RealizationSystem, Note: "Acme KYC SaaS"},
		{Kind: RealizationManual, Note: "Branch clerk"},
	}}}
	rep := Gaps(caps, nil, Landscape{})
	if len(rep.Findings) != 0 {
		t.Errorf("findings = %+v, want none: Atlas cannot see a SaaS or a clerk", rep.Findings)
	}
}

func TestGapsReportsARealizationTheCallerMayNotSee(t *testing.T) {
	// Restricted is not missing. Telling a reader their architecture is broken when in
	// fact they merely lack access to one application is the worst possible answer.
	caps := []Capability{{Key: "billing", Name: "Billing", State: StateActive,
		Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "fin", ProcessID: "invoice"}}}}
	land := Landscape{Processes: []Process{
		{ApplicationKey: "fin", ProcessID: "invoice", Version: 1, CanView: false},
	}}
	rep := Gaps(caps, nil, land)
	if has(rep, FindingRealizationMissing) {
		t.Errorf("a realization the caller cannot see was reported as missing: %+v", rep.Findings)
	}
	if rep.Restricted != 1 {
		t.Errorf("Restricted = %d, want 1 — a report that hides what it could not see is worse than none", rep.Restricted)
	}
}

func TestGapsReportsAnUnclaimedProcess(t *testing.T) {
	land := Landscape{Processes: []Process{
		{ApplicationKey: "crm", ProcessID: "onboarding", Name: "Onboarding", Version: 1, CanView: true},
		{ApplicationKey: "crm", ProcessID: "orphan", Name: "Orphan", Version: 1, CanView: true},
	}}
	caps := []Capability{{Key: "onboarding", Name: "Onboarding", State: StateActive,
		Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "crm", ProcessID: "onboarding"}}}}
	rep := Gaps(caps, nil, land)
	fs := findingsOf(rep, FindingProcessUnclaimed)
	if len(fs) != 1 || fs[0].ProcessID != "orphan" {
		t.Fatalf("findings = %+v, want the orphan process reported once", rep.Findings)
	}
}

func TestGapsDoesNotClaimAProcessTheCallerCannotSee(t *testing.T) {
	land := Landscape{Processes: []Process{
		{ApplicationKey: "hr", ProcessID: "secret", Version: 1, CanView: false},
	}}
	rep := Gaps(nil, nil, land)
	if has(rep, FindingProcessUnclaimed) {
		t.Errorf("a process the caller cannot see was reported as unclaimed: %+v", rep.Findings)
	}
}

func TestGapsReportsAnUnknownRequirement(t *testing.T) {
	caps := []Capability{{Key: "onboarding", Name: "Onboarding", State: StateActive,
		Realizations: []Realization{{Kind: RealizationManual, Note: "clerk"}},
		Requires:     []string{"identity"}}}
	rep := Gaps(caps, nil, Landscape{})
	fs := findingsOf(rep, FindingRequiresUnknown)
	if len(fs) != 1 || fs[0].RequiredKey != "identity" {
		t.Fatalf("findings = %+v, want the dangling requirement reported", rep.Findings)
	}
}

func TestGapsReportsAnEmptyAndAnUnknownStage(t *testing.T) {
	streams := []ValueStream{{Key: "loan", Name: "Loan", Stages: []Stage{
		{Key: "empty", Name: "Nobody does this"},
		{Key: "ghost", Name: "Ghost", Capabilities: []string{"missing"}},
	}}}
	rep := Gaps(nil, streams, Landscape{})
	if !has(rep, FindingStageEmpty) || !has(rep, FindingStageUnknown) {
		t.Fatalf("findings = %v, want both an empty stage and an unknown capability", kindsOf(rep))
	}
	for _, f := range rep.Findings {
		if f.ValueStreamKey != "loan" {
			t.Errorf("finding %+v does not name its value stream", f)
		}
	}
}

func TestGapsReportsAnUndeclaredCallAcrossCapabilities(t *testing.T) {
	// The finding worth the most: onboarding's process calls identity's process, and
	// onboarding never declared the dependency. Two things Atlas already holds,
	// compared.
	caps := []Capability{
		{Key: "onboarding", Name: "Onboarding", State: StateActive,
			Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "crm", ProcessID: "onboarding"}}},
		{Key: "identity", Name: "Identity", State: StateActive,
			Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "crm", ProcessID: "identity"}}},
	}
	land := Landscape{
		Processes: []Process{
			{ApplicationKey: "crm", ProcessID: "onboarding", Version: 1, CanView: true},
			{ApplicationKey: "crm", ProcessID: "identity", Version: 1, CanView: true},
		},
		Calls: []Call{{CallerProcessID: "onboarding", ElementID: "call-idv", CalledProcessID: "identity", Resolved: true}},
	}
	rep := Gaps(caps, nil, land)
	fs := findingsOf(rep, FindingCallUndeclared)
	if len(fs) != 1 {
		t.Fatalf("findings = %+v, want the undeclared call reported", rep.Findings)
	}
	if fs[0].CapabilityKey != "onboarding" || fs[0].RequiredKey != "identity" || fs[0].ElementID != "call-idv" {
		t.Errorf("finding = %+v, want caller, callee and the element that makes the call", fs[0])
	}
}

func TestGapsAcceptsADeclaredDependencyWithNoCall(t *testing.T) {
	// The comparison runs one way only. The method's black box is normally a service
	// task or a message, so a declared dependency with no call activity is the
	// ordinary case and must never be a finding — nor may the derived graph rewrite
	// what somebody declared.
	caps := []Capability{
		{Key: "onboarding", Name: "Onboarding", State: StateActive, Requires: []string{"identity"},
			Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "crm", ProcessID: "onboarding"}}},
		{Key: "identity", Name: "Identity", State: StateActive,
			Realizations: []Realization{{Kind: RealizationManual, Note: "clerk"}}},
	}
	land := Landscape{Processes: []Process{
		{ApplicationKey: "crm", ProcessID: "onboarding", Version: 1, CanView: true},
	}}
	rep := Gaps(caps, nil, land)
	if len(rep.Findings) != 0 {
		t.Errorf("findings = %+v, want none", rep.Findings)
	}
}

func TestGapsIgnoresACallWithinOneCapability(t *testing.T) {
	// A capability whose process calls its own supporting process is not a dependency
	// on anything: it is the inside of the black box.
	caps := []Capability{{Key: "onboarding", Name: "Onboarding", State: StateActive,
		Realizations: []Realization{
			{Kind: RealizationProcess, ApplicationKey: "crm", ProcessID: "onboarding"},
			{Kind: RealizationProcess, ApplicationKey: "crm", ProcessID: "onboarding-step"},
		}}}
	land := Landscape{
		Processes: []Process{
			{ApplicationKey: "crm", ProcessID: "onboarding", Version: 1, CanView: true},
			{ApplicationKey: "crm", ProcessID: "onboarding-step", Version: 1, CanView: true},
		},
		Calls: []Call{{CallerProcessID: "onboarding", ElementID: "c", CalledProcessID: "onboarding-step", Resolved: true}},
	}
	rep := Gaps(caps, nil, land)
	if has(rep, FindingCallUndeclared) {
		t.Errorf("a call inside one capability was reported: %+v", rep.Findings)
	}
}

func TestGapsIgnoresAnUnresolvedCall(t *testing.T) {
	caps := []Capability{
		{Key: "a", Name: "A", State: StateActive,
			Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "x", ProcessID: "pa"}}},
		{Key: "b", Name: "B", State: StateActive,
			Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "x", ProcessID: "pb"}}},
	}
	land := Landscape{
		Processes: []Process{
			{ApplicationKey: "x", ProcessID: "pa", Version: 1, CanView: true},
			{ApplicationKey: "x", ProcessID: "pb", Version: 1, CanView: true},
		},
		Calls: []Call{{CallerProcessID: "pa", ElementID: "c", CalledProcessID: "pb", Resolved: false}},
	}
	rep := Gaps(caps, nil, land)
	if has(rep, FindingCallUndeclared) {
		t.Errorf("an unresolved call was reported here rather than by the call inventory: %+v", rep.Findings)
	}
}

func TestGapsCountsEveryKind(t *testing.T) {
	rep := Gaps(
		[]Capability{{Key: "a", Name: "A", State: StateActive, Requires: []string{"nope"}}},
		[]ValueStream{{Key: "s", Name: "S", Stages: []Stage{{Key: "e", Name: "E"}}}},
		Landscape{Processes: []Process{{ApplicationKey: "x", ProcessID: "p", Version: 1, CanView: true}}},
	)
	for _, kind := range FindingKinds() {
		if _, ok := rep.Counts[kind]; !ok {
			t.Errorf("Counts has no entry for %q", kind)
		}
	}
	total := 0
	for _, n := range rep.Counts {
		total += n
	}
	if total != len(rep.Findings) {
		t.Errorf("counts sum to %d but there are %d findings", total, len(rep.Findings))
	}
}

func TestFindingKindsAreStableAndDocumented(t *testing.T) {
	kinds := FindingKinds()
	if len(kinds) == 0 {
		t.Fatal("no finding kinds")
	}
	seen := map[string]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("duplicate finding kind %q", k)
		}
		seen[k] = true
		if !strings.Contains(k, ".") {
			t.Errorf("finding kind %q is not <subject>.<problem>; the shape is what lets a UI group them", k)
		}
	}
}

func TestGapsOrdersFindingsDeterministically(t *testing.T) {
	caps := []Capability{
		{Key: "zeta", Name: "Z", State: StateActive},
		{Key: "alpha", Name: "A", State: StateActive},
	}
	first := Gaps(caps, nil, Landscape{})
	second := Gaps(caps, nil, Landscape{})
	if len(first.Findings) != len(second.Findings) {
		t.Fatal("two identical reports differ in length")
	}
	for i := range first.Findings {
		if first.Findings[i] != second.Findings[i] {
			t.Fatalf("finding %d differs between two identical reports", i)
		}
	}
	if first.Findings[0].CapabilityKey != "alpha" {
		t.Errorf("findings are not ordered by key: %+v", first.Findings)
	}
}

// TestFindingOrderBreaksEveryTie walks the comparator to the end. A report whose order
// changes between two identical reads cannot be diffed against yesterday's, and
// diffing it is how anybody notices the map drifting.
func TestFindingOrderBreaksEveryTie(t *testing.T) {
	in := []Finding{
		{Kind: FindingCallUndeclared, CapabilityKey: "a", RequiredKey: "z", ElementID: "e2"},
		{Kind: FindingCallUndeclared, CapabilityKey: "a", RequiredKey: "z", ElementID: "e1"},
		{Kind: FindingCallUndeclared, CapabilityKey: "a", RequiredKey: "b", ProcessID: "p2"},
		{Kind: FindingCallUndeclared, CapabilityKey: "a", RequiredKey: "b", ProcessID: "p1"},
		{Kind: FindingUnrealized, CapabilityKey: "a"},
		{Kind: FindingStageEmpty, ValueStreamKey: "s", StageKey: "b"},
		{Kind: FindingStageEmpty, ValueStreamKey: "s", StageKey: "a"},
		{Kind: FindingProcessUnclaimed, ApplicationKey: "app", ProcessID: "p"},
	}
	sortFindings(in)
	if kindRank("something.new") != len(FindingKinds()) {
		t.Error("an unlisted finding kind does not sort last, so it would displace the ones that matter")
	}
	want := []string{
		// Ordered by the declared report order, so the row somebody acts on is first.
		"a//" + FindingUnrealized + "//",
		"a//" + FindingCallUndeclared + "/b/p1",
		"a//" + FindingCallUndeclared + "/b/p2",
		"a//" + FindingCallUndeclared + "/z/e1",
		"a//" + FindingCallUndeclared + "/z/e2",
		"app/p//" + FindingProcessUnclaimed + "//p",
		"s//" + FindingStageEmpty + "/a/",
		"s//" + FindingStageEmpty + "/b/",
	}
	for i, f := range in {
		got := f.subject() + "//" + f.Kind + "/" + f.RequiredKey + f.StageKey + "/" + f.ProcessID + f.ElementID
		if got != want[i] {
			t.Errorf("finding %d = %q, want %q", i, got, want[i])
		}
	}
}

// TestGapsReportsAProcessTwoCapabilitiesClaim: the map exists to make ownership clear,
// so one implementation with two owners is the thing it is meant to surface. Before it
// was a finding, whichever capability happened to be read last silently won — and the
// call.undeclared comparison then attributed a call to the wrong caller.
func TestGapsReportsAProcessTwoCapabilitiesClaim(t *testing.T) {
	caps := []Capability{
		{Key: "billing", Name: "Billing", State: StateActive,
			Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "fin", ProcessID: "invoice"}}},
		{Key: "invoicing", Name: "Invoicing", State: StateActive,
			Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "fin", ProcessID: "invoice"}}},
	}
	land := Landscape{Processes: []Process{
		{ApplicationKey: "fin", ProcessID: "invoice", Version: 1, CanView: true},
	}}
	rep := Gaps(caps, nil, land)
	fs := findingsOf(rep, FindingProcessShared)
	if len(fs) != 1 {
		t.Fatalf("findings = %+v, want the contested process reported exactly once", rep.Findings)
	}
	if fs[0].ProcessID != "invoice" {
		t.Errorf("finding = %+v", fs[0])
	}
	for _, key := range []string{"billing", "invoicing"} {
		if !strings.Contains(fs[0].Detail, key) {
			t.Errorf("detail %q does not name %q", fs[0].Detail, key)
		}
	}
	// It is not also unclaimed: it is claimed twice, which is a different problem.
	if has(rep, FindingProcessUnclaimed) {
		t.Error("a contested process was also reported as unclaimed")
	}
}

// The claimant a contested process resolves to must not depend on read order, or two
// identical reports would disagree about who called what.
func TestGapsResolvesAContestedProcessDeterministically(t *testing.T) {
	build := func(order ...Capability) GapReport {
		return Gaps(order, nil, Landscape{
			Processes: []Process{
				{ApplicationKey: "x", ProcessID: "shared", Version: 1, CanView: true},
				{ApplicationKey: "x", ProcessID: "callee", Version: 1, CanView: true},
			},
			Calls: []Call{{CallerProcessID: "shared", ElementID: "c", CalledProcessID: "callee", Resolved: true}},
		})
	}
	first := Capability{Key: "aaa", Name: "A", State: StateActive,
		Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "x", ProcessID: "shared"}}}
	second := Capability{Key: "zzz", Name: "Z", State: StateActive,
		Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "x", ProcessID: "shared"}}}
	callee := Capability{Key: "callee", Name: "Callee", State: StateActive,
		Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "x", ProcessID: "callee"}}}

	a := findingsOf(build(first, second, callee), FindingCallUndeclared)
	b := findingsOf(build(second, first, callee), FindingCallUndeclared)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("findings = %+v / %+v", a, b)
	}
	if a[0].CapabilityKey != b[0].CapabilityKey {
		t.Errorf("the undeclared call was attributed to %q in one order and %q in the other",
			a[0].CapabilityKey, b[0].CapabilityKey)
	}
}
