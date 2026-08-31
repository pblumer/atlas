package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pblumer/atlas/api/httpapi"

	"github.com/pblumer/atlas/api/token"
	"github.com/pblumer/atlas/connector/ldif"
)

// defaultRegistrationProcessID is the process the login screen's "Registrieren"
// link starts when an operator has not configured registration (ADR-0126). It is
// Atlas's own bootstrap-deployed intake process (ADR-0122), whose start form
// deliberately carries no role field, so an anonymous registrant can never request
// an elevated account — the admin assigns the role at the approval step.
const defaultRegistrationProcessID = "proc_benutzer_aufnahme"

// registrationConfig is the public shape the login screen reads: whether a
// "Registrieren" link should be shown and, if so, the public URL it points at.
type registrationConfig struct {
	Enabled   bool   `json:"enabled"`
	ProcessID string `json:"processId,omitempty"`
	URL       string `json:"url,omitempty"`
}

// maxThemeBytes bounds the theme request body — it carries a single short colour
// string, so a tiny cap is plenty and fails a bloated body fast.
const maxThemeBytes = 1 << 12

// maxADMockBytes bounds the Active-Directory mockup body, which carries the seed's
// whole text rather than a setting's worth of it
// (ADR-0202).
const maxADMockBytes = 1 << 18 // 256 KiB

// hexColorRe matches a canonical "#rrggbb" colour. Validation lives server-side so
// a malformed value can never be persisted and served to every browser.
var hexColorRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// normalizeAccent validates and canonicalises a brand-accent hex to lowercase
// "#rrggbb". It accepts an optional leading "#" and the 3-digit shorthand,
// mirroring the browser's normalizeHex (theme.js), and returns ok=false for
// anything else.
func normalizeAccent(in string) (string, bool) {
	h := strings.ToLower(strings.TrimSpace(in))
	h = strings.TrimPrefix(h, "#")
	if len(h) == 3 { // #rgb → #rrggbb
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	h = "#" + h
	if !hexColorRe.MatchString(h) {
		return "", false
	}
	return h, true
}

// handleGetTheme returns the org-wide UI theme. It is public (like /info) so the
// login screen and every browser can apply the brand colour before authenticating
// — the accent is display metadata, not a secret.
func (s *Server) handleGetTheme(w http.ResponseWriter, _ *http.Request) {
	var (
		t       uiTheme
		loadErr error
	)
	s.do(func() { t, loadErr = s.settings.getTheme() })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read theme: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, t)
}

