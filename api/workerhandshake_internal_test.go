package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/workerproto"
)

// The Worker protocol handshake (ADR-0535 §3): every poll names the protocol version
// the instance speaks and, when it has one, the Worker Type it runs. The server
// refuses a version it does not speak, says which it does on every answer, and shows
// what an instance reported in the Workers view.

// pollRaw posts a poll to path and returns the recorder, so a test can read the header.
func pollRaw(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

type handshakeView struct {
	Workers []struct {
		Worker     string                `json:"worker"`
		Protocol   int                   `json:"protocol"`
		WorkerType *workerproto.Identity `json:"workerType"`
	} `json:"workers"`
}

func workersView(t *testing.T, srv *Server) handshakeView {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/workers", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET /workers: status=%d body=%s", code, raw)
	}
	var out handshakeView
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode workers: %v (%s)", err, raw)
	}
	return out
}

// A client from before the handshake names no version and is served as version 1; the
// answer tells it which versions this server speaks.
func TestAPollWithoutAProtocolIsServedAsVersionOne(t *testing.T) {
	srv := mailPullSrv(t)
	rec := pollRaw(t, srv, "/api/v1/jobs/activate", `{"type":"io.atlas.mail.send","worker":"old-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("poll: status=%d body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get(workerproto.Header); got != workerproto.HeaderValue {
		t.Errorf("%s = %q, want %q", workerproto.Header, got, workerproto.HeaderValue)
	}
	view := workersView(t, srv)
	if len(view.Workers) != 1 || view.Workers[0].Protocol != 1 || view.Workers[0].WorkerType != nil {
		t.Errorf("workers = %+v, want old-1 at protocol 1 with no Worker Type", view.Workers)
	}
}

// What an instance reports it runs is shown, and replaced on the next poll — an
// instance upgraded underneath the server shows its new version at once.
func TestTheWorkersViewShowsTheReportedWorkerType(t *testing.T) {
	srv := mailPullSrv(t)
	digest := "sha256:" + strings.Repeat("0f", 32)
	pollWorker(t, srv, `{"type":"io.atlas.mail.send","worker":"w-1","protocol":1,`+
		`"workerType":{"id":"io.atlas.mail","version":"1.4.0","digest":"`+digest+`"}}`)
	view := workersView(t, srv)
	if len(view.Workers) != 1 {
		t.Fatalf("workers = %+v, want one", view.Workers)
	}
	wt := view.Workers[0].WorkerType
	if wt == nil || wt.ID != "io.atlas.mail" || wt.Version != "1.4.0" || wt.Digest != digest {
		t.Errorf("workerType = %+v, want what the poll reported", wt)
	}

	pollWorker(t, srv, `{"type":"io.atlas.mail.send","worker":"w-1","protocol":1,"workerType":{"id":"io.atlas.mail","version":"1.5.0"}}`)
	if wt := workersView(t, srv).Workers[0].WorkerType; wt == nil || wt.Version != "1.5.0" || wt.Digest != "" {
		t.Errorf("workerType after an upgrade = %+v, want 1.5.0 and no digest", wt)
	}
	pollWorker(t, srv, `{"type":"io.atlas.mail.send","worker":"w-1","protocol":1}`)
	if wt := workersView(t, srv).Workers[0].WorkerType; wt != nil {
		t.Errorf("workerType after a poll reporting none = %+v, want none", wt)
	}
}

// A version the server does not speak is refused with the versions it does, on both
// polls, and a malformed Worker Type is refused with what is wrong with it.
func TestAPollTheServerCannotServeIsRefusedWithTheReason(t *testing.T) {
	srv := mailPullSrv(t)
	for _, tc := range []struct {
		name, path, body, want string
	}{
		{"job poll, newer protocol", "/api/v1/jobs/activate",
			`{"type":"io.atlas.mail.send","worker":"w-1","protocol":7}`, "worker protocol 7"},
		{"query poll, newer protocol", "/api/v1/worker-queries/activate",
			`{"type":"io.atlas.mail.send","worker":"w-1","protocol":7}`, "worker protocol 7"},
		{"job poll, malformed Worker Type", "/api/v1/jobs/activate",
			`{"type":"io.atlas.mail.send","worker":"w-1","workerType":{"id":"Mail","version":"1"}}`, "reverse-DNS"},
		{"query poll, malformed Worker Type", "/api/v1/worker-queries/activate",
			`{"type":"io.atlas.mail.send","worker":"w-1","workerType":{"id":"io.atlas.mail","version":"one"}}`, "semantic version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := pollRaw(t, srv, tc.path, tc.body)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("status=%d body=%s, want 400 naming %q", rec.Code, rec.Body, tc.want)
			}
			if got := rec.Header().Get(workerproto.Header); got != workerproto.HeaderValue {
				t.Errorf("%s = %q on a refusal, want %q", workerproto.Header, got, workerproto.HeaderValue)
			}
		})
	}
	if view := workersView(t, srv); len(view.Workers) != 0 {
		t.Errorf("workers = %+v; a refused poll must not register an instance", view.Workers)
	}
}
