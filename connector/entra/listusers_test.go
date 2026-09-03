package entra

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// pagingClient answers a scripted sequence of pages and records the path each call
// asked for, so the paging loop is assertable without a directory.
type pagingClient struct {
	paths []string
	pages []any
	err   error
	// always, when set, is returned for every call past the scripted ones — the
	// shape of a server that never stops offering another page.
	always any
}

func (c *pagingClient) BaseURL() string { return "https://graph.microsoft.com/v1.0" }

func (c *pagingClient) Call(_ context.Context, r Request) (any, error) {
	c.paths = append(c.paths, r.Path)
	if c.err != nil {
		return nil, c.err
	}
	if len(c.pages) > 0 {
		p := c.pages[0]
		c.pages = c.pages[1:]
		return p, nil
	}
	if c.always != nil {
		return c.always, nil
	}
	return map[string]any{"value": []any{}}, nil
}

// page builds one Graph collection response: the users it carries, and the link to
// the next page (empty for the last one).
func page(next string, ids ...string) map[string]any {
	vals := make([]any, 0, len(ids))
	for _, id := range ids {
		vals = append(vals, map[string]any{"id": id})
	}
	p := map[string]any{"value": vals}
	if next != "" {
		p["@odata.nextLink"] = next
	}
	return p
}

// idsOf reads the ids out of what list-users wrote into the result variable, so the
// assertions below read as "which users came back, in which order".
func idsOf(t *testing.T, v any) []string {
	t.Helper()
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("result = %#v, want a JSON array of users", v)
	}
	out := make([]string, 0, len(list))
	for _, u := range list {
		m, ok := u.(map[string]any)
		if !ok {
			t.Fatalf("user = %#v, want an object", u)
		}
		out = append(out, m["id"].(string))
	}
	return out
}

// The whole point of the operation: a listing is *one* result, not one page. The
// modeler asked for users, so following @odata.nextLink is the worker's job and
// not something a process has to model with a loop.
func TestListUsersFollowsEveryPage(t *testing.T) {
	const next1 = "https://graph.microsoft.com/v1.0/users?$skiptoken=A"
	const next2 = "https://graph.microsoft.com/v1.0/users?$skiptoken=B"
	c := &pagingClient{pages: []any{
		page(next1, "u1", "u2"),
		page(next2, "u3"),
		page("", "u4"),
	}}
	got, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "leute",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ids := idsOf(t, got["leute"]); strings.Join(ids, ",") != "u1,u2,u3,u4" {
		t.Errorf("users = %v, want every page's, in order", ids)
	}
	// The continuation is the link Graph handed back, verbatim — not a path this
	// worker reassembles, which is how a skiptoken gets mangled.
	want := []string{"/users", next1, next2}
	if strings.Join(c.paths, " ") != strings.Join(want, " ") {
		t.Errorf("requested %v, want %v", c.paths, want)
	}
}

// An empty directory is an empty list, not a null: a process doing count(users) or
// iterating over the result must not have to special-case "no users".
func TestListUsersEmptyIsAnEmptyList(t *testing.T) {
	c := &pagingClient{pages: []any{page("")}}
	got, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "leute",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ids := idsOf(t, got["leute"]); len(ids) != 0 {
		t.Errorf("users = %v, want an empty list", ids)
	}
}

// The authored query becomes Graph's own query parameters — the encoding a modeler
// would otherwise have to get right by hand, which is this worker's reason to
// exist (ADR-0172).
func TestListUsersBuildsTheQuery(t *testing.T) {
	for _, tc := range []struct {
		name string
		job  Job
		want string
	}{
		{name: "no query at all", job: Job{}, want: "/users"},
		{
			name: "filter",
			job:  Job{Filter: "startsWith(displayName,'Arno')"},
			want: "/users?$filter=startsWith%28displayName%2C%27Arno%27%29",
		},
		{
			name: "select",
			job:  Job{Select: "id,displayName,mail"},
			want: "/users?$select=id%2CdisplayName%2Cmail",
		},
		{name: "page size", job: Job{PageSize: 999}, want: "/users?$top=999"},
		{
			name: "all three, in Graph's documented order",
			job:  Job{Filter: "accountEnabled eq true", Select: "id", PageSize: 50},
			want: "/users?$filter=accountEnabled+eq+true&$select=id&$top=50",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &pagingClient{pages: []any{page("")}}
			j := tc.job
			j.Connector, j.Operation, j.ResultVariable = "contoso", "list-users", "leute"
			if _, err := Run(context.Background(), j, regWith("contoso", c)); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if c.paths[0] != tc.want {
				t.Errorf("path = %q, want %q", c.paths[0], tc.want)
			}
		})
	}
}

