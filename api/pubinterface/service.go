package pubinterface

// The HTTP half of ADR-0373: reading the interfaces a caller may see, the
// candidates a definition offers, and the publisher's acts — publish, rebind,
// deprecate, grant.
//
// It is a per-area service (ADR-0147). The store is owned by the run loop the
// service holds, and every collaborator the server supplies through [Host] — the
// deployed definitions, the caller's role on an application, the identity
// directory — is called only from inside that loop's turn, so a check and the write
// it guards happen in one turn (I3, ADR-0002). Nothing here reaches the processor,
// the WAL or recovery.
//
// What a reader sees depends on who they are, and that is the point of the record:
// a consumer holding view reads the contract — names, kinds, correlation keys,
// payloads, returns, outcomes — and never the element ids, message names or process
// behind it. Those are the publisher's internal organisation; the publishing
// application's own members read them, and its owner reads the grants and the audit.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/limits"
)

// Ranks of the caller's role on the publishing application (ADR-0071).
const (
	RankNone   = 0
	RankViewer = 1
	RankEditor = 2
	RankOwner  = 3
)

// Availability reasons: why a contract version cannot be delivered to now. The
// contract stays readable either way, so a consumer's binding stays legible.
const (
	UnavailableDefinitionMissing  = "definition-missing"
	UnavailableDefinitionInactive = "definition-inactive"
	UnavailableOtherApplication   = "definition-in-other-application"
)

const maxNote = 1000

// Definition is what the server knows about one deployed definition.
type Definition struct {
	Key           uint64
	ProcessID     string
	Version       int32
	ApplicationID string
	Inactive      bool
	Process       *compiler.CompiledProcess
}

// Host is what the server supplies. Definition, Rank, UserExists and GroupExists
// are called on the run loop only; Names is called off it.
type Host struct {
	Definition  func(key uint64) (Definition, bool)
	Rank        func(p *httpapi.Principal, applicationID string) (int, error)
	UserExists  func(id string) (bool, error)
	GroupExists func(id string) (bool, error)
	Names       func(key uint64) map[string]string
}

// Service serves the published-interface routes. Build it with [NewService].
type Service struct {
	loop        *runloop.Loop
	store       *Store
	host        Host
	authEnabled func() bool
	now         func() int64

	// Limits are the installation's resource budgets (ADR-0291); the server
	// overwrites the default with its own.
	Limits limits.Limits
}

// NewService builds the service. authEnabled is read per request because the server
// settles it after constructing its services; now stamps an audit entry in Unix
// seconds.
func NewService(loop *runloop.Loop, store *Store, host Host, authEnabled func() bool, now func() int64) *Service {
	return &Service{loop: loop, store: store, host: host, authEnabled: authEnabled, now: now, Limits: limits.Default()}
}

// Store answers the store, for the peer endpoint of ADR-0372 to check a send grant
// against once it exists.
func (s *Service) Store() *Store { return s.store }

// --- views -------------------------------------------------------------------

// VersionView is one contract version as a reader sees it.
type VersionView struct {
	Version            int      `json:"version"`
	PublishedAt        int64    `json:"publishedAt"`
	Deprecated         bool     `json:"deprecated"`
	DeprecatedAt       int64    `json:"deprecatedAt,omitempty"`
	DeprecationNote    string   `json:"deprecationNote,omitempty"`
	Available          bool     `json:"available"`
	UnavailableReasons []string `json:"unavailableReasons"`
	Contract           Contract `json:"contract"`
	// The fields below are the publisher's internal organisation, answered only to
	// the publishing application's own members.
	PublishedBy string   `json:"publishedBy,omitempty"`
	Binding     *Binding `json:"binding,omitempty"`
	BoundAt     int64    `json:"boundAt,omitempty"`
	BoundBy     string   `json:"boundBy,omitempty"`
}

