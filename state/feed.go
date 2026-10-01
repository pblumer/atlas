package state

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// The event feed (ADR-0429 §5): what leaves Atlas, as state.
//
// applyToState folds every action outcome, every grant and every revocation into one
// row keyed by the partition and the log position of the record that carried it —
// both fixed by the log, so replay writes the same rows under the same keys (I4). A
// reader pages through the rows in position order from a cursor. The rows are
// dropped by an explicit prune fact through a position, never by a delete nobody
// replays, and the position the feed was pruned through is kept beside the rows, so a
// cursor older than it is answered as expired rather than silently skipping what it
// missed.

// FeedKind is what a feed row records.
type FeedKind byte

const (
	// FeedOutcome is how an action asked of a position ended.
	FeedOutcome FeedKind = 1
	// FeedGranted is a right granted.
	FeedGranted FeedKind = 2
	// FeedRevoked is a right ended, with the hold it ended.
	FeedRevoked FeedKind = 3
)

const (
	feedRows  byte = 0x00
	feedPrune byte = 0x01
)

func feedRowPrefix(partition uint16) []byte {
	return appendBE32([]byte{byte(cfFeed), feedRows}, uint32(partition))
}

func keyFeedRow(partition uint16, position uint64) []byte {
	return appendBE64(feedRowPrefix(partition), position)
}

func keyFeedPrunedThrough(partition uint16) []byte {
	return appendBE32([]byte{byte(cfFeed), feedPrune}, uint32(partition))
}

// FeedEntry is one row of the feed: where the fact sits on the log, when it was
// recorded, and the fact itself — exactly one of the three values, by Kind.
type FeedEntry struct {
	Partition uint16
	Position  uint64
	At        int64
	Kind      FeedKind
	Outcome   *model.ActionOutcomeValue
	Granted   *model.EntitlementValue
	Revoked   *model.EntitlementHistoryValue
}

// feedValueType is the value type a kind's payload is encoded as.
func feedValueType(k FeedKind) (model.ValueType, bool) {
	switch k {
	case FeedOutcome:
		return model.VTActionOutcome, true
	case FeedGranted:
		return model.VTEntitlement, true
	case FeedRevoked:
		return model.VTEntitlementHistory, true
	}
	return 0, false
}

// PutFeedEntry writes the row for the fact at position on partition, recorded at at.
func (t *Tx) PutFeedEntry(partition uint16, position uint64, at int64, kind FeedKind, v model.Value) error {
	if vt, ok := feedValueType(kind); !ok || v.ValueType() != vt {
		return fmt.Errorf("state: a feed row of kind %d cannot carry a %s", kind, v.ValueType())
	}
	row := make([]byte, 0, 64)
	row = append(row, byte(kind))
	row = binary.BigEndian.AppendUint64(row, uint64(at))
	row = model.AppendValue(row, v)
	return t.b.Set(keyFeedRow(partition, position), row, nil)
}

// PruneFeed drops every row of partition at or before through, and remembers through
// so a reader whose cursor is older is told what it missed. A cut below the one
// already made is a no-op: the rows below it are gone already.
func (t *Tx) PruneFeed(partition uint16, through uint64) error {
	raw, closer, err := t.b.Get(keyFeedPrunedThrough(partition))
	switch {
	case err == nil:
		done := uint64(0)
		if len(raw) >= 8 {
			done = binary.BigEndian.Uint64(raw)
		}
		closer.Close()
		if through <= done {
			return nil
		}
	case !errors.Is(err, pebble.ErrNotFound):
		return err
	}
	if err := t.b.DeleteRange(feedRowPrefix(partition), keyFeedRow(partition, through+1), nil); err != nil {
		return err
	}
	return t.b.Set(keyFeedPrunedThrough(partition), binary.BigEndian.AppendUint64(nil, through), nil)
}

func decodeFeedRow(partition uint16, key, raw []byte) (FeedEntry, error) {
	if len(key) < 8 || len(raw) < 9 {
		return FeedEntry{}, fmt.Errorf("state: a feed row of %d bytes is cut short", len(raw))
	}
	e := FeedEntry{
		Partition: partition,
		Position:  binary.BigEndian.Uint64(key[len(key)-8:]),
		Kind:      FeedKind(raw[0]),
		At:        int64(binary.BigEndian.Uint64(raw[1:9])),
	}
	vt, ok := feedValueType(e.Kind)
	if !ok {
		return FeedEntry{}, fmt.Errorf("state: a feed row of unknown kind %d", e.Kind)
	}
	v, err := model.DecodeValue(vt, raw[9:])
	if err != nil {
		return FeedEntry{}, err
	}
	switch val := v.(type) {
	case *model.ActionOutcomeValue:
		e.Outcome = val
	case *model.EntitlementValue:
		e.Granted = val
	case *model.EntitlementHistoryValue:
		e.Revoked = val
	}
	return e, nil
}

// FeedAfter calls fn with the rows of partition after position after, in position
// order, until the rows run out or fn returns an error — a caller stops a page early
// with a sentinel of its own.
func (q queries) FeedAfter(partition uint16, after uint64, fn func(FeedEntry) error) error {
	if after == ^uint64(0) {
		return nil
	}
	lo := keyFeedRow(partition, after+1)
	return q.scanRange(lo, prefixEnd(feedRowPrefix(partition)), func(k, v []byte) error {
		e, err := decodeFeedRow(partition, k, v)
		if err != nil {
			return err
		}
		return fn(e)
	})
}

// FeedPrunedThrough is the position partition's feed was pruned through, or 0 when
// it never was.
func (q queries) FeedPrunedThrough(partition uint16) (uint64, error) {
	raw, ok, err := getCopy(q.r, keyFeedPrunedThrough(partition))
	if err != nil || !ok || len(raw) < 8 {
		return 0, err
	}
	return binary.BigEndian.Uint64(raw), nil
}

// FeedThroughBefore is the position of the last row of partition recorded before
// cutoff, in position order, and whether there is one: what the retention sweep
// prunes through. It stops at the first row recorded at or after cutoff, so a row
// recorded out of time order is kept until the rows before it age out too — the
// window is a lower bound, never a cut through the middle of the log.
func (q queries) FeedThroughBefore(partition uint16, cutoff int64) (uint64, bool, error) {
	var (
		through uint64
		found   bool
	)
	err := q.scanPrefix(feedRowPrefix(partition), func(k, v []byte) error {
		if len(v) < 9 || int64(binary.BigEndian.Uint64(v[1:9])) >= cutoff {
			return errFeedScanDone
		}
		through, found = binary.BigEndian.Uint64(k[len(k)-8:]), true
		return nil
	})
	if err != nil && !errors.Is(err, errFeedScanDone) {
		return 0, false, err
	}
	return through, found, nil
}

var errFeedScanDone = errors.New("state: feed scan done")
