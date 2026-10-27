package api

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
)

// A reader for the profiles runtime/pprof writes (ADR-0502).
//
// The summary an agent reads is built from the server's own profile, so the server has to
// read the format it writes: gzip-compressed protobuf, the profile.proto of
// github.com/google/pprof. Only what a summary needs is decoded — the sample types, the
// samples, each location's functions with the ones inlined into it, the function names
// and the duration — and the wire format is read by hand. It is varints and four wire
// types; a few dozen lines here against a module for the whole of pprof.

// errBadProfile is a profile the reader could not follow. The server only ever reads the
// runtime's own output, so this is a bug or a truncated buffer, never somebody's input.
var errBadProfile = errors.New("malformed pprof profile")

// pprofProfile is the part of a profile a summary reads.
type pprofProfile struct {
	sampleTypes   []pprofValueType
	samples       []pprofSample
	locations     map[uint64][]uint64 // location id → function ids, innermost first
	functions     map[uint64]string   // function id → name
	durationNanos int64
}

type pprofValueType struct{ typ, unit string }

// pprofSample is one stack and its values, the leaf at locations[0].
type pprofSample struct {
	locations []uint64
	values    []int64
}

// protoField is one field of a protobuf message: an integer for the varint and fixed
// wire types, the bytes for a length-delimited one.
type protoField struct {
	num int
	typ int
	v   uint64
	b   []byte
}

// protoFields calls fn for every field of a message, in order.
func protoFields(b []byte, fn func(protoField) error) error {
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return errBadProfile
		}
		b = b[n:]
		f := protoField{num: int(tag >> 3), typ: int(tag & 7)}
		switch f.typ {
		case 0: // varint
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return errBadProfile
			}
			f.v, b = v, b[n:]
		case 1: // fixed64
			if len(b) < 8 {
				return errBadProfile
			}
			f.v, b = binary.LittleEndian.Uint64(b), b[8:]
		case 2: // length-delimited
			l, n := binary.Uvarint(b)
			if n <= 0 || l > uint64(len(b)-n) {
				return errBadProfile
			}
			f.b, b = b[n:n+int(l)], b[n+int(l):]
		case 5: // fixed32
			if len(b) < 4 {
				return errBadProfile
			}
			f.v, b = uint64(binary.LittleEndian.Uint32(b)), b[4:]
		default:
			return errBadProfile
		}
		if err := fn(f); err != nil {
			return err
		}
	}
	return nil
}

// varints appends a repeated integer field to dst. The runtime writes a short one as
// separate fields and a longer one packed, so both are read.
func (f protoField) varints(dst []uint64) ([]uint64, error) {
	switch f.typ {
	case 0:
		return append(dst, f.v), nil
	case 2:
		for b := f.b; len(b) > 0; {
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return dst, errBadProfile
			}
			dst, b = append(dst, v), b[n:]
		}
		return dst, nil
	}
	return dst, errBadProfile
}

// parsePprof reads a profile as runtime/pprof writes it, compressed or not.
func parsePprof(data []byte) (*pprofProfile, error) {
	if len(data) > 1 && data[0] == 0x1f && data[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		if data, err = io.ReadAll(zr); err != nil {
			return nil, err
		}
	}
	p := &pprofProfile{locations: map[uint64][]uint64{}, functions: map[uint64]string{}}
	var (
		strs  []string
		types [][2]uint64           // type, unit: string indices
		names = map[uint64]uint64{} // function id → name index
	)
	err := protoFields(data, func(f protoField) error {
		switch f.num {
		case 1: // sample_type
			var t [2]uint64
			types = append(types, t)
			return protoFields(f.b, func(g protoField) error {
				if g.num == 1 || g.num == 2 {
					types[len(types)-1][g.num-1] = g.v
				}
				return nil
			})
		case 2: // sample
			var s pprofSample
			err := protoFields(f.b, func(g protoField) error {
				var err error
				switch g.num {
				case 1:
					s.locations, err = g.varints(s.locations)
				case 2:
					var vs []uint64
					vs, err = g.varints(nil)
					for _, v := range vs {
						s.values = append(s.values, int64(v))
					}
				}
				return err
			})
			p.samples = append(p.samples, s)
			return err
		case 4: // location
			var (
				id  uint64
				fns []uint64
			)
			err := protoFields(f.b, func(g protoField) error {
				switch g.num {
				case 1:
					id = g.v
				case 4: // line: the function, innermost first
					return protoFields(g.b, func(h protoField) error {
						if h.num == 1 {
							fns = append(fns, h.v)
						}
						return nil
					})
				}
				return nil
			})
			p.locations[id] = fns
			return err
		case 5: // function
			var id, name uint64
			err := protoFields(f.b, func(g protoField) error {
				switch g.num {
				case 1:
					id = g.v
				case 2:
					name = g.v
				}
				return nil
			})
			names[id] = name
			return err
		case 6: // string_table
			strs = append(strs, string(f.b))
		case 10: // duration_nanos
			p.durationNanos = int64(f.v)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	str := func(i uint64) string {
		if i < uint64(len(strs)) {
			return strs[i]
		}
		return ""
	}
	for _, t := range types {
		p.sampleTypes = append(p.sampleTypes, pprofValueType{typ: str(t[0]), unit: str(t[1])})
	}
	for id, name := range names {
		p.functions[id] = str(name)
	}
	return p, nil
}

// valueIndex is the position of a sample type among the values, or -1.
func (p *pprofProfile) valueIndex(typ string) int {
	for i, t := range p.sampleTypes {
		if t.typ == typ {
			return i
		}
	}
	return -1
}

// frames returns a sample's stack as function names, the leaf first and the functions
// inlined into a caller before it.
func (p *pprofProfile) frames(s pprofSample) []string {
	var out []string
	for _, loc := range s.locations {
		for _, fn := range p.locations[loc] {
			if name := p.functions[fn]; name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}
