package playground

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/connector/csvimport"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/playground"
)

// maxCasesPerRun is the ceiling the record settles on. It is a resource bound:
// a case is an instance in a live engine, and the report and the results pages
// are sized for this number, not for an unbounded one.
const maxCasesPerRun = 50_000

// maxCSVBytes caps a dataset upload, matching the CSV start path's own ceiling.
const maxCSVBytes = 16 << 20 // 16 MiB

// csvMultipartMemory is how much of a multipart body is kept in memory before
// parts spill to temp files.
const csvMultipartMemory = 4 << 20

// arrivalReq is how a caller spreads a dataset over simulated time.
type arrivalReq struct {
	// Mode is "allAtOnce" (the default), "sequential", "every" or "poisson".
	Mode string `json:"mode,omitempty"`
	// IntervalMillis is the takt for "every"; PerHour the mean rate for "poisson".
	IntervalMillis int64   `json:"intervalMillis,omitempty"`
	PerHour        float64 `json:"perHour,omitempty"`
	// Calendar confines the arrivals to business hours.
	Calendar calendarReq `json:"calendar,omitempty"`
}

// calendarReq is a working calendar on the wire: windows in minutes from
// midnight, and the weekdays they apply to (0 = Sunday, as time.Weekday counts).
type calendarReq struct {
	Open []windowReq `json:"open,omitempty"`
	Days []int       `json:"days,omitempty"`
}

type windowReq struct {
	FromMinutes int `json:"fromMinutes"`
	ToMinutes   int `json:"toMinutes"`
}

func (c calendarReq) toCalendar() playground.Calendar {
	out := playground.Calendar{}
	for _, w := range c.Open {
		out.Open = append(out.Open, playground.Window{
			From: time.Duration(w.FromMinutes) * time.Minute,
			To:   time.Duration(w.ToMinutes) * time.Minute,
		})
	}
	for _, d := range c.Days {
		if d >= 0 && d < 7 {
			out.Days[d] = true
		}
	}
	return out
}

func (a arrivalReq) toArrival() (playground.Arrival, error) {
	out := playground.Arrival{
		Interval: time.Duration(a.IntervalMillis) * time.Millisecond,
		PerHour:  a.PerHour,
		Calendar: a.Calendar.toCalendar(),
	}
	switch a.Mode {
	case "", "allAtOnce":
		out.Mode = playground.ArrivalAllAtOnce
	case "sequential":
		out.Mode = playground.ArrivalSequential
	case "every":
		out.Mode = playground.ArrivalEvery
	case "poisson":
		out.Mode = playground.ArrivalPoisson
	default:
		return out, fmt.Errorf("arrival mode %q is not one of allAtOnce, sequential, every, poisson", a.Mode)
	}
	return out, nil
}

// startRunReq starts a batch over a dataset given inline.
type startRunReq struct {
	// Cases is one entry per case: the start variables it begins with.
	Cases   []map[string]any `json:"cases"`
	Arrival arrivalReq       `json:"arrival"`
}

type runStatusResp struct {
	State       string `json:"state"`
	Occurrences int    `json:"occurrences"`
	Cases       int    `json:"cases"`
	Completed   int    `json:"completed"`
	SimTime     string `json:"simTime,omitempty"`
	Error       string `json:"error,omitempty"`
}

type reportResp struct {
	Cases       int    `json:"cases"`
	Completed   int    `json:"completed"`
	Incidents   int    `json:"incidents"`
	MaxInFlight int    `json:"maxInFlight"`
	SimStart    string `json:"simStart"`
	SimEnd      string `json:"simEnd"`
	Duration    struct {
		Count      int   `json:"count"`
		MinMillis  int64 `json:"minMillis"`
		P50Millis  int64 `json:"p50Millis"`
		P90Millis  int64 `json:"p90Millis"`
		MaxMillis  int64 `json:"maxMillis"`
		MeanMillis int64 `json:"meanMillis"`
	} `json:"duration"`
	Elements map[string]elementResp `json:"elements"`
	Pools    map[string]poolResp    `json:"pools"`
	Visits   map[string]int64       `json:"visits"`
}

