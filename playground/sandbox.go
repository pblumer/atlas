package playground

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// PartitionBase is the first partition id reserved for sandboxes. Every key a
// sandbox mints carries a partition at or above it, and the durable engine runs
// well below it (partition 1), so a sandbox key is recognisable on sight and can
// never be mistaken for — or collide with — a real one.
const PartitionBase uint16 = 0xF000

// nextPartition hands out sandbox partitions round-robin within the reserved
// range. Sandboxes hold separate stores, so reuse is harmless; the point of the
// counter is only that concurrent sandboxes on one server look different in a log.
var nextPartition atomic.Uint32

// Options configures a sandbox. Only ModelXML is required.
type Options struct {
	// ModelXML is the BPMN to run: a draft's XML, or a deployed definition's.
	ModelXML []byte
	// Root names the process to instantiate by its BPMN id. Empty picks the sole
	// executable process, and is an error if the model has several.
	Root string
	// BaseDir is where the sandbox's temporary directory is created. Empty uses the
	// host's temp dir.
	BaseDir string
	// StartTime is the instant simulated time starts at. The zero value starts at
	// the Unix epoch, which is legal but reads badly in a report; a caller that
	// shows times to a person should set it.
	StartTime time.Time
	// Seed makes the run reproducible: the same dataset, policy and seed replay the
	// same durations and failures.
	Seed int64
	// Stubs is the answering policy. The zero value parks every job, which is what
	// an environment with no workers does.
	Stubs StubSet
}

// Budget bounds a Run so an abandoned or pathological model cannot spin forever.
// A zero field means "no limit on this axis"; [DefaultBudget] is what a server
// should pass.
type Budget struct {
	// MaxOccurrences caps how many scheduled things a single Run may carry out.
	MaxOccurrences int
	// Horizon caps how far simulated time may travel from where the Run started.
	// A run stops *before* stepping past it, so the horizon is never overshot.
	Horizon time.Duration
	// Stop, when set, is asked before every occurrence whether to give up. It is
	// how a pause reaches a run that is already in flight: the run holds the
	// session's goroutine, so the answer cannot come through the loop. Like the
	// other bounds, stopping this way is not quiescence.
	Stop func() bool
}

// DefaultBudget is a bound generous enough for any single case a person is
// stepping through and small enough that a runaway model gives up quickly.
func DefaultBudget() Budget {
	return Budget{MaxOccurrences: 10_000, Horizon: 365 * 24 * time.Hour}
}

// Progress reports how a Run ended.
type Progress struct {
	// Occurrences is how many scheduled things the run carried out.
	Occurrences int
	// Quiescent is true when the run stopped because nothing was left to do, and
	// false when it stopped on the budget — the difference between "the model is
	// finished" and "we stopped looking".
	Quiescent bool
	// SimTime is where simulated time stands now.
	SimTime time.Time
}

// OccurrenceKind names what a step carried out.
type OccurrenceKind int

const (
	// OccJobCompleted: a stub answered a job the way a worker would.
	OccJobCompleted OccurrenceKind = iota
	// OccJobFailed: a stub failed a job into an incident.
	OccJobFailed
	// OccJobError: a stub threw a BPMN error from a job.
	OccJobError
	// OccTimer: simulated time reached a due timer and it fired.
	OccTimer
	// OccCaseStarted: a planned case reached its arrival time and was created.
	OccCaseStarted
)

// Occurrence is one scheduled thing the scheduler carried out.
type Occurrence struct {
	Kind OccurrenceKind
	// Element is the BPMN element id the occurrence happened on; empty for a timer
	// tick, which may fire several timers that came due at the same instant.
	Element string
	// At is the simulated instant it happened at.
	At time.Time
}

// Task is a job waiting for a person: one that the stub policy does not answer.
type Task struct {
	JobKey      uint64
	InstanceKey uint64
	// Element is the BPMN element id. The name a person reads is not carried here:
	// the Modeler has the diagram, and the compiled graph only knows an element's
	// name where its own detail record happens to keep one.
	Element string
	// Human is true for a user task — modelled human work, as opposed to a job
	// whose worker simply has no stub configured.
	Human bool
}

// CaseResult is what became of one case.
type CaseResult struct {
	InstanceKey uint64
	State       model.ProcessInstanceState
	// Path is the BPMN element ids a token activated, in order.
	Path []string
	// Variables are the case's root-scope variables as text.
	Variables map[string]string
	// Incidents is how many of this case's tokens are parked behind an incident.
	Incidents int
}

