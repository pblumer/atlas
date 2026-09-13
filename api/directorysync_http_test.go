package api_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/limits"
)

// The directory-synchronisation routes, over HTTP
// (ADR-draft-entra-directory-provisioning).
//
// Three of these are the reason the slice was cut this way: a first run must write
// nothing, it must leave the cursor exactly where it was, and what it reports must be
// what a run that wrote would have done. The rest are the boundary.

// tenant is a change set big enough to be worth being careful about: two people, one
// group, and a membership.
func tenant() map[string]any {
	return map[string]any{
		"users": []map[string]any{
			{"id": "oid-ada", "userPrincipalName": "ada@example.org", "displayName": "Ada Lovelace",
				"mail": "ada@example.org", "accountEnabled": true},
			{"id": "oid-bob", "userPrincipalName": "bob@example.org", "displayName": "Bob",
				"mail": "bob@example.org", "accountEnabled": true},
		},
		"usersDeltaLink": "https://graph.microsoft.com/v1.0/users/delta?$deltatoken=U1",
		"groups": []map[string]any{
			{"id": "oid-team", "displayName": "Team", "members@delta": []map[string]any{
				{"id": "oid-ada", "@odata.type": "#microsoft.graph.user"},
			}},
		},
		"groupsDeltaLink": "https://graph.microsoft.com/v1.0/groups/delta?$deltatoken=G1",
	}
}

