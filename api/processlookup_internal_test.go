package api

import (
	"slices"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// Where the catalogue's fulfilment report meets the engine.
//
// The report itself is a pure function and is proved against a lookup a test
// writes (api/catalog/fulfilmentreport_test.go). That proves the rule and it
// cannot prove the answer: what is deployed and what is worked are this server's
// facts, and a fake agrees with whatever the fake was told.
//
// The half the fake cannot reach is exactly the half that was wrong first. Served
// asked a worker's registry what it had *leased*, so a worker connected and
// polling a queue with no work in it read as nobody serving that queue — and the
// report called a healthy installation broken. It was found by running a server by
// hand, which is not a thing that runs again. These are.

// TestJobTypesOfNamesWhatTheProcessActuallyCarries.
func TestJobTypesOfNamesWhatTheProcessActuallyCarries(t *testing.T) {
	srv := newServerWithOptions(t, WithSystemProcesses())
	look := processLookup{s: srv}

	// The fulfilment orchestration does its work over HTTP, so a REST job is what
	// it carries — and it is the job type this whole correction turned on.
	types, deployed := look.JobTypesOf("atlas-auftrag-erfuellung")
	if !deployed {
		t.Fatal("the shipped fulfilment orchestration reads as not deployed, so the " +
			"report would name the one process every order goes through as missing")
	}
	if !slices.Contains(types, compiler.RestJobType) {
		t.Errorf("it carries %v, and not %q — the reserved REST type is what the engine "+
			"and the shipped worker serve, and a type outside that set is a queue "+
			"nobody pulls", types, compiler.RestJobType)
	}

	// An approval model carries a task somebody works as well as a call.
	types, deployed = look.JobTypesOf("atlas-genehmigung-fix")
	if !deployed {
		t.Fatal("the shipped approval model reads as not deployed")
	}
	if !slices.Contains(types, compiler.UserTaskJobType) {
		t.Errorf("the approval carries %v and no user task, which is the one step in "+
			"it a person takes", types)
	}
}

// TestAProcessNobodyDeployedIsSaidToBeMissing.
//
// The other half of a binding's failure, and the one an operator fixes
// differently: a name to correct rather than a worker to start. A lookup that
// answered "deployed, no job types" for a typo would have the report say nothing
// at all about it.
func TestAProcessNobodyDeployedIsSaidToBeMissing(t *testing.T) {
	look := processLookup{s: newServerWithOptions(t, WithSystemProcesses())}
	for _, id := range []string{"proc_tippfehler", "", "   "} {
		if types, deployed := look.JobTypesOf(id); deployed || types != nil {
			t.Errorf("%q reads as deployed carrying %v", id, types)
		}
	}
}

// TestAPersonCountsAsServingAUserTask.
//
// A user task waits for somebody by design. Counted as unserved it would put
// every human step in the installation on a list of defects, which is a list
// nobody reads.
func TestAPersonCountsAsServingAUserTask(t *testing.T) {
	look := processLookup{s: newServerWithOptions(t, WithSystemProcesses())}
	if !look.Served(compiler.UserTaskJobType) {
		t.Error("a user task reads as a queue nobody serves")
	}
}

// TestAJobTypeNothingWorksIsSaidSo.
//
// The finding the whole report exists for. The engine serves REST itself unless
// an operator offloads the kind, so the case is built rather than waited for:
// take the handler away and the type has nobody.
func TestAJobTypeNothingWorksIsSaidSo(t *testing.T) {
	srv := newServerWithOptions(t, WithSystemProcesses())
	look := processLookup{s: srv}

	if !look.Served(compiler.RestJobType) {
		t.Fatal("REST reads as unserved on a server that serves it in process, so the " +
			"report would call every stock installation broken")
	}
	srv.jobRunner.Unhandle(compiler.RestJobTypeIndex)
	if look.Served(compiler.RestJobType) {
		t.Error("REST still reads as served after the engine stopped handling it and " +
			"with no worker pulling it — which is the parked-forever queue this " +
			"report was written to name")
	}
}

// TestAWorkerThatOnlyPollsCountsAsServing.
//
// The bug this file exists for. A worker's registry counts what it was *given*,
// and a queue with no work in it gives nothing — so a connected worker polling an
// idle queue was indistinguishable from a worker that is not there. Most queues
// are idle most of the time, so reading the wrong one reports a healthy
// installation as broken, which is what it did on the first run against a real
// server.
func TestAWorkerThatOnlyPollsCountsAsServing(t *testing.T) {
	srv := newServerWithOptions(t, WithSystemProcesses())
	look := processLookup{s: srv}

	// A type the engine does not serve itself, so only a worker can answer for it.
	srv.jobRunner.Unhandle(compiler.RestJobTypeIndex)
	if look.Served(compiler.RestJobType) {
		t.Fatal("unserved before anybody polls, or this proves nothing")
	}

	// One poll, no job. Exactly what a worker on an idle queue reports.
	srv.do(func() { srv.workers.polls("rest", compiler.RestJobType) })
	if !look.Served(compiler.RestJobType) {
		t.Error("a worker that polled the queue and got nothing reads as nobody " +
			"serving it, so every idle queue on a working installation is a finding")
	}
}

// TestAJobTypeNoDeploymentInternedIsNotAFinding.
//
// A type no compiled process ever named has no job that could wait on it. Saying
// "nobody serves this" about it would be a finding about nothing.
func TestAJobTypeNoDeploymentInternedIsNotAFinding(t *testing.T) {
	look := processLookup{s: newServerWithOptions(t, WithSystemProcesses())}
	for _, unknown := range []string{"io.example.nothing-uses-this", "", "  "} {
		if !look.Served(unknown) {
			t.Errorf("%q is reported as a queue nobody serves, and nothing can wait "+
				"on it", unknown)
		}
	}
}
