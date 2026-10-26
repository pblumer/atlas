package api

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// maxStatisticsInstances bounds how many instances one statistics read folds. The walk
// is newest first, so a bound that bites drops the oldest part of the window, and the
// answer says so (Truncated, Scanned) instead of passing a part off as the whole
// (ADR-0496).
const maxStatisticsInstances = 10000

// statsSummary is one measured time — of an element instance, of a task's waiting or
// working, of a whole instance — over every case the window holds, in milliseconds.
//
// The median is what the overlay shades by, because the times a process produces are
// skewed: a task opened on a Friday evening waits over a weekend, and a mean lets that
// one case speak for all of them. The total is beside it because mean × count is the
// load a capacity plan needs, and the mean alone hides how many cases it was.
type statsSummary struct {
	Count       int   `json:"count"`
	MeanMillis  int64 `json:"meanMillis"`
	P50Millis   int64 `json:"p50Millis"`
	P90Millis   int64 `json:"p90Millis"`
	MaxMillis   int64 `json:"maxMillis"`
	TotalMillis int64 `json:"totalMillis"`
}

// statsElement is one element of the model over the window. Every element is listed,
// at zero when no token arrived: "never reached" is half of what a heatmap answers, and
// an element missing from the answer cannot say it.
type statsElement struct {
	ElementID  string        `json:"elementId"`
	Type       string        `json:"type"`
	Runs       int           `json:"runs"`       // tokens that arrived in the window
	Completed  int           `json:"completed"`  // of the element instances that ended in it, how many completed
	Terminated int           `json:"terminated"` // and how many were cancelled
	Duration   *statsSummary `json:"duration,omitempty"`
	// Waiting and Working split a task's duration at the moment somebody took it up —
	// a claim, a Worker's lease — for the instances whose last take-up before
	// completion was not handed back. A task completed without anybody taking it has a
	// duration and no split.
	Waiting *statsSummary `json:"waiting,omitempty"`
	Working *statsSummary `json:"working,omitempty"`
}

// statsFlow is one sequence flow of the model over the window: how many tokens arrived
// at its target over it, under the rule the engine's counter uses — a branch of an
// event-based gateway counts when it wins its race.
type statsFlow struct {
	FlowID string `json:"flowId"`
	Runs   int    `json:"runs"`
}

// statsInstances is the definition's instances over the window. Running is now, from the
// maintained counter, because "running at the end of the window" is a question nothing
// records the answer to.
type statsInstances struct {
	Started  int           `json:"started"`
	Finished int           `json:"finished"`
	Running  int           `json:"running"`
	Duration *statsSummary `json:"duration,omitempty"`
}

// processStatsResp is GET /api/v1/processes/{key}/statistics.
type processStatsResp struct {
	From      int64          `json:"from"` // unix nanoseconds, inclusive
	To        int64          `json:"to"`   // unix nanoseconds, exclusive
	Instances statsInstances `json:"instances"`
	Elements  []statsElement `json:"elements"`
	Flows     []statsFlow    `json:"flows"`
	// Scanned is how many instances the fold read, and Truncated whether the bound
	// stopped it before the window was exhausted.
	Scanned   int  `json:"scanned"`
	Truncated bool `json:"truncated"`
}

// handleProcessStatistics answers how one definition's elements and flows were used over
// a window, and where the time went (ADR-0496).
//
// ?from= and ?to= are RFC 3339 moments or positive unix nanoseconds, like the history
// purge's cut-off; from defaults to the beginning of what is retained, to to now. The
// fold runs off the loop on a snapshot: its cost is the window's instances, and holding
// the single writer for that is how one operator's question becomes everybody's stall
// (ADR-0239).
func (s *Server) handleProcessStatistics(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	q := r.URL.Query()
	from, err := statsInstant("from", q.Get("from"), 0)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	now := s.now()
	to, err := statsInstant("to", q.Get("to"), now)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if to <= from {
		httpapi.Error(w, http.StatusBadRequest, "to must be after from")
		return
	}
	// A window that ends now is one window for as long as its result is kept, whatever
	// second each request named it in; one that names its end is its own.
	cacheKey := statsResultKey{def: key, from: from, to: to}
	if strings.TrimSpace(q.Get("to")) == "" {
		cacheKey.to = 0
	}
	caches := s.statsCachesFor()
	resp, found, err := caches.results.get(cacheKey, now, func() (processStatsResp, bool, error) {
		var (
			found bool
			resp  processStatsResp
		)
		err := s.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
			d, ok := defs[key]
			if !ok || d.cp == nil {
				return nil
			}
			found = true
			var err error
			resp, err = foldProcessStatistics(rv, key, d.cp, from, to, maxStatisticsInstances, caches.instances)
			return err
		})
		return resp, found, err
	})
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read statistics: "+err.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
	default:
		httpapi.JSON(w, http.StatusOK, resp)
	}
}

