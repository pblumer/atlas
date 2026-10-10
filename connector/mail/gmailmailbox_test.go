package mail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// gmailFake is the slice of the Gmail API the mailbox reads and changes.
type gmailFake struct {
	srv      *httptest.Server
	mu       sync.Mutex
	profile  string
	history  map[string]any // answered for /history; nil → 404
	messages map[string]map[string]any
	list     []string
	reqs     []graphReq
}

func newGmailFake(t *testing.T) *gmailFake {
	t.Helper()
	g := &gmailFake{profile: "100", messages: map[string]map[string]any{}}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := graphReq{method: r.Method, path: r.URL.Path, auth: r.Header.Get("Authorization"), query: r.URL.Query()}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &req.body)
		}
		g.mu.Lock()
		g.reqs = append(g.reqs, req)
		status, body := g.answer(req)
		g.mu.Unlock()
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *gmailFake) answer(r graphReq) (int, any) {
	p := strings.TrimPrefix(r.path, "/users/me")
	switch {
	case p == "/profile":
		return 200, map[string]any{"historyId": g.profile}
	case p == "/history":
		if g.history == nil {
			return 404, map[string]any{"error": "history expired"}
		}
		return 200, g.history
	case p == "/messages" && r.method == "GET":
		var ms []any
		for _, id := range g.list {
			ms = append(ms, map[string]any{"id": id})
		}
		return 200, map[string]any{"messages": ms}
	case strings.HasSuffix(p, "/modify"), strings.HasSuffix(p, "/trash"), p == "/messages/send":
		return 200, map[string]any{"id": "x"}
	case strings.Contains(p, "/attachments/"):
		return 200, map[string]any{"data": base64.URLEncoding.EncodeToString([]byte("big body"))}
	case strings.HasPrefix(p, "/messages/"):
		if m, ok := g.messages[strings.TrimPrefix(p, "/messages/")]; ok {
			return 200, m
		}
		return 404, map[string]any{"error": "not found"}
	}
	return 400, map[string]any{"error": "unexpected " + r.path}
}

func (g *gmailFake) requests() []graphReq {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]graphReq(nil), g.reqs...)
}

func gmailMsg(id string, labels []string, payload map[string]any) map[string]any {
	return map[string]any{"id": id, "threadId": "t-" + id, "labelIds": labels, "internalDate": "1790922600000", "payload": payload}
}

func b64(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }

func gmailPayload() map[string]any {
	return map[string]any{
		"mimeType": "multipart/mixed",
		"headers": []any{
			map[string]any{"name": "From", "value": "Anna <Anna@Example.com>"},
			map[string]any{"name": "To", "value": "desk@example.com"},
			map[string]any{"name": "Subject", "value": "=?UTF-8?B?w6TDtsO8?="},
			map[string]any{"name": "Message-ID", "value": "<g1@x>"},
			map[string]any{"name": "Authentication-Results", "value": "mx.google.com; dkim=pass; spf=pass; dmarc=pass"},
			map[string]any{"name": "Authentication-Results", "value": "evil; dmarc=fail"},
		},
		"parts": []any{
			map[string]any{"partId": "0", "mimeType": "multipart/alternative", "parts": []any{
				map[string]any{"partId": "0.0", "mimeType": "text/plain",
					"headers": []any{map[string]any{"name": "Content-Type", "value": "text/plain; charset=ISO-8859-1"}},
					"body":    map[string]any{"size": 6, "data": base64.URLEncoding.EncodeToString([]byte("Gr\xfcezi"))}},
				map[string]any{"partId": "0.1", "mimeType": "text/html", "body": map[string]any{"size": 10, "data": b64("<p>x</p>")}},
			}},
			map[string]any{"partId": "1", "mimeType": "application/pdf", "filename": "R.pdf",
				"headers": []any{map[string]any{"name": "Content-Disposition", "value": "attachment; filename=\"R.pdf\""}},
				"body":    map[string]any{"size": 4000, "attachmentId": "ATT"}},
		},
	}
}

func gmailMailboxFor(t *testing.T, g *gmailFake) Mailbox {
	t.Helper()
	c := NewGmailClient(staticToken("send"), g.srv.URL, "desk@example.com").
		WithMailboxTokens(staticToken("read"), staticToken("modify"))
	mb, err := MailboxOf(c)
	if err != nil {
		t.Fatalf("MailboxOf(gmail): %v", err)
	}
	return mb
}

