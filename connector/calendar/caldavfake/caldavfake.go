// Package caldavfake is an in-process stand-in for a CalDAV server (RFC 4791), as far
// as the calendar Worker Type's CalDAV backend uses one: a calendar home with calendar
// collections, a calendar-query with time-range and expand, and PUT, GET and DELETE of
// calendar objects under ETags. It exists for tests and for the conformance suites
// (ADR-0483, ADR-0552), so a record verifies anywhere, without a server.
//
// What it answers is written from the RFCs, not observed from Nextcloud or iCloud. What
// it does on purpose:
//
//   - It refuses a calendar-query that does not narrow calendar-data to times,
//     availability and status, so a backend that starts reading other people's
//     appointments fails its tests.
//   - It expands a recurring series into its occurrences only when the query asks for
//     expand; NoExpand makes it ignore the request, as a server without the feature
//     does, and the backend must then refuse rather than read a series as one event.
//   - It answers If-None-Match: * on an existing object and a stale If-Match with 412,
//     a PUT into a calendar that does not exist with 409, a write to a read-only
//     calendar with 403.
//   - It answers every read with everything written before it.
package caldavfake

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Username and Password are the credential the fake accepts.
const (
	Username = "atlas@firma.ch"
	Password = "app-password"
)

// HomePath is the calendar home below the fake's root.
const HomePath = "/dav/calendars/atlas/"

// Busy is an appointment a calendar already holds. Transparent marks one shown as free.
// Weekly, above one, makes it a series of that many weekly occurrences (an RRULE).
// AllDay makes it an all-day appointment on Start's date as written in Zone.
type Busy struct {
	Start       time.Time
	End         time.Time
	Transparent bool
	Weekly      int
	AllDay      bool
}

// Calendar is one calendar collection of the fake. ReadOnly refuses every write with
// 403, as a calendar shared read-only is refused.
type Calendar struct {
	ReadOnly bool
	Busy     []Busy
}

// Zone is the zone an all-day seed's date is written in.
var Zone = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		panic(err)
	}
	return loc
}()

// Object is one calendar object as a test inspects it.
type Object struct {
	Name        string
	UID         string
	Start, End  time.Time
	Transparent bool
	Cancelled   bool
	Summary     string
	Description string
	Attendees   []string
	Text        string
}

type object struct {
	text string
	etag string
}

type collection struct {
	readOnly bool
	objects  map[string]*object
	order    []string
}

// Server is the fake.
type Server struct {
	srv  *httptest.Server
	mu   sync.Mutex
	cals map[string]*collection
	tag  int
	// NoExpand makes the fake ignore expand, as a server without it does.
	NoExpand bool
}

const stamp = "20060102T150405Z"

