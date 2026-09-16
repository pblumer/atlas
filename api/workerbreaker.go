package api

import (
	"sort"
	"time"
)

// The worker circuit breaker (ADR-0340): an outage stops at the worker, not at every
// token.
//
// A worker whose target stops answering does not fail once. It fails once *per instance
// that reaches its task*, and each of those failures spends a retry and eventually parks
// a token behind its own incident (ADR-0061). ADR-0337 made that pile readable and
// clearable; it does not stop it forming. This does: while a target is judged down, its
// jobs are simply not handed out. A held job has burned no retry, raised no incident and
// been written nowhere — its token waits at the task exactly as it waits for a worker
// that has not polled yet, and the backlog drains by itself when the target returns.
//
// Like the worker registry beside it, this is **runtime state, not engine state**. It is
// derived from traffic the workers generate, it may be lost on restart without harming
// anything, and it is never written into the durable record or rebuilt by applyToState
// (I4/I6): whether a host is reachable right now is not a fact about a process, and a
// recovered engine must not resurrect a stale opinion about a host. It does no locking,
// because every touch happens on the run-loop goroutine, the single writer (I3).
//
// # What it costs when nothing is wrong
//
// Nothing. A breaker keyed by (job type, Worker) lets the dispatch path ask
// [workerBreakers.holdingFor] once per *job type* — one lookup in a map keyed by the
// interned job-type index, empty in the steady state — and only if that says yes does
// anything resolve a job to its Worker. Entries exist only for targets that are currently
// misbehaving, and a target that recovers is forgotten entirely, so the map's size is the
// size of the problem rather than the size of the estate (I1).
//
// The index rather than the name is what both dispatch sites already hold — `Runner.Claim`
// ranges its factories by index, and the external pull interns the requested name before
// it scans — so the steady-state question is answered without hashing a string on the hot
// path. A Worker's name is a string, but nothing touches it until a breaker is open.
//
// That is why no job record carries a Worker. The record originally decided one, on the
// premise that the gate must attribute every candidate on every dispatch; it does not,
// and ADR-0340's amendment of 2026-09-15 drops the field. Resolving a job's Worker is a
// read of the compiled process, which this gate — unlike applyToState — is free to make;
// `incidentConnectorLookup` already does exactly that, derivationally, for the incident
// listing.

// The breaker's tuning. These are the defaults ADR-0340 leaves open for a per-Worker
// override later; they are constants rather than configuration until something needs to
// set them, so the tests exercise the numbers that actually ship.
const (
	// breakerThreshold is how many *distinct process instances* must fail in a row
	// before a target is judged down. Distinctness is what separates an outage from a
	// data fault: one instance with a bad record fails its whole retry budget against a
	// perfectly healthy host, and stopping the integration over it would punish every
	// other instance for one bad record. A dead host fails instances that have nothing
	// to do with each other, which no data fault does.
	breakerThreshold = 3

	// breakerCooldown is the wait before the first probe, and breakerMaxCooldown the
	// ceiling the doubling stops at — short enough that a blip costs seconds, capped so
	// a host that was down for an hour is not ignored for one after it returns.
	breakerCooldown    = 10 * time.Second
	breakerMaxCooldown = 5 * time.Minute

	// breakerStreakWindow bounds a streak in time as well as in outcome. Three failures
	// a week apart are three ordinary faults; without a window they would accumulate
	// forever and eventually trip a breaker on nothing. It is generous on purpose: a
	// low-volume process may take minutes to send three instances into the same task,
	// and an outage that slow is still an outage.
	breakerStreakWindow = 10 * time.Minute

	// breakerProbeTimeout is how long a probe may be outstanding before it is written
	// off, so that a worker which takes the probe and never reports — it crashed, or the
	// lease simply expired — cannot wedge a target half open forever. A lost probe is not
	// a failure: nothing was learned, so it does not grow the cooldown.
	//
	// It is the default job lease (job.DefaultLease and defaultJobLease are both five
	// minutes), which is the point at which the probe's own job becomes activatable
	// again — so the next probe is normally the same call rather than a second one. A
	// worker that asked for a *longer* lease can have a second probe admitted beside the
	// first, which is two calls into a recovering host instead of one. That is the
	// deliberate direction of the error: erring toward trying costs a call, erring toward
	// waiting wedges an integration that has already recovered.
	breakerProbeTimeout = 5 * time.Minute
)

