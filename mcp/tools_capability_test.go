package mcp_test

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The business-architecture tools, driven the way an agent would: read the subset,
// file a capability nothing automates, file the value stream it belongs to, then ask
// what is missing.

func callText(t *testing.T, ts *httptest.Server, tool string, args map[string]any) (string, bool) {
	t.Helper()
	resps := run(t, ts, callTool(1, tool, args))
	return toolText(t, result(t, resps[0]))
}

func TestCapabilityToolsRoundTrip(t *testing.T) {
	ts := newAtlas(t)

	text, isErr := callText(t, ts, "atlas_business_architecture_subset", map[string]any{})
	if isErr || !strings.Contains(text, "keyPattern") || !strings.Contains(text, "hierarchy") {
		t.Fatalf("subset = (%q, isErr=%v)", text, isErr)
	}

	// A capability nothing realizes: the normal first row of any map.
	text, isErr = callText(t, ts, "atlas_create_capability", map[string]any{
		"key": "loan-underwriting", "name": "Loan Underwriting",
		"scope": "The credit decision. Not identity or address checks.",
		"owner": map[string]any{"name": "Head of Credit Risk", "role": "Head of Credit Risk"},
		"slas": []any{map[string]any{"name": "Decision", "metric": "cycleTime",
			"threshold": "5 business days", "scope": "internal"}},
		"tags": []any{"area:lending"},
	})
	if isErr || !strings.Contains(text, `"revision":1`) {
		t.Fatalf("create = (%q, isErr=%v)", text, isErr)
	}

	text, isErr = callText(t, ts, "atlas_get_capability", map[string]any{"key": "loan-underwriting"})
	if isErr || !strings.Contains(text, "Head of Credit Risk") {
		t.Fatalf("get = (%q, isErr=%v)", text, isErr)
	}

	// The adoption backlog is one call.
	text, isErr = callText(t, ts, "atlas_list_capabilities", map[string]any{"realized": "false"})
	if isErr || !strings.Contains(text, "loan-underwriting") {
		t.Fatalf("list = (%q, isErr=%v)", text, isErr)
	}
	text, isErr = callText(t, ts, "atlas_list_capabilities", map[string]any{"realized": "true"})
	if isErr || strings.Contains(text, "loan-underwriting") {
		t.Fatalf("realized list = (%q, isErr=%v), want the unrealized capability absent", text, isErr)
	}

	text, isErr = callText(t, ts, "atlas_capability_coverage", map[string]any{"key": "loan-underwriting"})
	if isErr || !strings.Contains(text, "not measured") {
		t.Fatalf("coverage = (%q, isErr=%v): the answer carries an SLA and must say nothing is measured",
			text, isErr)
	}

	text, isErr = callText(t, ts, "atlas_save_capability", map[string]any{
		"key": "loan-underwriting", "name": "Underwriting", "state": "active",
		"realizations": []any{map[string]any{"kind": "manual", "note": "A clerk with a scoring tool"}},
		"revision":     1,
	})
	if isErr || !strings.Contains(text, `"revision":2`) {
		t.Fatalf("save = (%q, isErr=%v)", text, isErr)
	}
	// A stale revision is refused rather than overwriting the other edit.
	if text, isErr = callText(t, ts, "atlas_save_capability", map[string]any{
		"key": "loan-underwriting", "name": "Underwriting", "revision": 1,
	}); !isErr {
		t.Fatalf("a stale save was accepted: %q", text)
	}

	text, isErr = callText(t, ts, "atlas_create_value_stream", map[string]any{
		"key": "consumer-loan", "name": "Consumer Loan",
		"stages": []any{
			map[string]any{"key": "apply", "name": "Application submission",
				"capabilities": []any{"loan-underwriting"}},
			map[string]any{"key": "disburse", "name": "Disbursement"},
		},
	})
	if isErr || !strings.Contains(text, "consumer-loan") {
		t.Fatalf("create value stream = (%q, isErr=%v)", text, isErr)
	}
	if text, isErr = callText(t, ts, "atlas_get_value_stream", map[string]any{"key": "consumer-loan"}); isErr ||
		!strings.Contains(text, "disburse") {
		t.Fatalf("get value stream = (%q, isErr=%v)", text, isErr)
	}
	if text, isErr = callText(t, ts, "atlas_list_value_streams", map[string]any{}); isErr ||
		!strings.Contains(text, "consumer-loan") {
		t.Fatalf("list value streams = (%q, isErr=%v)", text, isErr)
	}
	if text, isErr = callText(t, ts, "atlas_save_value_stream", map[string]any{
		"key": "consumer-loan", "name": "Consumer Loan", "tags": []any{"area:lending"},
	}); isErr || !strings.Contains(text, `"revision":2`) {
		t.Fatalf("save value stream = (%q, isErr=%v)", text, isErr)
	}

	// The gap report: an empty stage, and nothing else, because the capability now has
	// a manual realization Atlas cannot and does not check.
	text, isErr = callText(t, ts, "atlas_business_architecture_gaps", map[string]any{})
	if isErr {
		t.Fatalf("gaps = %q", text)
	}
	if !strings.Contains(text, "stage.empty") {
		t.Errorf("gaps did not report the stage nobody performs: %q", text)
	}
	if strings.Contains(text, `"kind":"capability.unrealized"`) {
		t.Errorf("a manually realized capability was reported as unrealized: %q", text)
	}

	if text, isErr = callText(t, ts, "atlas_delete_value_stream", map[string]any{"key": "consumer-loan"}); isErr ||
		!strings.Contains(text, "deleted value stream") {
		t.Fatalf("delete value stream = (%q, isErr=%v)", text, isErr)
	}
	// Deleting a capability says what it left dangling rather than cascading.
	if text, isErr = callText(t, ts, "atlas_delete_capability", map[string]any{"key": "loan-underwriting"}); isErr ||
		!strings.Contains(text, "loan-underwriting") {
		t.Fatalf("delete capability = (%q, isErr=%v)", text, isErr)
	}
}

