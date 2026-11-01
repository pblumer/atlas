package state

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// MIHistoryState is a durable multi-instance lifecycle outcome (ADR-0517).
type MIHistoryState byte

const (
	MIHistoryActive MIHistoryState = 1
	MIHistoryCompleted MIHistoryState = 2
	MIHistoryTerminated MIHistoryState = 3
)

// MIHistoryEntry describes one historical MI body or iteration. A body's
// BodyElementKey equals ElementInstanceKey; an iteration references its own
// body's concrete activation, not merely a BPMN element ID.
type MIHistoryEntry struct {
	ElementInstanceKey uint64
	BodyElementKey uint64
	BodyActivationPosition uint64
	ActivationPosition uint64
	EndPosition uint64
	ProcessDefKey uint64
	ElementID int32
	TokenID uint64
	ParentTokenID uint64
	State MIHistoryState
}

const miHistoryValueVersion byte = 1
const miHistoryValueSize = 46

// appendMIHistoryValue uses the transaction's reusable value buffer; the
// version makes future schema additions independently decodable.
func appendMIHistoryValue(dst []byte, v MIHistoryEntry) []byte {
	dst = append(dst, miHistoryValueVersion)
	dst = appendBE64(dst, v.ProcessDefKey)
	dst = appendBE32(dst, uint32(v.ElementID))
	dst = appendBE64(dst, v.TokenID)
	dst = appendBE64(dst, v.ParentTokenID)
	dst = appendBE64(dst, v.ActivationPosition)
	dst = appendBE64(dst, v.EndPosition)
	return append(dst, byte(v.State))
}

func decodeMIHistoryValue(raw []byte) (MIHistoryEntry, error) {
	if len(raw) != miHistoryValueSize || raw[0] != miHistoryValueVersion {
		return MIHistoryEntry{}, fmt.Errorf("state: invalid MI history value version or size (%d bytes)", len(raw))
	}
	v := MIHistoryEntry{
		ProcessDefKey: binary.BigEndian.Uint64(raw[1:9]),
		ElementID: int32(binary.BigEndian.Uint32(raw[9:13])),
		TokenID: binary.BigEndian.Uint64(raw[13:21]),
		ParentTokenID: binary.BigEndian.Uint64(raw[21:29]),
		ActivationPosition: binary.BigEndian.Uint64(raw[29:37]),
		EndPosition: binary.BigEndian.Uint64(raw[37:45]),
		State: MIHistoryState(raw[45]),
	}
	if v.State < MIHistoryActive || v.State > MIHistoryTerminated {
		return MIHistoryEntry{}, fmt.Errorf("state: invalid MI lifecycle state %d", v.State)
	}
	return v, nil
}

func keyMIBodyByElement(piKey, bodyKey uint64) []byte {
	return appendBE64(appendBE64([]byte{byte(cfMIBodyByElement)}, piKey), bodyKey)
}

func keyMIRoundReference(piKey, roundKey uint64) []byte {
	return keyMIRoundByElement(piKey, roundKey)
}

func miRoundInstancePrefix(piKey uint64) []byte {
	return appendBE64([]byte{byte(cfMIIteration)}, piKey)
}

var errMIPageComplete = errors.New("mi page complete")

func validateMILimit(limit int) error {
	if limit < 1 || limit > 200 {
		return fmt.Errorf("state: MI history page limit %d outside [1, 200]", limit)
	}
	return nil
}

// MIBody gets one concrete body activation via the exact reverse index.
// Both *Store and *ReadView expose this query through their shared queries.
func (q queries) MIBody(piKey, bodyKey uint64) (*MIHistoryEntry, bool, error) {
	ref, ok, err := getCopy(q.r, keyMIBodyByElement(piKey, bodyKey))
	if err != nil || !ok {
		return nil, false, err
	}
	if len(ref) != 8 {
		return nil, false, fmt.Errorf("state: corrupt MI body reference (%d bytes)", len(ref))
	}
	pos := binary.BigEndian.Uint64(ref)
	raw, ok, err := getCopy(q.r, keyMIBody(piKey, pos, bodyKey))
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, errors.New("state: MI body reference points to missing row")
	}
	entry, err := decodeMIHistoryValue(raw)
	if err != nil {
		return nil, false, err
	}
	if entry.ActivationPosition != pos {
		return nil, false, errors.New("state: mismatched MI body activation position")
	}
	entry.ElementInstanceKey, entry.BodyElementKey = bodyKey, bodyKey
	entry.BodyActivationPosition = pos
	return &entry, true, nil
}