// The cap fails the job rather than truncating, for the reason the LDAP worker's
// does: a short result set is a wrong answer, not a partial one, and a process that
// decides something from it decides it confidently.
func TestListUsersCapIsAnErrorNotATruncation(t *testing.T) {
	c := &pagingClient{pages: []any{
		page("https://graph.microsoft.com/v1.0/users?$skiptoken=A", "u1", "u2"),
		page("", "u3"),
	}}
	_, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "leute", MaxUsers: 2,
	}, regWith("contoso", c))
	if err == nil {
		t.Fatal("exceeding the cap must fail the job")
	}
	if !strings.Contains(err.Error(), "maxUsers") {
		t.Errorf("error = %v, want it to name the bound an author can raise", err)
	}
	// Exactly at the cap is not exceeding it.
	c2 := &pagingClient{pages: []any{page("", "u1", "u2")}}
	if _, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "leute", MaxUsers: 2,
	}, regWith("contoso", c2)); err != nil {
		t.Errorf("a listing exactly at the cap must succeed: %v", err)
	}
}

// An unbounded listing still terminates. A server that offers a next page forever —
// broken, hostile, or merely a directory larger than anyone expected — must fail
// visibly instead of spinning a worker until the job's lease expires.
func TestListUsersUnboundedStopsAtThePageCeiling(t *testing.T) {
	c := &pagingClient{always: page("https://graph.microsoft.com/v1.0/users?$skiptoken=X", "u")}
	_, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "leute", MaxUsers: 0,
	}, regWith("contoso", c))
	if err == nil {
		t.Fatal("an endless nextLink chain must fail rather than loop forever")
	}
	if len(c.paths) != maxListPages {
		t.Errorf("made %d requests, want the %d-page ceiling", len(c.paths), maxListPages)
	}
}

// A response that is not a user collection is reported as that, rather than becoming
// an empty list a process would read as "no such users".
func TestListUsersRejectsAMalformedPage(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  any
	}{
		{name: "not an object", res: []any{"nope"}},
		{name: "no value member", res: map[string]any{"id": "u1"}},
		{name: "value is not a list", res: map[string]any{"value": "u1"}},
		{name: "nextLink is not a string", res: map[string]any{"value": []any{}, "@odata.nextLink": 7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &pagingClient{pages: []any{tc.res}}
			if _, err := Run(context.Background(), Job{
				Connector: "contoso", Operation: "list-users", ResultVariable: "leute",
			}, regWith("contoso", c)); err == nil {
				t.Errorf("a %s must fail the job", tc.name)
			}
		})
	}
	// A failing call on a later page fails the whole listing: half a directory is
	// the same wrong answer a truncation would be.
	c := &pagingClient{
		pages: []any{page("https://graph.microsoft.com/v1.0/users?$skiptoken=A", "u1")},
		err:   nil,
	}
	c.pages = append(c.pages, nil)
	if _, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "leute",
	}, regWith("contoso", c)); err == nil {
		t.Error("a nil page must fail the listing")
	}
}

// list-users needs no user and no group; what it needs is a result variable, because
// a listing that discards its result is a directory read nobody asked for.
func TestListUsersNeedsAResultVariable(t *testing.T) {
	c := &pagingClient{pages: []any{page("", "u1")}}
	if _, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users",
	}, regWith("contoso", c)); err == nil {
		t.Error("list-users without a result variable must fail")
	}
}

