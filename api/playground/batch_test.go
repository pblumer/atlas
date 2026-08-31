package playground

import (
	"encoding/csv"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// waitForRun polls the status endpoint until the batch reaches one of the given
// states, so a test never sleeps on a guess about how fast it runs.
func waitForRun(t *testing.T, svc *Service, id string, states ...string) runStatusResp {
	t.Helper()
	want := map[string]bool{}
	for _, s := range states {
		want[s] = true
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var st runStatusResp
		decodeInto(t, call(t, svc.HandleRunStatus, http.MethodGet, "", map[string]string{"id": id}), &st)
		if want[st.State] {
			return st
		}
		time.Sleep(3 * time.Millisecond)
	}
	t.Fatalf("the run never reached %v", states)
	return runStatusResp{}
}

// openBatchSession opens a session whose user task is answered by a pool of one,
// so a batch of it queues and the report has waiting time to show.
func openBatchSession(t *testing.T, svc *Service) string {
	t.Helper()
	body := `{"source":"xml","xml":` + jsonString(userTaskXML) + `,"seed":4711,` +
		`"startTime":"2026-03-05T08:00:00Z","stubs":{` +
		`"human":{"minMillis":3600000,"maxMillis":3600000},` +
		`"pools":{"clerks":{"capacity":1}},"poolOf":{"approve":"clerks"}}}`
	var sess sessionResp
	decodeInto(t, call(t, svc.HandleOpen, http.MethodPost, body, nil), &sess)
	return sess.ID
}

// The whole batch surface: start a dataset, watch it, read the summary, page the
// rows, and download them.
func TestABatchRunsAndIsReadBack(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)
	vals := map[string]string{"id": id}

	rec := call(t, svc.HandleStartRun, http.MethodPost,
		`{"cases":[{"kunde":"A"},{"kunde":"B"},{"kunde":"C"}],"arrival":{"mode":"allAtOnce"}}`, vals)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start = %d, body %s", rec.Code, rec.Body)
	}
	st := waitForRun(t, svc, id, "finished")
	if st.Cases != 3 || st.Completed != 3 {
		t.Errorf("status = %+v, want three cases all completed", st)
	}

	var rep reportResp
	decodeInto(t, call(t, svc.HandleReport, http.MethodGet, "", vals), &rep)
	if rep.Cases != 3 || rep.Completed != 3 {
		t.Errorf("report = %+v, want three completed", rep)
	}
	// One clerk, three one-hour cases: three hours, and the pool was busy for all
	// of it.
	if rep.SimEnd != "2026-03-05T11:00:00Z" {
		t.Errorf("run ended at %s, want 11:00", rep.SimEnd)
	}
	if p := rep.Pools["clerks"]; p.Served != 3 || p.MaxQueue != 2 || p.UtilisationPercent != 100 {
		t.Errorf("pool = %+v, want three served, a queue of two and full utilisation", p)
	}
	if el := rep.Elements["approve"]; el.Runs != 3 || el.WaitMillis != (3*time.Hour).Milliseconds() {
		t.Errorf("element = %+v, want three runs and three hours of waiting in total", el)
	}
	if rep.Visits["approve"] != 3 {
		t.Errorf("visits = %+v, want three on approve", rep.Visits)
	}

	// The rows come back a page at a time, in arrival order.
	var page resultsResp
	decodeInto(t, call(t, svc.HandleResults, http.MethodGet, "", vals), &page)
	if page.Total != 3 || len(page.Rows) != 3 {
		t.Fatalf("page = %+v, want three rows", page)
	}
	if page.Rows[0].Variables["kunde"] != "A" || page.Rows[2].Variables["kunde"] != "C" {
		t.Errorf("rows are not in arrival order: %+v", page.Rows)
	}
	if page.Rows[0].End != "end" || page.Rows[0].State != "completed" {
		t.Errorf("first row = %+v, want a completed case ending at \"end\"", page.Rows[0])
	}

	// And whole, as a download.
	rec = call(t, svc.HandleResultsCSV, http.MethodGet, "", vals)
	if rec.Code != http.StatusOK {
		t.Fatalf("csv = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content type = %q, want text/csv", ct)
	}
	records, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse the download: %v", err)
	}
	if len(records) != 4 {
		t.Fatalf("csv has %d lines, want a header and three rows", len(records))
	}
	if records[0][0] != "case" || records[0][len(records[0])-1] != "kunde" {
		t.Errorf("csv header = %v, want the fixed columns then the variables", records[0])
	}
	if records[1][1] != "completed" {
		t.Errorf("first csv row = %v", records[1])
	}
}

