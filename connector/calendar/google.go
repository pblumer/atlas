package calendar

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/connector/oauth2"
	"github.com/pblumer/atlas/limits"
)

// DefaultGoogleURL is the Google Calendar API's root, and DefaultGoogleTokenURL the
// token endpoint a service account's assertion is exchanged at.
const (
	DefaultGoogleURL      = "https://www.googleapis.com/calendar/v3"
	DefaultGoogleTokenURL = "https://oauth2.googleapis.com/token"
)

// googleScope is the one scope the backend asks for: read and write events.
const googleScope = "https://www.googleapis.com/auth/calendar"

// googleListFields narrows an events list to what finding slots and checking a
// reservation read: no summary, description, organizer or attendees of anybody else's
// appointment is asked for.
const (
	googleListFields  = "items(id,status,transparency,start,end),nextPageToken,timeZone"
	googleEventFields = "id,status,transparency,start,end"
)

// googleIDPrefix starts every event id the backend chooses. Google lets a client choose
// an event's id from the base32hex alphabet (0-9, a-v), which hex is a part of, and
// refuses a second insert with the same id: that is what makes a reservation safe to
// repeat. The create key is written in hex behind the prefix, so any key a job carries
// becomes a valid id and can be read back from it.
const googleIDPrefix = "atlas"

func googleEventID(key string) string { return googleIDPrefix + hex.EncodeToString([]byte(key)) }

func googleKeyOf(id string) string {
	rest, ok := strings.CutPrefix(id, googleIDPrefix)
	if !ok {
		return ""
	}
	b, err := hex.DecodeString(rest)
	if err != nil {
		return ""
	}
	return string(b)
}

// google is the Google Calendar backend. A calendar is a Google calendar id — the
// address of a person's calendar, or the id of a resource calendar
// (…@resource.calendar.google.com) — shared with the service account.
type google struct {
	tokens  oauth2.TokenSource
	baseURL string
	httpc   *http.Client
}

func newGoogle(endpoint string, b Bundle, opts Options) (*google, error) {
	if strings.TrimSpace(b.ClientEmail) == "" || strings.TrimSpace(b.PrivateKey) == "" {
		return nil, fmt.Errorf("calendar: a Google Calendar Worker needs clientEmail and privateKey, the client_email and private_key of the service account's JSON key file")
	}
	key, err := oauth2.ParseRSAPrivateKey("calendar", b.PrivateKey)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if base == "" {
		base = DefaultGoogleURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname()))) {
		return nil, fmt.Errorf("calendar: the endpoint %q is not an https URL of the Calendar API's root", clip(endpoint))
	}
	httpc := opts.HTTPClient
	if httpc == nil {
		httpc = nettimeout.HTTPClient()
	}
	tokenURL := opts.TokenURL
	if tokenURL == "" {
		tokenURL = DefaultGoogleTokenURL
	}
	tokens := oauth2.NewCached(oauth2.ServiceAccount(oauth2.ServiceAccountConfig{
		HTTPClient: httpc, Kind: "calendar", TokenURL: tokenURL,
		ClientEmail: strings.TrimSpace(b.ClientEmail), PrivateKey: key, Scope: googleScope,
		Subject: strings.TrimSpace(b.Subject),
	}, nil), nil)
	return &google{tokens: tokens, baseURL: base, httpc: httpc}, nil
}

// googleFailure is a request Google answered with a non-2xx status: the status, and the
// first reason of Google's error envelope.
type googleFailure struct {
	method, path string
	status       int
	reason       string
	message      string
}

func (e *googleFailure) Error() string {
	return fmt.Sprintf("calendar: Google Calendar %s %s returned HTTP %d: %s %s", e.method, e.path, e.status, e.reason, e.message)
}

