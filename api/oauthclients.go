package api

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// Administering the authorization server: which applications may ask, and which
// approvals stand (ADR-0200).
//
// Registration is over the API and not (yet) in the Console, which is where API
// tokens landed too (ADR-0194) and for the same reason: the credential surface is
// worth getting right before the screen for it. A Console page is the natural
// follow-up and changes nothing here.
//
// The two halves are administered differently on purpose. A **client** is an
// operator's decision about the installation, so registering and removing one is
// admin-only. A **grant** is a person's own decision about their own access, so
// they can see and revoke their own; an administrator sees and revokes all of
// them, which is what makes "cut off that application" something one person can do.

// newOAuthClientResp is the registration response: the only time the secret exists
// outside the caller's memory.
type newOAuthClientResp struct {
	oauthClientView
	Secret string `json:"secret"`
}

// loadOAuth fills the in-memory indexes from the durable records. Runs before the
// loop serves traffic, so touching the indexes directly is safe.
func (s *Server) loadOAuth() error {
	clients, err := s.oauthClientStore.LoadAll()
	if err != nil {
		return err
	}
	s.oauthClients.replaceAll(clients)
	grants, err := s.oauthGrantStore.LoadAll()
	if err != nil {
		return err
	}
	s.oauthGrants.replaceAll(grants)
	return nil
}

// handleRegisterOAuthClient registers an application. Body:
// {"name": "...", "redirectUris": ["https://…/callback"]}.
func (s *Server) handleRegisterOAuthClient(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var payload struct {
		Name         string   `json:"name"`
		RedirectURIs []string `json:"redirectUris"`
	}
	if !decodeJSONBody(w, r, &payload) {
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		httpapi.Error(w, http.StatusBadRequest, "client name is required")
		return
	}
	uris, err := validRedirectURIs(payload.RedirectURIs)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	id, err := newID()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "generate id: "+err.Error())
		return
	}
	suffix, err := randomHex(32)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "generate secret: "+err.Error())
		return
	}
	secret := oauthClientSecretPrefix + suffix
	rec := oauthClient{
		ID: id, Name: name, SecretHash: hashAPIToken(secret),
		RedirectURIs: uris, CreatedAt: time.Now().Unix(),
	}
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		rec.CreatedBy = p.UserID
	}

	var saveErr error
	s.do(func() {
		if saveErr = s.oauthClientStore.Save(rec); saveErr != nil {
			return
		}
		s.oauthClients.add(rec)
	})
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "register client: "+saveErr.Error())
		return
	}
	audit(r, logging.AuthOAuthClientRegistered, "oauth client registered",
		slog.String("client_id", rec.ID), slog.String("client_name", rec.Name),
		slog.Int("redirect_uris", len(rec.RedirectURIs)))
	httpapi.JSON(w, http.StatusOK, newOAuthClientResp{oauthClientView: rec.view(), Secret: secret})
}

// validRedirectURIs checks the set an application registers.
//
// Absolute, and https unless it is loopback. That carve-out is not politeness: the
// authorization code travels back on this URI, and http over a network hands it to
// anyone on the path — while a loopback redirect never leaves the machine and is
// how a desktop client is meant to work. A URI with a fragment is refused because
// the response parameters are appended to the query and a fragment would swallow
// them.
func validRedirectURIs(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, raw := range in {
		uri := strings.TrimSpace(raw)
		if uri == "" {
			continue
		}
		u, err := url.Parse(uri)
		if err != nil || !u.IsAbs() || u.Host == "" {
			return nil, errBadRedirect(uri, "must be an absolute URL")
		}
		if u.Fragment != "" || strings.Contains(uri, "#") {
			return nil, errBadRedirect(uri, "must not carry a fragment")
		}
		if u.Scheme != "https" && !isLoopbackHost(u.Hostname()) {
			return nil, errBadRedirect(uri, "must be https, or http on loopback")
		}
		out = append(out, uri)
	}
	if len(out) == 0 {
		return nil, errBadRedirect("", "at least one redirect URI is required")
	}
	return out, nil
}

func errBadRedirect(uri, why string) error {
	if uri == "" {
		return errors.New(why)
	}
	return errors.New("redirect URI " + uri + " " + why)
}

// principalIsAdmin mirrors requireAdmin's rule without writing a response: with
// enforcement off there is no one to distinguish, so everybody sees everything —
// the same answer requireAdmin gives on its first line.
func (s *Server) principalIsAdmin(p *httpapi.Principal) bool {
	return !s.authEnabled || p.HasRole(RoleAdmin)
}

