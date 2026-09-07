package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/agent"
)

// Server wiring for form generation (ADR-0260). The area's own
// behaviour — the prompt, the outline, the gate on what came back, every status — is
// tested against the service directly in api/formgen, which is what carving it out buys
// (ADR-0147). What a service test cannot show is that the three closures this server
// supplies are really connected to the stores, the vault and the deployment registry, so
// that is what is here.

// TestAgentWorkersForGenerationListsOnlyWhatCanBeAsked: an AI Worker is an agent record
// that is switched on. A disabled one, and every other kind, is not an AI Worker.
func TestAgentWorkersForGenerationListsOnlyWhatCanBeAsked(t *testing.T) {
	srv, _ := newValidateServer(t)
	_ = srv.connectors.Save(connector{ID: "1", Name: "haus", Kind: connectorKindAgent,
		Model: "claude-opus-5", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.Save(connector{ID: "2", Name: "aus", Kind: connectorKindAgent,
		Model: "gpt-4o", Provider: agentProtocolChatCompletions, Enabled: false, CreatedAt: 2})
	_ = srv.connectors.Save(connector{ID: "3", Name: "post", Kind: connectorKindMail, Enabled: true, CreatedAt: 3})

	got, err := srv.agentWorkersForGeneration(nil)
	if err != nil {
		t.Fatalf("agentWorkersForGeneration: %v", err)
	}
	if len(got) != 1 || got[0].Name != "haus" || got[0].Model != "claude-opus-5" {
		t.Fatalf("workers = %+v, want only the enabled agent record", got)
	}
	// The wire format defaults the way the worker defaults it, so a record written
	// before the field reads as Messages rather than as nothing.
	if got[0].Provider != agentProtocolMessages {
		t.Errorf("provider = %q, want the protocol default", got[0].Provider)
	}
}

// TestAgentWorkersForGenerationStoreFailure: reading the Workers can fail, and the
// service turns that into a 500 rather than into "nothing is configured".
func TestAgentWorkersForGenerationStoreFailure(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
	if _, err := srv.agentWorkersForGeneration(nil); err == nil {
		t.Error("a broken worker store read as no workers")
	}
}

// TestDialAgentWorkerBuildsTheAdapterTheRecordNames: the record's wire format picks the
// adapter, its endpoint and model reach it, and its credential is resolved from the
// vault — the same three things agentWorkerEnv hands a supervised worker (ADR-0255).
func TestDialAgentWorkerBuildsTheAdapterTheRecordNames(t *testing.T) {
	srv, _ := newValidateServer(t)
	_ = srv.connectors.Save(connector{ID: "1", Name: "haus", Kind: connectorKindAgent,
		Endpoint: "https://gw.example/v1/messages", Model: "claude-opus-5",
		CredentialsRef: "agent-key", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.Save(connector{ID: "2", Name: "openai", Kind: connectorKindAgent,
		Provider: agentProtocolChatCompletions, Model: "gpt-4o",
		CredentialsRef: "agent-key", Enabled: true, CreatedAt: 2})
	if srv.vault == nil {
		t.Fatal("no vault; the credential could not be seeded")
	}
	if _, err := srv.vault.Set("agent-key", "s3cr3t"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}

	m, err := srv.dialAgentWorker(nil, "haus")
	if err != nil {
		t.Fatalf("dialAgentWorker: %v", err)
	}
	http1, ok := m.(*agent.HTTPModel)
	if !ok {
		t.Fatalf("model = %T, want the Messages adapter", m)
	}
	if http1.Endpoint != "https://gw.example/v1/messages" || http1.Model != "claude-opus-5" {
		t.Errorf("adapter = %+v, want the record's endpoint and model", http1)
	}
	if http1.APIKey != "s3cr3t" {
		t.Errorf("api key = %q, want the value behind the credential reference", http1.APIKey)
	}

	m2, err := srv.dialAgentWorker(nil, "openai")
	if err != nil {
		t.Fatalf("dialAgentWorker (chat-completions): %v", err)
	}
	if _, ok := m2.(*agent.ChatCompletionsModel); !ok {
		t.Errorf("model = %T, want the Chat Completions adapter", m2)
	}
}

// TestDialAgentWorkerRefusesWhatCannotBeAsked: each of these is a record an operator
// saved and a generation cannot use, and each has to say which.
func TestDialAgentWorkerRefusesWhatCannotBeAsked(t *testing.T) {
	srv, _ := newValidateServer(t)
	_ = srv.connectors.Save(connector{ID: "1", Name: "leer", Kind: connectorKindAgent,
		Enabled: true, CreatedAt: 1})
	_ = srv.connectors.Save(connector{ID: "2", Name: "modellos", Kind: connectorKindAgent,
		Provider: agentProtocolChatCompletions, Endpoint: "https://gw.example/v1/chat",
		Enabled: true, CreatedAt: 2})
	_ = srv.connectors.Save(connector{ID: "3", Name: "aus", Kind: connectorKindAgent,
		Endpoint: "https://gw.example", Enabled: false, CreatedAt: 3})

	for _, tc := range []struct{ name, want string }{
		{"leer", "credential"},
		{"modellos", "no model"},
		{"aus", "no enabled"},
		{"gibtsnicht", "no enabled"},
	} {
		if _, err := srv.dialAgentWorker(nil, tc.name); err == nil {
			t.Errorf("dialAgentWorker(%q) succeeded", tc.name)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("dialAgentWorker(%q) = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
}

// TestDialAgentWorkerStoreFailure covers the store read behind the dial.
func TestDialAgentWorkerStoreFailure(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
	if _, err := srv.dialAgentWorker(nil, "haus"); err == nil {
		t.Error("a broken worker store read as a missing worker")
	}
}

// TestProcessSourceForGenerationPrefersTheDraft: the draft is what the author is looking
// at, so a form generated for a step they added five minutes ago has to see that step.
func TestProcessSourceForGenerationPrefersTheDraft(t *testing.T) {
	srv, _ := newValidateServer(t)
	if err := srv.drafts.Save(draft{ProcessID: "urlaub", Name: "Urlaub",
		XML: `<bpmn:definitions/>`, SavedAt: 1}); err != nil {
		t.Fatalf("save draft: %v", err)
	}

	got, ok, err := srv.processSourceForGeneration(httptest.NewRequest(http.MethodPost, "/", nil), "urlaub")
	if err != nil || !ok {
		t.Fatalf("processSourceForGeneration = %v, %v, %v", got, ok, err)
	}
	if got.Origin != "draft" || got.ProcessID != "urlaub" {
		t.Errorf("source = %+v, want the draft", got)
	}
}

// TestProcessSourceForGenerationFallsBackToTheDeployment: a process with no draft is
// still a process, and the running version is what a form for it should be written
// against.
func TestProcessSourceForGenerationFallsBackToTheDeployment(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.do(func() {
		d := &deployment{Key: 7, ProcessID: "gedeployt", Name: "Gedeployt", Version: 1,
			xml: []byte(`<bpmn:definitions id="d"/>`)}
		srv.deployments[d.Key] = d
		srv.order = append(srv.order, d.Key)
		srv.versions[d.ProcessID] = d.Version
	})

	got, ok, err := srv.processSourceForGeneration(httptest.NewRequest(http.MethodPost, "/", nil), "gedeployt")
	if err != nil || !ok {
		t.Fatalf("processSourceForGeneration = %v, %v, %v", got, ok, err)
	}
	if got.Origin != "deployment" || !strings.Contains(got.XML, "definitions") {
		t.Errorf("source = %+v, want the deployed version", got)
	}

	if _, ok, err := srv.processSourceForGeneration(httptest.NewRequest(http.MethodPost, "/", nil), "nichts"); ok || err != nil {
		t.Errorf("an unknown process = %v, %v, want absent", ok, err)
	}
}

// TestGenerateAFormEndToEnd is the whole feature over the mounted routes: an operator's
// AI Worker record, a real HTTP endpoint speaking the Messages format, and a form coming
// back for the author to look at. Nothing is stored — the form store is empty afterwards,
// which is the property the whole design rests on (ADR-0032's stance applied to forms).
func TestGenerateAFormEndToEnd(t *testing.T) {
	var asked struct {
		System   string `json:"system"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&asked); err != nil {
			t.Errorf("decode model request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stop_reason":"end_turn","content":[{"type":"text","text":
			"{\"type\":\"default\",\"components\":[{\"type\":\"textfield\",\"key\":\"grund\",\"label\":\"Grund\"}]}"}]}`))
	}))
	defer model.Close()

	srv, _ := newValidateServer(t)
	_ = srv.connectors.Save(connector{ID: "1", Name: "haus", Kind: connectorKindAgent,
		Endpoint: model.URL, Model: "claude-opus-5", CredentialsRef: "k", Enabled: true, CreatedAt: 1})
	if err := srv.drafts.Save(draft{ProcessID: "urlaub", Name: "Urlaubsantrag", SavedAt: 1,
		XML: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
			<bpmn:process id="urlaub" name="Urlaubsantrag">
			  <bpmn:userTask id="Task_1" name="Antrag prüfen">
			    <bpmn:documentation>Die Führungskraft entscheidet.</bpmn:documentation>
			  </bpmn:userTask>
			</bpmn:process></bpmn:definitions>`}); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	h := srv.Handler()

	// The editor asks first whether there is anything to ask at all.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/forms/generate/workers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("capability = %d, body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"available":true`) {
		t.Fatalf("capability body = %s, want the configured Worker", rec.Body)
	}

	body := `{"description":"Ein Formular zur Prüfung des Antrags.","processId":"urlaub",` +
		`"elementId":"Task_1","formId":"urlaub-pruefen"}`
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/forms/generate", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("generate = %d, body %s", rec.Code, rec.Body)
	}
	var got struct {
		Schema map[string]any `json:"schema"`
		Worker string         `json:"worker"`
		Source string         `json:"processSource"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	if got.Worker != "haus" || got.Source != "draft" || got.Schema["id"] != "urlaub-pruefen" {
		t.Fatalf("response = %+v", got)
	}
	// The draft's own words reached the model: this is the half of the request the
	// author did not have to type.
	if len(asked.Messages) == 0 || !strings.Contains(asked.Messages[0].Content, "Führungskraft entscheidet") {
		t.Errorf("the process did not reach the model: %+v", asked)
	}
	if strings.Contains(asked.System, "running business process") {
		t.Error("the model was told it is inside a running instance; it is on an authoring screen")
	}

	// Nothing was saved. The author reviews the proposal and saves it themselves.
	var forms []form
	var listErr error
	srv.do(func() { forms, listErr = srv.forms.LoadAll() })
	if listErr != nil {
		t.Fatalf("list forms: %v", listErr)
	}
	if len(forms) != 0 {
		t.Errorf("the form store holds %d forms; a generation must store nothing", len(forms))
	}
}

// TestProcessSourceForGenerationReadFailure: the draft store can fail, and reading that
// as "no such process" would tell an author their draft is gone when a file on disk is
// merely unreadable.
func TestProcessSourceForGenerationReadFailure(t *testing.T) {
	srv, _ := newValidateServer(t)
	if err := srv.drafts.Save(draft{ProcessID: "urlaub", XML: "<bpmn:definitions/>", SavedAt: 1}); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if err := os.WriteFile(srv.drafts.FileFor("urlaub"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt the record: %v", err)
	}
	if _, _, err := srv.processSourceForGeneration(httptest.NewRequest(http.MethodPost, "/", nil), "urlaub"); err == nil {
		t.Error("an unreadable draft record read as a missing process")
	}
}