// A dataset can be uploaded rather than typed, and the file's own header is the
// layout — an author exporting a CSV should not have to describe the columns
// they just exported.
func TestABatchStartsFromAnUploadedCSV(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)

	var body strings.Builder
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "cases.csv")
	if err != nil {
		t.Fatalf("multipart: %v", err)
	}
	if _, err := part.Write([]byte("kunde,betrag\nA,100\nB,200\n")); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	if err := mw.WriteField("arrival", `{"mode":"every","intervalMillis":600000}`); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body.String()))
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	svc.HandleStartRunFromCSV(rec, r)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start from csv = %d, body %s", rec.Code, rec.Body)
	}

	waitForRun(t, svc, id, "finished")
	var page resultsResp
	decodeInto(t, call(t, svc.HandleResults, http.MethodGet, "", map[string]string{"id": id}), &page)
	if page.Total != 2 {
		t.Fatalf("page = %+v, want the two rows of the file", page)
	}
	if page.Rows[0].Variables["kunde"] != "A" || page.Rows[1].Variables["betrag"] != "200" {
		t.Errorf("rows = %+v, want every column of the file as a start variable", page.Rows)
	}
}

// A run in flight can be stopped, and the answer says so.
func TestABatchCanBeCancelledOverHTTP(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)
	vals := map[string]string{"id": id}

	cases := make([]string, 0, 400)
	for i := 0; i < 400; i++ {
		cases = append(cases, `{"n":`+strconv.Itoa(i)+`}`)
	}
	body := `{"cases":[` + strings.Join(cases, ",") + `],"arrival":{"mode":"every","intervalMillis":60000}}`
	if rec := call(t, svc.HandleStartRun, http.MethodPost, body, vals); rec.Code != http.StatusAccepted {
		t.Fatalf("start = %d, body %s", rec.Code, rec.Body)
	}
	var st runStatusResp
	decodeInto(t, call(t, svc.HandleCancelRun, http.MethodPost, "", vals), &st)
	waitForRun(t, svc, id, "cancelled", "finished")
}

// What a batch refuses, and why.
func TestBatchRefusals(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)
	vals := map[string]string{"id": id}

	cases := []struct {
		name, body string
		want       int
	}{
		{"no cases", `{"cases":[]}`, http.StatusBadRequest},
		{"unknown arrival mode", `{"cases":[{}],"arrival":{"mode":"telepathy"}}`, http.StatusBadRequest},
		{"a takt with no interval", `{"cases":[{}],"arrival":{"mode":"every"}}`, http.StatusConflict},
		{"a Poisson stream with no rate", `{"cases":[{}],"arrival":{"mode":"poisson"}}`, http.StatusConflict},
		{"malformed body", `{`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := call(t, svc.HandleStartRun, http.MethodPost, tc.body, vals); rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

// The ceiling is a resource bound, and a dataset over it is refused with the
// number rather than accepted and then abandoned halfway.
func TestADatasetOverTheCeilingIsRefused(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)

	rows := make([]map[string]any, maxCasesPerRun+1)
	for i := range rows {
		rows[i] = map[string]any{}
	}
	body, err := json.Marshal(startRunReq{Cases: rows})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := call(t, svc.HandleStartRun, http.MethodPost, string(body), map[string]string{"id": id})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "50000") {
		t.Errorf("the refusal %s should name the limit", rec.Body)
	}
}

// A CSV whose header cannot be a set of variable names is refused before a run
// starts on it.
func TestACSVWithABadHeaderIsRefused(t *testing.T) {
	for name, content := range map[string]string{
		"empty file":      "",
		"repeated column": "a,a\n1,2\n",
		"unnamed column":  "a,\n1,2\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := rowsFromCSV([]byte(content)); err == nil {
				t.Error("this file should not be accepted as a dataset")
			}
		})
	}
}

// The batch routes answer for a session that is gone like every other route.
func TestBatchRoutesOnAnUnknownSession(t *testing.T) {
	svc := newService(t)
	vals := map[string]string{"id": "nope"}
	for name, h := range map[string]http.HandlerFunc{
		"start":   svc.HandleStartRun,
		"status":  svc.HandleRunStatus,
		"cancel":  svc.HandleCancelRun,
		"report":  svc.HandleReport,
		"results": svc.HandleResults,
		"csv":     svc.HandleResultsCSV,
	} {
		t.Run(name, func(t *testing.T) {
			rec := call(t, h, http.MethodPost, `{"cases":[{}]}`, vals)
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
		})
	}
}