// InterfaceView is one interface as a reader sees it.
type InterfaceView struct {
	ID            string        `json:"id"`
	LatestVersion int           `json:"latestVersion"`
	Public        bool          `json:"public"`
	MayView       bool          `json:"mayView"`
	MaySend       bool          `json:"maySend"`
	Versions      []VersionView `json:"versions"`
	// Member fields: the publishing side's own view.
	ProcessID     string  `json:"processId,omitempty"`
	ApplicationID string  `json:"applicationId,omitempty"`
	Revision      uint64  `json:"revision,omitempty"`
	Grants        *Grants `json:"grants,omitempty"`
}

// SummaryView is one interface in the list.
type SummaryView struct {
	ID                 string   `json:"id"`
	LatestVersion      int      `json:"latestVersion"`
	Deprecated         bool     `json:"deprecated"`
	Available          bool     `json:"available"`
	UnavailableReasons []string `json:"unavailableReasons"`
	EntryPoints        []string `json:"entryPoints"`
	Public             bool     `json:"public"`
	MaySend            bool     `json:"maySend"`
	ProcessID          string   `json:"processId,omitempty"`
	ApplicationID      string   `json:"applicationId,omitempty"`
}

// availability says whether a version's bound definition can be delivered to now.
func (s *Service) availability(in Interface, v Version) []string {
	d, ok := s.host.Definition(v.Binding.DefinitionKey)
	switch {
	case !ok:
		return []string{UnavailableDefinitionMissing}
	case d.ApplicationID != in.ApplicationID:
		return []string{UnavailableOtherApplication}
	case d.Inactive:
		return []string{UnavailableDefinitionInactive}
	}
	return []string{}
}

func (s *Service) versionView(in Interface, v Version, member bool) VersionView {
	reasons := s.availability(in, v)
	out := VersionView{
		Version: v.Version, PublishedAt: v.PublishedAt, Deprecated: v.Deprecated,
		DeprecatedAt: v.DeprecatedAt, DeprecationNote: v.DeprecationNote,
		Available: len(reasons) == 0, UnavailableReasons: reasons, Contract: v.Contract.clone(),
	}
	if member {
		b := v.Binding.clone()
		out.PublishedBy, out.Binding, out.BoundAt, out.BoundBy = v.PublishedBy, &b, v.BoundAt, v.BoundBy
	}
	return out
}

// access is what one caller may do with one interface.
type access struct {
	rank    int
	view    bool
	send    bool
	member  bool
	visible bool
}

func principalOf(p *httpapi.Principal) Principal {
	if p == nil {
		return Principal{}
	}
	return Principal{UserID: p.UserID, GroupIDs: append([]string(nil), p.GroupIDs...)}
}

// accessOn is evaluated on the loop. Without authentication there is one user, and
// they see everything; with it, the application's own members see the interface
// whatever its grants say, and everybody else needs public or a view grant.
func (s *Service) accessOn(p *httpapi.Principal, in Interface) (access, error) {
	if !s.authEnabled() {
		return access{rank: RankOwner, view: true, send: true, member: true, visible: true}, nil
	}
	rank, err := s.host.Rank(p, in.ApplicationID)
	if err != nil {
		return access{}, err
	}
	pr := principalOf(p)
	a := access{rank: rank, view: in.MayView(pr), send: in.MaySend(pr), member: rank >= RankViewer}
	a.visible = a.view || a.member
	return a, nil
}

func (s *Service) interfaceView(in Interface, a access) InterfaceView {
	out := InterfaceView{
		ID: in.ID, LatestVersion: in.Latest().Version, Public: in.Grants.Public,
		MayView: a.visible, MaySend: a.send, Versions: make([]VersionView, 0, len(in.Versions)),
	}
	for _, v := range in.Versions {
		out.Versions = append(out.Versions, s.versionView(in, v, a.member))
	}
	if a.member {
		out.ProcessID, out.ApplicationID, out.Revision = in.ProcessID, in.ApplicationID, in.Revision
	}
	if a.rank >= RankOwner {
		g := in.Grants.clone()
		out.Grants = &g
	}
	return out
}

// --- reads -------------------------------------------------------------------

func (s *Service) unavailable(w http.ResponseWriter) {
	httpapi.Error(w, http.StatusServiceUnavailable,
		"published interfaces are unavailable until the server restarts: an earlier write may or may not have reached the disk, so no grant is trusted until it is read back")
}

