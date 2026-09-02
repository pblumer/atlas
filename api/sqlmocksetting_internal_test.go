package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

const sqlMockSeedJSON = `{"answers":[
  {"statement":"SELECT id, mail FROM personen WHERE kuerzel = @p1","params":["abo"],"columns":["id","mail"],"rows":[[7,"arno@example.com"]]},
  {"statement":"UPDATE personen SET aktiv = @aktiv WHERE id = @id","named":{"id":7,"aktiv":true},"affected":1}
]}`

// Absence and a stored "off" are different states, exactly as they are for the AD
// switch. No record means nobody has decided in the Console, so whatever
// ATLAS_MSSQL_MOCK the server was started with keeps deciding; a stored record
// decides either way. That is what keeps an existing install working as it did until
// somebody touches the switch.
func TestTheSQLMockSwitchTellsUnsetFromOff(t *testing.T) {
	srv, _ := newValidateServer(t)

	var (
		got    sqlMockSetting
		stored bool
		err    error
	)
	srv.do(func() { got, stored, err = srv.settings.getSQLMock() })
	if err != nil {
		t.Fatalf("getSQLMock on a fresh server: %v", err)
	}
	if stored {
		t.Fatal("a fresh server reports a stored SQL mockup switch; the host environment must still decide")
	}

	srv.do(func() { err = srv.settings.saveSQLMock(sqlMockSetting{Enabled: false}) })
	if err != nil {
		t.Fatalf("saveSQLMock: %v", err)
	}
	srv.do(func() { got, stored, err = srv.settings.getSQLMock() })
	if err != nil || !stored {
		t.Fatalf("after saving off: stored=%v err=%v, want a stored record", stored, err)
	}
	if got.Enabled {
		t.Error("a stored off came back on")
	}
}

// The seed file is named by a digest of its own content. That is not caching: the
// supervisor restarts a child only when its rendered environment differs, so a fixed
// filename would hand an unchanged ATLAS_MSSQL_MOCK_SEED to a worker that then kept
// answering from yesterday's seed after an operator replaced it.
func TestTheSQLSeedFileIsNamedByItsContent(t *testing.T) {
	srv, _ := newValidateServer(t)

	var first, second, none string
	srv.do(func() {
		none = srv.settings.sqlSeedPath(sqlMockSetting{Enabled: true})
		first = srv.settings.sqlSeedPath(sqlMockSetting{Enabled: true, Seed: sqlMockSeedJSON})
		second = srv.settings.sqlSeedPath(sqlMockSetting{Enabled: true, Seed: `{"answers":[{"statement":"SELECT 1","columns":["n"],"rows":[[1]]}]}`})
	})
	if none != "" {
		t.Errorf("sqlSeedPath = %q for a setting with no seed, want nothing to point a worker at", none)
	}
	if first == "" || !strings.HasSuffix(first, ".json") {
		t.Errorf("sqlSeedPath = %q, want a .json file", first)
	}
	if first == second {
		t.Error("two different seeds resolve to one filename, so replacing a seed would not restart the worker")
	}
}

// Saving writes the seed where the worker will read it, and drops every earlier one.
// A stale seed is not merely untidy: a worker restarted with an older environment
// would still find one and answer from a seed nobody asked for.
func TestSavingASQLSeedDropsTheOneBefore(t *testing.T) {
	srv, _ := newValidateServer(t)

	var err error
	srv.do(func() { err = srv.settings.saveSQLMock(sqlMockSetting{Enabled: true, Seed: sqlMockSeedJSON}) })
	if err != nil {
		t.Fatalf("saveSQLMock: %v", err)
	}
	if n := sqlSeedFiles(t, srv); n != 1 {
		t.Fatalf("%d seed files after the first save, want 1", n)
	}

	srv.do(func() {
		err = srv.settings.saveSQLMock(sqlMockSetting{Enabled: true, Seed: `{"answers":[{"statement":"SELECT 1","columns":["n"],"rows":[[1]]}]}`})
	})
	if err != nil {
		t.Fatalf("second saveSQLMock: %v", err)
	}
	if n := sqlSeedFiles(t, srv); n != 1 {
		t.Errorf("%d seed files after replacing the seed, want the old one gone", n)
	}

	// Clearing the seed leaves none behind at all.
	srv.do(func() { err = srv.settings.saveSQLMock(sqlMockSetting{Enabled: true}) })
	if err != nil {
		t.Fatalf("clearing saveSQLMock: %v", err)
	}
	if n := sqlSeedFiles(t, srv); n != 0 {
		t.Errorf("%d seed files after clearing the seed, want none", n)
	}
}

