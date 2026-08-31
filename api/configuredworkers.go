package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// ConfiguredWorkerConfig contains non-secret configuration for a configured
// Worker. Credential material is represented separately by CredentialsRef.
type ConfiguredWorkerConfig struct {
	Endpoint string `json:"endpoint,omitempty"`
	Provider string `json:"provider,omitempty"`
	Sender   string `json:"sender,omitempty"`
}

// ConfiguredWorker is ADR-0208's canonical API representation over the existing
// connector record. It is a projection only; the connector store remains the
// persistence and runtime source of truth during the compatibility window.
type ConfiguredWorker struct {
	ID                 string                  `json:"id"`
	Name               string                  `json:"name"`
	WorkerTypeID       string                  `json:"workerTypeId,omitempty"`
	WorkerTypeVersion  string                  `json:"workerTypeVersion,omitempty"`
	Config             *ConfiguredWorkerConfig `json:"config,omitempty"`
	CredentialsRef     string                  `json:"credentialsRef,omitempty"`
	Enabled            bool                    `json:"enabled"`
	CreatedAt          int64                   `json:"createdAt,omitempty"`
	UpdatedAt          int64                   `json:"updatedAt,omitempty"`
	OwnerID            string                  `json:"ownerId,omitempty"`
	Visibility         string                  `json:"visibility,omitempty"`
	Members            []projectMember         `json:"members,omitempty"`
	Role               string                  `json:"role,omitempty"`
	Problem            string                  `json:"problem,omitempty"`
	UsedBy             []connectorUse          `json:"usedBy,omitempty"`
	CompatibilityError string                  `json:"compatibilityError,omitempty"`
}

type configuredWorkerRequest struct {
	Name              string                 `json:"name"`
	WorkerTypeID      string                 `json:"workerTypeId"`
	WorkerTypeVersion string                 `json:"workerTypeVersion"`
	Config            ConfiguredWorkerConfig `json:"config"`
	CredentialsRef    string                 `json:"credentialsRef"`
	Enabled           *bool                  `json:"enabled"`
}

type configuredWorkerPatch struct {
	Config         *ConfiguredWorkerConfig `json:"config"`
	CredentialsRef *string                 `json:"credentialsRef"`
	Enabled        *bool                   `json:"enabled"`
}

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header)}
}

func (w *bufferedResponse) Header() http.Header { return w.header }

func (w *bufferedResponse) WriteHeader(status int) { w.status = status }

func (w *bufferedResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}

func flushBufferedResponse(w http.ResponseWriter, captured *bufferedResponse) {
	for key, values := range captured.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := captured.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(captured.body.Bytes())
}

func builtInWorkerTypeForID(id string) (builtInWorkerTypeMetadata, bool) {
	for _, meta := range builtInManagedWorkerTypes {
		if meta.ID == id {
			return meta, true
		}
	}
	return builtInWorkerTypeMetadata{}, false
}

func configuredWorkerFromConnectorJSON(raw []byte) (ConfiguredWorker, error) {
	var legacy struct {
		connector
		Role    string         `json:"role,omitempty"`
		Problem string         `json:"problem,omitempty"`
		UsedBy  []connectorUse `json:"usedBy,omitempty"`
	}
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return ConfiguredWorker{}, err
	}

	worker := ConfiguredWorker{
		ID: legacy.ID, Name: legacy.Name, Enabled: legacy.Enabled,
		CreatedAt: legacy.CreatedAt, UpdatedAt: legacy.UpdatedAt,
		OwnerID: legacy.OwnerID, Visibility: legacy.Visibility, Members: legacy.Members,
		Role: legacy.Role, Problem: legacy.Problem, UsedBy: legacy.UsedBy,
	}
	if meta, ok := lookupBuiltInManagedWorkerType(legacy.Kind); ok {
		worker.WorkerTypeID = meta.ID
		worker.WorkerTypeVersion = meta.Version
	} else {
		worker.CompatibilityError = "connector kind " + legacy.Kind + " has no canonical Worker Type mapping"
	}

	// A catalog-only view intentionally contains no configuration fields. Preserve
	// that access boundary instead of manufacturing empty configuration values.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ConfiguredWorker{}, err
	}
	if _, visible := fields["endpoint"]; visible {
		worker.Config = &ConfiguredWorkerConfig{
			Endpoint: legacy.Endpoint,
			Provider: legacy.Provider,
			Sender:   legacy.Sender,
		}
		worker.CredentialsRef = legacy.CredentialsRef
	}
	return worker, nil
}

