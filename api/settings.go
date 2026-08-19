package api

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
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
	if !s.requireAdmin(w, r) {
		return
	}
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
	if !s.requireAdmin(w, r) {
		return
	}
	var clearErr error
	s.do(func() { clearErr = s.settings.clearTheme() })
	if clearErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "clear theme: "+clearErr.Error())
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
	token, err := newPublicToken()
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
	if !s.requireAdmin(w, r) {
		return
	}
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
	if !s.requireAdmin(w, r) {
		return
	}
	var saveErr error
	s.do(func() { saveErr = s.settings.saveRegistration(registrationSetting{ProcessID: ""}) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "disable registration: "+saveErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
