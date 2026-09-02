package api

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/jobtype"
	"github.com/pblumer/atlas/model"
)

// The Workers view (ADR-0157, step 4): who is doing the engine's out-of-process
// work, what they are working on, and how much of it.
//
// Before this an external worker was invisible. An operator saw jobs not moving
// and had no way to tell whether a worker was absent, wedged, or failing — which
// is why the console comes *before* supervision in that ADR's sequence: a restart
// button on something nobody can see the state of is a worse product than a view
// with no buttons at all.
//
// The registry is deliberately **runtime state, not engine state**. It is derived
// from traffic the workers themselves generate, it may be lost on restart without
// harming anything, and it is never written into the durable record or rebuilt by
// applyToState (I4/I6) — the same discipline the mail outbox follows (ADR-0150).
// Counters therefore describe *this run of the server*, which is what the view
// says.
//
// Like the design-time stores, it does no locking of its own: every mutation and
// every read happens on the run-loop goroutine, the single writer (I3).

// workerStat is one worker as the console shows it. Leased is a gauge (work in
// flight right now); the rest are counters since this server started.
type workerStat struct {
	Worker string `json:"worker"`
	// Types counts jobs leased per job type, which is how an operator sees what a
	// worker actually subscribes to rather than what it was configured for.
	Types map[string]int64 `json:"types"`
	// Connectors are the connector configurations this worker reports holding —
	// the providers it can reach. Empty for a worker that serves only plain job
	// types, which need no credential of their own.
	Connectors []string `json:"connectors,omitempty"`
	FirstSeen  int64    `json:"firstSeen"`
	LastSeen   int64    `json:"lastSeen"`
	Leased     int64    `json:"leased"`
	Pulled     int64    `json:"pulled"`
	Completed  int64    `json:"completed"`
	Failed     int64    `json:"failed"`
}

// workerRegistry accumulates what the workers endpoint reports.
type workerRegistry struct {
	byName map[string]*workerStat
	// inFlight counts leases outstanding per job *type*, which is what separates a
	// queue that is draining from one nobody is serving. It is exact rather than
	// inferred because the completion and failure handlers already hold the job, and
	// so know its type.
	inFlight map[string]int64
	// jobs holds each worker's most recent jobs, so the console can answer "which
	// ones" after the counters have answered "how many" (see workerjobs.go).
	jobs map[string]*jobRunRing
	// history, when an operator configured one, receives each settled job so the
	// record outlives this process (see workerhistory.go). nil is the ordinary case.
	history *historyExporter
	now     func() int64
}

// newWorkerRegistry builds an empty registry over a clock, injected so the
// counters are testable without depending on wall time.
func newWorkerRegistry(now func() int64) *workerRegistry {
	if now == nil {
		now = func() int64 { return time.Now().UnixNano() }
	}
	return &workerRegistry{
		byName:   map[string]*workerStat{},
		inFlight: map[string]int64{},
		jobs:     map[string]*jobRunRing{},
		now:      now,
	}
}

// seen returns the worker's record, creating it on first sight. A worker that
// gave no id is recorded under the empty name rather than dropped: it is still
// doing work, and hiding it would make every total lie. The console labels it.
func (r *workerRegistry) seen(worker string) *workerStat {
	st, ok := r.byName[worker]
	if !ok {
		st = &workerStat{Worker: worker, Types: map[string]int64{}, FirstSeen: r.now()}
		r.byName[worker] = st
	}
	st.LastSeen = r.now()
	return st
}

// holdsConnectors records the connector names a worker reports holding. It replaces
// rather than merges: the report is that worker's current configuration, so a
// connector removed from it should stop being counted as served rather than linger
// from an earlier poll.
func (r *workerRegistry) holdsConnectors(worker string, names []string) {
	if worker == "" && len(names) == 0 {
		return
	}
	st := r.seen(worker)
	st.Connectors = append([]string(nil), names...)
	sort.Strings(st.Connectors) // stable order, so the view does not shuffle
}

// leased records that a worker took n jobs of a type.
func (r *workerRegistry) leased(worker, jobType string, n int) {
	if n <= 0 {
		return
	}
	st := r.seen(worker)
	st.Pulled += int64(n)
	st.Leased += int64(n)
	st.Types[jobType] += int64(n)
	r.inFlight[jobType] += int64(n)
}

