// Package catalog is the device Worker Type's operation table
// (ADR-draft-device-workers-for-the-physical-world): which values each operation
// needs, what it returns, and the bounds a deploy and a run share.
//
// It imports nothing of atlas's, for the reason connector/wikiwikibangbang/catalog
// gives: the compiler reads these rows to refuse a model at deploy, and
// connector/device reads the same rows when it runs a job, so there is one table and
// nothing to keep in step.
//
// A model names outputs and inputs, never pins: which line a name is wired to is the
// device Worker's configuration, so rewiring a device does not redeploy a process.
package catalog

import (
	"fmt"
	"regexp"
	"sort"
	"time"
)

// The values a model authors, by the attribute that carries them.
const (
	// AttrOutput names a configured output: the one a write or a pulse drives.
	AttrOutput = "output"
	// AttrLine names a configured output or input: the one a read reports.
	AttrLine = "line"
	// AttrOn is the logical state to drive: true is on, false is off. Whether on is
	// a high or a low level on the wire is the output's activeLow setting.
	AttrOn = "on"
	// AttrDuration is how long a pulse holds its state, in milliseconds.
	AttrDuration = "durationMs"
)

// Attributes lists every value an operation may need or take, in the order a message
// names them.
var Attributes = []string{AttrOutput, AttrLine, AttrOn, AttrDuration}

// Op is one operation a model can author.
type Op struct {
	Name string
	// Needs are the attributes the operation cannot run without.
	Needs []string
	// Takes are the attributes it uses beyond Needs; an attribute in neither is
	// refused, so a value is never silently ignored.
	Takes []string
	// Returns says whether the operation hands back a value a result variable keeps.
	Returns bool
	// Idempotent says whether running it twice has the effect of running it once.
	// A job is delivered at least once, so an operation that is not idempotent is
	// deduplicated by its job key on the device.
	Idempotent bool
}

// NeedsAttr reports whether the operation cannot run without attr.
func (o Op) NeedsAttr(attr string) bool { return contains(o.Needs, attr) }

// Uses reports whether the operation reads attr at all.
func (o Op) Uses(attr string) bool { return contains(o.Needs, attr) || contains(o.Takes, attr) }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// The operations' names.
const (
	OpDigitalWrite = "digital.write"
	OpDigitalRead  = "digital.read"
	OpPulse        = "pulse"
)

var ops = map[string]Op{
	OpDigitalWrite: {Name: OpDigitalWrite, Needs: []string{AttrOutput, AttrOn}, Idempotent: true},
	OpDigitalRead:  {Name: OpDigitalRead, Needs: []string{AttrLine}, Returns: true, Idempotent: true},
	// A pulse drives its output on unless it is told otherwise, then returns it to
	// its safe state. Twice is not once — a door opened twice, a feeder run twice —
	// so a pulse is the operation the job-key deduplication exists for.
	OpPulse: {Name: OpPulse, Needs: []string{AttrOutput, AttrDuration}, Takes: []string{AttrOn}},
}

// Lookup returns the operation of that name.
func Lookup(name string) (Op, bool) {
	op, ok := ops[name]
	return op, ok
}

// Names lists the operations, sorted.
func Names() []string {
	names := make([]string, 0, len(ops))
	for n := range ops {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// MaxPulse is the longest pulse a model may author. A pulse is one job, held by one
// lease for as long as it lasts; anything longer is modelled as a write on, a timer,
// and a write off, so no lease has to outlast a physical action.
const MaxPulse = 60 * time.Second

// CheckDuration refuses a pulse duration outside (0, MaxPulse].
func CheckDuration(ms int64) error {
	if ms <= 0 {
		return fmt.Errorf("device: a pulse lasts at least 1 ms, not %d", ms)
	}
	if time.Duration(ms)*time.Millisecond > MaxPulse {
		return fmt.Errorf("device: a pulse of %d ms is longer than the %d ms one job may hold; model a write on, a timer and a write off instead", ms, MaxPulse.Milliseconds())
	}
	return nil
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// CheckName refuses a line name a model or a configuration may not use: lower-case
// letters, digits and hyphens, starting with a letter, at most 32 characters.
func CheckName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("device: %q is not a line name (lower-case letters, digits and hyphens, starting with a letter, at most 32)", name)
	}
	return nil
}
