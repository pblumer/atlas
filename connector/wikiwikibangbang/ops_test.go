package wikiwikibangbang

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/job"
)

func run(t *testing.T, f *fakeWiki, j Job) (map[string]any, error) {
	t.Helper()
	j.Connector = "wiki"
	return Run(context.Background(), j, registryOf(f.client(t)))
}

func mustRun(t *testing.T, f *fakeWiki, j Job) any {
	t.Helper()
	if j.ResultVariable == "" {
		j.ResultVariable = "out"
	}
	out, err := run(t, f, j)
	if err != nil {
		t.Fatalf("%s: %v", j.Operation, err)
	}
	return out[j.ResultVariable]
}

func asItem(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("result is a %T, not an item", v)
	}
	return m
}

// An item reaches a process as one flat object of fixed keys, never the wiki's whole
// answer, and only get-item carries the body.
func TestTheItemShape(t *testing.T) {
	f := newFakeWiki(t)
	f.add("OPS", "task", "Provision laptop", map[string]any{"source-id": "REQ-1"}, []any{map[string]any{"kind": "paragraph", "text": "Hello"}})
	got := asItem(t, mustRun(t, f, Job{Operation: "get-item", Item: "OPS-1"}))
	want := []string{"body", "id", "key", "labels", "parent", "properties", "space", "state", "title", "type", "version"}
	if keys := sortedKeys(got); !reflect.DeepEqual(keys, want) {
		t.Errorf("get-item keys = %v, want %v", keys, want)
	}
	if got["key"] != "OPS-1" || got["state"] != "open" || got["parent"] != "" {
		t.Errorf("get-item = %v", got)
	}
	updated := asItem(t, mustRun(t, f, Job{Operation: "update-item", Item: "OPS-1", Title: "Provision a laptop"}))
	if _, ok := updated["body"]; ok {
		t.Error("a write answered with the body; only get-item does")
	}
	if updated["title"] != "Provision a laptop" {
		t.Errorf("update-item = %v", updated)
	}
}

func TestCreateItem(t *testing.T) {
	f := newFakeWiki(t)
	parent := f.add("OPS", "page", "Onboarding", nil, nil)
	got := asItem(t, mustRun(t, f, Job{Operation: "create-item", Space: "OPS", ItemType: "task", Title: "Provision laptop",
		Parent: "OPS-1", Properties: map[string]any{"assignee": "anna", "due": "", "points": json.Number("3"), "unset": nil},
		Text: "Order the laptop.\nModel: X1\n\nThen install it."}))
	if got["parent"] != parent.ID {
		t.Errorf("parent = %v, want the id %s the key OPS-1 names", got["parent"], parent.ID)
	}
	created := f.find(got["id"].(string))
	if created == nil {
		t.Fatal("nothing was created")
	}
	if !reflect.DeepEqual(created.Properties, map[string]any{"assignee": "anna", "points": json.Number("3")}) {
		t.Errorf("properties = %v: a null or empty value is not sent on a create", created.Properties)
	}
	wantBody := []any{
		map[string]any{"kind": "paragraph", "text": "Order the laptop.\nModel: X1"},
		map[string]any{"kind": "paragraph", "text": "Then install it."},
	}
	if !reflect.DeepEqual(created.Body, wantBody) {
		t.Errorf("body = %v, want %v", created.Body, wantBody)
	}
}

func TestCreateItemWithBlocks(t *testing.T) {
	f := newFakeWiki(t)
	blocks := []any{map[string]any{"kind": "heading", "level": json.Number("2"), "text": "Log"}}
	got := asItem(t, mustRun(t, f, Job{Operation: "create-item", Space: "OPS", ItemType: "page", Title: "Post-mortem", Blocks: blocks}))
	if !reflect.DeepEqual(f.find(got["id"].(string)).Body, blocks) {
		t.Errorf("body = %v", f.find(got["id"].(string)).Body)
	}
}

