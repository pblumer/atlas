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

// repairFormsSetting binds a repair form to a *connector kind* (ADR-draft-repair-forms-without-authoring).
// A connector task's failure is the same failure in every model that uses that
// integration — a mail connector rejected the recipient, a REST connector got a 4xx — so
// the form for repairing it is worth authoring once rather than copying into every
// process. Keyed by kind ("mail", "rest", …) → form id.
//
// It is operator configuration rather than model: it describes the *integration's*
// failure, not any one model's data, and an operator must be able to change it without
// redeploying anything. So it lives here beside the theme (ADR-0113) and the
// registration process (ADR-0126) — org-wide, design-time, captured by the design-time
// backup, and never in the durable log.
//
// A kind mapped to "" is stored as absent rather than as a binding to nothing: unsetting
// is a delete, so the resolver never has to tell "no form" from "a form called nothing".
type repairFormsSetting struct {
	// ByKind maps a connector kind to the form id shown when a task of that kind parks.
	ByKind map[string]string `json:"byKind"`
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
	rfFile  string // repairforms.json (ADR-draft-repair-forms-without-authoring)
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
		rfFile:  filepath.Join(dir, "repairforms.json"),
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

// ---------- Repair forms per connector kind (ADR-draft-repair-forms-without-authoring) ----------

// getRepairForms returns the connector-kind → form-id bindings. An absent file is not an
// error: it means no kind has one, which is the state every installation starts in.
// Always returns a non-nil map so a caller can read it without a guard.
func (s *settingsStore) getRepairForms() (map[string]string, error) {
	data, err := os.ReadFile(s.rfFile)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("settingsstore: read repair forms: %w", err)
	}
	var r repairFormsSetting
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("settingsstore: decode repair forms: %w", err)
	}
	if r.ByKind == nil {
		r.ByKind = map[string]string{}
	}
	return r.ByKind, nil
}

// saveRepairForms writes the whole binding set durably, overwriting the previous one. A
// kind whose form id is empty is dropped rather than stored: unsetting a binding is a
// delete, so a reader never has to tell "no form" from "a form called nothing".
func (s *settingsStore) saveRepairForms(byKind map[string]string) error {
	clean := make(map[string]string, len(byKind))
	for kind, id := range byKind {
		if kind != "" && id != "" {
			clean[kind] = id
		}
	}
	return sidecar.WriteJSON(s.dir, s.rfFile, repairFormsSetting{ByKind: clean})
}

// ---------- Org brand logo (ADR-0148) ----------
//
// The org logo is an opaque image (a customer's brand mark) that replaces the
// built-in "A" letter mark across the Console and the login screen. It is
// design-time operator configuration like the theme, so it lives in the same
// settings directory (and is therefore captured by the design-time backup) and is
// served to every browser. Only two formats are accepted: PNG for raster marks and
// SVG for vector marks. The stored file's extension is the single source of truth
// for its type, so no metadata sidecar is needed — the logo is stored as exactly
// "logo.png" or "logo.svg".

// logoExts is the fixed, ordered set of logo file extensions. Iterating a slice
// (not the maps below) keeps getLogo deterministic if both files ever coexist.
var logoExts = []string{"png", "svg"}

// logoExtByType maps an accepted upload content type to its stored extension, and
// logoTypeByExt is the reverse used to report the type on read.
var (
	logoExtByType = map[string]string{"image/png": "png", "image/svg+xml": "svg"}
	logoTypeByExt = map[string]string{"png": "image/png", "svg": "image/svg+xml"}
)

func (s *settingsStore) logoPath(ext string) string {
	return filepath.Join(s.dir, "logo."+ext)
}

// getLogo returns the stored brand logo's bytes and content type, and whether one
// is set. A missing logo is not an error — it is the default (the built-in letter
// mark). Only one logo exists at a time; getLogo returns whichever format is on
// disk, checking the fixed order in logoExts.
func (s *settingsStore) getLogo() (data []byte, contentType string, ok bool, err error) {
	for _, ext := range logoExts {
		b, readErr := os.ReadFile(s.logoPath(ext))
		if readErr == nil {
			return b, logoTypeByExt[ext], true, nil
		}
		if !os.IsNotExist(readErr) {
			return nil, "", false, fmt.Errorf("settingsstore: read logo: %w", readErr)
		}
	}
	return nil, "", false, nil
}

// saveLogo writes the logo durably under the extension for contentType, then drops
// any previously-stored other format so a switch (e.g. PNG → SVG) never leaves a
// stale file getLogo could serve. contentType must be one of logoExtByType.
func (s *settingsStore) saveLogo(data []byte, contentType string) error {
	ext, ok := logoExtByType[contentType]
	if !ok {
		return fmt.Errorf("settingsstore: unsupported logo type %q", contentType)
	}
	if err := sidecar.WriteFile(s.dir, s.logoPath(ext), data); err != nil {
		return err
	}
	for _, other := range logoExts {
		if other == ext {
			continue
		}
		if err := os.Remove(s.logoPath(other)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("settingsstore: remove stale logo: %w", err)
		}
	}
	return sidecar.FsyncDir(s.dir)
}

// clearLogo removes any stored logo, restoring the built-in letter mark. A missing
// logo is not an error (idempotent).
func (s *settingsStore) clearLogo() error {
	for _, ext := range logoExts {
		if err := os.Remove(s.logoPath(ext)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("settingsstore: remove logo: %w", err)
		}
	}
	return sidecar.FsyncDir(s.dir)
}
