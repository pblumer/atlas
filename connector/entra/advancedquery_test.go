package entra

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// requestSpy records the whole [Request] of every call, so the assertions below are
// about what was *asked for* rather than about a string somebody parsed back out.
type requestSpy struct {
	reqs  []Request
	pages []any
}

func (requestSpy) BaseURL() string { return "https://graph.microsoft.com/v1.0" }

func (s *requestSpy) Call(_ context.Context, req Request) (any, error) {
	s.reqs = append(s.reqs, req)
	if len(s.pages) > 0 {
		p := s.pages[0]
		s.pages = s.pages[1:]
		return p, nil
	}
	return map[string]any{"value": []any{}}, nil
}

// Graph's advanced query support is two things that must travel together: the
// ConsistencyLevel: eventual header and $count=true. Sending one without the other
// is a 400, so the connector never lets a model do that — asking for an advanced
// query asks for both.
func TestAdvancedQueryAddsCountAndAsksForEventualConsistency(t *testing.T) {
	s := &requestSpy{pages: []any{page("")}}
	if _, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "leute",
		Filter: "endsWith(mail,'@blumer.net')", Advanced: true,
	}, regWith("contoso", s)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := s.reqs[0]
	if !got.Eventual {
		t.Error("an advanced query must ask for eventual consistency")
	}
	if !strings.Contains(got.Path, "$count=true") {
		t.Errorf("path = %q, want it to carry $count=true", got.Path)
	}
}

// And the plain case stays plain: eventual consistency is a semantic change, so it
// happens only when a model asks for it.
func TestPlainQueryStaysStronglyConsistent(t *testing.T) {
	s := &requestSpy{pages: []any{page("")}}
	if _, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "leute",
		Filter: "accountEnabled eq true",
	}, regWith("contoso", s)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.reqs[0].Eventual {
		t.Error("a plain listing must not silently become eventually consistent")
	}
	if strings.Contains(s.reqs[0].Path, "$count") {
		t.Errorf("path = %q, want no $count on a plain listing", s.reqs[0].Path)
	}
}

// The header is not a property of the first request but of the *query*, so every
// continuation carries it too. Graph rejects a page fetched without it, which would
// turn a working listing into one that dies halfway through.
func TestAdvancedQueryHoldsOnEveryPage(t *testing.T) {
	const next = "https://graph.microsoft.com/v1.0/users?$skiptoken=A&$count=true"
	s := &requestSpy{pages: []any{page(next, "u1"), page("", "u2")}}
	if _, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "leute",
		Search: `"displayName:Arno"`,
	}, regWith("contoso", s)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(s.reqs) != 2 {
		t.Fatalf("made %d requests, want 2", len(s.reqs))
	}
	for i, r := range s.reqs {
		if !r.Eventual {
			t.Errorf("request %d did not ask for eventual consistency", i)
		}
	}
}

// A search is only possible as an advanced query, so authoring one is the whole of
// asking for it — making a modeler tick a second box would be a trap whose only
// outcome is a Graph 400.
func TestSearchImpliesAnAdvancedQuery(t *testing.T) {
	s := &requestSpy{pages: []any{page("")}}
	if _, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "leute",
		Search: `"displayName:Arno"`,
	}, regWith("contoso", s)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := s.reqs[0]
	if !got.Eventual || !strings.Contains(got.Path, "$count=true") {
		t.Errorf("request = %+v, want an advanced query", got)
	}
	// The term travels as authored — quotes included. Graph's $search takes its own
	// quoting (and compound terms like "a" AND "b"), so this connector encodes the
	// value but does not invent quotes around it.
	if !strings.Contains(got.Path, "$search=%22displayName%3AArno%22") {
		t.Errorf("path = %q, want the authored search term percent-encoded verbatim", got.Path)
	}
}

// The query parameters appear in Graph's documented order, with $count last.
func TestAdvancedQueryBuildsTheWholeQuery(t *testing.T) {
	s := &requestSpy{pages: []any{page("")}}
	if _, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "leute",
		Filter: "accountEnabled eq true", Search: `"mail:blumer"`, Select: "id",
		PageSize: 50, Advanced: true,
	}, regWith("contoso", s)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "/users?$filter=accountEnabled+eq+true&$search=%22mail%3Ablumer%22&$select=id&$top=50&$count=true"
	if s.reqs[0].Path != want {
		t.Errorf("path = %q,\nwant       %q", s.reqs[0].Path, want)
	}
}

// End to end over HTTP: the header really reaches the wire, on the first request and
// on the continuation, and only when asked for.
func TestGraphClientSendsTheConsistencyLevelHeader(t *testing.T) {
	var seen []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("ConsistencyLevel"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("$skiptoken") == "" {
			_, _ = w.Write([]byte(`{"value":[{"id":"u1"}],"@odata.nextLink":"` + srv.URL + `/users?$skiptoken=A&$count=true"}`))
			return
		}
		_, _ = w.Write([]byte(`{"value":[{"id":"u2"}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewGraphClient(staticToken{tok: "tok-1"}, srv.URL, http.DefaultClient)
	if _, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "leute",
		Filter: "endsWith(mail,'@blumer.net')", Advanced: true,
	}, regWith("contoso", client)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(seen) != 2 || seen[0] != "eventual" || seen[1] != "eventual" {
		t.Errorf("ConsistencyLevel headers = %v, want eventual on both requests", seen)
	}

	// Not asked for, not sent.
	seen = nil
	if _, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "get-user", UserID: "u1", ResultVariable: "p",
	}, regWith("contoso", client)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(seen) != 1 || seen[0] != "" {
		t.Errorf("ConsistencyLevel headers = %q, want none on a plain read", seen)
	}
}

// Resolve is engine work: the search is a literal-or-FEEL value like the filter, and
// the advanced-query flag the compiler settled travels as a plain bool.
func TestResolveCarriesTheAdvancedQuery(t *testing.T) {
	cp, d := buildTask(t, compiler.EntraConfig{
		Connector: "contoso", Op: "list-users",
		Search:    compiler.RestExpr{Literal: `"displayName:Arno"`},
		Advanced:  true,
		ResultVar: "leute",
	})
	j, err := Resolve(fakeVarStore{}, cp, d, 1)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if j.Search != `"displayName:Arno"` || !j.Advanced {
		t.Errorf("job = %+v, want the authored search and the advanced flag", j)
	}
}