// pending is a stub answer the scheduler has committed to but not yet carried
// out: it comes due at a simulated instant, which is what turns a stub duration
// into elapsed time in the report.
type pending struct {
	dueAt   int64
	jobKey  uint64
	element string
	kind    OccurrenceKind
	outputs []model.VariableValue
	message string
	code    string
	// work is how long the answer takes once somebody is working on it. It is what
	// dueAt is derived from — directly for unpooled work, and across the pool's
	// calendar for pooled work.
	work time.Duration
	// pool names the pool this answer waits for a seat in, empty when the element
	// has none. enqueuedAt and startedAt are when it joined the queue and when a
	// seat took it: their difference is the waiting time the report shows beside
	// the work.
	pool                  string
	enqueuedAt, startedAt int64
	// queued says the answer is still waiting for a seat and dueAt means nothing
	// yet. It is a field rather than a zero dueAt because zero is a perfectly good
	// instant: a run that starts at the epoch and stubs a task at no duration
	// produces exactly that, and the two must not be confused.
	queued bool
}

// ElementStat is what a run measured at one element: how often the sandbox
// answered a job there, how long that work took, and how long it waited for a
// seat first. The split is the point — elapsed time that is all queue is a
// capacity problem, and elapsed time that is all work is a different one.
type ElementStat struct {
	Runs    int
	Work    time.Duration
	Wait    time.Duration
	MaxWait time.Duration
}

// PoolStat is what a run measured at one pool.
type PoolStat struct {
	// Served is how many jobs the pool worked on, BusyTime the seat time they took
	// together (nine one-hour cases are nine hours of seat time however many seats
	// shared them), and MaxQueue the longest the queue in front of it ever got.
	Served   int
	BusyTime time.Duration
	MaxQueue int
	// Capacity is the pool's size, and Available the seat time its calendar offered
	// over the run — capacity times the working time, not times the wall clock.
	// Utilisation is BusyTime over Available, and the distinction is the whole
	// point: counting the nights and the weekend as idle capacity reads as "we have
	// room" on a pool with three hundred cases queued.
	Capacity  int
	Available time.Duration
}

// poolState is a pool's live queue and seat count during a run.
type poolState struct {
	cfg      Pool
	queue    []uint64 // job keys waiting for a seat, in arrival order
	busy     int
	stat     PoolStat
	maxQueue int
}

// Sandbox is a complete, throwaway engine: its own partition, log, store and
// processor over a virtual clock. See the package doc for what it guarantees.
//
// It is owned by one goroutine and carries no lock (invariant I3).
type Sandbox struct {
	dir       string
	partition uint16
	seed      int64

	log   *wal.Log
	store *state.Store
	proc  *engine.Processor
	clock *vclock

	root  *compiler.CompiledProcess
	byKey map[uint64]*compiler.CompiledProcess // definition key → compiled process
	stubs StubSet

	// scheduled holds the answer committed to for each job, so a job is drawn for
	// exactly once however often the scheduler looks at it.
	scheduled map[uint64]pending
	// liveJobs is the set of jobs that were activatable at the last settle. Reused
	// between settles rather than reallocated, and the authority on whether a
	// committed answer still has a job to give.
	liveJobs map[uint64]struct{}
	// handedOut is the highest case key StartCase has returned, so the next case
	// can be identified as the newest instance above it.
	handedOut uint64

	// pools is the live state of each configured pool, and elementStats what the
	// run has measured so far. Both are the run's own accounting: the sandbox is
	// the thing that decides when a job is served, so it is the thing that knows
	// how long the job waited first — nothing in the event log records a queue that
	// exists only in the policy.
	pools        map[string]*poolState
	elementStats map[string]*ElementStat

	// plan is the batch still being released, nil for a sandbox somebody is
	// stepping through by hand. caseKeys is the arrival-ordered key list the report
	// and the results pages read, rebuilt when new cases appear.
	plan          *plan
	caseKeys      []uint64
	caseKeysStale bool
	// startedAt is where simulated time began, so a report can say what the run
	// spanned, and maxInFlight the most cases ever active at once.
	startedAt   int64
	maxInFlight int
}

