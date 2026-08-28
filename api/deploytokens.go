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

// Deploy-token management (ADR-0129). Minting, listing, and revoking the
// credentials peer Atlas servers use to publish a bundle here. Admin-gated: these
// are credentials, and issuing one is the same class of act as managing a user.
//
// The secret is returned exactly once, by handleCreateDeployToken. There is no
// endpoint that reveals it again, because the server does not keep it — only its
// SHA-256 (see deploytokenstore.go). Losing a token means minting a new one and
// revoking the old, which is the property that makes rotation honest.

// newDeployTokenResp is the mint response: the only time the secret exists outside
// the caller's memory.
type newDeployTokenResp struct {
	deployTokenView
	Token string `json:"token"`
}

// loadDeployTokens fills the in-memory index from the durable records. It runs
// before the loop serves traffic, so touching the index directly is safe — the
// same discipline loadDeployments and loadReleaseVersions use.
func (s *Server) loadDeployTokens() error {
	recs, err := s.deployTokenStore.LoadAll()
	if err != nil {
		return err
	}
	s.deployTokens.replaceAll(recs)
	return nil
}

// handleCreateDeployToken mints a token. Body: {"name": "..."} — a label naming
// the peer it is for, so an operator can tell two credentials apart when revoking.
func (s *Server) handleCreateDeployToken(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		httpapi.Error(w, http.StatusBadRequest, "deploy token name is required")
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
	secret := deployTokenPrefix + suffix

	rec := deployToken{
		ID:        id,
		Name:      name,
		Hash:      hashDeployToken(secret),
		CreatedAt: time.Now().Unix(),
	}
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		rec.CreatedBy = p.UserID
	}

	var saveErr error
	s.do(func() {
		if saveErr = s.deployTokenStore.Save(rec); saveErr != nil {
			return
		}
		s.deployTokens.add(rec)
	})
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "create deploy token: "+saveErr.Error())
		return
	}
	audit(r, logging.AuthTokenMinted, "deploy token minted",
		slog.String("token_id", rec.ID), slog.String("token_name", rec.Name))
	httpapi.JSON(w, http.StatusOK, newDeployTokenResp{deployTokenView: rec.view(), Token: secret})
}

// handleListDeployTokens lists the tokens by identity and provenance. The secret is
// absent because the server does not have it.
func (s *Server) handleListDeployTokens(w http.ResponseWriter, r *http.Request) {
	out := []deployTokenView{}
	var loadErr error
	s.do(func() {
		var recs []deployToken
		if recs, loadErr = s.deployTokenStore.LoadAll(); loadErr != nil {
			return
		}
		for _, rec := range recs {
			out = append(out, rec.view())
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list deploy tokens: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleRevokeDeployToken revokes a token. Revocation is deletion and takes effect
// immediately: the durable record goes first, then the in-memory index, so a
// failure mid-way leaves the credential *revoked in memory* rather than silently
// still valid — the safe direction for a credential.
func (s *Server) handleRevokeDeployToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var delErr error
	s.do(func() {
		delErr = s.deployTokenStore.Delete(id)
		s.deployTokens.remove(id)
	})
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "revoke deploy token: "+delErr.Error())
		return
	}
	audit(r, logging.AuthTokenRevoked, "deploy token revoked", slog.String("token_id", id))
	w.WriteHeader(http.StatusNoContent)
}
