// Package decisiondoc serves decision documentation
// (ADR-draft-decision-documentation): a published DMN decision as a stored PDF
// plus the prose and rule tables it describes, its version history, and the
// revocable public link a reader without an account follows.
//
// It is a deliberate sibling of api/processdoc, which does the same for a BPMN
// process, and the two are meant to be diffed against each other. The duplication
// is recorded in the ADR together with the rule for undoing it: when a third
// artifact kind wants a document, extract the generic half first and add the third
// second. Generalising now would mean either migrating a landed store's records or
// keeping a field whose name is a lie.
//
// The single-writer boundary is the [runloop.Loop] this service holds: every read
// and write of the store goes through it, and there is no other way from here to
// shared state (I3, ADR-0002). Design-time only — nothing here reaches the event
// log, the processor, or recovery.
package decisiondoc

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
	"github.com/pblumer/atlas/limits"
)

// Service serves the decision-documentation area. Build it with [New].
type Service struct {
	// loop is the single-writer boundary. Every store access below runs on it,
	// which is what lets concurrent HTTP handlers touch this state at all.
	loop *runloop.Loop
	// store holds the records and their PDFs; versions is the per-decision counter
	// that numbers the next one. Both are owned by the loop.
	store    *Store
	versions map[string]int32
	// allow rate-limits the unauthenticated public route by client IP. Supplied by
	// the server, which shares one limiter across every public surface, so a
	// scripted reader cannot spend the budget of one endpoint to spare another.
	allow func(clientIP string) bool
	// newToken mints a share token. Injected rather than called directly so the
	// service can be driven with a deterministic token in tests.
	newToken func() (string, error)

	// Limits are the installation's resource budgets. New sets them to
	// [limits.Default]; the server overwrites them with its own once it has read the
	// environment, so every ceiling in this service is the one operators configured.
	Limits limits.Limits
}

// New builds the documentation service over its own store directory. allow and
// newToken are the collaborators the server supplies; neither may touch state the
// loop owns.
//
// Unlike its process sibling it takes no deployment lookup. A decision document is
// a sign-off artifact and is usually published *before* the decision is deployed,
// so the field would be empty in the common case; ADR-draft-decision-documentation
// leaves it out until somebody asks for it.
func New(loop *runloop.Loop, store *Store, allow func(clientIP string) bool,
	newToken func() (string, error)) *Service {
	return &Service{
		loop:     loop,
		store:    store,
		versions: map[string]int32{},
		allow:    allow,
		newToken: newToken,
		Limits:   limits.Default(),
	}
}

// pdfMagic is the header every PDF opens with. The server stores opaque bytes,
// but it refuses to store something that is plainly not a document — otherwise a
// mistaken upload only reveals itself to the reader who opens the share link.
var pdfMagic = []byte("%PDF-")

