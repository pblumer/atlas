package docstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/internal/sharedread"
)

// Local keeps documents' bytes in a directory, one file per SHA-256. It is the default
// backend: a bare installation needs nothing else.
type Local struct{ dir string }

// NewLocal opens (creating) the local backend under dir.
func NewLocal(dir string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("docstore: %w", err)
	}
	return &Local{dir: dir}, nil
}

// Name implements Backend.
func (l *Local) Name() string { return "local" }

// Put implements Backend. The upload's temp file is already fsynced; it is renamed into
// place, and the directory is fsynced so the rename is durable. Bytes already held under
// the hash are kept, and the temp file is left for the caller to remove.
func (l *Local) Put(_ context.Context, hash, path string, _ int64, _ string) error {
	if !validHash(hash) {
		return fmt.Errorf("docstore: invalid hash %q", hash)
	}
	dest := filepath.Join(l.dir, hash)
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	if err := os.Rename(path, dest); err != nil {
		return err
	}
	return sidecar.FsyncDir(l.dir)
}

// Open implements Backend.
func (l *Local) Open(_ context.Context, hash string) (io.ReadCloser, error) {
	if !validHash(hash) {
		return nil, os.ErrNotExist
	}
	return sharedread.Open(filepath.Join(l.dir, hash))
}

// Has reports whether the backend holds bytes under hash, without reading them.
func (l *Local) Has(_ context.Context, hash string) (bool, error) {
	if !validHash(hash) {
		return false, nil
	}
	_, err := os.Stat(filepath.Join(l.dir, hash))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Delete implements Backend.
func (l *Local) Delete(_ context.Context, hash string) error {
	if !validHash(hash) {
		return nil
	}
	if err := os.Remove(filepath.Join(l.dir, hash)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Hashes lists the hashes the backend holds, which is what lets a sweep find bytes no
// record names any more.
func (l *Local) Hashes() ([]string, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if validHash(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func validHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	return strings.Trim(h, "0123456789abcdef") == ""
}
