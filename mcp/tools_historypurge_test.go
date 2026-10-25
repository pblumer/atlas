package mcp_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// The history-purge tools (ADR-0486): an agent
// that ran a test over many records clears its runs — the preview first, then the
// delete with the process id repeated, on every server not started with
// --instance-purge=false.

// finishedRuns deploys the parking sample, starts n instances and cancels them, so the
// version holds n finished instances.
func finishedRuns(t *testing.T, ts *httptest.Server, n int) {
	t.Helper()
	if text, isErr := callText(t, ts, "atlas_deploy", map[string]any{"xml": sampleBPMN}); isErr {
		t.Fatalf("deploy = %q", text)
	}
	for i := 0; i < n; i++ {
		if text, isErr := callText(t, ts, "atlas_create_instance", map[string]any{"key": 1}); isErr {
			t.Fatalf("create = %q", text)
		}
	}
	if text, isErr := callText(t, ts, "atlas_cancel_instances", map[string]any{"key": 1}); isErr {
		t.Fatalf("cancel = %q", text)
	}
}

func TestTheHistoryPurgeToolsPreviewAndThenDelete(t *testing.T) {
	ts := newAtlas(t)
	finishedRuns(t, ts, 3)

	text, isErr := callText(t, ts, "atlas_finished_instances_deletion", map[string]any{"key": 1})
	if isErr || !strings.Contains(text, `"finished":3`) || !strings.Contains(text, `"confirm":"order"`) || !strings.Contains(text, `"enabled":true`) {
		t.Fatalf("preview = (%q, isErr=%v), want three finished, confirm \"order\", enabled", text, isErr)
	}
	if text, isErr = callText(t, ts, "atlas_delete_finished_instances", map[string]any{"key": 1, "confirm": "other"}); !isErr {
		t.Fatalf("a delete confirming another process = %q, want refused", text)
	}
	text, isErr = callText(t, ts, "atlas_delete_finished_instances", map[string]any{"key": 1, "confirm": "order", "limit": 2})
	if isErr || !strings.Contains(text, `"purged":2`) || !strings.Contains(text, `"remaining":true`) {
		t.Fatalf("first batch = (%q, isErr=%v), want two purged and more remaining", text, isErr)
	}
	cursor := between(text, `"nextCursor":"`, `"`)
	if cursor == "" {
		t.Fatalf("first batch carries no cursor: %q", text)
	}
	text, isErr = callText(t, ts, "atlas_delete_finished_instances", map[string]any{"key": 1, "confirm": "order", "after": cursor})
	if isErr || !strings.Contains(text, `"purged":1`) || !strings.Contains(text, `"remaining":false`) {
		t.Fatalf("second batch = (%q, isErr=%v), want the last one purged", text, isErr)
	}
	if text, _ = callText(t, ts, "atlas_finished_instances_deletion", map[string]any{"key": 1}); !strings.Contains(text, `"finished":0`) {
		t.Fatalf("preview after the purge = %q, want nothing left", text)
	}
	if _, isErr = callText(t, ts, "atlas_delete_finished_instances", map[string]any{"key": 1}); !isErr {
		t.Fatal("a delete without confirm: want a tool error")
	}
}

func TestTheHistoryPurgeToolIsRefusedWhereTheServerSwitchedItOff(t *testing.T) {
	ts := newAtlasWith(t, api.WithoutInstancePurge())
	finishedRuns(t, ts, 1)

	text, isErr := callText(t, ts, "atlas_finished_instances_deletion", map[string]any{"key": 1})
	if isErr || !strings.Contains(text, `"enabled":false`) || !strings.Contains(text, `"finished":1`) {
		t.Fatalf("preview = (%q, isErr=%v), want enabled false and the count", text, isErr)
	}
	text, isErr = callText(t, ts, "atlas_delete_finished_instances", map[string]any{"key": 1, "confirm": "order"})
	if !isErr || !strings.Contains(text, "--instance-purge=false") {
		t.Fatalf("delete = (%q, isErr=%v), want a refusal naming the switch", text, isErr)
	}
}

// between returns the text after the first open and before the next close, or "".
func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
