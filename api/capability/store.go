package capability

import (
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
)

// The two stores. Both are design-time sidecars (ADR-0019's discipline, through
// [sidecar.Store]): one JSON file per record, written atomically and fsynced, owned
// by the API run loop and doing no locking of their own (I3).
//
// The filename is the key itself rather than a hex encoding of it, which is the one
// place this differs from most of Atlas's stores and is deliberate. The map is meant
// to be readable on disk and in an export: `capabilities/loan-underwriting.json` is
// something a person can find, diff and merge, and `capabilities/6c6f616e2d...json`
// is not. It is safe because [validKey] is exactly the predicate the store uses to
// recognise its own files, so a key that could name a path cannot address a record —
// the sidecar's own guard applies it on the way in, before any file is touched.

// Store holds capabilities.
type Store struct {
	*sidecar.Store[Capability]
}

// NewStore opens the capability directory. Records list by name, then by key, so a
// list reads the way a person would sort it and two capabilities sharing a name still
// come back in a stable order.
func NewStore(dir string) (*Store, error) {
	store, err := sidecar.NewStore(dir, "capabilitystore",
		func(c Capability) string { return c.Key },
		sidecar.Names[Capability](func(key string) string { return key }, validKey),
		sidecar.Order(func(a, b Capability) bool {
			if !strings.EqualFold(a.Name, b.Name) {
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			}
			return a.Key < b.Key
		}),
	)
	if err != nil {
		return nil, err
	}
	return &Store{store}, nil
}

// StreamStore holds value streams.
type StreamStore struct {
	*sidecar.Store[ValueStream]
}

// NewStreamStore opens the value-stream directory, ordered like the capability store.
func NewStreamStore(dir string) (*StreamStore, error) {
	store, err := sidecar.NewStore(dir, "valuestreamstore",
		func(v ValueStream) string { return v.Key },
		sidecar.Names[ValueStream](func(key string) string { return key }, validKey),
		sidecar.Order(func(a, b ValueStream) bool {
			if !strings.EqualFold(a.Name, b.Name) {
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			}
			return a.Key < b.Key
		}),
	)
	if err != nil {
		return nil, err
	}
	return &StreamStore{store}, nil
}
