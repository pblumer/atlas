package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/model"
)

// A password is set by its owner, through a one-time link
// (ADR-0526).
//
// Nobody chooses a password for somebody else any more: not an administrator approving a
// registration, not a process, not a script mailing an initial one. An account is created
// without a password, and its owner is sent a link to set one; somebody who forgot theirs
// asks for a new link on the login screen. The link is
//
//	{external URL}/public/password#{user id, base64url}.{expiry, unix s}.{mac}
//
// where mac is an HMAC, under a key the vault derives for this purpose alone, of the user
// id, the expiry, the account's e-mail and its current password hash. Nothing per link is
// stored. Because the hash is signed, setting a password — through the link or any other
// way — kills every link minted before; because the address is signed, so does correcting
// it. The token is in the fragment, which a browser never sends, so it is in no access
// log and no Referer: the page sends it in an Authorization header. Opening the link
// changes nothing; only the POST of a new password does.

// passwordPagePath is the static page a password link opens.
const passwordPagePath = "/public/password"

// passwordLinkPurpose names the vault-derived key password links are signed with. The
// version is in it so a change to what is signed can never accept an old link.
const passwordLinkPurpose = "atlas/password-link/v1"

// passwordMACLength is how many bytes of the HMAC a link carries: 128 bits, hex.
const passwordMACLength = 16

// How long a link holds. An account that has never had a password is being activated —
// its owner may read the mail days later — while a reset is asked for by somebody
// waiting for it.
const (
	activationLinkTTL = 7 * 24 * time.Hour
	resetLinkTTL      = time.Hour
)

// maxPasswordBytes is bcrypt's limit: it ignores everything past 72 bytes, and the Go
// implementation refuses the input outright. Saying so at the edge beats a 500.
const maxPasswordBytes = 72

// The process that mails a password link to an account's owner, for a forgotten password
// or an administrator's invitation. It is a system process (ADR-0122), so its texts are
// modelled and its failures are incidents.
const (
	passwordLinkProcessID = "proc_passwort_link"
	passwordAnlassForgot  = "vergessen"
	passwordAnlassInvite  = "einladung"
)

// errNoPasswordLink is a token that opens nothing; every refusal answers 404 alike.
var errNoPasswordLink = errors.New("this link is not valid (any more)")

// isLocalAccount reports whether u signs in with a password this server holds. A record
// written before identity sources existed carries none and is local.
func isLocalAccount(u User) bool { return u.Source == "" || u.Source == SourceLocal }

// validateNewPassword is the rule every password must meet, wherever it is set.
func validateNewPassword(pw, username string) error {
	switch {
	case utf8.RuneCountInString(pw) < minPasswordLen:
		return fmt.Errorf("password must be at least %d characters", minPasswordLen)
	case len(pw) > maxPasswordBytes:
		return fmt.Errorf("password must be at most %d bytes", maxPasswordBytes)
	case username != "" && strings.EqualFold(strings.TrimSpace(pw), strings.TrimSpace(username)):
		return errors.New("password must not be the username")
	}
	return nil
}

// passwordMAC is the signature of a link for u that holds until expiry.
func (s *Server) passwordMAC(u User, expiry int64) ([]byte, error) {
	if s.vault == nil {
		return nil, errors.New("password links are signed with a key the vault derives, and this server runs without a vault (--vault=false)")
	}
	m := hmac.New(sha256.New, s.vault.DerivedKey(passwordLinkPurpose))
	parts := []string{u.ID, strconv.FormatInt(expiry, 10), strings.ToLower(strings.TrimSpace(u.Email)), u.PasswordHash}
	if u.CredentialEpoch > 0 {
		parts = append(parts, strconv.FormatUint(u.CredentialEpoch, 10))
	}
	for _, part := range parts {
		m.Write([]byte(part))
		m.Write([]byte{0})
	}
	return m.Sum(nil)[:passwordMACLength], nil
}

// passwordLinkTTL is how long a link minted for u now holds.
func passwordLinkTTL(u User) time.Duration {
	if u.PasswordHash == "" {
		return activationLinkTTL
	}
	return resetLinkTTL
}

