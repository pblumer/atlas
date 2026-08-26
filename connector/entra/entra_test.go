package entra

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// staticToken is a TokenSource that hands out a fixed bearer, so the client tests are
// about Graph and not about OAuth (which connector/oauth2 covers on its own).
type staticToken struct {
	tok string
	err error
}

func (s staticToken) Token(context.Context) (string, error) { return s.tok, s.err }

// graphServer records what the client sent and answers with a canned response.
type graphServer struct {
	srv      *httptest.Server
	method   string
	path     string
	auth     string
	body     string
	status   int
	response string
}

func newGraphServer(t *testing.T) *graphServer {
	t.Helper()
	g := &graphServer{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.method, g.path, g.auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		buf := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(buf)
		}
		g.body = string(buf)
		if g.status != 0 {
			w.WriteHeader(g.status)
		}
		if g.response != "" {
			_, _ = w.Write([]byte(g.response))
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *graphServer) client() *GraphClient {
	return NewGraphClient(staticToken{tok: "tok-1"}, g.srv.URL, http.DefaultClient)
}

func TestGraphClientSendsABearerToken(t *testing.T) {
	g := newGraphServer(t)
	g.response = `{"id":"abc"}`
	got, err := g.client().Call(context.Background(), Request{Method: "GET", Path: "/users/arno@contoso.com"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if g.auth != "Bearer tok-1" {
		t.Errorf("Authorization = %q", g.auth)
	}
	if g.path != "/users/arno@contoso.com" {
		t.Errorf("path = %q", g.path)
	}
	obj, ok := got.(map[string]any)
	if !ok || obj["id"] != "abc" {
		t.Errorf("result = %#v", got)
	}
}

// Graph answers a DELETE and most PATCHes with 204 and no body. That is a success
// with nothing to report, not a decode failure.
func TestGraphClientEmptyBodyIsSuccess(t *testing.T) {
	g := newGraphServer(t)
	g.status = 204
	got, err := g.client().Call(context.Background(), Request{Method: "DELETE", Path: "/users/x"})
	if err != nil || got != nil {
		t.Errorf("Call = %#v, %v; want nil, nil", got, err)
	}
}

// A Graph error envelope is unwrapped, because "Request_BadRequest: Property netId is
// invalid" is the whole of what an operator needs and "HTTP 400" is not.
func TestGraphClientSurfacesTheErrorCode(t *testing.T) {
	g := newGraphServer(t)
	g.status = 400
	g.response = `{"error":{"code":"Request_BadRequest","message":"Property netId is invalid."}}`
	_, err := g.client().Call(context.Background(), Request{Method: "POST", Path: "/users", Body: map[string]any{"x": 1}})
	if err == nil {
		t.Fatal("a 400 must be an error")
	}
	if !strings.Contains(err.Error(), "Request_BadRequest") || !strings.Contains(err.Error(), "netId") {
		t.Errorf("error should carry Graph's code and message: %v", err)
	}
}

// A non-2xx whose body is not a Graph envelope still reports the body verbatim.
func TestGraphClientNonEnvelopeError(t *testing.T) {
	g := newGraphServer(t)
	g.status = 502
	g.response = "upstream boom"
	_, err := g.client().Call(context.Background(), Request{Method: "GET", Path: "/users/x"})
	if err == nil || !strings.Contains(err.Error(), "upstream boom") {
		t.Errorf("err = %v, want the raw body", err)
	}
}

func TestGraphClientErrors(t *testing.T) {
	g := newGraphServer(t)
	// A token that cannot be acquired fails before any request is made.
	bad := NewGraphClient(staticToken{err: errors.New("no token")}, g.srv.URL, http.DefaultClient)
	if _, err := bad.Call(context.Background(), Request{Method: "GET", Path: "/users/x"}); err == nil {
		t.Error("a token failure must fail the call")
	}
	// A body that cannot be encoded.
	if _, err := g.client().Call(context.Background(), Request{Method: "POST", Path: "/users", Body: map[string]any{"bad": make(chan int)}}); err == nil {
		t.Error("an unencodable body must fail")
	}
	// An unbuildable request.
	if _, err := g.client().Call(context.Background(), Request{Method: "\n", Path: "/users"}); err == nil {
		t.Error("an invalid method must fail")
	}
	// An unreachable host.
	down := NewGraphClient(staticToken{tok: "t"}, "http://127.0.0.1:1", http.DefaultClient)
	if _, err := down.Call(context.Background(), Request{Method: "GET", Path: "/users/x"}); err == nil {
		t.Error("an unreachable host must fail")
	}
	// A 2xx body that is not JSON.
	g2 := newGraphServer(t)
	g2.response = "not json"
	if _, err := g2.client().Call(context.Background(), Request{Method: "GET", Path: "/users/x"}); err == nil {
		t.Error("a non-JSON 2xx body must fail")
	}
}

// The default base URL is the worldwide cloud; a national cloud overrides it, which
// is why the address is configurable at all.
func TestGraphClientBaseURL(t *testing.T) {
	if got := NewGraphClient(staticToken{}, "", nil).BaseURL(); got != DefaultBaseURL {
		t.Errorf("default base = %q, want %q", got, DefaultBaseURL)
	}
	if got := NewGraphClient(staticToken{}, "https://graph.microsoft.us/v1.0/", nil).BaseURL(); got != "https://graph.microsoft.us/v1.0" {
		t.Errorf("base = %q, want the trailing slash trimmed", got)
	}
}

// recordingClient captures what Run asked Graph to do, so the operation table is
// assertable without HTTP.
type recordingClient struct {
	method, path string
	body         any
	res          any
	err          error
}

func (c *recordingClient) BaseURL() string { return "https://graph.microsoft.com/v1.0" }
func (c *recordingClient) Call(_ context.Context, r Request) (any, error) {
	c.method, c.path, c.body = r.Method, r.Path, r.Body
	return c.res, c.err
}

func regWith(name string, c Client) *Registry {
	r := NewRegistry()
	r.Register(name, c)
	return r
}

// Every operation maps to the Graph request an operator would otherwise have to
// hand-author — which is the reason this connector exists rather than a REST task.
func TestRunMapsEveryOperation(t *testing.T) {
	for _, tc := range []struct {
		op         string
		job        Job
		method     string
		path       string
		wantBodyIs func(any) bool
	}{
		{op: "create-user",
			job:    Job{Attributes: map[string]any{"displayName": "Arno"}},
			method: "POST", path: "/users",
			wantBodyIs: func(b any) bool { m, ok := b.(map[string]any); return ok && m["displayName"] == "Arno" }},
		{op: "get-user", job: Job{UserID: "arno@contoso.com"},
			method: "GET", path: "/users/arno@contoso.com",
			wantBodyIs: func(b any) bool { return b == nil }},
		{op: "list-users", job: Job{ResultVariable: "leute"},
			method: "GET", path: "/users",
			wantBodyIs: func(b any) bool { return b == nil }},
		{op: "delta-users", job: Job{ResultVariable: "aenderungen"},
			method: "GET", path: "/users/delta",
			wantBodyIs: func(b any) bool { return b == nil }},
		{op: "delta-groups", job: Job{ResultVariable: "aenderungen"},
			method: "GET", path: "/groups/delta",
			wantBodyIs: func(b any) bool { return b == nil }},
		{op: "update-user",
			job:    Job{UserID: "u1", Attributes: map[string]any{"department": "IT"}},
			method: "PATCH", path: "/users/u1",
			wantBodyIs: func(b any) bool { m, ok := b.(map[string]any); return ok && m["department"] == "IT" }},
		{op: "delete-user", job: Job{UserID: "u1"},
			method: "DELETE", path: "/users/u1",
			wantBodyIs: func(b any) bool { return b == nil }},
		{op: "enable", job: Job{UserID: "u1"},
			method: "PATCH", path: "/users/u1",
			wantBodyIs: func(b any) bool { m, ok := b.(map[string]any); return ok && m["accountEnabled"] == true }},
		{op: "disable", job: Job{UserID: "u1"},
			method: "PATCH", path: "/users/u1",
			wantBodyIs: func(b any) bool { m, ok := b.(map[string]any); return ok && m["accountEnabled"] == false }},
		{op: "add-group-member", job: Job{UserID: "u1", GroupID: "g1"},
			method: "POST", path: "/groups/g1/members/$ref",
			wantBodyIs: func(b any) bool {
				m, ok := b.(map[string]any)
				return ok && m["@odata.id"] == "https://graph.microsoft.com/v1.0/directoryObjects/u1"
			}},
		{op: "remove-group-member", job: Job{UserID: "u1", GroupID: "g1"},
			method: "DELETE", path: "/groups/g1/members/u1/$ref",
			wantBodyIs: func(b any) bool { return b == nil }},
		{op: "create-group",
			job:    Job{Attributes: map[string]any{"displayName": "Sales"}},
			method: "POST", path: "/groups",
			wantBodyIs: func(b any) bool { m, ok := b.(map[string]any); return ok && m["displayName"] == "Sales" }},
		{op: "delete-group", job: Job{GroupID: "g1"},
			method: "DELETE", path: "/groups/g1",
			wantBodyIs: func(b any) bool { return b == nil }},
		{op: "reset-password", job: Job{UserID: "u1", NewPassword: "S3cret!"},
			method: "PATCH", path: "/users/u1",
			wantBodyIs: func(b any) bool {
				m, ok := b.(map[string]any)
				if !ok {
					return false
				}
				pp, ok := m["passwordProfile"].(map[string]any)
				return ok && pp["password"] == "S3cret!" && pp["forceChangePasswordNextSignIn"] == true
			}},
		// A team's id is its group's id, so create-team teamifies the group and
		// add-team-member addresses /teams/{groupId}.
		{op: "create-team", job: Job{GroupID: "g1"},
			method: "PUT", path: "/groups/g1/team",
			wantBodyIs: func(b any) bool { m, ok := b.(map[string]any); _, has := m["memberSettings"]; return ok && has }},
		{op: "add-team-member", job: Job{UserID: "u1", GroupID: "g1"},
			method: "POST", path: "/teams/g1/members",
			wantBodyIs: func(b any) bool {
				m, ok := b.(map[string]any)
				roles, _ := m["roles"].([]any)
				return ok && m["@odata.type"] == "#microsoft.graph.aadUserConversationMember" &&
					len(roles) == 0 && m["user@odata.bind"] == "https://graph.microsoft.com/v1.0/users('u1')"
			}},
		{op: "add-team-owner", job: Job{UserID: "u1", GroupID: "g1"},
			method: "POST", path: "/teams/g1/members",
			wantBodyIs: func(b any) bool {
				m, ok := b.(map[string]any)
				roles, _ := m["roles"].([]any)
				return ok && len(roles) == 1 && roles[0] == "owner"
			}},
		{op: "create-channel",
			job:    Job{GroupID: "g1", Attributes: map[string]any{"displayName": "General"}},
			method: "POST", path: "/teams/g1/channels",
			wantBodyIs: func(b any) bool { m, ok := b.(map[string]any); return ok && m["displayName"] == "General" }},
		{op: "archive-team", job: Job{GroupID: "g1"},
			method: "POST", path: "/teams/g1/archive",
			wantBodyIs: func(b any) bool { return b == nil }},
		{op: "get-group", job: Job{GroupID: "g1"},
			method: "GET", path: "/groups/g1",
			wantBodyIs: func(b any) bool { return b == nil }},
		{op: "list-groups", job: Job{ResultVariable: "gruppen"},
			method: "GET", path: "/groups",
			wantBodyIs: func(b any) bool { return b == nil }},
		{op: "update-group",
			job:    Job{GroupID: "g1", Attributes: map[string]any{"displayName": "Vertrieb"}},
			method: "PATCH", path: "/groups/g1",
			wantBodyIs: func(b any) bool { m, ok := b.(map[string]any); return ok && m["displayName"] == "Vertrieb" }},
		{op: "add-group-owner", job: Job{UserID: "u1", GroupID: "g1"},
			method: "POST", path: "/groups/g1/owners/$ref",
			wantBodyIs: func(b any) bool {
				m, ok := b.(map[string]any)
				return ok && m["@odata.id"] == "https://graph.microsoft.com/v1.0/directoryObjects/u1"
			}},
		{op: "remove-group-owner", job: Job{UserID: "u1", GroupID: "g1"},
			method: "DELETE", path: "/groups/g1/owners/u1/$ref",
			wantBodyIs: func(b any) bool { return b == nil }},
		{op: "assign-license",
			job:    Job{UserID: "u1", Attributes: map[string]any{"addLicenses": []any{}, "removeLicenses": []any{}}},
			method: "POST", path: "/users/u1/assignLicense",
			wantBodyIs: func(b any) bool { m, ok := b.(map[string]any); _, has := m["addLicenses"]; return ok && has }},
		{op: "assign-role",
			job:    Job{UserID: "u1", Attributes: map[string]any{"roleDefinitionId": "62e90394-…"}},
			method: "POST", path: "/roleManagement/directory/roleAssignments",
			wantBodyIs: func(b any) bool {
				m, ok := b.(map[string]any)
				return ok && m["principalId"] == "u1" && m["directoryScopeId"] == "/" && m["roleDefinitionId"] == "62e90394-…"
			}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			res := map[string]any{"id": "x"}
			switch {
			case Ops[tc.op].IsDelta:
				// A delta's last page carries the cursor in place of a next link.
				res = map[string]any{
					"value":            []any{map[string]any{"id": "x"}},
					"@odata.deltaLink": "https://graph.microsoft.com/v1.0/users/delta?$deltatoken=t0",
				}
			case Ops[tc.op].IsList:
				res = map[string]any{"value": []any{map[string]any{"id": "x"}}}
			}
			c := &recordingClient{res: res}
			j := tc.job
			j.Connector, j.Operation = "contoso", tc.op
			if _, err := Run(context.Background(), j, regWith("contoso", c)); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if c.method != tc.method || c.path != tc.path {
				t.Errorf("request = %s %s, want %s %s", c.method, c.path, tc.method, tc.path)
			}
			if !tc.wantBodyIs(c.body) {
				t.Errorf("body = %#v", c.body)
			}
		})
	}
	// Every operation in the table is covered above; a new one must be added here too.
	if len(Ops) != 26 {
		t.Errorf("Ops has %d operations; add the new one to this test", len(Ops))
	}
}

func TestRunWritesTheResultVariable(t *testing.T) {
	c := &recordingClient{res: map[string]any{"id": "abc"}}
	got, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "get-user", UserID: "u1", ResultVariable: "person",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	obj, ok := got["person"].(map[string]any)
	if !ok || obj["id"] != "abc" {
		t.Errorf("result = %#v", got)
	}
	// With no result variable the call still happens; there is just nothing to write.
	got2, err := Run(context.Background(), Job{Connector: "contoso", Operation: "delete-user", UserID: "u1"}, regWith("contoso", c))
	if err != nil || got2 != nil {
		t.Errorf("result = %#v, %v; want nil, nil", got2, err)
	}
}

func TestRunErrors(t *testing.T) {
	c := &recordingClient{}
	// An unconfigured name is the actionable failure and is reported first.
	if _, err := Run(context.Background(), Job{Connector: "nope", Operation: "get-user", UserID: "u"}, NewRegistry()); err == nil {
		t.Error("an unregistered connector must fail")
	}
	if _, err := Run(context.Background(), Job{Connector: "contoso", Operation: "rename"}, regWith("contoso", c)); err == nil {
		t.Error("an unknown operation must fail")
	}
	// The compiler catches an under-specified model; this catches a hand-built job,
	// turning what would be a confusing Graph 404 into a named missing field.
	for _, j := range []Job{
		{Connector: "contoso", Operation: "get-user"},
		{Connector: "contoso", Operation: "add-group-member", UserID: "u"},
		{Connector: "contoso", Operation: "create-user"},
	} {
		if _, err := Run(context.Background(), j, regWith("contoso", c)); err == nil {
			t.Errorf("job %+v must fail a required-field check", j)
		}
	}
	// A Graph failure fails the job.
	boom := &recordingClient{err: errors.New("graph down")}
	if _, err := Run(context.Background(), Job{Connector: "contoso", Operation: "get-user", UserID: "u"}, regWith("contoso", boom)); err == nil {
		t.Error("a Graph error must fail the job")
	}
}

// --- the engine half ---

type fakeVarStore struct {
	vars    map[uint64][]model.VariableValue
	ei      map[uint64]model.ElementInstanceValue
	varsErr error
	eiErr   error
}

func (f fakeVarStore) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	if f.varsErr != nil {
		return f.varsErr
	}
	for i := range f.vars[scope] {
		v := f.vars[scope][i]
		if err := fn(&v); err != nil {
			return err
		}
	}
	return nil
}

func (f fakeVarStore) GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error) {
	if f.eiErr != nil {
		return nil, false, f.eiErr
	}
	ei, ok := f.ei[key]
	if !ok {
		return nil, false, nil
	}
	return &ei, true, nil
}

