package api

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The small decisions behind the completed-task routes (ADR-0554): who answered, which
// revision a completion is recorded with, which definitions a reach admits, and how a
// cursor and a bound are read.
func TestCompletedTaskHelpers(t *testing.T) {
	if got := completerOf(httptest.NewRequest("GET", "/", nil)); got != "" {
		t.Errorf("completer without a principal = %q", got)
	}
	for body, want := range map[string]string{
		"":                                "",
		"not json":                        "",
		`{"formRevision":" sha256:abc "}`: "sha256:abc",
		`{"variables":{"a":1}}`:           "",
	} {
		if got := offeredFormRevision([]byte(body)); got != want {
			t.Errorf("offeredFormRevision(%q) = %q, want %q", body, got, want)
		}
	}

	b := compiler.NewBuilder(1, "p", 1)
	start := b.AddStartEvent()
	task := b.AddUserTask("T", compiler.Assignment{}, compiler.Assignment{}, "the-form", 0, 0, 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	for el, want := range map[int32]string{task: "the-form", start: "", -1: "", 999: ""} {
		if got := userTaskFormID(cp, el); got != want {
			t.Errorf("userTaskFormID(%d) = %q, want %q", el, got, want)
		}
	}

	defs := defIndex{1: {ProcessID: "p", ProjectID: "app"}, 2: {ProcessID: "q"}}
	for _, c := range []struct {
		reach []string
		def   uint64
		want  bool
	}{
		{nil, 9, true}, {[]string{"app"}, 1, true}, {[]string{"other"}, 1, false},
		{[]string{"app"}, 2, false}, {[]string{"app"}, 9, false},
	} {
		if got := inReach(c.reach, defs, c.def); got != c.want {
			t.Errorf("inReach(%v, %d) = %v, want %v", c.reach, c.def, got, c.want)
		}
	}

	// A definition this server no longer holds still names the task by its keys.
	row := completedTaskOf(state.TaskAnswer{At: 1, Value: model.TaskAnswerValue{JobKey: 5, ProcessInstanceKey: 6, ProcessDefKey: 9}}, defs)
	if row.Key != "5" || row.ProcessDefKey != "9" || row.ProcessID != "" || row.Name != "" {
		t.Errorf("row of an unknown definition = %+v", row)
	}

	for _, raw := range []string{"x", "1.", ".1", "1.0", "a.1"} {
		if _, ok := parseCompletedCursor(raw); ok {
			t.Errorf("cursor %q was accepted", raw)
		}
	}
	if c, ok := parseCompletedCursor("7.8"); !ok || c.At != 7 || c.JobKey != 8 {
		t.Errorf("cursor 7.8 = %+v, %v", c, ok)
	}
	if n, err := parseInstant("2026-11-11"); err != nil || n == 0 {
		t.Errorf("a date = %d, %v", n, err)
	}
	if _, err := parseInstant("gestern"); err == nil {
		t.Error("a word was read as an instant")
	}
	if _, err := parseInstant("2999-01-01"); err == nil {
		t.Error("an instant past what nanoseconds hold was read")
	}
}

// answeredRevisionOnLoop records the revision the browser rendered when it is kept,
// the current schema otherwise, and nothing for a task without a form or a form that
// is gone (ADR-0554 §2).
func TestTheAnsweredRevision(t *testing.T) {
	s := &Server{forms: newForms(t)}
	old := form{ID: "f", Name: "F", SavedAt: 1, Schema: `{"components":[{"key":"a"}]}`}
	cur := form{ID: "f", Name: "F", SavedAt: 2, Schema: `{"components":[{"key":"b"}]}`}
	for _, f := range []form{old, cur} {
		if err := s.forms.Save(f); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.answeredRevisionOnLoop("", "sha256:x"); got != "" {
		t.Errorf("no form = %q", got)
	}
	if got := s.answeredRevisionOnLoop("f", formDigest(old.Schema)); got != formDigest(old.Schema) {
		t.Errorf("a kept revision offered = %q", got)
	}
	if got := s.answeredRevisionOnLoop("f", "sha256:unknown"); got != formDigest(cur.Schema) {
		t.Errorf("an unknown revision offered = %q, want the current one", got)
	}
	if got := s.answeredRevisionOnLoop("gone", ""); got != "" {
		t.Errorf("a missing form = %q", got)
	}
}

// taskFormIDOnLoop names no form for a job whose element instance is gone or whose
// definition this server no longer holds, and the form a held user task binds.
func TestTheTaskFormIDOfAJob(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b := compiler.NewBuilder(7, "p", 1)
	start := b.AddStartEvent()
	task := b.AddUserTask("T", compiler.Assignment{}, compiler.Assignment{}, "the-form", 0, 0, 3)
	b.Connect(start, task)
	cp, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	tx := st.NewTransaction()
	for key, def := range map[uint64]uint64{100: 7, 101: 8} {
		if err := tx.PutElementInstance(key, &model.ElementInstanceValue{ProcessDefKey: def, ElementId: task}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx.Close()
	s := &Server{store: st, deployments: map[uint64]*deployment{7: {Key: 7, cp: cp}}}
	for el, want := range map[uint64]string{100: "the-form", 101: "", 999: ""} {
		if got := s.taskFormIDOnLoop(&model.JobValue{ElementInstanceKey: el}); got != want {
			t.Errorf("element %d: form %q, want %q", el, got, want)
		}
	}
}

// A revision store that cannot be read records no revision and refuses nothing: the
// task is then shown without its form. Opening over a file and deleting with an
// unreadable store report the fault.
func TestABrokenRevisionStore(t *testing.T) {
	s := &Server{forms: newForms(t)}
	if err := s.forms.Save(form{ID: "f", Name: "F", Schema: `{"components":[]}`}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.forms.revisions.Dir()); err != nil {
		t.Fatal(err)
	}
	if got := s.answeredRevisionOnLoop("f", ""); got != "" {
		t.Errorf("revision with a broken store = %q", got)
	}
	if err := s.forms.Delete("f"); err == nil {
		t.Error("deleting with an unreadable revision store reported nothing")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "form-revisions"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newFormStore(filepath.Join(dir, "forms")); err == nil {
		t.Error("a revision directory that is a file was opened")
	}
}
