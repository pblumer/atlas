package taskfolder

import (
	"encoding/json"
	"github.com/pblumer/atlas/limits"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// maxNameLen bounds a folder's name to what the sidebar can show.
const maxNameLen = 60

// Option is one entry of a value listbox: the value a condition stores, and the
// label a person picks it by. The label is model or directory data — a process's
// name, a person's display name — never interface text, so it needs no
// translation and the client owns every word it adds around it.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// Options are the value lists the editor's third control is filled from. They
// come from the deployments and the design-time stores, which the service cannot
// reach — the server supplies them through the collaborator given to [New].
type Options struct {
	Processes []Option `json:"processes"`
	TaskNames []Option `json:"taskNames"`
	Users     []Option `json:"users"`
	Groups    []Option `json:"groups"`
	Lanes     []Option `json:"lanes"`
	// MyGroups are the identity groups the caller belongs to (ADR-0180), which is
	// what the "shared with" control offers. It is deliberately not the same list as
	// Groups above: those are the candidate groups a *model* names, which is what a
	// rule filters on, and the two being different lists that both read "group" is
	// exactly the confusion worth spelling out here.
	MyGroups []Option `json:"myGroups"`
}

// Counts is what the sidebar needs in one call: how many open tasks each visible
// folder holds, and how many the scan looked at. Truncated says the scan hit its
// budget, so the numbers are a floor rather than a total — the sidebar says so
// instead of showing a confident wrong number.
type Counts struct {
	Folders   map[string]int `json:"folders"`
	Total     int            `json:"total"`
	Truncated bool           `json:"truncated"`
}

// CountFunc answers "how many open tasks does this rule match, out of how many".
// It scans the engine, which this package deliberately cannot reach: the server
// owns that scan and hands it in, the same way the documentation service is handed
// its deployment lookup.
type CountFunc func(matchers []*Matcher, u User) (perMatcher []int, total int, truncated bool, err error)

// Service serves the task-folder area (ADR-0268).
// Build it with [New].
type Service struct {
	// loop is the single-writer boundary: every store access below runs on it, and
	// there is no other route from here to shared state.
	loop  *runloop.Loop
	store *Store
	// compiled memoises one matcher per folder, keyed by the folder's UpdatedAt so
	// an edit invalidates its own entry without anybody having to remember to. Owned
	// by the loop.
	compiled map[string]cachedMatcher
	// options supplies the editor's value lists; it reads the deployment registry
	// and design-time stores, so it is only ever called from inside the loop. It
	// takes the viewer because one of those lists — the groups a folder may be
	// shared with — is the caller's own membership.
	options func(User) Options
	// count scans open tasks for the preview and the sidebar counters.
	count CountFunc
	// newID mints folder ids. Injected so a test can drive the service with
	// deterministic ids.
	newID func() (string, error)

	// Limits are the installation's resource budgets. New sets them to
	// [limits.Default]; the server overwrites them with its own once it has read the
	// environment, so every ceiling in this service is the one operators configured
	// (ADR-0291).
	Limits limits.Limits
}

type cachedMatcher struct {
	at int64
	m  *Matcher
}

// New builds the task-folder service. options and count are the collaborators the
// server supplies; options is called on the loop, count off it.
func New(loop *runloop.Loop, store *Store, options func(User) Options, count CountFunc, newID func() (string, error)) *Service {
	return &Service{
		loop:     loop,
		store:    store,
		compiled: map[string]cachedMatcher{},
		options:  options,
		count:    count,
		newID:    newID,
		Limits:   limits.Default(),
	}
}

// Viewer is who a request is acting as. With authentication on it is the signed-in
// principal, including the groups a shared folder is matched against. With
// authentication off there is no identity to own anything, so the folder set is
// shared and `?me=` carries only the display-only name the Tasks app already uses
// for claiming (ADR-0045) — enough for "assigned to me" to mean something, not
// enough to own a folder.
func Viewer(r *http.Request) User {
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		return User{ID: p.UserID, Name: p.Username, Groups: p.GroupIDs}
	}
	return User{Name: strings.TrimSpace(r.URL.Query().Get("me"))}
}

// owner is the user id a folder created by this request belongs to. It is the
// authenticated user id or nothing: a display-only name must not become an
// ownership claim.
func owner(r *http.Request) string {
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		return p.UserID
	}
	return ""
}

// folderResp is a folder as the API renders it: the stored record plus the two
// things the client would otherwise have to derive — the expression the rule
// generates, and whether this viewer may change it.
type folderResp struct {
	Folder
	FEEL     string `json:"feel"`
	Editable bool   `json:"editable"`
}

func toResp(f Folder, viewerID string) folderResp {
	return folderResp{Folder: f, FEEL: f.Rule.FEEL(), Editable: f.EditableBy(viewerID)}
}

