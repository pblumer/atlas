package googlefake

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func call(t *testing.T, s *Server, method, path, body string) (int, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.srv.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer fake-google-token")
	resp, err := s.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

const narrowFields = "items(id,status,transparency,start,end),nextPageToken,timeZone"

func list(from, to time.Time, extra string) string {
	q := url.Values{}
	q.Set("timeMin", from.UTC().Format(time.RFC3339))
	q.Set("timeMax", to.UTC().Format(time.RFC3339))
	q.Set("fields", narrowFields)
	return "/calendar/v3/calendars/werkstatt/events?" + q.Encode() + extra
}

func TestTheFakeAnswersWhatTheBackendAsks(t *testing.T) {
	day := time.Date(2030, 3, 25, 8, 0, 0, 0, zone)
	s := New(map[string]Calendar{
		"werkstatt": {Busy: []Busy{
			{Start: day, End: day.Add(time.Hour), Weekly: 2},
			{Start: day.AddDate(0, 0, 1), End: day.AddDate(0, 0, 2), AllDay: true, Transparent: true},
		}},
		"lesen": {ReadOnly: true},
	})
	defer s.Close()
	if !strings.HasSuffix(s.APIURL(), "/calendar/v3") || !strings.HasSuffix(s.TokenURL(), "/token") {
		t.Fatal("the roots are not where the backend looks")
	}
	resp, err := s.HTTPClient().PostForm(s.TokenURL(), url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {"x"}})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("token: %v %v", resp, err)
	}
	resp.Body.Close()
	resp, _ = s.HTTPClient().PostForm(s.TokenURL(), url.Values{"grant_type": {"client_credentials"}})
	if resp.StatusCode != 400 {
		t.Fatalf("another grant: %d", resp.StatusCode)
	}
	resp.Body.Close()

	week := day.AddDate(0, 0, 14)
	for name, tc := range map[string]struct {
		method, path, body string
		status             int
		contains           string
	}{
		"the series expanded":          {"GET", list(day, week, "&singleEvents=true"), "", 200, `"date":"2030-03-26"`},
		"a page and a token for more":  {"GET", list(day, week, "&singleEvents=true&maxResults=1"), "", 200, "nextPageToken"},
		"a wide read":                  {"GET", "/calendar/v3/calendars/werkstatt/events?fields=items(summary)&timeMin=x&timeMax=y", "", 400, "fakeRefusesWideRead"},
		"no window":                    {"GET", "/calendar/v3/calendars/werkstatt/events?fields=" + url.QueryEscape(narrowFields), "", 400, "timeMin"},
		"the calendar":                 {"GET", "/calendar/v3/calendars/werkstatt?fields=id", "", 200, Zone},
		"an unknown calendar":          {"GET", "/calendar/v3/calendars/fehlt?fields=id", "", 404, "notFound"},
		"outside calendars":            {"GET", "/calendar/v3/users/me", "", 404, "notFound"},
		"an unknown route":             {"PUT", "/calendar/v3/calendars/werkstatt/acl", "", 404, "notFound"},
		"a write to a read-only one":   {"POST", "/calendar/v3/calendars/lesen/events", "{}", 403, "requiredAccessLevel"},
		"a body that is no event":      {"POST", "/calendar/v3/calendars/werkstatt/events", "{", 400, "badRequest"},
		"an empty range":               {"POST", "/calendar/v3/calendars/werkstatt/events", `{"start":{"dateTime":"2030-03-25T10:00:00Z"},"end":{"dateTime":"2030-03-25T09:00:00Z"}}`, 400, "timeRangeEmpty"},
		"an id outside base32hex":      {"POST", "/calendar/v3/calendars/werkstatt/events", `{"id":"xyz-1","start":{"dateTime":"2030-03-25T09:00:00Z"},"end":{"dateTime":"2030-03-25T10:00:00Z"}}`, 400, "invalid"},
		"an event that is not there":   {"GET", "/calendar/v3/calendars/werkstatt/events/nope?fields=id", "", 404, "notFound"},
		"a wide read of one event":     {"GET", "/calendar/v3/calendars/werkstatt/events/seed1?fields=summary", "", 400, "fakeRefusesWideRead"},
		"a patch that is no patch":     {"PATCH", "/calendar/v3/calendars/werkstatt/events/seed1", "{", 400, "badRequest"},
		"a method an event refuses":    {"POST", "/calendar/v3/calendars/werkstatt/events/seed1", "{}", 405, "methodNotAllowed"},
		"a generated id when none set": {"POST", "/calendar/v3/calendars/werkstatt/events", `{"summary":"x","transparency":"transparent","start":{"dateTime":"2030-03-27T09:00:00Z"},"end":{"dateTime":"2030-03-27T10:00:00Z"}}`, 200, `"transparent"`},
	} {
		status, body := call(t, s, tc.method, tc.path, tc.body)
		if status != tc.status || !strings.Contains(body, tc.contains) {
			t.Errorf("%s: %d %s, want %d containing %q", name, status, body, tc.status, tc.contains)
		}
	}
	// Without singleEvents a series is one item.
	_, body := call(t, s, "GET", list(day, week, ""), "")
	var got struct{ Items []any }
	_ = json.Unmarshal([]byte(body), &got)
	if len(got.Items) != 3 {
		t.Fatalf("without singleEvents: %d items, want the series once, the all-day one and the generated one", len(got.Items))
	}

	ins := `{"id":"atlas6b31","summary":"x","start":{"dateTime":"2030-03-26T09:00:00Z"},"end":{"dateTime":"2030-03-26T10:00:00Z"}}`
	if status, _ := call(t, s, "POST", "/calendar/v3/calendars/werkstatt/events", ins); status != 200 {
		t.Fatalf("insert: %d", status)
	}
	if status, body := call(t, s, "POST", "/calendar/v3/calendars/werkstatt/events", ins); status != 409 || !strings.Contains(body, "duplicate") {
		t.Fatalf("a second insert: %d %s", status, body)
	}
	patch := `{"status":"confirmed","summary":"y","description":"z","start":{"dateTime":"2030-03-26T10:00:00Z"},"end":{"dateTime":"2030-03-26T11:00:00Z"},"attendees":[{"email":"a@example.ch","displayName":"A"}]}`
	if status, _ := call(t, s, "PATCH", "/calendar/v3/calendars/werkstatt/events/atlas6b31?sendUpdates=all", patch); status != 200 {
		t.Fatalf("patch: %d", status)
	}
	if status, _ := call(t, s, "DELETE", "/calendar/v3/calendars/werkstatt/events/atlas6b31", ""); status != 204 {
		t.Fatalf("delete: %d", status)
	}
	if status, _ := call(t, s, "DELETE", "/calendar/v3/calendars/werkstatt/events/atlas6b31", ""); status != 410 {
		t.Fatalf("a second delete: %d, want 410", status)
	}
	if status, body := call(t, s, "GET", "/calendar/v3/calendars/werkstatt/events/atlas6b31?fields=id,status", ""); status != 200 || !strings.Contains(body, "cancelled") {
		t.Fatalf("a deleted event: %d %s", status, body)
	}
	if status, _ := call(t, s, "POST", "/calendar/v3/calendars/werkstatt/events", ins); status != 409 {
		t.Fatalf("an id of a deleted event was taken again: %d", status)
	}
	evs := s.Events("werkstatt")
	last := evs[len(evs)-1]
	if last.Summary != "y" || last.Description != "z" || len(last.Attendees) != 1 || last.Status != "cancelled" {
		t.Fatalf("event %+v", last)
	}
	if s.Events("fehlt") != nil {
		t.Fatal("an unknown calendar has events")
	}
	req, _ := http.NewRequest("GET", s.APIURL()+"/calendars/werkstatt?fields=id", nil)
	resp, _ = s.HTTPClient().Do(req)
	if resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAttendeesAreRefusedWithoutDelegation(t *testing.T) {
	s := New(map[string]Calendar{"c": {RefuseAttendees: true, Busy: []Busy{{Start: time.Now(), End: time.Now().Add(time.Hour)}}}})
	defer s.Close()
	status, body := call(t, s, "PATCH", "/calendar/v3/calendars/c/events/seed1", `{"attendees":[{"email":"a@example.ch"}]}`)
	if status != 403 || !strings.Contains(body, "forbiddenForServiceAccounts") {
		t.Fatalf("%d %s", status, body)
	}
	if !strings.Contains(Bundle("x@firma.ch"), `"subject":"x@firma.ch"`) || strings.Contains(Bundle(""), "subject") {
		t.Fatal("the bundle does not carry the subject as asked")
	}
}
