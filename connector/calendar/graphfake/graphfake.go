// Package graphfake is an in-process stand-in for the part of Microsoft Graph the
// calendar Worker Type's Graph backend calls: the token endpoint, calendarView, and the
// event routes of a mailbox's default calendar. It exists for tests and for the
// conformance suites (ADR-0483), so a record verifies anywhere, without a tenant.
//
// What it answers is written from Graph's documentation, not observed from a tenant:
// the status codes and error codes of a refusal, the dateTimeTimeZone shape, paging by
// @odata.nextLink, and calendarView expanding a recurring series into occurrences where
// the events collection would answer the series. Three things it does on purpose:
//
//   - It refuses a calendarView that does not narrow $select to times, availability and
//     the transaction id, so a backend that starts reading other people's subjects
//     fails its tests.
//   - It does not filter a repeated transactionId. Graph documents the field as the
//     client's means against a duplicate POST without saying for how long; the backend
//     must find its own reservation regardless, and against this fake it has to.
//   - It answers every read with everything written before it, the read-after-write
//     property the conflict rule needs and the record cannot yet confirm of Graph.
package graphfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Busy is an appointment a mailbox already holds. ShowAs is Graph's availability
// ("busy" when empty). Weekly, above one, makes it a series of that many weekly
// occurrences, kept as one series.
type Busy struct {
	Start  time.Time
	End    time.Time
	ShowAs string
	Weekly int
}

// Mailbox is one mailbox the fake knows. ReadOnly refuses every write with 403, as a
// mailbox outside the registration's access scope for writing is refused.
type Mailbox struct {
	ReadOnly bool
	Busy     []Busy
}

// Attendee is one invited person, as the fake recorded it.
type Attendee struct {
	Email string
	Name  string
}

// Event is one event as a test inspects it.
type Event struct {
	ID            string
	TransactionID string
	Start, End    time.Time
	ShowAs        string
	Subject       string
	Body          string
	Attendees     []Attendee
	weekly        int
}

// Server is the fake.
type Server struct {
	srv       *httptest.Server
	mu        sync.Mutex
	mailboxes map[string]*mailbox
	next      int
	// PageSize, when above zero, caps every calendarView page below what the client
	// asked, so paging is exercised.
	PageSize int
}

type mailbox struct {
	readOnly bool
	events   []*Event
}

// New starts the fake with the mailboxes named, by address.
func New(boxes map[string]Mailbox) *Server {
	s := &Server{mailboxes: map[string]*mailbox{}}
	names := make([]string, 0, len(boxes))
	for n := range boxes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		mb := &mailbox{readOnly: boxes[n].ReadOnly}
		for _, b := range boxes[n].Busy {
			s.next++
			show := b.ShowAs
			if show == "" {
				show = "busy"
			}
			mb.events = append(mb.events, &Event{ID: "seed-" + strconv.Itoa(s.next), Start: b.Start.UTC(), End: b.End.UTC(), ShowAs: show, Subject: "Privat", weekly: b.Weekly})
		}
		s.mailboxes[strings.ToLower(n)] = mb
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Close stops the fake.
func (s *Server) Close() { s.srv.Close() }

// GraphURL is the Graph root the backend is pointed at.
func (s *Server) GraphURL() string { return s.srv.URL + "/v1.0" }

// TokenURL is the token endpoint the backend is pointed at.
func (s *Server) TokenURL() string { return s.srv.URL + "/token" }

// HTTPClient reaches the fake.
func (s *Server) HTTPClient() *http.Client { return s.srv.Client() }

// Events returns a mailbox's events in creation order.
func (s *Server) Events(address string) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	mb := s.mailboxes[strings.ToLower(address)]
	if mb == nil {
		return nil
	}
	out := make([]Event, len(mb.events))
	for i, e := range mb.events {
		out[i] = *e
		out[i].Attendees = append([]Attendee(nil), e.Attendees...)
	}
	return out
}

func graphError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

const timeLayout = "2006-01-02T15:04:05.0000000"

