package api

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pblumer/atlas/checkpoint"

	"github.com/pblumer/atlas/api/httpapi"
)

// A full snapshot (ADR-0109) is the whole-instance backup: unlike the design-time
// backup (ADR-0107), it also captures the WAL — the source of truth, hence every
// running instance — plus the user accounts and the vault key, so a restore
// reconstitutes a complete, immediately-usable engine on another box.
//
// The materialized state store is deliberately NOT in the snapshot: it is derivable
// from the WAL (invariant: state == replay(WAL)), so a restore drops it and lets
// recovery rebuild it. That removes any need to snapshot a consistent WAL+state pair
// or to pause the single writer — the WAL's durable prefix is captured by a plain
// file read (recovery already stops cleanly at a torn tail), and state falls out of
// replay. Because a restore replaces the open WAL and state, it cannot be applied to
// a running engine; it is staged to disk and applied on the next start.
//
// # Compacted logs (ADR-0131)
//
// "state == replay(WAL)" holds only while the WAL still starts at genesis. Once
// compaction deletes the segments a recovery checkpoint makes redundant, an archive of
// `wal/` alone would restore a *silently partial* engine: an instance whose events were
// in the deleted prefix would simply not come back. So the snapshot also carries the
// **newest verified checkpoint**, and ApplyPendingRestore installs it as the state
// store — the missing piece that makes the remaining WAL suffix sufficient again.
//
// This is ADR-0109's own option B, rejected then only for want of a checkpoint API. The
// checkpoint is chosen *before* the WAL is read, so the WAL copy is always a superset of
// what the checkpoint needs, matching the ordering argument for the design-time dirs
// below. Exactly one checkpoint rides along — the archive should not carry every kept
// snapshot of the same store.

// fullBackupDirs is the whole-instance snapshot's directory set: the WAL first (so
// the design-time and secret dirs captured after it are a superset of whatever the
// WAL cut references — a deployment's sidecar is written before the WAL record that
// starts an instance on it), then the design-time allowlist, then the credential
// dirs — user accounts and peer deploy tokens (ADR-0129).
//
// Deploy tokens ride in the snapshot but deliberately *not* in the design-time
// backup (ADR-0107): a backup is a portable file meant to carry your models, and a
// peer's credential is not part of your models. A snapshot, by contrast, exists to
// reconstitute this exact engine elsewhere, which includes who may publish to it.
var fullBackupDirs = func() []string {
	dirs := []string{"wal"}
	dirs = append(dirs, backupDirs...)
	return append(dirs, "users", "deploy-tokens")
}()

// fullBackupFiles are the top-level files (not directories) in the snapshot. The
// vault key is a single file at the data-dir root.
var fullBackupFiles = []string{"vault.key"}

// newestVerifiedCheckpoint returns the archive-relative directory of the newest
// checkpoint under dataDir that passes full verification — manifest *and* state files —
// or "" when there is none. Verification is the whole point here: the archive's
// checkpoint is what stands in for a deleted WAL prefix on the restoring box, so
// shipping one that cannot be checked would trade a loud failure for a silent one.
// An older checkpoint that still verifies is preferred to a newest one that does not.
func newestVerifiedCheckpoint(dataDir string) string {
	root := checkpoint.Dir(dataDir)
	positions, err := checkpoint.List(root)
	if err != nil {
		return "" // no checkpoint root, or an unreadable one: the snapshot is WAL-only
	}
	for i := len(positions) - 1; i >= 0; i-- {
		if _, err := checkpoint.Verify(root, positions[i]); err == nil {
			// fs.FS and tar paths are slash-separated regardless of platform.
			return checkpoint.DirBase + "/" + checkpoint.DirName(positions[i])
		}
	}
	return ""
}

// restorePendingDir is the staging directory a full restore extracts into, under the
// data dir; ApplyPendingRestore moves it into place on the next boot. restoreReady
// is the marker file written last, so an interrupted upload (no marker) is never
// applied.
const (
	restorePendingDir = ".restore-pending"
	restoreReady      = ".restore-ready"
)

