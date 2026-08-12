package wal

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
)

// Cursor marks a resume point in the log for a Tailer: the segment (by its index
// in append order) and a byte offset at a frame boundary within it. The zero
// Cursor is the start of the log (genesis). Its fields are unexported — a caller
// only ever stores a Cursor returned by [Tailer.Read] and passes it back; it is
// valid within one process run (segments are append-only today, never deleted),
// and a restart resumes from genesis by design (ADR-0114).
type Cursor struct {
	seg int   // index into the ordered segment list
	off int64 // byte offset within that segment, at a frame boundary
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
		stopped, next, err := tailSegment(filepath.Join(t.dir, segs[cur.seg]), cur.off, fn)
		cur.off = next
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
			cur.off = 0
			continue
		}
		return cur, nil
	}
	return cur, nil
}

// tailSegment reads length+CRC framed records from the file at path, starting at
// byte offset start, invoking fn for each valid frame. It returns whether fn
// stopped and the offset to resume at: the start of the stopping frame (when
// stopped), or the end of the last valid frame (the segment's durable extent).
// An incomplete or CRC-failing frame is treated as a torn/not-yet-written tail:
// reading stops cleanly and the returned offset excludes it.
func tailSegment(path string, start int64, fn func([]byte) (bool, error)) (stopped bool, next int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return false, start, err
	}
	defer f.Close()

	off := start
	var hdr [frameHeaderSize]byte
	for {
		if _, err := f.ReadAt(hdr[:], off); err != nil {
			return false, off, nil // clean end or torn header
		}
		n := binary.LittleEndian.Uint32(hdr[0:])
		sum := binary.LittleEndian.Uint32(hdr[4:])
		if n == 0 || int64(n) > maxRecordSize {
			return false, off, nil // zeroed or corrupt length → treat as tail
		}
		payload := make([]byte, n)
		if _, err := f.ReadAt(payload, off+frameHeaderSize); err != nil {
			return false, off, nil // torn payload at tail
		}
		if crc32.Checksum(payload, castagnoli) != sum {
			return false, off, nil // corrupt frame → stop at last good offset
		}
		stop, ferr := fn(payload)
		if ferr != nil {
			return false, off, ferr
		}
		if stop {
			return true, off, nil // resume AT this frame; it is not consumed
		}
		off += frameHeaderSize + int64(n)
	}
}
