package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// ADR-0495 slice A: a read of a release's architecture
// names the layers it shows, and a link that names none shows what it showed before.

func TestAReadShowsTheLayersItAsksFor(t *testing.T) {
	ts, stop := openServerAt(t, t.TempDir(), api.WithExternalURL("https://atlas.example.com/"))
	defer stop()
	c := ts.Client()
	app := setUpPhysical(t, c, c, ts.URL)
	base := ts.URL + "/api/v1/applications/" + app + "/releases/2/architecture"

	var whole struct {
		Layers struct {
			Recorded []string `json:"recorded"`
			Shown    []string `json:"shown"`
		} `json:"layers"`
		Architecture struct {
			EntryPoints []json.RawMessage `json:"entryPoints"`
			Physical    *json.RawMessage  `json:"physical"`
		} `json:"architecture"`
	}
	if err := json.Unmarshal(mustCall(t, c, http.MethodGet, base, "", "", http.StatusOK), &whole); err != nil {
		t.Fatal(err)
	}
	if strings.Join(whole.Layers.Recorded, ",") != "application,technology" || strings.Join(whole.Layers.Shown, ",") != "application,technology" {
		t.Errorf("layers = %+v, want both recorded and both shown by default", whole.Layers)
	}

	var logical struct {
		Layers struct {
			Shown []string `json:"shown"`
		} `json:"layers"`
		Architecture struct {
			Physical *json.RawMessage `json:"physical"`
		} `json:"architecture"`
	}
	if err := json.Unmarshal(mustCall(t, c, http.MethodGet, base+"?layers=application", "", "", http.StatusOK), &logical); err != nil {
		t.Fatal(err)
	}
	if logical.Architecture.Physical != nil || strings.Join(logical.Layers.Shown, ",") != "application" {
		t.Errorf("the application layer alone = %+v, want no physical part", logical)
	}

	svg := string(mustCall(t, c, http.MethodGet, base+".svg?layers=technology", "", "", http.StatusOK))
	for _, want := range []string{"Physical view of prod-1 (PROD)", "→ https://clio.example.com:8443", "Job type adresse-pruefen"} {
		if !strings.Contains(svg, want) {
			t.Errorf("the technology layer alone lacks %q", want)
		}
	}
	if strings.Contains(svg, "Public start form") {
		t.Error("the technology layer alone draws a public start form, an application interface")
	}
	if xml := string(mustCall(t, c, http.MethodGet, base+".xml?layers=Application", "", "", http.StatusOK)); strings.Contains(xml, "clio.example.com") {
		t.Error("the ArchiMate document of the application layer alone carries an endpoint")
	}

	if code, body := call(t, c, http.MethodGet, base+".svg?layers=application,netzwerk", "", ""); code != http.StatusBadRequest ||
		!strings.Contains(string(body), "netzwerk") {
		t.Errorf("an unknown layer answers %d %s, want 400 naming it", code, body)
	}
	if code, body := call(t, c, http.MethodGet, base+".svg?layers=strategy", "", ""); code != http.StatusNotFound ||
		!strings.Contains(string(body), "records none of the layers asked for (strategy); it records application,technology") {
		t.Errorf("a layer the release does not record answers %d %s, want 404 saying what it records", code, body)
	}
}
