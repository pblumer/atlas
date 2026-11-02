package startgrant

// The administration half of ADR-0516: the routes an administrator reads, issues,
// revokes and previews a process-scoped start grant through.
//
// It is a per-area service (ADR-0147). The store it administers is owned by the
// run loop it holds, and every collaborator the rest of the server supplies — the
// identity directory and what the server knows about an application and its
// process — is called only from inside that loop's turn. So a check and the write
// it guards happen in one turn: an account deleted, a process redeployed into
// another application or a competing administrator's change cannot slip between
// them (I3, ADR-0002). Nothing here reaches the processor, the WAL or recovery.
//
// What it deliberately is not: a self-service route. Issuing a grant here makes
// nobody able to start anything until the authenticated self-service routes of
// ADR-0516 exist and check this store on every request.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/limits"
)

// Account is what the directory says about a person a grant or a preview names.
type Account struct {
	// Disabled accounts cannot sign in, so a grant to one starts nothing.
	Disabled bool
	// SelfService reports whether the account holds a role the self-service start
	// routes are declared for (user, or admin, which reaches every route). A grant
	// is a second key beside that role, never a replacement for it.
	SelfService bool
}

// Directory resolves the identities a grant names. Every function is called on the
// run loop only. Identities are looked up by id, never by a display or user name:
// a name can be changed or reused, an id cannot.
type Directory struct {
	User     func(id string) (Account, bool, error)
	Group    func(id string) (bool, error)
	GroupsOf func(userID string) ([]string, error)
}

// Blocker codes say why a granted process would still not be offered for a start.
// They are stable strings so the Console and tests can read them.
const (
	BlockApplicationMissing   = "application-missing"
	BlockApplicationProtected = "application-protected"
	BlockNotDeployed          = "not-deployed"
	BlockOtherApplication     = "newest-version-in-other-application"
	BlockNotExecutable        = "not-executable"
	BlockInactive             = "inactive"
	BlockTriggerOnly          = "trigger-only"
	BlockConstantsMissing     = "constants-missing"
)

// Target is what the server knows, now, about one application/process pair.
type Target struct {
	ApplicationFound     bool
	ApplicationProtected bool
	// ProcessInApplication reports that the newest deployed version of the process
	// id is filed under this application. A grant is issued only then: a process id
	// another application deployed last is not this application's to hand out.
	ProcessInApplication bool
	// DefinitionKey and Version name that newest deployed version, zero when the
	// process id is not deployed.
	DefinitionKey uint64
	Version       int32
	// Blockers say why the newest version would not be offered for a self-service
	// start; empty means it would be.
	Blockers []string
}

// Service serves the start-grant administration routes. Build it with
// [NewService].
type Service struct {
	loop        *runloop.Loop
	store       *Store
	dir         Directory
	resolve     func(applicationID, processID string) (Target, error)
	authEnabled func() bool
	now         func() int64

	// Limits are the installation's resource budgets. NewService sets them to
	// [limits.Default]; the server overwrites them with its own once it has read the
	// environment (ADR-0291). A grant body is one ordinary JSON request.
	Limits limits.Limits
}

// NewService builds the administration service over the store. resolve and every
// function of dir read state the loop owns and are only called from inside its
// turn. authEnabled reports whether the server enforces authentication; it is read
// per request because the server settles it after constructing its services. now
// stamps an audit entry, in Unix seconds; the actor and the time are the server's,
// never the client's.
func NewService(loop *runloop.Loop, store *Store, dir Directory,
	resolve func(applicationID, processID string) (Target, error), authEnabled func() bool, now func() int64) *Service {
	return &Service{loop: loop, store: store, dir: dir, resolve: resolve, authEnabled: authEnabled, now: now,
		Limits: limits.Default()}
}

// GrantView is one scope's current state as the administration routes answer it.
type GrantView struct {
	ApplicationID string `json:"applicationId"`
	ProcessID     string `json:"processId"`
	// Revision is the per-scope revision a change must name as expectedRevision;
	// zero when nothing was ever granted for this scope.
	Revision  uint64   `json:"revision"`
	Granted   bool     `json:"granted"`
	UserIDs   []string `json:"userIds"`
	GroupIDs  []string `json:"groupIds"`
	ChangedAt int64    `json:"changedAt,omitempty"`
	ChangedBy string   `json:"changedBy,omitempty"`
}

