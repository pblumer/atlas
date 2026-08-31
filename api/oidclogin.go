package api

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// The federated login itself (ADR-0210).
//
// Two endpoints and one seam. `/auth/oidc/start` sends the person to their
// provider; `/auth/oidc/callback` takes what comes back, refuses it unless every
// check passes, and ends where a local login ends — in sessionStore.create, with
// the same roles and groups snapshot. That is the whole design: there is one place
// a session is born, so every rule written since ADR-0195 keeps holding without
// being re-proved for a second kind of caller.
//
// What a first login grants is `user` and nothing else, exactly as a locally
// created account gets (ADR-0209) — unless an operator turned the claim mapping on,
// in which case the token's claims decide the roles and the group membership as
// oidcmapping.go describes.

const (
	oidcStartPath    = "/auth/oidc/start"
	oidcCallbackPath = "/auth/oidc/callback"

	// oidcStateCookie carries the state value in the browser, so the callback can
	// require that the browser finishing the flow is the one that started it. The
	// state parameter alone proves only that somebody knows it.
	oidcStateCookie = "atlas_oidc"

	// oidcLoginWindow is how long a login may take from the redirect to the
	// callback. Long enough to type a password and answer a second factor, short
	// enough that an abandoned flow is not a standing invitation.
	oidcLoginWindow = 10 * time.Minute

	// oidcMaxPending bounds how many logins may be in flight at once.
	//
	// /auth/oidc/start is public by necessity — it is where a browser that is nobody
	// yet begins — so anybody who can reach the port can add an entry. The sweep on
	// each insert bounds them by time; this bounds them by count, because ten
	// minutes is a long time at request speed. Far more than any real instance has
	// in flight, and small enough that a flood costs kilobytes rather than a
	// process.
	oidcMaxPending = 4096

	// oidcFailedQuery is what the login screen is told when a federated login did
	// not work. It carries no detail: the reason is in the audit log, where an
	// operator can read it, and not in a URL the person could be sent by anybody.
	oidcFailedQuery = "?sso=failed"
)

// oidcPending is a login in flight: what the callback needs to finish the flow,
// and nothing that would be worth stealing on its own.
type oidcPending struct {
	nonce       string
	verifier    string
	redirectURI string
	expires     time.Time
}

// oidcStateStore holds logins in flight. Touched from handler goroutines, so it
// guards itself; never persisted, because a restart during a ten-minute login is a
// login somebody repeats.
type oidcStateStore struct {
	mu      sync.Mutex
	pending map[string]oidcPending
}

func newOIDCStateStore() *oidcStateStore {
	return &oidcStateStore{pending: map[string]oidcPending{}}
}

// begin records a login in flight under its state value, sweeping what has run out
// and, if the store is still full, dropping the login that has been waiting
// longest.
//
// Evicting rather than refusing is deliberate: a person starting a login now must
// get a slot, and the entry that loses is the one closest to expiring anyway. The
// alternative — refusing once full — would let a flood deny logins for as long as
// its entries live.
func (s *oidcStateStore) begin(state string, rec oidcPending, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.pending {
		if now.After(v.expires) {
			delete(s.pending, k)
		}
	}
	for len(s.pending) >= oidcMaxPending {
		oldest, at := "", time.Time{}
		for k, v := range s.pending {
			if at.IsZero() || v.expires.Before(at) {
				oldest, at = k, v.expires
			}
		}
		delete(s.pending, oldest)
	}
	s.pending[state] = rec
}

