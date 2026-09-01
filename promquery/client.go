// Package promquery asks a Prometheus-compatible store what it recorded about an
// Atlas node (ADR-0189 P5b).
//
// It is the read side of what [metrics] writes, and it is a separate package for
// that reason: `metrics` owns Atlas's registry and the /metrics handler, and
// putting a client for somebody else's server in it would make the exposition
// depend on a query language it does not speak.
//
// # What this can and cannot ask
//
// Atlas's metrics carry no per-element labels, and ADR-0142 says why: a label whose
// values the data can invent — a process id, an instance key — turns one metric
// into unboundedly many series. That rule is the reason this package answers about
// a *node* and never about one process, and it is a property of the contract rather
// than a gap to close later.
//
// A node is identified the only way a metrics store knows one: by the scrape target
// it came from. Atlas can derive that from a deployment target's base URL, and for
// the server itself it cannot derive it at all — how this process appears in
// somebody's Prometheus is their scrape configuration, not Atlas's. So it is
// configured, and left unset it makes the local node honestly unidentifiable rather
// than silently matching the wrong series.
package promquery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxResponseBytes bounds a query response. Every query here is an aggregate over
// a bounded number of steps, so a correct answer is small; a large body means a
// store answering something other than what was asked.
const maxResponseBytes = 1 << 20

// ErrQueryRefused is returned when the store answered and declined — credentials,
// or a permission on its side. Callers separate it from a transport failure
// because the two send an operator to different places.
var ErrQueryRefused = fmt.Errorf("promquery: query refused")

// Config is the server-side configuration of the metrics reader. URL is the
// Prometheus-compatible base URL (e.g. "https://prometheus:9090"); an empty URL
// disables it. Username/Password are optional HTTP basic-auth credentials.
type Config struct {
	URL      string
	Username string
	Password string
	// Instance is how *this* node appears in the store's `instance` label. Atlas
	// cannot derive it: a scrape target is the operator's configuration, and
	// guessing it would answer a question about somebody else's process while
	// looking exactly like an answer about this one. Left empty, a binding to this
	// server's own runtime is reported unidentifiable and says so.
	Instance string
}

// Enabled reports whether a URL is configured — the opt-in switch.
func (c Config) Enabled() bool { return strings.TrimSpace(c.URL) != "" }

// Sample is one step of a range query: the moment, in Unix seconds, and the value.
type Sample struct {
	At    int64
	Value float64
}

// Querier runs one range query. It is an interface so a caller is testable without
// a live store.
type Querier interface {
	// QueryRange evaluates expr at each step between from and to, in Unix seconds, and
	// returns the samples of the single series it reduces to. A query that matches
	// nothing returns no samples and no error — that is an answer, not a failure.
	QueryRange(ctx context.Context, expr string, from, to, step int64) ([]Sample, error)
}

// HTTPClient implements [Querier] against a real store.
type HTTPClient struct {
	cfg  Config
	http *http.Client
}

// NewHTTPClient builds a client for a configured store.
func NewHTTPClient(cfg Config) *HTTPClient {
	return &HTTPClient{cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}}
}

// rangeResponse is the subset of the reply this reads. Prometheus reports its own
// failures in the body with a 200, so `status` is checked rather than assumed.
type rangeResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Values [][2]json.RawMessage `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// QueryRange posts one range query to /api/v1/query_range.
//
// The caller's expression is expected to reduce to a single series — every
// expression this serves wraps its metric in an aggregation — so only the first
// result is read. A store that returned several would mean the aggregation was
// dropped, and quietly summing them here would hide that.
func (c *HTTPClient) QueryRange(ctx context.Context, expr string, from, to, step int64) ([]Sample, error) {
	if step < 1 {
		step = 1
	}
	form := url.Values{
		"query": {expr},
		"start": {strconv.FormatInt(from, 10)},
		"end":   {strconv.FormatInt(to, 10)},
		"step":  {strconv.FormatInt(step, 10)},
	}
	endpoint := strings.TrimRight(c.cfg.URL, "/") + "/api/v1/query_range"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("promquery: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.cfg.Username != "" || c.cfg.Password != "" {
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// The address is deliberately not carried out of here: a context answer is
		// readable by anyone who may read the model, and the store's endpoint is
		// infrastructure they were not necessarily told about (ADR-0189 §6).
		return nil, fmt.Errorf("promquery: request failed")
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, ErrQueryRefused
	case resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusBadRequest:
		// 400 is how Prometheus reports a bad expression, and its body says which —
		// worth reading rather than discarding as a transport failure.
		return nil, fmt.Errorf("promquery: store returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("promquery: read response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("promquery: response exceeds %d bytes", maxResponseBytes)
	}

	var parsed rangeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("promquery: decode response: %w", err)
	}
	if parsed.Status != "success" {
		// Prometheus reports its own failures in the body. Carrying its sentence
		// through is what lets an operator fix a query rather than guess at one.
		return nil, fmt.Errorf("promquery: store reported %s: %s", parsed.ErrorType, parsed.Error)
	}
	if len(parsed.Data.Result) == 0 {
		// Matched nothing. That is an answer — the store was asked and holds no such
		// series — and the caller renders it as the empty state rather than a fault.
		return nil, nil
	}
	return samplesOf(parsed.Data.Result[0].Values)
}

// samplesOf decodes Prometheus's [timestamp, "value"] pairs. The timestamp is a
// JSON number and the value a *string*, which is the format's way of not losing
// precision on NaN and large floats; both are decoded explicitly rather than into
// `any`, so a change in either shape fails loudly instead of yielding zeros.
func samplesOf(values [][2]json.RawMessage) ([]Sample, error) {
	samples := make([]Sample, 0, len(values))
	for _, pair := range values {
		var at float64
		if err := json.Unmarshal(pair[0], &at); err != nil {
			return nil, fmt.Errorf("promquery: sample timestamp: %w", err)
		}
		var raw string
		if err := json.Unmarshal(pair[1], &raw); err != nil {
			return nil, fmt.Errorf("promquery: sample value: %w", err)
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("promquery: sample value %q: %w", raw, err)
		}
		// NaN is what Prometheus returns for a step it cannot evaluate, and +Inf for
		// one that divided by zero. Both parse cleanly — ParseFloat accepts "NaN" —
		// so they have to be rejected on their value rather than on a parse failure.
		// They are real readings of "nothing here", so the step is dropped and the
		// series kept; carrying one through would put the literal NaN on screen.
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		samples = append(samples, Sample{At: int64(at), Value: value})
	}
	return samples, nil
}

// EscapeLabelValue quotes a value for a PromQL label matcher. Every value this
// package matches on is operator configuration rather than user input, but a base
// URL with a quote in it would still produce a query that means something other
// than intended, and a malformed one is the better failure.
func EscapeLabelValue(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return replacer.Replace(value)
}
