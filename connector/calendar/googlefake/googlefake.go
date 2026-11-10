// Package googlefake is an in-process stand-in for the part of the Google Calendar API
// the calendar Worker Type's Google backend calls: the token endpoint, a calendar's
// metadata, its events list, and the routes of one event. It exists for tests and for
// the conformance suites (ADR-0483, ADR-0552), so a record verifies anywhere, without a
// Workspace domain.
//
// What it answers is written from Google's documentation, not observed from a domain:
// the error envelope and its reasons, the dateTime and all-day date forms, paging by
// nextPageToken, and the events list expanding a recurring series only when asked for
// singleEvents. What it does on purpose:
//
//   - It refuses an events list whose fields parameter asks for a summary, a
//     description, an organizer or attendees, so a backend that starts reading other
//     people's appointments fails its tests.
//   - It answers a list without singleEvents with the series, not its occurrences.
//   - It refuses a second insert with an id it has seen, a deleted event's included,
//     as Google documents; a deleted event stays readable as cancelled.
//   - A calendar can refuse attendees from the service account, as Google refuses them
//     without domain-wide delegation (forbiddenForServiceAccounts).
//   - It answers every read with everything written before it.
package googlefake

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Busy is an appointment a calendar already holds. Transparent marks one shown as free.
// Weekly, above one, makes it a series of that many weekly occurrences. AllDay makes it
// an all-day appointment on Start's date, in the calendar's zone.
type Busy struct {
	Start       time.Time
	End         time.Time
	Transparent bool
	Weekly      int
	AllDay      bool
}

// Calendar is one calendar the fake knows. ReadOnly refuses every write with 403, as a
// calendar shared with "see all event details" is refused. RefuseAttendees refuses
// attendees as Google refuses them from a service account without delegation.
type Calendar struct {
	ReadOnly        bool
	RefuseAttendees bool
	Busy            []Busy
}

// Attendee is one invited person, as the fake recorded it.
type Attendee struct {
	Email string
	Name  string
}

// Event is one event as a test inspects it.
type Event struct {
	ID          string
	Status      string
	Start, End  time.Time
	Transparent bool
	Summary     string
	Description string
	Attendees   []Attendee
	// SendUpdates is what the last write said about notifying attendees.
	SendUpdates string
	weekly      int
	allDay      bool
}

// Zone is every fake calendar's time zone.
const Zone = "Europe/Zurich"

var zone = func() *time.Location {
	loc, err := time.LoadLocation(Zone)
	if err != nil {
		panic(err)
	}
	return loc
}()

// Server is the fake.
type Server struct {
	srv       *httptest.Server
	mu        sync.Mutex
	calendars map[string]*calendar
	next      int
	// PageSize, when above zero, caps every list page below what the client asked.
	PageSize int
}

type calendar struct {
	readOnly, refuseAttendees bool
	events                    []*Event
}