// spend resolves a state and removes it in the same critical section, so a
// callback replayed twice succeeds at most once.
func (s *oidcStateStore) spend(state string, now time.Time) (oidcPending, bool) {
	if state == "" {
		return oidcPending{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.pending[state]
	if !ok {
		return oidcPending{}, false
	}
	delete(s.pending, state)
	if now.After(rec.expires) {
		return oidcPending{}, false
	}
	return rec, true
}

// authProvider is what the login screen is told about a way in that is not a
// password. It names no secret: a client id is not one, and this endpoint is read
// before anybody is signed in.
type authProvider struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Start string `json:"start"`
}

// handleAuthProviders lists the identity providers this server offers. An empty
// list is the ordinary answer and the reason this endpoint exists: without it the
// login screen would have to guess, and a button that leads to a route nobody
// mounted is worse than no button.
func (s *Server) handleAuthProviders(w http.ResponseWriter, _ *http.Request) {
	out := []authProvider{}
	if s.oidc != nil {
		out = append(out, authProvider{ID: "oidc", Name: s.oidc.cfg.label(), Start: oidcStartPath})
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleOIDCStart sends the person to their provider.
//
// Everything that makes the callback safe is minted here and kept here: the state
// that ties the reply to this browser, the nonce that ties the token to this
// login, and the PKCE verifier whose hash travels while the value itself does not.
func (s *Server) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	d, err := s.oidc.endpoints(r.Context(), now)
	if err != nil {
		s.oidcRefuse(w, r, "provider discovery failed", err)
		return
	}
	state, err := randomHex(16)
	if err != nil {
		s.oidcRefuse(w, r, "generate state", err)
		return
	}
	nonce, err := randomHex(16)
	if err != nil {
		s.oidcRefuse(w, r, "generate nonce", err)
		return
	}
	verifier, err := randomHex(32)
	if err != nil {
		s.oidcRefuse(w, r, "generate verifier", err)
		return
	}
	base := s.externalBase(r)
	if base == "" {
		s.oidcRefuse(w, r, "no origin to build a redirect URI from", nil)
		return
	}
	redirectURI := base + oidcCallbackPath
	s.oidcStates.begin(state, oidcPending{
		nonce: nonce, verifier: verifier, redirectURI: redirectURI,
		expires: now.Add(oidcLoginWindow),
	}, now)

	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {s.oidc.cfg.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {s.oidc.cfg.scopes()},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    state,
		Path:     oidcCallbackPath,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(oidcLoginWindow.Seconds()),
	})
	sep := "?"
	if strings.Contains(d.AuthorizeURL, "?") {
		sep = "&"
	}
	http.Redirect(w, r, d.AuthorizeURL+sep+q.Encode(), http.StatusFound)
}

// handleOIDCCallback finishes a federated login.
//
// Every failure lands in the same place: the login screen, with nothing said about
// why. The reason is in the audit log, because it is an operator's to read and not
// a stranger's to probe for.
func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	clearOIDCStateCookie(w, r)
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		s.oidcRefuse(w, r, "the provider refused: "+e, nil)
		return
	}
	state := q.Get("state")
	cookie, err := r.Cookie(oidcStateCookie)
	if err != nil || cookie.Value == "" || cookie.Value != state {
		// The reply came back to a browser that did not start this login, which is
		// what a cross-site attempt looks like from here.
		s.oidcRefuse(w, r, "state does not match this browser", nil)
		return
	}
	pending, ok := s.oidcStates.spend(state, now)
	if !ok {
		s.oidcRefuse(w, r, "no login in flight for this state", nil)
		return
	}
	code := q.Get("code")
	if code == "" {
		s.oidcRefuse(w, r, "the provider returned no code", nil)
		return
	}

	idToken, err := s.oidc.exchange(r.Context(), code, pending.verifier, pending.redirectURI, now)
	if err != nil {
		s.oidcRefuse(w, r, "token exchange failed", err)
		return
	}
	claims, err := s.verifyOIDCToken(r, idToken, pending.nonce, now)
	if err != nil {
		s.oidcRefuse(w, r, "id token refused", err)
		return
	}

	u, groupChanges, err := s.oidcAccount(claims, now.Unix())
	if err != nil {
		s.oidcRefuse(w, r, "resolve account", err)
		return
	}
	// What the mapping decided has to reach the person's *other* sessions and their
	// standing grants too, or a change at the provider would apply to this browser
	// and nowhere else (ADR-0185, ADR-0200). The session created below gets the
	// current picture by construction; these two carry it to everything else the
	// person is already holding.
	for _, c := range groupChanges {
		s.sessions.setUserGroupMembership(u.ID, c.groupID, c.member)
		s.setGrantGroupMembership(u.ID, c.groupID, c.member)
	}
	s.setUserGrantRoles(u.ID, u.Roles)
	if u.Disabled {
		// Disabling is how an account is taken away before the provider knows about
		// it. A second door that ignored it would make the first one decoration.
		auditRefusal(r, logging.AuthLoginFailed, "failed login",
			slog.String("username", u.Username), slog.String("source", SourceOIDC),
			slog.String("reason", "account disabled"))
		http.Redirect(w, r, "/"+oidcFailedQuery, http.StatusFound)
		return
	}

	var (
		groupIDs []string
		grpErr   error
	)
	s.do(func() { groupIDs, grpErr = s.groups.idsForUser(u.ID) })
	if grpErr != nil {
		s.oidcRefuse(w, r, "read groups", grpErr)
		return
	}
	token, err := s.sessions.create(u, groupIDs)
	if err != nil {
		s.oidcRefuse(w, r, "create session", err)
		return
	}
	setSessionCookie(w, r, token, s.sessions.ttl)
	audit(r, logging.AuthLogin, "login",
		slog.String("username", u.Username), slog.String("user_id", u.ID),
		slog.String("source", SourceOIDC))
	http.Redirect(w, r, "/", http.StatusFound)
}

