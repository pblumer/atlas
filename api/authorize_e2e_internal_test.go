package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/worker"
)

// The first deliverable of the record, end to end
// (ADR-0475 §22): a delegated Microsoft identity
// executing a policy-authorized SharePoint action within a durable BPMN process — this
// server, the shipped `atlas worker` SharePoint code, and a Graph that records what it
// was asked and with which token.

type fakeGraph struct {
	mu    sync.Mutex
	calls []string
}

func (g *fakeGraph) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.calls = append(g.calls, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		g.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"site-1"}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	})
}

func (g *fakeGraph) seen() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.calls...)
}

// supervisedFront puts the internal principal on a request carrying the supervised
// worker's token, as the authentication boundary would on a server with auth on.
func supervisedFront(srv *Server, internal *httpapi.Principal) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer supervised" {
			r = r.WithContext(httpapi.WithPrincipal(r.Context(), internal))
		}
		srv.Handler().ServeHTTP(w, r)
	})
}

func runSharePointWorker(t *testing.T, serverURL, graphURL, brokerKey string) {
	t.Helper()
	env := map[string]string{
		"ATLAS_SHAREPOINT_CONNECTORS":       "sp-prod",
		"ATLAS_SHAREPOINT_SP_PROD_ENDPOINT": graphURL,
		"ATLAS_SHAREPOINT_SP_PROD_BROKERED": "1",
	}
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, "sharepoint")
	if err != nil {
		t.Fatal(err)
	}
	w := worker.New(worker.Options{Server: serverURL, ID: "sharepoint", Token: "supervised", BrokerKey: brokerKey,
		Handlers: built.Handlers, Wait: 10 * time.Millisecond, Lease: time.Minute})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("worker: %v", err)
	}
}

const membersMayDelete = `{"id":"hr-documents","target":{"workerTypes":["sharepoint"],"operations":["delete-file"]},
  "ruleCombining":"first-applicable",
  "rules":[{"id":"members","effect":"Permit","when":{"subjectGroupsAny":["grp_documents"]}}]}`

func TestTheShippedWorkerDeletesAsTheRequesterWithABrokeredToken(t *testing.T) {
	r := newPEPRig(t)
	graph := &fakeGraph{}
	gs := httptest.NewServer(graph.handler())
	defer gs.Close()
	front := httptest.NewServer(supervisedFront(r.srv, r.internal))
	defer front.Close()
	if _, err := r.srv.policies.SaveDraft("hr-documents", []byte(membersMayDelete), "usr_admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.srv.policies.Publish("hr-documents", "usr_admin"); err != nil {
		t.Fatal(err)
	}
	r.linkAlice()
	r.govern("delegated-per-job", "enforce", `,"actAs":"initiator"`)
	pi := r.startAsAlice()

	runSharePointWorker(t, front.URL, gs.URL, r.srv.brokerKey)

	calls := graph.seen()
	deleted := false
	for _, c := range calls {
		if !strings.HasSuffix(c, "Bearer graph-token-1") {
			t.Fatalf("a Graph call without the brokered token: %q", c)
		}
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "Drafts/old.docx") {
			deleted = true
		}
	}
	if !deleted {
		t.Fatalf("no delete reached Graph: %v", calls)
	}
	ev := r.evidence(pi)
	if len(ev) != 1 || ev[0].Effect != "Permit" || ev[0].ExecutedAs != "microsoft:tid:oid-alice" || ev[0].PolicyVersion != 2 {
		t.Fatalf("evidence = %+v", ev)
	}
	r.srv.do(func() {
		inst, ok, _ := r.srv.store.ProcessInstance(pi)
		if !ok || inst.State != model.PICompleted {
			t.Errorf("the instance did not complete: %+v", inst)
		}
	})
}

// No unsafe fallback: a delegated Worker whose requester has no connection does not
// act at all — not as the app, not as anybody — and the worker reports nothing over
// the server's refusal.
func TestTheShippedWorkerDoesNothingWhenRefused(t *testing.T) {
	r := newPEPRig(t)
	graph := &fakeGraph{}
	gs := httptest.NewServer(graph.handler())
	defer gs.Close()
	front := httptest.NewServer(supervisedFront(r.srv, r.internal))
	defer front.Close()
	r.govern("delegated-per-job", "enforce", `,"actAs":"initiator"`)
	pi := r.startAsAlice()

	runSharePointWorker(t, front.URL, gs.URL, r.srv.brokerKey)

	if calls := graph.seen(); len(calls) != 0 {
		t.Fatalf("a refused action reached Graph: %v", calls)
	}
	ev := r.evidence(pi)
	if len(ev) != 1 || ev[0].ReasonCode != "NO_CONNECTION" {
		t.Fatalf("evidence = %+v", ev)
	}
}
