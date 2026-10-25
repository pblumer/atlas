package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// A CardDAV worker is configured entirely from its environment, one address book per
// name.
func TestBuiltinConnectorsRegistersCardDAV(t *testing.T) {
	got, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_CARDDAV_CONNECTORS":          "verkauf,privat",
		"ATLAS_CARDDAV_VERKAUF_URL":         "https://cloud.example.ch/remote.php/dav",
		"ATLAS_CARDDAV_VERKAUF_CREDENTIALS": `{"username":"verkauf","password":"p"}`,
		"ATLAS_CARDDAV_PRIVAT_URL":          "https://contacts.icloud.com",
		"ATLAS_CARDDAV_PRIVAT_CREDENTIALS":  `{"username":"anna@icloud.com","password":"p","groups":"group-vcards"}`,
	}), "carddav")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := got.Handlers[compiler.CardDAVJobType]; !ok {
		t.Errorf("no handler for %s; have %v", compiler.CardDAVJobType, got.Handlers)
	}
	if strings.Join(got.Names, ",") != "privat,verkauf" {
		t.Errorf("names = %v", got.Names)
	}
}

// With no address book the worker parks rather than failing: the kind is supervised by
// default.
func TestACardDAVWorkerWithNoAddressBookParks(t *testing.T) {
	built, err := BuiltinConnectors(envMap(nil), "carddav")
	if err != nil {
		t.Fatalf("an unconfigured carddav worker must not fail at startup: %v", err)
	}
	if len(built.Handlers) != 0 || len(built.Unconfigured) != 1 || built.Unconfigured[0] != "carddav" {
		t.Errorf("handlers %v unconfigured %v", built.Handlers, built.Unconfigured)
	}
}

// A *named* address book that does not build is refused at startup, quoting the
// variables to fix and not the secret they hold.
func TestBuiltinConnectorsRefusesAMisconfiguredCardDAVWorker(t *testing.T) {
	for _, env := range []map[string]string{
		{"ATLAS_CARDDAV_CONNECTORS": "verkauf", "ATLAS_CARDDAV_VERKAUF_CREDENTIALS": `{"username":"u","password":"TOPSECRET"}`},
		{"ATLAS_CARDDAV_CONNECTORS": "verkauf", "ATLAS_CARDDAV_VERKAUF_URL": "https://dav.example.ch", "ATLAS_CARDDAV_VERKAUF_CREDENTIALS": `{"username":"u","pasword":"TOPSECRET"}`},
	} {
		_, err := BuiltinConnectors(envMap(env), "carddav")
		if err == nil {
			t.Fatalf("%v was accepted", env)
		}
		if !strings.Contains(err.Error(), "ATLAS_CARDDAV_VERKAUF_URL") || !strings.Contains(err.Error(), "ATLAS_CARDDAV_VERKAUF_CREDENTIALS") {
			t.Errorf("err = %v", err)
		}
		if strings.Contains(err.Error(), "TOPSECRET") {
			t.Errorf("the error quotes the secret: %v", err)
		}
	}
}

// TestRunCardDAVJobRunsTheResolvedTask: the payload the server sends is all the worker
// needs — the address book's server and password come from the worker's own environment.
func TestRunCardDAVJobRunsTheResolvedTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "verkauf" || p != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == "PROPFIND" && r.URL.Path == "/book/":
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte(`<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:carddav"><d:response><d:href>/book/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/><c:addressbook/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`))
		case r.Method == http.MethodGet && r.URL.Path == "/book/anna.vcf":
			w.Header().Set("ETag", `"1"`)
			_, _ = w.Write([]byte("BEGIN:VCARD\r\nVERSION:3.0\r\nUID:a\r\nFN:Anna Muster\r\nN:Muster;Anna;;;\r\nEMAIL:anna@firma.ch\r\nEND:VCARD\r\n"))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	reg, _, err := cardDAVRegistryFromEnv(envMap(map[string]string{
		"ATLAS_CARDDAV_CONNECTORS":          "verkauf",
		"ATLAS_CARDDAV_VERKAUF_URL":         srv.URL + "/book/",
		"ATLAS_CARDDAV_VERKAUF_CREDENTIALS": `{"username":"verkauf","password":"pw"}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	out, err := RunCardDAVJob(context.Background(), Job{Connector: &ConnectorPayload{Kind: "carddav", Fields: map[string]any{
		"connector": "verkauf", "operation": "get-contact", "contactId": "anna.vcf", "resultVariable": "kunde",
	}}}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if k, _ := out["kunde"].(map[string]any); k["email"] != "anna@firma.ch" || k["familyName"] != "Muster" {
		t.Errorf("out = %v", out)
	}
}

func TestRunCardDAVJobNeedsAResolvedDetail(t *testing.T) {
	if _, err := RunCardDAVJob(context.Background(), Job{}, nil); err == nil || !strings.HasPrefix(err.Error(), "carddav:") {
		t.Errorf("err = %v", err)
	}
	_, err := RunCardDAVJob(context.Background(), Job{Connector: &ConnectorPayload{Fields: map[string]any{"contact": "kein objekt"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot read the resolved detail") {
		t.Errorf("err = %v", err)
	}
}
