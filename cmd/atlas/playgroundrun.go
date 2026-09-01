package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// exitScenarioFailed is what `atlas playground` leaves when the run happened and
// did not meet its expectations. It is distinct from a generic failure on purpose:
// a CI job wants to tell "the process no longer holds up" from "the server was
// unreachable", and it can only do that if the two leave different statuses.
const exitScenarioFailed = 3

// pollInterval is how often the runner asks how far a batch has got. The status
// endpoint is O(1) in the number of cases, so this is a courtesy to the server
// rather than a bound on how fast the run can be seen to finish.
const pollInterval = 500 * time.Millisecond

// runPlaygroundScenario runs a saved Playground scenario against a running Atlas
// and exits on the verdict — the automation half of the Playground.
//
// It replays the scenario's own three requests rather than reimplementing what a
// run is: open a session, start the batch, judge the report. That is what the
// stored spec *is* (see api/playground.Scenario), so this runner cannot drift from
// what the Modeler does with the same scenario — the two send the same bodies to
// the same endpoints.
//
// The session is discarded on every path out, including a failed one: a sandbox is
// a live engine and a directory on somebody's server, and a CI job that leaves one
// behind on every red build leaves a lot of them.
func runPlaygroundScenario(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("playground", flag.ExitOnError)
	server := fs.String("server", "http://localhost:8080", "base URL of the Atlas server to run against")
	token := fs.String("token", os.Getenv("ATLAS_TOKEN"),
		"bearer token, when the server requires authentication (or ATLAS_TOKEN)")
	id := fs.String("scenario", "", "id of the saved scenario to run")
	file := fs.String("file", "", "run a scenario from a local JSON file instead of a saved one, so a scenario can be reviewed in a pull request before it is stored")
	compare := fs.Bool("compare", false, "also set the run beside the scenario's stored baseline")
	keep := fs.Bool("keep-baseline", false, "keep this run's report as the scenario's new baseline. Only on a run that passed: a baseline is what the next run is measured against, and recording a failure as the thing to beat hides the failure from every run after it")
	timeout := fs.Duration("timeout", 30*time.Minute, "how long to wait for the batch before giving up")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*id == "") == (*file == "") {
		return errors.New("name a scenario: --scenario <id> for a saved one, or --file <path> for a local one (not both)")
	}
	if *compare && *file != "" {
		return errors.New("--compare reads the baseline the server stores with a saved scenario, so it needs --scenario rather than --file")
	}
	if *keep && *file != "" {
		return errors.New("--keep-baseline writes back to a saved scenario, so it needs --scenario rather than --file")
	}

	c := &playgroundClient{base: strings.TrimRight(*server, "/"), bearer: strings.TrimSpace(*token)}

	spec, baseline, err := c.scenario(*id, *file)
	if err != nil {
		return err
	}

	sessionID, err := c.open(spec.Open)
	if err != nil {
		return err
	}
	defer c.discard(sessionID)

	if err := c.startRun(sessionID, spec.Run); err != nil {
		return err
	}
	status, err := c.awaitRun(sessionID, *timeout)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "run %s: %d of %d cases finished, %d occurrences\n",
		status.State, status.Completed, status.Cases, status.Occurrences)
	if status.State == "failed" {
		return fmt.Errorf("the run failed: %s", status.Error)
	}

	report, err := c.report(sessionID)
	if err != nil {
		return err
	}
	verdict, err := c.verdict(sessionID, spec.Expect)
	if err != nil {
		return err
	}
	printVerdict(out, verdict)

	if *compare {
		if len(baseline) == 0 {
			fmt.Fprintln(out, "\nno baseline stored yet — run once with --keep-baseline to set one")
		} else {
			cmp, err := c.compare(sessionID, baseline)
			if err != nil {
				return err
			}
			printComparison(out, cmp)
		}
	}

	if *keep {
		// Only a run that passed. A baseline is what the next run is measured
		// against, so recording a failing one as the thing to beat hides the failure
		// from every run after it — the one thing a regression baseline must not do.
		if !verdict.Passed {
			fmt.Fprintln(out, "\nnot keeping this run as the baseline: it did not pass")
		} else if err := c.keepBaseline(*id, report); err != nil {
			return err
		} else {
			fmt.Fprintln(out, "\nkept as the scenario's baseline")
		}
	}

	if !verdict.Passed {
		return errScenarioFailed
	}
	return nil
}

