package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
)

// WorkerRuntimeMode identifies the execution boundary of a Worker Type as defined by
// ADR-0208. It replaces compatibility details such as managedConnectorKind.workerOnly
// on the Worker-oriented API without changing the existing runtime paths.
type WorkerRuntimeMode string

const (
	// WorkerRuntimeModeAtlasEmbedded means the trusted implementation executes in the
	// Atlas process through the existing post-fsync job-worker path.
	WorkerRuntimeModeAtlasEmbedded WorkerRuntimeMode = "atlas-embedded"
	// WorkerRuntimeModeAtlasSupervised means Atlas executes the trusted implementation
	// in an atlas worker child process that it supervises.
	WorkerRuntimeModeAtlasSupervised WorkerRuntimeMode = "atlas-supervised"
	// WorkerRuntimeModeExternal means Worker Instances are operated independently and
	// consume Atlas's public worker/job API.
	WorkerRuntimeModeExternal WorkerRuntimeMode = "external"
)

// WorkerTypeDefinition is the canonical Worker-oriented view over Atlas's capability
// catalog. ID remains the existing authoring identifier so model bindings do not move;
// WorkerTypeID is ADR-0208's globally namespaced identity. Built-in managed Worker
// Types additionally expose their package metadata. Placement remains install-specific
// and is deliberately independent from RuntimeMode.
type WorkerTypeDefinition struct {
	ID           string            `json:"id"`
	WorkerTypeID string            `json:"workerTypeId"`
	Version      string            `json:"version,omitempty"`
	Title        string            `json:"title,omitempty"`
	Vendor       string            `json:"vendor,omitempty"`
	Origin       WorkerTypeOrigin  `json:"origin,omitempty"`
	RuntimeMode  WorkerRuntimeMode `json:"runtimeMode"`
	Placement    string            `json:"placement"`
}

// workerTypeDefinitions projects the existing placement catalog into Worker Type
// vocabulary. Managed built-ins read their identity and package metadata from the
// ADR-0208 built-in metadata registry. Non-managed entries keep the step-1 fallback
// until their own Worker-Type/non-Worker semantics are migrated in a later slice.
func (s *Server) workerTypeDefinitions() []WorkerTypeDefinition {
	placements := s.connectorPlacements()
	out := make([]WorkerTypeDefinition, 0, len(placements))
	for _, placement := range placements {
		definition := WorkerTypeDefinition{
			ID:           placement.ID,
			WorkerTypeID: "atlas." + placement.ID,
			RuntimeMode:  WorkerRuntimeModeAtlasEmbedded,
			Placement:    placement.Placement,
		}
		if meta, ok := lookupBuiltInManagedWorkerType(placement.ID); ok {
			definition.WorkerTypeID = meta.ID
			definition.Version = meta.Version
			definition.Title = meta.Title
			definition.Vendor = meta.Vendor
			definition.Origin = meta.Origin
			definition.RuntimeMode = meta.RuntimeMode
		}
		out = append(out, definition)
	}
	return out
}

// handleWorkerTypes lists the canonical Worker Type projection while the deprecated
// /connector-kinds endpoint continues to expose its unchanged compatibility shape.
func (s *Server) handleWorkerTypes(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, map[string]any{"kinds": s.workerTypeDefinitions()})
}
