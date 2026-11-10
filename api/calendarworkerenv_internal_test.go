package api

import (
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/worker"
)

// The calendar is worker-only, and a supervised worker can serve it because the engine
// hands its accounts over: a task names its Worker and a calendar, and which provider
// answers and as whom is a Worker record and a vault secret.

const calendarMockBundle = `{"provider":"mock","calendars":{"werkstatt@firma.ch":{}}}`

func saveCalendarWorker(t *testing.T, srv *Server, id, name, endpoint, ref string) {
	t.Helper()
	if err := srv.connectors.Save(connector{
		ID: id, Name: name, Kind: connectorKindCalendar,
		Endpoint: endpoint, CredentialsRef: ref, Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save %s: %v", name, err)
	}
}

// What the engine renders is what a worker builds its backends from.
func TestSupervisedCalendarEnvHandsTheWorkerItsAccounts(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveCalendarWorker(t, srv, "1", "filiale", "", "kal-filiale")
	saveCalendarWorker(t, srv, "2", "zentrale", "https://graph.microsoft.com/v1.0", "kal-zentrale")
	t.Setenv("ATLAS_CONNECTOR_KAL_FILIALE_TOKEN", calendarMockBundle)
	t.Setenv("ATLAS_CONNECTOR_KAL_ZENTRALE_TOKEN", `{"provider":"microsoft-graph","tenantId":"t","clientId":"c","clientSecret":"s"}`)

	env := envOf(t, srv.calendarWorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindCalendar)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "filiale") || !slices.Contains(built.Names, "zentrale") {
		t.Errorf("the worker holds %v, want both accounts", built.Names)
	}
	if got := env["ATLAS_CALENDAR_FILIALE_CREDENTIALS"]; got != calendarMockBundle {
		t.Errorf("the handed bundle = %q, want it whole", got)
	}
}

// An account whose bundle does not build is left out rather than handed over, and two
// names that fold to one environment name keep only the first; accounts set on the host
// stay in the list.
func TestSupervisedCalendarEnvSkipsWhatCannotBeServed(t *testing.T) {
	for _, tc := range []struct{ name, endpoint, secret string }{
		{"never set", "", ""},
		{"malformed", "", "{"},
		{"no provider", "", `{}`},
		{"mock with an endpoint", "https://example.ch", calendarMockBundle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
			saveCalendarWorker(t, srv, "1", "filiale", tc.endpoint, "kal")
			if tc.secret != "" {
				t.Setenv("ATLAS_CONNECTOR_KAL_TOKEN", tc.secret)
			}
			if env := srv.calendarWorkerEnv(); env != nil {
				t.Errorf("rendered %v for an account that cannot be used; want nothing", env)
			}
		})
	}
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveCalendarWorker(t, srv, "1", "filiale-bern", "", "kal")
	saveCalendarWorker(t, srv, "2", "filiale_bern", "", "kal")
	t.Setenv("ATLAS_CONNECTOR_KAL_TOKEN", calendarMockBundle)
	t.Setenv("ATLAS_CALENDAR_CONNECTORS", "host-kalender")
	env := envOf(t, srv.calendarWorkerEnv())
	if got := env["ATLAS_CALENDAR_CONNECTORS"]; got != "host-kalender,filiale-bern" {
		t.Errorf("ATLAS_CALENDAR_CONNECTORS = %q", got)
	}
}

func TestCalendarIsSupervisedByDefault(t *testing.T) {
	if !slices.Contains(DefaultSupervisedWorkerOnlyKinds(), connectorKindCalendar) {
		t.Error("calendar is not supervised by default")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()[connectorKindCalendar]; !provisioned {
		t.Error("calendar is supervised but its bundle is not handed over")
	}
}

func TestValidateCalendarWorker(t *testing.T) {
	p := createConnectorParams{}
	if msg := validateCalendarConnector(&p); !strings.Contains(msg, "credentialsRef") {
		t.Errorf("validate = %q", msg)
	}
	p = createConnectorParams{CredentialsRef: "kal", Provider: "x", Sender: "y", Model: "z"}
	if msg := validateCalendarConnector(&p); msg != "" {
		t.Errorf("validate = %q", msg)
	}
	if p.Provider != "" || p.Sender != "" || p.Model != "" {
		t.Errorf("fields that do not apply were kept: %+v", p)
	}
}
