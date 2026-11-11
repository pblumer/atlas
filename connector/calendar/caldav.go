package calendar

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/limits"
)

// caldavUIDPrefix starts the UID of every event this backend creates, and its resource
// name; behind it stands the create key, so a repeated delivery finds its own event and
// the key reads back from the UID.
const caldavUIDPrefix = "atlas-"

func caldavResource(key string) string {
	return caldavUIDPrefix + hex.EncodeToString([]byte(key)) + ".ics"
}

func caldavKeyOf(uid string) string {
	k, ok := strings.CutPrefix(uid, caldavUIDPrefix)
	if !ok {
		return ""
	}
	return k
}

// caldavTimeFormat is the UTC form a CalDAV time-range and expand take.
const caldavTimeFormat = "20060102T150405Z"

// caldav is the CalDAV backend (RFC 4791): Nextcloud, iCloud, Fastmail, Synology, SOGo,
// Radicale, Baïkal and most hosting providers' calendars. The Worker's endpoint is the
// calendar home — the collection that holds the calendars — and a task names a calendar
// by its collection name below it, or by its full URL on the same host.
//
// It is not the CardDAV Worker's client: that one is built around an address book's
// discovery and vCard, and is not exported. This one speaks the four requests a booking
// needs and keeps that client's rules for a credential — https only, no redirect to
// another host or down to http, every answer bounded.
type caldav struct {
	home     *url.URL
	username string
	password string
	zone     *time.Location
	httpc    *http.Client
	now      func() time.Time
}

func newCalDAV(endpoint string, b Bundle, opts Options) (*caldav, error) {
	if strings.TrimSpace(b.Username) == "" || b.Password == "" {
		return nil, fmt.Errorf("calendar: a CalDAV Worker needs a username and a password (usually an app-specific password from the provider)")
	}
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return nil, fmt.Errorf("calendar: a CalDAV Worker needs an endpoint, the URL of the calendar home (https://cloud.example.ch/remote.php/dav/calendars/verkauf/)")
	}
	home, err := url.Parse(raw)
	if err != nil || home.Host == "" || (home.Scheme != "https" && !(home.Scheme == "http" && isLoopback(home.Hostname()))) {
		return nil, fmt.Errorf("calendar: the endpoint %q is not an https URL of a calendar home", clip(endpoint))
	}
	if !strings.HasSuffix(home.Path, "/") {
		home.Path += "/"
	}
	zone := time.UTC
	if tz := strings.TrimSpace(b.TimeZone); tz != "" {
		if zone, err = time.LoadLocation(tz); err != nil {
			return nil, fmt.Errorf("calendar: the bundle's timeZone %q is not an IANA name such as Europe/Zurich", clip(tz))
		}
	}
	base := opts.HTTPClient
	if base == nil {
		base = nettimeout.HTTPClient()
	}
	httpc := *base
	httpc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("the server redirected more than 5 times")
		}
		if req.URL.Host != home.Host || (home.Scheme == "https" && req.URL.Scheme != "https") {
			return fmt.Errorf("the server redirected to another host or to http, where the credential would go; refused")
		}
		return nil
	}
	return &caldav{home: home, username: strings.TrimSpace(b.Username), password: b.Password, zone: zone, httpc: &httpc, now: time.Now}, nil
}

// collection is the calendar's collection URL: a name below the home, or a URL on the
// home's own host — the credential goes nowhere else.
func (c *caldav) collection(cal string) (*url.URL, error) {
	cal = strings.TrimSpace(cal)
	if strings.Contains(cal, "://") {
		u, err := url.Parse(cal)
		if err != nil || u.Host != c.home.Host || u.Scheme != c.home.Scheme {
			return nil, &Error{Kind: KindCalendarNotFound, Message: "the calendar URL is not on the host of this Worker's endpoint"}
		}
		if !strings.HasSuffix(u.Path, "/") {
			u.Path += "/"
		}
		return u, nil
	}
	if cal == "" || strings.Contains(cal, "/") || cal == "." || cal == ".." {
		return nil, &Error{Kind: KindCalendarNotFound, Message: "a calendar is named by its collection name below the endpoint, or by its URL"}
	}
	u := *c.home
	u.Path = c.home.Path + cal + "/"
	u.RawPath = ""
	return &u, nil
}

