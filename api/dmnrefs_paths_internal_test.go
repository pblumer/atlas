package api

import (
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestDmnRefsReportsAnEditItCouldNotStore: a rename that did not land must not come
// back as the renamed reference — the Modeler would show a name the next reload takes
// away again.
func TestDmnRefsReportsAnEditItCouldNotStore(t *testing.T) {
	srv := newServerForErrors(t)
	before := dmnRef{ID: "ref-1", Name: "Eligibility", ModelRef: "eligibility", CreatedAt: 1}
	var err error
	srv.do(func() { err = srv.dmnrefs.Save(before) })
	if err != nil {
		t.Fatal(err)
	}
	// A directory where the store writes its temporary file: the reference reads,
	// the write fails.
	if err := os.MkdirAll(srv.dmnrefs.FileFor("ref-1")+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	code, body := serveInternal(t, srv, http.MethodPatch, "/api/v1/dmnrefs/ref-1", `{"name":"Renamed"}`, "application/json")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "update dmn reference") {
		t.Fatalf("unwritable reference: %d (%s), want 500", code, body)
	}
	var after dmnRef
	srv.do(func() { after, _, err = srv.dmnrefs.Get("ref-1") })
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("reference after a failed edit = %+v (%v), want it unchanged", after, err)
	}
}

// TestDmnRefsReportsADeleteItCouldNotMake: a delete that could not be made durable
// must not answer 204. The reference would vanish from the Modeler's list and come
// back after a restart, still resolving tasks to a model the author thought unhooked.
func TestDmnRefsReportsADeleteItCouldNotMake(t *testing.T) {
	srv := newServerForErrors(t)
	if err := os.RemoveAll(srv.dmnrefs.Dir()); err != nil {
		t.Fatal(err)
	}
	code, body := serveInternal(t, srv, http.MethodDelete, "/api/v1/dmnrefs/ref-1", "", "")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "delete dmn reference") {
		t.Errorf("unwritable store: %d (%s), want 500", code, body)
	}
}
