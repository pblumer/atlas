package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// dmnModelListPathsDir is the model store the server's DirResolver reads.
func dmnModelListPathsDir(t *testing.T, srv *Server) string {
	t.Helper()
	dir, ok := srv.dmnModelDir()
	if !ok {
		t.Fatal("the server has no local model store")
	}
	return dir
}

// TestDmnModelListSendsARemoteStoreElsewhere: with a remote temis service resolving
// models, Atlas has no folder to list or delete from. Answering with an empty list
// would read as "no models", and a 204 on delete as "removed" — both untrue — so both
// say where the models live instead.
func TestDmnModelListSendsARemoteStoreElsewhere(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.dmnResolver = dmn.ServiceResolver{BaseURL: "https://temis.example"}
	x := deployTestHarness{t, srv.Handler()}

	for _, tc := range []struct{ method, path, says string }{
		{http.MethodGet, "/api/v1/dmn-models", "list them there"},
		{http.MethodDelete, "/api/v1/dmn-models/dish", "remove them there"},
	} {
		code, body := x.do(tc.method, tc.path, "")
		if code != http.StatusConflict || !strings.Contains(string(body), tc.says) {
			t.Errorf("%s %s: %d (%s), want 409 saying %q", tc.method, tc.path, code, body, tc.says)
		}
	}
}

// TestDmnModelListRefusesAHandleThatNamesNothing: a handle is reduced to the
// characters a stored file name can carry, and one that reduces to nothing must not
// be passed on as the empty name — which would address the store itself.
func TestDmnModelListRefusesAHandleThatNamesNothing(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	code, body := x.do(http.MethodDelete, "/api/v1/dmn-models/---", "")
	if code != http.StatusBadRequest || !strings.Contains(string(body), "invalid model handle") {
		t.Errorf("empty handle: %d (%s), want 400", code, body)
	}
	if _, err := os.Stat(filepath.Join(dmnModelListPathsDir(t, srv), "dish.dmn")); err != nil {
		t.Errorf("a refused delete touched the store: %v", err)
	}
}

// TestDmnModelListTreatsAMissingStoreAsEmpty: a server nobody has uploaded a model to
// has no folder yet, and that is an empty store rather than a broken one.
func TestDmnModelListTreatsAMissingStoreAsEmpty(t *testing.T) {
	srv, _ := newValidateServer(t)
	if err := os.RemoveAll(dmnModelListPathsDir(t, srv)); err != nil {
		t.Fatal(err)
	}
	code, body := deployTestHarness{t, srv.Handler()}.do(http.MethodGet, "/api/v1/dmn-models", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d (%s)", code, body)
	}
	var rows []dmnModelResp
	if err := json.Unmarshal(body, &rows); err != nil || len(rows) != 0 {
		t.Errorf("list = %s (%v), want an empty array", body, err)
	}
}

// TestDmnModelListFailsOnAStoreThatIsNotAFolder: a file where the model folder should
// be is a broken data directory, and listing it as empty would hide every model an
// author uploaded.
func TestDmnModelListFailsOnAStoreThatIsNotAFolder(t *testing.T) {
	srv, _ := newValidateServer(t)
	approvalsPathsDirAsFile(t, dmnModelListPathsDir(t, srv))
	code, body := deployTestHarness{t, srv.Handler()}.do(http.MethodGet, "/api/v1/dmn-models", "")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "read model store") {
		t.Errorf("list: %d (%s), want 500 'read model store'", code, body)
	}
}

// TestDmnModelListFailsWhenTheReferencesCannotBeRead: whether a model is referenced
// is what decides if it may be deleted, and what the list reports. Neither may be
// answered from a reference store that cannot be read — the delete least of all,
// since it would remove a model somebody's reference still resolves through.
func TestDmnModelListFailsWhenTheReferencesCannotBeRead(t *testing.T) {
	srv, _ := newValidateServer(t)
	approvalsPathsDirAsFile(t, srv.dmnrefs.Dir())
	x := deployTestHarness{t, srv.Handler()}

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		path := "/api/v1/dmn-models"
		if method == http.MethodDelete {
			path += "/dish"
		}
		code, body := x.do(method, path, "")
		if code != http.StatusInternalServerError || !strings.Contains(string(body), "list dmn references") {
			t.Errorf("%s: %d (%s), want 500 'list dmn references'", method, code, body)
		}
	}
	if _, err := os.Stat(filepath.Join(dmnModelListPathsDir(t, srv), "dish.dmn")); err != nil {
		t.Errorf("the model was deleted although its references could not be checked: %v", err)
	}
}

// TestDmnModelListFailsOnAModelThatCannotBeRead: a handle whose .dmn is a folder
// rather than a file cannot be read, and that is a fault in the store — not a model
// that "does not compile", which is what listing it as invalid would claim. Deleting
// it fails the same way rather than reporting a removal that did not happen.
func TestDmnModelListFailsOnAModelThatCannotBeRead(t *testing.T) {
	srv, _ := newValidateServer(t)
	dir := dmnModelListPathsDir(t, srv)
	if err := os.MkdirAll(filepath.Join(dir, "odd.dmn", "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "odd.xml"), []byte(validDMNModel), 0o644); err != nil {
		t.Fatal(err)
	}
	x := deployTestHarness{t, srv.Handler()}

	code, body := x.do(http.MethodGet, "/api/v1/dmn-models", "")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "resolve dmn model") {
		t.Errorf("list: %d (%s), want 500 'resolve dmn model'", code, body)
	}
	code, body = x.do(http.MethodDelete, "/api/v1/dmn-models/odd", "")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "delete model") {
		t.Errorf("delete: %d (%s), want 500 'delete model'", code, body)
	}
}
