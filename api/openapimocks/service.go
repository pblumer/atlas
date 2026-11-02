package openapimocks

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/connector/rest/openapimock"
	"github.com/pblumer/atlas/limits"
)

// journalPrefix is the mock's own inspection area. The compiled server serves a
// journal and a report under it; this service does not expose either on the public
// route. ADR-0217 accepted a world-readable journal because a standalone mock is bound
// where only its caller can reach it, and on the engine's address that reasoning does
// not carry: the journal records request bodies, and those come from real instances of
// a real draft. What a run did is read through the authenticated Mockups view instead.
const journalPrefix = "/__mock/"

// Service serves the Console-managed mock APIs. Build it with [New].
type Service struct {
	// loop is the single-writer boundary (I3): every store access runs on it. Note
	// what does *not* — compiling a document, and serving a call. See the package
	// comment.
	loop  *runloop.Loop
	store *Store
	now   func() int64
	// live is the compiled set, by name. Readers take no lock at all: the read is on
	// the serving path of every mocked call, and a pointer load contends with nothing,
	// so a busy mock cannot slow the Console down and the Console cannot slow a busy
	// mock down.
	live atomic.Pointer[map[string]*openapimock.Server]
	// publishing serializes the writers. The swap is read-copy-write, so two
	// concurrent changes without it would each copy the same map and the second would
	// publish a map missing the first — both records stored, one mock not served. The
	// store writes are already serialized by the loop, but the swap happens outside it
	// (compiling must not run on the loop), so it needs this.
	publishing sync.Mutex

	// Limits are the installation's resource budgets. New sets them to
	// [limits.Default]; the server overwrites them with its own once it has read the
	// environment, so every ceiling here is the one operators configured (ADR-0291).
	Limits limits.Limits
}

// budgets is how this service reads a ceiling. It defaults a Service built as a struct
// literal to [limits.Default], because the zero Limits is every ceiling at zero and a
// ceiling of zero admits nothing — here, every uploaded document refused as empty,
// which reads as a bad request rather than as missing configuration. New always sets
// them.
func (s *Service) budgets() limits.Limits {
	if s.Limits == (limits.Limits{}) {
		return limits.Default()
	}
	return s.Limits
}

// New builds the service. now supplies the wall clock for the stored timestamps, in
// **unix seconds** — the convention every design-time store follows, not the engine
// clock's nanoseconds, which belong to events. It is injected so a test can drive it
// (the determinism rule in AGENTS.md).
func New(loop *runloop.Loop, store *Store, now func() int64) *Service {
	s := &Service{loop: loop, store: store, now: now, Limits: limits.Default()}
	s.live.Store(&map[string]*openapimock.Server{})
	return s
}

// Reload compiles every enabled record and publishes the result. It runs at startup,
// off the loop: compiling is the expensive part and it reads nothing shared.
//
// A record whose document no longer compiles is skipped rather than fatal, and the
// reason is returned for the caller to log. Refusing to start because a mock somebody
// left switched on months ago has rotted would make a dev aid able to stop the engine.
func (s *Service) Reload() []error {
	recs, err := s.store.LoadAll()
	if err != nil {
		return []error{err}
	}
	var problems []error
	live := map[string]*openapimock.Server{}
	for _, rec := range recs {
		if !rec.Enabled {
			continue
		}
		srv, err := s.compile(rec)
		if err != nil {
			problems = append(problems, fmt.Errorf("mock API %q: %w", rec.Name, err))
			continue
		}
		live[rec.Name] = srv
	}
	// Under the same lock as a swap: Reload is a startup path, but nothing stops a
	// request arriving while it runs, and a full replacement racing a copy-on-write
	// swap is the same lost update in the other direction.
	s.publishing.Lock()
	s.live.Store(&live)
	s.publishing.Unlock()
	return problems
}

