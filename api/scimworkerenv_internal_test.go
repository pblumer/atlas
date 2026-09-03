package api

import (
	"fmt"
	"net/http"
	"testing"
)

// SCIM is offloaded by default (ADR-0233, slice 6) on the condition REST and SOAP
// are: the engine hands its auth secrets over. A SCIM task's base URL and resource
// travel with the job; the secret behind its authSecret reference does not, because a
// reference is resolved where it is used.

// scimModel is a one-task model whose SCIM task carries the given auth attributes.
func scimModel(procID, auth string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs-%s">
  <bpmn:process id="%s" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:scimConnector baseUrl="https://idp.example.com/scim/v2" resource="Users"
                             operation="search" resultVariable="r" %s/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`, procID, procID, auth)
}

func deployScimModel(t *testing.T, srv *Server, xml string) {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", xml, "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
}

// The secret a deployed model names reaches the worker under the name the worker
// reads it by — and only that one.
func TestScimWorkerEnvHandsOverTheReferencesModelsName(t *testing.T) {
	srv, _ := newValidateServer(t)
	for ref, value := range map[string]string{"IDP_TOKEN": "s3cr3t", "IDP_UNUSED": "nobody-asks"} {
		if _, err := srv.vault.Set(ref, value); err != nil {
			t.Fatalf("vault.Set %s: %v", ref, err)
		}
	}
	deployScimModel(t, srv, scimModel("scim-auth", `authType="bearer" authSecret="IDP_TOKEN"`))

	env := envOf(t, srv.scimWorkerEnv())
	if got := env["ATLAS_CONNECTOR_IDP_TOKEN_TOKEN"]; got != "s3cr3t" {
		t.Errorf("ATLAS_CONNECTOR_IDP_TOKEN_TOKEN = %q, want the deployed model's secret", got)
	}
	if _, handed := env["ATLAS_CONNECTOR_IDP_UNUSED_TOKEN"]; handed {
		t.Error("a secret no deployed model names was handed to the worker; only what is deployed travels")
	}
}

// A task calling an open endpoint names no secret, and nothing is rendered for it.
func TestScimWorkerEnvIsEmptyWithoutAuthenticatedTasks(t *testing.T) {
	srv, _ := newValidateServer(t)
	deployScimModel(t, srv, scimModel("scim-open", ""))
	if env := srv.scimWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a model that authenticates nowhere", env)
	}
}

// Three kinds now share one reference collector, called with three job types. This
// holds that sharing honest at full width: a deployment carrying all three hands each
// worker its own kind's secret and none of the others'. A collector that ignored the
// job type would pass every one of the per-kind tests above and fail only this.
func TestTheThreeHTTPKindsCollectTheirOwnSecretsOnly(t *testing.T) {
	srv, _ := newValidateServer(t)
	for ref, value := range map[string]string{"REST_ONE": "r", "SOAP_ONE": "s", "SCIM_ONE": "c"} {
		if _, err := srv.vault.Set(ref, value); err != nil {
			t.Fatalf("vault.Set %s: %v", ref, err)
		}
	}
	deployRESTModel(t, srv, restModel("three-rest", `authType="bearer" authSecret="REST_ONE"`))
	deploySoapModel(t, srv, soapModel("three-soap", `authType="bearer" authSecret="SOAP_ONE"`))
	deployScimModel(t, srv, scimModel("three-scim", `authType="bearer" authSecret="SCIM_ONE"`))

	for _, tc := range []struct {
		kind string
		env  map[string]string
		own  string
	}{
		{"rest", envOf(t, srv.restWorkerEnv()), "REST_ONE"},
		{"soap", envOf(t, srv.soapWorkerEnv()), "SOAP_ONE"},
		{"scim", envOf(t, srv.scimWorkerEnv()), "SCIM_ONE"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			if _, got := tc.env["ATLAS_CONNECTOR_"+tc.own+"_TOKEN"]; !got {
				t.Errorf("the %s worker did not get its own task's secret", tc.kind)
			}
			for _, other := range []string{"REST_ONE", "SOAP_ONE", "SCIM_ONE"} {
				if other == tc.own {
					continue
				}
				if _, crossed := tc.env["ATLAS_CONNECTOR_"+other+"_TOKEN"]; crossed {
					t.Errorf("the %s worker was handed %s, which belongs to another kind's task", tc.kind, other)
				}
			}
		})
	}
}

// The kind this slice moves is in the default set and its handover is registered.
func TestScimIsOffloadedByDefault(t *testing.T) {
	defaults := map[string]bool{}
	for _, kind := range DefaultOffloadedKinds() {
		defaults[kind] = true
	}
	if !defaults["scim"] {
		t.Error("scim is not offloaded by default: a fresh install still provisions users from the engine's run loop")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()["scim"]; !provisioned {
		t.Error("scim is defaulted onto a worker but its auth secrets are not handed over")
	}
}
