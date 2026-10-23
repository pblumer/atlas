package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/sharepoint"
)

// Asking before acting, for SharePoint (ADR-0475 §4,
// §5): a governed Worker acts only on a permit or in shadow, on the parameters the
// decision covers, and a brokered instance acts only with the token its permit carried.

// graphLog stands in for Microsoft Graph: it records every call as
// "METHOD path Authorization" and answers a site lookup and a delete as Graph would.
type graphLog struct {
	mu    sync.Mutex
	calls []string
}

func newGraphLog(t *testing.T) (*httptest.Server, *graphLog) {
	t.Helper()
	g := &graphLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			t.Errorf("unexpected Graph call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, g
}

func (g *graphLog) seen() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.calls...)
}

// deleted reports whether a delete of path reached Graph, and with which bearer.
func (g *graphLog) deleted(path string) (bearer string, ok bool) {
	for _, c := range g.seen() {
		method, rest, _ := strings.Cut(c, " ")
		target, auth, _ := strings.Cut(rest, " ")
		if method == http.MethodDelete && strings.HasSuffix(target, "/drive/root:/"+path) {
			return auth, true
		}
	}
	return "", false
}

// deleteDraft is a delete-file task as the engine resolves it for instance name.
func deleteDraft(t *testing.T, name, path string) Job {
	t.Helper()
	j := sharePointJobFrom(t, sharepoint.Job{
		Connector: name,
		Operation: "delete-file",
		Site:      "https://contoso.sharepoint.com/sites/hr",
		Path:      path,
		RequestID: "42",
		Result:    "outcome",
	})
	j.JobKey, j.LeaseToken = 42, 7
	return j
}

// governedBy puts a worker's question for j within reach of ctx, answered by a server
// that writes answer to it.
func governedBy(t *testing.T, answer http.HandlerFunc, j Job) context.Context {
	t.Helper()
	srv, _ := newAskingAtlas(t, answer)
	w := New(Options{Server: srv.URL, ID: "sharepoint", BrokerKey: "bk-1"})
	return w.withAuthorizer(context.Background(), j)
}

// permitCarrying answers an enforced permit covering fields, with token brokered for it.
func permitCarrying(t *testing.T, fields map[string]any, token string) http.HandlerFunc {
	t.Helper()
	answer, err := json.Marshal(map[string]any{
		"enforcement": "enforce", "permitted": true, "enforced": true, "reasonCode": "PERMIT",
		"job":        fields,
		"credential": map[string]any{"type": "bearer", "accessToken": token, "expiresAt": 1700000000},
	})
	if err != nil {
		t.Fatal(err)
	}
	return answerJSON(http.StatusOK, string(answer))
}

// heldRegistry is a worker holding its own credential for name: an app token to Graph.
func heldRegistry(name, graphURL string) *sharepoint.Registry {
	return sharePointRegistryWith(name, sharepoint.NewGraphClient(staticToken("app-token"), graphURL))
}

// An instance marked brokered holds no credential at all — none is asked for, none is
// built — and only its Graph endpoint is known here; an instance beside it that is not
// brokered is built from its bundle as before.
func TestASharePointInstanceMarkedBrokeredHoldsNoCredential(t *testing.T) {
	env := map[string]string{
		"ATLAS_SHAREPOINT_CONNECTORS":           "sp-prod, intranet",
		"ATLAS_SHAREPOINT_SP_PROD_BROKERED":     "1",
		"ATLAS_SHAREPOINT_SP_PROD_ENDPOINT":     "https://graph.example/v1.0",
		"ATLAS_SHAREPOINT_INTRANET_CREDENTIALS": `{"method":"clientCredentials","tenantId":"t-1","clientId":"c-1","clientSecret":"s3cr3t"}`,
	}
	reg, names, brokered, err := sharepointSourcesFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("sharepointSourcesFromEnv: %v (a brokered instance needs no credential bundle)", err)
	}
	if strings.Join(names, ",") != "sp-prod,intranet" {
		t.Errorf("names = %v, want both instances CONNECTORS lists", names)
	}
	if len(brokered) != 1 || brokered["sp-prod"] != "https://graph.example/v1.0" {
		t.Errorf("brokered = %v, want only sp-prod with its endpoint", brokered)
	}
	if _, ok := reg.Client("sp-prod"); ok {
		t.Error("a brokered instance was given a client of its own to fall back to")
	}
	if _, ok := reg.Client("intranet"); !ok {
		t.Error("the instance beside it was not built from its bundle")
	}

	// Brokering is switched on by 1 alone; anything else is an instance that must bring
	// its own credential, and without one it is refused at startup as before.
	env["ATLAS_SHAREPOINT_SP_PROD_BROKERED"] = "0"
	if _, _, _, err := sharepointSourcesFromEnv(func(k string) string { return env[k] }); err == nil ||
		!strings.Contains(err.Error(), "sp-prod") {
		t.Errorf("err = %v, want sp-prod refused for want of a credential", err)
	}
}

