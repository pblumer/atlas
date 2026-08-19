// Package processdoc serves process documentation (ADR-0143): a published BPMN
// process as a stored PDF plus the element prose it describes, its version
// history, and the revocable public link a reader without an account follows.
//
// It is the first area carved out of the api server object under ADR-0147, and it
// is what a per-area service is meant to look like. Everything it needs is on
// [Service] and nothing else is reachable from here: its own store and version
// counters, the run loop that owns them, and three collaborators the rest of the
// server supplies — the rate limiter guarding the public route, a lookup for the
// deployment a document described, and the share-token minter.
//
// The single-writer boundary is the [runloop.Loop] this service holds: every read
// and write of the store goes through it, and there is no other way from here to
// shared state (I3, ADR-0002). Design-time only — nothing here reaches the event
// log, the processor, or recovery.
package processdoc

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"

	"github.com/pblumer/atlas/api/runloop"
)

// Deployment is what a documentation record needs to know about the process
// version it documented: which deployment, and which version of it. Narrow on
// purpose — the service has no business with the rest of a deployment.
type Deployment struct {
	Key     uint64
	Version int32
}

// Service serves the documentation area. Build it with [New].
type Service struct {
	// loop is the single-writer boundary. Every store access below runs on it,
	// which is what lets concurrent HTTP handlers touch this state at all.
	loop *runloop.Loop
	// store holds the records and their PDFs; versions is the per-process counter
	// that numbers the next one. Both are owned by the loop.
	store    *Store
	versions map[string]int32
	// allow rate-limits the unauthenticated public route by client IP. Supplied by
	// the server, which shares one limiter across every public surface, so a
	// scripted reader cannot spend the budget of one endpoint to spare another.
	allow func(clientIP string) bool
	// deployed resolves the deployment currently running a process, so a document
	// can record which running version it described. It reads the deployment
	// registry, so it is only called on the loop.
	deployed func(processID string) (Deployment, bool)
	// newToken mints a share token. Injected rather than called directly so the
	// service can be driven with a deterministic token in tests.
	newToken func() (string, error)
}

// New builds the documentation service over its own store directory. allow,
// deployed and newToken are the collaborators the server supplies; none of them
// may touch state the loop owns except when called from inside it (deployed is
// the only one that does, and the create path calls it on the loop).
func New(loop *runloop.Loop, store *Store, allow func(clientIP string) bool,
	deployed func(processID string) (Deployment, bool), newToken func() (string, error)) *Service {
	return &Service{
		loop:     loop,
		store:    store,
		versions: map[string]int32{},
		allow:    allow,
		deployed: deployed,
		newToken: newToken,
	}
}

// Process documentation export (ADR-0143). The document itself is produced in the
// browser, where bpmn-js already holds the authoritative picture; this file is the
// server half — it validates what arrives, numbers it as the next version of that
// process, stores it durably, and serves it back, optionally through a revocable
// public link.
//
// Design-time only: nothing here reaches the event log, the processor, or
// recovery.

// maxUploadBytes caps a documentation upload. A document is a diagram raster
// plus its element prose; this is a sanity bound on the base64-carrying request
// body, not a tuning knob.
const maxUploadBytes = 24 << 20 // 24 MiB

// pdfMagic is the header every PDF opens with. The server stores opaque bytes,
// but it refuses to store something that is plainly not a document — otherwise a
// mistaken upload only reveals itself to the reader who opens the share link.
var pdfMagic = []byte("%PDF-")

// docResp is one documentation version as the API renders it. Elements and
// XML are carried only by the single-version fetch: a history listing is a
// summary, and hauling every version's whole BPMN source through it would make
// the common read the expensive one.
type docResp struct {
	ID                string `json:"id"`
	ProcessID         string `json:"processId"`
	ProcessName       string `json:"processName,omitempty"`
	Version           int32  `json:"version"`
	Title             string `json:"title,omitempty"`
	Note              string `json:"note,omitempty"`
	CreatedAt         int64  `json:"createdAt"`
	CreatedBy         string `json:"createdBy,omitempty"`
	DeploymentKey     uint64 `json:"deploymentKey,omitempty"`
	DeploymentVersion int32  `json:"deploymentVersion,omitempty"`
	PDFSize           int64  `json:"pdfSize"`
	ElementCount      int    `json:"elementCount"`
	PDFURL            string `json:"pdfUrl"`
	// ShareToken and ShareURL are present only while the version is shared, so
	// their absence is the UI's signal that it is private.
	ShareToken string    `json:"shareToken,omitempty"`
	ShareURL   string    `json:"shareUrl,omitempty"`
	Elements   []Element `json:"elements,omitempty"`
	XML        string    `json:"xml,omitempty"`
}