// ChangeView is one audited change as the history route answers it.
type ChangeView struct {
	Revision uint64   `json:"revision"`
	At       int64    `json:"at"`
	ActorID  string   `json:"actorId"`
	Action   string   `json:"action"`
	UserIDs  []string `json:"userIds"`
	GroupIDs []string `json:"groupIds"`
}

// MutationView is a write's answer: the scope as it now stands, and whether this
// request changed it. Changed is false for a revoke of what is already revoked and
// for a grant of exactly the audience already granted — neither writes an audit
// entry, so a retried request does not pile up history.
type MutationView struct {
	GrantView
	Changed bool `json:"changed"`
}

// PreviewView answers whether one person or group would be offered the process.
type PreviewView struct {
	ApplicationID string   `json:"applicationId"`
	ProcessID     string   `json:"processId"`
	SubjectKind   string   `json:"subjectKind"`
	SubjectID     string   `json:"subjectId"`
	Revision      uint64   `json:"revision"`
	Granted       bool     `json:"granted"`
	MatchedBy     []string `json:"matchedBy"`
	AccountIssues []string `json:"accountIssues"`
	Startable     bool     `json:"startable"`
	Blockers      []string `json:"blockers"`
	DefinitionKey uint64   `json:"definitionKey,omitempty"`
	Version       int32    `json:"version,omitempty"`
	// WouldBeOffered is the conjunction the self-service list applies: granted, an
	// account that can sign in and holds the role, and a process that is startable.
	// A machine credential never qualifies, whatever it carries (ADR-0516).
	WouldBeOffered bool `json:"wouldBeOffered"`
}

func viewOf(applicationID, processID string, c Change, ok bool) GrantView {
	v := GrantView{ApplicationID: applicationID, ProcessID: processID, UserIDs: []string{}, GroupIDs: []string{}}
	if !ok {
		return v
	}
	v.Revision = c.Revision
	v.Granted = c.Action == ActionGrant
	v.ChangedAt = c.At
	v.ChangedBy = c.ActorID
	if c.Grant.UserIDs != nil {
		v.UserIDs = c.Grant.UserIDs
	}
	if c.Grant.GroupIDs != nil {
		v.GroupIDs = c.Grant.GroupIDs
	}
	return v
}

// gate answers the requests this area refuses before reading anything: a server
// without authentication, where nobody could be the audited actor and the
// self-service routes that would read a grant do not exist (ADR-0516), and a
// caller with no principal. The admin role itself is the route table's to demand.
func (s *Service) gate(w http.ResponseWriter, r *http.Request) (*httpapi.Principal, bool) {
	if !s.authEnabled() {
		httpapi.Error(w, http.StatusForbidden,
			"start grants need authentication: this server runs without it, so nobody could be recorded as having granted or revoked a start")
		return nil, false
	}
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil {
		httpapi.Error(w, http.StatusUnauthorized, "authentication required")
		return nil, false
	}
	return p, true
}

// gateWrite is gate for a change: the actor must be a person signed in with their
// own session. An API token never carries admin, and an application a person
// granted access to acts for them but is not them; changing who may start work is
// the person's own act, like answering an approval (ADR-0475 §10).
func (s *Service) gateWrite(w http.ResponseWriter, r *http.Request) (*httpapi.Principal, bool) {
	p, ok := s.gate(w, r)
	if !ok {
		return nil, false
	}
	if !p.Session || !validID(p.UserID) {
		httpapi.Error(w, http.StatusForbidden, "a start grant is changed by an administrator signed in with their own session")
		return nil, false
	}
	return p, true
}

// scopeFrom reads and checks the path's application and process ids.
func scopeFrom(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	app, proc := r.PathValue("id"), r.PathValue("processId")
	if !validID(app) || !validID(proc) {
		httpapi.Error(w, http.StatusBadRequest, "invalid application or process id")
		return "", "", false
	}
	return app, proc, true
}

