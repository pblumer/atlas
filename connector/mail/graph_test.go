package mail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// staticToken is a TokenSource returning a fixed token, for exercising the API clients
// without a real OAuth endpoint.
type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

// erroringToken fails token acquisition, standing in for a misconfigured credential.
type erroringToken struct{}

func (erroringToken) Token(context.Context) (string, error) { return "", errors.New("no token") }

// apiServer captures the last request an API client made.
type apiServer struct {
	srv    *httptest.Server
	path   string
	auth   string
	body   []byte
	status int
}

func newAPIServer(t *testing.T) *apiServer {
	t.Helper()
	a := &apiServer{status: 202}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.path = r.URL.Path
		a.auth = r.Header.Get("Authorization")
		a.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(a.status)
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func TestGraphClientSend(t *testing.T) {
	a := newAPIServer(t)
	c := NewGraphClient(staticToken("gtok"), a.srv.URL, "bot@example.com")
	err := c.Send(context.Background(), Message{
		To: []string{"a@example.com", "b@example.com"}, Cc: []string{"c@example.com"}, Bcc: []string{"d@example.com"},
		Subject: "Hi", Body: "Hello there.",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if a.auth != "Bearer gtok" {
		t.Errorf("Authorization = %q, want the bearer token", a.auth)
	}
	if a.path != "/users/bot@example.com/sendMail" {
		t.Errorf("path = %q, want the sender mailbox sendMail route", a.path)
	}
	var payload graphSendMail
	if err := json.Unmarshal(a.body, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Message.Subject != "Hi" || payload.Message.Body.Content != "Hello there." || payload.Message.Body.ContentType != "Text" {
		t.Errorf("message = %+v, want subject/body set as text", payload.Message)
	}
	if len(payload.Message.ToRecipients) != 2 || payload.Message.ToRecipients[0].EmailAddress.Address != "a@example.com" ||
		len(payload.Message.CcRecipients) != 1 || len(payload.Message.BccRecipients) != 1 {
		t.Errorf("recipients = %+v, want 2 to, 1 cc, 1 bcc", payload.Message)
	}
	if !payload.SaveToSentItems {
		t.Error("saveToSentItems = false, want true")
	}
}

// A task-authored From overrides the worker mailbox in the sendMail path.
func TestGraphClientFromOverride(t *testing.T) {
	a := newAPIServer(t)
	c := NewGraphClient(staticToken("t"), a.srv.URL, "default@example.com")
	if err := c.Send(context.Background(), Message{From: "custom@example.com", To: []string{"x@y.z"}, Subject: "s", Body: "b"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if a.path != "/users/custom@example.com/sendMail" {
		t.Errorf("path = %q, want the task's From mailbox", a.path)
	}
}

func TestGraphClientErrors(t *testing.T) {
	t.Run("no sender", func(t *testing.T) {
		if err := NewGraphClient(staticToken("t"), "http://x", "").Send(context.Background(), Message{To: []string{"a@b.c"}}); err == nil {
			t.Error("want an error when no mailbox is configured")
		}
	})
	t.Run("no recipients", func(t *testing.T) {
		if err := NewGraphClient(staticToken("t"), "http://x", "s@x").Send(context.Background(), Message{Subject: "s"}); err == nil {
			t.Error("want an error when there are no recipients")
		}
	})
	t.Run("token error", func(t *testing.T) {
		if err := NewGraphClient(erroringToken{}, "http://x", "s@x").Send(context.Background(), Message{To: []string{"a@b.c"}}); err == nil {
			t.Error("want the token error propagated")
		}
	})
	t.Run("api non-2xx", func(t *testing.T) {
		a := newAPIServer(t)
		a.status = 403
		if err := NewGraphClient(staticToken("t"), a.srv.URL, "s@x").Send(context.Background(), Message{To: []string{"a@b.c"}, Subject: "s"}); err == nil {
			t.Error("want an error for a 403 API response")
		}
	})
}

// NewGraphClient defaults the base URL to the Graph v1.0 API when none is given.
func TestGraphClientDefaultBase(t *testing.T) {
	if NewGraphClient(staticToken("t"), "", "s@x").baseURL != graphDefaultBase {
		t.Error("empty base URL should default to the Graph API base")
	}
}

// A malformed base URL makes the request unbuildable — sendJSON returns that error
// (the shared authenticated-POST helper both clients use).
func TestSendJSONBuildError(t *testing.T) {
	c := NewGraphClient(staticToken("t"), "://bad-url", "s@example.com")
	if err := c.Send(context.Background(), Message{To: []string{"a@b.c"}, Subject: "s", Body: "b"}); err == nil {
		t.Error("malformed endpoint: want a build error from sendJSON")
	}
}

func TestGmailClientSend(t *testing.T) {
	a := newAPIServer(t)
	a.status = 200
	c := NewGmailClient(staticToken("gm"), a.srv.URL, "bot@example.com")
	err := c.Send(context.Background(), Message{To: []string{"a@example.com"}, Subject: "Hÿ", Body: "Body text.", MessageID: "42"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if a.auth != "Bearer gm" {
		t.Errorf("Authorization = %q", a.auth)
	}
	if a.path != "/users/me/messages/send" {
		t.Errorf("path = %q, want the messages.send route", a.path)
	}
	var payload struct {
		Raw string `json:"raw"`
	}
	if err := json.Unmarshal(a.body, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	mime, err := base64.URLEncoding.DecodeString(payload.Raw)
	if err != nil {
		t.Fatalf("raw is not base64url: %v", err)
	}
	got := string(mime)
	for _, want := range []string{"From: bot@example.com\r\n", "To: a@example.com\r\n", "Message-ID: <42@atlas>\r\n", "Body text."} {
		if !strings.Contains(got, want) {
			t.Errorf("MIME missing %q:\n%s", want, got)
		}
	}
}

func TestGmailClientErrors(t *testing.T) {
	t.Run("no sender", func(t *testing.T) {
		if err := NewGmailClient(staticToken("t"), "http://x", "").Send(context.Background(), Message{To: []string{"a@b.c"}}); err == nil {
			t.Error("want an error when no sender is configured")
		}
	})
	t.Run("no recipients", func(t *testing.T) {
		if err := NewGmailClient(staticToken("t"), "http://x", "s@x").Send(context.Background(), Message{Subject: "s"}); err == nil {
			t.Error("want an error when there are no recipients")
		}
	})
	t.Run("token error", func(t *testing.T) {
		if err := NewGmailClient(erroringToken{}, "http://x", "s@x").Send(context.Background(), Message{To: []string{"a@b.c"}}); err == nil {
			t.Error("want the token error propagated")
		}
	})
	t.Run("default base", func(t *testing.T) {
		if NewGmailClient(staticToken("t"), "", "s@x").baseURL != gmailDefaultBase {
			t.Error("empty base URL should default to the Gmail API base")
		}
	})
}

// Graph carries one body with a declared content type, so an HTML body is sent as
// contentType "HTML" — and a text-only message keeps sending "Text" (covered by
// TestGraphClientSend), which is what every pre-HTML task does.
func TestGraphClientHTMLBody(t *testing.T) {
	a := newAPIServer(t)
	c := NewGraphClient(staticToken("gtok"), a.srv.URL, "bot@example.com")
	err := c.Send(context.Background(), Message{
		To: []string{"a@example.com"}, Subject: "Hi",
		Body: "Hello there.", HTML: "<p>Hello <b>there</b>.</p>",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var payload graphSendMail
	if err := json.Unmarshal(a.body, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Message.Body.ContentType != "HTML" || payload.Message.Body.Content != "<p>Hello <b>there</b>.</p>" {
		t.Errorf("body = %+v, want the markup declared as HTML", payload.Message.Body)
	}
}
