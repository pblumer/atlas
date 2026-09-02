package playground_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/playground"
)

// rows builds n cases whose only start variable is their number, so a report can
// be checked against the data that produced it.
func rows(n int) [][]model.VariableValue {
	out := make([][]model.VariableValue, n)
	for i := range out {
		out[i] = []model.VariableValue{{Name: "n", Kind: model.VarNumber, Text: itoa(i)}}
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// runPlan seeds a plan and runs it to rest.
func runPlan(t *testing.T, sb *playground.Sandbox, p playground.Plan) playground.Progress {
	t.Helper()
	if err := sb.StartPlan(p); err != nil {
		t.Fatalf("start plan: %v", err)
	}
	prog, err := sb.Run(playground.DefaultBudget())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !prog.Quiescent {
		t.Fatalf("run stopped on its budget: %+v", prog)
	}
	return prog
}

// Everything at once is the load test for the model: five cases, one hour of
// work, an hour on the clock.
func TestAllCasesArriveAtOnce(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Hour, Max: time.Hour},
	})
	runPlan(t, sb, playground.Plan{Cases: rows(5)})

	if got, want := sb.Now(), simStart.Add(time.Hour); !got.Equal(want) {
		t.Errorf("simulated end = %s, want %s", got, want)
	}
	rep, err := sb.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Cases != 5 || rep.Completed != 5 || rep.Incidents != 0 {
		t.Errorf("report = %+v, want five cases all completed", rep)
	}
}

// A fixed takt spreads the arrivals: four cases every half hour arrive at 0, 30,
// 60 and 90 minutes, and the last one finishes an hour after it arrived.
func TestCasesArriveOnAFixedTakt(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Hour, Max: time.Hour},
	})
	runPlan(t, sb, playground.Plan{
		Cases:   rows(4),
		Arrival: playground.Arrival{Mode: playground.ArrivalEvery, Interval: 30 * time.Minute},
	})

	if got, want := sb.Now(), simStart.Add(150*time.Minute); !got.Equal(want) {
		t.Errorf("simulated end = %s, want %s (last arrival at 90m + an hour of work)", got, want)
	}
}

// Sequential means the next case starts when the one before it is done — the
// shape a person uses to walk a dataset through one case at a time.
func TestSequentialArrivalsWaitForTheOneBefore(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Hour, Max: time.Hour},
	})
	runPlan(t, sb, playground.Plan{
		Cases:   rows(3),
		Arrival: playground.Arrival{Mode: playground.ArrivalSequential},
	})

	if got, want := sb.Now(), simStart.Add(3*time.Hour); !got.Equal(want) {
		t.Errorf("simulated end = %s, want %s — three cases, one after another", got, want)
	}
	// Never more than one case in flight is the property that distinguishes this
	// from a takt that happens to be as long as the work.
	if got := sb.Report; got == nil {
		t.Fatal("no report")
	}
	rep, err := sb.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.MaxInFlight != 1 {
		t.Errorf("max cases in flight = %d, want 1", rep.MaxInFlight)
	}
}

// A Poisson stream is random but confined: every arrival falls inside the window,
// none is lost, and the same seed produces the same stream.
func TestPoissonArrivalsStayInsideTheirWindow(t *testing.T) {
	plan := playground.Plan{
		Cases: rows(40),
		Arrival: playground.Arrival{
			Mode: playground.ArrivalPoisson, PerHour: 10,
			Calendar: playground.Calendar{Open: []playground.Window{{From: 8 * time.Hour, To: 17 * time.Hour}}},
		},
	}
	at := func(seed int64) []time.Time {
		sb, err := playground.Open(playground.Options{
			ModelXML: fixture(t, "sequence.bpmn"), BaseDir: t.TempDir(), StartTime: simStart, Seed: seed,
		})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = sb.Close() })
		if err := sb.StartPlan(plan); err != nil {
			t.Fatalf("start plan: %v", err)
		}
		return sb.Arrivals()
	}

	times := at(4711)
	if len(times) != 40 {
		t.Fatalf("planned %d arrivals, want 40", len(times))
	}
	for i, ts := range times {
		h := ts.UTC().Hour()
		if h < 8 || h >= 17 {
			t.Errorf("arrival %d at %s is outside the 08–17 window", i, ts)
		}
		if i > 0 && ts.Before(times[i-1]) {
			t.Errorf("arrival %d at %s is before its predecessor %s", i, ts, times[i-1])
		}
	}
	same := at(4711)
	for i := range times {
		if !times[i].Equal(same[i]) {
			t.Fatalf("the same seed produced a different stream at %d: %s vs %s", i, times[i], same[i])
		}
	}
}

