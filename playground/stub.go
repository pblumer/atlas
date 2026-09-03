package playground

import (
	"fmt"
	"time"

	"github.com/pblumer/atlas/model"
)

// Stub is how the sandbox answers one job — the stand-in for the worker,
// worker or person that would answer it in production.
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
		if err := p.Calendar.validate(fmt.Sprintf("pool %q", name)); err != nil {
			return err
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
	// Calendar is when the pool works. The zero value is always.
	Calendar
}
