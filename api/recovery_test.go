package api_test

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// stack is one boot of the whole persistence stack over a fixed data dir, so a
// test can tear it down and boot a fresh one over the same dir to simulate a
// process restart.
type stack struct {
	ts    *httptest.Server
	srv   *api.Server
	store *state.Store
	log   *wal.Log
}

func boot(t *testing.T, dir string) *stack {
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
	srv, err := api.New(proc, store, filepath.Join(dir, "deployments"))
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	return &stack{ts: httptest.NewServer(srv.Handler()), srv: srv, store: store, log: log}
}

func (s *stack) shutdown() {
	s.ts.Close()
	s.srv.Close()
	_ = s.store.Close()
	_ = s.log.Close()
}

// TestDeploymentSurvivesRestart is the core ADR-0019 recovery property: after a
// deploy + a started instance, a full restart over the same data dir restores the
// definition (list + version), its diagram XML, and the instance enriched with
// its process id — none of which survived when deployments lived only in memory.
func TestDeploymentSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	first := boot(t, dir)
	code, body := doReq(t, first.ts, "POST", "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != 200 {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, _ := doReq(t, first.ts, "POST", "/api/v1/processes/1/instances", "", "application/json"); code != 200 {
		t.Fatalf("create instance status=%d", code)
	}
	first.shutdown()

	// Restart over the same dir.
	second := boot(t, dir)
	defer second.shutdown()

	// The definition is back, at the version it had before.
	code, body = doReq(t, second.ts, "GET", "/api/v1/processes", "", "")
	if code != 200 || !strings.Contains(string(body), `"processId":"order"`) || !strings.Contains(string(body), `"version":1`) {
		t.Fatalf("processes after restart: status=%d body=%s", code, body)
	}

	// Its diagram XML renders again (was a 404 before durable deployments).
	code, body = doReq(t, second.ts, "GET", "/api/v1/processes/1/xml", "", "")
	if code != 200 || !strings.Contains(string(body), "order") {
		t.Fatalf("xml after restart: status=%d body=%s", code, body)
	}

	// The recovered instance is enriched with its process id/version, not orphaned.
	code, body = doReq(t, second.ts, "GET", "/api/v1/instances", "", "")
	if code != 200 || !strings.Contains(string(body), `"processId":"order"`) {
		t.Fatalf("instances after restart: status=%d body=%s", code, body)
	}

	// A new deployment must not collide with the restored key.
	code, body = doReq(t, second.ts, "POST", "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != 200 {
		t.Fatalf("redeploy after restart: status=%d body=%s", code, body)
	}
	var dep2 struct {
		Key     uint64 `json:"key"`
		Version int32  `json:"version"`
	}
	if err := json.Unmarshal(body, &dep2); err != nil {
		t.Fatalf("decode redeploy: %v", err)
	}
	if dep2.Key == dep.Key {
		t.Fatalf("redeploy reused key %d from before restart", dep2.Key)
	}
	if dep2.Version != 2 {
		t.Fatalf("redeploy version=%d, want 2 (version counter must survive restart)", dep2.Version)
	}
}

// TestDeleteSurvivesRestart proves a deletion is durable: a deleted definition
// does not reappear after a restart.
func TestDeleteSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	first := boot(t, dir)
	if code, body := doReq(t, first.ts, "POST", "/api/v1/deployments", sampleBPMN, "application/xml"); code != 200 {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if code, body := doReq(t, first.ts, "DELETE", "/api/v1/processes/1", "", ""); code != 204 {
		t.Fatalf("delete status=%d body=%s", code, body)
	}
	first.shutdown()

	second := boot(t, dir)
	defer second.shutdown()

	code, body := doReq(t, second.ts, "GET", "/api/v1/processes", "", "")
	if code != 200 || strings.Contains(string(body), `"processId":"order"`) {
		t.Fatalf("deleted definition reappeared after restart: status=%d body=%s", code, body)
	}
}