// handleBackupFull streams a whole-instance snapshot (ADR-0109) as a gzip tar:
// the WAL (running instances), the design-time data, the user accounts and the
// vault key — everything needed to reconstitute this engine elsewhere, minus the
// derivable state store. Admin-gated when auth is on.
func (s *Server) handleBackupFull(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	// Hold off WAL compaction for the duration (ADR-0131). This is raised *before* the
	// checkpoint below is chosen, which is what makes the guard sound rather than
	// merely likely: a compaction pass that reads zero here is one whose deletion
	// happens before this backup picks its checkpoint, so the suffix that checkpoint
	// needs starts at or above whatever was cut.
	s.backupsInFlight.Add(1)
	defer s.backupsInFlight.Add(-1)

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fullBackupFilename()))
	// Pick the checkpoint before reading the WAL, so the WAL copy is a superset of the
	// segments that checkpoint's suffix needs (see the package comment above).
	if err := streamFullBackup(w, os.DirFS(s.dataDir), newestVerifiedCheckpoint(s.dataDir)); err != nil {
		log.Printf("full backup: streaming failed: %v", err)
	}
}

// streamFullBackup writes the gzip-tar of the whole-instance snapshot in fsys to w.
// checkpointDir, when non-empty, is the archive-relative directory of the one
// checkpoint to carry along (ADR-0131).
func streamFullBackup(w io.Writer, fsys fs.FS, checkpointDir string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	if err := writeFullBackup(tw, fsys, checkpointDir); err != nil {
		return err
	}
	return errors.Join(tw.Close(), gz.Close())
}

// writeFullBackup copies the snapshot's checkpoint (when there is one), its directories
// (WAL leads) and its top-level files into tw.
func writeFullBackup(tw *tar.Writer, fsys fs.FS, checkpointDir string) error {
	if checkpointDir != "" {
		if err := walkDirInto(tw, fsys, checkpointDir); err != nil {
			return err
		}
	}
	for _, name := range fullBackupDirs {
		if err := walkDirInto(tw, fsys, name); err != nil {
			return err
		}
	}
	for _, name := range fullBackupFiles {
		if err := writeFileInto(tw, fsys, name); err != nil {
			return err
		}
	}
	return nil
}

// walkDirInto copies every regular file under the named directory in fsys into tw,
// named relative to the data-dir root. A directory that was never created is simply
// absent and skipped. Shared by the design-time backup (ADR-0107) and the full
// snapshot (ADR-0109).
func walkDirInto(tw *tar.Writer, fsys fs.FS, name string) error {
	return fs.WalkDir(fsys, name, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			if errors.Is(err, fs.ErrNotExist) {
				return nil // a store whose directory was never created
			}
			return err
		case !d.Type().IsRegular(), strings.HasSuffix(path, ".tmp"):
			return nil // directories are implied by their files; skip specials and in-flight temps
		}
		return writeFileInto(tw, fsys, path)
	})
}

// writeFileInto copies one file from fsys into tw. A missing file is skipped (e.g.
// vault.key when the vault is disabled), so the snapshot never fails on an optional
// component's absence.
func writeFileInto(tw *tar.Writer, fsys fs.FS, name string) error {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
		return err
	}
	_, err = tw.Write(data)
	return err
}