// errScenarioFailed says the run happened and did not meet its expectations,
// which main turns into its own exit status.
var errScenarioFailed = errors.New("the run did not meet its expectations")

func printVerdict(out io.Writer, v verdictBody) {
	if len(v.Checks) == 0 {
		fmt.Fprintln(out, "\nno expectations to check")
		return
	}
	fmt.Fprintln(out)
	for _, c := range v.Checks {
		mark := "FAIL"
		if c.Passed {
			mark = "ok  "
		}
		fmt.Fprintf(out, "%s %-28s want %-16s got %s\n", mark, c.Name, c.Want, c.Got)
	}
	if v.Passed {
		fmt.Fprintln(out, "\nPASS")
	} else {
		fmt.Fprintln(out, "\nFAIL")
	}
}

func printComparison(out io.Writer, c comparisonBody) {
	fmt.Fprintln(out, "\nagainst the baseline:")
	for _, d := range c.Deltas {
		if d.Before == d.After {
			continue // unchanged measures are noise in a diff of two runs
		}
		mark := "  "
		switch {
		case d.Better:
			mark = "->"
		case d.Worse:
			mark = "!!"
		}
		fmt.Fprintf(out, "%s %-28s %s -> %s\n", mark, d.Name,
			renderMeasure(d.Unit, d.Before), renderMeasure(d.Unit, d.After))
	}
}

// renderMeasure turns a delta's raw number into the unit it was measured in. The
// wire carries milliseconds and counts because a client should format them the way
// the rest of its output does; this is that formatting, for a terminal.
//
// Durations are rounded the same way the checks above them are. These come out of
// a virtual clock and carry nanoseconds nobody measured, and a log that prints a
// p90 as "29h21m44.964s" in one column and "29h21m45s" in another invites the
// reader to wonder which is right.
func renderMeasure(unit string, v int64) string {
	switch unit {
	case "millis":
		d := time.Duration(v) * time.Millisecond
		if d >= time.Minute {
			return d.Round(time.Second).String()
		}
		return d.String()
	case "percent":
		return fmt.Sprintf("%d%%", v)
	default:
		return fmt.Sprintf("%d", v)
	}
}

// --- the client ---------------------------------------------------------------

type playgroundClient struct {
	base   string
	bearer string
	http   http.Client
}

type scenarioSpec struct {
	Open   json.RawMessage `json:"open"`
	Run    json.RawMessage `json:"run"`
	Expect json.RawMessage `json:"expect"`
}

type runStatusBody struct {
	State       string `json:"state"`
	Occurrences int    `json:"occurrences"`
	Cases       int    `json:"cases"`
	Completed   int    `json:"completed"`
	Error       string `json:"error"`
}

type verdictBody struct {
	Passed bool `json:"passed"`
	Checks []struct {
		Name   string `json:"name"`
		Want   string `json:"want"`
		Got    string `json:"got"`
		Passed bool   `json:"passed"`
	} `json:"checks"`
}

type comparisonBody struct {
	Deltas []struct {
		Name   string `json:"name"`
		Unit   string `json:"unit"`
		Before int64  `json:"before"`
		After  int64  `json:"after"`
		Better bool   `json:"better"`
		Worse  bool   `json:"worse"`
	} `json:"deltas"`
}

