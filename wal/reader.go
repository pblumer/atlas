package wal

import (
	"bufio"
	"encoding/binary"
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

func replaySegment(path string, fn func([]byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = readFrames(f, fn)
	return err
}

// readFrames reads length+CRC framed records from r, invoking fn (if non-nil)
// for each valid one. It returns the byte offset of the end of the last valid
// frame — the log's durable extent.
//
// A frame that is incomplete (truncated header or payload) or fails its CRC is
// treated as a torn tail from a crash mid-batch: reading stops cleanly and the
// returned offset excludes it. Only the very end of the last-written segment
// can legitimately be torn, because whole frames are written and fsynced before
// a segment rolls.
func readFrames(r io.Reader, fn func([]byte) error) (int64, error) {
	br := bufio.NewReader(r)
	var consumed int64
	var hdr [frameHeaderSize]byte
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return consumed, nil // clean end or torn header
			}
			return consumed, err
		}
		n := binary.LittleEndian.Uint32(hdr[0:])
		sum := binary.LittleEndian.Uint32(hdr[4:])
		if n == 0 || int64(n) > maxRecordSize {
			return consumed, nil // corrupt or zeroed tail
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(br, payload); err != nil {
			return consumed, nil // torn payload at tail
		}
		if crc32.Checksum(payload, castagnoli) != sum {
			return consumed, nil // corrupt frame
		}
		if fn != nil {
			if err := fn(payload); err != nil {
				return consumed, err
			}
		}
		consumed += frameHeaderSize + int64(n)
	}
}