func buildTask(t *testing.T, cfg compiler.EntraConfig) (*compiler.CompiledProcess, *compiler.ConnectorTaskDetail) {
	t.Helper()
	b := compiler.NewBuilder(1, "p", 1)
	el := b.AddEntraConnectorTask(cfg)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(el).Detail)
}

func TestResolveProducesAPlainJob(t *testing.T) {
	cp, d := buildTask(t, compiler.EntraConfig{
		Connector: "contoso", Op: "create-user",
		AttributesVar: "neuerBenutzer", ResultVar: "angelegt",
	})
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{
		10: {{Name: "neuerBenutzer", Kind: model.VarJSON, Text: `{"displayName":"Arno","accountEnabled":true}`}},
	}}
	j, err := Resolve(store, cp, d, 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if j.Connector != "contoso" || j.Operation != "create-user" || j.ResultVariable != "angelegt" {
		t.Errorf("job = %+v", j)
	}
	if j.Attributes["displayName"] != "Arno" || j.Attributes["accountEnabled"] != true {
		t.Errorf("attributes = %#v", j.Attributes)
	}
	// There is nowhere in a Job to put a tenant id or a client secret; this asserts
	// the resolved payload really is credential-free.
	raw, _ := json.Marshal(j)
	for _, forbidden := range []string{"tenant", "clientSecret", "secret"} {
		if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(forbidden)) {
			t.Errorf("the resolved job carries %q: %s", forbidden, raw)
		}
	}
}