func (s *Service) principal(w http.ResponseWriter, r *http.Request) (*httpapi.Principal, bool) {
	p := httpapi.PrincipalFrom(r.Context())
	if s.authEnabled() && p == nil {
		httpapi.Error(w, http.StatusUnauthorized, "authentication required")
		return nil, false
	}
	return p, true
}

// HandleList answers every interface the caller may see, ordered by id — and only
// those: an interface the caller may not view is not listed, so its existence is
// not disclosed either (ADR-0373).
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	var (
		items     []SummaryView
		uncertain bool
		err       error
	)
	s.loop.Do(func() {
		if uncertain = s.store.Uncertain(); uncertain {
			return
		}
		items = []SummaryView{}
		for _, in := range s.store.All() {
			var a access
			if a, err = s.accessOn(p, in); err != nil {
				return
			}
			if !a.visible {
				continue
			}
			latest := in.Latest()
			reasons := s.availability(in, latest)
			item := SummaryView{
				ID: in.ID, LatestVersion: latest.Version, Deprecated: latest.Deprecated,
				Available: len(reasons) == 0, UnavailableReasons: reasons,
				EntryPoints: make([]string, 0, len(latest.Contract.EntryPoints)),
				Public:      in.Grants.Public, MaySend: a.send,
			}
			for _, e := range latest.Contract.EntryPoints {
				item.EntryPoints = append(item.EntryPoints, e.Name)
			}
			if a.member {
				item.ProcessID, item.ApplicationID = in.ProcessID, in.ApplicationID
			}
			items = append(items, item)
		}
	})
	switch {
	case uncertain:
		s.unavailable(w)
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "published interfaces: "+err.Error())
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
	}
}

// read loads one interface and the caller's access to it, on the loop. An interface
// the caller may not see answers not found, exactly like one that does not exist.
func (s *Service) read(w http.ResponseWriter, r *http.Request, then func(in Interface, a access) (int, string)) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !ValidID(id) {
		httpapi.Error(w, http.StatusBadRequest, "invalid interface id")
		return
	}
	var (
		code int
		msg  string
	)
	s.loop.Do(func() {
		if s.store.Uncertain() {
			code = http.StatusServiceUnavailable
			return
		}
		in, found := s.store.Get(id)
		if !found {
			code, msg = http.StatusNotFound, ErrNotFound.Error()
			return
		}
		a, err := s.accessOn(p, in)
		switch {
		case err != nil:
			code, msg = http.StatusInternalServerError, "published interfaces: "+err.Error()
		case !a.visible:
			code, msg = http.StatusNotFound, ErrNotFound.Error()
		default:
			code, msg = then(in, a)
		}
	})
	switch code {
	case 0:
	case http.StatusServiceUnavailable:
		s.unavailable(w)
	default:
		httpapi.Error(w, code, msg)
	}
}

// HandleGet answers one interface with every contract version.
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	var view InterfaceView
	done := false
	s.read(w, r, func(in Interface, a access) (int, string) {
		view, done = s.interfaceView(in, a), true
		return 0, ""
	})
	if done {
		httpapi.JSON(w, http.StatusOK, view)
	}
}

// HandleGetVersion answers one contract version.
func (s *Service) HandleGetVersion(w http.ResponseWriter, r *http.Request) {
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		httpapi.Error(w, http.StatusBadRequest, "invalid contract version")
		return
	}
	var view VersionView
	done := false
	s.read(w, r, func(in Interface, a access) (int, string) {
		v, ok := in.VersionOf(version)
		if !ok {
			return http.StatusNotFound, ErrNoVersion.Error()
		}
		view, done = s.versionView(in, v, a.member), true
		return 0, ""
	})
	if done {
		httpapi.JSON(w, http.StatusOK, view)
	}
}