// New starts the fake with the calendars named.
func New(cals map[string]Calendar) *Server {
	s := &Server{cals: map[string]*collection{}}
	names := make([]string, 0, len(cals))
	for n := range cals {
		names = append(names, n)
	}
	sort.Strings(names)
	seq := 0
	for _, n := range names {
		c := &collection{readOnly: cals[n].ReadOnly, objects: map[string]*object{}}
		for _, b := range cals[n].Busy {
			seq++
			uid := "seed-" + strconv.Itoa(seq)
			lines := []string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//caldavfake//EN", "BEGIN:VEVENT", "UID:" + uid,
				"SUMMARY:Privat", "DESCRIPTION:Arzttermin", "LOCATION:Bern"}
			if b.AllDay {
				lines = append(lines, "DTSTART;VALUE=DATE:"+b.Start.In(Zone).Format("20060102"), "DTEND;VALUE=DATE:"+b.End.In(Zone).Format("20060102"))
			} else {
				lines = append(lines, "DTSTART:"+b.Start.UTC().Format(stamp), "DTEND:"+b.End.UTC().Format(stamp))
			}
			if b.Transparent {
				lines = append(lines, "TRANSP:TRANSPARENT")
			}
			if b.Weekly > 1 {
				lines = append(lines, "RRULE:FREQ=WEEKLY;COUNT="+strconv.Itoa(b.Weekly))
			}
			lines = append(lines, "END:VEVENT", "END:VCALENDAR")
			s.tag++
			name := uid + ".ics"
			c.objects[name] = &object{text: strings.Join(lines, "\r\n") + "\r\n", etag: `"` + strconv.Itoa(s.tag) + `"`}
			c.order = append(c.order, name)
		}
		s.cals[n] = c
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Close stops the fake.
func (s *Server) Close() { s.srv.Close() }

// HomeURL is the calendar home the backend's endpoint names.
func (s *Server) HomeURL() string { return s.srv.URL + HomePath }

// HTTPClient reaches the fake.
func (s *Server) HTTPClient() *http.Client { return s.srv.Client() }

// Bundle is a calendar Worker bundle for the fake's account, reading floating and
// all-day times in zone.
func Bundle(zone string) string {
	return fmt.Sprintf(`{"provider":"caldav","username":%q,"password":%q,"timeZone":%q}`, Username, Password, zone)
}

// Objects returns a calendar's objects in the order they were first written.
func (s *Server) Objects(cal string) []Object {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cals[cal]
	if c == nil {
		return nil
	}
	var out []Object
	for _, name := range c.order {
		o, ok := c.objects[name]
		if !ok {
			continue
		}
		p := parse(o.text)
		out = append(out, Object{Name: name, UID: p.uid, Start: p.start, End: p.end, Transparent: p.transparent,
			Cancelled: p.cancelled, Summary: p.summary, Description: p.description, Attendees: p.attendees, Text: o.text})
	}
	return out
}

// parsed is the little of an object the fake reads.
type parsed struct {
	uid, summary, description string
	start, end                time.Time
	allDay                    bool
	weekly                    int
	transparent, cancelled    bool
	attendees                 []string
}

func parse(text string) parsed {
	var p parsed
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(l, " ") && len(lines) > 0 {
			lines[len(lines)-1] += l[1:]
			continue
		}
		lines = append(lines, l)
	}
	for _, l := range lines {
		head, value, _ := strings.Cut(l, ":")
		name, params, _ := strings.Cut(head, ";")
		switch strings.ToUpper(name) {
		case "UID":
			p.uid = value
		case "SUMMARY":
			p.summary = unescape(value)
		case "DESCRIPTION":
			p.description = unescape(value)
		case "DTSTART", "DTEND":
			var t time.Time
			if strings.Contains(params, "VALUE=DATE") {
				t, _ = time.ParseInLocation("20060102", value, Zone)
				p.allDay = true
			} else {
				t, _ = time.Parse(stamp, value)
			}
			if strings.EqualFold(name, "DTSTART") {
				p.start = t
			} else {
				p.end = t
			}
		case "TRANSP":
			p.transparent = value == "TRANSPARENT"
		case "STATUS":
			p.cancelled = value == "CANCELLED"
		case "RRULE":
			if _, c, ok := strings.Cut(value, "COUNT="); ok {
				p.weekly, _ = strconv.Atoi(strings.Split(c, ";")[0])
			}
		case "ATTENDEE":
			p.attendees = append(p.attendees, strings.TrimPrefix(value, "mailto:"))
		}
	}
	return p
}