// The report is what the run is for: how many cases, how they ended, how long
// they took, and where the time went.
func TestTheReportSaysWhatHappened(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human:  &playground.Stub{Min: time.Hour, Max: time.Hour},
		Pools:  map[string]playground.Pool{"clerks": {Capacity: 2}},
		PoolOf: map[string]string{"approve": "clerks"},
	})
	runPlan(t, sb, playground.Plan{Cases: rows(6)})

	rep, err := sb.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Cases != 6 || rep.Completed != 6 {
		t.Errorf("report = %+v, want six completed cases", rep)
	}
	if !rep.SimStart.Equal(simStart) || !rep.SimEnd.Equal(simStart.Add(3*time.Hour)) {
		t.Errorf("run spanned %s → %s, want %s → %s", rep.SimStart, rep.SimEnd, simStart, simStart.Add(3*time.Hour))
	}
	// Two seats, six one-hour cases: the median case waits an hour on top of its
	// hour of work, so the middle of the distribution is two hours.
	if rep.Duration.P50 < time.Hour || rep.Duration.P50 > 3*time.Hour {
		t.Errorf("median duration = %s, outside the possible range", rep.Duration.P50)
	}
	if rep.Duration.Max != 3*time.Hour {
		t.Errorf("longest case = %s, want 3h", rep.Duration.Max)
	}
	if rep.Pools["clerks"].Served != 6 {
		t.Errorf("pool served %d, want 6", rep.Pools["clerks"].Served)
	}
	if rep.Visits["approve"] != 6 {
		t.Errorf("visits on approve = %d, want 6", rep.Visits["approve"])
	}
}

// The per-case rows are read a page at a time, in the order the cases arrived,
// because fifty thousand of them will not be handed over in one response.
func TestCasesAreReadAPageAtATimeInArrivalOrder(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Hour, Max: time.Hour},
	})
	runPlan(t, sb, playground.Plan{Cases: rows(7)})

	page, total, err := sb.Cases(2, 3)
	if err != nil {
		t.Fatalf("cases: %v", err)
	}
	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
	if len(page) != 3 {
		t.Fatalf("page = %d rows, want 3", len(page))
	}
	for i, row := range page {
		if row.Index != i+2 {
			t.Errorf("row %d carries index %d, want %d", i, row.Index, i+2)
		}
		if row.Variables["n"] != itoa(i+2) {
			t.Errorf("row %d holds n=%q, want %q — the pages are in arrival order",
				row.Index, row.Variables["n"], itoa(i+2))
		}
		if row.State != model.PICompleted {
			t.Errorf("row %d state = %v, want completed", row.Index, row.State)
		}
		if row.Duration != time.Hour {
			t.Errorf("row %d took %s, want an hour", row.Index, row.Duration)
		}
		if row.End != "end" {
			t.Errorf("row %d ended at %q, want \"end\"", row.Index, row.End)
		}
	}

	// A page past the end is empty rather than an error: a caller walking pages
	// stops when one comes back short.
	last, _, err := sb.Cases(7, 3)
	if err != nil || len(last) != 0 {
		t.Errorf("page past the end = %d rows, err %v", len(last), err)
	}
}

// A failing stub puts cases into incidents, and the report counts them rather
// than reporting a completion rate that quietly excludes them.
func TestTheReportCountsCasesThatDidNotFinish(t *testing.T) {
	sb := openSandbox(t, "service-task.bpmn", playground.StubSet{
		Default: &playground.Stub{Min: time.Minute, Max: time.Minute, FailPerMillion: 1_000_000},
	})
	runPlan(t, sb, playground.Plan{Cases: rows(4)})

	rep, err := sb.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Cases != 4 || rep.Completed != 0 || rep.Incidents != 4 {
		t.Errorf("report = %+v, want four cases, none completed, four incidents", rep)
	}
	// And it counts them per element. "Four incidents" says a run went wrong; "four
	// incidents, all at the charge" says where, which is what the diagram can shade.
	if got := rep.Elements["charge"].Incidents; got != 4 {
		t.Errorf("incidents at the charge = %d, want all four", got)
	}
	for id, st := range rep.Elements {
		if id != "charge" && st.Incidents != 0 {
			t.Errorf("%q carries %d incidents, but nothing failed there", id, st.Incidents)
		}
	}
}