func (c *caldav) member(coll *url.URL, id string) (*url.URL, error) {
	if id == "" || strings.Contains(id, "/") || id == "." || id == ".." {
		return nil, &Error{Kind: KindBookingNotFound, Message: "the booking id is not a resource name of this calendar"}
	}
	u := *coll
	u.Path = coll.Path + id
	u.RawPath = ""
	return &u, nil
}

type davReply struct {
	status int
	etag   string
	body   []byte
}

func (c *caldav) do(ctx context.Context, method string, u *url.URL, hdr map[string]string, body []byte) (*davReply, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, fmt.Errorf("calendar: build %s request: %w", method, err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("User-Agent", "atlas-calendar-worker")
	resp, err := c.httpc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("calendar: CalDAV %s: %v", method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limits.Default().Payload+1))
	if err != nil {
		return nil, fmt.Errorf("calendar: read the CalDAV server's answer: %w", err)
	}
	if int64(len(raw)) > limits.Default().Payload {
		return nil, fmt.Errorf("calendar: the CalDAV server's answer exceeds the payload limit; read a shorter window")
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("calendar: the CalDAV server refused the credential (HTTP 401); where the provider issues app-specific passwords, the account's own one is refused")
	}
	return &davReply{status: resp.StatusCode, etag: resp.Header.Get("ETag"), body: raw}, nil
}

func davStatusError(method string, status int) error {
	return fmt.Errorf("calendar: CalDAV %s answered HTTP %d", method, status)
}

// calendarGone tells, after a member answered 404, whether the member or the calendar is
// missing: a server answers both alike, so the collection is asked.
func (c *caldav) calendarGone(ctx context.Context, coll *url.URL) error {
	r, err := c.do(ctx, "PROPFIND", coll, map[string]string{"Depth": "0", "Content-Type": "application/xml; charset=utf-8"},
		[]byte(`<?xml version="1.0" encoding="utf-8"?><D:propfind xmlns:D="DAV:"><D:prop><D:resourcetype/></D:prop></D:propfind>`))
	if err != nil {
		return err
	}
	switch {
	case r.status == http.StatusNotFound:
		return &Error{Kind: KindCalendarNotFound, Message: "the calendar does not exist or this Worker cannot see it"}
	case r.status == http.StatusForbidden:
		return &Error{Kind: KindCalendarNotFound, Message: "this Worker may not read the calendar"}
	case r.status >= 300:
		return davStatusError("PROPFIND", r.status)
	}
	return &Error{Kind: KindBookingNotFound, Message: "the booking does not exist in this calendar"}
}

type davMultistatus struct {
	Responses []struct {
		Href     string `xml:"DAV: href"`
		Propstat []struct {
			Status string `xml:"DAV: status"`
			Prop   struct {
				Data string `xml:"urn:ietf:params:xml:ns:caldav calendar-data"`
			} `xml:"DAV: prop"`
		} `xml:"DAV: propstat"`
	} `xml:"DAV: response"`
}

// eventsQuery is a calendar-query for the VEVENTs overlapping a window, expanded into
// their occurrences by the server and narrowed to the properties availability reads:
// no SUMMARY, DESCRIPTION, LOCATION or ATTENDEE of anybody else's appointment.
func eventsQuery(from, to time.Time) []byte {
	s, e := from.UTC().Format(caldavTimeFormat), to.UTC().Format(caldavTimeFormat)
	return []byte(`<?xml version="1.0" encoding="utf-8"?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop>
    <D:getetag/>
    <C:calendar-data>
      <C:expand start="` + s + `" end="` + e + `"/>
      <C:comp name="VCALENDAR">
        <C:comp name="VEVENT">
          <C:prop name="UID"/><C:prop name="DTSTART"/><C:prop name="DTEND"/><C:prop name="DURATION"/>
          <C:prop name="TRANSP"/><C:prop name="STATUS"/><C:prop name="RECURRENCE-ID"/>
        </C:comp>
      </C:comp>
    </C:calendar-data>
  </D:prop>
  <C:filter>
    <C:comp-filter name="VCALENDAR">
      <C:comp-filter name="VEVENT"><C:time-range start="` + s + `" end="` + e + `"/></C:comp-filter>
    </C:comp-filter>
  </C:filter>
</C:calendar-query>`)
}

