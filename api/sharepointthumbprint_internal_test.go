package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/worker"
)

// A SharePoint instance whose app certificate stays in the Windows certificate store,
// named by its thumbprint (ADR-0141, amended). The bundle travels to the supervised
// worker as a whole, as any SharePoint bundle does — but only where there is a store
// to find the certificate in — and the engine never asks the store on its run loop.

const spThumbprintBundle = `{"method":"clientCredentials","tenantId":"t-1","clientId":"c-1","certificateThumbprint":"02CB6038F2E91693AE8D31C983569366034B2100"}`

func TestSupervisedSharePointEnvHandsOverAThumbprintBundleWhereThereIsAStore(t *testing.T) {
	withCertStore(t, true)
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveSharePointConnector(t, srv, "1", "intranet", "", "sp-creds")
	t.Setenv("ATLAS_CONNECTOR_SP_CREDS_TOKEN", spThumbprintBundle)

	env := envOf(t, srv.sharepointWorkerEnv())
	if got := env["ATLAS_SHAREPOINT_INTRANET_CREDENTIALS"]; got != spThumbprintBundle {
		t.Errorf("CREDENTIALS = %q, want the thumbprint bundle verbatim", got)
	}
	if got := env["ATLAS_SHAREPOINT_CONNECTORS"]; got != "intranet" {
		t.Errorf("CONNECTORS = %q, want the instance", got)
	}
}

// Without a store the worker could only refuse the bundle — and refusing at startup
// takes every other instance it serves down with it — so it is left out, and the rest
// still reach a worker that starts.
func TestSupervisedSharePointEnvLeavesOutAThumbprintBundleWithoutAStore(t *testing.T) {
	withCertStore(t, false)
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveSharePointConnector(t, srv, "1", "instore", "", "sp-thumb")
	saveSharePointConnector(t, srv, "2", "works", "", "sp-creds")
	t.Setenv("ATLAS_CONNECTOR_SP_THUMB_TOKEN", spThumbprintBundle)
	t.Setenv("ATLAS_CONNECTOR_SP_CREDS_TOKEN", spBundle)

	env := envOf(t, srv.sharepointWorkerEnv())
	if _, handed := env["ATLAS_SHAREPOINT_INSTORE_CREDENTIALS"]; handed {
		t.Error("a thumbprint bundle was handed over on a host without a certificate store")
	}
	if got := env["ATLAS_SHAREPOINT_CONNECTORS"]; got != "works" {
		t.Errorf("CONNECTORS = %q, want only the instance that can be built", got)
	}
	if _, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindSharePoint); err != nil {
		t.Fatalf("the worker refused the environment: %v", err)
	}
}

// The engine builds its clients on its run loop, so it does not ask the store there:
// a thumbprint no store holds still builds a client on a host that has one — the store
// is asked on the first token request — and on a host without one the record is
// reported with the reason, so the Console shows why its tasks park.
func TestBuildSharePointClientsWithAThumbprint(t *testing.T) {
	t.Setenv("ATLAS_CONNECTOR_SP_THUMB_TOKEN", spThumbprintBundle)

	withCertStore(t, true)
	srv, _ := newValidateServer(t)
	_ = srv.connectors.Save(connector{ID: "1", Name: "instore", Kind: connectorKindSharePoint, CredentialsRef: "sp_thumb", Enabled: true, CreatedAt: 1})
	clients, problems, err := srv.buildSharePointClients()
	if err != nil {
		t.Fatalf("buildSharePointClients: %v", err)
	}
	if _, ok := clients["instore"]; !ok {
		t.Errorf("no client built for a thumbprint bundle on a host with a store (problem: %q)", problems["instore"])
	}

	withCertStore(t, false)
	clients, problems, err = srv.buildSharePointClients()
	if err != nil {
		t.Fatalf("buildSharePointClients: %v", err)
	}
	if _, ok := clients["instore"]; ok {
		t.Error("a client was built for a thumbprint bundle on a host without a certificate store")
	}
	if p := problems["instore"]; !strings.Contains(p, "not running on Windows") {
		t.Errorf("problem = %q, want the reason the certificate cannot be used here", p)
	}
}
