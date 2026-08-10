// Package remedymock is an in-memory stand-in for a BMC Remedy AR System REST API,
// so the Remedy connector (ADR-0106) can be exercised end to end without a real
// Remedy / Helix ITSM instance. It implements exactly the three endpoints the
// [github.com/pblumer/atlas/remedy.HTTPClient] calls, faithfully enough that the real
// client talks to it unmodified:
//
//	POST /api/jwt/login              form-encoded username/password → a JWT (plain text)
//	POST /api/arsys/v1/entry/{form}  {"values": …} + AR-JWT bearer → 201 + Location: …/{id}
//	POST /api/jwt/logout             AR-JWT bearer → 204
//
// plus one read-only convenience endpoint the real AR System does not have, for
// inspecting what a process created:
//
//	GET  /mock/entries               JSON array of every created entry
//
// It is a development and demo aid, not part of the connector's runtime path: point a
// managed Remedy connector's base URL at a running mock (see the `atlas mock-remedy`
// subcommand) and a Remedy connector task creates entries against it. Created entry
// ids are generated deterministically ("<prefix><zero-padded counter>", default prefix
// "INC"), so a demo is reproducible. Delivery is faithful to the real thing: there is
// no idempotency de-duplication, so an at-least-once replay creates a second entry —
// the X-Request-ID header the connector sends is recorded on each entry so a replay is
// visible.
package remedymock

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Entry is one entry created against the mock, as returned by GET /mock/entries. ID is
// the generated entry id (also handed back in the create response's Location header);
// Form is the Remedy form it was created in; Values are the field values the connector
// sent; RequestID is the connector's X-Request-ID (the job key) so an at-least-once
// replay is recognizable.
type Entry struct {
	ID        string         `json:"id"`
	Form      string         `json:"form"`
	Values    map[string]any `json:"values"`
	RequestID string         `json:"requestId,omitempty"`
}

// Server is a mock AR System REST API. Build one with [New], mount [Server.Handler] on
// an HTTP server, and read what was created with [Server.Entries]. It is safe for
// concurrent use.
type Server struct {
	mu       sync.Mutex
	user     string // required username; "" accepts any non-empty username/password
	password string
	idPrefix string
	entrySeq int
	tokenSeq int
	tokens   map[string]bool
	entries  []Entry
}

// Option configures a [Server].
type Option func(*Server)

// WithCredentials requires login to present exactly this username and password. With no
// such option (or empty values) the mock accepts any login with a non-empty username
// and password — convenient for a throwaway demo.
func WithCredentials(user, password string) Option {
	return func(s *Server) { s.user, s.password = user, password }
}

// WithIDPrefix sets the prefix of generated entry ids (default "INC", so a created
// incident looks like INC000000000001). Use e.g. "CRQ" to mimic a change form.
func WithIDPrefix(prefix string) Option {
	return func(s *Server) { s.idPrefix = prefix }
}

// New builds a mock AR System server.
func New(opts ...Option) *Server {
	s := &Server{idPrefix: "INC", tokens: map[string]bool{}}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Handler returns the HTTP handler implementing the mock endpoints.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/jwt/login", s.handleLogin)
	mux.HandleFunc("POST /api/arsys/v1/entry/{form...}", s.handleCreate)
	mux.HandleFunc("POST /api/jwt/logout", s.handleLogout)
	mux.HandleFunc("GET /mock/entries", s.handleEntries)
	return mux
}

// Entries returns a snapshot copy of every entry created so far, oldest first.
func (s *Server) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

// handleLogin checks the credentials and issues a JWT (returned as the plain-text
// response body, as the AR System does). Bad or missing credentials are a 401 so the
// connector's login step fails and the job retries.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	user := r.PostFormValue("username")
	pass := r.PostFormValue("password")
	if !s.credentialsOK(user, pass) {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	s.tokenSeq++
	token := "mock-jwt-" + strconv.Itoa(s.tokenSeq)
	s.tokens[token] = true
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, token)
}

// credentialsOK reports whether a login is accepted: an exact match when credentials
// were configured, otherwise any non-empty username and password.
func (s *Server) credentialsOK(user, pass string) bool {
	if s.user != "" || s.password != "" {
		return user == s.user && pass == s.password
	}
	return user != "" && pass != ""
}

// handleCreate validates the bearer token, stores the entry, and returns 201 with a
// Location header carrying the generated entry id — the shape the connector reads the
// id back from.
func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !s.tokenOK(r.Header.Get("Authorization")) {
		http.Error(w, "invalid or missing AR-JWT token", http.StatusUnauthorized)
		return
	}
	form := r.PathValue("form")
	if strings.TrimSpace(form) == "" {
		http.Error(w, "missing form", http.StatusBadRequest)
		return
	}
	var payload struct {
		Values map[string]any `json:"values"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.entrySeq++
	id := fmt.Sprintf("%s%012d", s.idPrefix, s.entrySeq)
	s.entries = append(s.entries, Entry{
		ID:        id,
		Form:      form,
		Values:    payload.Values,
		RequestID: r.Header.Get("X-Request-ID"),
	})
	s.mu.Unlock()
	// The real AR System returns the new entry's URL here; the connector reads the id
	// from its last path segment. r.URL.Path is /api/arsys/v1/entry/{form}, so appending
	// /{id} yields the faithful shape.
	w.Header().Set("Location", strings.TrimRight(r.URL.Path, "/")+"/"+id)
	w.WriteHeader(http.StatusCreated)
}

// tokenOK reports whether an Authorization header carries a token this mock issued and
// has not logged out.
func (s *Server) tokenOK(authHeader string) bool {
	const prefix = "AR-JWT "
	if !strings.HasPrefix(authHeader, prefix) {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, prefix))
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[token]
}

// handleLogout invalidates the bearer token. It always answers 204 (releasing an
// unknown token is a no-op), matching the connector's best-effort logout.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	const prefix = "AR-JWT "
	if token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), prefix)); token != "" {
		s.mu.Lock()
		delete(s.tokens, token)
		s.mu.Unlock()
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleEntries serves the created entries as JSON — the inspection endpoint the real
// AR System does not have.
func (s *Server) handleEntries(w http.ResponseWriter, _ *http.Request) {
	entries := s.Entries()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(entries)
}