// statsInstant reads one end of the window: empty is the default, otherwise an RFC 3339
// moment or positive unix nanoseconds.
func statsInstant(name, raw string, def int64) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil && t.UnixNano() > 0 {
		return t.UnixNano(), nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s=%q is neither an RFC 3339 moment nor positive unix nanoseconds", name, raw)
	}
	return n, nil
}

// errStatsWindowEnd stops the walk of the completion index at the window's start: the
// index is newest first, so everything after it finished earlier still.
var errStatsWindowEnd = errors.New("statistics window exhausted")

// instanceHistoryReader is what a fold over one instance's history reads. A *state.Store
// and a *state.ReadView both answer it, so the overlay's single-instance branch on the
// loop and the statistics fold on a snapshot share one reading.
type instanceHistoryReader interface {
	ElementReplayHistory(piKey uint64, fn func(ts int64, pos uint64, v state.ElementReplayValue) error) error
	OperatorActionHistory(piKey uint64, fn func(ts int64, pos uint64, v *model.OperatorActionValue) error) error
	JobTakeHistory(piKey uint64, fn func(ts int64, pos uint64, v state.JobTakeValue) error) error
}

// statsFold accumulates one definition's window, instance by instance.
type statsFold struct {
	elements []statsAcc // by node index
	flows    []int      // by flow index
	started  int
	finished int
	lifetime []int64
}

type statsAcc struct {
	runs, completed, terminated int
	duration, waiting, working  []int64
}

// instanceContribution is what one instance adds to a window: for each element it
// touched, the tokens that arrived, completed and were cancelled and the times measured,
// and for each flow the tokens that took it. first and last are the earliest and latest
// fact the fold read — what says whether a contribution folded over the whole trail is
// also the contribution to a window: it is when the window holds both.
type instanceContribution struct {
	elements    []elementContribution
	flows       []flowContribution
	first, last int64
}

type elementContribution struct {
	idx int32
	statsAcc
}

type flowContribution struct {
	idx  int32
	runs int
}

// add folds one instance's contribution into the window. The times are copied, never
// shared: summarize sorts what it is handed, and a cached contribution is read by every
// fold after this one.
func (f *statsFold) add(c *instanceContribution) {
	for i := range c.elements {
		e := &c.elements[i]
		a := &f.elements[e.idx]
		a.runs += e.runs
		a.completed += e.completed
		a.terminated += e.terminated
		a.duration = append(a.duration, e.duration...)
		a.waiting = append(a.waiting, e.waiting...)
		a.working = append(a.working, e.working...)
	}
	for _, fl := range c.flows {
		f.flows[fl.idx] += fl.runs
	}
}

// takeAt is one take-up or hand-back of a task, read back for the split.
type takeAt struct {
	ts   int64
	kind byte
}

// foldProcessStatistics folds the instances of one definition that were running or
// finished within [from, to), newest first and at most limit of them. cache, when not
// nil, supplies and keeps the contributions of finished instances.
func foldProcessStatistics(rv *state.ReadView, key uint64, cp *compiler.CompiledProcess, from, to int64, limit int, cache *instanceStatsCache) (processStatsResp, error) {
	f := &statsFold{
		elements: make([]statsAcc, cp.NodeCount()),
		flows:    make([]int, cp.FlowCount()),
	}
	resp := processStatsResp{From: from, To: to}

	visit := func(piKey uint64, v *model.ProcessInstanceValue) error {
		if v.CreatedAt >= to {
			return nil // started after the window: nothing of it is inside
		}
		if resp.Scanned >= limit {
			resp.Truncated = true
			return errListTruncated
		}
		resp.Scanned++
		if v.CreatedAt >= from {
			f.started++
		}
		if v.CompletedAt != 0 && v.CompletedAt >= from && v.CompletedAt < to {
			f.finished++
			if v.CompletedAt > v.CreatedAt {
				f.lifetime = append(f.lifetime, v.CompletedAt-v.CreatedAt)
			}
		}
		c, err := contributionOf(rv, cache, piKey, key, cp, v, from, to)
		if err != nil {
			return err
		}
		f.add(c)
		return nil
	}
	err := rv.ActiveInstancesOfDefDesc(key, 0, visit)
	if err == nil {
		err = rv.FinishedInstancesOfDefDesc(key, 0, 0, func(piKey uint64, v *model.ProcessInstanceValue) error {
			if v.CompletedAt < from {
				return errStatsWindowEnd
			}
			return visit(piKey, v)
		})
	}
	if err != nil && !errors.Is(err, errListTruncated) && !errors.Is(err, errStatsWindowEnd) {
		return processStatsResp{}, err
	}
	running, err := rv.DefInstanceCount(key)
	if err != nil {
		return processStatsResp{}, err
	}

	resp.Instances = statsInstances{Started: f.started, Finished: f.finished, Running: running, Duration: summarize(f.lifetime)}
	resp.Elements = make([]statsElement, 0, len(f.elements))
	for i := range f.elements {
		id := cp.ElementBpmnId(int32(i))
		if id == "" {
			continue
		}
		a := &f.elements[i]
		resp.Elements = append(resp.Elements, statsElement{
			ElementID: id, Type: cp.Node(int32(i)).Type.String(),
			Runs: a.runs, Completed: a.completed, Terminated: a.terminated,
			Duration: summarize(a.duration), Waiting: summarize(a.waiting), Working: summarize(a.working),
		})
	}
	resp.Flows = make([]statsFlow, 0, len(f.flows))
	for i, n := range f.flows {
		if id := cp.FlowBpmnId(int32(i)); id != "" {
			resp.Flows = append(resp.Flows, statsFlow{FlowID: id, Runs: n})
		}
	}
	return resp, nil
}