// A FEEL-authored id is evaluated against the instance's variables — engine work,
// because only the engine has the compiled expression and the scope.
func TestResolveEvaluatesFEELIds(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>
      <atlas:entraConnector connector="contoso" operation="disable" userId="=person.upn"/>
    </bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := compiler.Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{
		1: {{Name: "person", Kind: model.VarJSON, Text: `{"upn":"arno@contoso.com"}`}},
	}}
	j, err := Resolve(store, cp, cp.ConnectorTask(node.Detail), 1)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if j.UserID != "arno@contoso.com" {
		t.Errorf("userId = %q, want the evaluated UPN", j.UserID)
	}
}

// A connector authored as a FEEL expression resolves the tenant name from the
// instance's variables at call time, so one process can serve several tenants
// (ADR-0172). An expression that evaluates to nothing is refused at resolve, so the
// incident names this task rather than the worker later failing to find a connector
// called "".
func TestResolveEvaluatesTheConnectorExpression(t *testing.T) {
	bpmn := func(connector string) string {
		return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>
      <atlas:entraConnector connector="` + connector + `" operation="disable" userId="=upn"/>
    </bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	}
	taskOf := func(t *testing.T, xml string) (*compiler.CompiledProcess, *compiler.ConnectorTaskDetail) {
		cp, err := compiler.Parse(1, 1, strings.NewReader(xml))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
		return cp, cp.ConnectorTask(node.Detail)
	}

	// The expression reads a variable and picks the tenant.
	cp, d := taskOf(t, bpmn("=tenant"))
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{
		1: {{Name: "tenant", Kind: model.VarString, Text: "blumer_net"}, {Name: "upn", Kind: model.VarString, Text: "arno@blumer.net"}},
	}}
	j, err := Resolve(store, cp, d, 1)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if j.Connector != "blumer_net" {
		t.Errorf("connector = %q, want the resolved tenant blumer_net", j.Connector)
	}

	// A resolved-empty tenant name is refused rather than sent.
	cp, d = taskOf(t, bpmn("=tenant"))
	empty := fakeVarStore{vars: map[uint64][]model.VariableValue{
		1: {{Name: "upn", Kind: model.VarString, Text: "arno@blumer.net"}},
	}}
	if _, err := Resolve(empty, cp, d, 1); err == nil {
		t.Error("a connector expression that evaluates to nothing should be an error")
	}

	// A static connector name still resolves verbatim — the common path is unchanged.
	cp, d = taskOf(t, bpmn("contoso"))
	if j, err := Resolve(store, cp, d, 1); err != nil || j.Connector != "contoso" {
		t.Errorf("static connector: j.Connector=%q err=%v, want contoso/nil", j.Connector, err)
	}
}

