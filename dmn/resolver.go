package dmn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolver turns a DMN reference handle — the modelRef an Atlas project stores
// (ADR-0024) — into the DMN model XML authored in temis. It is the seam between
// "this project references decision X" and "here is X's model": a filesystem
// folder of temis-exported models today, a temis git repo or service later.
// Implementations must be safe for concurrent use.
type Resolver interface {
	Resolve(ctx context.Context, modelRef string) ([]byte, error)
}

// ErrNotFound is returned by a Resolver when no model matches the handle. It is
// deliberately distinct from an I/O failure so a caller can tell "unresolved
// reference" (a user-fixable modelRef) apart from "the model source is broken"
// (an infrastructure error).
var ErrNotFound = errors.New("dmn: no model matches the reference")

// DirResolver resolves a handle against a directory of DMN files exported from
// temis: modelRef "risk-score" resolves to <Dir>/risk-score.dmn, falling back to
// <Dir>/risk-score.xml. It is the zero-config default source; a temis git or
// service resolver can replace it behind the Resolver interface without touching
// callers.
type DirResolver struct {
	Dir string
}

// safeModelRef rejects a handle that is empty or would escape Dir, so a modelRef
// can never be used to read an arbitrary file (path traversal). A handle is a
// single filename stem: no path separators, no "." / "..".
func safeModelRef(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" || ref == "." || ref == ".." {
		return "", false
	}
	if strings.ContainsAny(ref, `/\`) {
		return "", false
	}
	return ref, true
}

// Resolve reads the model file for a handle. A missing model yields ErrNotFound;
// any other read failure is returned as-is so the caller reports it as an
// infrastructure error, not an unresolved reference.
func (r DirResolver) Resolve(_ context.Context, modelRef string) ([]byte, error) {
	name, ok := safeModelRef(modelRef)
	if !ok {
		return nil, ErrNotFound
	}
	for _, ext := range []string{".dmn", ".xml"} {
		data, err := os.ReadFile(filepath.Join(r.Dir, name+ext))
		if err == nil {
			return data, nil
		}
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("dmn: read model %q: %w", modelRef, err)
		}
	}
	return nil, ErrNotFound
}
