package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/jira"
)

// fakeJiraClient answers a search with a canned page and records what it was asked.
type fakeJiraClient struct {
	asked []jira.Request
	pages [][]any
	err   error
}

func (f *fakeJiraClient) Do(_ context.Context, req jira.Request) (any, error) {
	f.asked = append(f.asked, req)
	if f.err != nil {
		return nil, f.err
	}
	if len(f.pages) == 0 {
		return []any{}, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

// jiraIssue builds the shape the connector's search returns.
func jiraIssue(id, key, created, updated string) map[string]any {
	var issue map[string]any
	_ = json.Unmarshal([]byte(`{
		"id": "`+id+`", "key": "`+key+`",
		"fields": {
			"summary": "Zugang für `+key+`",
			"created": "`+created+`", "updated": "`+updated+`",
			"project":   {"key": "OPS"},
			"issuetype": {"name": "Task"},
			"status":    {"name": "To Do"},
			"reporter":  {"displayName": "Pat"}
		}}`), &issue)
	return issue
}

const (
	jiraT1 = "2026-09-01T10:00:00.000+0000"
	jiraT2 = "2026-09-01T10:05:00.000+0000"
)

// A jira watch asks the query the operator wrote, narrowed by its resume cursor and
// ordered by its own cursor field — the ordering is the bridge's, because the cursor's
// progress depends on the last issue of a page being the newest one.
func TestJiraSourceAsksTheWatchsQueryOrderedByItsCursorField(t *testing.T) {
	f := &fakeJiraClient{pages: [][]any{{jiraIssue("10001", "OPS-1", jiraT1, jiraT1)}}}
	src := jiraSource{client: f, now: func() time.Time { return time.Unix(0, 0) }}

	_, _, err := src.Read(context.Background(), inboundSubscription{
		JQL: "project = OPS", LastEventID: "2026/09/01 09:58",
	}, 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got := f.asked[0]
	for _, want := range []string{"(project = OPS)", `created >= "2026/09/01 09:58"`, "ORDER BY created ASC"} {
		if !strings.Contains(got.JQL, want) {
			t.Errorf("jql = %q, want it to contain %q", got.JQL, want)
		}
	}
	if got.MaxResults != 25 {
		t.Errorf("maxResults = %d, want the bridge's batch cap", got.MaxResults)
	}
}

// The first read of a watch that was never primed has no cursor, so the query carries
// no restriction of its own beyond the operator's — a `created >=` clause with an empty
// value would match nothing and the watch would never start.
func TestJiraSourceOmitsTheCursorClauseWhenThereIsNoCursor(t *testing.T) {
	f := &fakeJiraClient{pages: [][]any{{}}}
	src := jiraSource{client: f, now: time.Now}
	if _, _, err := src.Read(context.Background(), inboundSubscription{JQL: "project = OPS"}, 10); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if strings.Contains(f.asked[0].JQL, "created >=") {
		t.Errorf("jql = %q, want no cursor clause on the first read", f.asked[0].JQL)
	}
}

// Each issue carries its own mark and its own sequence, taken from the watch's cursor
// field. Per issue is what makes the watch correct at all: two issues never share a
// mark, so no delivery order can make one suppress the other (ADR-0214).
func TestJiraSourceMarksEachIssueSeparately(t *testing.T) {
	f := &fakeJiraClient{pages: [][]any{{
		jiraIssue("10001", "OPS-1", jiraT1, jiraT2),
		jiraIssue("10002", "OPS-2", jiraT2, jiraT2),
	}}}
	src := jiraSource{client: f, now: time.Now}

	page, cursor, err := src.Read(context.Background(), inboundSubscription{JQL: "project = OPS"}, 10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("page = %d events, want one per issue", len(page))
	}
	if page[0].MarkKey != "10001" || page[1].MarkKey != "10002" {
		t.Errorf("mark keys = %q/%q, want each issue's own id", page[0].MarkKey, page[1].MarkKey)
	}
	// The sequence is the *created* time here, not updated: a watch for new issues
	// that sequenced on updated would let an edit inside the lag window pass the mark
	// and start a second instance for a ticket already handled.
	wantSeq := uint64(mustParseJira(t, jiraT1).UnixMilli())
	if page[0].Seq != wantSeq {
		t.Errorf("seq = %d, want the created timestamp %d", page[0].Seq, wantSeq)
	}
	// The cursor lags the newest issue deliberately, so one indexed late is still
	// inside the next window. Re-reading it costs one skipped publish.
	wantCursor := mustParseJira(t, jiraT2).Add(-jiraDefaultLag).UTC().Format(jiraCursorLayout)
	if cursor != wantCursor {
		t.Errorf("cursor = %q, want the newest created minus the lag (%q)", cursor, wantCursor)
	}
}

// A watch on changed issues is the same mechanism with the cursor field moved: it
// filters, orders and sequences on `updated` instead, with no second design.
func TestJiraSourceFollowsUpdatedWhenTheWatchSaysSo(t *testing.T) {
	f := &fakeJiraClient{pages: [][]any{{jiraIssue("10001", "OPS-1", jiraT1, jiraT2)}}}
	src := jiraSource{client: f, now: time.Now}

	page, _, err := src.Read(context.Background(), inboundSubscription{
		JQL: "project = OPS", CursorField: "updated", LastEventID: "2026/09/01 09:00",
	}, 10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(f.asked[0].JQL, `updated >= "2026/09/01 09:00"`) ||
		!strings.Contains(f.asked[0].JQL, "ORDER BY updated ASC") {
		t.Errorf("jql = %q, want it to filter and order on updated", f.asked[0].JQL)
	}
	if want := uint64(mustParseJira(t, jiraT2).UnixMilli()); page[0].Seq != want {
		t.Errorf("seq = %d, want the updated timestamp %d", page[0].Seq, want)
	}
	if page[0].Fields["eventType"] != "jira.issue.updated" {
		t.Errorf("eventType = %v, want jira.issue.updated", page[0].Fields["eventType"])
	}
}

// What reaches a process is a bounded envelope plus the whole issue as one value.
// Bounded on purpose: flattening a Jira issue would seed one variable per field, and a
// project with custom fields has dozens that no model reads.
func TestJiraSourceSeedsABoundedEnvelope(t *testing.T) {
	f := &fakeJiraClient{pages: [][]any{{jiraIssue("10001", "OPS-42", jiraT1, jiraT1)}}}
	src := jiraSource{client: f, now: time.Now}
	page, _, err := src.Read(context.Background(), inboundSubscription{JQL: "project = OPS"}, 10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	f0 := page[0].Fields
	for name, want := range map[string]any{
		"eventType": "jira.issue.created", "issueId": "10001", "issueKey": "OPS-42",
		"projectKey": "OPS", "issueType": "Task", "status": "To Do", "reporter": "Pat",
		"summary": "Zugang für OPS-42",
	} {
		if f0[name] != want {
			t.Errorf("field %q = %v, want %v", name, f0[name], want)
		}
	}
	// Everything the envelope does not name stays reachable through `issue`.
	if _, ok := f0["issue"].(map[string]any); !ok {
		t.Errorf("issue = %T, want the whole issue object", f0["issue"])
	}
}

// Priming a forward-only watch reads the newest issue and stops. It publishes nothing:
// pointing a watch at a project with 500 open issues must not start 500 instances.
func TestJiraSourcePrimesToTheNewestIssueWithoutPublishing(t *testing.T) {
	f := &fakeJiraClient{pages: [][]any{{jiraIssue("10009", "OPS-9", jiraT2, jiraT2)}}}
	src := jiraSource{client: f, now: time.Now}

	cursor, done, err := src.Prime(context.Background(), inboundSubscription{JQL: "project = OPS"})
	if err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if !done {
		t.Error("priming did not report done; there is no backlog to page through")
	}
	if want := mustParseJira(t, jiraT2).Add(-jiraDefaultLag).UTC().Format(jiraCursorLayout); cursor != want {
		t.Errorf("cursor = %q, want the newest issue minus the lag (%q)", cursor, want)
	}
	if !strings.Contains(f.asked[0].JQL, "ORDER BY created DESC") || f.asked[0].MaxResults != 1 {
		t.Errorf("prime asked %+v, want one issue newest-first", f.asked[0])
	}
}

// A watch pointed at a project with no issues at all primes to now rather than to the
// zero time, so its first real poll asks for a window starting here instead of at the
// beginning of Jira's history.
func TestJiraSourcePrimesAnEmptyProjectToNow(t *testing.T) {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	f := &fakeJiraClient{pages: [][]any{{}}}
	src := jiraSource{client: f, now: func() time.Time { return at }}

	cursor, done, err := src.Prime(context.Background(), inboundSubscription{JQL: "project = EMPTY"})
	if err != nil || !done {
		t.Fatalf("Prime: cursor=%q done=%v err=%v", cursor, done, err)
	}
	if want := at.Add(-jiraDefaultLag).Format(jiraCursorLayout); cursor != want {
		t.Errorf("cursor = %q, want now minus the lag (%q)", cursor, want)
	}
}

// A read that fails leaves the cursor alone and says so, so the watch retries the same
// window next tick rather than stepping over it.
func TestJiraSourceReportsAFailedRead(t *testing.T) {
	f := &fakeJiraClient{err: errJiraTest}
	src := jiraSource{client: f, now: time.Now}
	if _, cursor, err := src.Read(context.Background(), inboundSubscription{JQL: "project = OPS"}, 10); err == nil || cursor != "" {
		t.Errorf("Read = %q, %v; want no cursor and the error", cursor, err)
	}
	if _, _, err := src.Prime(context.Background(), inboundSubscription{JQL: "project = OPS"}); err == nil {
		t.Error("Prime swallowed a failed read")
	}
}

// An issue with no usable timestamp has no sequence, and an event with no sequence has
// no place in a mechanism whose correctness rests on one. It is skipped rather than
// delivered — and that is not tidiness: uint64 of the zero time's millis is a negative
// count wrapped to near the top of the range, which would set that issue's mark so high
// that nothing about it is ever delivered again. The cursor stays where it was too,
// because stepping it somewhere arbitrary would skip a window.
func TestJiraSourceSkipsAnIssueWithNoUsableTimestamp(t *testing.T) {
	var issue map[string]any
	_ = json.Unmarshal([]byte(`{"id":"1","key":"OPS-1","fields":{"summary":"no dates"}}`), &issue)
	f := &fakeJiraClient{pages: [][]any{{issue}}}
	src := jiraSource{client: f, now: time.Now}

	page, cursor, err := src.Read(context.Background(), inboundSubscription{JQL: "project = OPS"}, 10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if cursor != "" {
		t.Errorf("cursor = %q, want it left where it was", cursor)
	}
	if len(page) != 0 {
		t.Errorf("page = %+v, want the issue skipped rather than marked at a wrapped sequence", page)
	}
}

// Anything in the answer that is not an issue is skipped rather than delivered with an
// empty mark, which would silently share the watch's scalar mark with every other one.
func TestJiraSourceSkipsWhatIsNotAnIssue(t *testing.T) {
	f := &fakeJiraClient{pages: [][]any{{"not an object", map[string]any{"key": "OPS-1"}}}}
	src := jiraSource{client: f, now: time.Now}
	page, _, err := src.Read(context.Background(), inboundSubscription{JQL: "project = OPS"}, 10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(page) != 0 {
		t.Errorf("page = %+v, want nothing delivered without an issue id to mark it by", page)
	}
}

func mustParseJira(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse("2006-01-02T15:04:05.000-0700", s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return at
}

var errJiraTest = errJira("jira: search returned HTTP 500")

type errJira string

func (e errJira) Error() string { return string(e) }

// A full page means the read stopped at the bridge's cap and not at the end of the
// result set, so the cursor must land past the page rather than inside it. Held back by
// the safety lag it lands *inside* the page it just read, and the next poll asks Jira
// for the same issues again — a watch that re-reads and re-publishes one page every
// tick and never reaches the issue behind it. That is not a slow watch, it is a
// stopped one.
func TestJiraSourceLeavesAFullPageBehind(t *testing.T) {
	f := &fakeJiraClient{pages: [][]any{{
		jiraIssue("10001", "OPS-1", jiraT1, jiraT1),
		jiraIssue("10002", "OPS-2", jiraT2, jiraT2),
	}}}
	src := jiraSource{client: f, now: time.Now}

	// limit 2 against a two-issue answer: the page is full.
	_, cursor, err := src.Read(context.Background(), inboundSubscription{
		JQL: "project = OPS", LastEventID: "2026/09/01 09:58",
	}, 2)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if want := mustParseJira(t, jiraT2).UTC().Format(jiraCursorLayout); cursor != want {
		t.Errorf("cursor = %q, want the newest issue's own minute (%q): the lag protects the tip, "+
			"and behind a full page it only holds the cursor inside the page just read", cursor, want)
	}
}

// The residual case a minute-granular cursor cannot express: a full page whose issues
// all share the cursor field's minute, read from a cursor that is already that minute.
// No cursor separates them — JQL compares to the minute — so time paging cannot drain
// the page. The watch steps past the minute rather than re-reading it for ever: that
// costs the issues in that one minute beyond the cap, where standing still costs every
// issue after it.
func TestJiraSourceStepsPastAMinuteAFullPageCannotLeave(t *testing.T) {
	f := &fakeJiraClient{pages: [][]any{{
		jiraIssue("10001", "OPS-1", jiraT1, jiraT1),
		jiraIssue("10002", "OPS-2", jiraT1, jiraT1),
	}}}
	src := jiraSource{client: f, now: time.Now}
	at := mustParseJira(t, jiraT1)

	_, cursor, err := src.Read(context.Background(), inboundSubscription{
		JQL: "project = OPS", LastEventID: at.UTC().Format(jiraCursorLayout),
	}, 2)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if want := at.Add(time.Minute).UTC().Format(jiraCursorLayout); cursor != want {
		t.Errorf("cursor = %q, want the next minute (%q): a page this one cannot drain must not be re-read for ever", cursor, want)
	}
}

// A page that is not full is the tip, and the tip is what the lag is for: an issue the
// search index publishes late is still inside the next window, and its own mark makes
// the re-read free.
func TestJiraSourceKeepsTheLagAtTheTip(t *testing.T) {
	f := &fakeJiraClient{pages: [][]any{{jiraIssue("10001", "OPS-1", jiraT2, jiraT2)}}}
	src := jiraSource{client: f, now: time.Now}

	_, cursor, err := src.Read(context.Background(), inboundSubscription{JQL: "project = OPS"}, 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if want := mustParseJira(t, jiraT2).Add(-jiraDefaultLag).UTC().Format(jiraCursorLayout); cursor != want {
		t.Errorf("cursor = %q, want the newest issue minus the lag (%q)", cursor, want)
	}
}
