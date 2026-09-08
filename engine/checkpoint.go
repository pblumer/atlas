package engine

import (
	"fmt"
	"sort"

	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/model"
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

// recordPosition decodes just a record's log position, so the wal package can locate
// the segment a suffix replay must start from without knowing the record format.
func recordPosition(data []byte) (uint64, error) {
	rec, err := model.ReadRecord(data)
	if err != nil {
		return 0, err
	}
	return rec.Header.Position, nil
}

// proveThePrefix refuses a recovery whose log no longer reaches back to where the
// state it is recovering into leaves off.
//
// covered is the highest position already accounted for: what the store has
// applied, or what a usable checkpoint stands in for, whichever reaches further.
// The log must then carry everything from covered+1 onward. If its oldest
// surviving record sits above that, the records in between are gone from the log
// and live only in a checkpoint — which this entry point cannot install, because
// installing state files means replacing a store that is already open.
//
// So it fails, and says what would fix it. Refusing to start is visible; starting
// without the prefix is not, and the missing instances look exactly like
// instances that never existed (ADR-draft-prove-the-prefix).
func (p *Processor) proveThePrefix(lastApplied, after uint64, checkpointRoot string) error {
	covered := lastApplied
	if after > covered {
		covered = after
	}
	earliest, ok, err := p.log.EarliestPosition(recordPosition)
	if err != nil {
		return err
	}
	if !ok || earliest <= covered+1 {
		return nil
	}
	where := "no checkpoint root was given"
	if checkpointRoot != "" {
		where = fmt.Sprintf("no checkpoint under %s covers it", checkpointRoot)
	}
	return fmt.Errorf("engine: the log starts at position %d but this state only reaches %d, so "+
		"positions %d-%d are in neither. They were compacted out of the log and live only in a "+
		"checkpoint, and %s. Restore the state directory from a checkpoint (or from a whole-instance "+
		"snapshot) before starting, or start against a log that still has its prefix — replaying what "+
		"is left would come up missing every instance below the cut",
		earliest, covered, covered+1, earliest-1, where)
}

// checkpointSeed picks the newest checkpoint under root that recovery may skip past,
// returning the position to replay after plus the highest position and key counter it
// recorded for the prefix it replaces. Zeros mean "replay from genesis" (ADR-0131).
//
// A checkpoint is usable only when it is for this partition and its applied position
// is at or below the store's: the store must already hold the state the skipped prefix
// produced, because this slice skips *reading* a prefix, it does not restore state
// files. A checkpoint ahead of the store would leave the gap between them unapplied,
// so it is refused and an older one — ultimately genesis — is used instead. Anything
// unreadable is likewise skipped rather than fatal.
//
// Only the manifest is read, not the state snapshot: its own checksum makes the fields
// consumed here trustworthy, and the snapshot's files are not touched by a suffix
// replay. Verifying their checksum belongs to the slice that actually restores them.
func (p *Processor) checkpointSeed(root string, lastApplied uint64) (after, highest, counter uint64) {
	if root == "" {
		return 0, 0, 0
	}
	positions, err := checkpoint.List(root)
	if err != nil {
		return 0, 0, 0
	}
	for i := len(positions) - 1; i >= 0; i-- { // newest first
		m, err := checkpoint.Load(root, positions[i])
		if err != nil || m.Partition != p.partition || m.AppliedPosition > lastApplied {
			continue
		}
		return m.AppliedPosition, m.HighestPosition, m.KeyCounter
	}
	return 0, 0, 0
}

// CompactLog deletes the WAL segments no longer needed for recovery and returns how
// many were removed (ADR-0131). It must be called on the single-writer goroutine.
//
// A segment is removed only when it lies entirely at or below the **compaction cut**,
// which is the minimum of:
//
//   - the newest checkpoint for this partition that **fully verifies** — manifest *and*
//     state files — and whose applied position is at or below the store's. Verification
//     is stricter here than in RecoverFrom on purpose: once the prefix is deleted, that
//     checkpoint's state files become the only way to rebuild it, so a corrupt snapshot
//     must never license a deletion. Requiring it to be at or below the store's applied
//     position matters just as much: recovery refuses a checkpoint ahead of the store
//     (it skips reading a prefix, it does not install state files), so deleting below
//     such a checkpoint would strand recovery with a gap it can no longer replay.
//   - every consumerLimit the caller passes — the exported-log high-water mark
//     (ADR-0114) and the retention safe position (ADR-0115) when those are enabled. The
//     caller owns this list because only it knows which consumers exist; passing nil
//     means "no consumers", so pass every enabled consumer's watermark or risk deleting
//     records it has not read yet.
//
// If no checkpoint qualifies the cut is zero and **nothing is deleted** — the log stays
// the sole recovery source rather than being trimmed on a promise that cannot be
// checked. Compaction is an optimization like the checkpoint itself: skipping it costs
// disk, never correctness.
func (p *Processor) CompactLog(checkpointRoot string, consumerLimits []uint64) (int, error) {
	cut, err := p.compactionCut(checkpointRoot, consumerLimits)
	if err != nil || cut == 0 {
		return 0, err
	}
	return p.log.Compact(cut, recordPosition)
}

// compactionCut resolves the highest position that is provably redundant, or zero when
// nothing may be deleted.
func (p *Processor) compactionCut(root string, consumerLimits []uint64) (uint64, error) {
	if root == "" {
		return 0, nil
	}
	lastApplied, err := p.store.LastAppliedPosition()
	if err != nil {
		return 0, err
	}
	positions, err := checkpoint.List(root)
	if err != nil {
		return 0, err
	}
	cut := uint64(0)
	for i := len(positions) - 1; i >= 0; i-- { // newest first
		m, verr := checkpoint.Verify(root, positions[i])
		if verr != nil || m.Partition != p.partition || m.AppliedPosition > lastApplied {
			continue // corrupt, foreign, or ahead of the store: it licenses no deletion
		}
		cut = m.AppliedPosition
		break
	}
	if cut == 0 {
		return 0, nil
	}
	for _, limit := range consumerLimits {
		if limit < cut {
			cut = limit
		}
	}
	return cut, nil
}
