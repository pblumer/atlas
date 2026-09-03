package api

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// This file answers one question the Organization page could not: of the accounts
// listed there, which have somebody at the other end right now
// (ADR-0228).
//
// The roster already says whether an account may sign in. That is a property of
// the record and says nothing about the person: an account that has been enabled
// for two years and an account somebody is looking at this second render
// identically. An administrator asking "who is in here" — before a restart, while
// tracing what just changed, when a task has to go to whoever is around — had the
// audit log, which answers who *was* here, one line at a time.
//
// Presence is derived, never stored. It is read out of the live session map and
// nothing else: no new record, no event, nothing in a backup, and a restart
// simply has nobody present because a restart really does end every session
// (ADR-0044). That is deliberate — see the record for why an attendance history
// is a different thing that Atlas is not building by accident.
//
// Three states, from two timestamps a session already carries:
//
//   - online  — connected, and the person did something inside the window.
//   - idle    — connected, but nothing done inside the window. A tab left open.
//   - offline — nothing connected. Either no session at all, or one whose browser
//     stopped reporting: a closed laptop leaves a session behind for as long as
//     its TTL, and calling that "signed in" would be the answer that is wrong.
//
// Both halves are needed because either alone lies. Requests alone: the Console
// polls on its own, so a tab nobody is looking at keeps making them, and everyone
// would read as online forever. Interaction alone: a browser that is gone stops
// reporting interaction and stops reporting anything, and there would be no way
// to tell that from somebody who is present and reading.

// presenceWindow is how long after a person's last doing they still count as
// active, and how long a session may go unheard from before it counts as gone.
// One constant for both, because the browser's heartbeat (every 60s, whether the
// tab is in front or behind) is well inside it: a browser that has missed five
// minutes of heartbeats is not a slow one, it is closed.
const presenceWindow = 5 * time.Minute

// The three presence states, as they go over the wire and into the Console.
const (
	presenceOnline  = "online"
	presenceIdle    = "idle"
	presenceOffline = "offline"
)

// userPresence is one account's presence: the state, when it was last heard from
// and last used, and how many sessions it holds — two browsers are one person, but
// an administrator wondering where a session is signed in should be able to see
// that there are two.
type userPresence struct {
	UserID       string `json:"userId"`
	Username     string `json:"username,omitempty"`
	State        string `json:"state"`
	LastSeenAt   int64  `json:"lastSeenAt,omitempty"`
	LastActiveAt int64  `json:"lastActiveAt,omitempty"`
	Sessions     int    `json:"sessions,omitempty"`
}

// presenceState classifies one account's newest session. Connection is asked
// first: activity from a session that has since gone silent describes a browser
// that is no longer there, whatever it was doing five minutes ago.
func presenceState(lastSeen, lastActive, now time.Time) string {
	if now.Sub(lastSeen) > presenceWindow {
		return presenceOffline
	}
	if now.Sub(lastActive) > presenceWindow {
		return presenceIdle
	}
	return presenceOnline
}

// touch stamps a session from the browser's own report: connected always, and
// active when somebody is using it. It reports whether the token resolved to a
// live session — an unknown or expired one is not an error, because a beacon can
// arrive from a tab whose session was destroyed a moment ago and there is nothing
// the caller could do about it.
//
// The connection stamp is written here even though lookup has already written it
// for this same request. Depending on that would make the beacon correct only for
// as long as a cookie is the credential the middleware happens to resolve first.
func (s *sessionStore) touch(token string, active bool) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byToken[token]
	if !ok {
		return false
	}
	now := s.now()
	if !now.Before(sess.expires) {
		delete(s.byToken, token)
		return false
	}
	sess.lastSeen = now
	if active {
		sess.lastActive = now
	}
	s.byToken[token] = sess
	return true
}

// presenceByUser summarizes the live sessions of every signed-in account, keyed by
// user id. Expired sessions are dropped on the way through, the same self-cleaning
// lookup does, so this is also what keeps the map from growing for accounts that
// never log out.
//
// A user with no session is absent from the result rather than present as
// "offline": the caller holds the roster and knows who exists, and the difference
// between the two is a session this store has never had.
func (s *sessionStore) presenceByUser() map[string]userPresence {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	out := map[string]userPresence{}
	for tok, sess := range s.byToken {
		if !now.Before(sess.expires) {
			delete(s.byToken, tok)
			continue
		}
		p := out[sess.userID]
		p.UserID = sess.userID
		p.Username = sess.username
		p.Sessions++
		if sess.lastSeen.Unix() > p.LastSeenAt {
			p.LastSeenAt = sess.lastSeen.Unix()
		}
		if sess.lastActive.Unix() > p.LastActiveAt {
			p.LastActiveAt = sess.lastActive.Unix()
		}
		out[sess.userID] = p
	}
	// The state is computed from the aggregate, not per session, because presence is
	// of the person: somebody working in one tab is online even with three forgotten
	// ones behind it.
	for id, p := range out {
		p.State = presenceState(time.Unix(p.LastSeenAt, 0), time.Unix(p.LastActiveAt, 0), now)
		out[id] = p
	}
	return out
}

// handlePresenceBeacon is what the Console posts to say the tab is still open and,
// when the flag is set, that somebody is using it. Every signed-in caller may
// reach it and it reports on nobody: it stamps the caller's own session and
// answers ok, so the endpoint reveals nothing an unprivileged caller does not
// already know about themselves.
//
// A caller without a session — an API or OAuth token — is answered the same way
// rather than refused. It holds no session to stamp, presence is about people in
// the Console, and a 400 would only invite a client to retry.
func (s *Server) handlePresenceBeacon(w http.ResponseWriter, r *http.Request) {
	payload := struct {
		Active bool `json:"active"`
	}{}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxUserBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	// An empty body is a plain heartbeat: the tab is open, nobody claims anything
	// more. That is the request `fetch` sends with no body at all, and refusing it
	// would make the simplest form of the call the broken one.
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	// The session token, not the principal: the beacon stamps the browser it came
	// from, which is the thing that is or is not still open.
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.touch(c.Value, payload.Active)
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleUserPresence lists who is signed in right now, one entry per account with
// at least one live session, ordered by user id so a client can diff two reads.
// Admin-only, like the roster it annotates: who is at their desk is not something
// a colleague's task list needs to know (ADR-0228).
//
// It reads the session map only — no store, no run loop — which is what makes it
// cheap enough for the Console to re-read every half minute without reloading the
// page around it.
func (s *Server) handleUserPresence(w http.ResponseWriter, _ *http.Request) {
	byUser := s.sessions.presenceByUser()
	list := make([]userPresence, 0, len(byUser))
	for _, p := range byUser {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].UserID < list[j].UserID })
	httpapi.JSON(w, http.StatusOK, list)
}

// rosterUser is the admin roster's projection: a public user plus that account's
// presence, so the Organization page's first paint already shows who is here
// instead of flashing everyone offline until the first refresh. An account with no
// session carries the offline state explicitly — the roster is a complete list, and
// a missing field would render as an empty pill.
type rosterUser struct {
	publicUser
	Presence userPresence `json:"presence"`
}

// withPresence annotates a roster with what the session map knows.
func withPresence(users []publicUser, byUser map[string]userPresence) []rosterUser {
	out := make([]rosterUser, 0, len(users))
	for _, u := range users {
		p, ok := byUser[u.ID]
		if !ok {
			p = userPresence{UserID: u.ID, Username: u.Username, State: presenceOffline}
		}
		out = append(out, rosterUser{publicUser: u, Presence: p})
	}
	return out
}
