package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The acceptance suite for ADR-draft-deploying-one-decision: an author can deploy
// the decision in front of them, alone, and what arrives at the runtime is
// indistinguishable from a decision an application publish deployed.
//
// It reuses the ADR-0319 fixtures next door — eligibilityDMN, discountDMN,
// eligibilityProcess and bootDecisionStack — deliberately: the claim under test is
// that this route reaches the *same* durable path, so it has to be measured with
// the same instruments.

// deployOneDecision posts DMN XML to the single-decision deploy and decodes the
// report. It is the editor's Deploy button, expressed as a call.
func deployOneDecision(t *testing.T, x deployTestHarness, query, dmnXML string) deployDecisionResp {
	t.Helper()
	code, b := x.do(http.MethodPost, "/api/v1/decision-deployments"+query, dmnXML)
	if code != http.StatusOK {
		t.Fatalf("deploy decision: %d %s", code, b)
	}
	var rep deployDecisionResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode deploy report: %v (%s)", err, b)
	}
	return rep
}

// seedReferencedDecision puts a decision in the model folder and gives it a
// reference, which is what makes it nameable by a business rule task. It is the
// state an editor session starts from when the decision already exists.
func seedReferencedDecision(t *testing.T, x deployTestHarness, handle, dmnXML string) {
	t.Helper()
	uploadModel(t, x, handle, dmnXML)
	x.addRef("", strings.ToUpper(handle[:1])+handle[1:], handle)
}

// TestDeployingOneDecisionMakesItRunnable is the headline criterion, and it
// separates the two things the editor used to conflate. The model folder holds
// "approve"; the author deploys what is on screen, which says "vip". Afterwards
// the runtime answers vip and the model still reads approve — Deploy changed the
// runtime, Save to model changes the shared layer, and neither does the other's
// job.
func TestDeployingOneDecisionMakesItRunnable(t *testing.T) {
	srv, dir := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	seedReferencedDecision(t, x, "eligibility", eligibilityDMN("approve"))
	before := modelFiles(t, dir)

	rep := deployOneDecision(t, x, "?modelRef=eligibility", eligibilityDMN("vip"))

	if rep.Key == 0 || len(rep.Decisions) != 1 {
		t.Fatalf("deploy report = %+v, want one decision under a runtime key", rep)
	}
	dec := rep.Decisions[0]
	if dec.DecisionID != "eligibility" || dec.Version != 1 || dec.Key != rep.Key || !dec.Current {
		t.Fatalf("decision = %+v, want eligibility v1 current under key %d", dec, rep.Key)
	}
	if dec.ResourceName != "eligibility.dmn" || dec.ModelRef != "eligibility" || dec.Checksum == "" {
		t.Fatalf("decision = %+v, want it to carry its handle and a checksum of what was deployed", dec)
	}

	// It is a runtime artifact like any other: listed, and its exact source readable
	// back from the record rather than re-exported from the model folder.
	rows := listDecisionDeployments(t, x, "?decisionId=eligibility")
	if len(rows) != 1 || rows[0].Key != rep.Key {
		t.Fatalf("listing = %+v, want the decision just deployed", rows)
	}
	code, xml := x.do(http.MethodGet, fmt.Sprintf("/api/v1/decision-deployments/%d/xml", rep.Key), "")
	if code != http.StatusOK || !strings.Contains(string(xml), `"vip"`) {
		t.Fatalf("deployed DMN XML: %d %s, want the bytes that were deployed", code, xml)
	}

	// A process deployed afterwards binds to the deployment, not to the model that
	// was bundled with it — which is what proves the deploy reached the registry and
	// the latest pointer, not merely the store.
	key := deployProcess(t, x, eligibilityProcess("orders", "latest"))
	if got := runAndReadVerdict(t, x, key, "orders"); got != "vip" {
		t.Fatalf("verdict = %q, want vip (the deployed decision)", got)
	}

	// And the shared layer is untouched: the same handles, and eligibility.dmn still
	// says what it said.
	if after := modelFiles(t, dir); len(after) != len(before) {
		t.Fatalf("model folder = %v, want it untouched (%v) — Deploy is not Save to model", after, before)
	}
	model, err := os.ReadFile(filepath.Join(dir, "dmn-models", "eligibility.dmn"))
	if err != nil || !strings.Contains(string(model), `"approve"`) {
		t.Fatalf("stored model = %s (%v), want it still reading approve", model, err)
	}
}

