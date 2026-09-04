package api

import (
	"strconv"

	"github.com/pblumer/atlas/api/sidecar"
)

// persistedDeployment is the on-disk form of a deployed definition: the metadata
// the UI needs plus the original BPMN XML, enough to recompile the definition and
// render its diagram after a restart. See ADR-0019.
type persistedDeployment struct {
	Key        uint64 `json:"key"`
	ProcessID  string `json:"processId"`
	Name       string `json:"name"`
	Version    int32  `json:"version"`
	DeployedAt int64  `json:"deployedAt"`
	// ProjectID files the deployment under the project its draft belonged to, so the
	// Modeler home can group deployed definitions by project the same way it groups
	// design-time artifacts (ADR-0034). Empty for a deployment made outside a project
	// (a raw-XML or pre-grouping deploy), which the UI shows under "Ungrouped".
	ProjectID string `json:"projectId,omitempty"`
	// DeployedBy is the account that deployed it (ADR-0205). It is not a sharing
	// scope — runtime visibility stays out of ADR-0071 — it is the one fact that
	// makes an ungrouped definition answerable to "is this yours", which the claim on
	// a message name has to ask. Empty on a deployment made before this, and with
	// authentication off; both then read as ownerless, which is open, exactly as an
	// ownerless artifact does.
	DeployedBy string `json:"deployedBy,omitempty"`
	XML        string `json:"xml"`
	// DMNXMLs are the resolved DMN models this process's business rule tasks evaluate
	// against, snapshotted at deploy time so the deployment is self-contained and
	// re-registers on restart without re-resolving the temis references
	// (ADR-0034/ADR-0014). A process may reference decisions across several models, so
	// this is a list; empty when the process has no local business rule tasks.
	DMNXMLs []string `json:"dmnXmls,omitempty"`
	// DMNXML is the pre-multi-model single-model field. Deployments written before
	// multi-model support carry it; loadDeployments reads it as a one-element list
	// when DMNXMLs is absent. New deployments write DMNXMLs and leave this empty.
	DMNXML string `json:"dmnXml,omitempty"`
	// DiagramUpdatedAt / DiagramUpdatedBy stamp the last layout-only adjustment made
	// to this deployment's diagram (ADR-draft-adjust-a-deployed-diagram): unix
	// seconds, and the account that made it. Both absent on a deployment whose
	// picture is still the one it was deployed with, which is why the omitempty
	// matters — the common record is unchanged by this field existing.
	//
	// They are a stamp rather than a history: what the operator needs is "this is not
	// the picture that was deployed, and here is who to ask". The audit line
	// (deployment.diagram_updated) carries the rest.
	DiagramUpdatedAt int64  `json:"diagramUpdatedAt,omitempty"`
	DiagramUpdatedBy string `json:"diagramUpdatedBy,omitempty"`
	// Inactive marks a definition an operator has deactivated: it stays deployed but
	// does not auto-start new instances from its timer/message/signal start events
	// (ADR-0119). Absent (false) in every record written before this field existed, so
	// deployments load active by default — the omitempty keeps the record unchanged for
	// the common active case.
	Inactive bool `json:"inactive,omitempty"`
}

// dmnModels returns the deployment's DMN models as a list, transparently reading a
// legacy single-model record (DMNXML) as a one-element list so old deployments load
// unchanged.
func (d persistedDeployment) dmnModels() []string {
	if len(d.DMNXMLs) > 0 {
		return d.DMNXMLs
	}
	if d.DMNXML != "" {
		return []string{d.DMNXML}
	}
	return nil
}

// deployStore is a durable store for deployments, one JSON file per deployment
// key under a single directory (ADR-0019). Unlike the other design-time stores
// its records are keyed by the engine's own uint64 key rather than a string, so
// it wraps the shared store to keep that key type at its edge; the file is named
// by the key in decimal, which is already filename-safe and recognizable.
type deployStore struct {
	*sidecar.Store[persistedDeployment]
}

// newDeployStore opens (creating if needed) the deployments directory.
// Deployments list in key order, which is deployment order.
func newDeployStore(dir string) (*deployStore, error) {
	s, err := sidecar.NewStore(dir, "deploystore",
		func(rec persistedDeployment) string { return deployKeyName(rec.Key) },
		sidecar.Names[persistedDeployment](
			func(key string) string { return key },
			func(stem string) bool { _, err := strconv.ParseUint(stem, 10, 64); return err == nil },
		),
		sidecar.Order(func(a, b persistedDeployment) bool { return a.Key < b.Key }),
	)
	if err != nil {
		return nil, err
	}
	return &deployStore{s}, nil
}

// deployKeyName renders a deployment key as the store's key string, which is also
// its filename stem.
func deployKeyName(key uint64) string { return strconv.FormatUint(key, 10) }

// fileFor maps a deployment key to its record path.
func (d *deployStore) fileFor(key uint64) string { return d.FileFor(deployKeyName(key)) }

// load returns the deployment for a key, or ok=false if none is stored.
func (d *deployStore) load(key uint64) (persistedDeployment, bool, error) {
	return d.Get(deployKeyName(key))
}

// delete removes a deployment. A missing deployment is not an error.
func (d *deployStore) delete(key uint64) error { return d.Delete(deployKeyName(key)) }
