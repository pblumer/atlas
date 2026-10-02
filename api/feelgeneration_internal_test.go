package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Server wiring for the FEEL assistant (ADR-draft-feel-assistant). The area's behaviour —
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
