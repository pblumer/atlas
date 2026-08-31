package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// scenarioBPMN is a model with one human task and one gateway branch, so a
// scenario has both a duration to bound and a path to demand.
const scenarioBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             id="defs_cli" targetNamespace="http://atlas/test">
  <process id="approval" name="Approval" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="approve"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="approve"/>
    <sequenceFlow id="f2" sourceRef="approve" targetRef="end"/>
  </process>
</definitions>`

// scenarioSpecJSON is the run the tests below store and replay: two cases, each an
// hour of work, everything expected to finish.
const scenarioSpecJSON = `{
	"open": {"source":"draft","ref":"approval","seed":7,
		"stubs":{"human":{"minMillis":3600000,"maxMillis":3600000}}},
	"run": {"cases":[{"n":1},{"n":2}],"arrival":{"mode":"allAtOnce"}},
	"expect": {"minCompleted":2,"maxIncidents":0,"minVisits":{"approve":2}}
}`

// seen records every request the runner made, so a test can assert on what it
// asked for rather than only on what it printed. Releasing the sandbox is the
// clearest example: it leaves no trace in the output and matters on every path.
type seen struct {
	mu    sync.Mutex
	calls []string
}

func (s *seen) record(method, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, method+" "+path)
}

// deletedSessions is how many sandboxes the runner released.
func (s *seen) deletedSessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.calls {
		if strings.HasPrefix(c, "DELETE /api/v1/playground/sessions/") {
			n++
		}
	}
	return n
}

// liveServer brings up a real Atlas over HTTP with the model saved as a draft, so
// the runner is exercised against the endpoints it will meet rather than a mock.
// The CLI is wiring, and wiring is exactly what a unit test of the packages under
// it cannot reach.
func liveServer(t *testing.T) (*httptest.Server, *seen) {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	watched := &seen{}
	inner := srv.Handler()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		watched.record(r.Method, r.URL.Path)
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/drafts", strings.NewReader(scenarioBPMN))
	req.Header.Set("Content-Type", "application/xml")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("save draft: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save draft = %s", resp.Status)
	}
	return ts, watched
}

// saveScenario stores the spec under an id, through the API the Modeler uses.
func saveScenario(t *testing.T, ts *httptest.Server, id, spec string) {
	t.Helper()
	body := `{"id":"` + id + `","name":"` + id + `","processId":"approval","spec":` + spec + `}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/playground/scenarios", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("save scenario: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save scenario = %s", resp.Status)
	}
}

// The CI half of the Playground: a stored scenario replayed against a running
// Atlas, judged, and reported in a form somebody reads in a build log.
func TestTheRunnerRunsAStoredScenarioAndPassesIt(t *testing.T) {
	ts, watched := liveServer(t)
	saveScenario(t, ts, "nightly", scenarioSpecJSON)

	var out bytes.Buffer
	if err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "nightly"}, &out); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	got := out.String()
	for _, want := range []string{"2 of 2 cases finished", "cases completed", "visits to approve", "PASS"} {
		if !strings.Contains(got, want) {
			t.Errorf("the output does not mention %q:\n%s", want, got)
		}
	}
	// The sandbox is released on the way out: a CI job that leaves one behind on
	// every build leaves a lot of them.
	if n := watched.deletedSessions(); n != 1 {
		t.Errorf("the runner released %d sandbox(es), want exactly the one it opened", n)
	}
}

// A run that did not meet its expectations leaves its own error, which main turns
// into its own exit status: a CI job has to tell "the process no longer holds up"
// from "the server was unreachable".
func TestTheRunnerFailsTheBuildOnAMissedExpectation(t *testing.T) {
	ts, watched := liveServer(t)
	saveScenario(t, ts, "strict", `{
		"open": {"source":"draft","ref":"approval","seed":7,
			"stubs":{"human":{"minMillis":3600000,"maxMillis":3600000}}},
		"run": {"cases":[{"n":1},{"n":2}]},
		"expect": {"minCompleted":5,"maxMillis":60000}
	}`)

	var out bytes.Buffer
	err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "strict"}, &out)
	if !errors.Is(err, errScenarioFailed) {
		t.Fatalf("err = %v, want the scenario-failed error\n%s", err, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "FAIL") || !strings.Contains(got, "at least 5") {
		t.Errorf("the output does not say what was missed:\n%s", got)
	}
	// Every check is reported, not just the first: a run that misses two targets
	// should say so once rather than over two builds.
	if strings.Count(got, "FAIL") < 3 { // two checks plus the closing verdict
		t.Errorf("only some of the failed checks were reported:\n%s", got)
	}
	if n := watched.deletedSessions(); n != 1 {
		t.Errorf("a failing run released %d sandbox(es), want the one it opened", n)
	}
}

