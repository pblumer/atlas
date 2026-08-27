package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// API-token management (ADR-0194): minting, listing and revoking the
// credentials a machine authenticates with. Admin-gated, because issuing one is
// the same class of act as creating an account — and, like the deploy tokens this
// follows, the secret is returned exactly once because the server does not keep it.

// maxAPITokenLifetime bounds how long a token may be asked to live. It is not a
// security boundary — a machine that needs a year gets a year by asking — but a
// request that omits a lifetime should not silently mean "forever", and a cap this
// far out makes an absurd value (a typo of a hundred years) fail loudly.
const maxAPITokenLifetime = 5 * 365 * 24 * time.Hour

// newAPITokenResp is the mint response: the only time the secret exists outside
// the caller's memory.
type newAPITokenResp struct {
	apiTokenView
	Token string `json:"token"`
}

// loadAPITokens fills the in-memory index from the durable records. It runs before
// the loop serves traffic, so touching the index directly is safe — the same
// discipline loadDeployTokens uses.
func (s *Server) loadAPITokens() error {
	recs, err := s.apiTokenStore.LoadAll()
	if err != nil {
		return err
	}
	s.apiTokens.replaceAll(recs)
	return nil
}

// handleCreateAPIToken mints a token. Body:
// {"name": "...", "scope": "full|worker", "expiresInDays": 90}.
//
// The name is required and the scope is required, both because the alternative is
// a credential nobody can identify later and one whose reach nobody chose. An
// omitted lifetime means the token does not expire, which is allowed and is said
// out loud in the response rather than hidden in a default.
func (s *Server) handleCreateAPIToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Name          string `json:"name"`
		Scope         string `json:"scope"`
		ExpiresInDays int    `json:"expiresInDays"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		httpapi.Error(w, http.StatusBadRequest, "api token name is required")
		return
	}
	scope := strings.TrimSpace(payload.Scope)
	if !validAPIScope(scope) {
		httpapi.Error(w, http.StatusBadRequest,
			"scope must be one of: "+strings.Join(apiScopes(), ", "))
		return
	}
	if payload.ExpiresInDays < 0 {
		httpapi.Error(w, http.StatusBadRequest, "expiresInDays must not be negative")
		return
	}
	lifetime := time.Duration(payload.ExpiresInDays) * 24 * time.Hour
	if lifetime > maxAPITokenLifetime {
		httpapi.Error(w, http.StatusBadRequest, "expiresInDays is beyond the maximum lifetime")
		return
	}

	id, err := newID()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "generate id: "+err.Error())
		return
	}
	// 32 bytes of CSPRNG output: enough entropy that the stored SHA-256 needs no
	// deliberate slowness to resist guessing.
	suffix, err := randomHex(32)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "generate token: "+err.Error())
		return
	}
	secret := apiTokenPrefix + suffix

	now := time.Now()
	rec := apiToken{
		ID:        id,
		Name:      name,
		Hash:      hashAPIToken(secret),
		Scope:     scope,
		CreatedAt: now.Unix(),
	}
	if lifetime > 0 {
		rec.ExpiresAt = now.Add(lifetime).Unix()
	}
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		rec.CreatedBy = p.UserID
	}

	var saveErr error
	s.do(func() {
		if saveErr = s.apiTokenStore.Save(rec); saveErr != nil {
			return
		}
		s.apiTokens.add(rec)
	})
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "create api token: "+saveErr.Error())
		return
	}
	audit(r, logging.AuthTokenMinted, "api token minted",
		slog.String("token_id", rec.ID), slog.String("token_name", rec.Name),
		slog.String("scope", rec.scope()), slog.Int64("expires_at", rec.ExpiresAt))
	httpapi.JSON(w, http.StatusOK, newAPITokenResp{apiTokenView: rec.view(), Token: secret})
}

// handleListAPITokens lists the tokens by identity, reach, lifetime and
// provenance. The secret is absent because the server does not have it.
func (s *Server) handleListAPITokens(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	out := []apiTokenView{}
	var loadErr error
	s.do(func() {
		var recs []apiToken
		if recs, loadErr = s.apiTokenStore.LoadAll(); loadErr != nil {
			return
		}
		for _, rec := range recs {
			out = append(out, rec.view())
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list api tokens: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleRevokeAPIToken revokes a token. Revocation is deletion and takes effect
// immediately: the durable record goes first, then the in-memory index, so a
// failure mid-way leaves the credential *revoked in memory* rather than silently
// still valid — the safe direction for a credential.
func (s *Server) handleRevokeAPIToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	var delErr error
	s.do(func() {
		delErr = s.apiTokenStore.Delete(id)
		s.apiTokens.remove(id)
	})
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "revoke api token: "+delErr.Error())
		return
	}
	audit(r, logging.AuthTokenRevoked, "api token revoked", slog.String("token_id", id))
	w.WriteHeader(http.StatusNoContent)
}
