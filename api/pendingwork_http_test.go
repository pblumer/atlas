package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// What is waiting for one person, over HTTP (ADR-0343).
//
// The route exists because every other route that answers this answers only for
// the caller, and a reminder process is not the person it is reminding. So the
// tests that matter are about the second mode: who may use it, and whether what it
// returns is something the person can actually act on.

func readPending(t *testing.T, c *http.Client, ts *httptest.Server, query string) map[string]any {
	t.Helper()
	code, raw := cReq(t, c, ts, "GET", "/api/v1/pending-work"+query, "")
	if code != http.StatusOK {
		t.Fatalf("GET pending-work%s: %d %s", query, code, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	return out
}

func pendingItems(t *testing.T, rep map[string]any) []map[string]any {
	t.Helper()
	raw, _ := rep["items"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("an item is not an object: %#v", r)
		}
		out = append(out, m)
	}
	return out
}

// TestAskingAboutSomebodyElseIsTheOperators.
//
// The whole record in one test. Everybody may ask what is waiting for them; asking
// about another person is the capability a reminder needs and the one a person must
// not have.
func TestAskingAboutSomebodyElseIsTheOperators(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	admin := newClient(t)
	if code := login(t, admin, ts, "root", "correct horse battery"); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	ada := anAccountWithMail(t, admin, ts, "ada", "ada@example.org")
	anAccountWithMail(t, admin, ts, "bo", "bo@example.org")

	// An ordinary user may ask about themselves.
	adaC := newClient(t)
	login(t, adaC, ts, "ada", "correct horse battery")
	rep := readPending(t, adaC, ts, "")
	if rep["principal"] != ada {
		t.Errorf("principal = %v, want the caller — an answer that named somebody else "+
			"would be a mail addressed to the wrong person", rep["principal"])
	}

	// And not about anybody else.
	code, body := cReq(t, adaC, ts, "GET", "/api/v1/pending-work?principal=bo", "")
	if code != http.StatusForbidden {
		t.Errorf("a user enumerated another user's work: %d %s, want 403", code, body)
	}
	if !strings.Contains(string(body), "operator") {
		t.Errorf("the refusal does not say whose it is: %s", body)
	}

	// The operator may, which is what makes a reminder possible at all.
	rep = readPending(t, admin, ts, "?principal=bo")
	if rep["principal"] == nil || rep["principal"] == "" {
		t.Error("the operator's answer does not say who it is about")
	}
}

// TestSomebodyCanBeNamedHoweverTheCallerKnowsThem.
//
// A reminder process resolves managers out of a directory and holds object ids or
// mail addresses; a person writing a curl holds a username. Forcing either to learn
// Atlas's principal ids to ask a question about somebody is how a reminder ends up
// addressed to nobody.
func TestSomebodyCanBeNamedHoweverTheCallerKnowsThem(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	admin := newClient(t)
	login(t, admin, ts, "root", "correct horse battery")
	ada := anAccountWithMail(t, admin, ts, "ada", "ada@example.org")

	for _, named := range []string{ada, "ada", "ada@example.org"} {
		rep := readPending(t, admin, ts, "?principal="+named)
		if rep["principal"] != ada {
			t.Errorf("naming Ada as %q answered about %v", named, rep["principal"])
		}
	}

	code, body := cReq(t, admin, ts, "GET", "/api/v1/pending-work?principal=nobody", "")
	if code != http.StatusNotFound {
		t.Errorf("asking about somebody who does not exist: %d %s, want 404", code, body)
	}
	if !strings.Contains(string(body), "username") {
		t.Errorf("the refusal does not say how to name somebody: %s", body)
	}
}

// TestOnlyWhatThePersonCanActOnNowIsListed.
//
// The one hard problem, made concrete. A row somebody already answered, and a row
// in a campaign that has closed, are both perfectly formattable into a message
// about nothing — and one wrong reminder costs more than ten right ones earn.
func TestOnlyWhatThePersonCanActOnNowIsListed(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	admin := newClient(t)
	login(t, admin, ts, "root", "correct horse battery")
	_, bo := aCertifiableEstate(t, admin, ts)

	opened := openCampaign(t, admin, ts, fmt.Sprintf(
		`{"name":"Q3 access review","reviewers":{"ada@example.org":%q}}`, bo))
	id := fmt.Sprint(opened["id"])
	row := fmt.Sprint(rowsOf(t, opened)[0]["id"])

	// Bo owes one review.
	rep := readPending(t, admin, ts, "?principal=bo")
	counts, _ := rep["counts"].(map[string]any)
	if counts["recertifications"] != float64(1) || counts["total"] != float64(1) {
		t.Fatalf("counts = %+v, want the one row Bo was asked about", counts)
	}
	item := pendingItems(t, rep)[0]
	if item["kind"] != "recertification" {
		t.Errorf("kind = %v", item["kind"])
	}
	if !strings.Contains(fmt.Sprint(item["what"]), "Q3 access review") {
		t.Errorf("what = %q; a line that does not name the campaign sends the reader "+
			"looking for it", item["what"])
	}
	if item["link"] == nil || strings.HasPrefix(fmt.Sprint(item["link"]), "http") {
		t.Errorf("link = %v, want a relative path — deciding the origin here would put "+
			"whichever host this server was reached on into somebody's mail", item["link"])
	}

	// Answered: it stops waiting.
	boC := newClient(t)
	login(t, boC, ts, "bo", "correct horse battery")
	if code, b := cReq(t, boC, ts,
		"POST", "/api/v1/recertification/"+id+"/rows/"+row+"/keep", ""); code != http.StatusOK {
		t.Fatalf("answer the row: %d %s", code, b)
	}
	rep = readPending(t, admin, ts, "?principal=bo")
	if counts, _ := rep["counts"].(map[string]any); counts["total"] != float64(0) {
		t.Errorf("counts = %+v after Bo answered; a reminder about work somebody has done "+
			"is how the next one gets deleted unread", counts)
	}
}

// TestAClosedCampaignIsWaitingForNobody.
func TestAClosedCampaignIsWaitingForNobody(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	admin := newClient(t)
	login(t, admin, ts, "root", "correct horse battery")
	_, bo := aCertifiableEstate(t, admin, ts)

	opened := openCampaign(t, admin, ts, fmt.Sprintf(
		`{"name":"Q3 access review","reviewers":{"ada@example.org":%q}}`, bo))
	id := fmt.Sprint(opened["id"])

	if counts, _ := readPending(t, admin, ts, "?principal=bo")["counts"].(map[string]any); counts["total"] != float64(1) {
		t.Fatalf("counts = %+v before closing, want the one row", counts)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/recertification/"+id+"/close", ""); code != http.StatusOK {
		t.Fatalf("close: %d %s", code, b)
	}

	counts, _ := readPending(t, admin, ts, "?principal=bo")["counts"].(map[string]any)
	if counts["total"] != float64(0) {
		t.Errorf("counts = %+v after the campaign closed. The row is still undecided and "+
			"stays that way — but nobody can answer it now, so reminding them is an "+
			"interruption with no action behind it", counts)
	}
}

// TestAnUnassignedRowIsTheCampaignOwnersToBeRemindedAbout.
//
// The same rule the decision route applies, minus the operator shortcut: an
// operator may answer any row, and reminding them of every row in the estate is how
// a reminder becomes something nobody reads.
func TestAnUnassignedRowIsTheCampaignOwnersToBeRemindedAbout(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "correct horse battery")
	admin := newClient(t)
	login(t, admin, ts, "root", "correct horse battery")
	aCertifiableEstate(t, admin, ts)

	openCampaign(t, admin, ts, `{"name":"Q3 access review"}`)

	// Nobody was named, so it is the opener's.
	counts, _ := readPending(t, admin, ts, "?principal=root")["counts"].(map[string]any)
	if counts["recertifications"] != float64(1) {
		t.Errorf("counts = %+v; a row nobody was asked about still has to reach somebody, "+
			"and that is whoever opened the campaign", counts)
	}

	// And not somebody else's, however senior.
	anAccountWithMail(t, admin, ts, "cy", "cy@example.org")
	counts, _ = readPending(t, admin, ts, "?principal=cy")["counts"].(map[string]any)
	if counts["total"] != float64(0) {
		t.Errorf("counts = %+v for somebody the campaign never addressed", counts)
	}
}