// The token this worker holds can read an entire directory. A paged result is the
// one place a *response* names the next URL, so the guard is that the continuation
// stays on the endpoint the worker was configured for — a redirected page must
// never carry the bearer to another host.
func TestGraphClientRefusesAForeignContinuation(t *testing.T) {
	g := newGraphServer(t)
	g.response = `{"value":[]}`
	c := g.client()
	if _, err := c.Call(context.Background(), Request{Method: "GET", Path: "https://evil.example/v1.0/users"}); err == nil {
		t.Fatal("a continuation on another host must be refused")
	} else if !strings.Contains(err.Error(), "evil.example") {
		t.Errorf("error = %v, want it to name the host it refused", err)
	}
	// Its own origin is followed, absolute or not.
	if _, err := c.Call(context.Background(), Request{Method: "GET", Path: g.srv.URL + "/users?$skiptoken=A"}); err != nil {
		t.Errorf("a continuation on the worker's own endpoint must be followed: %v", err)
	}
	if g.path != "/users" {
		t.Errorf("path = %q, want the absolute continuation to have been requested", g.path)
	}
}

// A malformed continuation is refused rather than concatenated onto the base URL,
// which is how a nonsense link becomes a plausible-looking request.
func TestGraphClientRefusesAnUnparseableContinuation(t *testing.T) {
	g := newGraphServer(t)
	c := g.client()
	if _, err := c.Call(context.Background(), Request{Method: "GET", Path: "https://exa mple.com/users"}); err == nil {
		t.Error("an unparseable continuation must be refused")
	}
	// And a client whose own base URL cannot be parsed cannot decide the question.
	bad := NewGraphClient(staticToken{tok: "t"}, "https://exa mple.com", http.DefaultClient)
	if _, err := bad.Call(context.Background(), Request{Method: "GET", Path: "https://graph.microsoft.com/v1.0/users"}); err == nil {
		t.Error("an unparseable base URL must refuse a continuation")
	}
}

// End to end over HTTP: the paging loop, the query it builds, and the client that
// follows an absolute continuation, against a server that behaves like Graph.
func TestListUsersOverHTTP(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("$skiptoken") == "" {
			_, _ = w.Write([]byte(`{"value":[{"id":"u1"}],"@odata.nextLink":"` + srv.URL + `/users?$skiptoken=A"}`))
			return
		}
		_, _ = w.Write([]byte(`{"value":[{"id":"u2"}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewGraphClient(staticToken{tok: "tok-1"}, srv.URL, http.DefaultClient)
	got, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "leute",
		Filter: "accountEnabled eq true", Select: "id", PageSize: 1,
	}, regWith("contoso", client))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ids := idsOf(t, got["leute"]); strings.Join(ids, ",") != "u1,u2" {
		t.Errorf("users = %v, want both pages", ids)
	}
}

// Resolve is engine work: the filter is a literal-or-FEEL value evaluated against
// the instance's variables, and the bounds the compiler settled travel as numbers.
func TestResolveCarriesTheListingQuery(t *testing.T) {
	cp, d := buildTask(t, compiler.EntraConfig{
		Connector: "contoso", Op: "list-users",
		Filter:    compiler.RestExpr{Literal: "accountEnabled eq true"},
		Select:    "id,displayName",
		PageSize:  100,
		MaxUsers:  250,
		ResultVar: "leute",
	})
	j, err := Resolve(fakeVarStore{}, cp, d, 1)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if j.Filter != "accountEnabled eq true" || j.Select != "id,displayName" {
		t.Errorf("job = %+v, want the authored query", j)
	}
	if j.PageSize != 100 || j.MaxUsers != 250 {
		t.Errorf("bounds = %d/%d, want 100/250", j.PageSize, j.MaxUsers)
	}
}

// A FEEL filter is evaluated at the task, so a process can list the members of the
// department it is actually about.
func TestResolveEvaluatesAFEELFilter(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>
      <atlas:entraConnector connector="contoso" operation="list-users"
                            filter="=&#34;department eq '&#34; + abteilung + &#34;'&#34;"
                            resultVariable="leute"/>
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
		1: {{Name: "abteilung", Kind: model.VarString, Text: "IT"}},
	}}
	j, err := Resolve(store, cp, cp.ConnectorTask(node.Detail), 1)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if j.Filter != "department eq 'IT'" {
		t.Errorf("filter = %q, want the evaluated expression", j.Filter)
	}
}

// A resolve error on the listing path fails the same way every other one does.
func TestListUsersRunPropagatesACallError(t *testing.T) {
	c := &pagingClient{err: errors.New("graph is down")}
	if _, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "leute",
	}, regWith("contoso", c)); err == nil {
		t.Error("a failing call must fail the listing")
	}
}
