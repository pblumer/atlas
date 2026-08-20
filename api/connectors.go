package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/clio"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/connector/remedy"
	"github.com/pblumer/atlas/connector/sharepoint"
	"github.com/pblumer/atlas/connector/temis"

	"github.com/pblumer/atlas/api/httpapi"
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

// connectorProblem is why a connector an operator *did* configure is missing from
// its kind's registry. A connector that cannot be built is deliberately left out so
// its tasks park rather than run wrongly (ADR-0093/0141), but the reason used to be
// dropped at the point of skipping — leaving a parked token to report "no connector
// registered as X", which reads as *you never configured it*. Each builder below now
// records the reason instead, and the registry carries it to the incident (ADR-0158).
//
// The strings are read by an operator in an incident message and in the connector
// list, so they say what to go and fix, not what the code decided.
const (
	problemDisabled   = "the connector is disabled"
	problemNoEndpoint = "no endpoint is configured"
)

// noteForeignKinds records, for every connector record that ended up as neither a
// client nor a problem of its own, that it is configured as a *different* kind than
// the one being built. A mail task pointed at the name of a clio connector is a
// mistake an operator actually makes, and it is the one case where "no connector
// registered" is not merely unhelpful but wrong.
func noteForeignKinds[T any](problems map[string]string, recs []connector, kind string, clients map[string]T) {
	for _, c := range recs {
		if _, ok := clients[c.Name]; ok {
			continue
		}
		if _, ok := problems[c.Name]; ok {
			continue
		}
		problems[c.Name] = fmt.Sprintf("it is configured as a %q connector, not %q", c.Kind, kind)
	}
}

// buildTemisClients assembles the temis connector clients from the environment
// (base) plus the enabled managed connector instances (which override by name),
// resolving each managed instance's token from its credentialsRef. It reads the
// connector store, so callers run it on the run-loop goroutine (the store's owner).
func (s *Server) buildTemisClients() (map[string]temis.Client, map[string]string, error) {
	clients := envTemisClients()
	problems := map[string]string{}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return nil, nil, err
	}
	for _, c := range recs {
		if c.Kind != connectorKindTemis {
			continue
		}
		switch {
		case !c.Enabled:
			problems[c.Name] = problemDisabled
		case strings.TrimSpace(c.Endpoint) == "":
			problems[c.Name] = problemNoEndpoint
		default:
			clients[c.Name] = temis.NewHTTPClient(temis.Connector{
				Endpoint: strings.TrimSpace(c.Endpoint),
				Token:    s.resolveConnectorSecret(c.CredentialsRef),
			})
		}
	}
	noteForeignKinds(problems, recs, connectorKindTemis, clients)
	return clients, problems, nil
}

// buildClioClients assembles the clio connector clients from the enabled managed
// connector instances of kind "clio", resolving each instance's token from its
// credentialsRef via the vault (ADR-0036/0041). It reads the connector store, so
// callers run it on the run-loop goroutine (the store's owner). It mirrors
// buildTemisClients; clio has no environment base (its endpoints are managed only).
func (s *Server) buildClioClients() (map[string]clio.Client, map[string]string, error) {
	clients := map[string]clio.Client{}
	problems := map[string]string{}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return nil, nil, err
	}
	for _, c := range recs {
		if c.Kind != connectorKindClio {
			continue
		}
		switch {
		case !c.Enabled:
			problems[c.Name] = problemDisabled
		case strings.TrimSpace(c.Endpoint) == "":
			problems[c.Name] = problemNoEndpoint
		default:
			clients[c.Name] = clio.NewHTTPClient(clio.Connector{
				Endpoint: strings.TrimSpace(c.Endpoint),
				Token:    s.resolveConnectorSecret(c.CredentialsRef),
			})
		}
	}
	noteForeignKinds(problems, recs, connectorKindClio, clients)
	return clients, problems, nil
}