// handleRestoreFull stages an uploaded whole-instance snapshot (ADR-0109) for
// application on the next server start. It cannot be applied live: the running
// engine holds the WAL and state open under the single-writer invariant, so the
// archive is unpacked into a staging directory and a marker written last; the boot
// path (ApplyPendingRestore) moves it into place and lets recovery rebuild state
// from the restored WAL. Admin-gated when auth is on.
func (s *Server) handleRestoreFull(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRestoreBytes)
	defer r.Body.Close()

	staging := filepath.Join(s.dataDir, restorePendingDir)
	// Best-effort clear of any stale or partially-uploaded staging before extracting a
	// fresh one; the marker rewrite and per-entry overwrite below reconstitute it, and
	// a genuine removal failure surfaces on the writes that follow.
	_ = os.RemoveAll(staging)
	gz, err := gzip.NewReader(r.Body)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "body is not a gzip stream")
		return
	}
	defer gz.Close()

	tr := tar.NewReader(io.LimitReader(gz, maxRestoreBytes))
	restored, entries := 0, 0
	sawWAL := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			failRestore(w, staging, http.StatusBadRequest, "corrupt tar archive")
			return
		}
		entries++
		if entries > maxRestoreEntries {
			failRestore(w, staging, http.StatusBadRequest, "archive has too many entries")
			return
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		dest, top, ok, err := stageDest(staging, hdr.Name)
		if err != nil {
			failRestore(w, staging, http.StatusBadRequest, err.Error())
			return
		}
		if !ok {
			continue // outside the snapshot allowlist — skip (e.g. a crafted state/ entry)
		}
		if top == "wal" {
			sawWAL = true
		}
		if err := writeRestoredFile(dest, tr); err != nil {
			failRestore(w, staging, http.StatusInternalServerError, "restore failed: "+err.Error())
			return
		}
		restored++
	}
	// A full snapshot always carries the WAL. Refusing an archive without it stops a
	// design-time-only backup (ADR-0107) from being applied here, which would wipe
	// state and running instances while leaving the current WAL in place.
	if !sawWAL {
		failRestore(w, staging, http.StatusBadRequest,
			"archive is not a full snapshot (no WAL); use POST /api/v1/restore for a design-time restore")
		return
	}
	// A staging ALWAYS carries a checkpoint entry, empty when the archive had none
	// (ADR-0131). That makes the apply's rename pass replace the local checkpoint root
	// unconditionally: whatever survives there afterwards came from this archive and so
	// belongs to the restored WAL. Deciding it here rather than in the apply also keeps
	// the apply idempotent — a crash mid-apply re-runs against a staging whose already
	// moved entries are simply absent, with no "was there one?" question left to ask.
	if err := os.MkdirAll(filepath.Join(staging, checkpoint.DirBase), 0o755); err != nil {
		failRestore(w, staging, http.StatusInternalServerError, "restore failed: "+err.Error())
		return
	}
	// The marker is written last: ApplyPendingRestore acts only on a complete staging.
	if err := os.WriteFile(filepath.Join(staging, restoreReady), []byte("1"), 0o600); err != nil {
		failRestore(w, staging, http.StatusInternalServerError, "restore failed: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{
		"restored":        restored,
		"restartRequired": true,
		"note":            "Full snapshot staged. It is applied on the next server restart, which replaces the WAL, running instances, design-time data, users and vault key, then rebuilds state from the restored snapshot's checkpoint and WAL.",
	})
}

// failRestore discards the partial staging and writes an error response.
func failRestore(w http.ResponseWriter, staging string, status int, msg string) {
	_ = os.RemoveAll(staging)
	httpapi.Error(w, status, msg)
}

// stageDest validates a snapshot entry name and resolves it to a path inside the
// staging dir, returning the top-level component for the caller's WAL check. ok is
// false for a well-formed path outside the snapshot allowlist (skip it); an error
// is for an absolute or escaping path (reject the archive).
func stageDest(staging, name string) (dest, top string, ok bool, err error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", false, fmt.Errorf("illegal path in archive: %q", name)
	}
	top = clean
	if i := strings.IndexRune(clean, filepath.Separator); i >= 0 {
		top = clean[:i]
	}
	if !allowedFullEntry(top) {
		return "", top, false, nil
	}
	return filepath.Join(staging, clean), top, true, nil
}

// allowedFullEntry reports whether a top-level name belongs to the whole-instance
// snapshot (a directory or the vault-key file).
func allowedFullEntry(top string) bool {
	if top == checkpoint.DirBase {
		return true // the recovery checkpoint that covers a compacted prefix (ADR-0131)
	}
	for _, d := range fullBackupDirs {
		if d == top {
			return true
		}
	}
	for _, f := range fullBackupFiles {
		if f == top {
			return true
		}
	}
	return false
}