// Open compiles the model and brings up a sandbox for it. The caller must Close
// it; Close removes everything the sandbox wrote.
func Open(opts Options) (*Sandbox, error) {
	if len(opts.ModelXML) == 0 {
		return nil, errors.New("playground: no model to run")
	}
	partition := PartitionBase + uint16(nextPartition.Add(1)%uint32(0xFFFF-uint32(PartitionBase)))

	if err := opts.Stubs.validate(); err != nil {
		return nil, err
	}
	deployables, err := compiler.ParseAll(model.NewKey(partition, 1), 1, bytes.NewReader(opts.ModelXML))
	if err != nil {
		return nil, fmt.Errorf("playground: compile: %w", err)
	}
	root, err := pickRoot(deployables, opts.Root)
	if err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp(opts.BaseDir, "atlas-playground-")
	if err != nil {
		return nil, fmt.Errorf("playground: sandbox dir: %w", err)
	}
	// The sandbox's log is written but never forced to the platter: nothing outside
	// the sandbox observes it and it is deleted when the run ends, so the fsync
	// that "durable before visible" (invariant I2) exists for would buy nothing and
	// costs most of the run. This is the deviation the record calls out, and it is
	// confined to exactly here.
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal"), NoFsync: true})
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("playground: open log: %w", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		_ = log.Close()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("playground: open state: %w", err)
	}

	clock := &vclock{nanos: opts.StartTime.UnixNano()}
	if opts.StartTime.IsZero() {
		clock.nanos = 0
	}
	s := &Sandbox{
		dir: dir, partition: partition, seed: opts.Seed, startedAt: clock.nanos,
		log: log, store: store, clock: clock,
		root:         root,
		byKey:        make(map[uint64]*compiler.CompiledProcess, len(deployables)),
		stubs:        opts.Stubs,
		scheduled:    map[uint64]pending{},
		liveJobs:     map[uint64]struct{}{},
		pools:        map[string]*poolState{},
		elementStats: map[string]*ElementStat{},
	}
	for name, cfg := range opts.Stubs.Pools {
		s.pools[name] = &poolState{cfg: cfg, stat: PoolStat{Capacity: cfg.Capacity}}
	}
	// No connector registry, no vault, no job runner, no HTTP client: a service
	// task in here has nothing that could reach the outside, which is how the
	// sandbox's side-effect freedom is guaranteed rather than configured.
	s.proc = engine.New(partition, log, store, clock)
	// Deploy the root last, so a deployment-bound call activity resolves a child
	// that is already registered.
	for _, d := range deployables {
		s.byKey[d.Process.Key] = d.Process
		if d.Process.Key != root.Key {
			s.proc.Deploy(d.Process)
		}
	}
	s.proc.Deploy(root)
	if err := s.proc.Recover(); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("playground: recover: %w", err)
	}
	return s, nil
}

// pickRoot selects the process to instantiate: the one whose BPMN id matches
// root, or the sole process when root is empty.
func pickRoot(deployables []compiler.Deployable, root string) (*compiler.CompiledProcess, error) {
	if root == "" {
		if len(deployables) == 1 {
			return deployables[0].Process, nil
		}
		ids := make([]string, 0, len(deployables))
		for _, d := range deployables {
			ids = append(ids, d.Process.ProcessId())
		}
		return nil, fmt.Errorf("playground: the model has %d executable processes %v; name the one to run", len(deployables), ids)
	}
	for _, d := range deployables {
		if d.Process.ProcessId() == root {
			return d.Process, nil
		}
	}
	return nil, fmt.Errorf("playground: no process %q in this model", root)
}

// Close shuts the engine down and removes the sandbox's directory. Nothing a
// playground run wrote outlives this call.
func (s *Sandbox) Close() error {
	var errs []error
	if s.store != nil {
		errs = append(errs, s.store.Close())
	}
	if s.log != nil {
		errs = append(errs, s.log.Close())
	}
	errs = append(errs, os.RemoveAll(s.dir))
	return errors.Join(errs...)
}

// Dir is the sandbox's temporary directory. Exported for tests and diagnostics;
// nothing outside the sandbox should write there.
func (s *Sandbox) Dir() string { return s.dir }

// Now is the current simulated instant.
func (s *Sandbox) Now() time.Time { return s.clock.time() }

// ProcessID is the BPMN id of the process this sandbox runs.
func (s *Sandbox) ProcessID() string { return s.root.ProcessId() }

