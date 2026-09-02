package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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

// The comparison is only worth printing when something moved, so the printing of
// a move needs a run that made one. A scenario with a different pool size against
// the same baseline is exactly the change somebody runs this to see.
func TestTheRunnerPrintsWhichWayEachMeasureMoved(t *testing.T) {
	ts, _ := liveServer(t)
	// Two seats, then one: the same dataset takes longer and queues more.
	roomy := `{
		"open": {"source":"draft","ref":"approval","seed":7,
			"stubs":{"human":{"minMillis":3600000,"maxMillis":3600000},
				"pools":{"clerks":{"capacity":2}},"poolOf":{"approve":"clerks"}}},
		"run": {"cases":[{"n":1},{"n":2},{"n":3},{"n":4}]},
		"expect": {"minCompleted":4}
	}`
	saveScenario(t, ts, "capacity", roomy)
	if err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "capacity", "--keep-baseline"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("baseline run: %v", err)
	}

	saveScenario(t, ts, "capacity", strings.Replace(roomy, `"capacity":2`, `"capacity":1`, 1))
	var out bytes.Buffer
	if err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "capacity", "--compare"}, &out); err != nil {
		t.Fatalf("second run: %v\n%s", err, out.String())
	}
	got := out.String()
	_, comparison, found := strings.Cut(got, "against the baseline:")
	if !found {
		t.Fatalf("no comparison printed:\n%s", got)
	}
	// Halving the pool makes cases wait: the queue and the slowest case both moved
	// the wrong way, and the runner says which way that is.
	if !strings.Contains(comparison, "!!") {
		t.Errorf("halving the pool moved nothing the wrong way:\n%s", got)
	}
	if !strings.Contains(comparison, "queue at clerks") {
		t.Errorf("the queue did not appear in the comparison:\n%s", got)
	}
	// A measure that did not move is left out of the *comparison* — it is still in
	// the checks above it, which is a different question. A table of unchanged
	// numbers is where the ones that did move go to hide.
	if strings.Contains(comparison, "cases completed") {
		t.Errorf("an unchanged measure was printed in the comparison:\n%s", got)
	}

	// And the other way round. Keeping the cramped run as the new baseline and then
	// restoring the seat moves the same measures the good way, which the runner
	// marks differently: a comparison that could only report bad news would be a
	// regression detector, not a comparison.
	if err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "capacity", "--keep-baseline"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("re-baseline: %v", err)
	}
	saveScenario(t, ts, "capacity", roomy)
	var better bytes.Buffer
	if err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "capacity", "--compare"}, &better); err != nil {
		t.Fatalf("third run: %v\n%s", err, better.String())
	}
	_, improved, _ := strings.Cut(better.String(), "against the baseline:")
	if !strings.Contains(improved, "->") {
		t.Errorf("restoring the seat moved nothing the good way:\n%s", better.String())
	}
	if strings.Contains(improved, "!!") {
		t.Errorf("restoring the seat moved something the wrong way:\n%s", better.String())
	}
}

// A scenario that expects nothing still runs, and says so rather than printing an
// empty verdict somebody has to interpret.
func TestAScenarioWithNoExpectationsSaysSo(t *testing.T) {
	ts, _ := liveServer(t)
	saveScenario(t, ts, "smoke", `{
		"open": {"source":"draft","ref":"approval","seed":7,
			"stubs":{"human":{"minMillis":60000,"maxMillis":60000}}},
		"run": {"cases":[{"n":1}]}
	}`)

	var out bytes.Buffer
	if err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "smoke"}, &out); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "no expectations to check") {
		t.Errorf("a scenario with no expectations did not say so:\n%s", out.String())
	}
}

// Asking to compare before anything has been kept is an ordinary state, not an
// error: it is the first run of a new scenario, and it says what to do next.
func TestComparingWithNoBaselineYetSaysWhatToDo(t *testing.T) {
	ts, _ := liveServer(t)
	saveScenario(t, ts, "fresh", scenarioSpecJSON)

	var out bytes.Buffer
	if err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "fresh", "--compare"}, &out); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "--keep-baseline") {
		t.Errorf("the runner did not say how to set a baseline:\n%s", out.String())
	}
}

