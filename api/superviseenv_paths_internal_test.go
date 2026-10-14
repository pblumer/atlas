package api

import (
	"strings"
	"testing"
)

// The handover rules the clio, temis, SharePoint and agent renderers share
// (ADR-0157), held for each of them: what an operator configured on the host stays
// in the list, two names that fold to one variable do not share a credential, a
// name that folds to nothing and a worker that is off or of another kind are left
// out, one with nothing to reach is not handed over half-filled — and a store that
// cannot be read hands over nothing rather than half a list.

// superviseEnvPathsKind is one renderer and the variable its endpoint lands in.
type superviseEnvPathsKind struct {
	name, kind, prefix, reach string
	// want is what the reach variable holds for the surviving worker.
	want   string
	render func(*Server) []string
}

func superviseEnvPathsKinds() []superviseEnvPathsKind {
	return []superviseEnvPathsKind{
		{"clio", connectorKindClio, "ATLAS_CLIO_", "ENDPOINT", "https://a.example", (*Server).clioWorkerEnv},
		{"temis", connectorKindTemis, "ATLAS_TEMIS_", "URL", "https://a.example", (*Server).temisWorkerEnv},
		{"sharepoint", connectorKindSharePoint, "ATLAS_SHAREPOINT_", "CREDENTIALS", "bundle-a", (*Server).sharepointWorkerEnv},
		{"agent", connectorKindAgent, "ATLAS_AGENT_", "ENDPOINT", "https://a.example", (*Server).agentWorkerEnv},
	}
}

// TestSuperviseEnvHandsOverOnlyWhatAWorkerCanUse.
func TestSuperviseEnvHandsOverOnlyWhatAWorkerCanUse(t *testing.T) {
	for _, k := range superviseEnvPathsKinds() {
		t.Run(k.name, func(t *testing.T) {
			srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
			t.Setenv(k.prefix+"CONNECTORS", "fromhost")
			t.Setenv("ATLAS_CONNECTOR_BUNDLE_A_TOKEN", "bundle-a")
			t.Setenv("ATLAS_CONNECTOR_BUNDLE_B_TOKEN", "bundle-b")
			for i, c := range []connector{
				// "Alpha" sorts before "alpha"; both fold to ALPHA, so the second is dropped.
				{Name: "Alpha", Kind: k.kind, Endpoint: "https://a.example", CredentialsRef: "bundle-a", Enabled: true},
				{Name: "alpha", Kind: k.kind, Endpoint: "https://b.example", CredentialsRef: "bundle-b", Enabled: true},
				{Name: "!!!", Kind: k.kind, Endpoint: "https://c.example", CredentialsRef: "bundle-a", Enabled: true},
				{Name: "off", Kind: k.kind, Endpoint: "https://d.example", CredentialsRef: "bundle-a", Enabled: false},
				{Name: "elsewhere", Kind: connectorKindMail, Endpoint: "mx:587", Enabled: true},
				// Nothing to reach: no endpoint, and no secret behind its reference.
				{Name: "empty", Kind: k.kind, CredentialsRef: "unset", Enabled: true},
			} {
				c.ID, c.CreatedAt = c.Name, int64(i+1)
				if err := srv.connectors.Save(c); err != nil {
					t.Fatalf("Save %s: %v", c.Name, err)
				}
			}

			env := envOf(t, k.render(srv))
			if got := env[k.prefix+"CONNECTORS"]; got != "fromhost,Alpha" {
				t.Errorf("CONNECTORS = %q, want the host's name kept and only Alpha added", got)
			}
			if got := env[k.prefix+"ALPHA_"+k.reach]; got != k.want {
				t.Errorf("%sALPHA_%s = %q, want Alpha's own %q and not alpha's", k.prefix, k.reach, got, k.want)
			}
			for name := range env {
				for _, gone := range []string{"_OFF_", "_ELSEWHERE_", "_EMPTY_", "_FROMHOST_"} {
					if strings.Contains(name, gone) {
						t.Errorf("rendered %s, which should not have been handed over", name)
					}
				}
			}
		})
	}
}

// TestSuperviseEnvAStoreThatCannotBeReadHandsOverNothing. Half a list would serve
// half the instances and leave the others' tasks reading as unconfigured.
func TestSuperviseEnvAStoreThatCannotBeReadHandsOverNothing(t *testing.T) {
	for _, k := range superviseEnvPathsKinds() {
		t.Run(k.name, func(t *testing.T) {
			srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
			t.Setenv(k.prefix+"CONNECTORS", "fromhost")
			recertifyHTTPPathsBreakDir(t, srv.connectors.Dir())
			if env := k.render(srv); len(env) != 0 {
				t.Errorf("env = %v, want nothing rendered", env)
			}
		})
	}
}
