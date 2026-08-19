package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/clio"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/connector/remedy"
	"github.com/pblumer/atlas/connector/sharepoint"
	"github.com/pblumer/atlas/connector/temis"
)

// clioReadScope builds the clio scope string granting read on a subject: the exact
// subject ("read:/employees") or, when recursive, its whole subtree
// ("read:/employees/*") — the recursive grant a subtree watch needs. The subject is
// made absolute first (clio subjects begin with "/").
func clioReadScope(subject string, recursive bool) string {
	subject = strings.TrimSpace(subject)
	if !strings.HasPrefix(subject, "/") {
		subject = "/" + subject
	}
	subject = strings.TrimRight(subject, "/")
	if subject == "" {
		subject = "/"
	}
	if recursive {
		if subject == "/" {
			return "read:/*"
		}
		return "read:" + subject + "/*"
	}
	return "read:" + subject
}

// envConnectorSecret returns the token for a connector's credentialsRef from the
// environment per the ADR-0041 A2 secret model: the engine stores only the
// reference; the value lives in ATLAS_CONNECTOR_<REF>_TOKEN (REF normalized like a
// connector name). An empty ref yields no token.
func envConnectorSecret(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv("ATLAS_CONNECTOR_" + connectorEnvKey(ref) + "_TOKEN"))
}

// resolveConnectorSecret resolves a credentialsRef to a token, consulting the
// engine-internal encrypted vault first (ADR-0069) and falling back to the
// environment reference (ADR-0041 A2) on a miss or when the vault is unconfigured.
// The decrypted value lives only in the returned string in the caller's memory at
// call time — it is never written to a variable, the WAL, or an event (I6). Runs on
// the run-loop goroutine (the vault store's owner), as buildTemisClients does.
func (s *Server) resolveConnectorSecret(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if s.vault != nil {
		if v, ok, err := s.vault.Get(ref); err == nil && ok {
			return v
		}
	}
	return envConnectorSecret(ref)
}

// envTemisClients builds the temis connector clients configured purely from the
// environment (the pre-managed mechanism, kept as a base): ATLAS_TEMIS_CONNECTORS
// plus per-name ATLAS_TEMIS_<NAME>_URL/_TOKEN.
func envTemisClients() map[string]temis.Client {
	out := map[string]temis.Client{}
	for _, name := range strings.Split(os.Getenv("ATLAS_TEMIS_CONNECTORS"), ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := connectorEnvKey(name)
		url := strings.TrimSpace(os.Getenv("ATLAS_TEMIS_" + key + "_URL"))
		if url == "" {
			continue
		}
		out[name] = temis.NewHTTPClient(temis.Connector{
			Endpoint: url,
			Token:    strings.TrimSpace(os.Getenv("ATLAS_TEMIS_" + key + "_TOKEN")),
		})
	}
	return out
}

// buildTemisClients assembles the temis connector clients from the environment
// (base) plus the enabled managed connector instances (which override by name),
// resolving each managed instance's token from its credentialsRef. It reads the
// connector store, so callers run it on the run-loop goroutine (the store's owner).
func (s *Server) buildTemisClients() (map[string]temis.Client, error) {
	clients := envTemisClients()
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return nil, err
	}
	for _, c := range recs {
		if !c.Enabled || c.Kind != connectorKindTemis || strings.TrimSpace(c.Endpoint) == "" {
			continue
		}
		clients[c.Name] = temis.NewHTTPClient(temis.Connector{
			Endpoint: strings.TrimSpace(c.Endpoint),
			Token:    s.resolveConnectorSecret(c.CredentialsRef),
		})
	}
	return clients, nil
}

// buildClioClients assembles the clio connector clients from the enabled managed
// connector instances of kind "clio", resolving each instance's token from its
// credentialsRef via the vault (ADR-0036/0041). It reads the connector store, so
// callers run it on the run-loop goroutine (the store's owner). It mirrors
// buildTemisClients; clio has no environment base (its endpoints are managed only).
func (s *Server) buildClioClients() (map[string]clio.Client, error) {
	clients := map[string]clio.Client{}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return nil, err
	}
	for _, c := range recs {
		if !c.Enabled || c.Kind != connectorKindClio || strings.TrimSpace(c.Endpoint) == "" {
			continue
		}
		clients[c.Name] = clio.NewHTTPClient(clio.Connector{
			Endpoint: strings.TrimSpace(c.Endpoint),
			Token:    s.resolveConnectorSecret(c.CredentialsRef),
		})
	}
	return clients, nil
}