// MIIteration finds one exact iteration without scanning any body history.
func (q queries) MIIteration(piKey, roundKey uint64) (*MIHistoryEntry, bool, error) {
	ref, ok, err := getCopy(q.r, keyMIRoundReference(piKey, roundKey))
	if err != nil || !ok {
		return nil, false, err
	}
	if len(ref) != 16 {
		return nil, false, fmt.Errorf("state: corrupt MI iteration reference (%d bytes)", len(ref))
	}
	bodyKey := binary.BigEndian.Uint64(ref[:8])
	bodyPos := binary.BigEndian.Uint64(ref[8:])
	raw, ok, err := getCopy(q.r, keyMIIteration(piKey, bodyKey, bodyPos, roundKey))
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, errors.New("state: MI iteration reference points to missing row")
	}
	entry, err := decodeMIHistoryValue(raw)
	if err != nil {
		return nil, false, err
	}
	entry.ElementInstanceKey, entry.BodyElementKey = roundKey, bodyKey
	entry.BodyActivationPosition = bodyPos
	return &entry, true, nil
}

// MIBodies walks at most limit body entries belonging to a single instance,
// ordered by durable activation position. Read it from a ReadView off the writer.
// A cursor-aware API will supply start keys in the next OHS2 increment.
func (q queries) MIBodies(piKey uint64, limit int, fn func(MIHistoryEntry) error) error {
	if err := validateMILimit(limit); err != nil {
		return err
	}
	count := 0
	err := q.scanPrefix(miBodyPrefix(piKey), func(k, raw []byte) error {
		if count == limit {
			return errMIPageComplete
		}
		if len(k) != 25 {
			return fmt.Errorf("state: corrupt MI body key (%d bytes)", len(k))
		}
		entry, err := decodeMIHistoryValue(raw)
		if err != nil {
			return err
		}
		pos := binary.BigEndian.Uint64(k[9:17])
		if entry.ActivationPosition != pos {
			return errors.New("state: mismatched MI body activation position")
		}
		key := binary.BigEndian.Uint64(k[17:25])
		entry.ElementInstanceKey, entry.BodyElementKey = key, key
		entry.BodyActivationPosition = pos
		count++
		return fn(entry)
	})
	if errors.Is(err, errMIPageComplete) {
		return nil
	}
	return err
}

// MIIterations returns up to limit rounds under one *body activation*, never
// mixing repeated or nested executions of the same BPMN element.
func (q queries) MIIterations(piKey, bodyKey, bodyPosition uint64, limit int, fn func(MIHistoryEntry) error) error {
	if err := validateMILimit(limit); err != nil {
		return err
	}
	count := 0
	err := q.scanPrefix(miIterationPrefix(piKey, bodyKey, bodyPosition), func(k, raw []byte) error {
		if count == limit {
			return errMIPageComplete
		}
		instance, body, pos, roundKey, ok := decodeMIIterationKey(k)
		if !ok || instance != piKey || body != bodyKey || pos != bodyPosition {
			return errors.New("state: corrupt MI iteration key")
		}
		entry, err := decodeMIHistoryValue(raw)
		if err != nil {
			return err
		}
		entry.ElementInstanceKey, entry.BodyElementKey = roundKey, bodyKey
		entry.BodyActivationPosition = bodyPosition
		count++
		return fn(entry)
	})
	if errors.Is(err, errMIPageComplete) {
		return nil
	}
	return err
}
