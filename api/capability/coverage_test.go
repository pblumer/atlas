package capability

import "testing"

func TestCoverageResolvesAProcessRealization(t *testing.T) {
	c := Capability{Key: "identity", Name: "Identity Verification", State: StateActive,
		Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "kyc", ProcessID: "idv"}}}
	land := Landscape{Processes: []Process{
		{ApplicationKey: "kyc", ApplicationName: "KYC", ProcessID: "idv", Name: "Identity Verification",
			Version: 4, ActiveInstances: 17, CanView: true},
	}}
	cov := Coverage(c, nil, nil, land)
	if len(cov.Realizations) != 1 {
		t.Fatalf("realizations = %+v", cov.Realizations)
	}
	r := cov.Realizations[0]
	if !r.Resolved || r.Restricted {
		t.Fatalf("realization = %+v, want resolved and not restricted", r)
	}
	if r.Name != "Identity Verification" || r.Version != 4 || r.ActiveInstances != 17 {
		t.Errorf("realization = %+v, want the live facts resolved at read time", r)
	}
	if !cov.Realized {
		t.Error("Realized = false for a capability with a resolved realization")
	}
}

func TestCoverageResolvesTheNewestVersion(t *testing.T) {
	// A realization names a process, not a version. Two deployed versions are one
	// realization, and the one it means is the current one.
	c := Capability{Key: "b", Name: "B", Realizations: []Realization{
		{Kind: RealizationProcess, ApplicationKey: "a", ProcessID: "p"}}}
	land := Landscape{Processes: []Process{
		{ApplicationKey: "a", ProcessID: "p", Version: 1, CanView: true},
		{ApplicationKey: "a", ProcessID: "p", Version: 7, CanView: true},
		{ApplicationKey: "a", ProcessID: "p", Version: 3, CanView: true},
	}}
	if got := Coverage(c, nil, nil, land).Realizations[0].Version; got != 7 {
		t.Errorf("resolved version = %d, want the newest (7)", got)
	}
}

func TestCoverageMarksAnUnresolvedRealization(t *testing.T) {
	c := Capability{Key: "b", Name: "B", Realizations: []Realization{
		{Kind: RealizationProcess, ApplicationKey: "gone", ProcessID: "p"}}}
	cov := Coverage(c, nil, nil, Landscape{})
	if cov.Realizations[0].Resolved {
		t.Error("a realization pointing at nothing resolved")
	}
	if cov.Realized {
		t.Error("Realized = true although nothing the capability names is here")
	}
}

func TestCoverageMarksARestrictedRealizationSeparatelyFromAMissingOne(t *testing.T) {
	c := Capability{Key: "b", Name: "B", Realizations: []Realization{
		{Kind: RealizationProcess, ApplicationKey: "fin", ProcessID: "p"}}}
	land := Landscape{Processes: []Process{
		{ApplicationKey: "fin", ProcessID: "p", Name: "Secret", Version: 2, ActiveInstances: 9, CanView: false},
	}}
	r := Coverage(c, nil, nil, land).Realizations[0]
	if !r.Restricted {
		t.Fatal("a realization the caller may not see was not marked restricted")
	}
	if r.Name != "" || r.Version != 0 || r.ActiveInstances != 0 {
		t.Errorf("realization = %+v: a restricted entry must disclose nothing about what it points at", r)
	}
	if r.Resolved {
		t.Error("a restricted realization reported as resolved, which asserts something the caller may not know")
	}
}

func TestCoverageResolvesAWorkerRealization(t *testing.T) {
	c := Capability{Key: "n", Name: "Notify", Realizations: []Realization{
		{Kind: RealizationWorker, WorkerRef: "mail-desk"}}}
	land := Landscape{Workers: []Worker{{Ref: "mail-desk", Name: "Service desk mail", Type: "mail", CanView: true}}}
	r := Coverage(c, nil, nil, land).Realizations[0]
	if !r.Resolved || r.Name != "Service desk mail" || r.WorkerType != "mail" {
		t.Errorf("realization = %+v", r)
	}
}

func TestCoverageTreatsASystemOrManualRealizationAsResolved(t *testing.T) {
	// Atlas cannot check either, and "unresolved" would read as broken. A capability
	// done by a clerk is realized; it is simply not realized *here*.
	c := Capability{Key: "k", Name: "K", Realizations: []Realization{
		{Kind: RealizationSystem, Note: "Acme SaaS"},
		{Kind: RealizationManual, Note: "Branch clerk"},
	}}
	cov := Coverage(c, nil, nil, Landscape{})
	for _, r := range cov.Realizations {
		if !r.Resolved {
			t.Errorf("%s realization reported unresolved", r.Kind)
		}
		if r.Name != r.Note {
			t.Errorf("realization = %+v, want the note to be what a reader sees as its name", r)
		}
	}
	if !cov.Realized {
		t.Error("a capability done by a system and a clerk is realized")
	}
}

