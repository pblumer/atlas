// Package wal is Chrampfer's write-ahead log: a segmented, append-only record
// store with group commit.
//
// The log is the single source of truth (ADR-0001). Durability comes from
// fsync, but one fsync per record caps throughput at a few thousand per second
// (ADR-0005), so the WAL separates buffering from flushing: [Log.Append]
// stages records in memory and [Log.Sync] writes the whole batch and issues
// exactly one fsync. A processor appends every event of a batch, then calls
// Sync once — the "durable before visible" boundary (invariant I2).
//
// Entries are opaque byte slices; the WAL does not interpret them, which keeps
// it decoupled from the record model. Each entry is framed with a length and a
// CRC32C so forward iteration can detect a torn tail left by a crash mid-batch
// and stop cleanly at the last durable record.
//
// A Log is owned by a single goroutine (the partition's writer, invariant I3)
// and is not safe for concurrent use.
package wal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	// frameHeaderSize is the per-record framing overhead: uint32 length + uint32 CRC.
	frameHeaderSize = 8
	// defaultMaxSegmentSize is the soft size cap that triggers a segment roll.
	defaultMaxSegmentSize = 64 << 20
	// maxRecordSize bounds a single record so a corrupt length prefix can't
	// drive an enormous allocation during replay.
	maxRecordSize = 64 << 20
	segmentSuffix = ".wal"
)

// castagnoli is the CRC32C table (hardware-accelerated on most CPUs).
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Options configures a Log.
type Options struct {
	// Dir is the directory holding segment files. Created if absent.
	Dir string
	// MaxSegmentSize is the soft cap after which the next Sync rolls to a new
	// segment. Zero means the default (64 MiB). A single batch is never split
	// across segments, so a segment may exceed this by up to one batch.
	MaxSegmentSize int64
}

// Log is a segmented append-only write-ahead log.
type Log struct {
	dir            string
	maxSegmentSize int64

	active     *os.File
	activeSize int64
	segSeq     uint64 // sequence number of the active segment

	pending []byte // framed records staged by Append, not yet durable
}

// Open opens (or creates) the log in opts.Dir. If the last segment has a torn
// tail from a crash mid-batch, it is truncated to the last valid frame so
// subsequent appends remain readable.
func Open(opts Options) (*Log, error) {
	if opts.Dir == "" {
		return nil, errors.New("wal: Dir is required")
	}
	max := opts.MaxSegmentSize
	if max <= 0 {
		max = defaultMaxSegmentSize
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, err
	}

	l := &Log{dir: opts.Dir, maxSegmentSize: max}
	segs, err := l.segmentFiles()
	if err != nil {
		return nil, err
	}
	if len(segs) == 0 {
		if err := l.openNewSegment(0); err != nil {
			return nil, err
		}
		return l, nil
	}

	last := segs[len(segs)-1]
	seq, err := parseSeq(last)
	if err != nil {
		return nil, fmt.Errorf("wal: bad segment name %q: %w", last, err)
	}
	f, err := os.OpenFile(filepath.Join(l.dir, last), os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	validEnd, err := readFrames(f, nil)
	if err != nil {
		f.Close()
		return nil, err
	}
	// Drop any torn bytes past the last durable frame so future appends extend
	// a clean log. With O_APPEND, writes resume at the truncated end.
	if err := f.Truncate(validEnd); err != nil {
		f.Close()
		return nil, err
	}
	l.active = f
	l.activeSize = validEnd
	l.segSeq = seq
	return l, nil
}

// Append stages data as the next record. It is buffered, not durable, until
// Sync returns. The data is copied into the WAL's buffer, so the caller may
// reuse its slice immediately.
//
// Records must be non-empty: a zero-length frame is indistinguishable from the
// zeroed tail a crash can leave, so the reader treats length zero as end of
// log. Real records always carry a header, so this is not a practical limit.
func (l *Log) Append(data []byte) error {
	if len(data) == 0 {
		return errors.New("wal: empty record")
	}
	if int64(len(data)) > maxRecordSize {
		return fmt.Errorf("wal: record too large: %d > %d", len(data), maxRecordSize)
	}
	var hdr [frameHeaderSize]byte
	binary.LittleEndian.PutUint32(hdr[0:], uint32(len(data)))
	binary.LittleEndian.PutUint32(hdr[4:], crc32.Checksum(data, castagnoli))
	l.pending = append(l.pending, hdr[:]...)
	l.pending = append(l.pending, data...)
	return nil
}

// Sync writes all staged records and issues exactly one fsync, making the whole
// batch durable. It is a no-op if nothing is staged. Nothing externally
// observable may happen before Sync returns (invariant I2).
func (l *Log) Sync() error {
	if len(l.pending) == 0 {
		return nil
	}
	// Roll before writing so a batch is never split across segments.
	if l.activeSize >= l.maxSegmentSize {
		if err := l.roll(); err != nil {
			return err
		}
	}
	n, err := l.active.Write(l.pending)
	l.activeSize += int64(n)
	if err != nil {
		return err
	}
	if err := l.active.Sync(); err != nil {
		return err
	}
	l.pending = l.pending[:0]
	return nil
}

// Close closes the active segment. Records staged but not yet Synced are
// discarded — by contract they were never durable.
func (l *Log) Close() error {
	if l.active == nil {
		return nil
	}
	err := l.active.Close()
	l.active = nil
	return err
}

func (l *Log) roll() error {
	if err := l.active.Close(); err != nil {
		return err
	}
	return l.openNewSegment(l.segSeq + 1)
}

func (l *Log) openNewSegment(seq uint64) error {
	name := filepath.Join(l.dir, segmentName(seq))
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	l.active = f
	l.activeSize = 0
	l.segSeq = seq
	// fsync the directory so the new segment's existence survives a crash.
	return l.syncDir()
}

func (l *Log) syncDir() error {
	d, err := os.Open(l.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (l *Log) segmentFiles() ([]string, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), segmentSuffix) {
			names = append(names, e.Name())
		}
	}
	// Zero-padded names sort lexically in segment order.
	sort.Strings(names)
	return names, nil
}

func segmentName(seq uint64) string {
	return fmt.Sprintf("%016d%s", seq, segmentSuffix)
}

func parseSeq(name string) (uint64, error) {
	return strconv.ParseUint(strings.TrimSuffix(name, segmentSuffix), 10, 64)
}
