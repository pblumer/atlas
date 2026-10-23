package workerquery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/lookup"
)

func serve(t *testing.T, s *Service, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/worker-queries/activate", s.HandleActivate)
	mux.HandleFunc("POST /api/v1/worker-queries/{id}/answer", s.HandleAnswer)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func activate(t *testing.T, s *Service, body string) []Leased {
	t.Helper()
	rec := serve(t, s, "POST", "/api/v1/worker-queries/activate", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("activate: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Queries []Leased `json:"queries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Queries
}

func TestTheWorkerAPILeasesAQueryAndTakesItsAnswer(t *testing.T) {
	c := newClock()
	b := testBroker(c)
	s := NewService(b, 1<<20)
	if got := activate(t, s, `{"type":"io.atlas.entra","worker":"w1","connectors":["ucc_entra"]}`); len(got) != 0 {
		t.Fatalf("an idle poll leased %d", len(got))
	}
	answer := ask(b, context.Background(), "ucc_entra")
	got := activate(t, s, `{"type":"io.atlas.entra","worker":"w1","connectors":["ucc_entra"],"waitMs":30000,"maxQueries":4}`)
	if len(got) != 1 {
		t.Fatalf("leased %d, want 1", len(got))
	}
	l := got[0]
	if l.Deadline <= c.now().UnixMilli() {
		t.Errorf("deadline %d is not in the future", l.Deadline)
	}
	body, _ := json.Marshal(map[string]any{"worker": "w1", "leaseToken": l.LeaseToken,
		"page": map[string]any{"items": []any{map[string]any{"id": "a"}}, "next": "n"}})
	if rec := serve(t, s, "POST", "/api/v1/worker-queries/"+l.ID+"/answer", string(body)); rec.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body)
	}
	a := <-answer
	if a.err != nil || len(a.page.Items) != 1 || a.page.Next != "n" {
		t.Fatalf("asker got %+v", a)
	}
}

func TestTheWorkerAPIRefusesAPollThatSaysNothingAboutItself(t *testing.T) {
	s := NewService(testBroker(newClock()), 1<<20)
	for _, body := range []string{`{}`, `{"type":"io.atlas.entra"}`, `{"worker":"w1"}`, `not json`,
		`{"type":"io.atlas.entra","worker":"w1","extra":1}`} {
		if rec := serve(t, s, "POST", "/api/v1/worker-queries/activate", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, rec.Code)
		}
	}
}

func TestTheWorkerAPIRefusesAnAnswerItCannotDeliver(t *testing.T) {
	b := testBroker(newClock())
	s := NewService(b, 2048)
	present(t, b, "w1", "ucc_entra")
	answer := ask(b, context.Background(), "ucc_entra")
	l := leaseOne(t, b, "w1", "ucc_entra")
	tooMany := make([]any, lookup.MaxPageSize+1)
	for i := range tooMany {
		tooMany[i] = map[string]any{"id": "x"}
	}
	for _, tc := range []struct {
		name string
		body any
		code int
	}{
		{"neither page nor error", map[string]any{"worker": "w1", "leaseToken": l.LeaseToken}, 400},
		{"both", map[string]any{"worker": "w1", "leaseToken": l.LeaseToken, "page": map[string]any{"items": []any{}},
			"error": map[string]any{"code": "provider_unavailable"}}, 400},
		{"an unknown code", map[string]any{"worker": "w1", "leaseToken": l.LeaseToken, "error": map[string]any{"code": "teapot"}}, 400},
		{"a page larger than any query may ask for", map[string]any{"worker": "w1", "leaseToken": l.LeaseToken,
			"page": map[string]any{"items": tooMany[:lookup.MaxPageSize+1]}}, 400},
		{"another lease", map[string]any{"worker": "w1", "leaseToken": "forged", "page": map[string]any{"items": []any{}}}, 409},
	} {
		raw, _ := json.Marshal(tc.body)
		if rec := serve(t, s, "POST", "/api/v1/worker-queries/"+l.ID+"/answer", string(raw)); rec.Code != tc.code {
			t.Errorf("%s: %d %s, want %d", tc.name, rec.Code, rec.Body, tc.code)
		}
	}
	// A body past the budget is refused before it is decoded.
	huge := `{"worker":"w1","leaseToken":"` + l.LeaseToken + `","page":{"items":[{"id":"` + strings.Repeat("x", 3000) + `"}]}}`
	if rec := serve(t, s, "POST", "/api/v1/worker-queries/"+l.ID+"/answer", huge); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized answer: %d, want 413", rec.Code)
	}
	// None of that reached the asker: the real answer still can.
	raw, _ := json.Marshal(map[string]any{"worker": "w1", "leaseToken": l.LeaseToken,
		"error": map[string]any{"code": "provider_throttled", "retryAfter": 4}})
	if rec := serve(t, s, "POST", "/api/v1/worker-queries/"+l.ID+"/answer", string(raw)); rec.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body)
	}
	if a := <-answer; a.err == nil || !strings.Contains(a.err.Error(), "provider_throttled") {
		t.Fatalf("asker got %+v", a)
	}
	// And once delivered, the query is gone.
	if rec := serve(t, s, "POST", "/api/v1/worker-queries/"+l.ID+"/answer", string(raw)); rec.Code != http.StatusNotFound {
		t.Errorf("second answer: %d, want 404", rec.Code)
	}
}

