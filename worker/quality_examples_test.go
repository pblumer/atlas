package worker_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/connector/ldap"
	"github.com/pblumer/atlas/connector/ldif"
	"github.com/pblumer/atlas/connector/sqldb"
	"github.com/pblumer/atlas/examples/testdaten"
	"github.com/pblumer/atlas/limits"
	"github.com/pblumer/atlas/worker"
)

// The quality-check examples, run as shipped (docs/planning/ad-search-as-document.md,
// S3): each reads its source as a document and checks every record with its decision
// in one job of the in-process DMN worker. The data is the generator's
// (examples/testdaten): six records in every hundred carry a finding, and which rules
// each one breaks follows from its index. TestTheQualityChecksAt50000Records runs the
// same four with 50,000 records when ATLAS_BENCH_BULK=full.

// broken are the rules each defect bucket breaks, per decision.
var broken = map[string]map[int][]string{
	"MitarbeitendeGueltig": {
		testdaten.BucketNoMail: {"mailGueltig"}, testdaten.BucketSecond: {"mailGueltig"},
		testdaten.BucketDepartment: {"abteilungBekannt"}, testdaten.BucketThird: {"pensumGueltig"},
		testdaten.BucketFourth: {"datenKonsistent"}, testdaten.BucketTwoFindings: {"abteilungBekannt", "mailGueltig"},
	},
	"PersonGueltig": {
		testdaten.BucketNoMail: {"mailGueltig"}, testdaten.BucketSecond: {"mailGueltig"},
		testdaten.BucketDepartment: {"abteilungBekannt"}, testdaten.BucketThird: {"personalnummerGueltig"},
		testdaten.BucketFourth: {"typBekannt"}, testdaten.BucketTwoFindings: {"abteilungBekannt", "mailGueltig"},
	},
	"AdBenutzerGueltig": {
		testdaten.BucketNoMail: {"mailGesetzt"}, testdaten.BucketSecond: {"kontoAktiv"},
		testdaten.BucketDepartment: {"abteilungBekannt"}, testdaten.BucketThird: {"lizenzEindeutig"},
		testdaten.BucketFourth: {"lizenzEindeutig"}, testdaten.BucketTwoFindings: {"abteilungBekannt", "kontoAktiv", "mailGesetzt"},
	},
}

// rules are each decision's rules, the outputs beside valid.
var rules = map[string][]string{
	"MitarbeitendeGueltig": {"mailGueltig", "abteilungBekannt", "pensumGueltig", "datenKonsistent"},
	"PersonGueltig":        {"mailGueltig", "abteilungBekannt", "personalnummerGueltig", "typBekannt"},
	"AdBenutzerGueltig":    {"kontoAktiv", "mailGesetzt", "abteilungBekannt", "lizenzEindeutig"},
}

// qualityExample is one example's directory, decision and process.
type qualityExample struct {
	dir, dmn, decision, bpmn string
	// key reads a failure's record index from its columns.
	key func(f map[string]any) int
}

var trailingDigits = regexp.MustCompile(`[0-9]+$`)

func indexFromAccount(column string) func(map[string]any) int {
	return func(f map[string]any) int {
		s, _ := f[column].(string)
		n, _ := strconv.Atoi(trailingDigits.FindString(s))
		return n
	}
}

func indexFromPersonalnummer(f map[string]any) int {
	s, _ := f["personalnummer"].(string)
	n, _ := strconv.Atoi(s)
	return n - 100000
}

var (
	employeesFile = qualityExample{"mitarbeitende-pruefen", "mitarbeitende-pruefen.dmn", "MitarbeitendeGueltig", "mitarbeitende-datei-pruefen.bpmn", indexFromPersonalnummer}
	employeesSQL  = qualityExample{"mitarbeitende-pruefen", "mitarbeitende-pruefen.dmn", "MitarbeitendeGueltig", "mitarbeitende-sql-pruefen.bpmn", indexFromPersonalnummer}
	peopleLDAP    = qualityExample{"ldap-personen-pruefen", "ldap-personen-pruefen.dmn", "PersonGueltig", "ldap-personen-pruefen.bpmn", indexFromAccount("uid")}
	accountsAD    = qualityExample{"ad-benutzer-pruefen", "ad-benutzer-pruefen.dmn", "AdBenutzerGueltig", "ad-benutzer-pruefen.bpmn", indexFromAccount("sAMAccountName")}
)

