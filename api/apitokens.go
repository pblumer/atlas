package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/logging"
)

// API-token management (ADR-0194): minting, listing and revoking the
// credentials a machine authenticates with. Admin-gated, because issuing one is
// the same class of act as creating an account — and, like the deploy tokens this
// follows, the secret is returned exactly once because the server does not keep it.
// These are the routes the Console's API-token page drives (web/apitokens.js).

// maxAPITokenLifetime bounds how long a token may be asked to live. It is not a
// security boundary — a machine that needs a year gets a year by asking — but a
// request that omits a lifetime should not silently mean "forever", and a cap this
// far out makes an absurd value (a typo of a hundred years) fail loudly.
const maxAPITokenLifetime = 5 * 365 * 24 * time.Hour

// apiTokenExpiryWarning is how far ahead a token's expiry is warned about, on the
// Console (EXPIRY_WARN_DAYS in web/apitokens.js) and on /metrics alike, so the page
// and an alert never disagree about which tokens are in trouble. A token that runs
// out fails every call its machine makes from then on; thirty days leaves room to
// mint a replacement and get it through a change process.
const apiTokenExpiryWarning = 30 * 24 * time.Hour

// newAPITokenResp is the mint response: the only time the secret exists outside
// the caller's memory. Token is empty, and therefore absent, when the secret was
// sealed in the vault instead: then it never leaves the server at all.
type newAPITokenResp struct {
	apiTokenView
	Token string `json:"token,omitempty"`
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
// {"name": "...", "scope": "full|worker|metrics|status|directory|inventory|landscape|events",
// "reach": ["..."], "expiresInDays": 90, "vaultSecret": "atlas"}. The reach is optional
// except for landscape, and names projects — or, for an events token, catalogues.
//
// The name is required and the scope is required, both because the alternative is
// a credential nobody can identify later and one whose reach nobody chose. An
// omitted lifetime means the token does not expire, which is allowed and is said
// out loud in the response rather than hidden in a default.
//
// vaultSecret is optional and seals the secret in this server's vault under that name
// instead of returning it (ADR-0194, amendment 2026-10-21). It exists for the token a
// model's own REST tasks present — authSecret="atlas" in the shop's system processes
// (ADR-0411) — which no person ever needs to read: returned and pasted into the vault
// by hand, it crossed the wire twice and sat in a clipboard in between. It is all or
// nothing: a vault that is missing or refuses the write leaves no token behind.
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
		VaultSecret   string   `json:"vaultSecret"`
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
	reach, reachErr, err := s.reachFor(r, scope, payload.Reach)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "check reach: "+err.Error())
		return
	}
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
	vaultName, refusal := vaultSecretName(payload.VaultSecret)
	if refusal != "" {
		httpapi.Error(w, http.StatusBadRequest, refusal)
		return
	}
	// Asked before anything is minted, so the answer to a server without a vault is a
	// refusal and not a token that exists with nobody holding its secret.
	if vaultName != "" && s.vault == nil {
		httpapi.Error(w, http.StatusServiceUnavailable,
			"vault not configured: no token was minted, because its secret would have nowhere to go")
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
	roles, refusal := tokenRolesFor(scope, p)
	if refusal != "" {
		httpapi.Error(w, http.StatusForbidden, refusal)
		return
	}
	rec.Roles = roles
	rec.VaultSecret = vaultName

	if vaultName != "" {
		s.mintIntoVault(w, r, rec, secret)
		return
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
	auditMinted(r, rec)
	httpapi.JSON(w, http.StatusOK, newAPITokenResp{apiTokenView: rec.view(), Token: secret})
}

// mintIntoVault stores a token whose secret is sealed in the vault under
// rec.VaultSecret, and answers without the secret.
//
// The order is what makes it all or nothing. The record is written first, then the
// secret is sealed; a vault that refuses gets the record deleted again, and only when
// both are durable does the token join the index that authenticates. So a failure
// leaves either nothing, or — if the delete fails too — a record whose secret nobody
// has, which can never be presented. Sealing first would be the worse order: a
// refused record write would leave the vault holding a secret for a token that does
// not exist, having overwritten whatever the name held before.
//
// The worker registries are rebuilt and the supervised workers refreshed in the same
// step, as PUT /api/v1/secrets does: a vault secret can be the credential behind a
// Worker, and there is no route by which a secret changes for the engine and not for
// its workers. A failed rebuild comes after the token exists, and says so.
func (s *Server) mintIntoVault(w http.ResponseWriter, r *http.Request, rec apiToken, secret string) {
	var saveErr, sealErr, reloadErr error
	s.doAndRefresh(func() {
		if saveErr = s.apiTokenStore.Save(rec); saveErr != nil {
			return
		}
		if _, sealErr = s.vault.Set(rec.VaultSecret, secret); sealErr != nil {
			if delErr := s.apiTokenStore.Delete(rec.ID); delErr != nil {
				sealErr = fmt.Errorf("%w (and removing the token record failed: %v; it can never be presented, because nobody holds its secret)", sealErr, delErr)
			}
			return
		}
		s.apiTokens.add(rec)
		reloadErr = s.rebuildConnectorRegistries()
	})
	switch {
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "create api token: "+saveErr.Error())
		return
	case sealErr != nil:
		httpapi.Error(w, http.StatusInternalServerError,
			"seal api token in the vault as "+rec.VaultSecret+": "+sealErr.Error()+"; no token was minted")
		return
	}
	auditMinted(r, rec)
	if reloadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError,
			"api token "+rec.ID+" was minted and sealed as "+rec.VaultSecret+
				", but reloading the workers failed: "+reloadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, newAPITokenResp{apiTokenView: rec.view()})
}