// HandleHistory answers the interface's audit, newest first. It names the people
// who published, rebound and granted, and every audience a grant change left, so
// like a project's grant audit (ADR-0184) it is the publishing application's
// owner's to read.
func (s *Service) HandleHistory(w http.ResponseWriter, r *http.Request) {
	var items []Change
	done := false
	s.read(w, r, func(in Interface, a access) (int, string) {
		if a.rank < RankOwner {
			return http.StatusForbidden, "the audit of a published interface is its application owner's to read"
		}
		items = make([]Change, 0, len(in.History))
		for i := len(in.History) - 1; i >= 0; i-- {
			items = append(items, in.History[i])
		}
		done = true
		return 0, ""
	})
	if done {
		httpapi.JSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
	}
}

// CandidatesView is what a definition offers for publication.
type CandidatesView struct {
	DefinitionKey uint64 `json:"definitionKey"`
	ProcessID     string `json:"processId"`
	Version       int32  `json:"version"`
	ApplicationID string `json:"applicationId"`
	Candidates
	// Interfaces are the published interfaces of this process already, so a
	// publisher adds a version rather than a second interface by mistake.
	Interfaces []string `json:"interfaces"`
}

// HandleCandidates answers what one deployed definition offers for publication: its
// entry points, every terminal outcome, the codes that can leave it and the messages
// it throws. It reads the model, so the application's viewers may ask.
func (s *Service) HandleCandidates(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil || key == 0 {
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var (
		def        Definition
		interfaces []string
		code       int
		msg        string
	)
	s.loop.Do(func() {
		if def, code, msg = s.definitionFor(p, key, RankViewer); code != 0 {
			return
		}
		interfaces = []string{}
		for _, in := range s.store.All() {
			if in.ProcessID == def.ProcessID {
				interfaces = append(interfaces, in.ID)
			}
		}
	})
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	var names map[string]string
	if s.host.Names != nil {
		names = s.host.Names(key)
	}
	httpapi.JSON(w, http.StatusOK, CandidatesView{
		DefinitionKey: def.Key, ProcessID: def.ProcessID, Version: def.Version, ApplicationID: def.ApplicationID,
		Candidates: Derive(def.Process, names), Interfaces: interfaces,
	})
}

// definitionFor finds a deployed definition and checks the caller's role on the
// application it is filed under. On the loop.
func (s *Service) definitionFor(p *httpapi.Principal, key uint64, min int) (Definition, int, string) {
	d, ok := s.host.Definition(key)
	if !ok || d.Process == nil {
		return Definition{}, http.StatusNotFound, "no deployed definition with that key"
	}
	if d.ApplicationID == "" {
		return Definition{}, http.StatusConflict, "a definition deployed outside an application publishes no interface: the application is who answers for it (ADR-0128)"
	}
	if !s.authEnabled() {
		return d, 0, ""
	}
	rank, err := s.host.Rank(p, d.ApplicationID)
	switch {
	case err != nil:
		return Definition{}, http.StatusInternalServerError, "published interfaces: " + err.Error()
	case rank == RankNone:
		return Definition{}, http.StatusNotFound, "no deployed definition with that key"
	case rank < min:
		return Definition{}, http.StatusForbidden, "insufficient application access"
	}
	return d, 0, ""
}

// --- writes ------------------------------------------------------------------

// gateWrite refuses a change nobody could be recorded as having made: a server
// without authentication has no actor for the audit, and a grant would name
// identities it does not have.
func (s *Service) gateWrite(w http.ResponseWriter, r *http.Request) (*httpapi.Principal, bool) {
	if !s.authEnabled() {
		httpapi.Error(w, http.StatusForbidden,
			"publishing an interface needs authentication: this server runs without it, so nobody could be recorded as having published, and no grant could name anybody")
		return nil, false
	}
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil || !validID(p.UserID) {
		httpapi.Error(w, http.StatusUnauthorized, "authentication required")
		return nil, false
	}
	return p, true
}

// decode reads a body strictly: one JSON object, no unknown field, no trailing data,
// bounded by the installation's Request budget.
func decode(r *http.Request, limit int64, v any) (int, string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return http.StatusBadRequest, "read body: " + err.Error()
	}
	if int64(len(body)) > limit {
		return http.StatusRequestEntityTooLarge, "the body is at most " + strconv.FormatInt(limit, 10) + " bytes"
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return http.StatusBadRequest, "invalid body: " + err.Error()
	}
	if dec.More() {
		return http.StatusBadRequest, "invalid body: trailing data after the JSON object"
	}
	return 0, ""
}