func lastSegmentOf(href string) string {
	p := href
	if u, err := url.Parse(href); err == nil {
		p = u.Path
	}
	return path.Base(strings.TrimSuffix(p, "/"))
}

// Events implements [Backend].
func (c *caldav) Events(ctx context.Context, cal string, from, to time.Time) ([]Event, error) {
	coll, err := c.collection(cal)
	if err != nil {
		return nil, err
	}
	r, err := c.do(ctx, "REPORT", coll, map[string]string{"Depth": "1", "Content-Type": "application/xml; charset=utf-8"}, eventsQuery(from, to))
	if err != nil {
		return nil, err
	}
	switch {
	case r.status == http.StatusNotFound:
		return nil, &Error{Kind: KindCalendarNotFound, Message: "the calendar does not exist or this Worker cannot see it"}
	case r.status == http.StatusForbidden:
		return nil, &Error{Kind: KindCalendarNotFound, Message: "this Worker may not read the calendar"}
	case r.status != http.StatusMultiStatus:
		return nil, davStatusError("REPORT", r.status)
	}
	var ms davMultistatus
	if err := xml.Unmarshal(r.body, &ms); err != nil {
		return nil, fmt.Errorf("calendar: the CalDAV server answered the calendar-query with something that is not a multistatus")
	}
	var out []Event
	for _, resp := range ms.Responses {
		for _, ps := range resp.Propstat {
			if !strings.Contains(ps.Status, " 200") || strings.TrimSpace(ps.Prop.Data) == "" {
				continue
			}
			evs, err := parseICalEvents(ps.Prop.Data, c.zone)
			if err != nil {
				return nil, fmt.Errorf("calendar: %w", err)
			}
			for _, e := range evs {
				ev := Event{ID: lastSegmentOf(resp.Href), Key: caldavKeyOf(e.uid), Start: e.start, End: e.end, Busy: e.busy}
				if ev.overlaps(from, to) {
					out = append(out, ev)
				}
			}
			if len(out) > maxGraphPages*graphPageSize {
				return nil, fmt.Errorf("calendar: the window holds more than %d appointments; read a shorter window", maxGraphPages*graphPageSize)
			}
		}
	}
	return out, nil
}

// fetch reads one calendar object: its text and its ETag.
func (c *caldav) fetch(ctx context.Context, coll *url.URL, id string) (string, string, error) {
	u, err := c.member(coll, id)
	if err != nil {
		return "", "", err
	}
	r, err := c.do(ctx, http.MethodGet, u, nil, nil)
	if err != nil {
		return "", "", err
	}
	switch {
	case r.status == http.StatusNotFound || r.status == http.StatusGone:
		return "", "", c.calendarGone(ctx, coll)
	case r.status == http.StatusForbidden:
		return "", "", &Error{Kind: KindCalendarNotFound, Message: "this Worker may not read the calendar"}
	case r.status != http.StatusOK:
		return "", "", davStatusError("GET", r.status)
	}
	return string(r.body), r.etag, nil
}

func (c *caldav) eventOf(id, text string) (Event, bool, error) {
	evs, err := parseICalEvents(text, c.zone)
	if err != nil {
		return Event{}, false, fmt.Errorf("calendar: %w", err)
	}
	if len(evs) == 0 {
		return Event{}, false, fmt.Errorf("calendar: the calendar object %s holds no event", clip(id))
	}
	e := evs[0]
	return Event{ID: id, Key: caldavKeyOf(e.uid), Start: e.start, End: e.end, Busy: e.busy}, !e.cancelled, nil
}

