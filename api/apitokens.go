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
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().ModelUpload))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Name          string   `json:"name"`
		Scope         string   `json:"scope"`
		Reach         []string `json:"reach"`
		ExpiresInDays int      `json:"expiresInDays"`
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
	reach, reachErr := s.reachFor(r, scope, payload.Reach)
	if reachErr != "" {
		httpapi.Error(w, http.StatusBadRequest, reachErr)
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
		Reach:     reach,
		CreatedAt: now.Unix(),
	}
	if lifetime > 0 {
		rec.ExpiresAt = now.Add(lifetime).Unix()
	}
	p := httpapi.PrincipalFrom(r.Context())
	if p != nil {
		rec.CreatedBy = p.UserID
	}
	rec.Roles = tokenRoles(p)

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
		slog.String("scope", rec.scope()), slog.String("roles", strings.Join(rec.roles(), " ")),
		slog.Int64("expires_at", rec.ExpiresAt))
	httpapi.JSON(w, http.StatusOK, newAPITokenResp{apiTokenView: rec.view(), Token: secret})
}

// handleListAPITokens lists the tokens by identity, reach, lifetime and
// provenance. The secret is absent because the server does not have it.
func (s *Server) handleListAPITokens(w http.ResponseWriter, r *http.Request) {
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

// reachFor validates a minted credential's reach and returns the value to store, or a
// refusal to send back.
//
// Two rules, and both are about the door rather than the read (ADR-0410).
//
// **A scope whose answer is wide must state a reach.** The landscape read serves a whole
// derived picture, and a machine principal holding it with no reach would be viewer on
// everything — the escalation ADR-0402 §1 set out to close. Refusing at minting keeps the
// fail-closed rule from breaking every credential already in the field, none of which states
// a reach and none of which serves this read.
//
// **A minter cannot grant a reach they do not hold.** This is the rule the record leaves
// implicit and the code must not: a credential is never more privileged than the person who
// created it. Its roles are already snapshotted from the minter for that reason (ADR-0209),
// and a reach naming a project the minter cannot view would be that property broken one step
// removed — mint the token, then read through it.
func (s *Server) reachFor(r *http.Request, scope string, asked []string) (reach []string, refusal string) {
	for _, id := range asked {
		if id = strings.TrimSpace(id); id != "" {
			reach = append(reach, id)
		}
	}
	if len(reach) == 0 {
		if scope == apiScopeLandscape {
			return nil, "a " + apiScopeLandscape + " token must state the reach it may see: " +
				`"reach" naming one or more projects`
		}
		return nil, ""
	}
	var (
		projs   map[string]project
		loadErr error
	)
	s.do(func() { projs, loadErr = s.projectsByID() })
	if loadErr != nil {
		return nil, "read projects: " + loadErr.Error()
	}
	for _, id := range reach {
		p, ok := projs[id]
		if !ok {
			return nil, "reach names no project this server has: " + id
		}
		if !s.canViewArtifact(r, p.ID, p.OwnerID, projs) {
			return nil, "reach names a project you cannot see: " + id
		}
	}
	return reach, ""
}
