package taskfolder

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/runloop"
)

// corrupt writes a record the store cannot decode, which is how these tests reach
// the read-error branches: a folder file that was truncated by a full disk, or
// hand-edited, must produce a plain 500 rather than a half-rendered sidebar.
func corrupt(t *testing.T, store *Store) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(store.Dir(), "deadbeef.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestReadErrorsAreReported covers every route that reads the store: an
// unreadable record is an error the caller is told about, not an empty answer
// that reads as "you have no folders".
func TestReadErrorsAreReported(t *testing.T) {
	svc, store := newService(t)
	kept := create(t, svc, kundenRule, "usr_me")
	corrupt(t, store)

	cases := map[string]struct {
		h      http.HandlerFunc
		method string
		body   string
		vals   map[string]string
	}{
		"list":   {svc.HandleList, http.MethodGet, "", nil},
		"counts": {svc.HandleCounts, http.MethodGet, "", nil},
		"update": {svc.HandleUpdate, http.MethodPut, kundenRule, map[string]string{"id": kept.ID}},
		"delete": {svc.HandleDelete, http.MethodDelete, "", map[string]string{"id": kept.ID}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := do(t, tc.h, as(tc.method, tc.body, "usr_me"), tc.vals)
			if name == "update" || name == "delete" {
				// These read one record by id, which still succeeds — the corrupt file
				// is a different one. They are here to prove that: a neighbouring bad
				// record must not break the folder somebody is actually working on.
				if rec.Code != http.StatusOK {
					t.Errorf("%s = %d, want 200 despite an unrelated corrupt record (%s)", name, rec.Code, rec.Body)
				}
				return
			}
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("%s = %d, want 500 (%s)", name, rec.Code, rec.Body)
			}
		})
	}

	if _, _, err := svc.Visible(User{ID: "usr_me"}); err == nil {
		t.Error("Visible swallowed a store read error")
	}
}

// TestMintFailureIsReported covers the one thing that can go wrong before a
// folder exists at all.
func TestMintFailureIsReported(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	t.Cleanup(func() { close(quit); wg.Wait() })

	svc := New(loop, store, func(User) Options { return Options{} },
		func(ms []*Matcher, _ User) ([]int, int, bool, error) { return make([]int, len(ms)), 0, false, nil },
		func() (string, error) { return "", errors.New("no entropy") })
	rec := do(t, svc.HandleCreate, as(http.MethodPost, kundenRule, "usr_me"), nil)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("create with a failing id minter = %d, want 500 (%s)", rec.Code, rec.Body)
	}
}

// TestScanFailuresAreReported covers the collaborator the server supplies: when
// the task scan fails, the count and the preview say so rather than reporting
// zero matches, which a person would read as "my folder is empty".
func TestScanFailuresAreReported(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	t.Cleanup(func() { close(quit); wg.Wait() })

	svc := New(loop, store, func(User) Options { return Options{} },
		func([]*Matcher, User) ([]int, int, bool, error) {
			return nil, 0, false, errors.New("the loop is closing")
		},
		NewID)
	create(t, svc, kundenRule, "usr_me")

	if rec := do(t, svc.HandleCounts, as(http.MethodGet, "", "usr_me"), nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("counts with a failing scan = %d, want 500 (%s)", rec.Code, rec.Body)
	}
	body := `{"rule":{"match":"all","conditions":[{"field":"process","op":"is","value":"p"}]}}`
	if rec := do(t, svc.HandlePreview, as(http.MethodPost, body, "usr_me"), nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("preview with a failing scan = %d, want 500 (%s)", rec.Code, rec.Body)
	}
	if rec := do(t, svc.HandlePreview, as(http.MethodPost, "{not json", "usr_me"), nil); rec.Code != http.StatusBadRequest {
		t.Errorf("preview with a broken body = %d, want 400", rec.Code)
	}
}

// TestStoreRefusesAnUnusableDirectory covers the construction error: a store
// whose directory cannot exist must fail at startup, where an operator sees it,
// rather than on the first save.
func TestStoreRefusesAnUnusableDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(filepath.Join(file, "folders")); err == nil {
		t.Error("NewStore accepted a directory it cannot create")
	}
}

// TestMatchSurvivesAnEvaluationError covers the fail-closed reading from the
// other side: a rule whose evaluation errors excludes the task instead of
// letting foreign work into somebody's queue.
func TestMatchSurvivesAnEvaluationError(t *testing.T) {
	m, err := Compile(Rule{Match: MatchAll, Conditions: []Condition{
		{Field: FieldLane, Op: OpUnder, Value: "Kundenservice"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// A task with no lane path at all: the membership test has nothing to look in.
	if m.Match(Task{ProcessID: "p"}, User{}, time.Now()) {
		t.Error("a lane condition matched a task that is in no lane")
	}
}

// TestVisibleToWithNoIdentity covers the server running without authentication:
// there is no owner, so there is one shared set of folders.
func TestVisibleToWithNoIdentity(t *testing.T) {
	f := Folder{Owner: "", Visibility: VisibilityPrivate}
	if !f.VisibleTo("", nil) {
		t.Error("with auth off a folder is invisible to the only identity there is")
	}
	if !f.EditableBy("") {
		t.Error("with auth off a folder cannot be edited by anybody")
	}
}