// HandleList returns the folders this viewer may see, in sidebar order.
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	v := Viewer(r)
	var (
		folders []Folder
		loadErr error
	)
	s.loop.Do(func() { folders, loadErr = s.store.VisibleTo(v.ID, v.Groups) })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list task folders: "+loadErr.Error())
		return
	}
	out := make([]folderResp, 0, len(folders))
	for _, f := range folders {
		out = append(out, toResp(f, v.ID))
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// HandleFields describes what the editor can build: the field/operator catalogue
// and the value lists that fill each field's third control.
//
// It deliberately carries no interface text. Every string here is an id or a name
// that came out of a model or the directory, so the console renders the labels in
// whatever language it is showing and the server never has to be translated.
func (s *Service) HandleFields(w http.ResponseWriter, r *http.Request) {
	v := Viewer(r)
	var opts Options
	s.loop.Do(func() { opts = s.options(v) })
	httpapi.JSON(w, http.StatusOK, struct {
		Fields  []FieldSpec `json:"fields"`
		Options Options     `json:"options"`
	}{Fields: Catalog(), Options: opts})
}

// folderReq is the editable half of a folder. Owner, timestamps and id are the
// server's to decide, so they are absent here rather than ignored.
type folderReq struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	GroupID    string `json:"groupId"`
	Position   *int   `json:"position"`
	Rule       Rule   `json:"rule"`
}

// decode reads and checks a folder request body, answering the client directly on
// anything malformed. ok=false means a response has already been written.
func (s *Service) decode(w http.ResponseWriter, r *http.Request) (folderReq, bool) {
	var req folderReq
	if err := json.NewDecoder(io.LimitReader(r.Body, s.budgets().Request)).Decode(&req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return req, false
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httpapi.Error(w, http.StatusBadRequest, "a folder needs a name")
		return req, false
	}
	if len([]rune(req.Name)) > maxNameLen {
		httpapi.Error(w, http.StatusBadRequest, "a folder name may be at most 60 characters")
		return req, false
	}
	if req.Visibility == "" {
		req.Visibility = VisibilityPrivate
	}
	switch req.Visibility {
	case VisibilityPrivate, VisibilityOrg:
		req.GroupID = ""
	case VisibilityGroup:
		if strings.TrimSpace(req.GroupID) == "" {
			httpapi.Error(w, http.StatusBadRequest, "a folder shared with a group must name the group")
			return req, false
		}
	default:
		httpapi.Error(w, http.StatusBadRequest, "visibility must be private, group or org")
		return req, false
	}
	if req.Rule.Match == "" {
		req.Rule.Match = MatchAll
	}
	if req.Rule.Conditions == nil {
		req.Rule.Conditions = []Condition{}
	}
	// Compiling here, at save time, is what keeps the listing free of it: a folder
	// that reaches the store is one whose expression the FEEL compiler has already
	// accepted (invariant 5, ADR-0008).
	if _, err := Compile(req.Rule); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "the rule is not valid: "+err.Error())
		return req, false
	}
	return req, true
}

// HandleCreate stores a new folder for the calling identity.
func (s *Service) HandleCreate(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decode(w, r)
	if !ok {
		return
	}
	id, err := s.newID()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "mint folder id: "+err.Error())
		return
	}
	now := time.Now().UnixMilli()
	rec := Folder{
		ID: id, Name: req.Name, Owner: owner(r),
		Visibility: req.Visibility, GroupID: req.GroupID,
		Rule: req.Rule, CreatedAt: now, UpdatedAt: now,
	}
	var saveErr error
	s.loop.Do(func() {
		if req.Position != nil {
			rec.Position = *req.Position
		} else {
			// A new folder lands at the end of the person's own list, which is where
			// somebody who just created one looks for it.
			rec.Position = s.nextPosition(rec.Owner)
		}
		saveErr = s.store.Save(rec)
	})
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save folder: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, toResp(rec, Viewer(r).ID))
}

// nextPosition is the slot after this owner's last folder. Called on the loop.
func (s *Service) nextPosition(ownerID string) int {
	all, err := s.store.LoadAll()
	if err != nil {
		return 0
	}
	next := 0
	for _, f := range all {
		if f.Owner == ownerID && f.Position >= next {
			next = f.Position + 1
		}
	}
	return next
}

// HandleUpdate rewrites a folder the caller owns.
func (s *Service) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decode(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	v := Viewer(r)
	var (
		rec     Folder
		found   bool
		allowed bool
		opErr   error
	)
	s.loop.Do(func() {
		rec, found, opErr = s.store.Get(id)
		if opErr != nil || !found {
			return
		}
		if allowed = rec.EditableBy(v.ID); !allowed {
			return
		}
		rec.Name, rec.Visibility, rec.GroupID, rec.Rule = req.Name, req.Visibility, req.GroupID, req.Rule
		if req.Position != nil {
			rec.Position = *req.Position
		}
		rec.UpdatedAt = time.Now().UnixMilli()
		opErr = s.store.Save(rec)
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save folder: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no folder with that id")
	case !allowed:
		httpapi.Error(w, http.StatusForbidden, "only the folder's owner can change it")
	default:
		httpapi.JSON(w, http.StatusOK, toResp(rec, v.ID))
	}
}

// HandleDelete removes a folder the caller owns.
func (s *Service) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v := Viewer(r)
	var (
		found   bool
		allowed bool
		opErr   error
	)
	s.loop.Do(func() {
		var rec Folder
		rec, found, opErr = s.store.Get(id)
		if opErr != nil || !found {
			return
		}
		if allowed = rec.EditableBy(v.ID); !allowed {
			return
		}
		if opErr = s.store.Delete(id); opErr == nil {
			delete(s.compiled, id)
		}
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "delete folder: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no folder with that id")
	case !allowed:
		httpapi.Error(w, http.StatusForbidden, "only the folder's owner can delete it")
	default:
		httpapi.JSON(w, http.StatusOK, map[string]string{"id": id})
	}
}