func exampleFile(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "examples", dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// deployExample puts the example's decision and process into an application, the
// decision first, because the deploy gate refuses a binding that does not resolve.
func deployExample(t *testing.T, ts *httptest.Server, ex qualityExample) uint64 {
	t.Helper()
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(post(t, ts, "/api/v1/applications", `{"name":"Beispiele: Datenqualität"}`, "application/json"), &app); err != nil || app.ID == "" {
		t.Fatalf("application: %v", err)
	}
	handle := strings.TrimSuffix(ex.dmn, ".dmn")
	post(t, ts, "/api/v1/dmn-models?handle="+handle, string(exampleFile(t, ex.dir, ex.dmn)), "application/xml")
	post(t, ts, "/api/v1/dmnrefs", `{"name":"`+ex.decision+`","modelRef":"`+handle+`","projectId":"`+app.ID+`"}`, "application/json")
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(post(t, ts, "/api/v1/deployments?projectId="+app.ID, string(exampleFile(t, ex.dir, ex.bpmn)), "application/xml"), &dep); err != nil || dep.Key == 0 {
		t.Fatalf("deploy %s: %v", ex.bpmn, err)
	}
	return dep.Key
}

// checkSummary holds the run to the generator's numbers: n records, the defective ones
// reported, each with exactly the rules its bucket breaks, and the result document
// holding every record.
func checkSummary(t *testing.T, ts *httptest.Server, ex qualityExample, n int) {
	t.Helper()
	var sum struct {
		Total, Valid, Invalid, Errors int
		Truncated                     bool
		Failures                      []map[string]any
		Result                        map[string]any
	}
	raw, _ := json.Marshal(instanceVariables(t, ts)["pruefung"])
	if err := json.Unmarshal(raw, &sum); err != nil {
		t.Fatalf("pruefung = %s", raw)
	}
	bad := testdaten.Defective(n)
	if sum.Total != n || sum.Valid != n-bad || sum.Invalid != bad || sum.Errors != 0 {
		t.Fatalf("%s: total %d valid %d invalid %d errors %d, want %d / %d / %d / 0", ex.bpmn, sum.Total, sum.Valid, sum.Invalid, sum.Errors, n, n-bad, bad)
	}
	if want := min(bad, 100); len(sum.Failures) != want {
		t.Fatalf("%s: %d failures in the sample, want %d", ex.bpmn, len(sum.Failures), want)
	}
	for _, f := range sum.Failures {
		i := ex.key(f)
		want, ok := broken[ex.decision][testdaten.Bucket(i)]
		if !ok {
			t.Errorf("%s: record %d is reported, but the generator made it in order: %v", ex.bpmn, i, f)
			continue
		}
		var got []string
		for _, r := range rules[ex.decision] {
			if f["_"+r] == false {
				got = append(got, r)
			}
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: record %d breaks %v, want %v", ex.bpmn, i, got, want)
		}
	}
	id, _ := sum.Result["$document"].(string)
	resp, err := http.Get(ts.URL + "/api/v1/documents/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(body), "\uFEFF"))).ReadAll()
	if err != nil || len(rows) != n+1 {
		t.Fatalf("%s: result document %d rows (%v), want %d", ex.bpmn, len(rows), err, n+1)
	}
}

// runWorker leases one job with the given handler and fails the test on its error.
func runWorker(t *testing.T, ts *httptest.Server, jobType string, run func(context.Context, worker.Job) (map[string]any, error)) {
	t.Helper()
	var handlerErr error
	w := worker.New(worker.Options{Server: ts.URL, ID: "quality", Handlers: map[string]worker.Exec{
		jobType: worker.ExecFunc(func(ctx context.Context, j worker.Job) (map[string]any, error) {
			out, err := run(ctx, j)
			handlerErr = err
			return out, err
		}),
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if handlerErr != nil {
		t.Fatalf("the worker's handler failed: %v", handlerErr)
	}
}

func waitDone(t *testing.T, ts *httptest.Server) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for runningInstances(t, ts) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the check did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// fileCheck runs the file variant: the start parks on the upload task, the file is
// uploaded as a document and the task completed with the reference, as the Tasks app
// does for a filepicker.
func fileCheck(t *testing.T, ts *httptest.Server, data []byte, n int) {
	t.Helper()
	def := deployExample(t, ts, employeesFile)
	post(t, ts, "/api/v1/processes/"+itoa(def)+"/instances", `{}`, "application/json")
	ref := post201(t, ts, "/api/v1/documents?name=mitarbeitende.csv", data, "text/csv")
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(listRows(t, get(t, ts, "/api/v1/tasks")), &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("tasks = %v (%v)", tasks, err)
	}
	post(t, ts, fmt.Sprintf("/api/v1/tasks/%d/complete", tasks[0].Key), `{"variables":{"mitarbeitende":`+ref+`}}`, "application/json")
	waitDone(t, ts)
	checkSummary(t, ts, employeesFile, n)
}

// sqlCheck runs the SQL Server variant against the mock database, seeded as the
// example's README says.
func sqlCheck(t *testing.T, ts *httptest.Server, seed []byte, n int) {
	t.Helper()
	def := deployExample(t, ts, employeesSQL)
	post(t, ts, "/api/v1/processes/"+itoa(def)+"/instances", `{}`, "application/json")
	answers, err := sqldb.ParseMockSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	product, _ := sqldb.ProductByJobType(compiler.MsSqlJobType)
	reg := sqldb.NewRegistry()
	reg.Register("hr", sqldb.OpenMock(sqldb.NewMockDatabase(product, answers...)))
	runWorker(t, ts, compiler.MsSqlJobType, func(ctx context.Context, j worker.Job) (map[string]any, error) {
		return worker.RunSQLJob(ctx, j, reg)
	})
	waitDone(t, ts)
	checkSummary(t, ts, employeesSQL, n)
}

// ldapCheck runs the LDAP variant against an in-memory directory loaded from the LDIF
// the example ships: the AD mock behind an LDAP dialer, which speaks the same filter
// language and pages its result (the LDAP worker has no mock mode of its own).
func ldapCheck(t *testing.T, ts *httptest.Server, data []byte, n int) {
	t.Helper()
	def := deployExample(t, ts, peopleLDAP)
	post(t, ts, "/api/v1/processes/"+itoa(def)+"/instances", `{}`, "application/json")
	dir := ad.NewMockDirectory(seedEntries(t, data)...)
	secret := func(ref string) string {
		if ref == "LDAP_BIND" {
			return "geheim"
		}
		return ""
	}
	runWorker(t, ts, compiler.LdapJobType, func(ctx context.Context, j worker.Job) (map[string]any, error) {
		return worker.RunLdapJob(ctx, j, mockLDAP{dir}, secret)
	})
	waitDone(t, ts)
	checkSummary(t, ts, peopleLDAP, n)
}

// adCheck runs the AD example against its mock, seeded with data.
func adCheck(t *testing.T, ts *httptest.Server, data []byte, n int) {
	t.Helper()
	def := deployExample(t, ts, accountsAD)
	post(t, ts, "/api/v1/processes/"+itoa(def)+"/instances", `{}`, "application/json")
	dir := ad.NewMockDirectory(seedEntries(t, data)...)
	runWorker(t, ts, compiler.AdJobType, func(ctx context.Context, j worker.Job) (map[string]any, error) {
		return worker.RunADJob(ctx, j, dir, func(string) string { return "beliebig" }, nil)
	})
	waitDone(t, ts)
	checkSummary(t, ts, accountsAD, n)
}

func seedEntries(t *testing.T, data []byte) []ad.Entry {
	t.Helper()
	parsed, err := ldif.Parse(ldif.FormatLDIF, data)
	if err != nil {
		t.Fatalf("the seed does not parse: %v", err)
	}
	out := make([]ad.Entry, 0, len(parsed))
	for _, e := range parsed {
		out = append(out, ad.Entry{DN: e.DN, Attributes: e.Attributes})
	}
	return out
}

func post201(t *testing.T, ts *httptest.Server, path string, body []byte, contentType string) string {
	t.Helper()
	res, err := http.Post(ts.URL+path, contentType, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST %s: %d %s", path, res.StatusCode, out)
	}
	return string(out)
}

func qualityAtlas(t *testing.T, opts ...api.Option) *httptest.Server {
	return exampleAtlas(t, append([]api.Option{api.WithOffloadedConnectorKinds([]string{"ad", "ldap"})}, opts...)...)
}

func TestTheEmployeeFileCheckExample(t *testing.T) {
	fileCheck(t, qualityAtlas(t), exampleFile(t, "mitarbeitende-pruefen", "mitarbeitende.csv"), 200)
}

func TestTheEmployeeSQLCheckExample(t *testing.T) {
	sqlCheck(t, qualityAtlas(t), exampleFile(t, "mitarbeitende-pruefen", "mssql-seed.json"), 200)
}

func TestTheLDAPPersonCheckExample(t *testing.T) {
	ldapCheck(t, qualityAtlas(t), exampleFile(t, "ldap-personen-pruefen", "personen.ldif"), 200)
}

// The same four with the generator's 50,000 records: the size the plan set out to
// reach. Run with ATLAS_BENCH_BULK=full; each run logs how long it took.
func TestTheQualityChecksAt50000Records(t *testing.T) {
	if os.Getenv("ATLAS_BENCH_BULK") != "full" {
		t.Skip("set ATLAS_BENCH_BULK=full to check 50,000 records per source")
	}
	const n = 50000
	people := testdaten.Population(n)
	l := limits.Default()
	for _, tc := range []struct {
		name string
		run  func()
	}{
		{"file", func() { fileCheck(t, qualityAtlas(t, api.WithLimits(l)), testdaten.CSV(people), n) }},
		{"sql", func() { sqlCheck(t, qualityAtlas(t, api.WithLimits(l)), testdaten.MSSQLSeed(people), n) }},
		{"ldap", func() { ldapCheck(t, qualityAtlas(t, api.WithLimits(l)), testdaten.PeopleLDIF(people), n) }},
		{"ad", func() { adCheck(t, qualityAtlas(t, api.WithLimits(l)), testdaten.AccountsLDIF(people), n) }},
	} {
		start := time.Now()
		tc.run()
		t.Logf("%s: %d records checked in %v", tc.name, n, time.Since(start).Round(time.Millisecond))
	}
}

// mockLDAP is an LDAP dialer over the AD mock directory: the same in-memory tree, the
// same filters, the same paging, behind the LDAP worker's interfaces.
type mockLDAP struct{ dir *ad.MockDirectory }

func (m mockLDAP) Dial(o ldap.DialOptions) (ldap.Conn, error) {
	c, err := m.dir.Dial(o.URL, o.BindDN, o.BindPassword, o.StartTLS)
	if err != nil {
		return nil, err
	}
	return mockLDAPConn{c}, nil
}

type mockLDAPConn struct{ c ad.Conn }

func (m mockLDAPConn) request(r ldap.SearchRequest) ad.SearchRequest {
	return ad.SearchRequest{BaseDN: r.BaseDN, Scope: r.Scope, Filter: r.Filter, Attributes: r.Attributes, PageSize: r.PageSize, MaxEntries: r.MaxEntries}
}

func toLDAP(entries []ad.Entry) []ldap.Entry {
	out := make([]ldap.Entry, len(entries))
	for i, e := range entries {
		out[i] = ldap.Entry{DN: e.DN, Attributes: e.Attributes}
	}
	return out
}

func (m mockLDAPConn) Search(r ldap.SearchRequest) ([]ldap.Entry, error) {
	e, err := m.c.Search(m.request(r))
	return toLDAP(e), err
}

func (m mockLDAPConn) SearchPages(r ldap.SearchRequest, fn func([]ldap.Entry) error) error {
	ps, ok := m.c.(interface {
		SearchPages(ad.SearchRequest, func([]ad.Entry) error) error
	})
	if !ok {
		e, err := m.Search(r)
		if err != nil {
			return err
		}
		return fn(e)
	}
	return ps.SearchPages(m.request(r), func(e []ad.Entry) error { return fn(toLDAP(e)) })
}

func (mockLDAPConn) Add(string, map[string][]string) error { return fmt.Errorf("read only") }
func (mockLDAPConn) Modify(string, []ldap.Mod) error       { return fmt.Errorf("read only") }
func (mockLDAPConn) Delete(string) error                   { return fmt.Errorf("read only") }
func (mockLDAPConn) SetPassword(string, string) error      { return fmt.Errorf("read only") }
func (m mockLDAPConn) Close() error                        { return m.c.Close() }
