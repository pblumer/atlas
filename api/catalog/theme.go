package catalog

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// A theme belongs to a catalogue (ADR-draft-portal-theme-per-catalogue).
//
// ADR-0113 stores one accent for the instance, on the grounds that a brand is an
// organisation property rather than a personal preference. That reasoning
// survives ten catalogues; it relocates. An instance serving several customer
// groups has several brands, and the catalogue is what tells them apart.
//
// What is stored is the *source* accent and nothing derived from it. theme.js
// computes the hover and soft shades, and --accent-ink — white or near-black,
// whichever contrasts better against the chosen colour (ADR-0263). Eleven copies
// of that derivation is eleven places for one of them to be wrong, and the one
// that matters most is the one that keeps a button's label readable on a pale
// brand yellow.

// Theme is a catalogue's appearance. An empty field means the instance's own.
type Theme struct {
	// Accent is the source brand colour as #rgb or #rrggbb, lower case.
	Accent string `json:"accent,omitempty"`
	// Typeface names one of [Typefaces]. It is a choice from a list rather than a
	// URL on purpose: a web-font URL would reach a third party on every portal
	// page load, carrying the visitor's address there — an outbound dependency on
	// pages that must render when nothing else is reachable, and a data flow an
	// operator in public administration cannot accept on a customer-facing
	// surface.
	Typeface string `json:"typeface,omitempty"`
}

// Typefaces are the font stacks the binary ships. Each names a fallback, because
// a stack of one is a stack that fails on the machine without that font.
var Typefaces = map[string]string{
	"system":   `system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`,
	"humanist": `"Segoe UI", Candara, Optima, "Trebuchet MS", sans-serif`,
	"serif":    `Georgia, Cambria, "Times New Roman", serif`,
	"mono":     `ui-monospace, "SF Mono", "Cascadia Mono", Menlo, monospace`,
}

// hexColour is the two spellings a browser takes. Anything else would leave the
// page half-branded with no error anywhere.
var hexColour = regexp.MustCompile(`^#([0-9a-f]{3}|[0-9a-f]{6})$`)

// Valid reports whether this theme can be stored, and says why not.
func (t Theme) Valid() error {
	if t.Accent != "" && !hexColour.MatchString(t.Accent) {
		return fmt.Errorf("catalog: %q is not a colour — write it as #rgb or #rrggbb", t.Accent)
	}
	if t.Typeface != "" {
		if _, ok := Typefaces[t.Typeface]; !ok {
			names := make([]string, 0, len(Typefaces))
			for n := range Typefaces {
				names = append(names, n)
			}
			return fmt.Errorf("catalog: %q is not a typeface this server ships; choose one of %s",
				t.Typeface, strings.Join(names, ", "))
		}
	}
	return nil
}

// HandleSetTheme sets or clears a catalogue's appearance.
//
// Administration, not catalogue maintenance (decision 12). That is the smaller
// change: the logo upload keeps the gate it has, and no new file-validation
// surface reaches a lesser role. The cost is that ten brands queue behind one
// administrator — acceptable, because a brand changes rarely and a catalogue's
// contents change weekly.
func (s *Service) HandleSetTheme(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())

	var in Theme
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}
	in.Accent = strings.ToLower(strings.TrimSpace(in.Accent))
	in.Typeface = strings.TrimSpace(in.Typeface)
	if err := in.Valid(); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	var (
		got     Catalog
		found   bool
		allowed bool
		opErr   error
	)
	s.loop.Do(func() {
		if got, found, opErr = s.store.Catalog(id); opErr != nil || !found {
			return
		}
		if found = s.mayRead(got, p); !found {
			return
		}
		if allowed = p != nil && s.admin(p); !allowed {
			return
		}
		got.Theme = in
		got.UpdatedAt = s.now()
		opErr = s.store.SaveCatalog(got)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "set theme: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no catalogue "+id)
	case !allowed:
		httpapi.Error(w, http.StatusForbidden, "a catalogue's appearance is set by an administrator")
	default:
		httpapi.JSON(w, http.StatusOK, got)
	}
}
