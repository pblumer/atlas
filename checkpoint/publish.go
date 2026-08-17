package checkpoint

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ManifestName is the manifest file's name inside a checkpoint directory.
const ManifestName = "manifest"

// tempPrefix marks an in-progress checkpoint directory. A directory carrying it is
// never a published checkpoint: publication is the rename that drops the prefix, so a
// crash at any earlier point leaves only an ignorable temp directory (ADR-0131).
const tempPrefix = "tmp-"

// dirNameWidth zero-pads a checkpoint directory's applied position so lexical order
// matches numeric order and "the newest checkpoint" is a sort away. 20 digits is the
// widest a uint64 can be.
const dirNameWidth = 20

// ErrStateChecksum reports that a published checkpoint's state files no longer match
// the checksum recorded in its manifest — a corrupt or truncated snapshot. Like the
// manifest decode errors it is a signal to skip this checkpoint and fall back, not to
// fail startup.
var ErrStateChecksum = errors.New("checkpoint: state checksum mismatch")

// DirName is the directory name a checkpoint at appliedPos is published under.
func DirName(appliedPos uint64) string {
	return fmt.Sprintf("%0*d", dirNameWidth, appliedPos)
}

// Publish creates a checkpoint under root and publishes it atomically (ADR-0131).
//
// snapshot is called with a fresh, non-existent temporary directory path that it must
// create and populate with the state snapshot (state.Store.Snapshot does exactly
// this). Publish then records a checksum of that content in the manifest, writes and
// fsyncs the manifest, fsyncs the directory, and finally **renames** it to its
// published name and fsyncs root. The rename is the publication point: a crash before
// it leaves only a `tmp-` directory, which List ignores and the next attempt clears,
// so a checkpoint directory is never half-published.
//
// Publishing at a position that is already published is a no-op, so a retry after a
// crash between the rename and the caller's bookkeeping is safe.
//
// m.StateChecksum is filled in by Publish; the caller sets the rest.
func Publish(root string, m *Manifest, snapshot func(dir string) error) (path string, err error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	final := filepath.Join(root, DirName(m.AppliedPosition))
	if _, err := os.Stat(final); err == nil {
		return final, nil // already published at this position
	}

	// Clear any leftover from a previously crashed attempt at this position, then let
	// snapshot create the directory (Pebble's Checkpoint requires a fresh path).
	tmp := filepath.Join(root, tempPrefix+DirName(m.AppliedPosition))
	if err := os.RemoveAll(tmp); err != nil {
		return "", err
	}
	// From here on the attempt is all-or-nothing: any failure removes the temporary
	// directory, so a partially-built checkpoint is never left behind for the next
	// publish (or a human) to mistake for work in progress. Nothing is published until
	// the rename below, so an abandoned attempt is invisible either way.
	defer func() {
		if err != nil {
			_ = os.RemoveAll(tmp)
		}
	}()

	if err = snapshot(tmp); err != nil {
		return "", err
	}
	sum, err := ChecksumDir(tmp)
	if err != nil {
		return "", err
	}
	m.StateChecksum = sum
	blob, err := m.Marshal()
	if err != nil {
		return "", err
	}
	if err = writeFileSync(filepath.Join(tmp, ManifestName), blob); err != nil {
		return "", err
	}
	if err = fsyncDir(tmp); err != nil {
		return "", err
	}
	// The rename publishes the checkpoint; the parent fsync makes the rename itself
	// durable, so a crash right after cannot lose it. If that last fsync fails the
	// checkpoint is already visible but not provably durable, so the error is
	// reported: a retry finds it published and returns success, and if a crash did
	// lose it the retry republishes instead.
	if err = renameDir(tmp, final); err != nil {
		return "", err
	}
	if err = fsyncDir(root); err != nil {
		return "", err
	}
	return final, nil
}

// The durability syscalls Publish depends on, as variables so tests can exercise the
// failure paths a healthy temp directory never produces — an fsync or rename that fails
// must abandon the publish rather than leave a half-visible checkpoint. Production
// always uses the real implementations.
var (
	fsyncDir  = syncDir
	renameDir = os.Rename
)

