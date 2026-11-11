package calendar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/calendar/caldavfake"
	"github.com/pblumer/atlas/connector/calendar/catalog"
)

func caldavRegistry(t *testing.T, cals map[string]caldavfake.Calendar) (*Registry, *caldavfake.Server) {
	t.Helper()
	fake := caldavfake.New(cals)
	t.Cleanup(fake.Close)
	b, err := NewBackend(fake.HomeURL(), caldavfake.Bundle("Europe/Zurich"), Options{HTTPClient: fake.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register("shop", b)
	return reg, fake
}

func TestTheCalDAVBundleIsReadStrictly(t *testing.T) {
	for name, tc := range map[string]struct{ endpoint, bundle, want string }{
		"no password":    {"https://dav.example.ch/cal/", `{"provider":"caldav","username":"u"}`, "needs a username and a password"},
		"no endpoint":    {"", `{"provider":"caldav","username":"u","password":"p"}`, "needs an endpoint"},
		"plain http":     {"http://dav.example.ch/cal/", `{"provider":"caldav","username":"u","password":"p"}`, "not an https URL"},
		"a bad zone":     {"https://dav.example.ch/cal/", `{"provider":"caldav","username":"u","password":"p","timeZone":"Bern"}`, "IANA"},
		"a Google field": {"https://dav.example.ch/cal/", `{"provider":"caldav","username":"u","password":"p","subject":"x"}`, "subject belongs to another provider"},
		"a CalDAV field": {"", `{"provider":"mock","password":"p"}`, "password belongs to another provider"},
	} {
		err := CheckBundle(tc.endpoint, tc.bundle)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	if err := CheckBundle("https://dav.example.ch/remote.php/dav/calendars/u", `{"provider":"caldav","username":"u","password":"p"}`); err != nil {
		t.Errorf("a complete CalDAV bundle: %v", err)
	}
}

func TestCalDAVWalksABookingFromReservationToCancellation(t *testing.T) {
	reg, fake := caldavRegistry(t, map[string]caldavfake.Calendar{"werkstatt": {Busy: []caldavfake.Busy{
		{Start: at(25, 10, 0), End: at(25, 11, 0)},
		{Start: at(25, 13, 30), End: at(25, 18, 0), Transparent: true},
		{Start: at(26, 0, 0), End: at(27, 0, 0), AllDay: true},
		{Start: at(18, 8, 0), End: at(18, 9, 0), Weekly: 3},
	}}})
	ctx := context.Background()
	out, err := RunAt(ctx, findJob("werkstatt"), reg, now)
	if err != nil {
		t.Fatal(err)
	}
	// 08:00 is taken by the weekly series' occurrence on the 25th, 10:00 by the
	// appointment, Tuesday by the all-day one; the afternoon marked free is offered.
	if got := times(slotsOf(t, out)); got != "25 09:00,25 11:00,25 13:30,25 14:30,25 15:30,25 16:30" {
		t.Fatalf("slots %s", got)
	}

	res := reserveJob("k1", at(25, 9, 0), at(25, 10, 0))
	res.Calendar = "werkstatt"
	out, err = RunAt(ctx, res, reg, now)
	if err != nil {
		t.Fatal(err)
	}
	id := out["booking"].(map[string]any)["bookingId"].(string)
	if id != caldavResource("k1") {
		t.Fatalf("the booking id %q is not the resource chosen from the key", id)
	}
	if again, err := RunAt(ctx, res, reg, now); err != nil || again["booking"].(map[string]any)["bookingId"] != id {
		t.Fatalf("a repeated delivery: %v %v", again, err)
	}
	base := Job{Connector: "shop", Calendar: "werkstatt", BookingID: id, TimeZone: "Europe/Zurich", ResultVariable: "b", ErrorCodes: Codes}

	confirm := base
	confirm.Operation = catalog.OpConfirmBooking
	confirm.Subject, confirm.Body = "Veloservice – Muster, E-Bike", "Bremsen; quietschen\nseit Montag"
	confirm.Attendees = []Attendee{{Email: "kundin@example.ch", Name: `Eva "Evi" Muster`}}
	if _, err := RunAt(ctx, confirm, reg, now); err != nil {
		t.Fatal(err)
	}
	var mine caldavfake.Object
	for _, o := range fake.Objects("werkstatt") {
		if o.Name == id {
			mine = o
		}
	}
	if mine.Summary != "Veloservice – Muster, E-Bike" || mine.Description != "Bremsen; quietschen\nseit Montag" ||
		len(mine.Attendees) != 1 || mine.Attendees[0] != "kundin@example.ch" || mine.UID != "atlas-k1" {
		t.Fatalf("confirmed object %+v", mine)
	}

	get := base
	get.Operation = catalog.OpGetBooking
	out, err = RunAt(ctx, get, reg, now)
	if err != nil || out["b"].(map[string]any)["exists"] != true || out["b"].(map[string]any)["start"] != "2030-03-25T09:00:00+01:00" {
		t.Fatalf("get: %v %v", out, err)
	}
	cancel := base
	cancel.Operation, cancel.ResultVariable = catalog.OpCancelBooking, ""
	for i := 0; i < 2; i++ {
		if _, err := RunAt(ctx, cancel, reg, now); err != nil {
			t.Fatalf("cancel %d: %v", i, err)
		}
	}
	if out, err = RunAt(ctx, get, reg, now); err != nil || out["b"].(map[string]any)["exists"] != false {
		t.Fatalf("get after cancel: %v %v", out, err)
	}
	if _, err := RunAt(ctx, confirm, reg, now); bpmnCode(err) != CodeBookingNotFound {
		t.Fatalf("confirm after cancel: %v", err)
	}
}

func TestCalDAVTellsACalendarFromABooking(t *testing.T) {
	reg, _ := caldavRegistry(t, map[string]caldavfake.Calendar{"werkstatt": {}, "lesen": {ReadOnly: true}})
	ctx := context.Background()
	for name, tc := range map[string]struct {
		job  Job
		code string
	}{
		"find in an unknown calendar":    {findJob("fehlt"), CodeCalendarNotFound},
		"reserve in a read-only one":     {func() Job { j := reserveJob("k2", at(25, 9, 0), at(25, 10, 0)); j.Calendar = "lesen"; return j }(), CodeCalendarNotWritable},
		"reserve in an unknown one":      {func() Job { j := reserveJob("k3", at(25, 9, 0), at(25, 10, 0)); j.Calendar = "fehlt"; return j }(), CodeCalendarNotFound},
		"get from an unknown calendar":   {Job{Connector: "shop", Operation: catalog.OpGetBooking, Calendar: "fehlt", BookingID: "x.ics", ResultVariable: "b", ErrorCodes: Codes}, CodeCalendarNotFound},
		"cancel in an unknown calendar":  {Job{Connector: "shop", Operation: catalog.OpCancelBooking, Calendar: "fehlt", BookingID: "x.ics", ErrorCodes: Codes}, CodeCalendarNotFound},
		"cancel in a read-only calendar": {Job{Connector: "shop", Operation: catalog.OpCancelBooking, Calendar: "lesen", BookingID: "x.ics", ErrorCodes: Codes}, CodeCalendarNotWritable},
		"confirm a booking not there":    {Job{Connector: "shop", Operation: catalog.OpConfirmBooking, Calendar: "werkstatt", BookingID: "x.ics", ErrorCodes: Codes}, CodeBookingNotFound},
		"a calendar named with a slash":  {Job{Connector: "shop", Operation: catalog.OpGetBooking, Calendar: "../andere", BookingID: "x.ics", ResultVariable: "b", ErrorCodes: Codes}, CodeCalendarNotFound},
		"a booking named with a slash":   {Job{Connector: "shop", Operation: catalog.OpConfirmBooking, Calendar: "werkstatt", BookingID: "../x.ics", ErrorCodes: Codes}, CodeBookingNotFound},
		"a calendar on another host":     {Job{Connector: "shop", Operation: catalog.OpGetBooking, Calendar: "https://evil.example/cal/", BookingID: "x.ics", ResultVariable: "b", ErrorCodes: Codes}, CodeCalendarNotFound},
	} {
		if _, err := RunAt(ctx, tc.job, reg, now); bpmnCode(err) != tc.code {
			t.Errorf("%s: err = %v, want %s", name, err, tc.code)
		}
	}
}

func TestCalDAVTakesACalendarByItsURLOnTheSameHost(t *testing.T) {
	reg, fake := caldavRegistry(t, map[string]caldavfake.Calendar{"werkstatt": {}})
	if _, err := RunAt(context.Background(), findJob(fake.HomeURL()+"werkstatt"), reg, now); err != nil {
		t.Fatalf("a calendar by its URL: %v", err)
	}
}

func TestCalDAVRefusesASeriesTheServerDidNotExpand(t *testing.T) {
	reg, fake := caldavRegistry(t, map[string]caldavfake.Calendar{"werkstatt": {Busy: []caldavfake.Busy{{Start: at(18, 8, 0), End: at(18, 9, 0), Weekly: 3}}}})
	fake.NoExpand = true
	_, err := RunAt(context.Background(), findJob("werkstatt"), reg, now)
	if err == nil || !strings.Contains(err.Error(), "expand") {
		t.Fatalf("err = %v; a series read as one event would leave its later occurrences free", err)
	}
}

func TestCalDAVNeverSendsTheCredentialElsewhere(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the backend followed a redirect to another host")
	}))
	defer evil.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/denied/") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, evil.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	b, err := NewBackend(srv.URL+"/dav/", `{"provider":"caldav","username":"u","password":"p"}`, Options{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Events(context.Background(), "werkstatt", at(25, 0, 0), at(26, 0, 0)); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("err = %v", err)
	}
	if _, err := b.Events(context.Background(), "denied", at(25, 0, 0), at(26, 0, 0)); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}