// completed records a job a worker finished, and takes it out of flight.
func (r *workerRegistry) completed(worker, jobType string) {
	r.settle(worker, jobType, func(st *workerStat) { st.Completed++ })
}

// failed records a job a worker reported as failed, and takes it out of flight.
func (r *workerRegistry) failed(worker, jobType string) {
	r.settle(worker, jobType, func(st *workerStat) { st.Failed++ })
}

// inFlightOf reports how many jobs of a type are leased out right now.
func (r *workerRegistry) inFlightOf(jobType string) int64 { return r.inFlight[jobType] }

// settle bumps an outcome counter and decrements the in-flight gauge, which never
// goes negative: an outcome can arrive for a lease this run never saw — a restart
// lost the count, or an operator finished the job — and a negative gauge would
// make the view nonsense rather than merely incomplete.
func (r *workerRegistry) settle(worker, jobType string, bump func(*workerStat)) {
	st := r.seen(worker)
	bump(st)
	if st.Leased > 0 {
		st.Leased--
	}
	if r.inFlight[jobType] > 0 {
		r.inFlight[jobType]--
	}
}

// list returns every worker, sorted by id so the console does not reshuffle rows
// between polls.
func (r *workerRegistry) list() []workerStat {
	out := make([]workerStat, 0, len(r.byName))
	for _, st := range r.byName {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Worker < out[j].Worker })
	return out
}

// maxTypeScan bounds the per-type queue-depth count. The Workers view is a
// dashboard, not a report: a backlog past this is "at least this many", which is
// the same answer for an operator and costs a bounded scan instead of an
// unbounded one (the discipline ADR-0083 set for the instance summary).
const maxTypeScan = 10000

// jobTypeStat is one job type as the console shows it: how deep its queue is, how
// much of it is out with workers, how much of it has parked on an incident, and
// whether Atlas serves it itself.
type jobTypeStat struct {
	Type  string `json:"type"`
	Index int32  `json:"index"`
	// ServedInProcess is the answer to "why is nothing external picking this up",
	// and the flag that says relocating the kind means unregistering that handler
	// (ADR-0157).
	ServedInProcess bool `json:"servedInProcess"`
	// BuiltIn marks a reserved job type — one of the kinds Atlas ships rather than
	// one a model authored. The view leans on it to keep quiet built-ins out of the
	// way: an engine knows eighteen of them, and listing every idle one buries the
	// two an operator actually deployed.
	BuiltIn bool `json:"builtIn"`
	// Leasable is whether an external worker may pull this type at all. False for a
	// type an in-process worker serves, and false for the user task, which waits for
	// a person — the same two refusals the pull endpoint makes. Without it the view
	// would draw "nobody is serving this" against a type nothing is meant to serve.
	Leasable  bool  `json:"leasable"`
	Parked    int64 `json:"parked"`
	Truncated bool  `json:"truncated,omitempty"`
	InFlight  int64 `json:"inFlight"`
	Incidents int64 `json:"incidents"`
	// Processes names the deployed definitions whose tasks create jobs of this type,
	// newest version of each. It is what makes a row actionable: an engine with fifty
	// job types has fifty rows saying "nobody", and none of them means anything until
	// you can see which process is waiting on it.
	Processes []typeUser `json:"processes"`
}

// typeUser is one definition that uses a job type, with what a link needs.
type typeUser struct {
	ProcessDefKey uint64 `json:"processDefKey"`
	ProcessID     string `json:"processId"`
	Name          string `json:"name"`
	Version       int32  `json:"version"`
}

