package api

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// The design-time subtree this backup carries is derived from the store registry
// (storeregistry.go): every store classified design-time, and nothing else. It used
// to be a list here, kept by hand and in a different file from the code creating
// the stores — which is how twelve of them drifted out of the whole-instance
// snapshot without a single test noticing (ADR-0282).
//
// Deliberately absent, and now absent by classification rather than by memory: the
// log and state store (runtime, rebuilt on restart), accounts (identity), and
// anything holding a secret or a credential. A design-time backup is a portable
// file meant to carry models between installations, so a credential riding along
// in it would be a credential leaving the building. The worker store *is* carried
// — it is design-time configuration — but its secrets live in the vault, so a
// restore onto a fresh instance leaves workers needing their credentials again.

// maxRestoreEntries caps how many archive members a restore will process, so an
// archive of a vast number of tiny (or zero-byte) entries cannot spin the
// handler — the byte budget alone would never trip on such an archive.
const maxRestoreEntries = 100_000

// handleBackup streams the design-time data directory as a gzip-compressed tar
// (ADR-0107): one download an operator can archive and later feed to
// POST /api/v1/restore. It is a pure read of on-disk files — the sidecar stores
// write atomically (temp + rename), so a concurrent save is never captured
// half-written — and touches neither the engine nor the run loop. Admin-gated
// when auth is on: a backup is a full dump of every model on the instance.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", backupFilename()))
	if err := streamBackup(w, os.DirFS(s.dataDir)); err != nil {
		// The header (200) is already sent, so a mid-stream failure can only be
		// logged; the truncated archive fails gzip/tar validation on the client
		// rather than masquerading as complete.
		logging.Error(logging.BackupStreamFailed, "design-time backup stream failed after the header was sent",
			slog.String("error", err.Error()))
	}
}

// streamBackup writes the gzip-tar of the allowlisted design-time files in fsys
// to w. Split from handleBackup so the streaming can be exercised against an
// injected filesystem and writer.
func streamBackup(w io.Writer, fsys fs.FS) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	if err := writeBackup(tw, fsys); err != nil {
		return err
	}
	// Closing flushes the tar footer and gzip trailer; a failure there is the same
	// broken-writer condition writeBackup would surface, so report whichever hits.
	return errors.Join(tw.Close(), gz.Close())
}

// writeBackup walks each allowlisted design-time directory in fsys and copies its
// regular files into tw, named relative to the data-dir root so restore places them
// back exactly. A directory that was never created is simply absent and skipped. The
// per-directory walk is shared with the full snapshot (walkDirInto, ADR-0109).
func writeBackup(tw *tar.Writer, fsys fs.FS) error {
	for _, name := range backupDirs() {
		if err := walkDirInto(tw, fsys, name); err != nil {
			return err
		}
	}
	return nil
}

// handleRestore unpacks an uploaded backup archive (the gzip tar from
// handleBackup) back into the data directory (ADR-0107). Only entries inside the
// design-time allowlist are written; a runtime or secret path in a hostile
// archive is skipped, and any traversal outside the data dir is rejected
// outright. Files land atomically; the read-through sidecar stores serve them
// immediately, but deployments are loaded into the engine at startup, so they
// take effect on the next restart. Admin-gated when auth is on: a restore
// overwrites design-time state.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	// Bound the compressed upload; the LimitReader below bounds the decompressed
	// stream so a bomb cannot fill the disk.
	r.Body = http.MaxBytesReader(w, r.Body, s.limits.Archive)
	defer r.Body.Close()
	gz, err := gzip.NewReader(r.Body)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "body is not a gzip stream")
		return
	}
	defer gz.Close()

	tr := tar.NewReader(io.LimitReader(gz, s.limits.Archive))
	restored, entries := 0, 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "corrupt tar archive")
			return
		}
		entries++
		if entries > maxRestoreEntries {
			httpapi.Error(w, http.StatusBadRequest, "archive has too many entries")
			return
		}
		if hdr.Typeflag != tar.TypeReg {
			continue // only regular files carry restorable content
		}
		dest, ok, err := s.restoreDest(hdr.Name)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, err.Error())
			return
		}
		if !ok {
			continue // outside the allowlist — silently ignored
		}
		if err := writeRestoredFile(dest, tr); err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "restore failed: "+err.Error())
			return
		}
		restored++
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{
		"restored":        restored,
		"restartRequired": true,
		"note":            "Design-time artifacts (drafts, projects, forms, …) are live immediately; deployed processes take effect after a server restart.",
	})
}

// restoreDest validates an archive entry name and resolves it to an absolute
// path inside the data dir. It returns ok=false for a well-formed path whose
// top-level directory is outside the allowlist (skip it), and an error for a
// path that is absolute or escapes the data dir (reject the whole archive).
func (s *Server) restoreDest(name string) (dest string, ok bool, err error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("illegal path in archive: %q", name)
	}
	top, rest := clean, ""
	if i := strings.IndexRune(clean, filepath.Separator); i >= 0 {
		top, rest = clean[:i], clean[i:]
	}
	if current, legacy := renamedBackupDirs[top]; legacy {
		top, clean = current, current+rest
	}
	if !allowedBackupDir(top) {
		return "", false, nil
	}
	return filepath.Join(s.dataDir, clean), true, nil
}

// renamedBackupDirs maps a top-level directory an older Atlas wrote into an
// archive to the name that directory goes by now, so a backup taken before a
// rename still restores in full rather than having its members silently skipped
// by the allowlist. Only the directory moved — the records inside are unchanged,
// which is what makes the mapping a pure path rewrite.
//
// The mapping is one-way and applies to the top-level name only: an entry deeper
// in the tree, or one that merely starts with a legacy name, is untouched. An
// archive that somehow carries both names for the same record resolves the way
// two entries for any one path already do — the last one in the archive wins.
var renamedBackupDirs = map[string]string{
	"marketplace": "repository", // the Marketplace area became the Repository
}

// allowedBackupDir reports whether a top-level directory is in the design-time
// allowlist.
func allowedBackupDir(top string) bool {
	for _, d := range backupDirs() {
		if d == top {
			return true
		}
	}
	return false
}

// writeRestoredFile writes r to path via a temp file and rename, creating parent
// directories as needed. The rename makes a restored file appear whole or not at
// all, never half-written, matching the sidecar stores' write discipline.
func writeRestoredFile(path string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// backupFilename is the download's suggested name, stamped with the current time
// so successive backups don't collide in a downloads folder.
func backupFilename() string {
	return "atlas-backup-" + time.Now().UTC().Format("20060102-150405") + ".tar.gz"
}
