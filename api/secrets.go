package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/vault"
)

// handleListSecrets lists secret names and metadata in the vault — never values
// (ADR-0069). Admin-guarded like the other operator surfaces.
func (s *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "vault not configured")
		return
	}
	var (
		metas   []vault.Meta
		loadErr error
	)
	s.do(func() { metas, loadErr = s.vault.List() })
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list secrets: "+loadErr.Error())
		return
	}
	if metas == nil {
		metas = []vault.Meta{}
	}
	writeJSON(w, http.StatusOK, metas)
}

// handleSetSecret seals a secret value under the {name} in the path. The plaintext
// arrives in the request body, is sealed immediately, and is never persisted in the
// clear, logged, or echoed back — the response carries only value-free metadata.
func (s *Server) handleSetSecret(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "vault not configured")
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "secret name is required")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if p.Value == "" {
		writeError(w, http.StatusBadRequest, "secret value is required")
		return
	}
	var (
		meta    vault.Meta
		saveErr error
	)
	s.do(func() {
		if meta, saveErr = s.vault.Set(name, p.Value); saveErr != nil {
			return
		}
		// A rotated secret must reach the live connector clients immediately — a
		// managed connector holds only a reference to this value, and its client was
		// built with the old one. Rebuild the registries in the same run-loop step
		// that saved the secret, so a bridge/worker picks up the new token without the
		// operator re-saving the connector.
		saveErr = s.rebuildConnectorRegistries()
	})
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, "set secret: "+saveErr.Error())
		return
	}
	if meta.Name == "" {
		writeError(w, http.StatusServiceUnavailable, "server unavailable")
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

// handleDeleteSecret removes a secret from the vault and rebuilds the connector
// registries, so a connector that referenced it resolves to no token again (and
// parks) at once, without waiting for a re-save.
func (s *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "vault not configured")
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "secret name is required")
		return
	}
	var delErr error
	s.do(func() {
		if delErr = s.vault.Delete(name); delErr != nil {
			return
		}
		delErr = s.rebuildConnectorRegistries()
	})
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, "delete secret: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