func wire(e *Event, start, end time.Time) map[string]any {
	return map[string]any{
		"id":            e.ID,
		"start":         map[string]any{"dateTime": start.UTC().Format(timeLayout), "timeZone": "UTC"},
		"end":           map[string]any{"dateTime": end.UTC().Format(timeLayout), "timeZone": "UTC"},
		"showAs":        e.ShowAs,
		"isCancelled":   false,
		"transactionId": e.TransactionID,
	}
}

// allowedSelect is every field the fake lets calendarView and an event read ask for.
var allowedSelect = map[string]bool{"id": true, "start": true, "end": true, "showAs": true, "isCancelled": true, "transactionId": true}

func selectAllowed(q url.Values) bool {
	sel := q.Get("$select")
	if sel == "" {
		return false
	}
	for _, f := range strings.Split(sel, ",") {
		if !allowedSelect[strings.TrimSpace(f)] {
			return false
		}
	}
	return true
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/token" {
		if r.Method != http.MethodPost {
			graphError(w, http.StatusMethodNotAllowed, "invalid_request", "POST only")
			return
		}
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "client_credentials" || r.PostForm.Get("client_id") == "" {
			graphError(w, http.StatusBadRequest, "invalid_client", "client credentials expected")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"access_token": "fake-token", "expires_in": 3600, "token_type": "Bearer"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer fake-token" {
		graphError(w, http.StatusUnauthorized, "InvalidAuthenticationToken", "no token")
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/v1.0/users/")
	if !ok {
		graphError(w, http.StatusNotFound, "ResourceNotFound", "the fake answers only /v1.0/users/…")
		return
	}
	address, tail, _ := strings.Cut(rest, "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	mb := s.mailboxes[strings.ToLower(address)]
	if mb == nil {
		graphError(w, http.StatusNotFound, "ErrorInvalidUser", "The requested user is invalid.")
		return
	}
	write := r.Method != http.MethodGet
	if write && mb.readOnly {
		graphError(w, http.StatusForbidden, "ErrorAccessDenied", "Access is denied. Check credentials and try again.")
		return
	}
	switch {
	case tail == "calendar/calendarView" && r.Method == http.MethodGet:
		s.calendarView(w, r, mb)
	case tail == "calendar/events" && r.Method == http.MethodPost:
		s.create(w, r, mb)
	case strings.HasPrefix(tail, "events/"):
		id := strings.TrimPrefix(tail, "events/")
		s.event(w, r, mb, id)
	default:
		graphError(w, http.StatusNotFound, "ResourceNotFound", "the fake does not answer "+r.Method+" "+tail)
	}
}

type occurrence struct {
	e          *Event
	start, end time.Time
}

func (s *Server) calendarView(w http.ResponseWriter, r *http.Request, mb *mailbox) {
	q := r.URL.Query()
	if !selectAllowed(q) {
		graphError(w, http.StatusBadRequest, "FakeRefusesWideRead", "the fake answers calendarView only when $select is narrowed to times and availability")
		return
	}
	from, err1 := time.Parse(time.RFC3339, q.Get("startDateTime"))
	to, err2 := time.Parse(time.RFC3339, q.Get("endDateTime"))
	if err1 != nil || err2 != nil {
		graphError(w, http.StatusBadRequest, "ErrorInvalidParameter", "startDateTime and endDateTime are required")
		return
	}
	if r.Header.Get("Prefer") != `outlook.timezone="UTC"` {
		graphError(w, http.StatusBadRequest, "FakeWantsUTC", "the fake answers only in UTC")
		return
	}
	var all []occurrence
	for _, e := range mb.events {
		for i := 0; i < max(e.weekly, 1); i++ {
			st, en := e.Start.AddDate(0, 0, 7*i), e.End.AddDate(0, 0, 7*i)
			if st.Before(to) && from.Before(en) {
				all = append(all, occurrence{e, st, en})
			}
		}
	}
	top, _ := strconv.Atoi(q.Get("$top"))
	if top <= 0 {
		top = 10
	}
	if s.PageSize > 0 && s.PageSize < top {
		top = s.PageSize
	}
	skip, _ := strconv.Atoi(q.Get("$skip"))
	end := min(skip+top, len(all))
	page := make([]any, 0, end-skip)
	for _, o := range all[min(skip, len(all)):end] {
		page = append(page, wire(o.e, o.start, o.end))
	}
	out := map[string]any{"value": page}
	if end < len(all) {
		q.Set("$skip", strconv.Itoa(end))
		out["@odata.nextLink"] = s.GraphURL() + "/users/" + url.PathEscape(strings.SplitN(strings.TrimPrefix(r.URL.Path, "/v1.0/users/"), "/", 2)[0]) + "/calendar/calendarView?" + q.Encode()
	}
	writeJSON(w, http.StatusOK, out)
}

type wireTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

func (t wireTime) parse() (time.Time, error) {
	if t.TimeZone != "UTC" {
		return time.Time{}, fmt.Errorf("the fake takes UTC only")
	}
	return time.ParseInLocation("2006-01-02T15:04:05", t.DateTime, time.UTC)
}

func (s *Server) create(w http.ResponseWriter, r *http.Request, mb *mailbox) {
	var body struct {
		Subject       string   `json:"subject"`
		Start         wireTime `json:"start"`
		End           wireTime `json:"end"`
		ShowAs        string   `json:"showAs"`
		TransactionID string   `json:"transactionId"`
		Attendees     []any    `json:"attendees"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		graphError(w, http.StatusBadRequest, "ErrorInvalidRequest", "the body is not an event")
		return
	}
	start, err1 := body.Start.parse()
	end, err2 := body.End.parse()
	if err1 != nil || err2 != nil || !start.Before(end) {
		graphError(w, http.StatusBadRequest, "ErrorInvalidRequest", "start and end must be UTC and in order")
		return
	}
	show := body.ShowAs
	if show == "" {
		show = "busy"
	}
	s.next++
	e := &Event{ID: "AAMk-" + strconv.Itoa(s.next), TransactionID: body.TransactionID, Start: start, End: end, ShowAs: show, Subject: body.Subject}
	mb.events = append(mb.events, e)
	writeJSON(w, http.StatusCreated, wire(e, e.Start, e.End))
}

func (s *Server) event(w http.ResponseWriter, r *http.Request, mb *mailbox, id string) {
	idx := -1
	for i, e := range mb.events {
		if e.ID == id {
			idx = i
		}
	}
	if idx < 0 {
		graphError(w, http.StatusNotFound, "ErrorItemNotFound", "The specified object was not found in the store.")
		return
	}
	e := mb.events[idx]
	switch r.Method {
	case http.MethodGet:
		if !selectAllowed(r.URL.Query()) {
			graphError(w, http.StatusBadRequest, "FakeRefusesWideRead", "the fake answers an event read only when $select is narrowed")
			return
		}
		writeJSON(w, http.StatusOK, wire(e, e.Start, e.End))
	case http.MethodDelete:
		mb.events = append(mb.events[:idx], mb.events[idx+1:]...)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPatch:
		var body struct {
			Subject *string `json:"subject"`
			Body    *struct {
				Content string `json:"content"`
			} `json:"body"`
			Attendees []struct {
				EmailAddress struct{ Address, Name string } `json:"emailAddress"`
			} `json:"attendees"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			graphError(w, http.StatusBadRequest, "ErrorInvalidRequest", "the body is not an event patch")
			return
		}
		if body.Subject != nil {
			e.Subject = *body.Subject
		}
		if body.Body != nil {
			e.Body = body.Body.Content
		}
		if body.Attendees != nil {
			e.Attendees = e.Attendees[:0]
			for _, a := range body.Attendees {
				e.Attendees = append(e.Attendees, Attendee{Email: a.EmailAddress.Address, Name: a.EmailAddress.Name})
			}
		}
		writeJSON(w, http.StatusOK, wire(e, e.Start, e.End))
	default:
		graphError(w, http.StatusMethodNotAllowed, "ErrorInvalidRequest", "not allowed")
	}
}
