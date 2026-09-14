package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/pblumer/atlas/dmn"

	"github.com/pblumer/atlas/api/httpapi"
)

// handleUploadDmnModel accepts a DMN model file, validates it compiles, and stores
// it in the local model folder the DirResolver reads, returning the handle a DMN
// reference then points at. This is the "pick a .dmn and use it" path: the author
// uploads a model instead of first placing a file on the server and typing its
// handle. It works only when models are served from a writable local folder; when
// a remote temis service is configured (ServiceResolver) models are managed there,
// so the upload is refused with a clear message.
//
// The body is the raw DMN XML; ?name= carries the source filename, from which a
// safe, unique handle is derived. Compilation happens off the run loop (CPU), and
// the file is created with O_EXCL so concurrent uploads pick distinct handles
// without a lock — writing a model file is not engine state, so it never touches
// the processor.
//
// Writing a model is the act with an audience: a handle is what every reference,
// every application's publish and the business-rule-task picker resolve. So the
// decision editor's Save no longer comes here — it keeps a draft
// (ADR-draft-decision-drafts) — and what does come here says so with ?from=, which
// turns a taken handle into a named refusal instead of a silently suffixed second
// copy (ADR-0222).
func (s *Server) handleUploadDmnModel(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.dmnModelDir()
	if !ok {
		httpapi.Error(w, http.StatusConflict, "DMN models are served by a remote temis service (ATLAS_DMN_RESOLVER_URL); add models there and reference them by name")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().ModelUpload))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "empty DMN model")
		return
	}
	res := s.dmnValidator.ValidateXML(r.Context(), body)
	if !res.Valid {
		httpapi.Error(w, http.StatusBadRequest, "not a valid DMN model: "+res.Message)
		return
	}

	// ?handle= overwrites an existing model in place — the save path of the
	// embedded editor, which edits a model a reference already points at and must
	// keep that same handle so the reference (and any picker selection) stays valid.
	// Absent it, a new upload derives a fresh, collision-free handle from ?name=.
	var final string
	if want := sanitizeHandle(r.URL.Query().Get("handle")); want != "" {
		if err := writeModelInPlace(dir, want, body); err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "store model: "+err.Error())
			return
		}
		final = want
	} else {
		handle := sanitizeHandle(r.URL.Query().Get("name"))
		if handle == "" {
			handle = sanitizeHandle(res.ModelName)
		}
		if handle == "" {
			handle = "model"
		}
		// ADR-0222's rule, opted into the same way it is everywhere else: a caller
		// that says which model its editing session opened (?from=, empty for a
		// decision that has none yet) is telling us it means *this* decision, so the
		// handle it would land on has to be free. Suffixing it to base-2 instead
		// files a second model under a name the author never chose and leaves two
		// decisions in the Explorer with the same display name, told apart only by
		// the handle in the row's subtitle. A caller that omits ?from= — an import, a
		// source-tree apply, an MCP authoring tool — keeps the plain upsert.
		//
		// ?overwrite=true is the author answering "replace it" to that refusal. It is
		// deliberately the same derivation rather than a handle the client computes,
		// so the Console never has to re-implement sanitizeHandle to name the file it
		// is replacing.
		origin, identityAware := modelOrigin(r)
		taken := fileExists(filepath.Join(dir, handle+".dmn"))
		switch {
		case identityAware && taken && origin != handle && r.URL.Query().Get("overwrite") != "true":
			httpapi.Error(w, http.StatusConflict, fmt.Sprintf(
				"a decision model is already stored as %q — rename this decision, or save it over that model deliberately", handle+".dmn"))
			return
		case identityAware && taken:
			// The session's own model, or a replacement the author just chose.
			if err := writeModelInPlace(dir, handle, body); err != nil {
				httpapi.Error(w, http.StatusInternalServerError, "store model: "+err.Error())
				return
			}
			final = handle
		default:
			f, err := writeUniqueModel(dir, handle, body)
			if err != nil {
				httpapi.Error(w, http.StatusInternalServerError, "store model: "+err.Error())
				return
			}
			final = f
		}
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{
		"modelRef":  final,
		"modelName": res.ModelName,
		"decisions": res.Decisions,
	})
}

// dmnModelDir reports the writable local model folder, or false when models come
// from a remote temis service (a ServiceResolver, which Atlas does not write to).
func (s *Server) dmnModelDir() (string, bool) {
	if dr, ok := s.dmnResolver.(dmn.DirResolver); ok {
		return dr.Dir, true
	}
	return "", false
}

// modelOrigin reports the model handle the calling editing session opened, and
// whether the caller passed ?from= at all. Present (even empty) it makes the upload
// identity-aware, which is what turns a taken handle into a refusal rather than a
// silently suffixed copy; absent it keeps the upsert every non-interactive writer
// depends on (ADR-0222).
func modelOrigin(r *http.Request) (string, bool) {
	if !r.URL.Query().Has("from") {
		return "", false
	}
	return sanitizeHandle(r.URL.Query().Get("from")), true
}

// sanitizeHandle turns a filename or model name into a safe DMN reference handle:
// lower-case, the .dmn/.xml extension dropped, every run of non-alphanumeric
// characters collapsed to a single "-", and leading/trailing "-" trimmed. The
// result never contains a path separator or ".", so it passes the resolver's
// traversal guard.
func sanitizeHandle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, ".dmn")
	s = strings.TrimSuffix(s, ".xml")
	var b strings.Builder
	pendingDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pendingDash && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingDash = false
			b.WriteRune(r)
		} else {
			pendingDash = true
		}
	}
	return b.String()
}

// writeUniqueModel stores data as <dir>/<handle>.dmn, choosing a suffixed handle
// (base-2, base-3, …) when one already exists, so an upload never clobbers an
// existing model. It returns the handle actually written. Uploads are rare and
// operator-driven, so a plain "find a free name, then write" is enough — no lock.
func writeUniqueModel(dir, base string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	h := base
	for i := 2; fileExists(filepath.Join(dir, h+".dmn")); i++ {
		h = fmt.Sprintf("%s-%d", base, i)
	}
	if err := os.WriteFile(filepath.Join(dir, h+".dmn"), data, 0o644); err != nil {
		return "", err
	}
	return h, nil
}

// writeModelInPlace overwrites <dir>/<handle>.dmn atomically (temp file + rename),
// so an editor save that replaces an existing model can never leave a torn file a
// later resolve would fail to compile. The handle is already traversal-safe
// (sanitizeHandle), so it names a single file inside dir.
func writeModelInPlace(dir, handle string, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	final := filepath.Join(dir, handle+".dmn")
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// fileExists reports whether a path already names a file (or anything).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
