package remedy_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/remedy"
)

// arSystemStub is a minimal AR System REST stand-in: it issues a JWT on login, echoes
// the created entry with a Location header, and accepts logout. Each hook can be
// overridden to exercise a failure path.
type arSystemStub struct {
	loginStatus  int
	loginBody    string
	createStatus int
	location     string
	lastAuth     string
	lastBody     string
	lastRequest  string
	loggedOut    bool
}

func (s *arSystemStub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/jwt/login", func(w http.ResponseWriter, r *http.Request) {
		if s.loginStatus == 0 {
			s.loginStatus = http.StatusOK
		}
		w.WriteHeader(s.loginStatus)
		body := s.loginBody
		if s.loginStatus/100 == 2 && body == "" {
			body = "jwt-token-123"
		}
		io.WriteString(w, body)
	})
	mux.HandleFunc("/api/arsys/v1/entry/", func(w http.ResponseWriter, r *http.Request) {
		s.lastAuth = r.Header.Get("Authorization")
		s.lastRequest = r.Header.Get("X-Request-ID")
		b, _ := io.ReadAll(r.Body)
		s.lastBody = string(b)
		if s.createStatus == 0 {
			s.createStatus = http.StatusCreated
		}
		loc := s.location
		if loc == "" {
			loc = "https://h/api/arsys/v1/entry/HPD:IncidentInterface_Create/INC000000000101"
		}
		w.Header().Set("Location", loc)
		w.WriteHeader(s.createStatus)
	})
	mux.HandleFunc("/api/jwt/logout", func(w http.ResponseWriter, r *http.Request) {
		s.loggedOut = true
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func TestHTTPClientCreateEntry(t *testing.T) {
	stub := &arSystemStub{}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	c := remedy.NewHTTPClient(remedy.Connector{BaseURL: srv.URL, Username: "svc", Password: "pw"})
	res, err := c.CreateEntry(context.Background(), remedy.Entry{
		Form:      "HPD:IncidentInterface_Create",
		Values:    map[string]any{"Description": "Disk full"},
		RequestID: "9001",
	})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if res.EntryID != "INC000000000101" {
		t.Errorf("EntryID = %q, want the id from the Location header", res.EntryID)
	}
	if stub.lastAuth != "AR-JWT jwt-token-123" {
		t.Errorf("Authorization = %q, want the AR-JWT bearer", stub.lastAuth)
	}
	if stub.lastRequest != "9001" {
		t.Errorf("X-Request-ID = %q, want the job key", stub.lastRequest)
	}
	if !strings.Contains(stub.lastBody, `"values"`) || !strings.Contains(stub.lastBody, "Disk full") {
		t.Errorf("body = %q, want a values object carrying the field", stub.lastBody)
	}
	if !stub.loggedOut {
		t.Error("expected the client to release the JWT (logout)")
	}
}

func TestHTTPClientLoginFailure(t *testing.T) {
	stub := &arSystemStub{loginStatus: http.StatusUnauthorized, loginBody: "bad creds"}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()
	c := remedy.NewHTTPClient(remedy.Connector{BaseURL: srv.URL, Username: "x", Password: "y"})
	if _, err := c.CreateEntry(context.Background(), remedy.Entry{Form: "F"}); err == nil {
		t.Fatal("want an error for a 401 login, got nil")
	}
}

func TestHTTPClientEmptyToken(t *testing.T) {
	stub := &arSystemStub{loginBody: "   "} // 2xx but a blank token
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()
	c := remedy.NewHTTPClient(remedy.Connector{BaseURL: srv.URL})
	if _, err := c.CreateEntry(context.Background(), remedy.Entry{Form: "F"}); err == nil {
		t.Fatal("want an error for an empty token, got nil")
	}
}

func TestHTTPClientCreateFailure(t *testing.T) {
	stub := &arSystemStub{createStatus: http.StatusBadRequest}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()
	c := remedy.NewHTTPClient(remedy.Connector{BaseURL: srv.URL})
	if _, err := c.CreateEntry(context.Background(), remedy.Entry{Form: "F", Values: map[string]any{"A": "b"}}); err == nil {
		t.Fatal("want an error for a 400 create, got nil")
	}
}

func TestHTTPClientTransportError(t *testing.T) {
	// A base URL that resolves to nothing makes the login transport fail.
	c := remedy.NewHTTPClient(remedy.Connector{BaseURL: "http://127.0.0.1:1"})
	if _, err := c.CreateEntry(context.Background(), remedy.Entry{Form: "F"}); err == nil {
		t.Fatal("want a transport error, got nil")
	}
}

// TestHTTPClientCreateNoLocation covers a create that succeeds without a Location
// header: the call still succeeds, with an empty entry id.
func TestHTTPClientCreateNoLocation(t *testing.T) {
	stub := &arSystemStub{location: " "}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()
	c := remedy.NewHTTPClient(remedy.Connector{BaseURL: srv.URL})
	res, err := c.CreateEntry(context.Background(), remedy.Entry{Form: "F"})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if res.EntryID != "" {
		t.Errorf("EntryID = %q, want empty when no Location is returned", res.EntryID)
	}
}