// verifyOIDCToken checks an ID token, refetching the provider's keys once when it
// names a key id that is not held.
//
// A rotation at the provider is otherwise an outage here until the cache expires,
// and rotating keys is a thing providers are supposed to do.
func (s *Server) verifyOIDCToken(r *http.Request, idToken, nonce string, now time.Time) (oidcClaims, error) {
	want := oidcExpect{
		issuer:   s.oidc.cfg.Issuer,
		clientID: s.oidc.cfg.ClientID,
		nonce:    nonce,
		now:      now,
	}
	keys, err := s.oidc.keySet(r.Context(), now, false)
	if err != nil {
		return oidcClaims{}, err
	}
	claims, err := verifyIDToken(idToken, keys, want)
	if err == nil || !strings.Contains(err.Error(), "signing key") {
		return claims, err
	}
	fresh, ferr := s.oidc.keySet(r.Context(), now, true)
	if ferr != nil {
		return oidcClaims{}, err
	}
	return verifyIDToken(idToken, fresh, want)
}

// groupChange is one group membership a federated login decided, so the caller can
// push it into the person's live sessions and standing grants after the run-loop
// turn that wrote it (ADR-0185).
type groupChange struct {
	groupID string
	member  bool
}

// oidcAccount resolves the account a validated token belongs to, creating one on a
// first login and applying the claim mapping when an operator turned it on.
//
// The link is the subject, never the email address: an address can be reassigned
// to a different person at the provider, and linking by it would hand that person
// somebody else's account. For the same reason a matching local account is *not*
// adopted — a federated login creates its own record, and joining the two is an
// administrator's deliberate act rather than a coincidence of spelling.
func (s *Server) oidcAccount(claims oidcClaims, now int64) (User, []groupChange, error) {
	var (
		out     User
		changes []groupChange
		err     error
	)
	s.do(func() {
		var mapping oidcMapping
		if mapping, err = s.settings.getOIDCMapping(); err != nil {
			return
		}
		var found bool
		out, found, err = s.users.byExternalID(SourceOIDC, claims.Subject)
		if err != nil {
			return
		}
		if !found {
			var id string
			if id, err = newUserID(); err != nil {
				return
			}
			var username string
			if username, err = s.freeUsername(oidcUsername(claims)); err != nil {
				return
			}
			out = User{
				ID:              id,
				Username:        username,
				Roles:           []string{RoleUser},
				Source:          SourceOIDC,
				ExternalID:      claims.Subject,
				CreatedAt:       now,
				UpdatedAt:       now,
				RolesUpgradedAt: now,
			}
		}
		// Keep the display material current: the provider is the source of truth for
		// it, and a stale name in a task list is a small lie that accumulates. The
		// username is left alone — it identifies sessions, assignments and audit lines.
		changed := applyOIDCProfile(&out, claims) || !found
		if mapping.Enabled {
			roles, groups := mapping.apply(claimValues(claims.Raw, mapping.Claim))
			next := withUserFloor(roles)
			if !sameRoles(out.Roles, next) {
				out.Roles, changed = next, true
			}
			if changes, err = s.syncOIDCGroups(out.ID, groups, mapping.namedGroups(), now); err != nil {
				return
			}
		}
		if !changed {
			return
		}
		out.UpdatedAt = now
		if err = s.users.Save(out); err != nil {
			return
		}
		if !found {
			logging.Info(logging.AuthUserCreated, "account created by a federated login",
				slog.String("username", out.Username), slog.String("user_id", out.ID),
				slog.String("source", SourceOIDC), slog.Any("roles", out.Roles))
		}
	})
	return out, changes, err
}