// unescape reads a TEXT value (RFC 5545 §3.3.11).
func unescape(v string) string {
	return strings.NewReplacer(`\\`, `\`, `\;`, ";", `\,`, ",", `\n`, "\n", `\N`, "\n").Replace(v)
}

var rangeRE = regexp.MustCompile(`time-range start="(\d{8}T\d{6}Z)" end="(\d{8}T\d{6}Z)"`)

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	if !ok || user != Username || pass != Password {
		w.Header().Set("WWW-Authenticate", `Basic realm="caldavfake"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, HomePath)
	if !ok || rest == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	calName, member, _ := strings.Cut(rest, "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cals[calName]
	if member == "" {
		if c == nil {
			http.Error(w, "no such calendar", http.StatusNotFound)
			return
		}
		switch r.Method {
		case "PROPFIND":
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			fmt.Fprintf(w, `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:"><D:response><D:href>%s</D:href><D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`, r.URL.Path)
		case "REPORT":
			s.report(w, r, c)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if c == nil {
		if r.Method == http.MethodPut {
			http.Error(w, "the parent collection does not exist", http.StatusConflict)
			return
		}
		http.Error(w, "no such calendar", http.StatusNotFound)
		return
	}
	o := c.objects[member]
	switch r.Method {
	case http.MethodGet:
		if o == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("ETag", o.etag)
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		_, _ = w.Write([]byte(o.text))
	case http.MethodPut:
		if c.readOnly {
			http.Error(w, "read-only calendar", http.StatusForbidden)
			return
		}
		if r.Header.Get("If-None-Match") == "*" && o != nil {
			http.Error(w, "exists", http.StatusPreconditionFailed)
			return
		}
		if m := r.Header.Get("If-Match"); m != "" && (o == nil || m != o.etag) {
			http.Error(w, "changed", http.StatusPreconditionFailed)
			return
		}
		raw := make([]byte, 0, 4096)
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			raw = append(raw, buf[:n]...)
			if err != nil {
				break
			}
		}
		if !strings.Contains(string(raw), "BEGIN:VCALENDAR") {
			http.Error(w, "not a calendar object", http.StatusUnsupportedMediaType)
			return
		}
		s.tag++
		created := o == nil
		c.objects[member] = &object{text: string(raw), etag: `"` + strconv.Itoa(s.tag) + `"`}
		w.Header().Set("ETag", c.objects[member].etag)
		if created {
			c.order = append(c.order, member)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if c.readOnly {
			http.Error(w, "read-only calendar", http.StatusForbidden)
			return
		}
		if o == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		delete(c.objects, member)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func (s *Server) report(w http.ResponseWriter, r *http.Request, c *collection) {
	raw := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		raw = append(raw, buf[:n]...)
		if err != nil {
			break
		}
	}
	body := string(raw)
	if !strings.Contains(body, `<C:comp name="VEVENT">`) || strings.Contains(body, `name="SUMMARY"`) || strings.Contains(body, `name="DESCRIPTION"`) {
		http.Error(w, "the fake answers a calendar-query only when calendar-data is narrowed to times and availability", http.StatusBadRequest)
		return
	}
	m := rangeRE.FindStringSubmatch(body)
	if m == nil {
		http.Error(w, "a time-range is required", http.StatusBadRequest)
		return
	}
	from, _ := time.Parse(stamp, m[1])
	to, _ := time.Parse(stamp, m[2])
	expand := strings.Contains(body, "<C:expand") && !s.NoExpand
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">`)
	for _, name := range c.order {
		o, ok := c.objects[name]
		if !ok {
			continue
		}
		p := parse(o.text)
		var data string
		var instances []string
		for i := 0; i < max(p.weekly, 1); i++ {
			st, en := p.start.AddDate(0, 0, 7*i), p.end.AddDate(0, 0, 7*i)
			if !(st.Before(to) && from.Before(en)) {
				continue
			}
			if !expand {
				instances = append(instances, "")
				break
			}
			ev := []string{"BEGIN:VEVENT", "UID:" + p.uid, "DTSTART:" + st.UTC().Format(stamp), "DTEND:" + en.UTC().Format(stamp)}
			if p.weekly > 1 {
				ev = append(ev, "RECURRENCE-ID:"+st.UTC().Format(stamp))
			}
			if p.transparent {
				ev = append(ev, "TRANSP:TRANSPARENT")
			}
			if p.cancelled {
				ev = append(ev, "STATUS:CANCELLED")
			}
			instances = append(instances, strings.Join(append(ev, "END:VEVENT"), "\r\n"))
		}
		if len(instances) == 0 {
			continue
		}
		if expand {
			data = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" + strings.Join(instances, "\r\n") + "\r\nEND:VCALENDAR\r\n"
		} else {
			data = o.text
		}
		fmt.Fprintf(&sb, `<D:response><D:href>%s%s</D:href><D:propstat><D:prop><D:getetag>%s</D:getetag><C:calendar-data>%s</C:calendar-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`,
			r.URL.Path, name, xmlEscape(o.etag), xmlEscape(data))
	}
	sb.WriteString(`</D:multistatus>`)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = w.Write([]byte(sb.String()))
}
