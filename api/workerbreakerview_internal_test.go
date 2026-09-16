package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The operator's half of ADR-0340, which the record makes part of the decision rather
// than a follow-up: *"work that silently does not happen is the one failure mode an
// operator cannot diagnose. A flood at least says something is wrong."*
//
// A breaker turns a flood into silence. These tests pin what breaks that silence — the
// Workers view, the control that ends a hold early, and the metrics an alert fires on.

type breakerRow struct {
	JobType   string `json:"jobType"`
	Connector string `json:"connector"`
	State     string `json:"state"`
	TrippedAt int64  `json:"trippedAt"`
	Reason    string `json:"reason"`
	ProbeAt   int64  `json:"probeAt"`
	Cooldown  int64  `json:"cooldown"`
	Refused   int64  `json:"refused"`
}

func breakerRows(t *testing.T, srv *Server) []breakerRow {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/workers", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET /workers: status=%d body=%s", code, raw)
	}
	var out struct {
		Breakers []breakerRow `json:"breakers"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode workers: %v (%s)", err, raw)
	}
	return out.Breakers
}

// TestTheWorkersViewNamesWhatIsBeingHeld is the row an operator lands on when work has
// stopped. It has to answer the four questions they are about to ask — which target,
// since when, on what, and when the engine will try again — because none of them is
// answerable from the queue depth alone.
func TestTheWorkersViewNamesWhatIsBeingHeld(t *testing.T) {
	srv, _ := floodedServer(t, 20)

	rows := breakerRows(t, srv)
	if len(rows) != 1 {
		t.Fatalf("breakers = %+v, want the one that is holding work back", rows)
	}
	got := rows[0]
	if got.Connector != "Patrick Blumer" {
		t.Errorf("connector = %q, want the Worker the model names", got.Connector)
	}
	if got.JobType != "io.atlas.mail.send" {
		t.Errorf("jobType = %q, want the name, not an interned index", got.JobType)
	}
	if got.State != "open" {
		t.Errorf("state = %q, want open", got.State)
	}
	if got.TrippedAt == 0 || got.ProbeAt <= got.TrippedAt {
		t.Errorf("trippedAt=%d probeAt=%d, want a trip and a probe after it", got.TrippedAt, got.ProbeAt)
	}
	if got.Cooldown != int64(breakerCooldown) {
		t.Errorf("cooldown = %d, want the first cooldown %d", got.Cooldown, int64(breakerCooldown))
	}
	if !strings.Contains(got.Reason, "Patrick Blumer") {
		t.Errorf("reason = %q, want the failure that tripped it", got.Reason)
	}
}

// TestNothingIsListedWhenNothingIsHeld keeps the view honest in the ordinary case: a
// server where every target answers shows no breaker row at all, rather than a table of
// green ones nobody needs to read.
func TestNothingIsListedWhenNothingIsHeld(t *testing.T) {
	srv, cleanup := newOffLoopServer(t)
	defer cleanup()

	if rows := breakerRows(t, srv); len(rows) != 0 {
		t.Errorf("breakers = %+v on a healthy server, want none", rows)
	}
}

// TestCloseNowEndsTheHoldFromTheConsole is the control the record asks for by name: an
// operator who has already fixed the endpoint does not wait out a cooldown. It is the
// only way a person may touch a breaker, and it deliberately cannot do the opposite —
// there is no "open this", because judging a target down is a conclusion the engine
// draws from evidence, not an opinion a person asserts.
func TestCloseNowEndsTheHoldFromTheConsole(t *testing.T) {
	srv, _ := floodedServer(t, 20)
	jobType := mailJobType(t, srv)
	if !srv.breakers.holdingFor(jobType) {
		t.Fatal("setup: nothing is being held")
	}

	// The operator fixes the endpoint first, which is the case the button exists for.
	conn := `{"name":"Patrick Blumer","kind":"mail","provider":"preview","endpoint":"mx.example.ch:587","sender":"a@b.ch"}`
	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/connectors", conn, "application/json"); code != http.StatusOK {
		t.Fatalf("configure the worker: status=%d body=%s", code, raw)
	}

	body := `{"jobType":"io.atlas.mail.send","connector":"Patrick Blumer"}`
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/workers/breakers/close", body, "application/json")
	if code != http.StatusOK {
		t.Fatalf("close now: status=%d body=%s", code, raw)
	}
	if !strings.Contains(string(raw), `"closed":true`) {
		t.Errorf("close now = %s, want closed:true", raw)
	}
	if srv.breakers.holdingFor(jobType) {
		t.Error("the breaker is still holding work back after an operator closed a fixed target")
	}
	if rows := breakerRows(t, srv); len(rows) != 0 {
		t.Errorf("breakers = %+v after closing, want none", rows)
	}
	// And the held backlog went out on the close rather than waiting for the cooldown,
	// which is the whole point of not waiting.
	if left := activatable(t, srv, jobType); left != 0 {
		t.Errorf("%d jobs still held after the close, want the backlog released", left)
	}

	// Closing one that is not open is not an error — an operator clicking a row that
	// recovered a second earlier has done nothing wrong — but the reply says so rather
	// than implying an action that did not happen.
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/workers/breakers/close", body, "application/json")
	if code != http.StatusOK {
		t.Fatalf("second close: status=%d body=%s", code, raw)
	}
	if !strings.Contains(string(raw), `"closed":false`) {
		t.Errorf("second close = %s, want an honest closed:false", raw)
	}
}

// TestClosingATargetThatIsStillDownCostsOneTrip is the price of pressing the button too
// early, and it is worth pinning because it is the reason closing is safe to expose at
// all: the engine re-judges from evidence. Nothing is stuck open and nothing needs a
// second operator action — the next three distinct failures hold the work again.
//
// The cost is smaller than it first looks. Those three failures each spend *one* retry
// of one instance; none of them parks a token, because a retry budget is not exhausted
// by a single attempt. So a premature close costs three attempts against a host that is
// down, not a flood.
func TestClosingATargetThatIsStillDownCostsOneTrip(t *testing.T) {
	srv, _ := floodedServer(t, 20)
	jobType := mailJobType(t, srv)
	parkedBefore := incidentCount(t, srv)
	heldBefore := activatable(t, srv, jobType)

	body := `{"jobType":"io.atlas.mail.send","connector":"Patrick Blumer"}`
	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/workers/breakers/close", body, "application/json"); code != http.StatusOK {
		t.Fatalf("close now: status=%d body=%s", code, raw)
	}

	if !srv.breakers.holdingFor(jobType) {
		t.Error("the target is still down and nothing is holding its jobs back again")
	}
	if held := activatable(t, srv, jobType); held < heldBefore-breakerThreshold-1 {
		t.Errorf("held jobs %d → %d: far more than one trip's worth went out against a host that is down",
			heldBefore, held)
	}
	// No token paid for the operator's optimism: an attempt spends a retry, and a
	// retry budget is not a token parking.
	if parked := incidentCount(t, srv); parked != parkedBefore {
		t.Errorf("incidents %d → %d, want the released attempts to have cost retries and not tokens",
			parkedBefore, parked)
	}
}

// TestCloseNowRefusesWhatItCannotActOn: a request that names no job type would mean
// "every breaker on the server", and an unknown type is a typo rather than a no-op.
func TestCloseNowRefusesWhatItCannotActOn(t *testing.T) {
	srv, _ := floodedServer(t, 20)

	for name, body := range map[string]string{
		"no job type":  `{"connector":"Patrick Blumer"}`,
		"unknown type": `{"jobType":"io.atlas.nonsense","connector":"x"}`,
		"not json":     `{`,
	} {
		code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/workers/breakers/close", body, "application/json")
		if code == http.StatusOK {
			t.Errorf("%s: status=%d body=%s, want a refusal", name, code, raw)
		}
	}
}

// TestBreakerMetricsSayWhatAnAlertNeeds. The log line says a breaker opened; a metric is
// what an alerting rule evaluates on a schedule, and that is the difference between an
// operator finding out and an operator being told.
//
// They are aggregates with no labels, and that is not laziness. ADR-0340 asked for "a
// counter per Worker", but a Worker's name comes from a deployed model — a label carrying
// it would be a label whose values the data invents, which is exactly what ADR-0142's
// cardinality rule forbids, and for the reason that rule gives: a scrape target can only
// fall over where an API can paginate. "Which target" is answered by GET /api/v1/workers.
func TestBreakerMetricsSayWhatAnAlertNeeds(t *testing.T) {
	srv, advance := floodedServer(t, 20)

	body := scrapeServer(t, srv)
	for _, want := range []string{
		"atlas_worker_breakers_open 1",
		"atlas_worker_breaker_trips_total 1",
		"atlas_worker_breaker_probes_total",
		"atlas_worker_breaker_refused_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %q:\n%s", want, breakerLines(body))
		}
	}
	// No label may carry a Worker's name or a job type's: both are model-authored, and
	// an estate of a few hundred of them would turn one metric into a few hundred series.
	for _, forbidden := range []string{`connector="`, `job_type="`, "Patrick Blumer"} {
		if strings.Contains(breakerLines(body), forbidden) {
			t.Errorf("a breaker series carries %q, which the cardinality rule forbids:\n%s",
				forbidden, breakerLines(body))
		}
	}

	// A recovered target stops being counted as open — the gauge is a statement about
	// now — but the totals stay, because a counter that restarts at zero reads as a
	// reset and every rate computed over it is wrong.
	advance(breakerCooldown)
	conn := `{"name":"Patrick Blumer","kind":"mail","provider":"preview","endpoint":"mx.example.ch:587","sender":"a@b.ch"}`
	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/connectors", conn, "application/json"); code != http.StatusOK {
		t.Fatalf("configure the worker: status=%d body=%s", code, raw)
	}
	for i := 0; i < 10 && srv.breakers.holdingFor(mailJobType(t, srv)); i++ {
		if err := srv.drive(); err != nil {
			t.Fatalf("drive: %v", err)
		}
	}
	after := scrapeServer(t, srv)
	if !strings.Contains(after, "atlas_worker_breakers_open 0") {
		t.Errorf("the open gauge did not fall back to zero after recovery:\n%s", breakerLines(after))
	}
	if !strings.Contains(after, "atlas_worker_breaker_trips_total 1") {
		t.Errorf("the trip total changed with the recovery; every rate over it would read as a reset:\n%s",
			breakerLines(after))
	}
}

func scrapeServer(t *testing.T, srv *Server) string {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodGet, "/metrics", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET /metrics: status=%d", code)
	}
	return string(raw)
}

// breakerLines trims a scrape to the breaker series, so a failure message is readable.
func breakerLines(s string) string {
	out := []string{}
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "breaker") && !strings.HasPrefix(l, "# ") {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return "(no breaker series at all)"
	}
	return strings.Join(out, "\n")
}
