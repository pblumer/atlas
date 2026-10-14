package mail

import (
	"bufio"
	"context"
	"strings"
	"testing"
	"time"
)

const fakeHeader1 = "From: =?ISO-8859-1?Q?J=FCrg?= <Juerg@Example.com>\r\n" +
	"Reply-To: desk@example.com\r\n" +
	"To: me@example.com, Other <other@example.com>\r\n" +
	"Subject: =?UTF-8?B?w6TDtsO8?=\r\n" +
	"Message-ID: <m1@example.com>\r\n" +
	"References: <m0@example.com>\r\n" +
	"Authentication-Results: mx.example.com; spf=pass smtp.mailfrom=example.com;\r\n" +
	" dkim=pass header.d=example.com; dmarc=pass header.from=example.com\r\n" +
	"Authentication-Results: forged.example; dmarc=fail\r\n" +
	"\r\n"

// fakeBS1 is multipart/mixed: an alternative (plain in Latin-1, HTML) and a PDF.
const fakeBS1 = `((("text" "plain" ("charset" "iso-8859-1") NIL NIL "quoted-printable" 20 1 NIL NIL NIL NIL)` +
	`("text" "html" ("charset" "utf-8") NIL NIL "7bit" 40 1 NIL NIL NIL NIL) "alternative" ("boundary" "b2") NIL NIL NIL)` +
	`("application" "pdf" ("name" "R.pdf") NIL NIL "base64" 1000 NIL ("attachment" ("filename" "Rechnung.pdf")) NIL NIL)` +
	` "mixed" ("boundary" "b1") NIL NIL NIL)`

const fakeHeader2 = "From: bob@example.com\r\nSubject: plain\r\nMessage-ID: <m2@example.com>\r\n\r\n"

const fakeBS2 = `("text" "html" ("charset" "utf-8") NIL NIL "7bit" 30 1 NIL NIL NIL NIL)`

func msg1() *fakeMsg {
	return &fakeMsg{date: "02-Oct-2026 08:30:00 +0200", header: fakeHeader1, bs: fakeBS1,
		sections: map[string]string{"1.1": "Gr=FCezi miteinand", "1.2": "<p>html</p>"}}
}

func msg2() *fakeMsg {
	return &fakeMsg{date: "02-Oct-2026 09:00:00 +0200", header: fakeHeader2, bs: fakeBS2,
		sections: map[string]string{"1": "<p>Hallo&nbsp;<b>Bob</b></p>"}}
}