// An element's incidents sit beside its other measures rather than replacing them:
// a failing answer is still an answer, so the element records a run *and* an
// incident. An overlay that shaded by incidents would otherwise disagree with one
// that shaded by runs about whether anything happened there.
func TestAnElementsIncidentsSitBesideItsOtherMeasures(t *testing.T) {
	sb := openSandbox(t, "service-task.bpmn", playground.StubSet{
		Default: &playground.Stub{Min: time.Minute, Max: time.Minute, FailPerMillion: 1_000_000},
	})
	runPlan(t, sb, playground.Plan{Cases: rows(2)})

	rep, err := sb.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	st, ok := rep.Elements["charge"]
	if !ok {
		t.Fatal("the element every case is stuck on is missing from the report")
	}
	if st.Runs != 2 || st.Incidents != 2 {
		t.Errorf("stat = %+v, want two answers, both of them failures", st)
	}
	if st.Work != 2*time.Minute {
		t.Errorf("work = %s, want the two minutes the failing answers took", st.Work)
	}
}

// The same dataset, policy and seed produce the same run — the property that lets
// a report be quoted in a review and a scenario be used as a regression check.
func TestABatchIsReproducible(t *testing.T) {
	end := func(seed int64) (time.Time, time.Duration) {
		sb, err := playground.Open(playground.Options{
			ModelXML: fixture(t, "user-task.bpmn"), BaseDir: t.TempDir(), StartTime: simStart, Seed: seed,
			Stubs: playground.StubSet{
				Human:  &playground.Stub{Min: time.Minute, Max: 4 * time.Hour},
				Pools:  map[string]playground.Pool{"clerks": {Capacity: 2}},
				PoolOf: map[string]string{"approve": "clerks"},
			},
		})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = sb.Close() })
		runPlan(t, sb, playground.Plan{
			Cases:   rows(12),
			Arrival: playground.Arrival{Mode: playground.ArrivalPoisson, PerHour: 4},
		})
		rep, err := sb.Report()
		if err != nil {
			t.Fatalf("report: %v", err)
		}
		return rep.SimEnd, rep.Duration.P50
	}
	e1, p1 := end(99)
	e2, p2 := end(99)
	if !e1.Equal(e2) || p1 != p2 {
		t.Errorf("the same seed produced different runs: %s/%s vs %s/%s", e1, p1, e2, p2)
	}
}

// A plan with no cases is a mistake worth naming, not an empty run.
func TestAnEmptyPlanIsRefused(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	if err := sb.StartPlan(playground.Plan{}); err == nil {
		t.Error("a plan with no cases should be refused")
	}
}

