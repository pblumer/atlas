package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// uiTheme is the org-wide UI theme (ADR-0112): a single brand accent colour the
// whole Console is tinted with. It is design-time configuration — an operator
// preference shared across the instance, not engine state — so it lives in a
// sidecar JSON file, is served to every browser, and is captured by the
// design-time backup. Only the source accent is stored; the hover/soft shades are
// derived in the browser (theme.js), keeping that derivation in exactly one place.
type uiTheme struct {
	// Accent is a normalised "#rrggbb" hex, or "" for the built-in default.
	Accent string `json:"accent"`
}

// settingsStore persists org-wide UI settings as a single JSON file, using the
// same atomic-write + directory-fsync discipline as the other sidecar stores
// (ADR-0019/0041). Unlike them it is a singleton — one instance-wide record, not a
// set keyed by id — so it exposes plain get/save/clear rather than a CRUD-by-id
// surface. Owned by the run-loop goroutine (accessed through s.do), so it needs no
// locking, and it holds no secret material.
type settingsStore struct {
	dir  string
	file string
}

// newSettingsStore opens (creating if needed) the settings directory.
func newSettingsStore(dir string) (*settingsStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("settingsstore: create dir: %w", err)
	}
	return &settingsStore{dir: dir, file: filepath.Join(dir, "theme.json")}, nil
}

// getTheme returns the stored theme, or the zero value (the built-in default
// accent) when none has been set. A missing file is not an error — it is exactly
// the default state.
func (s *settingsStore) getTheme() (uiTheme, error) {
	data, err := os.ReadFile(s.file)
	if err != nil {
		if os.IsNotExist(err) {
			return uiTheme{}, nil
		}
		return uiTheme{}, fmt.Errorf("settingsstore: read: %w", err)
	}
	var t uiTheme
	if err := json.Unmarshal(data, &t); err != nil {
		return uiTheme{}, fmt.Errorf("settingsstore: decode: %w", err)
	}
	return t, nil
}

// saveTheme writes the theme durably (atomic write + directory fsync), overwriting
// any previous value.
func (s *settingsStore) saveTheme(t uiTheme) error {
	return atomicWriteJSON(s.dir, s.file, t)
}

// clearTheme removes any stored theme, restoring the built-in default. A missing
// file is not an error (idempotent).
func (s *settingsStore) clearTheme() error {
	if err := os.Remove(s.file); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("settingsstore: remove: %w", err)
	}
	return fsyncDir(s.dir)
}
