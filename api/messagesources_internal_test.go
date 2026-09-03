package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The Modeler asks which inbound watches publish a message name, so an author looking
// at a message start event can see whether anything actually feeds it. The name is the
// whole coupling between a model and its event source, and a typo in it is invisible
// until the process simply never starts.
func TestMessageSourcesListsWhatPublishesEachName(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	h := srv.Handler()
	do := func(method, path, body string) (int, []byte) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	_, cb := do(http.MethodPost, "/api/v1/connectors", `{"name":"ev","kind":"clio","endpoint":"http://x"}`)
	var clioConn connector
	_ = json.Unmarshal(cb, &clioConn)
	_, jb := do(http.MethodPost, "/api/v1/connectors", `{"name":"acme","kind":"jira","endpoint":"https://acme.atlassian.net","credentialsRef":"JIRA"}`)
	var jiraConn connector
	_ = json.Unmarshal(jb, &jiraConn)

	if code, b := do(http.MethodPost, "/api/v1/connectors/"+clioConn.ID+"/inbound-subscriptions",
		`{"watchedSubject":"employees","recursive":true,"messageName":"employee.created"}`); code != http.StatusOK {
		t.Fatalf("create clio watch: %d %s", code, b)
	}
	if code, b := do(http.MethodPost, "/api/v1/connectors/"+jiraConn.ID+"/inbound-subscriptions",
		`{"jql":"project = OPS","cursorField":"created","messageName":"jira.ticket.created"}`); code != http.StatusOK {
		t.Fatalf("create jira watch: %d %s", code, b)
	}

	code, lb := do(http.MethodGet, "/api/v1/message-sources", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, lb)
	}
	var got []messageSourceView
	if err := json.Unmarshal(lb, &got); err != nil {
		t.Fatalf("decode %s: %v", lb, err)
	}
	if len(got) != 2 {
		t.Fatalf("sources = %+v, want one per watch", got)
	}
	byName := map[string]messageSourceView{}
	for _, s := range got {
		byName[s.MessageName] = s
	}
	jira, ok := byName["jira.ticket.created"]
	if !ok {
		t.Fatalf("no source for the jira watch: %+v", got)
	}
	if jira.Kind != "jira" || jira.ConnectorName != "acme" || !jira.Enabled {
		t.Errorf("jira source = %+v, want it to name the worker it polls", jira)
	}
	// The description is what makes the line worth reading: which watch, not just that
	// one exists.
	if !strings.Contains(jira.Description, "project = OPS") || !strings.Contains(jira.Description, "created") {
		t.Errorf("jira description = %q, want the JQL and the field it follows", jira.Description)
	}
	clioSrc := byName["employee.created"]
	if !strings.Contains(clioSrc.Description, "employees") || !strings.Contains(clioSrc.Description, "subtree") {
		t.Errorf("clio description = %q, want the subject and that it is recursive", clioSrc.Description)
	}
}

// A disabled watch still appears, and says so. Removing it from the list would answer
// "nothing publishes this name" to an author whose watch is merely switched off — which
// is the one case where the fix is a toggle rather than a new watch.
func TestMessageSourcesKeepsDisabledWatches(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	h := srv.Handler()
	do := func(method, path, body string) (int, []byte) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	_, cb := do(http.MethodPost, "/api/v1/connectors", `{"name":"ev","kind":"clio","endpoint":"http://x"}`)
	var conn connector
	_ = json.Unmarshal(cb, &conn)
	_, sb := do(http.MethodPost, "/api/v1/connectors/"+conn.ID+"/inbound-subscriptions",
		`{"watchedSubject":"orders","messageName":"orderEvent"}`)
	var sub inboundSubscription
	_ = json.Unmarshal(sb, &sub)
	if code, b := do(http.MethodPatch, "/api/v1/inbound-subscriptions/"+sub.ID, `{"enabled":false}`); code != http.StatusOK {
		t.Fatalf("disable: %d %s", code, b)
	}

	_, lb := do(http.MethodGet, "/api/v1/message-sources", "")
	var got []messageSourceView
	_ = json.Unmarshal(lb, &got)
	if len(got) != 1 || got[0].Enabled {
		t.Fatalf("sources = %+v, want the disabled watch listed as disabled", got)
	}
}

// With no watches at all the answer is an empty array rather than null: the Modeler
// renders "nothing publishes this name" from it, and a null would read as a failed
// fetch, which the panel deliberately says nothing about.
func TestMessageSourcesEmpty(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/message-sources", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty listing = %d %q, want 200 []", rec.Code, rec.Body.String())
	}
}

// The hint is only worth having if the Modeler asks the endpoint this server serves.
// The path is a string on each side and nothing forces them to agree: rename the route
// and the panel's fetch 404s, which it swallows by design — so the line would go quiet
// and look exactly like "no watch publishes this name", which is the answer it exists to
// distinguish. Source-read for the same reason the moddle drift tests are: a fetch inside
// a function body is not something reflection can see.
func TestModelerAsksForMessageSources(t *testing.T) {
	editor, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatalf("read editor.js: %v", err)
	}
	src := string(editor)
	if !strings.Contains(src, `id="f-msgsources"`) {
		t.Error("the message panel renders no element for the source hint")
	}
	if !strings.Contains(src, "fillMessageSources(api,") {
		t.Error("nothing fills the source hint after the panel renders")
	}
	m := regexp.MustCompile(`fillMessageSources[\s\S]{0,900}?api\("GET", "([^"]+)"\)`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("fillMessageSources makes no GET; the pattern must have changed")
	}
	routes, err := os.ReadFile("openapi.go")
	if err != nil {
		t.Fatalf("read openapi.go: %v", err)
	}
	if !strings.Contains(string(routes), `"`+m[1]+`", s.handleListMessageSources`) {
		t.Errorf("the Modeler fetches %q, which this server does not route to handleListMessageSources", m[1])
	}
}