func TestIMAPWatchReadsForwardFromTheCursor(t *testing.T) {
	f, ctls := newFakeIMAP(t)
	m := f.mailbox(t, ctls, nil)
	ctx := context.Background()

	f.add("INBOX", msg1()) // history: the tip skips it
	tip, err := m.WatchTip(ctx, "INBOX")
	if err != nil {
		t.Fatalf("WatchTip: %v", err)
	}
	if tip != "7:1" {
		t.Fatalf("tip = %q, want validity:last-uid 7:1", tip)
	}
	f.add("INBOX", msg1())
	f.add("INBOX", msg2())

	page, err := m.WatchSince(ctx, WatchRequest{Folder: "INBOX", Cursor: tip, Limit: 10, IncludeBody: true})
	if err != nil {
		t.Fatalf("WatchSince: %v", err)
	}
	if len(page.Items) != 2 || page.Cursor != "7:3" || page.Gap != "" {
		t.Fatalf("page = %d items, cursor %q, gap %q", len(page.Items), page.Cursor, page.Gap)
	}
	first := page.Items[0]
	if first.MarkKey != "INBOX#7" || first.Seq != 2 {
		t.Errorf("mark = %q/%d, want one per folder and validity, sequenced by UID", first.MarkKey, first.Seq)
	}
	e := first.Envelope
	if e.ID != "7/2/INBOX" || e.From != "juerg@example.com" || e.FromName != "Jürg" || e.ReplyTo != "desk@example.com" {
		t.Errorf("envelope identity = %+v", e)
	}
	if e.Subject != "äöü" || strings.Join(e.To, ",") != "me@example.com,other@example.com" {
		t.Errorf("subject/to = %q / %q", e.Subject, e.To)
	}
	if e.Auth != (AuthResults{SPF: "pass", DKIM: "pass", DMARC: "pass"}) {
		t.Errorf("auth = %+v: the topmost header, unfolded, is the verdict", e.Auth)
	}
	if want := time.Date(2026, 10, 2, 6, 30, 0, 0, time.UTC); !e.ReceivedAt.Equal(want) {
		t.Errorf("receivedAt = %v, want INTERNALDATE %v", e.ReceivedAt, want)
	}
	if !e.Unread || e.Body != "Grüezi miteinand" || e.BodyTruncated {
		t.Errorf("unread %v, body %q, cut %v", e.Unread, e.Body, e.BodyTruncated)
	}
	if len(e.Attachments) != 1 || e.Attachments[0] != (Attachment{Name: "Rechnung.pdf", ContentType: "application/pdf", Size: 1000}) {
		t.Errorf("attachments = %+v", e.Attachments)
	}
	if got := page.Items[1].Envelope.Body; got != "Hallo Bob" {
		t.Errorf("an HTML-only message's body = %q, want its text", got)
	}
	for _, c := range f.cmds() {
		if strings.HasPrefix(c, "SELECT") || strings.Contains(c, "STORE") {
			t.Errorf("a watch changed the mailbox or opened it writable: %q", c)
		}
		if strings.Contains(c, "FETCH") && strings.Contains(c, "BODY[") {
			t.Errorf("a fetch without PEEK marks the message read: %q", c)
		}
	}
}