// pickRequest is one entry point or outcome as a request names it. Commitment is
// read only to be refused by name: an unknown-field error would not say why.
type pickRequest struct {
	Name       string          `json:"name"`
	ElementID  string          `json:"elementId"`
	ElementIDs []string        `json:"elementIds"`
	Commitment json.RawMessage `json:"commitment"`
}

func picks(in []pickRequest) ([]Pick, string) {
	out := make([]Pick, 0, len(in))
	for _, p := range in {
		if len(p.Commitment) > 0 {
			return nil, "entry point " + p.Name + " states a commitment; this server does not measure commitments yet, and a promise nobody measures would read as kept — publish without one (ADR-0373)"
		}
		out = append(out, Pick{Name: p.Name, ElementID: p.ElementID, ElementIDs: p.ElementIDs})
	}
	return out, ""
}

type publishRequest struct {
	DefinitionKey    uint64        `json:"definitionKey"`
	EntryPoints      []pickRequest `json:"entryPoints"`
	Outcomes         []pickRequest `json:"outcomes"`
	Replies          []string      `json:"replies"`
	ExpectedRevision *uint64       `json:"expectedRevision"`
}

// MutationView is a write's answer: the interface as it now stands, and the version
// the write concerned.
type MutationView struct {
	InterfaceView
	Version int `json:"version"`
}

// refusal is a write refused for reasons the publisher can act on.
type refusal struct {
	code     int
	msg      string
	problems []string
	current  uint64
}

func (s *Service) answer(w http.ResponseWriter, f refusal, res MutationView) {
	switch {
	case f.code == 0:
		httpapi.JSON(w, http.StatusOK, res)
	case f.code == http.StatusServiceUnavailable:
		s.unavailable(w)
	case len(f.problems) > 0:
		httpapi.JSON(w, f.code, map[string]any{"error": f.msg, "problems": f.problems})
	case f.code == http.StatusConflict && f.msg == "":
		httpapi.JSON(w, http.StatusConflict, map[string]any{
			"error":           "another change to this interface came first; read it again and decide on its current revision",
			"currentRevision": f.current,
		})
	default:
		httpapi.Error(w, f.code, f.msg)
	}
}

func writeRefusal(err error, current uint64) refusal {
	switch {
	case errors.Is(err, ErrStaleRevision):
		return refusal{code: http.StatusConflict, current: current}
	case errors.Is(err, ErrUncertainWrite):
		return refusal{code: http.StatusServiceUnavailable}
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNoVersion):
		return refusal{code: http.StatusNotFound, msg: err.Error()}
	case errors.Is(err, ErrOtherProcess), errors.Is(err, ErrSameContract), errors.Is(err, ErrTooManyVersions),
		errors.Is(err, ErrDeprecated):
		return refusal{code: http.StatusConflict, msg: err.Error()}
	default:
		// The write failed and its outcome on disk is unknown, so the store now refuses
		// every answer until a restart reads back what is actually there.
		return refusal{code: http.StatusInternalServerError, msg: "published interface not saved: " + err.Error()}
	}
}