type elementResp struct {
	Runs          int   `json:"runs"`
	WorkMillis    int64 `json:"workMillis"`
	WaitMillis    int64 `json:"waitMillis"`
	MaxWaitMillis int64 `json:"maxWaitMillis"`
}

type poolResp struct {
	Capacity   int   `json:"capacity"`
	Served     int   `json:"served"`
	BusyMillis int64 `json:"busyMillis"`
	// AvailableMillis is the seat time the pool's calendar offered over the run —
	// what BusyMillis is a fraction of.
	AvailableMillis    int64 `json:"availableMillis"`
	MaxQueue           int   `json:"maxQueue"`
	UtilisationPercent int   `json:"utilisationPercent"`
}

type caseRowResp struct {
	Index       int               `json:"index"`
	InstanceKey string            `json:"instanceKey"`
	State       string            `json:"state"`
	Started     string            `json:"started"`
	Ended       string            `json:"ended,omitempty"`
	DurationMs  int64             `json:"durationMillis"`
	Incidents   int               `json:"incidents"`
	End         string            `json:"end,omitempty"`
	Variables   map[string]string `json:"variables"`
}

type resultsResp struct {
	Total  int           `json:"total"`
	Offset int           `json:"offset"`
	Rows   []caseRowResp `json:"rows"`
}

// HandleStartRun starts a batch over a dataset sent inline.
func (s *Service) HandleStartRun(w http.ResponseWriter, r *http.Request) {
	var req startRunReq
	if !decode(w, r, maxModelBytes, &req) {
		return
	}
	plan, ok := s.planFrom(w, req.Cases, req.Arrival)
	if !ok {
		return
	}
	s.startRun(w, r, plan)
}

// HandleStartRunFromCSV starts a batch over an uploaded CSV, one case per row.
//
// The file is parsed by the same code the CSV start path uses (ADR-0084/0139),
// against a layout derived from its own header: every column becomes a start
// variable under its header's name. A playground dataset is somebody's export,
// not a configured integration, so asking them to describe the columns they just
// exported would be asking twice.
func (s *Service) HandleStartRunFromCSV(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCSVBytes)
	if err := r.ParseMultipartForm(csvMultipartMemory); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read upload: "+err.Error())
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "the upload needs a \"file\" part holding the CSV")
		return
	}
	defer func() { _ = file.Close() }()
	data := make([]byte, 0, 64<<10)
	buf := make([]byte, 32<<10)
	for {
		n, readErr := file.Read(buf)
		data = append(data, buf[:n]...)
		if readErr != nil {
			break
		}
		if len(data) > maxCSVBytes {
			httpapi.Error(w, http.StatusRequestEntityTooLarge, "the CSV is larger than the upload limit")
			return
		}
	}

	rows, err := rowsFromCSV(data)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	var arrival arrivalReq
	if raw := r.FormValue("arrival"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &arrival); err != nil {
			httpapi.Error(w, http.StatusBadRequest, "arrival: "+err.Error())
			return
		}
	}
	plan, ok := s.planFrom(w, rows, arrival)
	if !ok {
		return
	}
	s.startRun(w, r, plan)
}

// rowsFromCSV turns an uploaded file into one row object per line, using the
// file's own header as the layout.
func rowsFromCSV(data []byte) ([]map[string]any, error) {
	header, err := csv.NewReader(bytes.NewReader(data)).Read()
	if err != nil {
		return nil, fmt.Errorf("read the CSV header: %w", err)
	}
	cfg := csvimport.Config{Columns: make([]csvimport.Column, 0, len(header))}
	seen := map[string]bool{}
	for i, name := range header {
		if name == "" || seen[name] {
			return nil, fmt.Errorf("column %d has an empty or repeated header; every column needs its own name", i+1)
		}
		seen[name] = true
		cfg.Columns = append(cfg.Columns, csvimport.Column{Name: name, Header: name})
	}
	return csvimport.ParseRows(cfg, data)
}