func TestIMAPWatchWithoutBodyFetchesNoBody(t *testing.T) {
	f, ctls := newFakeIMAP(t)
	m := f.mailbox(t, ctls, nil)
	f.add("INBOX", msg1())
	page, err := m.WatchSince(context.Background(), WatchRequest{Folder: "INBOX", Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("WatchSince from the beginning = %d items, %v", len(page.Items), err)
	}
	if page.Items[0].Envelope.Body != "" {
		t.Error("a body nobody asked for was read")
	}
	for _, c := range f.cmds() {
		if strings.Contains(c, "BODY.PEEK[1") {
			t.Errorf("a body part was fetched without being asked for: %q", c)
		}
	}
}

func TestIMAPWatchAtTheTipReadsNothing(t *testing.T) {
	f, ctls := newFakeIMAP(t)
	m := f.mailbox(t, ctls, nil)
	f.add("INBOX", msg1())
	// "2:*" makes a real server answer UID 1 — the highest — even though it is below 2.
	page, err := m.WatchSince(context.Background(), WatchRequest{Folder: "INBOX", Cursor: "7:1", Limit: 10})
	if err != nil {
		t.Fatalf("WatchSince: %v", err)
	}
	if len(page.Items) != 0 || page.Cursor != "" {
		t.Errorf("at the tip = %d items, cursor %q; the highest UID must not be re-delivered", len(page.Items), page.Cursor)
	}
}

func TestIMAPWatchLimitsThePage(t *testing.T) {
	f, ctls := newFakeIMAP(t)
	m := f.mailbox(t, ctls, nil)
	for i := 0; i < 5; i++ {
		f.add("INBOX", msg2())
	}
	page, err := m.WatchSince(context.Background(), WatchRequest{Folder: "INBOX", Cursor: "7:1", Limit: 2})
	if err != nil {
		t.Fatalf("WatchSince: %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].Seq != 2 || page.Cursor != "7:3" {
		t.Errorf("page = %d items from %d, cursor %q; want the two oldest after the cursor", len(page.Items), page.Items[0].Seq, page.Cursor)
	}
}

func TestIMAPWatchRenumberedFolderResumesAtTheTipAndSaysSo(t *testing.T) {
	f, ctls := newFakeIMAP(t)
	m := f.mailbox(t, ctls, nil)
	f.add("INBOX", msg1())
	f.add("INBOX", msg2())
	page, err := m.WatchSince(context.Background(), WatchRequest{Folder: "INBOX", Cursor: "3:1", Limit: 10})
	if err != nil {
		t.Fatalf("WatchSince: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("a renumbered folder published %d messages under a fresh mark", len(page.Items))
	}
	if page.Cursor != "7:2" || !strings.Contains(page.Gap, "renumbered") {
		t.Errorf("cursor %q, gap %q; want the tip and a gap that says what happened", page.Cursor, page.Gap)
	}
}

func TestIMAPListAndGet(t *testing.T) {
	f, ctls := newFakeIMAP(t)
	m := f.mailbox(t, ctls, nil)
	ctx := context.Background()
	f.add("INBOX", msg1())
	read := msg2()
	read.flags = map[string]bool{`\Seen`: true}
	f.add("INBOX", read)
	f.add("INBOX", msg2())

	all, err := m.List(ctx, ListRequest{Folder: "INBOX", MaxResults: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 || all[0].ID != "7/3/INBOX" || all[1].ID != "7/2/INBOX" {
		t.Errorf("list = %v; want the two newest, newest first", ids(all))
	}
	unread, err := m.List(ctx, ListRequest{UnreadOnly: true})
	if err != nil {
		t.Fatalf("List unread: %v", err)
	}
	if strings.Join(ids(unread), ",") != "7/3/INBOX,7/1/INBOX" {
		t.Errorf("unread list = %v", ids(unread))
	}
	got, err := m.Get(ctx, "7/1/INBOX", true)
	if err != nil || got.Body != "Grüezi miteinand" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if _, err := m.Get(ctx, "6/1/INBOX", false); err == nil || !strings.Contains(err.Error(), "renumbered") {
		t.Errorf("an id from an earlier numbering = %v; it must be refused, not answered with another message", err)
	}
	if _, err := m.Get(ctx, "7/99/INBOX", false); err == nil {
		t.Error("a message that does not exist answered")
	}
	if _, err := m.Get(ctx, "garbage", false); err == nil || !strings.Contains(err.Error(), "not an IMAP message id") {
		t.Errorf("a malformed id = %v", err)
	}
}

func ids(envs []Envelope) []string {
	out := make([]string, len(envs))
	for i, e := range envs {
		out[i] = e.ID
	}
	return out
}

func TestIMAPMoveWithMove(t *testing.T) {
	f, ctls := newFakeIMAP(t, withCaps("MOVE", "UIDPLUS"), withFolder("Archiv", 11, 40))
	m := f.mailbox(t, ctls, nil)
	f.add("INBOX", msg1())
	newID, err := m.Move(context.Background(), "7/1/INBOX", "Archiv")
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if newID != "11/40/Archiv" {
		t.Errorf("moved id = %q, want the destination's validity and UID from COPYUID", newID)
	}
	if len(f.snapshot("INBOX")) != 0 || len(f.snapshot("Archiv")) != 1 {
		t.Error("the message did not move")
	}
}

func TestIMAPMoveWithoutMoveExpungesOnlyThatMessage(t *testing.T) {
	f, ctls := newFakeIMAP(t, withCaps("UIDPLUS"), withFolder("Archiv", 11, 1))
	m := f.mailbox(t, ctls, nil)
	f.add("INBOX", msg1())
	other := msg2()
	other.flags = map[string]bool{`\Deleted`: true} // somebody else's decision
	f.add("INBOX", other)
	if _, err := m.Move(context.Background(), "7/1/INBOX", "Archiv"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if in := f.snapshot("INBOX"); len(in) != 1 || in[0].uid != 2 {
		t.Error("moving one message expunged another")
	}
	var sawUIDExpunge bool
	for _, c := range f.cmds() {
		if c == "EXPUNGE" {
			t.Error("a plain EXPUNGE removes every \\Deleted message in the folder")
		}
		sawUIDExpunge = sawUIDExpunge || strings.HasPrefix(c, "UID EXPUNGE 1")
	}
	if !sawUIDExpunge {
		t.Error("with UIDPLUS the moved message is expunged by its UID")
	}
}

func TestIMAPMoveWithoutUIDPlusOnlyFlags(t *testing.T) {
	f, ctls := newFakeIMAP(t, withFolder("Archiv", 11, 1))
	m := f.mailbox(t, ctls, nil)
	f.add("INBOX", msg1())
	newID, err := m.Move(context.Background(), "7/1/INBOX", "Archiv")
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if newID != "" {
		t.Errorf("without COPYUID the new id is unknown, got %q", newID)
	}
	src := f.snapshot("INBOX")
	if len(src) != 1 || !src[0].flags[`\Deleted`] {
		t.Error("the source copy must stay, flagged \\Deleted, when the server cannot expunge one UID")
	}
	for _, c := range f.cmds() {
		if strings.Contains(c, "EXPUNGE") {
			t.Errorf("an expunge without UIDPLUS: %q", c)
		}
	}
}

func TestIMAPSetRead(t *testing.T) {
	f, ctls := newFakeIMAP(t)
	m := f.mailbox(t, ctls, nil)
	f.add("INBOX", msg1())
	ctx := context.Background()
	if err := m.SetRead(ctx, "7/1/INBOX", true); err != nil {
		t.Fatalf("SetRead: %v", err)
	}
	if !f.snapshot("INBOX")[0].flags[`\Seen`] {
		t.Fatal("not marked read")
	}
	if err := m.SetRead(ctx, "7/1/INBOX", false); err != nil {
		t.Fatalf("SetRead false: %v", err)
	}
	if f.snapshot("INBOX")[0].flags[`\Seen`] {
		t.Fatal("not marked unread")
	}
}

func TestIMAPDeleteMovesToTrash(t *testing.T) {
	f, ctls := newFakeIMAP(t, withCaps("MOVE"), withFolder("Papierkorb", 5, 1), func(f *fakeIMAP) {
		f.listEntries = []string{`(\HasNoChildren) "/" "INBOX"`, `(\HasNoChildren \Trash) "/" "Papierkorb"`}
	})
	m := f.mailbox(t, ctls, nil)
	f.add("INBOX", msg1())
	if err := m.Delete(context.Background(), "7/1/INBOX"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(f.snapshot("INBOX")) != 0 || len(f.snapshot("Papierkorb")) != 1 {
		t.Error("delete did not move the message to the folder marked \\Trash")
	}
}

func TestIMAPDeleteWithoutTrashOnlyFlags(t *testing.T) {
	f, ctls := newFakeIMAP(t)
	m := f.mailbox(t, ctls, nil)
	f.add("INBOX", msg1())
	if err := m.Delete(context.Background(), "7/1/INBOX"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if msgs := f.snapshot("INBOX"); len(msgs) != 1 || !msgs[0].flags[`\Deleted`] {
		t.Error("without a trash folder the message is flagged, not removed")
	}
	for _, c := range f.cmds() {
		if strings.Contains(c, "EXPUNGE") {
			t.Errorf("delete expunged: %q", c)
		}
	}
}

func TestIMAPReplyThreadsThroughTheSender(t *testing.T) {
	f, ctls := newFakeIMAP(t)
	var sent Message
	m := f.mailbox(t, ctls, func(_ context.Context, msg Message) error { sent = msg; return nil })
	f.add("INBOX", msg1())
	err := m.Reply(context.Background(), "7/1/INBOX", Reply{Body: "Danke", MessageID: "42"})
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.Join(sent.To, ",") != "desk@example.com" || sent.Subject != "Re: äöü" {
		t.Errorf("reply to %q, subject %q; want the Reply-To and a Re: subject", sent.To, sent.Subject)
	}
	if sent.InReplyTo != "<m1@example.com>" || sent.References != "<m0@example.com> <m1@example.com>" {
		t.Errorf("threading = %q / %q", sent.InReplyTo, sent.References)
	}
	if sent.MessageID != "42" || sent.Body != "Danke" {
		t.Errorf("reply content = %+v", sent)
	}
	if err := m.Reply(context.Background(), "7/1/INBOX", Reply{}); err == nil {
		t.Error("an empty reply was sent")
	}
}

func TestIMAPStartTLSIsRequired(t *testing.T) {
	// A plaintext listener that does not offer STARTTLS.
	f, ctls := newFakeIMAP(t, func(f *fakeIMAP) { f.startTLS = true })
	m := f.mailbox(t, ctls, nil)
	_, err := m.WatchTip(context.Background(), "INBOX")
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("a server without STARTTLS = %v; the password must not be sent in the clear", err)
	}
	for _, c := range f.cmds() {
		if c == "LOGIN" {
			t.Fatal("LOGIN was sent over plaintext")
		}
	}

	g, ctls2 := newFakeIMAP(t, func(f *fakeIMAP) { f.startTLS, f.offerTLS = true, true })
	tip, err := g.mailbox(t, ctls2, nil).WatchTip(context.Background(), "INBOX")
	if err != nil || tip != "7:0" {
		t.Fatalf("STARTTLS session = %q, %v", tip, err)
	}
}

func TestIMAPLoginRefusalDoesNotCarryThePassword(t *testing.T) {
	f, ctls := newFakeIMAP(t)
	m := f.mailbox(t, ctls, nil)
	m.password = "hunter2"
	_, err := m.WatchTip(context.Background(), "INBOX")
	if err == nil {
		t.Fatal("a wrong password logged in")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("the error carries the password: %v", err)
	}
}

func TestIMAPOversizedLiteralIsRefused(t *testing.T) {
	f, ctls := newFakeIMAP(t, func(f *fakeIMAP) { f.maxLiteral = maxIMAPLiteral + 1 })
	m := f.mailbox(t, ctls, nil)
	f.add("INBOX", msg1())
	if _, err := m.List(context.Background(), ListRequest{}); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("an oversized literal = %v; it must be refused before anything is allocated for it", err)
	}
}

func TestIMAPFolderNameEncoding(t *testing.T) {
	cases := map[string]string{
		"INBOX":         `"INBOX"`,
		"Entwürfe":      `"Entw&APw-rfe"`,
		"A&B":           `"A&-B"`,
		`Quote"Back\`:   `"Quote\"Back\\"`,
		"日本語":           `"&ZeVnLIqe-"`,
		"Gesendete Obj": `"Gesendete Obj"`,
	}
	for in, want := range cases {
		if got := imapFolderName(in); got != want {
			t.Errorf("imapFolderName(%q) = %s, want %s", in, got, want)
		}
	}
	if got := imapQuote("a\r\nb LOGOUT"); got != `""` {
		t.Errorf("a value with a line break must not reach the wire: %s", got)
	}
}

func TestNormalizeIMAPEndpoint(t *testing.T) {
	good := map[string]string{
		"imap.example.com":            "imaps://imap.example.com:993",
		"imap.example.com:993":        "imaps://imap.example.com:993",
		"imap.example.com:143":        "imap://imap.example.com:143",
		"imaps://imap.example.com":    "imaps://imap.example.com:993",
		"imap://imap.example.com":     "imap://imap.example.com:143",
		"IMAPS://imap.example.com/x?": "imaps://imap.example.com:993",
		"imap.example.com:1993":       "imaps://imap.example.com:1993",
		"::1":                         "imaps://[::1]:993",
	}
	for in, want := range good {
		got, err := NormalizeIMAPEndpoint(in)
		if err != nil || got != want {
			t.Errorf("NormalizeIMAPEndpoint(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "pop3://x", "me@example.com", "host:0", "host:99999", "ho st", "a:b:c:d:x"} {
		if _, err := NormalizeIMAPEndpoint(bad); err == nil {
			t.Errorf("NormalizeIMAPEndpoint(%q) accepted", bad)
		}
	}
}

func parseResponses(t *testing.T, raw string) []imapResponse {
	t.Helper()
	ic := &imapConn{r: bufio.NewReader(strings.NewReader(raw))}
	var out []imapResponse
	for {
		r, err := ic.read()
		if err != nil {
			return out
		}
		out = append(out, r)
	}
}

func TestIMAPResponseParser(t *testing.T) {
	rs := parseResponses(t, "* OK [UIDVALIDITY 3857529045] UIDs valid\r\n"+
		"* OK [PERMANENTFLAGS (\\Deleted \\Seen \\*)] Limited\r\n"+
		"* 12 FETCH (UID 34 FLAGS (\\Seen) BODY[HEADER] {5}\r\nab\r\nc BODY[1.2]<0> NIL X \"q\\\"uo\")\r\n"+
		"* LIST (\\HasNoChildren) NIL \"Entw&APw-rfe\"\r\n"+
		"+ go ahead\r\n"+
		"a1 NO [TRYCREATE] no such\r\n")
	if len(rs) != 6 {
		t.Fatalf("parsed %d responses", len(rs))
	}
	if rs[0].Status != "OK" || strings.Join(rs[0].Code, " ") != "UIDVALIDITY 3857529045" || rs[0].Text != "UIDs valid" {
		t.Errorf("status = %+v", rs[0])
	}
	if strings.Join(rs[1].Code, " ") != `PERMANENTFLAGS \Deleted \Seen \*` {
		t.Errorf("code with a list = %q", rs[1].Code)
	}
	items := fetchItems(rs[2])
	if items["UID"] != "34" || items["BODY[HEADER]"] != "ab\r\nc" || items["BODY[1.2]<0>"] != nil || items["X"] != `q"uo` {
		t.Errorf("fetch items = %#v", items)
	}
	if rs[3].Fields[2] != nil || rs[3].Fields[3] != "Entw&APw-rfe" {
		t.Errorf("list = %#v", rs[3].Fields)
	}
	if rs[4].Tag != "+" || rs[5].Tag != "a1" || rs[5].Status != "NO" {
		t.Errorf("continuation / tagged = %+v / %+v", rs[4], rs[5])
	}
}

func TestIMAPParserLimits(t *testing.T) {
	deep := "* X " + strings.Repeat("(", maxIMAPDepth+2) + strings.Repeat(")", maxIMAPDepth+2) + "\r\n"
	for name, raw := range map[string]string{
		"nesting":        deep,
		"literal":        "* X {999999999}\r\n",
		"literal digits": "* X {12a}\r\n",
		"long atom":      "* X " + strings.Repeat("a", maxIMAPToken+1) + "\r\n",
		"open list":      "* X (a b\r\n",
		"open quote":     "* X \"abc\r\n",
		"open section":   "* X BODY[1 \r\n",
	} {
		ic := &imapConn{r: bufio.NewReader(strings.NewReader(raw))}
		if _, err := ic.read(); err == nil {
			t.Errorf("%s: a malformed or oversized response parsed", name)
		}
	}
}

func TestParseBodyStructureShapes(t *testing.T) {
	rs := parseResponses(t, "* 1 FETCH (BODYSTRUCTURE "+fakeBS1+")\r\n")
	root := parseBodyStructure(fetchItems(rs[0])["BODYSTRUCTURE"], "", true)
	if root.Type != "multipart" || root.Subtype != "mixed" || len(root.Children) != 2 {
		t.Fatalf("root = %+v", root)
	}
	alt := root.Children[0]
	if alt.Subtype != "alternative" || alt.Children[0].Section != "1.1" || alt.Children[1].Section != "1.2" {
		t.Errorf("alternative = %+v", alt)
	}
	if pdf := root.Children[1]; pdf.Section != "2" || pdf.Disposition != "attachment" || pdf.Size != 1000 {
		t.Errorf("pdf = %+v", pdf)
	}
	// A single-part message's only part is section 1, which is what BODY[1] reads.
	rs = parseResponses(t, "* 1 FETCH (BODYSTRUCTURE "+fakeBS2+")\r\n")
	if single := parseBodyStructure(fetchItems(rs[0])["BODYSTRUCTURE"], "", true); single.Section != "1" || single.Subtype != "html" {
		t.Errorf("single = %+v", single)
	}
	// A forwarded message carries envelope, body and lines before its extension data.
	fwd := `(("text" "plain" NIL NIL NIL "7bit" 3 1 NIL NIL NIL NIL)("message" "rfc822" NIL NIL NIL "7bit" 300 ` +
		`(NIL "s" NIL NIL NIL NIL NIL NIL NIL NIL) ("text" "plain" NIL NIL NIL "7bit" 3 1) 12 NIL ("attachment" ("filename" "fwd.eml")) NIL NIL) "mixed")`
	rs = parseResponses(t, "* 1 FETCH (BODYSTRUCTURE "+fwd+")\r\n")
	p := parseBodyStructure(fetchItems(rs[0])["BODYSTRUCTURE"], "", true)
	if atts := p.attachments(); len(atts) != 1 || atts[0].Name != "fwd.eml" || atts[0].Size != 300 {
		t.Errorf("forwarded attachments = %+v", atts)
	}
	if got := parseBodyStructure("not a list", "", true); got.Type != "" {
		t.Errorf("garbage = %+v", got)
	}
}

// A quoted string is seven-bit, so a password with an umlaut goes through AUTHENTICATE
// PLAIN when the server offers it with an initial response — and an ASCII one keeps
// using LOGIN, which every server accepts.
func TestIMAPEightBitPasswordUsesAuthenticatePlain(t *testing.T) {
	f, ctls := newFakeIMAP(t, withCaps("AUTH=PLAIN", "SASL-IR"), func(f *fakeIMAP) { f.password = "Grüezi-2026" })
	if _, err := f.mailbox(t, ctls, nil).WatchTip(context.Background(), "INBOX"); err != nil {
		t.Fatalf("WatchTip: %v", err)
	}
	if got := strings.Join(f.cmds(), ","); !strings.Contains(got, "AUTHENTICATE") || strings.Contains(got, "LOGIN") {
		t.Errorf("commands = %s; an 8-bit password must not travel as a quoted string", got)
	}
	g, ctls2 := newFakeIMAP(t, withCaps("AUTH=PLAIN", "SASL-IR"))
	if _, err := g.mailbox(t, ctls2, nil).WatchTip(context.Background(), "INBOX"); err != nil {
		t.Fatalf("WatchTip: %v", err)
	}
	if got := strings.Join(g.cmds(), ","); !strings.Contains(got, "LOGIN") {
		t.Errorf("commands = %s; an ASCII password keeps using LOGIN", got)
	}
}

func TestIMAPLoginDisabledWithoutAnAlternativeIsRefused(t *testing.T) {
	f, ctls := newFakeIMAP(t, withCaps("LOGINDISABLED"))
	if _, err := f.mailbox(t, ctls, nil).WatchTip(context.Background(), "INBOX"); err == nil || !strings.Contains(err.Error(), "disabled LOGIN") {
		t.Fatalf("= %v", err)
	}
}

func TestIMAPParserToleratesATrailingSpace(t *testing.T) {
	rs := parseResponses(t, "* SEARCH \r\n* SEARCH 3 4 \r\n")
	if len(rs) != 2 || len(rs[0].Fields) != 1 || len(rs[1].Fields) != 3 {
		t.Fatalf("parsed = %#v", rs)
	}
}
