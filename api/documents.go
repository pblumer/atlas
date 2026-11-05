package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/api/docstore"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/connector/s3"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The document store, as the server serves it
// (ADR-draft-a-file-is-a-document-not-a-variable).
//
// A file a process receives — a CSV upload in a task form — is stored once in
// api/docstore, and the variable holds a reference to it. This file is the server's
// half: the routes that upload and download a document, who may read one, the check
// every route that lets a variable value into the engine makes on the references it
// carries, the binding of a document to the instance it entered, and the sweep that
// removes what nobody can reach any more.
//
// None of it is on the processor's path. No event carries a document's bytes, and
// applyToState never opens the store (I4): a reference is plain JSON in a variable.

// DefaultDocumentPendingTTL is how long an uploaded document that no variable has
// named yet is kept: long enough for somebody to pick a file, leave the form open over
// lunch and submit it.
const DefaultDocumentPendingTTL = 24 * time.Hour

// documentsDir is the store's directory under the data directory; storeregistry.go
// classifies it as runtime data a snapshot carries only when asked to.
const documentsDir = "documents"

// documentConfig is how this installation keeps documents.
type documentConfig struct {
	backend    string // "local" (default) or "s3"
	s3Worker   string
	s3Bucket   string
	s3Prefix   string
	pendingTTL time.Duration
}

// WithDocumentBackend chooses where documents' bytes are kept: "local" (the default)
// in the data directory, or "s3" in a bucket reached through the S3 Worker named
// worker. The Worker's credentials stay in the vault; only its name is configured here.
func WithDocumentBackend(backend, worker, bucket, prefix string) Option {
	return func(s *Server) {
		s.docCfg.backend = strings.TrimSpace(backend)
		s.docCfg.s3Worker = strings.TrimSpace(worker)
		s.docCfg.s3Bucket = strings.TrimSpace(bucket)
		s.docCfg.s3Prefix = strings.TrimSpace(prefix)
	}
}

// WithDocumentPendingTTL sets how long an upload no variable names is kept.
func WithDocumentPendingTTL(d time.Duration) Option {
	return func(s *Server) {
		if d > 0 {
			s.docCfg.pendingTTL = d
		}
	}
}

func (s *Server) documentPendingTTL() time.Duration {
	if s.docCfg.pendingTTL > 0 {
		return s.docCfg.pendingTTL
	}
	return DefaultDocumentPendingTTL
}

// ValidateDocumentBackend reports what is wrong with a backend configuration, so the
// command line can refuse it at start rather than at the first upload.
func ValidateDocumentBackend(backend, worker, bucket string) error {
	switch strings.TrimSpace(backend) {
	case "", "local":
		return nil
	case "s3":
		if strings.TrimSpace(worker) == "" || strings.TrimSpace(bucket) == "" {
			return errors.New(`the "s3" document backend needs an S3 Worker name and a bucket`)
		}
		return nil
	default:
		return fmt.Errorf(`unknown document backend %q (want "local" or "s3")`, backend)
	}
}

// documentState is the store, opened on first use: a server that never receives a
// file never creates the directory (storeregistry.go marks it onDemand).
type documentState struct {
	once  sync.Once
	store *docstore.Store
	err   error
}

// documents opens the store on first use.
func (s *Server) documents() (*docstore.Store, error) {
	s.docState.once.Do(func() {
		s.docState.store, s.docState.err = s.openDocuments()
		if s.docState.err != nil {
			logging.Error(logging.DocumentStoreUnavailable, "the document store could not be opened",
				slog.String("error", s.docState.err.Error()))
		}
	})
	return s.docState.store, s.docState.err
}

func (s *Server) openDocuments() (*docstore.Store, error) {
	cfg := s.docCfg
	if err := ValidateDocumentBackend(cfg.backend, cfg.s3Worker, cfg.s3Bucket); err != nil {
		return nil, err
	}
	o := docstore.Options{MaxBytes: s.budgets().Document}
	bucketOf := func() (docstore.Backend, error) {
		return docstore.NewBucket(serverObjects{s}, cfg.s3Worker, cfg.s3Bucket, cfg.s3Prefix, s3.ErrNoSuchDocument)
	}
	if cfg.backend == "s3" {
		b, err := bucketOf()
		if err != nil {
			return nil, err
		}
		o.Default = b
	} else if cfg.s3Worker != "" && cfg.s3Bucket != "" {
		// Configured but not the default: documents written while it was keep readable.
		if b, err := bucketOf(); err == nil {
			o.Others = append(o.Others, b)
		}
	}
	return docstore.Open(filepath.Join(s.dataDir, documentsDir), o)
}