// compile reads a record's stored document and compiles it into a server ready to
// serve. The base path stays the document's own, so a document written against
// https://api.example.com/v1 mocks /v1/… exactly as the real API does and the author's
// substitution is a base URL rather than a rewrite of every task.
func (s *Service) compile(rec Mock) (*openapimock.Server, error) {
	doc, err := s.store.Document(rec.ID)
	if err != nil {
		return nil, err
	}
	spec, err := openapimock.Load(doc)
	if err != nil {
		return nil, err
	}
	return openapimock.New(spec, openapimock.WithID(rec.Name)), nil
}

// publish swaps names in or out in one step. It copies the map rather than mutating it
// because readers hold the old one: a reader that saw the pointer before the swap keeps
// serving from a complete map until it next loads (copy-on-write, the same discipline as
// the connector registries). A nil server removes that name.
//
// It takes every change at once because a rename is two changes that must not be
// visible apart: the old address answering after the new one exists is the window a
// second swap would open.
func (s *Service) publish(changes map[string]*openapimock.Server) {
	s.publishing.Lock()
	defer s.publishing.Unlock()
	next := map[string]*openapimock.Server{}
	for k, v := range *s.live.Load() {
		next[k] = v
	}
	for name, srv := range changes {
		if srv == nil {
			delete(next, name)
		} else {
			next[name] = srv
		}
	}
	s.live.Store(&next)
}

// view is one mock as the Console reads it: the record, plus the two URLs that are the
// whole point of the card — what to call instead, and what it stands in for.
type view struct {
	Mock
	// BaseURL is the path a task must call, document base path included. It is served
	// relative to whatever address Atlas is reached on, so it is a path and not a URL:
	// the browser knows its own origin and the server does not know which of its
	// names the caller used.
	BaseURL string `json:"baseUrl"`
	// Serving says whether this record is actually being served right now, which is
	// not the same as Enabled: a document that stopped compiling is enabled and not
	// serving, and that difference is exactly what a card has to show.
	Serving bool `json:"serving"`
}

func (s *Service) viewOf(rec Mock) view {
	_, serving := (*s.live.Load())[rec.Name]
	return view{Mock: rec, BaseURL: MockPath + rec.Name + rec.SpecBasePath, Serving: serving}
}

// HandleList answers every stored mock.
func (s *Service) HandleList(w http.ResponseWriter, _ *http.Request) {
	var recs []Mock
	var err error
	s.loop.Do(func() { recs, err = s.store.LoadAll() })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]view, 0, len(recs))
	for _, rec := range recs {
		views = append(views, s.viewOf(rec))
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"mocks": views})
}

// saveParams is the body of a create or update.
type saveParams struct {
	Name     string `json:"name"`
	Document string `json:"document"`
	Filename string `json:"filename"`
	// Enabled is a pointer so an update that does not mention it leaves the switch
	// where it was: renaming a mock must not silently turn it on.
	Enabled *bool `json:"enabled"`
}

func (s *Service) decode(w http.ResponseWriter, r *http.Request) (saveParams, bool) {
	var p saveParams
	// The import budget rather than the payload one: what travels here is a document
	// somebody else published, and the largest anyone has pointed this at is
	// DigitalOcean's 659 operations assembled from 1141 files.
	if err := json.NewDecoder(io.LimitReader(r.Body, s.budgets().Import)).Decode(&p); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return p, false
	}
	return p, true
}