// previewReq is an unsaved rule, as the editor asks about it on every change.
type previewReq struct {
	Rule Rule `json:"rule"`
}

// HandlePreview answers what a rule would select, without saving it: the
// expression it generates and how many open tasks it matches. It is what makes
// the editor's live counter honest — the same scan the folder itself will use,
// rather than a client-side guess over whatever page happened to be loaded.
func (s *Service) HandlePreview(w http.ResponseWriter, r *http.Request) {
	var req previewReq
	if err := json.NewDecoder(io.LimitReader(r.Body, s.budgets().Request)).Decode(&req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if req.Rule.Match == "" {
		req.Rule.Match = MatchAll
	}
	m, err := Compile(req.Rule)
	if err != nil {
		// An incomplete rule is the normal state of a dialog somebody is still
		// filling in, so it answers with the reason rather than an HTTP error the
		// editor would have to translate into one anyway.
		httpapi.JSON(w, http.StatusOK, map[string]any{
			"ok": false, "error": err.Error(), "feel": req.Rule.FEEL(),
		})
		return
	}
	counts, total, truncated, err := s.count([]*Matcher{m}, Viewer(r))
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "preview folder: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{
		"ok": true, "feel": m.Source(), "matched": counts[0],
		"total": total, "truncated": truncated,
	})
}

// HandleCounts returns the badge number for every folder this viewer sees, from
// one scan. The alternative — a request per folder — multiplies the work by the
// number of folders somebody happens to have made.
func (s *Service) HandleCounts(w http.ResponseWriter, r *http.Request) {
	v := Viewer(r)
	folders, matchers, err := s.Visible(v)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "count folders: "+err.Error())
		return
	}
	out := Counts{Folders: map[string]int{}}
	if len(matchers) > 0 {
		counts, total, truncated, cErr := s.count(matchers, v)
		if cErr != nil {
			httpapi.Error(w, http.StatusInternalServerError, "count folders: "+cErr.Error())
			return
		}
		out.Total, out.Truncated = total, truncated
		for i, f := range folders {
			out.Folders[f.ID] = counts[i]
		}
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// Visible returns the folders a viewer may see together with their compiled
// rules, dropping any folder whose rule no longer compiles rather than failing
// the whole listing — one broken folder must not empty somebody's sidebar.
func (s *Service) Visible(v User) ([]Folder, []*Matcher, error) {
	var (
		folders  []Folder
		matchers []*Matcher
		loadErr  error
	)
	s.loop.Do(func() {
		var all []Folder
		all, loadErr = s.store.VisibleTo(v.ID, v.Groups)
		if loadErr != nil {
			return
		}
		for _, f := range all {
			m, err := s.matcher(f)
			if err != nil {
				continue
			}
			folders = append(folders, f)
			matchers = append(matchers, m)
		}
	})
	return folders, matchers, loadErr
}

// MatcherFor returns one visible folder's compiled rule. found=false covers both
// "no such folder" and "not yours to see", which are the same answer to a caller.
func (s *Service) MatcherFor(id string, v User) (Folder, *Matcher, bool, error) {
	var (
		rec   Folder
		m     *Matcher
		found bool
		opErr error
	)
	s.loop.Do(func() {
		rec, found, opErr = s.store.Get(id)
		if opErr != nil || !found {
			return
		}
		if !rec.VisibleTo(v.ID, v.Groups) {
			found = false
			return
		}
		m, opErr = s.matcher(rec)
	})
	return rec, m, found && opErr == nil, opErr
}

// matcher returns a folder's compiled rule, compiling it the first time and
// whenever the folder has changed since. Keyed by UpdatedAt so an edit
// invalidates its own entry — there is no separate invalidation to forget.
// Called on the loop, which owns the cache.
func (s *Service) matcher(f Folder) (*Matcher, error) {
	if c, ok := s.compiled[f.ID]; ok && c.at == f.UpdatedAt {
		return c.m, nil
	}
	m, err := Compile(f.Rule)
	if err != nil {
		return nil, err
	}
	s.compiled[f.ID] = cachedMatcher{at: f.UpdatedAt, m: m}
	return m, nil
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
