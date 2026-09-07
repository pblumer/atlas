package wal

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
)

// Replay calls fn for every durable record across all segments, in append
// order. It reads only what is on disk, so records staged but not yet Synced
// are not visible. Replay is the recovery entry point: a processor folds these
// records through applyToState to rebuild state (ADR-0001).
//
// The data slice passed to fn is owned by the caller for the duration of the
// call; it is freshly allocated per record, so it remains valid after fn
// returns. If fn returns an error, Replay stops and returns it.
func (l *Log) Replay(fn func(data []byte) error) error {
	segs, err := l.segmentFiles()
	if err != nil {
		return err
	}
	for _, name := range segs {
		if err := replaySegment(filepath.Join(l.dir, name), fn); err != nil {
			return err
		}
	}
	return nil
}

// errStopScan ends a readFrames scan early; it never escapes this package.
var errStopScan = errors.New("wal: stop scan")

// ReplayFrom is Replay restricted to the suffix of the log holding records past
// after. It is what lets recovery skip a prefix a checkpoint already covers
// (ADR-0131) instead of reading the whole log from genesis.
//
// Log positions increase monotonically in append order, so a segment is entirely at
// or below after whenever the *next* segment starts at or below it. ReplayFrom uses
// that to skip whole segment files, reading just the first record of each to learn
// where it starts; positionOf extracts a record's log position (the wal package does
// not decode records itself). The surviving segments are replayed in full, so fn
// still sees the few records at or below after that share the boundary segment —
// filtering those is the caller's job, exactly as with Replay.
//
// after == 0 means "everything", which is plain Replay. A segment whose first record
// cannot be read stops the skipping conservatively: replay starts no later than it.
// The final segment has no successor to bound its extent, so it is always replayed.
func (l *Log) ReplayFrom(after uint64, positionOf func(data []byte) (uint64, error), fn func(data []byte) error) error {
	if after == 0 {
		return l.Replay(fn)
	}
	segs, err := l.segmentFiles()
	if err != nil {
		return err
	}
	start, err := l.firstSegmentHolding(segs, after, positionOf)
	if err != nil {
		return err
	}
	for _, name := range segs[start:] {
		if err := replaySegment(filepath.Join(l.dir, name), fn); err != nil {
			return err
		}
	}
	return nil
}

// firstSegmentHolding returns the index of the earliest segment that may hold a record
// past after — every segment before it is provably entirely at or below after.
func (l *Log) firstSegmentHolding(segs []string, after uint64, positionOf func([]byte) (uint64, error)) (int, error) {
	start := 0
	for i := 1; i < len(segs); i++ {
		first, ok, err := l.firstPosition(segs[i], positionOf)
		if err != nil {
			return 0, err
		}
		// Segment i-1 spans [first(i-1), first(i)-1], so it is entirely at or below
		// after exactly when first(i)-1 <= after. Anything else — an empty or
		// unreadable head, or a segment whose predecessor may still hold wanted
		// records — stops the skipping conservatively.
		if !ok || first == 0 || first-1 > after {
			break
		}
		start = i
	}
	return start, nil
}

// firstPosition reads just the first record of a segment and reports its log position,
// or ok=false when the segment holds no readable record.
func (l *Log) firstPosition(name string, positionOf func([]byte) (uint64, error)) (uint64, bool, error) {
	f, err := os.Open(filepath.Join(l.dir, name))
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	var pos uint64
	var ok bool
	_, err = readBatches(f, func(data []byte) error {
		p, perr := positionOf(data)
		if perr != nil {
			return perr
		}
		pos, ok = p, true
		return errStopScan
	})
	if err != nil && !errors.Is(err, errStopScan) {
		return 0, false, err
	}
	return pos, ok, nil
}

