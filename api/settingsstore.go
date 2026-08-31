package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/connector/ldif"
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

// adMockSetting is the org-wide Active-Directory mockup switch
// (ADR-0193). ADR-0181 decided the switch belongs to the
// operator rather than to the model, and that is unchanged — this only moves where
// the operator reaches it, from the process environment into the Console, because a
// variable set once at start is the wrong ceremony for a thing you flip while trying
// a process out.
//
// Its absence and a stored "off" are different states. No file means the operator has
// not decided here, so whatever ATLAS_AD_MOCK the server was started with keeps
// deciding; a stored record decides, either way. That is what keeps an existing
// installation working exactly as it did until somebody touches the switch.
type adMockSetting struct {
	// Enabled turns the AD worker's mockup mode on.
	Enabled bool `json:"enabled"`

	// Seed is the directory content the mock starts from — the accounts and groups a
	// process expects to find, since a leaver has nothing to disable in an empty
	// forest. It holds the LDIF or DSML *text*, and Atlas owns the file the worker
	// reads it from.
	//
	// It used to hold a path instead, which was wrong twice over. The Console is
	// org-wide while a path belongs to one machine's filesystem, so the field asked an
	// operator to name a file they could not see, complete or verify from where they
	// were typing; and a relative one resolved against the child process's working
	// directory, which is not a thing anybody can predict from a browser. Worse, the
	// field looked like a choice among several directories when there is exactly one
	// (ADR-0202).
	Seed string `json:"seed,omitempty"`
	// SeedName is the file an operator uploaded the seed from. Display only — it says
	// which one is loaded, and never reaches the worker.
	SeedName string `json:"seedName,omitempty"`
	// SeedFormat is ldif or dsml, decided once when the seed was saved rather than at
	// every read, so the worker parses what the Console already validated instead of
	// guessing again from a file extension.
	SeedFormat string `json:"seedFormat,omitempty"`
	// SeedEntries is what the seed parsed to when it was saved, so the Console can say
	// "142 entries" rather than leaving the operator to trust a silent upload.
	SeedEntries int `json:"seedEntries,omitempty"`
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
	adFile  string // admock.json
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
		adFile:  filepath.Join(dir, "admock.json"),
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

// ---------- Org brand logo (ADR-0148) ----------
//
// The org logo is an opaque image (a customer's brand mark) that replaces the
// built-in Atlas mark across the Console and the login screen. It is
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

// clearLogo removes any stored logo, restoring the built-in Atlas mark. A missing
// logo is not an error (idempotent).
func (s *settingsStore) clearLogo() error {
	for _, ext := range logoExts {
		if err := os.Remove(s.logoPath(ext)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("settingsstore: remove logo: %w", err)
		}
	}
	return sidecar.FsyncDir(s.dir)
}

// getADMock returns the stored AD mockup switch and whether a record exists. A
// missing file returns (zero, false, nil): nobody has decided in the Console, so the
// server's own environment still decides.
func (s *settingsStore) getADMock() (adMockSetting, bool, error) {
	data, err := os.ReadFile(s.adFile)
	if err != nil {
		if os.IsNotExist(err) {
			return adMockSetting{}, false, nil
		}
		return adMockSetting{}, false, fmt.Errorf("settingsstore: read ad mock: %w", err)
	}
	var a adMockSetting
	if err := json.Unmarshal(data, &a); err != nil {
		return adMockSetting{}, false, fmt.Errorf("settingsstore: decode ad mock: %w", err)
	}
	return a, true, nil
}

// adSeedPrefix names the files a mock directory's starting entries are written to.
const adSeedPrefix = "admock-seed-"

// adSeedPath is the file a supervised worker reads the seed from. It carries a digest
// of the seed's own content, and that is not about caching: the supervisor restarts a
// child only when its rendered *environment* differs (see supervisor.refresh), so a
// fixed filename would hand an unchanged ATLAS_AD_MOCK_SEED to a worker that then kept
// serving yesterday's directory after an operator replaced the seed. Naming the file
// by its content makes the variable change exactly when the content does.
//
// Empty when the setting carries no seed: there is then nothing to point a worker at.
func (s *settingsStore) adSeedPath(a adMockSetting) string {
	if strings.TrimSpace(a.Seed) == "" {
		return ""
	}
	format := a.SeedFormat
	if format == "" {
		format = ldif.FormatLDIF
	}
	sum := sha256.Sum256([]byte(a.Seed))
	return filepath.Join(s.dir, adSeedPrefix+hex.EncodeToString(sum[:8])+"."+format)
}

// saveADMock writes the switch durably, overwriting any previous value. A stored
// Enabled=false is a real value — the operator turning the mockup off — and not the
// same as no record at all.
//
// The seed is written beside it as the file a supervised worker will read, and every
// earlier one is dropped. Both halves have to land before the record does: the worker
// is restarted off the back of this save, and a record naming a file that is not there
// yet is the outage this design exists to end.
func (s *settingsStore) saveADMock(a adMockSetting) error {
	if err := s.writeADSeed(a); err != nil {
		return err
	}
	return sidecar.WriteJSON(s.dir, s.adFile, a)
}

// writeADSeed puts the seed on disk under its content-addressed name and removes any
// seed file that is not the current one — including all of them when the operator
// cleared the seed. Stale ones are not merely untidy: a worker restarted with an older
// environment would still find one and start from a directory nobody asked for.
func (s *settingsStore) writeADSeed(a adMockSetting) error {
	want := s.adSeedPath(a)
	if want != "" {
		if err := sidecar.WriteFile(s.dir, want, []byte(a.Seed)); err != nil {
			return err
		}
	}
	olds, err := filepath.Glob(filepath.Join(s.dir, adSeedPrefix+"*"))
	if err != nil {
		return fmt.Errorf("settingsstore: list ad mock seeds: %w", err)
	}
	for _, old := range olds {
		if old == want {
			continue
		}
		if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("settingsstore: remove stale ad mock seed: %w", err)
		}
	}
	return sidecar.FsyncDir(s.dir)
}