// auditMinted writes the audit line of a mint. It names the vault secret a token was
// sealed as — a name, never the value — because that is where the credential went.
func auditMinted(r *http.Request, rec apiToken) {
	attrs := []slog.Attr{
		slog.String("token_id", rec.ID), slog.String("token_name", rec.Name),
		slog.String("scope", rec.scope()), slog.String("roles", strings.Join(rec.roles(), " ")),
		slog.Int64("expires_at", rec.ExpiresAt),
	}
	if rec.VaultSecret != "" {
		attrs = append(attrs, slog.String("vault_secret", rec.VaultSecret))
	}
	audit(r, logging.AuthTokenMinted, "api token minted", attrs...)
}

// vaultSecretName validates the optional vault secret a token is to be sealed as. An
// absent or empty field means none. A name of only spaces is refused rather than read
// as absent: somebody filled the field in, and handing their secret back instead of
// sealing it would be the surprising answer. A data subject's personal-data key is
// refused for the reason PUT /api/v1/secrets refuses it: overwriting it makes that
// subject's data unreadable, silently and irreversibly (ADR-0314).
func vaultSecretName(raw string) (name, refusal string) {
	if raw == "" {
		return "", ""
	}
	name = strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", "vaultSecret must name a vault secret, or be left out"
	case vault.IsDataKeyName(name):
		return "", "vaultSecret names a data subject's personal-data key, which is not an operator secret (ADR-0314)"
	case vault.IsReservedName(name):
		return "", "vaultSecret names a delegated connection's credential, which is not an operator secret"
	}
	return name, ""
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
// refusal to send back. A store it cannot read comes back as an error rather than a
// refusal: that is the server's fault, not something wrong with the request.
//
// What a reach names depends on the scope. For an events token it is catalogues
// (catalogReach); for every other scope it is projects, and two rules apply, both about
// the door rather than the read (ADR-0410).
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
func (s *Server) reachFor(r *http.Request, scope string, asked []string) (reach []string, refusal string, err error) {
	for _, id := range asked {
		if id = strings.TrimSpace(id); id != "" {
			reach = append(reach, id)
		}
	}
	if scope == apiScopeEvents {
		return s.catalogReach(r, reach)
	}
	if len(reach) == 0 {
		if scope == apiScopeLandscape {
			return nil, "a " + apiScopeLandscape + " token must state the reach it may see: " +
				`"reach" naming one or more projects`, nil
		}
		return nil, "", nil
	}
	var (
		projs   map[string]project
		loadErr error
	)
	s.do(func() { projs, loadErr = s.projectsByID() })
	if loadErr != nil {
		return nil, "", fmt.Errorf("read projects: %w", loadErr)
	}
	for _, id := range reach {
		p, ok := projs[id]
		if !ok {
			return nil, "reach names no project this server has: " + id, nil
		}
		if !s.canViewArtifact(r, p.ID, p.OwnerID, projs) {
			return nil, "reach names a project you cannot see: " + id, nil
		}
	}
	return reach, "", nil
}

// catalogReach validates an events token's reach, which names catalogues: the feed
// answers such a token only the rows about products those catalogues maintain
// (eventfeed.go). Empty is allowed and means the whole feed, which is what an events
// token minted before reach meant anything still reads.
//
// The minter rule is the same as for projects, with the catalogue's own notion of who
// may see behind it: a minter names only catalogues they maintain (catalog.MayMaintain).
// Being the audience a catalogue is offered to does not count, because the feed of a
// catalogue is a map of who holds its products, which is the estate behind it rather
// than the shop in front.
func (s *Server) catalogReach(r *http.Request, reach []string) ([]string, string, error) {
	if len(reach) == 0 {
		return nil, "", nil
	}
	p := httpapi.PrincipalFrom(r.Context())
	var (
		refusal string
		loadErr error
	)
	s.do(func() {
		for _, id := range reach {
			c, found, err := s.catalogStore.Catalog(id)
			switch {
			case err != nil:
				loadErr = fmt.Errorf("read catalogues: %w", err)
				return
			case !found:
				refusal = "reach names no catalogue this server has: " + id
				return
			case !s.catalogs.MayMaintain(c, p):
				refusal = "reach names a catalogue you do not maintain: " + id
				return
			}
		}
	})
	if loadErr != nil || refusal != "" {
		return nil, refusal, loadErr
	}
	return reach, "", nil
}
