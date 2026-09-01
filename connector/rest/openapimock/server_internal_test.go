package openapimock

import (
	"net/http"
	"testing"
)

func TestNormalizeBasePath(t *testing.T) {
	cases := map[string]string{"": "", "/": "", "api": "/api", "/api/": "/api", " /v1 ": "/v1"}
	for in, want := range cases {
		if got := normalizeBasePath(in); got != want {
			t.Errorf("normalizeBasePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReportNamesAnUntitledDocument(t *testing.T) {
	srv := New(&Spec{Operations: []Operation{{Method: "GET", Path: "/x"}}})
	report := srv.Report()
	if report.Source != "openapi-mock" || report.Target != "" {
		t.Errorf("envelope = %+v", report)
	}
	// The summary is what a folded card shows, so it says something either way.
	if want := "an untitled API — 1 operation, 0 calls"; report.Summary != want {
		t.Errorf("summary = %q, want %q", report.Summary, want)
	}
}

func TestWithIDIgnoresAnEmptyName(t *testing.T) {
	srv := New(&Spec{}, WithID("  "))
	if srv.id != "openapi-mock" {
		t.Errorf("id = %q, want the default kept", srv.id)
	}
}

func TestChooseStatus(t *testing.T) {
	cases := map[string]struct {
		responses []Response
		want      int
	}{
		"the lowest success":             {[]Response{{Status: 201}, {Status: 200}, {Status: 400}}, 200},
		"an error-only operation":        {[]Response{{Status: 404}, {Status: 500}}, 404},
		"the lowest error, either order": {[]Response{{Status: 500}, {Status: 404}}, 404},
		"the default":                    {[]Response{{Status: 0}, {Status: 404}}, 0},
		"nothing described":              {nil, http.StatusNoContent},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			group, refused := chooseStatus(&Operation{Responses: tc.responses}, preference{})
			if refused != nil {
				t.Fatalf("refused: %s", refused.message)
			}
			if group[0].Status != tc.want {
				t.Errorf("chose %d, want %d", group[0].Status, tc.want)
			}
		})
	}
}

func TestPreferredCodeFallsBackToTheDefaultResponse(t *testing.T) {
	// A document that describes only `default` describes every code, so asking for one
	// is honoured rather than refused.
	op := &Operation{Responses: []Response{{Status: 0, Media: "application/json", Body: []byte(`{}`)}}}
	group, refused := chooseStatus(op, preference{code: 503, hasCode: true})
	if refused != nil {
		t.Fatalf("refused: %s", refused.message)
	}
	if len(group) != 1 || group[0].Status != 503 {
		t.Errorf("group = %+v, want the default response answering 503", group)
	}
}

func TestPreferredMediaType(t *testing.T) {
	json := Response{Media: "application/json"}
	problem := Response{Media: "application/problem+json"}
	csv := Response{Media: "text/csv"}
	cases := map[string]struct {
		group []Response
		want  string
	}{
		"json wins":              {[]Response{csv, json}, "application/json"},
		"a +json flavour counts": {[]Response{csv, problem}, "application/problem+json"},
		"otherwise the first":    {[]Response{csv}, "text/csv"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := preferred(tc.group).Media; got != tc.want {
				t.Errorf("preferred = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatusNamesIncludesTheDefault(t *testing.T) {
	op := &Operation{Responses: []Response{
		{Status: 0, Media: "application/json"}, {Status: 0, Media: "text/csv"}, {Status: 404},
	}}
	got := listOr(statusNames(op), "none")
	if got != "404, default" {
		t.Errorf("statusNames = %q", got)
	}
	if got := listOr(nil, "none"); got != "none" {
		t.Errorf("listOr(nil) = %q", got)
	}
}

func TestRecordedBodyKeepsWhatIsNotJSON(t *testing.T) {
	if got := recordedBody(nil); got != nil {
		t.Errorf("empty body = %s", got)
	}
	if got := string(recordedBody([]byte(`{"a":1}`))); got != `{"a":1}` {
		t.Errorf("json body = %s", got)
	}
	// A form post or an XML body is still worth having in the journal; it travels as
	// the JSON string it is not.
	if got := string(recordedBody([]byte("name=fido"))); got != `"name=fido"` {
		t.Errorf("form body = %s", got)
	}
}

func TestScalarRendersHeaderValues(t *testing.T) {
	cases := map[string]struct {
		value any
		want  string
	}{
		"a string": {"abc", "abc"},
		"a number": {int64(2), "2"},
		"nothing":  {nil, ""},
		"a list":   {[]any{"a"}, `["a"]`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := scalar(tc.value); got != tc.want {
				t.Errorf("scalar(%v) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestStatusOfReadsRangesAndRefusesTheRest(t *testing.T) {
	for key, want := range map[string]int{"200": 200, "default": 0, "4XX": 400, "5xx": 500} {
		got, err := statusOf(key)
		if err != nil || got != want {
			t.Errorf("statusOf(%q) = %d, %v; want %d", key, got, err, want)
		}
	}
	for _, key := range []string{"okay", "99", "600", "XXX"} {
		if _, err := statusOf(key); err == nil {
			t.Errorf("statusOf(%q) = nil, want an error", key)
		}
	}
}
