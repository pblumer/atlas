package playground

import (
	"fmt"
	"time"

	"github.com/pblumer/atlas/model"
)

// Stub is how the sandbox answers one job — the stand-in for the worker,
// connector or person that would answer it in production.
//
// It is deliberately the same vocabulary as the mockup service task (ADR-0120):
// a duration band, an optional result, an optional failure. The difference is
// where it lives. A mockup task is *in the model*, so testing with one means
// shipping something else than what was tested; a Stub is run configuration
// against an untouched model.
type Stub struct {
	// Min and Max bound the simulated time the work takes. The draw between them
	// is deterministic in the run's seed and the job's key, so a re-run with the
	// same seed produces the same durations. Max below Min is treated as Min.
	Min, Max time.Duration
	// Outputs are the variables the answer writes, as a worker's completion would.
	Outputs []model.VariableValue
	// FailPerMillion is the failure probability in parts per million (0 never
	// fails, 1_000_000 always). The draw is deterministic like the duration.
	FailPerMillion int32
	// FailMessage is the incident message a simulated failure raises. Empty uses a
	// message naming the element, so an incident is never anonymous.
	FailMessage string
	// ErrorCode, when set, makes a simulated failure throw a BPMN error with this
	// code — caught by a matching boundary or event subprocess — instead of raising
	// an incident. That is how a *business* error path is exercised, not just a
	// technical one.
	ErrorCode string
}

// duration is the simulated work time this stub takes for the given draw.
func (s Stub) duration(d draw) time.Duration {
	if s.Max <= s.Min {
		return s.Min
	}
	span := int64(s.Max - s.Min)
	return s.Min + time.Duration(d.below(uint64(span)+1))
}

// fails reports whether this answer is a failure rather than a completion.
func (s Stub) fails(d draw) bool {
	if s.FailPerMillion <= 0 {
		return false
	}
	if s.FailPerMillion >= 1_000_000 {
		return true
	}
	return d.below(1_000_000) < uint64(s.FailPerMillion)
}

// StubSet is the whole answering policy for a run: what answers a job, and what
// is left for a person.
//
// A job that no entry covers **parks**. That is the deliberate default: a sandbox
// with no policy at all behaves like an environment with no workers, which is
// exactly what it is, rather than silently completing tasks nobody configured.
type StubSet struct {
	// Default answers every non-human job that ByElement does not name. Nil parks
	// them.
	Default *Stub
	// Human answers user tasks. Nil — the default — parks them for the person at
	// the keyboard: that is the interactive half of the Playground. A batch run
	// sets it, so 50 000 cases do not wait for anybody.
	Human *Stub
	// ByElement answers one BPMN element by id, ahead of Default and Human alike.
	ByElement map[string]Stub
	// Pools are the sets of workers elements compete for, by name. An element with
	// no pool is worked on the instant it arrives, however many cases are already
	// in flight — which is the right model for a machine and the wrong one for a
	// person.
	Pools map[string]Pool
	// PoolOf assigns a BPMN element to a pool by name. Naming a pool that does not
	// exist is refused at [Open]: a silently ignored assignment would show up only
	// as a waiting time that never appears.
	PoolOf map[string]string
}

// validate reports what is wrong with the policy before a run starts on it.
func (ss StubSet) validate() error {
	for name, p := range ss.Pools {
		if p.Capacity < 1 {
			return fmt.Errorf("playground: pool %q has no capacity; a pool with no seats never works", name)
		}
		for _, w := range p.Open {
			if w.To <= w.From {
				return fmt.Errorf("playground: pool %q has a window that ends before it starts (%s–%s)", name, w.From, w.To)
			}
		}
	}
	for element, name := range ss.PoolOf {
		if _, ok := ss.Pools[name]; !ok {
			return fmt.Errorf("playground: element %q is assigned to pool %q, which is not configured", element, name)
		}
	}
	return nil
}

// DefaultStubs is the policy a Playground session starts with: machine work is
// answered quickly, people are not answered at all. It is what makes "open the
// tab and press play" work on a model whose integrations do not exist yet.
func DefaultStubs() StubSet {
	return StubSet{Default: &Stub{Min: 200 * time.Millisecond, Max: 2 * time.Second}}
}

// forJob resolves the stub that answers a job on the given element, reporting
// false when nothing does — in which case the job waits for a person.
func (ss StubSet) forJob(element string, human bool) (Stub, bool) {
	if s, ok := ss.ByElement[element]; ok {
		return s, true
	}
	if human {
		if ss.Human == nil {
			return Stub{}, false
		}
		return *ss.Human, true
	}
	if ss.Default == nil {
		return Stub{}, false
	}
	return *ss.Default, true
}

// draw is a deterministic pseudo-random draw for one job.
//
// It is derived from the run's seed and the job's key — both frozen: the seed is
// part of the run's stated input, and the key was minted by the engine and written
// into an event, from a counter that starts at the same place in every sandbox. Re-running the same dataset with the same seed therefore replays
// the same durations and the same failures, which is what lets a run be quoted in
// a review or used as a regression check. Nothing here reads a global random
// source.
type draw struct{ state uint64 }

// newDraw mixes the run's seed with a job's key *counter* (see the note where it
// is called: the partition half of a key is an allocation detail and would break
// reproducibility) and a salt that separates the duration draw from the failure
// draw for the same job.
func newDraw(seed int64, jobSeq uint64, salt uint64) draw {
	// splitmix64 over the three inputs: cheap, well-distributed, and — unlike a
	// hash of a formatted string — allocation-free.
	x := uint64(seed)*0x9E3779B97F4A7C15 ^ jobSeq ^ salt*0xD1B54A32D192ED03
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return draw{state: x}
}