// The whole path through the shipped worker: a brokered instance, leased, asked about,
// permitted — and every Graph call, the site lookup included, carries the token the
// permit brought and nothing else. The worker completes the job with what Graph
// answered.
func TestABrokeredSharePointActionUsesOnlyTheTokenItsPermitCarried(t *testing.T) {
	graph, log := newGraphLog(t)
	job := deleteDraft(t, "sp-prod", "Drafts/old.docx")
	job.Type = compiler.SharePointOpsJobType
	leased, err := json.Marshal(map[string]any{"jobs": []Job{job}})
	if err != nil {
		t.Fatal(err)
	}
	permit := permitCarrying(t, job.Connector.Fields, "graph-token-1")
	var complete map[string]any
	srv, atlas := newAskingAtlas(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/activate"):
			// Only one of the two SharePoint job types has work; the poll of the
			// other comes back empty.
			var poll struct {
				Type string `json:"type"`
			}
			_ = json.NewDecoder(r.Body).Decode(&poll)
			if poll.Type == compiler.SharePointOpsJobType {
				_, _ = w.Write(leased)
				return
			}
			_, _ = w.Write([]byte(`{"jobs":[]}`))
		case strings.HasSuffix(r.URL.Path, "/authorize"):
			permit(w, r)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})

	env := map[string]string{
		"ATLAS_SHAREPOINT_CONNECTORS":       "sp-prod",
		"ATLAS_SHAREPOINT_SP_PROD_BROKERED": "1",
		"ATLAS_SHAREPOINT_SP_PROD_ENDPOINT": graph.URL,
	}
	built, err := BuiltinConnectors(func(k string) string { return env[k] }, "sharepoint")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	w := New(Options{Server: srv.URL, ID: "sharepoint", BrokerKey: "bk-1", Handlers: built.Handlers})
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	calls := log.seen()
	if len(calls) == 0 {
		t.Fatal("nothing reached Graph")
	}
	for _, c := range calls {
		if !strings.HasSuffix(c, " Bearer graph-token-1") {
			t.Errorf("a Graph call without the brokered token: %q", c)
		}
	}
	if _, ok := log.deleted("Drafts/old.docx"); !ok {
		t.Errorf("no delete reached Graph: %v", calls)
	}
	for _, r := range atlas.requests() {
		switch {
		case strings.HasSuffix(r.path, "/authorize"):
			if r.header.Get("X-Atlas-Broker-Key") != "bk-1" {
				t.Errorf("the worker asked without its broker key: %v", r.header)
			}
		case strings.HasSuffix(r.path, "/complete"):
			complete = r.body
		case strings.HasSuffix(r.path, "/fail"):
			t.Errorf("the job was failed: %v", r.body)
		}
	}
	vars, _ := complete["variables"].(map[string]any)
	outcome, _ := vars["outcome"].(map[string]any)
	if outcome["deleted"] != true || outcome["path"] != "Drafts/old.docx" {
		t.Errorf("completed with %v, want what Graph answered under the result variable", complete)
	}
}

