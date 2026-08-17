// Package checkpoint holds the on-disk format primitives for engine recovery
// checkpoints (ADR-0133). A checkpoint is a consistent Pebble snapshot of the state
// store at a known applied log position, from which recovery replays only the WAL
// suffix instead of the whole log from genesis.
//
// This package currently provides only the Manifest — the small, engine-owned,
// versioned descriptor that makes a checkpoint interpretable and verifiable — with a
// deterministic binary codec and validation. Creating and publishing checkpoints,
// restoring from them, and compacting WAL segments are later slices of ADR-0133; none
// of that lives here yet, and no WAL segment is deleted by anything in this package.
package checkpoint

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// FormatVersion is the manifest/checkpoint on-disk format version. Recovery ignores a
// checkpoint whose version it does not understand and falls back to an older
// checkpoint or genesis replay (ADR-0133): pre-1.0 there is no in-place migration.
const FormatVersion uint32 = 1

// magic prefixes every encoded manifest so a stray or foreign file is rejected before
// any field is trusted.
var magic = [4]byte{'A', 'C', 'K', 'P'}

// castagnoli is the CRC-32 table used for the manifest's trailing self-checksum, the
// same polynomial the WAL frames use.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Errors returned by Unmarshal and Validate. They are sentinels so callers (recovery,
// tests) can branch on the failure mode — a corrupt manifest is skipped, not fatal.
var (
	ErrBadMagic           = errors.New("checkpoint: bad manifest magic")
	ErrTruncated          = errors.New("checkpoint: truncated manifest")
	ErrChecksum           = errors.New("checkpoint: manifest checksum mismatch")
	ErrUnsupportedVersion = errors.New("checkpoint: unsupported manifest format version")
	ErrInconsistent       = errors.New("checkpoint: inconsistent manifest fields")
	ErrTooLong            = errors.New("checkpoint: manifest field too long to encode")
)

// maxAtlasVersion bounds the encoded creation-metadata string so a length prefix
// always fits and a malformed manifest cannot claim an absurd length.
const maxAtlasVersion = 255

// DeploymentRef names a deployed definition (its key and version) that a checkpoint's
// replayed events assume is registered. Deployments are durable and reloaded
// independently before replay (ADR-0019); recording them lets recovery detect a
// checkpoint taken against a deployment set that no longer resolves.
type DeploymentRef struct {
	Key     uint64
	Version int32
}

// Manifest is the engine-owned descriptor of a recovery checkpoint (ADR-0133). It is
// self-describing and self-verifying: encoded with a magic prefix, the format
// version, the fields below, and a trailing checksum over the whole body.
type Manifest struct {
	// Partition is the partition this checkpoint belongs to; recovery ignores a
	// checkpoint for a different partition.
	Partition uint16
	// AppliedPosition is the highest log position folded into the checkpoint's state.
	// Recovery replays the WAL strictly after it. The load-bearing field.
	AppliedPosition uint64
	// HighestPosition is the highest log position seen when the checkpoint was taken
	// (>= AppliedPosition); it restores the processor's position without scanning the
	// pruned prefix.
	HighestPosition uint64
	// KeyCounter is the partition key-generator counter at the checkpoint, restored so
	// live keys never collide with replayed ones without scanning the prefix.
	KeyCounter uint64
	// StateChecksum is a checksum over the checkpoint's state-store files, verified at
	// restore so a torn or corrupt snapshot is rejected. It is metadata to this codec
	// (computed by the creation path, a later slice); the codec only round-trips it.
	StateChecksum uint64
	// CreatedUnixNano is the wall-clock creation time (diagnostics only).
	CreatedUnixNano int64
	// AtlasVersion is the Atlas build that wrote the checkpoint (diagnostics only).
	AtlasVersion string
	// Deployments are the (key, version) references the replayed events assume exist.
	Deployments []DeploymentRef
}