// A batch far larger than anything a person steps through has to stay consistent:
// every case accounted for, the report's totals agreeing with the pages, and the
// pages covering the whole set exactly once. Two thousand is enough to catch a
// mistake that only appears in bulk; the stated ceiling of fifty thousand is
// exercised by BenchmarkBatch, which reports what it costs.
func TestALargeBatchStaysConsistent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the bulk consistency check in short mode")
	}
	const n = 2000
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human:  &playground.Stub{Min: time.Minute, Max: 20 * time.Minute},
		Pools:  map[string]playground.Pool{"clerks": {Capacity: 25}},
		PoolOf: map[string]string{"approve": "clerks"},
	})
	runPlan(t, sb, playground.Plan{
		Cases:   rows(n),
		Arrival: playground.Arrival{Mode: playground.ArrivalPoisson, PerHour: 400},
	})

	rep, err := sb.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Cases != n || rep.Completed != n {
		t.Fatalf("report = %d cases, %d completed, want %d of each", rep.Cases, rep.Completed, n)
	}
	if rep.Duration.Count != n {
		t.Errorf("summarised %d durations, want %d", rep.Duration.Count, n)
	}
	if rep.Duration.Min > rep.Duration.P50 || rep.Duration.P50 > rep.Duration.P90 || rep.Duration.P90 > rep.Duration.Max {
		t.Errorf("durations are not ordered: %+v", rep.Duration)
	}
	if rep.Pools["clerks"].Served != n {
		t.Errorf("the pool served %d, want %d", rep.Pools["clerks"].Served, n)
	}
	if rep.MaxInFlight < 2 {
		t.Errorf("max in flight = %d; a Poisson stream at 400/h against 25 seats should overlap", rep.MaxInFlight)
	}

	// The pages cover every row exactly once, in order.
	seen := make(map[string]bool, n)
	for offset := 0; offset < n; offset += 250 {
		page, total, err := sb.Cases(offset, 250)
		if err != nil {
			t.Fatalf("page at %d: %v", offset, err)
		}
		if total != n {
			t.Fatalf("page at %d reports %d rows in total, want %d", offset, total, n)
		}
		for i, row := range page {
			if row.Index != offset+i {
				t.Fatalf("row %d of the page at %d carries index %d", i, offset, row.Index)
			}
			if seen[row.Variables["n"]] {
				t.Fatalf("case n=%s appears in two pages", row.Variables["n"])
			}
			seen[row.Variables["n"]] = true
		}
	}
	if len(seen) != n {
		t.Errorf("the pages covered %d cases, want %d", len(seen), n)
	}
}

// BenchmarkBatch reports what a batch costs per case. It is how the ceiling in
// the record is checked rather than asserted: run it with -benchtime=Nx to size
// the batch, e.g. `go test ./playground -bench=Batch -benchtime=50000x`.
func BenchmarkBatch(b *testing.B) {
	xml, err := os.ReadFile(filepath.Join("testdata", "user-task.bpmn"))
	if err != nil {
		b.Fatalf("read fixture: %v", err)
	}
	sb, err := playground.Open(playground.Options{
		ModelXML: xml, BaseDir: b.TempDir(), StartTime: simStart, Seed: 1,
		Stubs: playground.StubSet{Human: &playground.Stub{Min: time.Minute, Max: 30 * time.Minute}},
	})
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer func() { _ = sb.Close() }()

	if err := sb.StartPlan(playground.Plan{
		Cases:   rows(b.N),
		Arrival: playground.Arrival{Mode: playground.ArrivalPoisson, PerHour: 5000},
	}); err != nil {
		b.Fatalf("start plan: %v", err)
	}
	b.ResetTimer()
	prog, err := sb.Run(playground.Budget{MaxOccurrences: b.N * 20, Horizon: 365 * 24 * time.Hour})
	if err != nil {
		b.Fatalf("run: %v", err)
	}
	if !prog.Quiescent {
		b.Fatalf("the batch did not finish: %+v", prog)
	}
}

// What a plan refuses before anything runs.
func TestArrivalConfigurationRefusals(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	cases := map[string]playground.Arrival{
		"a takt with no interval":       {Mode: playground.ArrivalEvery},
		"a Poisson stream with no rate": {Mode: playground.ArrivalPoisson},
		"an arrival mode nobody has":    {Mode: playground.ArrivalMode(42)},
	}
	for name, a := range cases {
		t.Run(name, func(t *testing.T) {
			if err := sb.StartPlan(playground.Plan{Cases: rows(1), Arrival: a}); err == nil {
				t.Error("this plan should be refused")
			}
		})
	}
	// A calendar that never opens cannot carry a stream either.
	never := playground.Calendar{Open: []playground.Window{{From: 9 * time.Hour, To: 9 * time.Hour}}}
	if err := sb.StartPlan(playground.Plan{
		Cases:   rows(1),
		Arrival: playground.Arrival{Mode: playground.ArrivalEvery, Interval: time.Minute, Calendar: never},
	}); err == nil {
		t.Error("a stream on a calendar that never opens should be refused")
	}
}

// A sandbox with no plan has no arrivals to show.
func TestASandboxWithNoPlanHasNoArrivals(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	if got := sb.Arrivals(); len(got) != 0 {
		t.Errorf("arrivals = %v, want none", got)
	}
}

