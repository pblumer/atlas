package api

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
)

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
		writeError(w, http.StatusInternalServerError, "read theme: "+loadErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handleSetTheme stores the org-wide brand accent. Admin-gated: it changes what
// every user of the instance sees.
func (s *Server) handleSetTheme(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxThemeBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p struct {
		Accent string `json:"accent"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	accent, ok := normalizeAccent(p.Accent)
	if !ok {
		writeError(w, http.StatusBadRequest, `accent must be a hex colour like "#0b5cff"`)
		return
	}
	t := uiTheme{Accent: accent}
	var saveErr error
	s.do(func() { saveErr = s.settings.saveTheme(t) })
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, "save theme: "+saveErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
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
		writeError(w, http.StatusInternalServerError, "clear theme: "+clearErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