// StartCase creates one instance of the root process, seeded with the given
// variables, and settles the engine. It returns the new instance's key.
//
// The key is found by looking for the newest instance rather than being returned
// by the engine, which does not hand one back: creation is a queued command and
// the key is minted when it is processed. That scan is cheap while a person is
// stepping through a handful of cases; a batch of tens of thousands will want a
// cheaper identity, and that is a problem for the batch stage, not this one.
func (s *Sandbox) StartCase(vars ...model.VariableValue) (uint64, error) {
	s.proc.CreateInstance(s.root.Key, vars...)
	// Settle rather than merely drain: a new case can park on a job immediately,
	// and until the schedule has been reconciled that job is neither answered by
	// the policy nor completable by hand.
	if err := s.settle(); err != nil {
		return 0, fmt.Errorf("playground: start case: %w", err)
	}
	key, err := s.newestCase()
	if err != nil {
		return 0, err
	}
	s.handedOut = key
	s.caseKeysStale = true
	return key, nil
}

// newestCase finds the highest-keyed instance of the root definition above the
// last one handed out. Keys are minted from a monotonic counter, so "newest" is
// simply "highest".
func (s *Sandbox) newestCase() (uint64, error) {
	newest := uint64(0)
	pick := func(k uint64, v *model.ProcessInstanceValue) error {
		if v.ProcessDefKey == s.root.Key && k > s.handedOut && k > newest {
			newest = k
		}
		return nil
	}
	if err := s.store.ActiveProcessInstances(pick); err != nil {
		return 0, fmt.Errorf("playground: scan active instances: %w", err)
	}
	// A case with nothing to wait for finishes inside the same drain, so the
	// completed side has to be looked at too.
	if err := s.store.CompletedProcessInstances(pick); err != nil {
		return 0, fmt.Errorf("playground: scan finished instances: %w", err)
	}
	if newest == 0 {
		return 0, errors.New("playground: no case was created")
	}
	return newest, nil
}

// settle runs the engine until it is idle and then brings the schedule back in
// step with reality. A plan whose next arrival has come is released here, so the
// rest of the scheduler never has to know whether a case came from a person
// pressing "+ Case" or from a dataset.
func (s *Sandbox) settle() error {
	for {
		if err := s.proc.RunUntilIdle(); err != nil {
			return fmt.Errorf("playground: run: %w", err)
		}
		released, err := s.releaseArrivals()
		if err != nil {
			return err
		}
		if !released {
			break
		}
	}
	if err := s.trackInFlight(); err != nil {
		return err
	}
	return s.reconcileJobs()
}

// trackInFlight keeps the high-water mark of cases running at the same time — the
// work in progress a report shows against the arrival rate.
//
// It reads the maintained per-definition counter (ADR-0080), which is O(1). The
// scanning count next to it walks every instance in the store, and this runs
// after every settle: on a batch of fifty thousand that difference was two fifths
// of the whole run.
func (s *Sandbox) trackInFlight() error {
	n, err := s.store.DefInstanceCount(s.root.Key)
	if err != nil {
		return fmt.Errorf("playground: count active cases: %w", err)
	}
	if n > s.maxInFlight {
		s.maxInFlight = n
	}
	return nil
}

// reconcileJobs matches the committed answers against the jobs that actually
// exist: it draws an answer for each new job the policy covers, due at now + the
// drawn duration, and drops any answer whose job has gone away — an interrupting
// boundary event took the token while the answer was still pending.
//
// Dropping is not tidiness. A stale answer keeps its due date in the schedule, and
// the scheduler would dutifully drag simulated time to it for a job that no longer
// exists: a four-hour call cancelled after ten minutes would still report four
// hours. Every duration in the report depends on this staying true.
//
// A job the policy does not cover is left alone: it waits for a person (see
// [Sandbox.OpenTasks]).
func (s *Sandbox) reconcileJobs() error {
	now := s.clock.Now()
	clear(s.liveJobs)
	err := s.store.AllActivatableJobs(func(jobKey uint64) error {
		s.liveJobs[jobKey] = struct{}{}
		if _, ok := s.scheduled[jobKey]; ok {
			return nil
		}
		jv, ok, err := s.store.GetJob(jobKey)
		if err != nil || !ok {
			return err
		}
		element, err := s.elementOf(jv)
		if err != nil || element == "" {
			return err
		}
		stub, ok := s.stubs.forJob(element, jv.JobType == compiler.UserTaskJobTypeIndex)
		if !ok {
			return nil
		}
		// Draw on the key's *counter*, not the whole key: the high bits carry the
		// partition, which a sandbox is handed round-robin at Open. Seeding on the
		// whole key would make the same dataset, policy and seed produce different
		// durations in two sandboxes, which is exactly the reproducibility the seed
		// exists to provide.
		seq := model.CounterOf(jobKey)
		work := stub.duration(newDraw(s.seed, seq, 1))
		p := pending{
			dueAt:      now + int64(work),
			jobKey:     jobKey,
			element:    element,
			kind:       OccJobCompleted,
			outputs:    stub.Outputs,
			work:       work,
			pool:       s.stubs.PoolOf[element],
			enqueuedAt: now,
			startedAt:  now,
		}
		if stub.fails(newDraw(s.seed, seq, 2)) {
			switch {
			case stub.ErrorCode != "":
				p.kind, p.code = OccJobError, stub.ErrorCode
			default:
				p.kind, p.message = OccJobFailed, stub.FailMessage
				if p.message == "" {
					p.message = "simulated failure of " + element
				}
			}
		}
		if ps := s.pools[p.pool]; ps != nil {
			// Pooled work waits for a seat: it has no due date until one takes it, so
			// it goes on the queue rather than into the schedule.
			p.queued = true
			ps.queue = append(ps.queue, jobKey)
		}
		s.scheduled[jobKey] = p
		return nil
	})
	if err != nil {
		return fmt.Errorf("playground: scan jobs: %w", err)
	}
	for jobKey := range s.scheduled {
		if _, live := s.liveJobs[jobKey]; !live {
			s.abandon(jobKey)
			delete(s.scheduled, jobKey)
		}
	}
	return s.serveQueues(now)
}