// New starts the fake with the calendars named.
func New(cals map[string]Calendar) *Server {
	s := &Server{calendars: map[string]*calendar{}}
	names := make([]string, 0, len(cals))
	for n := range cals {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := &calendar{readOnly: cals[n].ReadOnly, refuseAttendees: cals[n].RefuseAttendees}
		for _, b := range cals[n].Busy {
			s.next++
			c.events = append(c.events, &Event{ID: "seed" + strconv.Itoa(s.next), Status: "confirmed",
				Start: b.Start, End: b.End, Transparent: b.Transparent, Summary: "Privat", weekly: b.Weekly, allDay: b.AllDay})
		}
		s.calendars[n] = c
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Close stops the fake.
func (s *Server) Close() { s.srv.Close() }

// APIURL is the API root the backend is pointed at.
func (s *Server) APIURL() string { return s.srv.URL + "/calendar/v3" }

// TokenURL is the token endpoint the backend is pointed at.
func (s *Server) TokenURL() string { return s.srv.URL + "/token" }

// HTTPClient reaches the fake.
func (s *Server) HTTPClient() *http.Client { return s.srv.Client() }

// Events returns a calendar's events in creation order, cancelled ones included.
func (s *Server) Events(cal string) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.calendars[cal]
	if c == nil {
		return nil
	}
	out := make([]Event, len(c.events))
	for i, e := range c.events {
		out[i] = *e
		out[i].Attendees = append([]Attendee(nil), e.Attendees...)
	}
	return out
}

func apiError(w http.ResponseWriter, status int, reason, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code": status, "message": msg, "errors": []any{map[string]any{"reason": reason, "message": msg}},
	}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func wireTime(t time.Time, allDay bool) map[string]any {
	if allDay {
		return map[string]any{"date": t.In(zone).Format(time.DateOnly)}
	}
	return map[string]any{"dateTime": t.In(zone).Format(time.RFC3339), "timeZone": Zone}
}

func wire(e *Event, start, end time.Time) map[string]any {
	transparency := "opaque"
	if e.Transparent {
		transparency = "transparent"
	}
	return map[string]any{"id": e.ID, "status": e.Status, "transparency": transparency,
		"start": wireTime(start, e.allDay), "end": wireTime(end, e.allDay)}
}

// narrow reports whether a fields parameter asks for nothing beyond times,
// availability and status.
func narrow(fields string) bool {
	if fields == "" {
		return false
	}
	for _, f := range []string{"summary", "description", "attendees", "organizer", "creator", "location"} {
		if strings.Contains(fields, f) {
			return false
		}
	}
	return true
}

// idPattern is Google's rule for a client-chosen id: base32hex, 5 to 1024 characters.
var idPattern = regexp.MustCompile(`^[a-v0-9]+$`)

func validID(id string) bool { return len(id) >= 5 && len(id) <= 1024 && idPattern.MatchString(id) }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/token" {
		_ = r.ParseForm()
		if r.Method != http.MethodPost || r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || r.PostForm.Get("assertion") == "" {
			apiError(w, http.StatusBadRequest, "invalid_grant", "a service account's assertion is expected")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"access_token": "fake-google-token", "expires_in": 3600, "token_type": "Bearer"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer fake-google-token" {
		apiError(w, http.StatusUnauthorized, "authError", "Invalid Credentials")
		return
	}
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/calendar/v3/calendars/")
	if !ok {
		apiError(w, http.StatusNotFound, "notFound", "Not Found")
		return
	}
	parts := strings.Split(rest, "/")
	calID, _ := url.PathUnescape(parts[0])
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.calendars[calID]
	if c == nil {
		apiError(w, http.StatusNotFound, "notFound", "Not Found")
		return
	}
	write := r.Method != http.MethodGet
	if write && c.readOnly {
		apiError(w, http.StatusForbidden, "requiredAccessLevel", "You need to have writer access to this calendar.")
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"id": calID, "timeZone": Zone})
	case len(parts) == 2 && parts[1] == "events" && r.Method == http.MethodGet:
		s.list(w, r, c)
	case len(parts) == 2 && parts[1] == "events" && r.Method == http.MethodPost:
		s.insert(w, r, c)
	case len(parts) == 3 && parts[1] == "events":
		id, _ := url.PathUnescape(parts[2])
		s.event(w, r, c, id)
	default:
		apiError(w, http.StatusNotFound, "notFound", "the fake does not answer "+r.Method+" "+r.URL.Path)
	}
}

type occurrence struct {
	e          *Event
	start, end time.Time
}

func (s *Server) list(w http.ResponseWriter, r *http.Request, c *calendar) {
	q := r.URL.Query()
	if !narrow(q.Get("fields")) {
		apiError(w, http.StatusBadRequest, "fakeRefusesWideRead", "the fake answers a list only when fields is narrowed to times and availability")
		return
	}
	from, err1 := time.Parse(time.RFC3339, q.Get("timeMin"))
	to, err2 := time.Parse(time.RFC3339, q.Get("timeMax"))
	if err1 != nil || err2 != nil {
		apiError(w, http.StatusBadRequest, "badRequest", "timeMin and timeMax are required")
		return
	}
	expand := q.Get("singleEvents") == "true"
	var all []occurrence
	for _, e := range c.events {
		if e.Status == "cancelled" && q.Get("showDeleted") != "true" {
			continue
		}
		n := max(e.weekly, 1)
		if !expand {
			n = 1 // the series, answered as its first occurrence
		}
		for i := 0; i < n; i++ {
			st, en := e.Start.AddDate(0, 0, 7*i), e.End.AddDate(0, 0, 7*i)
			if st.Before(to) && from.Before(en) {
				all = append(all, occurrence{e, st, en})
			}
		}
	}
	size, _ := strconv.Atoi(q.Get("maxResults"))
	if size <= 0 {
		size = 250
	}
	if s.PageSize > 0 && s.PageSize < size {
		size = s.PageSize
	}
	skip, _ := strconv.Atoi(q.Get("pageToken"))
	end := min(skip+size, len(all))
	items := make([]any, 0, end-skip)
	for _, o := range all[min(skip, len(all)):end] {
		items = append(items, wire(o.e, o.start, o.end))
	}
	out := map[string]any{"items": items, "timeZone": Zone}
	if end < len(all) {
		out["nextPageToken"] = strconv.Itoa(end)
	}
	writeJSON(w, http.StatusOK, out)
}

type wireBody struct {
	ID          *string `json:"id"`
	Status      *string `json:"status"`
	Summary     *string `json:"summary"`
	Description *string `json:"description"`
	Start       *struct {
		DateTime string `json:"dateTime"`
	} `json:"start"`
	End *struct {
		DateTime string `json:"dateTime"`
	} `json:"end"`
	Transparency *string `json:"transparency"`
	Attendees    []struct {
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
	} `json:"attendees"`
}

