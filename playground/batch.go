package playground

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/pblumer/atlas/model"
)

// ArrivalMode is how a plan's cases are spread over simulated time.
type ArrivalMode int

const (
	// ArrivalAllAtOnce starts every case at the run's first instant. It is the load
	// test for the model rather than for the server: fifty thousand tokens in the
	// graph, not fifty thousand requests per second.
	ArrivalAllAtOnce ArrivalMode = iota
	// ArrivalSequential starts the next case when the one before it has finished —
	// one case in flight at a time.
	ArrivalSequential
	// ArrivalEvery starts one case per Interval.
	ArrivalEvery
	// ArrivalPoisson draws the gaps between arrivals from an exponential
	// distribution around PerHour, which is what a stream of unrelated arrivals
	// actually looks like: bursty, not evenly spaced.
	ArrivalPoisson
)

// Arrival says when a plan's cases start.
type Arrival struct {
	Mode ArrivalMode
	// Interval is the gap for ArrivalEvery.
	Interval time.Duration
	// PerHour is the mean rate for ArrivalPoisson.
	PerHour float64
	// Calendar confines arrivals to business hours: the stream pauses when it
	// closes and resumes when it opens, so a day's worth of cases lands inside the
	// day. The zero value is around the clock.
	Calendar
}

// Plan is a batch: the cases to run and when each of them starts.
type Plan struct {
	// Cases are the start variables of each case, in the order they were given.
	// That order is the report's order.
	Cases [][]model.VariableValue
	// Arrival spreads them over simulated time. The zero value starts them all at
	// once.
	Arrival Arrival
}

// Durations summarises how long the cases took. The percentiles come from the
// values themselves rather than from a fitted curve, because a run's own numbers
// are what a reader will check them against.
type Durations struct {
	Count              int
	Min, P50, P90, Max time.Duration
	Mean               time.Duration
}

// Report is what a run measured. It is folded from the sandbox's state in one
// pass, holding no object per case, so it is the same size for fifty cases and
// fifty thousand.
type Report struct {
	// Cases is how many were started, Completed how many reached an end event, and
	// Incidents how many tokens are parked behind a simulated failure. A completion
	// rate that quietly excluded the parked ones would be the useful-looking wrong
	// number.
	Cases, Completed, Incidents int
	// SimStart and SimEnd bound the run in simulated time.
	SimStart, SimEnd time.Time
	// MaxInFlight is the most cases that were ever active at the same time — the
	// work in progress a reader compares against the arrival rate.
	MaxInFlight int
	// Duration summarises the finished cases' elapsed time.
	Duration Durations
	// Visits counts every token that passed through an element (the heat map),
	// Elements what the sandbox measured at the jobs it answered (the bottleneck
	// ranking), and Pools what each pool did.
	Visits   map[string]int64
	Elements map[string]ElementStat
	Pools    map[string]PoolStat
}

// CaseRow is one case in the results table.
type CaseRow struct {
	// Index is the row's place in the dataset that produced it.
	Index       int
	InstanceKey uint64
	State       model.ProcessInstanceState
	Started     time.Time
	Ended       time.Time
	Duration    time.Duration
	Incidents   int
	// End is the BPMN id of the last element the case reached — the outcome a
	// reader groups the table by.
	End       string
	Variables map[string]string
}

// plan is the sandbox's live view of a batch: the cases still to release and when.
type plan struct {
	cases [][]model.VariableValue
	// at holds each case's arrival instant. It is empty for a sequential plan,
	// whose next arrival depends on the run rather than on the clock.
	at   []int64
	next int
	mode ArrivalMode
}

// StartPlan seeds a batch. It does not run it: the cases are released as
// simulated time reaches their arrival, so a plan and a run stay separable — the
// plan is reproducible input, the run is what happens to it.
func (s *Sandbox) StartPlan(p Plan) error {
	if len(p.Cases) == 0 {
		return errors.New("playground: a plan needs at least one case")
	}
	if err := p.Arrival.Calendar.validate("the arrival calendar"); err != nil {
		return err
	}
	at, err := s.arrivalTimes(p)
	if err != nil {
		return err
	}
	s.plan = &plan{cases: p.Cases, at: at, mode: p.Arrival.Mode}
	s.caseKeysStale = true
	return nil
}

// Arrivals reports the instants the planned cases start at, for a caller that
// wants to see the stream before running it. It is empty for a sequential plan,
// which has no schedule of its own.
func (s *Sandbox) Arrivals() []time.Time {
	if s.plan == nil {
		return nil
	}
	out := make([]time.Time, 0, len(s.plan.at))
	for _, t := range s.plan.at {
		out = append(out, time.Unix(0, t).UTC())
	}
	return out
}