// abandon drops a job that went away before it was answered out of whatever pool
// it was waiting in or occupying, so an interrupted case does not hold a seat for
// the rest of the run.
func (s *Sandbox) abandon(jobKey uint64) {
	p, ok := s.scheduled[jobKey]
	if !ok || p.pool == "" {
		return
	}
	ps := s.pools[p.pool]
	if ps == nil {
		return
	}
	if !p.queued { // it held a seat
		ps.busy--
		return
	}
	for i, k := range ps.queue {
		if k == jobKey {
			ps.queue = append(ps.queue[:i], ps.queue[i+1:]...)
			return
		}
	}
}

// serveQueues hands waiting work to the free seats of every pool that is open at
// now, in arrival order. A job that starts here is given the due date its work
// reaches across the pool's calendar, so a case begun before closing time carries
// on where it left off when the pool opens again — and keeps its seat overnight,
// as the person working it would.
func (s *Sandbox) serveQueues(now int64) error {
	for name, ps := range s.pools {
		if !ps.cfg.openAt(now) {
			continue
		}
		for ps.busy < ps.cfg.Capacity && len(ps.queue) > 0 {
			jobKey := ps.queue[0]
			ps.queue = ps.queue[1:]
			p, ok := s.scheduled[jobKey]
			if !ok {
				continue // it went away while queued; nothing to serve
			}
			due, ok := ps.cfg.finishAt(now, p.work)
			if !ok {
				return fmt.Errorf("playground: pool %q does not work often enough to finish %s of work within %d days",
					name, p.work, calendarSearchDays)
			}
			p.startedAt, p.dueAt, p.queued = now, due, false
			s.scheduled[jobKey] = p
			ps.busy++
		}
		// The high-water mark is taken after serving, so it counts the work that is
		// actually waiting rather than the work that arrived: a single case at an
		// idle pool is not "a queue of one".
		if n := len(ps.queue); n > ps.stat.MaxQueue {
			ps.stat.MaxQueue = n
		}
	}
	return nil
}

// elementOf resolves the BPMN element id a job belongs to. A job carries its
// element-instance key, not the element index, so the lookup is two hops: job →
// element instance → the compiled process that owns it.
func (s *Sandbox) elementOf(jv *model.JobValue) (string, error) {
	ei, ok, err := s.store.GetElementInstance(jv.ElementInstanceKey)
	if err != nil || !ok {
		return "", err
	}
	pi, ok, err := s.store.ProcessInstance(jv.ProcessInstanceKey)
	if err != nil || !ok {
		return "", err
	}
	cp := s.byKey[pi.ProcessDefKey]
	if cp == nil {
		return "", nil
	}
	return cp.ElementBpmnId(ei.ElementId), nil
}