// A refused action never reaches Graph — not with a token of its own the worker
// holds, not at all — and the handler says so with the error the worker reads as
// settled by the server.
func TestARefusedSharePointActionNeverReachesGraph(t *testing.T) {
	graph, log := newGraphLog(t)
	job := deleteDraft(t, "intranet", "Drafts/old.docx")
	ctx := governedBy(t, answerJSON(http.StatusOK,
		`{"enforcement":"enforce","permitted":false,"enforced":true,"reasonCode":"NO_CONNECTION"}`), job)

	out, err := runSharePointJob(ctx, job, heldRegistry("intranet", graph.URL), nil)
	if !errors.Is(err, errPolicyRefused) {
		t.Fatalf("runSharePointJob = (%v, %v), want the policy refusal", out, err)
	}
	if calls := log.seen(); len(calls) != 0 {
		t.Errorf("a refused action reached Graph: %v", calls)
	}
}

// In shadow the decision is recorded and not enforced: a refusal there still acts, as
// the Worker did before it was governed.
func TestAShadowRefusalStillActs(t *testing.T) {
	graph, log := newGraphLog(t)
	job := deleteDraft(t, "intranet", "Drafts/old.docx")
	ctx := governedBy(t, answerJSON(http.StatusOK,
		`{"enforcement":"shadow","permitted":false,"enforced":false,"reasonCode":"NO_CONNECTION"}`), job)

	out, err := runSharePointJob(ctx, job, heldRegistry("intranet", graph.URL), nil)
	if err != nil {
		t.Fatalf("runSharePointJob: %v", err)
	}
	if bearer, ok := log.deleted("Drafts/old.docx"); !ok || bearer != "Bearer app-token" {
		t.Errorf("delete = (%q, %v), want it made with the worker's own credential: %v", bearer, ok, log.seen())
	}
	if res, _ := out["outcome"].(map[string]any); res["deleted"] != true {
		t.Errorf("out = %v, want what Graph answered", out)
	}
}