// With matchField a create finds the item an earlier round — or anybody — created,
// instead of creating a second one.
func TestCreateItemFindsTheItemMatchFieldNames(t *testing.T) {
	f := newFakeWiki(t)
	first := f.add("OPS", "task", "Provision laptop", map[string]any{"source-id": "REQ-1"}, nil)
	got := asItem(t, mustRun(t, f, Job{Operation: "create-item", Space: "OPS", ItemType: "task", Title: "Provision laptop",
		Properties: map[string]any{"source-id": "REQ-1"}, MatchField: "source-id"}))
	if got["id"] != first.ID || len(f.items) != 1 {
		t.Errorf("got %v and %d items, want the existing item and no second", got["id"], len(f.items))
	}
	if f.count("POST /api/items") != 0 {
		t.Error("a create was sent for an item that exists")
	}
}

// Two rounds racing: the find sees nothing, the create is refused because the other
// round created the item in between, and the worker answers with that item.
func TestCreateItemLosingARaceAnswersWithTheWinner(t *testing.T) {
	f := newFakeWiki(t)
	f.pageSize = 100
	c := f.client(t)
	// The race is the find answering before the other round's create: simulate it by
	// creating the winner between the two requests.
	reg := registryOf(c)
	c.afterFind = func() { f.add("OPS", "task", "Winner", map[string]any{"source-id": "REQ-9"}, nil) }
	out, err := Run(context.Background(), Job{Connector: "wiki", Operation: "create-item", Space: "OPS", ItemType: "task", Title: "Loser",
		Properties: map[string]any{"source-id": "REQ-9"}, MatchField: "source-id", ResultVariable: "x"}, reg)
	if err != nil {
		t.Fatalf("create-item: %v", err)
	}
	if got := asItem(t, out["x"]); got["title"] != "Winner" || len(f.items) != 1 {
		t.Errorf("got %v with %d items, want the winner and nothing else", got["title"], len(f.items))
	}
}

func TestCreateItemMatchFieldMustBeSet(t *testing.T) {
	f := newFakeWiki(t)
	_, err := run(t, f, Job{Operation: "create-item", Space: "OPS", ItemType: "task", Title: "x", MatchField: "source-id"})
	if err == nil || !strings.Contains(err.Error(), "matchField") {
		t.Errorf("err = %v, want matchField refused without its property", err)
	}
	_, err = run(t, f, Job{Operation: "create-item", Space: "OPS", ItemType: "task", Title: "x",
		Properties: map[string]any{"source-id": ""}, MatchField: "source-id"})
	if err == nil || !strings.Contains(err.Error(), "matchField") {
		t.Errorf("err = %v, want an empty match value refused", err)
	}
}

func TestCreateItemMatchFieldAmbiguous(t *testing.T) {
	f := newFakeWiki(t)
	f.add("OPS", "task", "a", map[string]any{"assignee": "anna"}, nil)
	f.add("OPS", "task", "b", map[string]any{"assignee": "anna"}, nil)
	_, err := run(t, f, Job{Operation: "create-item", Space: "OPS", ItemType: "task", Title: "c",
		Properties: map[string]any{"assignee": "anna"}, MatchField: "assignee"})
	if err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Errorf("err = %v, want an ambiguous match refused", err)
	}
}

