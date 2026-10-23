package api

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// Where a governed Worker's credential may go, and who may change that
// (ADR-draft-identity-aware-worker-execution §11). The identity policy is an
// administrator's; these tests hold the settings around it to the same standard,
// because a Worker editor who could move a delegated Worker's endpoint would receive
// every person's token the policy then permits.

func TestADelegatedCredentialNeedsTheBrokerKey(t *testing.T) {
	r := newPEPRig(t)
	r.linkAlice()
	r.govern("delegated-per-job", "enforce", `,"actAs":"initiator"`)
	pi := r.startAsAlice()
	job, lease := r.lease()
	// Every supervised child holds the internal credential; a child serving another
	// Worker Type has no broker key, and is handed nothing.
	for name, key := range map[string]string{"no key": "", "another key": "not-the-broker-key"} {
		if code, _ := r.authorizeWithKey(r.internal, key, job, lease); code != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", name, code)
		}
	}
	if ev := r.evidence(pi); len(ev) != 0 {
		t.Fatalf("a refused broker request recorded %d decisions", len(ev))
	}
	// The Worker Instance the supervisor handed the key is answered.
	if code, a := r.authorize(r.internal, job, lease); code != http.StatusOK || a.DecisionKey == 0 {
		t.Fatalf("with the key: %d %+v", code, a)
	}
}

func TestAGovernedWorkerIsChangedOnlyByAnAdministrator(t *testing.T) {
	r := newPEPRig(t)
	r.linkAlice()
	r.govern("delegated-per-job", "enforce", `,"actAs":"initiator"`)
	// A modeler created the Worker and owns it, as ADR-0205 lets them.
	r.srv.do(func() {
		rec, _, err := r.srv.connectors.Get(r.worker.ID)
		if err != nil {
			t.Fatal(err)
		}
		rec.OwnerID = "usr_mona"
		if err := r.srv.connectors.Save(rec); err != nil {
			t.Fatal(err)
		}
	})
	r.srv.authEnabled = true
	mona := &httpapi.Principal{UserID: "usr_mona", Username: "mona", Roles: []string{RoleModeler}}
	id := map[string]string{"id": r.worker.ID}

	if code, raw := r.as(mona, r.srv.handleUpdateConnector, http.MethodPatch, "/",
		`{"endpoint":"https://graph.attacker.example/v1.0"}`, id); code != http.StatusForbidden {
		t.Fatalf("the owner moved a governed Worker's endpoint: %d %s", code, raw)
	}
	if code, raw := r.as(mona, r.srv.handleUpdateConnector, http.MethodPatch, "/",
		`{"enabled":false}`, id); code != http.StatusForbidden {
		t.Fatalf("the owner changed a governed Worker: %d %s", code, raw)
	}
	// Deleting drops the identity policy, and a Worker created again under the name
	// would inherit the consent people gave the name.
	if code, raw := r.as(mona, r.srv.handleDeleteConnector, http.MethodDelete, "/?force=true", "", id); code != http.StatusForbidden {
		t.Fatalf("the owner deleted a governed Worker: %d %s", code, raw)
	}
	// An administrator may change it — and a delegated Worker's endpoint only to
	// another Microsoft Graph.
	if code, raw := r.as(r.admin, r.srv.handleUpdateConnector, http.MethodPatch, "/",
		`{"endpoint":"https://graph.attacker.example/v1.0"}`, id); code != http.StatusBadRequest {
		t.Fatalf("an administrator moved a delegated Worker off Graph: %d %s", code, raw)
	}
	if code, raw := r.as(r.admin, r.srv.handleUpdateConnector, http.MethodPatch, "/",
		`{"endpoint":"https://graph.microsoft.us/v1.0"}`, id); code != http.StatusOK {
		t.Fatalf("an administrator could not move it to another Graph: %d %s", code, raw)
	}
	if g, ok := r.srv.governedWorkerNamed("sp-prod"); !ok || g.endpoint != "https://graph.microsoft.us/v1.0" {
		t.Fatalf("the enforcement point did not learn the new endpoint: %+v", g)
	}
}

