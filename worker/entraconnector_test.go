package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/entra"
)

// An Entra worker is configured entirely from its environment. The app credential
// here can create and disable accounts across a directory, so it must never be
// reachable through argv (ADR-0172).
func TestBuiltinConnectorsRegistersEntra(t *testing.T) {
	got, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_ENTRA_CONNECTORS":            "contoso",
		"ATLAS_ENTRA_CONTOSO_TENANT_ID":     "11111111-2222-3333-4444-555555555555",
		"ATLAS_ENTRA_CONTOSO_CLIENT_ID":     "app-1",
		"ATLAS_ENTRA_CONTOSO_CLIENT_SECRET": "s3cr3t",
	}), "entra")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := got.Handlers[compiler.EntraJobType]; !ok {
		t.Errorf("no handler for %s; have %v", compiler.EntraJobType, got.Handlers)
	}
	if len(got.Names) != 1 || got.Names[0] != "contoso" {
		t.Errorf("names = %v, want [contoso]", got.Names)
	}
}

// With no in-process fallback, a broken configuration must be refused while the
// operator is still watching — and the message must quote the exact variable.
func TestBuiltinConnectorsRefusesAMisconfiguredEntraWorker(t *testing.T) {
	if _, err := BuiltinConnectors(envMap(nil), "entra"); err == nil {
		t.Error("no ATLAS_ENTRA_CONNECTORS must fail at startup")
	}
	for _, missing := range []string{"TENANT_ID", "CLIENT_ID", "CLIENT_SECRET"} {
		env := map[string]string{
			"ATLAS_ENTRA_CONNECTORS":            "contoso",
			"ATLAS_ENTRA_CONTOSO_TENANT_ID":     "t",
			"ATLAS_ENTRA_CONTOSO_CLIENT_ID":     "c",
			"ATLAS_ENTRA_CONTOSO_CLIENT_SECRET": "s",
		}
		delete(env, "ATLAS_ENTRA_CONTOSO_"+missing)
		_, err := BuiltinConnectors(envMap(env), "entra")
		if err == nil {
			t.Errorf("a connector missing %s must fail at startup", missing)
			continue
		}
		if !strings.Contains(err.Error(), "ATLAS_ENTRA_CONTOSO_"+missing) {
			t.Errorf("missing %s: error should quote the variable, got: %v", missing, err)
		}
	}
}

// A job that arrives without a resolved detail is a server that does not resolve
// Entra tasks — reported as that, rather than as an empty operation.
func TestRunEntraJobWithoutADetail(t *testing.T) {
	if _, err := RunEntraJob(context.Background(), Job{}, entra.NewRegistry()); err == nil {
		t.Fatal("a job with no connector detail must fail")
	}
}

// The resolved detail round-trips through the job payload into an entra.Job.
func TestRunEntraJobReadsTheResolvedDetail(t *testing.T) {
	_, err := RunEntraJob(context.Background(), Job{Connector: &ConnectorPayload{
		Kind: "entra",
		Fields: map[string]any{
			"connector": "contoso", "operation": "disable", "userId": "arno@contoso.com",
		},
	}}, entra.NewRegistry())
	// The registry is empty, so this reaches the connector lookup — evidence the
	// payload decoded, since an undecodable one fails earlier and differently.
	if err == nil || !strings.Contains(err.Error(), "contoso") {
		t.Errorf("err = %v, want an unresolved-connector error naming contoso", err)
	}
}

// The listing's bounds must survive the engine→worker hop. They are the only fields
// whose loss is silent and unsafe: a dropped maxUsers turns a capped listing into an
// unbounded one, and a job that decoded "successfully" would then read a whole
// directory into a process variable.
//
// So each is asserted through what it does rather than through the decoded struct —
// the query the connector built, and the cap actually refusing an oversized listing.
func TestRunEntraJobCarriesTheListingBounds(t *testing.T) {
	spy := &listingSpy{users: 51}
	reg := entra.NewRegistry()
	reg.Register("contoso", spy)

	_, err := RunEntraJob(context.Background(), Job{Connector: &ConnectorPayload{
		Kind: "entra",
		Fields: map[string]any{
			"connector": "contoso", "operation": "list-users",
			"filter": "department eq 'IT'", "select": "id,displayName",
			"pageSize": int32(200), "maxUsers": int32(50),
			"resultVariable": "leute",
		},
	}}, reg)

	// maxUsers arrived: 51 users against a cap of 50 is refused, by that number.
	if err == nil || !strings.Contains(err.Error(), "50-user") {
		t.Fatalf("err = %v, want the 50-user cap to have refused the listing", err)
	}
	// filter, select and pageSize arrived: each is in the query the connector built.
	for _, want := range []string{"$filter=department", "$select=id", "$top=200"} {
		if !strings.Contains(spy.path, want) {
			t.Errorf("path = %q, want it to carry %q", spy.path, want)
		}
	}
}

// listingSpy answers one page of a given size and records the path it was asked for,
// which is where the decoded query becomes observable.
type listingSpy struct {
	users int
	path  string
}

func (listingSpy) BaseURL() string { return "https://graph.microsoft.com/v1.0" }

func (s *listingSpy) Call(_ context.Context, r entra.Request) (any, error) {
	s.path = r.Path
	vals := make([]any, 0, s.users)
	for i := 0; i < s.users; i++ {
		vals = append(vals, map[string]any{"id": "u"})
	}
	return map[string]any{"value": vals}, nil
}
