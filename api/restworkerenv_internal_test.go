package api

import (
	"fmt"
	"net/http"
	"testing"
)

// REST is offloaded by default (ADR-0233), and the
// reason it can be is that the engine hands its auth secrets over. A REST task's
// endpoint travels with the job; the secret behind its authSecret reference does not,
// because a reference is resolved where it is used — and a supervised worker has no
// vault to resolve it against.
//
// Without this handover the default would have moved every authenticated REST task to
// a process that fails it with "auth secret is not configured", which is the failure
// AD had before ADR-0182 and the one this mirrors.

// restModel is a one-task model whose REST task authenticates with a secret reference.
func restModel(procID, auth string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs-%s">
  <bpmn:process id="%s" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:restConnector method="get" url="https://api.example.com/kunden" resultVariable="r" %s/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`, procID, procID, auth)
}

func deployRESTModel(t *testing.T, srv *Server, xml string) {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", xml, "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
}

// The secret a deployed model names reaches the worker under the name the worker
// reads it by — and only that one. The vault holds a second secret nothing deploys,
// and handing it over would give the worker more of the vault than the running models
// use.
func TestRESTWorkerEnvHandsOverTheReferencesModelsName(t *testing.T) {
	srv, _ := newValidateServer(t)
	for ref, value := range map[string]string{"API_KUNDEN": "s3cr3t", "API_UNUSED": "nobody-asks"} {
		if _, err := srv.vault.Set(ref, value); err != nil {
			t.Fatalf("vault.Set %s: %v", ref, err)
		}
	}
	deployRESTModel(t, srv, restModel("rest-auth", `authType="bearer" authSecret="API_KUNDEN"`))

	env := envOf(t, srv.restWorkerEnv())
	if got := env["ATLAS_CONNECTOR_API_KUNDEN_TOKEN"]; got != "s3cr3t" {
		t.Errorf("ATLAS_CONNECTOR_API_KUNDEN_TOKEN = %q, want the deployed model's secret", got)
	}
	if _, handed := env["ATLAS_CONNECTOR_API_UNUSED_TOKEN"]; handed {
		t.Error("a secret no deployed model names was handed to the worker; only what is deployed travels")
	}
}

// An OAuth2 client-credentials task references its client secret the same way, so it
// is handed over the same way — the worker fetches the token itself (ADR-0152) and
// needs the secret to do it.
func TestRESTWorkerEnvCoversOAuth2ClientSecrets(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("OAUTH_CLIENT", "cs"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	deployRESTModel(t, srv, restModel("rest-oauth",
		`authType="oauth2" authSecret="OAUTH_CLIENT" authTokenUrl="https://id.example.com/token" authClientId="atlas"`))

	if got := envOf(t, srv.restWorkerEnv())["ATLAS_CONNECTOR_OAUTH_CLIENT_TOKEN"]; got != "cs" {
		t.Errorf("client secret = %q, want it handed to the worker that performs the grant", got)
	}
}

// A task calling an open endpoint names no secret, and nothing is rendered for it. An
// empty variable would read as a configured blank credential.
func TestRESTWorkerEnvIsEmptyWithoutAuthenticatedTasks(t *testing.T) {
	srv, _ := newValidateServer(t)
	deployRESTModel(t, srv, restModel("rest-open", ""))
	if env := srv.restWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a model that authenticates nowhere", env)
	}
}

// The two kinds this record moves are in the default set, and the guard tests above
// (TestEveryDefaultOffloadedKindCanBeServedByItsWorker) hold that each can be served.
// This pins the intent rather than the mechanism: a fresh install must not make an
// outbound HTTP call from the engine's own process.
func TestRESTAndLdifAreOffloadedByDefault(t *testing.T) {
	defaults := map[string]bool{}
	for _, kind := range DefaultOffloadedKinds() {
		defaults[kind] = true
	}
	for _, kind := range []string{"rest", "ldif"} {
		if !defaults[kind] {
			t.Errorf("%q is not offloaded by default: a fresh install still runs it on the engine's run loop", kind)
		}
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()["rest"]; !provisioned {
		t.Error("rest is defaulted onto a worker but its auth secrets are not handed over")
	}
}