// toResp renders a stored version. detail asks for the element prose
// and the BPMN source; a listing passes false.
func toResp(rec Doc, detail bool) docResp {
	out := docResp{
		ID: rec.ID, ProcessID: rec.ProcessID, ProcessName: rec.ProcessName,
		Version: rec.Version, Title: rec.Title, Note: rec.Note,
		CreatedAt: rec.CreatedAt, CreatedBy: rec.CreatedBy,
		DeploymentKey: rec.DeploymentKey, DeploymentVersion: rec.DeploymentVersion,
		PDFSize: rec.PDFSize, ElementCount: len(rec.Elements),
		PDFURL: "/api/v1/documentation/" + rec.ID + "/pdf",
	}
	if rec.ShareToken != "" {
		out.ShareToken = rec.ShareToken
		out.ShareURL = PublicPath + rec.ShareToken
	}
	if detail {
		out.Elements = rec.Elements
		out.XML = rec.XML
	}
	return out
}

// PublicPath is the prefix of the unauthenticated share URL. It lives
// in one place so the minted URL and the served route cannot drift.
const PublicPath = "/public/process-docs/"

// LoadVersions rebuilds the per-process documentation counter from the
// durable records, so an export after a restart continues the sequence rather than
// restarting it at v1 and overwriting history. It runs before the loop serves
// traffic, so touching the map directly here respects the single-writer invariant.
func (s *Service) LoadVersions() error {
	recs, err := s.store.LoadAll()
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if rec.Version > s.versions[rec.ProcessID] {
			s.versions[rec.ProcessID] = rec.Version
		}
	}
	return nil
}

// createReq is the upload: the document plus what it documents. The PDF
// arrives base64-encoded inside the JSON body so the whole publication — prose,
// source, and document — is one atomic request rather than a multi-step upload
// that can half-fail.
type createReq struct {
	Title       string    `json:"title"`
	Note        string    `json:"note"`
	ProcessName string    `json:"processName"`
	XML         string    `json:"xml"`
	Elements    []Element `json:"elements"`
	PDFBase64   string    `json:"pdfBase64"`
}

// HandleCreate records the next documentation version of a process
// (ADR-0143). Body: the produced PDF plus the element prose it describes.
func (s *Service) HandleCreate(w http.ResponseWriter, r *http.Request) {
	processID := r.PathValue("processId")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxUploadBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload createReq
	if err := json.Unmarshal(body, &payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if payload.PDFBase64 == "" {
		httpapi.Error(w, http.StatusBadRequest, "pdfBase64 is required")
		return
	}
	pdf, err := base64.StdEncoding.DecodeString(payload.PDFBase64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "pdfBase64 is not valid base64: "+err.Error())
		return
	}
	if !bytes.HasPrefix(pdf, pdfMagic) {
		httpapi.Error(w, http.StatusBadRequest, "the uploaded bytes are not a PDF document")
		return
	}

	id, err := NewID()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "mint documentation id: "+err.Error())
		return
	}
	title := strings.TrimSpace(payload.Title)
	if title == "" {
		title = processID
	}
	actor := ""
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		actor = p.Username
	}

	rec := Doc{
		ID: id, ProcessID: processID, ProcessName: payload.ProcessName,
		Title: title, Note: payload.Note,
		CreatedAt: time.Now().Unix(), CreatedBy: actor,
		PDFSize: int64(len(pdf)), Elements: payload.Elements, XML: payload.XML,
	}

	var opErr error
	s.loop.Do(func() {
		// A documented process that is deployed records which deployment it
		// described, so a reader of the history can tell which running version a
		// document belongs to. Read on the loop, which owns the registry.
		if d, ok := s.deployed(processID); ok {
			rec.DeploymentKey = d.Key
			rec.DeploymentVersion = d.Version
		}
		rec.Version = s.versions[processID] + 1
		if opErr = s.store.Save(rec, pdf); opErr != nil {
			return
		}
		// The counter advances only once the record is durable, so a failed save
		// cannot burn a version number.
		s.versions[processID] = rec.Version
	})
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save documentation: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, toResp(rec, false))
}

// HandleList returns one process's documentation history, newest
// version first.
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	processID := r.PathValue("processId")
	out := []docResp{}
	var loadErr error
	s.loop.Do(func() {
		recs, e := s.store.ForProcess(processID)
		if e != nil {
			loadErr = e
			return
		}
		for _, rec := range recs {
			out = append(out, toResp(rec, false))
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list documentation: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// lookup reads one version on the run loop. It is the shared prologue
// of every route that addresses a version by id.
func (s *Service) lookup(id string) (Doc, bool, error) {
	var (
		rec   Doc
		found bool
		err   error
	)
	s.loop.Do(func() { rec, found, err = s.store.Get(id) })
	return rec, found, err
}

// HandleGet returns one version in full: its metadata, the element
// prose, and the BPMN source it was produced from.
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	rec, found, err := s.lookup(r.PathValue("id"))
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read documentation: "+err.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no documentation version with that id")
	default:
		httpapi.JSON(w, http.StatusOK, toResp(rec, true))
	}
}

// HandleGetPDF downloads a version's document.
func (s *Service) HandleGetPDF(w http.ResponseWriter, r *http.Request) {
	rec, found, err := s.lookup(r.PathValue("id"))
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read documentation: "+err.Error())
		return
	}
	if !found {
		httpapi.Error(w, http.StatusNotFound, "no documentation version with that id")
		return
	}
	s.servePDF(w, rec)
}