// handleSetTheme stores the org-wide brand accent. Admin-gated: it changes what
// every user of the instance sees.
func (s *Server) handleSetTheme(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxThemeBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p struct {
		Accent string `json:"accent"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	accent, ok := normalizeAccent(p.Accent)
	if !ok {
		httpapi.Error(w, http.StatusBadRequest, `accent must be a hex colour like "#0b5cff"`)
		return
	}
	t := uiTheme{Accent: accent}
	var saveErr error
	s.do(func() { saveErr = s.settings.saveTheme(t) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save theme: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, t)
}

// handleDeleteTheme clears the org-wide theme, restoring the built-in default.
// Admin-gated.
func (s *Server) handleDeleteTheme(w http.ResponseWriter, r *http.Request) {
	var clearErr error
	s.do(func() { clearErr = s.settings.clearTheme() })
	if clearErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "clear theme: "+clearErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// maxLogoBytes bounds an uploaded org logo (ADR-0148). A brand mark is small — a
// few KB of PNG or SVG — so a tight cap rejects an oversized or bogus upload fast
// while leaving generous headroom for a high-resolution raster mark.
const maxLogoBytes = 512 << 10 // 512 KiB

// pngMagic is the 8-byte signature every PNG begins with. Validating the bytes
// server-side (not just the client's Content-Type) means a mislabelled or corrupt
// upload can never be persisted and served to every browser as the org logo.
var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// normalizeLogoType canonicalises an upload's Content-Type to a bare, lowercase
// media type, dropping any parameters (e.g. "image/svg+xml; charset=utf-8").
func normalizeLogoType(h string) string {
	if i := strings.IndexByte(h, ';'); i >= 0 {
		h = h[:i]
	}
	return strings.ToLower(strings.TrimSpace(h))
}

// validLogo checks the uploaded bytes actually look like the declared image type:
// the PNG magic for a raster mark, and well-formed UTF-8 containing an "<svg" root
// for a vector one. It is a sanity gate, not a sanitiser — the served response's
// CSP (handleGetLogo) is what neutralises a hostile SVG, so this only rejects
// obvious non-images.
func validLogo(contentType string, data []byte) bool {
	switch contentType {
	case "image/png":
		return bytes.HasPrefix(data, pngMagic)
	case "image/svg+xml":
		return utf8.Valid(data) && bytes.Contains(bytes.ToLower(data), []byte("<svg"))
	}
	return false
}

// handleGetLogo serves the org-wide brand logo image, or 404 when none is set. It
// is public (like the theme) so the login screen and every browser can show the
// customer's mark before authenticating. A stored SVG is attacker-influenced
// content, so the response is locked down: nosniff pins the declared type, and a
// strict CSP with the sandbox directive neutralises any script the SVG carries even
// if it is opened as a top-level document — the app itself only ever renders it via
// <img>, where SVG script never runs anyway.
func (s *Server) handleGetLogo(w http.ResponseWriter, _ *http.Request) {
	var (
		data []byte
		ct   string
		ok   bool
		err  error
	)
	s.do(func() { data, ct, ok, err = s.settings.getLogo() })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read logo: "+err.Error())
		return
	}
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "no org logo set")
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleSetLogo stores the org-wide brand logo. Admin-gated: it changes what every
// user of the instance (and every visitor to the login screen) sees. The image is
// the raw request body; its format comes from the Content-Type header and is
// re-validated against the bytes before anything is persisted.
func (s *Server) handleSetLogo(w http.ResponseWriter, r *http.Request) {
	ct := normalizeLogoType(r.Header.Get("Content-Type"))
	if _, ok := logoExtByType[ct]; !ok {
		httpapi.Error(w, http.StatusUnsupportedMediaType, "logo must be uploaded as image/png or image/svg+xml")
		return
	}
	// Read one byte past the cap so an over-limit body is detected, not silently
	// truncated into a "valid" smaller image.
	data, err := io.ReadAll(io.LimitReader(r.Body, maxLogoBytes+1))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(data) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "empty logo body")
		return
	}
	if len(data) > maxLogoBytes {
		httpapi.Error(w, http.StatusRequestEntityTooLarge, "logo exceeds the 512 KiB limit")
		return
	}
	if !validLogo(ct, data) {
		httpapi.Error(w, http.StatusBadRequest, "body is not a valid "+ct+" image")
		return
	}
	var saveErr error
	s.do(func() { saveErr = s.settings.saveLogo(data, ct) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save logo: "+saveErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteLogo clears the org-wide logo, restoring the built-in Atlas mark.
// Admin-gated.
func (s *Server) handleDeleteLogo(w http.ResponseWriter, r *http.Request) {
	var clearErr error
	s.do(func() { clearErr = s.settings.clearLogo() })
	if clearErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "clear logo: "+clearErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// effectiveRegistrationPID resolves the configured registration process id: the
// stored value when a record exists (an empty string means an operator switched
// registration off), else the built-in default. Must be called on the run-loop
// goroutine (it reads the settings store through s.do at the call site).
func (s *Server) effectiveRegistrationPID() (string, error) {
	r, ok, err := s.settings.getRegistration()
	if err != nil {
		return "", err
	}
	if ok {
		return r.ProcessID, nil // "" here means explicitly disabled
	}
	return defaultRegistrationProcessID, nil
}

// registrationLinkForLocked returns the public-start URL for pid, minting a public
// link (ADR-0029) if the process is deployed with a start form and none exists
// yet. It returns "" (no error) when pid is empty or the process is not a
// publishable public form, so registration silently stays hidden until an operator
// deploys a suitable process. Idempotent: an existing link for pid is reused.
// Runs on the run-loop goroutine (bootstrap or inside s.do): it reads the
// deployment registry and mutates the public-link store directly.
func (s *Server) registrationLinkForLocked(pid string, now int64) (string, error) {
	if pid == "" {
		return "", nil
	}
	d := s.latestDeploymentByProcessID(pid)
	if d == nil || d.cp == nil || !d.cp.IsExecutable() || d.cp.StartFormId() == "" {
		return "", nil
	}
	links, err := s.publicLinks.LoadAll()
	if err != nil {
		return "", err
	}
	for _, l := range links {
		if l.ProcessID == pid {
			return "/public/forms/" + l.Token, nil
		}
	}
	token, err := token.New()
	if err != nil {
		return "", err
	}
	l := publicLink{Token: token, ProcessID: pid, FormID: d.cp.StartFormId(), CreatedAt: now}
	if err := s.publicLinks.Save(l); err != nil {
		return "", err
	}
	return "/public/forms/" + l.Token, nil
}

// ensureRegistrationLink mints the public link for the effective registration
// process at bootstrap, so a fresh instance shows the "Registrieren" link with no
// manual publishing step (ADR-0126). A disabled or not-yet-deployable registration
// is a no-op. Runs on the constructing goroutine before the loop serves, within
// the same single-writer discipline as ensureSystemProcesses.
func (s *Server) ensureRegistrationLink(now int64) error {
	pid, err := s.effectiveRegistrationPID()
	if err != nil {
		return err
	}
	_, err = s.registrationLinkForLocked(pid, now)
	return err
}

// handleGetRegistration reports whether the login screen should offer a
// "Registrieren" link and, if so, the public URL it opens (ADR-0126). It is public
// (like /info and the theme) so the pre-auth login screen can read it, and it is
// read-only: the link is minted at bootstrap or when an admin configures it, never
// on an anonymous GET.
func (s *Server) handleGetRegistration(w http.ResponseWriter, _ *http.Request) {
	var (
		out   registrationConfig
		opErr error
	)
	s.do(func() {
		pid, err := s.effectiveRegistrationPID()
		if err != nil {
			opErr = err
			return
		}
		if pid == "" {
			return // explicitly disabled → enabled:false
		}
		url, err := s.registrationLinkForLocked(pid, time.Now().Unix())
		if err != nil {
			opErr = err
			return
		}
		if url == "" {
			return // process not (yet) publishable → enabled:false
		}
		out = registrationConfig{Enabled: true, ProcessID: pid, URL: url}
	})
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read registration: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleSetRegistration configures the self-service registration process and mints
// its public link (ADR-0126). Admin-gated: it opens an unauthenticated start path
// into a process. Body: {"processId": "..."}; an empty processId switches
// registration off. 400 if the named process is not a publishable public form, so
// an operator gets immediate feedback rather than a silently dead link.
func (s *Server) handleSetRegistration(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxThemeBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p struct {
		ProcessID string `json:"processId"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	pid := strings.TrimSpace(p.ProcessID)
	var (
		out        registrationConfig
		notPublish bool
		opErr      error
	)
	s.do(func() {
		if pid != "" {
			url, err := s.registrationLinkForLocked(pid, time.Now().Unix())
			if err != nil {
				opErr = err
				return
			}
			if url == "" {
				notPublish = true
				return
			}
			out = registrationConfig{Enabled: true, ProcessID: pid, URL: url}
		}
		opErr = s.settings.saveRegistration(registrationSetting{ProcessID: pid})
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save registration: "+opErr.Error())
	case notPublish:
		httpapi.Error(w, http.StatusBadRequest, "process is not deployed with a start form and cannot be published for registration")
	default:
		httpapi.JSON(w, http.StatusOK, out)
	}
}

// handleDeleteRegistration switches self-service registration off by storing an
// empty process id (ADR-0126). Admin-gated. It stores a record rather than
// removing the file, so the "off" choice sticks instead of reverting to the
// built-in default on the next read.
func (s *Server) handleDeleteRegistration(w http.ResponseWriter, r *http.Request) {
	var saveErr error
	s.do(func() { saveErr = s.settings.saveRegistration(registrationSetting{ProcessID: ""}) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "disable registration: "+saveErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- The Active-Directory mockup switch (ADR-0193) ----------

// handleGetADMock reports the org-wide AD mockup switch: whether directory writes are
// simulated in the worker's memory, and the seed file it starts from.
//
// Readable by anyone signed in, because it answers a question every operator watching
// a directory task has — did that account really get created? A run that only looks
// successful is the failure mode this switch exists around, so hiding its state would
// be the wrong secrecy.
func (s *Server) handleGetADMock(w http.ResponseWriter, r *http.Request) {
	var (
		a      adMockSetting
		stored bool
		err    error
	)
	s.do(func() { a, stored, err = s.settings.getADMock() })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read ad mock: "+err.Error())
		return
	}
	// "configured" tells the Console the difference between a decision made here and
	// none at all — without a record the server's own environment decides, and the
	// switch must not claim otherwise.
	// The seed's *content* is admin-only, the rest is not. What the switch is set to
	// answers a question every operator watching a directory task has — did that
	// account really get created? — so hiding it would be the wrong secrecy. The seed
	// is directory data: invented for a mockup, but shaped like a staff list, and
	// there is no reason for everyone signed in to read one.
	seed := ""
	if s.isAdmin(r) {
		seed = a.Seed
	}
	httpapi.JSON(w, http.StatusOK, struct {
		Enabled    bool   `json:"enabled"`
		Seed       string `json:"seed,omitempty"`
		SeedName   string `json:"seedName,omitempty"`
		SeedFormat string `json:"seedFormat,omitempty"`
		Entries    int    `json:"seedEntries,omitempty"`
		HasSeed    bool   `json:"hasSeed"`
		Configured bool   `json:"configured"`
	}{
		Enabled: a.Enabled, Seed: seed, SeedName: a.SeedName, SeedFormat: a.SeedFormat,
		Entries: a.SeedEntries, HasSeed: strings.TrimSpace(a.Seed) != "", Configured: stored,
	})
}

// adSeedFormat decides whether a pasted seed is DSML or LDIF by looking at it rather
// than at a file name, because there no longer is one to look at: an operator pastes
// text or drops a file whose name Atlas keeps only to show back.
//
// The two are not close enough to confuse: DSML is XML, so the first thing that is not
// whitespace, a BOM, or an XML declaration is a '<'. Anything else is LDIF, which is
// the right default in the ambiguous case because it is what a directory exports.
func adSeedFormat(seed string) string {
	t := strings.TrimSpace(strings.TrimPrefix(seed, "\ufeff"))
	if strings.HasPrefix(t, "<") {
		return ldif.FormatDSML
	}
	return ldif.FormatLDIF
}

// handleSetADMock stores the switch and lets the supervised AD worker pick it up.
//
// Admin-gated: it decides whether this instance writes to a real directory. The save
// goes through doAndRefresh, so the worker is restarted with the new environment
// rather than being left holding the old one — which is the whole point of putting
// the switch here instead of on the command line.
func (s *Server) handleSetADMock(w http.ResponseWriter, r *http.Request) {
	// Its own limit, and a MaxBytesReader rather than a LimitReader. The theme's 4 KB
	// is the size of a colour; this body carries a directory. Truncating one silently
	// at 4 KB — which is what a LimitReader does — would have surfaced as "invalid JSON
	// body" on a seed that was perfectly good, which is a maddening thing to debug.
	// MaxBytesReader says the body is too large instead, and 256 KB holds a few thousand
	// entries: past that, the answer is a smaller seed, not a bigger field.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxADMockBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p struct {
		Enabled  bool   `json:"enabled"`
		Seed     string `json:"seed"`
		SeedName string `json:"seedName"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	a := adMockSetting{
		Enabled:  p.Enabled,
		Seed:     strings.TrimSpace(p.Seed),
		SeedName: strings.TrimSpace(p.SeedName),
	}
	// Parse the seed here, where the person who can fix it is waiting for an answer.
	// The worker parses it again at startup and no longer dies if it cannot — but a
	// mock that quietly starts empty is a bad way to learn about a typo, and this is
	// the one moment somebody is looking at the thing they just wrote.
	if a.Seed != "" {
		a.SeedFormat = adSeedFormat(a.Seed)
		entries, err := ldif.Parse(a.SeedFormat, []byte(a.Seed))
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest,
				"the seed is not readable as "+a.SeedFormat+": "+err.Error())
			return
		}
		// No length check here: ldif.Parse already refuses a document that holds no
		// entries, in both formats and with a better message than this layer could give
		// ("the file holds no entries"). A guard for it would be a branch nothing can
		// reach.
		a.SeedEntries = len(entries)
	}
	var saveErr error
	s.doAndRefresh(func() { saveErr = s.settings.saveADMock(a) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save ad mock: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, struct {
		Enabled    bool   `json:"enabled"`
		SeedName   string `json:"seedName,omitempty"`
		SeedFormat string `json:"seedFormat,omitempty"`
		Entries    int    `json:"seedEntries,omitempty"`
		HasSeed    bool   `json:"hasSeed"`
	}{
		Enabled: a.Enabled, SeedName: a.SeedName, SeedFormat: a.SeedFormat,
		Entries: a.SeedEntries, HasSeed: a.Seed != "",
	})
}
