package agent_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/agent"
)

// The plumbing both providers share, tested where it is shared. None of it is about a
// wire format — it is how a credential is presented and how a failure reaches an
// operator — which is why getting it wrong would be wrong twice over.

// An auth scheme nobody implements is refused rather than silently sending no
// credential. Sending none would surface as a 401 from the endpoint, and an operator
// reading that would go looking for a bad key instead of a typo in their own
// configuration.
func TestAnUnknownAuthSchemeFailsTheRoundRatherThanSendingNothing(t *testing.T) {
	srv, seen, _ := endpoint(t, http.StatusOK,
		`{"stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}`)
	m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "sk", Auth: "basic", Client: srv.Client()}

	_, err := m.Decide(context.Background(), chatRound())
	if err == nil {
		t.Fatal("an unknown auth scheme was accepted")
	}
	if !strings.Contains(err.Error(), "basic") {
		t.Errorf("err = %v, want it to name the scheme that is not understood", err)
	}
	if len(*seen) != 0 {
		t.Error("the request was sent anyway; a credential this process cannot present must not reach an endpoint")
	}
}

// An endpoint that needs no credential is served. A self-hosted model may have none,
// and sending an empty one would turn that into a 401 nobody can read.
func TestAnEmptyCredentialSendsNoAuthHeaderAtAll(t *testing.T) {
	srv, _, headers := endpoint(t, http.StatusOK,
		`{"stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}`)
	m := &agent.HTTPModel{Endpoint: srv.URL, Client: srv.Client()}

	if _, err := m.Decide(context.Background(), chatRound()); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got := headers.Get("x-api-key"); got != "" {
		t.Errorf("x-api-key = %q, want the header absent rather than empty", got)
	}
}

// An endpoint that answers with a page — a proxy's error document, an HTML login form
// where JSON was expected — must not put the whole page in an incident message. The
// operator needs the first part of it and the status, not a screenful.
func TestARefusalCarryingAPageIsTruncated(t *testing.T) {
	page := "<html><body>" + strings.Repeat("gateway timeout ", 200) + "</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, page)
	}))
	t.Cleanup(srv.Close)
	m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "sk", Client: srv.Client()}

	_, err := m.Decide(context.Background(), chatRound())
	if err == nil {
		t.Fatal("a 502 was accepted as an answer")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("err = %v, want the status in it: it is what decides what an operator does", err)
	}
	if len(err.Error()) > 600 {
		t.Errorf("the message is %d bytes; an incident carrying a whole page is unreadable", len(err.Error()))
	}
	if !strings.HasSuffix(err.Error(), "…") {
		t.Errorf("err = %q, want it to show that it was cut short", err)
	}
}

// An endpoint that is not there at all fails the round with the transport's own error.
// There is nothing to add to it: "connection refused" is the whole diagnosis.
func TestAnUnreachableEndpointFailsTheRound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	m := &agent.HTTPModel{Endpoint: url, APIKey: "sk"}
	if _, err := m.Decide(context.Background(), chatRound()); err == nil {
		t.Fatal("an unreachable endpoint produced a decision")
	}
}

// A body that is not a decision at all — an endpoint answering 200 with something else
// entirely. Failing the decode says so rather than ending the run with nothing.
func TestAnAnswerThatIsNotJSONFailsTheRound(t *testing.T) {
	for name, body := range map[string]string{
		"the Messages format":         `not json at all`,
		"the Chat-Completions format": `<html>a login page</html>`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)
			var m agent.Model = &agent.HTTPModel{Endpoint: srv.URL, APIKey: "sk", Client: srv.Client()}
			if strings.HasPrefix(name, "the Chat") {
				m = &agent.ChatCompletionsModel{Endpoint: srv.URL, APIKey: "sk", Model: "gpt-4o", Client: srv.Client()}
			}
			if _, err := m.Decide(context.Background(), chatRound()); err == nil {
				t.Error("a body that is not a decision was accepted as one")
			}
		})
	}
}
