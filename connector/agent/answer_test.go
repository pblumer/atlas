package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/model"
)

var verdict = []Output{
	{Name: "entscheid", Type: "string", Required: true, Values: []string{"ja", "nein"}, Description: "Ob es tragbar ist"},
	{Name: "quote", Type: "number"},
	{Name: "geprueft", Type: "boolean"},
}

// An answer is held to its declared fields here, whatever the provider promised, and
// comes back in the canonical form a gateway reads (ADR-draft-agent-transcript-and-answer).
func TestAStructuredAnswerIsCheckedAgainstItsFields(t *testing.T) {
	for name, tc := range map[string]struct{ text, want, wantErr string }{
		"all fields":      {`{"quote": 0.33, "entscheid": "ja", "geprueft": true}`, `{"entscheid":"ja","geprueft":true,"quote":0.33}`, ""},
		"optional absent": {`{"entscheid":"nein"}`, `{"entscheid":"nein"}`, ""},
		"optional null":   {`{"entscheid":"nein","quote":null}`, `{"entscheid":"nein"}`, ""},
		"fenced":          {"```json\n{\"entscheid\":\"ja\"}\n```", `{"entscheid":"ja"}`, ""},
		"exact decimal":   {`{"entscheid":"ja","quote":0.10000000000000000001}`, `{"entscheid":"ja","quote":0.10000000000000000001}`, ""},
		"prose":           {`Ja, das ist tragbar.`, "", "not a JSON object"},
		"trailing text":   {`{"entscheid":"ja"} Hoffe das hilft.`, "", "text after its JSON object"},
		"required absent": {`{"quote":1}`, "", `lacks the required field "entscheid"`},
		"not a value":     {`{"entscheid":"vielleicht"}`, "", `not one of ja, nein`},
		"wrong type":      {`{"entscheid":"ja","quote":"viel"}`, "", `"quote" is not a number`},
		"wrong bool":      {`{"entscheid":"ja","geprueft":"ja"}`, "", `"geprueft" is not a boolean`},
		"wrong string":    {`{"entscheid":true}`, "", `"entscheid" is not a string`},
		"undeclared":      {`{"entscheid":"ja","bemerkung":"x"}`, "", `"bemerkung", which is not declared`},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := structuredAnswer(verdict, tc.text)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want one naming %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("answer = %s, %v, want %s", got, err, tc.want)
			}
		})
	}
}

// recordingEndpoint answers with body and keeps what it was sent.
func recordingEndpoint(t *testing.T, body string, sent *map[string]any) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, sent)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// Both formats are asked for the declared object in their own way, told about it in
// words as well, and the answer lands as one JSON variable — or fails the call.
func TestBothFormatsAskForAndCheckTheDeclaredAnswer(t *testing.T) {
	var sent map[string]any
	m := &HTTPModel{APIKey: "k", Endpoint: recordingEndpoint(t,
		`{"stop_reason":"end_turn","content":[{"type":"text","text":"{\"entscheid\":\"ja\",\"quote\":0.3}"}]}`, &sent)}
	d, err := m.Decide(context.Background(), Request{Goal: "Tragbar?", Round: 1, Outputs: verdict})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(d.Outputs) != 1 || d.Outputs[0].Kind != model.VarJSON || d.Outputs[0].Text != `{"entscheid":"ja","quote":0.3}` {
		t.Errorf("answer = %+v, want one JSON variable", d.Outputs)
	}
	format, _ := sent["output_config"].(map[string]any)["format"].(map[string]any)
	schema, _ := format["schema"].(map[string]any)
	if format["type"] != "json_schema" || schema["additionalProperties"] != false ||
		len(schema["required"].([]any)) != 1 || schema["properties"].(map[string]any)["entscheid"].(map[string]any)["enum"] == nil {
		t.Errorf("output_config = %v, want the closed schema of the declared fields", sent["output_config"])
	}
	if sys, _ := sent["system"].(string); !strings.Contains(sys, "- entscheid (string, required), one of: ja, nein — Ob es tragbar ist") {
		t.Errorf("system prompt does not state the fields: %q", sys)
	}

	sent = nil
	c := &ChatCompletionsModel{APIKey: "k", Model: "gpt", Endpoint: recordingEndpoint(t,
		`{"choices":[{"finish_reason":"stop","message":{"content":"Ja, tragbar."}}]}`, &sent)}
	if _, err := c.Decide(context.Background(), Request{Goal: "Tragbar?", Round: 1, Outputs: verdict}); err == nil ||
		!strings.Contains(err.Error(), "not a JSON object") {
		t.Errorf("a prose answer to a declared object: %v, want the call refused", err)
	}
	js, _ := sent["response_format"].(map[string]any)["json_schema"].(map[string]any)
	if js["strict"] != false || js["name"] != "answer" {
		t.Errorf("response_format = %v, want the schema as guidance while a field is optional", sent["response_format"])
	}
	if _, strict := answerSchema(verdict[:1]); !strict {
		t.Error("a schema whose every field is required can be enforced exactly")
	}

	// And an element that declares nothing sends nothing new.
	sent = nil
	plain := &HTTPModel{APIKey: "k", Endpoint: recordingEndpoint(t, `{"content":[{"type":"text","text":"ok"}]}`, &sent)}
	if _, err := plain.Decide(context.Background(), Request{Goal: "g", Round: 1}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if _, has := sent["output_config"]; has {
		t.Error("a request without declared fields carries an output_config")
	}
}

// The fields travel to the worker with a round and with an ai task.
func TestAnswerFieldsTravelInThePayload(t *testing.T) {
	over := func(m map[string]any) map[string]any { // as a worker receives it
		raw, _ := json.Marshal(m)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out
	}
	r, err := RoundFromPayload(over(ResolveJobPayload(Round{Request: Request{Goal: "g", Tools: oneTool, Round: 1, Outputs: verdict}})))
	if err != nil || len(r.Outputs) != 3 || r.Outputs[0].Values[1] != "nein" || !r.Outputs[0].Required {
		t.Errorf("round outputs = %+v, %v", r.Outputs, err)
	}
	task, err := TaskFromPayload(TaskJobPayload(Task{Prompt: "p", ResultVariable: "v", Outputs: verdict}))
	if err != nil || len(TaskRequest(task).Outputs) != 3 {
		t.Errorf("task outputs = %+v, %v", task.Outputs, err)
	}
	if got := outputsFromPayload("nonsense"); got != nil {
		t.Errorf("a payload of the wrong shape = %+v, want none", got)
	}
}

// The same facts are put in the same order on every round.
func TestTheContextIsPutInNameOrder(t *testing.T) {
	req := Request{Goal: "g", Tools: oneTool, Round: 1, Context: map[string]string{"zins": "1.5", "dossier": "D", "kunde": "K", "alter": "40"}}
	want := "- alter: 40\n- dossier: D\n- kunde: K\n- zins: 1.5"
	for i := 0; i < 20; i++ {
		if got := roundPrompt(req); !strings.Contains(got, want) {
			t.Fatalf("prompt = %q, want the context in name order", got)
		}
	}
}
