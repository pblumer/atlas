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
// This is the line the reload path draws (ADR-draft-reload-skips-the-deploy-gate):
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
