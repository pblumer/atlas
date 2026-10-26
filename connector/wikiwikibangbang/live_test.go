package wikiwikibangbang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/job"
)

// TestAgainstARealWiki walks every operation against a wikiwikibangbang wiki, which no
// other test here reaches. It is skipped unless one is named:
//
//	ATLAS_TEST_WWBB_URL        the wiki's base URL (http only on a loopback host)
//	ATLAS_TEST_WWBB_TOKEN      an API token of scope full of an account that holds no
//	                           system role and is an editor in the space
//	ATLAS_TEST_WWBB_SPACE      the space it writes to
//	ATLAS_TEST_WWBB_TASK_TYPE  a type with a workflow whose transitions include finish,
//	                           and a text property source-id the type declares unique
//	ATLAS_TEST_WWBB_PAGE_TYPE  a type with a body
//
// It writes into the space and leaves what it wrote there — a task and a page, both
// titled with the run's time — because this Worker Type deletes nothing.
func TestAgainstARealWiki(t *testing.T) {
	base := os.Getenv("ATLAS_TEST_WWBB_URL")
	if base == "" {
		t.Skip("set ATLAS_TEST_WWBB_URL, ATLAS_TEST_WWBB_TOKEN, ATLAS_TEST_WWBB_SPACE, ATLAS_TEST_WWBB_TASK_TYPE and ATLAS_TEST_WWBB_PAGE_TYPE to run against a real wiki")
	}
	space, taskType, pageType := os.Getenv("ATLAS_TEST_WWBB_SPACE"), os.Getenv("ATLAS_TEST_WWBB_TASK_TYPE"), os.Getenv("ATLAS_TEST_WWBB_PAGE_TYPE")
	c, err := NewClient(base, `{"token":"`+os.Getenv("ATLAS_TEST_WWBB_TOKEN")+`"}`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register("wiki", c)
	ctx := context.Background()
	runID := time.Now().UTC().Format("20060102T150405.000")
	do := func(j Job) any {
		t.Helper()
		j.Connector, j.ResultVariable = "wiki", "out"
		j.ErrorCodes = Codes
		out, err := Run(ctx, j, reg)
		if err != nil {
			t.Fatalf("%s: %v", j.Operation, err)
		}
		return out["out"]
	}
	item := func(v any) map[string]any { return v.(map[string]any) }

	// A page, then a task below it, found again by its source-id.
	page := item(do(Job{Operation: "create-item", Space: space, ItemType: pageType, Title: "Atlas live test " + runID,
		Text: "Created by TestAgainstARealWiki.\nSecond line.\n\nSecond paragraph."}))
	task := item(do(Job{Operation: "create-item", Space: space, ItemType: taskType, Title: "Atlas live task " + runID,
		Parent: fmt.Sprint(page["key"]), Properties: map[string]any{"source-id": "atlas-" + runID}, MatchField: "source-id"}))
	if task["parent"] != page["id"] {
		t.Errorf("the task's parent = %v, want the page's id %v", task["parent"], page["id"])
	}
	again := item(do(Job{Operation: "create-item", Space: space, ItemType: taskType, Title: "Atlas live task " + runID + " (repeated)",
		Properties: map[string]any{"source-id": "atlas-" + runID}, MatchField: "source-id"}))
	if again["id"] != task["id"] {
		t.Errorf("a repeated create with matchField made %v, want the first task %v", again["id"], task["id"])
	}

	// Read, update, move twice, comment twice (once delivered again).
	got := item(do(Job{Operation: "get-item", Item: fmt.Sprint(task["id"])}))
	if got["key"] != task["key"] {
		t.Errorf("get-item = %v", got)
	}
	labels := []string{"atlas", "Live Test"}
	updated := item(do(Job{Operation: "update-item", Item: fmt.Sprint(task["key"]), Title: "Atlas live task " + runID + " (updated)", Labels: &labels}))
	if !strings.HasSuffix(fmt.Sprint(updated["title"]), "(updated)") || len(updated["labels"].([]any)) != 2 {
		t.Errorf("update-item = %v", updated)
	}
	moved := item(do(Job{Operation: "transition-item", Item: fmt.Sprint(task["id"]), Transition: "finish"}))
	movedAgain := item(do(Job{Operation: "transition-item", Item: fmt.Sprint(task["id"]), Transition: "finish"}))
	if moved["state"] != movedAgain["state"] {
		t.Errorf("a repeated finish left state %v, then %v", moved["state"], movedAgain["state"])
	}
	first := item(do(Job{Operation: "add-comment", Item: fmt.Sprint(task["id"]), Comment: "Done " + runID}))
	second := item(do(Job{Operation: "add-comment", Item: fmt.Sprint(task["id"]), Comment: "Done " + runID, Redelivered: true}))
	if first["id"] != second["id"] {
		t.Errorf("a redelivered comment wrote a second one: %v, %v", first["id"], second["id"])
	}

	// Append twice, the second delivered again; then read the page both ways.
	do(Job{Operation: "append-to-page", Item: fmt.Sprint(page["id"]), Blocks: []any{map[string]any{"kind": "heading", "level": 2, "text": "Log"}}})
	do(Job{Operation: "append-to-page", Item: fmt.Sprint(page["id"]), Blocks: []any{map[string]any{"kind": "heading", "level": 2, "text": "Log"}}, Redelivered: true})
	read := item(do(Job{Operation: "get-item", Item: fmt.Sprint(page["id"])}))
	if body := read["body"].([]any); len(body) != 3 {
		t.Errorf("the page has %d blocks, want two paragraphs and one heading: %v", len(body), body)
	}
	md := do(Job{Operation: "read-page", Item: fmt.Sprint(page["id"])})
	if !strings.Contains(fmt.Sprint(md), "## Log") || !strings.Contains(fmt.Sprint(md), "Second paragraph.") {
		t.Errorf("read-page = %q", md)
	}

	// Query, and the two codes.
	found := do(Job{Operation: "query-items", Query: fmt.Sprintf("type = %s AND title ~ %q", taskType, runID), MaxResults: 10}).([]any)
	if len(found) != 1 {
		t.Errorf("query-items found %d, want the one task", len(found))
	}
	var be *job.BPMNError
	_, err = Run(ctx, Job{Connector: "wiki", Operation: "get-item", Item: space + "-999999", ResultVariable: "x", ErrorCodes: Codes}, reg)
	if !errors.As(err, &be) || be.Code != CodeItemNotFound {
		t.Errorf("a missing item: %v", err)
	}
	_, err = Run(ctx, Job{Connector: "wiki", Operation: "transition-item", Item: fmt.Sprint(task["id"]), Transition: "no-such-transition", ErrorCodes: Codes}, reg)
	if !errors.As(err, &be) || be.Code != CodeValidationFailed {
		t.Errorf("an unknown transition: %v", err)
	}
	t.Logf("wrote page %v and task %v", page["key"], task["key"])
}
