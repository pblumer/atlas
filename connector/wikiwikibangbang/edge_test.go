package wikiwikibangbang

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

func compilerRestLiteral(s string) compiler.RestExpr { return compiler.RestExpr{Literal: s} }

// The redelivery check compares blocks as the wiki returns them: a field the wiki leaves
// out when it is empty, and the mentions it derives from the marks, are no difference.
func TestEndsWithComparesAsTheWikiReturns(t *testing.T) {
	authored := []any{map[string]any{"kind": "task", "text": "Call @anna", "checked": false, "level": json.Number("0"),
		"marks": []any{map[string]any{"type": "mention", "start": json.Number("5"), "end": json.Number("10"), "ref": "i1", "href": ""}}}}
	returned := []any{
		map[string]any{"kind": "paragraph", "text": "before"},
		map[string]any{"kind": "task", "text": "Call @anna", "mentions": []any{"i1"},
			"marks": []any{map[string]any{"type": "mention", "start": 5.0, "end": json.Number("10"), "ref": "i1"}}},
	}
	if !endsWith(returned, authored) {
		t.Error("the same block as the wiki returns it was taken for another")
	}
	other := []any{map[string]any{"kind": "task", "text": "Call @anna", "checked": true}}
	if endsWith(returned, other) {
		t.Error("a checked task was taken for an unchecked one")
	}
	if endsWith(returned, nil) || endsWith(nil, authored) {
		t.Error("an empty side matched")
	}
	if canonical(func() {}) != "" {
		t.Error("a value that cannot be encoded has a canonical form")
	}
}

func TestAPIErrorSaysWhatToDo(t *testing.T) {
	for status, want := range map[int]string{
		401: "not accepted", 403: "lacks a role", 409: "tried again", 500: "answered 500",
	} {
		msg := (&APIError{Call: "c", Status: status}).Error()
		if !strings.Contains(msg, want) {
			t.Errorf("%d: %q lacks %q", status, msg, want)
		}
	}
	if msg := (&APIError{Call: "c", Status: 500, Message: "boom"}).Error(); !strings.HasSuffix(msg, ": boom") {
		t.Errorf("message = %q", msg)
	}
}

func TestWikiMessage(t *testing.T) {
	c := &Client{token: "wwbb_0123456789"}
	if got := c.wikiMessage([]byte(`<html>bad gateway</html>`)); got != "" {
		t.Errorf("a body that is no envelope gave %q", got)
	}
	long := c.wikiMessage([]byte(`{"error":"` + strings.Repeat("x", 400) + `"}`))
	if len(long) > 310 || !strings.HasSuffix(long, "…") {
		t.Errorf("a long message was not clipped: %d", len(long))
	}
	if got := c.wikiMessage([]byte(`{"error":"bad wwbb_0123456789"}`)); got != "bad [token]" {
		t.Errorf("got %q", got)
	}
	if clip(strings.Repeat("y", 100)) == strings.Repeat("y", 100) {
		t.Error("clip kept a long value")
	}
}

func TestRedirectLoopsAreRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, `{"token":"wwbb_0123456789"}`, Options{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Standing(context.Background()); err == nil || !strings.Contains(err.Error(), "too often") {
		t.Errorf("err = %v", err)
	}
}

// A wiki that answers something else than this Worker expects is named as such.
func TestAnUnexpectedAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			_, _ = w.Write([]byte(`{"admin":false,"user":{"id":"","username":"bot"}}`))
		default:
			_, _ = w.Write([]byte(`not json`))
		}
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, `{"token":"wwbb_0123456789"}`, Options{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Standing(context.Background()); err == nil || !strings.Contains(err.Error(), "names no account") {
		t.Errorf("err = %v", err)
	}
	c.me = "u1"
	c.checkedAt = c.now()
	if _, err := c.getItem(context.Background(), "OPS-1"); err == nil || !strings.Contains(err.Error(), "not what this Worker expects") {
		t.Errorf("err = %v", err)
	}
}