// Inline attributes (the modeler's JSON template) are evaluated at resolve time: the
// FEEL leaves resolve against the instance's variables, and the nested passwordProfile
// survives, so a create-user body is built from the joiner's own data with no separate
// variable.
func TestResolveEvaluatesInlineAttributes(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>
      <atlas:entraConnector connector="contoso" operation="create-user" resultVariable="konto"
        attributes="{&quot;displayName&quot;:&quot;=vorname&quot;,&quot;accountEnabled&quot;:true,&quot;passwordProfile&quot;:{&quot;password&quot;:&quot;=pw&quot;,&quot;forceChangePasswordNextSignIn&quot;:true}}"/>
    </bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := compiler.Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{
		1: {
			{Name: "vorname", Kind: model.VarString, Text: "Arno"},
			{Name: "pw", Kind: model.VarString, Text: "Temp1!"},
		},
	}}
	j, err := Resolve(store, cp, cp.ConnectorTask(node.Detail), 1)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if j.Attributes["displayName"] != "Arno" || j.Attributes["accountEnabled"] != true {
		t.Errorf("attributes = %#v", j.Attributes)
	}
	pp, ok := j.Attributes["passwordProfile"].(map[string]any)
	if !ok || pp["password"] != "Temp1!" || pp["forceChangePasswordNextSignIn"] != true {
		t.Errorf("passwordProfile = %#v", j.Attributes["passwordProfile"])
	}
}

