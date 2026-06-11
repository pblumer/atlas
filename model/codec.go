package model

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// codecVersion is the first byte of every encoded record. Bumping it is how the
// log format evolves; readers reject versions they do not understand (ADR-0009).
const codecVersion byte = 1

// HeaderSize is the encoded size of the version byte plus the fixed header.
const HeaderSize = 1 + // version
	8 + // Position
	8 + // SourcePos
	8 + // Key
	8 + // Timestamp
	1 + // RecordType
	1 + // ValueType
	1 + // Intent
	2 // PartitionId

// Header field offsets within an encoded record.
const (
	offPosition    = 1
	offSourcePos   = 9
	offKey         = 17
	offTimestamp   = 25
	offRecordType  = 33
	offValueType   = 34
	offIntent      = 35
	offPartitionId = 36
)

var (
	// ErrShortBuffer is returned when a buffer is too small to hold the record
	// or payload being read.
	ErrShortBuffer = errors.New("model: buffer too short")
	// ErrUnknownVersion is returned when a record's version byte is not one this
	// build can decode.
	ErrUnknownVersion = errors.New("model: unknown codec version")
)

// AppendRecord encodes r and appends it to dst, returning the extended slice.
// Passing a reused buffer (e.g. buf[:0]) makes encoding allocation-free, which
// the processor hot path relies on (invariant I1).
func AppendRecord(dst []byte, r *Record) []byte {
	dst = append(dst, codecVersion)
	dst = binary.LittleEndian.AppendUint64(dst, r.Header.Position)
	dst = binary.LittleEndian.AppendUint64(dst, r.Header.SourcePos)
	dst = binary.LittleEndian.AppendUint64(dst, r.Header.Key)
	dst = binary.LittleEndian.AppendUint64(dst, uint64(r.Header.Timestamp))
	dst = append(dst, byte(r.Header.RecordType), byte(r.Header.ValueType), byte(r.Header.Intent))
	dst = binary.LittleEndian.AppendUint16(dst, r.Header.PartitionId)
	if r.Value != nil {
		dst = r.Value.encode(dst)
	}
	return dst
}

// ReadRecord decodes a single record from the front of src. The payload (if
// any) is chosen by the header's ValueType.
func ReadRecord(src []byte) (Record, error) {
	h, rest, err := readHeader(src)
	if err != nil {
		return Record{}, err
	}
	v := newValue(h.ValueType)
	if v != nil {
		if err := v.decode(rest); err != nil {
			return Record{}, err
		}
	}
	return Record{Header: h, Value: v}, nil
}

func readHeader(src []byte) (RecordHeader, []byte, error) {
	if len(src) < HeaderSize {
		return RecordHeader{}, nil, ErrShortBuffer
	}
	if v := src[0]; v != codecVersion {
		return RecordHeader{}, nil, fmt.Errorf("%w: %d", ErrUnknownVersion, v)
	}
	h := RecordHeader{
		Position:    binary.LittleEndian.Uint64(src[offPosition:]),
		SourcePos:   binary.LittleEndian.Uint64(src[offSourcePos:]),
		Key:         binary.LittleEndian.Uint64(src[offKey:]),
		Timestamp:   int64(binary.LittleEndian.Uint64(src[offTimestamp:])),
		RecordType:  RecordType(src[offRecordType]),
		ValueType:   ValueType(src[offValueType]),
		Intent:      Intent(src[offIntent]),
		PartitionId: binary.LittleEndian.Uint16(src[offPartitionId:]),
	}
	return h, src[HeaderSize:], nil
}

// AppendValue appends v's payload bytes (no record header) to dst and returns
// the extended slice. The state store uses this to persist materialized values;
// the log uses AppendRecord, which frames a full header around the payload.
func AppendValue(dst []byte, v Value) []byte {
	return v.encode(dst)
}

// DecodeValue decodes a payload of the given value type from src into a freshly
// allocated Value. It returns an error if vt has no payload type or src is too
// short for it.
func DecodeValue(vt ValueType, src []byte) (Value, error) {
	v := newValue(vt)
	if v == nil {
		return nil, fmt.Errorf("model: no payload type for %s", vt)
	}
	if err := v.decode(src); err != nil {
		return nil, err
	}
	return v, nil
}
