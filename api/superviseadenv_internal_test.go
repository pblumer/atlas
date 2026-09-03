package api

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/worker"
)

// What a supervised Active Directory worker is handed at spawn.
//
// AD is the kind whose secret is a *per-task reference* rather than a worker
// record (ADR-0166), and the reference resolves against the engine's vault or its
// environment. A supervised worker inherits the environment and has no vault, so
// offloading AD by default would have left every vault-backed bind password behind —
// which is why the engine renders the references its deployed models actually name.

// A model naming a bind secret the vault holds: the worker is handed that password
// under the reference the model authored, and nothing else.
func TestASupervisedADWorkerIsHandedTheBindSecretsItsModelsName(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("AD_BIND", "s3cr3t"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	deployADModel(t, srv, adModel("joiner", `bindSecret="AD_BIND"`))

	env := envOf(t, srv.adWorkerEnv())
	if got := env["ATLAS_CONNECTOR_AD_BIND_TOKEN"]; got != "s3cr3t" {
		t.Errorf("ATLAS_CONNECTOR_AD_BIND_TOKEN = %q, want the password out of the vault", got)
	}
	if len(env) != 1 {
		t.Errorf("environment = %v, want only the reference the model names", env)
	}
}

// A reference nothing answers to is left out rather than handed over empty: a blank
// variable reads as a configured blank password, and the worker's own error — which
// names the variable to set — is the better failure.
func TestAnADBindSecretNothingAnswersToIsNotHandedOver(t *testing.T) {
	srv, _ := newValidateServer(t)
	deployADModel(t, srv, adModel("joiner", `bindSecret="NOT_SET"`))

	if env := srv.adWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a reference that resolves to nothing", env)
	}
}

// An anonymous bind authors no reference and needs nothing handed over.
func TestAnAnonymousADTaskNeedsNoBindSecret(t *testing.T) {
	srv, _ := newValidateServer(t)
	deployADModel(t, srv, adModel("joiner", ""))

	if env := srv.adWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a model that authors no reference", env)
	}
}

// Every deployed definition contributes, each reference once — a forest reached by
// two models under one service account must not render the same variable twice, and
// two accounts must both arrive.
func TestADBindSecretsAreCollectedOncePerReference(t *testing.T) {
	srv, _ := newValidateServer(t)
	for ref, value := range map[string]string{"AD_BIND": "s3cr3t", "AD_ADMIN": "n0ch-eins"} {
		if _, err := srv.vault.Set(ref, value); err != nil {
			t.Fatalf("vault.Set(%s): %v", ref, err)
		}
	}
	deployADModel(t, srv, adModel("joiner", `bindSecret="AD_BIND"`))
	deployADModel(t, srv, adModel("leaver", `bindSecret="AD_BIND"`))
	deployADModel(t, srv, adModel("mover", `bindSecret="AD_ADMIN"`))

	lines := srv.adWorkerEnv()
	if len(lines) != 2 {
		t.Fatalf("environment = %v, want one entry per distinct reference", lines)
	}
	env := envOf(t, lines)
	if env["ATLAS_CONNECTOR_AD_BIND_TOKEN"] != "s3cr3t" || env["ATLAS_CONNECTOR_AD_ADMIN_TOKEN"] != "n0ch-eins" {
		t.Errorf("environment = %v, want both accounts", env)
	}
}

// Two references that fold to one variable would silently give one of them the
// other's password. The second is left out and said out loud, exactly as two mail
// workers that collide are.
func TestTwoADReferencesThatFoldToOneVariableDoNotShareACredential(t *testing.T) {
	srv, _ := newValidateServer(t)
	for ref, value := range map[string]string{"ad-bind": "erste", "AD.BIND": "zweite"} {
		if _, err := srv.vault.Set(ref, value); err != nil {
			t.Fatalf("vault.Set(%s): %v", ref, err)
		}
	}
	deployADModel(t, srv, adModel("joiner", `bindSecret="ad-bind"`))
	deployADModel(t, srv, adModel("leaver", `bindSecret="AD.BIND"`))

	lines := srv.adWorkerEnv()
	if len(lines) != 1 {
		t.Fatalf("environment = %v, want the colliding second reference left out", lines)
	}
	if got := envOf(t, lines)["ATLAS_CONNECTOR_AD_BIND_TOKEN"]; got != "erste" {
		t.Errorf("ATLAS_CONNECTOR_AD_BIND_TOKEN = %q, want the first reference's own value", got)
	}
}

