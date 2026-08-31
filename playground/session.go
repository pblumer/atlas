package playground

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pblumer/atlas/api/runloop"
)

// Session is a [Sandbox] with an owner goroutine and a lifetime.
//
// A sandbox is a partition, and a partition has exactly one writer (invariant
// I3). HTTP handlers are concurrent, so the sandbox is put behind a
// [runloop.Loop] and reached only through [Session.With] — the same boundary the
// rest of the API uses for the durable engine.
//
// A session outlives a request on purpose: 50 000 cases will not finish inside an
// HTTP call, and stepping through one case by hand is a conversation rather than
// a call. It is closed explicitly, or reclaimed by its registry once nobody has
// touched it for the TTL.
type Session struct {
	id string
	sb *Sandbox
	// owner is the principal that opened the session, as the caller names them
	// ("" when authentication is off). A session is not a shared resource: it can
	// hold the variables of a draft only its owner may read, so whoever looks it
	// up has to be the same person. The registry does not enforce this — it has no
	// idea what a principal is — the HTTP layer does, with [Session.OwnedBy].
	owner string

	loop *runloop.Loop
	quit chan struct{}
	done chan struct{}

	// closed guards With: runloop.Do silently does nothing once the loop is
	// stopping, which would make a call on a closed session look like a success
	// that produced a zero value.
	closed atomic.Bool
	// paused is read by a Run in flight, from the goroutine that asked for the
	// pause — so it must not go through the loop, which that Run is holding.
	paused atomic.Bool
	// touched is the last time anybody used this session, as unix nanoseconds.
	touched atomic.Int64

	createdAt time.Time
}

// NewID mints a session id.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("playground: random: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// newSession opens a sandbox and starts the goroutine that owns it.
func newSession(id, owner string, opts Options) (*Session, error) {
	sb, err := Open(opts)
	if err != nil {
		return nil, err
	}
	quit := make(chan struct{})
	s := &Session{
		id: id, owner: owner, sb: sb,
		loop: runloop.New(quit), quit: quit, done: make(chan struct{}),
		createdAt: time.Now(),
	}
	s.touched.Store(s.createdAt.UnixNano())
	go func() {
		defer close(s.done)
		s.loop.Run()
	}()
	return s, nil
}

// ID is the session's identifier.
func (s *Session) ID() string { return s.id }

// OwnedBy reports whether principal is the one that opened this session. An
// unowned session (authentication off) belongs to everyone, which is the same
// reach every other route has in that mode.
func (s *Session) OwnedBy(principal string) bool { return s.owner == "" || s.owner == principal }

// CreatedAt is when the session was opened.
func (s *Session) CreatedAt() time.Time { return s.createdAt }

// LastUsed is when [Session.With] last ran something. The TTL runs from here, so
// a session somebody is working in is not reclaimed under them.
func (s *Session) LastUsed() time.Time { return time.Unix(0, s.touched.Load()) }

// With runs fn against the sandbox on the session's own goroutine and returns
// what fn returned. It is the only way to reach the sandbox, which is what makes
// the sandbox single-writer.
//
// It refuses a closed session rather than returning fn's zero value, so a caller
// can tell "your session is gone" from "your closure produced nothing".
func (s *Session) With(fn func(*Sandbox) error) error {
	if s.closed.Load() {
		return errClosedSession
	}
	s.touched.Store(time.Now().UnixNano())
	var err error
	ran := false
	s.loop.Do(func() {
		ran = true
		err = fn(s.sb)
	})
	if !ran {
		return errClosedSession
	}
	s.touched.Store(time.Now().UnixNano())
	return err
}

// errClosedSession is what every call on a session that is gone returns.
var errClosedSession = errors.New("playground: session is closed")

// ErrClosedSession reports whether err says the session no longer exists, so an
// HTTP layer can answer 404 rather than 500.
func ErrClosedSession(err error) bool { return errors.Is(err, errClosedSession) }