// Marshal encodes the manifest into its deterministic on-disk form: magic, format
// version, the fields, and a trailing CRC-32 over everything preceding it. It errors
// only if a field cannot be represented (an over-long string or deployment list).
func (m *Manifest) Marshal() ([]byte, error) {
	if len(m.AtlasVersion) > maxAtlasVersion {
		return nil, fmt.Errorf("%w: atlas version %d bytes", ErrTooLong, len(m.AtlasVersion))
	}

	buf := make([]byte, 0, 64+len(m.AtlasVersion)+len(m.Deployments)*12)
	buf = append(buf, magic[:]...)
	buf = binary.LittleEndian.AppendUint32(buf, FormatVersion)
	buf = binary.LittleEndian.AppendUint16(buf, m.Partition)
	buf = binary.LittleEndian.AppendUint64(buf, m.AppliedPosition)
	buf = binary.LittleEndian.AppendUint64(buf, m.HighestPosition)
	buf = binary.LittleEndian.AppendUint64(buf, m.KeyCounter)
	buf = binary.LittleEndian.AppendUint64(buf, m.StateChecksum)
	buf = binary.LittleEndian.AppendUint64(buf, uint64(m.CreatedUnixNano))
	buf = append(buf, byte(len(m.AtlasVersion)))
	buf = append(buf, m.AtlasVersion...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(m.Deployments)))
	for _, d := range m.Deployments {
		buf = binary.LittleEndian.AppendUint64(buf, d.Key)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(d.Version))
	}
	buf = binary.LittleEndian.AppendUint32(buf, crc32.Checksum(buf, castagnoli))
	return buf, nil
}

// fixedFields is the byte length of the fixed-width prefix: magic(4) + version(4) +
// partition(2) + appliedPos(8) + highestPos(8) + keyCounter(8) + stateChecksum(8) +
// createdUnixNano(8) + atlasVersionLen(1).
const fixedFields = 4 + 4 + 2 + 8 + 8 + 8 + 8 + 8 + 1

// Unmarshal decodes a manifest and verifies its magic, format version, and trailing
// checksum. A malformed, wrong-version, or corrupt manifest returns a sentinel error
// (ErrBadMagic, ErrTruncated, ErrUnsupportedVersion, ErrChecksum) so recovery can skip
// the checkpoint and fall back rather than fail (ADR-0133).
func Unmarshal(b []byte) (*Manifest, error) {
	if len(b) < 4 || [4]byte{b[0], b[1], b[2], b[3]} != magic {
		return nil, ErrBadMagic
	}
	if len(b) < fixedFields+4 { // fixed prefix + at least the deployments count and CRC need more, checked below
		return nil, ErrTruncated
	}
	if v := binary.LittleEndian.Uint32(b[4:]); v != FormatVersion {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedVersion, v)
	}
	// Verify the trailing CRC over the whole body before trusting any length field.
	body, want := b[:len(b)-4], binary.LittleEndian.Uint32(b[len(b)-4:])
	if crc32.Checksum(body, castagnoli) != want {
		return nil, ErrChecksum
	}

	var m Manifest
	off := 8 // past magic + version
	m.Partition = binary.LittleEndian.Uint16(b[off:])
	off += 2
	m.AppliedPosition = binary.LittleEndian.Uint64(b[off:])
	off += 8
	m.HighestPosition = binary.LittleEndian.Uint64(b[off:])
	off += 8
	m.KeyCounter = binary.LittleEndian.Uint64(b[off:])
	off += 8
	m.StateChecksum = binary.LittleEndian.Uint64(b[off:])
	off += 8
	m.CreatedUnixNano = int64(binary.LittleEndian.Uint64(b[off:]))
	off += 8
	vlen := int(b[off])
	off++
	if off+vlen+4 > len(body) { // string + deployments count must fit within the body
		return nil, ErrTruncated
	}
	m.AtlasVersion = string(b[off : off+vlen])
	off += vlen
	n := int(binary.LittleEndian.Uint32(b[off:]))
	off += 4
	if n > 0 {
		if off+n*12 != len(body) {
			return nil, ErrTruncated
		}
		m.Deployments = make([]DeploymentRef, n)
		for i := 0; i < n; i++ {
			m.Deployments[i].Key = binary.LittleEndian.Uint64(b[off:])
			off += 8
			m.Deployments[i].Version = int32(binary.LittleEndian.Uint32(b[off:]))
			off += 4
		}
	} else if off != len(body) {
		return nil, ErrTruncated
	}
	return &m, nil
}

// Validate checks the semantic invariants a usable checkpoint manifest must hold,
// independent of encoding. Recovery calls it after Unmarshal; a failure means skip the
// checkpoint, not crash.
//
// Encodability is deliberately not checked here — that is Marshal's job, and a decoded
// manifest cannot violate it anyway (the version string's length prefix is one byte).
func (m *Manifest) Validate() error {
	if m.HighestPosition < m.AppliedPosition {
		return fmt.Errorf("%w: highest position %d < applied position %d", ErrInconsistent, m.HighestPosition, m.AppliedPosition)
	}
	return nil
}