// passwordToken mints the token of a link for u, and the moment it stops holding.
func (s *Server) passwordToken(u User, now time.Time) (string, int64, error) {
	if !isLocalAccount(u) {
		return "", 0, fmt.Errorf("%q signs in through its identity provider and has no password here", u.Username)
	}
	if u.Disabled {
		return "", 0, fmt.Errorf("%q is disabled", u.Username)
	}
	expiry := now.Add(passwordLinkTTL(u)).Unix()
	mac, err := s.passwordMAC(u, expiry)
	if err != nil {
		return "", 0, err
	}
	return base64.RawURLEncoding.EncodeToString([]byte(u.ID)) + "." + strconv.FormatInt(expiry, 10) + "." +
		hex.EncodeToString(mac), expiry, nil
}

// passwordLinkFor is the whole link for u, on the operator's configured origin, and the
// moment it stops holding.
func (s *Server) passwordLinkFor(u User, now time.Time) (string, int64, error) {
	base := strings.TrimRight(s.externalURL, "/")
	if base == "" {
		return "", 0, errors.New("password links are built on the server's external URL, and none is configured (--external-url or ATLAS_EXTERNAL_URL)")
	}
	tok, expiry, err := s.passwordToken(u, now)
	if err != nil {
		return "", 0, err
	}
	return base + passwordPagePath + "#" + tok, expiry, nil
}

// PasswordLink mints the link a mail task of processID carries for the account named
// username ([mail.PasswordLinker]). It is refused unless the process is a system process,
// and unless the mail goes to the account's own address and to nobody beside it: a model
// — even a modified system process — can send somebody's link only to the mailbox that
// already is their recovery anchor. Runs on the loop.
func (d mailDirectory) PasswordLink(processDefKey uint64, processID, username string, to, cc, bcc []string) (string, error) {
	// The deployment, not the process id, must be the system project's: any model may
	// call its process proc_passwort_link (systemprocessguard.go).
	if !d.s.isSystemDefKey(processDefKey) {
		return "", fmt.Errorf("process %q is not a system process; only the system project mails password links", processID)
	}
	if username == "" {
		return "", errors.New("passwordFor resolved no account")
	}
	u, ok, err := d.s.users.byUsername(username)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no account named %q", username)
	}
	addr := strings.TrimSpace(u.Email)
	if addr == "" {
		return "", fmt.Errorf("%q has no e-mail address to send its link to", u.Username)
	}
	if len(cc) > 0 || len(bcc) > 0 || len(to) != 1 || !strings.EqualFold(strings.TrimSpace(to[0]), addr) {
		return "", fmt.Errorf("a password link goes to the account's own address and to nobody else; address the mail to %q alone (to=\"=benutzername\" does)", u.Username)
	}
	link, _, err := d.s.passwordLinkFor(u, time.Now())
	if err != nil {
		return "", err
	}
	logging.Info(logging.AuthPasswordLinkIssued, "password link mailed",
		slog.String("user_id", u.ID), slog.String("process", processID))
	return link, nil
}

// passwordAccountOf reads and checks the token in the request's Authorization header,
// "Bearer {token}", and returns the account it opens. The shape is checked before
// anything is decoded, the expiry before the store is read, and the signature against
// the account as it is now — which is what makes a used link, or one sent before the
// address changed, open nothing.
func (s *Server) passwordAccountOf(r *http.Request, now time.Time) (User, int64, bool) {
	raw, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found {
		return User{}, 0, false
	}
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 3 || len(parts[0]) == 0 || len(parts[0]) > 128 || len(parts[2]) != 2*passwordMACLength {
		return User{}, 0, false
	}
	id, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(id) == 0 {
		return User{}, 0, false
	}
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || expiry <= now.Unix() {
		return User{}, 0, false
	}
	got, err := hex.DecodeString(parts[2])
	if err != nil {
		return User{}, 0, false
	}
	u, ok, err := s.users.Get(string(id))
	if err != nil || !ok || u.Disabled || !isLocalAccount(u) {
		return User{}, 0, false
	}
	want, err := s.passwordMAC(u, expiry)
	if err != nil || !hmac.Equal(got, want) {
		return User{}, 0, false
	}
	return u, expiry, true
}

