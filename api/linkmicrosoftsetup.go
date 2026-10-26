package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/identity"
	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/logging"
)

// Setting up account linking in the Console (ADR-0475, amended).
//
// The start flags were the only way to switch linking on, which put a command line
// between the people who meet the feature — in the account menu — and the feature.
// An administrator now sets it up under Administration → Security. The tenant, the
// client id and the scopes are kept beside the identities and connections they are
// for (identity/link-microsoft.json), so a restore that carries the connections
// carries the tenant they belong to, and a portable backup carries neither. The client
// secret is a vault secret under a reserved name that no Worker and no secrets route
// can reach.
//
// The start flags still win. With any of them given, the Console shows what they say
// and changes nothing, so an installation its deployment configures is not
// reconfigured from a browser.
//
// What cannot be done from here is the half at Microsoft: the app registration, its
// redirect URI and whatever consent the tenant requires are made in Entra. The page
// says what to make there and shows the redirect URI this server will send, so it is
// copied rather than guessed.

const (
	linkMicrosoftSetupPath = "/api/v1/security/linking/microsoft"
	linkMicrosoftSetupFile = "link-microsoft.json"

	// Where the running link configuration came from.
	linkSourceNone    = "none"
	linkSourceFlags   = "flags"
	linkSourceConsole = "console"

	linkMaxScopes    = 32
	linkMaxScopeLen  = 256
	linkMaxSecretLen = 1024
)

// guidShape is how Entra writes a directory (tenant) id and an application (client)
// id. Both are required in that form: every token is checked against the tenant by
// its id, so a domain name or "organizations" here would refuse every link with a
// reason only the audit log knows.
var guidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// scopeShape is RFC 6749 §3.3's scope-token: printable ASCII without space, quote
// or backslash.
var scopeShape = regexp.MustCompile(`^[\x21\x23-\x5B\x5D-\x7E]+$`)

// microsoftLinkSetup is what the Console stores: everything but the client secret.
type microsoftLinkSetup struct {
	TenantID string `json:"tenantId"`
	ClientID string `json:"clientId"`
	Scopes   string `json:"scopes"`
}

func (set microsoftLinkSetup) config(secret, authority string) MicrosoftLinkConfig {
	return MicrosoftLinkConfig{TenantID: set.TenantID, ClientID: set.ClientID, ClientSecret: secret,
		Scopes: set.Scopes, Authority: authority}
}

func (s *Server) linkSetupDir() string { return filepath.Join(s.dataDir, "identity") }

func (s *Server) linkSetupFile() string {
	return filepath.Join(s.linkSetupDir(), linkMicrosoftSetupFile)
}

var linkSecretName = vault.LinkClientSecretName(identity.ProviderMicrosoft)

// loadMicrosoftLinkSetup reads the stored setup and its client secret; ok is false
// when none is stored. On the run loop, or while the server is built.
func (s *Server) loadMicrosoftLinkSetup() (set microsoftLinkSetup, secret string, ok bool, err error) {
	data, err := os.ReadFile(s.linkSetupFile())
	if errors.Is(err, os.ErrNotExist) {
		return microsoftLinkSetup{}, "", false, nil
	}
	if err != nil {
		return microsoftLinkSetup{}, "", false, fmt.Errorf("read the linking setup: %w", err)
	}
	if err := json.Unmarshal(data, &set); err != nil {
		return microsoftLinkSetup{}, "", false, fmt.Errorf("decode the linking setup: %w", err)
	}
	if s.vault == nil {
		return set, "", true, nil
	}
	if secret, _, err = s.vault.Get(linkSecretName); err != nil {
		return set, "", true, fmt.Errorf("read the linking client secret: %w", err)
	}
	return set, secret, true, nil
}

// startMicrosoftLinking decides, once the identity service exists, which
// configuration people link through: the start flags when any is given, else what
// the Console stored. It runs while the server is built, before the loop serves, so
// it reads the store directly.
func (s *Server) startMicrosoftLinking() {
	if s.msLinkStart.given() {
		s.installMicrosoftLink(s.msLinkStart)
		return
	}
	set, secret, ok, err := s.loadMicrosoftLinkSetup()
	if err != nil {
		logging.Warn(logging.LinkingSetupUnreadable,
			"the account-linking setup stored in the Console could not be read; linking stays off until an administrator sets it up again",
			slog.String("error", err.Error()))
		return
	}
	if ok {
		s.installMicrosoftLink(set.config(secret, s.msLinkStart.Authority))
	}
}