// handleWorkers is the Workers view (ADR-0157): every job type the engine knows
// with its queue depth, and every worker seen this run with what it is doing.
//
// It is deliberately readable before any worker exists — a type with a growing
// queue and no worker against it is the state an operator most needs to
// recognize, and until now nothing showed it.
//
// The worker counters describe *this run of the server*: the registry is runtime
// state and is not restored on restart, which the view says rather than hides.
func (s *Server) handleWorkers(w http.ResponseWriter, r *http.Request) {
	// Empty rather than nil from the start: both are overwritten below, but a reply
	// that fell through with a nil slice would serialize as null and the console
	// iterates these. Saying it here beats a nil check further down that nothing can
	// reach, since neither list() nor All() ever returns nil.
	var (
		types    = []jobTypeStat{}
		list     = []workerStat{}
		unserved = []unservedConnector{}
		scanErr  error
		reachErr error
	)
	s.do(func() {
		incidents := s.incidentsByJobType()
		users := s.jobTypeUsers()
		for _, e := range s.jobTypes.All() {
			inProcess := s.jobRunner.Handles(e.Index)
			st := jobTypeStat{
				Type:            e.Name,
				Index:           e.Index,
				ServedInProcess: inProcess,
				// The reserved count, not the dynamic floor: a legacy assignment sits
				// between the two and is model-authored, not built in.
				BuiltIn:   e.Index < compiler.ReservedJobTypeCount(),
				Leasable:  !inProcess && e.Index != compiler.UserTaskJobTypeIndex,
				InFlight:  s.workers.inFlightOf(e.Name),
				Incidents: incidents[e.Index],
				Processes: users[e.Index],
			}
			if err := s.store.ActivatableJobs(e.Index, func(uint64) error {
				if st.Parked >= maxTypeScan {
					st.Truncated = true
					return nil
				}
				st.Parked++
				return nil
			}); err != nil {
				scanErr = err
				return
			}
			types = append(types, st)
		}
		list = s.workers.list()
		reachable, err := s.reachableDeployments()
		if err != nil {
			reachErr = err
			return
		}
		unserved = s.unservedConnectors(reachable)
	})
	if scanErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read job queues: "+scanErr.Error())
		return
	}
	if reachErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read which definitions are still live: "+reachErr.Error())
		return
	}
	// Supervised workers are reported separately from the ones seen pulling: an
	// operator can restart and read the logs of a process Atlas launched, and can do
	// neither for one running in someone else's cluster. The view says which is
	// which rather than offering a button that silently does nothing.
	supervised := []childStatus{}
	if s.supervisor != nil {
		supervised = s.supervisor.list()
	}
	// Job types whose stored index a built-in has since taken. Read straight off the
	// registry rather than recomputed: it is what the load actually had to discard,
	// and it does not change while the server runs. The server also warns about it at
	// startup and `atlas check-job-types` answers offline, but neither reaches an
	// operator whose engine runs in a container they would rather not shell into.
	collisions := s.jobTypes.Dropped()
	if collisions == nil {
		collisions = []jobtype.Collision{}
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{
		"workers": list, "types": types, "supervised": supervised,
		"unservedConnectors": unserved, "jobTypeCollisions": collisions,
	})
}

// reachableDeployments returns the definition keys whose tasks can still put a job on
// a queue: the newest version of each process id, every version an instance is still
// running on, and any version an operator has pinned a call activity to (ADR-0105).
//
// It exists because a deploy supersedes its predecessor without removing it — the
// registry is the deploy history, not the current state of the models. Reading
// connector coverage over all of it means a name an author corrected two versions ago
// is reported forever, under the process's *current* name, which reads as an
// accusation against a model that is fine. A superseded version creates no jobs on its
// own: an instance starts on the newest version, and a call activity resolves the
// newest unless it is pinned. The two exceptions are exactly the two listed above.
//
// Deliberately not a static audit of every deployed artifact: an operator can still
// start an old version by key, and the moment they do it has a live instance and is
// reachable again. The card answers "what cannot be served right now", which is the
// question an operator staring at a queue is asking.
//
// Runs on the run-loop goroutine: the deployment registry, one scan of the live
// instances, and the override sidecar.
func (s *Server) reachableDeployments() (map[uint64]bool, error) {
	newest := map[string]uint64{}
	best := map[string]int32{}
	for key, d := range s.deployments {
		if v, seen := best[d.ProcessID]; !seen || d.Version > v {
			best[d.ProcessID], newest[d.ProcessID] = d.Version, key
		}
	}
	out := make(map[uint64]bool, len(newest))
	for _, key := range newest {
		out[key] = true
	}
	if err := s.store.ActiveProcessInstances(func(_ uint64, v *model.ProcessInstanceValue) error {
		out[v.ProcessDefKey] = true
		return nil
	}); err != nil {
		return nil, err
	}
	// A pin makes one exact version the target of every call to that process id, so it
	// is current in the only sense this question cares about. A redirect or a disable
	// does not name a version, and neither can revive a superseded one.
	recs, err := s.callOverrides.LoadAll()
	if err != nil {
		return nil, err
	}
	for _, rec := range recs {
		if rec.Action != overridePin {
			continue
		}
		if d := s.deploymentByProcessIDVersion(rec.CalledProcessID, rec.TargetVersion); d != nil {
			out[d.Key] = true
		}
	}
	return out, nil
}

