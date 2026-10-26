package worker

import (
	"context"
	"errors"
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

// The codes the process catches arrive with the job, and a refusal under one of them
// leaves the handler as the BPMN error the worker throws — not as a failure it retries.
func TestATeamsPhoneRefusalTheProcessCatchesIsThrown(t *testing.T) {
	got, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_TEAMSPHONE_CONNECTORS": "contoso",
		"ATLAS_TEAMSPHONE_MOCK":       "true",
		"ATLAS_TEAMSPHONE_MOCK_USERS": "anna@contoso.example",
	}), "teamsphone")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	h := got.Handlers[compiler.TeamsPhoneJobType].(ExecFunc)
	job := func(codes []any) Job {
		return Job{Connector: &ConnectorPayload{Kind: "teamsphone", Fields: map[string]any{
			"connector": "contoso", "operation": "ensure-resource-account", "resultVariable": "konto",
			"args":       map[string]any{"userPrincipalName": "anna@contoso.example", "displayName": "Anna", "accountType": "call-queue"},
			"errorCodes": codes,
		}}}
	}
	_, err = h(context.Background(), job([]any{"TEAMSPHONE_UPN_TAKEN"}))
	var thrown *BPMNError
	if !errors.As(err, &thrown) || thrown.Code != "TEAMSPHONE_UPN_TAKEN" {
		t.Errorf("a refusal the process catches: %v; want the BPMN error TEAMSPHONE_UPN_TAKEN", err)
	}
	if _, err = h(context.Background(), job(nil)); err == nil || errors.As(err, &thrown) {
		t.Errorf("a refusal the process does not catch: %v; want a failure the worker retries", err)
	}
}

// In mock mode the tenant's numbers are the ones ATLAS_TEAMSPHONE_MOCK_NUMBERS names, so a
// model tries the refusal of a number the tenant lacks before any tenant exists.
func TestATeamsPhoneMockTenantHasTheNumbersItIsGiven(t *testing.T) {
	got, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_TEAMSPHONE_CONNECTORS":   "contoso",
		"ATLAS_TEAMSPHONE_MOCK":         "true",
		"ATLAS_TEAMSPHONE_MOCK_NUMBERS": "+41 44 111 00 00",
	}), "teamsphone")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	h := got.Handlers[compiler.TeamsPhoneJobType].(ExecFunc)
	assign := func(number string) error {
		_, err := h(context.Background(), Job{Connector: &ConnectorPayload{Kind: "teamsphone", Fields: map[string]any{
			"connector": "contoso", "operation": "ensure-phone-number", "resultVariable": "nummer",
			"args": map[string]any{"identity": "anna@contoso.example", "phoneNumber": number, "numberType": "calling-plan"},
		}}})
		return err
	}
	if err := assign("+41441110000"); err != nil {
		t.Errorf("a number the mock tenant has: %v", err)
	}
	if err := assign("+41449999999"); err == nil || !strings.Contains(err.Error(), "acquire or port it first") {
		t.Errorf("a number the mock tenant lacks: %v; want it refused", err)
	}
}

// A job from a server that resolves nothing says so instead of running an empty request.
func TestATeamsPhoneJobWithoutDetailIsRefused(t *testing.T) {
	if _, err := RunTeamsPhoneJob(context.Background(), Job{}, nil); err == nil || !strings.Contains(err.Error(), "no resolved worker detail") {
		t.Errorf("want the missing detail named, got %v", err)
	}
}
