package userprefs

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// Service serves one person's display preferences: GET and PUT on
// /api/v1/me/preferences, and nothing else. There is no listing and no admin
// view — a preference is read by the person it belongs to, and by nobody else.
type Service struct {
	loop  *runloop.Loop
	store *Store
	// now is injected so a test can pin the stamp. Unix nanos, like every other
	// timestamp Atlas writes.
	now func() int64
}

// New builds the service over the store, serializing every access on the loop:
// a sidecar store does no locking of its own, and two tabs saving at once is the
// ordinary case for a setting a person edits on one screen.
func New(loop *runloop.Loop, store *Store) *Service {
	return &Service{loop: loop, store: store, now: func() int64 { return time.Now().UnixNano() }}
}

// who is the signed-in user this request acts as, or "" when there is no
// identity at all (authentication off).
//
// There is no fallback to a name from the query string, as the Tasks app has for
// claiming: a display-only name may say which tasks are "mine", but it must not
// decide whose stored settings get rewritten.
func who(r *http.Request) string {
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		return p.UserID
	}
	return ""
}

// noIdentity answers the request that has nobody behind it.
//
// 404 rather than 401: with authentication off there is no sign-in to offer and
// nothing is being refused — the resource "the signed-in person's preferences"
// genuinely does not exist on this instance. The console reads it as "keep this
// in the browser", which is the honest answer there, and a 401 would have it
// showing a login prompt for a server that has no logins.
func noIdentity(w http.ResponseWriter) {
	httpapi.Error(w, http.StatusNotFound, "no signed-in identity on this instance — display preferences are kept in the browser")
}

// HandleGet answers this person's preferences.
//
// Nothing stored is not a miss: it is the state every account starts in, and the
// answer is the unset record, which is the console's behaviour before anybody
// opened the screen.
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	id := who(r)
	if id == "" {
		noIdentity(w)
		return
	}
	var (
		rec     Prefs
		loadErr error
	)
	s.loop.Do(func() {
		stored, ok, err := s.store.Get(id)
		if err != nil {
			loadErr = err
			return
		}
		if ok {
			rec = stored
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, loadErr.Error())
		return
	}
	rec.UserID = id
	httpapi.JSON(w, http.StatusOK, rec)
}

// HandleSave rewrites this person's preferences and answers the stored record.
//
// The whole record is written rather than patched: the screen edits its fields
// together and sends all of them, so a field left out is a field cleared — which
// is how "back to the defaults" works without a second endpoint. The top bar's
// light/dark switch sends the whole record too, with only the appearance changed.
func (s *Service) HandleSave(w http.ResponseWriter, r *http.Request) {
	id := who(r)
	if id == "" {
		noIdentity(w)
		return
	}
	var body struct {
		Region     string `json:"region"`
		TimeZone   string `json:"timeZone"`
		Language   string `json:"language"`
		Appearance string `json:"appearance"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "malformed body: "+err.Error())
		return
	}
	// The id comes from the principal and from nowhere else; a userId in the body
	// is not read, so a request cannot write under somebody else's name.
	rec := Prefs{UserID: id, Region: body.Region, TimeZone: body.TimeZone, Language: body.Language, Appearance: body.Appearance}
	if err := rec.Validate(); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	rec.UpdatedAt = s.now()
	var saveErr error
	s.loop.Do(func() { saveErr = s.store.Save(rec) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, rec)
}