// documentsExist reports whether the store was ever created, so the sweep does not
// create it to find it empty.
func (s *Server) documentsExist() bool {
	_, err := os.Stat(filepath.Join(s.dataDir, documentsDir))
	return err == nil
}

// serverObjects reaches the S3 Worker the store keeps documents with through the
// server's own registry, read at call time: the registry is rebuilt whenever a Worker
// changes, and the store must not hold a stale one.
type serverObjects struct{ s *Server }

func (o serverObjects) PutDocument(ctx context.Context, worker, bucket, key, contentType, disposition string, data []byte, metadata map[string]string) (map[string]any, error) {
	return s3.Documents(o.s.s3Registry).PutDocument(ctx, worker, bucket, key, contentType, disposition, data, metadata)
}

func (o serverObjects) GetDocument(ctx context.Context, worker, bucket, key string) ([]byte, error) {
	return s3.Documents(o.s.s3Registry).GetDocument(ctx, worker, bucket, key)
}

func (o serverObjects) DeleteDocument(ctx context.Context, worker, bucket, key string) error {
	return s3.Documents(o.s.s3Registry).DeleteDocument(ctx, worker, bucket, key)
}

// --- routes -------------------------------------------------------------------

// handleUploadDocument stores one document: the request body is the file, ?name= its
// file name, Content-Type the type the browser claims. It answers the reference a
// variable holds. The body is read before anything touches the run loop (I3).
func (s *Server) handleUploadDocument(w http.ResponseWriter, r *http.Request) {
	docs, err := s.documents()
	if err != nil {
		httpapi.Error(w, http.StatusServiceUnavailable, "the document store is not available: "+err.Error())
		return
	}
	rec, err := docs.Put(r.Context(), r.URL.Query().Get("name"), r.Header.Get("Content-Type"), principalID(r), r.Body)
	switch {
	case errors.Is(err, docstore.ErrTooLarge):
		httpapi.Error(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("the document is larger than the %d bytes a document may be (ATLAS_LIMIT_DOCUMENT)", docs.MaxBytes()))
		return
	case errors.Is(err, docstore.ErrEmpty):
		httpapi.Error(w, http.StatusBadRequest, "the document is empty")
		return
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "store document: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusCreated, rec.Ref())
}

// documentMeta is what GET /api/v1/documents/{id}/meta answers.
type documentMeta struct {
	Reference map[string]any `json:"reference"`
	State     string         `json:"state"` // "pending" or "bound"
	CreatedAt int64          `json:"createdAt"`
	Backend   string         `json:"backend"`
	Available bool           `json:"available"`
}

// readableDocument resolves the {id} of a request to a record the caller may read, or
// writes the refusal. Unknown and not-yours answer alike, so the route is not an
// oracle for which ids exist.
func (s *Server) readableDocument(w http.ResponseWriter, r *http.Request) (*docstore.Store, docstore.Record, bool) {
	docs, err := s.documents()
	if err != nil {
		httpapi.Error(w, http.StatusServiceUnavailable, "the document store is not available: "+err.Error())
		return nil, docstore.Record{}, false
	}
	rec, err := docs.Get(r.PathValue("id"))
	if err == nil {
		ok, aerr := s.mayReadDocument(r, rec)
		if aerr != nil {
			httpapi.Error(w, http.StatusInternalServerError, "check document access: "+aerr.Error())
			return nil, docstore.Record{}, false
		}
		if ok {
			return docs, rec, true
		}
	} else if !errors.Is(err, docstore.ErrNotFound) {
		httpapi.Error(w, http.StatusInternalServerError, "read document: "+err.Error())
		return nil, docstore.Record{}, false
	}
	httpapi.Error(w, http.StatusNotFound, "no document with that id")
	return nil, docstore.Record{}, false
}

func (s *Server) handleDocumentMeta(w http.ResponseWriter, r *http.Request) {
	docs, rec, ok := s.readableDocument(w, r)
	if !ok {
		return
	}
	state := "bound"
	if rec.Pending() {
		state = "pending"
	}
	available, err := docs.Available(r.Context(), rec)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "check document: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, documentMeta{Reference: rec.Ref(), State: state, CreatedAt: rec.CreatedAt,
		Backend: rec.Backend, Available: available})
}

