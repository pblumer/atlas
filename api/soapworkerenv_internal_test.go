package api

import (
	"fmt"
	"net/http"
	"testing"
)

// SOAP is offloaded by default (ADR-0233, slice 4) on the same condition REST is: the
// engine hands its auth secrets over. A SOAP task's endpoint, SOAPAction and envelope
// body travel with the job; the secret behind its authSecret reference does not,
// because a reference is resolved where it is used — and a supervised worker has no
// vault to resolve it against.

// soapModel is a one-task model whose SOAP task carries the given auth attributes.
func soapModel(procID, auth string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs-%s">
  <bpmn:process id="%s" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:soapConnector endpoint="https://example.com/svc" operation="GetRate"
                             body="&lt;req/&gt;" resultVariable="r" %s/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`, procID, procID, auth)
}

func deploySoapModel(t *testing.T, srv *Server, xml string) {
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
func TestSoapWorkerEnvHandsOverTheReferencesModelsName(t *testing.T) {
	srv, _ := newValidateServer(t)
	for ref, value := range map[string]string{"WSDL_SVC": "s3cr3t", "WSDL_UNUSED": "nobody-asks"} {
		if _, err := srv.vault.Set(ref, value); err != nil {
			t.Fatalf("vault.Set %s: %v", ref, err)
		}
	}
	deploySoapModel(t, srv, soapModel("soap-auth", `authType="basic" authUsername="svc" authSecret="WSDL_SVC"`))

	env := envOf(t, srv.soapWorkerEnv())
	if got := env["ATLAS_CONNECTOR_WSDL_SVC_TOKEN"]; got != "s3cr3t" {
		t.Errorf("ATLAS_CONNECTOR_WSDL_SVC_TOKEN = %q, want the deployed model's secret", got)
	}
	if _, handed := env["ATLAS_CONNECTOR_WSDL_UNUSED_TOKEN"]; handed {
		t.Error("a secret no deployed model names was handed to the worker; only what is deployed travels")
	}
}

// A task calling an open service names no secret, and nothing is rendered for it. An
// empty variable would read as a configured blank credential.
func TestSoapWorkerEnvIsEmptyWithoutAuthenticatedTasks(t *testing.T) {
	srv, _ := newValidateServer(t)
	deploySoapModel(t, srv, soapModel("soap-open", ""))
	if env := srv.soapWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a model that authenticates nowhere", env)
	}
}

// REST and SOAP author their credentials identically, and the collector is now one
// function called with two job types. This is what holds that sharing honest: a
// deployment carrying both kinds must hand over both secrets, and neither collector
// may pick up the other kind's tasks.
func TestRESTAndSOAPSecretsAreCollectedSeparatelyButBothArrive(t *testing.T) {
	srv, _ := newValidateServer(t)
	for ref, value := range map[string]string{"REST_ONE": "r", "SOAP_ONE": "s"} {
		if _, err := srv.vault.Set(ref, value); err != nil {
			t.Fatalf("vault.Set %s: %v", ref, err)
		}
	}
	deployRESTModel(t, srv, restModel("both-rest", `authType="bearer" authSecret="REST_ONE"`))
	deploySoapModel(t, srv, soapModel("both-soap", `authType="bearer" authSecret="SOAP_ONE"`))

	restEnv, soapEnv := envOf(t, srv.restWorkerEnv()), envOf(t, srv.soapWorkerEnv())
	if got := restEnv["ATLAS_CONNECTOR_REST_ONE_TOKEN"]; got != "r" {
		t.Errorf("the REST worker did not get the REST task's secret: %q", got)
	}
	if got := soapEnv["ATLAS_CONNECTOR_SOAP_ONE_TOKEN"]; got != "s" {
		t.Errorf("the SOAP worker did not get the SOAP task's secret: %q", got)
	}
	// Each worker gets its own kind's secrets and no more. A collector that ignored
	// the job type would hand the SOAP worker the REST credential too, which is the
	// failure a shared implementation makes easy and this makes visible.
	if _, crossed := restEnv["ATLAS_CONNECTOR_SOAP_ONE_TOKEN"]; crossed {
		t.Error("the REST worker was handed the SOAP task's secret")
	}
	if _, crossed := soapEnv["ATLAS_CONNECTOR_REST_ONE_TOKEN"]; crossed {
		t.Error("the SOAP worker was handed the REST task's secret")
	}
}

// The kind this slice moves is in the default set and its handover is registered.
func TestSoapIsOffloadedByDefault(t *testing.T) {
	defaults := map[string]bool{}
	for _, kind := range DefaultOffloadedKinds() {
		defaults[kind] = true
	}
	if !defaults["soap"] {
		t.Error("soap is not offloaded by default: a fresh install still calls a web service from the engine's run loop")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()["soap"]; !provisioned {
		t.Error("soap is defaulted onto a worker but its auth secrets are not handed over")
	}
}
