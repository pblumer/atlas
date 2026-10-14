package mail

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"testing"
)

// The branches a well-behaved provider never takes, and the ones a misbehaving one
// does: each is a sentence a real server can say.

func TestIMAPTipWithoutUIDNext(t *testing.T) {
	f, ctls := newFakeIMAP(t, func(f *fakeIMAP) { f.noUIDNext = true })
	m := f.mailbox(t, ctls, nil)
	tip, err := m.WatchTip(context.Background(), "INBOX")
	if err != nil || tip != "7:0" {
		t.Fatalf("an empty folder without UIDNEXT = %q, %v", tip, err)
	}
	f.add("INBOX", msg2())
	f.add("INBOX", msg2())
	if tip, err = m.WatchTip(context.Background(), "INBOX"); err != nil || tip != "7:2" {
		t.Fatalf("a folder without UIDNEXT = %q, %v; the highest UID is the tip", tip, err)
	}
	// A renumbered folder resumes at the tip that way too.
	page, err := m.WatchSince(context.Background(), WatchRequest{Folder: "INBOX", Cursor: "1:1"})
	if err != nil || page.Cursor != "7:2" {
		t.Fatalf("renumbered without UIDNEXT = %q, %v", page.Cursor, err)
	}
}

func TestIMAPPreauthSkipsTheLogin(t *testing.T) {
	f, ctls := newFakeIMAP(t, func(f *fakeIMAP) { f.preauth = true })
	m := f.mailbox(t, ctls, nil)
	if _, err := m.WatchTip(context.Background(), "INBOX"); err != nil {
		t.Fatalf("WatchTip: %v", err)
	}
	for _, c := range f.cmds() {
		if c == "LOGIN" {
			t.Fatal("a LOGIN in the authenticated state is a protocol error")
		}
	}
}

