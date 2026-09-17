package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestNewFailsOnUncompilableStoredDeployment covers loadDeployments' compile
// error branch (ADR-0019): a persisted definition whose XML no longer compiles
// makes New fail loudly rather than booting with a silently missing definition.
// This is the line the reload path draws (ADR-0177):
// a model today's *validation* would refuse still loads, because it compiled and
// its instances run; one that yields no compiled process at all does not, because
// there is nothing to bring back. The failure names the record it read, since
// acting on it means going to that file.
func TestNewFailsOnUncompilableStoredDeployment(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer log.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer store.Close()
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Seed a deployment record whose XML has no <process> element, so
	// compiler.Parse rejects it on reload.
	depDir := filepath.Join(dir, "deployments")
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rec := persistedDeployment{Key: 1, ProcessID: "broken", Version: 1, XML: `<definitions></definitions>`}
	data, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(depDir, "1.json"), data, 0o644); err != nil {
		t.Fatalf("write record: %v", err)
	}

	srv, err := New(proc, store, dir)
	if err == nil {
		srv.Close()
		t.Fatal("New with an uncompilable stored deployment: want error, got nil")
	}
	if !strings.Contains(err.Error(), filepath.Join(depDir, "1.json")) {
		t.Fatalf("error does not name the record to fix: %v", err)
	}
}

// TestNewStartsWithAStoredCallThatCanOnlyBeNull is the other side of the line
// above, and the reason it is drawn where it is (ADR-0177).
//
// ADR-0388 made the compiler refuse a FEEL call this build can only answer with
// null. The rule is right at a deploy — such a call cannot ever have done what its
// author meant — but it arrived after models carrying one were already stored, and
// it refused them from inside the *build* stage, where the reload has no compiled
// process to keep. A server holding one such definition then exited during
// startup, was restarted by its supervisor, and exited again, with every other
// definition and every running instance unreachable behind it. The record on disk
// was fine; only a rule that did not exist when it was deployed was not.
//
// So: the definition comes back, compiled exactly as the build that stored it
// compiled it, and the server serves.
func TestNewStartsWithAStoredCallThatCanOnlyBeNull(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer log.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer store.Close()
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	depDir := filepath.Join(dir, "deployments")
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const model = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                 xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="probe_feel_caps" isExecutable="true">
    <startEvent id="start"/>
    <scriptTask id="s_keys">
      <extensionElements><zeebe:script expression="= get keys(kunde)" resultVariable="keys"/></extensionElements>
    </scriptTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="s_keys"/>
    <sequenceFlow id="f2" sourceRef="s_keys" targetRef="end"/>
  </process>
</definitions>`
	rec := persistedDeployment{Key: 379, ProcessID: "probe_feel_caps", Version: 1, XML: model}
	data, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(depDir, "379.json"), data, 0o644); err != nil {
		t.Fatalf("write record: %v", err)
	}

	srv, err := New(proc, store, dir)
	if err != nil {
		t.Fatalf("New with a stored null-only call: %v", err)
	}
	defer srv.Close()
	if _, ok := srv.deployments[379]; !ok {
		t.Fatal("the definition was not brought back; the reload dropped it silently")
	}
}
