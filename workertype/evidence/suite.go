package evidence

import (
	"context"

	"github.com/pblumer/atlas/workertype"
)

// Suite is the executable cases that check one operation against one contract. The
// cases run in process against a fake of the provider, so a record can be renewed and
// verified anywhere, without a tenant or a credential; what a fake cannot show is a
// case the suite reports as not exercised, and Environment says what it ran against.
type Suite struct {
	// Package is the import path of the suite's code and fixtures. Its dependency
	// digest is the fixture revision a record carries, and it includes the
	// schema validator's version through go.sum.
	Package string
	// Environment states what the cases ran against, for the reviewer.
	Environment string
	// Run executes every case, in a fixed order, and reports each outcome. It
	// fails only when the suite cannot run at all.
	Run func(ctx context.Context, contract workertype.TechnicalContract) ([]Result, error)
}

// suites are the registered suites by name.
var suites = map[string]Suite{}

// Register adds a suite under the name records use for it. A suite package registers
// itself from init, and both the gate in workertype/evidence/gate and the evidence
// command import it, so a record verifies in CI exactly as it renews on a desk. A
// name registered twice is a programming error.
func Register(name string, s Suite) {
	if _, ok := suites[name]; ok {
		panic("evidence: suite " + name + " registered twice")
	}
	suites[name] = s
}