func syncBody(t *testing.T, apply bool, fromRevision int, extra map[string]any) string {
	t.Helper()
	msg := tenant()
	msg["apply"] = apply
	msg["fromRevision"] = fromRevision
	for k, v := range extra {
		msg[k] = v
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// syncState reads the state route.
func syncState(t *testing.T, c *http.Client, ts *httptest.Server) map[string]any {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/directory-sync", "")
	if code != http.StatusOK {
		t.Fatalf("GET state: status=%d body=%s", code, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode state: %v (%s)", err, body)
	}
	return out
}

// postSync reports a change set and returns the report.
func postSync(t *testing.T, c *http.Client, ts *httptest.Server, body string) map[string]any {
	t.Helper()
	code, raw := cReq(t, c, ts, "POST", "/api/v1/directory-sync", body)
	if code != http.StatusOK {
		t.Fatalf("POST sync: status=%d body=%s", code, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode report: %v (%s)", err, raw)
	}
	return out
}

// identityFingerprint hashes everything the mirror could possibly write: the accounts,
// the groups, and its own state. A reporting run has to leave every byte of it alone,
// and asking the filesystem is the only way to be sure nothing was written that the
// API happens not to show.
func identityFingerprint(t *testing.T, dir string) string {
	t.Helper()
	sum := sha256.New()
	var paths []string
	for _, sub := range []string{"users", "groups", "directory-sync"} {
		root := filepath.Join(dir, sub)
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			switch {
			case os.IsNotExist(err):
				return nil
			case err != nil:
				return err
			case info.IsDir():
				return nil
			}
			paths = append(paths, p)
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		fmt.Fprintf(sum, "%s:%s\n", filepath.Base(p), hex.EncodeToString(data))
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// TestAReportingRunWritesNothingAtAll.
//
// The first run enumerates a whole tenant against an empty user store, which is the
// run where a defect in the rules reaches everybody at once rather than one person.
// So it writes nothing until somebody has read what it would do — and "nothing" is
// checked against the disk, not against the answer.
func TestAReportingRunWritesNothingAtAll(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	if code := login(t, c, ts, "root", "correct horse battery"); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}

	before := identityFingerprint(t, dir)
	rep := postSync(t, c, ts, syncBody(t, false, 0, nil))

	if rep["applied"] != false || rep["mode"] != "report-only" {
		t.Errorf("report says applied=%v mode=%v", rep["applied"], rep["mode"])
	}
	if reason, _ := rep["reason"].(string); !strings.Contains(reason, "asked for a report") {
		t.Errorf("reason = %q, want it to say nothing was written and why", reason)
	}
	counts, _ := rep["counts"].(map[string]any)
	if counts["usersCreated"] != float64(2) || counts["groupsCreated"] != float64(1) {
		t.Errorf("counts = %+v, want the run to have decided two accounts and a group", counts)
	}
	if after := identityFingerprint(t, dir); after != before {
		t.Error("a reporting run changed something on disk; it must decide everything and write nothing")
	}
}

// TestAReportingRunDoesNotMoveTheCursor is the single most dangerous mistake this
// slice could make, so it is checked by doing the thing that would expose it: report
// first, then apply the same batch, and require the real run to still see the whole
// tenant. A cursor that moved during the report would leave the second run reading an
// empty change set, the store empty, and nothing anywhere saying so.
func TestAReportingRunDoesNotMoveTheCursor(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	start := syncState(t, c, ts)
	if start["usersDeltaLink"] != "" || start["groupsDeltaLink"] != "" || start["revision"] != float64(0) {
		t.Fatalf("a fresh installation starts at %+v, want no cursor and revision 0", start)
	}
	if start["everApplied"] != false {
		t.Error("everApplied must be false before anything was ever written")
	}

	postSync(t, c, ts, syncBody(t, false, 0, nil))

	mid := syncState(t, c, ts)
	if mid["usersDeltaLink"] != "" || mid["groupsDeltaLink"] != "" || mid["revision"] != float64(0) {
		t.Fatalf("the reporting run moved the cursor to %+v; the next real run would read nothing "+
			"and the mirror would stay empty with nothing reporting it", mid)
	}

	// The same batch, for real. It must still carry the whole tenant into the store.
	rep := postSync(t, c, ts, syncBody(t, true, 0, nil))
	if rep["applied"] != true {
		t.Fatalf("the real run did not write: %+v", rep)
	}
	end := syncState(t, c, ts)
	if end["revision"] != float64(1) || end["everApplied"] != true {
		t.Errorf("state after the real run = %+v", end)
	}
	if !strings.Contains(end["usersDeltaLink"].(string), "U1") ||
		!strings.Contains(end["groupsDeltaLink"].(string), "G1") {
		t.Errorf("cursors after the real run = %+v, want both stored", end)
	}

	code, body := cReq(t, c, ts, "GET", "/api/v1/users", "")
	if code != http.StatusOK {
		t.Fatalf("list users: %d", code)
	}
	for _, want := range []string{"ada", "bob"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the account %q is missing after the real run: %s", want, body)
		}
	}
}

// TestAReportPredictsWhatTheRealRunDoes holds the two modes against one another end
// to end, over HTTP, on two installations in the same starting state. The decision
// function is shared by construction; this is what would catch somebody adding a
// second opinion above it.
func TestAReportPredictsWhatTheRealRunDoes(t *testing.T) {
	body := syncBody(t, false, 0, nil)
	reporting, _ := newAuthServer(t, "root", "correct horse battery")
	rc := newClient(t)
	login(t, rc, reporting, "root", "correct horse battery")
	predicted := postSync(t, rc, reporting, body)

	applying, _ := newAuthServer(t, "root", "correct horse battery")
	ac := newClient(t)
	login(t, ac, applying, "root", "correct horse battery")
	actual := postSync(t, ac, applying, syncBody(t, true, 0, nil))

	if !sameJSON(predicted["counts"], actual["counts"]) {
		t.Errorf("the report predicted %v and the real run did %v", predicted["counts"], actual["counts"])
	}
	if !sameJSON(predicted["notes"], actual["notes"]) {
		t.Errorf("the report's lines were %v and the real run's were %v", predicted["notes"], actual["notes"])
	}
	// And the two things that are supposed to differ, do.
	if predicted["applied"] != false || actual["applied"] != true {
		t.Errorf("applied = %v / %v", predicted["applied"], actual["applied"])
	}
}

// TestASecondDeliveryOfTheSameBatchWritesNothing. The job protocol delivers at least
// once; without the revision the message is pinned to, the repeat would advance the
// cursor a second time and the changes between the two positions would be gone.
func TestASecondDeliveryOfTheSameBatchWritesNothing(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	if rep := postSync(t, c, ts, syncBody(t, true, 0, nil)); rep["applied"] != true {
		t.Fatalf("the first delivery did not write: %+v", rep)
	}
	settled := identityFingerprint(t, dir)

	again := postSync(t, c, ts, syncBody(t, true, 0, nil))
	if again["applied"] != false {
		t.Error("the repeat delivery wrote again")
	}
	if reason, _ := again["reason"].(string); !strings.Contains(reason, "repeat") {
		t.Errorf("reason = %q, want it to name the repeat rather than read as a reporting run", reason)
	}
	if again["mode"] != "applied" {
		t.Errorf("mode = %v, want the mode the message asked for", again["mode"])
	}
	if identityFingerprint(t, dir) != settled {
		t.Error("the repeat delivery changed something")
	}
}

// TestTheRoutesRefuseWhenAuthenticationIsOff. Every other route is reachable in an
// open installation; this pair is not. An unauthenticated caller able to create an
// account would be able to sign in as it the moment authentication was turned on.
func TestTheRoutesRefuseWhenAuthenticationIsOff(t *testing.T) {
	ts := newTestServer(t)
	c := newClient(t)
	for _, call := range []struct{ method, body string }{
		{"GET", ""},
		{"POST", syncBody(t, true, 0, nil)},
	} {
		code, body := cReq(t, c, ts, call.method, "/api/v1/directory-sync", call.body)
		if code != http.StatusForbidden {
			t.Errorf("%s with authentication off: status=%d, want 403", call.method, code)
		}
		if !strings.Contains(string(body), "authentication") {
			t.Errorf("%s refusal = %s, want it to name the reason", call.method, body)
		}
	}
}

// TestADirectoryTokenReachesItsTwoRoutesAndNothingElse. The credential a scheduled
// run carries is the one that creates accounts, so what else it could do has to have
// a short answer — and "it cannot deploy" has to be part of it.
func TestADirectoryTokenReachesItsTwoRoutesAndNothingElse(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	code, body := cReq(t, c, ts, "POST", "/api/v1/api-tokens",
		`{"name":"entra sync","scope":"directory"}`)
	if code != http.StatusOK {
		t.Fatalf("mint: status=%d body=%s", code, body)
	}
	var minted struct {
		Token string   `json:"token"`
		Roles []string `json:"roles"`
		Scope string   `json:"scope"`
	}
	if err := json.Unmarshal(body, &minted); err != nil {
		t.Fatalf("decode mint: %v (%s)", err, body)
	}
	if minted.Scope != "directory" {
		t.Errorf("scope = %q", minted.Scope)
	}
	for _, r := range minted.Roles {
		if r == "admin" {
			t.Fatal("a directory token was minted holding admin; a machine credential never is")
		}
	}

	reaches := func(method, path, payload string) int {
		t.Helper()
		var r io.Reader
		if payload != "" {
			r = strings.NewReader(payload)
		}
		req, err := http.NewRequest(method, ts.URL+path, r)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+minted.Token)
		if payload != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer res.Body.Close()
		_, _ = io.ReadAll(res.Body)
		return res.StatusCode
	}

	if got := reaches("GET", "/api/v1/directory-sync", ""); got != http.StatusOK {
		t.Errorf("the token cannot read the state it needs: %d", got)
	}
	if got := reaches("POST", "/api/v1/directory-sync", syncBody(t, false, 0, nil)); got != http.StatusOK {
		t.Errorf("the token cannot report a change set: %d", got)
	}
	// And the reach beyond that, which is the point of the scope.
	for _, call := range []struct{ method, path, body string }{
		{"POST", "/api/v1/deployments", `{}`},
		{"GET", "/api/v1/users", ""},
		{"POST", "/api/v1/users", `{"username":"x","password":"12345678"}`},
		{"GET", "/api/v1/logs", ""},
		{"GET", "/api/v1/processes", ""},
	} {
		if got := reaches(call.method, call.path, call.body); got != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403: a provisioning credential reaches its two routes and no others",
				call.method, call.path, got)
		}
	}
}

// TestABatchAboveTheBudgetIsRefusedWhole. Truncating would be worse than refusing: the
// cursor would move past what was dropped and record a short change set as a complete
// one.
func TestABatchAboveTheBudgetIsRefusedWhole(t *testing.T) {
	budgets := limits.Default()
	budgets.DirectoryObjects = 3
	ts, dir := newAuthServerWith(t, "root", "correct horse battery", api.WithLimits(budgets))
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	before := identityFingerprint(t, dir)
	code, body := cReq(t, c, ts, "POST", "/api/v1/directory-sync", syncBody(t, true, 0, map[string]any{
		"users": []map[string]any{
			{"id": "o1", "userPrincipalName": "a@x.test"}, {"id": "o2", "userPrincipalName": "b@x.test"},
			{"id": "o3", "userPrincipalName": "c@x.test"}, {"id": "o4", "userPrincipalName": "d@x.test"},
		},
	}))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", code, body)
	}
	if !strings.Contains(string(body), "refused whole") {
		t.Errorf("refusal = %s, want it to say why it is not truncated", body)
	}
	if identityFingerprint(t, dir) != before {
		t.Error("a refused batch wrote something")
	}
}

