package api

import (
	"log/slog"
	"sync"
	"time"

	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/logging"
)

// This file keeps the one durable fact about signing in that the Organization page
// needs and the session map cannot hold: the day each account last signed in
// (ADR-0477).
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
//
// It is kept beside the account, in a store of its own, and written by the login
// itself before it answers. Not on the account record: that record is only written
// on the run loop, a login must not wait for the loop (ADR-0265), and a write left
// for later would land on the account at a moment nobody chose — after an
// administrator's edit, a restore, or a check that nothing changed. This store has
// one kind of record and one writer, so its own mutex is all the ordering it needs,
// the way the session store's is.

// loginDateLayout is how a login day is written: an ISO 8601 calendar date, which
// sorts as text in the same order it sorts as time.
const loginDateLayout = "2006-01-02"

// loginDate is the UTC calendar day of an instant.
func loginDate(t time.Time) string { return t.UTC().Format(loginDateLayout) }

// lastLogin is one account's login day.
type lastLogin struct {
	UserID string `json:"userId"`
	Date   string `json:"date"`
}

// lastLoginStore holds one lastLogin per account that has signed in.
//
// Writes take mu. Two browsers of one person can sign in at the same moment, and
// the sidecar writes a record through a temp file whose name is fixed per key, so
// two unserialised writes of one key could leave a torn record behind. The mutex is
// also what makes "no day for a deleted account" hold: record checks that the
// account exists and forget removes the day after the account is gone, both under
// it, so neither can slip between the other's check and write.
//
// Reads take nothing: a record is replaced by an atomic rename, so a reader sees the
// old day or the new one.
type lastLoginStore struct {
	mu    sync.Mutex
	store *sidecar.Store[lastLogin]
	users *userStore
}

// newLastLoginStore opens (creating if needed) the login-day directory.
func newLastLoginStore(dir string, users *userStore) (*lastLoginStore, error) {
	st, err := sidecar.NewStore(dir, "lastloginstore", func(rec lastLogin) string { return rec.UserID })
	if err != nil {
		return nil, err
	}
	return &lastLoginStore{store: st, users: users}, nil
}

// get returns the day an account last signed in, or "" when none is recorded.
func (l *lastLoginStore) get(userID string) (string, error) {
	rec, _, err := l.store.Get(userID)
	return rec.Date, err
}

// record stores day as userID's last login, unless a day as late is already there or
// the account no longer exists. A day never moves backwards, so two logins either
// side of midnight leave the later one whichever is written last.
//
// The common case — the account already signed in today — is one read and no lock.
func (l *lastLoginStore) record(userID, day string) error {
	if cur, err := l.get(userID); err == nil && cur >= day {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// Asked again under the lock: the answer outside it may be a moment old. An
	// unreadable record is replaced rather than reported — it holds nothing a
	// login could lose.
	if cur, err := l.get(userID); err == nil && cur >= day {
		return nil
	}
	if _, ok, err := l.users.Get(userID); err != nil || !ok {
		return err // an account deleted since its login gets no day
	}
	return l.store.Save(lastLogin{UserID: userID, Date: day})
}

// forget removes an account's login day. It is called after the account itself is
// deleted, which is the order that keeps record from writing a day for it afterwards.
func (l *lastLoginStore) forget(userID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.store.Delete(userID)
}

// prune removes the days of accounts that no longer exist. A delete whose second step
// failed, or an account file removed from the disk by hand, can leave one behind, and
// a login day is no reason to keep anything about an account that is gone. Run at
// startup, before anybody can sign in.
func (l *lastLoginStore) prune() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	all, err := l.store.LoadAll()
	if err != nil {
		return err
	}
	for _, rec := range all {
		if _, ok, err := l.users.Get(rec.UserID); err != nil || ok {
			continue // an unreadable account is not a deleted one
		}
		if err := l.store.Delete(rec.UserID); err != nil {
			return err
		}
	}
	return nil
}

// recordLogin stores the day u has just signed in. It runs on the sign-in path after
// the session exists, never on the run loop. A failure is logged rather than returned:
// the person is signed in either way, and their next login writes the day again.
//
// The day is read from the session store's clock, because it is the day the session
// that login opened was opened.
func (s *Server) recordLogin(u User) {
	if err := s.lastLogins.record(u.ID, loginDate(s.sessions.now())); err != nil {
		logging.Warn(logging.AuthLoginDateUnrecorded, "the day of a login could not be recorded",
			slog.String("user_id", u.ID), slog.String("error", err.Error()))
	}
}

// withLastLogins puts each account's login day on the roster. One record that cannot
// be read leaves that row without a day and is logged; it must not take the page an
// administrator needs to fix things down with it.
func (s *Server) withLastLogins(roster []rosterUser) []rosterUser {
	for i := range roster {
		day, err := s.lastLogins.get(roster[i].ID)
		if err != nil {
			logging.Warn(logging.AuthLoginDateUnrecorded, "the day of an account's last login could not be read",
				slog.String("user_id", roster[i].ID), slog.String("error", err.Error()))
			continue
		}
		roster[i].LastLoginDate = day
	}
	return roster
}
