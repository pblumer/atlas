package api

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// TestTheNewestVersionWinsWhicheverOrderTheRegistryHandsThemOver pins what the Workers
// view's Processes column promises: one row per process, at the version now deployed.
// An engine keeps every version it ever deployed, so a column that listed all of them
// would turn one useful link into a stack of near-identical ones.
//
// The awkward part is that the registry is a map. `for key, d := range s.deployments`
// visits two versions of the same process in an order Go deliberately randomises, so
// half the time the newest arrives first and the older one has to be discarded on
// arrival — which is what the version guard in jobTypeUsers is for. A single call would
// exercise one of those two orders and pass either way, and the run after it would
// exercise the other. So the property under test is order-independence, and the loop is
// what lets the test see both orders rather than whichever one it drew.
func TestTheNewestVersionWinsWhicheverOrderTheRegistryHandsThemOver(t *testing.T) {
	// Same BPMN process id, two deployed versions — the shape the column is about.
	// A user task is enough to make each one create jobs of a type, which is what puts
	// it in front of a worker row at all.
	s := &Server{deployments: map[uint64]*deployment{
		11: {Key: 11, ProcessID: "p", Name: "Kreditantrag", Version: 1, cp: oneTaskProcess(t)},
		22: {Key: 22, ProcessID: "p", Name: "Kreditantrag", Version: 2, cp: oneTaskProcess(t)},
	}}

	for i := range 64 {
		byType := s.jobTypeUsers()
		if len(byType) != 1 {
			t.Fatalf("round %d: %d job types, want 1", i, len(byType))
		}
		for jobType, users := range byType {
			if len(users) != 1 {
				t.Fatalf("round %d: job type %d has %d rows, want the one process", i, jobType, len(users))
			}
			if u := users[0]; u.Version != 2 || u.ProcessDefKey != 22 {
				t.Fatalf("round %d: named v%d (key %d), want the newest, v2 (key 22)",
					i, u.Version, u.ProcessDefKey)
			}
		}
	}
}

// oneTaskProcess is a compiled process with a single job-creating element, so it has a
// job type and therefore a place in the Workers view.
func oneTaskProcess(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(1, "p", 1)
	b.AddUserTask("", "", "", "", 0, 0, 1)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return cp
}