// docResp is one documentation version as the API renders it. Decisions and XML
// are carried only by the single-version fetch: a history listing is a summary,
// and hauling every version's whole DMN source through it would make the common
// read the expensive one.
type docResp struct {
	ID            string `json:"id"`
	DecisionID    string `json:"decisionId"`
	ModelName     string `json:"modelName,omitempty"`
	ModelRef      string `json:"modelRef,omitempty"`
	Version       int32  `json:"version"`
	Title         string `json:"title,omitempty"`
	Note          string `json:"note,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
	CreatedBy     string `json:"createdBy,omitempty"`
	PDFSize       int64  `json:"pdfSize"`
	DecisionCount int    `json:"decisionCount"`
	PDFURL        string `json:"pdfUrl"`
	// ShareToken and ShareURL are present only while the version is shared, so
	// their absence is the UI's signal that it is private.
	ShareToken string     `json:"shareToken,omitempty"`
	ShareURL   string     `json:"shareUrl,omitempty"`
	Decisions  []Decision `json:"decisions,omitempty"`
	XML        string     `json:"xml,omitempty"`
}

// toResp renders a stored version. detail asks for the documented decisions and
// the DMN source; a listing passes false.
func toResp(rec Doc, detail bool) docResp {
	out := docResp{
		ID: rec.ID, DecisionID: rec.DecisionID, ModelName: rec.ModelName, ModelRef: rec.ModelRef,
		Version: rec.Version, Title: rec.Title, Note: rec.Note,
		CreatedAt: rec.CreatedAt, CreatedBy: rec.CreatedBy,
		PDFSize: rec.PDFSize, DecisionCount: len(rec.Decisions),
		PDFURL: "/api/v1/decision-docs/" + rec.ID + "/pdf",
	}
	if rec.ShareToken != "" {
		out.ShareToken = rec.ShareToken
		out.ShareURL = PublicPath + rec.ShareToken
	}
	if detail {
		out.Decisions = rec.Decisions
		out.XML = rec.XML
	}
	return out
}

// PublicPath is the prefix of the unauthenticated share URL. It lives in one place
// so the minted URL and the served route cannot drift.
const PublicPath = "/public/decision-docs/"

// LoadVersions rebuilds the per-decision documentation counter from the durable
// records, so an export after a restart continues the sequence rather than
// restarting it at v1 and overwriting history. It runs before the loop serves
// traffic, so touching the map directly here respects the single-writer invariant.
func (s *Service) LoadVersions() error {
	recs, err := s.store.LoadAll()
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if rec.Version > s.versions[rec.DecisionID] {
			s.versions[rec.DecisionID] = rec.Version
		}
	}
	return nil
}

// createReq is the upload: the document plus what it documents. The PDF arrives
// base64-encoded inside the JSON body so the whole publication — prose, source,
// and document — is one atomic request rather than a multi-step upload that can
// half-fail.
type createReq struct {
	Title     string     `json:"title"`
	Note      string     `json:"note"`
	ModelName string     `json:"modelName"`
	ModelRef  string     `json:"modelRef"`
	XML       string     `json:"xml"`
	Decisions []Decision `json:"decisions"`
	PDFBase64 string     `json:"pdfBase64"`
}

// HandleCreate records the next documentation version of a decision. Body: the
// produced PDF plus the decision prose and rule tables it describes.
func (s *Service) HandleCreate(w http.ResponseWriter, r *http.Request) {
	decisionID := r.PathValue("decisionId")
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Import))
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
		title = decisionID
	}
	actor := ""
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		actor = p.Username
	}

	rec := Doc{
		ID: id, DecisionID: decisionID, ModelName: payload.ModelName, ModelRef: payload.ModelRef,
		Title: title, Note: payload.Note,
		CreatedAt: time.Now().Unix(), CreatedBy: actor,
		PDFSize: int64(len(pdf)), Decisions: payload.Decisions, XML: payload.XML,
	}

	var opErr error
	s.loop.Do(func() {
		rec.Version = s.versions[decisionID] + 1
		if opErr = s.store.Save(rec, pdf); opErr != nil {
			return
		}
		// The counter advances only once the record is durable, so a failed save
		// cannot burn a version number.
		s.versions[decisionID] = rec.Version
	})
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save documentation: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, toResp(rec, false))
}

// HandleList returns one decision's documentation history, newest version first.
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	decisionID := r.PathValue("decisionId")
	out := []docResp{}
	var loadErr error
	s.loop.Do(func() {
		recs, e := s.store.ForDecision(decisionID)
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

// lookup reads one version on the run loop. It is the shared prologue of every
// route that addresses a version by id.
func (s *Service) lookup(id string) (Doc, bool, error) {
	var (
		rec   Doc
		found bool
		err   error
	)
	s.loop.Do(func() { rec, found, err = s.store.Get(id) })
	return rec, found, err
}

// HandleGet returns one version in full: its metadata, the documented decisions,
// and the DMN source it was produced from.
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

// filenameFor names the downloaded file after the decision and version.
// Everything outside a conservative allowlist becomes '-', so the name cannot
// carry a quote, a path separator, or a header-splitting byte into the
// Content-Disposition header.
func filenameFor(rec Doc) string {
	var b strings.Builder
	for _, r := range rec.DecisionID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	name := b.String()
	if name == "" {
		name = "decision"
	}
	return name + "-v" + strconv.Itoa(int(rec.Version)) + ".pdf"
}

// HandleShare mints (or returns the existing) public link for one version.
// Idempotent by design: re-sharing must not rotate a URL that readers already
// hold.
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
		tok, e := s.newToken()
		if e != nil {
			opErr = e
			return
		}
		rec.ShareToken = tok
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

// HandleDelete prunes a version, taking its public URL with it. A missing version
// is not an error, so pruning is idempotent.
func (s *Service) HandleDelete(w http.ResponseWriter, r *http.Request) {
	var delErr error
	s.loop.Do(func() { delErr = s.store.Delete(r.PathValue("id")) })
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete documentation: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pruneReq is the retention request: keep the newest `keep` versions of this
// decision and prune the rest. `keep` is required and must be non-negative; a
// document is a real artifact someone chose to publish, so nothing is pruned by
// default and the caller has to say how much history to retain.
type pruneReq struct {
	Keep *int `json:"keep"`
}

// pruneResp reports what a prune removed, so the UI can tell the reader exactly
// which versions are gone rather than silently shrinking the list.
type pruneResp struct {
	Deleted []string `json:"deleted"`
	Kept    int      `json:"kept"`
}

// HandlePrune applies a retention limit to a decision's documentation history: it
// keeps the newest `keep` versions and deletes the older ones, PDF and all.
// Idempotent — pruning an already-short history removes nothing.
func (s *Service) HandlePrune(w http.ResponseWriter, r *http.Request) {
	decisionID := r.PathValue("decisionId")
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Request))
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
	s.loop.Do(func() { pruned, opErr = s.store.PruneDecision(decisionID, *payload.Keep) })
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "prune documentation: "+opErr.Error())
		return
	}
	if pruned == nil {
		pruned = []string{}
	}
	httpapi.JSON(w, http.StatusOK, pruneResp{Deleted: pruned, Kept: *payload.Keep})
}

// --- public, unauthenticated endpoint (ADR-0029's mechanism) ---

// HandlePublic serves a shared version's PDF to a reader with no account. An
// unknown, malformed, or revoked token is one indistinguishable 404: the response
// must not reveal whether a document ever existed behind it.
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

// budgets is how this service reads a ceiling. It defaults a Service built as a
// struct literal to [limits.Default], because the zero Limits is every ceiling at
// zero and a ceiling of zero admits nothing — a failure that looks like a bad
// request rather than like missing configuration. New always sets them.
func (s *Service) budgets() limits.Limits {
	if s.Limits == (limits.Limits{}) {
		return limits.Default()
	}
	return s.Limits
}