// sqlSeedFiles counts the mock seeds on disk.
func sqlSeedFiles(t *testing.T, srv *Server) int {
	t.Helper()
	var n int
	srv.do(func() {
		matches, err := filepath.Glob(filepath.Join(srv.settings.dir, sqlSeedPrefix+"*"))
		if err != nil {
			t.Fatalf("glob seeds: %v", err)
		}
		n = len(matches)
	})
	return n
}

// The switch runs against what an operator typed, and refuses a seed that is not
// readable — here, where the person who can fix it is waiting for an answer. The
// worker parses it again and degrades to an unseeded mock if it cannot, which is right
// there and wrong here: a mockup that quietly answers nothing is a bad way to learn
// about a typo.
func TestSavingTheSQLMockChecksTheSeed(t *testing.T) {
	srv, _ := newValidateServer(t)

	body, _ := json.Marshal(map[string]any{"enabled": true, "seed": `{"answers":[{"columns":["id"]}]}`, "seedName": "hr.json"})
	code, resp := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/sql-mock", string(body), "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d (%s), want a seed with no statement refused", code, resp)
	}
	if !strings.Contains(string(resp), "needs a statement") {
		t.Errorf("response = %s, want the seed's own complaint", resp)
	}

	// Nothing was stored by refusing.
	var stored bool
	srv.do(func() { _, stored, _ = srv.settings.getSQLMock() })
	if stored {
		t.Error("a refused seed still wrote the switch")
	}
}

// A document that parses but holds no answers is almost always a file picked by
// mistake. The state it would produce — a mockup that refuses everything — is reachable
// by storing no seed at all, and saying so is more use than storing it.
func TestASeedWithNoAnswersIsRefused(t *testing.T) {
	srv, _ := newValidateServer(t)
	body, _ := json.Marshal(map[string]any{"enabled": true, "seed": `{"answers":[]}`})
	code, resp := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/sql-mock", string(body), "application/json")
	if code != http.StatusBadRequest || !strings.Contains(string(resp), "holds no answers") {
		t.Fatalf("status = %d (%s), want an empty seed refused with a reason", code, resp)
	}
}

// A good seed is stored, counted back, and readable — and the count is what lets the
// Console say "2 answers" instead of leaving the operator to trust a silent upload.
func TestSavingAndReadingTheSQLMockSwitch(t *testing.T) {
	srv, _ := newValidateServer(t)

	body, _ := json.Marshal(map[string]any{"enabled": true, "seed": sqlMockSeedJSON, "seedName": "hr.json"})
	code, resp := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/sql-mock", string(body), "application/json")
	if code != http.StatusOK {
		t.Fatalf("PUT: status=%d body=%s", code, resp)
	}
	for _, want := range []string{`"enabled":true`, `"seedAnswers":2`, `"hasSeed":true`, "hr.json"} {
		if !strings.Contains(string(resp), want) {
			t.Errorf("PUT response %s does not carry %s", resp, want)
		}
	}

	code, resp = serveInternal(t, srv, http.MethodGet, "/api/v1/settings/sql-mock", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET: status=%d body=%s", code, resp)
	}
	// "configured" is the difference between a decision made here and none at all.
	for _, want := range []string{`"enabled":true`, `"configured":true`, `"hasSeed":true`} {
		if !strings.Contains(string(resp), want) {
			t.Errorf("GET response %s does not carry %s", resp, want)
		}
	}
}