func (s *Service) unavailable(w http.ResponseWriter) {
	httpapi.Error(w, http.StatusServiceUnavailable,
		"start grants are unavailable until the server restarts: an earlier write may or may not have reached the disk, so no grant is trusted until it is read back")
}

// HandleList answers every scope of one application that was ever granted,
// revoked ones included, ordered by process id.
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	app := r.PathValue("id")
	if !validID(app) {
		httpapi.Error(w, http.StatusBadRequest, "invalid application id")
		return
	}
	var (
		items     []GrantView
		found     bool
		uncertain bool
		err       error
	)
	s.loop.Do(func() {
		if uncertain = s.store.Uncertain(); uncertain {
			return
		}
		changes := s.store.ForApplication(app)
		var t Target
		if t, err = s.resolve(app, ""); err != nil {
			return
		}
		found = t.ApplicationFound || len(changes) > 0
		items = make([]GrantView, 0, len(changes))
		for _, c := range changes {
			items = append(items, viewOf(app, c.Grant.ProcessID, c, true))
		}
	})
	switch {
	case uncertain:
		s.unavailable(w)
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "start grants: "+err.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no application with that id")
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// read is the shared first half of the single-scope reads: the scope's current
// change and whether the scope is addressable at all — an application that exists,
// or one that is gone but still has a grant record an administrator must be able to
// see and revoke.
func (s *Service) read(applicationID, processID string) (c Change, ok bool, t Target, found, uncertain bool, err error) {
	if uncertain = s.store.Uncertain(); uncertain {
		return
	}
	c, ok = s.store.Current(applicationID, processID)
	if t, err = s.resolve(applicationID, processID); err != nil {
		return
	}
	found = t.ApplicationFound || ok
	return
}

// HandleGet answers one scope's current grant. A scope never granted answers
// revision 0 and an empty audience: deny is the default, not a missing record.
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	app, proc, ok := scopeFrom(w, r)
	if !ok {
		return
	}
	var (
		c                     Change
		has, found, uncertain bool
		err                   error
	)
	s.loop.Do(func() { c, has, _, found, uncertain, err = s.read(app, proc) })
	switch {
	case uncertain:
		s.unavailable(w)
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "start grants: "+err.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no application with that id")
	default:
		httpapi.JSON(w, http.StatusOK, viewOf(app, proc, c, has))
	}
}

// HandleHistory answers every audited change of one scope, newest first.
func (s *Service) HandleHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	app, proc, ok := scopeFrom(w, r)
	if !ok {
		return
	}
	var (
		history          []Change
		found, uncertain bool
		err              error
	)
	s.loop.Do(func() {
		if _, _, _, found, uncertain, err = s.read(app, proc); found && !uncertain && err == nil {
			history = s.store.History(app, proc)
		}
	})
	switch {
	case uncertain:
		s.unavailable(w)
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "start grants: "+err.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no application with that id")
	default:
		items := make([]ChangeView, 0, len(history))
		for i := len(history) - 1; i >= 0; i-- {
			c := history[i]
			items = append(items, ChangeView{
				Revision: c.Revision, At: c.At, ActorID: c.ActorID, Action: c.Action,
				UserIDs: nonNil(c.Grant.UserIDs), GroupIDs: nonNil(c.Grant.GroupIDs),
			})
		}
		httpapi.JSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
	}
}