// nextDue reports the earliest simulated instant at which anything happens: a
// committed stub answer coming due, or a timer. It reports false when the model
// has come to rest.
func (s *Sandbox) nextDue() (int64, bool, error) {
	due, found := int64(0), false
	for _, p := range s.scheduled {
		if p.queued {
			continue // still waiting for a seat; it has no due date yet
		}
		if !found || p.dueAt < due {
			due, found = p.dueAt, true
		}
	}
	// A planned case that has not arrived yet is an instant the run must travel to,
	// exactly like a due answer or a timer.
	if at, ok, err := s.nextArrival(); err != nil {
		return 0, false, err
	} else if ok && (!found || at < due) {
		due, found = at, true
	}
	// Work waiting on a closed pool has no due date either, but the pool's next
	// opening is an instant the run must travel to — otherwise a queue in front of
	// a closed pool reads as quiescence and the run reports itself finished.
	if open, ok := s.nextPoolOpening(); ok && (!found || open < due) {
		due, found = open, true
	}
	t, ok, err := s.nextTimer()
	if err != nil {
		return 0, false, err
	}
	if ok && (!found || t < due) {
		due, found = t, true
	}
	return due, found, nil
}

// nextPoolOpening is the earliest instant a pool with work waiting and a seat to
// spare starts working again.
func (s *Sandbox) nextPoolOpening() (int64, bool) {
	now := s.clock.Now()
	best, found := int64(0), false
	for _, ps := range s.pools {
		if len(ps.queue) == 0 || ps.busy >= ps.cfg.Capacity {
			continue
		}
		at, ok := ps.cfg.opensAfter(now)
		if !ok || at <= now {
			continue // open already: serveQueues will have taken it
		}
		if !found || at < best {
			best, found = at, true
		}
	}
	return best, found
}

// errStopScan ends a store scan early; the timer family is ordered by due date,
// so the first row is the earliest and there is no reason to decode the rest.
var errStopScan = errors.New("stop")

// nextTimer is the due date of the earliest armed timer, if any.
func (s *Sandbox) nextTimer() (int64, bool, error) {
	var due int64
	found := false
	err := s.store.DueTimers(math.MaxInt64, func(_ uint64, v *model.TimerValue) error {
		due, found = v.DueDate, true
		return errStopScan
	})
	if err != nil && !errors.Is(err, errStopScan) {
		return 0, false, fmt.Errorf("playground: scan timers: %w", err)
	}
	return due, found, nil
}

// Step carries out the next scheduled occurrence and settles the engine after it,
// reporting false when nothing was left to do. It is what "step" means in the
// Playground: exactly one thing happens, and simulated time moves to it.
func (s *Sandbox) Step() (Occurrence, bool, error) {
	if err := s.settle(); err != nil {
		return Occurrence{}, false, err
	}
	return s.stepSettled()
}

// stepSettled is Step for a caller that has already settled the engine: it moves
// simulated time to the next scheduled instant and carries out what is due there.
func (s *Sandbox) stepSettled() (Occurrence, bool, error) {
	due, ok, err := s.nextDue()
	if err != nil || !ok {
		return Occurrence{}, false, err
	}
	s.clock.advanceTo(due)
	// The clock may have reached a planned arrival or a pool's opening. Both are
	// things this step carries out; releasing a case is reported as its own
	// occurrence so a stepping author sees the case appear rather than watching
	// the clock jump for no visible reason.
	if released, err := s.releaseArrivals(); err != nil {
		return Occurrence{}, false, err
	} else if released {
		if err := s.settle(); err != nil {
			return Occurrence{}, false, err
		}
		return Occurrence{Kind: OccCaseStarted, At: s.Now()}, true, nil
	}
	if err := s.serveQueues(s.clock.Now()); err != nil {
		return Occurrence{}, false, err
	}

	// A committed answer that came due wins over a timer at the same instant: it
	// was scheduled first, and the engine will see the timer on the next pass
	// anyway. Anything else would make a step depend on map iteration order.
	p, isJob := s.dueJob(due)
	if !isJob {
		if err := s.proc.TickTimers(); err != nil {
			return Occurrence{}, false, fmt.Errorf("playground: fire timers: %w", err)
		}
		if err := s.settle(); err != nil {
			return Occurrence{}, false, err
		}
		return Occurrence{Kind: OccTimer, At: s.Now()}, true, nil
	}

	// The schedule was reconciled against the live jobs by the settle that got us
	// here, so this answer still has a job to give.
	delete(s.scheduled, p.jobKey)
	s.record(p)
	s.answer(p)
	if err := s.settle(); err != nil {
		return Occurrence{}, false, err
	}
	return Occurrence{Kind: p.kind, Element: p.element, At: s.Now()}, true, nil
}