// A refusal has to teach the vocabulary. An agent has nothing to grey out, so it
// learns the rules one refusal at a time unless the message carries them.
func TestCapabilityToolRefusalsCarryTheVocabulary(t *testing.T) {
	ts := newAtlas(t)
	tests := []struct {
		name, tool string
		args       map[string]any
		want       string
	}{
		{"missing key", "atlas_create_capability", map[string]any{"name": "X"}, "key"},
		{"missing name", "atlas_create_capability", map[string]any{"key": "x"}, "name"},
		{"bad key shape", "atlas_create_capability",
			map[string]any{"key": "Not A Key", "name": "X"}, "lower-case"},
		{"unknown realization kind", "atlas_create_capability",
			map[string]any{"key": "x", "name": "X",
				"realizations": []any{map[string]any{"kind": "magic"}}}, "process, worker, system, manual"},
		{"missing value stream key", "atlas_create_value_stream", map[string]any{"name": "X"}, "key"},
		{"missing value stream name", "atlas_create_value_stream", map[string]any{"key": "x"}, "name"},
		{"save without a key", "atlas_save_capability", map[string]any{"name": "X"}, "key"},
		{"save without a name", "atlas_save_capability", map[string]any{"key": "x"}, "name"},
		{"save stream without a key", "atlas_save_value_stream", map[string]any{"name": "X"}, "key"},
		{"save stream without a name", "atlas_save_value_stream", map[string]any{"key": "x"}, "name"},
		{"get without a key", "atlas_get_capability", map[string]any{}, "key"},
		{"coverage without a key", "atlas_capability_coverage", map[string]any{}, "key"},
		{"delete without a key", "atlas_delete_capability", map[string]any{}, "key"},
		{"get stream without a key", "atlas_get_value_stream", map[string]any{}, "key"},
		{"delete stream without a key", "atlas_delete_value_stream", map[string]any{}, "key"},
		{"unknown capability", "atlas_get_capability", map[string]any{"key": "ghost"}, "no such capability"},
		{"unknown stream", "atlas_get_value_stream", map[string]any{"key": "ghost"}, "no such value stream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, isErr := callText(t, ts, tt.tool, tt.args)
			if !isErr {
				t.Fatalf("%s accepted %v: %q", tt.tool, tt.args, text)
			}
			if !strings.Contains(text, tt.want) {
				t.Errorf("refusal %q does not mention %q", text, tt.want)
			}
		})
	}
}

// The findings a validation refusal carries reach the agent. An agent has no form to
// read the details out of, so a refusal that says only "the record is not valid" would
// leave it retrying blind — which is what this whole surface exists not to do.
func TestValidationFindingsReachTheAgent(t *testing.T) {
	ts := newAtlas(t)

	text, isErr := callText(t, ts, "atlas_create_capability", map[string]any{
		"key": "a", "name": "A", "state": "retired",
		"kpis": []any{map[string]any{"goal": "faster"}},
	})
	if !isErr {
		t.Fatalf("an invalid capability was accepted: %q", text)
	}
	for _, want := range []string{"state", "kpi 1", "metric"} {
		if !strings.Contains(text, want) {
			t.Errorf("refusal %q does not carry %q", text, want)
		}
	}
}