// HandlePublish publishes a new contract version derived from one deployed
// definition, creating the interface on its first version. The publisher names
// which elements are entry points and what every terminal outcome is called; kind,
// correlation key, payload, errors and escalations are read from the model. The
// caller needs an editor's role on the application the definition is filed under.
func (s *Service) HandlePublish(w http.ResponseWriter, r *http.Request) {
	p, ok := s.gateWrite(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !ValidID(id) {
		httpapi.Error(w, http.StatusBadRequest, "invalid interface id: a lower-case slug such as kreditpruefung, at most 64 characters")
		return
	}
	var req publishRequest
	if code, msg := decode(r, s.Limits.Request, &req); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	if req.ExpectedRevision == nil {
		httpapi.Error(w, http.StatusBadRequest, "expectedRevision is required: 0 for a new interface, else the revision you read")
		return
	}
	entries, bad := picks(req.EntryPoints)
	if bad == "" {
		var outcomes []Pick
		if outcomes, bad = picks(req.Outcomes); bad == "" {
			s.publish(w, p, id, req, Selection{EntryPoints: entries, Outcomes: outcomes, Replies: req.Replies})
			return
		}
	}
	httpapi.Error(w, http.StatusBadRequest, bad)
}

func (s *Service) publish(w http.ResponseWriter, p *httpapi.Principal, id string, req publishRequest, sel Selection) {
	var (
		f   refusal
		res MutationView
	)
	s.loop.Do(func() {
		if s.store.Uncertain() {
			f.code = http.StatusServiceUnavailable
			return
		}
		def, code, msg := s.definitionFor(p, req.DefinitionKey, RankEditor)
		if code != 0 {
			f.code, f.msg = code, msg
			return
		}
		cur, exists := s.store.Get(id)
		f.current = cur.Revision
		if exists && cur.ApplicationID != def.ApplicationID {
			f.code, f.msg = http.StatusConflict, "the interface belongs to another application"
			return
		}
		contract, binding, problems := Realize(Derive(def.Process, nil), def.ProcessID, def.Key, def.Version, sel)
		if len(problems) > 0 {
			f.code, f.msg, f.problems = http.StatusBadRequest, "the selection does not make a contract", problems
			return
		}
		in, err := s.store.Publish(id, def.ApplicationID, contract, binding,
			Meta{ActorID: p.UserID, At: s.now(), ExpectedRevision: *req.ExpectedRevision})
		if err != nil {
			f = writeRefusal(err, cur.Revision)
			return
		}
		a, err := s.accessOn(p, in)
		if err != nil {
			f.code, f.msg = http.StatusInternalServerError, "published interfaces: "+err.Error()
			return
		}
		res = MutationView{InterfaceView: s.interfaceView(in, a), Version: in.Latest().Version}
	})
	s.answer(w, f, res)
}

// version reads the path's contract version.
func versionFrom(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	id := r.PathValue("id")
	version, err := strconv.Atoi(r.PathValue("version"))
	if !ValidID(id) || err != nil || version < 1 {
		httpapi.Error(w, http.StatusBadRequest, "invalid interface id or contract version")
		return "", 0, false
	}
	return id, version, true
}

// loadForWrite is a write's first step on the loop: the interface exists, the
// caller sees it, and holds at least min on its application.
func (s *Service) loadForWrite(p *httpapi.Principal, id string, min int) (Interface, refusal) {
	if s.store.Uncertain() {
		return Interface{}, refusal{code: http.StatusServiceUnavailable}
	}
	in, ok := s.store.Get(id)
	if !ok {
		return Interface{}, refusal{code: http.StatusNotFound, msg: ErrNotFound.Error()}
	}
	a, err := s.accessOn(p, in)
	switch {
	case err != nil:
		return Interface{}, refusal{code: http.StatusInternalServerError, msg: "published interfaces: " + err.Error()}
	case !a.visible:
		return Interface{}, refusal{code: http.StatusNotFound, msg: ErrNotFound.Error()}
	case a.rank < min:
		return Interface{}, refusal{code: http.StatusForbidden, msg: "insufficient application access"}
	}
	return in, refusal{current: in.Revision}
}

type rebindRequest struct {
	DefinitionKey    uint64        `json:"definitionKey"`
	EntryPoints      []pickRequest `json:"entryPoints"`
	Outcomes         []pickRequest `json:"outcomes"`
	ExpectedRevision *uint64       `json:"expectedRevision"`
}

// HandleRebind points one contract version at another deployed definition of the
// same process — the publisher's refactoring behind an unchanged contract. The new
// definition must realize exactly the version's contract; the element mapping
// defaults to the current one and may be given anew where an element was renamed.
// A contract that differs is refused with what differs: that is a new version.
func (s *Service) HandleRebind(w http.ResponseWriter, r *http.Request) {
	p, ok := s.gateWrite(w, r)
	if !ok {
		return
	}
	id, version, ok := versionFrom(w, r)
	if !ok {
		return
	}
	var req rebindRequest
	if code, msg := decode(r, s.Limits.Request, &req); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	if req.ExpectedRevision == nil {
		httpapi.Error(w, http.StatusBadRequest, "expectedRevision is required: the revision you read")
		return
	}
	entries, bad := picks(req.EntryPoints)
	var outcomes []Pick
	if bad == "" {
		outcomes, bad = picks(req.Outcomes)
	}
	if bad != "" {
		httpapi.Error(w, http.StatusBadRequest, bad)
		return
	}
	var (
		f   refusal
		res MutationView
	)
	s.loop.Do(func() {
		in, fr := s.loadForWrite(p, id, RankEditor)
		if f = fr; f.code != 0 {
			return
		}
		v, found := in.VersionOf(version)
		if !found {
			f = refusal{code: http.StatusNotFound, msg: ErrNoVersion.Error()}
			return
		}
		def, code, msg := s.definitionFor(p, req.DefinitionKey, RankEditor)
		switch {
		case code != 0:
			f.code, f.msg = code, msg
			return
		case def.ProcessID != in.ProcessID:
			f.code, f.msg = http.StatusConflict, ErrOtherProcess.Error()
			return
		case def.ApplicationID != in.ApplicationID:
			f.code, f.msg = http.StatusConflict, "the definition is filed under another application than the interface"
			return
		}
		sel := SelectionOf(v.Binding, v.Contract.Returns.Messages)
		if len(entries) > 0 {
			sel.EntryPoints = entries
		}
		if len(outcomes) > 0 {
			sel.Outcomes = outcomes
		}
		contract, binding, problems := Realize(Derive(def.Process, nil), def.ProcessID, def.Key, def.Version, sel)
		if len(problems) == 0 && !contract.Equal(v.Contract) {
			problems = contract.Differences(v.Contract)
			problems = append([]string{"the definition realizes another contract than version " + strconv.Itoa(version) + "; publish a new version instead"}, problems...)
		}
		if len(problems) > 0 {
			f.code, f.msg, f.problems = http.StatusConflict, "the definition does not realize this contract version", problems
			return
		}
		next, err := s.store.Rebind(id, version, binding,
			Meta{ActorID: p.UserID, At: s.now(), ExpectedRevision: *req.ExpectedRevision})
		if err != nil {
			f = writeRefusal(err, in.Revision)
			return
		}
		a, err := s.accessOn(p, next)
		if err != nil {
			f.code, f.msg = http.StatusInternalServerError, "published interfaces: "+err.Error()
			return
		}
		res = MutationView{InterfaceView: s.interfaceView(next, a), Version: version}
	})
	s.answer(w, f, res)
}

type deprecateRequest struct {
	Note             string  `json:"note"`
	ExpectedRevision *uint64 `json:"expectedRevision"`
}

func validNote(s string) bool {
	if len(s) > maxNote {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' {
			return false
		}
	}
	return true
}