// contributionOf is one instance's contribution to [from, to).
//
// A finished instance's history no longer changes, so its contribution over the whole
// trail is folded once and kept; it is the contribution to every window that holds the
// whole trail, which is nearly every finished instance a recent window reads. Only an
// instance still running, or one the window cuts, is folded for this window alone — the
// difference between re-reading ten thousand histories on every request and re-reading
// the handful that can have changed.
func contributionOf(rv instanceHistoryReader, cache *instanceStatsCache, piKey, key uint64, cp *compiler.CompiledProcess, v *model.ProcessInstanceValue, from, to int64) (*instanceContribution, error) {
	if cache != nil && v.CompletedAt != 0 && v.CreatedAt >= from && v.CompletedAt < to {
		k := instanceStatsKey{def: key, pi: piKey}
		c, ok := cache.get(k)
		if !ok {
			var err error
			if c, err = foldInstance(rv, piKey, key, cp, math.MinInt64, math.MaxInt64); err != nil {
				return nil, err
			}
			cache.put(k, c)
		}
		if c.first >= from && c.last < to {
			return c, nil
		}
	}
	return foldInstance(rv, piKey, key, cp, from, to)
}

// foldInstance reads one instance's trail and take-up history into its contribution to
// [from, to).
func foldInstance(rv instanceHistoryReader, piKey, key uint64, cp *compiler.CompiledProcess, from, to int64) (*instanceContribution, error) {
	takes := map[uint64][]takeAt{}
	if err := rv.JobTakeHistory(piKey, func(ts int64, _ uint64, v state.JobTakeValue) error {
		takes[v.ElementInstanceKey] = append(takes[v.ElementInstanceKey], takeAt{ts, v.Kind})
		return nil
	}); err != nil {
		return nil, err
	}
	c := &instanceContribution{first: math.MaxInt64, last: math.MinInt64}
	at := map[int32]int{}    // element index → its place in c.elements
	flows := map[int32]int{} // flow index → tokens that took it
	element := func(idx int32) *statsAcc {
		i, ok := at[idx]
		if !ok {
			i = len(c.elements)
			at[idx] = i
			c.elements = append(c.elements, elementContribution{idx: idx})
		}
		return &c.elements[i].statsAcc
	}
	activated := map[uint64]int64{} // element instance key → when it activated
	err := foldInstanceTrail(rv, piKey, key, cp, func(ts int64, v state.ElementReplayValue, flow int32) {
		c.first, c.last = min(c.first, ts), max(c.last, ts)
		in := ts >= from && ts < to
		if in && flow >= 0 {
			flows[flow]++
		}
		switch v.Action {
		case state.ReplayActivated:
			activated[v.ElementInstanceKey] = ts
			if in {
				element(v.ElementID).runs++
			}
		case state.ReplayCompleted, state.ReplayTerminated:
			start, ok := activated[v.ElementInstanceKey]
			delete(activated, v.ElementInstanceKey)
			if !in {
				return
			}
			a := element(v.ElementID)
			if v.Action == state.ReplayTerminated {
				a.terminated++
				return
			}
			a.completed++
			if !ok {
				return // activated before the trail began, or under another version
			}
			a.duration = append(a.duration, ts-start)
			if took, ok := lastTake(takes[v.ElementInstanceKey], ts); ok && took >= start {
				a.waiting = append(a.waiting, took-start)
				a.working = append(a.working, ts-took)
			}
		}
	})
	if err != nil {
		return nil, err
	}
	for idx, n := range flows {
		c.flows = append(c.flows, flowContribution{idx: idx, runs: n})
	}
	return c, nil
}