// Confirming through the tools: the act an agent has to perform deliberately, and the
// review backlog it works down.
func TestConfirmationTools(t *testing.T) {
	ts := newAtlas(t)

	if _, isErr := callText(t, ts, "atlas_create_capability", map[string]any{
		"key": "underwriting", "name": "Underwriting", "state": "active",
	}); isErr {
		t.Fatal("create failed")
	}

	text, isErr := callText(t, ts, "atlas_confirm_capability", map[string]any{
		"key": "underwriting", "with": "Head of Credit Risk", "note": "SLA unchanged",
	})
	if isErr {
		t.Fatalf("confirm = %q", text)
	}
	for _, want := range []string{"Head of Credit Risk", "SLA unchanged", `"horizonMonths"`} {
		if !strings.Contains(text, want) {
			t.Errorf("confirmation %q does not carry %q", text, want)
		}
	}

	// The note replaces rather than accumulates, and the record keeps it.
	if _, isErr := callText(t, ts, "atlas_confirm_capability", map[string]any{
		"key": "underwriting", "note": "reviewed, nothing changed",
	}); isErr {
		t.Fatal("second confirm failed")
	}
	text, _ = callText(t, ts, "atlas_get_capability", map[string]any{"key": "underwriting"})
	if strings.Contains(text, "SLA unchanged") || !strings.Contains(text, "reviewed, nothing changed") {
		t.Errorf("the record kept a log of notes rather than the last one: %q", text)
	}

	// A confirmation with nothing to say is the commonest one and must not need fields.
	if text, isErr = callText(t, ts, "atlas_confirm_capability", map[string]any{"key": "underwriting"}); isErr {
		t.Fatalf("a bare confirmation was refused: %q", text)
	}

	// The value stream twin.
	if _, isErr := callText(t, ts, "atlas_create_value_stream", map[string]any{
		"key": "consumer-loan", "name": "Consumer Loan",
	}); isErr {
		t.Fatal("create value stream failed")
	}
	if text, isErr = callText(t, ts, "atlas_confirm_value_stream", map[string]any{
		"key": "consumer-loan", "with": "SVP Consumer Loans",
	}); isErr || !strings.Contains(text, "SVP Consumer Loans") {
		t.Fatalf("confirm value stream = (%q, isErr=%v)", text, isErr)
	}

	// Both backlogs are one call each, and a map written today is in neither.
	for _, tool := range []string{"atlas_list_capabilities", "atlas_list_value_streams"} {
		text, isErr = callText(t, ts, tool, map[string]any{"stale": "true"})
		if isErr {
			t.Fatalf("%s = %q", tool, text)
		}
		if strings.TrimSpace(text) != "[]" {
			t.Errorf("%s?stale=true on a map written today = %q", tool, text)
		}
	}

	// The gap report names the horizon it applied, whether or not it found anything.
	text, isErr = callText(t, ts, "atlas_business_architecture_gaps", map[string]any{})
	if isErr || !strings.Contains(text, `"horizonMonths"`) {
		t.Errorf("gaps = (%q, isErr=%v)", text, isErr)
	}
}

func TestConfirmationToolRefusals(t *testing.T) {
	ts := newAtlas(t)
	tests := []struct {
		name, tool string
		args       map[string]any
		want       string
	}{
		{"no key", "atlas_confirm_capability", map[string]any{}, "key"},
		{"no key, value stream", "atlas_confirm_value_stream", map[string]any{}, "key"},
		{"unknown capability", "atlas_confirm_capability", map[string]any{"key": "ghost"}, "no such capability"},
		{"unknown value stream", "atlas_confirm_value_stream", map[string]any{"key": "ghost"}, "no such value stream"},
		{"a note that is a report", "atlas_confirm_capability",
			map[string]any{"key": "ghost", "note": strings.Repeat("x", 600)}, "note is longer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, isErr := callText(t, ts, tt.tool, tt.args)
			if !isErr {
				t.Fatalf("%s accepted %v: %q", tt.tool, tt.args, text)
			}
			if !strings.Contains(text, tt.want) {
				t.Errorf("refusal %q does not mention %q", text, tt.want)
			}
		})
	}
}