// A file that is not a scenario is refused with the path in the message: a CI log
// that says only "invalid character" costs somebody an afternoon.
func TestAFileThatIsNotAScenarioIsRefusedByName(t *testing.T) {
	ts, _ := liveServer(t)
	path := filepath.Join(t.TempDir(), "notes.json")
	if err := os.WriteFile(path, []byte("this is not JSON"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := runPlaygroundScenario([]string{"--server", ts.URL, "--file", path}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "notes.json") {
		t.Errorf("err = %v, want the file named in it", err)
	}
}

// The runner carries a credential when it is given one: a server with --auth
// refuses every call without it, and "the scenario is missing" is what that looks
// like from the outside if the header never goes out.
func TestTheRunnerSendsItsToken(t *testing.T) {
	var auth string
	upstream, _ := liveServer(t)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("Authorization"); h != "" {
			auth = h
		}
		req, _ := http.NewRequest(r.Method, upstream.URL+r.URL.RequestURI(), r.Body)
		req.Header = r.Header.Clone()
		resp, err := upstream.Client().Do(req)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(proxy.Close)
	saveScenario(t, upstream, "nightly", scenarioSpecJSON)

	if err := runPlaygroundScenario(
		[]string{"--server", proxy.URL, "--scenario", "nightly", "--token", "  sekrit\n"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	// Trimmed, because a token read out of a file or a shell export routinely
	// carries a newline and a bearer sent with one is refused for a reason nothing
	// in the 401 explains.
	if auth != "Bearer sekrit" {
		t.Errorf("Authorization = %q, want the trimmed bearer", auth)
	}
}

// scriptedServer answers the runner from a table keyed by the tail of the path,
// so the ways a run can go wrong that a healthy Atlas will not produce on demand
// can still be exercised deliberately. Anything unscripted answers an empty
// object, which is enough for the calls a case does not care about.
func scriptedServer(t *testing.T, script map[string]string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for suffix, body := range script {
			if strings.HasSuffix(r.URL.Path, suffix) {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		_, _ = w.Write([]byte(`{"id":"s1","state":"finished","cases":1,"completed":1,"passed":true}`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// Every way the conversation with the server can go wrong is reported with enough
// in it to act on. A CI log that says only "failed" costs somebody an afternoon,
// and these are exactly the failures nobody is watching when they happen.
func TestTheRunnerReportsWhatWentWrongWithTheServer(t *testing.T) {
	t.Run("a server that is not there", func(t *testing.T) {
		err := runPlaygroundScenario([]string{"--server", "http://127.0.0.1:1", "--scenario", "x"}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "/api/v1/playground/scenarios/x") {
			t.Errorf("err = %v, want the request that could not be made", err)
		}
	})

	t.Run("an answer that is not JSON", func(t *testing.T) {
		ts := scriptedServer(t, map[string]string{"/scenarios/x": "not json at all"})
		err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "x"}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "decode response") {
			t.Errorf("err = %v, want the decode named", err)
		}
	})

	t.Run("a session with no id", func(t *testing.T) {
		ts := scriptedServer(t, map[string]string{
			"/scenarios/x":         `{"spec":{"open":{},"run":{}}}`,
			"/playground/sessions": `{}`,
		})
		err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "x"}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "without an id") {
			t.Errorf("err = %v, want the missing id named", err)
		}
	})

	t.Run("a run the server gives up on", func(t *testing.T) {
		ts := scriptedServer(t, map[string]string{
			"/scenarios/x": `{"spec":{"open":{},"run":{}}}`,
			"/runs":        `{"state":"failed","error":"the sandbox could not be read"}`,
		})
		var out bytes.Buffer
		err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "x"}, &out)
		if err == nil || !strings.Contains(err.Error(), "the sandbox could not be read") {
			t.Errorf("err = %v, want the server's own reason", err)
		}
		// It is a failure of the run, not of the expectations: the two leave
		// different statuses, and only one of them means the process changed.
		if errors.Is(err, errScenarioFailed) {
			t.Error("a run that could not finish was reported as a missed expectation")
		}
	})

	t.Run("a batch that never stops", func(t *testing.T) {
		ts := scriptedServer(t, map[string]string{
			"/scenarios/x": `{"spec":{"open":{},"run":{}}}`,
			"/runs":        `{"state":"running","cases":9,"completed":1}`,
		})
		err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "x", "--timeout", "1ns"}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "still running") {
			t.Errorf("err = %v, want the deadline named", err)
		}
		if !strings.Contains(err.Error(), "1 of 9") {
			t.Errorf("err = %v, want how far it got", err)
		}
	})
}

// A described dataset is what makes a scenario worth running twice: the same
// twenty lines produce the same forty cases, so a build comparing a run against
// its baseline is measuring the process rather than the data. When nothing has
// regressed the comparison has nothing to list — and has to say so, because a
// heading with nothing under it reads as output that was cut off.
func TestAGeneratedScenarioReproducesItselfExactly(t *testing.T) {
	ts, _ := liveServer(t)
	saveScenario(t, ts, "generated", `{
		"open": {"source":"draft","ref":"approval","seed":7,
			"stubs":{"human":{"minMillis":600000,"maxMillis":5400000},
				"pools":{"clerks":{"capacity":2}},"poolOf":{"approve":"clerks"}}},
		"run": {"generate":{"count":40,"fields":[
			{"name":"amount","kind":"int","min":100,"max":5000},
			{"name":"tier","kind":"choice","choices":[{"value":"gold","weight":1},{"value":"standard","weight":9}]},
			{"name":"ref","kind":"sequence","prefix":"ORDER-"}
		]},"arrival":{"mode":"every","intervalMillis":900000}},
		"expect": {"minCompleted":40,"maxIncidents":0}
	}`)

	var first bytes.Buffer
	if err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "generated", "--keep-baseline"}, &first); err != nil {
		t.Fatalf("baseline run: %v\n%s", err, first.String())
	}
	if !strings.Contains(first.String(), "40 of 40 cases finished") {
		t.Fatalf("the description did not produce forty cases:\n%s", first.String())
	}

	var second bytes.Buffer
	if err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "generated", "--compare"}, &second); err != nil {
		t.Fatalf("second run: %v\n%s", err, second.String())
	}
	_, comparison, found := strings.Cut(second.String(), "against the baseline:")
	if !found {
		t.Fatalf("no comparison printed:\n%s", second.String())
	}
	if !strings.Contains(comparison, "nothing moved") {
		t.Errorf("the same description and seed produced a different run:\n%s", second.String())
	}
	// Durations, queues and outcomes alike: a single figure that moved would be
	// listed, so the absence of every mark is the claim being made.
	for _, mark := range []string{"!!", "->"} {
		if strings.Contains(comparison, mark) {
			t.Errorf("a measure moved between two runs of one description:\n%s", second.String())
		}
	}
}

