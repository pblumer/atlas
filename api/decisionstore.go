package api

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	"github.com/pblumer/atlas/api/sidecar"
)

// persistedDecision is a **decision deployment**: a DMN model published as a
// runtime artifact in its own right, rather than as a model bundled with some
// process's deployment (ADR-draft-durable-versioned-decision-deployments).
//
// It is the decision counterpart of [persistedDeployment] and follows the same
// discipline (ADR-0019): the record holds the validated *source* plus the stable
// metadata needed to bring it back, and the temis registry is rebuilt from it at
// startup by compiling the XML again. No compiled temis structure is ever
// persisted — those are a dependency's internals with no compatibility contract.
//
// The key comes from the one global definition key space (`Server.nextKey`), the
// same space process-definition keys come from, which is what lets the registry
// hold both kinds under one map and lets a process deployment pin a reference to
// an exact decision deployment by key alone.
type persistedDecision struct {
	Key uint64 `json:"key"`
	// ApplicationID is the application this was published from — on disk the
	// ADR-0034 projectId, as everywhere else in this tree. Empty for a decision
	// deployed outside an application.
	ApplicationID string `json:"applicationId,omitempty"`
	// ArtifactID is the DMN reference artifact (dmnRef.ID) it was published from, so
	// a deployed decision can be traced back to the thing an author edits.
	ArtifactID string `json:"artifactId,omitempty"`
	// ModelRef is the resolver handle the model was read from, and ResourceName the
	// file-shaped name a release manifest shows ("eligibility.dmn").
	ModelRef     string `json:"modelRef,omitempty"`
	ResourceName string `json:"resourceName,omitempty"`
	// ModelName is the DMN <definitions name>.
	ModelName string `json:"modelName,omitempty"`
	// Decisions is one entry per decision the model provides, each with the version
	// this deployment gave it. A model that provides several decisions advances
	// several lineages at once.
	Decisions []deployedDecisionEntry `json:"decisions"`
	// Checksum is the sha256 of XML, hex-encoded: the integrity fact that says two
	// deployments carry the same model without comparing megabytes of source.
	Checksum   string `json:"checksum"`
	DeployedAt int64  `json:"deployedAt"`
	DeployedBy string `json:"deployedBy,omitempty"`
	XML        string `json:"xml"`
}

// deployedDecisionEntry is one decision inside a decision deployment: the id a
// business rule task names it by, and the version this deployment gave it.
//
// Versions are counted per decision id rather than per model file, because that is
// the lineage an author reasons about ("eligibility v2") and the one a latest-bound
// reference follows. The id is the string temis's model index lists — which is the
// decision's *name*, since that is what temis resolves a decision by and what a
// `calledDecision decisionId` therefore has to carry (see dmn.DecisionInfo). There
// is no second display name to store.
type deployedDecisionEntry struct {
	ID      string `json:"id"`
	Version int32  `json:"version"`
}

// decisionStore is a durable store for decision deployments, one JSON file per
// deployment key under a single directory. Like [deployStore] its records are
// keyed by the engine's own uint64 key rather than a string, so it wraps the
// shared store to keep that key type at its edge and names the file by the key in
// decimal, which is already filename-safe and recognizable.
type decisionStore struct {
	*sidecar.Store[persistedDecision]
}

// newDecisionStore opens (creating if needed) the decisions directory. Records
// list in key order, which is deployment order — what recovery needs to rebuild
// the registry the way it was built live.
func newDecisionStore(dir string) (*decisionStore, error) {
	s, err := sidecar.NewStore(dir, "decisionstore",
		func(rec persistedDecision) string { return decisionKeyName(rec.Key) },
		sidecar.Names[persistedDecision](
			func(key string) string { return key },
			func(stem string) bool { _, err := strconv.ParseUint(stem, 10, 64); return err == nil },
		),
		sidecar.Order(func(a, b persistedDecision) bool { return a.Key < b.Key }),
	)
	if err != nil {
		return nil, err
	}
	return &decisionStore{s}, nil
}

// decisionKeyName renders a decision-deployment key as the store's key string,
// which is also its filename stem.
func decisionKeyName(key uint64) string { return strconv.FormatUint(key, 10) }

// fileFor maps a decision-deployment key to its record path.
func (d *decisionStore) fileFor(key uint64) string { return d.FileFor(decisionKeyName(key)) }

// load returns the decision deployment for a key, or ok=false if none is stored.
func (d *decisionStore) load(key uint64) (persistedDecision, bool, error) {
	return d.Get(decisionKeyName(key))
}

// modelChecksum is the hex sha256 of a DMN model's bytes, the checksum a decision
// deployment records.
func modelChecksum(xml []byte) string {
	sum := sha256.Sum256(xml)
	return hex.EncodeToString(sum[:])
}