// HandleCreate stores a new mock from an uploaded document.
//
// The document is compiled before anything is stored, and a document that will not
// compile is refused with the compiler's own message. That is ADR-0217's "refuse rather
// than improvise" moved to where it does the most good: the person who chose the
// document is standing right there, instead of finding out from a null in the middle of
// a demo.
func (s *Service) HandleCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.decode(w, r)
	if !ok {
		return
	}
	name, err := ValidateName(p.Name)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	doc := []byte(p.Document)
	if len(strings.TrimSpace(p.Document)) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "an OpenAPI document is required: Atlas serves what you upload, so there is no file path to give it")
		return
	}
	spec, err := openapimock.Load(doc)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "this document cannot be served: "+err.Error())
		return
	}
	id, err := NewID()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	enabled := p.Enabled == nil || *p.Enabled
	at := s.now()
	rec := recordFrom(Mock{ID: id, Name: name, Enabled: enabled, Filename: strings.TrimSpace(p.Filename),
		Size: len(doc), CreatedAt: at, UpdatedAt: at}, spec)

	// One turn does the whole check-then-write: the name's uniqueness is only
	// meaningful if nothing can take it between the check and the save.
	var taken bool
	s.loop.Do(func() {
		var recs []Mock
		if recs, err = s.store.LoadAll(); err != nil {
			return
		}
		for _, other := range recs {
			if other.Name == name {
				taken = true
				return
			}
		}
		err = s.store.Save(rec, doc)
	})
	switch {
	case taken:
		httpapi.Error(w, http.StatusConflict, "a mock API named "+name+" is already here")
		return
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if enabled {
		s.publish(map[string]*openapimock.Server{name: openapimock.New(spec, openapimock.WithID(name))})
	}
	httpapi.JSON(w, http.StatusCreated, s.viewOf(rec))
}

// recordFrom stamps onto a record what compiling the document found.
func recordFrom(rec Mock, spec *openapimock.Spec) Mock {
	rec.Title = spec.Title
	rec.Version = spec.Version
	rec.Operations = len(spec.Operations)
	rec.Files = spec.Files
	rec.Skipped = spec.Skipped
	rec.SpecBasePath = spec.BasePath
	rec.ServerURL = spec.ServerURL
	return rec
}

// HandleUpdate renames a mock, switches it on or off, or replaces its document. An
// omitted document leaves the stored one alone, which is what a rename and a flip of
// the switch both want.
func (s *Service) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.decode(w, r)
	if !ok {
		return
	}
	var rec Mock
	var found bool
	var err error
	s.loop.Do(func() { rec, found, err = s.store.Get(id) })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		httpapi.Error(w, http.StatusNotFound, "no mock API with id "+id)
		return
	}
	was := rec.Name
	if strings.TrimSpace(p.Name) != "" {
		name, err := ValidateName(p.Name)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, err.Error())
			return
		}
		rec.Name = name
	}
	if p.Enabled != nil {
		rec.Enabled = *p.Enabled
	}

	// Whichever document is going to be served next — the replacement, or the stored
	// one this record is being switched back on with — is compiled before anything is
	// written, and off the loop.
	replacing := strings.TrimSpace(p.Document) != ""
	var doc []byte
	var spec *openapimock.Spec
	if replacing {
		doc = []byte(p.Document)
		if spec, err = openapimock.Load(doc); err != nil {
			httpapi.Error(w, http.StatusBadRequest, "this document cannot be served: "+err.Error())
			return
		}
		rec = recordFrom(rec, spec)
		rec.Size = len(doc)
		if f := strings.TrimSpace(p.Filename); f != "" {
			rec.Filename = f
		}
	}
	rec.UpdatedAt = s.now()

	var taken bool
	s.loop.Do(func() {
		var recs []Mock
		if recs, err = s.store.LoadAll(); err != nil {
			return
		}
		for _, other := range recs {
			if other.Name == rec.Name && other.ID != rec.ID {
				taken = true
				return
			}
		}
		if replacing {
			err = s.store.Save(rec, doc)
			return
		}
		err = s.store.SaveRecord(rec)
	})
	switch {
	case taken:
		httpapi.Error(w, http.StatusConflict, "a mock API named "+rec.Name+" is already here")
		return
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	// One swap carries the whole change. The old name is dropped in the same step as
	// the new one appears, so a rename never leaves the previous address answering.
	changes := map[string]*openapimock.Server{}
	if was != rec.Name {
		changes[was] = nil
	}
	switch {
	case !rec.Enabled:
		changes[rec.Name] = nil
	case spec != nil:
		changes[rec.Name] = openapimock.New(spec, openapimock.WithID(rec.Name))
	default:
		// Switched on (or renamed) without a new document: compile the stored one.
		// Reading it off the loop is sanctioned — a record is replaced by an atomic
		// rename, so a reader sees the whole old document or the whole new one.
		srv, cerr := s.compile(rec)
		if cerr != nil {
			changes[rec.Name] = nil
			s.publish(changes)
			httpapi.Error(w, http.StatusBadRequest, "the stored document cannot be served: "+cerr.Error())
			return
		}
		changes[rec.Name] = srv
	}
	s.publish(changes)
	httpapi.JSON(w, http.StatusOK, s.viewOf(rec))
}

