package engine

import (
	"sort"

	"github.com/pblumer/atlas/checkpoint"
)

// BuildVersion is the Atlas build recorded in checkpoint manifests for diagnostics
// (ADR-0131). It is metadata only — nothing branches on it — and the server may stamp
// it at startup; an unset value simply records no version.
var BuildVersion string

// Checkpoint writes a recovery checkpoint of the currently applied state under root
// and returns the applied log position it captures (ADR-0131).
//
// It **must be called on the partition's single-writer goroutine, between batches**
// (invariant I3). That is what makes the checkpoint's consistency boundary exact: no
// state mutation can race the snapshot, so the position it records is precisely the
// position the snapshotted state contains. Calling it concurrently with the run loop
// would produce a snapshot at a fuzzy position — the failure ADR-0131 rejects.
//
// It is purely additive to durability (invariant I2): a checkpoint is an optimization
// that lets a later recovery replay only the WAL suffix past its applied position.
// Nothing here writes to the log, mutates state, or acknowledges anything, so a failed
// or absent checkpoint costs only a slower recovery, never correctness — the caller
// may log the error and retry on the next cadence.
//
// Restoring from a checkpoint, and deleting the WAL segments it makes redundant, are
// the later ADR-0131 slices; this only produces one.
func (p *Processor) Checkpoint(root string) (uint64, error) {
	applied, err := p.store.LastAppliedPosition()
	if err != nil {
		return 0, err
	}
	m := &checkpoint.Manifest{
		Partition:       p.partition,
		AppliedPosition: applied,
		// p.position is the highest position assigned so far; it is >= applied by
		// construction, and Publish validates that rather than silently correcting it.
		HighestPosition: p.position,
		KeyCounter:      p.keygen.counter,
		CreatedUnixNano: p.clock.Now(),
		AtlasVersion:    BuildVersion,
		Deployments:     p.deploymentRefs(),
	}
	if _, err := checkpoint.Publish(root, m, p.store.Snapshot); err != nil {
		return 0, err
	}
	return applied, nil
}

// deploymentRefs lists the definitions registered with this processor, ordered by key
// so a manifest is byte-identical for identical state. Recovery reloads deployments
// independently (ADR-0019) before replaying; recording them lets a restore detect a
// checkpoint taken against a deployment set that no longer resolves.
func (p *Processor) deploymentRefs() []checkpoint.DeploymentRef {
	if len(p.processes) == 0 {
		return nil
	}
	refs := make([]checkpoint.DeploymentRef, 0, len(p.processes))
	for key, cp := range p.processes {
		refs = append(refs, checkpoint.DeploymentRef{Key: key, Version: cp.Version})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Key < refs[j].Key })
	return refs
}
