package capability

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The window is required, and these are the two ways a caller finds that out.
func TestParseWindowRefusesTheUnbounded(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, tc := range []struct {
		name string
		days int
		want string
	}{
		{"zero is not all history", 0, "required"},
		{"negative", -7, "required"},
		{"beyond the ceiling", maxWindowDays + 1, "at most"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseWindow(tc.days, now); err == nil {
				t.Fatal("ParseWindow accepted it; an unbounded reading is what the measurement ruled out")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// A window ends now and reaches back, and says how far so a response can report what
// it measured rather than echoing what was asked for.
func TestParseWindowReachesBackFromNow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	w, err := ParseWindow(30, now)
	if err != nil {
		t.Fatalf("ParseWindow: %v", err)
	}
	if w.To != now.Unix() {
		t.Errorf("To = %d, want now (%d)", w.To, now.Unix())
	}
	if got := w.Days(); got != 30 {
		t.Errorf("Days() = %d, want 30", got)
	}
	if w.From >= w.To {
		t.Errorf("From %d is not before To %d", w.From, w.To)
	}
}

// TestMeasureKeepsCountedAndWalkedApart is the property the whole surface rests on.
// Outcome counts come from all-time counters and cycle times from a windowed walk; a
// response that presented them as one kind of number would be wrong in a way a reader
// cannot see, because both are just integers on a screen.
func TestMeasureKeepsCountedAndWalkedApart(t *testing.T) {
	w, err := ParseWindow(30, time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatalf("ParseWindow: %v", err)
	}
	m := Measure(Capability{Key: "loan-underwriting", Name: "Underwrite a loan"}, w, []RecordedProcess{{
		ProcessID: "underwrite", Name: "Underwriting", Deployed: true,
		EndEvents: []ElementCount{{ElementID: "e-declined", Name: "Declined", Count: 40}},
		Durations: []int64{10, 20},
	}})
	if m.CountedBasis == "" || m.WalkedBasis == "" {
		t.Fatal("the response does not say what its figures rest on")
	}
	if m.CountedBasis == m.WalkedBasis {
		t.Error("both bases read the same; the distinction is the point")
	}
	// The counters say 40 cases ended declined, all time. The window held 2 cases.
	// Both are true, and a reader who took the 40 as being inside the window would be
	// wrong — which is what the basis strings exist to prevent.
	if m.Processes[0].Outcomes[0].Count != 40 {
		t.Errorf("outcome count = %d, want the all-time 40", m.Processes[0].Outcomes[0].Count)
	}
	if m.Processes[0].Cases.Cases != 2 {
		t.Errorf("cases = %d, want the windowed 2", m.Processes[0].Cases.Cases)
	}
}

// An SLA with a machine-readable threshold is measured by counting; one written only
// as prose is reported as not measured, with the reason and the remedy.
func TestMeasureOnlyComputesAnSLAItCanRead(t *testing.T) {
	w, _ := ParseWindow(7, time.Unix(1_800_000_000, 0))
	c := Capability{
		Key: "onboarding",
		SLAs: []SLA{
			{Name: "Ten minutes", Metric: "cycle time", Threshold: "within 10 minutes", ThresholdSeconds: 600},
			{Name: "Five business days", Metric: "cycle time", Threshold: "within five business days"},
		},
	}
	// Four cases: 300s, 600s, 601s, 900s. Two are within ten minutes — and 600 is,
	// because "within ten minutes" includes ten.
	m := Measure(c, w, []RecordedProcess{{
		ProcessID: "onboard", Deployed: true, Durations: []int64{300, 600, 601, 900},
	}})
	if len(m.SLAs) != 1 {
		t.Fatalf("measured %d SLAs, want only the one with a number: %+v", len(m.SLAs), m.SLAs)
	}
	got := m.SLAs[0]
	if got.Within != 2 || got.Cases != 4 {
		t.Errorf("attainment = %d/%d, want 2/4 (the case at exactly the threshold is within it)", got.Within, got.Cases)
	}
	if got.Share != 0.5 {
		t.Errorf("share = %v, want 0.5", got.Share)
	}
	var prose *NotMeasured
	for i := range m.NotMeasured {
		if m.NotMeasured[i].Name == "Five business days" {
			prose = &m.NotMeasured[i]
		}
	}
	if prose == nil {
		t.Fatal("the prose SLA is neither measured nor reported as unmeasured; it vanished")
	}
	if prose.Reason != ReasonProseThreshold {
		t.Errorf("reason = %q, want the prose-threshold reason naming the remedy", prose.Reason)
	}
}

// A KPI is never computed, and that is reported rather than left to an absence. A KPI
// names a goal in the business's words; guessing which recorded figure it refers to
// would put a number somebody acts on under a name nobody authored.
func TestMeasureReportsEveryKPIAsNotMeasured(t *testing.T) {
	w, _ := ParseWindow(7, time.Unix(1_800_000_000, 0))
	m := Measure(Capability{KPIs: []KPI{{Name: "Disburse within three days"}}}, w, nil)
	if len(m.NotMeasured) != 1 || m.NotMeasured[0].Kind != "kpi" {
		t.Fatalf("notMeasured = %+v, want the KPI named", m.NotMeasured)
	}
}

// Restricted and not-deployed are different answers and must not collapse into zero.
// Zero cases is a finding — nothing ran — and "you may not look" is not.
func TestMeasureDoesNotZeroFillWhatItCouldNotRead(t *testing.T) {
	w, _ := ParseWindow(7, time.Unix(1_800_000_000, 0))
	m := Measure(Capability{Key: "c"}, w, []RecordedProcess{
		{ProcessID: "hidden", Restricted: true},
		{ProcessID: "gone", Deployed: false},
	})
	if len(m.Processes) != 2 {
		t.Fatalf("processes = %d, want both kept", len(m.Processes))
	}
	if !m.Processes[0].Restricted || m.Processes[1].Restricted {
		t.Errorf("restricted flags = %v/%v", m.Processes[0].Restricted, m.Processes[1].Restricted)
	}
	// Neither could be measured, so the capability as a whole reports that there was
	// nothing to measure rather than reporting zeroes that look like an answer.
	if !m.Unrealizable {
		t.Error("unrealizable = false, but neither realisation could be read")
	}
}

// One capability, two realising processes: each keeps its own cycle time, and the SLA
// is measured over both. Summing two implementations' cycle times would produce a
// number describing nothing; an SLA, by contrast, is a promise about the capability.
func TestMeasurePoolsCasesForTheSLAButNotForTheProcesses(t *testing.T) {
	w, _ := ParseWindow(7, time.Unix(1_800_000_000, 0))
	c := Capability{SLAs: []SLA{{Name: "fast", ThresholdSeconds: 100}}}
	m := Measure(c, w, []RecordedProcess{
		{ProcessID: "a", Deployed: true, Durations: []int64{10, 20}},
		{ProcessID: "b", Deployed: true, Durations: []int64{1000}},
	})
	if m.Processes[0].Cases.Cases != 2 || m.Processes[1].Cases.Cases != 1 {
		t.Errorf("per-process cases = %d/%d, want 2 and 1 kept apart",
			m.Processes[0].Cases.Cases, m.Processes[1].Cases.Cases)
	}
	if m.SLAs[0].Cases != 3 || m.SLAs[0].Within != 2 {
		t.Errorf("SLA attainment = %d/%d, want 2/3 pooled across both realisations",
			m.SLAs[0].Within, m.SLAs[0].Cases)
	}
}

// An SLA over a window that held no case is not 100% attained and not 0%. Share is
// zero because there is nothing to divide, and Cases is what says so — which is why
// the reader is given both rather than the rate alone.
func TestMeasureSaysNothingRanRatherThanClaimingAttainment(t *testing.T) {
	w, _ := ParseWindow(7, time.Unix(1_800_000_000, 0))
	m := Measure(Capability{SLAs: []SLA{{Name: "fast", ThresholdSeconds: 100}}},
		w, []RecordedProcess{{ProcessID: "a", Deployed: true}})
	if m.SLAs[0].Cases != 0 || m.SLAs[0].Share != 0 {
		t.Errorf("attainment over an empty window = %+v", m.SLAs[0])
	}
	if m.Processes[0].Cases.Cases != 0 || m.Processes[0].Cases.MeanSeconds != 0 {
		t.Errorf("stats over no cases = %+v", m.Processes[0].Cases)
	}
}

// Cycle-time statistics are a streaming fold, and this pins the arithmetic.
func TestSummarizeFoldsWithoutHoldingTheWindow(t *testing.T) {
	got := summarize([]int64{30, 10, 20})
	if got.Cases != 3 || got.MinSeconds != 10 || got.MaxSeconds != 30 || got.MeanSeconds != 20 {
		t.Errorf("summarize = %+v, want 3 cases, 10..30, mean 20", got)
	}
}

// Outcomes come back commonest first, and ties break by element id so two reads of an
// unchanged store are diffable.
func TestOutcomesAreOrderedCommonestFirstAndDeterministically(t *testing.T) {
	got := sortedCounts([]ElementCount{
		{ElementID: "b", Count: 5},
		{ElementID: "a", Count: 5},
		{ElementID: "c", Count: 9},
	})
	if got[0].ElementID != "c" {
		t.Errorf("first = %q, want the commonest", got[0].ElementID)
	}
	if got[1].ElementID != "a" || got[2].ElementID != "b" {
		t.Errorf("tie order = %q,%q, want a,b by element id", got[1].ElementID, got[2].ElementID)
	}
}

// The handler's own paths, exercised in this package because that is where they are
// counted: a request from api_test runs them but credits them to nothing, and a
// function nobody can see the coverage of is a function nobody notices going untested.

// A server with no resolver wired says so rather than answering with an empty
// measurement, which would read as "nothing ran".
func TestMeasurementWithoutAResolverSaysSo(t *testing.T) {
	fx := newFixture(t)
	if rec := fx.do(t, "POST", "/api/v1/capabilities",
		map[string]any{"key": "c", "name": "C"}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	rec := fx.do(t, "GET", "/api/v1/capabilities/c/measurement?windowDays=7", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("code = %d %s, want 501", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "cannot measure") {
		t.Errorf("body = %s, want it to say this server cannot measure", rec.Body)
	}
}

// windowDays is validated before anything is read, and each way of getting it wrong
// gets its own answer.
func TestMeasurementValidatesTheWindowBeforeReading(t *testing.T) {
	fx := newFixture(t)
	reached := false
	fx.svc.SetMeasurementResolver(func(*http.Request, []string, Window) ([]RecordedProcess, error) {
		reached = true
		return nil, nil
	})
	for _, tc := range []struct{ name, query, want string }{
		{"missing", "", "windowDays is required"},
		{"not a number", "?windowDays=soon", "whole number"},
		{"zero", "?windowDays=0", "required"},
		{"too large", "?windowDays=99999", "at most"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := fx.do(t, "GET", "/api/v1/capabilities/c/measurement"+tc.query, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d %s, want 400", rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("body = %s, want %q", rec.Body, tc.want)
			}
		})
	}
	// And none of them reached the resolver: a bad window must not cost a read, which
	// is the whole reason the parameter is checked first.
	if reached {
		t.Error("the resolver ran despite an invalid window")
	}
}

// A capability that does not exist is a 404, not an empty measurement.
func TestMeasurementOfAnUnknownCapabilityIsNotFound(t *testing.T) {
	fx := newFixture(t)
	fx.svc.SetMeasurementResolver(func(*http.Request, []string, Window) ([]RecordedProcess, error) {
		return nil, nil
	})
	if rec := fx.do(t, "GET", "/api/v1/capabilities/nope/measurement?windowDays=7", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d %s, want 404", rec.Code, rec.Body)
	}
}

// A failing read is reported as a failure. An empty measurement would say the
// capability ran nothing, which is a different and false claim.
func TestMeasurementReportsAFailedRead(t *testing.T) {
	fx := newFixture(t)
	if rec := fx.do(t, "POST", "/api/v1/capabilities",
		map[string]any{"key": "c", "name": "C"}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d", rec.Code)
	}
	fx.svc.SetMeasurementResolver(func(*http.Request, []string, Window) ([]RecordedProcess, error) {
		return nil, errors.New("the store is unreadable")
	})
	rec := fx.do(t, "GET", "/api/v1/capabilities/c/measurement?windowDays=7", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d %s, want 500", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "unreadable") {
		t.Errorf("body = %s, want the underlying reason", rec.Body)
	}
}

// Only process realisations reach the resolver. A purchased system or a person is not
// a process that failed to deploy, and passing it on would make the resolver answer
// "not deployed" about something never meant to be.
func TestOnlyProcessRealizationsAreMeasured(t *testing.T) {
	fx := newFixture(t)
	if rec := fx.do(t, "POST", "/api/v1/capabilities", map[string]any{
		"key": "c", "name": "C",
		"realizations": []any{
			map[string]any{"kind": "process", "applicationKey": "app", "processId": "p1"},
			map[string]any{"kind": "manual", "note": "a clerk"},
			map[string]any{"kind": "system", "note": "a purchased scoring tool"},
		},
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var asked []string
	fx.svc.SetMeasurementResolver(func(_ *http.Request, ids []string, _ Window) ([]RecordedProcess, error) {
		asked = ids
		return nil, nil
	})
	if rec := fx.do(t, "GET", "/api/v1/capabilities/c/measurement?windowDays=7", nil); rec.Code != http.StatusOK {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
	if len(asked) != 1 || asked[0] != "p1" {
		t.Errorf("resolver was asked about %v, want only the process realization", asked)
	}
}