// The page bounds are clamped rather than refused: a caller walking pages should
// not have to reason about the last one.
func TestPageBoundsAreClamped(t *testing.T) {
	sb := openSandbox(t, "sequence.bpmn", playground.StubSet{})
	runPlan(t, sb, playground.Plan{Cases: rows(3)})

	if rows, total, err := sb.Cases(-5, 2); err != nil || total != 3 || len(rows) != 2 || rows[0].Index != 0 {
		t.Errorf("a negative offset gave %d rows starting at %v (err %v)", len(rows), rows, err)
	}
	if rows, _, err := sb.Cases(1, 99); err != nil || len(rows) != 2 {
		t.Errorf("a limit past the end gave %d rows (err %v)", len(rows), err)
	}
	if rows, _, err := sb.Cases(0, 0); err != nil || len(rows) != 0 {
		t.Errorf("a zero limit gave %d rows (err %v)", len(rows), err)
	}
}

// A pool whose calendar cannot fit the work says so rather than running for ever.
func TestWorkThatOutlastsItsPoolCalendarIsReported(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: 2 * time.Hour, Max: 2 * time.Hour},
		Pools: map[string]playground.Pool{"clerks": {
			Capacity: 1,
			Calendar: playground.Calendar{Open: []playground.Window{{From: 9 * time.Hour, To: 9*time.Hour + time.Minute}}},
		}},
		PoolOf: map[string]string{"approve": "clerks"},
	})
	if _, err := sb.StartCase(); err != nil {
		t.Fatalf("start case: %v", err)
	}
	if _, err := sb.Run(playground.DefaultBudget()); err == nil {
		t.Error("a pool that works a minute a day cannot finish two hours of work; the run should say so")
	}
}

// An interrupting boundary event takes a case away while it is queued for a seat.
// The seat and the queue have to forget it, or the pool works for a case that no
// longer exists.
func TestAnInterruptedCaseLeavesItsPool(t *testing.T) {
	sb := openSandbox(t, "boundary-timer.bpmn", playground.StubSet{
		Default: &playground.Stub{Min: 4 * time.Hour, Max: 4 * time.Hour},
		Pools:   map[string]playground.Pool{"machines": {Capacity: 1}},
		PoolOf:  map[string]string{"work": "machines"},
	})
	// Two cases for one seat: the first is served, the second waits — and the
	// ten-minute boundary timer takes both before either finishes.
	runPlan(t, sb, playground.Plan{Cases: rows(2)})

	rep, err := sb.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Completed != 2 {
		t.Errorf("completed = %d, want both cases down the escalation branch", rep.Completed)
	}
	if p := rep.Pools["machines"]; p.Served != 0 {
		t.Errorf("the pool served %d, want none: neither case's work ever finished", p.Served)
	}
	if got, want := sb.Now(), simStart.Add(10*time.Minute); !got.Equal(want) {
		t.Errorf("simulated end = %s, want %s — the timer, not the four-hour work", got, want)
	}
}

// Every kind of start variable has to reach the results table as something a
// reader can see. Reading only the text field lost two of them: a boolean keeps
// its value elsewhere, so a dataset carrying an "express" flag showed an empty
// column — the column was there, which reads as "no case had one" rather than as
// a gap in the panel.
func TestEveryKindOfVariableReachesTheResultsTable(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	runPlan(t, sb, playground.Plan{Cases: [][]model.VariableValue{{
		{Name: "kunde", Kind: model.VarString, Text: "A"},
		{Name: "betrag", Kind: model.VarNumber, Text: "1200"},
		{Name: "express", Kind: model.VarBool, Bool: true},
		{Name: "eilig", Kind: model.VarBool, Bool: false},
		{Name: "notiz", Kind: model.VarNull},
		{Name: "adresse", Kind: model.VarJSON, Text: `{"ort":"Bern"}`},
	}}})

	page, _, err := sb.Cases(0, 1)
	if err != nil {
		t.Fatalf("cases: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("page = %d rows, want the one case", len(page))
	}
	for name, want := range map[string]string{
		"kunde":   "A",
		"betrag":  "1200",
		"express": "true",
		"eilig":   "false",
		"notiz":   "null",
		"adresse": `{"ort":"Bern"}`,
	} {
		if got := page[0].Variables[name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