// arrivalTimes lays the plan out on the clock. Every mode but sequential is
// computed here, up front and from the seed, so the stream is part of the run's
// reproducible input rather than something the run improvises.
func (s *Sandbox) arrivalTimes(p Plan) ([]int64, error) {
	start := s.clock.Now()
	n := len(p.Cases)
	switch p.Arrival.Mode {
	case ArrivalSequential:
		return nil, nil
	case ArrivalAllAtOnce:
		at := make([]int64, n)
		for i := range at {
			at[i] = start
		}
		return at, nil
	case ArrivalEvery:
		if p.Arrival.Interval <= 0 {
			return nil, errors.New("playground: a fixed takt needs a positive interval")
		}
		at := make([]int64, n)
		t := start
		for i := range at {
			var ok bool
			if t, ok = p.Arrival.opensAfter(t); !ok {
				return nil, fmt.Errorf("playground: the arrival calendar does not open within %d days", calendarSearchDays)
			}
			at[i] = t
			t += int64(p.Arrival.Interval)
		}
		return at, nil
	case ArrivalPoisson:
		if p.Arrival.PerHour <= 0 {
			return nil, errors.New("playground: a Poisson stream needs a positive rate")
		}
		mean := float64(time.Hour) / p.Arrival.PerHour
		at := make([]int64, n)
		t := start
		for i := range at {
			var ok bool
			if t, ok = p.Arrival.opensAfter(t); !ok {
				return nil, fmt.Errorf("playground: the arrival calendar does not open within %d days", calendarSearchDays)
			}
			at[i] = t
			// An exponential gap from a uniform draw: -mean * ln(1-u). The draw is
			// seeded on the case's index, so the stream is the same on a re-run.
			u := float64(newDraw(s.seed, uint64(i), 3).below(1<<32)) / float64(1<<32)
			t += int64(-mean * math.Log(1-u))
		}
		return at, nil
	default:
		return nil, fmt.Errorf("playground: unknown arrival mode %d", p.Arrival.Mode)
	}
}

// nextArrival is when the next planned case starts, if one is waiting. A
// sequential plan releases as soon as nothing is in flight, which is why it
// answers "now" rather than a scheduled instant.
func (s *Sandbox) nextArrival() (int64, bool, error) {
	if s.plan == nil || s.plan.next >= len(s.plan.cases) {
		return 0, false, nil
	}
	if s.plan.mode != ArrivalSequential {
		return s.plan.at[s.plan.next], true, nil
	}
	// The maintained per-definition counter, not the scanning one: this is asked
	// on every settle of a sequential run.
	active, err := s.store.DefInstanceCount(s.root.Key)
	if err != nil {
		return 0, false, fmt.Errorf("playground: count active cases: %w", err)
	}
	if active > 0 {
		return 0, false, nil
	}
	return s.clock.Now(), true, nil
}

// releaseArrivals creates every planned case whose arrival has come, and reports
// whether it created any.
func (s *Sandbox) releaseArrivals() (bool, error) {
	created := false
	for {
		at, ok, err := s.nextArrival()
		if err != nil {
			return created, err
		}
		if !ok || at > s.clock.Now() {
			return created, nil
		}
		s.proc.CreateInstance(s.root.Key, s.plan.cases[s.plan.next]...)
		s.plan.next++
		s.caseKeysStale = true
		created = true
		if s.plan.mode == ArrivalSequential {
			// One at a time: the next release waits for this one to finish, which it
			// cannot have done yet.
			return created, nil
		}
	}
}