// HandleDeprecate marks one contract version deprecated (the vocabulary ADR-0130 has
// for process versions). The version stays readable and deliverable; deprecation
// tells a consumer to move.
func (s *Service) HandleDeprecate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.gateWrite(w, r)
	if !ok {
		return
	}
	id, version, ok := versionFrom(w, r)
	if !ok {
		return
	}
	var req deprecateRequest
	if code, msg := decode(r, s.Limits.Request, &req); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	note := strings.TrimSpace(req.Note)
	switch {
	case req.ExpectedRevision == nil:
		httpapi.Error(w, http.StatusBadRequest, "expectedRevision is required: the revision you read")
		return
	case !validNote(note):
		httpapi.Error(w, http.StatusBadRequest, "a deprecation note is plain text of at most "+strconv.Itoa(maxNote)+" bytes")
		return
	}
	var (
		f   refusal
		res MutationView
	)
	s.loop.Do(func() {
		in, fr := s.loadForWrite(p, id, RankEditor)
		if f = fr; f.code != 0 {
			return
		}
		next, err := s.store.Deprecate(id, version, note,
			Meta{ActorID: p.UserID, At: s.now(), ExpectedRevision: *req.ExpectedRevision})
		if err != nil {
			f = writeRefusal(err, in.Revision)
			return
		}
		a, err := s.accessOn(p, next)
		if err != nil {
			f.code, f.msg = http.StatusInternalServerError, "published interfaces: "+err.Error()
			return
		}
		res = MutationView{InterfaceView: s.interfaceView(next, a), Version: version}
	})
	s.answer(w, f, res)
}