// handleDownloadDocument streams a document's bytes under the type its first bytes
// showed. A type a browser would run as a page is sent as an attachment, and the
// response may not be framed, sniffed or scripted, so a document someone uploaded can
// never act as a page of the Console.
func (s *Server) handleDownloadDocument(w http.ResponseWriter, r *http.Request) {
	docs, rec, ok := s.readableDocument(w, r)
	if !ok {
		return
	}
	rc, err := docs.OpenBytes(r.Context(), rec)
	if errors.Is(err, docstore.ErrUnavailable) {
		// The reference survived and the bytes did not — a restore from a snapshot taken
		// without documents. Gone, not missing: asking again will not help.
		httpapi.Error(w, http.StatusGone, "the document is not available: its bytes are not in this installation's document store")
		return
	}
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "read document: "+err.Error())
		return
	}
	defer rc.Close()
	disposition := "inline"
	if docstore.RendersAsPage(rec.ContentType) || r.URL.Query().Get("download") != "" {
		disposition = "attachment"
	}
	h := w.Header()
	h.Set("Content-Type", rec.ContentType)
	h.Set("Content-Length", strconv.FormatInt(rec.Size, 10))
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": rec.Name}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// --- who may read, and who may use --------------------------------------------

// mayReadDocument decides whether the caller may read a document. With authentication
// off everybody may. Otherwise its uploader may, and for a bound document whoever may
// read one of the instances it entered (ADR-0275): an operator or project member reads
// it whole; a person holding a user task only when a variable that task's form shows
// holds the reference.
func (s *Server) mayReadDocument(r *http.Request, rec docstore.Record) (bool, error) {
	if !s.authEnabled {
		return true, nil
	}
	pr := httpapi.PrincipalFrom(r.Context())
	if pr == nil {
		return false, nil
	}
	if rec.Uploader != "" && rec.Uploader == pr.UserID {
		return true, nil
	}
	for _, root := range rec.BoundTo {
		acc, code, _ := s.instanceAccessFor(r, root)
		if code != 0 {
			continue
		}
		if acc.full {
			return true, nil
		}
		held, err := s.variablesNaming(root, rec.ID)
		if err != nil {
			return false, err
		}
		for _, name := range held {
			if acc.allows(name) {
				return true, nil
			}
		}
	}
	return false, nil
}

// variablesNaming lists the variables of a process instance that hold a reference to
// the document id.
func (s *Server) variablesNaming(instance uint64, id string) ([]string, error) {
	var names []string
	err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		return rv.VariablesOfScope(instance, func(v *model.VariableValue) error {
			if v.Kind != model.VarJSON {
				return nil
			}
			for _, ref := range docstore.FindRefsInJSON(v.Text) {
				if ref == id {
					names = append(names, v.Name)
					break
				}
			}
			return nil
		})
	})
	return names, err
}

// documentRefsOf lists the documents a set of variables names.
func documentRefsOf(vars []model.VariableValue) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range vars {
		if v.Kind != model.VarJSON {
			continue
		}
		for _, id := range docstore.FindRefsInJSON(v.Text) {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// checkDocumentRefs is the check every route makes that lets variable values into the
// engine on somebody's behalf: each document a value names must exist, and the caller
// must be one who may read it — its uploader, or somebody who may read an instance it
// already entered. A reference is a string anybody can type; this is what makes it
// mean no more than the caller could already reach. It runs before the command is
// submitted, and before personal values are sealed, which would hide the reference.
// It answers the ids to bind once the command is durable.
func (s *Server) checkDocumentRefs(r *http.Request, vars []model.VariableValue) ([]string, int, string) {
	ids := documentRefsOf(vars)
	if len(ids) == 0 {
		return nil, 0, ""
	}
	docs, err := s.documents()
	if err != nil {
		return nil, http.StatusServiceUnavailable, "the document store is not available: " + err.Error()
	}
	for _, id := range ids {
		rec, err := docs.Get(id)
		if errors.Is(err, docstore.ErrNotFound) {
			return nil, http.StatusBadRequest, "a variable names document " + id + ", which does not exist"
		}
		if err != nil {
			return nil, http.StatusInternalServerError, "read document: " + err.Error()
		}
		ok, err := s.mayReadDocument(r, rec)
		if err != nil {
			return nil, http.StatusInternalServerError, "check document access: " + err.Error()
		}
		if !ok {
			return nil, http.StatusForbidden, "a variable names document " + id + ", which is not one you may use"
		}
	}
	return ids, 0, ""
}

// refuseDocumentRefs is the answer of a route that lets variables in but cannot bind a
// document to where they land — a published message, whose instance is decided by
// correlation later; a public form, whose visitor uploaded nothing. A reference there
// would name a document nothing keeps, so it is refused rather than accepted and lost.
func refuseDocumentRefs(vars []model.VariableValue) (int, string) {
	if ids := documentRefsOf(vars); len(ids) > 0 {
		return http.StatusBadRequest, "this route does not accept document references (" + ids[0] + "); " +
			"a document enters an instance through a task, a start or a variable change"
	}
	return 0, ""
}

// bindDocuments records that the documents entered the instance's root. It runs after
// the command that carried them is durable (I2). A failure does not fail the request —
// the command is already in the log — and is logged: the document stays readable by its
// uploader until its time to live, and a later write that names it binds it.
func (s *Server) bindDocuments(ids []string, instance uint64) {
	if len(ids) == 0 || instance == 0 {
		return
	}
	docs, err := s.documents()
	if err != nil {
		return
	}
	root := instance
	if err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		var err error
		root, err = rootInstanceOf(rv, instance)
		return err
	}); err != nil {
		logging.Warn(logging.DocumentBindFailed, "could not resolve the root instance documents entered",
			slog.Uint64("instance", instance), slog.String("error", err.Error()))
	}
	for _, id := range ids {
		if err := docs.Bind(id, root); err != nil {
			logging.Warn(logging.DocumentBindFailed, "a document a variable names could not be bound to its instance",
				slog.String("document", id), slog.Uint64("instance", root), slog.String("error", err.Error()))
		}
	}
}

