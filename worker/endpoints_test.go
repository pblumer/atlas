package worker_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/worker"
)

// TestAWorkerReportsWhereItsWorkersConnect: the endpoints a worker holds are reduced
// to scheme, host and port before they leave it, reported beside the names on every
// poll, and shown in the Workers view (ADR-0471).
func TestAWorkerReportsWhereItsWorkersConnect(t *testing.T) {
	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_MAIL_CONNECTORS":              "internal-relay",
		"ATLAS_MAIL_INTERNAL_RELAY_ENDPOINT": "smtp://svc@relay.internal:25/ignored",
		"ATLAS_CLIO_CONNECTORS":              "events",
		"ATLAS_CLIO_EVENTS_ENDPOINT":         "https://svc:geheim@clio.example.com:8443/hook?x=1",
	}), "mail", "clio", "csv")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	want := map[string]string{
		"internal-relay": "smtp://relay.internal:25",
		"events":         "https://clio.example.com:8443",
	}
	if len(built.Endpoints) != len(want) {
		t.Fatalf("endpoints = %v, want %v", built.Endpoints, want)
	}
	for name, e := range want {
		if built.Endpoints[name] != e {
			t.Errorf("endpoint of %s = %q, want %q", name, built.Endpoints[name], e)
		}
	}

	// Every kind this worker serves is offloaded, so the engine leases each of its
	// job types to it rather than refusing one it drains itself.
	ts := liveAtlasWith(t, mailConnectorModel, `{"customer":"ada@example.com"}`,
		api.WithOffloadedConnectorKinds([]string{"mail", "clio", "csv"}))
	w := worker.New(worker.Options{
		Server: ts.URL, ID: "mailer-1", Handlers: built.Handlers,
		Connectors: built.Names, Endpoints: built.Endpoints,
		// Two of the three queues hold no work; a short wait answers them empty
		// instead of holding the poll open.
		Wait: 50 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = w.RunOnce(ctx)

	var view struct {
		Workers []struct {
			Worker    string            `json:"worker"`
			Endpoints map[string]string `json:"endpoints"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(get(t, ts, "/api/v1/workers"), &view); err != nil {
		t.Fatalf("decode workers: %v", err)
	}
	if len(view.Workers) != 1 || view.Workers[0].Endpoints["events"] != "https://clio.example.com:8443" ||
		view.Workers[0].Endpoints["internal-relay"] != "smtp://relay.internal:25" {
		t.Fatalf("workers = %+v, want mailer-1 with both reduced endpoints", view.Workers)
	}
}

// TestAKindWithoutAnEndpointReportsNone: a worker whose kinds configure no endpoint of
// their own reports none, rather than an empty value a sketch would draw.
func TestAKindWithoutAnEndpointReportsNone(t *testing.T) {
	built, err := worker.BuiltinConnectors(fakeEnv(map[string]string{
		"ATLAS_CLIO_CONNECTORS":      "events",
		"ATLAS_CLIO_EVENTS_ENDPOINT": "this is not an address",
	}), "clio", "csv", "webscrape")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if len(built.Endpoints) != 0 {
		t.Errorf("endpoints = %v, want none: the one configured value is not an address", built.Endpoints)
	}
}