// changingServer answers the first If-Match PUT with 412, as a server does when
// somebody changed the event between the read and the write.
type changingServer struct {
	*caldavfake.Server
	refused bool
}

func TestAConfirmationReadsAgainWhenTheEventChangedInBetween(t *testing.T) {
	fake := caldavfake.New(map[string]caldavfake.Calendar{"werkstatt": {}})
	defer fake.Close()
	cs := &changingServer{Server: fake}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.Header.Get("If-Match") != "" && !cs.refused {
			cs.refused = true
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Scheme, r2.URL.Host, r2.RequestURI = "http", strings.TrimPrefix(fake.HomeURL(), "http://")[:strings.Index(strings.TrimPrefix(fake.HomeURL(), "http://"), "/")], ""
		resp, err := fake.HTTPClient().Do(r2)
		if err != nil {
			t.Error(err)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		buf := make([]byte, 1<<16)
		n, _ := resp.Body.Read(buf)
		_, _ = w.Write(buf[:n])
	}))
	defer front.Close()
	b, err := NewBackend(front.URL+caldavfake.HomePath, caldavfake.Bundle("UTC"), Options{HTTPClient: front.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := b.Create(context.Background(), "werkstatt", NewEvent{Key: "k", Subject: "x", Start: at(25, 9, 0), End: at(25, 10, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Update(context.Background(), "werkstatt", ev.ID, Details{Subject: "y"}); err != nil || !cs.refused {
		t.Fatalf("update: %v (refused once: %v)", err, cs.refused)
	}
	if o := fake.Objects("werkstatt"); len(o) != 1 || o[0].Summary != "y" {
		t.Fatalf("objects %+v", o)
	}
}

func TestICalendarIsReadAsWritten(t *testing.T) {
	zone := zurich()
	text := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:a\r\nDTSTART;TZID=Europe/Zurich:20300325T090000\r\nDURATION:PT1H30M\r\n" +
		"BEGIN:VALARM\r\nTRIGGER:-PT15M\r\nDTSTART:19700101T000000Z\r\nEND:VALARM\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:b\r\nDTSTART:20300325T120000\r\nSTATUS:CANCELLED\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:c\r\nDTSTART;VALUE=DATE:20300326\r\nTRANSP:TRANSPARENT\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:d\r\nDTSTART:20300327T080000Z\r\nDURATION:P1W\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	evs, err := parseICalEvents(text, zone)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 4 {
		t.Fatalf("%d events", len(evs))
	}
	if !evs[0].start.Equal(at(25, 9, 0)) || !evs[0].end.Equal(at(25, 10, 30)) || !evs[0].busy {
		t.Errorf("a: %+v; the alarm's DTSTART must not be the event's", evs[0])
	}
	if !evs[1].start.Equal(at(25, 12, 0)) || !evs[1].end.Equal(evs[1].start) || evs[1].busy || !evs[1].cancelled {
		t.Errorf("b: %+v", evs[1])
	}
	if !evs[2].start.Equal(at(26, 0, 0)) || !evs[2].end.Equal(at(27, 0, 0)) || evs[2].busy {
		t.Errorf("c: %+v", evs[2])
	}
	if evs[3].end.Sub(evs[3].start) != 7*24*time.Hour {
		t.Errorf("d: %+v", evs[3])
	}
	for name, bad := range map[string]string{
		"a series":          "BEGIN:VEVENT\nDTSTART:20300325T090000Z\nRRULE:FREQ=DAILY\nEND:VEVENT\n",
		"no start":          "BEGIN:VEVENT\nUID:x\nEND:VEVENT\n",
		"an unknown zone":   "BEGIN:VEVENT\nDTSTART;TZID=W. Europe Standard Time:20300325T090000\nEND:VEVENT\n",
		"a bad duration":    "BEGIN:VEVENT\nDTSTART:20300325T090000Z\nDURATION:1H\nEND:VEVENT\n",
		"a bad unit":        "BEGIN:VEVENT\nDTSTART:20300325T090000Z\nDURATION:PT1Y\nEND:VEVENT\n",
		"a unit no number":  "BEGIN:VEVENT\nDTSTART:20300325T090000Z\nDURATION:PTH\nEND:VEVENT\n",
		"an unreadable end": "BEGIN:VEVENT\nDTSTART:20300325T090000Z\nDTEND:morgen\nEND:VEVENT\n",
	} {
		if _, err := parseICalEvents(bad, zone); err == nil {
			t.Errorf("%s: no refusal", name)
		}
	}
	if d, err := icalDuration("-PT15M"); err != nil || d != -15*time.Minute {
		t.Errorf("a negative duration: %v %v", d, err)
	}
	if l := parseLine("NOVALUE"); l.name != "NOVALUE" {
		t.Errorf("a line without a value: %+v", l)
	}
}

func TestAConfirmationKeepsWhatItDoesNotSet(t *testing.T) {
	text := icalPlaceholder("atlas-k", "Reserviert", at(25, 9, 0), at(25, 10, 0), at(25, 7, 0))
	text = strings.Replace(text, "END:VEVENT", "X-PRIVATE:bleibt\r\nATTENDEE:mailto:alt@example.ch\r\nBEGIN:VALARM\r\nTRIGGER:-PT15M\r\nEND:VALARM\r\nEND:VEVENT", 1)
	out := icalConfirm(text, Details{Subject: strings.Repeat("Velo ", 30), Attendees: []Attendee{{Email: "neu@example.ch", Name: "Neu"}}})
	flat := strings.Join(unfold(out), "\n")
	if !strings.Contains(out, "\r\n ") {
		t.Error("a long line was not folded")
	}
	for _, want := range []string{"X-PRIVATE:bleibt", "TRIGGER:-PT15M", "mailto:neu@example.ch"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the confirmed object lost or lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "alt@example.ch") || strings.Contains(out, "SUMMARY:Reserviert") {
		t.Errorf("a replaced line was kept:\n%s", out)
	}
	if kept := icalConfirm(text, Details{}); !strings.Contains(kept, "SUMMARY:Reserviert") || !strings.Contains(kept, "alt@example.ch") {
		t.Errorf("an empty confirmation changed the object:\n%s", kept)
	}
}

func TestAResourceNameCarriesItsKey(t *testing.T) {
	if caldavKeyOf("atlas-k1") != "k1" || caldavKeyOf("seed-1") != "" {
		t.Fatal("a key is not read back from its UID")
	}
	if n := caldavResource("delivered twice/../x"); strings.ContainsAny(n, "/ ") || !strings.HasSuffix(n, ".ics") {
		t.Fatalf("resource name %q", n)
	}
	if lastSegmentOf("/dav/calendars/a/werkstatt/x%20y.ics") != "x y.ics" {
		t.Fatal("a member's name is not read from its href")
	}
}

// scripted answers each request with the status its method is given, and a body, so
// every answer a CalDAV server may give reaches the backend's handling of it.
func scripted(t *testing.T, status map[string]int, body map[string]string) Backend {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code, ok := status[r.Method]
		if !ok {
			code = http.StatusInternalServerError
		}
		w.Header().Set("ETag", `"1"`)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body[r.Method]))
	}))
	t.Cleanup(srv.Close)
	b, err := NewBackend(srv.URL+"/dav/", `{"provider":"caldav","username":"u","password":"p"}`, Options{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCalDAVReadsEveryAnswerAServerMayGive(t *testing.T) {
	ctx := context.Background()
	ne := NewEvent{Key: "k", Subject: "x", Start: at(25, 9, 0), End: at(25, 10, 0)}
	event := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:atlas-k\r\nDTSTART:20300325T080000Z\r\nDTEND:20300325T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cancelled := strings.Replace(event, "END:VEVENT", "STATUS:CANCELLED\r\nEND:VEVENT", 1)

	for name, tc := range map[string]struct {
		status map[string]int
		body   map[string]string
		run    func(Backend) error
		kind   Kind
		plain  string // a plain failure containing this
	}{
		"a REPORT refused":            {map[string]int{"REPORT": 403}, nil, func(b Backend) error { _, err := b.Events(ctx, "c", ne.Start, ne.End); return err }, KindCalendarNotFound, ""},
		"a REPORT failing":            {map[string]int{"REPORT": 500}, nil, func(b Backend) error { _, err := b.Events(ctx, "c", ne.Start, ne.End); return err }, 0, "HTTP 500"},
		"a REPORT answering junk":     {map[string]int{"REPORT": 207}, map[string]string{"REPORT": "<<<"}, func(b Backend) error { _, err := b.Events(ctx, "c", ne.Start, ne.End); return err }, 0, "not a multistatus"},
		"a REPORT with a bad event":   {map[string]int{"REPORT": 207}, map[string]string{"REPORT": `<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:response><D:href>/dav/c/x.ics</D:href><D:propstat><D:prop><C:calendar-data>BEGIN:VEVENT&#13;&#10;UID:x&#13;&#10;END:VEVENT</C:calendar-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`}, func(b Backend) error { _, err := b.Events(ctx, "c", ne.Start, ne.End); return err }, 0, "without a start"},
		"a PUT failing":               {map[string]int{"PUT": 500}, nil, func(b Backend) error { _, err := b.Create(ctx, "c", ne); return err }, 0, "HTTP 500"},
		"a repeated PUT, gone":        {map[string]int{"PUT": 412, "GET": 404, "PROPFIND": 207}, nil, func(b Backend) error { _, err := b.Create(ctx, "c", ne); return err }, KindBookingNotFound, ""},
		"a repeated PUT, empty":       {map[string]int{"PUT": 412, "GET": 200}, map[string]string{"GET": "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"}, func(b Backend) error { _, err := b.Create(ctx, "c", ne); return err }, 0, "holds no event"},
		"a GET refused":               {map[string]int{"GET": 403}, nil, func(b Backend) error { _, _, err := b.Get(ctx, "c", "x.ics"); return err }, KindCalendarNotFound, ""},
		"a GET failing":               {map[string]int{"GET": 500}, nil, func(b Backend) error { _, _, err := b.Get(ctx, "c", "x.ics"); return err }, 0, "HTTP 500"},
		"a GET of junk":               {map[string]int{"GET": 200}, map[string]string{"GET": "BEGIN:VEVENT\r\nEND:VEVENT\r\n"}, func(b Backend) error { _, _, err := b.Get(ctx, "c", "x.ics"); return err }, 0, "without a start"},
		"a probe refused":             {map[string]int{"GET": 404, "PROPFIND": 403}, nil, func(b Backend) error { _, _, err := b.Get(ctx, "c", "x.ics"); return err }, KindCalendarNotFound, ""},
		"a probe failing":             {map[string]int{"GET": 404, "PROPFIND": 500}, nil, func(b Backend) error { _, _, err := b.Get(ctx, "c", "x.ics"); return err }, 0, "HTTP 500"},
		"a DELETE failing":            {map[string]int{"DELETE": 500}, nil, func(b Backend) error { return b.Delete(ctx, "c", "x.ics") }, 0, "HTTP 500"},
		"an update refused":           {map[string]int{"GET": 200, "PUT": 403}, map[string]string{"GET": event}, func(b Backend) error { _, err := b.Update(ctx, "c", "x.ics", Details{Subject: "y"}); return err }, KindCalendarNotWritable, ""},
		"an update failing":           {map[string]int{"GET": 200, "PUT": 500}, map[string]string{"GET": event}, func(b Backend) error { _, err := b.Update(ctx, "c", "x.ics", Details{Subject: "y"}); return err }, 0, "HTTP 500"},
		"an update always changed":    {map[string]int{"GET": 200, "PUT": 412}, map[string]string{"GET": event}, func(b Backend) error { _, err := b.Update(ctx, "c", "x.ics", Details{Subject: "y"}); return err }, 0, "kept changing"},
		"an update of a cancelled":    {map[string]int{"GET": 200}, map[string]string{"GET": cancelled}, func(b Backend) error { _, err := b.Update(ctx, "c", "x.ics", Details{Subject: "y"}); return err }, KindBookingNotFound, ""},
		"an update of junk":           {map[string]int{"GET": 200}, map[string]string{"GET": "BEGIN:VEVENT\r\nEND:VEVENT\r\n"}, func(b Backend) error { _, err := b.Update(ctx, "c", "x.ics", Details{Subject: "y"}); return err }, 0, "without a start"},
		"an update of a bad id":       {nil, nil, func(b Backend) error { _, err := b.Update(ctx, "c", "", Details{}); return err }, KindBookingNotFound, ""},
		"a delete of a bad calendar":  {nil, nil, func(b Backend) error { return b.Delete(ctx, "", "x.ics") }, KindCalendarNotFound, ""},
		"a delete of a bad id":        {nil, nil, func(b Backend) error { return b.Delete(ctx, "c", "..") }, KindBookingNotFound, ""},
		"a create in a bad calendar":  {nil, nil, func(b Backend) error { _, err := b.Create(ctx, ".", ne); return err }, KindCalendarNotFound, ""},
		"an update in a bad calendar": {nil, nil, func(b Backend) error { _, err := b.Update(ctx, "a/b", "x.ics", Details{}); return err }, KindCalendarNotFound, ""},
		"a get in a bad calendar":     {nil, nil, func(b Backend) error { _, _, err := b.Get(ctx, "..", "x.ics"); return err }, KindCalendarNotFound, ""},
		"events in a bad calendar":    {nil, nil, func(b Backend) error { _, err := b.Events(ctx, "", ne.Start, ne.End); return err }, KindCalendarNotFound, ""},
	} {
		err := tc.run(scripted(t, tc.status, tc.body))
		switch {
		case tc.kind != 0 && kindOf(err) != tc.kind:
			t.Errorf("%s: err = %v, want kind %d", name, err, tc.kind)
		case tc.kind == 0 && (err == nil || kindOf(err) != 0 || !strings.Contains(err.Error(), tc.plain)):
			t.Errorf("%s: err = %v, want a plain failure with %q", name, err, tc.plain)
		}
	}
	// A cancelled event read back is one that no longer exists.
	if _, ok, err := scripted(t, map[string]int{"GET": 200}, map[string]string{"GET": cancelled}).Get(ctx, "c", "x.ics"); ok || err != nil {
		t.Errorf("a cancelled event: %v %v", ok, err)
	}
	// A server that answers a PUT with 200 has created it too.
	if _, err := scripted(t, map[string]int{"PUT": 200}, nil).Create(ctx, "c", ne); err != nil {
		t.Errorf("PUT 200: %v", err)
	}
}

func TestCalDAVFailsAWindowTooFullToReadWhole(t *testing.T) {
	var busy []caldavfake.Busy
	for i := 0; i < maxGraphPages*graphPageSize+1; i++ {
		busy = append(busy, caldavfake.Busy{Start: at(25, 8, 0).Add(time.Duration(i) * time.Second), End: at(25, 8, 0).Add(time.Duration(i+1) * time.Second)})
	}
	reg, _ := caldavRegistry(t, map[string]caldavfake.Calendar{"werkstatt": {Busy: busy}})
	if _, err := RunAt(context.Background(), findJob("werkstatt"), reg, now); err == nil || !strings.Contains(err.Error(), "shorter window") {
		t.Fatalf("err = %v", err)
	}
}
