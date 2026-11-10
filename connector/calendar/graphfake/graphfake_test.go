package graphfake

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func call(t *testing.T, s *Server, method, path, body string, header map[string]string) (int, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.srv.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer fake-token")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := s.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

var utc = map[string]string{"Prefer": `outlook.timezone="UTC"`}

func view(from, to time.Time, sel string) string {
	q := url.Values{}
	q.Set("startDateTime", from.UTC().Format(time.RFC3339))
	q.Set("endDateTime", to.UTC().Format(time.RFC3339))
	if sel != "" {
		q.Set("$select", sel)
	}
	return "/v1.0/users/werkstatt/calendar/calendarView?" + q.Encode()
}

func TestTheFakeAnswersWhatTheBackendAsks(t *testing.T) {
	day := time.Date(2030, 3, 25, 8, 0, 0, 0, time.UTC)
	s := New(map[string]Mailbox{
		"werkstatt": {Busy: []Busy{{Start: day, End: day.Add(time.Hour), Weekly: 2}, {Start: day.Add(2 * time.Hour), End: day.Add(3 * time.Hour), ShowAs: "free"}}},
		"lesen":     {ReadOnly: true},
	})
	defer s.Close()
	if !strings.HasSuffix(s.GraphURL(), "/v1.0") || !strings.HasSuffix(s.TokenURL(), "/token") {
		t.Fatal("the roots are not where the backend looks")
	}

	// The token endpoint takes a client-credentials grant and nothing else.
	resp, err := s.HTTPClient().PostForm(s.TokenURL(), url.Values{"grant_type": {"client_credentials"}, "client_id": {"c"}})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("token: %v %v", resp, err)
	}
	resp.Body.Close()
	resp, _ = s.HTTPClient().PostForm(s.TokenURL(), url.Values{"grant_type": {"password"}})
	if resp.StatusCode != 400 {
		t.Fatalf("a password grant: %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp, _ = s.HTTPClient().Get(s.TokenURL())
	if resp.StatusCode != 405 {
		t.Fatalf("GET token: %d", resp.StatusCode)
	}
	resp.Body.Close()

	sel := "id,start,end,showAs,isCancelled,transactionId"
	for name, tc := range map[string]struct {
		method, path, body string
		header             map[string]string
		status             int
		contains           string
	}{
		"a weekly series expanded":   {"GET", view(day, day.AddDate(0, 0, 14), sel), "", utc, 200, "seed-1"},
		"a page and a link to more":  {"GET", view(day, day.AddDate(0, 0, 14), sel) + "&$top=1", "", utc, 200, "@odata.nextLink"},
		"a wide read":                {"GET", view(day, day.Add(time.Hour), "id,subject"), "", utc, 400, "FakeRefusesWideRead"},
		"no select":                  {"GET", view(day, day.Add(time.Hour), ""), "", utc, 400, "FakeRefusesWideRead"},
		"another zone":               {"GET", view(day, day.Add(time.Hour), sel), "", nil, 400, "FakeWantsUTC"},
		"no window":                  {"GET", "/v1.0/users/werkstatt/calendar/calendarView?$select=" + sel, "", utc, 400, "ErrorInvalidParameter"},
		"an unknown mailbox":         {"GET", "/v1.0/users/fehlt/calendar/calendarView", "", utc, 404, "ErrorInvalidUser"},
		"a write to a read-only box": {"POST", "/v1.0/users/lesen/calendar/events", "{}", nil, 403, "ErrorAccessDenied"},
		"outside users":              {"GET", "/v1.0/me", "", nil, 404, "ResourceNotFound"},
		"an unknown route":           {"PUT", "/v1.0/users/werkstatt/calendar", "", nil, 404, "ResourceNotFound"},
		"a body that is no event":    {"POST", "/v1.0/users/werkstatt/calendar/events", "[", nil, 400, "ErrorInvalidRequest"},
		"times out of order":         {"POST", "/v1.0/users/werkstatt/calendar/events", `{"start":{"dateTime":"2030-03-25T10:00:00","timeZone":"UTC"},"end":{"dateTime":"2030-03-25T09:00:00","timeZone":"UTC"}}`, nil, 400, "in order"},
		"a time not in UTC":          {"POST", "/v1.0/users/werkstatt/calendar/events", `{"start":{"dateTime":"2030-03-25T09:00:00","timeZone":"CET"},"end":{"dateTime":"2030-03-25T10:00:00","timeZone":"UTC"}}`, nil, 400, "in order"},
		"an event that is not there": {"GET", "/v1.0/users/werkstatt/events/nope?$select=" + sel, "", nil, 404, "ErrorItemNotFound"},
		"a wide read of one event":   {"GET", "/v1.0/users/werkstatt/events/seed-1?$select=subject", "", nil, 400, "FakeRefusesWideRead"},
		"a patch that is no patch":   {"PATCH", "/v1.0/users/werkstatt/events/seed-1", "[", nil, 400, "ErrorInvalidRequest"},
		"a method an event refuses":  {"POST", "/v1.0/users/werkstatt/events/seed-1", "{}", nil, 405, "not allowed"},
	} {
		status, body := call(t, s, tc.method, tc.path, tc.body, tc.header)
		if status != tc.status || !strings.Contains(body, tc.contains) {
			t.Errorf("%s: %d %s, want %d containing %q", name, status, body, tc.status, tc.contains)
		}
	}

	status, body := call(t, s, "POST", "/v1.0/users/werkstatt/calendar/events",
		`{"subject":"x","start":{"dateTime":"2030-03-26T09:00:00","timeZone":"UTC"},"end":{"dateTime":"2030-03-26T10:00:00","timeZone":"UTC"},"transactionId":"k"}`, nil)
	if status != 201 || !strings.Contains(body, `"transactionId":"k"`) {
		t.Fatalf("create: %d %s", status, body)
	}
	status, _ = call(t, s, "PATCH", "/v1.0/users/werkstatt/events/AAMk-3",
		`{"subject":"y","body":{"content":"z"},"attendees":[{"emailAddress":{"address":"a@example.ch","name":"A"}}]}`, nil)
	if evs := s.Events("werkstatt"); status != 200 || evs[2].Subject != "y" || evs[2].Body != "z" || len(evs[2].Attendees) != 1 {
		t.Fatalf("patch: %d %+v", status, evs)
	}
	if status, _ = call(t, s, "GET", "/v1.0/users/werkstatt/events/AAMk-3?$select="+sel, "", nil); status != 200 {
		t.Fatalf("get: %d", status)
	}
	if status, _ = call(t, s, "DELETE", "/v1.0/users/werkstatt/events/AAMk-3", "", nil); status != 204 || len(s.Events("werkstatt")) != 2 {
		t.Fatalf("delete: %d", status)
	}
	if s.Events("fehlt") != nil {
		t.Fatal("an unknown mailbox has events")
	}
	req, _ := http.NewRequest("GET", s.srv.URL+"/v1.0/users/werkstatt/events/seed-1", nil)
	resp, _ = s.HTTPClient().Do(req)
	if resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	resp.Body.Close()
}