// List returns the applied positions of the checkpoints published under root, in
// ascending order — so the last element is the newest. Temporary directories from a
// crashed publish, stray files, and unrecognised names are ignored, which is what
// makes an interrupted publish invisible. A missing root is not an error: it just has
// no checkpoints.
func List(root string) ([]uint64, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []uint64
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), tempPrefix) || len(e.Name()) != dirNameWidth {
			continue
		}
		pos, err := strconv.ParseUint(e.Name(), 10, 64)
		if err != nil {
			continue // not a checkpoint directory
		}
		out = append(out, pos)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// Load reads and validates the manifest of the checkpoint published at pos. A
// malformed, corrupt, or wrong-version manifest returns the codec's sentinel error so
// the caller can skip this checkpoint and fall back (ADR-0131).
func Load(root string, pos uint64) (*Manifest, error) {
	blob, err := os.ReadFile(filepath.Join(root, DirName(pos), ManifestName))
	if err != nil {
		return nil, err
	}
	m, err := Unmarshal(blob)
	if err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// Verify loads the manifest at pos and confirms the checkpoint's state files still
// hash to the checksum recorded when it was published, so a corrupt or truncated
// snapshot is rejected (ErrStateChecksum) rather than restored.
func Verify(root string, pos uint64) (*Manifest, error) {
	m, err := Load(root, pos)
	if err != nil {
		return nil, err
	}
	sum, err := ChecksumDir(filepath.Join(root, DirName(pos)))
	if err != nil {
		return nil, err
	}
	if sum != m.StateChecksum {
		return nil, fmt.Errorf("%w: checkpoint %d", ErrStateChecksum, pos)
	}
	return m, nil
}

// Prune deletes all but the newest keep published checkpoints under root, bounding
// the disk a rotating checkpoint schedule uses. keep is clamped to at least one, so
// Prune never removes the only recovery source. Temporary directories left by a
// crashed publish are cleaned up too.
func Prune(root string, keep int) error {
	if keep < 1 {
		keep = 1
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// One pass classifies every entry: published checkpoints by position, and temporary
	// directories from a crashed publish as immediately removable.
	var published []uint64
	var doomed []string
	for _, e := range entries {
		switch {
		case !e.IsDir():
		case strings.HasPrefix(e.Name(), tempPrefix):
			doomed = append(doomed, e.Name())
		default:
			if pos, err := strconv.ParseUint(e.Name(), 10, 64); err == nil && len(e.Name()) == dirNameWidth {
				published = append(published, pos)
			}
		}
	}
	sort.Slice(published, func(i, j int) bool { return published[i] < published[j] })
	if len(published) > keep {
		for _, pos := range published[:len(published)-keep] {
			doomed = append(doomed, DirName(pos))
		}
	}
	for _, name := range doomed {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	return syncDir(root)
}

// ChecksumDir hashes a checkpoint directory's state files — every regular file except
// the manifest, in sorted relative-path order, mixing each path in with its content so
// a rename is caught as well as an edit. It is computed before the manifest is written
// and re-computed by Verify, so both see exactly the same set.
func ChecksumDir(dir string) (uint64, error) {
	var rels []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == ManifestName {
			return nil
		}
		rels = append(rels, rel)
		return nil
	})
	if err != nil {
		return 0, err
	}
	sort.Strings(rels)

	h := fnv.New64a()
	for _, rel := range rels {
		_, _ = h.Write([]byte(rel))
		f, err := os.Open(filepath.Join(dir, rel))
		if err != nil {
			return 0, err
		}
		_, err = io.Copy(h, f)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return 0, err
		}
	}
	return h.Sum64(), nil
}

// writeFileSync writes data to path and fsyncs the file, so its content is durable
// before the directory entry that publishes it is renamed into place.
func writeFileSync(path string, data []byte) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

// syncDir fsyncs a directory so entries created or renamed inside it survive a crash,
// mirroring the WAL's own directory-durability step.
func syncDir(dir string) (err error) {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := d.Close(); err == nil {
			err = cerr
		}
	}()
	return d.Sync()
}