// HandleDelete removes a mock and stops serving it.
func (s *Service) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var rec Mock
	var found bool
	var err error
	s.loop.Do(func() {
		if rec, found, err = s.store.Get(id); err != nil || !found {
			return
		}
		err = s.store.Delete(id)
	})
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		httpapi.Error(w, http.StatusNotFound, "no mock API with id "+id)
		return
	}
	s.publish(map[string]*openapimock.Server{rec.Name: nil})
	httpapi.JSON(w, http.StatusOK, map[string]any{"id": id, "name": rec.Name})
}

// HandleDocument serves back the document Atlas holds for a record, so what is being
// served can be read without keeping a copy somewhere else.
func (s *Service) HandleDocument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var rec Mock
	var found bool
	var err error
	var doc []byte
	s.loop.Do(func() {
		if rec, found, err = s.store.Get(id); err != nil || !found {
			return
		}
		doc, err = s.store.Document(id)
	})
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		httpapi.Error(w, http.StatusNotFound, "no mock API with id "+id)
		return
	}
	// The document is served as text, not as the JSON or YAML it happens to be: it was
	// uploaded verbatim and it goes back verbatim, and guessing between the two from
	// the bytes would be a second place that guess could be wrong.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+rec.Name+".openapi\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(doc)
}

// HandleMock serves a mocked call. It is mounted public under [MockPath] and it is the
// one handler here that must stay off the loop entirely — see the package comment.
func (s *Service) HandleMock(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, MockPath)
	name, rest, _ := strings.Cut(rest, "/")
	srv, ok := (*s.live.Load())[name]
	if !ok {
		// A mock that is not here and a mock that is switched off are the same answer:
		// this route says what is being served and nothing about what is stored, which
		// is all an unauthenticated caller has any business learning from it.
		httpapi.Error(w, http.StatusNotFound, "no mock API is being served at "+MockPath+name)
		return
	}
	if rest = "/" + rest; strings.HasPrefix(rest, journalPrefix) {
		httpapi.Error(w, http.StatusNotFound,
			"this mock's journal is not public: read it in Operations › Diagnostics › Mockups")
		return
	}
	// Serve the document's own paths: the request is re-pointed at the path below this
	// mock's prefix, so the compiled matcher sees exactly what the real API would. The
	// copy is http.StripPrefix's — shallow, with only the URL replaced — rather than
	// r.Clone, which would deep-copy every header on a path that serves every call.
	r2 := new(http.Request)
	*r2 = *r
	r2.URL = new(url.URL)
	*r2.URL = *r.URL
	r2.URL.Path = rest
	srv.Handler().ServeHTTP(w, r2)
}

// Reports is every served mock in ADR-0216's envelope, collected at read time. An
// in-process mock posts nothing: there is no report to go stale and no transport to
// build, which is why ADR-0216's POST /api/v1/mocks is not needed for this kind.
func (s *Service) Reports() []openapimock.Report {
	live := *s.live.Load()
	reports := make([]openapimock.Report, 0, len(live))
	for _, srv := range live {
		reports = append(reports, srv.Report())
	}
	// By name, because a map's order is not one: the view polls, and cards that
	// reordered themselves between two polls would be a list nobody can read.
	sort.Slice(reports, func(i, j int) bool { return reports[i].Source < reports[j].Source })
	return reports
}
