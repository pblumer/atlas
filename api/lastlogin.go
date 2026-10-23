package api

import (
	"log/slog"
	"sync"
	"time"

	"github.com/pblumer/atlas/logging"
)

// This file keeps the one durable fact about signing in that the Organization page
// needs and the session map cannot hold: the day each account last signed in
// (ADR-draft-last-login-day).
//
// Presence (ADR-0228) says who is here this minute, and a restart forgets it, which
// is right for that question. It cannot answer the other one an administrator brings
// to the roster: which of these accounts is anybody still using. An account nobody
// has signed in to since spring is one to disable, and the page could not tell it
// from an account used this morning.
//
// It is a day, not an instant, and it is overwritten, never appended to. A day
// answers "still in use?" completely; the time of day would add only when somebody
// starts work, which is the attendance question ADR-0228 keeps Atlas from answering
// by accident. The audit log keeps the exact instant of every login (ADR-0197) for
// the operators whose job is to read it.
//
// The day is a UTC calendar day. Atlas has no instance-wide time zone — every person
// chooses their own (ADR-0459) — and a day that meant something different to each
// reader would make the column disagree with itself. Near midnight that puts a login
// on the neighbouring day, which does not change whether an account is in use.

// loginDateLayout is how a login day is written: an ISO 8601 calendar date, which
// sorts as text in the same order it sorts as time.
const loginDateLayout = "2006-01-02"

// loginDate is the UTC calendar day of an instant.
func loginDate(t time.Time) string { return t.UTC().Format(loginDateLayout) }

// loginDates carries login days from the sign-in path to the account records.
//
// A login must not wait for the run loop (ADR-0265), and account records are only
// written on it, so the two meet here: a login leaves its day in this map and
// returns, and the writer moves the map onto the records in the loop's next turn for
// it. The map holds one entry per account, so however long the loop is busy, a burst
// of logins is one write per account, and the map can grow no larger than the roster.
type loginDates struct {
	mu      sync.Mutex
	pending map[string]string // user id → login day
	wake    chan struct{}     // capacity 1: there is something to write
}

func newLoginDates() *loginDates {
	return &loginDates{pending: map[string]string{}, wake: make(chan struct{}, 1)}
}

// note records that an account signed in on day. A later day replaces an earlier
// one and never the other way round, so two logins either side of midnight leave the
// later day whichever of them is noted last.
func (l *loginDates) note(userID, day string) {
	l.mu.Lock()
	if day > l.pending[userID] {
		l.pending[userID] = day
	}
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default: // a wake is already pending, and the write it starts takes this entry too
	}
}

// take empties the map and returns what it held.
func (l *loginDates) take() map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.pending
	l.pending = map[string]string{}
	return out
}

// noteLogin records that u has just signed in. It never waits: the write happens on
// a later run-loop turn, and the login has already succeeded by the time this runs.
//
// The day is read from the session store's clock, because it is the day the session
// that login opened was opened. An account whose record already says today is left
// alone, which is the common case and costs no loop turn at all.
func (s *Server) noteLogin(u User) {
	day := loginDate(s.sessions.now())
	if u.LastLoginDate >= day {
		return
	}
	s.loginDates.note(u.ID, day)
}

// loginDateWriter writes noted login days onto the account records whenever there are
// some, until the server stops.
func (s *Server) loginDateWriter() {
	defer s.wg.Done()
	for {
		select {
		case <-s.quit:
			return
		case <-s.loginDates.wake:
			s.flushLoginDates()
		}
	}
}

// flushLoginDates writes every noted login day in one run-loop turn.
//
// The map is taken inside the turn, not before it. A flush that never gets its turn
// because the server is stopping then leaves the days where Close finds them,
// instead of holding them in a closure that does not run.
func (s *Server) flushLoginDates() {
	s.do(func() { s.recordLoginDates(s.loginDates.take()) })
}

// recordLoginDates writes login days onto the account records. It must run where
// nothing else writes the user store: on the run loop, or after it has stopped.
//
// Each record is read again here and only its login day changes, so an edit an
// administrator made after the login read the account is never overwritten with that
// older copy. An account deleted since is not recreated: a login day is no reason
// for a record to exist.
func (s *Server) recordLoginDates(days map[string]string) {
	for id, day := range days {
		u, ok, err := s.users.Get(id)
		if err == nil && (!ok || u.LastLoginDate >= day) {
			continue
		}
		if err == nil {
			u.LastLoginDate = day
			err = s.users.Save(u)
		}
		if err != nil {
			// Nothing the caller could act on: the login it describes has succeeded, and
			// the account's next login writes the day again.
			logging.Warn(logging.AuthLoginDateUnrecorded, "the day of a login could not be written to the account",
				slog.String("user_id", id), slog.String("error", err.Error()))
		}
	}
}