// breakerKey names the thing a breaker is about: one target, in the ADR-0203 sense of a
// Worker — the configured identity a task refers to (`connector="Patrick Blumer"`) under
// the reserved job type its kind compiles to.
//
// Not the job type alone: every mail task in the estate shares the one reserved mail job
// type, so a job-type breaker would let one dead SMTP host stop mail for every process on
// the server. Not the worker *instance* either: several worker processes pulling the same
// type all reach the same host, so the state belongs to the thing they share. A task that
// names no worker carries an empty connector, and its job type is its target — that is
// all this engine can see of it.
type breakerKey struct {
	jobType   int32
	connector string
}

type breakerState uint8

const (
	breakerClosed   breakerState = iota // jobs go out; a failure streak may be building
	breakerOpen                         // jobs are held; the next probe goes at probeAt
	breakerHalfOpen                     // one probe is out; its verdict decides
)

func (s breakerState) String() string {
	switch s {
	case breakerOpen:
		return "open"
	case breakerHalfOpen:
		return "probing"
	default:
		return "closed"
	}
}

// breakerEntry is one target's state. Entries exist only while a target is misbehaving:
// a success at any point deletes one, so an absent key means "closed, nothing pending",
// which is the answer for almost every key almost always.
type breakerEntry struct {
	state breakerState
	// instances holds the distinct process instances in the current failure streak. It
	// never exceeds the threshold, because reaching it trips the breaker and clears it.
	instances map[uint64]struct{}
	lastFail  int64 // when the streak last grew, for breakerStreakWindow
	trippedAt int64
	reason    string        // the failure that tripped it, for the operator
	cooldown  time.Duration // the current wait; doubles on each failed probe, capped
	probeAt   int64         // when the next probe may go (open)
	probeJob  uint64        // the job admitted as the probe (half-open)
	probeBy   int64         // when that probe is written off (half-open)
	// refused counts dispatch attempts turned away since the trip. It is refusals, not
	// distinct jobs: the same job is refused again on every round it is scanned in.
	refused int64
}

// breakerCounts are one target's totals for this run of the server: how often it was
// judged down, how often the engine tried it again, and how many dispatch attempts were
// turned away meanwhile.
type breakerCounts struct{ trips, probes, refused int64 }

// workerBreakers is the collection, owned by the run loop.
type workerBreakers struct {
	byKey map[breakerKey]*breakerEntry
	// holding counts the entries per job *type* that are currently open or probing.
	// It is the whole reason the gate is free when nothing is wrong: the dispatch path
	// asks this once per type and, finding nothing, never resolves a job to a Worker.
	holding map[int32]int
	// stats is what a scrape reads, and it is deliberately NOT cleared when a target
	// recovers. byKey forgets a recovered target entirely — which is what keeps it the
	// size of the problem rather than of the estate — but a counter that disappears and
	// comes back at zero is a counter Prometheus reads as a reset, so the totals live
	// apart from the state and outlive it. Its size is bounded by the Workers the
	// deployed models name, which is a property of the estate and not of the traffic.
	stats map[breakerKey]*breakerCounts
	// tracked counts *every* entry per job type, held or merely mid-streak. The
	// reporting side needs the wider question: a completion has to be able to break a
	// streak that is not holding anything yet, and asking this first is what keeps a
	// job completing normally from resolving its Worker for no reason.
	tracked map[int32]int
	now     func() int64
	// onChange is told about every transition between held and not held. Work that
	// silently does not happen is the one failure mode an operator cannot diagnose, so
	// a breaker is never allowed to trip in silence — and the breaker itself holds only
	// an interned job-type index, so saying anything legible about it is the caller's
	// job. nil means nobody is listening, which is only true in a test.
	onChange func(k breakerKey, state breakerState, reason string, cooldown time.Duration)
}

// newWorkerBreakers builds an empty collection over a clock, injected so the transitions
// are testable without depending on wall time.
func newWorkerBreakers(now func() int64) *workerBreakers {
	if now == nil {
		now = func() int64 { return time.Now().UnixNano() }
	}
	return &workerBreakers{
		byKey:   map[breakerKey]*breakerEntry{},
		holding: map[int32]int{},
		tracked: map[int32]int{},
		stats:   map[breakerKey]*breakerCounts{},
		now:     now,
	}
}