func TestResolveErrors(t *testing.T) {
	cp, d := buildTask(t, compiler.EntraConfig{Connector: "c", Op: "create-user", AttributesVar: "attrs"})
	if _, err := Resolve(fakeVarStore{}, cp, nil, 1); err == nil {
		t.Error("a nil detail must fail")
	}
	if _, err := Resolve(fakeVarStore{}, cp, d, 1); err == nil {
		t.Error("a missing attributes variable must fail")
	}
	for _, v := range []model.VariableValue{
		{Name: "attrs", Kind: model.VarString, Text: "nope"},
		{Name: "attrs", Kind: model.VarJSON, Text: `["a","b"]`},
		{Name: "attrs", Kind: model.VarJSON, Text: `null`},
	} {
		store := fakeVarStore{vars: map[uint64][]model.VariableValue{1: {v}}}
		if _, err := Resolve(store, cp, d, 1); err == nil {
			t.Errorf("attributes %v must be refused: Graph takes an object", v.Text)
		}
	}
	if _, err := Resolve(fakeVarStore{varsErr: errors.New("x")}, cp, d, 1); err == nil {
		t.Error("a variables error must fail the resolve")
	}
	if _, err := Resolve(fakeVarStore{eiErr: errors.New("x")}, cp, d, 1); err == nil {
		t.Error("an element-instance error must fail the resolve")
	}
}

