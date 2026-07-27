package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pblumer/atlas/temis"
)

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
	recs, err := s.connectors.loadAll()
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

// handleListConnectors lists the managed connector instances, oldest first. The
// records carry only credential *references*, never secrets, so nothing is
// redacted.
func (s *Server) handleListConnectors(w http.ResponseWriter, _ *http.Request) {
	var (
		recs    []connector
		loadErr error
	)
	s.do(func() { recs, loadErr = s.connectors.loadAll() })
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
	var p struct {
		Name           string `json:"name"`
		Kind           string `json:"kind"`
		Endpoint       string `json:"endpoint"`
		CredentialsRef string `json:"credentialsRef"`
		Enabled        *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	name := strings.TrimSpace(p.Name)
	kind := strings.TrimSpace(p.Kind)
	if kind == "" {
		kind = connectorKindTemis
	}
	endpoint := strings.TrimSpace(p.Endpoint)
	if name == "" {
		writeError(w, http.StatusBadRequest, "connector name is required")
		return
	}
	if kind != connectorKindTemis {
		writeError(w, http.StatusBadRequest, "only the \"temis\" connector kind is configurable in this build")
		return
	}
	if endpoint == "" {
		writeError(w, http.StatusBadRequest, "connector endpoint is required")
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
		ID: id, Name: name, Kind: kind, Endpoint: endpoint,
		CredentialsRef: strings.TrimSpace(p.CredentialsRef), Enabled: enabled,
		CreatedAt: time.Now().Unix(),
	}

	var (
		dupErr  bool
		saveErr error
	)
	s.do(func() {
		existing, e := s.connectors.loadAll()
		if e != nil {
			saveErr = e
			return
		}
		for _, c := range existing {
			if strings.EqualFold(c.Name, name) {
				dupErr = true
				return
			}
		}
		if saveErr = s.connectors.save(rec); saveErr != nil {
			return
		}
		var clients map[string]temis.Client
		if clients, saveErr = s.buildTemisClients(); saveErr != nil {
			return
		}
		s.temisRegistry.Replace(clients)
	})
	switch {
	case dupErr:
		writeError(w, http.StatusConflict, "a connector named "+name+" already exists")
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
		rec, found, e = s.connectors.get(id)
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
		if p.Enabled != nil {
			rec.Enabled = *p.Enabled
		}
		if saveErr = s.connectors.save(rec); saveErr != nil {
			return
		}
		var clients map[string]temis.Client
		if clients, saveErr = s.buildTemisClients(); saveErr != nil {
			return
		}
		s.temisRegistry.Replace(clients)
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
		if delErr = s.connectors.delete(id); delErr != nil {
			return
		}
		var clients map[string]temis.Client
		if clients, delErr = s.buildTemisClients(); delErr != nil {
			return
		}
		s.temisRegistry.Replace(clients)
	})
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, "delete connector: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