func TestADelegatedWorkerActsOnlyAgainstMicrosoftGraph(t *testing.T) {
	r := newPEPRig(t)
	r.linkAlice()
	setEndpoint := func(endpoint string) {
		r.srv.do(func() {
			rec, _, _ := r.srv.connectors.Get(r.worker.ID)
			rec.Endpoint = endpoint
			if err := r.srv.connectors.Save(rec); err != nil {
				t.Fatal(err)
			}
			r.srv.refreshGoverned()
		})
	}
	setEndpoint("https://graph.attacker.example/v1.0")
	if code, raw := r.as(r.admin, r.srv.handleSetWorkerIdentity, http.MethodPut, "/",
		`{"mode":"delegated-per-job","enforcement":"enforce","actAs":"initiator"}`, map[string]string{"id": r.worker.ID}); code != http.StatusBadRequest ||
		!strings.Contains(string(raw), "Microsoft Graph") {
		t.Fatalf("a delegated identity was set on a Worker that sends elsewhere: %d %s", code, raw)
	}
	// Were the store to say otherwise anyway, the enforcement point refuses before it
	// decides, so no Permit is recorded that nothing may act on.
	setEndpoint("")
	r.govern("delegated-per-job", "enforce", `,"actAs":"initiator"`)
	setEndpoint("https://graph.attacker.example/v1.0")
	pi := r.startAsAlice()
	job, lease := r.lease()
	if code, _ := r.authorize(r.internal, job, lease); code != http.StatusConflict {
		t.Fatalf("brokered to a non-Graph endpoint: %d", code)
	}
	if ev := r.evidence(pi); len(ev) != 0 {
		t.Fatalf("recorded %d decisions for a refused broker request", len(ev))
	}
}

func TestTheDelegatedEndpointIsMicrosoftGraph(t *testing.T) {
	for endpoint, want := range map[string]bool{
		"":                                             true,
		"https://graph.microsoft.com/v1.0":             true,
		"https://GRAPH.microsoft.com/beta":             true,
		"https://graph.microsoft.us/v1.0":              true,
		"https://dod-graph.microsoft.us/v1.0":          true,
		"https://microsoftgraph.chinacloudapi.cn/v1.0": true,
		"https://graph.microsoft.com:443/v1.0":         true,
		"http://graph.microsoft.com/v1.0":              false,
		"https://graph.microsoft.com:8443/v1.0":        false,
		"https://graph.microsoft.com.attacker.example": false,
		"https://user@graph.microsoft.com/v1.0":        false,
		"https://attacker.example/graph.microsoft.com": false,
		"graph.microsoft.com":                          false,
	} {
		if got := delegatedEndpointAllowed(endpoint); got != want {
			t.Errorf("%q: %v, want %v", endpoint, got, want)
		}
	}
}

// With --in-process-connectors the engine would run a SharePoint task itself, with
// the Worker's own credential, without asking, and complete the job past the
// completion guard. A governed Worker is therefore never built in-process.
func TestAGovernedWorkerNeverRunsInsideTheEngine(t *testing.T) {
	r := newPEPRig(t)
	// Setting the identity policy rebuilds the registries itself: no other change has
	// to come first for the in-engine client to go.
	r.govern("service", "enforce", "")
	if _, ok := r.srv.sharePointRegistry.Client("sp-prod"); ok {
		t.Fatal("a governed Worker has an in-engine client")
	}
	if e := r.srv.sharePointRegistry.Unresolved("sharepoint", "sp-prod"); !strings.Contains(e.Error(), "governed") {
		t.Fatalf("an in-process task would park with %q", e)
	}
	// The Console does not call that a problem: a governed Worker served by a
	// supervised Worker Instance works as intended.
	if p := r.srv.connectorProblem(connectorKindSharePoint, "sp-prod"); p != "" {
		t.Fatalf("the Console shows %q", p)
	}
}

