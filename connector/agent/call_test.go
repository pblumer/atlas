package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func cannedEndpoint(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

var oneTool = []Tool{{Name: "lookup", Params: []Param{{Name: "id", Type: "string"}}}}

// The Messages format reports the uncached, cache-written and cache-read input apart.
// The account sums them, because a budget over a figure that silently leaves out the
// cached part would not be a budget (ADR-0566).
func TestMessagesAccountOfACall(t *testing.T) {
	m := &HTTPModel{APIKey: "k", Endpoint: cannedEndpoint(t, `{
	  "model": "claude-opus-5-5", "stop_reason": "tool_use",
	  "usage": {"input_tokens": 40, "output_tokens": 25, "cache_creation_input_tokens": 100, "cache_read_input_tokens": 900},
	  "content": [{"type": "text", "text": " Ich schlage nach. "},
	              {"type": "tool_use", "id": "toolu_1", "name": "lookup", "input": {"id": "7"}}]}`)}
	d, err := m.Decide(context.Background(), Request{Goal: "prüfe", Tools: oneTool, Round: 1})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	c := d.Call
	if c.Model != "claude-opus-5-5" || c.StopReason != "tool_use" {
		t.Errorf("call = %+v, want the model and the stop reason the provider named", c)
	}
	if want := (Usage{InputTokens: 1040, OutputTokens: 25, CacheReadTokens: 900, CacheWriteTokens: 100}); c.Usage != want {
		t.Errorf("usage = %+v, want %+v", c.Usage, want)
	}
	if c.Answer != "Ich schlage nach." || c.System != roundSystemPrompt || c.Prompt != roundPrompt(Request{Goal: "prüfe", Tools: oneTool, Round: 1}) {
		t.Errorf("texts = %q / %q / %q, want what went each way", c.Answer, c.System, c.Prompt)
	}
	if len(d.ToolCalls) != 1 {
		t.Errorf("tool calls = %+v", d.ToolCalls)
	}
}

// Chat Completions counts the cached part inside prompt_tokens already.
func TestChatCompletionsAccountOfACall(t *testing.T) {
	m := &ChatCompletionsModel{APIKey: "k", Model: "gpt", Endpoint: cannedEndpoint(t, `{
	  "model": "gpt-x", "usage": {"prompt_tokens": 500, "completion_tokens": 30, "prompt_tokens_details": {"cached_tokens": 400}},
	  "choices": [{"finish_reason": "stop", "message": {"content": "tragbar"}}]}`)}
	d, err := m.Decide(context.Background(), Request{Goal: "Ist das tragbar?", Round: 1})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	c := d.Call
	if want := (Usage{InputTokens: 500, OutputTokens: 30, CacheReadTokens: 400}); c.Usage != want || c.Model != "gpt-x" || c.StopReason != "stop" {
		t.Errorf("call = %+v, want usage %+v from gpt-x, stopped", c, want)
	}
	if c.Answer != "tragbar" || c.System != taskSystemPrompt || c.Prompt != "Ist das tragbar?" {
		t.Errorf("texts = %q / %q / %q", c.Answer, c.System, c.Prompt)
	}
}

// A call's authored bound travels beside the request in whole milliseconds and comes
// back out on the worker's side, for a round and for an ai task alike.
func TestACallsTimeoutTravelsInThePayload(t *testing.T) {
	r, err := RoundFromPayload(ResolveJobPayload(Round{Request: Request{Goal: "g", Tools: oneTool, Round: 1, Timeout: 90 * time.Second}}))
	if err != nil || r.Timeout != 90*time.Second {
		t.Errorf("round timeout = %v, %v, want 90s", r.Timeout, err)
	}
	task, err := TaskFromPayload(TaskJobPayload(Task{Prompt: "p", ResultVariable: "v", TimeoutMs: 20000}))
	if err != nil || TaskRequest(task).Timeout != 20*time.Second {
		t.Errorf("task timeout = %v, %v, want 20s", TaskRequest(task).Timeout, err)
	}
	// Over JSON a number is a float64, and no bound at all is zero.
	if got := timeoutFromPayload(float64(1500)); got != 1500*time.Millisecond {
		t.Errorf("from JSON = %v", got)
	}
	if got := timeoutFromPayload(int(7)); got != 7*time.Millisecond {
		t.Errorf("from an int = %v", got)
	}
	if got := timeoutFromPayload(nil); got != 0 {
		t.Errorf("unstated = %v, want 0", got)
	}
	if got := callTimeout(time.Minute, Request{}); got != time.Minute {
		t.Errorf("unstated bound = %v, want the adapter's own", got)
	}
	if got := callTimeout(time.Minute, Request{Timeout: time.Second}); got != time.Second {
		t.Errorf("stated bound = %v, want the model's", got)
	}
}
