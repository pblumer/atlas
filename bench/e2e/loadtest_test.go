// Package e2e is level 2 of the performance suite: it drives Atlas through its
// real HTTP API against an in-process server, so the numbers include everything
// a client actually pays — request handling, JSON/XML (de)serialization, and the
// single processor mutex — on top of the engine work measured in level 1.
//
// Each op is one create-instance round trip. The handler creates the instance
// and drives the engine to idle before responding (durable before visible,
// invariant I2), so the measurement is honest client-visible latency including
// the fsync, not a fire-and-forget enqueue. Auto-completing scenarios run to
// their end event inside that call; the service-task scenario (linear) starts
// and parks on its job, since job completion is not an HTTP operation today.
//
// This is noisier than level 1 (loopback HTTP, JSON) and intentionally so — it
// is the client's view. Compare like-for-like across runs with benchstat; see
// bench/README.md.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/bench/scenarios"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// server wires a real wal+state+engine behind the HTTP API over a temp dir and
// returns the running test server. Cleanup is registered on tb.
func server(tb testing.TB) *httptest.Server {
	tb.Helper()
	dir := tb.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		tb.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		tb.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		tb.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir)
	if err != nil {
		tb.Fatalf("api.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	tb.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	return ts
}

// deploy posts a scenario's BPMN and returns its process definition key.
func deploy(tb testing.TB, ts *httptest.Server, bpmn string) uint64 {
	tb.Helper()
	resp, err := http.Post(ts.URL+"/api/v1/deployments", "application/xml", strings.NewReader(bpmn))
	if err != nil {
		tb.Fatalf("deploy POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tb.Fatalf("deploy status = %d", resp.StatusCode)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dep); err != nil {
		tb.Fatalf("decode deploy: %v", err)
	}
	return dep.Key
}

// startBody renders a scenario's start variables as the create-instance request
// body, e.g. {"variables":{"amount":150}}. An empty set yields {} so the request
// shape is identical whether or not the scenario routes on data.
func startBody(vars []model.VariableValue) string {
	if len(vars) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(vars))
	for _, v := range vars {
		parts = append(parts, strconv.Quote(v.Name)+":"+varJSON(v))
	}
	return `{"variables":{` + strings.Join(parts, ",") + `}}`
}

// varJSON renders one variable value as JSON by kind. The catalogue only uses
// number/string/bool start variables.
func varJSON(v model.VariableValue) string {
	switch v.Kind {
	case model.VarNumber:
		return v.Text // canonical number string, already valid JSON
	case model.VarBool:
		return strconv.FormatBool(v.Bool)
	default:
		return strconv.Quote(v.Text)
	}
}

// createInstance posts one create-instance request and returns the HTTP status.
func createInstance(ts *httptest.Server, key uint64, body string) (int, error) {
	resp, err := http.Post(
		fmt.Sprintf("%s/api/v1/processes/%d/instances", ts.URL, key),
		"application/json", strings.NewReader(body))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// BenchmarkE2ECreateInstance measures the create-instance round trip per
// scenario over the real HTTP API. ns/op is client-visible latency; invert for
// requests/sec. Storage medium is reported by scripts/bench.sh — as at level 1,
// the number is meaningless without it.
func BenchmarkE2ECreateInstance(b *testing.B) {
	for _, s := range scenarios.All() {
		b.Run(s.Name, func(b *testing.B) {
			ts := server(b)
			key := deploy(b, ts, s.BPMN)
			body := startBody(s.StartVars)

			b.ResetTimer()
			for range b.N {
				code, err := createInstance(ts, key, body)
				if err != nil {
					b.Fatalf("create instance: %v", err)
				}
				if code != http.StatusOK {
					b.Fatalf("create instance status = %d", code)
				}
			}
			b.StopTimer()
		})
	}
}

// TestE2EScenariosDeploy is the level-2 behavioral check and covers the harness
// helpers: every scenario deploys and accepts an instance over HTTP. Auto-
// completing scenarios are additionally asserted to reach a completed state.
func TestE2EScenariosDeploy(t *testing.T) {
	for _, s := range scenarios.All() {
		t.Run(s.Name, func(t *testing.T) {
			ts := server(t)
			key := deploy(t, ts, s.BPMN)

			code, err := createInstance(ts, key, startBody(s.StartVars))
			if err != nil {
				t.Fatalf("create instance: %v", err)
			}
			if code != http.StatusOK {
				t.Fatalf("create instance status = %d", code)
			}

			if s.Name == "linear" {
				return // parks on its "work" job; no HTTP completion path
			}
			resp, err := http.Get(ts.URL + "/api/v1/instances")
			if err != nil {
				t.Fatalf("list instances: %v", err)
			}
			defer resp.Body.Close()
			var list []struct {
				State string `json:"state"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
				t.Fatalf("decode instances: %v", err)
			}
			var completed bool
			for _, it := range list {
				if it.State == "completed" {
					completed = true
				}
			}
			if !completed {
				t.Errorf("%s: no completed instance after create", s.Name)
			}
		})
	}
}
