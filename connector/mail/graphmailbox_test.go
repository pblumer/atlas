package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// graphFake answers Graph requests from a handler and records each one.
type graphFake struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []graphReq
}

type graphReq struct {
	method, path, prefer, auth string
	query                      url.Values
	body                       map[string]any
}

func newGraphFake(t *testing.T, answer func(r graphReq) (int, any)) *graphFake {
	t.Helper()
	g := &graphFake{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := graphReq{method: r.Method, path: r.URL.Path, prefer: r.Header.Get("Prefer"),
			auth: r.Header.Get("Authorization"), query: r.URL.Query()}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &req.body)
		}
		g.mu.Lock()
		g.reqs = append(g.reqs, req)
		g.mu.Unlock()
		status, body := answer(req)
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *graphFake) requests() []graphReq {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]graphReq(nil), g.reqs...)
}

func graphMailboxFor(t *testing.T, g *graphFake) Mailbox {
	t.Helper()
	mb, err := MailboxOf(NewGraphClient(staticToken("gtok"), g.srv.URL, "Desk@Example.com"))
	if err != nil {
		t.Fatalf("MailboxOf(graph): %v", err)
	}
	return mb
}

func graphJSONMsg(id, received string, extra map[string]any) map[string]any {
	m := map[string]any{
		"id": id, "internetMessageId": "<" + id + "@x>", "subject": "S " + id,
		"from":             map[string]any{"emailAddress": map[string]any{"name": "Anna", "address": "Anna@Example.com"}},
		"toRecipients":     []any{map[string]any{"emailAddress": map[string]any{"address": "desk@example.com"}}},
		"receivedDateTime": received, "isRead": false, "hasAttachments": false,
		"internetMessageHeaders": []any{
			map[string]any{"name": "Authentication-Results", "value": "spf=pass smtp.mailfrom=example.com; dkim=pass header.d=example.com; dmarc=pass header.from=example.com"},
			map[string]any{"name": "References", "value": "<r0@x>"},
		},
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestGraphWatchDeliversEachMessageOnceAcrossTheBoundarySecond(t *testing.T) {
	var answer []any
	g := newGraphFake(t, func(r graphReq) (int, any) { return 200, map[string]any{"value": answer} })
	mb := graphMailboxFor(t, g)
	ctx := context.Background()

	answer = []any{graphJSONMsg("m1", "2026-10-02T06:30:00Z", nil)}
	tip, err := mb.WatchTip(ctx, "INBOX")
	if err != nil || tip != "2026-10-02T06:30:00Z|m1" {
		t.Fatalf("WatchTip = %q, %v", tip, err)
	}
	reqs := g.requests()
	if reqs[0].path != "/users/Desk@Example.com/mailFolders/inbox/messages" || reqs[0].query.Get("$orderby") != "receivedDateTime desc" {
		t.Errorf("tip request = %s ?%v", reqs[0].path, reqs[0].query)
	}
	if !strings.Contains(reqs[0].prefer, `IdType="ImmutableId"`) || reqs[0].auth != "Bearer gtok" {
		t.Errorf("tip request prefer %q auth %q", reqs[0].prefer, reqs[0].auth)
	}

	// The server answers "ge" the cursor's second, so m1 comes back; m2 shares its second.
	answer = []any{
		graphJSONMsg("m1", "2026-10-02T06:30:00Z", nil),
		graphJSONMsg("m2", "2026-10-02T06:30:00Z", nil),
		graphJSONMsg("m3", "2026-10-02T06:31:00Z", nil),
	}
	page, err := mb.WatchSince(ctx, WatchRequest{Folder: "INBOX", Cursor: tip, Limit: 10})
	if err != nil {
		t.Fatalf("WatchSince: %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].Envelope.ID != "m2" || page.Items[1].Envelope.ID != "m3" {
		t.Fatalf("page = %+v; want m2 (same second, not yet delivered) and m3", page.Items)
	}
	if page.Items[0].MarkKey != "m2" || page.Items[0].Seq == 0 {
		t.Errorf("mark = %q/%d; want one per message", page.Items[0].MarkKey, page.Items[0].Seq)
	}
	if page.Cursor != "2026-10-02T06:31:00Z|m3" {
		t.Errorf("cursor = %q", page.Cursor)
	}
	last := g.requests()[len(g.requests())-1]
	if last.query.Get("$filter") != "receivedDateTime ge 2026-10-02T06:30:00Z" || last.query.Get("$top") != "11" {
		t.Errorf("since request = %v; the top grows by the ids already delivered at the boundary", last.query)
	}
	if strings.Contains(last.query.Get("$select"), "body") {
		t.Error("a watch without includeBody asked for the body")
	}

	// A third message in the same second as the cursor joins the seen set.
	answer = []any{
		graphJSONMsg("m3", "2026-10-02T06:31:00Z", nil),
		graphJSONMsg("m4", "2026-10-02T06:31:00Z", nil),
	}
	page, err = mb.WatchSince(ctx, WatchRequest{Folder: "INBOX", Cursor: page.Cursor, Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Cursor != "2026-10-02T06:31:00Z|m3,m4" {
		t.Fatalf("page = %d items, cursor %q, %v", len(page.Items), page.Cursor, err)
	}
	// Nothing new: no items and no cursor, so the caller stores nothing.
	page, err = mb.WatchSince(ctx, WatchRequest{Folder: "INBOX", Cursor: page.Cursor, Limit: 10})
	if err != nil || len(page.Items) != 0 || page.Cursor != "" {
		t.Fatalf("an idle page = %d items, cursor %q, %v", len(page.Items), page.Cursor, err)
	}
}

func TestGraphEnvelopeAndBody(t *testing.T) {
	g := newGraphFake(t, func(r graphReq) (int, any) {
		if strings.HasSuffix(r.path, "/attachments") {
			return 200, map[string]any{"value": []any{map[string]any{"name": "R.pdf", "contentType": "Application/PDF", "size": 1234}}}
		}
		return 200, graphJSONMsg("m1", "2026-10-02T06:30:00Z", map[string]any{
			"hasAttachments": true, "isRead": true, "parentFolderId": "AQMk",
			"replyTo": []any{map[string]any{"emailAddress": map[string]any{"address": "Reply@Example.com"}}},
			"body":    map[string]any{"contentType": "html", "content": "<p>Hallo&nbsp;Welt</p>"},
		})
	})
	mb := graphMailboxFor(t, g)
	e, err := mb.Get(context.Background(), "m1", true)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if e.From != "anna@example.com" || e.FromName != "Anna" || e.ReplyTo != "reply@example.com" || e.Unread {
		t.Errorf("envelope = %+v", e)
	}
	if e.Auth != (AuthResults{SPF: "pass", DKIM: "pass", DMARC: "pass"}) || e.References != "<r0@x>" {
		t.Errorf("headers = %+v / %q", e.Auth, e.References)
	}
	if e.Body != "Hallo Welt" || e.Folder != "AQMk" {
		t.Errorf("body %q folder %q", e.Body, e.Folder)
	}
	if len(e.Attachments) != 1 || e.Attachments[0] != (Attachment{Name: "R.pdf", ContentType: "application/pdf", Size: 1234}) {
		t.Errorf("attachments = %+v", e.Attachments)
	}
	reqs := g.requests()
	if !strings.Contains(reqs[0].prefer, `outlook.body-content-type="text"`) || !strings.Contains(reqs[0].query.Get("$select"), "body") {
		t.Errorf("a body read must ask for it, as text: prefer %q select %q", reqs[0].prefer, reqs[0].query.Get("$select"))
	}
	if sel := reqs[1].query.Get("$select"); strings.Contains(sel, "contentBytes") || sel == "" {
		t.Errorf("the attachment list must never ask for content: $select=%q", sel)
	}
}

func TestGraphListUnreadKeepsTheOrderingPropertyFirst(t *testing.T) {
	g := newGraphFake(t, func(r graphReq) (int, any) { return 200, map[string]any{"value": []any{}} })
	mb := graphMailboxFor(t, g)
	if _, err := mb.List(context.Background(), ListRequest{Folder: "archive", UnreadOnly: true, MaxResults: 500}); err != nil {
		t.Fatalf("List: %v", err)
	}
	r := g.requests()[0]
	if r.path != "/users/Desk@Example.com/mailFolders/archive/messages" {
		t.Errorf("path = %s", r.path)
	}
	if !strings.HasPrefix(r.query.Get("$filter"), "receivedDateTime ") || !strings.Contains(r.query.Get("$filter"), "isRead eq false") {
		t.Errorf("$filter = %q", r.query.Get("$filter"))
	}
	if r.query.Get("$top") != "100" {
		t.Errorf("$top = %q, want the cap", r.query.Get("$top"))
	}
}

func TestGraphChangingOperations(t *testing.T) {
	g := newGraphFake(t, func(r graphReq) (int, any) {
		if strings.HasSuffix(r.path, "/move") {
			return 201, map[string]any{"id": "m1"}
		}
		return 202, nil
	})
	mb := graphMailboxFor(t, g)
	ctx := context.Background()
	if id, err := mb.Move(ctx, "m1", "Archive"); err != nil || id != "m1" {
		t.Fatalf("Move = %q, %v", id, err)
	}
	if err := mb.SetRead(ctx, "m1", true); err != nil {
		t.Fatalf("SetRead: %v", err)
	}
	if err := mb.Delete(ctx, "m1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := mb.Reply(ctx, "m1", Reply{Body: "text", HTML: "<b>html</b>"}); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if err := mb.Reply(ctx, "m1", Reply{}); err == nil {
		t.Error("an empty reply was sent")
	}
	reqs := g.requests()
	want := []struct{ method, path, key, val string }{
		{"POST", "/users/Desk@Example.com/messages/m1/move", "destinationId", "Archive"},
		{"PATCH", "/users/Desk@Example.com/messages/m1", "isRead", "true"},
		{"POST", "/users/Desk@Example.com/messages/m1/move", "destinationId", "deleteditems"},
		{"POST", "/users/Desk@Example.com/messages/m1/reply", "comment", "<b>html</b>"},
	}
	if len(reqs) != len(want) {
		t.Fatalf("%d requests, want %d", len(reqs), len(want))
	}
	for i, w := range want {
		r := reqs[i]
		if r.method != w.method || r.path != w.path || fmt.Sprint(r.body[w.key]) != w.val {
			t.Errorf("request %d = %s %s %v, want %s %s %s=%s", i, r.method, r.path, r.body, w.method, w.path, w.key, w.val)
		}
	}
}

func TestGraphErrorsCarryTheProviderAnswer(t *testing.T) {
	g := newGraphFake(t, func(r graphReq) (int, any) {
		return 403, map[string]any{"error": map[string]any{"code": "ErrorAccessDenied"}}
	})
	mb := graphMailboxFor(t, g)
	_, err := mb.Get(context.Background(), "m1", false)
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "ErrorAccessDenied") {
		t.Fatalf("a refused read = %v; the operator needs Graph's own reason", err)
	}
	if _, err := MailboxOf(NewGraphClient(staticToken("t"), g.srv.URL, "")); err == nil {
		t.Error("a Graph worker with no sender has no mailbox to read")
	}
	bad := NewGraphClient(erroringToken{}, g.srv.URL, "a@b.c")
	mb2, _ := MailboxOf(bad)
	if _, err := mb2.WatchTip(context.Background(), ""); err == nil {
		t.Error("a token failure answered")
	}
}

func TestGraphCursorParsing(t *testing.T) {
	if _, ok := parseGraphCursor(""); ok {
		t.Error("an empty cursor parsed")
	}
	if _, ok := parseGraphCursor("not-a-time|x"); ok {
		t.Error("a cursor without a time parsed")
	}
	c, ok := parseGraphCursor("2026-10-02T06:30:00Z|b,a,")
	if !ok || c.String() != "2026-10-02T06:30:00Z|a,b" {
		t.Errorf("round trip = %q", c.String())
	}
}