// buildMailClients assembles the mail connector clients from the enabled managed
// connector instances of kind "mail", resolving each instance's credential from its
// credentialsRef via the vault (ADR-0079/0081/0041). It reads the connector store, so
// callers run it on the run-loop goroutine (the store's owner). It mirrors
// buildClioClients; a mail connector has no environment base (its provider and
// credentials are managed only). Provider dispatch (SMTP, Gmail, Microsoft Graph)
// lives in mail.NewProviderClient; a record whose provider is misconfigured — an
// unparseable credential bundle, a missing field — is skipped (its tasks park) rather
// than failing the whole rebuild. The resolved secret is an SMTP password or, for a
// native provider, the OAuth credential JSON bundle held in the vault (I6).
func (s *Server) buildMailClients() (map[string]mail.Client, error) {
	clients := map[string]mail.Client{}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return nil, err
	}
	for _, c := range recs {
		if !c.Enabled || c.Kind != connectorKindMail {
			continue
		}
		provider := strings.TrimSpace(c.Provider)
		if provider == "" {
			provider = mail.ProviderSMTP
		}
		// SMTP needs a submission endpoint; the native providers default their API base.
		if provider == mail.ProviderSMTP && strings.TrimSpace(c.Endpoint) == "" {
			continue
		}
		client, err := mail.NewProviderClient(mail.ProviderConfig{
			Provider: provider,
			Endpoint: strings.TrimSpace(c.Endpoint),
			Sender:   strings.TrimSpace(c.Sender),
			Secret:   s.resolveConnectorSecret(c.CredentialsRef),
		})
		if err != nil {
			continue // misconfigured provider: its tasks park until it is fixed (ADR-0093)
		}
		clients[c.Name] = client
	}
	return clients, nil
}

// buildSharePointClients assembles the SharePoint connector clients from the enabled
// managed connector instances of kind "sharepoint", resolving each instance's OAuth
// credential bundle from its credentialsRef via the vault (ADR-0141/0041). It reads
// the connector store, so callers run it on the run-loop goroutine (the store's
// owner). It mirrors buildMailClients; provider construction (Graph base + token
// source) lives in sharepoint.NewProviderClient, and a record whose credential bundle
// is misconfigured — unparseable, a missing field — is skipped (its tasks park)
// rather than failing the whole rebuild. The resolved secret is the OAuth credential
// JSON bundle held in the vault (I6), never a value in a model.
func (s *Server) buildSharePointClients() (map[string]sharepoint.Client, error) {
	clients := map[string]sharepoint.Client{}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return nil, err
	}
	for _, c := range recs {
		if !c.Enabled || c.Kind != connectorKindSharePoint {
			continue
		}
		client, err := sharepoint.NewProviderClient(sharepoint.ProviderConfig{
			Endpoint: strings.TrimSpace(c.Endpoint),
			Secret:   s.resolveConnectorSecret(c.CredentialsRef),
		})
		if err != nil {
			continue // misconfigured credential: its tasks park until it is fixed (ADR-0141)
		}
		clients[c.Name] = client
	}
	return clients, nil
}

// remedyCredentials is the shape of a Remedy connector's credential bundle held in
// the vault under its credentialsRef (ADR-0106): the AR System username and password
// used to obtain a JWT. Only a *reference* to this bundle is stored in the connector
// record; the values live in the vault, never in a model or the record (I6).
type remedyCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// buildRemedyClients assembles the Remedy connector clients from the enabled managed
// connector instances of kind "remedy", resolving each instance's credential bundle
// from its credentialsRef via the vault (ADR-0106/0041). It reads the connector store,
// so callers run it on the run-loop goroutine (the store's owner). It mirrors
// buildMailClients: a record with no endpoint, or whose credential bundle is missing
// or not valid JSON, is skipped (its tasks park) rather than failing the whole
// rebuild. The resolved secret is the {username,password} JSON bundle held in the
// vault (I6).
func (s *Server) buildRemedyClients() (map[string]remedy.Client, error) {
	clients := map[string]remedy.Client{}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return nil, err
	}
	for _, c := range recs {
		if !c.Enabled || c.Kind != connectorKindRemedy || strings.TrimSpace(c.Endpoint) == "" {
			continue
		}
		raw := strings.TrimSpace(s.resolveConnectorSecret(c.CredentialsRef))
		if raw == "" {
			continue // no credential configured yet: its tasks park until it is
		}
		var creds remedyCredentials
		if err := json.Unmarshal([]byte(raw), &creds); err != nil {
			continue // a malformed bundle: skip rather than call Remedy wrongly
		}
		clients[c.Name] = remedy.NewHTTPClient(remedy.Connector{
			BaseURL:  strings.TrimSpace(c.Endpoint),
			Username: creds.Username,
			Password: creds.Password,
		})
	}
	return clients, nil
}

