package api

import (
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/pblumer/atlas/api/feelgen"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/metrics"
)

// What the FEEL assistant is allowed to reach (ADR-0445).
//
// It reaches less than form generation does, and the same of it: the agent Workers an
// operator configured, and one of them dialled with its vault-held key. There is no
// process source — an expression is written from the conversation and the author's own
// test variables, not from a model read off the server — so of the three closures form
// generation holds, the assistant needs two, and both are form generation's own. One
// configuration, one credential and one place to change the model (ADR-0255).

// agentWorkersForFeel lists the AI Workers the assistant may ask: exactly the ones form
// generation lists, in the assistant's own type.
func (s *Server) agentWorkersForFeel(r *http.Request) ([]feelgen.Worker, error) {
	listed, err := s.agentWorkersForGeneration(r)
	if err != nil {
		return nil, err
	}
	out := make([]feelgen.Worker, 0, len(listed))
	for _, w := range listed {
		out = append(out, feelgen.Worker{Name: w.Name, Model: w.Model, Provider: w.Provider})
	}
	return out, nil
}

// What the assistant's requests came to (feelgen.Outcome): a log line for each, and,
// where metrics are on, counters to chart them by.
//
// The two carry different halves on purpose. The counters are labelled only by the
// closed lists feelgen declares — the result, the answer's form, the round's fault —
// because a label whose values a request or a model can invent is the one thing
// ADR-0142 forbids, and a model name is exactly that: a request may ask for any. The
// log line carries what the counters cannot: the Worker, the model, the callees refused
// and the engine's verdicts, which is what comparing two models or two versions of the
// prompt actually needs.

// feelAssistantMetrics are the assistant's counters, each label value resolved to its
// own series when the registry is built, so a request touches a counter and never a
// label lookup (the same rule the engine's counters keep, for the same reason).
type feelAssistantMetrics struct {
	requests map[string]prometheus.Counter
	formats  map[string]prometheus.Counter
	faults   map[string]prometheus.Counter
	seconds  prometheus.Histogram

	vecs []prometheus.Collector
}

func newFeelAssistantMetrics() *feelAssistantMetrics {
	// Every series carries the prompt version as a constant label: one value per
	// build, fixed by the code, so it adds no series — and it is what keeps the
	// counts of two prompts apart across an upgrade.
	prompt := prometheus.Labels{"prompt": feelgen.PromptVersion}
	resolve := func(name, help, label string, values []string) (map[string]prometheus.Counter, prometheus.Collector) {
		vec := prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Name: name, Help: help, ConstLabels: prompt,
		}, []string{label})
		out := make(map[string]prometheus.Counter, len(values))
		for _, v := range values {
			out[v] = vec.WithLabelValues(v)
		}
		return out, vec
	}
	m := &feelAssistantMetrics{
		// A free model behind a shared gateway answers in seconds to most of a minute,
		// and a request may take three answers; the buckets end at the service's own
		// bound.
		seconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: metrics.Namespace, Name: "feel_assistant_request_seconds",
			Help:        "How long one FEEL assistant request took, correction rounds included.",
			Buckets:     []float64{1, 2, 5, 10, 20, 30, 45, 60, 90, 120, 150},
			ConstLabels: prompt,
		}),
	}
	var requests, formats, faults prometheus.Collector
	m.requests, requests = resolve("feel_assistant_requests_total",
		"FEEL assistant requests that reached a model, by how they ended.", "outcome", feelgen.Results)
	m.formats, formats = resolve("feel_assistant_attempts_total",
		"Rounds of the FEEL assistant, by the form the model answered in.", "format", feelgen.Formats)
	m.faults, faults = resolve("feel_assistant_attempt_faults_total",
		"Rounds of the FEEL assistant, by what the engine's check found (none when it passed).", "fault", feelgen.Faults)
	m.vecs = []prometheus.Collector{requests, formats, faults, m.seconds}
	return m
}

func (m *feelAssistantMetrics) collectors() []prometheus.Collector { return m.vecs }

func (m *feelAssistantMetrics) observe(o feelgen.Outcome) {
	if c, ok := m.requests[o.Result]; ok {
		c.Inc()
	}
	for _, a := range o.Attempts {
		if c, ok := m.formats[a.Format]; ok {
			c.Inc()
		}
		if c, ok := m.faults[a.Fault]; ok {
			c.Inc()
		}
	}
	m.seconds.Observe(o.Duration.Seconds())
}

// maxLoggedError bounds each round's verdict in the log line. The engine's own
// sentences are short; an endpoint's error body is not, and the start of it says what
// went wrong.
const maxLoggedError = 300

// observeFeelAssistant reports one request: the log line always, the counters when this
// server exports metrics.
func (s *Server) observeFeelAssistant(o feelgen.Outcome) {
	var (
		faults, formats, errs []string
		calls                 []string
		seen                  = map[string]bool{}
	)
	for _, a := range o.Attempts {
		faults = append(faults, a.Fault)
		formats = append(formats, a.Format)
		if a.Error != "" {
			e := a.Error
			if len(e) > maxLoggedError {
				// Cut on a character boundary: the engine's sentences carry dashes and
				// quotes that are more than one byte, and half of one is not text.
				cut := maxLoggedError
				for cut > 0 && !utf8.RuneStart(e[cut]) {
					cut--
				}
				e = e[:cut] + "…"
			}
			errs = append(errs, e)
		}
		for _, c := range a.Calls {
			if !seen[c] {
				seen[c] = true
				calls = append(calls, c)
			}
		}
	}
	logging.Info(logging.FeelAssistantAnswered, "the FEEL assistant answered",
		slog.String("worker", o.Worker), slog.String("model", o.Model), slog.String("prompt", o.Prompt),
		slog.String("outcome", o.Result), slog.Int("attempts", len(o.Attempts)),
		slog.String("faults", strings.Join(faults, ",")), slog.String("formats", strings.Join(formats, ",")),
		slog.String("calls", strings.Join(calls, ",")), slog.String("errors", strings.Join(errs, " | ")),
		slog.Int64("durationMs", o.Duration.Milliseconds()))
	if s.feelMetrics != nil {
		s.feelMetrics.observe(o)
	}
}
