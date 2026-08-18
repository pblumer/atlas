package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// probe is a trivial collector for asserting what a registry does and does not export.
func probe(name string) prometheus.Collector {
	return prometheus.NewGauge(prometheus.GaugeOpts{Namespace: Namespace, Name: name, Help: "test probe"})
}

// TestRegistryIsNotTheProcessDefault is the property the package doc claims: Atlas
// exports its own registry, so what an operator scrapes is what Atlas registered — not
// whatever every library in the process happened to add to the global default, and not
// something a test in another package can perturb (ADR-0142).
func TestRegistryIsNotTheProcessDefault(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(probe("registry_probe")); err != nil {
		t.Fatalf("Register: %v", err)
	}

	families, err := r.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(families) != 1 || families[0].GetName() != Namespace+"_registry_probe" {
		t.Fatalf("own registry gathered %v, want just the probe", names(families))
	}
	// The same metric must not have landed in the process-wide default.
	global, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather default: %v", err)
	}
	for _, f := range global {
		if f.GetName() == Namespace+"_registry_probe" {
			t.Fatal("registering on the Atlas registry also published to the process default")
		}
	}
}

// TestFreshRegistryExportsNothing: Go-runtime and process collectors are opt-in. What
// appears at /metrics should be a decision, not a side effect of construction.
func TestFreshRegistryExportsNothing(t *testing.T) {
	families, err := NewRegistry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(families) != 0 {
		t.Fatalf("a fresh registry already exports %v, want nothing", names(families))
	}
}

// TestDuplicateRegistrationFailsAtConstruction: registering the same metric twice is a
// programming error, and it surfaces where the caller can act on it — at boot — rather
// than as a broken scrape hours later.
func TestDuplicateRegistrationFailsAtConstruction(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(probe("dupe")); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register(probe("dupe")); err == nil {
		t.Fatal("registering the same metric twice was accepted")
	}
}

// TestBuildInfoIsASingleSeries: build info is the one place a label carries a value, so
// it has to stay bounded — one series, whatever the labels say.
func TestBuildInfoIsASingleSeries(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(BuildInfo("0.2.0", "abc123")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	families, err := r.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(families) != 1 {
		t.Fatalf("gathered %v, want only build info", names(families))
	}
	f := families[0]
	if f.GetName() != Namespace+"_build_info" {
		t.Errorf("name = %q, want %s_build_info", f.GetName(), Namespace)
	}
	if len(f.GetMetric()) != 1 {
		t.Fatalf("build info has %d series, want exactly 1", len(f.GetMetric()))
	}
	m := f.GetMetric()[0]
	if v := m.GetGauge().GetValue(); v != 1 {
		t.Errorf("value = %v, want 1 (the labels carry the information)", v)
	}
	got := map[string]string{}
	for _, l := range m.GetLabel() {
		got[l.GetName()] = l.GetValue()
	}
	if got["version"] != "0.2.0" || got["revision"] != "abc123" {
		t.Errorf("labels = %v, want the version and revision passed in", got)
	}
}

// TestHandlerServesTheExposition: the handler is what a scraper actually talks to, so
// the round trip is worth asserting rather than only the gather.
func TestHandlerServesTheExposition(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(BuildInfo("0.2.0", "abc123")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"# TYPE atlas_build_info gauge", `version="0.2.0"`, `revision="abc123"`} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition is missing %q:\n%s", want, body)
		}
	}
}

func names(families []*dto.MetricFamily) []string {
	out := make([]string, len(families))
	for i, f := range families {
		out[i] = f.GetName()
	}
	return out
}