// rebuildConnectorRegistries rebuilds every managed connector registry from the
// current connector store and swaps each live registry atomically, so a task
// referencing a changed connector starts (or stops) resolving at once. It iterates the
// managedConnectorKinds registry in order, so a CRUD handler never has to know which
// kinds exist and adding a kind needs no change here. Callers run it on the run-loop
// goroutine, inside the same s.do closure that saved the change, so the rebuild sees
// the write.
func (s *Server) rebuildConnectorRegistries() error {
	for _, k := range managedConnectorKinds {
		if err := k.rebuild(s); err != nil {
			return err
		}
	}
	return nil
}

// handleListConnectors lists the managed connector instances, oldest first. The
// records carry only credential *references*, never secrets, so nothing is
// redacted.
func (s *Server) handleListConnectors(w http.ResponseWriter, _ *http.Request) {
	var (
		recs    []connector
		loadErr error
	)
	s.do(func() { recs, loadErr = s.connectors.LoadAll() })
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list connectors: "+loadErr.Error())
		return
	}
	if recs == nil {
		recs = []connector{}
	}
	writeJSON(w, http.StatusOK, recs)
}

// handleCreateConnector creates a managed connector instance and rebuilds the
// runtime registry so a central decision referencing it starts resolving at once.
func (s *Server) handleCreateConnector(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p createConnectorParams
	if err := json.Unmarshal(body, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Kind = strings.TrimSpace(p.Kind)
	if p.Kind == "" {
		p.Kind = connectorKindTemis
	}
	p.Endpoint = strings.TrimSpace(p.Endpoint)
	p.Provider = strings.TrimSpace(p.Provider)
	p.Sender = strings.TrimSpace(p.Sender)
	p.CredentialsRef = strings.TrimSpace(p.CredentialsRef)
	if p.Name == "" {
		writeError(w, http.StatusBadRequest, "connector name is required")
		return
	}
	kind, ok := lookupManagedConnectorKind(p.Kind)
	if !ok {
		writeError(w, http.StatusBadRequest, managedConnectorKindsError())
		return
	}
	// The kind's validator applies its own rules and normalizes p (defaulting a mail
	// provider, clearing mail-only fields for kinds that don't use them).
	if msg := kind.validateCreate(&p); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	id, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate id: "+err.Error())
		return
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	rec := connector{
		ID: id, Name: p.Name, Kind: p.Kind, Endpoint: p.Endpoint,
		CredentialsRef: p.CredentialsRef, Enabled: enabled,
		Provider: p.Provider, Sender: p.Sender,
		CreatedAt: time.Now().Unix(),
	}

	var (
		dupErr  bool
		saveErr error
	)
	s.do(func() {
		existing, e := s.connectors.LoadAll()
		if e != nil {
			saveErr = e
			return
		}
		for _, c := range existing {
			if strings.EqualFold(c.Name, p.Name) {
				dupErr = true
				return
			}
		}
		if saveErr = s.connectors.Save(rec); saveErr != nil {
			return
		}
		saveErr = s.rebuildConnectorRegistries()
	})
	switch {
	case dupErr:
		writeError(w, http.StatusConflict, "a connector named "+p.Name+" already exists")
		return
	case saveErr != nil:
		writeError(w, http.StatusInternalServerError, "save connector: "+saveErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleUpdateConnector applies a partial change to a managed connector (endpoint,
// credential reference, or enabled state) and rebuilds the registry.
func (s *Server) handleUpdateConnector(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p struct {
		Endpoint       *string `json:"endpoint"`
		CredentialsRef *string `json:"credentialsRef"`
		Sender         *string `json:"sender"`
		Enabled        *bool   `json:"enabled"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	var (
		rec     connector
		found   bool
		saveErr error
	)
	s.do(func() {
		var e error
		rec, found, e = s.connectors.Get(id)
		if e != nil {
			saveErr = e
			return
		}
		if !found {
			return
		}
		if p.Endpoint != nil {
			rec.Endpoint = strings.TrimSpace(*p.Endpoint)
		}
		if p.CredentialsRef != nil {
			rec.CredentialsRef = strings.TrimSpace(*p.CredentialsRef)
		}
		if p.Sender != nil {
			rec.Sender = strings.TrimSpace(*p.Sender)
		}
		if p.Enabled != nil {
			rec.Enabled = *p.Enabled
		}
		if saveErr = s.connectors.Save(rec); saveErr != nil {
			return
		}
		saveErr = s.rebuildConnectorRegistries()
	})
	switch {
	case saveErr != nil:
		writeError(w, http.StatusInternalServerError, "update connector: "+saveErr.Error())
		return
	case !found:
		writeError(w, http.StatusNotFound, "no connector with that id")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleDeleteConnector removes a managed connector and rebuilds the registry so a
// decision referencing it parks again (until reconfigured).
func (s *Server) handleDeleteConnector(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var delErr error
	s.do(func() {
		if delErr = s.connectors.Delete(id); delErr != nil {
			return
		}
		delErr = s.rebuildConnectorRegistries()
	})
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, "delete connector: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleProvisionClioKey provisions a managed clio connector's credential in one
// step (ADR-0092): it mints a scoped read key on the connector's clio instance (using an admin
// token the operator supplies once) and seals the returned key in the vault as the
// connector's credential, then rebuilds the registries so the inbound bridge and
// outbound tasks use it at once — no copy-pasting a token. The admin token is used
// only for the one-off mint and is never stored (I6); only the scoped key is sealed.
// Admin-gated, and the mint (a network call) runs off the run loop (I3); only the
// connector read and the vault write ride s.do.
func (s *Server) handleProvisionClioKey(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "vault not configured")
		return
	}
	connID := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p struct {
		AdminToken string `json:"adminToken"`
		Subject    string `json:"subject"`
		Recursive  bool   `json:"recursive"`
		KeyName    string `json:"keyName"`
		ExpiresAt  string `json:"expiresAt"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	adminToken := strings.TrimSpace(p.AdminToken)
	subject := strings.TrimSpace(p.Subject)
	if adminToken == "" || subject == "" {
		writeError(w, http.StatusBadRequest, "adminToken and subject are required")
		return
	}

	// Resolve the connector on the run loop (the store's owner).
	var (
		conn    connector
		ok      bool
		loadErr error
	)
	s.do(func() { conn, ok, loadErr = s.connectors.Get(connID) })
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "load connector: "+loadErr.Error())
		return
	}
	if !ok || conn.Kind != connectorKindClio {
		writeError(w, http.StatusBadRequest, "no clio connector with that id")
		return
	}
	if strings.TrimSpace(conn.Endpoint) == "" {
		writeError(w, http.StatusBadRequest, "connector has no endpoint")
		return
	}

	keyName := strings.TrimSpace(p.KeyName)
	if keyName == "" {
		keyName = "atlas-" + conn.Name
	}
	scope := clioReadScope(subject, p.Recursive)

	// Mint the scoped key OFF the run loop (a network call, I3). The admin token
	// lives only for this call and is never written anywhere.
	token, err := clio.MintKey(r.Context(), strings.TrimSpace(conn.Endpoint), adminToken, clio.KeyRequest{
		Name:      keyName,
		Scopes:    []string{scope},
		ExpiresAt: strings.TrimSpace(p.ExpiresAt),
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "mint clio key: "+err.Error())
		return
	}

	// Seal the scoped key as the connector's credential and rebuild, on the run loop.
	ref := strings.TrimSpace(conn.CredentialsRef)
	setRef := ref == ""
	if setRef {
		ref = conn.Name
	}
	var saveErr error
	s.do(func() {
		if _, saveErr = s.vault.Set(ref, token); saveErr != nil {
			return
		}
		if setRef {
			conn.CredentialsRef = ref
			if saveErr = s.connectors.Save(conn); saveErr != nil {
				return
			}
		}
		saveErr = s.rebuildConnectorRegistries()
	})
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, "store provisioned key: "+saveErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"credentialsRef": ref,
		"scope":          scope,
		"keyName":        keyName,
	})
}