// scenario reads the spec to run, and the baseline to compare against when there
// is one. A local file carries no baseline: it is a scenario somebody is reviewing
// in a pull request, not one the server has a history for.
func (c *playgroundClient) scenario(id, file string) (scenarioSpec, json.RawMessage, error) {
	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return scenarioSpec{}, nil, fmt.Errorf("read scenario file: %w", err)
		}
		var spec scenarioSpec
		if err := json.Unmarshal(raw, &spec); err != nil {
			return scenarioSpec{}, nil, fmt.Errorf("%s is not a scenario ({open, run, expect}): %w", file, err)
		}
		return spec, nil, nil
	}
	var body struct {
		Spec     scenarioSpec    `json:"spec"`
		Baseline json.RawMessage `json:"baseline"`
	}
	if err := c.do(http.MethodGet, "/api/v1/playground/scenarios/"+url.PathEscape(id), nil, &body); err != nil {
		return scenarioSpec{}, nil, err
	}
	return body.Spec, body.Baseline, nil
}

func (c *playgroundClient) open(openReq json.RawMessage) (string, error) {
	var sess struct {
		ID string `json:"id"`
	}
	if err := c.do(http.MethodPost, "/api/v1/playground/sessions", openReq, &sess); err != nil {
		return "", err
	}
	if sess.ID == "" {
		return "", errors.New("the server opened a session without an id")
	}
	return sess.ID, nil
}

func (c *playgroundClient) startRun(id string, runReq json.RawMessage) error {
	return c.do(http.MethodPost, c.sessionPath(id, "/runs"), runReq, nil)
}

// awaitRun polls until the batch stops or the deadline passes. The deadline is
// what keeps a CI job from hanging on a model that never comes to rest — the run's
// own budget bounds the work, but a wedged server is not the run's problem to
// solve.
func (c *playgroundClient) awaitRun(id string, timeout time.Duration) (runStatusBody, error) {
	deadline := time.Now().Add(timeout)
	for {
		var st runStatusBody
		if err := c.do(http.MethodGet, c.sessionPath(id, "/runs"), nil, &st); err != nil {
			return st, err
		}
		if st.State != "running" {
			return st, nil
		}
		if time.Now().After(deadline) {
			return st, fmt.Errorf("the batch was still running after %s (%d of %d cases finished)",
				timeout, st.Completed, st.Cases)
		}
		time.Sleep(pollInterval)
	}
}

func (c *playgroundClient) report(id string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := c.do(http.MethodGet, c.sessionPath(id, "/report"), nil, &raw)
	return raw, err
}

func (c *playgroundClient) verdict(id string, expect json.RawMessage) (verdictBody, error) {
	var v verdictBody
	err := c.do(http.MethodPost, c.sessionPath(id, "/verdict"), expect, &v)
	return v, err
}

func (c *playgroundClient) compare(id string, baseline json.RawMessage) (comparisonBody, error) {
	body, err := json.Marshal(map[string]json.RawMessage{"baseline": baseline})
	if err != nil {
		return comparisonBody{}, err
	}
	var cmp comparisonBody
	err = c.do(http.MethodPost, c.sessionPath(id, "/compare"), body, &cmp)
	return cmp, err
}

func (c *playgroundClient) keepBaseline(id string, report json.RawMessage) error {
	return c.do(http.MethodPut, "/api/v1/playground/scenarios/"+url.PathEscape(id)+"/baseline", report, nil)
}

// discard releases the sandbox. Its failure is not reported: the run's answer is
// already in hand, and a tidy-up that could not reach the server is not a reason
// to turn a green build red — the session's TTL reclaims it either way.
func (c *playgroundClient) discard(id string) {
	_ = c.do(http.MethodDelete, c.sessionPath(id, ""), nil, nil)
}

func (c *playgroundClient) sessionPath(id, suffix string) string {
	return "/api/v1/playground/sessions/" + url.PathEscape(id) + suffix
}

// do makes one request and decodes the answer into out, which may be nil when the
// answer is not wanted. A non-2xx carries the server's own message, because a CI
// log that says only "400" costs somebody an afternoon.
func (c *playgroundClient) do(method, path string, body json.RawMessage, out any) error {
	var rdr io.Reader
	if len(body) > 0 {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("%s %s: read response: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, msg)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: decode response: %w", method, path, err)
	}
	return nil
}