// A detail whose job type is not Entra is not an Entra task.
func TestResolveRejectsAForeignJobType(t *testing.T) {
	b := compiler.NewBuilder(1, "p", 1)
	el := b.AddSqlConnectorTask(compiler.SqlConfig{
		JobType: compiler.PostgresJobType, Connector: "db", Op: "query",
		Statement: "SELECT 1", ResultVar: "r",
	})
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := Resolve(fakeVarStore{}, cp, cp.ConnectorTask(cp.Node(el).Detail), 1); err == nil {
		t.Error("a SQL task must not resolve as Entra")
	}
}

// An attributes variable on an outer scope is visible at the task.
func TestResolveWalksTheScopeChain(t *testing.T) {
	cp, d := buildTask(t, compiler.EntraConfig{Connector: "c", Op: "create-user", AttributesVar: "attrs"})
	store := fakeVarStore{
		vars: map[uint64][]model.VariableValue{99: {{Name: "attrs", Kind: model.VarJSON, Text: `{"displayName":"A"}`}}},
		ei:   map[uint64]model.ElementInstanceValue{10: {FlowScopeKey: 99}},
	}
	j, err := Resolve(store, cp, d, 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if j.Attributes["displayName"] != "A" {
		t.Errorf("attributes = %#v, want the outer scope's", j.Attributes)
	}
}

func TestOpNames(t *testing.T) {
	got := OpNames()
	if len(got) != len(Ops) || got[0] != "add-group-member" {
		t.Errorf("OpNames() = %v, want the operations sorted", got)
	}
}

// Every variable kind binds into a FEEL expression, and the reserved
// processInstanceKey name binds to the scope's own key. Exercised through a real
// compiled expression rather than by calling the helpers directly, because the point
// is that an authored id actually resolves.
func TestResolveBindsEveryVariableKind(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>
      <atlas:entraConnector connector="contoso" operation="add-group-member"
                            userId="=string(zahl) + text + string(flagge) + obj.a + processInstanceKey"
                            groupId="=fehlt"/>
    </bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := compiler.Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{
		42: {
			{Name: "zahl", Kind: model.VarNumber, Text: "7"},
			{Name: "text", Kind: model.VarString, Text: "-abc-"},
			{Name: "flagge", Kind: model.VarBool, Bool: true},
			{Name: "obj", Kind: model.VarJSON, Text: `{"a":"-json-"}`},
			{Name: "leer", Kind: model.VarNull},
		},
	}}
	j, err := Resolve(store, cp, cp.ConnectorTask(node.Detail), 42)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(j.UserID, "-abc-") || !strings.Contains(j.UserID, "-json-") || !strings.Contains(j.UserID, "42") {
		t.Errorf("userId = %q, want every bound kind and the instance key in it", j.UserID)
	}
	// An expression over an unbound name is FEEL null, which becomes the empty
	// string — and the required-field check is then what reports it by name.
	if j.GroupID != "" {
		t.Errorf("groupId = %q, want empty for an unbound name", j.GroupID)
	}
	if _, err := Run(context.Background(), j, regWith("contoso", &recordingClient{})); err == nil ||
		!strings.Contains(err.Error(), "groupId") {
		t.Errorf("err = %v, want the missing groupId named", err)
	}
}