func replaySegment(path string, fn func([]byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = readBatches(f, fn)
	return err
}

// readBatches reads framed batches from r, invoking fn (if non-nil) for every
// record inside each whole batch, in order. It returns the byte offset of the
// end of the last valid batch — the log's durable extent.
//
// A batch that is incomplete (truncated header or payload) or fails its CRC is
// treated as a torn tail from a crash mid-write: reading stops cleanly and the
// returned offset excludes it, so the batch is discarded entire rather than in
// part. That is the whole point of framing a batch instead of a record: a single
// write is not an atomic one, and every prefix of a per-record framing looked
// like a shorter valid log (ADR-draft-wal-batch-envelope).
//
// A segment written before batch framing has no header and one record per frame.
// It is read in that shape, which keeps an existing log readable across the
// upgrade; those records were never batch-atomic on disk and cannot be made so
// after the fact.
func readBatches(r io.Reader, fn func([]byte) error) (int64, error) {
	br := bufio.NewReader(r)
	consumed := int64(0)
	batched, err := consumeSegmentHeader(br)
	if err != nil {
		return 0, err
	}
	if batched {
		consumed = segmentHeaderSize
	}
	var hdr [batchHeaderSize]byte
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return consumed, nil // clean end or torn header
			}
			return consumed, err
		}
		n := binary.LittleEndian.Uint32(hdr[0:])
		sum := binary.LittleEndian.Uint32(hdr[4:])
		if n == 0 || int64(n) > maxBatchBytes {
			return consumed, nil // corrupt or zeroed tail
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(br, payload); err != nil {
			return consumed, nil // torn payload at tail
		}
		if crc32.Checksum(payload, castagnoli) != sum {
			return consumed, nil // corrupt batch
		}
		if !batched {
			// Version 1: the frame's payload is the record.
			if fn != nil {
				if err := fn(payload); err != nil {
					return consumed, err
				}
			}
		} else if err := forEachEntry(payload, fn); err != nil {
			return consumed, err
		}
		consumed += batchHeaderSize + int64(n)
	}
}

// consumeSegmentHeader reads and validates the version-2 segment header if one is
// there, reporting whether the segment is batch-framed. A segment without the
// magic is a version-1 file: nothing is consumed and the caller reads frames.
func consumeSegmentHeader(br *bufio.Reader) (bool, error) {
	hdr, err := br.Peek(segmentHeaderSize)
	if err != nil || !bytes.Equal(hdr[:len(segmentMagic)], segmentMagic[:]) {
		return false, nil // short file or no magic: version 1
	}
	if v := binary.LittleEndian.Uint32(hdr[len(segmentMagic):]); v != segmentVersion {
		return false, fmt.Errorf("wal: segment is format version %d, which this build cannot read", v)
	}
	if _, err := br.Discard(segmentHeaderSize); err != nil {
		return false, err
	}
	return true, nil
}

// forEachEntry walks the entries of a whole batch payload, handing every record
// to fn. Only records are delivered: a batch may carry other kinds, and Replay's
// contract is the events a caller folds.
//
// The payload has already passed its CRC, so a malformed entry here is not a torn
// tail — it is a batch that is intact on disk and does not parse. That is
// corruption, and it is returned as an error rather than swallowed as an end of
// log.
func forEachEntry(payload []byte, fn func([]byte) error) error {
	for off := 0; off < len(payload); {
		if off+entryHeaderSize > len(payload) {
			return fmt.Errorf("wal: truncated entry header at byte %d of a checksummed batch", off)
		}
		n := int(binary.LittleEndian.Uint32(payload[off:]))
		off += entryHeaderSize
		if n < 1 || off+n > len(payload) {
			return fmt.Errorf("wal: entry length %d out of range at byte %d of a checksummed batch", n, off)
		}
		kind, body := payload[off], payload[off+1:off+n]
		off += n
		if kind != entryRecord || fn == nil {
			continue
		}
		// Copied: the contract is that fn's slice stays valid after it returns.
		if err := fn(append([]byte(nil), body...)); err != nil {
			return err
		}
	}
	return nil
}

// Compact deletes the segments a replay after `after` would never open, returning how
// many were removed. It is the disk-bounding half of ADR-0131.
//
// The set it deletes is computed by the very same rule ReplayFrom uses to skip, via the
// same helper: a segment goes only when the next one provably starts past its last
// record, so "deleted" and "skipped" can never drift apart. Two consequences fall out
// structurally rather than by a check:
//
//   - the **active segment is never deleted** — the skip rule can never advance past the
//     last segment, because nothing follows it to bound its extent;
//   - a segment is deleted only when *every* record in it is at or below after.
//
// Deletion runs oldest-first, so an interruption leaves a contiguous suffix — still a
// valid log, just less compacted. The caller owns the safety question of *what* after
// may be: it must be a position that no future recovery, and no log consumer, still
// needs (Processor.CompactLog derives it). after == 0 deletes nothing.
func (l *Log) Compact(after uint64, positionOf func(data []byte) (uint64, error)) (int, error) {
	if after == 0 {
		return 0, nil
	}
	segs, err := l.segmentFiles()
	if err != nil {
		return 0, err
	}
	start, err := l.firstSegmentHolding(segs, after, positionOf)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, name := range segs[:start] {
		if err := os.Remove(filepath.Join(l.dir, name)); err != nil {
			return removed, err
		}
		removed++
	}
	if removed > 0 {
		// fsync the directory so the removals survive a crash.
		if err := l.syncDir(); err != nil {
			return removed, err
		}
	}
	return removed, nil
}