// linkSetupView is what an administrator is told about linking. It names whether a
// client secret is held and never what it is.
type linkSetupView struct {
	// Available is false when the vault is off: a delegated credential has nowhere it
	// may live, so linking cannot be set up at all.
	Available bool `json:"available"`
	// Source is where the running configuration came from: none, flags or console.
	Source string `json:"source"`
	// Active says whether people can link an account right now.
	Active          bool   `json:"active"`
	TenantID        string `json:"tenantId"`
	ClientID        string `json:"clientId"`
	Scopes          string `json:"scopes"`
	ClientSecretSet bool   `json:"clientSecretSet"`
	// RedirectURI is what this server sends Microsoft, which the app registration has
	// to list exactly; empty when the request carried no host to build it from.
	// RedirectURIDerived says it was built from this request rather than from
	// --external-url, so a page reached through an internal name shows that name.
	RedirectURI        string `json:"redirectUri"`
	RedirectURIDerived bool   `json:"redirectUriDerived"`
	// Connections counts the Microsoft connections not revoked; Stranded, those of
	// them that cannot act under the running configuration — another tenant's, or
	// all of them while linking is off.
	Connections int `json:"connections"`
	Stranded    int `json:"stranded"`
}

// linkSetupView builds the view. It reads the store through the loop.
func (s *Server) linkSetupView(r *http.Request) (linkSetupView, error) {
	v := linkSetupView{Available: s.identity != nil, Source: linkSourceNone, Active: s.msLink.Load() != nil}
	if base := s.externalBase(r); base != "" {
		v.RedirectURI = base + linkMicrosoftCallbackPath
	}
	v.RedirectURIDerived = s.externalURL == ""
	running := ""
	switch {
	case s.msLinkStart.given():
		f := s.msLinkStart
		v.Source = linkSourceFlags
		v.TenantID, v.ClientID, v.Scopes = strings.TrimSpace(f.TenantID), strings.TrimSpace(f.ClientID), strings.TrimSpace(f.Scopes)
		v.ClientSecretSet = f.ClientSecret != ""
	case s.identity != nil:
		var (
			set    microsoftLinkSetup
			secret string
			ok     bool
			err    error
		)
		s.do(func() { set, secret, ok, err = s.loadMicrosoftLinkSetup() })
		if err != nil {
			return linkSetupView{}, err
		}
		if ok {
			v.Source = linkSourceConsole
			v.TenantID, v.ClientID, v.Scopes = set.TenantID, set.ClientID, set.Scopes
			v.ClientSecretSet = secret != ""
		}
	}
	if l := s.msLink.Load(); l != nil {
		running = strings.TrimSpace(l.cfg.TenantID)
	}
	if s.identity != nil {
		for tenant, n := range s.identity.Unrevoked(identity.ProviderMicrosoft) {
			v.Connections += n
			if running == "" || tenant != running {
				v.Stranded += n
			}
		}
	}
	return v, nil
}

// linkSetupBody is what an administrator sends. An empty client secret keeps the one
// held.
type linkSetupBody struct {
	TenantID     string `json:"tenantId"`
	ClientID     string `json:"clientId"`
	Scopes       string `json:"scopes"`
	ClientSecret string `json:"clientSecret"`
}

// normalize checks the body and returns the setup to store and the secret to write,
// empty when the held one is kept.
func (b linkSetupBody) normalize() (microsoftLinkSetup, string, error) {
	tenant, client := strings.TrimSpace(b.TenantID), strings.TrimSpace(b.ClientID)
	if !guidShape.MatchString(tenant) {
		return microsoftLinkSetup{}, "", errors.New("tenantId must be the directory (tenant) id, a GUID that Entra shows on the app registration's overview; a domain name or common, organizations or consumers is not accepted, because every token is checked against this one tenant")
	}
	if !guidShape.MatchString(client) {
		return microsoftLinkSetup{}, "", errors.New("clientId must be the application (client) id, a GUID that Entra shows on the app registration's overview")
	}
	var scopes []string
	for _, sc := range strings.Fields(b.Scopes) {
		switch strings.ToLower(sc) {
		case "openid", "profile", "offline_access":
			continue // always asked for
		}
		if len(sc) > linkMaxScopeLen || !scopeShape.MatchString(sc) {
			return microsoftLinkSetup{}, "", fmt.Errorf("scope %q is not a scope: printable characters without quotes or backslashes, at most %d of them", sc, linkMaxScopeLen)
		}
		scopes = append(scopes, sc)
	}
	if len(scopes) == 0 {
		return microsoftLinkSetup{}, "", errors.New("scopes must name at least one delegated permission a connection is for, e.g. https://graph.microsoft.com/Sites.ReadWrite.All; openid, profile and offline_access are always added")
	}
	if len(scopes) > linkMaxScopes {
		return microsoftLinkSetup{}, "", fmt.Errorf("at most %d scopes", linkMaxScopes)
	}
	secret := strings.TrimSpace(b.ClientSecret)
	if len(secret) > linkMaxSecretLen || strings.ContainsFunc(secret, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return microsoftLinkSetup{}, "", fmt.Errorf("clientSecret must be the secret's value as Entra shows it once: at most %d characters, none of them a control character", linkMaxSecretLen)
	}
	return microsoftLinkSetup{TenantID: strings.ToLower(tenant), ClientID: strings.ToLower(client),
		Scopes: strings.Join(scopes, " ")}, secret, nil
}