// A response whose body cannot be read is a failed call, not an empty result.
func TestGraphClientUnreadableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "50")
		_, _ = w.Write([]byte("short"))
		if h, ok := w.(http.Hijacker); ok {
			if conn, _, err := h.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
	}))
	t.Cleanup(srv.Close)
	c := NewGraphClient(staticToken{tok: "t"}, srv.URL, http.DefaultClient)
	if _, err := c.Call(context.Background(), Request{Method: "GET", Path: "/users/x"}); err == nil {
		t.Error("a truncated body must fail the call")
	}
}

// TestEntraOpsMatchTheConnector is the drift guard between this package's [Ops] table
// and the compiler's own copy of the operation rules.
//
// The compiler cannot import this package — connector/entra imports compiler, so the
// dependency only runs one way — which is why the rules exist twice. The check is
// therefore behavioural rather than structural: for every operation, a model
// supplying exactly what Ops says is required must compile, and a model missing any
// one of those fields must not. Two tables that disagree fail here rather than in a
// process that deploys and then cannot run.
func TestEntraOpsMatchTheConnector(t *testing.T) {
	attrsFor := func(op string, spec Op, omit string) string {
		parts := []string{`connector="contoso"`, `operation="` + op + `"`}
		if spec.NeedsUser && omit != "user" {
			parts = append(parts, `userId="arno@contoso.com"`)
		}
		if spec.NeedsGroup && omit != "group" {
			parts = append(parts, `groupId="g1"`)
		}
		if spec.NeedsAttributes && omit != "attributes" {
			parts = append(parts, `attributesVariable="attrs"`)
		}
		if spec.NeedsPassword && omit != "password" {
			parts = append(parts, `newPassword="S3cret!"`)
		}
		// A listing or a delta query has nowhere to put a collection without one, which
		// both halves enforce — the compiler at deploy, checkRequired on the worker.
		if (spec.IsList || spec.IsDelta) && omit != "result" {
			parts = append(parts, `resultVariable="leute"`)
		}
		return strings.Join(parts, " ")
	}
	compile := func(attrs string) error {
		_, err := compiler.Parse(1, 1, strings.NewReader(entraTaskBPMN(attrs)))
		return err
	}

	for op, spec := range Ops {
		t.Run(op, func(t *testing.T) {
			if err := compile(attrsFor(op, spec, "")); err != nil {
				t.Fatalf("the compiler rejects a model that satisfies Ops[%q]: %v", op, err)
			}
			for _, omit := range []string{"user", "group", "attributes", "password", "result"} {
				required := (omit == "user" && spec.NeedsUser) ||
					(omit == "group" && spec.NeedsGroup) ||
					(omit == "attributes" && spec.NeedsAttributes) ||
					(omit == "password" && spec.NeedsPassword) ||
					(omit == "result" && (spec.IsList || spec.IsDelta))
				if !required {
					continue
				}
				if err := compile(attrsFor(op, spec, omit)); err == nil {
					t.Errorf("Ops[%q] requires %s but the compiler accepted a model without it", op, omit)
				}
			}
			// The query fields are the other half of the same agreement, in three classes
			// both halves must place the same way: filter/search/advancedQuery on a
			// listing; select/pageSize/maxUsers on a listing or a delta query (both return
			// a collection); deltaLink on a delta query alone. The compiler must refuse
			// each where it has no meaning rather than drop it silently.
			for _, c := range []struct {
				q       string
				allowed bool
			}{
				{`filter="accountEnabled eq true"`, spec.IsList},
				{`search="&#34;displayName:Arno&#34;"`, spec.IsList},
				{`advancedQuery="true"`, spec.IsList},
				{`select="id"`, spec.IsList || spec.IsDelta},
				{`pageSize="10"`, spec.IsList || spec.IsDelta},
				{`maxUsers="10"`, spec.IsList || spec.IsDelta},
				{`deltaLink="https://graph.microsoft.com/v1.0/users/delta?$deltatoken=t"`, spec.IsDelta},
			} {
				err := compile(attrsFor(op, spec, "") + " " + c.q)
				if c.allowed && err != nil {
					t.Errorf("Ops[%q] should accept %s but the compiler rejects it: %v", op, c.q, err)
				}
				if !c.allowed && err == nil {
					t.Errorf("Ops[%q] should reject %s but the compiler accepted it", op, c.q)
				}
			}
		})
	}

	// And the other direction: an operation this package does not know must not
	// compile either.
	if err := compile(`connector="contoso" operation="rename-user" userId="u"`); err == nil {
		t.Error("the compiler accepted an operation Ops does not carry")
	}
}

// assign-role merges the authored user in as principalId and defaults the scope to the
// whole directory — but an authored directoryScopeId is kept, so a scoped assignment is
// expressible.
func TestEntraAssignRoleKeepsAnAuthoredScope(t *testing.T) {
	c := &recordingClient{res: map[string]any{"id": "ra1"}}
	j := Job{Connector: "contoso", Operation: "assign-role", UserID: "u1",
		Attributes: map[string]any{"roleDefinitionId": "r1", "directoryScopeId": "/administrativeUnits/au1"}}
	if _, err := Run(context.Background(), j, regWith("contoso", c)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	m, _ := c.body.(map[string]any)
	if m["principalId"] != "u1" {
		t.Errorf("principalId = %v, want the authored user", m["principalId"])
	}
	if m["directoryScopeId"] != "/administrativeUnits/au1" {
		t.Errorf("directoryScopeId = %v, want the authored scope kept, not defaulted", m["directoryScopeId"])
	}
}

// entraTaskBPMN builds a one-task model from raw connector attributes.
func entraTaskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:entraConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}