// passwordLinkState is what the page is told about the link it opened.
type passwordLinkState struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName,omitempty"`
	// Purpose is "activate" for an account that has never had a password, "reset" for
	// one that has.
	Purpose   string `json:"purpose"`
	ExpiresAt int64  `json:"expiresAt"`
}

func passwordPurpose(u User) string {
	if u.PasswordHash == "" {
		return "activate"
	}
	return "reset"
}

// handlePasswordPage serves the page a password link opens. It is static: the token is
// in the fragment, which never reaches this handler.
func (s *Server) handlePasswordPage(w http.ResponseWriter, r *http.Request) {
	if !s.publicRate.allow(httpapi.ClientIP(r)) {
		httpapi.Error(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	page, err := webFS.ReadFile("web/public-password.html")
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "public page missing")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	setNoFraming(h)
	_, _ = w.Write(page)
}

// handlePasswordLinkState answers GET /public/password/link: whom the link is for and
// whether it activates an account or resets a password. It answers and changes nothing,
// so a link scanner that opens the link changes nothing either.
func (s *Server) handlePasswordLinkState(w http.ResponseWriter, r *http.Request) {
	if !s.publicRate.allow(httpapi.ClientIP(r)) {
		httpapi.Error(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	u, expiry, ok := s.passwordAccountOf(r, time.Now())
	if !ok {
		httpapi.Error(w, http.StatusNotFound, errNoPasswordLink.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.JSON(w, http.StatusOK, passwordLinkState{
		Username: u.Username, DisplayName: u.DisplayName, Purpose: passwordPurpose(u), ExpiresAt: expiry,
	})
}

// handlePasswordLinkSet answers POST /public/password/link: the holder of the link sets
// the account's password. The link is checked again on the loop against the account as
// it is there, so of two posts racing with one link the second finds it dead. Every
// session of the account ends: whoever was signed in as it before the reset is not any
// more.
func (s *Server) handlePasswordLinkSet(w http.ResponseWriter, r *http.Request) {
	if !s.publicRate.allow(httpapi.ClientIP(r)) {
		httpapi.Error(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	var payload struct {
		Password string `json:"password"`
	}
	if !s.decodeJSONBody(w, r, &payload) {
		return
	}
	now := time.Now()
	u, _, ok := s.passwordAccountOf(r, now)
	if !ok {
		httpapi.Error(w, http.StatusNotFound, errNoPasswordLink.Error())
		return
	}
	if err := validateNewPassword(payload.Password, u.Username); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := hashPassword(payload.Password)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "set password: "+err.Error())
		return
	}
	purpose := passwordPurpose(u)
	var (
		stale bool
		opErr error
	)
	s.do(func() {
		cur, _, ok := s.passwordAccountOf(r, now)
		if !ok || cur.ID != u.ID {
			stale = true
			return
		}
		cur.PasswordHash = hash
		cur.UpdatedAt = now.Unix()
		opErr = s.users.Save(cur)
	})
	switch {
	case stale:
		httpapi.Error(w, http.StatusNotFound, errNoPasswordLink.Error())
		return
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "set password: "+opErr.Error())
		return
	}
	s.sessions.destroyUser(u.ID)
	s.logins.forgive(u.Username)
	audit(r, logging.AuthPasswordLinkUsed, "password set through a link",
		slog.String("user_id", u.ID), slog.String("purpose", purpose))
	httpapi.JSON(w, http.StatusOK, map[string]any{"username": u.Username, "purpose": purpose})
}

// passwordRecovery reports whether this server can mail a password link at all: it signs
// with the vault, builds on the external URL, and sends through the system process.
// Read on the loop for the deployment registry.
func (s *Server) passwordRecoveryReady() bool {
	if s.vault == nil || strings.TrimSpace(s.externalURL) == "" {
		return false
	}
	ready := false
	s.do(func() { ready = s.latestDeploymentOf(passwordLinkProcessID) != nil })
	return ready
}

// handlePasswordRecovery answers GET /api/v1/auth/password-recovery: whether the login
// screen offers "Passwort vergessen". Public, like the registration link it sits beside.
func (s *Server) handlePasswordRecovery(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, map[string]any{"enabled": s.passwordRecoveryReady()})
}

// handlePasswordForgot answers POST /public/password/forgot. It always answers 202 with
// the same body, whether the username or address names an account or not, so the form
// cannot be read as a directory. Only when it names an enabled local account with an
// address — and that account was not sent a link in the last minutes — does the server
// start the system process that mails the link.
func (s *Server) handlePasswordForgot(w http.ResponseWriter, r *http.Request) {
	ip := httpapi.ClientIP(r)
	if !s.publicRate.allow(ip) || !s.recoveryRate.allow(ip) {
		httpapi.Error(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	var payload struct {
		Login string `json:"login"`
	}
	if !s.decodeJSONBody(w, r, &payload) {
		return
	}
	accepted := map[string]any{"status": "accepted"}
	login := strings.TrimSpace(payload.Login)
	if login == "" || len(login) > 320 {
		httpapi.JSON(w, http.StatusAccepted, accepted)
		return
	}
	u, ok, err := s.users.byUsername(login)
	if err == nil && !ok && strings.Contains(login, "@") {
		u, ok, err = s.users.byEmail(login)
	}
	switch {
	case err != nil:
		logging.Warn(logging.AuthPasswordRecovery, "password recovery: could not read the account store", slog.String("error", err.Error()))
	case !ok || u.Disabled || !isLocalAccount(u) || strings.TrimSpace(u.Email) == "":
		// Nothing to send, and nothing said about why.
	case !s.passwordMails.allow(u.ID):
		auditRefusal(r, logging.AuthPasswordRecovery, "password recovery: a link was mailed to this account moments ago",
			slog.String("user_id", u.ID))
	default:
		if _, err := s.startPasswordLinkProcess(u, passwordAnlassForgot); err != nil {
			logging.Warn(logging.AuthPasswordRecovery, "password recovery: the link could not be sent",
				slog.String("user_id", u.ID), slog.String("error", err.Error()))
		} else {
			audit(r, logging.AuthPasswordRecovery, "password recovery: link requested", slog.String("user_id", u.ID))
		}
	}
	httpapi.JSON(w, http.StatusAccepted, accepted)
}

// startPasswordLinkProcess starts the system process that mails u its password link.
// The instance carries the username and why — never the link, which the mail task mints
// when it is resolved and which is written nowhere.
func (s *Server) startPasswordLinkProcess(u User, anlass string) (uint64, error) {
	var key uint64
	s.do(func() {
		if d := s.latestDeploymentOf(passwordLinkProcessID); d != nil {
			key = d.Key
		}
	})
	if key == 0 {
		return 0, fmt.Errorf("the system process %s is not deployed", passwordLinkProcessID)
	}
	vars := []model.VariableValue{
		{Name: "benutzername", Kind: model.VarString, Text: u.Username},
		{Name: "anlass", Kind: model.VarString, Text: anlass},
	}
	var instKey uint64
	s.do(func() { s.proc.CreateInstanceReporting(key, &instKey, vars...) })
	if err := s.drive(); err != nil {
		return 0, err
	}
	return instKey, nil
}

// handleChangeOwnPassword answers POST /api/v1/auth/password: a signed-in person changes
// their own password. It asks for the current one — a session left open on a shared
// screen must not be enough to take the account — and is throttled like a login, because
// it is one. Every other session of the account ends.
func (s *Server) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil || !p.Session {
		httpapi.Error(w, http.StatusForbidden, "only a signed-in person changes their own password")
		return
	}
	var payload struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !s.decodeJSONBody(w, r, &payload) {
		return
	}
	if !s.logins.allow(httpapi.ClientIP(r), p.Username) {
		auditRefusal(r, logging.AuthLoginThrottled, "password change throttled", slog.String("username", p.Username))
		httpapi.Error(w, http.StatusTooManyRequests, "too many attempts; try again shortly")
		return
	}
	u, ok, err := s.users.Get(p.UserID)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "change password: "+err.Error())
		return
	}
	if !ok || u.Disabled || !isLocalAccount(u) {
		httpapi.Error(w, http.StatusConflict, "this account has no password here; it signs in through its identity provider")
		return
	}
	if !checkPassword(u.PasswordHash, payload.CurrentPassword) {
		auditRefusal(r, logging.AuthPasswordChanged, "password change refused: wrong current password", slog.String("user_id", u.ID))
		httpapi.Error(w, http.StatusBadRequest, "the current password is not correct")
		return
	}
	if err := validateNewPassword(payload.NewPassword, u.Username); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if payload.NewPassword == payload.CurrentPassword {
		httpapi.Error(w, http.StatusBadRequest, "the new password is the current one")
		return
	}
	hash, err := hashPassword(payload.NewPassword)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "change password: "+err.Error())
		return
	}
	var (
		changed bool
		opErr   error
	)
	s.do(func() {
		cur, ok, e := s.users.Get(u.ID)
		if e != nil || !ok {
			opErr = e
			return
		}
		// Changed meanwhile — by a link, by an administrator: what was checked above
		// is not the password any more.
		if cur.PasswordHash != u.PasswordHash {
			return
		}
		cur.PasswordHash = hash
		cur.UpdatedAt = time.Now().Unix()
		if opErr = s.users.Save(cur); opErr == nil {
			changed = true
		}
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "change password: "+opErr.Error())
		return
	case !changed:
		httpapi.Error(w, http.StatusConflict, "the password changed meanwhile; sign in again")
		return
	}
	keep := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		keep = c.Value
	}
	s.sessions.destroyUserExcept(u.ID, keep)
	s.logins.forgive(u.Username)
	audit(r, logging.AuthPasswordChanged, "password changed by its owner", slog.String("user_id", u.ID))
	httpapi.JSON(w, http.StatusOK, map[string]any{"id": u.ID})
}