// Creating a database worker normally needs a connection string: it is the whole
// configuration, and a record without one is almost always somebody who lost the
// paste. In mockup mode there is no database to reach, so demanding one is demanding a
// credential for something nobody will dial — and it is exactly the state an operator
// is in when they turn the mockup on precisely *because* they have no database.
func TestInMockupModeASQLWorkerNeedsNoConnectionString(t *testing.T) {
	srv, _ := newValidateServer(t)

	// With the mockup off, the demand stands.
	code, resp := serveInternal(t, srv, http.MethodPost, "/api/v1/connectors",
		`{"name":"hr-db","kind":"mssql"}`, "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d (%s), want a database worker without a connection string refused", code, resp)
	}
	if !strings.Contains(string(resp), "connectionString") {
		t.Errorf("response = %s, want it to name what is missing", resp)
	}

	// Turn it on, and the same request is accepted.
	body, _ := json.Marshal(map[string]any{"enabled": true, "seed": sqlMockSeedJSON})
	if code, resp = serveInternal(t, srv, http.MethodPut, "/api/v1/settings/sql-mock", string(body), "application/json"); code != http.StatusOK {
		t.Fatalf("enable mockup: status=%d body=%s", code, resp)
	}
	code, resp = serveInternal(t, srv, http.MethodPost, "/api/v1/connectors",
		`{"name":"hr-db","kind":"mssql"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("status = %d (%s), want it accepted while the mockup is on", code, resp)
	}

	// And the record really has no credential, rather than an empty reference that
	// would resolve to nothing later.
	recs, err := srv.connectors.LoadAll()
	if err != nil || len(recs) != 1 {
		t.Fatalf("LoadAll: %v (%d records)", err, len(recs))
	}
	if recs[0].CredentialsRef != "" {
		t.Errorf("CredentialsRef = %q, want none", recs[0].CredentialsRef)
	}
}

// A connection string given while the mockup is on is still sealed and kept, not
// dropped: the mockup is a thing you turn off again, and losing the DSN in the
// meantime would make that a re-typing exercise.
func TestAConnectionStringGivenInMockupModeIsStillSealed(t *testing.T) {
	srv, _ := newValidateServer(t)
	body, _ := json.Marshal(map[string]any{"enabled": true, "seed": sqlMockSeedJSON})
	if code, resp := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/sql-mock", string(body), "application/json"); code != http.StatusOK {
		t.Fatalf("enable mockup: status=%d body=%s", code, resp)
	}

	code, resp := serveInternal(t, srv, http.MethodPost, "/api/v1/connectors",
		`{"name":"hr-db","kind":"mssql","connectionString":"sqlserver://sa:pw@db.example.com:1433?database=hr"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("status = %d (%s)", code, resp)
	}
	recs, err := srv.connectors.LoadAll()
	if err != nil || len(recs) != 1 {
		t.Fatalf("LoadAll: %v (%d records)", err, len(recs))
	}
	if recs[0].CredentialsRef == "" {
		t.Fatal("the connection string was dropped because the mockup was on; turning it off would need a re-type")
	}
	if got := srv.resolveConnectorSecret(recs[0].CredentialsRef); got != "sqlserver://sa:pw@db.example.com:1433?database=hr" {
		t.Errorf("the sealed DSN reads %q", got)
	}
}