func (g *google) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("calendar: encode request: %w", err)
		}
		rdr = bytes.NewReader(raw)
	}
	target := g.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rdr)
	if err != nil {
		return fmt.Errorf("calendar: build %s request: %w", method, err)
	}
	tok, err := g.tokens.Token(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("calendar: Google Calendar %s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limits.Default().Payload+1))
	if err != nil {
		return fmt.Errorf("calendar: read Google's answer: %w", err)
	}
	if int64(len(raw)) > limits.Default().Payload {
		return fmt.Errorf("calendar: Google's answer exceeds the payload limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		f := &googleFailure{method: method, path: req.URL.Path, status: resp.StatusCode}
		var env struct {
			Error struct {
				Message string `json:"message"`
				Errors  []struct {
					Reason string `json:"reason"`
				} `json:"errors"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil {
			f.message = env.Error.Message
			if len(env.Error.Errors) > 0 {
				f.reason = env.Error.Errors[0].Reason
			}
		}
		return f
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("calendar: Google answered %s with something that is not the expected JSON", method)
	}
	return nil
}

func googleStatus(err error) (int, string) {
	if f, ok := err.(*googleFailure); ok {
		return f.status, f.reason
	}
	return 0, ""
}

// classifyGoogle turns Google's refusals on a calendar into the kinds a process acts on.
// 404 is a calendar that does not exist or is not shared with the service account;
// 403 on a read is the same for the process, on a write a calendar shared read-only.
// The reasons are Google's documented ones, not observed from a domain.
func classifyGoogle(err error, write bool) error {
	status, reason := googleStatus(err)
	switch {
	case status == http.StatusNotFound:
		return &Error{Kind: KindCalendarNotFound, Message: "the calendar does not exist or is not shared with this Worker (" + reason + ")"}
	case status == http.StatusForbidden && write:
		return &Error{Kind: KindCalendarNotWritable, Message: "this Worker may not change events of the calendar (" + reason + ")"}
	case status == http.StatusForbidden:
		return &Error{Kind: KindCalendarNotFound, Message: "this Worker may not read the calendar (" + reason + ")"}
	}
	return err
}

// eventGone tells, after an event route answered 404 or 410, whether the event or the
// whole calendar is missing: Google answers both with the same status, so the calendar
// is asked for. A booking a process holds is gone; a calendar it names is a different
// thing to fix.
func (g *google) eventGone(ctx context.Context, cal string, err error) error {
	status, _ := googleStatus(err)
	if status != http.StatusNotFound && status != http.StatusGone {
		return nil
	}
	if cerr := g.do(ctx, http.MethodGet, googleCalendarPath(cal), url.Values{"fields": {"id"}}, nil, nil); cerr != nil {
		return classifyGoogle(cerr, false)
	}
	return &Error{Kind: KindBookingNotFound, Message: "the booking does not exist in this calendar"}
}

func googleCalendarPath(cal string) string {
	return "/calendars/" + url.PathEscape(strings.TrimSpace(cal))
}

func googleEventPath(cal, id string) string {
	return googleCalendarPath(cal) + "/events/" + url.PathEscape(id)
}

type googleTime struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
}

func (t googleTime) parse(loc *time.Location) (time.Time, error) {
	if t.DateTime != "" {
		return time.Parse(time.RFC3339, t.DateTime)
	}
	// An all-day appointment is a date in the calendar's own zone.
	return time.ParseInLocation(time.DateOnly, t.Date, loc)
}

type googleEvent struct {
	ID           string     `json:"id"`
	Status       string     `json:"status"`
	Transparency string     `json:"transparency"`
	Start        googleTime `json:"start"`
	End          googleTime `json:"end"`
}

func (e googleEvent) event(loc *time.Location) (Event, error) {
	start, err := e.Start.parse(loc)
	if err != nil {
		return Event{}, fmt.Errorf("calendar: Google answered an event whose start cannot be read")
	}
	end, err := e.End.parse(loc)
	if err != nil {
		return Event{}, fmt.Errorf("calendar: Google answered an event whose end cannot be read")
	}
	// The contracts' rule: busy unless marked free (transparent) or cancelled.
	busy := e.Status != "cancelled" && e.Transparency != "transparent"
	return Event{ID: e.ID, Key: googleKeyOf(e.ID), Start: start, End: end, Busy: busy}, nil
}

// Events implements [Backend] over the events list with singleEvents, which expands
// recurring appointments into their occurrences — without it the list answers the
// series. Cancelled occurrences are left out by Google unless asked for.
func (g *google) Events(ctx context.Context, cal string, from, to time.Time) ([]Event, error) {
	q := url.Values{}
	q.Set("timeMin", from.UTC().Format(time.RFC3339))
	q.Set("timeMax", to.UTC().Format(time.RFC3339))
	q.Set("singleEvents", "true")
	q.Set("maxResults", fmt.Sprint(graphPageSize))
	q.Set("fields", googleListFields)
	var out []Event
	for page := 0; ; page++ {
		if page == maxGraphPages {
			return nil, fmt.Errorf("calendar: the window holds more than %d appointments; read a shorter window", maxGraphPages*graphPageSize)
		}
		var resp struct {
			Items         []googleEvent `json:"items"`
			NextPageToken string        `json:"nextPageToken"`
			TimeZone      string        `json:"timeZone"`
		}
		if err := g.do(ctx, http.MethodGet, googleCalendarPath(cal)+"/events", q, nil, &resp); err != nil {
			return nil, classifyGoogle(err, false)
		}
		loc := time.UTC
		if resp.TimeZone != "" {
			if l, err := time.LoadLocation(resp.TimeZone); err == nil {
				loc = l
			}
		}
		for _, ge := range resp.Items {
			ev, err := ge.event(loc)
			if err != nil {
				return nil, err
			}
			if ev.overlaps(from, to) {
				out = append(out, ev)
			}
		}
		if resp.NextPageToken == "" {
			return out, nil
		}
		q.Set("pageToken", resp.NextPageToken)
	}
}

// Create implements [Backend]. The event's id is chosen from the create key, and Google
// refuses a second insert with an id it has seen — a deleted event's included. A
// repeated delivery therefore reads the event the first one made; if the conflict rule
// had given that one up, it is confirmed again and the rule runs on it again.
func (g *google) Create(ctx context.Context, cal string, ne NewEvent) (Event, error) {
	id := googleEventID(ne.Key)
	body := map[string]any{
		"id":           id,
		"summary":      ne.Subject,
		"start":        map[string]any{"dateTime": ne.Start.UTC().Format(time.RFC3339)},
		"end":          map[string]any{"dateTime": ne.End.UTC().Format(time.RFC3339)},
		"transparency": "opaque",
		"reminders":    map[string]any{"useDefault": false},
	}
	q := url.Values{"sendUpdates": {"none"}, "fields": {googleEventFields}}
	var ge googleEvent
	err := g.do(ctx, http.MethodPost, googleCalendarPath(cal)+"/events", q, body, &ge)
	if status, _ := googleStatus(err); status == http.StatusConflict {
		if err := g.do(ctx, http.MethodGet, googleEventPath(cal, id), url.Values{"fields": {googleEventFields}}, nil, &ge); err != nil {
			return Event{}, classifyGoogle(err, false)
		}
		if ge.Status == "cancelled" {
			revive := map[string]any{"status": "confirmed", "summary": ne.Subject, "start": body["start"], "end": body["end"]}
			if err := g.do(ctx, http.MethodPatch, googleEventPath(cal, id), q, revive, &ge); err != nil {
				return Event{}, classifyGoogle(err, true)
			}
		}
		return ge.event(time.UTC)
	}
	if err != nil {
		return Event{}, classifyGoogle(err, true)
	}
	return ge.event(time.UTC)
}

// Update implements [Backend]. sendUpdates=all is what makes Google send the attendees
// their invitation. A service account without domain-wide delegation is refused
// attendees (forbiddenForServiceAccounts), which the process can catch.
func (g *google) Update(ctx context.Context, cal, id string, d Details) (Event, error) {
	body := map[string]any{}
	if d.Subject != "" {
		body["summary"] = d.Subject
	}
	if d.Body != "" {
		body["description"] = d.Body
	}
	if len(d.Attendees) > 0 {
		list := make([]any, len(d.Attendees))
		for i, a := range d.Attendees {
			list[i] = map[string]any{"email": a.Email, "displayName": a.Name}
		}
		body["attendees"] = list
	}
	var ge googleEvent
	q := url.Values{"sendUpdates": {"all"}, "fields": {googleEventFields}}
	if err := g.do(ctx, http.MethodPatch, googleEventPath(cal, id), q, body, &ge); err != nil {
		if gone := g.eventGone(ctx, cal, err); gone != nil {
			return Event{}, gone
		}
		if _, reason := googleStatus(err); reason == "forbiddenForServiceAccounts" {
			return Event{}, &Error{Kind: KindAttendeesNotAllowed, Message: "Google refuses attendees from a service account without domain-wide delegation; set subject in the bundle, or let the confirmation mail come from the process"}
		}
		return Event{}, classifyGoogle(err, true)
	}
	if ge.Status == "cancelled" {
		return Event{}, &Error{Kind: KindBookingNotFound, Message: "the booking was cancelled in the calendar"}
	}
	return ge.event(time.UTC)
}

// Delete implements [Backend]. An event already gone is success.
func (g *google) Delete(ctx context.Context, cal, id string) error {
	err := g.do(ctx, http.MethodDelete, googleEventPath(cal, id), url.Values{"sendUpdates": {"all"}}, nil, nil)
	if err == nil {
		return nil
	}
	if gone := g.eventGone(ctx, cal, err); gone != nil {
		if kindOf(gone) == KindBookingNotFound {
			return nil
		}
		return gone
	}
	return classifyGoogle(err, true)
}

// Get implements [Backend]. A cancelled event is one that no longer exists.
func (g *google) Get(ctx context.Context, cal, id string) (Event, bool, error) {
	var ge googleEvent
	err := g.do(ctx, http.MethodGet, googleEventPath(cal, id), url.Values{"fields": {googleEventFields}}, nil, &ge)
	if err != nil {
		if gone := g.eventGone(ctx, cal, err); gone != nil {
			if kindOf(gone) == KindBookingNotFound {
				return Event{}, false, nil
			}
			return Event{}, false, gone
		}
		return Event{}, false, classifyGoogle(err, false)
	}
	if ge.Status == "cancelled" {
		return Event{}, false, nil
	}
	ev, err := ge.event(time.UTC)
	return ev, err == nil, err
}