// caseKeys is every case's instance key in arrival order. Keys are minted from a
// monotonic counter, so ascending key order *is* the order the cases were
// created — no marker variable in the model, and no scan per case.
//
// It is built once per batch of new cases and cached: the list is the only
// per-case thing the sandbox holds, and it is eight bytes each.
func (s *Sandbox) caseKeyList() ([]uint64, error) {
	if !s.caseKeysStale {
		return s.caseKeys, nil
	}
	keys := s.caseKeys[:0]
	collect := func(k uint64, v *model.ProcessInstanceValue) error {
		if v.ProcessDefKey == s.root.Key && v.ParentElementInstanceKey == 0 {
			keys = append(keys, k)
		}
		return nil
	}
	if err := s.store.ActiveProcessInstances(collect); err != nil {
		return nil, fmt.Errorf("playground: scan active cases: %w", err)
	}
	if err := s.store.CompletedProcessInstances(collect); err != nil {
		return nil, fmt.Errorf("playground: scan finished cases: %w", err)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	s.caseKeys, s.caseKeysStale = keys, false
	return s.caseKeys, nil
}

// Report folds the run into one summary. It reads each case's record once and
// keeps none of them, so its cost is linear in the cases and its size is not.
func (s *Sandbox) Report() (Report, error) {
	keys, err := s.caseKeyList()
	if err != nil {
		return Report{}, err
	}
	rep := Report{
		Cases:       len(keys),
		SimStart:    time.Unix(0, s.startedAt).UTC(),
		SimEnd:      s.Now(),
		MaxInFlight: s.maxInFlight,
		Elements:    s.ElementStats(),
		Pools:       s.PoolStats(),
	}
	if rep.Visits, err = s.ElementVisits(); err != nil {
		return Report{}, err
	}

	durations := make([]time.Duration, 0, len(keys))
	for _, k := range keys {
		pi, ok, err := s.store.ProcessInstance(k)
		if err != nil {
			return Report{}, fmt.Errorf("playground: read case %d: %w", k, err)
		}
		if !ok {
			continue
		}
		if pi.State == model.PICompleted {
			rep.Completed++
			durations = append(durations, time.Duration(pi.CompletedAt-pi.CreatedAt))
		}
	}
	if err := s.store.Incidents(func(_ uint64, _ *model.IncidentValue) error {
		rep.Incidents++
		return nil
	}); err != nil {
		return Report{}, fmt.Errorf("playground: count incidents: %w", err)
	}
	rep.Duration = summarise(durations)
	return rep, nil
}

// summarise turns a run's durations into the numbers a report shows. Sorting the
// values is affordable because there is one per finished case and they are eight
// bytes; a streaming digest would trade an exact median for memory this does not
// need.
func summarise(d []time.Duration) Durations {
	if len(d) == 0 {
		return Durations{}
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	var sum time.Duration
	for _, v := range d {
		sum += v
	}
	at := func(q float64) time.Duration {
		i := int(q * float64(len(d)-1))
		return d[i]
	}
	return Durations{
		Count: len(d), Min: d[0], Max: d[len(d)-1],
		P50: at(0.5), P90: at(0.9), Mean: sum / time.Duration(len(d)),
	}
}

// Cases reads one page of the results table, in arrival order, and reports how
// many rows there are in total. Fifty thousand cases are not handed over in one
// response, so this is the only way to read them.
func (s *Sandbox) Cases(offset, limit int) ([]CaseRow, int, error) {
	keys, err := s.caseKeyList()
	if err != nil {
		return nil, 0, err
	}
	total := len(keys)
	if offset < 0 {
		offset = 0
	}
	if offset >= total || limit <= 0 {
		return nil, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}

	incidents, err := s.incidentsByCase()
	if err != nil {
		return nil, 0, err
	}
	rows := make([]CaseRow, 0, end-offset)
	for i := offset; i < end; i++ {
		row, ok, err := s.caseRow(i, keys[i], incidents)
		if err != nil {
			return nil, 0, err
		}
		if ok {
			rows = append(rows, row)
		}
	}
	return rows, total, nil
}

// incidentsByCase counts the parked tokens of each case in one scan, so a page of
// rows does not scan the incidents once per row.
func (s *Sandbox) incidentsByCase() (map[uint64]int, error) {
	out := map[uint64]int{}
	if err := s.store.Incidents(func(_ uint64, v *model.IncidentValue) error {
		out[v.ProcessInstanceKey]++
		return nil
	}); err != nil {
		return nil, fmt.Errorf("playground: scan incidents: %w", err)
	}
	return out, nil
}

// caseRow reads one case into a row.
func (s *Sandbox) caseRow(index int, key uint64, incidents map[uint64]int) (CaseRow, bool, error) {
	pi, ok, err := s.store.ProcessInstance(key)
	if err != nil {
		return CaseRow{}, false, fmt.Errorf("playground: read case %d: %w", key, err)
	}
	if !ok {
		return CaseRow{}, false, nil
	}
	cp := s.byKey[pi.ProcessDefKey]
	row := CaseRow{
		Index: index, InstanceKey: key, State: pi.State,
		Started:   time.Unix(0, pi.CreatedAt).UTC(),
		Incidents: incidents[key],
		Variables: map[string]string{},
	}
	if pi.CompletedAt != 0 {
		row.Ended = time.Unix(0, pi.CompletedAt).UTC()
		row.Duration = time.Duration(pi.CompletedAt - pi.CreatedAt)
	}
	if err := s.store.VariablesOfScope(key, func(v *model.VariableValue) error {
		row.Variables[v.Name] = v.Text
		return nil
	}); err != nil {
		return CaseRow{}, false, fmt.Errorf("playground: read case variables: %w", err)
	}
	if cp != nil {
		last := int32(-1)
		if err := s.store.ElementStepHistory(key, func(_ int64, _ uint64, elementId int32) error {
			last = elementId
			return nil
		}); err != nil {
			return CaseRow{}, false, fmt.Errorf("playground: read case path: %w", err)
		}
		if last >= 0 {
			row.End = cp.ElementBpmnId(last)
		}
	}
	return row, true, nil
}

// Counts is how many cases have been created and how many have finished, read
// from the engine's maintained per-definition counters (ADR-0080). It is O(1),
// which is what makes it safe to ask on every progress poll of a batch of fifty
// thousand.
func (s *Sandbox) Counts() (cases, completed int, err error) {
	active, err := s.store.DefInstanceCount(s.root.Key)
	if err != nil {
		return 0, 0, fmt.Errorf("playground: count active cases: %w", err)
	}
	completed, err = s.store.DefCompletedCount(s.root.Key)
	if err != nil {
		return 0, 0, fmt.Errorf("playground: count finished cases: %w", err)
	}
	return active + completed, completed, nil
}
