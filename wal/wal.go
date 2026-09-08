// Package wal is Atlas's write-ahead log: a segmented, append-only record
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
// it decoupled from the record model. The unit of framing is the *batch*, not
// the record: one length and one CRC32C cover everything a Sync writes, so a
// write torn anywhere inside it fails as a whole and the batch is discarded
// entire. Framing each record separately made every prefix of that one write
// look like a shorter valid log, which let a crash leave half a command's
// events behind (ADR-0285).
//
// Every segment opens with a 16-byte header naming the format, so a file written
// by an older build is recognised rather than misread. Those segments — one
// record per frame, no header — are still replayed, and are never appended to.
//
// A Log is owned by a single goroutine (the partition's writer, invariant I3)
// and is not safe for concurrent use.
package wal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	// batchHeaderSize is the per-batch framing overhead: uint32 payload length +
	// uint32 CRC32C over that payload.
	batchHeaderSize = 8
	// entryHeaderSize is the per-entry overhead inside a batch payload: a uint32
	// length covering the kind byte and the entry's bytes.
	entryHeaderSize = 4
	// defaultMaxSegmentSize is the soft size cap that triggers a segment roll.
	defaultMaxSegmentSize = 64 << 20
	// maxRecordSize bounds a single record so one oversized entry cannot be
	// staged.
	maxRecordSize = 64 << 20
	// maxBatchBytes bounds a batch payload read off disk, so a corrupt length
	// prefix cannot drive an enormous allocation during replay. A batch holds at
	// most maxBatchSize commands' events; this leaves generous room above any
	// plausible one while staying an allocation a machine can refuse.
	maxBatchBytes = 256 << 20
	segmentSuffix = ".wal"

	// segmentHeaderSize is the fixed preamble every batch-framed segment starts
	// with: magic, format version, and a reserved word.
	segmentHeaderSize = 16
	// segmentVersion is the format this build writes. Version 1 was one frame per
	// record with no batch envelope, and is identified by the *absence* of the
	// magic — those files carry no header at all.
	segmentVersion = 2
)

// segmentMagic opens every version-2 segment. A version-1 segment starts
// directly with a frame length, so the magic is what tells the two apart.
var segmentMagic = [8]byte{'A', 'T', 'L', 'A', 'S', 'W', 'A', 'L'}

// Entry kinds inside a batch payload. The kind byte is what lets a batch carry
// something that is not an event without a second format change: everything the
// engine folds is a record, and Replay delivers only those.
const (
	entryRecord uint8 = 0
	// entryContinuation carries the work a batch still owes after committing: the
	// commands its events scheduled, which lived only in memory until now. It is
	// not an event — nothing folds it into state — so a reader that only wants
	// records never sees it (ADR-0271).
	entryContinuation uint8 = 1
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
	// NoFsync writes each batch but does not force it to the platter, so a crash
	// can lose the tail of the log.
	//
	// It exists for a log nobody will ever recover: the Playground's sandbox is
	// discarded when the run ends, and its whole point is to be cheap. Turning it
	// on for anything a process instance depends on breaks "durable before
	// visible" (invariant I2), which is the one thing this log is for — so it is
	// named after what it gives up rather than after the speed it buys.
	NoFsync bool
}

