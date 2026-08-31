package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pblumer/atlas/api/sidecar"
)

// installedTemplate is a repository package a user has installed into this
// server (ADR-0081): the element-template payload (ADR-0027) plus the manifest
// fields describing it.
//
// What an install does *today* is narrower than the payload suggests, and it is
// worth stating here rather than leaving to be discovered: the Modeler does not
// read this store. Its service-task kind picker is SERVICE_TASK_KINDS in
// editor.js, compiled into the binary, and the only reader of
// /repository/installed is the Console gallery, which uses it to mark a package
// "Installed" and to uninstall it. So an install records a choice; it does not
// yet put anything in the palette. Connecting the two is unbuilt and unrecorded —
// ADR-0081's own follow-ups name the remote registry, signing, script sandboxing
// and version upgrades, not this — so it wants a decision before it wants code.
//
// The record is design-time data only — installing writes a template the compiler
// already accepts, never engine state — so, like every sidecar record, it holds no
// secret material (the "no secret travels in a shared artifact" rule of
// ADR-0081/0041/0069).
type installedTemplate struct {
	ID          string          `json:"id"`        // the installed record id (== package id)
	PackageID   string          `json:"packageId"` // the source repository package id
	Version     string          `json:"version"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title"`
	Template    json.RawMessage `json:"template"`
	InstalledAt int64           `json:"installedAt"`
}

// repositoryStore is a durable store for installed repository templates, one
// JSON file per id under a single directory — the same on-disk sidecar approach
// as the connector/project/draft stores (ADR-0019/0034/0041). Like them it is
// owned solely by the server's run-loop goroutine, so it needs no locking, and it
// holds no secret material.
type repositoryStore = sidecar.Store[installedTemplate]

// newRepositoryStore opens (creating if needed) the repository directory.
func newRepositoryStore(dir string) (*repositoryStore, error) {
	return sidecar.NewStore(dir, "repositorystore",
		func(rec installedTemplate) string { return rec.ID },
		sidecar.Order(func(a, b installedTemplate) bool {
			if a.InstalledAt != b.InstalledAt {
				return a.InstalledAt < b.InstalledAt
			}
			return a.ID < b.ID
		}),
	)
}

// migrateRepositoryDir carries an install created before the area was renamed
// from "Marketplace" to "Repository" across to the new directory name: it moves
// <data>/marketplace to <data>/repository. Without it an upgrade would open an
// empty store and the operator's installed templates would sit unreferenced on
// disk under the old name.
//
// It is a one-time, self-disabling move — it acts only when the old directory
// exists and the new one does not, so a fresh install and every start after the
// first are both no-ops. If both exist (a downgrade-then-upgrade, or a hand-made
// directory), the new one is authoritative and the old is left untouched rather
// than clobbering live data. A move that fails is reported, not swallowed:
// starting on an empty repository would look like the templates were lost.
func migrateRepositoryDir(dataDir string) error {
	old := filepath.Join(dataDir, "marketplace")
	current := filepath.Join(dataDir, "repository")
	if _, err := os.Stat(old); err != nil {
		return nil // nothing left under the old name
	}
	if _, err := os.Stat(current); err == nil {
		return nil // already migrated; the current directory wins
	}
	if err := os.Rename(old, current); err != nil {
		return fmt.Errorf("repository: move %s to %s: %w", old, current, err)
	}
	// The rename is only durable once the parent directory entry is fsynced —
	// the same discipline every sidecar write follows (ADR-0019).
	return sidecar.FsyncDir(dataDir)
}
