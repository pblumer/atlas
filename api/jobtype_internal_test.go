package api

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// jobTypeSrv builds a server over a temp dir and returns it with its handler, so a
// test can drive HTTP and then look at the state store the requests wrote.
func jobTypeSrv(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := New(proc, store, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	return srv, dir
}

func jobTypeBPMN(procID, jobType string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="%s" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements><zeebe:taskDefinition type="%s" retries="3"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`, procID, jobType)
}

// TestDeployedJobTypesAreEngineWide is the defect in its observable form: two
// definitions, two different model-authored job types, and the jobs they create
// must sit on the engine-wide activatable index under *different* types. Before
// the job-type table both interned to the same local index, so a worker
// subscribing to one type would have been handed the other's work (ADR-0007).
func TestDeployedJobTypesAreEngineWide(t *testing.T) {
	srv, _ := jobTypeSrv(t)
	h := srv.Handler()

	orders := deployBPMN(t, h, jobTypeBPMN("orders", "send-email"))
	billing := deployBPMN(t, h, jobTypeBPMN("billing", "charge-card"))
	startInstance(t, h, orders, "{}")
	startInstance(t, h, billing, "{}")

	// The registry assigned each name an index past the reserved range.
	sendEmail, ok := srv.jobTypes.Index("send-email")
	if !ok {
		t.Fatal("send-email was never registered")
	}
	chargeCard, ok := srv.jobTypes.Index("charge-card")
	if !ok {
		t.Fatal("charge-card was never registered")
	}
	if sendEmail == chargeCard {
		t.Fatalf("both job types resolved to %d", sendEmail)
	}
	for name, idx := range map[string]int32{"send-email": sendEmail, "charge-card": chargeCard} {
		if idx < compiler.FirstDynamicJobTypeIndex() {
			t.Errorf("%q resolved to %d, inside the reserved range below %d", name, idx, compiler.FirstDynamicJobTypeIndex())
		}
	}

	// And each job is findable under exactly its own type — the property a
	// type-keyed pull rests on.
	count := func(jobType int32) int {
		t.Helper()
		n := 0
		if err := srv.store.ActivatableJobs(jobType, func(uint64) error { n++; return nil }); err != nil {
			t.Fatalf("ActivatableJobs(%d): %v", jobType, err)
		}
		return n
	}
	if got := count(sendEmail); got != 1 {
		t.Errorf("%d jobs activatable as send-email, want 1", got)
	}
	if got := count(chargeCard); got != 1 {
		t.Errorf("%d jobs activatable as charge-card, want 1", got)
	}
}

// A restart must not renumber anything: the deployments are recompiled from their
// stored XML, so resolution runs again and has to land on the indices the jobs
// already on disk were written under.
func TestJobTypeIndicesSurviveAReload(t *testing.T) {
	dir := t.TempDir()
	// open builds a whole server over dir and returns it with a close that releases
	// the state lock, so the same directory can be reopened as a restart would.
	open := func() (*Server, func()) {
		t.Helper()
		log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
		if err != nil {
			t.Fatalf("wal.Open: %v", err)
		}
		store, err := state.Open(filepath.Join(dir, "state"))
		if err != nil {
			t.Fatalf("state.Open: %v", err)
		}
		proc := engine.New(1, log, store, nil)
		if err := proc.Recover(); err != nil {
			t.Fatalf("Recover: %v", err)
		}
		srv, err := New(proc, store, dir)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return srv, func() {
			srv.Close()
			_ = store.Close()
			_ = log.Close()
		}
	}

	srv, closeSrv := open()
	deployBPMN(t, srv.Handler(), jobTypeBPMN("orders", "send-email"))
	deployBPMN(t, srv.Handler(), jobTypeBPMN("billing", "charge-card"))
	before := map[string]int32{}
	for _, name := range []string{"send-email", "charge-card"} {
		idx, ok := srv.jobTypes.Index(name)
		if !ok {
			t.Fatalf("%q missing before the reload", name)
		}
		before[name] = idx
	}
	closeSrv()

	restarted, closeRestarted := open()
	defer closeRestarted()
	for name, want := range before {
		got, ok := restarted.jobTypes.Index(name)
		if !ok {
			t.Errorf("%q is missing after the restart", name)
			continue
		}
		if got != want {
			t.Errorf("%q = %d after the restart, want %d — jobs on disk carry the old index", name, got, want)
		}
	}
	// A name first seen after the restart must not reuse an index already issued.
	fresh, err := restarted.jobTypes.Intern("ship-order")
	if err != nil {
		t.Fatalf("Intern: %v", err)
	}
	for name, used := range before {
		if fresh == used {
			t.Errorf("a job type registered after the restart reused %d, held by %q", fresh, name)
		}
	}
}