// A governed action acts on the parameters the decision covers, not on what the job
// carried at lease time: what was decided is what is done.
func TestAGovernedActionActsOnTheParametersTheDecisionCovers(t *testing.T) {
	graph, log := newGraphLog(t)
	job := deleteDraft(t, "intranet", "Drafts/old.docx")
	decided := deleteDraft(t, "intranet", "Drafts/approved.docx").Connector.Fields
	answer, err := json.Marshal(map[string]any{
		"enforcement": "enforce", "permitted": true, "enforced": true, "job": decided,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := governedBy(t, answerJSON(http.StatusOK, string(answer)), job)

	if _, err := runSharePointJob(ctx, job, heldRegistry("intranet", graph.URL), nil); err != nil {
		t.Fatalf("runSharePointJob: %v", err)
	}
	if _, ok := log.deleted("Drafts/approved.docx"); !ok {
		t.Errorf("the decided path was not deleted: %v", log.seen())
	}
	if _, ok := log.deleted("Drafts/old.docx"); ok {
		t.Error("the lease-time path was acted on instead of the decided one")
	}
}

// A brokered instance has no credential to fall back to, by design: without one in the
// answer — whatever the answer was — it does not act, and says which Worker could not.
func TestABrokeredInstanceWithoutABrokeredCredentialDoesNotAct(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer http.HandlerFunc
	}{
		{"a server without the step", http.NotFound},
		{"enforcement off", answerJSON(http.StatusOK, `{"enforcement":"off","permitted":true}`)},
		{"a permit without a credential", answerJSON(http.StatusOK, `{"enforcement":"enforce","permitted":true,"enforced":true}`)},
		{"a credential without a token", answerJSON(http.StatusOK,
			`{"enforcement":"enforce","permitted":true,"enforced":true,"credential":{"type":"bearer","accessToken":""}}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph, log := newGraphLog(t)
			job := deleteDraft(t, "sp-prod", "Drafts/old.docx")
			ctx := governedBy(t, tc.answer, job)

			_, err := runSharePointJob(ctx, job, sharepoint.NewRegistry(), map[string]string{"sp-prod": graph.URL})
			if err == nil {
				t.Fatal("a brokered instance acted without a brokered credential")
			}
			if errors.Is(err, errPolicyRefused) {
				t.Errorf("err = %v; nothing was refused, so the server has settled nothing", err)
			}
			if !strings.Contains(err.Error(), `"sp-prod"`) || !strings.Contains(err.Error(), "credential") {
				t.Errorf("err = %v, want it to name the Worker and the missing credential", err)
			}
			if calls := log.seen(); len(calls) != 0 {
				t.Errorf("Graph was called anyway: %v", calls)
			}
		})
	}
}

// A server without the step, or one whose Worker has no identity policy, is the legacy
// answer: the worker acts with the credential it holds, on what it leased.
func TestALegacyAnswerActsWithTheCredentialTheWorkerHolds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer http.HandlerFunc
	}{
		{"a server without the step", http.NotFound},
		{"enforcement off", answerJSON(http.StatusOK, `{"enforcement":"off","permitted":true}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph, log := newGraphLog(t)
			job := deleteDraft(t, "intranet", "Drafts/old.docx")
			ctx := governedBy(t, tc.answer, job)

			if _, err := runSharePointJob(ctx, job, heldRegistry("intranet", graph.URL), nil); err != nil {
				t.Fatalf("runSharePointJob: %v", err)
			}
			if bearer, ok := log.deleted("Drafts/old.docx"); !ok || bearer != "Bearer app-token" {
				t.Errorf("delete = (%q, %v), want it made with the held credential: %v", bearer, ok, log.seen())
			}
		})
	}
}

// A question the server could not answer is not a permit: the job fails with why, and
// nothing reaches Graph.
func TestASharePointActionThatCannotAskDoesNotAct(t *testing.T) {
	graph, log := newGraphLog(t)
	job := deleteDraft(t, "intranet", "Drafts/old.docx")
	ctx := governedBy(t, answerJSON(http.StatusInternalServerError, `{"error":"store closed"}`), job)

	_, err := runSharePointJob(ctx, job, heldRegistry("intranet", graph.URL), nil)
	if err == nil || !strings.Contains(err.Error(), "could not ask") || !strings.Contains(err.Error(), "store closed") {
		t.Fatalf("err = %v, want the failed question and the server's reason", err)
	}
	if calls := log.seen(); len(calls) != 0 {
		t.Errorf("Graph was called without an answer: %v", calls)
	}
}

// A caller embedding RunSharePointJob builds the detail itself, and one holding a value
// JSON cannot carry is refused before anything is sent.
func TestRunSharePointJobRefusesADetailItCannotEncode(t *testing.T) {
	graph, log := newGraphLog(t)
	job := deleteDraft(t, "intranet", "Drafts/old.docx")
	job.Connector.Fields["fields"] = map[string]any{"Title": make(chan int)}

	if _, err := RunSharePointJob(context.Background(), job, heldRegistry("intranet", graph.URL)); err == nil {
		t.Fatal("a detail that cannot be encoded was run")
	}
	if calls := log.seen(); len(calls) != 0 {
		t.Errorf("Graph was called: %v", calls)
	}
}

// A permit that carries a person's token says the server recorded the action as theirs.
// An instance that is not set up to act with it — its environment predates the
// Worker becoming delegated — must not act with its own app credential instead: the
// evidence would name a person for an action the Worker took as itself.
func TestAnInstanceNotSetUpForAPersonsTokenDoesNotActAsItself(t *testing.T) {
	graph, log := newGraphLog(t)
	job := deleteDraft(t, "intranet", "Drafts/old.docx")
	ctx := governedBy(t, permitCarrying(t, job.Connector.Fields, "persons-token"), job)

	_, err := runSharePointJob(ctx, job, heldRegistry("intranet", graph.URL), nil)
	if err == nil || !strings.Contains(err.Error(), "not set up") {
		t.Fatalf("err = %v, want a refusal to act as the Worker for a person's permit", err)
	}
	if calls := log.seen(); len(calls) != 0 {
		t.Errorf("Graph was called: %v", calls)
	}
}
