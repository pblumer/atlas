package compiler

import "testing"

// TestConnectorTaskOfStaleJob is a regression test for the job-runner crash where
// a persisted worker job outlives the process definition that compiled its
// element as a worker task. Resolving it must return an error (so the worker
// fails the job into an incident) rather than index out of range and panic the
// job-runner goroutine, which crashes the whole server.
func TestConnectorTaskOfStaleJob(t *testing.T) {
	// Element 0 claims a worker-task detail index, but the worker-task table
	// is empty — the exact shape a stale job hits after a redeploy/recompile.
	p := &CompiledProcess{
		nodes:          []CompiledNode{{Detail: 0}},
		connectorTasks: nil,
	}
	if _, err := p.ConnectorTaskOf(0); err == nil {
		t.Fatal("expected error for element with an empty connector-task table, got nil")
	}

	// An element id past the node table.
	if _, err := p.ConnectorTaskOf(5); err == nil {
		t.Fatal("expected error for an out-of-range element id, got nil")
	}
	if _, err := p.ConnectorTaskOf(-1); err == nil {
		t.Fatal("expected error for a negative element id, got nil")
	}

	// A node that is not a task at all (Detail == -1, the "none" sentinel).
	p.nodes = []CompiledNode{{Detail: -1}}
	if _, err := p.ConnectorTaskOf(0); err == nil {
		t.Fatal("expected error for a non-worker-task node, got nil")
	}

	// Happy path: a valid task returns its detail without error.
	p.nodes = []CompiledNode{{Detail: 0}}
	p.connectorTasks = []ConnectorTaskDetail{{Connector: 7}}
	got, err := p.ConnectorTaskOf(0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Connector != 7 {
		t.Fatalf("got worker %d, want 7", got.Connector)
	}
}
