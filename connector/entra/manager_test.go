package entra

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func managerJob() Job {
	return Job{Connector: "contoso", Operation: "get-user-manager", UserID: "u1", ResultVariable: "chef"}
}

// The manager is read through the user — /users/{id}?$expand=manager — and not from
// /users/{id}/manager (ADR-0172, amended 2026-10-22). Graph answers that path 404 both
// for a person with no manager and for an id that is nobody's; through the user, the
// two stay apart.
//
// A select projects the manager, because the manager is what the result variable
// receives; the user it was read through is cut down to its id, which is all of it the
// read throws away.
func TestTheManagerIsReadThroughTheUser(t *testing.T) {
	for _, tc := range []struct {
		name   string
		userID string
		sel    string
		path   string
	}{
		{"no select", "u1", "", "/users/u1?$select=id&$expand=manager"},
		{"blank select", "u1", "   ", "/users/u1?$select=id&$expand=manager"},
		{"a select applies to the manager", "arno@contoso.com", "id,displayName,mail",
			"/users/arno@contoso.com?$select=id&$expand=manager($select=id%2CdisplayName%2Cmail)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := &recordingClient{res: map[string]any{"id": "u1"}}
			j := managerJob()
			j.UserID, j.Select = tc.userID, tc.sel
			if _, err := Run(context.Background(), j, regWith("contoso", rc)); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if rc.method != "GET" || rc.path != tc.path {
				t.Errorf("request = %s %s, want GET %s", rc.method, rc.path, tc.path)
			}
			if rc.body != nil || rc.binary {
				t.Errorf("body = %#v, binary = %v; a read sends no body and decodes JSON", rc.body, rc.binary)
			}
		})
	}
}

// The result variable receives the manager, not the user it was read through: a model
// that asked for the manager addresses =chef.mail, not =chef.manager.mail.
func TestTheManagerArrivesInPlaceOfTheUser(t *testing.T) {
	manager := map[string]any{"@odata.type": "#microsoft.graph.user", "id": "m1", "displayName": "Mara Muster", "mail": "mara@contoso.com"}
	rc := &recordingClient{res: map[string]any{"id": "u1", "manager": manager}}
	out, err := Run(context.Background(), managerJob(), regWith("contoso", rc))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, ok := out["chef"].(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want the manager object", out["chef"])
	}
	if got["id"] != "m1" || got["mail"] != "mara@contoso.com" {
		t.Errorf("result = %#v, want the manager", got)
	}
	if _, leaked := got["manager"]; leaked {
		t.Errorf("result = %#v carries the user's envelope, not the manager", got)
	}
}

// A person without a manager is an answer, not a failure — the chief executive has
// none, and neither has an account nobody has maintained yet. The result is null, so a
// model branches on `= chef = null` to a fallback approver instead of collecting an
// incident it cannot route around.
func TestAPersonWithoutAManagerIsNull(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  map[string]any
	}{
		{"property absent", map[string]any{"id": "u1"}},
		{"property null", map[string]any{"id": "u1", "manager": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Run(context.Background(), managerJob(), regWith("contoso", &recordingClient{res: tc.res}))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			v, present := out["chef"]
			if !present || v != nil {
				t.Errorf("result = %#v (present %v), want the variable set to null", v, present)
			}
		})
	}
}

// An id that is nobody's fails the job. This is what reading through the user buys over
// /users/{id}/manager: there, the same 404 would have read as "no manager", and an
// approval would have gone to the fallback approver for a person who does not exist.
func TestAnUnknownUserIsAFailureNotAMissingManager(t *testing.T) {
	var gotQuery, gotConsistency string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotConsistency = r.Header.Get("ConsistencyLevel")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"Request_ResourceNotFound","message":"Resource 'nobody@contoso.com' does not exist or one of its queried reference-property objects are not present."}}`))
	}))
	t.Cleanup(srv.Close)
	c := NewGraphClient(staticToken{tok: "t"}, srv.URL, http.DefaultClient)
	j := managerJob()
	j.UserID = "nobody@contoso.com"
	out, err := Run(context.Background(), j, regWith("contoso", c))
	if err == nil {
		t.Fatalf("Run = %#v, nil; an unknown user must fail the job, not read as no manager", out)
	}
	if !strings.Contains(err.Error(), "Request_ResourceNotFound") {
		t.Errorf("err = %v, want Graph's code in it", err)
	}
	// The plain expansion of the immediate manager is not an advanced query: Graph asks
	// for ConsistencyLevel only with $levels, which this read does not send.
	if gotConsistency != "" {
		t.Errorf("ConsistencyLevel = %q, want none", gotConsistency)
	}
	if !strings.Contains(gotQuery, "$expand=manager") {
		t.Errorf("query = %q, want the manager expanded", gotQuery)
	}
}

// What Graph receives over the wire, decoded: the select reaches the manager intact.
func TestTheManagerSelectReachesGraph(t *testing.T) {
	var sel, expand string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sel, expand = r.URL.Query().Get("$select"), r.URL.Query().Get("$expand")
		_, _ = w.Write([]byte(`{"id":"u1","manager":{"id":"m1","mail":"mara@contoso.com"}}`))
	}))
	t.Cleanup(srv.Close)
	c := NewGraphClient(staticToken{tok: "t"}, srv.URL, http.DefaultClient)
	j := managerJob()
	j.Select = "id,mail"
	out, err := Run(context.Background(), j, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sel != "id" || expand != "manager($select=id,mail)" {
		t.Errorf("$select = %q, $expand = %q; want id and manager($select=id,mail)", sel, expand)
	}
	if m, _ := out["chef"].(map[string]any); m["mail"] != "mara@contoso.com" {
		t.Errorf("result = %#v, want the manager", out["chef"])
	}
}

// An answer that is not a user carrying an object there is an error rather than a
// null. Reading a malformed response as "no manager" is a wrong answer a process would
// route an approval on.
func TestAMalformedManagerAnswerFails(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  any
	}{
		{"no body", nil},
		{"not an object", []any{"u1"}},
		{"manager is not an object", map[string]any{"id": "u1", "manager": "m1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Run(context.Background(), managerJob(), regWith("contoso", &recordingClient{res: tc.res}))
			if err == nil {
				t.Errorf("Run = %#v, nil; want an error", out)
			}
		})
	}
}

// A read of the manager that discards it is one nothing asked for, so the worker
// refuses a job without a result variable, as the compiler does at deploy.
func TestTheManagerNeedsAResultVariable(t *testing.T) {
	j := managerJob()
	j.ResultVariable = ""
	rc := &recordingClient{res: map[string]any{"id": "u1"}}
	if _, err := Run(context.Background(), j, regWith("contoso", rc)); err == nil || !strings.Contains(err.Error(), "resultVariable") {
		t.Errorf("err = %v, want the missing resultVariable named", err)
	}
	if rc.method != "" {
		t.Errorf("Graph was called (%s %s) for a job that could not keep the answer", rc.method, rc.path)
	}
}