func TestIMAPUnknownFolderAndUnreachableServer(t *testing.T) {
	f, ctls := newFakeIMAP(t)
	m := f.mailbox(t, ctls, nil)
	if _, err := m.List(context.Background(), ListRequest{Folder: "Gibts nicht"}); err == nil ||
		!strings.Contains(err.Error(), "Gibts nicht") {
		t.Errorf("an unknown folder = %v; the error must name it", err)
	}
	if err := m.SetRead(context.Background(), "7/1/Gibts nicht", true); err == nil {
		t.Error("a message in an unknown folder was marked")
	}
	var se *imapStatusError
	_, err := m.List(context.Background(), ListRequest{Folder: "Gibts nicht"})
	if !errors.As(err, &se) || !strings.Contains(se.Error(), "EXAMINE") {
		t.Errorf("status error = %v", err)
	}
	dead, err := newIMAPMailbox("imaps://127.0.0.1:1", "u", "p", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dead.WatchTip(context.Background(), "INBOX"); err == nil || !strings.Contains(err.Error(), "connect to") {
		t.Errorf("an unreachable server = %v", err)
	}
	if _, err := newIMAPMailbox("pop3://x", "u", "p", nil); err == nil {
		t.Error("a bad endpoint built a mailbox")
	}
	nosend := f.mailbox(t, ctls, nil)
	f.add("INBOX", msg1())
	if err := nosend.Reply(context.Background(), "7/1/INBOX", Reply{Body: "x"}); err == nil {
		t.Error("a mailbox with no sender replied")
	}
}

func TestIMAPParserEdges(t *testing.T) {
	rs := parseResponses(t, "* X \"a\\\\b\" {0}\r\n NIL\r\n+\r\n* OK\r\n")
	if len(rs) != 3 || rs[0].Fields[1] != `a\b` || rs[0].Fields[2] != "" || rs[0].Fields[3] != nil {
		t.Fatalf("parsed = %#v", rs)
	}
	for name, raw := range map[string]string{
		"bad line end":    "* X a\rb\r\n",
		"stray character": "* X a)b\r\n",
		"empty tag":       " X\r\n",
		"literal no crlf": "* X {3}abc\r\n",
		"short literal":   "* X {9}\r\nabc",
		"open literal":    "* X {12",
		"long line":       "* OK " + strings.Repeat("x", maxIMAPToken+1) + "\r\n",
		"long quote":      "* X \"" + strings.Repeat("x", maxIMAPToken+1) + "\"\r\n",
		"long tag":        strings.Repeat("t", maxIMAPToken+1) + " OK\r\n",
		"eof":             "* X",
	} {
		ic := &imapConn{r: bufio.NewReader(strings.NewReader(raw))}
		if _, err := ic.read(); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestParseAddressFallbacks(t *testing.T) {
	if addr, _ := parseAddress("not <<parseable@example.com"); addr != "not <<parseable@example.com" {
		t.Errorf("an unparseable sender = %q; kept as written when it addresses somebody", addr)
	}
	if addr, name := parseAddress("nobody"); addr != "" || name != "" {
		t.Errorf("a sender with no address = %q, %q", addr, name)
	}
}

func TestGraphListAndTipEdges(t *testing.T) {
	var answer []any
	status := 200
	g := newGraphFake(t, func(r graphReq) (int, any) { return status, map[string]any{"value": answer} })
	mb := graphMailboxFor(t, g)
	ctx := context.Background()
	if tip, err := mb.WatchTip(ctx, ""); err != nil || tip != "" {
		t.Errorf("an empty folder's tip = %q, %v", tip, err)
	}
	answer = []any{map[string]any{"id": "x", "receivedDateTime": "not a time"}}
	if tip, err := mb.WatchTip(ctx, ""); err != nil || tip != "" {
		t.Errorf("a tip without a time = %q, %v", tip, err)
	}
	answer = []any{graphJSONMsg("a", "2026-10-02T06:30:00Z", nil), map[string]any{"id": "", "receivedDateTime": "2026-10-02T06:31:00Z"}}
	page, err := mb.WatchSince(ctx, WatchRequest{Limit: 5000})
	if err != nil || len(page.Items) != 1 || page.Cursor != "2026-10-02T06:30:00Z|a" {
		t.Errorf("a backfill read = %d items, %q, %v", len(page.Items), page.Cursor, err)
	}
	if got := g.requests()[len(g.requests())-1].query.Get("$top"); got != "1000" {
		t.Errorf("$top = %s, want Graph's page maximum", got)
	}
	envs, err := mb.List(ctx, ListRequest{})
	if err != nil || len(envs) != 2 {
		t.Errorf("List = %d, %v", len(envs), err)
	}
	status = 500
	if _, err := mb.List(ctx, ListRequest{}); err == nil {
		t.Error("a failed list answered")
	}
	if _, err := mb.WatchSince(ctx, WatchRequest{}); err == nil {
		t.Error("a failed watch read answered")
	}
	if _, err := mb.Move(ctx, "a", "b"); err == nil {
		t.Error("a failed move answered")
	}
}

func TestGraphAttachmentFailureFailsTheRead(t *testing.T) {
	g := newGraphFake(t, func(r graphReq) (int, any) {
		if strings.HasSuffix(r.path, "/attachments") {
			return 403, map[string]any{"error": "no"}
		}
		return 200, graphJSONMsg("a", "2026-10-02T06:30:00Z", map[string]any{"hasAttachments": true})
	})
	if _, err := graphMailboxFor(t, g).Get(context.Background(), "a", false); err == nil {
		t.Error("a message whose attachments could not be listed was answered without them")
	}
}

func TestGmailEdges(t *testing.T) {
	g := newGmailFake(t)
	mb := gmailMailboxFor(t, g)
	ctx := context.Background()
	g.profile = "not-a-number"
	if _, err := mb.WatchTip(ctx, ""); err == nil {
		t.Error("a profile without a history id answered a tip")
	}
	g.profile = "50"
	g.history = map[string]any{"historyId": "60", "history": []any{
		map[string]any{"id": "55", "messagesAdded": []any{map[string]any{"message": map[string]any{"id": "missing"}}}},
	}}
	page, err := mb.WatchSince(ctx, WatchRequest{Cursor: "50"})
	if err != nil || len(page.Items) != 0 || page.Cursor != "60" {
		t.Errorf("a deleted message = %d items, %q, %v", len(page.Items), page.Cursor, err)
	}
	if _, err := mb.Get(ctx, "missing", false); err == nil {
		t.Error("a missing message answered")
	}
	if err := mb.Reply(ctx, "missing", Reply{Body: "x"}); err == nil {
		t.Error("a reply to a missing message was sent")
	}
	g.messages["nofrom"] = gmailMsg("nofrom", nil, map[string]any{"mimeType": "text/plain"})
	if err := mb.Reply(ctx, "nofrom", Reply{Body: "x"}); err == nil {
		t.Error("a reply to a message with no sender was sent")
	}
	nosender, _ := MailboxOf(NewGmailClient(staticToken("t"), g.srv.URL, ""))
	g.messages["m"] = gmailMsg("m", nil, gmailPayload())
	if err := nosender.Reply(ctx, "m", Reply{Body: "x"}); err == nil {
		t.Error("a worker with no sender replied")
	}
	if err := mb.SetRead(ctx, "m", false); err != nil {
		t.Errorf("mark unread: %v", err)
	}
	if id, err := mb.Move(ctx, "m", "INBOX"); err != nil || id != "m" {
		t.Errorf("a move back into the inbox = %q, %v", id, err)
	}
	if got := gmailLabel(" "); got != DefaultFolder {
		t.Errorf("gmailLabel(blank) = %q", got)
	}
	bad := NewGmailClient(erroringToken{}, g.srv.URL, "a@b.c")
	bmb, _ := MailboxOf(bad)
	if _, err := bmb.List(ctx, ListRequest{}); err == nil {
		t.Error("a token failure answered a list")
	}
	if _, err := bmb.WatchSince(ctx, WatchRequest{Cursor: "5"}); err == nil {
		t.Error("a token failure answered a watch read")
	}
}