// Create implements [Backend]: a PUT under a resource name chosen from the create key,
// with If-None-Match: *, so a repeated delivery is refused and reads the event the first
// one wrote. A reservation the conflict rule gave up was deleted, so a later delivery
// writes it afresh and the rule runs on it again.
func (c *caldav) Create(ctx context.Context, cal string, ne NewEvent) (Event, error) {
	coll, err := c.collection(cal)
	if err != nil {
		return Event{}, err
	}
	id := caldavResource(ne.Key)
	u, err := c.member(coll, id)
	if err != nil {
		return Event{}, err
	}
	body := icalPlaceholder(caldavUIDPrefix+ne.Key, ne.Subject, ne.Start, ne.End, c.now())
	r, err := c.do(ctx, http.MethodPut, u, map[string]string{"If-None-Match": "*", "Content-Type": "text/calendar; charset=utf-8"}, []byte(body))
	if err != nil {
		return Event{}, err
	}
	switch {
	case r.status == http.StatusCreated || r.status == http.StatusNoContent || r.status == http.StatusOK:
		return Event{ID: id, Key: ne.Key, Start: ne.Start, End: ne.End, Busy: true}, nil
	case r.status == http.StatusPreconditionFailed:
		text, _, err := c.fetch(ctx, coll, id)
		if err != nil {
			return Event{}, err
		}
		ev, _, err := c.eventOf(id, text)
		return ev, err
	case r.status == http.StatusNotFound || r.status == http.StatusConflict:
		return Event{}, &Error{Kind: KindCalendarNotFound, Message: "the calendar does not exist or this Worker cannot see it"}
	case r.status == http.StatusForbidden:
		return Event{}, &Error{Kind: KindCalendarNotWritable, Message: "this Worker may not write to the calendar"}
	}
	return Event{}, davStatusError("PUT", r.status)
}

// Update implements [Backend]: the event is read with its ETag, its SUMMARY, DESCRIPTION
// and ATTENDEE lines replaced and everything else kept, and written back with If-Match,
// so a change somebody made in between is not overwritten blindly — the read is
// repeated once. Whether the server sends the attendees an invitation is its own
// scheduling (RFC 6638); the attendees are on the event either way.
func (c *caldav) Update(ctx context.Context, cal, id string, d Details) (Event, error) {
	coll, err := c.collection(cal)
	if err != nil {
		return Event{}, err
	}
	u, err := c.member(coll, id)
	if err != nil {
		return Event{}, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		text, etag, err := c.fetch(ctx, coll, id)
		if err != nil {
			return Event{}, err
		}
		ev, live, err := c.eventOf(id, text)
		if err != nil {
			return Event{}, err
		}
		if !live {
			return Event{}, &Error{Kind: KindBookingNotFound, Message: "the booking was cancelled in the calendar"}
		}
		hdr := map[string]string{"Content-Type": "text/calendar; charset=utf-8"}
		if etag != "" {
			hdr["If-Match"] = etag
		}
		r, err := c.do(ctx, http.MethodPut, u, hdr, []byte(icalConfirm(text, d)))
		if err != nil {
			return Event{}, err
		}
		switch {
		case r.status == http.StatusCreated || r.status == http.StatusNoContent || r.status == http.StatusOK:
			return ev, nil
		case r.status == http.StatusPreconditionFailed:
			continue // changed in between: read it again
		case r.status == http.StatusForbidden:
			return Event{}, &Error{Kind: KindCalendarNotWritable, Message: "this Worker may not write to the calendar"}
		}
		return Event{}, davStatusError("PUT", r.status)
	}
	return Event{}, fmt.Errorf("calendar: the booking kept changing while it was being confirmed; the job is retried")
}

// Delete implements [Backend]. An event already gone is success.
func (c *caldav) Delete(ctx context.Context, cal, id string) error {
	coll, err := c.collection(cal)
	if err != nil {
		return err
	}
	u, err := c.member(coll, id)
	if err != nil {
		return err
	}
	r, err := c.do(ctx, http.MethodDelete, u, nil, nil)
	if err != nil {
		return err
	}
	switch {
	case r.status == http.StatusNoContent || r.status == http.StatusOK:
		return nil
	case r.status == http.StatusNotFound || r.status == http.StatusGone:
		if gone := c.calendarGone(ctx, coll); kindOf(gone) != KindBookingNotFound {
			return gone
		}
		return nil
	case r.status == http.StatusForbidden:
		return &Error{Kind: KindCalendarNotWritable, Message: "this Worker may not change the calendar"}
	}
	return davStatusError("DELETE", r.status)
}

// Get implements [Backend]. A cancelled event is one that no longer exists.
func (c *caldav) Get(ctx context.Context, cal, id string) (Event, bool, error) {
	coll, err := c.collection(cal)
	if err != nil {
		return Event{}, false, err
	}
	text, _, err := c.fetch(ctx, coll, id)
	if kindOf(err) == KindBookingNotFound {
		return Event{}, false, nil
	}
	if err != nil {
		return Event{}, false, err
	}
	ev, live, err := c.eventOf(id, text)
	if err != nil {
		return Event{}, false, err
	}
	return ev, live, nil
}