// dueJob picks the committed answer due at or before t, earliest first and lowest
// job key to break a tie, so a step is deterministic when two answers land on the
// same instant.
func (s *Sandbox) dueJob(t int64) (pending, bool) {
	var best pending
	found := false
	for _, p := range s.scheduled {
		if p.queued || p.dueAt > t {
			continue // waiting for a seat, or not due yet
		}
		if !found || p.dueAt < best.dueAt || (p.dueAt == best.dueAt && p.jobKey < best.jobKey) {
			best, found = p, true
		}
	}
	return best, found
}

// record folds a carried-out answer into the run's measurements and frees the
// seat it held, if any.
func (s *Sandbox) record(p pending) {
	st := s.elementStats[p.element]
	if st == nil {
		st = &ElementStat{}
		s.elementStats[p.element] = st
	}
	wait := time.Duration(p.startedAt - p.enqueuedAt)
	st.Runs++
	st.Work += p.work
	st.Wait += wait
	if wait > st.MaxWait {
		st.MaxWait = wait
	}
	if ps := s.pools[p.pool]; ps != nil {
		ps.busy--
		ps.stat.Served++
		ps.stat.BusyTime += p.work
	}
}

// ElementStats reports what the run measured at each element that ran a job.
// It is the bottleneck ranking's raw material; [Sandbox.ElementVisits] is the
// heat map's, and the two count different things — every token that passed
// through, against every job the sandbox answered.
func (s *Sandbox) ElementStats() map[string]ElementStat {
	out := make(map[string]ElementStat, len(s.elementStats))
	for id, st := range s.elementStats {
		out[id] = *st
	}
	return out
}

// PoolStats reports what the run measured at each pool, including the seat time
// its calendar offered over the run so far.
func (s *Sandbox) PoolStats() map[string]PoolStat {
	out := make(map[string]PoolStat, len(s.pools))
	for name, ps := range s.pools {
		st := ps.stat
		st.Available = time.Duration(ps.cfg.Capacity) * ps.cfg.workingTimeBetween(s.startedAt, s.clock.Now())
		out[name] = st
	}
	return out
}

// answer enqueues the committed outcome for a job.
func (s *Sandbox) answer(p pending) {
	switch p.kind {
	case OccJobError:
		s.proc.ThrowJobError(p.jobKey, p.code)
	case OccJobFailed:
		// No retries left: the point of a simulated failure is to reach the
		// incident, not to be quietly retried into success.
		s.proc.FailJob(p.jobKey, 0, p.message, 0)
	default:
		s.proc.CompleteJob(p.jobKey, p.outputs...)
	}
}

// Run carries out scheduled occurrences until the model comes to rest or the
// budget stops it.
func (s *Sandbox) Run(b Budget) (Progress, error) {
	startedAt := s.clock.Now()
	prog := Progress{SimTime: s.Now()}
	// Settle once here rather than at the top of every turn: stepSettled ends with
	// a settle of its own, so a per-turn one would rescan the open jobs with
	// nothing having happened in between — half the scanning in a long batch.
	if err := s.settle(); err != nil {
		return prog, err
	}
	for {
		due, ok, err := s.nextDue()
		if err != nil {
			return prog, err
		}
		if !ok {
			prog.Quiescent = true
			prog.SimTime = s.Now()
			return prog, nil
		}
		if b.Stop != nil && b.Stop() {
			prog.SimTime = s.Now()
			return prog, nil
		}
		if b.MaxOccurrences > 0 && prog.Occurrences >= b.MaxOccurrences {
			prog.SimTime = s.Now()
			return prog, nil
		}
		if b.Horizon > 0 && due > startedAt+int64(b.Horizon) {
			prog.SimTime = s.Now()
			return prog, nil
		}
		if _, stepped, err := s.stepSettled(); err != nil {
			return prog, err
		} else if !stepped {
			prog.Quiescent = true
			prog.SimTime = s.Now()
			return prog, nil
		}
		prog.Occurrences++
	}
}

// Advance moves simulated time forward by d and fires whatever came due, without
// waiting for the scheduler to get there on its own — the "jump the clock" control
// an author uses when stepping through a case by hand.
func (s *Sandbox) Advance(d time.Duration) error {
	if d < 0 {
		return errors.New("playground: cannot move simulated time backwards")
	}
	s.clock.advanceTo(s.clock.Now() + int64(d))
	if err := s.proc.TickTimers(); err != nil {
		return fmt.Errorf("playground: fire timers: %w", err)
	}
	return s.settle()
}