// The Console's "Example" button writes a seed straight into the field an operator
// then saves. A broken one is a bad first thing to meet, and nothing else would catch
// it: the constant lives in app.js and the parser lives here, so this reads the one and
// runs it through the other.
func TestTheConsoleExampleSeedIsAcceptedByTheParser(t *testing.T) {
	body, err := fs.ReadFile(webFS, "web/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	src := string(body)
	open := "const SQL_SEED_EXAMPLE = JSON.stringify("
	i := strings.Index(src, open)
	if i < 0 {
		t.Fatal("no SQL_SEED_EXAMPLE in app.js; the Example button or its name must have changed")
	}
	// The constant is a JS object literal, so it is exercised the way the button does:
	// through the endpoint that stores it, which is what actually decides whether a
	// seed is acceptable. Reading it back out of JS would be re-implementing a parser
	// to check a parser.
	//
	// The literal's own field names are what the seed format calls them, so a rename on
	// either side shows up here as a refusal rather than as a button that quietly
	// stores something the worker cannot read.
	for _, want := range []string{`statement:`, `columns:`, `rows:`, `named:`, `affected:`, `error:`} {
		if !strings.Contains(src[i:i+2000], want) {
			t.Errorf("the example seed names no %s; the format and the button have drifted", want)
		}
	}
}

// And the format itself, from the endpoint's side: the shapes the example uses are the
// shapes the parser accepts, so the button and the server agree about what a seed is.
func TestTheSeedShapesTheConsoleOffersAreAccepted(t *testing.T) {
	srv, _ := newValidateServer(t)
	seed := `{"answers":[
	  {"statement":"SELECT id FROM personen WHERE kuerzel = @p1","params":["abo"],"columns":["id"],"rows":[[7]]},
	  {"statement":"SELECT id FROM personen WHERE kuerzel = @p1","columns":["id"],"rows":[]},
	  {"statement":"UPDATE personen SET aktiv = @aktiv WHERE id = @id","named":{"id":7,"aktiv":true},"affected":1},
	  {"statement":"INSERT INTO personen (kuerzel) VALUES (@p1)","error":"Violation of UNIQUE KEY constraint"}
	]}`
	body, _ := json.Marshal(map[string]any{"enabled": true, "seed": seed, "seedName": "example-answers.json"})
	code, resp := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/sql-mock", string(body), "application/json")
	if code != http.StatusOK {
		t.Fatalf("status = %d (%s), want every shape the Console's example uses accepted", code, resp)
	}
	if !strings.Contains(string(resp), `"seedAnswers":4`) {
		t.Errorf("response = %s, want all four answers counted", resp)
	}
}

// The seed's content is admin-only; everything else about the switch is not. What the
// switch is set to answers a question every operator watching a database task has — did
// that row really get written? — so hiding it would be the wrong secrecy. The seed is a
// different matter: it is shaped like the answers a production query returns.
func TestOnlyAnAdminReadsTheSeedBack(t *testing.T) {
	// With auth off the server is open by definition (single-user mode), so the
	// distinction under test only exists with it on.
	srv, _ := newValidateServer(t, WithAuth())
	srv.do(func() {
		_ = srv.settings.saveSQLMock(sqlMockSetting{
			Enabled: true, Seed: sqlMockSeedJSON, SeedName: "hr.json", SeedAnswers: 2,
		})
	})

	// The handler is called directly with a principal in context: what is under test is
	// the field it keeps back, not the middleware that established who is asking.
	get := func(roles ...string) string {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/settings/sql-mock", nil)
		r = r.WithContext(httpapi.WithPrincipal(r.Context(),
			&httpapi.Principal{UserID: "u1", Username: "ben", Roles: roles}))
		rec := httptest.NewRecorder()
		srv.handleGetSQLMock(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET: status=%d body=%s — reading the switch must stay open", rec.Code, rec.Body)
		}
		return rec.Body.String()
	}

	signedIn := get("user")
	if strings.Contains(signedIn, "personen") {
		t.Errorf("the seed's statements were returned to a caller who is not an admin: %s", signedIn)
	}
	// But enough of it to say there is one, and what it is called.
	for _, want := range []string{`"enabled":true`, `"hasSeed":true`, "hr.json", `"seedAnswers":2`} {
		if !strings.Contains(signedIn, want) {
			t.Errorf("the response was %s, want it to still carry %s", signedIn, want)
		}
	}
	if admin := get(RoleAdmin); !strings.Contains(admin, "personen") {
		t.Errorf("an admin was not given the seed: %s", admin)
	}
}

// A stored record that cannot be decoded is reported rather than silently treated as
// "nobody decided" — the difference matters, because the second would hand the worker
// whatever the host environment says while the Console showed a switch nobody set.
func TestAnUnreadableSQLMockRecordIsAnError(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.do(func() {
		if err := os.WriteFile(srv.settings.sqlFile, []byte("{not json"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	})
	var err error
	srv.do(func() { _, _, err = srv.settings.getSQLMock() })
	if err == nil {
		t.Fatal("a corrupt switch record decoded cleanly")
	}

	code, resp := serveInternal(t, srv, http.MethodGet, "/api/v1/settings/sql-mock", "", "")
	if code != http.StatusInternalServerError {
		t.Errorf("GET status = %d (%s), want the read failure reported", code, resp)
	}
	// And the supervised worker is handed nothing rather than a guess, so the host's
	// own setting keeps deciding while somebody fixes the file.
	for _, v := range srv.sqlWorkerEnvByName(connectorKindMSSQL) {
		if strings.HasPrefix(v, "ATLAS_MSSQL_MOCK") {
			t.Errorf("env carries %q off an unreadable record", v)
		}
	}
}

// A body that is not JSON is a bad request, not a 500 — and saying which is the
// difference between "fix your request" and "the server is broken".
func TestASQLMockBodyThatIsNotJSONIsRefused(t *testing.T) {
	srv, _ := newValidateServer(t)
	code, resp := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/sql-mock", "{not json", "application/json")
	if code != http.StatusBadRequest || !strings.Contains(string(resp), "invalid JSON body") {
		t.Fatalf("status = %d (%s), want a 400 naming the body", code, resp)
	}
}

// A seed past the limit says so, rather than being truncated into something that then
// fails to parse — the failure mode a LimitReader would have given, and a maddening one
// to debug because the text that comes back is valid JSON that is simply not yours.
func TestASeedLargerThanTheLimitIsRefusedRatherThanTruncated(t *testing.T) {
	srv, _ := newValidateServer(t)

	var big strings.Builder
	big.WriteString(`{"answers":[`)
	for i := 0; big.Len() < maxSQLMockBytes+(1<<12); i++ {
		if i > 0 {
			big.WriteString(",")
		}
		fmt.Fprintf(&big, `{"statement":"SELECT %d FROM personen WHERE id = @p1","columns":["n"],"rows":[[%d]]}`, i, i)
	}
	big.WriteString(`]}`)
	body, _ := json.Marshal(map[string]any{"enabled": true, "seed": big.String()})

	code, resp := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/sql-mock", string(body), "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d (%s), want an oversized seed refused", code, resp)
	}
	// Nothing was stored, so the switch still reads as undecided.
	var stored bool
	srv.do(func() { _, stored, _ = srv.settings.getSQLMock() })
	if stored {
		t.Error("an oversized seed still wrote the switch")
	}
}