func nonNil(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

// putRequest is the body of a grant. The scope is the path's; a body that names one
// must name the same. The audit fields — actor, time, revision — are the server's,
// so a body carrying them is refused as malformed rather than silently ignored.
type putRequest struct {
	ApplicationID    *string  `json:"applicationId"`
	ProcessID        *string  `json:"processId"`
	UserIDs          []string `json:"userIds"`
	GroupIDs         []string `json:"groupIds"`
	ExpectedRevision *uint64  `json:"expectedRevision"`
}

// decodePut reads a grant body strictly: one JSON object, no unknown field, no
// trailing data, bounded by the installation's Request budget.
func decodePut(r *http.Request, limit int64) (putRequest, int, string) {
	var req putRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return req, http.StatusBadRequest, "read body: " + err.Error()
	}
	if int64(len(body)) > limit {
		return req, http.StatusRequestEntityTooLarge, "a start grant body is at most " + strconv.FormatInt(limit, 10) + " bytes"
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, http.StatusBadRequest, "invalid start grant: " + err.Error()
	}
	if dec.More() {
		return req, http.StatusBadRequest, "invalid start grant: trailing data after the JSON object"
	}
	return req, 0, ""
}

// checkAudience refuses an empty, oversized, malformed or repeating audience, and
// answers it sorted, so a grant is stored and compared in one canonical order.
func checkAudience(users, groups []string) ([]string, []string, string) {
	if len(users)+len(groups) == 0 {
		return nil, nil, "a start grant names at least one user or group id; DELETE revokes it"
	}
	if len(users)+len(groups) > maxAudience {
		return nil, nil, "a start grant names at most " + strconv.Itoa(maxAudience) + " user and group ids"
	}
	check := func(kind string, ids []string) ([]string, string) {
		out := append([]string(nil), ids...)
		sort.Strings(out)
		for i, id := range out {
			if !validID(id) {
				return nil, "invalid " + kind + " id " + strconv.Quote(id)
			}
			if i > 0 && out[i-1] == id {
				return nil, "duplicate " + kind + " id " + strconv.Quote(id)
			}
		}
		return out, ""
	}
	u, bad := check("user", users)
	if bad != "" {
		return nil, nil, bad
	}
	g, bad := check("group", groups)
	if bad != "" {
		return nil, nil, bad
	}
	return u, g, ""
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// HandlePut issues or wholly replaces one scope's grant. The application must
// exist and not be a protected system application, the newest deployed version of
// the process must be filed under it, and every user and group id must name an
// account or group that exists now. All of it is checked in the same loop turn as
// the write, which is durable — written and fsynced — before the answer says so.
func (s *Service) HandlePut(w http.ResponseWriter, r *http.Request) {
	p, ok := s.gateWrite(w, r)
	if !ok {
		return
	}
	app, proc, ok := scopeFrom(w, r)
	if !ok {
		return
	}
	req, code, msg := decodePut(r, s.Limits.Request)
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	if (req.ApplicationID != nil && *req.ApplicationID != app) || (req.ProcessID != nil && *req.ProcessID != proc) {
		httpapi.Error(w, http.StatusBadRequest, "the body names another application or process than the path")
		return
	}
	if req.ExpectedRevision == nil {
		httpapi.Error(w, http.StatusBadRequest, "expectedRevision is required: 0 for a scope never granted, else the revision you read")
		return
	}
	users, groups, bad := checkAudience(req.UserIDs, req.GroupIDs)
	if bad != "" {
		httpapi.Error(w, http.StatusBadRequest, bad)
		return
	}
	var (
		res     MutationView
		current uint64
	)
	code, msg = 0, ""
	s.loop.Do(func() {
		if s.store.Uncertain() {
			code = http.StatusServiceUnavailable
			return
		}
		t, err := s.resolve(app, proc)
		switch {
		case err != nil:
			code, msg = http.StatusInternalServerError, "start grants: "+err.Error()
			return
		case !t.ApplicationFound:
			code, msg = http.StatusNotFound, "no application with that id"
			return
		case t.ApplicationProtected:
			code, msg = http.StatusConflict, "a protected system application's processes are not granted to people to start"
			return
		case !t.ProcessInApplication:
			code, msg = http.StatusNotFound, "no deployed process "+strconv.Quote(proc)+" whose newest version is filed under this application"
			return
		}
		if missing, err := s.unknownIDs(users, groups); err != nil {
			code, msg = http.StatusInternalServerError, "start grants: "+err.Error()
			return
		} else if missing != "" {
			code, msg = http.StatusBadRequest, missing
			return
		}
		cur, has := s.store.Current(app, proc)
		current = cur.Revision
		if has && cur.Revision == *req.ExpectedRevision && cur.Action == ActionGrant &&
			sameIDs(cur.Grant.UserIDs, users) && sameIDs(cur.Grant.GroupIDs, groups) {
			res = MutationView{GrantView: viewOf(app, proc, cur, true)}
			return
		}
		change, err := s.store.Set(ChangeRequest{
			Grant:   Grant{ApplicationID: app, ProcessID: proc, UserIDs: users, GroupIDs: groups},
			ActorID: p.UserID, At: s.now(), ExpectedRevision: *req.ExpectedRevision,
		})
		code, msg = writeOutcome(err)
		if code == 0 {
			res = MutationView{GrantView: viewOf(app, proc, change, true), Changed: true}
		}
	})
	s.answerWrite(w, code, msg, current, res)
}

// unknownIDs names the user and group ids that resolve to nobody. Called on the loop.
func (s *Service) unknownIDs(users, groups []string) (string, error) {
	var missingUsers, missingGroups []string
	for _, id := range users {
		if _, ok, err := s.dir.User(id); err != nil {
			return "", err
		} else if !ok {
			missingUsers = append(missingUsers, id)
		}
	}
	for _, id := range groups {
		if ok, err := s.dir.Group(id); err != nil {
			return "", err
		} else if !ok {
			missingGroups = append(missingGroups, id)
		}
	}
	var parts []string
	if len(missingUsers) > 0 {
		parts = append(parts, "no account with user id "+strings.Join(missingUsers, ", "))
	}
	if len(missingGroups) > 0 {
		parts = append(parts, "no group with id "+strings.Join(missingGroups, ", "))
	}
	return strings.Join(parts, "; "), nil
}

// writeOutcome maps a store write's error onto the answer it owes.
func writeOutcome(err error) (int, string) {
	switch {
	case err == nil:
		return 0, ""
	case errors.Is(err, ErrStaleRevision):
		return http.StatusConflict, ""
	case errors.Is(err, ErrUncertainWrite):
		return http.StatusServiceUnavailable, ""
	default:
		// The write failed and its outcome on disk is unknown, so the store now refuses
		// every grant until a restart reads back what is actually there.
		return http.StatusInternalServerError, "start grant not saved: " + err.Error()
	}
}

func (s *Service) answerWrite(w http.ResponseWriter, code int, msg string, current uint64, res MutationView) {
	switch code {
	case 0:
		httpapi.JSON(w, http.StatusOK, res)
	case http.StatusConflict:
		if msg == "" {
			httpapi.JSON(w, http.StatusConflict, map[string]any{
				"error":           "another change to this start grant came first; read it again and decide on its current revision",
				"currentRevision": current,
			})
			return
		}
		httpapi.Error(w, code, msg)
	case http.StatusServiceUnavailable:
		s.unavailable(w)
	default:
		httpapi.Error(w, code, msg)
	}
}

// HandleDelete revokes one scope's grant. A revocation takes effect for every
// authorization check after it is durable; a start authorized before it may still
// finish (ADR-0516's prospective ordering). Revoking needs no application or
// process to exist any more — taking a right away must always be possible — and
// revoking what is already revoked writes nothing.
func (s *Service) HandleDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.gateWrite(w, r)
	if !ok {
		return
	}
	app, proc, ok := scopeFrom(w, r)
	if !ok {
		return
	}
	expected, err := strconv.ParseUint(r.URL.Query().Get("expectedRevision"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "?expectedRevision= is required: the revision you read")
		return
	}
	var (
		res     MutationView
		current uint64
		code    int
		msg     string
	)
	s.loop.Do(func() {
		if s.store.Uncertain() {
			code = http.StatusServiceUnavailable
			return
		}
		cur, has := s.store.Current(app, proc)
		current = cur.Revision
		if !has || cur.Action == ActionRevoke {
			t, err := s.resolve(app, proc)
			switch {
			case err != nil:
				code, msg = http.StatusInternalServerError, "start grants: "+err.Error()
			case !has && !t.ApplicationFound:
				code, msg = http.StatusNotFound, "no application with that id"
			default:
				res = MutationView{GrantView: viewOf(app, proc, cur, has)}
			}
			return
		}
		change, err := s.store.Set(ChangeRequest{
			Grant:   Grant{ApplicationID: app, ProcessID: proc},
			ActorID: p.UserID, At: s.now(), ExpectedRevision: expected,
		})
		code, msg = writeOutcome(err)
		if code == 0 {
			res = MutationView{GrantView: viewOf(app, proc, change, true), Changed: true}
		}
	})
	s.answerWrite(w, code, msg, current, res)
}