// planFrom turns a dataset and an arrival configuration into a plan, refusing
// what cannot be run.
func (s *Service) planFrom(w http.ResponseWriter, cases []map[string]any, a arrivalReq) (playground.Plan, bool) {
	switch {
	case len(cases) == 0:
		httpapi.Error(w, http.StatusBadRequest, "a run needs at least one case")
		return playground.Plan{}, false
	case len(cases) > maxCasesPerRun:
		httpapi.Error(w, http.StatusBadRequest,
			fmt.Sprintf("a run holds at most %d cases; this dataset has %d", maxCasesPerRun, len(cases)))
		return playground.Plan{}, false
	}
	arrival, err := a.toArrival()
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return playground.Plan{}, false
	}
	plan := playground.Plan{Arrival: arrival, Cases: make([][]model.VariableValue, 0, len(cases))}
	for i, row := range cases {
		vars, err := s.vars(row)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf("case %d: %s", i, err))
			return playground.Plan{}, false
		}
		plan.Cases = append(plan.Cases, vars)
	}
	return plan, true
}

// startRun hands the plan to the session and answers with the batch's first
// status. It answers 202: the run outlives the request that started it.
func (s *Service) startRun(w http.ResponseWriter, r *http.Request, plan playground.Plan) {
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	if err := sess.StartRun(plan); err != nil {
		if playground.ErrClosedSession(err) {
			httpapi.Error(w, http.StatusNotFound, "the playground session has been closed")
			return
		}
		httpapi.Error(w, http.StatusConflict, err.Error())
		return
	}
	httpapi.JSON(w, http.StatusAccepted, renderStatus(sess.RunStatus()))
}

// HandleRunStatus reports how far a batch has got.
func (s *Service) HandleRunStatus(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	httpapi.JSON(w, http.StatusOK, renderStatus(sess.RunStatus()))
}

// HandleCancelRun stops a batch at the end of its current slice, leaving what it
// did readable.
func (s *Service) HandleCancelRun(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	sess.Cancel()
	httpapi.JSON(w, http.StatusOK, renderStatus(sess.RunStatus()))
}

// HandleReport returns the run's summary.
func (s *Service) HandleReport(w http.ResponseWriter, r *http.Request) {
	var out reportResp
	s.run(w, r, func(sb *playground.Sandbox) error {
		rep, err := sb.Report()
		if err != nil {
			return err
		}
		out = renderReport(rep)
		return nil
	}, &out)
}

// HandleResults returns one page of the results table.
func (s *Service) HandleResults(w http.ResponseWriter, r *http.Request) {
	offset := intParam(r, "offset", 0)
	limit := intParam(r, "limit", 100)
	if limit > 1000 {
		limit = 1000 // a page is read by a person or a script, not swallowed whole
	}
	out := resultsResp{Offset: offset, Rows: []caseRowResp{}}
	s.run(w, r, func(sb *playground.Sandbox) error {
		rows, total, err := sb.Cases(offset, limit)
		if err != nil {
			return err
		}
		out.Total = total
		for _, row := range rows {
			out.Rows = append(out.Rows, renderRow(row))
		}
		return nil
	}, &out)
}

// HandleResultsCSV streams the whole results table as CSV, a page at a time, so
// the response is bounded however many cases there are.
func (s *Service) HandleResultsCSV(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	// The header goes out before the first page is read, so a failure halfway
	// through cannot be turned into an error response — it truncates the file
	// instead. That is the trade every streamed download makes; the alternative is
	// building fifty thousand rows in memory first, which is the thing this avoids.
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="playground-results.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()

	var (
		columns []string
		wrote   bool
	)
	for offset := 0; ; offset += csvPageSize {
		var (
			rows  []playground.CaseRow
			total int
		)
		if err := sess.With(func(sb *playground.Sandbox) error {
			var e error
			rows, total, e = sb.Cases(offset, csvPageSize)
			return e
		}); err != nil {
			return // the session went away mid-stream; the file stops where it is
		}
		if len(rows) == 0 {
			break
		}
		if !wrote {
			columns = variableColumns(rows)
			header := append([]string{"case", "state", "end", "startedAt", "endedAt", "durationSeconds", "incidents"}, columns...)
			_ = cw.Write(header)
			wrote = true
		}
		for _, row := range rows {
			_ = cw.Write(csvRow(row, columns))
		}
		cw.Flush()
		if offset+len(rows) >= total {
			break
		}
	}
}