func (s *Server) insert(w http.ResponseWriter, r *http.Request, c *calendar) {
	var b wireBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.Start == nil || b.End == nil {
		apiError(w, http.StatusBadRequest, "badRequest", "the body is not an event")
		return
	}
	start, err1 := time.Parse(time.RFC3339, b.Start.DateTime)
	end, err2 := time.Parse(time.RFC3339, b.End.DateTime)
	if err1 != nil || err2 != nil || !start.Before(end) {
		apiError(w, http.StatusBadRequest, "timeRangeEmpty", "The specified time range is empty.")
		return
	}
	id := ""
	if b.ID != nil {
		id = *b.ID
		if !validID(id) {
			apiError(w, http.StatusBadRequest, "invalid", "Invalid resource id value.")
			return
		}
		for _, e := range c.events {
			if e.ID == id {
				apiError(w, http.StatusConflict, "duplicate", "The requested identifier already exists.")
				return
			}
		}
	} else {
		s.next++
		id = "gen" + strconv.Itoa(s.next)
	}
	e := &Event{ID: id, Status: "confirmed", Start: start, End: end, SendUpdates: r.URL.Query().Get("sendUpdates")}
	if b.Summary != nil {
		e.Summary = *b.Summary
	}
	if b.Transparency != nil && *b.Transparency == "transparent" {
		e.Transparent = true
	}
	c.events = append(c.events, e)
	writeJSON(w, http.StatusOK, wire(e, e.Start, e.End))
}

func (s *Server) event(w http.ResponseWriter, r *http.Request, c *calendar, id string) {
	var e *Event
	for _, x := range c.events {
		if x.ID == id {
			e = x
		}
	}
	if e == nil {
		apiError(w, http.StatusNotFound, "notFound", "Not Found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !narrow(r.URL.Query().Get("fields")) {
			apiError(w, http.StatusBadRequest, "fakeRefusesWideRead", "the fake answers an event only when fields is narrowed")
			return
		}
		writeJSON(w, http.StatusOK, wire(e, e.Start, e.End))
	case http.MethodDelete:
		if e.Status == "cancelled" {
			apiError(w, http.StatusGone, "deleted", "Resource has been deleted")
			return
		}
		e.Status = "cancelled"
		e.SendUpdates = r.URL.Query().Get("sendUpdates")
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPatch:
		var b wireBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			apiError(w, http.StatusBadRequest, "badRequest", "the body is not an event patch")
			return
		}
		if len(b.Attendees) > 0 && c.refuseAttendees {
			apiError(w, http.StatusForbidden, "forbiddenForServiceAccounts",
				"Service accounts cannot invite attendees without Domain-Wide Delegation of Authority.")
			return
		}
		if b.Status != nil {
			e.Status = *b.Status
		}
		if b.Summary != nil {
			e.Summary = *b.Summary
		}
		if b.Description != nil {
			e.Description = *b.Description
		}
		if b.Start != nil && b.End != nil {
			if st, err := time.Parse(time.RFC3339, b.Start.DateTime); err == nil {
				e.Start = st
			}
			if en, err := time.Parse(time.RFC3339, b.End.DateTime); err == nil {
				e.End = en
			}
		}
		if b.Attendees != nil {
			e.Attendees = e.Attendees[:0]
			for _, a := range b.Attendees {
				e.Attendees = append(e.Attendees, Attendee{Email: a.Email, Name: a.DisplayName})
			}
		}
		e.SendUpdates = r.URL.Query().Get("sendUpdates")
		writeJSON(w, http.StatusOK, wire(e, e.Start, e.End))
	default:
		apiError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "not allowed")
	}
}

var (
	keyOnce sync.Once
	keyPEM  string
)

// ServiceAccountKeyPEM is an RSA key in the PEM form of a service account's JSON key
// file, made once per process. The fake checks no signature; the backend needs a key
// it can sign its assertion with.
func ServiceAccountKeyPEM() string {
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic("googlefake: " + err.Error())
		}
		der, err := x509.MarshalPKCS8PrivateKey(k)
		if err != nil {
			panic("googlefake: " + err.Error())
		}
		keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	})
	return keyPEM
}

// Bundle is a calendar Worker bundle for a service account of this fake. subject, when
// not empty, is the Workspace user it acts as.
func Bundle(subject string) string {
	b := map[string]any{"provider": "google-calendar", "clientEmail": "atlas@fake.iam.gserviceaccount.com", "privateKey": ServiceAccountKeyPEM()}
	if subject != "" {
		b["subject"] = subject
	}
	raw, _ := json.Marshal(b)
	return string(raw)
}
