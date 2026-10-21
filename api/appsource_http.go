package api

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/importplan"
	"github.com/pblumer/atlas/logging"
)

// The HTTP face of the curated source layout (ADR-0134 Phase 4).
//
// The tree itself is the deliverable; tar+gzip is only how it travels over one
// request, chosen to match the ADR-0107 backup so an operator meets one archive
// format rather than two. The git slice hands the same []sourceFile to a
// worktree and never touches this file.
//
// Export is a read; import writes into the design-time stores. Neither goes near
// the engine, the event log, or recovery.

// maxSourceEntries caps the number of files in an uploaded archive, bounding the
// work a tar with millions of tiny entries can ask for.
const maxSourceEntries = 2000

// handleExportApplicationSource streams an application's source tree as a
// gzip-compressed tar. Viewer access is enough: this is the same content the
// Modeler already shows whoever may open the application, and ?constants=true adds
// the constants the application holds here, which its viewers may read as well
// (ADR-0453). The records are read on the run loop and the tree rendered off it,
// because naming what the drafts need compiles them.
func (s *Server) handleExportApplicationSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	proj, code, msg := s.authorizeProject(r, id, ScopeRoleViewer)
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	withConstants, err := boolQuery(r.URL.Query().Get("constants"), "constants")
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	var (
		rec     sourceRecords
		loadErr error
	)
	s.do(func() { rec, loadErr = s.loadApplicationSource(proj, withConstants) })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "export source: "+loadErr.Error())
		return
	}
	files, err := rec.tree()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "export source: "+err.Error())
		return
	}
	name := sourceArchiveName(files)
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	if err := writeSourceArchive(w, files); err != nil {
		// The 200 header is already out, so a mid-stream failure can only be logged;
		// the truncated archive fails gzip/tar validation on the client rather than
		// passing for a complete tree (the same posture as the ADR-0107 backup).
		logging.Error(logging.ApplicationSourceStreamFailed, "application source stream failed after the header was sent",
			slog.String("error", err.Error()))
	}
}

// sourceArchiveName derives the download's filename from the manifest's key, so
// the file an operator ends up with is named after the application rather than
// after its random id. The key is known to be a slug, so it is already safe in a
// filename.
func sourceArchiveName(files []sourceFile) string {
	man, _, err := parseSourceTree(files)
	if err != nil || man.Key == "" {
		return "atlas-application-source.tar.gz"
	}
	return man.Key + "-source.tar.gz"
}

// writeSourceArchive writes the tree to w as a gzip-compressed tar.
func writeSourceArchive(w io.Writer, files []sourceFile) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		hdr := &tar.Header{
			Name:     f.Path,
			Mode:     0o644,
			Size:     int64(len(f.Data)),
			Typeflag: tar.TypeReg,
		}
		// No ModTime: the archive of an unchanged application is then byte-identical
		// every time, which is the same determinism the tree itself has.
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(f.Data); err != nil {
			return err
		}
	}
	return errors.Join(tw.Close(), gz.Close())
}

// readSourceArchive reads a gzip tar back into a tree, rejecting anything it
// cannot place: a path that is absolute or escapes the root, a non-regular entry,
// more entries or more bytes than the caps allow.
func (s *Server) readSourceArchive(r io.Reader) ([]sourceFile, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("body is not a gzip stream")
	}
	defer gz.Close()

	tr := tar.NewReader(io.LimitReader(gz, s.budgets().AppBundle))
	var files []sourceFile
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("corrupt tar archive")
		}
		if len(files) >= maxSourceEntries {
			return nil, fmt.Errorf("archive has too many entries")
		}
		if hdr.Typeflag != tar.TypeReg {
			continue // only regular files carry content
		}
		clean, ok := cleanSourcePath(hdr.Name)
		if !ok {
			return nil, fmt.Errorf("illegal path in archive: %q", hdr.Name)
		}
		data, err := io.ReadAll(io.LimitReader(tr, s.budgets().AppBundle))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", clean, err)
		}
		files = append(files, sourceFile{Path: clean, Data: data})
	}
	return files, nil
}

// cleanSourcePath normalizes an archive entry name to a tree path and reports
// whether it is one at all. Nothing here is ever written to disk under this name
// — the tree is a map in memory — but a path that climbs out of the root is a
// sign the archive was not written by an export, and refusing it keeps that
// obvious rather than letting it land as an artifact with a strange name.
func cleanSourcePath(name string) (string, bool) {
	clean := path.Clean(strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "./"))
	if clean == "." || clean == "" || path.IsAbs(clean) ||
		clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

