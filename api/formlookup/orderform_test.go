package formlookup

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/workersdk/lookup"
)

// orderFormQuery posts a search from a product's order form in the shop, the way
// the server routes it.
func orderFormQuery(t *testing.T, s *Service, user, release, item, field, body string) reply {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/shop/releases/{release}/items/{item}/form-data/{field}/query", s.HandleOrderFormQuery)
	req := httptest.NewRequest("POST", "/api/v1/shop/releases/"+release+"/items/"+item+"/form-data/"+field+"/query",
		strings.NewReader(body))
	req.Header.Set("X-User", user)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return reply{rec.Code, out, rec.Header()}
}

// TestAnOrderFormIsSearchedLikeATasksForm: a directory field in a product's order form
// searches through the same Worker as a task's form, once the release's catalogue lets
// the person order; every refusal of the order form keeps its own answer.
func TestAnOrderFormIsSearchedLikeATasksForm(t *testing.T) {
	f := newFakes()
	f.page = lookup.Page{Items: []map[string]any{{"id": anna, "displayName": "Anna Müller", "mail": "anna.mueller@example.com"}}}
	var formErr error
	var asked [2]string
	cfg := f.config()
	cfg.OrderForm = func(r *http.Request, release, item string) (Task, error) {
		asked = [2]string{release, item}
		if formErr != nil {
			return Task{}, formErr
		}
		return Task{Schema: []byte(exampleForm)}, nil
	}
	s := New(cfg)

	r := orderFormQuery(t, s, "anna", "rel-7", "badge", "user", `{"search":"Ann"}`)
	if r.status != http.StatusOK {
		t.Fatalf("search = %d %v, want 200", r.status, r.body)
	}
	if asked != [2]string{"rel-7", "badge"} {
		t.Errorf("order form asked for %v, want rel-7/badge", asked)
	}
	if len(f.asked) != 1 {
		t.Errorf("Worker asked %d times, want once", len(f.asked))
	}

	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrNoTask, http.StatusNotFound, "not_found"},
		{ErrForbidden, http.StatusForbidden, "forbidden"},
	} {
		formErr = c.err
		if r := orderFormQuery(t, s, "anna", "rel-7", "badge", "user", `{"search":"Ann"}`); r.status != c.status || code(r) != c.code {
			t.Errorf("%v: %d %v, want %d %s", c.err, r.status, r.body, c.status, c.code)
		}
	}
	formErr = errors.New("store unreadable")
	if r := orderFormQuery(t, s, "anna", "rel-7", "badge", "user", `{"search":"Ann"}`); r.status != http.StatusInternalServerError {
		t.Errorf("unreadable order form: %d %v, want 500", r.status, r.body)
	}
}

// TestAServerWithoutOrderFormsAnswersNotFound: a server that wires no order forms
// serves none, rather than reaching a nil function.
func TestAServerWithoutOrderFormsAnswersNotFound(t *testing.T) {
	f := newFakes()
	if r := orderFormQuery(t, f.service(), "anna", "rel-7", "badge", "user", `{"search":"Ann"}`); r.status != http.StatusNotFound || code(r) != "not_found" {
		t.Fatalf("no order forms: %d %v, want 404 not_found", r.status, r.body)
	}
}

// TestAnOrderFormSearchIsRateLimitedLikeAnyOther: the per-person budget applies to the
// shop's searches too.
func TestAnOrderFormSearchIsRateLimitedLikeAnyOther(t *testing.T) {
	f := newFakes()
	f.allowed = 0
	if r := orderFormQuery(t, f.service(), "anna", "rel-7", "badge", "user", `{"search":"Ann"}`); r.status != http.StatusTooManyRequests {
		t.Fatalf("over budget: %d %v, want 429", r.status, r.body)
	}
}
