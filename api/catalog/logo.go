package catalog

import (
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/pblumer/atlas/api/brandimage"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/sidecar"
)

// A catalogue's brand mark (ADR-0316).
//
// The accent and the typeface are two short strings and ride along in the
// catalogue record. A mark cannot: it is up to half a megabyte of opaque bytes,
// and the record is read on the path that resolves a visitor's catalogue on every
// portal load. Carrying the image there would put the bytes into the answer to a
// question nobody asked with them — and into every listing, of which there is one
// per maintenance page.
//
// So the mark is a file beside the stores, named by the catalogue it belongs to.
// There is no flag in the record saying one exists: the file is the fact, and a
// second copy of that fact is a second copy to be wrong after a restore that
// brought the JSON and not the image. The portal asks for the mark and falls back
// when the answer is 404, which is what the console already does with the
// instance's own (logo.js).
//
// Three things are deliberately *not* here. There is no upload page, because the
// theme has none either — an operator brands a catalogue through the API, and a
// screen for it belongs with the rest of catalogue administration rather than
// alone. There is no format conversion or resizing: what was uploaded is what is
// served, because a server that re-encodes somebody's mark is a server that
// decides their brand looks near enough. And there is no third format; PNG and
// SVG are what [brandimage] accepts for the instance, and a catalogue accepting
// more would mean two answers to one question.

// logoPath is where one catalogue's mark is stored. The id is hex-encoded, which
// is the scheme sidecar uses for exactly this reason: a request-supplied key that
// reaches a filename is a path, and hex is the encoding under which "../secret"
// is a file name and not a direction. Ids here always come from a record that was
// already read, so this is the second lock rather than the first.
func (s *Store) logoPath(catalogID, ext string) string {
	return filepath.Join(s.logos, hex.EncodeToString([]byte(catalogID))+"."+ext)
}

// Logo reads a catalogue's mark and the type to serve it as. A catalogue without
// one is not an error — it is the ordinary case, and the caller falls back.
func (s *Store) Logo(catalogID string) (data []byte, contentType string, ok bool, err error) {
	for _, ext := range brandimage.Exts {
		b, readErr := os.ReadFile(s.logoPath(catalogID, ext))
		if readErr == nil {
			return b, brandimage.TypeByExt[ext], true, nil
		}
		if !os.IsNotExist(readErr) {
			return nil, "", false, fmt.Errorf("catalogstore: read logo: %w", readErr)
		}
	}
	return nil, "", false, nil
}

// SaveLogo writes a catalogue's mark durably, then removes any other format the
// catalogue had, so switching from PNG to SVG cannot leave a stale file that
// [Logo] would serve instead.
func (s *Store) SaveLogo(catalogID string, data []byte, contentType string) error {
	ext, ok := brandimage.ExtByType[contentType]
	if !ok {
		return fmt.Errorf("catalogstore: unsupported logo type %q", contentType)
	}
	if err := sidecar.WriteFile(s.logos, s.logoPath(catalogID, ext), data); err != nil {
		return err
	}
	for _, other := range brandimage.Exts {
		if other == ext {
			continue
		}
		if err := os.Remove(s.logoPath(catalogID, other)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("catalogstore: remove stale logo: %w", err)
		}
	}
	return sidecar.FsyncDir(s.logos)
}

// ClearLogo removes a catalogue's mark, whatever format it was in. A catalogue
// that had none is not an error: the caller asked for a state, and it holds.
func (s *Store) ClearLogo(catalogID string) error {
	for _, ext := range brandimage.Exts {
		if err := os.Remove(s.logoPath(catalogID, ext)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("catalogstore: remove logo: %w", err)
		}
	}
	return sidecar.FsyncDir(s.logos)
}

// HandleGetLogo serves a catalogue's brand mark, or 404 when it has none.
//
// Unlike the instance's mark this one is *not* public. ADR-0113's logo is shown
// on the sign-in screen and has to be reachable before anybody is known; a
// catalogue's is shown inside the portal, to the group the catalogue is for. An
// open endpoint here would answer "does catalogue X exist" to anybody who asked,
// which is the catalogue-name oracle the theme record refused to open — and it
// would hand a customer's mark to every other customer on the instance.
//
// So it is the catalogue's own read right, and a catalogue somebody may not read
// answers 404 rather than 403: the two have to be indistinguishable, or the
// status code is the oracle the missing endpoint would have been.
func (s *Service) HandleGetLogo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())

	var (
		data  []byte
		ct    string
		found bool
		has   bool
		opErr error
	)
	s.loop.Do(func() {
		var got Catalog
		if got, found, opErr = s.store.Catalog(id); opErr != nil || !found {
			return
		}
		if found = s.mayRead(got, p); !found {
			return
		}
		data, ct, has, opErr = s.store.Logo(id)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read logo: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no catalogue "+id)
	case !has:
		httpapi.Error(w, http.StatusNotFound, "catalogue "+id+" has no logo")
	default:
		brandimage.Serve(w, ct, data)
	}
}

// HandleSetLogo stores a catalogue's brand mark from the raw request body.
//
// Administration, like the theme (decision 12) and for the same reason, with one
// of its own: this is the only place in the catalogue where a person supplies
// bytes that the server later serves back to a browser. Every check that makes
// that safe is [brandimage]'s, and none of it reaches a lesser role.
//
// The body is read before the loop is entered. Reading a request body is waiting
// on a network, and the single writer must not wait on anybody (invariant I3).
func (s *Service) HandleSetLogo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())

	ct := brandimage.NormalizeType(r.Header.Get("Content-Type"))
	if _, ok := brandimage.ExtByType[ct]; !ok {
		httpapi.Error(w, http.StatusUnsupportedMediaType, "a logo is uploaded as image/png or image/svg+xml")
		return
	}
	// One byte past the budget, so an over-large image is refused rather than
	// truncated into a smaller one that still passes the format check.
	max := s.budgets().Asset
	data, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	case len(data) == 0:
		httpapi.Error(w, http.StatusBadRequest, "empty logo body")
		return
	case int64(len(data)) > max:
		httpapi.Error(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("the logo exceeds the %d byte limit", max))
		return
	case !brandimage.Valid(ct, data):
		httpapi.Error(w, http.StatusBadRequest, "the body is not a valid "+ct+" image")
		return
	}

	var (
		found   bool
		allowed bool
		opErr   error
	)
	s.loop.Do(func() {
		var got Catalog
		if got, found, opErr = s.store.Catalog(id); opErr != nil || !found {
			return
		}
		if found = s.mayRead(got, p); !found {
			return
		}
		if allowed = p != nil && s.admin(p); !allowed {
			return
		}
		opErr = s.store.SaveLogo(id, data, ct)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save logo: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no catalogue "+id)
	case !allowed:
		httpapi.Error(w, http.StatusForbidden, "a catalogue's logo is set by an administrator")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleDeleteLogo removes a catalogue's brand mark, so the portal falls back to
// the operator's. Same gate as setting one: taking a brand away is as much a
// change to what a customer group sees as putting one there.
func (s *Service) HandleDeleteLogo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())

	var (
		found   bool
		allowed bool
		opErr   error
	)
	s.loop.Do(func() {
		var got Catalog
		if got, found, opErr = s.store.Catalog(id); opErr != nil || !found {
			return
		}
		if found = s.mayRead(got, p); !found {
			return
		}
		if allowed = p != nil && s.admin(p); !allowed {
			return
		}
		opErr = s.store.ClearLogo(id)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "clear logo: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no catalogue "+id)
	case !allowed:
		httpapi.Error(w, http.StatusForbidden, "a catalogue's logo is set by an administrator")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