// A baseline makes the runner answer "did that change help?" rather than only
// "does it still hold up". It is kept only from a run that passed: a failing
// baseline is the thing to beat, which would hide the failure from every run after
// it.
func TestTheRunnerKeepsAndComparesABaseline(t *testing.T) {
	ts, _ := liveServer(t)
	saveScenario(t, ts, "nightly", scenarioSpecJSON)

	var first bytes.Buffer
	if err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "nightly", "--keep-baseline"}, &first); err != nil {
		t.Fatalf("first run: %v\n%s", err, first.String())
	}
	if !strings.Contains(first.String(), "kept as the scenario's baseline") {
		t.Fatalf("the first run did not keep a baseline:\n%s", first.String())
	}

	var second bytes.Buffer
	if err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "nightly", "--compare"}, &second); err != nil {
		t.Fatalf("second run: %v\n%s", err, second.String())
	}
	// The same scenario on the same seed produces the same numbers, so the
	// comparison has nothing to report — and says so by listing nothing rather than
	// by inventing movement.
	got := second.String()
	if !strings.Contains(got, "against the baseline:") {
		t.Errorf("the second run did not compare:\n%s", got)
	}
	if strings.Contains(got, "->") && !strings.Contains(got, "-> ") {
		t.Errorf("a reproducible re-run reported a change:\n%s", got)
	}
}

// A failing run must not become the baseline, however explicitly the flag was
// passed: that is the one thing a regression baseline exists to prevent.
func TestAFailingRunIsNotKeptAsTheBaseline(t *testing.T) {
	ts, _ := liveServer(t)
	saveScenario(t, ts, "strict", `{
		"open": {"source":"draft","ref":"approval","seed":7,
			"stubs":{"human":{"minMillis":3600000,"maxMillis":3600000}}},
		"run": {"cases":[{"n":1}]},
		"expect": {"minCompleted":99}
	}`)

	var out bytes.Buffer
	if err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "strict", "--keep-baseline"}, &out); !errors.Is(err, errScenarioFailed) {
		t.Fatalf("err = %v, want the scenario to fail\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "not keeping this run as the baseline") {
		t.Errorf("the runner did not say it withheld the baseline:\n%s", out.String())
	}
	var scenario struct {
		Baseline json.RawMessage `json:"baseline"`
	}
	resp, err := ts.Client().Get(ts.URL + "/api/v1/playground/scenarios/strict")
	if err != nil {
		t.Fatalf("read scenario: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(&scenario); err != nil {
		t.Fatalf("decode scenario: %v", err)
	}
	if len(scenario.Baseline) != 0 {
		t.Errorf("a failing run was recorded as the baseline: %s", scenario.Baseline)
	}
}

// A scenario in a file is one somebody is reviewing in a pull request before it is
// stored anywhere. It runs the same way — the spec is the same three requests —
// and the flags that write back to the server are refused rather than ignored.
func TestTheRunnerRunsAScenarioFromAFile(t *testing.T) {
	ts, _ := liveServer(t)
	path := filepath.Join(t.TempDir(), "nightly.json")
	if err := os.WriteFile(path, []byte(scenarioSpecJSON), 0o600); err != nil {
		t.Fatalf("write scenario: %v", err)
	}

	var out bytes.Buffer
	if err := runPlaygroundScenario([]string{"--server", ts.URL, "--file", path}, &out); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "PASS") {
		t.Errorf("a scenario from a file did not pass:\n%s", out.String())
	}

	for _, flagName := range []string{"--compare", "--keep-baseline"} {
		if err := runPlaygroundScenario([]string{"--server", ts.URL, "--file", path, flagName}, &bytes.Buffer{}); err == nil {
			t.Errorf("%s was accepted with --file, but there is no saved scenario to read or write", flagName)
		}
	}
}

// The mistakes worth catching before anything is run: naming no scenario, naming
// two, and naming one the server does not have.
func TestTheRunnerRefusesAnAmbiguousOrMissingScenario(t *testing.T) {
	ts, _ := liveServer(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"neither", []string{"--server", ts.URL}},
		{"both", []string{"--server", ts.URL, "--scenario", "a", "--file", "b.json"}},
		{"a scenario the server does not have", []string{"--server", ts.URL, "--scenario", "nope"}},
		{"a file that is not there", []string{"--server", ts.URL, "--file", "/nowhere/nope.json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := runPlaygroundScenario(tc.args, &bytes.Buffer{}); err == nil {
				t.Error("the runner accepted it")
			}
		})
	}
}

// The two halves of the output measure the same durations, so they render them
// the same way. A log that prints a p90 as "29h21m44.964s" in the comparison and
// "29h21m45s" in the checks invites the reader to wonder which is right.
func TestTheTwoHalvesOfTheOutputAgreeOnPrecision(t *testing.T) {
	for _, tc := range []struct {
		millis int64
		want   string
	}{
		{(29*time.Hour + 21*time.Minute + 44*time.Second + 964*time.Millisecond).Milliseconds(), "29h21m45s"},
		{(90 * time.Second).Milliseconds(), "1m30s"},
		{1500, "1.5s"},
	} {
		if got := renderMeasure("millis", tc.millis); got != tc.want {
			t.Errorf("renderMeasure(%d ms) = %q, want %q", tc.millis, got, tc.want)
		}
	}
	if got := renderMeasure("percent", 96); got != "96%" {
		t.Errorf("a percentage rendered as %q", got)
	}
	if got := renderMeasure("count", 41); got != "41" {
		t.Errorf("a count rendered as %q", got)
	}
}