// csvPageSize is how many rows the CSV stream reads at a time. Large enough that
// the paging is not the cost, small enough that nothing large is held.
const csvPageSize = 500

// variableColumns picks the variable names the CSV carries, from the first page.
// A later case that carries a variable none of the first five hundred had is
// reported without it: a CSV has one header, and choosing it from the rows in
// hand beats reading the whole table twice to find out.
func variableColumns(rows []playground.CaseRow) []string {
	seen := map[string]bool{}
	var out []string
	for _, row := range rows {
		for name := range row.Variables {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func csvRow(row playground.CaseRow, columns []string) []string {
	rec := []string{
		strconv.Itoa(row.Index),
		row.State.String(),
		row.End,
		rfc3339(row.Started),
		"",
		strconv.FormatFloat(row.Duration.Seconds(), 'f', 3, 64),
		strconv.Itoa(row.Incidents),
	}
	if !row.Ended.IsZero() {
		rec[4] = rfc3339(row.Ended)
	}
	for _, name := range columns {
		rec = append(rec, row.Variables[name])
	}
	return rec
}

func renderStatus(st playground.RunStatus) runStatusResp {
	out := runStatusResp{
		State: string(st.State), Occurrences: st.Occurrences,
		Cases: st.Cases, Completed: st.Completed, Error: st.Err,
	}
	if !st.SimTime.IsZero() {
		out.SimTime = rfc3339(st.SimTime)
	}
	return out
}

func renderReport(rep playground.Report) reportResp {
	out := reportResp{
		Cases: rep.Cases, Completed: rep.Completed, Incidents: rep.Incidents,
		MaxInFlight: rep.MaxInFlight,
		SimStart:    rfc3339(rep.SimStart), SimEnd: rfc3339(rep.SimEnd),
		Elements: map[string]elementResp{}, Pools: map[string]poolResp{},
		Visits: rep.Visits,
	}
	out.Duration.Count = rep.Duration.Count
	out.Duration.MinMillis = rep.Duration.Min.Milliseconds()
	out.Duration.P50Millis = rep.Duration.P50.Milliseconds()
	out.Duration.P90Millis = rep.Duration.P90.Milliseconds()
	out.Duration.MaxMillis = rep.Duration.Max.Milliseconds()
	out.Duration.MeanMillis = rep.Duration.Mean.Milliseconds()
	for id, el := range rep.Elements {
		out.Elements[id] = elementResp{
			Runs: el.Runs, WorkMillis: el.Work.Milliseconds(),
			WaitMillis: el.Wait.Milliseconds(), MaxWaitMillis: el.MaxWait.Milliseconds(),
		}
	}
	for name, p := range rep.Pools {
		pr := poolResp{
			Capacity: p.Capacity, Served: p.Served,
			BusyMillis: p.BusyTime.Milliseconds(), MaxQueue: p.MaxQueue,
			AvailableMillis: p.Available.Milliseconds(),
		}
		// Utilisation is seat time over the seat time the pool's calendar actually
		// offered. Dividing by the run's span instead would count every night and
		// weekend as idle capacity, and report a pool with three hundred cases
		// queued as a quarter busy.
		if p.Available > 0 {
			pr.UtilisationPercent = int(100 * p.BusyTime / p.Available)
		}
		out.Pools[name] = pr
	}
	if out.Visits == nil {
		out.Visits = map[string]int64{}
	}
	return out
}

func renderRow(row playground.CaseRow) caseRowResp {
	out := caseRowResp{
		Index: row.Index, InstanceKey: strconv.FormatUint(row.InstanceKey, 10),
		State: row.State.String(), Started: rfc3339(row.Started),
		DurationMs: row.Duration.Milliseconds(), Incidents: row.Incidents,
		End: row.End, Variables: row.Variables,
	}
	if !row.Ended.IsZero() {
		out.Ended = rfc3339(row.Ended)
	}
	if out.Variables == nil {
		out.Variables = map[string]string{}
	}
	return out
}

// intParam reads a non-negative query parameter, falling back to def.
func intParam(r *http.Request, name string, def int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return def
	}
	return v
}