// Pause asks a run in flight to stop at its next occurrence, and keeps later runs
// from starting. It is deliberately not dispatched onto the loop: the run it is
// stopping is holding that loop.
func (s *Session) Pause() { s.paused.Store(true) }

// Resume clears the pause.
func (s *Session) Resume() { s.paused.Store(false) }

// Paused reports whether the session is holding.
func (s *Session) Paused() bool { return s.paused.Load() }

// Budget returns b with this session's pause wired into it, so a run started
// through the session stops when somebody presses pause.
func (s *Session) Budget(b Budget) Budget {
	b.Stop = s.paused.Load
	return b
}

// Close stops the session's goroutine and discards its sandbox. It is safe to
// call twice: the second call reports that there was nothing left to close.
func (s *Session) Close() error {
	if s.closed.Swap(true) {
		return errClosedSession
	}
	close(s.quit)
	<-s.done // the loop has stopped, so nothing else is touching the sandbox
	return s.sb.Close()
}

// Registry holds the live sessions of a server: it hands them out by id, bounds
// how many may exist, and reclaims the ones nobody came back to.
type Registry struct {
	mu       sync.Mutex
	sessions map[string]*Session
	ttl      time.Duration
	max      int
}

// NewRegistry builds a registry that reclaims a session after ttl of disuse and
// refuses to hold more than max at once. Both are resource bounds, not tuning: a
// session is a live engine and a directory on disk.
func NewRegistry(ttl time.Duration, max int) *Registry {
	return &Registry{sessions: map[string]*Session{}, ttl: ttl, max: max}
}

// Open starts a session on a fresh sandbox, owned by the named principal (empty
// when authentication is off). It fails if the model does not compile — in which
// case nothing is registered — or if the registry is full.
func (r *Registry) Open(owner string, opts Options) (*Session, error) {
	id, err := NewID()
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	if r.max > 0 && len(r.sessions) >= r.max {
		r.mu.Unlock()
		return nil, fmt.Errorf("playground: too many open sessions (%d); close one first", r.max)
	}
	// Reserve the slot before the sandbox is built: opening one takes long enough
	// (compile, two stores) that two concurrent callers would otherwise both see
	// room and both get it.
	r.sessions[id] = nil
	r.mu.Unlock()

	s, err := newSession(id, owner, opts)
	if err != nil {
		r.mu.Lock()
		delete(r.sessions, id)
		r.mu.Unlock()
		return nil, err
	}

	r.mu.Lock()
	r.sessions[id] = s
	r.mu.Unlock()
	return s, nil
}

// Get finds a live session by id.
func (r *Registry) Get(id string) (*Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	return s, ok && s != nil
}

// Close ends one session and forgets it.
func (r *Registry) Close(id string) error {
	r.mu.Lock()
	s, ok := r.sessions[id]
	delete(r.sessions, id)
	r.mu.Unlock()
	if !ok || s == nil {
		return fmt.Errorf("playground: no session %q", id)
	}
	return s.Close()
}

// Len is how many sessions are open.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sessions {
		if s != nil {
			n++
		}
	}
	return n
}

// Reap closes every session nobody has touched for the TTL and reports how many
// went. A server calls it on a timer; the sweep is what keeps an abandoned tab
// from holding an engine and a directory for ever.
func (r *Registry) Reap(now time.Time) int {
	r.mu.Lock()
	var stale []*Session
	for id, s := range r.sessions {
		if s == nil {
			continue // a slot reserved by an Open still in flight
		}
		if now.Sub(s.LastUsed()) >= r.ttl {
			stale = append(stale, s)
			delete(r.sessions, id)
		}
	}
	r.mu.Unlock()

	for _, s := range stale {
		_ = s.Close()
	}
	return len(stale)
}

// CloseAll ends every session, for server shutdown.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	all := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		if s != nil {
			all = append(all, s)
		}
	}
	r.sessions = map[string]*Session{}
	r.mu.Unlock()

	for _, s := range all {
		_ = s.Close()
	}
}
