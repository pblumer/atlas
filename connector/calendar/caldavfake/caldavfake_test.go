package caldavfake

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func call(t *testing.T, s *Server, method, path, body string, hdr map[string]string, auth bool) (int, http.Header, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.srv.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if auth {
		req.SetBasicAuth(Username, Password)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := s.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(raw)
}

const query = `<C:calendar-query xmlns:C="urn:ietf:params:xml:ns:caldav"><C:calendar-data><C:expand start="20300325T000000Z" end="20300415T000000Z"/>` +
	`<C:comp name="VCALENDAR"><C:comp name="VEVENT"><C:prop name="DTSTART"/></C:comp></C:comp></C:calendar-data>` +
	`<C:filter><C:time-range start="20300325T000000Z" end="20300415T000000Z"/></C:filter></C:calendar-query>`

func TestTheFakeAnswersWhatTheBackendAsks(t *testing.T) {
	day := time.Date(2030, 3, 25, 8, 0, 0, 0, Zone)
	s := New(map[string]Calendar{
		"werkstatt": {Busy: []Busy{
			{Start: day, End: day.Add(time.Hour), Weekly: 3, Transparent: true},
			{Start: day.AddDate(0, 0, 1), End: day.AddDate(0, 0, 2), AllDay: true},
		}},
		"lesen": {ReadOnly: true},
	})
	defer s.Close()
	if !strings.HasSuffix(s.HomeURL(), HomePath) || !strings.Contains(Bundle("UTC"), `"provider":"caldav"`) {
		t.Fatal("the home or the bundle is not what the backend is given")
	}
	col := HomePath + "werkstatt/"
	obj := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:atlas-k\r\nDTSTART:20300327T080000Z\r\nDTEND:20300327T090000Z\r\nSUMMARY:a\\, b\r\nSTATUS:CANCELLED\r\nATTENDEE;CN=X:mailto:x@example.ch\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	for name, tc := range map[string]struct {
		method, path, body string
		hdr                map[string]string
		auth               bool
		status             int
		contains           string
	}{
		"no credential":            {"GET", col, "", nil, false, 401, ""},
		"outside the home":         {"GET", "/other/", "", nil, true, 404, ""},
		"the home itself":          {"GET", HomePath, "", nil, true, 404, ""},
		"the collection":           {"PROPFIND", col, "", nil, true, 207, "collection"},
		"an unknown collection":    {"PROPFIND", HomePath + "fehlt/", "", nil, true, 404, ""},
		"a method it refuses":      {"POST", col, "", nil, true, 405, ""},
		"the series expanded":      {"REPORT", col, query, nil, true, 207, "RECURRENCE-ID"},
		"a wide query":             {"REPORT", col, strings.Replace(query, "DTSTART", "SUMMARY", 1), nil, true, 400, "narrowed"},
		"no time-range":            {"REPORT", col, `<C:comp name="VEVENT">`, nil, true, 400, "time-range"},
		"a PUT outside a calendar": {"PUT", HomePath + "fehlt/x.ics", obj, nil, true, 409, ""},
		"a GET outside a calendar": {"GET", HomePath + "fehlt/x.ics", "", nil, true, 404, ""},
		"a write to a read-only":   {"PUT", HomePath + "lesen/x.ics", obj, nil, true, 403, ""},
		"a delete in a read-only":  {"DELETE", HomePath + "lesen/x.ics", "", nil, true, 403, ""},
		"something not a calendar": {"PUT", col + "x.ics", "hello", nil, true, 415, ""},
		"a member it refuses":      {"POST", col + "x.ics", "", nil, true, 405, ""},
		"a member not there":       {"GET", col + "x.ics", "", nil, true, 404, ""},
		"a delete not there":       {"DELETE", col + "x.ics", "", nil, true, 404, ""},
		"a stale If-Match":         {"PUT", col + "x.ics", obj, map[string]string{"If-Match": `"99"`}, true, 412, ""},
	} {
		status, _, body := call(t, s, tc.method, tc.path, tc.body, tc.hdr, tc.auth)
		if status != tc.status || !strings.Contains(body, tc.contains) {
			t.Errorf("%s: %d %s, want %d containing %q", name, status, body, tc.status, tc.contains)
		}
	}
	if status, _, _ := call(t, s, "PUT", col+"atlas-k.ics", obj, map[string]string{"If-None-Match": "*"}, true); status != 201 {
		t.Fatalf("create: %d", status)
	}
	if status, _, _ := call(t, s, "PUT", col+"atlas-k.ics", obj, map[string]string{"If-None-Match": "*"}, true); status != 412 {
		t.Fatalf("a second create: %d", status)
	}
	_, hdr, _ := call(t, s, "GET", col+"atlas-k.ics", "", nil, true)
	if status, _, _ := call(t, s, "PUT", col+"atlas-k.ics", obj, map[string]string{"If-Match": hdr.Get("ETag")}, true); status != 204 {
		t.Fatalf("an update: %d", status)
	}
	s.NoExpand = true
	if _, _, body := call(t, s, "REPORT", col, query, nil, true); !strings.Contains(body, "RRULE") {
		t.Fatalf("without expand the series is not answered as written: %s", body)
	}
	objs := s.Objects("werkstatt")
	last := objs[len(objs)-1]
	if last.Summary != "a, b" || !last.Cancelled || len(last.Attendees) != 1 || last.UID != "atlas-k" {
		t.Fatalf("object %+v", last)
	}
	if !objs[0].Transparent || !objs[1].Start.Equal(time.Date(2030, 3, 26, 0, 0, 0, 0, Zone)) {
		t.Fatalf("seeds %+v", objs[:2])
	}
	if status, _, _ := call(t, s, "DELETE", col+"atlas-k.ics", "", nil, true); status != 204 || len(s.Objects("werkstatt")) != 2 {
		t.Fatalf("delete: %d", status)
	}
	if s.Objects("fehlt") != nil {
		t.Fatal("an unknown calendar has objects")
	}
}
