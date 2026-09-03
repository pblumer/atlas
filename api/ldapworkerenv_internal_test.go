package api

import (
	"fmt"
	"net/http"
	"testing"
)

// LDAP is offloaded by default (ADR-0233, slice 3), and the reason it can be is that
// the engine hands its directory credentials over. An LDAP task's server and bind DN
// travel with the job; the secrets behind its bindSecret and clientCertSecret
// references do not, because a reference is resolved where it is used — and a
// supervised worker has no vault to resolve it against.
//
// Without this handover the default would have moved every authenticating LDAP task
// to a process that fails it naming a variable nobody set, which is the failure AD
// had before ADR-0182 and the one this mirrors.

// ldapModel is a one-task model whose LDAP task carries the given attributes.
func ldapModel(procID, attrs string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs-%s">
  <bpmn:process id="%s" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:ldapConnector url="ldaps://dc.example.com" operation="search"
                             baseDN="dc=example,dc=com" filter="(objectClass=person)"
                             resultVariable="found" %s/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`, procID, procID, attrs)
}

func deployLdapModel(t *testing.T, srv *Server, xml string) {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", xml, "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
}

// The bind password a deployed model names reaches the worker under the name the
// worker reads it by — and only that one. The vault holds a second secret nothing
// deploys, and handing it over would give the worker more of the vault than the
// running models use.
func TestLdapWorkerEnvHandsOverTheReferencesModelsName(t *testing.T) {
	srv, _ := newValidateServer(t)
	for ref, value := range map[string]string{"DC_BIND": "s3cr3t", "DC_UNUSED": "nobody-asks"} {
		if _, err := srv.vault.Set(ref, value); err != nil {
			t.Fatalf("vault.Set %s: %v", ref, err)
		}
	}
	deployLdapModel(t, srv, ldapModel("ldap-bind", `bindDN="cn=svc,dc=example,dc=com" bindSecret="DC_BIND"`))

	env := envOf(t, srv.ldapWorkerEnv())
	if got := env["ATLAS_CONNECTOR_DC_BIND_TOKEN"]; got != "s3cr3t" {
		t.Errorf("ATLAS_CONNECTOR_DC_BIND_TOKEN = %q, want the deployed model's bind password", got)
	}
	if _, handed := env["ATLAS_CONNECTOR_DC_UNUSED_TOKEN"]; handed {
		t.Error("a secret no deployed model names was handed to the worker; only what is deployed travels")
	}
}

// A client certificate is the other way an LDAP task authenticates, and it fails the
// same way if it does not arrive: a bind that cannot present an identity. So it is
// handed over on the same terms as the password — the reason both references are
// collected rather than only the obvious one.
func TestLdapWorkerEnvCoversClientCertificates(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("DC_CERT", "-----BEGIN CERTIFICATE-----"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	deployLdapModel(t, srv, ldapModel("ldap-cert", `clientCertSecret="DC_CERT"`))

	if got := envOf(t, srv.ldapWorkerEnv())["ATLAS_CONNECTOR_DC_CERT_TOKEN"]; got != "-----BEGIN CERTIFICATE-----" {
		t.Errorf("client certificate = %q, want it handed to the worker that presents it", got)
	}
}

// An anonymous bind over plain LDAP names no secret, and nothing is rendered for it.
// An empty variable would read as a configured blank password.
func TestLdapWorkerEnvIsEmptyWithoutCredentials(t *testing.T) {
	srv, _ := newValidateServer(t)
	deployLdapModel(t, srv, ldapModel("ldap-anon", ""))
	if env := srv.ldapWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a model that binds anonymously", env)
	}
}

// The kind this slice moves is in the default set and its handover is registered.
// The guard tests (TestEveryDefaultOffloadedKindCanBeServedByItsWorker) hold that the
// two agree; this pins the intent rather than the mechanism — a fresh install must not
// bind to somebody's directory from the engine's own process.
func TestLdapIsOffloadedByDefault(t *testing.T) {
	defaults := map[string]bool{}
	for _, kind := range DefaultOffloadedKinds() {
		defaults[kind] = true
	}
	if !defaults["ldap"] {
		t.Error("ldap is not offloaded by default: a fresh install still binds to a directory on the engine's run loop")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()["ldap"]; !provisioned {
		t.Error("ldap is defaulted onto a worker but its directory credentials are not handed over")
	}
}