// ApplyPendingRestore applies a staged full-snapshot restore, if one is present and
// complete, into dataDir — replacing each staged top-level entry and dropping the
// materialized state so recovery rebuilds it from the restored WAL. It MUST run
// before the WAL and state stores are opened (the engine holds them under the
// single-writer invariant), i.e. at the very start of a boot. It returns whether it
// applied anything.
//
// Each staged entry is moved (rename) into place and the staging directory removed
// only once every entry is in place. The marker is removed last with the staging, so
// a crash mid-apply re-runs on the next boot: an entry already moved is simply absent
// from the staging and skipped, so the apply is idempotent.
func ApplyPendingRestore(dataDir string) (bool, error) {
	staging := filepath.Join(dataDir, restorePendingDir)
	if _, err := os.Stat(filepath.Join(staging, restoreReady)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No complete staging. Discard any marker-less leftovers from an
			// interrupted upload so they cannot accumulate.
			return false, os.RemoveAll(staging)
		}
		return false, err
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Name() == restoreReady {
			continue
		}
		target := filepath.Join(dataDir, e.Name())
		if err := os.RemoveAll(target); err != nil {
			return false, err
		}
		if err := os.Rename(filepath.Join(staging, e.Name()), target); err != nil {
			return false, err
		}
	}
	// The snapshot omits the derivable state store, so drop any existing state. The
	// rename pass above already replaced the checkpoint root with the archive's own
	// (empty when it carried none), so nothing here can still belong to the log this
	// restore replaced.
	if err := os.RemoveAll(filepath.Join(dataDir, "state")); err != nil {
		return false, err
	}
	// Rebuild the starting point from the archive's checkpoint, when there is one. With
	// a whole log this only saves a replay; with a compacted one (ADR-0131) it is what
	// makes the restore correct at all, since the deleted prefix lives nowhere else.
	if err := installCheckpointState(dataDir); err != nil {
		return false, err
	}
	return true, os.RemoveAll(staging)
}

// installCheckpointState seeds <dataDir>/state from the newest verified checkpoint the
// restore brought along, so recovery replays only the WAL suffix past it. With no
// checkpoint it does nothing and recovery replays the whole log — the pre-ADR-0131
// behaviour, still correct for an uncompacted archive.
//
// Checkpoints present but none verifying is an **error**, not a fallback. Falling back
// would replay whatever WAL the archive holds and boot: fine if the log is whole,
// silently missing every instance below the cut if it is compacted — and a restore
// cannot tell which. Refusing to start is the only answer that is never wrong.
func installCheckpointState(dataDir string) error {
	root := checkpoint.Dir(dataDir)
	// A missing root is not an error to List — it simply has no checkpoints — so only a
	// genuinely unreadable one lands here, and that is worth refusing to boot over: it
	// may be hiding the checkpoint a compacted log needs.
	positions, err := checkpoint.List(root)
	if err != nil {
		return err
	}
	for i := len(positions) - 1; i >= 0; i-- {
		if _, err := checkpoint.Verify(root, positions[i]); err != nil {
			continue
		}
		return installStateFrom(filepath.Join(root, checkpoint.DirName(positions[i])), dataDir)
	}
	if len(positions) > 0 {
		return fmt.Errorf("api: restored snapshot carries %d checkpoint(s) but none verifies; "+
			"refusing to start rather than rebuild state from a possibly compacted WAL", len(positions))
	}
	return nil
}

// installStateFrom copies a checkpoint directory into place as the state store. A
// published checkpoint *is* a complete Pebble directory (its manifest rides along
// harmlessly), so this is a copy, not a conversion.
//
// It copies rather than renames so the checkpoint stays where it is: recovery still
// needs it to seed the highest log position and the key counter, which a compacted
// prefix can no longer supply. The copy lands in a temporary directory and is renamed
// into place, so a crash leaves either the old state or none — never a half-built store
// that Pebble would open.
func installStateFrom(src, dataDir string) error {
	dst := filepath.Join(dataDir, "state")
	tmp := dst + ".restoring"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := copyTree(src, tmp); err != nil {
		return errors.Join(err, os.RemoveAll(tmp))
	}
	return os.Rename(tmp, dst)
}

// copyTree copies the regular files under src into dst, creating directories as it
// goes. Checkpoint directories hold only regular files and directories, so nothing here
// needs to handle links or specials.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, strings.TrimPrefix(p, src))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// fullBackupFilename is the download's suggested name for a whole-instance snapshot.
func fullBackupFilename() string {
	return "atlas-full-snapshot-" + time.Now().UTC().Format("20060102-150405") + ".tar.gz"
}
