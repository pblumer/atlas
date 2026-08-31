package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// ConfiguredWorkerConfig is the non-secret configuration of a configured Worker as
// the API returns it. Credential material travels as a reference beside it, never as
// a value (I6), and a SQL Worker's connection string is deliberately absent from this
// type rather than merely omitted: a response with no field for it cannot echo it
// back by accident.
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

// configuredWorkerConfigRequest is the configuration a create request carries. It is
// a separate type from the one responses use because the two are not the same set of
// fields: this one takes a connection string, and no response type has anywhere to
// put one.
type configuredWorkerConfigRequest struct {
	Endpoint string `json:"endpoint"`
	Provider string `json:"provider"`
	Sender   string `json:"sender"`
	// ConnectionString is a SQL Worker's whole configuration, sealed into the vault by
	// the create path which then stores only the reference (I6). It is here because
	// for those Worker Types the credential *is* the configuration: without it the
	// canonical API could name an existing vault key and nothing else, which would
	// leave three of the built-in Worker Types configurable only through the
	// deprecated connector API. Never echoed back.
	ConnectionString string `json:"connectionString"`
}

type configuredWorkerRequest struct {
	Name              string                        `json:"name"`
	WorkerTypeID      string                        `json:"workerTypeId"`
	WorkerTypeVersion string                        `json:"workerTypeVersion"`
	Config            configuredWorkerConfigRequest `json:"config"`
	CredentialsRef    string                        `json:"credentialsRef"`
	Enabled           *bool                         `json:"enabled"`
}

// configuredWorkerConfigPatch changes only the configuration keys a request body
// actually names. Pointers, like the connector patch it translates into, because
// absent and empty are two different acts: a body naming only an endpoint has to
// leave the provider alone. Sending all three regardless would move a Gmail Worker
// onto SMTP whenever somebody edited its sender, and answer 200.
type configuredWorkerConfigPatch struct {
	Endpoint *string `json:"endpoint"`
	Provider *string `json:"provider"`
	Sender   *string `json:"sender"`
}

type configuredWorkerPatch struct {
	Config         *configuredWorkerConfigPatch `json:"config"`
	CredentialsRef *string                      `json:"credentialsRef"`
	Enabled        *bool                        `json:"enabled"`
}

// builtInWorkerTypeForID resolves a canonical Worker Type id to the built-in package
// metadata, the reverse of lookupBuiltInManagedWorkerType's kind lookup.
func builtInWorkerTypeForID(id string) (builtInWorkerTypeMetadata, bool) {
	for _, meta := range builtInManagedWorkerTypes {
		if meta.ID == id {
			return meta, true
		}
	}
	return builtInWorkerTypeMetadata{}, false
}

// configuredWorkerFrom projects one listing into the canonical representation. It
// takes the listing rather than a record because whether this caller may see the
// Worker's configuration is a fact the role check established (ADR-0205), not
// something a projection should try to infer.
func configuredWorkerFrom(listing connectorListing) ConfiguredWorker {
	rec := listing.record
	worker := ConfiguredWorker{
		ID:      rec.ID,
		Name:    rec.Name,
		Enabled: rec.Enabled,
		Problem: listing.problem,
	}
	if meta, ok := lookupBuiltInManagedWorkerType(rec.Kind); ok {
		worker.WorkerTypeID, worker.WorkerTypeVersion = meta.ID, meta.Version
	} else {
		// A stored kind this release ships no Worker Type for is reported as exactly
		// that. Inventing an identity would put a workerTypeId into the API that no
		// installed Worker Type answers to, and the record would look migrated when it
		// is not.
		worker.CompatibilityError = "connector kind " + rec.Kind + " has no canonical Worker Type mapping"
	}
	if listing.catalogOnly {
		// Below viewer, existence is visible and configuration is not. The projection
		// stops here instead of emitting empty configuration values, which would say
		// something false about the Worker rather than something true about this
		// caller's access.
		return worker
	}
	worker.Config = &ConfiguredWorkerConfig{
		Endpoint: rec.Endpoint,
		Provider: rec.Provider,
		Sender:   rec.Sender,
	}
	worker.CredentialsRef = rec.CredentialsRef
	worker.CreatedAt, worker.UpdatedAt = rec.CreatedAt, rec.UpdatedAt
	worker.OwnerID, worker.Visibility, worker.Members = rec.OwnerID, rec.Visibility, rec.Members
	worker.Role, worker.UsedBy = listing.role, listing.usedBy
	return worker
}

// configuredWorkerFromRecord projects a record the caller has just created or
// changed, and may therefore see in full.
func configuredWorkerFromRecord(rec connector) ConfiguredWorker {
	return configuredWorkerFrom(connectorListing{record: rec})
}

func (s *Server) handleListConfiguredWorkers(w http.ResponseWriter, r *http.Request) {
	listings, err := s.connectorListings(r)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list configured Workers: "+err.Error())
		return
	}
	workers := make([]ConfiguredWorker, 0, len(listings))
	for _, listing := range listings {
		workers = append(workers, configuredWorkerFrom(listing))
	}
	httpapi.JSON(w, http.StatusOK, workers)
}

func (s *Server) handleCreateConfiguredWorker(w http.ResponseWriter, r *http.Request) {
	var request configuredWorkerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxXMLBytes)).Decode(&request); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	meta, ok := builtInWorkerTypeForID(strings.TrimSpace(request.WorkerTypeID))
	if !ok {
		httpapi.Error(w, http.StatusBadRequest, "unknown workerTypeId")
		return
	}
	if strings.TrimSpace(request.WorkerTypeVersion) != meta.Version {
		httpapi.Error(w, http.StatusBadRequest, "workerTypeVersion does not match the installed Worker Type")
		return
	}
	rec, code, msg := s.createConnector(r, createConnectorParams{
		Name:             request.Name,
		Kind:             meta.ConnectorKind,
		Endpoint:         request.Config.Endpoint,
		Provider:         request.Config.Provider,
		Sender:           request.Config.Sender,
		ConnectionString: request.Config.ConnectionString,
		CredentialsRef:   request.CredentialsRef,
		Enabled:          request.Enabled,
	})
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	httpapi.JSON(w, http.StatusOK, configuredWorkerFromRecord(rec))
}

func (s *Server) handleUpdateConfiguredWorker(w http.ResponseWriter, r *http.Request) {
	var request configuredWorkerPatch
	if err := json.NewDecoder(io.LimitReader(r.Body, maxXMLBytes)).Decode(&request); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	patch := connectorPatch{CredentialsRef: request.CredentialsRef, Enabled: request.Enabled}
	if request.Config != nil {
		patch.Endpoint = request.Config.Endpoint
		patch.Provider = request.Config.Provider
		patch.Sender = request.Config.Sender
	}
	rec, code, msg := s.updateConnector(r, r.PathValue("id"), patch)
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	httpapi.JSON(w, http.StatusOK, configuredWorkerFromRecord(rec))
}