// buildMailClients assembles the mail connector clients from the enabled managed
// connector instances of kind "mail", resolving each instance's credential from its
// credentialsRef via the vault (ADR-0079/0081/0041). It reads the connector store, so
// callers run it on the run-loop goroutine (the store's owner). It mirrors
// buildClioClients; a mail connector has no environment base (its provider and
// credentials are managed only). Provider dispatch (SMTP, Gmail, Microsoft Graph)
// lives in mail.NewProviderClient; a record whose provider is misconfigured — an
// unparseable credential bundle, a missing field, an endpoint that names no host — is
// skipped (its tasks park) rather than failing the whole rebuild. The preview provider
// (ADR-0150) needs no credential at all and is handed the server's outbox instead. The resolved secret is an SMTP password or, for a
// native provider, the OAuth credential JSON bundle held in the vault (I6).
func (s *Server) buildMailClients() (map[string]mail.Client, map[string]string, error) {
	clients := map[string]mail.Client{}
	problems := map[string]string{}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return nil, nil, err
	}
	for _, c := range recs {
		if c.Kind != connectorKindMail {
			continue
		}
		if !c.Enabled {
			problems[c.Name] = problemDisabled
			continue
		}
		provider := strings.TrimSpace(c.Provider)
		if provider == "" {
			provider = mail.ProviderSMTP
		}
		// SMTP needs a submission endpoint and the native providers a credential;
		// mail.NewProviderClient is the single judge of both, so a record it cannot
		// build is skipped below rather than pre-screened here.
		client, err := mail.NewProviderClient(mail.ProviderConfig{
			Provider: provider,
			Endpoint: strings.TrimSpace(c.Endpoint),
			Sender:   strings.TrimSpace(c.Sender),
			Secret:   s.resolveConnectorSecret(c.CredentialsRef),
			Name:     c.Name,
			Outbox:   s.mailOutbox,
		})
		if err != nil {
			// Misconfigured provider: its tasks park until it is fixed (ADR-0093) — and
			// the incident now says which of the provider's requirements is unmet
			// instead of claiming the connector does not exist (ADR-0158).
			problems[c.Name] = err.Error()
			continue
		}
		clients[c.Name] = client
	}
	noteForeignKinds(problems, recs, connectorKindMail, clients)
	return clients, problems, nil
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
func (s *Server) buildSharePointClients() (map[string]sharepoint.Client, map[string]string, error) {
	clients := map[string]sharepoint.Client{}
	problems := map[string]string{}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return nil, nil, err
	}
	for _, c := range recs {
		if c.Kind != connectorKindSharePoint {
			continue
		}
		if !c.Enabled {
			problems[c.Name] = problemDisabled
			continue
		}
		client, err := sharepoint.NewProviderClient(sharepoint.ProviderConfig{
			Endpoint: strings.TrimSpace(c.Endpoint),
			Secret:   s.resolveConnectorSecret(c.CredentialsRef),
		})
		if err != nil {
			problems[c.Name] = err.Error() // its tasks park until it is fixed (ADR-0141)
			continue
		}
		clients[c.Name] = client
	}
	noteForeignKinds(problems, recs, connectorKindSharePoint, clients)
	return clients, problems, nil
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
func (s *Server) buildRemedyClients() (map[string]remedy.Client, map[string]string, error) {
	clients := map[string]remedy.Client{}
	problems := map[string]string{}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return nil, nil, err
	}
	for _, c := range recs {
		if c.Kind != connectorKindRemedy {
			continue
		}
		raw := strings.TrimSpace(s.resolveConnectorSecret(c.CredentialsRef))
		var creds remedyCredentials
		switch {
		case !c.Enabled:
			problems[c.Name] = problemDisabled
		case strings.TrimSpace(c.Endpoint) == "":
			problems[c.Name] = problemNoEndpoint
		case raw == "":
			problems[c.Name] = "no credential is configured (set credentialsRef to a {username,password} bundle in the vault)"
		default:
			// A malformed bundle: park rather than call Remedy wrongly.
			if err := json.Unmarshal([]byte(raw), &creds); err != nil {
				problems[c.Name] = "the credential is not valid JSON: " + err.Error()
				continue
			}
			clients[c.Name] = remedy.NewHTTPClient(remedy.Connector{
				BaseURL:  strings.TrimSpace(c.Endpoint),
				Username: creds.Username,
				Password: creds.Password,
			})
		}
	}
	noteForeignKinds(problems, recs, connectorKindRemedy, clients)
	return clients, problems, nil
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
	var views []connectorView
	// The store read and the registry reads happen in the same closure: the registries
	// are owned by the run-loop goroutine (rebuild swaps them there), so asking them
	// anywhere else would race a rebuild (invariant I3).
	s.do(func() {
		recs, loadErr = s.connectors.LoadAll()
		if loadErr != nil {
			return
		}
		views = make([]connectorView, 0, len(recs))
		for _, c := range recs {
			views = append(views, connectorView{connector: c, Problem: s.connectorProblem(c.Kind, c.Name)})
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list connectors: "+loadErr.Error())
		return
	}
	if views == nil {
		views = []connectorView{}
	}
	httpapi.JSON(w, http.StatusOK, views)
}

// connectorView is a connector record as the operator UI reads it: the stored record
// plus what the runtime made of it. Problem is why its client could not be built —
// empty when the connector is usable — and is derived on read, never stored (ADR-0158).
type connectorView struct {
	connector
	Problem string `json:"problem,omitempty"`
}

// connectorProblem asks the kind's live registry why this connector is not usable, or
// returns "" when it is (or when the kind has no registry). The registries are owned
// by the run-loop goroutine, so callers must be on it (invariant I3).
func (s *Server) connectorProblem(kind, name string) string {
	for _, k := range managedConnectorKinds {
		if k.name != kind || k.problem == nil {
			continue
		}
		if why, ok := k.problem(s, name); ok {
			return why
		}
		return ""
	}
	return ""
}

// handleCreateConnector creates a managed connector instance and rebuilds the
// runtime registry so a central decision referencing it starts resolving at once.
func (s *Server) handleCreateConnector(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p createConnectorParams
	if err := json.Unmarshal(body, &p); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
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
		httpapi.Error(w, http.StatusBadRequest, "connector name is required")
		return
	}
	kind, ok := lookupManagedConnectorKind(p.Kind)
	if !ok {
		httpapi.Error(w, http.StatusBadRequest, managedConnectorKindsError())
		return
	}
	// The kind's validator applies its own rules and normalizes p (defaulting a mail
	// provider, clearing mail-only fields for kinds that don't use them).
	if msg := kind.validateCreate(&p); msg != "" {
		httpapi.Error(w, http.StatusBadRequest, msg)
		return
	}
	id, err := newID()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "generate id: "+err.Error())
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
		httpapi.Error(w, http.StatusConflict, "a connector named "+p.Name+" already exists")
		return
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save connector: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, rec)
}

