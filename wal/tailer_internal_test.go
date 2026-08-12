package wal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

// frame renders one length+CRC framed record, as the writer stages it.
func frame(payload []byte) []byte {
	var hdr [frameHeaderSize]byte
	binary.LittleEndian.PutUint32(hdr[0:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(hdr[4:], crc32.Checksum(payload, castagnoli))
	return append(hdr[:], payload...)
}

// TestTailerStopsAtCorruptFrame: a segment holding one valid frame followed by a
// CRC-corrupt frame yields only the valid record — the corrupt frame is treated
// as a torn tail, exactly as recovery does.
func TestTailerStopsAtCorruptFrame(t *testing.T) {
	dir := t.TempDir()
	good := frame([]byte("good"))
	bad := frame([]byte("bad"))
	bad[len(bad)-1] ^= 0xFF // flip a payload byte so the CRC no longer matches

	seg := filepath.Join(dir, segmentName(0))
	if err := os.WriteFile(seg, append(good, bad...), 0o644); err != nil {
		t.Fatalf("write segment: %v", err)
	}

	var got []string
	if _, err := NewTailer(dir).Read(Cursor{}, func(data []byte) (bool, error) {
		got = append(got, string(data))
		return false, nil
	}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 1 || got[0] != "good" {
		t.Fatalf("read %q, want just [good] (stop at the corrupt frame)", got)
	}
}

// TestTailerStopsAtZeroLengthFrame: a zeroed length prefix (the tail a crash can
// leave) ends reading cleanly.
func TestTailerStopsAtZeroLengthFrame(t *testing.T) {
	dir := t.TempDir()
	seg := filepath.Join(dir, segmentName(0))
	// One valid frame, then eight zero bytes (a zero length + zero crc header).
	if err := os.WriteFile(seg, append(frame([]byte("only")), make([]byte, frameHeaderSize)...), 0o644); err != nil {
		t.Fatalf("write segment: %v", err)
	}
	var got []string
	if _, err := NewTailer(dir).Read(Cursor{}, func(data []byte) (bool, error) {
		got = append(got, string(data))
		return false, nil
	}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 1 || got[0] != "only" {
		t.Fatalf("read %q, want just [only]", got)
	}
}

// TestTailerPropagatesFnError: an error from fn stops the read and is returned.
func TestTailerPropagatesFnError(t *testing.T) {
	dir := t.TempDir()
	seg := filepath.Join(dir, segmentName(0))
	if err := os.WriteFile(seg, frame([]byte("x")), 0o644); err != nil {
		t.Fatalf("write segment: %v", err)
	}
	boom := errors.New("boom")
	_, err := NewTailer(dir).Read(Cursor{}, func([]byte) (bool, error) {
		return false, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Read error = %v, want boom", err)
	}
}

// TestTailerMissingDirErrors: reading a directory that does not exist surfaces the
// listing error rather than silently returning nothing.
func TestTailerMissingDirErrors(t *testing.T) {
	if _, err := NewTailer(filepath.Join(t.TempDir(), "nope")).Read(Cursor{}, func([]byte) (bool, error) { return false, nil }); err == nil {
		t.Fatalf("Read of a missing dir should error")
	}
}