// The long poll is bounded by the server, not by the worker's request: an Instance
// asking to wait an hour holds a request the server would otherwise never release.
func TestTheWorkerAPICapsALongPoll(t *testing.T) {
	c := newClock()
	b := testBroker(c)
	s := NewService(b, 1<<20)
	var asked time.Duration
	b.after = func(d time.Duration) <-chan time.Time {
		asked = d
		ch := make(chan time.Time, 1)
		ch <- c.now()
		return ch
	}
	_ = activate(t, s, `{"type":"io.atlas.entra","worker":"w1","connectors":["ucc_entra"],"waitMs":3600000}`)
	if asked != MaxPollWait {
		t.Errorf("waited %v, want the %v cap", asked, MaxPollWait)
	}
}

func TestTheWorkerAPIReadsAnAnswerAsJSON(t *testing.T) {
	s := NewService(testBroker(newClock()), 1<<20)
	req := httptest.NewRequest("POST", "/api/v1/worker-queries/x/answer", bytes.NewReader([]byte("{")))
	req.SetPathValue("id", "x")
	rec := httptest.NewRecorder()
	s.HandleAnswer(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("%d, want 400", rec.Code)
	}
}

// What the API passes on is held to what an asker can use: a page without items is an
// empty page, not a missing one, and a wait longer than any provider would ask for is
// dropped rather than shown to the person.
func TestTheWorkerAPINormalizesWhatItPassesOn(t *testing.T) {
	b := testBroker(newClock())
	s := NewService(b, 1<<20)
	present(t, b, "w1", "ucc_entra")

	answer := ask(b, context.Background(), "ucc_entra")
	l := leaseOne(t, b, "w1", "ucc_entra")
	// A continuation longer than any provider's is refused, within a budget that holds it.
	long, _ := json.Marshal(map[string]any{"worker": "w1", "leaseToken": l.LeaseToken,
		"page": map[string]any{"items": []any{}, "next": strings.Repeat("n", maxNextLength+1)}})
	if rec := serve(t, s, "POST", "/api/v1/worker-queries/"+l.ID+"/answer", string(long)); rec.Code != http.StatusBadRequest {
		t.Fatalf("an overlong continuation: %d %s, want 400", rec.Code, rec.Body)
	}
	raw, _ := json.Marshal(map[string]any{"worker": "w1", "leaseToken": l.LeaseToken, "page": map[string]any{}})
	if rec := serve(t, s, "POST", "/api/v1/worker-queries/"+l.ID+"/answer", string(raw)); rec.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body)
	}
	if a := <-answer; a.err != nil || a.page.Items == nil || len(a.page.Items) != 0 {
		t.Fatalf("asker got %+v, want an empty page", a)
	}

	answer = ask(b, context.Background(), "ucc_entra")
	l = leaseOne(t, b, "w1", "ucc_entra")
	raw, _ = json.Marshal(map[string]any{"worker": "w1", "leaseToken": l.LeaseToken,
		"error": map[string]any{"code": "provider_throttled", "retryAfter": maxRetryAdvice + 1}})
	if rec := serve(t, s, "POST", "/api/v1/worker-queries/"+l.ID+"/answer", string(raw)); rec.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body)
	}
	a := <-answer
	var le *lookup.Error
	if !errors.As(a.err, &le) || le.Code != lookup.CodeThrottled || le.RetryAfter != 0 {
		t.Fatalf("asker got %+v, want provider_throttled without a wait", a)
	}
}