func TestCoverageResolvesDependenciesBothWays(t *testing.T) {
	onboarding := Capability{Key: "onboarding", Name: "Onboarding", Requires: []string{"identity", "ghost"}}
	all := []Capability{
		onboarding,
		{Key: "identity", Name: "Identity Verification",
			Realizations: []Realization{{Kind: RealizationManual, Note: "clerk"}},
			SLAs:         []SLA{{Name: "Decision", Metric: "cycleTime", Threshold: "10 min", Scope: SLAInternal}}},
		{Key: "loan-application", Name: "Loan Application", Requires: []string{"onboarding"}},
	}
	cov := Coverage(onboarding, all, nil, Landscape{})

	if len(cov.Requires) != 2 {
		t.Fatalf("requires = %+v", cov.Requires)
	}
	known, ghost := cov.Requires[0], cov.Requires[1]
	if known.Key == "ghost" {
		known, ghost = ghost, known
	}
	if !known.Known || !known.Realized || known.Name != "Identity Verification" {
		t.Errorf("known requirement = %+v", known)
	}
	if len(known.SLAs) != 1 {
		t.Errorf("a dependency's SLAs are the whole interface and must travel with it, got %+v", known.SLAs)
	}
	if ghost.Known {
		t.Errorf("a requirement naming nothing was reported as known: %+v", ghost)
	}

	if len(cov.RequiredBy) != 1 || cov.RequiredBy[0].Key != "loan-application" {
		t.Errorf("requiredBy = %+v, want the capability that depends on this one", cov.RequiredBy)
	}
}

func TestCoverageNamesTheValueStreamsAndStagesThatUseIt(t *testing.T) {
	c := Capability{Key: "loan-application", Name: "Loan Application"}
	streams := []ValueStream{
		{Key: "consumer-loan", Name: "Consumer Loan", Stages: []Stage{
			{Key: "apply", Name: "Application submission", Capabilities: []string{"loan-application"}},
			{Key: "underwrite", Name: "Credit evaluation", Capabilities: []string{"loan-application"}},
			{Key: "monitor", Name: "Repayment", Capabilities: []string{"servicing"}},
		}},
		{Key: "mortgage", Name: "Mortgage", Stages: []Stage{{Key: "x", Name: "X", Capabilities: []string{"other"}}}},
	}
	cov := Coverage(c, nil, streams, Landscape{})
	if len(cov.ValueStreams) != 1 {
		t.Fatalf("valueStreams = %+v", cov.ValueStreams)
	}
	use := cov.ValueStreams[0]
	if use.Key != "consumer-loan" || len(use.Stages) != 2 {
		t.Errorf("use = %+v, want an end-to-end capability named by every stage it spans", use)
	}
	if use.Stages[0].Key != "apply" || use.Stages[1].Key != "underwrite" {
		t.Errorf("stages = %+v, want them in the stream's own order", use.Stages)
	}
}

func TestCoverageIsDeterministic(t *testing.T) {
	c := Capability{Key: "a", Name: "A", Requires: []string{"z", "b"}}
	all := []Capability{c, {Key: "b", Name: "B"}, {Key: "z", Name: "Z"},
		{Key: "y", Name: "Y", Requires: []string{"a"}}, {Key: "x", Name: "X", Requires: []string{"a"}}}
	first, second := Coverage(c, all, nil, Landscape{}), Coverage(c, all, nil, Landscape{})
	if first.Requires[0].Key != "b" || first.Requires[1].Key != "z" {
		t.Errorf("requires are not in key order: %+v", first.Requires)
	}
	if first.RequiredBy[0].Key != "x" || first.RequiredBy[1].Key != "y" {
		t.Errorf("requiredBy is not in key order: %+v", first.RequiredBy)
	}
	if len(first.Requires) != len(second.Requires) || first.Requires[0].Key != second.Requires[0].Key {
		t.Error("two identical coverage reads differ")
	}
}

func TestCoverageSaysNothingIsMeasured(t *testing.T) {
	// The KPIs and SLAs on a record are declarations. If this read ever grows numbers,
	// it will be because a measurement slice landed — until then the answer has to say
	// so, or a client will render a goal as though it were an achievement.
	cov := Coverage(Capability{Key: "a", Name: "A",
		KPIs: []KPI{{Name: "Speed", Metric: "cycleTime", Goal: "< 3 d"}}}, nil, nil, Landscape{})
	if cov.Measurement == "" {
		t.Fatal("coverage does not say whether its KPIs are measured")
	}
}