// refuseLinkSetupChange answers a write the server cannot take, and reports whether
// it did: without the vault there is nowhere for the secret and the connections to
// live, and with start flags the flags decide.
func (s *Server) refuseLinkSetupChange(w http.ResponseWriter) bool {
	switch {
	case s.identity == nil:
		httpapi.Error(w, http.StatusServiceUnavailable, "account linking needs the vault, which is off on this server")
		return true
	case s.msLinkStart.given():
		httpapi.Error(w, http.StatusConflict, "account linking is set by this server's start flags (--link-microsoft-*); change it there, or remove them to set it up here")
		return true
	}
	return false
}

// handleGetMicrosoftLinkSetup tells an administrator how linking is set up (admin-only).
func (s *Server) handleGetMicrosoftLinkSetup(w http.ResponseWriter, r *http.Request) {
	v, err := s.linkSetupView(r)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, v)
}

// handlePutMicrosoftLinkSetup sets up, or changes, linking from the Console
// (admin-only). The secret is written before the setup that names it, and whatever
// is durable afterwards — all of it, or on a failure what was there before or half
// of it — is what runs, so the server never links through a configuration its disk
// does not hold.
func (s *Server) handlePutMicrosoftLinkSetup(w http.ResponseWriter, r *http.Request) {
	if s.refuseLinkSetupChange(w) {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Request))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var body linkSetupBody
	if err := json.Unmarshal(raw, &body); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	set, secret, err := body.normalize()
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	var (
		missing  bool
		writeErr error
		cfg      MicrosoftLinkConfig
	)
	s.do(func() {
		_, held, _, err := s.loadMicrosoftLinkSetup()
		if secret == "" && (err != nil || held == "") {
			// A web app registration redeems its code with a secret; a setup without
			// one could only ever fail at Microsoft.
			missing = true
			return
		}
		if secret != "" {
			if _, writeErr = s.vault.Set(linkSecretName, secret); writeErr != nil {
				return
			}
		}
		writeErr = sidecar.WriteJSON(s.linkSetupDir(), s.linkSetupFile(), set)
		// Whatever the disk now holds is what runs.
		if stored, held, ok, err := s.loadMicrosoftLinkSetup(); err == nil && ok {
			cfg = stored.config(held, s.msLinkStart.Authority)
		}
	})
	if missing {
		httpapi.Error(w, http.StatusBadRequest, "clientSecret is required: create one under the app registration's Certificates & secrets and enter its value")
		return
	}
	s.installMicrosoftLink(cfg)
	if writeErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "store the linking setup: "+writeErr.Error())
		return
	}
	audit(r, logging.LinkingSetUp, "account linking set up in the Console",
		slog.String("tenant", set.TenantID), slog.String("clientId", set.ClientID),
		slog.String("scopes", set.Scopes), slog.Bool("secretReplaced", secret != ""))
	s.handleGetMicrosoftLinkSetup(w, r)
}

// handleDeleteMicrosoftLinkSetup removes the Console's setup and its client secret
// (admin-only). Linking is off from the next request on; the connections people
// authorized stay, and cannot act until linking is set up again for their tenant or
// they are revoked.
func (s *Server) handleDeleteMicrosoftLinkSetup(w http.ResponseWriter, r *http.Request) {
	if s.refuseLinkSetupChange(w) {
		return
	}
	var (
		err error
		cfg MicrosoftLinkConfig
	)
	s.do(func() {
		// The setup file is what decides, so it goes first: once it is gone nothing
		// links, whether or not the secret's removal goes through.
		if err = os.Remove(s.linkSetupFile()); err == nil || errors.Is(err, os.ErrNotExist) {
			if err = sidecar.FsyncDir(s.linkSetupDir()); err == nil {
				err = s.vault.Delete(linkSecretName)
			}
		}
		if stored, held, ok, lerr := s.loadMicrosoftLinkSetup(); lerr == nil && ok {
			cfg = stored.config(held, s.msLinkStart.Authority)
		}
	})
	s.installMicrosoftLink(cfg)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "remove the linking setup: "+err.Error())
		return
	}
	audit(r, logging.LinkingRemoved, "account linking removed in the Console")
	s.handleGetMicrosoftLinkSetup(w, r)
}
