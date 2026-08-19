package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pblumer/atlas/api/sidecar"
)

// uiTheme is the org-wide UI theme (ADR-0113): a single brand accent colour the
// whole Console is tinted with. It is design-time configuration — an operator
// preference shared across the instance, not engine state — so it lives in a
// sidecar JSON file, is served to every browser, and is captured by the
// design-time backup. Only the source accent is stored; the hover/soft shades are
// derived in the browser (theme.js), keeping that derivation in exactly one place.
type uiTheme struct {
	// Accent is a normalised "#rrggbb" hex, or "" for the built-in default.
	Accent string `json:"accent"`
}

// registrationSetting is the org-wide self-service-registration configuration
// (ADR-0126): which process the login screen's "Registrieren" link starts. Like
// the theme it is design-time operator configuration, not engine state, so it
// lives in its own sidecar JSON file.
//
// The stored record and its absence are distinct states: no file means "not
// configured — fall back to the built-in default registration process", a stored
// non-empty ProcessID means "use this process", and a stored empty ProcessID means
// "an operator switched registration off". So getRegistration reports whether a
// record exists, letting the handler tell default from off.
type registrationSetting struct {
	// ProcessID is the process whose public start form the login link opens, or ""
	// when an operator has explicitly disabled registration.
	ProcessID string `json:"processId"`
}

// settingsStore persists org-wide UI settings as JSON sidecar files, using the
// same atomic-write + directory-fsync discipline as the other sidecar stores
// (ADR-0019/0041). Each setting is a singleton — one instance-wide record, not a
// set keyed by id — so it exposes plain get/save/clear per setting rather than a
// CRUD-by-id surface. Owned by the run-loop goroutine (accessed through s.do), so
// it needs no locking, and it holds no secret material.
type settingsStore struct {
	dir     string
	file    string // theme.json
	regFile string // registration.json
}

// newSettingsStore opens (creating if needed) the settings directory.
func newSettingsStore(dir string) (*settingsStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("settingsstore: create dir: %w", err)
	}
	return &settingsStore{
		dir:     dir,
		file:    filepath.Join(dir, "theme.json"),
		regFile: filepath.Join(dir, "registration.json"),
	}, nil
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
	return sidecar.WriteJSON(s.dir, s.file, t)
}

// clearTheme removes any stored theme, restoring the built-in default. A missing
// file is not an error (idempotent).
func (s *settingsStore) clearTheme() error {
	if err := os.Remove(s.file); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("settingsstore: remove: %w", err)
	}
	return sidecar.FsyncDir(s.dir)
}

// getRegistration returns the stored registration setting and whether a record
// exists. A missing file returns (zero, false, nil): the caller reads that as "not
// configured" and falls back to the built-in default process.
func (s *settingsStore) getRegistration() (registrationSetting, bool, error) {
	data, err := os.ReadFile(s.regFile)
	if err != nil {
		if os.IsNotExist(err) {
			return registrationSetting{}, false, nil
		}
		return registrationSetting{}, false, fmt.Errorf("settingsstore: read registration: %w", err)
	}
	var r registrationSetting
	if err := json.Unmarshal(data, &r); err != nil {
		return registrationSetting{}, false, fmt.Errorf("settingsstore: decode registration: %w", err)
	}
	return r, true, nil
}

// saveRegistration writes the registration setting durably, overwriting any
// previous value. An empty ProcessID is a valid stored value meaning "disabled".
func (s *settingsStore) saveRegistration(r registrationSetting) error {
	return sidecar.WriteJSON(s.dir, s.regFile, r)
}