// TestADecisionDeployedWithoutAModelCannotBeNamedByATask pins the trade-off the
// ADR accepts rather than hides. A decision deployed from the editor and never
// saved to the model is a real runtime artifact — keyed, versioned, evaluable in
// its own right — but a business rule task naming it is still refused at deploy
// time, because what a task may name is what a reference resolves. The editor has
// to say so; the server refuses the process, not the decision.
func TestADecisionDeployedWithoutAModelCannotBeNamedByATask(t *testing.T) {
	srv, dir := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	rep := deployOneDecision(t, x, "", eligibilityDMN("approve"))
	if got := rep.Decisions[0]; !got.Current || got.ModelRef != "" {
		t.Fatalf("decision = %+v, want it deployed and current, claiming no model", got)
	}
	if models := modelFiles(t, dir); len(models) != 2 { // dish + broken, the seeded pair
		t.Fatalf("model folder = %v, want the deploy to have added nothing", models)
	}

	code, b := x.do(http.MethodPost, "/api/v1/deployments", eligibilityProcess("orders", "latest"))
	if code != http.StatusConflict {
		t.Fatalf("deploy a process naming it = %d %s, want 409", code, b)
	}
	if !strings.Contains(string(b), "add its reference") {
		t.Fatalf("refusal = %s, want it to name the missing reference", b)
	}
}

// TestDeployingTheSameDecisionAgainVersionsIt: the second deploy of a decision id
// is a new version under a new key, exactly as a second publish is. The version
// counter is per decision id and lives on the run loop, so it does not matter
// which route reached it.
func TestDeployingTheSameDecisionAgainVersionsIt(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	seedReferencedDecision(t, x, "eligibility", eligibilityDMN("approve"))

	v1 := deployOneDecision(t, x, "?modelRef=eligibility", eligibilityDMN("approve"))
	procA := deployProcess(t, x, eligibilityProcess("proc-a", "latest"))

	v2 := deployOneDecision(t, x, "?modelRef=eligibility", eligibilityDMN("vip"))
	if v2.Decisions[0].Version != 2 || v2.Key == v1.Key {
		t.Fatalf("second deploy = %+v, want v2 under a new key (v1 was %+v)", v2, v1)
	}

	// A process deployed before does not move (ADR-0319's deploy-time pinning); one
	// deployed after resolves latest to the new version.
	if got := runAndReadVerdict(t, x, procA, "proc-a"); got != "approve" {
		t.Fatalf("A after v2 was deployed: verdict = %q, want approve (still v1)", got)
	}
	procB := deployProcess(t, x, eligibilityProcess("proc-b", "latest"))
	if got := runAndReadVerdict(t, x, procB, "proc-b"); got != "vip" {
		t.Fatalf("B: verdict = %q, want vip (v2)", got)
	}

	rows := listDecisionDeployments(t, x, "?decisionId=eligibility")
	if len(rows) != 2 || !rows[0].Current || rows[0].Version != 2 || rows[1].Current {
		t.Fatalf("listing = %+v, want v2 current and v1 superseded", rows)
	}
}

// TestADecisionDeployedFromTheEditorSurvivesARestart: the record is written
// before anything is registered (I2), so a reboot over the same data directory
// rebuilds the registry from it. The deployed model answers "vip" while the model
// folder answers "approve", so what comes back after the restart says which of
// the two was persisted.
func TestADecisionDeployedFromTheEditorSurvivesARestart(t *testing.T) {
	dir := t.TempDir()

	first := bootDecisionStack(t, dir)
	seedReferencedDecision(t, first.x, "eligibility", eligibilityDMN("approve"))
	rep := deployOneDecision(t, first.x, "?modelRef=eligibility", eligibilityDMN("vip"))
	procKey := deployProcess(t, first.x, eligibilityProcess("orders", "latest"))
	if got := runAndReadVerdict(t, first.x, procKey, "orders"); got != "vip" {
		t.Fatalf("before restart: verdict = %q, want vip", got)
	}
	first.shutdown()

	second := bootDecisionStack(t, dir)
	defer second.shutdown()

	rows := listDecisionDeployments(t, second.x, "")
	if len(rows) != 1 || rows[0].Key != rep.Key || rows[0].Version != 1 {
		t.Fatalf("after restart: listing = %+v, want key %d at v1", rows, rep.Key)
	}
	if got := runAndReadVerdict(t, second.x, procKey, "orders"); got != "vip" {
		t.Fatalf("after restart: verdict = %q, want vip (the deployed record, not the model folder)", got)
	}
}

