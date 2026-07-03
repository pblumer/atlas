package state

import (
	"encoding/binary"
	"io"

	"github.com/cockroachdb/pebble"
)

// counterMergerName identifies the merge operator in the Pebble manifest. It
// must stay stable: a store written with it can only be reopened with it.
const counterMergerName = "atlas.counter.sum.v1"

// counterMerger sums signed int64 deltas. It backs the active-children counters
// so increments and decrements are write-only Merge operations — no read on the
// hot path (invariant I1). Pebble folds the deltas on read and during
// compaction. Recovery stays correct because merges compose: the persisted value
// already holds pre-crash deltas, and replay adds the rest.
var counterMerger = &pebble.Merger{
	Name: counterMergerName,
	Merge: func(_, value []byte) (pebble.ValueMerger, error) {
		return &counterValueMerger{sum: decodeCounter(value)}, nil
	},
}

type counterValueMerger struct{ sum int64 }

func (m *counterValueMerger) MergeNewer(value []byte) error {
	m.sum += decodeCounter(value)
	return nil
}

func (m *counterValueMerger) MergeOlder(value []byte) error {
	m.sum += decodeCounter(value)
	return nil
}

func (m *counterValueMerger) Finish(includesBase bool) ([]byte, io.Closer, error) {
	return encodeCounter(m.sum), nil, nil
}

func appendCounter(dst []byte, delta int64) []byte {
	return binary.LittleEndian.AppendUint64(dst, uint64(delta))
}

func encodeCounter(v int64) []byte {
	return appendCounter(nil, v)
}

func decodeCounter(b []byte) int64 {
	if len(b) < 8 {
		return 0
	}
	return int64(binary.LittleEndian.Uint64(b))
}