// holdingFor reports whether anything under this job type is currently held. It is the
// dispatch path's first question, asked once per type per round: when it is false — the
// steady state — no candidate needs to be attributed to anything.
func (b *workerBreakers) holdingFor(jobType int32) bool { return b.holding[jobType] > 0 }

// tracking reports whether this job type has any breaker state at all — a target
// currently held, or one part-way through a failure streak. It is the reporting
// path's first question: a completion on a type nothing is wrong with has nothing to
// tell the breaker, and must not pay a read to discover that.
func (b *workerBreakers) tracking(jobType int32) bool { return b.tracked[jobType] > 0 }

// allow answers whether this job may be handed out. A refusal changes nothing about the
// job: it stays activatable, unleased and untouched.
//
// An open breaker whose cooldown has elapsed admits exactly **one** job, as the probe —
// a recovering host handed its whole backlog the moment it answers is a host that goes
// down again. Exactly-one needs no coordination because every caller is on the single
// writer.
//
// It is therefore not a pure predicate, and a caller must ask only about a job it will
// actually hand out: the "yes" it gives an open breaker *is* the probe slot, and a caller
// that asks speculatively and then drops the job spends that slot on a call nobody makes,
// wedging the target until the probe is written off.
func (b *workerBreakers) allow(k breakerKey, jobKey uint64) bool {
	e := b.byKey[k]
	if e == nil || e.state == breakerClosed {
		return true
	}
	now := b.now()
	switch e.state {
	case breakerOpen:
		if now < e.probeAt {
			e.refused++
			b.count(k).refused++
			return false
		}
		e.state = breakerHalfOpen
	case breakerHalfOpen:
		// A probe that never reported. Its job's lease has expired by now, so the next
		// probe is the same call rather than a second one in flight.
		if now < e.probeBy {
			e.refused++
			b.count(k).refused++
			return false
		}
	}
	e.probeJob = jobKey
	e.probeBy = now + int64(breakerProbeTimeout)
	b.count(k).probes++
	return true
}

// failed reports a job of this target failing, for the instance it belonged to. Only the
// two things that carry information reach the state machine: a failure while the breaker
// is closed, which grows the streak, and the probe's own failure, which re-opens with a
// longer cooldown.
//
// Everything else is old news. A job leased *before* the trip can report long after it —
// a lease runs for minutes, the first cooldown for seconds — and letting that re-open a
// breaker would double a cooldown over a call that was already in flight when the target
// was last known to be down.
func (b *workerBreakers) failed(k breakerKey, jobKey, instanceKey uint64, message string) {
	now := b.now()
	e := b.byKey[k]
	if e == nil {
		e = &breakerEntry{instances: map[uint64]struct{}{}}
		b.byKey[k] = e
		b.tracked[k.jobType]++
	}
	switch e.state {
	case breakerHalfOpen:
		if jobKey == e.probeJob {
			b.trip(k, e, message, now)
		}
	case breakerOpen:
		// In flight before the trip; the breaker already knows.
	default:
		if len(e.instances) > 0 && now-e.lastFail > int64(breakerStreakWindow) {
			clear(e.instances) // too far apart to be one outage
		}
		e.lastFail = now
		e.instances[instanceKey] = struct{}{}
		if len(e.instances) >= breakerThreshold {
			b.trip(k, e, message, now)
		}
	}
}

// succeeded reports a job of this target completing, and closes the breaker if one was
// open — whichever job it was.
//
// That is a deliberate asymmetry with [workerBreakers.failed], which ignores a completion's
// mirror image. Holding healthy work back is the expensive error, because avoiding it is
// what the breaker is for; a wrong close costs at most one more trip, which is three
// failures and no incidents. So any evidence that the target answers is acted on, while
// stale evidence that it does not is not. It is why this takes no job key: there is no
// outcome here that the breaker would read one for.
func (b *workerBreakers) succeeded(k breakerKey) {
	if e := b.byKey[k]; e != nil {
		b.forget(k, e)
	}
}

// closeNow is the operator's override — someone who has fixed the endpoint does not wait
// out a cooldown. It reports whether there was in fact something being held, so the
// caller can say so rather than guess.
func (b *workerBreakers) closeNow(k breakerKey) bool {
	e := b.byKey[k]
	if e == nil || e.state == breakerClosed {
		return false
	}
	b.forget(k, e)
	return true
}