// handleListOAuthClients lists the registered applications. Secrets are absent
// because the server does not have them.
func (s *Server) handleListOAuthClients(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	out := []oauthClientView{}
	var loadErr error
	s.do(func() {
		var recs []oauthClient
		if recs, loadErr = s.oauthClientStore.LoadAll(); loadErr != nil {
			return
		}
		for _, rec := range recs {
			out = append(out, rec.view())
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list oauth clients: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleDeleteOAuthClient removes an application and, with it, every grant anybody
// approved for it.
//
// The cascade is the point. Removing a client while its grants kept working would
// leave tokens that no longer correspond to anything an operator can see — the
// exact shape of a credential nobody knows exists. Deleting the client is how an
// operator says "this application is done", and it has to mean it.
func (s *Server) handleDeleteOAuthClient(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	var (
		delErr  error
		revoked int
	)
	s.do(func() {
		var grants []oauthGrant
		if grants, delErr = s.oauthGrantStore.LoadAll(); delErr != nil {
			return
		}
		for _, g := range grants {
			if g.ClientID != id {
				continue
			}
			if delErr = s.oauthGrantStore.Delete(g.ID); delErr != nil {
				return
			}
			s.oauthGrants.remove(g.ID)
			revoked++
		}
		delErr = s.oauthClientStore.Delete(id)
		s.oauthClients.remove(id)
	})
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete oauth client: "+delErr.Error())
		return
	}
	audit(r, logging.AuthOAuthClientDeleted, "oauth client deleted",
		slog.String("client_id", id), slog.Int("grants_revoked", revoked))
	w.WriteHeader(http.StatusNoContent)
}

// handleListOAuthGrants lists standing approvals: all of them for an
// administrator, and their own for everybody else.
func (s *Server) handleListOAuthGrants(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	// With enforcement off there is no principal and nobody to distinguish, so the
	// listing is everybody's — the same answer requireAdmin gives on its first line.
	// Refusing here instead would make an open server answer 401, which is the one
	// thing an open server must never do.
	all := p == nil || s.principalIsAdmin(p)
	mine := ""
	if p != nil {
		mine = p.UserID
	}
	out := []oauthGrantView{}
	var loadErr error
	s.do(func() {
		var recs []oauthGrant
		if recs, loadErr = s.oauthGrantStore.LoadAll(); loadErr != nil {
			return
		}
		for _, rec := range recs {
			if all || rec.UserID == mine {
				out = append(out, rec.view())
			}
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list oauth grants: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleRevokeOAuthGrant withdraws one approval, effective on the next request.
//
// A person may revoke their own; an administrator may revoke anyone's. A grant
// somebody else approved is reported as not found rather than as forbidden, so this
// endpoint cannot be used to discover whose approvals exist.
func (s *Server) handleRevokeOAuthGrant(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	// As above: no principal means enforcement is off, and then anyone may revoke
	// anything because there is nobody to be someone else.
	anyone := p == nil || s.principalIsAdmin(p)
	mine := ""
	if p != nil {
		mine = p.UserID
	}
	id := r.PathValue("id")
	var (
		err   error
		found bool
	)
	s.do(func() {
		var recs []oauthGrant
		if recs, err = s.oauthGrantStore.LoadAll(); err != nil {
			return
		}
		for _, rec := range recs {
			if rec.ID != id {
				continue
			}
			if !anyone && rec.UserID != mine {
				return // not theirs: indistinguishable from absent
			}
			found = true
			err = s.oauthGrantStore.Delete(id)
			s.oauthGrants.remove(id)
			return
		}
	})
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "revoke oauth grant: "+err.Error())
		return
	}
	if !found {
		httpapi.Error(w, http.StatusNotFound, "no such grant")
		return
	}
	audit(r, logging.AuthOAuthGrantRevoked, "oauth grant revoked", slog.String("grant_id", id))
	w.WriteHeader(http.StatusNoContent)
}

// revokeUserGrants withdraws every grant belonging to a user, and refreshUserGrants
// rewrites what they may do.
//
// These are the maintenance that makes the snapshot on a grant safe to keep. They
// hang off the same decisions that already invalidate live sessions: disabling or
// deleting an account destroys its sessions, and now its grants — otherwise a
// disabled person would keep acting through a connector, which is the whole reason
// disabling exists. A role or group change rewrites the snapshot instead of
// dropping it, because the person did nothing wrong and their connector should not
// fall over for an administrative edit.
//
// Both run inside a run-loop turn, so the caller must not already be in one.
func (s *Server) revokeUserGrants(userID string) {
	ids := s.oauthGrants.forUser(userID)
	if len(ids) == 0 {
		return
	}
	s.do(func() {
		for _, id := range ids {
			_ = s.oauthGrantStore.Delete(id)
			s.oauthGrants.remove(id)
		}
	})
	logging.Info(logging.AuthOAuthGrantRevoked, "oauth grants revoked with the account",
		slog.String("user_id", userID), slog.Int("grants", len(ids)))
}

func (s *Server) setUserGrantRoles(userID string, roles []string) {
	s.rewriteUserGrants(userID, func(g *oauthGrant) bool {
		g.Roles = append([]string(nil), roles...)
		return true
	})
}

// setGrantGroupMembership and dropGroupFromGrants mirror the sessionStore methods
// of the same shape, called from the same places, so a group change reaches a
// person's connector exactly as it reaches their browser session (ADR-0185).
func (s *Server) setGrantGroupMembership(userID, groupID string, member bool) {
	s.rewriteUserGrants(userID, func(g *oauthGrant) bool {
		next, changed := applyGroup(g.GroupIDs, groupID, member)
		g.GroupIDs = next
		return changed
	})
}

func (s *Server) dropGroupFromGrants(groupID string) {
	s.rewriteAllGrants(func(g *oauthGrant) bool {
		next, changed := applyGroup(g.GroupIDs, groupID, false)
		g.GroupIDs = next
		return changed
	})
}

// rewriteUserGrants applies a change to one person's grants, and rewriteAllGrants
// to everybody's. Both save only what the mutator says actually changed, so a
// no-op edit costs no disk write.
func (s *Server) rewriteUserGrants(userID string, apply func(*oauthGrant) bool) {
	if len(s.oauthGrants.forUser(userID)) == 0 {
		return
	}
	s.rewriteAllGrants(func(g *oauthGrant) bool {
		if g.UserID != userID {
			return false
		}
		return apply(g)
	})
}

func (s *Server) rewriteAllGrants(apply func(*oauthGrant) bool) {
	s.do(func() {
		recs, err := s.oauthGrantStore.LoadAll()
		if err != nil {
			return
		}
		for _, rec := range recs {
			if !apply(&rec) {
				continue
			}
			if err := s.oauthGrantStore.Save(rec); err != nil {
				continue
			}
			s.oauthGrants.put(rec)
		}
	})
}