// lastTake is when the task was last taken up at or before it completed, and false when
// nobody took it or the last thing that happened was handing it back — a task an
// operator completed after it was unclaimed has no working time to report.
func lastTake(takes []takeAt, completed int64) (int64, bool) {
	var (
		last  takeAt
		found bool
	)
	for _, t := range takes {
		if t.ts > completed {
			break
		}
		last, found = t, true
	}
	if !found || last.kind != state.JobTaken {
		return 0, false
	}
	return last.ts, true
}

// foldInstanceTrail walks one instance's lifecycle trail and hands fn every fact that
// belongs to the definition key, with the sequence flow it counts for — or -1.
//
// The flow follows the engine's counter (applyToState): an activation over a flow counts
// for it, except a catch armed by an event-based gateway, whose flow counts when it
// completes — the race decided in its favour. Here the compiled graph says which flows
// leave such a gateway, so the rule is exact.
//
// An instance migrated onto this definition (ADR-0162) recorded its earlier facts
// against another version's graph, where the same index means another element; those
// facts are passed over rather than misread.
func foldInstanceTrail(rv instanceHistoryReader, piKey, key uint64, cp *compiler.CompiledProcess, fn func(ts int64, v state.ElementReplayValue, flow int32)) error {
	var migrations []migrationBoundary
	if err := rv.OperatorActionHistory(piKey, func(_ int64, pos uint64, v *model.OperatorActionValue) error {
		if v.Kind == model.OperatorActionMigrate {
			migrations = append(migrations, migrationBoundary{pos: pos, from: v.FromProcessDefKey})
		}
		return nil
	}); err != nil {
		return err
	}
	ver := newVersionAt(migrations, key, func(def uint64) *compiler.CompiledProcess {
		if def == key {
			return cp
		}
		return nil
	})
	return rv.ElementReplayHistory(piKey, func(ts int64, pos uint64, v state.ElementReplayValue) error {
		if ver.def(pos) != key || v.ElementID < 0 || int(v.ElementID) >= cp.NodeCount() {
			return nil
		}
		flow := int32(-1)
		if v.SourceFlowID >= 0 && int(v.SourceFlowID) < cp.FlowCount() {
			armed := cp.Node(cp.Flow(v.SourceFlowID).Source).Type == compiler.TypeEventBasedGateway
			if (v.Action == state.ReplayActivated && !armed) || (v.Action == state.ReplayCompleted && armed) {
				flow = v.SourceFlowID
			}
		}
		fn(ts, v, flow)
		return nil
	})
}

// instanceFlowVisits is one instance's arrows for the runtime overlay, folded from its own
// trail under the counter's rule. It runs on a snapshot, off the loop: the trail is
// bounded by the instance, but an instance that loops has a long one, and the overlay
// asks for it every 1.5 seconds for as long as somebody has it selected.
func instanceFlowVisits(rv instanceHistoryReader, piKey, key uint64, cp *compiler.CompiledProcess) ([]runtimeFlow, error) {
	visits := make([]int, cp.FlowCount())
	if err := foldInstanceTrail(rv, piKey, key, cp, func(_ int64, _ state.ElementReplayValue, flow int32) {
		if flow >= 0 {
			visits[flow]++
		}
	}); err != nil {
		return nil, err
	}
	out := []runtimeFlow{}
	for i, n := range visits {
		if id := cp.FlowBpmnId(int32(i)); id != "" && n > 0 {
			out = append(out, runtimeFlow{FlowID: id, Visits: n})
		}
	}
	return out, nil
}

// summarize reduces measured nanoseconds to a statsSummary in milliseconds, or nil when
// nothing was measured — "no case" is not "zero seconds". The percentiles are nearest
// rank, so each is a time one real case took.
func summarize(ns []int64) *statsSummary {
	if len(ns) == 0 {
		return nil
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i] < ns[j] })
	var total int64
	for _, d := range ns {
		total += d
	}
	rank := func(p float64) int64 {
		return ns[max(0, int(math.Ceil(p*float64(len(ns))))-1)]
	}
	const ms = int64(time.Millisecond)
	return &statsSummary{
		Count:       len(ns),
		MeanMillis:  total / int64(len(ns)) / ms,
		P50Millis:   rank(0.5) / ms,
		P90Millis:   rank(0.9) / ms,
		MaxMillis:   ns[len(ns)-1] / ms,
		TotalMillis: total / ms,
	}
}