// trip opens the breaker. The cooldown starts at the base and doubles on each *failed
// probe* up to the cap; it starts over at the base whenever a target recovers, because
// forgetting the entry is what lets a host that was down an hour and healthy since not be
// treated as a repeat offender.
func (b *workerBreakers) trip(k breakerKey, e *breakerEntry, reason string, now int64) {
	switch {
	case e.cooldown == 0:
		e.cooldown = breakerCooldown
	case e.state == breakerHalfOpen:
		e.cooldown = min(2*e.cooldown, breakerMaxCooldown)
	}
	if e.state == breakerClosed {
		b.holding[k.jobType]++
	}
	b.count(k).trips++
	e.state = breakerOpen
	e.trippedAt = now
	e.reason = reason
	e.probeAt = now + int64(e.cooldown)
	e.probeJob = 0
	clear(e.instances)
	if b.onChange != nil {
		// Every trip, not only the first: a re-opened breaker with a longer cooldown is
		// how an operator learns the target is still down, and how long the next
		// attempt is away.
		b.onChange(k, breakerOpen, reason, e.cooldown)
	}
}

// forget closes a breaker and drops it, so a recovered target costs exactly what one that
// never failed costs — including its map entry.
func (b *workerBreakers) forget(k breakerKey, e *breakerEntry) {
	held := e.state != breakerClosed
	if held {
		if b.holding[k.jobType]--; b.holding[k.jobType] <= 0 {
			delete(b.holding, k.jobType)
		}
	}
	if b.tracked[k.jobType]--; b.tracked[k.jobType] <= 0 {
		delete(b.tracked, k.jobType)
	}
	delete(b.byKey, k)
	if b.onChange != nil && held {
		b.onChange(k, breakerClosed, "", 0)
	}
}

// count returns this target's totals, creating them on first sight.
func (b *workerBreakers) count(k breakerKey) *breakerCounts {
	c, ok := b.stats[k]
	if !ok {
		c = &breakerCounts{}
		b.stats[k] = c
	}
	return c
}

// breakerView is one held target as an operator sees it. Work that silently does not
// happen is the one failure mode nobody can diagnose — a flood at least says something is
// wrong — so a breaker carries its own account: which target, since when, on what, when
// the next probe goes, and how much it has turned away.
type breakerView struct {
	JobType   string `json:"jobType"`
	TypeIndex int32  `json:"-"`
	Connector string `json:"connector,omitempty"`
	State     string `json:"state"`
	TrippedAt int64  `json:"trippedAt"`
	Reason    string `json:"reason,omitempty"`
	ProbeAt   int64  `json:"probeAt"`
	Cooldown  int64  `json:"cooldown"`
	Refused   int64  `json:"refused"`
}

// openBreakers lists what is being held, sorted so the console does not reshuffle rows
// between polls. A target in the middle of a failure streak has not tripped and does not
// appear: the question this answers is "what is not going out", not "what has ever
// failed".
//
// nameOf turns an interned job-type index back into the name a person deployed, because
// the index is this engine's bookkeeping and means nothing to the operator reading the
// row. A nil resolver, or one that does not know an index, leaves the name empty rather
// than printing a number.
func (b *workerBreakers) openBreakers(nameOf func(int32) (string, bool)) []breakerView {
	out := make([]breakerView, 0, len(b.byKey))
	for k, e := range b.byKey {
		if e.state == breakerClosed {
			continue
		}
		name := ""
		if nameOf != nil {
			name, _ = nameOf(k.jobType)
		}
		out = append(out, breakerView{
			JobType:   name,
			TypeIndex: k.jobType,
			Connector: k.connector,
			State:     e.state.String(),
			TrippedAt: e.trippedAt,
			Reason:    e.reason,
			ProbeAt:   e.probeAt,
			Cooldown:  int64(e.cooldown),
			Refused:   e.refused,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		switch {
		case out[i].JobType != out[j].JobType:
			return out[i].JobType < out[j].JobType
		case out[i].TypeIndex != out[j].TypeIndex:
			return out[i].TypeIndex < out[j].TypeIndex // two types the resolver could not name
		default:
			return out[i].Connector < out[j].Connector
		}
	})
	return out
}