// below returns a value in [0, n), or 0 for n == 0.
func (d draw) below(n uint64) uint64 {
	if n == 0 {
		return 0
	}
	return d.state % n
}

// Window is a stretch of a day a pool works in, as offsets from midnight UTC.
// 08:00–17:00 is {From: 8 * time.Hour, To: 17 * time.Hour}.
type Window struct{ From, To time.Duration }

// Pool is a set of interchangeable workers — three clerks, two reviewers, one
// approver — that the elements assigned to it compete for.
//
// A pool is what turns a stub duration into a waiting time. Without one, every
// case is worked on the instant it arrives and the report's "waiting" column is
// zero by construction, which makes a bottleneck ranking a restatement of the
// durations somebody typed in. With one, "do three clerks suffice for 200
// applications a day" becomes a question the run answers.
type Pool struct {
	// Capacity is how many cases the pool works on at once. It must be at least 1.
	Capacity int
	// Open are the windows in a day the pool works. Empty means always: a queue in
	// a pool with no calendar never waits for an opening.
	Open []Window
	// Days selects the weekdays the pool works, indexed by time.Weekday. The zero
	// value — no day selected — means every day, so a caller that does not care
	// about weekends does not have to say so.
	Days [7]bool
}

// alwaysOpen reports whether the pool has no calendar at all.
func (p Pool) alwaysOpen() bool { return len(p.Open) == 0 && p.Days == [7]bool{} }

// worksOn reports whether the pool works on a weekday.
func (p Pool) worksOn(d time.Weekday) bool {
	if p.Days == [7]bool{} {
		return true
	}
	return p.Days[int(d)]
}

// openAt reports whether the pool is working at instant t (unix nanoseconds).
func (p Pool) openAt(t int64) bool {
	if p.alwaysOpen() {
		return true
	}
	ts := time.Unix(0, t).UTC()
	if !p.worksOn(ts.Weekday()) {
		return false
	}
	if len(p.Open) == 0 {
		return true // a day filter with no hours: the whole of a working day
	}
	off := time.Duration(ts.Hour())*time.Hour + time.Duration(ts.Minute())*time.Minute +
		time.Duration(ts.Second())*time.Second + time.Duration(ts.Nanosecond())
	for _, w := range p.Open {
		if off >= w.From && off < w.To {
			return true
		}
	}
	return false
}

// opensAfter is the next instant at or after t the pool is working, and false if
// it never works again within the search horizon.
//
// It steps day by day rather than solving for the answer: a calendar is at most a
// handful of windows, the horizon is bounded, and a loop that anyone can read is
// worth more here than arithmetic nobody will check.
func (p Pool) opensAfter(t int64) (int64, bool) {
	if p.alwaysOpen() {
		return t, true
	}
	ts := time.Unix(0, t).UTC()
	for day := 0; day <= calendarSearchDays; day++ {
		d := ts.AddDate(0, 0, day)
		if !p.worksOn(d.Weekday()) {
			continue
		}
		midnight := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
		if len(p.Open) == 0 {
			if start := midnight.UnixNano(); start >= t {
				return start, true
			}
			return t, true // already inside a working day with no hour windows
		}
		best, found := int64(0), false
		for _, w := range p.Open {
			start := midnight.Add(w.From).UnixNano()
			end := midnight.Add(w.To).UnixNano()
			if end <= t {
				continue // this window is behind us
			}
			if start < t {
				start = t // we are inside it already
			}
			if !found || start < best {
				best, found = start, true
			}
		}
		if found {
			return best, true
		}
	}
	return 0, false
}

// calendarSearchDays bounds how far opensAfter looks for the next working moment.
// A pool that does not work within a fortnight is a mistake in the policy, not a
// schedule, and the run says so rather than searching for ever.
const calendarSearchDays = 14

// finishAt is when work of length d, started at t, is done — counting only the
// time the pool is actually working, so a case started before closing time carries
// on where it left off when the pool opens again.
func (p Pool) finishAt(t int64, d time.Duration) (int64, bool) {
	if p.alwaysOpen() {
		return t + int64(d), true
	}
	remaining := int64(d)
	at := t
	for guard := 0; guard <= calendarSearchDays*len(p.Open)+calendarSearchDays+1; guard++ {
		if remaining <= 0 {
			return at, true
		}
		open, ok := p.opensAfter(at)
		if !ok {
			return 0, false
		}
		at = open
		closes, ok := p.closesAfter(at)
		if !ok {
			return 0, false
		}
		if avail := closes - at; avail >= remaining {
			return at + remaining, true
		} else {
			remaining -= avail
			at = closes
		}
	}
	return 0, false
}

// closesAfter is the end of the working stretch that contains t.
func (p Pool) closesAfter(t int64) (int64, bool) {
	ts := time.Unix(0, t).UTC()
	midnight := time.Date(ts.Year(), ts.Month(), ts.Day(), 0, 0, 0, 0, time.UTC)
	if len(p.Open) == 0 {
		return midnight.AddDate(0, 0, 1).UnixNano(), true // the whole working day
	}
	off := time.Duration(ts.Sub(midnight))
	for _, w := range p.Open {
		if off >= w.From && off < w.To {
			return midnight.Add(w.To).UnixNano(), true
		}
	}
	return 0, false
}