// servePDF writes a version's document to the response. Shared by the
// authenticated download and the public share link so both serve identical bytes
// under identical headers.
func (s *Service) servePDF(w http.ResponseWriter, rec Doc) {
	var (
		pdf []byte
		err error
	)
	s.loop.Do(func() { pdf, err = s.store.PDF(rec.ID) })
	if err != nil {
		httpapi.Error(w, http.StatusNotFound, "the document for that version is not available")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "inline; filename=\""+filenameFor(rec)+"\"")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdf)
}

// filenameFor names the downloaded file after the process and version.
// Everything outside a conservative allowlist becomes '-', so the name cannot
// carry a quote, a path separator, or a header-splitting byte into the
// Content-Disposition header.
func filenameFor(rec Doc) string {
	var b strings.Builder
	for _, r := range rec.ProcessID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	name := b.String()
	if name == "" {
		name = "process"
	}
	return name + "-v" + strconv.Itoa(int(rec.Version)) + ".pdf"
}

// HandleShare mints (or returns the existing) public link for one
// version. Idempotent by design: re-sharing must not rotate a URL that readers
// already hold.
func (s *Service) HandleShare(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		rec   Doc
		found bool
		opErr error
	)
	s.loop.Do(func() {
		rec, found, opErr = s.store.Get(id)
		if opErr != nil || !found || rec.ShareToken != "" {
			return
		}
		token, e := s.newToken()
		if e != nil {
			opErr = e
			return
		}
		rec.ShareToken = token
		opErr = s.store.SaveRecord(rec)
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "share documentation: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no documentation version with that id")
	default:
		httpapi.JSON(w, http.StatusOK, toResp(rec, false))
	}
}

// HandleUnshare revokes a version's public link, killing the URL.
func (s *Service) HandleUnshare(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		rec   Doc
		found bool
		opErr error
	)
	s.loop.Do(func() {
		rec, found, opErr = s.store.Get(id)
		if opErr != nil || !found || rec.ShareToken == "" {
			return
		}
		rec.ShareToken = ""
		opErr = s.store.SaveRecord(rec)
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "revoke documentation link: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no documentation version with that id")
	default:
		httpapi.JSON(w, http.StatusOK, toResp(rec, false))
	}
}

// HandleDelete prunes a version, taking its public URL with it. A
// missing version is not an error, so pruning is idempotent.
func (s *Service) HandleDelete(w http.ResponseWriter, r *http.Request) {
	var delErr error
	s.loop.Do(func() { delErr = s.store.Delete(r.PathValue("id")) })
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete documentation: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pruneReq is the retention request: keep the newest `keep` versions
// of this process and prune the rest. `keep` is required and must be non-negative;
// a document is a real artifact someone chose to publish, so nothing is pruned by
// default and the caller has to say how much history to retain.
type pruneReq struct {
	Keep *int `json:"keep"`
}

// pruneResp reports what a prune removed, so the UI can tell the reader
// exactly which versions are gone rather than silently shrinking the list.
type pruneResp struct {
	Deleted []string `json:"deleted"`
	Kept    int      `json:"kept"`
}

// HandlePrune applies a retention limit to a process's documentation
// history (ADR-0143's bounded-growth follow-up): it keeps the newest `keep`
// versions and deletes the older ones, PDF and all. Idempotent — pruning an
// already-short history removes nothing.
func (s *Service) HandlePrune(w http.ResponseWriter, r *http.Request) {
	processID := r.PathValue("processId")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload pruneReq
	if err := json.Unmarshal(body, &payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if payload.Keep == nil {
		httpapi.Error(w, http.StatusBadRequest, "keep is required")
		return
	}
	if *payload.Keep < 0 {
		httpapi.Error(w, http.StatusBadRequest, "keep must not be negative")
		return
	}
	var (
		pruned []string
		opErr  error
	)
	s.loop.Do(func() { pruned, opErr = s.store.PruneProcess(processID, *payload.Keep) })
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "prune documentation: "+opErr.Error())
		return
	}
	if pruned == nil {
		pruned = []string{}
	}
	httpapi.JSON(w, http.StatusOK, pruneResp{Deleted: pruned, Kept: *payload.Keep})
}

// --- public, unauthenticated endpoint (ADR-0143, ADR-0029's mechanism) ---

// HandlePublic serves a shared version's PDF to a reader with no
// account. An unknown, malformed, or revoked token is one indistinguishable 404:
// the response must not reveal whether a document ever existed behind it.
func (s *Service) HandlePublic(w http.ResponseWriter, r *http.Request) {
	if !s.allow(httpapi.ClientIP(r)) {
		httpapi.Error(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	var (
		rec   Doc
		found bool
		opErr error
	)
	s.loop.Do(func() { rec, found, opErr = s.store.ByShareToken(r.PathValue("token")) })
	if opErr != nil || !found {
		httpapi.Error(w, http.StatusNotFound, "not found")
		return
	}
	s.servePDF(w, rec)
}
