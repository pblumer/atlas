package feelgen

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/agent"
)

func TestHandleCapability(t *testing.T) {
	for _, tc := range []struct {
		workers []Worker
		want    string
	}{
		{nil, `{"available":false,"workers":[]}`},
		{one, `{"available":true,"workers":[{"name":"openrouter","model":"meta-llama/llama-3.3-70b-instruct:free","provider":"chat-completions"}]}`},
	} {
		rec := httptest.NewRecorder()
		service(tc.workers, nil).HandleCapability(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if got := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusOK || got != tc.want {
			t.Errorf("%d %s, want %s", rec.Code, got, tc.want)
		}
	}

	broken := New(func(*http.Request) ([]Worker, error) { return nil, errors.New("disk") },
		func(*http.Request, string) (agent.Model, error) { return nil, nil })
	rec := httptest.NewRecorder()
	broken.HandleCapability(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("a broken store answered %d", rec.Code)
	}
}

func TestHandleGenerate(t *testing.T) {
	svc := service(one, &scripted{answers: []string{good}})

	rec := httptest.NewRecorder()
	svc.HandleGenerate(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"messages":[{"role":"user","content":"Rabatt"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Expression == "" || resp.Check == nil {
		t.Errorf("body %s: %v", rec.Body, err)
	}

	for name, body := range map[string]string{
		"not JSON":  `{`,
		"no prompt": `{"messages":[]}`,
	} {
		rec := httptest.NewRecorder()
		svc.HandleGenerate(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", name, rec.Code)
		}
	}
}

// failingBody is a request body whose connection dropped mid-read.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestHandleGenerateUnreadableBody(t *testing.T) {
	rec := httptest.NewRecorder()
	service(one, nil).HandleGenerate(rec, httptest.NewRequest(http.MethodPost, "/", failingBody{}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", rec.Code)
	}
}