// unservedConnector is a connector name a deployed model references that nothing on
// this server can currently serve.
type unservedConnector struct {
	Name      string     `json:"name"`
	JobType   string     `json:"jobType"`
	Processes []typeUser `json:"processes"`
}

// unservedConnectors names the connectors deployed models reference that neither the
// engine nor any worker seen pulling can serve.
//
// This exists because moving a credential onto a worker (ADR-0168) took a check away
// from the engine. It used to refuse an unconfigured connector at lease time, since
// it held every credential; once a kind is offloaded it holds none and cannot read a
// worker's environment. So the answer has to be assembled from two halves: the names
// models reference, which only the engine knows, and the names workers hold, which
// only they know and report on each poll.
//
// A kind the engine still serves itself is left out. There the engine's own registry
// is the authority and already refuses at deploy and at lease time (ADR-0163);
// listing it here would say "missing" about a connector that works.
//
// A worker that has never polled reports nothing, so a connector is listed until the
// worker holding it is first seen. That is the honest reading — an operator looking
// at a queue that is not moving wants to know that nothing has claimed it — and it
// resolves itself on the first poll.
//
// Only the definitions in `reachable` are read (see reachableDeployments): the
// registry is the whole deploy history, and a superseded version nothing runs on
// creates no jobs, so it has no connector left to serve.
//
// Runs on the run-loop goroutine, over the in-memory deployment registry.
func (s *Server) unservedConnectors(reachable map[uint64]bool) []unservedConnector {
	held := map[string]bool{}
	for _, st := range s.workers.byName {
		for _, name := range st.Connectors {
			held[name] = true
		}
	}
	// name+jobType -> the processes referencing it, newest version per process id.
	type key struct {
		name    string
		jobType int32
	}
	users := map[key]map[string]typeUser{}
	for defKey, d := range s.deployments {
		if d.cp == nil || !reachable[defKey] {
			continue
		}
		for _, ref := range d.cp.ConnectorRefs() {
			if ref.Connector == "" || held[ref.Connector] {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(ref.Connector), "=") {
				// A connector authored as a FEEL expression (entra, ADR-0172) names no
				// fixed connector to match against what workers hold — the tenant is
				// resolved from the instance's variables at call time. Listing "= tenant"
				// as unserved would be a false alarm; the runtime unresolved-connector
				// incident stands in for it, exactly as the deploy-time preflight does.
				continue
			}
			if s.jobRunner.Handles(ref.JobType) {
				continue // the engine serves this kind itself and holds its credentials
			}
			k := key{ref.Connector, ref.JobType}
			byID, ok := users[k]
			if !ok {
				byID = map[string]typeUser{}
				users[k] = byID
			}
			if prev, seen := byID[d.ProcessID]; seen && prev.Version >= d.Version {
				continue
			}
			byID[d.ProcessID] = typeUser{
				ProcessDefKey: defKey, ProcessID: d.ProcessID, Name: d.Name, Version: d.Version,
			}
		}
	}
	out := make([]unservedConnector, 0, len(users))
	for k, byID := range users {
		procs := make([]typeUser, 0, len(byID))
		for _, u := range byID {
			procs = append(procs, u)
		}
		sort.Slice(procs, func(i, j int) bool { return procs[i].ProcessID < procs[j].ProcessID })
		out = append(out, unservedConnector{
			Name:      k.name,
			JobType:   s.jobTypeName(nil, k.jobType),
			Processes: procs,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].JobType < out[j].JobType
	})
	return out
}

// incidentsByJobType counts unresolved incidents per job-type index, so a stalled
// queue can be told apart from a failing one. An incident names the job it parked,
// which still carries its type; a timer incident has no job and is not counted
// here — it belongs to the Incidents view, not to a worker's queue. Runs on the
// run-loop goroutine.
func (s *Server) incidentsByJobType() map[int32]int64 {
	out := map[int32]int64{}
	_ = s.store.Incidents(func(_ uint64, inc *model.IncidentValue) error {
		if inc.JobKey == 0 {
			return nil
		}
		if jv, ok, err := s.store.GetJob(inc.JobKey); err == nil && ok {
			out[jv.JobType]++
		}
		return nil
	})
	return out
}