// A per-case rule in a scenario becomes a check the build exits on, and the log
// names the cases that broke it. "2 broke it" sends somebody looking; "cases 2 and
// 3 broke it" sends them to the two rows that did.
func TestTheRunnerNamesTheCasesThatBrokeARule(t *testing.T) {
	ts, _ := liveServer(t)
	saveScenario(t, ts, "rules", `{
		"open": {"source":"draft","ref":"approval","seed":7,
			"stubs":{"human":{"minMillis":600000,"maxMillis":600000}}},
		"run": {"cases":[{"betrag":100},{"betrag":9000},{"betrag":9000}]},
		"expect": {"minCompleted":3,"rules":[
			{"name":"small ones reach the end","when":"betrag < 1000","then":"end = \"end\""},
			{"name":"big ones go nowhere","when":"betrag > 1000","then":"end = \"nowhere\""}
		]}
	}`)

	var out bytes.Buffer
	err := runPlaygroundScenario([]string{"--server", ts.URL, "--scenario", "rules"}, &out)
	if !errors.Is(err, errScenarioFailed) {
		t.Fatalf("err = %v, want the scenario to have failed\n%s", err, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "ok   small ones reach the end") {
		t.Errorf("the rule that held is not reported as a passing check:\n%s", got)
	}
	if !strings.Contains(got, "FAIL big ones go nowhere") {
		t.Errorf("the broken rule is not reported as a failing check:\n%s", got)
	}
	// Numbered from one, as the results table numbers them.
	if !strings.Contains(got, "big ones go nowhere: cases 2, 3") {
		t.Errorf("the log does not name the offending cases:\n%s", got)
	}
	// A rule that held names nothing: a list of cases under a passing check is noise
	// somebody has to read past.
	if strings.Contains(got, "small ones reach the end: cases") {
		t.Errorf("a rule nothing broke listed cases anyway:\n%s", got)
	}
}

// The list of offending cases is bounded and says so, so a rule broken everywhere
// does not fill a build log with numbers nobody reads.
func TestTheListOfOffendingCasesIsBounded(t *testing.T) {
	for _, tc := range []struct {
		name      string
		idx       []int
		truncated bool
		want      string
	}{
		{"a few", []int{0, 4}, false, "cases 1, 5"},
		{"more than fit", []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, false, "cases 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, and more"},
		{"a sample the server already cut", []int{0, 1}, true, "cases 1, 2, and more"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := offendingCases(tc.idx, tc.truncated); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
