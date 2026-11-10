package calendar

import (
	"bytes"
	"context"
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

// DefaultGraphURL is Microsoft Graph's public root.
const DefaultGraphURL = "https://graph.microsoft.com/v1.0"

// graphScope is the client-credentials scope: whatever application permissions the
// registration was granted, here Calendars.ReadWrite.
const graphScope = "https://graph.microsoft.com/.default"

// maxGraphPages bounds the pages one read of a window follows. A window that holds more
// appointments than that fails rather than answers: a busy list cut short would offer a
// slot that is taken.
const (
	graphPageSize = 250
	maxGraphPages = 20
)

// graphSelect is every field the backend reads of an event. Subject, body, organizer
// and attendees of other people's appointments are never asked for.
const graphSelect = "id,start,end,showAs,isCancelled,transactionId"

// graphTimeLayout is Graph's dateTimeTimeZone.dateTime: no offset, up to seven
// fractional digits; the zone travels beside it, and every request here asks for UTC.
const graphTimeLayout = "2006-01-02T15:04:05.9999999"

// graph is the Microsoft Graph backend. A calendar is a mailbox — a resource mailbox
// by default — named by its address or object id, and its default calendar is used.
type graph struct {
	tokens  oauth2.TokenSource
	baseURL string
	httpc   *http.Client
}

func newGraph(endpoint string, b Bundle, opts Options) (*graph, error) {
	if strings.TrimSpace(b.TenantID) == "" || strings.TrimSpace(b.ClientID) == "" || b.ClientSecret == "" {
		return nil, fmt.Errorf("calendar: a Microsoft Graph Worker needs tenantId, clientId and clientSecret of an app registration with the application permission Calendars.ReadWrite")
	}
	base := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if base == "" {
		base = DefaultGraphURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname()))) {
		return nil, fmt.Errorf("calendar: the endpoint %q is not an https URL of a Graph root", clip(endpoint))
	}
	httpc := opts.HTTPClient
	if httpc == nil {
		httpc = nettimeout.HTTPClient()
	}
	tokenURL := opts.TokenURL
	if tokenURL == "" {
		tokenURL = "https://login.microsoftonline.com/" + url.PathEscape(strings.TrimSpace(b.TenantID)) + "/oauth2/v2.0/token"
	}
	tokens := oauth2.NewCached(oauth2.ClientCredentials(oauth2.Config{
		HTTPClient: httpc, Kind: "calendar", TokenURL: tokenURL,
		ClientID: strings.TrimSpace(b.ClientID), ClientSecret: b.ClientSecret, Scope: graphScope,
	}), nil)
	return &graph{tokens: tokens, baseURL: base, httpc: httpc}, nil
}