// handleUserPasswordLink answers POST /api/v1/users/{id}/password-link: an administrator
// has the account sent its link ({"send": true}), or takes the link to hand over in
// person — on an instance with no mail — and never chooses a password themselves.
//
// With {"revoke": true} the account's current password stops working first: for an
// account whose password is known to somebody else — typed by an administrator, mailed,
// read off a log — a link alone would leave that password valid until the owner uses
// it. The account then awaits activation, every session ends and its OAuth grants are
// revoked, and the link it is sent is an activation link (ADR-0526, amendment of
// 2026-11-04).
func (s *Server) handleUserPasswordLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var payload struct {
		Send   bool `json:"send"`
		Revoke bool `json:"revoke"`
	}
	if !s.decodeJSONBody(w, r, &payload) {
		return
	}
	u, ok, err := s.users.Get(id)
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "password link: "+err.Error())
		return
	case !ok:
		httpapi.Error(w, http.StatusNotFound, "no user with that id")
		return
	case !isLocalAccount(u):
		httpapi.Error(w, http.StatusConflict, "this account signs in through its identity provider and has no password here")
		return
	case u.Disabled:
		httpapi.Error(w, http.StatusConflict, "this account is disabled; enable it first")
		return
	}
	// Everything the link needs is checked before a password is revoked, so a refusal
	// never leaves an account locked with no link on its way.
	if payload.Send {
		if strings.TrimSpace(u.Email) == "" {
			httpapi.Error(w, http.StatusConflict, "this account has no e-mail address; give it one, or take the link and hand it over in person")
			return
		}
		if s.vault == nil || strings.TrimSpace(s.externalURL) == "" {
			httpapi.Error(w, http.StatusConflict, "password links need the vault and the server's external URL (--external-url)")
			return
		}
		if payload.Revoke && !s.passwordRecoveryReady() {
			httpapi.Error(w, http.StatusConflict, "the system process "+passwordLinkProcessID+" is not deployed; take the link and hand it over in person")
			return
		}
	} else if s.vault == nil {
		httpapi.Error(w, http.StatusConflict, "password links are signed with a key the vault derives, and this server runs without a vault (--vault=false)")
		return
	}
	if payload.Revoke {
		revoked, status, err := s.revokePassword(r, u)
		if err != nil {
			httpapi.Error(w, status, "revoke password: "+err.Error())
			return
		}
		u = revoked
	}
	if payload.Send {
		inst, err := s.startPasswordLinkProcess(u, passwordAnlassInvite)
		if err != nil {
			httpapi.Error(w, http.StatusConflict, "password link: "+err.Error())
			return
		}
		audit(r, logging.AuthPasswordLinkIssued, "password link mailed by an administrator",
			slog.String("user_id", u.ID), slog.Uint64("instance", inst), slog.Bool("revoked", payload.Revoke))
		httpapi.JSON(w, http.StatusAccepted, map[string]any{"sent": true, "instanceKey": inst, "revoked": payload.Revoke})
		return
	}
	tok, expiry, err := s.passwordToken(u, time.Now())
	if err != nil {
		httpapi.Error(w, http.StatusConflict, "password link: "+err.Error())
		return
	}
	path := passwordPagePath + "#" + tok
	resp := map[string]any{"path": path, "expiresAt": expiry, "purpose": passwordPurpose(u), "revoked": payload.Revoke}
	if base := strings.TrimRight(s.externalURL, "/"); base != "" {
		resp["url"] = base + path
	}
	audit(r, logging.AuthPasswordLinkIssued, "password link handed to an administrator", slog.String("user_id", u.ID))
	w.Header().Set("Cache-Control", "no-store")
	httpapi.JSON(w, http.StatusOK, resp)
}