// HandlePreview answers whether one account (?userId=) or one group (?groupId=)
// would be offered the process now, and why not. It reads the account's group
// membership from the directory — the same source a session's groups are kept
// current from (ADR-0185) — and writes nothing.
func (s *Service) HandlePreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	app, proc, ok := scopeFrom(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	userID, groupID := q.Get("userId"), q.Get("groupId")
	if (userID == "") == (groupID == "") {
		httpapi.Error(w, http.StatusBadRequest, "name exactly one of ?userId= or ?groupId=")
		return
	}
	kind, subject := "user", userID
	if groupID != "" {
		kind, subject = "group", groupID
	}
	if !validID(subject) {
		httpapi.Error(w, http.StatusBadRequest, "invalid "+kind+" id")
		return
	}
	var (
		res  PreviewView
		code int
		msg  string
	)
	s.loop.Do(func() {
		c, has, t, found, uncertain, err := s.read(app, proc)
		switch {
		case uncertain:
			code = http.StatusServiceUnavailable
			return
		case err != nil:
			code, msg = http.StatusInternalServerError, "start grants: "+err.Error()
			return
		case !found:
			code, msg = http.StatusNotFound, "no application with that id"
			return
		}
		granted := has && c.Action == ActionGrant
		res = PreviewView{
			ApplicationID: app, ProcessID: proc, SubjectKind: kind, SubjectID: subject,
			Revision: c.Revision, MatchedBy: []string{}, AccountIssues: []string{},
			Blockers: nonNil(t.Blockers), DefinitionKey: t.DefinitionKey, Version: t.Version,
		}
		res.Startable = len(res.Blockers) == 0
		if kind == "group" {
			exists, err := s.dir.Group(subject)
			switch {
			case err != nil:
				code, msg = http.StatusInternalServerError, "start grants: "+err.Error()
				return
			case !exists:
				code, msg = http.StatusNotFound, "no group with that id"
				return
			}
			if granted && contains(c.Grant.GroupIDs, subject) {
				res.MatchedBy = append(res.MatchedBy, "group:"+subject)
			}
		} else {
			acct, exists, err := s.dir.User(subject)
			switch {
			case err != nil:
				code, msg = http.StatusInternalServerError, "start grants: "+err.Error()
				return
			case !exists:
				code, msg = http.StatusNotFound, "no account with that user id"
				return
			}
			groups, err := s.dir.GroupsOf(subject)
			if err != nil {
				code, msg = http.StatusInternalServerError, "start grants: "+err.Error()
				return
			}
			if acct.Disabled {
				res.AccountIssues = append(res.AccountIssues, "disabled")
			}
			if !acct.SelfService {
				res.AccountIssues = append(res.AccountIssues, "missing-user-role")
			}
			if granted {
				if contains(c.Grant.UserIDs, subject) {
					res.MatchedBy = append(res.MatchedBy, "user")
				}
				for _, g := range c.Grant.GroupIDs {
					if contains(groups, g) {
						res.MatchedBy = append(res.MatchedBy, "group:"+g)
					}
				}
			}
		}
		res.Granted = len(res.MatchedBy) > 0
		res.WouldBeOffered = res.Granted && len(res.AccountIssues) == 0 && res.Startable
	})
	switch code {
	case 0:
		httpapi.JSON(w, http.StatusOK, res)
	case http.StatusServiceUnavailable:
		s.unavailable(w)
	default:
		httpapi.Error(w, code, msg)
	}
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