// Log is a segmented append-only write-ahead log.
type Log struct {
	dir            string
	maxSegmentSize int64
	noFsync        bool

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

	l := &Log{dir: opts.Dir, maxSegmentSize: max, noFsync: opts.NoFsync}
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
	path := filepath.Join(l.dir, last)
	batched, err := isBatchFramed(path)
	if err != nil {
		return nil, err
	}
	if !batched {
		// A segment written before batch framing stays exactly as it is: its records
		// are still replayed, but a batch cannot be appended into a file whose framing
		// predates batches. Writing continues in a fresh segment, so an existing log
		// keeps running across the upgrade and only the records written from here on
		// gain the all-or-nothing guarantee — the ones already on disk cannot be given
		// it retroactively (ADR-0285).
		if err := l.openNewSegment(seq + 1); err != nil {
			return nil, err
		}
		return l, nil
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	// The active segment: the one place a crash may legitimately have left a torn
	// tail, so the scan is allowed to stop at one.
	seg, err := scanOf(path, false)
	if err != nil {
		f.Close()
		return nil, err
	}
	validEnd, err := readBatches(f, seg, nil, nil)
	if err != nil {
		f.Close()
		return nil, err
	}
	// Drop any torn bytes past the last durable batch so future appends extend
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

// isOurTornHead reports whether head is a proper prefix of a version-2 segment
// header — the shape a crash leaves when it interrupts the header write itself.
// A version-1 segment begins with a frame length, and the four bytes that would
// collide here decode to a length far past maxBatchBytes, so nothing real is
// misread as torn.
func isOurTornHead(head []byte) bool {
	if len(head) == 0 || len(head) >= segmentHeaderSize {
		return false
	}
	if len(head) <= len(segmentMagic) {
		return bytes.HasPrefix(segmentMagic[:], head)
	}
	return bytes.HasPrefix(head, segmentMagic[:])
}

// isBatchFramed reports whether the segment at path carries the version-2 header.
// A file too short to hold one, or starting with anything else, is a version-1
// segment: those begin directly with a frame length and have no header at all.
func isBatchFramed(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	var hdr [segmentHeaderSize]byte
	n, err := io.ReadFull(f, hdr[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return false, err
	}
	batched, torn, herr := parseSegmentHeader(hdr[:n])
	if herr != nil {
		return false, fmt.Errorf("wal: %s: %w", filepath.Base(path), herr)
	}
	// A segment whose header was cut short is still ours: it holds nothing, and the
	// truncation below trims it back to empty so writing can continue in it.
	return batched || torn, nil
}

// parseSegmentHeader reads a segment's opening bytes and says what kind of file it
// is: batch-framed, ours with the header cut short mid-write, or a version-1
// segment (which has no header at all and begins directly with a frame length).
//
// It is the one place that decides, because the writer and the reader both have to
// agree and a second copy of this rule is a second chance to disagree with it.
func parseSegmentHeader(head []byte) (batched, torn bool, err error) {
	if len(head) < segmentHeaderSize {
		// Too short to hold a header. If what is there is the start of ours, this is
		// our segment with its header cut short by a crash — not a version-1 file
		// that happens to begin with those bytes.
		return false, isOurTornHead(head), nil
	}
	if !bytes.Equal(head[:len(segmentMagic)], segmentMagic[:]) {
		return false, false, nil
	}
	if v := binary.LittleEndian.Uint32(head[len(segmentMagic):]); v != segmentVersion {
		return false, false, fmt.Errorf("segment is format version %d, which this build cannot read", v)
	}
	return true, false, nil
}

// Append stages data as the next record. It is buffered, not durable, until
// Sync returns. The data is copied into the WAL's buffer, so the caller may
// reuse its slice immediately.
//
// Records must be non-empty: a zero-length frame is indistinguishable from the
// zeroed tail a crash can leave, so the reader treats length zero as end of
// log. Real records always carry a header, so this is not a practical limit.
func (l *Log) Append(data []byte) error {
	return l.appendEntry(entryRecord, data)
}

// AppendContinuation stages the batch's outstanding work as a non-record entry.
// It is durable exactly when the batch's events are — the same frame, the same
// fsync — which is the point: the obligation to continue must not be able to
// survive or vanish separately from the events that created it.
//
// At most one continuation belongs in a batch, and the newest one supersedes
// every earlier one, since each describes the whole queue rather than a delta.
func (l *Log) AppendContinuation(data []byte) error {
	return l.appendEntry(entryContinuation, data)
}

// appendEntry stages one typed entry in the batch being built. The batch header
// is reserved in front of the first entry and filled in by Sync, so the whole
// batch — header and payload — is one contiguous buffer handed to one write.
func (l *Log) appendEntry(kind uint8, data []byte) error {
	if len(data) == 0 {
		return errors.New("wal: empty record")
	}
	if int64(len(data)) > maxRecordSize {
		return fmt.Errorf("wal: record too large: %d > %d", len(data), maxRecordSize)
	}
	if len(l.pending) == 0 {
		// Room for the length and CRC Sync writes once the payload is complete.
		l.pending = append(l.pending, 0, 0, 0, 0, 0, 0, 0, 0)
	}
	var hdr [entryHeaderSize]byte
	binary.LittleEndian.PutUint32(hdr[0:], uint32(len(data)+1)) // +1 for the kind byte
	l.pending = append(l.pending, hdr[:]...)
	l.pending = append(l.pending, kind)
	l.pending = append(l.pending, data...)
	return nil
}

// Sync writes all staged records and issues exactly one fsync, making the whole
// batch durable. It is a no-op if nothing is staged. Nothing externally
// observable may happen before Sync returns (invariant I2).
func (l *Log) Sync() error {
	if len(l.pending) <= batchHeaderSize {
		return nil
	}
	// Roll before writing so a batch is never split across segments. A segment
	// holding nothing but its header is never rolled: it is already the fresh
	// segment a roll would produce, and rolling it would leave an empty file
	// behind on every start with a small size cap.
	if l.activeSize > segmentHeaderSize && l.activeSize >= l.maxSegmentSize {
		if err := l.roll(); err != nil {
			return err
		}
	}
	// One length and one checksum over the whole payload. This is what makes a
	// batch all-or-nothing: a write torn anywhere inside it leaves bytes that
	// either fall short of the declared length or fail the checksum, and either
	// way the reader discards the batch entire. Framing each record separately
	// made every prefix of the same write look like a shorter valid log, so half
	// a command's events could survive a crash (ADR-0285).
	payload := l.pending[batchHeaderSize:]
	binary.LittleEndian.PutUint32(l.pending[0:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(l.pending[4:], crc32.Checksum(payload, castagnoli))

	n, err := l.active.Write(l.pending)
	l.activeSize += int64(n)
	if err != nil {
		return err
	}
	// A log opened with NoFsync is written but not forced: see Options.NoFsync for
	// the one caller that may ask for it and what it gives up.
	if !l.noFsync {
		if err := l.active.Sync(); err != nil {
			return err
		}
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
	// The header names the format, so a reader never has to guess how to parse
	// what follows — and an older file, which has no header, is recognised as
	// older rather than misread as a batch of nonsense.
	var hdr [segmentHeaderSize]byte
	copy(hdr[:], segmentMagic[:])
	binary.LittleEndian.PutUint32(hdr[len(segmentMagic):], segmentVersion)
	if _, err := f.Write(hdr[:]); err != nil {
		f.Close()
		return err
	}
	l.active = f
	l.activeSize = segmentHeaderSize
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

func (l *Log) segmentFiles() ([]string, error) { return segmentFilesIn(l.dir) }

// segmentFilesIn lists the segment file names in dir, in append (segment) order.
// It is a free function so a Tailer can read a log directory without holding the
// writing *Log (invariant I3: the Tailer never touches the writer's state).
func segmentFilesIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
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
	if err := checkSegmentContinuity(names); err != nil {
		return nil, err
	}
	return names, nil
}

// checkSegmentContinuity refuses a segment list with a hole in it.
//
// Segments are numbered consecutively as they roll, and the only thing that ever
// removes one is compaction, which deletes a *prefix* — so what remains is always
// a contiguous run. A gap in the middle is a segment that went missing some other
// way, and replaying across it would skip everything it held without a word,
// exactly the silent hole strict corruption checking exists to prevent
// (ADR-0283).
func checkSegmentContinuity(names []string) error {
	for i := 1; i < len(names); i++ {
		prev, err := parseSeq(names[i-1])
		if err != nil {
			return fmt.Errorf("wal: bad segment name %q: %w", names[i-1], err)
		}
		cur, err := parseSeq(names[i])
		if err != nil {
			return fmt.Errorf("wal: bad segment name %q: %w", names[i], err)
		}
		if cur != prev+1 {
			return fmt.Errorf("wal: segments jump from %s to %s, so %d segment(s) are missing "+
				"between them. Compaction only ever removes the oldest segments, so a gap in the "+
				"middle means the records they held are gone", names[i-1], names[i], cur-prev-1)
		}
	}
	return nil
}

func segmentName(seq uint64) string {
	return fmt.Sprintf("%016d%s", seq, segmentSuffix)
}

func parseSeq(name string) (uint64, error) {
	return strconv.ParseUint(strings.TrimSuffix(name, segmentSuffix), 10, 64)
}
