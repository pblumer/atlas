package userprefs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// newService builds a service over a temporary store and a running loop — the
// loop because a design-time store does no locking of its own, and two people
// saving at once is the ordinary case for a per-person setting.
func newService(t *testing.T) *Service {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	t.Cleanup(func() { close(quit); wg.Wait() })
	return New(loop, store)
}

// as builds a request carrying a signed-in principal.
func as(userID, method, body string) *http.Request {
	r := httptest.NewRequest(method, "/api/v1/me/preferences", strings.NewReader(body))
	p := &httpapi.Principal{UserID: userID, Username: userID}
	return r.WithContext(httpapi.WithPrincipal(context.Background(), p))
}

// anonymous builds the same request on an instance with authentication off.
func anonymous(method, body string) *http.Request {
	return httptest.NewRequest(method, "/api/v1/me/preferences", strings.NewReader(body))
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return got
}

// Nothing stored is not an error: it is the state every account starts in, and
// the answer is the defaults — which are exactly what the console did before this
// screen existed.
func TestReadingBeforeAnythingIsSavedAnswersTheDefaults(t *testing.T) {
	s := newService(t)
	rec := httptest.NewRecorder()
	s.HandleGet(rec, as("usr_1", "GET", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decode(t, rec)
	for _, field := range []string{"region", "timeZone", "language", "appearance"} {
		if got[field] != "" {
			t.Errorf("%s = %v, want the unset default", field, got[field])
		}
	}
}

func TestASavedPreferenceIsReadBack(t *testing.T) {
	s := newService(t)
	rec := httptest.NewRecorder()
	s.HandleSave(rec, as("usr_1", "PUT", `{"region":"ch","timeZone":"Europe/Zurich","language":"de","appearance":"dark"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d (%s), want 200", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	s.HandleGet(rec, as("usr_1", "GET", ""))
	got := decode(t, rec)
	if got["region"] != "ch" || got["timeZone"] != "Europe/Zurich" || got["language"] != "de" || got["appearance"] != "dark" {
		t.Fatalf("read back %v, want the saved record", got)
	}
}

// The whole point of putting this on the server rather than in the browser is
// that it belongs to a person. Two people on one instance must not share one.
func TestOnePersonsPreferenceIsNotAnothers(t *testing.T) {
	s := newService(t)
	s.HandleSave(httptest.NewRecorder(), as("usr_1", "PUT", `{"region":"ch"}`))
	s.HandleSave(httptest.NewRecorder(), as("usr_2", "PUT", `{"region":"us"}`))

	rec := httptest.NewRecorder()
	s.HandleGet(rec, as("usr_1", "GET", ""))
	if got := decode(t, rec)["region"]; got != "ch" {
		t.Fatalf("usr_1 reads %v, want its own ch", got)
	}
}

// With authentication off there is no "me" to own a preference, so the resource
// does not exist. The console reads that as "keep it in this browser" rather than
// as a failure — which is why it must be a plain 404 and not an error page.
func TestWithoutAnIdentityThereIsNoSuchResource(t *testing.T) {
	s := newService(t)
	for _, c := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		req     *http.Request
	}{
		{"get", s.HandleGet, anonymous("GET", "")},
		{"save", s.HandleSave, anonymous("PUT", `{"region":"ch"}`)},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c.handler(rec, c.req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
		})
	}
}

func TestAnInvalidChoiceIsRefusedRatherThanStored(t *testing.T) {
	s := newService(t)
	rec := httptest.NewRecorder()
	s.HandleSave(rec, as("usr_1", "PUT", `{"region":"fr"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	// And the store is untouched: a refused save must not be a half-applied one.
	rec = httptest.NewRecorder()
	s.HandleGet(rec, as("usr_1", "GET", ""))
	if got := decode(t, rec)["region"]; got != "" {
		t.Fatalf("region = %v after a refused save, want it unset", got)
	}
}

func TestAMalformedBodyIsAClientError(t *testing.T) {
	s := newService(t)
	rec := httptest.NewRecorder()
	s.HandleSave(rec, as("usr_1", "PUT", `{"region":`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// Clearing the screen back to the defaults has to be possible, and it is the same
// request with empty fields rather than a second endpoint.
func TestSavingEmptyFieldsClearsTheChoice(t *testing.T) {
	s := newService(t)
	s.HandleSave(httptest.NewRecorder(), as("usr_1", "PUT", `{"region":"us","timeZone":"UTC","appearance":"system"}`))
	rec := httptest.NewRecorder()
	s.HandleSave(rec, as("usr_1", "PUT", `{"region":"","timeZone":"","language":"","appearance":""}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	s.HandleGet(rec, as("usr_1", "GET", ""))
	got := decode(t, rec)
	if got["region"] != "" || got["timeZone"] != "" || got["appearance"] != "" {
		t.Fatalf("read back %v, want the cleared record", got)
	}
}

// The identity comes from the authenticated principal and from nowhere else: a
// request may not name whose preferences it is writing.
func TestTheBodyCannotNameSomebodyElse(t *testing.T) {
	s := newService(t)
	s.HandleSave(httptest.NewRecorder(), as("usr_1", "PUT", `{"userId":"usr_2","region":"ch"}`))

	rec := httptest.NewRecorder()
	s.HandleGet(rec, as("usr_2", "GET", ""))
	if got := decode(t, rec)["region"]; got != "" {
		t.Fatalf("usr_2 reads %v, want nothing written under their id", got)
	}
}
