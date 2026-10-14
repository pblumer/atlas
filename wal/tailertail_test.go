package wal_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/wal"
)

// tailAll reads every record a tailer can see from genesis.
func tailAll(t *testing.T, dir string) []string {
	t.Helper()
	var got []string
	if _, err := wal.NewTailer(dir).Read(wal.Cursor{}, func(data []byte) (bool, error) {
		got = append(got, string(data))
		return false, nil
	}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	return got
}

// TestTailerStopsAtATruncatedPayload is the crash tail as it actually looks: the
// batch header made it to disk and the payload did not. The length says how much to
// expect and the file ends short of it.
//
// A batch is all or nothing (ADR-0285), so the tailer must stop
// at the last whole one and hand back an offset that excludes the fragment — not
// read what is there and deliver half a batch's records, which is precisely the
// "half a command" the batch envelope exists to prevent.
func TestTailerStopsAtATruncatedPayload(t *testing.T) {
	dir := t.TempDir()
	whole := batchOf("first", "second")
	torn := batchOf("never-written")
	torn = torn[:len(torn)-3] // header intact, payload cut short

	writeSegment(t, dir, segmentHeaderFor(2), whole, torn)
	if got := tailAll(t, dir); len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("read %q, want the two records of the whole batch and nothing from the torn one", got)
	}
}

// TestTailerReportsAMalformedBatchPayload: a batch whose checksum is right but
// whose entry framing is not is a different failure from a torn tail. The bytes on
// disk are the bytes that were written, so stopping quietly would call a corrupt
// batch "the end of the log" and lose everything after it. It is returned as an
// error instead.
func TestTailerReportsAMalformedBatchPayload(t *testing.T) {
	dir := t.TempDir()
	// An entry that claims more bytes than the payload holds — intact on disk,
	// wrong inside — wrapped in a correct batch header and checksum.
	payload := make([]byte, 4+1+4)
	binary.LittleEndian.PutUint32(payload[0:], 1<<20) // entry length far past the end
	writeSegment(t, dir, segmentHeaderFor(2), frameBatch(payload))

	_, err := wal.NewTailer(dir).Read(wal.Cursor{}, func([]byte) (bool, error) { return false, nil })
	if err == nil {
		t.Fatal("a batch whose entry framing is malformed was read as the end of the log")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "0000000000000000.wal")); statErr != nil {
		t.Fatalf("the segment should still be there: %v", statErr)
	}
}
