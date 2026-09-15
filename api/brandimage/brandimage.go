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
	"strings"
	"unicode/utf8"
)

// Exts is the fixed, ordered set of stored extensions. It is a slice rather than
// a map so a reader that has to look for "whichever one is there" does it
// deterministically, and finds the same mark on every call.
var Exts = []string{"png", "svg"}

// ExtByType maps an accepted upload's media type to the extension it is stored
// under; TypeByExt is the reverse, used to report the type on read.
var (
	ExtByType = map[string]string{"image/png": "png", "image/svg+xml": "svg"}
	TypeByExt = map[string]string{"png": "image/png", "svg": "image/svg+xml"}
)

// pngMagic is the 8-byte signature every PNG begins with. Validating the bytes
// server-side — not only the client's Content-Type — means a mislabelled or
// corrupt upload can never be persisted and served on to every browser.
var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// NormalizeType canonicalises an upload's Content-Type to a bare, lowercase
// media type, dropping any parameters (e.g. "image/svg+xml; charset=utf-8").
func NormalizeType(h string) string {
	if i := strings.IndexByte(h, ';'); i >= 0 {
		h = h[:i]
	}
	return strings.ToLower(strings.TrimSpace(h))
}

// Valid reports whether the bytes look like the media type they were declared
// as: the PNG magic for a raster mark, well-formed UTF-8 containing an "<svg"
// root for a vector one. An unknown type is never valid, which is what makes this
// the type check as well as the content check.
func Valid(contentType string, data []byte) bool {
	switch contentType {
	case "image/png":
		return bytes.HasPrefix(data, pngMagic)
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