// OpenTasks lists every job waiting for a person: the ones the stub policy does
// not answer. A user task with no Human stub is the everyday case; a service task
// with no stub at all is the other, and both are exactly "nothing is going to
// answer this unless you do".
func (s *Sandbox) OpenTasks() ([]Task, error) {
	var tasks []Task
	err := s.store.AllActivatableJobs(func(jobKey uint64) error {
		if _, scheduled := s.scheduled[jobKey]; scheduled {
			return nil
		}
		jv, ok, err := s.store.GetJob(jobKey)
		if err != nil || !ok {
			return err
		}
		element, err := s.elementOf(jv)
		if err != nil || element == "" {
			return err
		}
		tasks = append(tasks, Task{
			JobKey:      jobKey,
			InstanceKey: jv.ProcessInstanceKey,
			Element:     element,
			Human:       jv.JobType == compiler.UserTaskJobTypeIndex,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("playground: list open tasks: %w", err)
	}
	return tasks, nil
}

// CompleteTask completes a parked job the way the person or worker would have,
// and settles the engine — the author standing in for the human.
func (s *Sandbox) CompleteTask(jobKey uint64, vars ...model.VariableValue) error {
	if _, live := s.liveJobs[jobKey]; !live {
		return fmt.Errorf("playground: job %d is not waiting to be completed", jobKey)
	}
	s.proc.CompleteJob(jobKey, vars...)
	return s.settle()
}

// PublishMessage delivers a message into the sandbox, correlating whatever waits
// for it — the stand-in for the outside world sending one.
func (s *Sandbox) PublishMessage(name, correlationKey string, vars ...model.VariableValue) error {
	if name == "" {
		return errors.New("playground: a message needs a name")
	}
	s.proc.PublishMessage(name, correlationKey, vars...)
	return s.settle()
}

// Case reports what became of one case.
func (s *Sandbox) Case(piKey uint64) (CaseResult, error) {
	pi, ok, err := s.store.ProcessInstance(piKey)
	if err != nil {
		return CaseResult{}, fmt.Errorf("playground: read case: %w", err)
	}
	if !ok {
		return CaseResult{}, fmt.Errorf("playground: no case %d", piKey)
	}
	cp := s.byKey[pi.ProcessDefKey]
	if cp == nil {
		return CaseResult{}, fmt.Errorf("playground: case %d belongs to an unknown definition", piKey)
	}

	res := CaseResult{InstanceKey: piKey, State: pi.State, Variables: map[string]string{}}
	if err := s.store.ElementStepHistory(piKey, func(_ int64, _ uint64, elementId int32) error {
		res.Path = append(res.Path, cp.ElementBpmnId(elementId))
		return nil
	}); err != nil {
		return CaseResult{}, fmt.Errorf("playground: case path: %w", err)
	}
	if err := s.store.VariablesOfScope(piKey, func(v *model.VariableValue) error {
		res.Variables[v.Name] = v.Text
		return nil
	}); err != nil {
		return CaseResult{}, fmt.Errorf("playground: case variables: %w", err)
	}
	if err := s.store.Incidents(func(_ uint64, v *model.IncidentValue) error {
		if v.ProcessInstanceKey == piKey {
			res.Incidents++
		}
		return nil
	}); err != nil {
		return CaseResult{}, fmt.Errorf("playground: case incidents: %w", err)
	}
	return res, nil
}

// InjectUnreadableCase writes an undecodable record under a case's key, so a
// caller in another package can exercise what every read path does when the
// sandbox's own state cannot be decoded — report it, rather than returning a
// report with rows silently missing.
//
// It is a test/tooling affordance only, mirroring the store's own
// InjectCorruptProcessInstance, and nothing in the sandbox's normal operation
// writes a record this way.
func (s *Sandbox) InjectUnreadableCase(piKey uint64) error {
	return s.store.InjectCorruptProcessInstance(piKey)
}

// ElementVisits reports how many tokens have passed through each element of the
// root process, keyed by BPMN element id — the heat map's raw material, read from
// the same maintained counters the runtime overlay uses (ADR-0080).
func (s *Sandbox) ElementVisits() (map[string]int64, error) {
	visits := map[string]int64{}
	err := s.store.ElementVisitTotals(s.root.Key, func(elementId int32, count int64) error {
		if id := s.root.ElementBpmnId(elementId); id != "" {
			visits[id] = count
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("playground: element visits: %w", err)
	}
	return visits, nil
}
