package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

func teamsPhoneEnv(extra map[string]string) map[string]string {
	env := map[string]string{
		"ATLAS_TEAMSPHONE_CONNECTORS":        "contoso",
		"ATLAS_TEAMSPHONE_CONTOSO_TENANT_ID": "11111111-2222-3333-4444-555555555555",
		"ATLAS_TEAMSPHONE_CONTOSO_CLIENT_ID": "app-1",
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// A tenant signs in with a certificate it was handed as PEM or one the worker host
// holds by thumbprint; either is served, and the tenant's name is reported to the
// engine like every held worker name.
func TestBuiltinConnectorsServesATeamsPhoneTenant(t *testing.T) {
	for name, cred := range map[string]map[string]string{
		"pem":        {"ATLAS_TEAMSPHONE_CONTOSO_CERTIFICATE": entraTestCertificate(t)},
		"thumbprint": {"ATLAS_TEAMSPHONE_CONTOSO_CERTIFICATE_THUMBPRINT": "01 23 45 67 89 ab cd ef 01 23 45 67 89 ab cd ef 01 23 45 67"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := BuiltinConnectors(envMap(teamsPhoneEnv(cred)), "teamsphone")
			if err != nil {
				t.Fatalf("BuiltinConnectors: %v", err)
			}
			if _, ok := got.Handlers[compiler.TeamsPhoneJobType]; !ok {
				t.Errorf("no handler for %s", compiler.TeamsPhoneJobType)
			}
			if len(got.Names) != 1 || got.Names[0] != "contoso" {
				t.Errorf("names = %v, want [contoso]", got.Names)
			}
		})
	}
}

// No tenant is a kind this worker does not serve yet, not an error: the kind is
// supervised by default and must park until a tenant is configured.
func TestATeamsPhoneWorkerWithoutTenantsParks(t *testing.T) {
	got, err := BuiltinConnectors(envMap(map[string]string{}), "teamsphone")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if len(got.Unconfigured) != 1 || got.Unconfigured[0] != "teamsphone" || len(got.Handlers) != 0 {
		t.Errorf("got %+v, want teamsphone reported unconfigured", got)
	}
}

// Everything that can be checked without a tenant is refused at startup, naming the
// variable, and never quoting a PEM.
func TestATeamsPhoneTenantIsCheckedAtStartup(t *testing.T) {
	cert := entraTestCertificate(t)
	certOnly := cert[:strings.Index(cert, "-----BEGIN PRIVATE KEY-----")]
	cases := map[string]struct {
		extra map[string]string
		want  string
	}{
		"no certificate":  {nil, "missing its certificate"},
		"both":            {map[string]string{"ATLAS_TEAMSPHONE_CONTOSO_CERTIFICATE": cert, "ATLAS_TEAMSPHONE_CONTOSO_CERTIFICATE_THUMBPRINT": strings.Repeat("A", 40)}, "set exactly one"},
		"no key":          {map[string]string{"ATLAS_TEAMSPHONE_CONTOSO_CERTIFICATE": certOnly}, `"contoso"`},
		"short thumb":     {map[string]string{"ATLAS_TEAMSPHONE_CONTOSO_CERTIFICATE_THUMBPRINT": "ABCDEF"}, "40 hexadecimal"},
		"no tenant":       {map[string]string{"ATLAS_TEAMSPHONE_CONTOSO_TENANT_ID": "", "ATLAS_TEAMSPHONE_CONTOSO_CERTIFICATE_THUMBPRINT": strings.Repeat("A", 40)}, "ATLAS_TEAMSPHONE_CONTOSO_TENANT_ID"},
		"bad timeout":     {map[string]string{"ATLAS_TEAMSPHONE_TIMEOUT": "soon", "ATLAS_TEAMSPHONE_CONTOSO_CERTIFICATE_THUMBPRINT": strings.Repeat("A", 40)}, "ATLAS_TEAMSPHONE_TIMEOUT"},
		"undecided mock":  {map[string]string{"ATLAS_TEAMSPHONE_MOCK": "maybe"}, "ATLAS_TEAMSPHONE_MOCK"},
		"negative budget": {map[string]string{"ATLAS_TEAMSPHONE_TIMEOUT": "-1m", "ATLAS_TEAMSPHONE_CONTOSO_CERTIFICATE_THUMBPRINT": strings.Repeat("A", 40)}, "positive duration"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := BuiltinConnectors(envMap(teamsPhoneEnv(c.extra)), "teamsphone")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error containing %q, got %v", c.want, err)
			}
			if strings.Contains(err.Error(), "BEGIN") {
				t.Errorf("error %q quotes the PEM", err)
			}
		})
	}
}

// In mock mode a tenant needs no credential, and a resolved job runs end to end through
// the handler the worker registers — the same path a leased job takes.
func TestATeamsPhoneWorkerInMockModeServesAJob(t *testing.T) {
	got, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_TEAMSPHONE_CONNECTORS": "contoso",
		"ATLAS_TEAMSPHONE_MOCK":       "true",
		"ATLAS_TEAMSPHONE_MOCK_USERS": "anna@contoso.example",
	}), "teamsphone")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	h := got.Handlers[compiler.TeamsPhoneJobType]
	job := Job{Connector: &ConnectorPayload{Kind: "teamsphone", Fields: map[string]any{
		"connector": "contoso", "operation": "resolve-users",
		"args":           map[string]any{"users": []any{"anna@contoso.example", "ghost@contoso.example"}},
		"resultVariable": "agents",
	}}}
	out, err := h.(ExecFunc)(context.Background(), job)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	res := out["agents"].(map[string]any)
	if len(res["ids"].([]string)) != 1 || res["notFound"].([]string)[0] != "ghost@contoso.example" {
		t.Errorf("result = %v", res)
	}
}

// A job from a server that resolves nothing says so instead of running an empty request.
func TestATeamsPhoneJobWithoutDetailIsRefused(t *testing.T) {
	if _, err := RunTeamsPhoneJob(context.Background(), Job{}, nil); err == nil || !strings.Contains(err.Error(), "no resolved worker detail") {
		t.Errorf("want the missing detail named, got %v", err)
	}
}
