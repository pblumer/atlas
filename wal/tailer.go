package wal

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
)

// Cursor marks a resume point in the log for a Tailer: the segment (by its index
// in append order), a byte offset at a batch boundary within it, and how many of
// that batch's records have already been consumed. The zero Cursor is the start
// of the log (genesis). Its fields are unexported — a caller only ever stores a
// Cursor returned by [Tailer.Read] and passes it back; it is valid within one
// process run (segments are append-only today, never deleted), and a restart
// resumes from genesis by design (ADR-0114).
//
// The record index is what keeps "resume at exactly the stopping record" true now
// that several records share one framed batch: without it, stopping part-way
// through a batch would have to resume at the batch's start and re-deliver the
// records before the stopping one.
type Cursor struct {
	seg int   // index into the ordered segment list
	off int64 // byte offset within that segment, at a batch boundary
	rec int   // records of the batch at off already consumed
}

// Tailer reads durable records forward from a [Cursor], across segment rolls,
// resuming where a previous read left off. Unlike [Log.Replay] (one shot, from
// genesis), a Tailer is stateless apart from the Cursor the caller threads
// through, so it can poll a growing log for newly-appended records.
//
// A Tailer opens segment files read-only and independently of the writing [Log],
// so it is safe to run from another goroutine while the log is being appended
// (invariant I3). It reads only whole, CRC-valid frames and stops cleanly at a
// torn or not-yet-written tail — the same discipline recovery relies on — so it
// never observes a partially-written record. Records staged by [Log.Append] but
// not yet made durable by [Log.Sync] are not on disk and are invisible to it.
type Tailer struct {
	dir string
}

// NewTailer returns a Tailer over the segment files in dir.
func NewTailer(dir string) *Tailer { return &Tailer{dir: dir} }

// Read invokes fn for each durable record at or after `from`, in append order.
// If fn returns stop=true, Read halts immediately and the stopping record is
// **not** consumed: the returned Cursor points at it, so a later Read from that
// Cursor re-delivers it. This lets a caller bound how far it reads by a limit it
// computes from the record itself (e.g. a durable-position watermark, ADR-0114)
// and resume at exactly that record next time.
//
// When fn never stops, Read consumes every currently-durable record and returns
// the Cursor at the log's durable end. The []byte passed to fn is freshly
// allocated per record, so it remains valid after fn returns. On an I/O error
// Read returns the last safe Cursor and the error; the caller should not adopt a
// Cursor from a failed Read.
func (t *Tailer) Read(from Cursor, fn func(data []byte) (stop bool, err error)) (Cursor, error) {
	segs, err := segmentFilesIn(t.dir)
	if err != nil {
		return from, err
	}
	cur := from
	for cur.seg < len(segs) {
		stopped, next, rec, err := tailSegment(filepath.Join(t.dir, segs[cur.seg]), cur.off, cur.rec, fn)
		cur.off, cur.rec = next, rec
		if err != nil {
			return cur, err
		}
		if stopped {
			return cur, nil
		}
		// Durable end of this segment reached without stopping. If a later segment
		// exists, this one has rolled (it is immutable and now fully drained), so
		// advance to the next; otherwise we have caught up with the active tail.
		if cur.seg < len(segs)-1 {
			cur.seg++
			cur.off, cur.rec = 0, 0
			continue
		}
		return cur, nil
	}
	return cur, nil
}

// tailSegment reads framed batches from the file at path, starting at byte offset
// start and skipping the first skip records of the batch found there, invoking fn
// for every record of every whole batch. It returns whether fn stopped, the
// offset to resume at, and how many records of the batch at that offset have been
// consumed.
//
// An incomplete or CRC-failing batch is treated as a torn/not-yet-written tail:
// reading stops cleanly and the returned offset excludes it, so a batch is never
// observed in part.
func tailSegment(path string, start int64, skip int, fn func([]byte) (bool, error)) (stopped bool, next int64, rec int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return false, start, skip, err
	}
	defer f.Close()

	off := start
	batched, err := segmentIsBatched(f)
	if err != nil {
		return false, start, skip, err
	}
	if batched && off == 0 {
		off = segmentHeaderSize
	}
	var hdr [batchHeaderSize]byte
	for {
		if _, err := f.ReadAt(hdr[:], off); err != nil {
			return false, off, skip, nil // clean end or torn header
		}
		n := binary.LittleEndian.Uint32(hdr[0:])
		sum := binary.LittleEndian.Uint32(hdr[4:])
		if n == 0 || int64(n) > maxBatchBytes {
			return false, off, skip, nil // zeroed or corrupt length → treat as tail
		}
		payload := make([]byte, n)
		if _, err := f.ReadAt(payload, off+batchHeaderSize); err != nil {
			return false, off, skip, nil // torn payload at tail
		}
		if crc32.Checksum(payload, castagnoli) != sum {
			return false, off, skip, nil // corrupt batch → stop at last good offset
		}

		records, derr := batchRecords(payload, batched)
		if derr != nil {
			return false, off, skip, derr
		}
		for i := skip; i < len(records); i++ {
			stop, ferr := fn(records[i])
			if ferr != nil {
				return false, off, i, ferr
			}
			if stop {
				// Resume AT this record: it is not consumed.
				return true, off, i, nil
			}
		}
		off += batchHeaderSize + int64(n)
		skip = 0
	}
}

// segmentIsBatched reports whether f carries the version-2 header, without
// disturbing the caller's own offsets (it reads at an explicit offset).
func segmentIsBatched(f *os.File) (bool, error) {
	var hdr [segmentHeaderSize]byte
	if _, err := f.ReadAt(hdr[:], 0); err != nil {
		return false, nil // too short to be a version-2 segment
	}
	if !bytes.Equal(hdr[:len(segmentMagic)], segmentMagic[:]) {
		return false, nil
	}
	if v := binary.LittleEndian.Uint32(hdr[len(segmentMagic):]); v != segmentVersion {
		return false, fmt.Errorf("wal: segment is format version %d, which this build cannot read", v)
	}
	return true, nil
}

// batchRecords splits a whole batch payload into its records. A version-1 frame
// holds exactly one.
func batchRecords(payload []byte, batched bool) ([][]byte, error) {
	if !batched {
		return [][]byte{payload}, nil
	}
	var out [][]byte
	if err := forEachEntry(payload, func(rec []byte) error {
		out = append(out, rec)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}