// revokePassword makes u's current password stop working: the account awaits activation,
// every session ends and its OAuth grants are revoked. It refuses the caller's own
// account — "Change password" is how a person replaces theirs — and the last enabled
// administrator who could still sign in, which would leave the instance with nobody to
// administer it until somebody runs atlas reset-password on the server. It returns the
// account as stored and, on a refusal, the status to answer with.
func (s *Server) revokePassword(r *http.Request, u User) (User, int, error) {
	if p := httpapi.PrincipalFrom(r.Context()); p != nil && p.UserID == u.ID {
		return u, http.StatusConflict, errors.New("this is your own account; change your password under the account menu instead")
	}
	var (
		out    User
		status = http.StatusInternalServerError
		opErr  error
	)
	s.do(func() {
		cur, ok, err := s.users.Get(u.ID)
		if err != nil || !ok {
			status, opErr = http.StatusNotFound, errors.New("no user with that id")
			if err != nil {
				status, opErr = http.StatusInternalServerError, err
			}
			return
		}
		if cur.hasRole(RoleAdmin) {
			all, err := s.users.LoadAll()
			if err != nil {
				opErr = err
				return
			}
			others := 0
			for _, o := range all {
				if o.ID != cur.ID && !o.Disabled && o.hasRole(RoleAdmin) && (o.PasswordHash != "" || !isLocalAccount(o)) {
					others++
				}
			}
			if others == 0 {
				status, opErr = http.StatusConflict, errors.New("this is the last administrator who can sign in; revoking their password would leave nobody to administer the instance (atlas reset-password on the server is the way back)")
				return
			}
		}
		cur.PasswordHash = ""
		// The empty hash is the state an activation link was signed over; the new
		// epoch is what keeps such a link — used or not — from opening the account
		// again (ADR-0526, amendment of 2026-11-04).
		cur.CredentialEpoch++
		cur.UpdatedAt = time.Now().Unix()
		if opErr = s.users.Save(cur); opErr == nil {
			out = cur
		}
	})
	if opErr != nil {
		return u, status, opErr
	}
	s.sessions.destroyUser(out.ID)
	s.revokeUserGrants(out.ID)
	audit(r, logging.AuthPasswordRevoked, "password revoked by an administrator", slog.String("user_id", out.ID))
	return out, 0, nil
}

// ownWorker reports whether the request is the server's own worker credential — the one
// it hands the workers it supervises — or comes from a server that enforces no
// authentication at all, where every caller is everybody.
func (s *Server) ownWorker(r *http.Request) bool {
	if !s.authEnabled {
		return true
	}
	p := httpapi.PrincipalFrom(r.Context())
	return p != nil && p.UserID == servicePrincipalName
}

// carriesPasswordLink reports whether the job jobKey is a mail task that names an account
// with passwordFor. Runs on the loop.
func (s *Server) carriesPasswordLink(jobKey uint64) bool {
	jv, ok, err := s.store.GetJob(jobKey)
	if err != nil || !ok || jv.JobType != compiler.MailJobTypeIndex {
		return false
	}
	ei, ok, err := s.store.GetElementInstance(jv.ElementInstanceKey)
	if err != nil || !ok {
		return false
	}
	d, ok := s.deployments[ei.ProcessDefKey]
	if !ok || d.cp == nil {
		return false
	}
	_, carries := d.cp.PasswordFor(ei.ElementId)
	return carries
}