// handleUpdateConnector applies a partial change to a managed connector (endpoint,
// credential reference, or enabled state) and rebuilds the registry.
func (s *Server) handleUpdateConnector(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	// Every field an operator can change on a stored connector. Provider is here
	// because switching one — an SMTP host that will not authenticate moved to the
	// in-app preview transport, say — is the fix for a whole class of parked mail
	// tasks, and re-creating the connector under the same name to change it would
	// break every model referencing it (ADR-0160).
	var p struct {
		Endpoint       *string `json:"endpoint"`
		CredentialsRef *string `json:"credentialsRef"`
		Provider       *string `json:"provider"`
		Sender         *string `json:"sender"`
		Enabled        *bool   `json:"enabled"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	var (
		rec        connector
		found      bool
		saveErr    error
		badRequest string
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
		if p.Provider != nil {
			rec.Provider = strings.TrimSpace(*p.Provider)
		}
		if p.Sender != nil {
			rec.Sender = strings.TrimSpace(*p.Sender)
		}
		if p.Enabled != nil {
			rec.Enabled = *p.Enabled
		}
		// The updated record has to satisfy what a create would have demanded of it —
		// an SMTP endpoint that dials, a provider that has a credential — or the change
		// is refused here instead of parking a token later (ADR-0150/0159).
		if badRequest = normalizeConnectorUpdate(&rec); badRequest != "" {
			return
		}
		if saveErr = s.connectors.Save(rec); saveErr != nil {
			return
		}
		saveErr = s.rebuildConnectorRegistries()
	})
	switch {
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "update connector: "+saveErr.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no connector with that id")
		return
	case badRequest != "":
		httpapi.Error(w, http.StatusBadRequest, badRequest)
		return
	}
	httpapi.JSON(w, http.StatusOK, rec)
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
		httpapi.Error(w, http.StatusInternalServerError, "delete connector: "+delErr.Error())
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
		httpapi.Error(w, http.StatusServiceUnavailable, "vault not configured")
		return
	}
	connID := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
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
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	adminToken := strings.TrimSpace(p.AdminToken)
	subject := strings.TrimSpace(p.Subject)
	if adminToken == "" || subject == "" {
		httpapi.Error(w, http.StatusBadRequest, "adminToken and subject are required")
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
		httpapi.Error(w, http.StatusInternalServerError, "load connector: "+loadErr.Error())
		return
	}
	if !ok || conn.Kind != connectorKindClio {
		httpapi.Error(w, http.StatusBadRequest, "no clio connector with that id")
		return
	}
	if strings.TrimSpace(conn.Endpoint) == "" {
		httpapi.Error(w, http.StatusBadRequest, "connector has no endpoint")
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
		httpapi.Error(w, http.StatusBadGateway, "mint clio key: "+err.Error())
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
		httpapi.Error(w, http.StatusInternalServerError, "store provisioned key: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{
		"credentialsRef": ref,
		"scope":          scope,
		"keyName":        keyName,
	})
}

// handleMailOutbox lists what the preview mail provider has delivered, newest first —
// the Outbox view in Operations (ADR-0150). An optional ?limit= returns only the
// newest n; the response's "truncated" says older messages were left behind, by that
// limit or by the outbox's own capacity.
//
// It deliberately does not go through s.do: the outbox is not run-loop state. A mail
// worker writes it off the loop after fsync and the outbox holds its own lock, so
// reading it here keeps the single writer free for the engine's own work, and a
// browser polling the view can never slow a running process down.
func (s *Server) handleMailOutbox(w http.ResponseWriter, r *http.Request) {
	limit := 0 // everything the outbox holds — it is capacity-bounded already
	if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n <= 0 {
			httpapi.Error(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
			return
		}
		limit = n
	}
	msgs, truncated := []mail.OutboxMessage{}, false
	if s.mailOutbox != nil {
		msgs, truncated = s.mailOutbox.Messages(limit)
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"messages": msgs, "truncated": truncated})
}

// handleClearMailOutbox empties the preview outbox. Nothing here was ever sent and
// nothing survives a restart, so clearing it destroys no record of anything —
// it is the "start from a clean slate" the next trial run wants.
func (s *Server) handleClearMailOutbox(w http.ResponseWriter, _ *http.Request) {
	if s.mailOutbox != nil {
		s.mailOutbox.Clear()
	}
	w.WriteHeader(http.StatusNoContent)
}

// connectorTestTimeout backstops a connector check. Each attempt inside it is already
// bounded by the shared connector call budget (ADR-0149) — this only bounds a check
// that somehow outlives its own transport, so that a person waiting in front of the
// form always gets an answer.
const connectorTestTimeout = 2 * nettimeout.Default

// connectorTestReq is the body of a connector check: the connector's own fields — the
// same shape a create takes, so the form can check what is *typed*, before it is
// saved, which is the moment a mistake is cheapest to fix — plus an optional
// recipient. Without a recipient the check stops at the door: connect, upgrade,
// authenticate. With one it sends a real message, the only way to learn what the
// provider does with a message rather than with a connection.
type connectorTestReq struct {
	createConnectorParams
	To string `json:"to"`
}

// handleTestConnector checks a mail connector and reports what happened in words the
// person who typed it can act on (ADR-0150).
//
// It exists because every failure this catches used to be discovered the same way: a
// process ran, a token parked, and an incident carried the provider's error to
// whoever thought to look for it, hours later and far from the form where the mistake
// was made. A revoked refresh token, a host that does not resolve, a password the
// server rejects — all of them are answerable in a second at configuration time, and
// none of them were being asked.
//
// A failed check is a 200 carrying ok:false, not an HTTP error: the request was
// served correctly and the answer is "no". Only a malformed or unusable request
// (a kind with nothing to check, an endpoint that cannot dial) is a 400. The vault
// read rides the run loop because the store's owner is the run loop; the network call
// never does (I3).
func (s *Server) handleTestConnector(w http.ResponseWriter, r *http.Request) {
	var req connectorTestReq
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Kind == "" {
		req.Kind = connectorKindMail
	}
	if req.Kind != connectorKindMail {
		httpapi.Error(w, http.StatusBadRequest, "only a mail connector can be checked today")
		return
	}
	if msg := validateMailConnector(&req.createConnectorParams); msg != "" {
		httpapi.Error(w, http.StatusBadRequest, msg)
		return
	}

	var secret string
	s.do(func() { secret = s.resolveConnectorSecret(req.CredentialsRef) })

	client, err := mail.NewProviderClient(mail.ProviderConfig{
		Provider: req.Provider,
		Endpoint: req.Endpoint,
		Sender:   req.Sender,
		Secret:   secret,
		Name:     req.Name,
		Outbox:   s.mailOutbox,
	})
	if err != nil {
		httpapi.JSON(w, http.StatusOK, map[string]any{"ok": false, "detail": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), connectorTestTimeout)
	defer cancel()

	to := strings.TrimSpace(req.To)
	if to == "" {
		err = mail.Probe(ctx, client)
	} else {
		err = client.Send(ctx, mail.Message{
			To:      []string{to},
			Subject: "Atlas connector test",
			Body: "This is a test message from the Atlas connector \"" + req.Name + "\".\n\n" +
				"It was sent from the connector form, not by a process. If it reached you, " +
				"the connector can deliver mail.\n",
		})
	}
	if err != nil {
		httpapi.JSON(w, http.StatusOK, map[string]any{"ok": false, "detail": err.Error()})
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"ok": true, "detail": connectorTestDetail(req.Provider, req.Endpoint, to)})
}

// connectorTestDetail says what the check actually proved, rather than "OK" — the
// difference between "the credential was accepted" and "a message was delivered"
// matters to whoever has to decide the connector is finished.
func connectorTestDetail(provider, endpoint, to string) string {
	switch {
	case to != "" && provider == mail.ProviderPreview:
		return "Test message delivered to the outbox (Operations › Outbox) — the preview provider sends nothing outward."
	case to != "":
		return "Test message sent to " + to + ". If it does not arrive, the message left Atlas and the provider still has it."
	case provider == mail.ProviderPreview:
		return "Ready. The preview provider dials nothing: messages land in the outbox (Operations › Outbox)."
	case provider == mail.ProviderSMTP:
		return "Connected to " + endpoint + " and authenticated. No message was sent."
	default:
		return "Credential accepted — the provider issued an access token. No message was sent."
	}
}

// connectorWarnings checks a freshly compiled process's connector references against
// what is actually configured, and returns one sentence per reference that will not
// resolve. A deploy is not refused for it: a model is routinely deployed before its
// connectors are provisioned, and to an environment where they are provisioned later —
// refusing would be wrong, and staying silent is how a model reaches production
// referring to a connector nobody ever created. So it warns, at the moment somebody is
// looking, instead of leaving the first token to discover it (ADR-0158).
//
// It reads the connector store and the live registries, so it runs on the run-loop
// goroutine (invariant I3), inside the same closure as the deploy it describes.
func (s *Server) connectorWarnings(cp *compiler.CompiledProcess) []string {
	refs := cp.ConnectorRefs()
	if len(refs) == 0 {
		return nil
	}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return nil // the deploy itself succeeded; a warning is best-effort
	}
	byName := map[string]connector{}
	for _, c := range recs {
		byName[c.Name] = c
	}
	var out []string
	seen := map[string]bool{}
	for _, ref := range refs {
		kind := connectorKindOfJobType(ref.JobType)
		if kind == "" || seen[kind+"/"+ref.Connector] {
			continue // a job type no managed kind claims (a local decision, say)
		}
		seen[kind+"/"+ref.Connector] = true
		rec, exists := byName[ref.Connector]
		switch {
		case !exists:
			out = append(out, fmt.Sprintf("%s references the %s connector %q, which is not configured on this server — its tasks will park until it is created",
				ref.ElementId, kind, ref.Connector))
		case rec.Kind != kind:
			out = append(out, fmt.Sprintf("%s references %q as a %s connector, but it is configured as a %q connector",
				ref.ElementId, ref.Connector, kind, rec.Kind))
		default:
			if why := s.connectorProblem(kind, ref.Connector); why != "" {
				out = append(out, fmt.Sprintf("%s references the %s connector %q, which is configured but not usable: %s",
					ref.ElementId, kind, ref.Connector, why))
			}
		}
	}
	return out
}

// connectorKindOfJobType maps a reserved job-type index back to the managed connector
// kind whose registry serves it, or "" for a job type no managed kind claims.
func connectorKindOfJobType(jobType int32) string {
	for _, k := range managedConnectorKinds {
		for _, jt := range k.jobTypes {
			if jt == jobType {
				return k.name
			}
		}
	}
	return ""
}

// elementConnectorRef is the model half of "which connector is this element stuck
// on?": the connector name the task refers to and the connector kind its reserved job
// type says it needs. Both empty when the element refers to no connector — a local
// decision, a task of another type, an element the compiled process does not describe
// that way (ADR-0160).
func elementConnectorRef(cp *compiler.CompiledProcess, elementID int32) (name, kind string) {
	if cp == nil {
		return "", ""
	}
	ref, ok := cp.NodeConnectorRef(elementID)
	if !ok {
		return "", "" // a local decision, or a task that names no connector
	}
	return ref.Connector, connectorKindOfJobType(ref.JobType)
}

// incidentConnectorLookup returns the resolver an incident listing uses to answer that
// question for every row: the connector's name, the kind it needs, and the id of the
// configured record standing under that name, if one is. An incident on a connector
// task is very often a connector problem, and the fix is a field on the connector
// rather than anything about the instance — so the incident carries what it takes to
// get there in one click, instead of leaving an operator to read a name out of a
// message and go hunting (ADR-0160).
//
// It is a closure rather than a plain function because both callers loop over up to
// thousands of incidents on the run-loop goroutine: the connector store is read at
// most once per listing, and not at all when nothing on the page is on a connector
// task. A per-incident directory read there would block every writer behind it
// (invariant I3).
//
// Everything is derived on read from the compiled process and the connector store;
// nothing about it is durable (I6). An empty id means no connector of that kind is
// configured under the name — a record under the same name but *another* kind is not
// this task's connector, and offering it would open the wrong one.
func (s *Server) incidentConnectorLookup() func(cp *compiler.CompiledProcess, elementID int32) (name, kind, id string) {
	var (
		recs   []connector
		loaded bool
	)
	return func(cp *compiler.CompiledProcess, elementID int32) (string, string, string) {
		name, kind := elementConnectorRef(cp, elementID)
		if name == "" || kind == "" {
			return name, kind, ""
		}
		if !loaded {
			loaded = true
			recs, _ = s.connectors.LoadAll() // on error the name and kind are still worth having
		}
		for _, c := range recs {
			if c.Name == name && c.Kind == kind {
				return name, kind, c.ID
			}
		}
		return name, kind, ""
	}
}
