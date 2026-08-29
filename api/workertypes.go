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

// WorkerTypeDefinition is the first canonical Worker-oriented view over Atlas's
// built-in capability catalog. ID remains the existing authoring identifier so model
// bindings do not move; WorkerTypeID supplies ADR-0208's globally namespaced identity.
// Placement is retained during the compatibility window because it describes this
// server's current execution placement, while RuntimeMode gives that placement a
// Worker-domain vocabulary instead of exposing connector-only flags.
type WorkerTypeDefinition struct {
	ID           string            `json:"id"`
	WorkerTypeID string            `json:"workerTypeId"`
	RuntimeMode  WorkerRuntimeMode `json:"runtimeMode"`
	Placement    string            `json:"placement"`
}

// workerTypeDefinitions projects the existing authoritative placement catalog into
// Worker Type vocabulary. The placement catalog already derives from the immutable
// built-in job-type tables and the server's configured handler registry, so this adds
// no second catalog or runtime source of truth.
func (s *Server) workerTypeDefinitions() []WorkerTypeDefinition {
	placements := s.connectorPlacements()
	out := make([]WorkerTypeDefinition, 0, len(placements))
	for _, placement := range placements {
		out = append(out, WorkerTypeDefinition{
			ID:           placement.ID,
			WorkerTypeID: "atlas." + placement.ID,
			RuntimeMode:  workerRuntimeModeForPlacement(placement.Placement),
			Placement:    placement.Placement,
		})
	}
	return out
}

func workerRuntimeModeForPlacement(placement string) WorkerRuntimeMode {
	switch placement {
	case placementWorker, placementWorkerOnly:
		return WorkerRuntimeModeAtlasSupervised
	default:
		return WorkerRuntimeModeAtlasEmbedded
	}
}

// handleWorkerTypes lists the canonical Worker Type projection while the deprecated
// /connector-kinds endpoint continues to expose its unchanged compatibility shape.
func (s *Server) handleWorkerTypes(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, map[string]any{"kinds": s.workerTypeDefinitions()})
}