// A worker that does not serve AD is not handed AD's bind passwords. One worker per
// kind is what keeps a script task from reading the directory's service account.
func TestAScriptWorkerIsNeverGivenTheADBindSecret(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("AD_BIND", "s3cr3t"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	deployADModel(t, srv, adModel("joiner", `bindSecret="AD_BIND"`))

	scripts := envOf(t, srv.superviseEnv(SuperviseSpec{ID: "script", Connectors: []string{"script"}})())
	for name, value := range scripts {
		if strings.Contains(name, "AD_BIND") {
			t.Errorf("a script worker was handed %s=%q", name, value)
		}
	}
	ad := envOf(t, srv.superviseEnv(SuperviseSpec{ID: "ad", Connectors: []string{"ad"}})())
	if got := ad["ATLAS_CONNECTOR_AD_BIND_TOKEN"]; got != "s3cr3t" {
		t.Errorf("the ad worker's secret = %q, want it to hold the credential", got)
	}
}

// The names the engine writes are the names the worker reads — declared in two
// packages, because the engine cannot import the worker. Here the whole path is
// exercised: what the engine rendered configures a real worker, and the bind it then
// performs carries the password. Without it the mock directory refuses the bind, the
// way a domain controller refuses a DN with no password behind it.
func TestSupervisedADEnvUsesTheWorkersOwnNames(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("AD_BIND", "s3cr3t"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	deployADModel(t, srv, adModel("joiner", `bindSecret="AD_BIND"`))
	env := envOf(t, srv.adWorkerEnv())
	env["ATLAS_AD_MOCK"] = "1" // no domain controller in a test; the bind still has to hold

	job := worker.Job{Connector: &worker.ConnectorPayload{Kind: "ad", Fields: map[string]any{
		"url": "ldaps://dc.example.com:636", "bindDN": "cn=svc,dc=example,dc=com",
		"bindSecretRef": "AD_BIND", "operation": "create-user", "dn": "cn=Arno,dc=example,dc=com",
		"attributes": map[string]any{"sAMAccountName": []any{"arno"}},
	}}}

	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, "ad")
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if _, err := built.Handlers[compiler.AdJobType].Run(context.Background(), job); err != nil {
		t.Fatalf("the worker could not bind with what the engine handed it: %v", err)
	}

	// The same worker without the engine's environment cannot bind at all, which is
	// what makes the assertion above about the *name* rather than about mock mode.
	bare, err := worker.BuiltinConnectors(func(k string) string {
		if k == "ATLAS_AD_MOCK" {
			return "1"
		}
		return ""
	}, "ad")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, err := bare.Handlers[compiler.AdJobType].Run(context.Background(), job); err == nil {
		t.Error("a worker holding no bind password bound anyway; the name assertion above proves nothing")
	}
}

// deployADModel deploys one model the way a fresh deploy does, on the run loop.
func deployADModel(t *testing.T, srv *Server, xml string) {
	t.Helper()
	var compErr, persistErr error
	srv.do(func() { _, compErr, persistErr = srv.deployModel([]byte(xml), nil, 1, "", "") })
	if compErr != nil {
		t.Fatalf("compile: %v", compErr)
	}
	if persistErr != nil {
		t.Fatalf("deploy: %v", persistErr)
	}
}

// adModel is a one-task AD process, optionally naming a bind secret.
func adModel(processID, bindSecret string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="` + processID + `" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>
      <atlas:adConnector url="ldaps://dc.example.com:636" bindDN="cn=svc,dc=example,dc=com"
                         ` + bindSecret + ` operation="disable" dn="cn=Arno,dc=example,dc=com"/>
    </bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

// Deploying a model that names a bind secret nothing had named yet reaches the
// worker. Without this the password would sit in the vault, unreachable, until
// somebody restarted Atlas or pressed Restart in the Workers view — and the symptom
// would be a directory task failing on a worker that looks perfectly healthy.
func TestDeployingAModelThatNamesANewBindSecretRestartsTheADWorker(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("AD_BIND", "s3cr3t"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}

	quit := make(chan struct{})
	sup := newSupervisor(quit)
	sup.exe, sup.backoff = "sh", time.Millisecond
	sup.add(SuperviseSpec{ID: "ad", Connectors: []string{"ad"}}, []string{"-c", "sleep 30"},
		srv.superviseEnv(SuperviseSpec{ID: "ad", Connectors: []string{"ad"}}))
	sup.start()
	srv.supervisor = sup
	t.Cleanup(func() { close(quit); sup.wait() })

	waitFor(t, "the child to report running", func() bool {
		list := sup.list()
		return len(list) == 1 && list[0].State == "running"
	})
	first := sup.list()[0].Starts

	deployADModel(t, srv, adModel("joiner", `bindSecret="AD_BIND"`))
	waitFor(t, "the ad worker to come back holding the bind password", func() bool {
		return sup.list()[0].Starts > first
	})

	// A second model naming the same reference changes nothing about the worker, so
	// it is not cycled: an ordinary redeploy must not cost a restart.
	settled := sup.list()[0].Starts
	deployADModel(t, srv, adModel("leaver", `bindSecret="AD_BIND"`))
	srv.refreshSupervisedWorkers()
	if got := sup.list()[0].Starts; got != settled {
		t.Errorf("starts = %d after a deploy that changed nothing, want %d", got, settled)
	}
}

// A reference that folds to nothing — a name with no letter or digit in it — is left
// out rather than rendered as ATLAS_CONNECTOR__TOKEN, which is a variable no operator
// could ever set and which the next reference would collide with.
func TestAnADReferenceThatFoldsToNothingIsLeftOut(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("---", "s3cr3t"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	deployADModel(t, srv, adModel("joiner", `bindSecret="---"`))

	if env := srv.adWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a reference with no variable name in it", env)
	}
}

// A deployment whose model no longer compiles carries no compiled process, and asking
// it for references must skip it rather than panic — recovery can leave one behind.
func TestADeploymentWithNoCompiledProcessContributesNothing(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.do(func() { srv.deployments[99] = &deployment{Key: 99, ProcessID: "kaputt"} })

	if env := srv.adWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing from a deployment with no compiled process", env)
	}
}
