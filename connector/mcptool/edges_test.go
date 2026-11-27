package mcptool

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A client handed an HTTP client of its own uses it, and a page that is not JSON-RPC is
// quoted in the error only as far as an operator can read it.
func TestAClientsOwnHTTPClientIsUsedAndALongPageIsShortened(t *testing.T) {
	used := false
	f := newFake(t, func(_ *fakeServer, w http.ResponseWriter, _ *http.Request, _ rpcIn) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, strings.Repeat("x", 1000))
	})
	hc := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used = true
		return http.DefaultTransport.RoundTrip(r)
	})}
	_, err := (&Client{Endpoint: f.srv.URL, HTTP: hc}).CallTool(context.Background(), "x", nil)
	if !used {
		t.Error("the client's own HTTP client was not used")
	}
	if err == nil || !strings.HasSuffix(err.Error(), "…") || len(err.Error()) > 400 {
		t.Errorf("error = %.80q (len %d), want the page shortened", err, len(err.Error()))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A stream that carries a line that is not JSON, and then ends without the answer, is a
// call that got no answer — not a hang, and not a success.
func TestAStreamThatEndsWithoutTheAnswerIsAnError(t *testing.T) {
	f := modernServer(t, func(_ *fakeServer, w http.ResponseWriter, _ rpcIn) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: not json\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":999,\"result\":{}}\n\n")
	})
	if _, err := (&Client{Endpoint: f.srv.URL}).CallTool(context.Background(), "get_weather", nil); err == nil || !strings.Contains(err.Error(), "no answer to the request") {
		t.Errorf("error = %v, want one saying the response carried no answer", err)
	}
}

// A header name may carry any token character, and a tool whose re-read list no longer
// lists it is not retried.
func TestHeaderTokensAndAToolThatLeftTheList(t *testing.T) {
	for _, name := range []string{"Trace-ID", "a!#$%&'*+-.^_`|~9"} {
		if !isToken(name) {
			t.Errorf("isToken(%q) = false", name)
		}
	}
	for _, name := range []string{"a b", "ä", "a:b", "a\tb"} {
		if isToken(name) {
			t.Errorf("isToken(%q) = true", name)
		}
	}
	listed := true
	f := newFake(t, func(_ *fakeServer, w http.ResponseWriter, r *http.Request, req rpcIn) {
		switch req.Method {
		case "tools/list":
			if listed {
				rpcResult(w, req.ID, `{"tools":[{"name":"t","inputSchema":{"type":"object"}}]}`)
			} else {
				rpcResult(w, req.ID, `{"tools":[]}`)
			}
		case "tools/call":
			listed = false
			rpcFail(w, http.StatusBadRequest, req.ID, codeHeaderMismatch, "HeaderMismatch")
		}
	})
	if _, err := (&Client{Endpoint: f.srv.URL}).CallTool(context.Background(), "t", nil); err == nil || !strings.Contains(err.Error(), `no tool named "t"`) {
		t.Errorf("error = %v, want the tool named as gone", err)
	}
	if n := len(f.calls("tools/call")); n != 1 {
		t.Errorf("%d calls, want no retry for a tool that is no longer listed", n)
	}
}