func isLoopback(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func clip(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

// graphFailure is a request Graph answered with a non-2xx status.
type graphFailure struct {
	method, path string
	status       int
	code         string
	message      string
}

func (e *graphFailure) Error() string {
	return fmt.Sprintf("calendar: Microsoft Graph %s %s returned HTTP %d: %s %s", e.method, e.path, e.status, e.code, e.message)
}

// do sends one request and decodes a JSON answer into out (nil to discard it).
func (g *graph) do(ctx context.Context, method, target string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("calendar: encode request: %w", err)
		}
		rdr = bytes.NewReader(raw)
	}
	if !strings.HasPrefix(target, "http") {
		target = g.baseURL + target
	}
	if !strings.HasPrefix(target, g.baseURL+"/") {
		// A nextLink pointing anywhere but the Graph root this Worker trusts would carry
		// its bearer token there.
		return fmt.Errorf("calendar: Microsoft Graph answered a link outside its own root")
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
	// Every dateTime Graph answers is in UTC, so no zone name has to be mapped.
	req.Header.Set("Prefer", `outlook.timezone="UTC"`)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("calendar: Microsoft Graph %s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limits.Default().Payload+1))
	if err != nil {
		return fmt.Errorf("calendar: read Microsoft Graph's answer: %w", err)
	}
	if int64(len(raw)) > limits.Default().Payload {
		return fmt.Errorf("calendar: Microsoft Graph's answer exceeds the payload limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		f := &graphFailure{method: method, path: req.URL.Path, status: resp.StatusCode}
		var env struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil {
			f.code, f.message = env.Error.Code, env.Error.Message
		}
		return f
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("calendar: Microsoft Graph answered %s with something that is not the expected JSON", method)
	}
	return nil
}

// classify turns Graph's refusals into the kinds a process can act on. On a calendar,
// 404 is a mailbox that does not exist and 403 one the registration may not reach —
// which, for a read, is indistinguishable for the process from one that does not
// exist. On one event, 404 is a booking deleted since it was made only when Graph says
// the item is missing (ErrorItemNotFound); a 404 for the mailbox (ErrorInvalidUser,
// ResourceNotFound) is the calendar's. The codes are Graph's documented ones, not
// observed from a tenant.
func classify(err error, onEvent, write bool) error {
	f, ok := err.(*graphFailure)
	if !ok {
		return err
	}
	switch {
	case f.status == http.StatusNotFound && onEvent && f.code == "ErrorItemNotFound":
		return &Error{Kind: KindBookingNotFound, Message: "the booking does not exist in this calendar (" + f.code + ")"}
	case f.status == http.StatusNotFound:
		return &Error{Kind: KindCalendarNotFound, Message: "the calendar does not exist or this Worker cannot see it (" + f.code + ")"}
	case f.status == http.StatusForbidden && write:
		return &Error{Kind: KindCalendarNotWritable, Message: "this Worker may not write to the calendar (" + f.code + ")"}
	case f.status == http.StatusForbidden:
		return &Error{Kind: KindCalendarNotFound, Message: "this Worker may not read the calendar (" + f.code + ")"}
	}
	return err
}

type graphTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

func toGraphTime(t time.Time) graphTime {
	return graphTime{DateTime: t.UTC().Format("2006-01-02T15:04:05"), TimeZone: "UTC"}
}

func (gt graphTime) parse() (time.Time, error) {
	if gt.TimeZone != "UTC" && gt.TimeZone != "" {
		return time.Time{}, fmt.Errorf("calendar: Microsoft Graph answered a time in %q although UTC was asked for", gt.TimeZone)
	}
	return time.ParseInLocation(graphTimeLayout, gt.DateTime, time.UTC)
}

type graphEvent struct {
	ID            string    `json:"id"`
	Start         graphTime `json:"start"`
	End           graphTime `json:"end"`
	ShowAs        string    `json:"showAs"`
	IsCancelled   bool      `json:"isCancelled"`
	TransactionID string    `json:"transactionId"`
}

func (e graphEvent) event() (Event, error) {
	start, err := e.Start.parse()
	if err != nil {
		return Event{}, err
	}
	end, err := e.End.parse()
	if err != nil {
		return Event{}, err
	}
	// The contracts' rule: busy unless marked free, or cancelled. Tentative, out of
	// office, working elsewhere and unknown block their time.
	busy := !e.IsCancelled && !strings.EqualFold(e.ShowAs, "free")
	return Event{ID: e.ID, Key: e.TransactionID, Start: start, End: end, Busy: busy}, nil
}

func mailbox(calendar string) string { return "/users/" + url.PathEscape(strings.TrimSpace(calendar)) }

// Events implements [Backend] over calendarView, which expands recurring
// appointments into their occurrences — /events would answer the series instead.
func (g *graph) Events(ctx context.Context, calendar string, from, to time.Time) ([]Event, error) {
	q := url.Values{}
	q.Set("startDateTime", from.UTC().Format(time.RFC3339))
	q.Set("endDateTime", to.UTC().Format(time.RFC3339))
	q.Set("$select", graphSelect)
	q.Set("$top", fmt.Sprint(graphPageSize))
	next := mailbox(calendar) + "/calendar/calendarView?" + q.Encode()
	var out []Event
	for page := 0; next != ""; page++ {
		if page == maxGraphPages {
			return nil, fmt.Errorf("calendar: the window holds more than %d appointments; read a shorter window", maxGraphPages*graphPageSize)
		}
		var resp struct {
			Value    []graphEvent `json:"value"`
			NextLink string       `json:"@odata.nextLink"`
		}
		if err := g.do(ctx, http.MethodGet, next, nil, &resp); err != nil {
			return nil, classify(err, false, false)
		}
		for _, ge := range resp.Value {
			ev, err := ge.event()
			if err != nil {
				return nil, err
			}
			if ev.overlaps(from, to) {
				out = append(out, ev)
			}
		}
		next = resp.NextLink
	}
	return out, nil
}

// Create implements [Backend]. The create key is written as the event's transactionId,
// which Graph documents as the client's means to avoid a duplicate on a retried POST.
// How long Graph remembers one is not documented, so the backend also looks for an
// event with the key in the slot first and returns it: a delivery repeated after any
// delay finds its own reservation.
func (g *graph) Create(ctx context.Context, calendar string, ne NewEvent) (Event, error) {
	existing, err := g.Events(ctx, calendar, ne.Start, ne.End)
	if err != nil {
		return Event{}, err
	}
	for _, e := range existing {
		if ne.Key != "" && e.Key == ne.Key {
			return e, nil
		}
	}
	body := map[string]any{
		"subject":       ne.Subject,
		"start":         toGraphTime(ne.Start),
		"end":           toGraphTime(ne.End),
		"showAs":        "busy",
		"isReminderOn":  false,
		"transactionId": ne.Key,
	}
	var ge graphEvent
	if err := g.do(ctx, http.MethodPost, mailbox(calendar)+"/calendar/events", body, &ge); err != nil {
		return Event{}, classify(err, false, true)
	}
	return ge.event()
}

// Update implements [Backend]. Attendees added to an event in the calendar of its
// organizer — here the booking mailbox — are sent an invitation by Exchange; that is
// why reserve-slot adds none.
func (g *graph) Update(ctx context.Context, calendar, id string, d Details) (Event, error) {
	body := map[string]any{}
	if d.Subject != "" {
		body["subject"] = d.Subject
	}
	if d.Body != "" {
		body["body"] = map[string]any{"contentType": "text", "content": d.Body}
	}
	if len(d.Attendees) > 0 {
		list := make([]any, len(d.Attendees))
		for i, a := range d.Attendees {
			list[i] = map[string]any{"type": "required", "emailAddress": map[string]any{"address": a.Email, "name": a.Name}}
		}
		body["attendees"] = list
	}
	var ge graphEvent
	path := mailbox(calendar) + "/events/" + url.PathEscape(id) + "?$select=" + graphSelect
	if err := g.do(ctx, http.MethodPatch, path, body, &ge); err != nil {
		return Event{}, classify(err, true, true)
	}
	return ge.event()
}

// Delete implements [Backend].
func (g *graph) Delete(ctx context.Context, calendar, id string) error {
	err := g.do(ctx, http.MethodDelete, mailbox(calendar)+"/events/"+url.PathEscape(id), nil, nil)
	if err == nil {
		return nil
	}
	e := classify(err, true, true)
	if kindOf(e) == KindBookingNotFound {
		return nil // already gone
	}
	return e
}

// Get implements [Backend].
func (g *graph) Get(ctx context.Context, calendar, id string) (Event, bool, error) {
	var ge graphEvent
	err := g.do(ctx, http.MethodGet, mailbox(calendar)+"/events/"+url.PathEscape(id)+"?$select="+graphSelect, nil, &ge)
	if err != nil {
		e := classify(err, true, false)
		if kindOf(e) == KindBookingNotFound {
			return Event{}, false, nil
		}
		return Event{}, false, e
	}
	ev, err := ge.event()
	return ev, err == nil, err
}
