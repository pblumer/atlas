package wal_test

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/wal"
)

var castagnoliTable = crc32.MakeTable(crc32.Castagnoli)

// segmentHeaderFor renders a segment header naming version v.
func segmentHeaderFor(v uint32) []byte {
	hdr := make([]byte, 16)
	copy(hdr, "ATLASWAL")
	binary.LittleEndian.PutUint32(hdr[8:], v)
	return hdr
}

// batchOf renders one framed batch holding the given records.
func batchOf(records ...string) []byte {
	var payload []byte
	for _, rec := range records {
		var eh [4]byte
		binary.LittleEndian.PutUint32(eh[0:], uint32(len(rec)+1))
		payload = append(payload, eh[:]...)
		payload = append(payload, 0) // kind: record
		payload = append(payload, rec...)
	}
	return frameBatch(payload)
}

// frameBatch wraps a payload in the batch header, checksum and all, so a test can
// hand the reader a batch that is intact on disk but wrong inside.
func frameBatch(payload []byte) []byte {
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(hdr[4:], crc32.Checksum(payload, castagnoliTable))
	return append(hdr[:], payload...)
}

func writeSegment(t *testing.T, dir string, parts ...[]byte) {
	t.Helper()
	var blob []byte
	for _, p := range parts {
		blob = append(blob, p...)
	}
	if err := os.WriteFile(filepath.Join(dir, "0000000000000000.wal"), blob, 0o644); err != nil {
		t.Fatalf("write segment: %v", err)
	}
}

// TestUnknownFormatVersionIsRefusedByName: a segment written by a future build
// must be refused, and refused *by version*, rather than parsed as though its
// layout were the one this build knows.
//
// This is the reason the header exists at all. Guessing at an unfamiliar layout
// is how a log gets misread into plausible-looking nonsense; saying "version 3,
// which this build cannot read" is a message an operator can act on.
func TestUnknownFormatVersionIsRefusedByName(t *testing.T) {
	dir := t.TempDir()
	writeSegment(t, dir, segmentHeaderFor(3), batchOf("unreadable"))

	_, err := wal.Open(wal.Options{Dir: dir})
	if err == nil {
		t.Fatal("Open of a future-version segment: got nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "3") {
		t.Errorf("error %q does not name the version it refused", err)
	}
}

// TestUnknownFormatVersionStopsReplay: the refusal reaches every reader, not just
// Open — a replay that quietly returned nothing would look like an empty log.
func TestUnknownFormatVersionStopsReplay(t *testing.T) {
	dir := t.TempDir()
	// A readable first segment, then one this build cannot parse.
	writeSegment(t, dir, segmentHeaderFor(2), batchOf("fine"))
	if err := os.WriteFile(filepath.Join(dir, "0000000000000001.wal"),
		append(segmentHeaderFor(9), batchOf("later")...), 0o644); err != nil {
		t.Fatalf("write second segment: %v", err)
	}

	l, err := wal.Open(wal.Options{Dir: dir})
	if err == nil {
		defer l.Close()
		var got []string
		rerr := l.Replay(func(data []byte) error {
			got = append(got, string(data))
			return nil
		})
		if rerr == nil {
			t.Fatalf("Replay across an unreadable segment returned %q and no error", got)
		}
		if !strings.Contains(rerr.Error(), "9") {
			t.Errorf("replay error %q does not name the version it refused", rerr)
		}
		return
	}
	if !strings.Contains(err.Error(), "9") {
		t.Errorf("Open error %q does not name the version it refused", err)
	}
}

// TestMalformedEntryInAValidBatchIsCorruption: once a batch has passed its
// checksum, its bytes are the bytes that were written. An entry that does not
// parse there is not a torn tail — nothing was torn — so it must surface as an
// error rather than be swallowed as an end of log.
//
// The distinction matters because the two look identical to a caller who only
// sees "replay returned fewer records": one is a crash that cost the tail, the
// other is a log that is lying about its own contents.
func TestMalformedEntryInAValidBatchIsCorruption(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []byte
	}{
		{
			name: "entry length runs past the batch",
			// Declares 99 bytes of entry inside a 5-byte payload.
			payload: func() []byte {
				p := make([]byte, 4)
				binary.LittleEndian.PutUint32(p, 99)
				return append(p, 0)
			}(),
		},
		{
			name: "entry length of zero leaves no room for the kind byte",
			payload: func() []byte {
				p := make([]byte, 4)
				binary.LittleEndian.PutUint32(p, 0)
				return p
			}(),
		},
		{
			name: "trailing bytes too short for an entry header",
			payload: func() []byte {
				var eh [4]byte
				binary.LittleEndian.PutUint32(eh[0:], 3)
				p := append(eh[:], 0, 'h', 'i')
				return append(p, 0x01, 0x02) // two stray bytes: not a header
			}(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSegment(t, dir, segmentHeaderFor(2), frameBatch(tc.payload))

			// Either path may report it — Open scans the active segment, Replay
			// re-reads it — but one of them must, and neither may treat it as a
			// clean end of log.
			l, err := wal.Open(wal.Options{Dir: dir})
			if err != nil {
				t.Logf("reported at Open: %v", err)
				return
			}
			defer l.Close()
			rerr := l.Replay(func([]byte) error { return nil })
			if rerr == nil {
				t.Fatal("a checksummed batch that does not parse was accepted as a clean end of log")
			}
			t.Logf("reported at Replay: %v", rerr)
		})
	}
}

// TestTailerRefusesAnUnknownFormatVersion: the export path reads the same files
// and must not be the one place a future format is guessed at.
func TestTailerRefusesAnUnknownFormatVersion(t *testing.T) {
	dir := t.TempDir()
	writeSegment(t, dir, segmentHeaderFor(7), batchOf("nope"))
	if _, err := wal.NewTailer(dir).Read(wal.Cursor{}, func([]byte) (bool, error) {
		return false, nil
	}); err == nil {
		t.Fatal("Tailer over a future-version segment: got nil error, want a refusal")
	}
}
