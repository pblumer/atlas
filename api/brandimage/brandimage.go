// Package brandimage is the one place that decides what counts as a brand mark:
// which formats Atlas accepts, whether a body really is one, and under what
// headers it is handed back to a browser.
//
// There is more than one mark in an installation. ADR-0148 gave the instance its
// own — the operator's, shown on the sign-in screen and in the console. The
// portal added a second kind: a catalogue's, shown to the customer group that
// catalogue is for (ADR-0316). They are stored in
// different places and gated by different rules, and that is as it should be.
//
// What must not differ is the check. An uploaded image is attacker-influenced
// content that this server persists and later serves to every browser that opens
// the page; the validation on the way in and the headers on the way out are the
// whole of the mitigation. Two copies of that pair is one copy that a future
// hardening will miss — so both marks read it from here, and a third will too.
//
// It is deliberately a gate rather than a sanitiser. An SVG is a document format
// with scripting in it, and no byte-level inspection makes one safe; what makes
// it safe is [Serve]'s response — nosniff pins the declared type, and a
// `default-src 'none'; sandbox` policy neutralises script even when the image is
// opened as a top-level document, which is the only context where an SVG's
// script would run at all (an <img> never runs it).
package brandimage

import (
	"bytes"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"
)

// A Set is what one upload surface accepts: which media types, and the extension
// each is stored under.
//
// There is a set per surface and one [Valid] for all of them, and the split is
// the point. Whether bytes really are the type they claim is a security question
// with one answer everywhere — two copies of it is one copy a future hardening
// will miss. *Which* types a surface accepts is a different question, and the
// surfaces genuinely differ: see [Mark] and [Photo].
type Set struct {
	// exts is the stored extensions, ordered. A slice rather than a map so a reader
	// that has to look for "whichever one is there" does it deterministically, and
	// finds the same image on every call.
	exts   []string
	byType map[string]string
	byExt  map[string]string
}

func newSet(byType map[string]string) Set {
	s := Set{byType: byType, byExt: make(map[string]string, len(byType))}
	for ct, ext := range byType {
		s.exts = append(s.exts, ext)
		s.byExt[ext] = ct
	}
	sort.Strings(s.exts)
	return s
}

// Exts is the stored extensions, in a fixed order.
func (s Set) Exts() []string { return s.exts }

// ExtFor is the extension an accepted upload is stored under; ok is false for a
// type this surface does not take, which makes it the acceptance test as well.
func (s Set) ExtFor(contentType string) (string, bool) {
	ext, ok := s.byType[contentType]
	return ext, ok
}

// TypeFor is the media type a stored extension is reported as.
func (s Set) TypeFor(ext string) (string, bool) {
	ct, ok := s.byExt[ext]
	return ct, ok
}

// Mark is what a brand mark may be: a raster PNG or a vector SVG. An instance's
// mark and a catalogue's are both marks, and both take this set.
var Mark = newSet(map[string]string{"image/png": "png", "image/svg+xml": "svg"})

// Photo is what a picture of a person may be: PNG or JPEG.
//
// **Deliberately not SVG**, and that is the one place the two sets differ in
// kind rather than in taste. An SVG is a document with scripting in it; [Serve]
// makes one inert, but a mark has a reason to be a vector — it is drawn, it is
// scaled, a designer delivers one — and a photograph does not. A picture of a
// person arrives from a camera or from a directory, and both give raster bytes.
// Accepting a format nothing needs, in the one place where the uploader is every
// account rather than an administrator, is widening the surface for nothing.
//
// JPEG is here because that is what Microsoft Graph answers with for a user's
// photo. A set that refused it would have made the directory path impossible and
// said nothing about why.
var Photo = newSet(map[string]string{"image/png": "png", "image/jpeg": "jpg"})

// pngMagic is the 8-byte signature every PNG begins with, and jpegMagic the three
// bytes every JPEG does (SOI, then the first marker). Validating the bytes
// server-side — not only the client's Content-Type — means a mislabelled or
// corrupt upload can never be persisted and served on to every browser.
var (
	pngMagic  = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	jpegMagic = []byte{0xff, 0xd8, 0xff}
)

// NormalizeType canonicalises an upload's Content-Type to a bare, lowercase
// media type, dropping any parameters (e.g. "image/svg+xml; charset=utf-8").
func NormalizeType(h string) string {
	if i := strings.IndexByte(h, ';'); i >= 0 {
		h = h[:i]
	}
	return strings.ToLower(strings.TrimSpace(h))
}

// Valid reports whether the bytes look like the media type they were declared
// as: the PNG or JPEG magic for a raster image, well-formed UTF-8 containing an
// "<svg" root for a vector one. An unknown type is never valid.
//
// It answers for every [Set], and knowing a type here does not make a surface
// take it — the surface's own set decides that, and asks this only about what it
// already accepts.
func Valid(contentType string, data []byte) bool {
	switch contentType {
	case "image/png":
		return bytes.HasPrefix(data, pngMagic)
	case "image/jpeg":
		return bytes.HasPrefix(data, jpegMagic)
	case "image/svg+xml":
		return utf8.Valid(data) && bytes.Contains(bytes.ToLower(data), []byte("<svg"))
	}
	return false
}

// Serve writes a stored mark as the response, under the headers that make a
// hostile SVG inert. Callers have already decided who may see it; this decides
// only how it travels.
func Serve(w http.ResponseWriter, contentType string, data []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	// A mark is replaced in place, under an unchanged URL. no-cache lets the
	// browser keep the bytes but makes it ask, so a re-upload is visible on the
	// next load rather than whenever a heuristic expiry runs out.
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
