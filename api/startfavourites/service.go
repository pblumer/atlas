// Package startfavourites keeps the processes each person starred on the Tasks app's
// Start view, so the list they start from most opens with those at the top on every
// browser and device they sign in from (ADR-draft-start-view-groups-and-favourites).
//
// A mark stores a process id under a user id and nothing else. It is a bookmark,
// never a grant — the shop's favourites (ADR-0348) and the diagram shortcuts
// (ADR-0460) settled that, and this takes it over unchanged: whether the person may
// start the process is decided where the start is made, not here.
//
// It is not the diagram shortcuts' "process" kind, although both name a process by
// its id. Those are resolved against the diagram's design-time scope, so a person who
// starts a process without being a member of its application could not mark it —
// and that person is exactly who the Start view is for. Nor are a modeler's
// favourite diagrams and somebody's favourite forms to fill in the same list.
package startfavourites

import (
	"net/http"
	"slices"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// maxProcessID bounds an id before it is stored. A BPMN process id is far shorter;
// this keeps a hand-written request from filing a megabyte under a star.
const maxProcessID = 256

// Record is one person's marks.
type Record struct {
	// UserID owns the record and is its key.
	UserID string `json:"userId"`
	// ProcessIDs are kept in the order they were marked. A process id rather than a
	// deployment key, because a key names one version and a mark names the process:
	// deploying v2 must not leave the star on v1.
	ProcessIDs []string `json:"processIds"`
	// UpdatedAt is Unix nanos, like every other timestamp Atlas writes.
	UpdatedAt int64 `json:"updatedAt"`
}

// listResp is what every handler answers: the marks as they now stand.
type listResp struct {
	ProcessIDs []string `json:"processIds"`
}

// Service serves one person's start favourites: read the list, mark one process,
// clear one. There is no listing across people and no admin view.
type Service struct {
	loop  *runloop.Loop
	store *Store
	// known reports whether a process id has a deployed version. It reads engine
	// state, so it is only ever called from inside the loop's turn.
	known func(processID string) bool
	// limit is how many marks one person may keep (limits.StartFavourites), read
	// when a mark is made so a server's configured budgets apply.
	limit func() int
	now   func() int64
}

// New builds the service over the store, serializing every access on the loop: a
// sidecar store does no locking of its own, and two tabs starring at once is the
// ordinary case.
func New(loop *runloop.Loop, store *Store, known func(string) bool, limit func() int) *Service {
	return &Service{loop: loop, store: store, known: known, limit: limit,
		now: func() int64 { return time.Now().UnixNano() }}
}

// who is the signed-in user this request acts as, or "" when there is no identity at
// all (authentication off, or a machine credential with no account behind it).
func who(r *http.Request) string {
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		return p.UserID
	}
	return ""
}

// noIdentity answers the request that has nobody behind it. 404 rather than 401, for
// the reason the display preferences give: with nobody signed in the resource does not
// exist, and the console reads the answer as "keep them in the browser".
func noIdentity(w http.ResponseWriter) {
	httpapi.Error(w, http.StatusNotFound, "no signed-in identity on this instance — start favourites are kept in the browser")
}

// processIDFrom reads the path's process id and says what is wrong with it, if
// anything.
func processIDFrom(r *http.Request) (string, string) {
	id := r.PathValue("processId")
	switch {
	case id == "":
		return "", "a process id is required"
	case len(id) > maxProcessID:
		return "", "that is not a process id: it is longer than any process id can be"
	}
	return id, ""
}

// load reads one person's record, or the empty one when nothing is stored. Called on
// the loop.
func (s *Service) load(userID string) (Record, error) {
	rec, ok, err := s.store.Get(userID)
	if err != nil {
		return Record{}, err
	}
	if !ok {
		rec = Record{UserID: userID}
	}
	if rec.ProcessIDs == nil {
		rec.ProcessIDs = []string{}
	}
	return rec, nil
}

// HandleGet answers this person's marks.
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	id := who(r)
	if id == "" {
		noIdentity(w)
		return
	}
	var (
		rec Record
		err error
	)
	s.loop.Do(func() { rec, err = s.load(id) })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "start favourites: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, listResp{ProcessIDs: rec.ProcessIDs})
}

// HandleMark stars one deployed process for this person. Marking one already marked
// writes nothing.
func (s *Service) HandleMark(w http.ResponseWriter, r *http.Request) {
	s.change(w, r, true)
}

// HandleUnmark clears one mark. It is never checked against the engine, so a mark on
// a process deleted since can always be taken away; clearing one never made is not an
// error either.
func (s *Service) HandleUnmark(w http.ResponseWriter, r *http.Request) {
	s.change(w, r, false)
}

func (s *Service) change(w http.ResponseWriter, r *http.Request, mark bool) {
	userID := who(r)
	if userID == "" {
		noIdentity(w)
		return
	}
	processID, bad := processIDFrom(r)
	if bad != "" {
		httpapi.Error(w, http.StatusBadRequest, "start favourites: "+bad)
		return
	}
	var (
		rec           Record
		err           error
		missing, full bool
	)
	s.loop.Do(func() {
		if rec, err = s.load(userID); err != nil {
			return
		}
		has := slices.Contains(rec.ProcessIDs, processID)
		switch {
		case mark && has, !mark && !has:
			return
		case mark && !s.known(processID):
			missing = true
			return
		case mark && len(rec.ProcessIDs) >= s.limit():
			full = true
			return
		case mark:
			rec.ProcessIDs = append(rec.ProcessIDs, processID)
		default:
			rec.ProcessIDs = slices.DeleteFunc(rec.ProcessIDs, func(id string) bool { return id == processID })
		}
		rec.UpdatedAt = s.now()
		err = s.store.Save(rec)
	})
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "start favourites: "+err.Error())
	case missing:
		httpapi.Error(w, http.StatusNotFound, "start favourites: no deployed process with that id")
	case full:
		httpapi.Error(w, http.StatusUnprocessableEntity,
			"that is more favourite processes than one account may keep. A list nobody can "+
				"scan is not a shortcut, so clear one before marking another")
	default:
		httpapi.JSON(w, http.StatusOK, listResp{ProcessIDs: rec.ProcessIDs})
	}
}