func TestGmailWatchReadsHistoryAsALog(t *testing.T) {
	g := newGmailFake(t)
	g.messages["m1"] = gmailMsg("m1", []string{"INBOX", "UNREAD"}, gmailPayload())
	g.messages["m2"] = gmailMsg("m2", []string{"INBOX"}, gmailPayload())
	mb := gmailMailboxFor(t, g)
	ctx := context.Background()

	tip, err := mb.WatchTip(ctx, "INBOX")
	if err != nil || tip != "100" {
		t.Fatalf("WatchTip = %q, %v", tip, err)
	}
	g.history = map[string]any{
		"historyId": "120",
		"history": []any{
			map[string]any{"id": "100"}, // inclusive or not, the start is never re-read
			map[string]any{"id": "105", "messagesAdded": []any{
				map[string]any{"message": map[string]any{"id": "m1"}},
				map[string]any{"message": map[string]any{"id": "gone"}}, // deleted before the read
				map[string]any{"message": map[string]any{"id": "m2"}},
			}},
		},
	}
	page, err := mb.WatchSince(ctx, WatchRequest{Folder: "INBOX", Cursor: tip, Limit: 50, IncludeBody: true})
	if err != nil {
		t.Fatalf("WatchSince: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("page = %d items, want m1 and m2", len(page.Items))
	}
	a, b := page.Items[0], page.Items[1]
	if a.MarkKey != "label:INBOX" || a.Seq != 105<<gmailSeqShift || b.Seq != 105<<gmailSeqShift|2 {
		t.Errorf("marks = %q %d / %d; one mark per label, the record id with the position below it", a.MarkKey, a.Seq, b.Seq)
	}
	if page.Cursor != "120" {
		t.Errorf("cursor = %q; a page read to the end resumes from the mailbox's history id", page.Cursor)
	}
	e := a.Envelope
	if e.From != "anna@example.com" || e.Subject != "äöü" || !e.Unread || e.Folder != "INBOX" {
		t.Errorf("envelope = %+v", e)
	}
	if e.Auth.DMARC != "pass" || e.Body != "Grüezi" {
		t.Errorf("auth %+v body %q", e.Auth, e.Body)
	}
	if len(e.Attachments) != 1 || e.Attachments[0] != (Attachment{Name: "R.pdf", ContentType: "application/pdf", Size: 4000}) {
		t.Errorf("attachments = %+v", e.Attachments)
	}
	for _, r := range g.requests() {
		if strings.Contains(r.path, "/attachments/") {
			t.Errorf("an attachment's content was fetched: %s", r.path)
		}
		if r.method == "GET" && r.auth != "Bearer read" {
			t.Errorf("a read used %q, want the read-scoped token", r.auth)
		}
	}
	hist := g.requests()[1]
	if hist.query.Get("startHistoryId") != "100" || hist.query.Get("historyTypes") != "messageAdded" || hist.query.Get("labelId") != "INBOX" {
		t.Errorf("history query = %v", hist.query)
	}
}

func TestGmailWatchPagesAndGaps(t *testing.T) {
	g := newGmailFake(t)
	g.messages["m1"] = gmailMsg("m1", nil, gmailPayload())
	mb := gmailMailboxFor(t, g)
	ctx := context.Background()

	g.history = map[string]any{"historyId": "500", "nextPageToken": "more",
		"history": []any{map[string]any{"id": "200", "messagesAdded": []any{map[string]any{"message": map[string]any{"id": "m1"}}}}}}
	page, err := mb.WatchSince(ctx, WatchRequest{Folder: "INBOX", Cursor: "150", Limit: 1})
	if err != nil || page.Cursor != "200" {
		t.Fatalf("a page with more behind it = cursor %q, %v; want its last record", page.Cursor, err)
	}
	g.history = map[string]any{"historyId": "150"}
	page, err = mb.WatchSince(ctx, WatchRequest{Folder: "INBOX", Cursor: "150"})
	if err != nil || page.Cursor != "" || len(page.Items) != 0 {
		t.Fatalf("an idle mailbox = %q, %d, %v; an unchanged cursor is not returned", page.Cursor, len(page.Items), err)
	}
	g.history = nil // 404: older than Gmail keeps
	g.profile = "900"
	page, err = mb.WatchSince(ctx, WatchRequest{Folder: "INBOX", Cursor: "150"})
	if err != nil || page.Cursor != "900" || !strings.Contains(page.Gap, "no longer keeps history") {
		t.Fatalf("expired history = cursor %q gap %q, %v", page.Cursor, page.Gap, err)
	}
	page, err = mb.WatchSince(ctx, WatchRequest{Folder: "INBOX"})
	if err != nil || page.Cursor != "900" || page.Gap == "" {
		t.Fatalf("no cursor = %q gap %q, %v; Gmail cannot replay from the beginning and must say so", page.Cursor, page.Gap, err)
	}
}

func TestGmailBodyNotInlinedIsFetched(t *testing.T) {
	g := newGmailFake(t)
	g.messages["m1"] = gmailMsg("m1", nil, map[string]any{
		"mimeType": "text/plain", "headers": []any{map[string]any{"name": "From", "value": "a@b.ch"}},
		"body": map[string]any{"size": 9, "attachmentId": "BODY"},
	})
	e, err := gmailMailboxFor(t, g).Get(context.Background(), "m1", true)
	if err != nil || e.Body != "big body" || len(e.Attachments) != 0 {
		t.Fatalf("Get = %q, %+v, %v", e.Body, e.Attachments, err)
	}
}

func TestGmailListAndChanges(t *testing.T) {
	g := newGmailFake(t)
	g.messages["m1"] = gmailMsg("m1", []string{"INBOX"}, gmailPayload())
	g.list = []string{"m1", "deleted"}
	mb := gmailMailboxFor(t, g)
	ctx := context.Background()

	envs, err := mb.List(ctx, ListRequest{Folder: "Label_7", UnreadOnly: true, MaxResults: 3})
	if err != nil || len(envs) != 1 || envs[0].Folder != "Label_7" {
		t.Fatalf("List = %+v, %v", envs, err)
	}
	if q := g.requests()[0].query; q.Get("labelIds") != "Label_7" || q.Get("q") != "is:unread" || q.Get("maxResults") != "3" {
		t.Errorf("list query = %v", q)
	}
	before := len(g.requests())
	if id, err := mb.Move(ctx, "m1", "Label_9"); err != nil || id != "m1" {
		t.Fatalf("Move = %q, %v", id, err)
	}
	if _, err := mb.Move(ctx, "m1", ""); err == nil {
		t.Error("a move without a destination was sent")
	}
	if err := mb.SetRead(ctx, "m1", true); err != nil {
		t.Fatal(err)
	}
	if err := mb.SetRead(ctx, "m1", false); err != nil {
		t.Fatal(err)
	}
	if err := mb.Delete(ctx, "m1"); err != nil {
		t.Fatal(err)
	}
	if err := mb.Reply(ctx, "m1", Reply{Body: "Danke", MessageID: "77"}); err != nil {
		t.Fatal(err)
	}
	var calls []string
	for _, r := range g.requests()[before:] {
		calls = append(calls, r.method+" "+strings.TrimPrefix(r.path, "/users/me")+" "+r.auth)
		switch {
		case strings.HasSuffix(r.path, "/modify") && r.body["addLabelIds"] != nil:
			if add := r.body["addLabelIds"].([]any); add[0] == "Label_9" {
				if rm := r.body["removeLabelIds"].([]any); rm[0] != "INBOX" {
					t.Errorf("a move must take the message out of the inbox: %v", r.body)
				}
			}
		case r.path == "/users/me/messages/send":
			raw, _ := base64.URLEncoding.DecodeString(r.body["raw"].(string))
			if r.body["threadId"] != "t-m1" || !strings.Contains(string(raw), "In-Reply-To: <g1@x>") ||
				!strings.Contains(string(raw), "To: anna@example.com") {
				t.Errorf("reply = thread %v\n%s", r.body["threadId"], raw)
			}
		}
	}
	want := []string{
		"POST /messages/m1/modify Bearer modify",
		"POST /messages/m1/modify Bearer modify",
		"POST /messages/m1/modify Bearer modify",
		"POST /messages/m1/trash Bearer modify",
		"GET /messages/m1 Bearer read",
		"POST /messages/send Bearer send",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls =\n%s\nwant\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestGmailClientWithoutMailboxTokensUsesItsOwn(t *testing.T) {
	g := newGmailFake(t)
	mb, _ := MailboxOf(NewGmailClient(staticToken("only"), g.srv.URL, "desk@example.com"))
	if _, err := mb.WatchTip(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if got := g.requests()[0].auth; got != "Bearer only" {
		t.Errorf("auth = %q", got)
	}
}
