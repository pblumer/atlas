package startfavourites

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// deployed are the process ids the fake engine knows.
var deployed = map[string]bool{"onboarding": true, "travel": true, "offboarding": true}

// newServiceOn builds a service over dir and a running loop — the loop because a
// design-time store does no locking of its own, and two tabs starring at once is the
// ordinary case. limit caps the marks one person may keep.
func newServiceOn(t *testing.T, dir string, limit int) *Service {
	t.Helper()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	t.Cleanup(func() { close(quit); wg.Wait() })
	return New(loop, store, func(id string) bool { return deployed[id] }, func() int { return limit })
}

func newService(t *testing.T) *Service { return newServiceOn(t, t.TempDir(), 100) }

// as builds a request carrying a signed-in principal, naming processID in the path
// when it is not empty.
func as(userID, method, processID string) *http.Request {
	r := httptest.NewRequest(method, "/api/v1/me/start-favourites/"+processID, nil)
	r.SetPathValue("processId", processID)
	if userID == "" {
		return r
	}
	p := &httpapi.Principal{UserID: userID, Username: userID}
	return r.WithContext(httpapi.WithPrincipal(context.Background(), p))
}

// call runs one handler and answers its status and the process ids it listed.
func call(t *testing.T, h http.HandlerFunc, r *http.Request) (int, []string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, r)
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	var got struct {
		ProcessIDs []string `json:"processIds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	if got.ProcessIDs == nil {
		t.Fatalf("body %q lists null rather than an empty list", rec.Body.String())
	}
	return rec.Code, got.ProcessIDs
}

// Nothing stored is the state every account starts in, not a miss: an empty list.
func TestReadingBeforeAnythingIsMarkedAnswersAnEmptyList(t *testing.T) {
	s := newService(t)
	code, ids := call(t, s.HandleGet, as("usr_1", "GET", ""))
	if code != http.StatusOK || len(ids) != 0 {
		t.Fatalf("GET = %d %v, want 200 []", code, ids)
	}
}

// A mark is kept in the order it was made, a second mark of the same process writes
// nothing new, and the answer is the list as it now stands.
func TestMarkingKeepsTheOrderAndDoesNotRepeat(t *testing.T) {
	s := newService(t)
	for _, id := range []string{"travel", "onboarding", "travel"} {
		if code, _ := call(t, s.HandleMark, as("usr_1", "PUT", id)); code != http.StatusOK {
			t.Fatalf("mark %s = %d", id, code)
		}
	}
	_, ids := call(t, s.HandleGet, as("usr_1", "GET", ""))
	if !reflect.DeepEqual(ids, []string{"travel", "onboarding"}) {
		t.Fatalf("marks = %v, want [travel onboarding]", ids)
	}
	code, ids := call(t, s.HandleUnmark, as("usr_1", "DELETE", "travel"))
	if code != http.StatusOK || !reflect.DeepEqual(ids, []string{"onboarding"}) {
		t.Fatalf("unmark = %d %v, want 200 [onboarding]", code, ids)
	}
}

// A star on a process nobody deployed would be a list entry nobody can ever use. A
// clear is never checked, so a mark whose process was deleted can still be taken away.
func TestOnlyADeployedProcessCanBeMarkedButAnyMarkCanBeCleared(t *testing.T) {
	s := newService(t)
	if code, _ := call(t, s.HandleMark, as("usr_1", "PUT", "no-such-process")); code != http.StatusNotFound {
		t.Fatalf("mark unknown = %d, want 404", code)
	}
	if code, _ := call(t, s.HandleMark, as("usr_1", "PUT", "offboarding")); code != http.StatusOK {
		t.Fatalf("mark = %d", code)
	}
	delete(deployed, "offboarding")
	t.Cleanup(func() { deployed["offboarding"] = true })
	code, ids := call(t, s.HandleUnmark, as("usr_1", "DELETE", "offboarding"))
	if code != http.StatusOK || len(ids) != 0 {
		t.Fatalf("unmark of an undeployed process = %d %v, want 200 []", code, ids)
	}
	// And clearing what was never marked is not an error either.
	if code, _ := call(t, s.HandleUnmark, as("usr_1", "DELETE", "travel")); code != http.StatusOK {
		t.Fatalf("unmark of an unmarked process = %d, want 200", code)
	}
}

// One person's marks are theirs: another account reads its own list and nothing else.
func TestMarksBelongToThePersonWhoMadeThem(t *testing.T) {
	s := newService(t)
	call(t, s.HandleMark, as("usr_1", "PUT", "travel"))
	if _, ids := call(t, s.HandleGet, as("usr_2", "GET", "")); len(ids) != 0 {
		t.Fatalf("usr_2 reads %v, want []", ids)
	}
}

// Without a signed-in person there is no record to address. 404 rather than 401, as
// the display preferences answer: the console reads it as "keep them in the browser".
func TestWithoutAnIdentityThereIsNoList(t *testing.T) {
	s := newService(t)
	for name, h := range map[string]http.HandlerFunc{"GET": s.HandleGet, "PUT": s.HandleMark, "DELETE": s.HandleUnmark} {
		if code, _ := call(t, h, as("", name, "travel")); code != http.StatusNotFound {
			t.Errorf("%s without identity = %d, want 404", name, code)
		}
	}
}

// The budget caps the list: a mark past it is refused and nothing is written.
func TestAMarkPastTheBudgetIsRefused(t *testing.T) {
	s := newServiceOn(t, t.TempDir(), 2)
	call(t, s.HandleMark, as("usr_1", "PUT", "travel"))
	call(t, s.HandleMark, as("usr_1", "PUT", "onboarding"))
	if code, _ := call(t, s.HandleMark, as("usr_1", "PUT", "offboarding")); code != http.StatusUnprocessableEntity {
		t.Fatalf("third mark = %d, want 422", code)
	}
	// Re-marking one already kept changes nothing and is not refused.
	if code, _ := call(t, s.HandleMark, as("usr_1", "PUT", "travel")); code != http.StatusOK {
		t.Fatalf("re-mark at the budget = %d, want 200", code)
	}
	if _, ids := call(t, s.HandleGet, as("usr_1", "GET", "")); !reflect.DeepEqual(ids, []string{"travel", "onboarding"}) {
		t.Fatalf("marks = %v, want [travel onboarding]", ids)
	}
}

// An id that could not be a process id is refused before anything is read or written.
func TestAMalformedIDIsRefused(t *testing.T) {
	s := newService(t)
	for _, id := range []string{"", strings.Repeat("x", maxProcessID+1)} {
		if code, _ := call(t, s.HandleMark, as("usr_1", "PUT", id)); code != http.StatusBadRequest {
			t.Errorf("mark %q = %d, want 400", id, code)
		}
		if code, _ := call(t, s.HandleUnmark, as("usr_1", "DELETE", id)); code != http.StatusBadRequest {
			t.Errorf("unmark %q = %d, want 400", id, code)
		}
	}
}
