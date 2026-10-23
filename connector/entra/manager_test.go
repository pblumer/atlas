package entra

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
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
	// A process that catches nothing gets an ordinary failure, with its retries.
	var be *job.BPMNError
	if errors.As(err, &be) {
		t.Errorf("err = %v is a BPMN error, but the process catches no code", err)
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

// notFoundServer answers every request 404 with the given body, as Graph answers a read
// of a user that does not exist.
func notFoundServer(t *testing.T, body string) Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewGraphClient(staticToken{tok: "t"}, srv.URL, http.DefaultClient)
}

const graphUserNotFound = `{"error":{"code":"Request_ResourceNotFound","message":"Resource 'nobody@contoso.com' does not exist or one of its queried reference-property objects are not present."}}`

// A process that catches ENTRA_USER_NOT_FOUND gets an unknown user as that BPMN error,
// so it can route the order to a person who decides, the way it routes a person
// without a manager — at once, with no retry spent on an answer that will not change
// (ADR-0461's rule, ADR-0172 amended 2026-10-22).
//
// The message names no user. It is kept with the error, and the id it would name is
// often the very value a process declares personal and keeps enciphered.
func TestAnUnknownUserIsThrownWhenTheProcessCatchesIt(t *testing.T) {
	j := managerJob()
	j.UserID = "nobody@contoso.com"
	j.ErrorCodes = []string{"SOMETHING_ELSE", CodeUserNotFound}
	_, err := Run(context.Background(), j, regWith("contoso", notFoundServer(t, graphUserNotFound)))
	var be *job.BPMNError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want a BPMN error the process catches", err)
	}
	if be.Code != CodeUserNotFound {
		t.Errorf("code = %q, want %s", be.Code, CodeUserNotFound)
	}
	if strings.Contains(be.Message, "nobody") {
		t.Errorf("message = %q names the user; it is kept with the error", be.Message)
	}
}

// Everything else stays an ordinary failure, which keeps its retries and ends in an
// incident: a process that does not catch the code, a 404 that is not Graph saying the
// user does not exist, another status, and another operation.
func TestOnlyACaughtUnknownUserIsThrown(t *testing.T) {
	caught := []string{CodeUserNotFound}
	for _, tc := range []struct {
		name   string
		op     string
		codes  []string
		status int
		body   string
	}{
		{"the process catches nothing", "get-user-manager", nil, http.StatusNotFound, graphUserNotFound},
		{"a 404 without Graph's code", "get-user-manager", caught, http.StatusNotFound, "Not Found"},
		{"a refused read", "get-user-manager", caught, http.StatusForbidden, `{"error":{"code":"Authorization_RequestDenied","message":"Insufficient privileges"}}`},
		{"another operation", "get-user", caught, http.StatusNotFound, graphUserNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)
			c := NewGraphClient(staticToken{tok: "t"}, srv.URL, http.DefaultClient)
			j := managerJob()
			j.Operation, j.ErrorCodes = tc.op, tc.codes
			_, err := Run(context.Background(), j, regWith("contoso", c))
			if err == nil {
				t.Fatal("Run succeeded; want a failure")
			}
			var be *job.BPMNError
			if errors.As(err, &be) {
				t.Errorf("err = %v is a BPMN error; want an ordinary failure", err)
			}
		})
	}
}

// The codes a process catches travel with the job, because the worker has no compiled
// process to ask.
func TestResolveCarriesTheCaughtErrorCodes(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:error id="unbekannt" errorCode="ENTRA_USER_NOT_FOUND"/>
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:entraConnector connector="contoso" operation="get-user-manager" userId="=upn" resultVariable="chef"/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:boundaryEvent id="b" attachedToRef="t"><bpmn:errorEventDefinition errorRef="unbekannt"/></bpmn:boundaryEvent>
    <bpmn:endEvent id="e"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
    <bpmn:sequenceFlow id="f3" sourceRef="b" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := compiler.Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{
		1: {{Name: "upn", Kind: model.VarString, Text: "arno@contoso.com"}},
	}}
	j, err := Resolve(store, cp, cp.ConnectorTask(node.Detail), 1)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !slices.Contains(j.ErrorCodes, CodeUserNotFound) {
		t.Errorf("errorCodes = %v, want %s", j.ErrorCodes, CodeUserNotFound)
	}
	if j.UserID != "arno@contoso.com" {
		t.Errorf("userId = %q", j.UserID)
	}
}
