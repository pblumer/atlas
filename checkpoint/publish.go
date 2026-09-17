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

// DirBase is the checkpoint root's name inside a data directory. It is exported for
// the whole-instance snapshot, which names archive entries relative to the data dir
// and so needs the name rather than the path (ADR-0109/0131).
const DirBase = "checkpoints"

// Dir is the checkpoint root inside an Atlas data directory, alongside the WAL and the
// state store. The server's checkpoint cadence and the recovery that reads what it
// publishes both resolve the path through this one function, so a checkpoint can never
// be written somewhere recovery does not look (ADR-0131).
func Dir(dataDir string) string { return filepath.Join(dataDir, DirBase) }

// Staged is a checkpoint whose snapshot has been taken but which is not yet
// published. It exists to split the publication into its two very different halves
// (ADR-0131).
//
// Taking the snapshot needs the writer stopped: only between batches do the store's
// applied position and the state it holds agree exactly, which is what makes the
// recorded position describe the snapshotted state (invariant I3). Everything after
// it does not. Once [Stage] returns, the staged directory is a set of hard links to
// immutable SST files under a `tmp-` name nothing else looks at, so checksumming it,
// writing the manifest and renaming it into place can run with the engine free.
//
// The split is not cosmetic. The checksum reads **every byte of the state store**, so
// on a store of any size it is by far the longest step — running it inside the
// single-writer turn stops command processing, and with it every request in the API,
// for as long as the read takes. That is the shape [Prune] was already kept out of the
// writer's way for; this keeps the much larger reader out of it too.
type Staged struct {
	root  string
	tmp   string
	final string
	m     *Manifest
	// published marks a position that already had a checkpoint when it was staged: no
	// snapshot was taken and there is nothing to commit, so Commit answers with the
	// existing path rather than re-reading a state it did not write.
	published bool
}

// AppliedPosition is the log position this checkpoint captures.
func (s *Staged) AppliedPosition() uint64 { return s.m.AppliedPosition }

// Stage snapshots the state under a temporary directory and returns the staged
// checkpoint for [Staged.Commit] to publish.
//
// **It must run on the partition's single-writer goroutine** — that is the whole
// reason it is a separate call. snapshot is given a fresh, non-existent directory
// path that it must create and populate (state.Store.Snapshot does exactly this).
//
// Staging at a position that already has a published checkpoint takes no snapshot at
// all; the returned Staged commits to the existing path.
func Stage(root string, m *Manifest, snapshot func(dir string) error) (*Staged, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	s := &Staged{
		root:  root,
		final: filepath.Join(root, DirName(m.AppliedPosition)),
		tmp:   filepath.Join(root, tempPrefix+DirName(m.AppliedPosition)),
		m:     m,
	}
	if _, err := os.Stat(s.final); err == nil {
		s.published = true
		return s, nil
	}
	// Clear any leftover from a previously crashed attempt at this position, then let
	// snapshot create the directory (Pebble's Checkpoint requires a fresh path).
	if err := os.RemoveAll(s.tmp); err != nil {
		return nil, err
	}
	if err := snapshot(s.tmp); err != nil {
		// Nothing is published until Commit renames, so an abandoned attempt is
		// invisible either way — but it must not be left on disk for the next pass.
		_ = os.RemoveAll(s.tmp)
		return nil, err
	}
	return s, nil
}

// Commit checksums the staged state, records it in the manifest, and **renames** the
// directory to its published name, fsyncing the directory and then root.
//
// It runs with the writer free: everything it touches was fixed by [Stage]. The rename
// is the publication point — a crash before it leaves only a `tmp-` directory, which
// [List] ignores and the next attempt clears, so a checkpoint directory is never
// half-published. Any failure removes the temporary directory rather than leaving a
// partially-built checkpoint for the next publish (or a human) to mistake for work in
// progress.
//
// m.StateChecksum is filled in here; the caller sets the rest.
func (s *Staged) Commit() (path string, err error) {
	if s.published {
		return s.final, nil // already published at this position
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(s.tmp)
		}
	}()
	sum, err := checksumDir(s.tmp)
	if err != nil {
		return "", err
	}
	s.m.StateChecksum = sum
	blob, err := s.m.Marshal()
	if err != nil {
		return "", err
	}
	if err = writeFileSync(filepath.Join(s.tmp, ManifestName), blob); err != nil {
		return "", err
	}
	if err = fsyncDir(s.tmp); err != nil {
		return "", err
	}
	// The rename publishes the checkpoint; the parent fsync makes the rename itself
	// durable, so a crash right after cannot lose it. If that last fsync fails the
	// checkpoint is already visible but not provably durable, so the error is
	// reported: a retry finds it published and returns success, and if a crash did
	// lose it the retry republishes instead.
	if err = renameDir(s.tmp, s.final); err != nil {
		return "", err
	}
	if err = fsyncDir(s.root); err != nil {
		return "", err
	}
	return s.final, nil
}

// Abandon discards a staged checkpoint without publishing it, for a caller that
// staged and then could not go on (a shutdown, a failed pass). It is safe to call on
// a Staged that found its position already published, where it does nothing.
func (s *Staged) Abandon() error {
	if s.published {
		return nil
	}
	return os.RemoveAll(s.tmp)
}

// Publish stages a checkpoint and commits it in one call (ADR-0131).
//
// Both halves run wherever the caller is, so this is for callers that have no writer
// to keep free — tests, and synchronous embedding. **The server does not use it**: it
// stages on the run loop and commits off it, because [Staged.Commit] reads the whole
// state store and would otherwise do so with the engine stopped.
//
// Publishing at a position that is already published is a no-op, so a retry after a
// crash between the rename and the caller's bookkeeping is safe.
//
// m.StateChecksum is filled in by Publish; the caller sets the rest.
func Publish(root string, m *Manifest, snapshot func(dir string) error) (string, error) {
	staged, err := Stage(root, m, snapshot)
	if err != nil {
		return "", err
	}
	return staged.Commit()
}

// The durability syscalls Publish depends on, as variables so tests can exercise the
// failure paths a healthy temp directory never produces — an fsync or rename that fails
// must abandon the publish rather than leave a half-visible checkpoint. Production
// always uses the real implementations.
var (
	fsyncDir  = syncDir
	renameDir = os.Rename
	// checksumDir is the whole-state read Commit does. It is a seam for the same
	// reason as the two above, and for one more: a test can count the calls, which is
	// how "Stage reads nothing, Commit reads everything" is asserted as structure
	// rather than observed as a race.
	checksumDir = ChecksumDir
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
