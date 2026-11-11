package state

import (
	"errors"

	"github.com/cockroachdb/pebble"
)

// The reference read (ADR-draft-runloop-reference-probe).
//
// The run-loop probe times a fixed piece of store work on the single writer, so that a
// change in its duration says the store has become slower to read rather than that the
// server was asked to do something different. For that the work has to be the same on
// every call and small enough to run every few seconds on the writer without being
// noticed. It is therefore a fixed number of point reads of two fixed keys:
//
//   - the applied log position, which every batch rewrites. It is the key the store
//     reads most often, so its read is the cost of the common path.
//   - a key in the same meta range that is never written. A read of an absent key cannot
//     stop at the first table holding it, so it consults every memtable and every table
//     whose bounds cover the key, filters included. Its cost therefore grows with the
//     store's read amplification, which is how a backlog of compactions slows the reads
//     the engine makes.
//
// Both keys sit in the meta range, column family 0x00, the lowest in the key space. A
// batch writes the applied position together with keys of higher families, so a table a
// flush produces from it spans everything between them, the absent key included. That
// is what lets the absent key see the whole stack of tables.

// referenceReadRounds is how often the two keys are read per call. Sixteen point reads
// take about 5µs on a small, healthy store: long enough to be measured above the
// clock's resolution, short enough to hold the writer unnoticed.
const referenceReadRounds = 8

// metaReferenceAbsent is the key the reference read expects to be absent. Nothing
// writes it; if something ever did, the read would still be a fixed read, only a
// cheaper one, which is why its presence is not an error.
const metaReferenceAbsent = "reference_read_absent"

// referenceKeys are built once: the reference read is called on a cadence for as long
// as the server runs, and building its keys per call would be allocation for nothing.
var referenceKeys = [...][]byte{keyMeta(metaLastApplied), keyMeta(metaReferenceAbsent)}

// ReferenceRead performs the fixed reference read and reports the first error the
// store returned. It reads and changes nothing a caller can observe.
//
// Like every other Store method it is called on the owning goroutine (invariant I3):
// the probe exists to measure the store as the writer experiences it.
func (s *Store) ReferenceRead() error {
	for range referenceReadRounds {
		for _, k := range referenceKeys {
			_, closer, err := s.db.Get(k)
			if errors.Is(err, pebble.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if err := closer.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}
