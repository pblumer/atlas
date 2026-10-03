package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/pblumer/atlas/api/feelgen"
)

// Server wiring for the FEEL assistant (ADR-0445). The area's behaviour —
// the prompt, the check, the correction rounds — is tested against the service in
// api/feelgen. What is here is that this server connects it to the agent Workers an
// operator configured, through the real adapter and the real routes.

// TestFeelAssistantAsksTheConfiguredWorkerAndCorrectsIt drives the assistant the way the
// console does, against a Chat Completions endpoint standing in for OpenRouter: the first
// answer reaches for a foreign function, the engine refuses it, and the second round —
// shown the refusal — is what the author gets.
func TestFeelAssistantAsksTheConfiguredWorkerAndCorrectsIt(t *testing.T) {
	var (
		mu    sync.Mutex
		goals []string
	)
	answers := []string{
		`{"expression":"is defined(kunde.email)","explanation":"x","variables":{"kunde":{"email":"a@b.ch"}},"expected":true}`,
		`{"expression":"kunde.email != null","explanation":"Wahr, wenn eine E-Mail-Adresse erfasst ist.","variables":{"kunde":{"email":"a@b.ch"}},"expected":true}`,
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer or-key" {
			t.Errorf("authorization = %q, want the vault's key as a bearer token", got)
		}
		var req struct {
			Model    string `json:"model"`
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode model request: %v", err)
		}
		mu.Lock()
		n := len(goals)
		goals = append(goals, req.Messages[len(req.Messages)-1].Content)
		mu.Unlock()
		if req.Model != "meta-llama/llama-3.3-70b-instruct:free" {
			t.Errorf("model = %q, want the record's", req.Model)
		}
		if req.Messages[0].Role != "system" || !strings.Contains(req.Messages[0].Content, "FEEL expressions for Atlas") {
			t.Errorf("the system turn is not the assistant's prompt: %+v", req.Messages[0])
		}
		answer, _ := json.Marshal(answers[min(n, len(answers)-1)])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":` + string(answer) + `}}]}`))
	}))
	defer model.Close()

	sink := captureAuditLog(t)
	srv, _ := newValidateServer(t)
	_ = srv.connectors.Save(connector{ID: "1", Name: "openrouter", Kind: connectorKindAgent,
		Provider: agentProtocolChatCompletions, Endpoint: model.URL,
		Model: "meta-llama/llama-3.3-70b-instruct:free", CredentialsRef: "or", Enabled: true, CreatedAt: 1})
	if _, err := srv.vault.Set("or", "or-key"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	h := srv.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/feel/generate/workers", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"openrouter"`) ||
		!strings.Contains(rec.Body.String(), `"provider":"chat-completions"`) {
		t.Fatalf("capability = %d %s", rec.Code, rec.Body)
	}

	body := `{"messages":[{"role":"user","content":"Ist beim Kunden eine E-Mail erfasst?"}],"target":"Bedingung eines Gateways"}`
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/feel/generate", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("generate = %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Expression string `json:"expression"`
		Attempts   int    `json:"attempts"`
		Worker     string `json:"worker"`
		Check      struct {
			OK      bool  `json:"ok"`
			Matches *bool `json:"matches"`
		} `json:"check"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	if got.Expression != "kunde.email != null" || got.Attempts != 2 || got.Worker != "openrouter" ||
		!got.Check.OK || got.Check.Matches == nil || !*got.Check.Matches {
		t.Fatalf("response = %+v (%s)", got, rec.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(goals) != 2 || !strings.Contains(goals[0], "Bedingung eines Gateways") ||
		!strings.Contains(goals[1], "x != null") {
		t.Errorf("what reached the model:\n%s", strings.Join(goals, "\n---\n"))
	}

	// What the request came to is logged, for whoever is improving the prompt: the
	// rounds, what each failed on, the callee it reached for — and never the
	// conversation or the expressions.
	lines := feelLines(t, sink)
	if len(lines) != 1 {
		t.Fatalf("%d feel_assistant.answered lines, want one:\n%s", len(lines), sink)
	}
	line := lines[0]
	for key, want := range map[string]any{
		"worker": "openrouter", "model": "meta-llama/llama-3.3-70b-instruct:free",
		"outcome": "settled", "attempts": float64(2), "faults": "calls,none",
		"formats": "contract,contract", "calls": "is defined", "prompt": feelgen.PromptVersion,
	} {
		if line[key] != want {
			t.Errorf("log %s = %v, want %v (line %v)", key, line[key], want, line)
		}
	}
	if errs, _ := line["errors"].(string); !strings.Contains(errs, "x != null") {
		t.Errorf("log errors = %v, want the engine's verdict", line["errors"])
	}
	for _, private := range []string{"E-Mail erfasst", "kunde.email"} {
		if strings.Contains(sink.String(), private) {
			t.Errorf("the log carries %q, from the conversation or an expression", private)
		}
	}

	// …and counted, by closed labels only (ADR-0142).
	p := `prompt="` + feelgen.PromptVersion + `"`
	for series, want := range map[string]float64{
		`atlas_feel_assistant_requests_total{outcome="settled",` + p + `}`:      1,
		`atlas_feel_assistant_requests_total{outcome="unsettled",` + p + `}`:    0,
		`atlas_feel_assistant_attempts_total{format="contract",` + p + `}`:      2,
		`atlas_feel_assistant_attempt_faults_total{fault="calls",` + p + `}`:    1,
		`atlas_feel_assistant_attempt_faults_total{fault="none",` + p + `}`:     1,
		`atlas_feel_assistant_attempt_faults_total{fault="mismatch",` + p + `}`: 0,
		`atlas_feel_assistant_request_seconds_count{` + p + `}`:                 1,
	} {
		if got := gatheredValue(t, srv, series); got != want {
			t.Errorf("%s = %v, want %v", series, got, want)
		}
	}
}

// feelLines is every feel_assistant.answered line the sink caught, decoded.
func feelLines(t *testing.T, sink *auditSink) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(sink.String()), "\n") {
		if !strings.Contains(line, `"event":"feel_assistant.answered"`) {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// gatheredValue reads one series from the server's registry, written the way the
// exposition writes it: name{label="value"}.
func gatheredValue(t *testing.T, srv *Server, series string) float64 {
	t.Helper()
	if srv.metrics == nil {
		t.Fatal("the server has no metrics registry")
	}
	families, err := srv.metrics.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range families {
		for _, m := range f.GetMetric() {
			var labels []string
			for _, l := range m.GetLabel() {
				labels = append(labels, l.GetName()+`="`+l.GetValue()+`"`)
			}
			set := ""
			if len(labels) > 0 {
				set = "{" + strings.Join(labels, ",") + "}"
			}
			switch {
			case f.GetName()+set == series && m.GetCounter() != nil:
				return m.GetCounter().GetValue()
			case f.GetName()+"_count"+set == series && m.GetHistogram() != nil:
				return float64(m.GetHistogram().GetSampleCount())
			}
		}
	}
	t.Fatalf("no series %s", series)
	return 0
}

// TestFeelAssistantOutcomeWithoutMetrics: a server with metrics turned off still logs
// what the assistant's requests came to, and counting nothing does not fail.
func TestFeelAssistantOutcomeWithoutMetrics(t *testing.T) {
	sink := captureAuditLog(t)
	srv, _ := newValidateServer(t, WithoutMetrics())
	if srv.metrics != nil {
		t.Fatal("metrics are on")
	}
	srv.observeFeelAssistant(feelgen.Outcome{Worker: "w", Model: "m", Result: feelgen.ResultUnanswered,
		Attempts: []feelgen.Attempt{{Format: feelgen.FormatUnusable, Fault: feelgen.FaultUnusable,
			Error: "model endpoint returned 429: " + strings.Repeat("—", 2000)}}})
	lines := feelLines(t, sink)
	if len(lines) != 1 || lines[0]["outcome"] != "unanswered" {
		t.Fatalf("lines = %v", lines)
	}
	// An endpoint's error body can be long; the line keeps the start of it.
	errs, _ := lines[0]["errors"].(string)
	if len(errs) > 600 || !strings.HasPrefix(errs, "model endpoint returned 429") {
		t.Errorf("errors = %d bytes: %.80s…", len(errs), errs)
	}
	// Cut between characters, never inside one: the dashes are three bytes each.
	if !utf8.ValidString(errs) || strings.ContainsRune(errs, utf8.RuneError) {
		t.Errorf("the shortened error is not valid text: %q", errs[len(errs)-12:])
	}
}

// TestAgentWorkersForFeelAreTheGenerationWorkers: the assistant and form generation
// offer the same AI Workers, because they are one configuration (ADR-0255).
func TestAgentWorkersForFeelAreTheGenerationWorkers(t *testing.T) {
	srv, _ := newValidateServer(t)
	_ = srv.connectors.Save(connector{ID: "1", Name: "haus", Kind: connectorKindAgent,
		Model: "claude-opus-5", Enabled: true, CreatedAt: 1})
	got, err := srv.agentWorkersForFeel(nil)
	if err != nil || len(got) != 1 || got[0].Name != "haus" || got[0].Model != "claude-opus-5" ||
		got[0].Provider != agentProtocolMessages {
		t.Fatalf("workers = %+v, %v", got, err)
	}

	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
	if _, err := srv.agentWorkersForFeel(nil); err == nil {
		t.Error("a broken worker store read as no workers")
	}
}