func TestCreateItemRefusals(t *testing.T) {
	f := newFakeWiki(t)
	_, err := run(t, f, Job{Operation: "create-item", Space: "NOPE", ItemType: "task", Title: "x"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 {
		t.Errorf("err = %v, want the wiki's 403", err)
	}
	_, err = run(t, f, Job{Operation: "create-item", Space: "OPS", ItemType: "page", Title: "x", Properties: map[string]any{"unknown": "1"},
		ErrorCodes: []string{CodeValidationFailed}})
	var be *job.BPMNError
	if !errors.As(err, &be) || be.Code != CodeValidationFailed || !strings.Contains(be.Message, "has no field") {
		t.Errorf("err = %#v, want the validation thrown as %s with the wiki's sentence", err, CodeValidationFailed)
	}
	_, err = run(t, f, Job{Operation: "create-item", Space: "OPS", ItemType: "page", Title: "x", Parent: "OPS-404", ErrorCodes: []string{CodeItemNotFound}})
	if !errors.As(err, &be) || be.Code != CodeItemNotFound {
		t.Errorf("err = %#v, want a missing parent thrown as %s", err, CodeItemNotFound)
	}
}

func TestGetItemNotFound(t *testing.T) {
	f := newFakeWiki(t)
	_, err := run(t, f, Job{Operation: "get-item", Item: "OPS-404", ResultVariable: "x"})
	if err == nil || !strings.Contains(err.Error(), "does not exist or may not be read") {
		t.Errorf("err = %v", err)
	}
	var be *job.BPMNError
	if errors.As(err, &be) {
		t.Error("a code the process does not catch was thrown as a BPMN error")
	}
	_, err = run(t, f, Job{Operation: "get-item", Item: "OPS-404", ResultVariable: "x", ErrorCodes: []string{CodeItemNotFound}})
	if !errors.As(err, &be) || be.Code != CodeItemNotFound {
		t.Errorf("err = %#v, want %s", err, CodeItemNotFound)
	}
}

func TestReadPage(t *testing.T) {
	f := newFakeWiki(t)
	f.add("OPS", "page", "Runbook", nil, nil)
	if got := mustRun(t, f, Job{Operation: "read-page", Item: "OPS-1"}); got != "# Runbook\n\n- **Key:** OPS-1\n" {
		t.Errorf("read-page = %q", got)
	}
}

func TestUpdateItem(t *testing.T) {
	f := newFakeWiki(t)
	it := f.add("OPS", "task", "Provision laptop", map[string]any{"due": "2026-11-01", "points": json.Number("3")}, nil)
	labels := []string{"Onboarding", "it"}
	got := asItem(t, mustRun(t, f, Job{Operation: "update-item", Item: "OPS-1", Title: "Provision a laptop",
		Properties: map[string]any{"assignee": "anna", "due": "", "points": nil}, Labels: &labels}))
	if it.Title != "Provision a laptop" {
		t.Errorf("title = %q", it.Title)
	}
	want := map[string]any{"assignee": "anna", "points": json.Number("3")}
	if !reflect.DeepEqual(it.Properties, want) {
		t.Errorf("properties = %v, want %v: \"\" unsets, null changes nothing", it.Properties, want)
	}
	if !reflect.DeepEqual(got["labels"], []any{"it", "onboarding"}) {
		t.Errorf("labels = %v", got["labels"])
	}
	if f.count("PUT /api/items/OPS-1/properties/points") != 0 {
		t.Error("a null value was sent")
	}
}

func TestUpdateItemClearsLabels(t *testing.T) {
	f := newFakeWiki(t)
	it := f.add("OPS", "task", "x", nil, nil)
	it.Labels = []string{"old"}
	none := []string{}
	mustRun(t, f, Job{Operation: "update-item", Item: "OPS-1", Labels: &none})
	if len(it.Labels) != 0 {
		t.Errorf("labels = %v, want an empty list to clear them", it.Labels)
	}
}

// An update whose every value evaluated to null changes nothing and answers the item.
func TestUpdateItemWithNothingToChange(t *testing.T) {
	f := newFakeWiki(t)
	f.add("OPS", "task", "x", nil, nil)
	got := asItem(t, mustRun(t, f, Job{Operation: "update-item", Item: "OPS-1", Properties: map[string]any{"due": nil}}))
	if got["key"] != "OPS-1" {
		t.Errorf("update-item = %v", got)
	}
}

func TestTransitionItem(t *testing.T) {
	f := newFakeWiki(t)
	it := f.add("OPS", "task", "x", nil, nil)
	got := asItem(t, mustRun(t, f, Job{Operation: "transition-item", Item: "OPS-1", Transition: "finish"}))
	if it.State != "done" || got["state"] != "done" {
		t.Fatalf("state = %q", it.State)
	}
	// Again: the wiki refuses, the item already stands where finish leads, so the task
	// succeeds — a repeated job, or a person who moved the task first.
	got = asItem(t, mustRun(t, f, Job{Operation: "transition-item", Item: "OPS-1", Transition: "finish"}))
	if got["state"] != "done" {
		t.Errorf("repeated finish = %v", got)
	}
	// A transition the item cannot take and does not stand at the end of stays refused.
	_, err := run(t, f, Job{Operation: "transition-item", Item: "OPS-1", Transition: "start", ErrorCodes: []string{CodeValidationFailed}})
	var be *job.BPMNError
	if !errors.As(err, &be) || be.Code != CodeValidationFailed || !strings.Contains(be.Message, "not allowed from state") {
		t.Errorf("err = %#v", err)
	}
	_, err = run(t, f, Job{Operation: "transition-item", Item: "OPS-1", Transition: "nosuch"})
	if err == nil || !strings.Contains(err.Error(), "no transition") {
		t.Errorf("err = %v", err)
	}
}

func TestAddComment(t *testing.T) {
	f := newFakeWiki(t)
	f.add("OPS", "task", "x", nil, nil)
	got := asItem(t, mustRun(t, f, Job{Operation: "add-comment", Item: "OPS-1", Comment: "Laptop ordered."}))
	if got["text"] != "Laptop ordered." || got["author"] != f.me.ID || got["id"] == "" {
		t.Errorf("add-comment = %v", got)
	}
	// A first delivery writes, even when the same text is already the last comment: a
	// process that says "Reminder sent." twice means it twice.
	mustRun(t, f, Job{Operation: "add-comment", Item: "OPS-1", Comment: "Laptop ordered."})
	if n := len(f.comments[f.items[0].ID]); n != 2 {
		t.Errorf("comments = %d, want 2", n)
	}
	// A redelivered job finds its own comment and does not write it again.
	got = asItem(t, mustRun(t, f, Job{Operation: "add-comment", Item: "OPS-1", Comment: "Laptop ordered.", Redelivered: true}))
	if n := len(f.comments[f.items[0].ID]); n != 2 || got["id"] != "c2" {
		t.Errorf("comments = %d (answer %v), want the redelivery to find c2", n, got["id"])
	}
	// … but writes when the last comment is somebody else's text.
	mustRun(t, f, Job{Operation: "add-comment", Item: "OPS-1", Comment: "Laptop delivered.", Redelivered: true})
	if n := len(f.comments[f.items[0].ID]); n != 3 {
		t.Errorf("comments = %d, want 3", n)
	}
}

// The last comment is found across pages.
func TestAddCommentRedeliveredReadsToTheLastPage(t *testing.T) {
	f := newFakeWiki(t)
	f.pageSize = 2
	it := f.add("OPS", "task", "x", nil, nil)
	for _, text := range []string{"a", "b", "c", "d", "e"} {
		f.comments[it.ID] = append(f.comments[it.ID], map[string]any{"id": "c-" + text, "author": f.me.ID, "created": "t", "text": text})
	}
	got := asItem(t, mustRun(t, f, Job{Operation: "add-comment", Item: "OPS-1", Comment: "e", Redelivered: true}))
	if got["id"] != "c-e" || len(f.comments[it.ID]) != 5 {
		t.Errorf("got %v with %d comments", got["id"], len(f.comments[it.ID]))
	}
}

func TestAppendToPage(t *testing.T) {
	f := newFakeWiki(t)
	unknown := map[string]any{"kind": "process", "engine": "atlas", "process": "onboarding", "view": "both", "future": json.Number("1")}
	it := f.add("OPS", "page", "Log", nil, []any{map[string]any{"kind": "heading", "level": json.Number("1"), "text": "Log"}, unknown})
	mustRun(t, f, Job{Operation: "append-to-page", Item: "OPS-1", Text: "Step 1 done."})
	want := []any{
		map[string]any{"kind": "heading", "level": json.Number("1"), "text": "Log"},
		unknown,
		map[string]any{"kind": "paragraph", "text": "Step 1 done."},
	}
	if !reflect.DeepEqual(it.Body, want) {
		t.Errorf("body = %v, want %v (every block read carried back, an unknown field included)", it.Body, want)
	}
	// A first delivery appends again, even the same text.
	mustRun(t, f, Job{Operation: "append-to-page", Item: "OPS-1", Text: "Step 1 done."})
	if len(it.Body) != 4 {
		t.Errorf("body has %d blocks, want 4", len(it.Body))
	}
	// A redelivery whose round already wrote does not write again.
	commits := f.count("PUT /api/items/OPS-1/body")
	mustRun(t, f, Job{Operation: "append-to-page", Item: "OPS-1", Text: "Step 1 done.", Redelivered: true})
	if len(it.Body) != 4 || f.count("PUT /api/items/OPS-1/body") != commits {
		t.Errorf("a redelivered append wrote again: %d blocks", len(it.Body))
	}
	// A redelivery whose round did not write does.
	mustRun(t, f, Job{Operation: "append-to-page", Item: "OPS-1", Blocks: []any{map[string]any{"kind": "divider"}}, Redelivered: true})
	if len(it.Body) != 5 {
		t.Errorf("body has %d blocks, want 5", len(it.Body))
	}
}

// The window the record names: a person's change between the read and the write is
// replaced. This test states the behaviour so a change to it is a decision, not a
// surprise.
func TestAppendToPageReplacesAChangeBetweenReadAndWrite(t *testing.T) {
	f := newFakeWiki(t)
	it := f.add("OPS", "page", "Log", nil, []any{map[string]any{"kind": "paragraph", "text": "a"}})
	f.beforeBody = func(it *fakeItem) {
		it.Body = append(it.Body, map[string]any{"kind": "paragraph", "text": "a person's line"})
		it.Version++
	}
	mustRun(t, f, Job{Operation: "append-to-page", Item: "OPS-1", Text: "b"})
	want := []any{map[string]any{"kind": "paragraph", "text": "a"}, map[string]any{"kind": "paragraph", "text": "b"}}
	if !reflect.DeepEqual(it.Body, want) {
		t.Errorf("body = %v", it.Body)
	}
}

func TestAppendToPageInALiveSessionFails(t *testing.T) {
	f := newFakeWiki(t)
	it := f.add("OPS", "page", "Log", nil, nil)
	f.busy[it.ID] = true
	_, err := run(t, f, Job{Operation: "append-to-page", Item: "OPS-1", Text: "b", ErrorCodes: []string{CodeValidationFailed}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 409 {
		t.Errorf("err = %#v, want the 409 as a failure to retry", err)
	}
}

func TestQueryItems(t *testing.T) {
	f := newFakeWiki(t)
	f.pageSize = 2
	for i := 0; i < 5; i++ {
		f.add("OPS", "task", "t", nil, []any{map[string]any{"kind": "paragraph", "text": "long"}})
	}
	f.add("OPS", "page", "p", nil, nil)
	f.items[4].State = "done"
	got, ok := mustRun(t, f, Job{Operation: "query-items", Query: "type = task AND state != done", MaxResults: 10}).([]any)
	if !ok || len(got) != 4 {
		t.Fatalf("query-items = %v, want 4 items across pages", got)
	}
	if _, ok := got[0].(map[string]any)["body"]; ok {
		t.Error("a listed item carries its body")
	}
	// More matches than maxResults fail rather than answer a short list.
	_, err := run(t, f, Job{Operation: "query-items", Query: "type = task", MaxResults: 3, ResultVariable: "x"})
	if err == nil || !strings.Contains(err.Error(), "more than 3") {
		t.Errorf("err = %v, want a query above its bound refused", err)
	}
	// An empty result is an empty list, not null.
	f.items = nil
	if got := mustRun(t, f, Job{Operation: "query-items", Query: "type = task", MaxResults: 3}); !reflect.DeepEqual(got, []any{}) {
		t.Errorf("query-items = %#v, want []", got)
	}
	_, err = run(t, f, Job{Operation: "query-items", Query: "type == task", ResultVariable: "x", ErrorCodes: []string{CodeValidationFailed}})
	var be *job.BPMNError
	if !errors.As(err, &be) || be.Code != CodeValidationFailed || !strings.Contains(be.Message, "at character 7") {
		t.Errorf("err = %#v, want the wiki's sentence about the query", err)
	}
}

// The bound holds across pages when the wiki does not say how many there are.
func TestQueryItemsStopsAtTheBoundWithoutATotal(t *testing.T) {
	f := newFakeWiki(t)
	f.pageSize = 2
	for i := 0; i < 7; i++ {
		f.add("OPS", "task", "t", nil, nil)
	}
	c := f.client(t)
	c.ignoreTotal = true
	_, err := Run(context.Background(), Job{Connector: "wiki", Operation: "query-items", Query: "type = task", MaxResults: 3, ResultVariable: "x"}, registryOf(c))
	if err == nil || !strings.Contains(err.Error(), "more than 3") {
		t.Errorf("err = %v", err)
	}
	if n := f.count("GET /api/items"); n > 2 {
		t.Errorf("read %d pages to learn that more than 3 match", n)
	}
}

// Every value is checked before the wiki is reached, so a refused job changed nothing.
func TestRunChecksBeforeItCalls(t *testing.T) {
	f := newFakeWiki(t)
	for _, j := range []Job{
		{Operation: "nosuch"},
		{Operation: "get-item", Item: "../me", ResultVariable: "x"},
		{Operation: "get-item", Item: "", ResultVariable: "x"},
		{Operation: "create-item", Space: "", ItemType: "task", Title: "x"},
		{Operation: "create-item", Space: "OPS", ItemType: "", Title: "x"},
		{Operation: "create-item", Space: "OPS", ItemType: "task", Title: " "},
		{Operation: "create-item", Space: "OPS", ItemType: "task", Title: "x", Parent: "a/b"},
		{Operation: "create-item", Space: "OPS", ItemType: "task", Title: "x", Text: "a", Blocks: []any{map[string]any{"kind": "divider"}}},
		{Operation: "create-item", Space: "OPS", ItemType: "task", Title: "x", Blocks: []any{"no block"}},
		{Operation: "create-item", Space: "OPS", ItemType: "task", Title: "x", Properties: map[string]any{"a=b": "1"}},
		{Operation: "update-item", Item: "OPS-1"},
		{Operation: "transition-item", Item: "OPS-1", Transition: " "},
		{Operation: "add-comment", Item: "OPS-1", Comment: ""},
		{Operation: "append-to-page", Item: "OPS-1"},
		{Operation: "query-items", Query: "", ResultVariable: "x"},
	} {
		if _, err := run(t, f, j); err == nil {
			t.Errorf("%+v ran", j)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for call, n := range f.calls {
		if call != "GET /api/me" && n > 0 {
			t.Errorf("%s was called %d times by jobs that should have been refused first", call, n)
		}
	}
}

func TestRunUnknownWorker(t *testing.T) {
	_, err := Run(context.Background(), Job{Connector: "nobody", Operation: "get-item", Item: "OPS-1"}, NewRegistry())
	if err == nil || !strings.Contains(err.Error(), "nobody") {
		t.Errorf("err = %v", err)
	}
}

// A write without a result variable completes with nothing.
func TestAWriteWithoutAResultVariableReturnsNothing(t *testing.T) {
	f := newFakeWiki(t)
	f.add("OPS", "task", "x", nil, nil)
	out, err := run(t, f, Job{Operation: "add-comment", Item: "OPS-1", Comment: "c"})
	if err != nil || out != nil {
		t.Errorf("out = %v, err = %v", out, err)
	}
}

func TestTextToBlocks(t *testing.T) {
	for in, want := range map[string][]any{
		"one":                   {para("one")},
		"a\nb":                  {para("a\nb")},
		"a\n\nb":                {para("a"), para("b")},
		"\n\n a \r\n\r\n\n b\n": {para("a"), para("b")},
		"a\n  \nb":              {para("a"), para("b")},
		"   ":                   nil,
	} {
		if got := textToBlocks(in); !reflect.DeepEqual(got, want) {
			t.Errorf("textToBlocks(%q) = %v, want %v", in, got, want)
		}
	}
}

func para(s string) any { return map[string]any{"kind": "paragraph", "text": s} }

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
