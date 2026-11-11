package atlasreport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/atlasreport/catalog"
	"github.com/pblumer/atlas/job"
)

var now = time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC)

// fakeAtlas answers the four read routes with recorded answers, and records what it was
// asked and with which token.
type fakeAtlas struct {
	srv     *httptest.Server
	asked   []string
	tokens  []string
	refuse  bool
	summary string
}

const recordedSummary = `[
 {"processDefKey":11,"processId":"eintritt","version":1,"active":2,"completed":40,"latestCompletedAt":0},
 {"processDefKey":12,"processId":"eintritt","version":2,"active":5,"completed":3,"latestCompletedAt":0},
 {"processDefKey":21,"processId":"austritt","version":1,"active":1,"completed":17,"latestCompletedAt":0}]`

func newFake(t *testing.T) *fakeAtlas {
	f := &fakeAtlas{summary: recordedSummary}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.asked = append(f.asked, r.URL.Path+"?"+r.URL.RawQuery)
		f.tokens = append(f.tokens, r.Header.Get("Authorization"))
		if f.refuse {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		switch {
		case r.URL.Path == "/api/v1/instances/summary":
			fmt.Fprint(w, f.summary)
		case r.URL.Path == "/api/v1/processes/12/statistics":
			from, _ := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
			to, _ := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
			fmt.Fprintf(w, `{"from":%d,"to":%d,"instances":{"started":%d,"finished":4,"running":5},
			  "elements":[{"elementId":"pruefen","runs":9,"duration":{"count":9,"p50Millis":60000,"p90Millis":7200000}},
			              {"elementId":"start","runs":9}],"scanned":9,"truncated":false}`, from.UnixNano(), to.UnixNano(), from.Day())
		case r.URL.Path == "/api/v1/incidents/summary":
			fmt.Fprint(w, `{"total":4,"groups":[{"processId":"eintritt","elementId":"ad","count":3,"message":"Konto mueller existiert"},
			  {"processId":"austritt","elementId":"mail","count":1,"message":"x"}]}`)
		case r.URL.Path == "/api/v1/catalog-products/delivery-report":
			fmt.Fprint(w, `{"from":"2026-10-01","to":"2026-10-31","products":[
			  {"itemId":"vpn","name":"VPN","met":18,"missed":2,"overdue":1,"open":3,"leadMedianDays":1.5},
			  {"itemId":"laptop","name":"Laptop","met":9,"missed":3,"overdue":0,"open":1}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAtlas) registry(t *testing.T) *Registry {
	t.Helper()
	c, err := NewClient(f.srv.URL, `{"token":"atl_report"}`)
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register("atlas", c)
	return reg
}

func runReport(t *testing.T, f *fakeAtlas, j Job) Report {
	t.Helper()
	j.Connector, j.ResultVariable = "atlas", "report"
	out, err := RunAt(context.Background(), j, f.registry(t), now)
	if err != nil {
		t.Fatalf("%s: %v", j.Operation, err)
	}
	r, ok := out["report"].(Report)
	if !ok {
		t.Fatalf("%s: result = %#v", j.Operation, out)
	}
	if err := r.Check(); err != nil {
		t.Fatalf("%s: %v", j.Operation, err)
	}
	// The report travels as JSON; it must read as the contract the chart reads.
	raw, _ := json.Marshal(r)
	if !strings.Contains(string(raw), `"schema":"atlas.report/1"`) {
		t.Fatalf("%s: %s", j.Operation, raw)
	}
	return r
}

func vals(s Series) []float64 {
	out := make([]float64, len(s.Values))
	for i, v := range s.Values {
		if v == nil {
			out[i] = -1
		} else {
			out[i] = *v
		}
	}
	return out
}

func TestInstancesSummarySumsAProcesssVersions(t *testing.T) {
	f := newFake(t)
	r := runReport(t, f, Job{Operation: catalog.OpInstancesSummary, Title: "Prozesse"})
	d := r.Datasets["byProcess"]
	if fmt.Sprint(d.Categories) != "[eintritt austritt]" || fmt.Sprint(vals(d.Series[0])) != "[7 1]" || fmt.Sprint(vals(d.Series[1])) != "[43 17]" {
		t.Fatalf("byProcess = %v %v %v", d.Categories, vals(d.Series[0]), vals(d.Series[1]))
	}
	if d.Series[0].Name != "laufend" || *r.Datasets["active"].Value != 8 || r.Title != "Prozesse" || r.GeneratedAt != "2026-11-11T07:00:00Z" {
		t.Fatalf("report = %+v", r)
	}
	if f.tokens[0] != "Bearer atl_report" {
		t.Fatalf("token = %q", f.tokens[0])
	}
	// One process: its versions, in order.
	one := runReport(t, f, Job{Operation: catalog.OpInstancesSummary, Process: "eintritt", Language: "en"})
	if fmt.Sprint(one.Datasets["byProcess"].Categories) != "[v1 v2]" || one.Datasets["byProcess"].Series[0].Name != "running" {
		t.Fatalf("one process = %+v", one.Datasets["byProcess"])
	}
}

func TestProcessStatisticsMeasuresTheNewestVersion(t *testing.T) {
	f := newFake(t)
	r := runReport(t, f, Job{Operation: catalog.OpProcessStatistics, Process: "eintritt", From: "2026-10-01", To: "2026-11-01"})
	if !strings.Contains(f.asked[1], "/api/v1/processes/12/statistics?from=2026-10-01T00%3A00%3A00Z&to=2026-11-01T00%3A00%3A00Z") {
		t.Fatalf("asked %v", f.asked)
	}
	if *r.Datasets["started"].Value != 1 || *r.Datasets["finished"].Value != 4 || *r.Datasets["running"].Value != 5 {
		t.Fatalf("values = %+v", r.Datasets)
	}
	d := r.Datasets["durationByElement"]
	if fmt.Sprint(d.Categories) != "[pruefen]" || d.Unit != "ms" || fmt.Sprint(vals(d.Series[1])) != "[7.2e+06]" {
		t.Fatalf("durations = %+v", d)
	}
	if len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "Version 2 von eintritt") {
		t.Fatalf("notes = %v", r.Notes)
	}
	// Cut into weeks: one statistics read per week, the oldest first.
	f.asked = nil
	w := runReport(t, f, Job{Operation: catalog.OpProcessStatistics, Process: "eintritt", Buckets: 3, Bucket: "week"})
	if len(f.asked) != 4 {
		t.Fatalf("asked %d routes, want the summary and three weeks: %v", len(f.asked), f.asked)
	}
	ot := w.Datasets["overTime"]
	if fmt.Sprint(ot.Categories) != "[KW 44 KW 45 KW 46]" {
		t.Fatalf("weeks = %v", ot.Categories)
	}
	if w.Datasets["started"].Previous == nil {
		t.Fatal("the last week's start count has no previous to compare with")
	}
}

func TestWindowsCountBackFromTheirEnd(t *testing.T) {
	end := time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC) // a Wednesday
	weeks := Windows(end, 3, "week", "de")
	if weeks[2].From != time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC) || weeks[2].To != end || weeks[0].From != time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("weeks = %+v", weeks)
	}
	if weeks[0].Label != "KW 44" || weeks[2].Label != "KW 46" {
		t.Fatalf("labels = %s %s", weeks[0].Label, weeks[2].Label)
	}
	months := Windows(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 2, "month", "en")
	if months[1].From != time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) || months[1].Label != "2026-10" {
		t.Fatalf("a month ending at its first instant is the month before: %+v", months)
	}
	days := Windows(end, 2, "day", "en")
	if days[1].Label != "2026-11-11" || days[0].Label != "2026-11-10" {
		t.Fatalf("days = %+v", days)
	}
}

func TestIncidentSummaryCountsButNeverQuotes(t *testing.T) {
	f := newFake(t)
	r := runReport(t, f, Job{Operation: catalog.OpIncidentSummary})
	if fmt.Sprint(r.Datasets["byProcess"].Categories) != "[eintritt austritt]" || *r.Datasets["openIncidents"].Value != 4 {
		t.Fatalf("report = %+v", r.Datasets)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "mueller") {
		t.Fatal("an incident's message reached the report")
	}
	one := runReport(t, f, Job{Operation: catalog.OpIncidentSummary, Process: "eintritt"})
	if fmt.Sprint(one.Datasets["byElement"].Categories) != "[ad]" || *one.Datasets["openIncidents"].Value != 3 {
		t.Fatalf("one process = %+v", one.Datasets)
	}
}

func TestDeliveryReportCountsPromisesKeptPerProduct(t *testing.T) {
	f := newFake(t)
	r := runReport(t, f, Job{Operation: catalog.OpDeliveryReport, Catalog: "it", From: "2026-10-01T00:00:00Z", To: "2026-10-31"})
	if !strings.Contains(f.asked[0], "catalog=it") || !strings.Contains(f.asked[0], "from=2026-10-01") || !strings.Contains(f.asked[0], "to=2026-10-31") {
		t.Fatalf("asked %v", f.asked)
	}
	d := r.Datasets["deliveryByProduct"]
	if fmt.Sprint(d.Categories) != "[Laptop VPN]" || fmt.Sprint(vals(d.Series[0])) != "[9 18]" {
		t.Fatalf("delivery = %+v", d)
	}
	if fmt.Sprint(vals(r.Datasets["leadTimeByProduct"].Series[0])) != "[-1 1.5]" {
		t.Fatalf("a product without a measured lead time is a gap: %v", vals(r.Datasets["leadTimeByProduct"].Series[0]))
	}
	if *r.Datasets["metShare"].Value != 81.8 || r.Period.From != "2026-10-01" {
		t.Fatalf("share = %v, period %+v", *r.Datasets["metShare"].Value, r.Period)
	}
}

func TestManyProcessesFoldIntoOther(t *testing.T) {
	var rows []InstanceSummaryRow
	for i := 0; i < 60; i++ {
		rows = append(rows, InstanceSummaryRow{ProcessDefKey: uint64(i + 1), ProcessID: fmt.Sprintf("p%02d", i), Version: 1, Active: i, Completed: 1})
	}
	r := FoldInstancesSummary(rows, "", "", "de", now)
	d := r.Datasets["byProcess"]
	if len(d.Categories) != MaxCategories || d.Categories[MaxCategories-1] != "Übrige" {
		t.Fatalf("categories = %d, last %q", len(d.Categories), d.Categories[len(d.Categories)-1])
	}
	// The eleven folded processes ran 0..10 instances: the "other" row is their sum.
	if v := *d.Series[0].Values[MaxCategories-1]; v != 55 {
		t.Fatalf("other = %v, want 55", v)
	}
	if len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "11 weitere") {
		t.Fatalf("notes = %v", r.Notes)
	}
	if err := r.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestARefusalIsThrownOnlyWhereTheProcessCatchesIt(t *testing.T) {
	f := newFake(t)
	f.refuse = true
	j := Job{Connector: "atlas", Operation: catalog.OpIncidentSummary, ResultVariable: "report"}
	_, err := RunAt(context.Background(), j, f.registry(t), now)
	var be *job.BPMNError
	if err == nil || errors.As(err, &be) || !strings.Contains(err.Error(), "operator") {
		t.Fatalf("uncaught refusal = %v; want a failure naming the role", err)
	}
	j.ErrorCodes = []string{CodeRefused}
	_, err = RunAt(context.Background(), j, f.registry(t), now)
	if !errors.As(err, &be) || be.Code != CodeRefused {
		t.Fatalf("caught refusal = %v", err)
	}
	// A process that is not deployed is not found, the same way.
	f.refuse = false
	j = Job{Connector: "atlas", Operation: catalog.OpProcessStatistics, Process: "nope", ResultVariable: "report", ErrorCodes: []string{CodeNotFound}}
	if _, err := RunAt(context.Background(), j, f.registry(t), now); !errors.As(err, &be) || be.Code != CodeNotFound {
		t.Fatalf("missing process = %v", err)
	}
	if _, err := RunAt(context.Background(), Job{Connector: "other", Operation: catalog.OpIncidentSummary}, f.registry(t), now); err == nil {
		t.Fatal("an unknown Worker ran")
	}
}

func TestTheBundleAndEndpointAreChecked(t *testing.T) {
	for _, c := range []struct{ endpoint, bundle string }{
		{"", `{"token":"x"}`},
		{"ftp://atlas", `{"token":"x"}`},
		{"https://atlas.example.ch?x=1", `{"token":"x"}`},
		{"https://u:p@atlas.example.ch", `{"token":"x"}`},
		{"https://atlas.example.ch", `{}`},
		{"https://atlas.example.ch", `{token`},
		{"https://atlas.example.ch", `two words`},
		{"https://atlas.example.ch", `  `},
	} {
		if CheckBundle(c.endpoint, c.bundle) == nil {
			t.Errorf("accepted %q with %q", c.endpoint, c.bundle)
		}
	}
	if err := CheckBundle("https://atlas.example.ch/", `{"token":"atl_x"}`); err != nil {
		t.Fatal(err)
	}
	// The bare token is what the API-token page seals when it is told a vaultSecret.
	c, err := NewClient("https://atlas.example.ch", " atl_sealed\n")
	if err != nil || c.token != "atl_sealed" {
		t.Fatalf("a bare token: %v, %q", err, c.token)
	}
}

func TestDecodeInputChecksWhatADeployCouldNotSee(t *testing.T) {
	for _, bad := range []string{
		`{"process":"p","buckets":3}`,
		`{"process":"p","bucket":"week"}`,
		`{"process":"p","buckets":3,"bucket":"year"}`,
		`{"process":"p","buckets":27,"bucket":"week"}`,
		`{"process":"p","buckets":2.5,"bucket":"week"}`,
		`{"process":"p","buckets":3,"bucket":"week","from":"2026-01-01"}`,
		`{"process":"p","language":"fr"}`,
		`{"process":"p","to":"gestern"}`,
		`{"process":"p","catalog":"x"}`,
		`{}`,
	} {
		if _, err := DecodeInput(catalog.OpProcessStatistics, bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	j, err := DecodeInput(catalog.OpProcessStatistics, `{"process":"p","buckets":4,"bucket":"week","to":null,"language":"en"}`)
	if err != nil || j.Buckets != 4 || j.Bucket != "week" || j.Language != "en" {
		t.Fatalf("job = %+v, %v", j, err)
	}
}
