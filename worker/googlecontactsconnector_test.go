package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// A Google Contacts worker is configured entirely from its environment, one address
// book per name.
func TestBuiltinConnectorsRegistersGoogleContacts(t *testing.T) {
	got, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_GOOGLECONTACTS_CONNECTORS":          "verkauf,privat",
		"ATLAS_GOOGLECONTACTS_VERKAUF_CREDENTIALS": `{"method":"refreshToken","clientId":"id","clientSecret":"s","refreshToken":"r"}`,
		"ATLAS_GOOGLECONTACTS_PRIVAT_CREDENTIALS":  `{"method":"refreshToken","clientId":"id2","clientSecret":"s2","refreshToken":"r2"}`,
		"ATLAS_GOOGLECONTACTS_PRIVAT_URL":          "https://proxy.example/people",
	}), "googlecontacts")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := got.Handlers[compiler.GoogleContactsJobType]; !ok {
		t.Errorf("no handler for %s; have %v", compiler.GoogleContactsJobType, got.Handlers)
	}
	if strings.Join(got.Names, ",") != "privat,verkauf" {
		t.Errorf("names = %v", got.Names)
	}
	// The address book behind a proxy reports where it connects, reduced to scheme and
	// host; the one reaching Google directly names nothing and reports nothing.
	if got.Endpoints["privat"] != "https://proxy.example" {
		t.Errorf("endpoint of privat = %q", got.Endpoints["privat"])
	}
	if _, ok := got.Endpoints["verkauf"]; ok {
		t.Errorf("verkauf reports an endpoint it never named: %v", got.Endpoints)
	}
}

// With no address book the worker parks rather than failing: the kind is supervised by
// default, and failing here would be a restart loop on every server that has not
// configured one.
func TestAGoogleContactsWorkerWithNoAddressBookParks(t *testing.T) {
	built, err := BuiltinConnectors(envMap(nil), "googlecontacts")
	if err != nil {
		t.Fatalf("an unconfigured googlecontacts worker must not fail at startup: %v", err)
	}
	if len(built.Handlers) != 0 || len(built.Unconfigured) != 1 || built.Unconfigured[0] != "googlecontacts" {
		t.Errorf("handlers %v unconfigured %v", built.Handlers, built.Unconfigured)
	}
}

// A *named* address book whose bundle does not build is refused at startup, quoting the
// variable to fix and not the secret it holds.
func TestBuiltinConnectorsRefusesAMisconfiguredGoogleContactsWorker(t *testing.T) {
	_, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_GOOGLECONTACTS_CONNECTORS":          "verkauf",
		"ATLAS_GOOGLECONTACTS_VERKAUF_CREDENTIALS": `{"method":"refreshToken","clientSecret":"TOPSECRET"}`,
	}), "googlecontacts")
	if err == nil {
		t.Fatal("a bundle without clientId was accepted")
	}
	if !strings.Contains(err.Error(), "ATLAS_GOOGLECONTACTS_VERKAUF_CREDENTIALS") || !strings.Contains(err.Error(), "clientId") {
		t.Errorf("err = %v", err)
	}
	if strings.Contains(err.Error(), "TOPSECRET") {
		t.Errorf("the error quotes the secret: %v", err)
	}
}

// TestRunGoogleContactsJobRunsTheResolvedTask: the payload the server sends is all the
// worker needs — the address book's credential comes from the worker's own environment.
func TestRunGoogleContactsJobRunsTheResolvedTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
		case "/v1/people/c42":
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"resourceName":"people/c42","names":[{"givenName":"Anna","familyName":"Muster"}],"emailAddresses":[{"value":"anna@firma.ch"}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	reg, _, err := googleContactsRegistryFromEnv(envMap(map[string]string{
		"ATLAS_GOOGLECONTACTS_CONNECTORS":          "verkauf",
		"ATLAS_GOOGLECONTACTS_VERKAUF_URL":         srv.URL,
		"ATLAS_GOOGLECONTACTS_VERKAUF_CREDENTIALS": `{"method":"refreshToken","clientId":"id","clientSecret":"s","refreshToken":"r","tokenUrl":"` + srv.URL + `/token"}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	out, err := RunGoogleContactsJob(context.Background(), Job{Connector: &ConnectorPayload{Kind: "googlecontacts", Fields: map[string]any{
		"connector": "verkauf", "operation": "get-contact", "contactId": "people/c42", "resultVariable": "kunde",
	}}}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if k, _ := out["kunde"].(map[string]any); k["email"] != "anna@firma.ch" || k["familyName"] != "Muster" {
		t.Errorf("out = %v", out)
	}
}

func TestRunGoogleContactsJobNeedsAResolvedDetail(t *testing.T) {
	if _, err := RunGoogleContactsJob(context.Background(), Job{}, nil); err == nil || !strings.HasPrefix(err.Error(), "googlecontacts:") {
		t.Errorf("err = %v", err)
	}
	_, err := RunGoogleContactsJob(context.Background(), Job{Connector: &ConnectorPayload{Fields: map[string]any{"contact": "kein objekt"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot read the resolved detail") {
		t.Errorf("err = %v", err)
	}
}