// maxCallDepth bounds the walk from a called instance to its root.
const maxCallDepth = 256

// rootInstanceOf walks a process instance up its call activities to the instance at the
// top. An instance that is not found is its own root.
func rootInstanceOf(rv *state.ReadView, instance uint64) (uint64, error) {
	key := instance
	for i := 0; i < maxCallDepth; i++ {
		pi, ok, err := rv.ProcessInstance(key)
		if err != nil {
			return instance, err
		}
		if !ok || pi.ParentElementInstanceKey == 0 {
			return key, nil
		}
		ei, ok, err := rv.GetElementInstance(pi.ParentElementInstanceKey)
		if err != nil {
			return instance, err
		}
		if !ok || ei.ProcessInstanceKey == 0 || ei.ProcessInstanceKey == key {
			return key, nil
		}
		key = ei.ProcessInstanceKey
	}
	return key, nil
}

// readDocumentBytes reads a whole document for an in-process worker — the CSV import
// parsing a referenced upload. The reference was checked when it entered the instance;
// the worker reads on the instance's behalf.
func (s *Server) readDocumentBytes(id string) ([]byte, error) {
	docs, err := s.documents()
	if err != nil {
		return nil, err
	}
	rec, err := docs.Get(id)
	if err != nil {
		return nil, err
	}
	rc, err := docs.OpenBytes(context.Background(), rec)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, docs.MaxBytes()+1))
}

// --- the sweep ------------------------------------------------------------------

// documentSweepInterval is how often the store is swept. An upload nobody used and a
// document whose instances are gone are found within this of becoming so.
const documentSweepInterval = 15 * time.Minute

// documentSweeper removes documents nobody can reach any more, off the run loop.
func (s *Server) documentSweeper(every time.Duration) {
	defer s.wg.Done()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-t.C:
			s.sweepDocuments()
		}
	}
}

// sweepDocuments runs one sweep: documents whose instances history retention removed,
// uploads past their time to live, and bytes no record names. It asks a read view
// whether an instance exists, live or in history, so a finished instance keeps its
// documents for as long as it keeps its history.
func (s *Server) sweepDocuments() {
	if s.writesPaused() || !s.documentsExist() {
		return
	}
	docs, err := s.documents()
	if err != nil {
		return
	}
	var res docstore.SweepResult
	err = s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		var err error
		res, err = docs.Sweep(context.Background(), s.documentPendingTTL(), func(root uint64) (bool, error) {
			_, ok, err := rv.ProcessInstance(root)
			return ok, err
		})
		return err
	})
	if err != nil {
		if !errors.Is(err, errLoopClosing) {
			logging.Warn(logging.DocumentSweepFailed, "the document sweep stopped part-way",
				slog.String("error", err.Error()))
		}
		return
	}
	if res != (docstore.SweepResult{}) {
		logging.Info(logging.DocumentsSwept, "removed documents nobody can reach any more",
			slog.Int("expired", res.Expired), slog.Int("released", res.Released), slog.Int("blobs", res.Blobs))
	}
}