type grantsRequest struct {
	Public           bool     `json:"public"`
	View             Audience `json:"view"`
	Send             Audience `json:"send"`
	ExpectedRevision *uint64  `json:"expectedRevision"`
}

// HandleSetGrants wholly replaces who may view and who may send. It is the
// application owner's act, from their own signed-in session, as sharing an
// application is: a send grant lets another domain drive work into this one.
func (s *Service) HandleSetGrants(w http.ResponseWriter, r *http.Request) {
	p, ok := s.gateWrite(w, r)
	if !ok {
		return
	}
	if !p.Session {
		httpapi.Error(w, http.StatusForbidden, "an interface's grants are changed by its application owner signed in with their own session")
		return
	}
	id := r.PathValue("id")
	if !ValidID(id) {
		httpapi.Error(w, http.StatusBadRequest, "invalid interface id")
		return
	}
	var req grantsRequest
	if code, msg := decode(r, s.Limits.Request, &req); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	if req.ExpectedRevision == nil {
		httpapi.Error(w, http.StatusBadRequest, "expectedRevision is required: the revision you read")
		return
	}
	view, err := CheckAudience(req.View)
	if err == nil {
		var send Audience
		if send, err = CheckAudience(req.Send); err == nil {
			s.setGrants(w, p, id, Grants{Public: req.Public, View: view, Send: send}, *req.ExpectedRevision)
			return
		}
	}
	httpapi.Error(w, http.StatusBadRequest, err.Error())
}

func (s *Service) setGrants(w http.ResponseWriter, p *httpapi.Principal, id string, g Grants, expected uint64) {
	var (
		f   refusal
		res MutationView
	)
	s.loop.Do(func() {
		in, fr := s.loadForWrite(p, id, RankOwner)
		if f = fr; f.code != 0 {
			return
		}
		if missing, err := s.unknownIDs(g); err != nil {
			f.code, f.msg = http.StatusInternalServerError, "published interfaces: "+err.Error()
			return
		} else if missing != "" {
			f.code, f.msg = http.StatusBadRequest, missing
			return
		}
		next, err := s.store.SetGrants(id, g, Meta{ActorID: p.UserID, At: s.now(), ExpectedRevision: expected})
		if err != nil {
			f = writeRefusal(err, in.Revision)
			return
		}
		a, err := s.accessOn(p, next)
		if err != nil {
			f.code, f.msg = http.StatusInternalServerError, "published interfaces: "+err.Error()
			return
		}
		res = MutationView{InterfaceView: s.interfaceView(next, a)}
	})
	s.answer(w, f, res)
}

// unknownIDs names the user and group ids of a grant that resolve to nobody now.
// Called on the loop.
func (s *Service) unknownIDs(g Grants) (string, error) {
	var users, groups []string
	seenU, seenG := map[string]bool{}, map[string]bool{}
	for _, a := range []Audience{g.View, g.Send} {
		for _, id := range a.UserIDs {
			if seenU[id] {
				continue
			}
			seenU[id] = true
			ok, err := s.host.UserExists(id)
			if err != nil {
				return "", err
			}
			if !ok {
				users = append(users, id)
			}
		}
		for _, id := range a.GroupIDs {
			if seenG[id] {
				continue
			}
			seenG[id] = true
			ok, err := s.host.GroupExists(id)
			if err != nil {
				return "", err
			}
			if !ok {
				groups = append(groups, id)
			}
		}
	}
	var parts []string
	if len(users) > 0 {
		parts = append(parts, "no account with user id "+strings.Join(users, ", "))
	}
	if len(groups) > 0 {
		parts = append(parts, "no group with id "+strings.Join(groups, ", "))
	}
	return strings.Join(parts, "; "), nil
}
