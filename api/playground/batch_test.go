package playground

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/playground"
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

// Business hours and weekdays travel over the wire as minutes and day numbers,
// because a JSON body has no duration type and a caller should not have to
// encode one.
func TestCalendarOnTheWire(t *testing.T) {
	c := calendarReq{
		Open: []windowReq{{FromMinutes: 8 * 60, ToMinutes: 17 * 60}},
		Days: []int{1, 2, 3, 4, 5, 9, -1}, // Monday to Friday; the two impossible ones are ignored
	}
	got := c.toCalendar()
	if len(got.Open) != 1 || got.Open[0].From != 8*time.Hour || got.Open[0].To != 17*time.Hour {
		t.Errorf("windows = %+v, want 08:00–17:00", got.Open)
	}
	for d := 1; d <= 5; d++ {
		if !got.Days[d] {
			t.Errorf("weekday %d should be selected", d)
		}
	}
	if got.Days[0] || got.Days[6] {
		t.Error("the weekend should not be selected")
	}
}

// Every arrival mode has a name on the wire, and one that is not a mode is
// refused rather than silently treated as the default.
func TestArrivalModesOnTheWire(t *testing.T) {
	for name, want := range map[string]playground.ArrivalMode{
		"":           playground.ArrivalAllAtOnce,
		"allAtOnce":  playground.ArrivalAllAtOnce,
		"sequential": playground.ArrivalSequential,
		"every":      playground.ArrivalEvery,
		"poisson":    playground.ArrivalPoisson,
	} {
		got, err := arrivalReq{Mode: name}.toArrival()
		if err != nil || got.Mode != want {
			t.Errorf("mode %q = %v (err %v), want %v", name, got.Mode, err, want)
		}
	}
	if _, err := (arrivalReq{Mode: "telepathy"}).toArrival(); err == nil {
		t.Error("an unknown mode should be refused")
	}
}

// What the CSV upload refuses, and why.
func TestCSVUploadRefusals(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)

	t.Run("not a multipart body", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("plain text"))
		r.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		svc.HandleStartRunFromCSV(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("no file part", func(t *testing.T) {
		var body strings.Builder
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("arrival", `{"mode":"allAtOnce"}`)
		_ = mw.Close()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body.String()))
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		svc.HandleStartRunFromCSV(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("arrival that is not JSON", func(t *testing.T) {
		var body strings.Builder
		mw := multipart.NewWriter(&body)
		part, _ := mw.CreateFormFile("file", "cases.csv")
		_, _ = part.Write([]byte("a\n1\n"))
		_ = mw.WriteField("arrival", "{oops")
		_ = mw.Close()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body.String()))
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		svc.HandleStartRunFromCSV(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("a CSV the parser refuses", func(t *testing.T) {
		var body strings.Builder
		mw := multipart.NewWriter(&body)
		part, _ := mw.CreateFormFile("file", "cases.csv")
		_, _ = part.Write([]byte("a,a\n1,2\n"))
		_ = mw.Close()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body.String()))
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		svc.HandleStartRunFromCSV(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

// A case whose variables the server cannot express is refused with its row
// number: a dataset of fifty thousand needs to say which one is wrong.
func TestACaseWithUnconvertibleVariablesNamesItsRow(t *testing.T) {
	reg := playground.NewRegistry(time.Hour, 4)
	t.Cleanup(reg.CloseAll)
	picky := func(in map[string]any) ([]model.VariableValue, error) {
		if _, bad := in["nope"]; bad {
			return nil, errors.New("unsupported value type")
		}
		return vars(in)
	}
	svc := New(reg, func(*http.Request, string, string) ([]byte, int, string) {
		return nil, http.StatusNotFound, "no"
	}, picky)
	var sess sessionResp
	decodeInto(t, call(t, svc.HandleOpen, http.MethodPost,
		`{"source":"xml","xml":`+jsonString(userTaskXML)+`}`, nil), &sess)

	rec := call(t, svc.HandleStartRun, http.MethodPost,
		`{"cases":[{"a":1},{"nope":1}]}`, map[string]string{"id": sess.ID})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "case 1") {
		t.Errorf("the refusal %s should name the row", rec.Body)
	}
}

// Starting a batch on a session that closed underneath the request is a 404, not
// a conflict: the session is gone, and a retry with the same id will not help.
func TestStartingABatchOnAClosedSession(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)
	live, ok := svc.sessions.Get(id)
	if !ok {
		t.Fatal("the registry lost the session")
	}
	if err := live.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	rec := call(t, svc.HandleStartRun, http.MethodPost, `{"cases":[{}]}`, map[string]string{"id": id})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// The results page reads its bounds from the query, clamps a limit nobody should
// ask for, and ignores one that is not a number.
func TestResultsPageBounds(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)
	if rec := call(t, svc.HandleStartRun, http.MethodPost,
		`{"cases":[{"n":"0"},{"n":"1"},{"n":"2"}]}`, map[string]string{"id": id}); rec.Code != http.StatusAccepted {
		t.Fatalf("start = %d, body %s", rec.Code, rec.Body)
	}
	waitForRun(t, svc, id, "finished")

	page := func(query string) resultsResp {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		r.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		svc.HandleResults(rec, r)
		var out resultsResp
		decodeInto(t, rec, &out)
		return out
	}
	if got := page("offset=1&limit=1"); len(got.Rows) != 1 || got.Rows[0].Index != 1 || got.Offset != 1 {
		t.Errorf("offset/limit = %+v", got)
	}
	if got := page("limit=99999"); len(got.Rows) != 3 {
		t.Errorf("an oversized limit should be clamped, not refused: %+v", got)
	}
	if got := page("offset=nonsense&limit=-4"); len(got.Rows) != 3 || got.Offset != 0 {
		t.Errorf("unreadable bounds should fall back to the defaults: %+v", got)
	}
}

// A run with no cases still downloads: an empty table is a header and nothing
// else, not an error.
func TestTheCSVOfAnEmptyRunIsEmpty(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)
	rec := call(t, svc.HandleResultsCSV, http.MethodGet, "", map[string]string{"id": id})
	if rec.Code != http.StatusOK {
		t.Fatalf("csv = %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "" {
		t.Errorf("csv of a run with no cases = %q, want nothing", body)
	}
}

// A CSV with a header and no rows is a dataset of nothing, refused where an
// inline dataset of nothing is refused.
func TestACSVWithNoRowsIsRefused(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)

	var body strings.Builder
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "cases.csv")
	_, _ = part.Write([]byte("kunde,betrag\n"))
	_ = mw.Close()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body.String()))
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	svc.HandleStartRunFromCSV(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body %s)", rec.Code, rec.Body)
	}
}