func TestOnlyTheProcessServingADelegatedWorkerGetsTheBrokerKey(t *testing.T) {
	r := newPEPRig(t)
	has := func() bool {
		for _, kv := range r.srv.sharepointWorkerEnv() {
			if kv == brokerKeyEnv+"="+r.srv.brokerKey {
				return true
			}
		}
		return false
	}
	r.srv.do(func() {})
	r.govern("service", "shadow", "")
	if has() {
		t.Fatal("a service-mode SharePoint Worker's process was handed the broker key")
	}
	r.linkAlice()
	r.govern("delegated-per-job", "enforce", `,"actAs":"initiator"`)
	if !has() {
		t.Fatal("the process serving a delegated Worker was not handed the broker key")
	}
}

// A Worker's credential reference names a vault entry the server reads — and, for a
// SQL connection string or a provisioned clio key, writes. A reference into the
// reserved regions would let a Worker editor read a person's refresh token through a
// Worker, or have it overwritten, so none is accepted.
func TestAWorkerCannotReferenceAReservedVaultName(t *testing.T) {
	srv := newServerWithOptions(t)
	reserved := "atlas:connection-credential:con_victim"
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/configured-workers",
		`{"name":"sp-x","kind":"sharepoint","credentialsRef":"`+reserved+`"}`, "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("created with a reserved reference: %d %s", code, raw)
	}
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/configured-workers",
		`{"name":"sp-y","kind":"sharepoint","credentialsRef":"sp-bundle"}`, "application/json")
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create: %d %s", code, raw)
	}
	var rec connector
	_ = json.Unmarshal(raw, &rec)
	code, raw = serveInternal(t, srv, http.MethodPatch, "/api/v1/configured-workers/"+rec.ID,
		`{"credentialsRef":"`+reserved+`"}`, "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("changed to a reserved reference: %d %s", code, raw)
	}
}

// The canonical resource is a list of name=value segments. A value that carries the
// separators — a list a model names "Inv;path=/x", a FEEL value from a variable — is
// escaped, so it cannot forge a segment a resource prefix would then match.
func TestTheResourceEscapesItsSeparators(t *testing.T) {
	res, _ := sharePointAction(map[string]any{"site": "contoso.sharepoint.com:/sites/hr",
		"list": "Inv;path=/Secret", "path": "/a%b.docx"})
	want := "sharepoint:site=contoso.sharepoint.com:/sites/hr;list=Inv%3Bpath%3D/Secret;path=/a%25b.docx"
	if res != want {
		t.Fatalf("resource = %q, want %q", res, want)
	}
}

// The supervised SharePoint Worker's environment says whether a Worker is brokered and
// carries the broker key, so setting or clearing an identity policy restarts it —
// otherwise it would act on the old environment, with the app credential, until
// something else happened to restart it.
func TestSettingAnIdentityPolicyRestartsTheSupervisedWorker(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	r := newPEPRig(t)
	r.linkAlice()
	quit := make(chan struct{})
	sup := newSupervisor(quit)
	sup.exe, sup.backoff = "sh", time.Millisecond
	spec := SuperviseSpec{ID: "sharepoint", Connectors: []string{"sharepoint"}}
	sup.add(spec, []string{"-c", "sleep 30"}, r.srv.superviseEnv(spec))
	sup.start()
	r.srv.supervisor = sup
	t.Cleanup(func() { close(quit); sup.wait() })
	waitFor(t, "the child to report running", func() bool {
		list := sup.list()
		return len(list) == 1 && list[0].State == "running"
	})
	first := sup.list()[0].Starts

	r.govern("delegated-per-job", "enforce", `,"actAs":"initiator"`)
	waitFor(t, "the SharePoint Worker to restart with its brokered environment", func() bool {
		return sup.list()[0].Starts > first
	})
	second := sup.list()[0].Starts
	if code, _ := r.as(r.admin, r.srv.handleClearWorkerIdentity, http.MethodDelete, "/", "", map[string]string{"id": r.worker.ID}); code != http.StatusNoContent {
		t.Fatalf("clear: %d", code)
	}
	waitFor(t, "the SharePoint Worker to restart without it", func() bool {
		return sup.list()[0].Starts > second
	})
}
