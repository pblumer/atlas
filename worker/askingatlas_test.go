package worker

// The fake atlas server and the names the Worker Type tests here reach for. They lived
// beside the protocol client's own tests until that client moved to workersdk
// (ADR-0535); the Worker Type tests that ask
// before acting still need a server to ask, so this is a copy of that harness rather
// than an import of another package's test file.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/pblumer/atlas/workersdk"
)

// errPolicyRefused is the error a handler returns for a refused action.
var errPolicyRefused = workersdk.Refused()

// runHandler runs one job through whichever shape its handler implements.
var runHandler = workersdk.RunHandler

// askedRequest is one request a fake Atlas server received.
type askedRequest struct {
	method string
	path   string
	header http.Header
	body   map[string]any
}

// askingAtlas stands in for an Atlas server: it records every request it is sent and
// answers each with whatever its answer handler writes.
type askingAtlas struct {
	mu   sync.Mutex
	reqs []askedRequest
}

func newAskingAtlas(t *testing.T, answer http.HandlerFunc) (*httptest.Server, *askingAtlas) {
	t.Helper()
	a := &askingAtlas{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		a.mu.Lock()
		a.reqs = append(a.reqs, askedRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: body})
		a.mu.Unlock()
		// The answer may read the body too.
		r.Body = io.NopCloser(bytes.NewReader(raw))
		answer(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, a
}

func (a *askingAtlas) requests() []askedRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]askedRequest(nil), a.reqs...)
}

// answerJSON answers every request with status and a JSON body, as the API does.
func answerJSON(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}