// jobTypeUsers maps each job type to the deployed definitions whose service or send
// tasks create jobs of it. Only a model-authored type has users to find: every other
// job-creating element carries a reserved type, and those are the engine's own.
//
// One entry per BPMN process id, at its newest deployed version. An engine keeps
// every version it ever deployed, and listing all of them turns one useful link into
// a column of near-identical ones. Runs on the run-loop goroutine, over the
// in-memory deployment registry — no state reads.
func (s *Server) jobTypeUsers() map[int32][]typeUser {
	// newest[jobType][processId] keeps the highest version seen for that pair.
	newest := map[int32]map[string]typeUser{}
	for key, d := range s.deployments {
		if d.cp == nil {
			continue
		}
		for _, jobType := range jobCreatingTypes(d.cp) {
			byID, ok := newest[jobType]
			if !ok {
				byID = map[string]typeUser{}
				newest[jobType] = byID
			}
			if prev, seen := byID[d.ProcessID]; seen && prev.Version >= d.Version {
				continue
			}
			byID[d.ProcessID] = typeUser{
				ProcessDefKey: key, ProcessID: d.ProcessID, Name: d.Name, Version: d.Version,
			}
		}
	}
	out := make(map[int32][]typeUser, len(newest))
	for jobType, byID := range newest {
		list := make([]typeUser, 0, len(byID))
		for _, u := range byID {
			list = append(list, u)
		}
		// Stable order, so the console does not reshuffle links between polls.
		sort.Slice(list, func(i, j int) bool { return list[i].ProcessID < list[j].ProcessID })
		out[jobType] = list
	}
	return out
}

// jobCreatingTypes is the set of engine-wide job types a compiled process creates jobs
// of — every element that puts one on a queue, not only the ones whose type a model
// wrote out by hand.
//
// It used to be service and send tasks alone, on the reasoning that those are "the only
// elements carrying a job type the model authored" (ADR-0157). That is true of the
// *string* and false of the job: a connector task, a script task, a business rule task
// and a user task each carry a reserved type instead of an authored one, and each
// creates a job under it. So the Workers view's Processes column — the thing that is
// supposed to make a row actionable, since "an engine with fifty job types has fifty
// rows saying nobody, and none of them means anything until you can see which process
// is waiting on it" — was empty for exactly the rows an operator asks about. Every Jira,
// clio, AD, script and DMN row said nothing about who uses it.
//
// A reserved detail's JobType is already the engine-wide index and needs no resolution:
// the reserved range is engine-wide by construction, which is the whole reason it is
// reserved. Only a service task's authored type is interned per process, which is why
// that one alone goes through GlobalJobType.
func jobCreatingTypes(cp *compiler.CompiledProcess) []int32 {
	var out []int32
	seen := map[int32]bool{}
	add := func(jobType int32) {
		if !seen[jobType] {
			seen[jobType] = true
			out = append(out, jobType)
		}
	}
	for id := range int32(cp.NodeCount()) {
		n := cp.Node(id)
		switch n.Type {
		case compiler.TypeServiceTask, compiler.TypeSendTask:
			add(cp.ServiceTask(n.Detail).GlobalJobType())
		case compiler.TypeConnectorTask:
			add(cp.ConnectorTask(n.Detail).JobType)
		case compiler.TypeScriptJobTask:
			add(cp.ScriptJobTask(n.Detail).JobType)
		case compiler.TypeBusinessRuleTask:
			add(cp.BusinessRuleTask(n.Detail).JobType)
		case compiler.TypeUserTask:
			add(cp.UserTask(n.Detail).JobType)
		}
	}
	return out
}

// handleRestartWorker cycles one supervised worker. It names a worker the operator
// already configured on the command line — the request cannot introduce one, or
// name a command — so the whole of its authority is "turn that one off and on".
//
// The restart goes through the supervisor's ordinary run loop, so a button press
// and a crash take the identical path: there is no second way for a worker to come
// up that could drift from the first.
func (s *Server) handleRestartWorker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.supervisor == nil {
		httpapi.Error(w, http.StatusConflict,
			"this server supervises no workers; a worker running elsewhere is restarted where it runs")
		return
	}
	if err := s.supervisor.restart(id); err != nil {
		httpapi.Error(w, http.StatusNotFound, err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"id": id, "restarting": true})
}