// handleImportApplicationSource reads an uploaded source tree into this server.
// The application it lands in is the one the manifest's portable key names — the
// point of the key being that a repository cloned into a server that has never
// seen the application still knows what it is (ADR-0134).
//
// ?dryRun=true answers the plan and writes nothing. An artifact that differs from
// this server's is written only when ?overwrite= names it, kept when ?keep= names
// it, and stops the import with 409 and the plan when neither does.
//
// The constants a tree carries are planned with the rest and set only for an
// administrator. The answer names what the tree's processes need from this server —
// server constants, application constants, configured workers — and how it answers
// each once the import is in, so whoever imports learns before the first publish what
// is still to be set up here.
//
// The archive is decoded, its decision models and its processes compiled off the run
// loop; resolving the application, authorizing against it, planning, writing and
// answering the needs happen together in one turn of the loop.
func (s *Server) handleImportApplicationSource(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.budgets().AppBundle)
	defer r.Body.Close()

	decide, err := importplan.Parse(r.URL.Query())
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	files, err := s.readSourceArchive(r.Body)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	man, byPath, err := parseSourceTree(files)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.checkSourceModels(r.Context(), man, byPath); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	sources := make([]needSource, 0, len(man.Processes))
	for _, p := range man.Processes {
		sources = append(sources, needSource{ID: p.ID, XML: byPath[p.Path]})
	}
	needs, unread := needsOf(sources)
	offered, err := treeConstants(man, byPath)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	caller := sourceCaller{
		authorize: func(app project) (int, string) {
			// Editor is the bar: an import rewrites the application's artifacts. An
			// application this server has never seen is created instead, which needs no
			// role — it is the same act as creating one through the API.
			code, msg := s.checkProjectRole(r, app, ScopeRoleEditor)
			if code == http.StatusNotFound {
				// The caller has no role at all, so the rule hides the application's
				// existence (ADR-0071). Say it in the terms this request used — it named a
				// key, not an id — without revealing that the key is in fact taken.
				msg = "no application with that key"
			}
			return code, msg
		},
		admin: s.isAdmin(r),
		audit: func(appID string, changes []constantChange) error {
			return s.recordConstantChanges(r, appID, changes)
		},
	}
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		caller.ownerID = p.UserID
	}
	var (
		res      sourceImportResult
		applyErr error
	)
	s.do(func() {
		res, applyErr = s.applySourceTree(man, byPath, caller, decide)
		answer := func(plan *sourceImportResult) {
			plan.Needs = s.answerNeedsOnLoop(plan.ApplicationID, needs, pendingConstants(*plan, offered))
			plan.NeedsUnread = unread
		}
		var undecided sourceUndecided
		switch {
		case applyErr == nil:
			answer(&res)
		case errors.As(applyErr, &undecided):
			answer(&undecided.plan)
			applyErr = undecided
		}
	})
	var (
		refusal   sourceRefusal
		undecided sourceUndecided
	)
	switch {
	case errors.As(applyErr, &refusal):
		httpapi.Error(w, refusal.status, refusal.msg)
	case errors.As(applyErr, &undecided):
		httpapi.JSON(w, http.StatusConflict, map[string]any{
			"error": undecided.Error(), "undecided": undecided.refs, "plan": undecided.plan,
		})
	case applyErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "import source: "+applyErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, res)
	}
}

// pendingConstants is what an import's plan sets that the stores do not hold yet:
// the offered constants whose outcome writes, for a caller who may write them. After
// a real import they are held already, and answering them again changes nothing.
func pendingConstants(plan sourceImportResult, offered []heldConstant) map[string]heldConstant {
	notSet := map[string]bool{}
	for _, name := range plan.ConstantsNotSet {
		notSet[name] = true
	}
	writes := map[string]bool{}
	for _, a := range plan.Artifacts {
		if name, ok := strings.CutPrefix(a.Ref, "constant:"); ok && importplan.Writes(a.Outcome) && !notSet[name] {
			writes[name] = true
		}
	}
	out := map[string]heldConstant{}
	for _, c := range offered {
		if writes[c.Name] {
			out[c.Name] = c
		}
	}
	return out
}