func rewriteRequestBody(r *http.Request, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return nil
}

func writeConfiguredWorkerResponse(w http.ResponseWriter, captured *bufferedResponse) {
	if captured.status != http.StatusOK {
		flushBufferedResponse(w, captured)
		return
	}
	worker, err := configuredWorkerFromConnectorJSON(captured.body.Bytes())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "project configured Worker response: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(worker)
}

func (s *Server) handleListConfiguredWorkers(w http.ResponseWriter, r *http.Request) {
	captured := newBufferedResponse()
	s.handleListConnectors(captured, r)
	if captured.status != http.StatusOK {
		flushBufferedResponse(w, captured)
		return
	}
	var records []json.RawMessage
	if err := json.Unmarshal(captured.body.Bytes(), &records); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "project configured Workers: "+err.Error())
		return
	}
	workers := make([]ConfiguredWorker, 0, len(records))
	for _, record := range records {
		worker, err := configuredWorkerFromConnectorJSON(record)
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "project configured Workers: "+err.Error())
			return
		}
		workers = append(workers, worker)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(workers)
}

func (s *Server) handleCreateConfiguredWorker(w http.ResponseWriter, r *http.Request) {
	var request configuredWorkerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxXMLBytes)).Decode(&request); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	request.WorkerTypeID = strings.TrimSpace(request.WorkerTypeID)
	meta, ok := builtInWorkerTypeForID(request.WorkerTypeID)
	if !ok {
		httpapi.Error(w, http.StatusBadRequest, "unknown workerTypeId")
		return
	}
	if request.WorkerTypeVersion != meta.Version {
		httpapi.Error(w, http.StatusBadRequest, "workerTypeVersion does not match the installed Worker Type")
		return
	}
	legacy := createConnectorParams{
		Name: request.Name, Kind: meta.ConnectorKind,
		Endpoint: request.Config.Endpoint, Provider: request.Config.Provider,
		Sender: request.Config.Sender, CredentialsRef: request.CredentialsRef,
		Enabled: request.Enabled,
	}
	if err := rewriteRequestBody(r, legacy); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "encode connector compatibility request: "+err.Error())
		return
	}
	captured := newBufferedResponse()
	s.handleCreateConnector(captured, r)
	writeConfiguredWorkerResponse(w, captured)
}

func (s *Server) handleUpdateConfiguredWorker(w http.ResponseWriter, r *http.Request) {
	var request configuredWorkerPatch
	if err := json.NewDecoder(io.LimitReader(r.Body, maxXMLBytes)).Decode(&request); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	legacy := struct {
		Endpoint       *string `json:"endpoint,omitempty"`
		Provider       *string `json:"provider,omitempty"`
		Sender         *string `json:"sender,omitempty"`
		CredentialsRef *string `json:"credentialsRef,omitempty"`
		Enabled        *bool   `json:"enabled,omitempty"`
	}{CredentialsRef: request.CredentialsRef, Enabled: request.Enabled}
	if request.Config != nil {
		legacy.Endpoint = &request.Config.Endpoint
		legacy.Provider = &request.Config.Provider
		legacy.Sender = &request.Config.Sender
	}
	if err := rewriteRequestBody(r, legacy); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "encode connector compatibility request: "+err.Error())
		return
	}
	captured := newBufferedResponse()
	s.handleUpdateConnector(captured, r)
	writeConfiguredWorkerResponse(w, captured)
}