func TestAnUnreachableWiki(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, err := NewClient(url, `{"token":"wwbb_0123456789"}`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Standing(context.Background()); err == nil || !strings.Contains(err.Error(), "could not be reached") || strings.Contains(err.Error(), "wwbb_0123456789") {
		t.Errorf("err = %v", err)
	}
}

// An item the wiki answers without the keys an item without values lacks still has every
// key of the item a model sees.
func TestSummaryFillsEveryKey(t *testing.T) {
	got := summary(map[string]any{"id": "i1", "key": "OPS-1", "title": "t"}, true)
	for k, want := range map[string]any{"parent": "", "state": "", "version": 0} {
		if got[k] != want {
			t.Errorf("%s = %#v, want %#v", k, got[k], want)
		}
	}
	if props, ok := got["properties"].(map[string]any); !ok || len(props) != 0 {
		t.Errorf("properties = %#v", got["properties"])
	}
	if labels, ok := got["labels"].([]any); !ok || len(labels) != 0 {
		t.Errorf("labels = %#v", got["labels"])
	}
	if body, ok := got["body"].([]any); !ok || len(body) != 0 {
		t.Errorf("body = %#v", got["body"])
	}
	if c := commentOf(map[string]any{"id": "c1"}); c["text"] != "" || c["author"] != "" {
		t.Errorf("comment = %v", c)
	}
}

func TestGetItemWithoutAnItem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/me" {
			_, _ = w.Write([]byte(`{"user":{"id":"u1","username":"bot"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, `{"token":"wwbb_0123456789"}`, Options{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Job{Connector: "wiki", Operation: "get-item", Item: "OPS-1", ResultVariable: "x"}, registryOf(c)); err == nil || !strings.Contains(err.Error(), "holds no item") {
		t.Errorf("err = %v", err)
	}
}

// Failures part-way are reported, not hidden behind a later success.
func TestFailuresPartWay(t *testing.T) {
	f := newFakeWiki(t)
	f.add("OPS", "page", "p", nil, nil)
	// A page has no properties: the wiki refuses the second step.
	_, err := run(t, f, Job{Operation: "update-item", Item: "OPS-1", Title: "new", Properties: map[string]any{"due": "2026-11-01"}})
	if err == nil || !strings.Contains(err.Error(), "has no field") {
		t.Errorf("err = %v", err)
	}
	if f.items[0].Title != "new" {
		t.Error("the step before the refusal was not made; the record says it is, and the retry sets it again")
	}
	// Labels on an item that does not exist.
	none := []string{}
	if _, err := run(t, f, Job{Operation: "update-item", Item: "OPS-9", Labels: &none}); err == nil {
		t.Error("labels on a missing item succeeded")
	}
	// A transition on a page: refused, and the page has no workflow to stand at the end of.
	if _, err := run(t, f, Job{Operation: "transition-item", Item: "OPS-1", Transition: "finish"}); err == nil {
		t.Error("a page was moved")
	}
	// A transition on an item that does not exist.
	if _, err := run(t, f, Job{Operation: "transition-item", Item: "OPS-9", Transition: "finish"}); err == nil {
		t.Error("a missing item was moved")
	}
	// Comments on an item that does not exist, first and redelivered.
	for _, again := range []bool{false, true} {
		if _, err := run(t, f, Job{Operation: "add-comment", Item: "OPS-9", Comment: "x", Redelivered: again}); err == nil {
			t.Error("a comment on a missing item succeeded")
		}
	}
	// A create under a parent the wiki cannot find, and a find that fails.
	if _, err := run(t, f, Job{Operation: "create-item", Space: "OPS", ItemType: "page", Title: "x", Parent: "OPS-9"}); err == nil {
		t.Error("a create under a missing parent succeeded")
	}
	if _, err := run(t, f, Job{Operation: "append-to-page", Item: "OPS-9", Text: "x"}); err == nil {
		t.Error("an append to a missing page succeeded")
	}
	if _, err := run(t, f, Job{Operation: "read-page", Item: "OPS-9", ResultVariable: "x"}); err == nil {
		t.Error("a missing page was read")
	}
}

// A create refused while matchField is set, for a reason that is not the race, stays
// refused.
func TestCreateRefusedForAnotherReasonStaysRefused(t *testing.T) {
	f := newFakeWiki(t)
	_, err := run(t, f, Job{Operation: "create-item", Space: "OPS", ItemType: "page", Title: "x",
		Properties: map[string]any{"source-id": "REQ-1"}, MatchField: "source-id"})
	if err == nil || !strings.Contains(err.Error(), "has no field") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveEdges(t *testing.T) {
	// No element instance known: processInstanceKey falls back to the scope it was asked
	// for, which is what the compile-time tests of a bare store see.
	cp, d := compileTask(t, `connector="wiki" operation="add-comment" item="OPS-1" comment="=string(processInstanceKey)"`)
	j, err := Resolve(fakeVarStore{}, cp, d, 7)
	if err != nil || j.Comment != "7" {
		t.Errorf("comment = %q, err = %v", j.Comment, err)
	}
	// Variables of every kind bind.
	cp, d = compileTask(t, `connector="wiki" operation="add-comment" item="OPS-1" comment="=if ok then string(n + 1) else &quot;no&quot;"`)
	j, err = Resolve(vars(
		model.VariableValue{Name: "ok", Kind: model.VarBool, Bool: true},
		model.VariableValue{Name: "n", Kind: model.VarNumber, Text: "41"},
	), cp, d, 10)
	if err != nil || j.Comment != "42" {
		t.Errorf("comment = %q, err = %v", j.Comment, err)
	}
	if toExprKind(model.VarKind(99)) != expr.KindNull {
		t.Error("an unknown kind is not null")
	}
	if _, err := decodeObject(`[1]`, "x"); err == nil {
		t.Error("a list was taken for an object")
	}
	if blocks, err := resolveBlocks(compilerRestLiteral(" "), 10, nil); blocks != nil || err != nil {
		t.Errorf("a blank literal gave %v, %v", blocks, err)
	}
	if _, err := resolveBlocks(compilerRestLiteral("{"), 10, nil); err == nil {
		t.Error("a broken literal was taken for blocks")
	}
	// Labels that are an object are refused.
	cp, d = compileTask(t, `connector="wiki" operation="update-item" item="OPS-1" labels="=obj"`)
	if _, err := Resolve(vars(model.VariableValue{Name: "obj", Kind: model.VarJSON, Text: `{"a":1}`}), cp, d, 10); err == nil {
		t.Error("labels that are an object were accepted")
	}
}

func TestRunMatchFieldNotInTheEvaluatedProperties(t *testing.T) {
	f := newFakeWiki(t)
	_, err := run(t, f, Job{Operation: "create-item", Space: "OPS", ItemType: "task", Title: "x",
		Properties: map[string]any{"due": "2026-11-01"}, MatchField: "a=b"})
	if err == nil {
		t.Error("an unaddressable matchField ran")
	}
	var nf *notFound
	if errors.As(err, &nf) {
		t.Error("a refused value was reported as a missing item")
	}
}