// TestADeployedDecisionNamesItselfWhenItHasNoModel covers the provenance the
// route is honest about. A decision that has never been written to the model has
// no handle to claim, so the record is named after its own decision id rather
// than a file that does not exist.
func TestADeployedDecisionNamesItselfWhenItHasNoModel(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	loose := deployOneDecision(t, x, "", eligibilityDMN("approve"))
	if got := loose.Decisions[0]; got.ResourceName != "eligibility.dmn" || got.ModelRef != "" {
		t.Fatalf("decision without a model = %+v, want it named after its decision id and claiming no model", got)
	}

	// The handle goes through the same sanitizer the model upload uses, so a
	// traversal cannot be recorded as provenance and cannot name a path.
	evil := deployOneDecision(t, x, "?modelRef=../../etc/passwd", discountDMN)
	if got := evil.Decisions[0]; strings.ContainsAny(got.ModelRef, "/.") || got.ResourceName != got.ModelRef+".dmn" {
		t.Fatalf("decision with a hostile handle = %+v, want the handle reduced to a plain one", got)
	}
}

// TestDeployingADecisionRefusesWhatCannotRun: everything that could not be
// evaluated is refused before a key is spent or a record is written, off the run
// loop. "Nothing to deploy" is its own refusal — a model with no decision in it
// compiles, and would otherwise be written as a deployment providing nothing.
func TestDeployingADecisionRefusesWhatCannotRun(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	const noDecisions = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs_empty" name="Empty" namespace="http://atlas/dmn">
  <inputData id="id_amount" name="amount"/>
</definitions>`

	for _, tc := range []struct{ name, xml string }{
		{"nothing at all", ""},
		{"malformed xml", "<definitions"},
		{"decision logic that does not compile", brokenDMNModel},
		{"a model declaring no decision", noDecisions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, b := x.do(http.MethodPost, "/api/v1/decision-deployments", tc.xml); code != http.StatusBadRequest {
				t.Fatalf("deploy = %d %s, want 400", code, b)
			}
		})
	}
	if rows := listDecisionDeployments(t, x, ""); len(rows) != 0 {
		t.Fatalf("decision deployments = %+v, want none — a refused deploy writes nothing", rows)
	}
}

// TestFilingADecisionDeploymentNeedsEditorInThatApplication: naming an
// application is a write on it (ADR-0071), the same check the single-diagram
// deploy makes, and an unknown id is the caller error it is rather than a
// deployment filed under nothing.
func TestFilingADecisionDeploymentNeedsEditorInThatApplication(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())
	srv.do(func() {
		if err := srv.projects.Save(project{
			ID: "app1", Name: "Order Management", OwnerID: "usr_owner", Visibility: VisibilityShared,
			Members: []projectMember{
				{Ref: principalRef{Type: PrincipalTypeUser, ID: "usr_editor"}, Role: ScopeRoleEditor},
				{Ref: principalRef{Type: PrincipalTypeUser, ID: "usr_viewer"}, Role: ScopeRoleViewer},
			},
		}); err != nil {
			t.Fatalf("save project: %v", err)
		}
	})
	h := srv.Handler()
	as := func(t *testing.T, userID, method, path, body string) (int, []byte) {
		t.Helper()
		tok, err := srv.sessions.create(User{ID: userID, Username: userID, Roles: []string{RoleModeler, RoleOperator, RoleUser}}, nil)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	for _, tc := range []struct {
		name, user, query string
		want              int
	}{
		{"an editor files it", "usr_editor", "?projectId=app1", http.StatusOK},
		{"a viewer may not", "usr_viewer", "?projectId=app1", http.StatusForbidden},
		{"a stranger is not told it exists", "usr_stranger", "?projectId=app1", http.StatusNotFound},
		{"an unknown application is a caller error", "usr_owner", "?projectId=nope", http.StatusBadRequest},
		{"no application at all is fine", "usr_stranger", "", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, b := as(t, tc.user, http.MethodPost, "/api/v1/decision-deployments"+tc.query, eligibilityDMN("approve"))
			if code != tc.want {
				t.Fatalf("deploy = %d %s, want %d", code, b, tc.want)
			}
		})
	}

	// Exactly the two that were allowed were written, and the filed one is
	// attributed to the application it named.
	decode := func(t *testing.T, b []byte) []deployedDecisionResp {
		t.Helper()
		var rows []deployedDecisionResp
		if err := json.Unmarshal(b, &rows); err != nil {
			t.Fatalf("decode listing: %v (%s)", err, b)
		}
		return rows
	}
	_, b := as(t, "usr_owner", http.MethodGet, "/api/v1/decision-deployments?applicationId=app1", "")
	if rows := decode(t, b); len(rows) != 1 || rows[0].ApplicationID != "app1" {
		t.Fatalf("listing for app1 = %+v, want the one deployment an editor filed", rows)
	}
	_, b = as(t, "usr_owner", http.MethodGet, "/api/v1/decision-deployments", "")
	if rows := decode(t, b); len(rows) != 2 {
		t.Fatalf("listing = %+v, want only the two deploys that were authorized", rows)
	}
}

// TestDeployingOneDecisionIsAtomicWhenTheStoreFails is the durability boundary
// (I2) on this route: a record that cannot be written registers nothing, so the
// decision does not become evaluable behind a deploy the caller was told failed.
func TestDeployingOneDecisionIsAtomicWhenTheStoreFails(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	srv.decisionDeploys = brokenStore(newDecisionStore(filepath.Join(t.TempDir(), "gone")))

	if code, b := x.do(http.MethodPost, "/api/v1/decision-deployments", eligibilityDMN("approve")); code != http.StatusInternalServerError {
		t.Fatalf("deploy = %d %s, want 500", code, b)
	}
	srv.do(func() {
		if key, ok := srv.dmnRegistry.LatestDecisionKey("eligibility"); ok {
			t.Errorf("the registry holds decision deployment %d after a failed write", key)
		}
	})
}

// TestDecisionResourceNameFallsBackToAGenericName covers the naming rule's own
// branches directly: a decision id that sanitizes to nothing still has to produce
// a file-shaped name for the release manifest and the listing.
func TestDecisionResourceNameFallsBackToAGenericName(t *testing.T) {
	for _, tc := range []struct {
		name      string
		modelRef  string
		decisions []string
		want      string
	}{
		{"a handle wins", "eligibility", []string{"other"}, "eligibility.dmn"},
		{"the first usable decision id", "", []string{"../..", "eligibility"}, "eligibility.dmn"},
		{"nothing usable at all", "", []string{"../.."}, "decision.dmn"},
		{"no decisions at all", "", nil, "decision.dmn"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := decisionResourceName(tc.modelRef, tc.decisions); got != tc.want {
				t.Fatalf("decisionResourceName = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestADeployedDecisionRecordsWhoDeployedItAndWhen: the deploy stamps who and
// when from the request, the way every other durable record in this package does,
// so an operator reading the listing can tell a decision somebody deployed from
// the editor apart from one a publish shipped.
func TestADeployedDecisionRecordsWhoDeployedItAndWhen(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())
	h := srv.Handler()
	tok, err := srv.sessions.create(User{ID: "usr_author", Username: "author", Roles: []string{RoleModeler, RoleUser}}, nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	before := time.Now().Unix()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/decision-deployments", strings.NewReader(discountDMN))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deploy = %d %s", rec.Code, rec.Body)
	}
	var rep deployDecisionResp
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := rep.Decisions[0]
	if got.DeployedBy != "usr_author" || got.DeployedAt < before {
		t.Fatalf("decision = %+v, want it stamped with usr_author at or after %d", got, before)
	}
}
