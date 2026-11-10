package worker_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/connector/ldif"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
	"github.com/pblumer/atlas/worker"
)

// The example "AD-Benutzer prüfen" runs as shipped (docs/planning/ad-search-as-document.md,
// S2): its model, its decision and its seed are read from examples/ad-benutzer-pruefen
// unchanged. A Worker Instance serves the AD search against a mock directory started
// from benutzer.ldif, as the AD worker's mockup mode does, and writes the accounts as a
// document; the business rule task over that file checks each account in the
// in-process DMN worker. The six accounts the seed describes as defective are the six
// the check reports, with the rules each one breaks.
func TestTheADUserCheckExampleRunsAgainstTheMockDirectory(t *testing.T) {
	dir := filepath.Join("..", "examples", "ad-benutzer-pruefen")
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	ts := exampleAtlas(t, api.WithOffloadedConnectorKinds([]string{"ad"}))

	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(post(t, ts, "/api/v1/applications", `{"name":"Beispiel: AD-Benutzer prüfen"}`, "application/json"), &app); err != nil || app.ID == "" {
		t.Fatalf("application: %v", err)
	}
	post(t, ts, "/api/v1/dmn-models?handle=ad-benutzer-pruefen", string(read("ad-benutzer-pruefen.dmn")), "application/xml")
	post(t, ts, "/api/v1/dmnrefs", `{"name":"AdBenutzerGueltig","modelRef":"ad-benutzer-pruefen","projectId":"`+app.ID+`"}`, "application/json")
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(post(t, ts, "/api/v1/deployments?projectId="+app.ID, string(read("ad-benutzer-pruefen.bpmn")), "application/xml"), &dep); err != nil || dep.Key == 0 {
		t.Fatalf("deploy: %v", err)
	}
	post(t, ts, "/api/v1/processes/"+itoa(dep.Key)+"/instances", `{}`, "application/json")

	parsed, err := ldif.Parse(ldif.FormatLDIF, read("benutzer.ldif"))
	if err != nil {
		t.Fatalf("the seed does not parse: %v", err)
	}
	seed := make([]ad.Entry, 0, len(parsed))
	for _, e := range parsed {
		seed = append(seed, ad.Entry{DN: e.DN, Attributes: e.Attributes})
	}
	// The mock refuses a bind that names a DN and sends no password, as AD does; what
	// the password is does not matter (the handbook's mockup section says so).
	secret := func(ref string) string {
		if ref == "AD_BIND" {
			return "beliebig"
		}
		return ""
	}
	directory := ad.NewMockDirectory(seed...)
	var handlerErr error
	w := worker.New(worker.Options{Server: ts.URL, ID: "ad-mock", Handlers: map[string]worker.Exec{
		compiler.AdJobType: worker.ExecFunc(func(ctx context.Context, j worker.Job) (map[string]any, error) {
			out, err := worker.RunADJob(ctx, j, directory, secret, nil)
			handlerErr = err
			return out, err
		}),
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if handlerErr != nil {
		t.Fatalf("the AD search failed: %v", handlerErr)
	}
	waitFor(t, "the check to finish", func() bool { return runningInstances(t, ts) == 0 })

	vars := instanceVariables(t, ts)
	benutzer, _ := vars["benutzer"].(map[string]any)
	if benutzer["count"] != 24.0 || benutzer["found"] != true {
		t.Fatalf("benutzer = %v", benutzer)
	}
	var sum struct {
		Total, Valid, Invalid, Errors int
		Failures                      []map[string]any
		Result                        map[string]any
	}
	raw, _ := json.Marshal(vars["pruefung"])
	if err := json.Unmarshal(raw, &sum); err != nil {
		t.Fatalf("pruefung = %s", raw)
	}
	if sum.Total != 24 || sum.Valid != 18 || sum.Invalid != 6 || sum.Errors != 0 {
		t.Fatalf("pruefung = %s", raw)
	}
	// Each defective account fails on exactly the rules the seed's comment names.
	want := map[string][]string{
		"ebaumann": {"kontoAktiv"},
		"iweber":   {"mailGesetzt"},
		"lsteiner": {"abteilungBekannt"},
		"obucher":  {"lizenzEindeutig"},
		"sroth":    {"lizenzEindeutig"},
		"vlehmann": {"kontoAktiv", "mailGesetzt"},
	}
	rules := []string{"kontoAktiv", "mailGesetzt", "abteilungBekannt", "lizenzEindeutig"}
	for _, f := range sum.Failures {
		sam, _ := f["sAMAccountName"].(string)
		broken, ok := want[sam]
		if !ok {
			t.Errorf("%s is reported, but the seed describes it as in order: %v", sam, f)
			continue
		}
		delete(want, sam)
		for _, r := range rules {
			isBroken := strings.Contains(" "+strings.Join(broken, " ")+" ", " "+r+" ")
			// A rule is reported under its name with a leading _, so it cannot collide
			// with a column of the file.
			if f["_"+r] != !isBroken {
				t.Errorf("%s: _%s = %v, want %v", sam, r, f["_"+r], !isBroken)
			}
		}
	}
	if len(want) != 0 {
		t.Errorf("not reported: %v", want)
	}

	// The result document holds every account with its verdict.
	id, _ := sum.Result["$document"].(string)
	resp, err := http.Get(ts.URL + "/api/v1/documents/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(body), "\uFEFF"))).ReadAll()
	if err != nil || resp.StatusCode != http.StatusOK || len(rows) != 25 {
		t.Fatalf("result document = %d, %d rows (%v)", resp.StatusCode, len(rows), err)
	}
	if name, _ := sum.Result["name"].(string); name != "benutzer.result.csv" {
		t.Errorf("result document is named %q", name)
	}
}

// exampleAtlas is a server with nothing deployed: the example's test deploys its
// decision before its model, because the deploy gate refuses a decision binding that
// does not resolve.
func exampleAtlas(t *testing.T, opts ...api.Option) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatal(err)
	}
	srv, err := api.New(proc, store, dir, opts...)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close(); _ = store.Close(); _ = log.Close() })
	return ts
}
