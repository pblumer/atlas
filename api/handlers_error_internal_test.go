package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// errReader always fails, to drive the io.ReadAll error branches of the deploy
// and create-instance handlers.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// newServerForErrors builds a real engine-backed Server for white-box handler
// tests. The engine is single-writer; the Server owns it through its run loop,
// so the test drives it only via ServeHTTP.
func newServerForErrors(t *testing.T) *Server {
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
	srv, err := New(proc, store, filepath.Join(dir, "deployments"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	return srv
}

// TestReadBodyErrors covers the io.ReadAll failure branch in handleDeploy and
// handleCreateInstance by supplying a request body that errors on read.
func TestReadBodyErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	cases := []struct {
		name string
		path string
	}{
		{"deploy", "/api/v1/deployments"},
		{"create instance", "/api/v1/processes/1/instances"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, errReader{})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestDoAfterCloseIsNoop covers the do() closing branch: once the run loop has
// stopped, a submitted closure never runs and the result stays zero-valued. It
// builds its own server so it can Close exactly once (no cleanup double-close).
func TestDoAfterCloseIsNoop(t *testing.T) {
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
	t.Cleanup(func() {
		_ = store.Close()
		_ = log.Close()
	})

	srv, err := New(proc, store, filepath.Join(dir, "deployments"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.Close() // stop the loop

	ran := false
	srv.do(func() { ran = true })
	if ran {
		t.Fatal("do ran a closure after the loop was stopped")
	}
}
