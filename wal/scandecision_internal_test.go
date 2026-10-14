package wal

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
)

// batchBytes frames a payload the way Sync does.
func batchBytes(payload []byte) []byte {
	var hdr [batchHeaderSize]byte
	binary.LittleEndian.PutUint32(hdr[0:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(hdr[4:], crc32.Checksum(payload, castagnoli))
	return append(hdr[:], payload...)
}

// entryBytes frames one record entry inside a batch payload.
func entryBytes(kind uint8, body string) []byte {
	var hdr [entryHeaderSize]byte
	binary.LittleEndian.PutUint32(hdr[0:], uint32(len(body)+1))
	out := append(hdr[:], kind)
	return append(out, body...)
}

func v2Segment(batches ...[]byte) []byte {
	seg := make([]byte, segmentHeaderSize)
	copy(seg, segmentMagic[:])
	binary.LittleEndian.PutUint32(seg[len(segmentMagic):], segmentVersion)
	for _, b := range batches {
		seg = append(seg, b...)
	}
	return seg
}

// TestScanDecisionTable drives the one question the scan has to answer for every
// anomaly it meets: is this the tail a crash left, or is it damage?
//
// The answer turns on two facts and nothing else — whether the segment has rolled,
// and whether anything follows the damage — and the anomalies reach that decision
// by different routes: a bad length is rejected before any payload is read, a
// short payload runs out mid-read, a bad checksum needs the whole batch first.
// Driving the scan directly is what makes the table legible; going through files
// tests the same four cells over and over and leaves the rest untried.
func TestScanDecisionTable(t *testing.T) {
	good := batchBytes(append(entryBytes(entryRecord, "one"), entryBytes(entryRecord, "two")...))
	corrupt := func(b []byte) []byte {
		c := append([]byte(nil), b...)
		c[len(c)-1] ^= 0xFF // break the payload, leaving the length intact
		return c
	}

	for _, tc := range []struct {
		name    string
		content []byte
		sealed  bool
		wantErr string   // substring; empty means the scan must tolerate it
		wantRec []string // records delivered before it stopped
	}{
		{
			name:    "whole batches in a sealed segment",
			content: v2Segment(good, good),
			sealed:  true,
			wantRec: []string{"one", "two", "one", "two"},
		},
		{
			name:    "checksum fails at the end of the active segment",
			content: v2Segment(good, corrupt(good)),
			wantRec: []string{"one", "two"},
		},
		{
			name:    "checksum fails at the end of a sealed segment",
			content: v2Segment(good, corrupt(good)),
			sealed:  true,
			wantErr: "checksum",
			wantRec: []string{"one", "two"},
		},
		{
			name:    "checksum fails with a whole batch after it",
			content: v2Segment(corrupt(good), good),
			wantErr: "checksum",
		},
		{
			name:    "payload cut short at the end of the active segment",
			content: v2Segment(good, good)[:segmentHeaderSize+len(good)+batchHeaderSize+2],
			wantRec: []string{"one", "two"},
		},
		{
			name:    "payload cut short in a sealed segment",
			content: v2Segment(good, good)[:segmentHeaderSize+len(good)+batchHeaderSize+2],
			sealed:  true,
			wantErr: "cut short",
			wantRec: []string{"one", "two"},
		},
		{
			name:    "zero length at the end of the active segment",
			content: append(v2Segment(good), make([]byte, batchHeaderSize)...),
			wantRec: []string{"one", "two"},
		},
		{
			name:    "zero length with a batch after it",
			content: append(append(v2Segment(good), make([]byte, batchHeaderSize)...), good...),
			wantErr: "not a length this log writes",
			// The whole batch before the damage is still delivered: what is read is read.
			wantRec: []string{"one", "two"},
		},
		{
			name:    "segment header cut short in a sealed segment",
			content: v2Segment()[:5],
			sealed:  true,
			wantErr: "segment header cut short",
		},
		{
			name:    "segment header cut short in the active segment",
			content: v2Segment()[:5],
		},
		{
			name: "an entry that does not parse inside a checksummed batch",
			content: v2Segment(batchBytes(func() []byte {
				e := entryBytes(entryRecord, "one")
				binary.LittleEndian.PutUint32(e[0:], 0xFFFF) // length past the payload
				return e
			}())),
			wantErr: "out of range",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			seg := segmentScan{name: "0000000000000000.wal", size: int64(len(tc.content)), sealed: tc.sealed}
			_, err := readBatches(bytes.NewReader(tc.content), seg, func(b []byte) error {
				got = append(got, string(b))
				return nil
			}, nil)

			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("scan reported %v; this shape is a tail a crash can leave", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("scan accepted %s as a clean end of log", tc.name)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error %q does not say %q", err, tc.wantErr)
			}
			if tc.wantErr != "" && !strings.Contains(err.Error(), seg.name) {
				t.Errorf("error %q does not name the segment", err)
			}
			if len(got) != len(tc.wantRec) {
				t.Fatalf("delivered %q, want %q", got, tc.wantRec)
			}
			for i := range tc.wantRec {
				if got[i] != tc.wantRec[i] {
					t.Errorf("record %d = %q, want %q", i, got[i], tc.wantRec[i])
				}
			}
		})
	}
}

// TestScanSurfacesARecordCallbackError: a caller that fails mid-scan gets its own
// error back, not a truncated log. The two versions take different paths through
// the scan, so both are checked.
func TestScanSurfacesARecordCallbackError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content []byte
	}{
		{"batch framed", v2Segment(batchBytes(entryBytes(entryRecord, "one")))},
		{"version 1", frame([]byte("one"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seg := segmentScan{name: "seg.wal", size: int64(len(tc.content))}
			_, err := readBatches(bytes.NewReader(tc.content), seg, func([]byte) error {
				return errStopScan
			}, nil)
			if err == nil {
				t.Fatal("a callback error was swallowed; the caller cannot tell a stop from a short log")
			}
		})
	}
}
