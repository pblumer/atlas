package playground

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/playground"
)

// Scenario is a saved run: everything needed to run it again and get the same
// answer, plus what it must show for that answer to count as a pass.
//
// It is literally the three requests that make a run — open a session, start a
// batch, judge the report — kept as the bodies those endpoints already take. The
// alternative was a parallel set of structs describing a stub policy, an arrival
// profile and a set of expectations a second time; this cannot drift from the
// endpoints, because it *is* them. A client that can run a scenario is a client
// that can replay three requests, which is what the CI runner does.
//
// The design-time store that holds these keeps them opaque for the same reason a
// form's schema is opaque to it: storage has no business understanding a stub
// policy, and a second copy of these shapes would be a second place to keep in
// step.
type Scenario struct {
	// Open is the body of POST /playground/sessions: the model source, the seed,
	// and the stub and pool policy.
	Open json.RawMessage `json:"open"`
	// Run is the body of POST /playground/sessions/{id}/runs: the dataset and the
	// arrival profile.
	Run json.RawMessage `json:"run"`
	// Expect is the body of POST /playground/sessions/{id}/verdict: what the run
	// has to show. Omitted means the scenario runs but judges nothing.
	Expect json.RawMessage `json:"expect,omitempty"`
}

// Validate reports what is wrong with a scenario's shape. It is a syntactic check
// only — that each part is a JSON object the matching endpoint could be handed —
// because the endpoints are the authority on their own bodies and re-deciding
// here is how the two answers start disagreeing.
func (s Scenario) Validate() error {
	for _, part := range []struct {
		name string
		raw  json.RawMessage
		must bool
	}{
		{"open", s.Open, true},
		{"run", s.Run, true},
		{"expect", s.Expect, false},
	} {
		if len(part.raw) == 0 {
			if part.must {
				return fmt.Errorf("a scenario needs its %q request", part.name)
			}
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(part.raw, &obj); err != nil {
			return fmt.Errorf("the %q request is not a JSON object: %w", part.name, err)
		}
	}
	return nil
}

// expectReq is a set of expectations on the wire. Durations travel as
// milliseconds like everywhere else in this API, and the two fields where zero is
// itself a target keep the shape that lets them say it: a pointer, and a map.
type expectReq struct {
	MinCompleted int              `json:"minCompleted,omitempty"`
	MaxIncidents *int             `json:"maxIncidents,omitempty"`
	MaxP50Millis int64            `json:"maxP50Millis,omitempty"`
	MaxP90Millis int64            `json:"maxP90Millis,omitempty"`
	MaxMillis    int64            `json:"maxMillis,omitempty"`
	MinVisits    map[string]int64 `json:"minVisits,omitempty"`
	MaxVisits    map[string]int64 `json:"maxVisits,omitempty"`
	MaxQueue     map[string]int   `json:"maxQueue,omitempty"`
	// Rules are the expectations stated per case rather than per run.
	Rules []ruleReq `json:"rules,omitempty"`
}

// ruleReq is one per-case rule: which cases it speaks about, and what they have
// to show. Both halves are FEEL over the case's variables as the run left them,
// with "end" bound to the BPMN id of the last element it reached and
// "durationSeconds" to how long it took.
type ruleReq struct {
	Name string `json:"name,omitempty"`
	// When selects the cases. Omitted means every case.
	When string `json:"when,omitempty"`
	Then string `json:"then"`
}

// millis turns a wire duration into a Go one. Zero stays zero, which every
// expectation reads as "not checked".
func millis(v int64) time.Duration { return time.Duration(v) * time.Millisecond }

// reportFrom rebuilds the parts of a report a comparison measures, from the shape
// the report endpoint hands out. Only those parts: a baseline is read to be set
// beside a fresh run, not to be shown as a report of its own, and rebuilding
// fields nothing compares would be inventing a round trip this API does not
// promise.
func reportFrom(r reportResp) playground.Report {
	rep := playground.Report{
		Cases: r.Cases, Completed: r.Completed, Incidents: r.Incidents,
		MaxInFlight: r.MaxInFlight,
		Elements:    make(map[string]playground.ElementStat, len(r.Elements)),
		Pools:       make(map[string]playground.PoolStat, len(r.Pools)),
	}
	rep.Duration = playground.Durations{
		Count: r.Duration.Count, Min: millis(r.Duration.MinMillis),
		P50: millis(r.Duration.P50Millis), P90: millis(r.Duration.P90Millis),
		Max: millis(r.Duration.MaxMillis), Mean: millis(r.Duration.MeanMillis),
	}
	for id, el := range r.Elements {
		rep.Elements[id] = playground.ElementStat{
			Runs: el.Runs, Work: millis(el.WorkMillis),
			Wait: millis(el.WaitMillis), MaxWait: millis(el.MaxWaitMillis),
		}
	}
	for name, p := range r.Pools {
		rep.Pools[name] = playground.PoolStat{
			Capacity: p.Capacity, Served: p.Served, MaxQueue: p.MaxQueue,
			BusyTime: millis(p.BusyMillis), Available: millis(p.AvailableMillis),
		}
	}
	return rep
}

func (e expectReq) toExpectations() playground.Expectations {
	return playground.Expectations{
		MinCompleted: e.MinCompleted,
		MaxIncidents: e.MaxIncidents,
		MaxP50:       millis(e.MaxP50Millis),
		MaxP90:       millis(e.MaxP90Millis),
		MaxDuration:  millis(e.MaxMillis),
		MinVisits:    e.MinVisits,
		MaxVisits:    e.MaxVisits,
		MaxQueue:     e.MaxQueue,
		Rules:        rulesFrom(e.Rules),
	}
}

// rulesFrom converts the wire rules. Nothing is validated here: a rule is refused
// where it is compiled, which is the one place that knows what FEEL accepts.
func rulesFrom(in []ruleReq) []playground.Rule {
	if len(in) == 0 {
		return nil
	}
	out := make([]playground.Rule, 0, len(in))
	for _, r := range in {
		out = append(out, playground.Rule{Name: r.Name, When: r.When, Then: r.Then})
	}
	return out
}

// verdictResp is a run judged, in the shape a person reads and a CI job exits on.
type verdictResp struct {
	Passed bool        `json:"passed"`
	Checks []checkResp `json:"checks"`
	// Rules carries what each per-case rule did, beside the check that summarises
	// it: the counts a panel shows as a breakdown, and the offending cases it marks
	// in the results table.
	Rules []ruleResp `json:"rules"`
}

type ruleResp struct {
	Name string `json:"name"`
	When string `json:"when,omitempty"`
	Then string `json:"then"`
	// Cases is how many the run had, Matched how many the rule spoke about, and the
	// three after that split the matched ones.
	Cases     int  `json:"cases"`
	Matched   int  `json:"matched"`
	Satisfied int  `json:"satisfied"`
	Violated  int  `json:"violated"`
	Undecided int  `json:"undecided"`
	Passed    bool `json:"passed"`
	// Examples are the offending cases by their position in the dataset, bounded;
	// Truncated says the run had more than are listed.
	Examples  []int `json:"examples"`
	Truncated bool  `json:"truncated"`
}

type checkResp struct {
	Name   string `json:"name"`
	Want   string `json:"want"`
	Got    string `json:"got"`
	Passed bool   `json:"passed"`
	// Rule marks a check that came from a per-case rule. It decides the verdict like
	// any other; a client that also shows the rule breakdown uses this to avoid
	// saying the same thing twice, in a column too narrow to say it once.
	Rule bool `json:"rule,omitempty"`
}

// HandleVerdict judges the run against the expectations in the body.
//
// It is a POST rather than a field of the report because the expectations belong
// to the scenario, not to the session: the same run can be judged against a
// stricter target without being run again, which is what makes a scenario
// editable after the fact.
func (s *Service) HandleVerdict(w http.ResponseWriter, r *http.Request) {
	var req expectReq
	if !decode(w, r, maxBodyBytes, &req) {
		return
	}
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	exp := req.toExpectations()
	var (
		out     verdictResp
		ruleErr error
	)
	// Not through the run helper: a rule that will not compile is the caller's
	// mistake, and that helper answers 200 with whatever the closure left behind. The
	// error is carried out of the session and answered as a 400 of its own.
	if !s.ok(w, sess.With(func(sb *playground.Sandbox) error {
		rep, err := sb.Report()
		if err != nil {
			return err
		}
		outcomes, rerr := sb.JudgeRules(exp.Rules)
		if rerr != nil {
			ruleErr = rerr
			return nil
		}
		out = renderVerdict(exp.Judge(rep, outcomes), outcomes)
		return nil
	})) {
		return
	}
	if ruleErr != nil {
		httpapi.Error(w, http.StatusBadRequest, ruleErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

func renderVerdict(v playground.Verdict, rules []playground.RuleOutcome) verdictResp {
	out := verdictResp{
		Passed: v.Passed,
		Checks: make([]checkResp, 0, len(v.Checks)),
		Rules:  make([]ruleResp, 0, len(rules)),
	}
	for _, c := range v.Checks {
		out.Checks = append(out.Checks, checkResp{
			Name: c.Name, Want: c.Want, Got: c.Got, Passed: c.Passed, Rule: c.Rule,
		})
	}
	for _, o := range rules {
		r := ruleResp{
			Name: o.Rule.Label(), When: o.Rule.When, Then: o.Rule.Then,
			Cases: o.Cases, Matched: o.Matched, Satisfied: o.Satisfied,
			Violated: o.Violated, Undecided: o.Undecided,
			Passed: o.Passed(), Truncated: o.Truncated,
			Examples: o.Examples,
		}
		if r.Examples == nil {
			r.Examples = []int{}
		}
		out.Rules = append(out.Rules, r)
	}
	return out
}

// comparisonResp is one run set beside another.
type comparisonResp struct {
	Deltas []deltaResp `json:"deltas"`
}

// deltaResp carries the raw numbers and which way is good, rather than rendered
// text: the client formats them the way the rest of its screen does, and a CI log
// and a panel can then disagree about presentation without disagreeing about the
// measurement.
type deltaResp struct {
	Name   string `json:"name"`
	Unit   string `json:"unit"`
	Before int64  `json:"before"`
	After  int64  `json:"after"`
	Better bool   `json:"better"`
	Worse  bool   `json:"worse"`
}

// HandleCompare sets this run beside a report sent in the body — a stored
// baseline, normally.
//
// The baseline travels in the request rather than being looked up here on
// purpose: this service holds no design-time state (ADR-0147), and a session that
// could read the scenario store would be the first thing to break that. The
// client already holds both halves.
func (s *Service) HandleCompare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Baseline reportResp `json:"baseline"`
	}
	// A report carries a sixty-slice timeline, so the baseline is the one body here
	// that is not small.
	if !decode(w, r, maxModelBytes, &req) {
		return
	}
	before := reportFrom(req.Baseline)
	var out comparisonResp
	s.run(w, r, func(sb *playground.Sandbox) error {
		after, err := sb.Report()
		if err != nil {
			return err
		}
		out = renderComparison(playground.Compare(before, after))
		return nil
	}, &out)
}

func renderComparison(c playground.Comparison) comparisonResp {
	out := comparisonResp{Deltas: make([]deltaResp, 0, len(c.Deltas))}
	for _, d := range c.Deltas {
		out.Deltas = append(out.Deltas, deltaResp{
			Name: d.Name, Unit: unitName(d.Unit), Before: d.Before, After: d.After,
			Better: d.Better(), Worse: d.Worse(),
		})
	}
	return out
}

func unitName(u playground.Unit) string {
	switch u {
	case playground.UnitDuration:
		return "millis"
	case playground.UnitPercent:
		return "percent"
	default:
		return "count"
	}
}
