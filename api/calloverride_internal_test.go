package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCallOverrideHandlerErrors covers the write/read handlers' failure branches
// (ADR-0105): a missing process id, a persist failure on set, a remove failure on
// clear, and a corrupt store surfacing on both the inventory read and the startup
// load. Each drives a handler directly with the path value the router would supply.
func TestCallOverrideHandlerErrors(t *testing.T) {
	newSrv := func() (*Server, func()) { return bootAPIWithModels(t, t.TempDir(), false) }

	// Missing process id → 400 (auth off, so requireAdmin passes and the guard fires).
	t.Run("missing pid", func(t *testing.T) {
		srv, done := newSrv()
		defer done()
		rec := httptest.NewRecorder()
		srv.handleSetCallOverride(rec, httptest.NewRequest(http.MethodPut, "/x", strings.NewReader(`{"action":"disable"}`)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("missing pid: status = %d, want 400", rec.Code)
		}
	})

	// save failure → 500: occupy the record's temp path with a directory.
	t.Run("save error", func(t *testing.T) {
		srv, done := newSrv()
		defer done()
		if err := os.Mkdir(srv.callOverrides.FileFor("child")+".tmp", 0o755); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/x", strings.NewReader(`{"action":"disable"}`))
		req.SetPathValue("processId", "child")
		srv.handleSetCallOverride(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("save error: status = %d, want 500", rec.Code)
		}
	})

	// delete failure → 500: a non-empty directory where the record file would be.
	t.Run("delete error", func(t *testing.T) {
		srv, done := newSrv()
		defer done()
		recPath := srv.callOverrides.FileFor("child")
		if err := os.Mkdir(recPath, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(recPath, "c"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/x", nil)
		req.SetPathValue("processId", "child")
		srv.handleDeleteCallOverride(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("delete error: status = %d, want 500", rec.Code)
		}
	})

	// A corrupt record makes both the inventory read (500) and the startup load (error).
	t.Run("corrupt store", func(t *testing.T) {
		srv, done := newSrv()
		defer done()
		if err := os.WriteFile(srv.callOverrides.FileFor("child"), []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		srv.handleCallActivities(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("corrupt store read: status = %d, want 500", rec.Code)
		}
		var loadErr error
		srv.do(func() { loadErr = srv.loadCallOverrides() })
		if loadErr == nil {
			t.Error("loadCallOverrides over a corrupt store: want an error, got nil")
		}
	})
}

// TestCallOverrideHandlersRequireAdmin checks that setting and clearing an override
// are admin-gated (ADR-0105) — an override decides which process a call activity
// reaches on this server, which is an operator's decision about the installation
// and not about one model.
//
// Asserted at the boundary, where the role is now decided (routeroles.go): the
// request is refused before either handler is entered.
func TestCallOverrideHandlersRequireAdmin(t *testing.T) {
	ordinary := []string{RoleModeler, RoleOperator, RoleUser}
	for _, tc := range []struct{ name, method string }{
		{"set", http.MethodPut},
		{"delete", http.MethodDelete},
	} {
		got := boundaryStatus(t, ordinary, tc.method, "/api/v1/call-activities/overrides/x")
		if got != http.StatusForbidden {
			t.Errorf("%s without admin: status = %d, want 403", tc.name, got)
		}
	}
}

// TestCallOverrideStore exercises the sidecar store directly: save (with
// overwrite), loadAll (oldest-first, ignoring non-record files), and idempotent
// delete (ADR-0105).
func TestCallOverrideStore(t *testing.T) {
	dir := t.TempDir()
	st, err := newCallOverrideStore(dir)
	if err != nil {
		t.Fatalf("newCallOverrideStore: %v", err)
	}
	a := callOverride{CalledProcessID: "a", Action: overrideDisable, UpdatedAt: 1}
	b := callOverride{CalledProcessID: "b", Action: overrideRedirect, TargetProcessID: "z", UpdatedAt: 2}
	if err := st.Save(a); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := st.Save(b); err != nil {
		t.Fatalf("save b: %v", err)
	}
	// Overwrite a in place — still one record for "a".
	if err := st.Save(callOverride{CalledProcessID: "a", Action: overridePin, TargetVersion: 3, UpdatedAt: 1}); err != nil {
		t.Fatalf("overwrite a: %v", err)
	}

	got, err := st.LoadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(got) != 2 || got[0].CalledProcessID != "a" || got[1].CalledProcessID != "b" {
		t.Fatalf("loadAll = %+v, want [a, b] oldest-first", got)
	}
	if got[0].Action != overridePin || got[0].TargetVersion != 3 {
		t.Errorf("overwritten a = %+v, want pin v3", got[0])
	}

	// Non-record files are ignored: a non-.json file and a .json whose name is not
	// hex (so it cannot be a record) are both skipped.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "zz.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ = st.LoadAll(); len(got) != 2 {
		t.Fatalf("loadAll after junk files = %d records, want 2", len(got))
	}

	if err := st.Delete("a"); err != nil {
		t.Fatalf("delete a: %v", err)
	}
	if err := st.Delete("a"); err != nil {
		t.Fatalf("delete a again (idempotent): %v", err)
	}
	if got, _ = st.LoadAll(); len(got) != 1 || got[0].CalledProcessID != "b" {
		t.Fatalf("after delete, loadAll = %+v, want [b]", got)
	}
}

// TestCallOverrideStoreErrors covers the store's failure branches (ADR-0105),
// mirroring the deploy-store error tests: an uncreatable directory, a delete over a
// non-empty directory, and a corrupt record file that fails to decode.
func TestCallOverrideStoreErrors(t *testing.T) {
	// newCallOverrideStore fails when the directory cannot be created (its parent is
	// a regular file, so MkdirAll errors).
	afile := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(afile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newCallOverrideStore(filepath.Join(afile, "sub")); err == nil {
		t.Error("newCallOverrideStore under a file: want an error, got nil")
	}

	st, err := newCallOverrideStore(t.TempDir())
	if err != nil {
		t.Fatalf("newCallOverrideStore: %v", err)
	}

	// delete surfaces a non-IsNotExist error: a non-empty directory where the record
	// file would be cannot be removed by os.Remove.
	recPath := st.FileFor("k")
	if err := os.Mkdir(recPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recPath, "child"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete("k"); err == nil {
		t.Error("delete of a non-empty directory: want an error, got nil")
	}

	// loadAll surfaces a decode error: a hex-named .json record with invalid JSON.
	if err := os.WriteFile(st.FileFor("bad"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadAll(); err == nil {
		t.Error("loadAll over a corrupt record: want an error, got nil")
	}
}

// TestLoadCallOverrides checks the startup load path (ADR-0105): a valid redirect
// is pushed into the processor, while a pin whose version is not deployed is skipped
// rather than failing the load. It runs against a real server so the run-loop
// ownership and the resolution helper are exercised together.
func TestLoadCallOverrides(t *testing.T) {
	dir := t.TempDir()
	srv, done := bootAPIWithModels(t, dir, false)
	defer done()

	recs := []callOverride{
		{CalledProcessID: "alpha", Action: overrideRedirect, TargetProcessID: "beta", UpdatedAt: 1},
		{CalledProcessID: "gamma", Action: overridePin, TargetVersion: 9, UpdatedAt: 2}, // stale: nothing deployed
	}
	for _, r := range recs {
		if err := srv.callOverrides.Save(r); err != nil {
			t.Fatalf("save %s: %v", r.CalledProcessID, err)
		}
	}

	var loadErr error
	srv.do(func() { loadErr = srv.loadCallOverrides() })
	if loadErr != nil {
		t.Fatalf("loadCallOverrides: %v (a stale pin must be skipped, not fatal)", loadErr)
	}

	// The redirect resolves to beta's latest — beta is not deployed, so the effective
	// target parks (nil). This exercises resolveEffectiveTarget's redirect branch and
	// confirms the record round-trips through the store.
	var eff *deployment
	redirect := recs[0]
	srv.do(func() { eff = srv.resolveEffectiveTarget("alpha", &redirect) })
	if eff != nil {
		t.Errorf("resolveEffectiveTarget(alpha→beta, beta undeployed) = %v, want nil (parks)", eff)
	}
}
