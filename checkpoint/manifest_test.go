package checkpoint

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"strings"
	"testing"
)

func sampleManifest() Manifest {
	return Manifest{
		Partition:       1,
		AppliedPosition: 4242,
		HighestPosition: 4250,
		KeyCounter:      99,
		StateChecksum:   0xDEADBEEFCAFEF00D,
		CreatedUnixNano: 1_755_000_000_000_000_000,
		AtlasVersion:    "0.2.0-dev+abc123",
		Deployments: []DeploymentRef{
			{Key: 7, Version: 3},
			{Key: 9, Version: 1},
		},
	}
}

// TestManifestRoundTrip: a fully-populated manifest survives Marshal → Unmarshal
// unchanged, and validates.
func TestManifestRoundTrip(t *testing.T) {
	m := sampleManifest()
	b, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Partition != m.Partition || got.AppliedPosition != m.AppliedPosition ||
		got.HighestPosition != m.HighestPosition || got.KeyCounter != m.KeyCounter ||
		got.StateChecksum != m.StateChecksum || got.CreatedUnixNano != m.CreatedUnixNano ||
		got.AtlasVersion != m.AtlasVersion || len(got.Deployments) != len(m.Deployments) {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, m)
	}
	for i := range m.Deployments {
		if got.Deployments[i] != m.Deployments[i] {
			t.Fatalf("deployment %d: got %+v want %+v", i, got.Deployments[i], m.Deployments[i])
		}
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// TestManifestRoundTripMinimal: no deployments and an empty version string still
// round-trips (exercises the empty-string and zero-deployments branches).
func TestManifestRoundTripMinimal(t *testing.T) {
	m := Manifest{Partition: 2, AppliedPosition: 1, HighestPosition: 1}
	b, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.AtlasVersion != "" || len(got.Deployments) != 0 || got.Partition != 2 || got.AppliedPosition != 1 {
		t.Fatalf("minimal round-trip mismatch: %+v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestManifestMarshalRejectsOverLongVersion(t *testing.T) {
	m := Manifest{AtlasVersion: strings.Repeat("x", maxAtlasVersion+1)}
	if _, err := m.Marshal(); !errors.Is(err, ErrTooLong) {
		t.Fatalf("Marshal over-long version err = %v, want ErrTooLong", err)
	}
}

func TestManifestValidate(t *testing.T) {
	// HighestPosition below AppliedPosition is inconsistent.
	bad := Manifest{AppliedPosition: 10, HighestPosition: 9}
	if err := bad.Validate(); !errors.Is(err, ErrInconsistent) {
		t.Fatalf("Validate highest<applied err = %v, want ErrInconsistent", err)
	}
	// An over-long version is rejected by Validate too (independent of encoding).
	long := Manifest{AtlasVersion: strings.Repeat("y", maxAtlasVersion+1)}
	if err := long.Validate(); !errors.Is(err, ErrTooLong) {
		t.Fatalf("Validate over-long version err = %v, want ErrTooLong", err)
	}
	// Equal positions are valid (a checkpoint with nothing un-applied beyond it).
	if err := (&Manifest{AppliedPosition: 5, HighestPosition: 5}).Validate(); err != nil {
		t.Fatalf("Validate equal positions: %v", err)
	}
}

func TestUnmarshalBadMagic(t *testing.T) {
	if _, err := Unmarshal([]byte{1, 2}); !errors.Is(err, ErrBadMagic) { // shorter than the magic
		t.Fatalf("short input err = %v, want ErrBadMagic", err)
	}
	bad := make([]byte, 80)
	copy(bad, []byte("XXXX"))
	if _, err := Unmarshal(bad); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("wrong magic err = %v, want ErrBadMagic", err)
	}
}

func TestUnmarshalTruncatedPrefix(t *testing.T) {
	b := make([]byte, 20) // valid magic, but far too short for the fixed prefix
	copy(b, magic[:])
	if _, err := Unmarshal(b); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated prefix err = %v, want ErrTruncated", err)
	}
}

func TestUnmarshalUnsupportedVersion(t *testing.T) {
	m := sampleManifest()
	b, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	binary.LittleEndian.PutUint32(b[4:], FormatVersion+1) // bump the version field
	// The CRC no longer matches, but the version is checked first.
	if _, err := Unmarshal(b); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("unsupported version err = %v, want ErrUnsupportedVersion", err)
	}
}

func TestUnmarshalChecksumMismatch(t *testing.T) {
	m := sampleManifest()
	b, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	b[10] ^= 0xFF // flip a body byte without fixing the trailing CRC
	if _, err := Unmarshal(b); !errors.Is(err, ErrChecksum) {
		t.Fatalf("checksum mismatch err = %v, want ErrChecksum", err)
	}
}

// reseal recomputes the trailing CRC so a hand-tampered body still passes the checksum
// gate — letting the internal length-consistency checks be exercised.
func reseal(b []byte) []byte {
	binary.LittleEndian.PutUint32(b[len(b)-4:], crc32.Checksum(b[:len(b)-4], castagnoli))
	return b
}

func TestUnmarshalInconsistentStringLength(t *testing.T) {
	m := Manifest{Partition: 1, AppliedPosition: 1, HighestPosition: 1}
	b, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// The version-length byte sits right after the fixed numeric fields (offset 50).
	// Claim a huge string that cannot fit, then reseal so the CRC passes.
	b[50] = 200
	b = reseal(b)
	if _, err := Unmarshal(b); !errors.Is(err, ErrTruncated) {
		t.Fatalf("bad string length err = %v, want ErrTruncated", err)
	}
}

func TestUnmarshalInconsistentDeploymentCount(t *testing.T) {
	m := Manifest{Partition: 1, AppliedPosition: 1, HighestPosition: 1,
		Deployments: []DeploymentRef{{Key: 1, Version: 1}}}
	b, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// The deployment count is the uint32 just before the 12-byte ref and the 4-byte CRC.
	countOff := len(b) - 4 - 12 - 4
	binary.LittleEndian.PutUint32(b[countOff:], 99) // claim 99 refs; only one is present
	b = reseal(b)
	if _, err := Unmarshal(b); !errors.Is(err, ErrTruncated) {
		t.Fatalf("bad deployment count err = %v, want ErrTruncated", err)
	}
}

func TestUnmarshalTrailingGarbageWithZeroDeployments(t *testing.T) {
	m := Manifest{Partition: 1, AppliedPosition: 1, HighestPosition: 1}
	b, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Insert an extra byte before the CRC so, with zero deployments, off != len(body).
	tampered := make([]byte, 0, len(b)+1)
	tampered = append(tampered, b[:len(b)-4]...)
	tampered = append(tampered, 0x7F)
	tampered = append(tampered, b[len(b)-4:]...)
	tampered = reseal(tampered)
	if _, err := Unmarshal(tampered); !errors.Is(err, ErrTruncated) {
		t.Fatalf("trailing garbage err = %v, want ErrTruncated", err)
	}
}