// syncOIDCGroups makes the person's membership of the groups the mapping owns
// exactly what this login's claims say — joining what they name and leaving the
// rest of the owned set.
//
// Leaving is the half that matters. Adding a group on a claim is convenience;
// removing one when the claim goes away is the reason a provider is worth
// federating with at all, and a rule that only ever added would leave a leaver
// holding every project their old team shares.
//
// `owned` is what bounds that: the groups the mapping names anywhere. A group no
// rule mentions is not the provider's business, so a membership somebody added by
// hand survives (see the oidcmapping.go file comment).
//
// It runs inside a run-loop turn, so it writes the group records directly; the
// changes it returns are what the caller pushes into live sessions and grants.
func (s *Server) syncOIDCGroups(userID string, want, owned []string, now int64) ([]groupChange, error) {
	all, err := s.groups.LoadAll()
	if err != nil {
		return nil, err
	}
	wanted, mine := map[string]bool{}, map[string]bool{}
	for _, id := range want {
		wanted[id] = true
	}
	for _, id := range owned {
		mine[id] = true
	}
	var changes []groupChange
	for _, g := range all {
		if !mine[g.ID] {
			continue
		}
		has, should := g.hasMember(userID), wanted[g.ID]
		if has == should {
			continue
		}
		if should {
			g.Members = append(g.Members, userID)
		} else {
			kept := make([]string, 0, len(g.Members))
			for _, m := range g.Members {
				if m != userID {
					kept = append(kept, m)
				}
			}
			g.Members = kept
		}
		g.UpdatedAt = now
		if err := s.groups.Save(g); err != nil {
			return nil, err
		}
		changes = append(changes, groupChange{groupID: g.ID, member: should})
	}
	return changes, nil
}

// sameRoles reports whether two role lists say the same thing in the same order,
// which is all that decides whether the record needs rewriting.
func sameRoles(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// applyOIDCProfile copies the display material a token carries onto a record,
// reporting whether anything changed.
func applyOIDCProfile(u *User, claims oidcClaims) bool {
	changed := false
	if name := strings.TrimSpace(claims.Name); name != "" && name != u.DisplayName {
		u.DisplayName, changed = name, true
	}
	if mail := strings.TrimSpace(claims.Email); mail != "" && mail != u.Email {
		u.Email, changed = mail, true
	}
	return changed
}

// oidcUsername proposes a username for a new federated account: what the provider
// calls the person, then the local part of their address, and a name derived from
// the subject when it offers neither.
func oidcUsername(claims oidcClaims) string {
	for _, candidate := range []string{claims.PreferredUsername, claims.Email} {
		if name := sanitizeUsername(candidate); name != "" {
			return name
		}
	}
	if name := sanitizeUsername(claims.Subject); name != "" {
		return name
	}
	return "sso-user"
}

// sanitizeUsername reduces a claim to something usable as a username: the part
// before any "@", lowercased, with anything that is not a letter, digit, dot,
// dash or underscore dropped.
func sanitizeUsername(in string) string {
	name := strings.ToLower(strings.TrimSpace(in))
	if at := strings.Index(name, "@"); at > 0 {
		name = name[:at]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// freeUsername returns want, or want with a number appended until nothing holds
// it. It runs inside a run-loop turn, where a concurrent create cannot slip in
// between the check and the write.
func (s *Server) freeUsername(want string) (string, error) {
	for i := 0; i < 100; i++ {
		candidate := want
		if i > 0 {
			candidate = want + "-" + strconv.Itoa(i+1)
		}
		_, taken, err := s.users.byUsername(candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", errors.New("oidc: no free username derived from the provider's claims")
}

// oidcRefuse records why a federated login failed and sends the person back to the
// login screen. The response says nothing beyond "it did not work", for the reason
// a failed password says nothing: the wire must not become a place to learn things.
func (s *Server) oidcRefuse(w http.ResponseWriter, r *http.Request, what string, err error) {
	attrs := []slog.Attr{
		slog.String("source", SourceOIDC),
		slog.String("reason", what),
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	auditRefusal(r, logging.AuthLoginFailed, "federated login refused", attrs...)
	http.Redirect(w, r, "/"+oidcFailedQuery, http.StatusFound)
}

// clearOIDCStateCookie expires the state cookie: the login it belonged to is over,
// whichever way it ended.
func clearOIDCStateCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    "",
		Path:     oidcCallbackPath,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   -1,
	})
}
