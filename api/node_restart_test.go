package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// signedIn logs the built-in administrator in against a server started by
// newServerOn, which sets those credentials.
func signedIn(t *testing.T, ts *httptest.Server) *http.Client {
	t.Helper()
	c := newClient(t)
	if code := login(t, c, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("login: status = %d", code)
	}
	return c
}

// nodeAs reads the descriptor as a signed-in caller.
func nodeAs(t *testing.T, c *http.Client, ts *httptest.Server) nodeDescriptor {
	t.Helper()
	code, body := cReq(t, c, ts, http.MethodGet, "/api/v1/node", "")
	if code != http.StatusOK {
		t.Fatalf("GET node: status = %d, body = %s", code, body)
	}
	var n nodeDescriptor
	if err := json.Unmarshal(body, &n); err != nil {
		t.Fatalf("decode node: %v (%s)", err, body)
	}
	return n
}

// TestNodeIdentitySurvivesARestart is the whole claim of the word "stable". An id
// regenerated on every start would be worse than no id at all: every correlation
// made yesterday would point at a node that no longer exists, and one server would
// look to a landscape view like an endless series of new ones.
//
// It runs against a data directory that outlives the process, which is the only
// way to test the property rather than the code path that implements it.
func TestNodeIdentitySurvivesARestart(t *testing.T) {
	dir := t.TempDir()

	first := newServerOn(t, dir)
	admin := signedIn(t, first.Server)
	code, body := cReq(t, admin, first.Server, http.MethodPut, "/api/v1/node",
		`{"name":"Zurich primary","environment":"production"}`)
	if code != http.StatusOK {
		t.Fatalf("name the node: status = %d, body = %s", code, body)
	}
	before := nodeAs(t, admin, first.Server)
	first.stop()

	second := newServerOn(t, dir)
	defer second.stop()
	after := nodeAs(t, signedIn(t, second.Server), second.Server)

	if after.ID != before.ID {
		t.Fatalf("the node came back as a different runtime: %q -> %q", before.ID, after.ID)
	}
	// The operator's name comes back with it. An id nobody recognises is
	// correlatable but not readable, and re-typing the name after every restart is
	// how a landscape view fills up with nodes called nothing.
	if after.Name != "Zurich primary" || after.Environment != "production" {
		t.Errorf("restarted node = %+v, want the name and environment kept", after)
	}
}