// TestAMalformedMessageIsRefusedRatherThanPartlyRead.
func TestAMalformedMessageIsRefusedRatherThanPartlyRead(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	before := identityFingerprint(t, dir)
	code, _ := cReq(t, c, ts, "POST", "/api/v1/directory-sync", `{"apply":true,"users":`)
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
	if identityFingerprint(t, dir) != before {
		t.Error("a malformed message wrote something")
	}
}

// sameJSON compares two decoded JSON values.
func sameJSON(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

// TestAnAppliedRunThatChangesNothingStillMovesTheCursor. A quiet hour is the ordinary
// case once a mirror is running, and it is the case that must not stall: a run that
// refused to advance because nothing changed would re-read the same empty change set
// forever, and a later real change would arrive against a cursor that had gone stale.
func TestAnAppliedRunThatChangesNothingStillMovesTheCursor(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "correct horse battery")
	c := newClient(t)
	login(t, c, ts, "root", "correct horse battery")

	if rep := postSync(t, c, ts, syncBody(t, true, 0, nil)); rep["applied"] != true {
		t.Fatalf("the first run did not write: %+v", rep)
	}
	settled := identityFingerprint(t, dir)

	// The same tenant again, pinned to the revision the first run produced: nothing has
	// changed in the directory, so nothing should change here.
	rep := postSync(t, c, ts, syncBody(t, true, 1, nil))
	if rep["applied"] != true {
		t.Fatalf("the second run was refused: %+v", rep)
	}
	counts, _ := rep["counts"].(map[string]any)
	if counts["usersUnchanged"] != float64(2) || counts["groupsUnchanged"] != float64(1) {
		t.Errorf("counts = %+v, want everything recognised as unchanged", counts)
	}
	if counts["usersUpdated"] != float64(0) || counts["groupsUpdated"] != float64(0) {
		t.Errorf("counts = %+v, want no record rewritten", counts)
	}
	if identityFingerprint(t, dir) == settled {
		t.Error("the state record was not written, so the cursor never advances on a quiet hour")
	}
	if end := syncState(t, c, ts); end["revision"] != float64(2) {
		t.Errorf("revision = %v, want 2", end["revision"])
	}
}
