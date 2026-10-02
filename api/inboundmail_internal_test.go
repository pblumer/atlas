package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/model"
)

// fakeMailbox is a mail Worker client that is also its own mailbox: the watch reads
// scripted pages from it and records what it was asked.
type fakeMailbox struct {
	tip    string
	pages  []mail.WatchPage
	asked  []mail.WatchRequest
	tipped []string
	err    error
}

func (f *fakeMailbox) Send(context.Context, mail.Message) error { return nil }
func (f *fakeMailbox) WatchTip(_ context.Context, folder string) (string, error) {
	f.tipped = append(f.tipped, folder)
	return f.tip, f.err
}
func (f *fakeMailbox) WatchSince(_ context.Context, r mail.WatchRequest) (mail.WatchPage, error) {
	f.asked = append(f.asked, r)
	if f.err != nil {
		return mail.WatchPage{}, f.err
	}
	if len(f.pages) == 0 {
		return mail.WatchPage{}, nil
	}
	p := f.pages[0]
	f.pages = f.pages[1:]
	return p, nil
}
func (f *fakeMailbox) List(context.Context, mail.ListRequest) ([]mail.Envelope, error) {
	return nil, nil
}
func (f *fakeMailbox) Get(context.Context, string, bool) (mail.Envelope, error) {
	return mail.Envelope{}, nil
}
func (f *fakeMailbox) Move(context.Context, string, string) (string, error) { return "", nil }
func (f *fakeMailbox) SetRead(context.Context, string, bool) error          { return nil }
func (f *fakeMailbox) Delete(context.Context, string) error                 { return nil }
func (f *fakeMailbox) Reply(context.Context, string, mail.Reply) error      { return nil }

func received(seq uint64, from, dmarc, subject string) mail.Received {
	return mail.Received{MarkKey: "INBOX#7", Seq: seq, Envelope: mail.Envelope{
		ID: "7/" + subject, From: from, Subject: subject, Body: "Text " + subject,
		Auth: mail.AuthResults{DMARC: dmarc},
	}}
}

func mailWatch(cursor string) inboundSubscription {
	return inboundSubscription{ID: "w-1", ConnectorID: "m1", MailFolder: "INBOX", LastEventID: cursor, MessageName: "mail.eingang"}
}

func TestMailSourcePassesTheMailboxMarksThrough(t *testing.T) {
	f := &fakeMailbox{pages: []mail.WatchPage{{Cursor: "7:12", Items: []mail.Received{received(12, "a@example.com", "pass", "eins")}}}}
	rec := mailWatch("7:11")
	rec.IncludeBody = true
	events, cursor, err := mailSource{mb: f}.Read(context.Background(), rec, 256)
	if err != nil || len(events) != 1 || cursor != "7:12" {
		t.Fatalf("Read = %d events, cursor %q, %v", len(events), cursor, err)
	}
	if events[0].MarkKey != "INBOX#7" || events[0].Seq != 12 {
		t.Errorf("mark = %q/%d: the mailbox's own mark must reach the engine unchanged", events[0].MarkKey, events[0].Seq)
	}
	fields := events[0].Fields
	if fields["eventType"] != "mail.received" || fields["from"] != "a@example.com" || fields["body"] != "Text eins" {
		t.Errorf("fields = %v", fields)
	}
	asked := f.asked[0]
	if asked.Cursor != "7:11" || asked.Folder != "INBOX" || !asked.IncludeBody || asked.Limit != mailWatchBatch {
		t.Errorf("asked = %+v; the bridge's cap of 256 must be held to the mail batch", asked)
	}
	rec.IncludeBody = false
	f.pages = []mail.WatchPage{{Cursor: "7:13", Items: []mail.Received{received(13, "a@example.com", "pass", "zwei")}}}
	events, _, _ = mailSource{mb: f}.Read(context.Background(), rec, 10)
	if _, ok := events[0].Fields["body"]; ok {
		t.Error("a body reached the event on a watch that did not ask for it")
	}
}

func TestMailSourceAdmitsOnlyTheSendersTheWatchAllows(t *testing.T) {
	rec := mailWatch("c")
	rec.AllowedSenders = []string{"@example.com"}
	rec.RequireDmarcPass = true
	f := &fakeMailbox{pages: []mail.WatchPage{{Cursor: "c2", Items: []mail.Received{
		received(1, "anna@example.com", "pass", "erlaubt"),
		received(2, "mallory@evil.example", "pass", "fremd"),
		received(3, "anna@example.com", "fail", "gefaelscht"),
		received(4, "anna@example.com", "", "ungeprueft"),
	}}}}
	events, cursor, err := mailSource{mb: f}.Read(context.Background(), rec, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Fields["subject"] != "erlaubt" {
		t.Errorf("admitted = %v; only an allowed sender with a DMARC pass may start a process", events)
	}
	if cursor != "c2" {
		t.Errorf("cursor = %q; refused mail is consumed, not deferred", cursor)
	}
}

func TestMailSourceErrorsAndPriming(t *testing.T) {
	f := &fakeMailbox{err: errors.New("imap down")}
	if _, _, err := (mailSource{mb: f}).Read(context.Background(), mailWatch(""), 10); err == nil {
		t.Error("a read failure was swallowed")
	}
	if _, _, err := (mailSource{mb: f}).Prime(context.Background(), mailWatch("")); err == nil {
		t.Error("a prime failure was swallowed")
	}
	f = &fakeMailbox{tip: "7:99"}
	cursor, done, err := mailSource{mb: f}.Prime(context.Background(), mailWatch(""))
	if err != nil || !done || cursor != "7:99" || f.tipped[0] != "INBOX" {
		t.Errorf("Prime = %q, %v, %v (asked %v)", cursor, done, err, f.tipped)
	}
	// A gap is logged and its cursor still returned, so the watch moves on.
	f = &fakeMailbox{pages: []mail.WatchPage{{Cursor: "8:0", Gap: "renumbered"}}}
	events, cursor, err := mailSource{mb: f}.Read(context.Background(), mailWatch("7:5"), 10)
	if err != nil || len(events) != 0 || cursor != "8:0" {
		t.Errorf("gap page = %d events, cursor %q, %v", len(events), cursor, err)
	}
}

func TestValidateMailWatch(t *testing.T) {
	rec := inboundSubscription{AllowedSenders: []string{" Anna@Example.com ", "example.org"}}
	if msg := validateInboundWatch(connectorKindMail, &rec); msg != "" {
		t.Fatalf("a plain mail watch refused: %s", msg)
	}
	if rec.MailFolder != "INBOX" || strings.Join(rec.AllowedSenders, ",") != "anna@example.com,@example.org" {
		t.Errorf("normalized = folder %q, senders %v", rec.MailFolder, rec.AllowedSenders)
	}
	for name, bad := range map[string]inboundSubscription{
		"subject":       {WatchedSubject: "x"},
		"jql":           {JQL: "project = X"},
		"channel":       {ChannelID: "42"},
		"drive folder":  {FolderID: "f"},
		"cursor field":  {CursorField: "created"},
		"lag":           {LagSeconds: 5},
		"negative poll": {PollSeconds: -1},
		"bad sender":    {AllowedSenders: []string{"not an address"}},
	} {
		b := bad
		if msg := validateInboundWatch(connectorKindMail, &b); msg == "" {
			t.Errorf("%s: a mail watch carrying it was accepted", name)
		}
	}
	// And the other way round: the mail fields on another kind's watch.
	for _, kind := range []string{connectorKindClio, connectorKindJira, connectorKindDiscord, connectorKindGoogleSheets} {
		rec := inboundSubscription{WatchedSubject: "s", JQL: "project = X", ChannelID: "42", SpreadsheetID: "s", IncludeBody: true}
		if msg := validateInboundWatch(kind, &rec); !strings.Contains(msg, "belong to a mail watch") {
			t.Errorf("%s watch with includeBody = %q", kind, msg)
		}
	}
}

func TestDescribeAndPaceAMailWatch(t *testing.T) {
	if got := describeInboundWatch(connectorKindMail, inboundSubscription{}); got != "new mail in folder INBOX" {
		t.Errorf("describe = %q", got)
	}
	if got := describeInboundWatch(connectorKindMail, inboundSubscription{MailFolder: "Rechnungen", AllowedSenders: []string{"@lieferant.ch"}}); got != "new mail in folder Rechnungen from @lieferant.ch" {
		t.Errorf("describe = %q", got)
	}
	if got := inboundCadence(connectorKindMail, inboundSubscription{}); got != mailDefaultPoll {
		t.Errorf("cadence = %v, want the mail default", got)
	}
}

func TestResolveInboundSubsBuildsAMailSourceOnlyForAMailbox(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "m1", Name: "postfach", Kind: connectorKindMail, Provider: "smtp", Sender: "a@b.ch", Enabled: true, CreatedAt: 1})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "s-m", ConnectorID: "m1", MailFolder: "INBOX", MessageName: "m", Enabled: true, CreatedAt: 1})
	})
	// A sender only — no mailbox behind it — resolves to nothing.
	srv.do(func() {
		srv.mailRegistry.Replace(map[string]mail.Client{"postfach": mail.NewSMTPClient(mail.Connector{Endpoint: "smtp.example.com:587"})})
	})
	var got []pendingSub
	srv.do(func() { got = srv.resolveInboundSubs() })
	if len(got) != 0 {
		t.Fatalf("a sender-only worker resolved %d watches", len(got))
	}
	srv.do(func() { srv.mailRegistry.Replace(map[string]mail.Client{"postfach": &fakeMailbox{}}) })
	srv.do(func() { got = srv.resolveInboundSubs() })
	if len(got) != 1 {
		t.Fatalf("resolved %d watches, want the mail watch", len(got))
	}
	if _, ok := got[0].source.(mailSource); !ok || got[0].kind != connectorKindMail {
		t.Errorf("source = %T kind %q", got[0].source, got[0].kind)
	}
}

const mailStartBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <message id="Mstart" name="mail.eingang"/>
  <message id="Mnever" name="never"/>
  <process id="onMail" isExecutable="true">
    <startEvent id="s"><messageEventDefinition messageRef="Mstart"/></startEvent>
    <intermediateCatchEvent id="park"><messageEventDefinition messageRef="Mnever"/></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="park"/>
    <sequenceFlow id="f2" sourceRef="park" targetRef="e"/>
  </process>
</definitions>`

// TestMailWatchStartsAProcessEndToEnd is the slice through every layer above the
// mailbox: the watch is created over HTTP, primes to the tip, publishes the one
// admitted message as an Atlas message that starts a process carrying the envelope,
// and moves past a page of refused mail without publishing anything.
func TestMailWatchStartsAProcessEndToEnd(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	x := deployTestHarness{t, srv.Handler()}
	pid := x.mkProject("Posteingang")
	x.saveDraft(pid, mailStartBPMN)
	if code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	code, cb := x.do(http.MethodPost, "/api/v1/connectors", `{"name":"postfach","kind":"mail","provider":"smtp",`+
		`"endpoint":"smtp.example.com","sender":"desk@example.com","mailboxEndpoint":"imap.example.com"}`)
	if code != http.StatusOK {
		t.Fatalf("create worker: %d %s", code, cb)
	}
	var conn connector
	_ = json.Unmarshal(cb, &conn)
	if conn.MailboxEndpoint != "imaps://imap.example.com:993" {
		t.Errorf("mailboxEndpoint = %q, want it normalized", conn.MailboxEndpoint)
	}
	code, sb := x.do(http.MethodPost, "/api/v1/connectors/"+conn.ID+"/inbound-subscriptions",
		`{"messageName":"mail.eingang","allowedSenders":["Example.com"],"requireDmarcPass":true,"includeBody":true}`)
	if code != http.StatusOK {
		t.Fatalf("create watch: %d %s", code, sb)
	}
	var sub inboundSubscription
	_ = json.Unmarshal(sb, &sub)
	if sub.MailFolder != "INBOX" || strings.Join(sub.AllowedSenders, ",") != "@example.com" || !sub.StartFromTip {
		t.Errorf("stored watch = %+v", sub)
	}

	fake := &fakeMailbox{tip: "7:10", pages: []mail.WatchPage{
		{Cursor: "7:12", Items: []mail.Received{
			received(11, "kunde@example.com", "pass", "Bestellung"),
			received(12, "spam@evil.example", "pass", "Gewinn"),
		}},
		{Cursor: "7:14", Items: []mail.Received{
			received(13, "spam@evil.example", "pass", "nochmal"),
			received(14, "kunde@example.com", "fail", "gefaelscht"),
		}},
	}}
	srv.do(func() { srv.mailRegistry.Replace(map[string]mail.Client{"postfach": fake}) })

	srv.pollInbound(context.Background()) // primes: the mailbox's history starts nothing
	if n := activeInstances(t, srv); n != 0 {
		t.Fatalf("priming started %d instances", n)
	}
	srv.inboundClock = nil
	srv.do(func() { srv.markInboundPolled(sub.ID, 0) })
	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 1 {
		t.Fatalf("active = %d, want 1: one admitted message, one refused", n)
	}
	vars := soleInstanceVars(t, srv)
	if vars["subject"] != "Bestellung" || vars["from"] != "kunde@example.com" || vars["body"] != "Text Bestellung" {
		t.Errorf("instance variables = %v", vars)
	}
	if fake.asked[0].Cursor != "7:10" {
		t.Errorf("the first read resumed from %q, want the primed tip", fake.asked[0].Cursor)
	}

	srv.do(func() { srv.markInboundPolled(sub.ID, 0) })
	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 1 {
		t.Fatalf("a page of refused mail started something: active = %d", n)
	}
	var stored inboundSubscription
	srv.do(func() { stored, _, _ = srv.inboundSubs.Get(sub.ID) })
	if stored.LastEventID != "7:14" {
		t.Errorf("cursor = %q, want 7:14: a page the watch refused whole must still be consumed", stored.LastEventID)
	}
}

// soleInstanceVars reads the root-scope variables of the one active instance, as the
// stored text of each value.
func soleInstanceVars(t *testing.T, srv *Server) map[string]string {
	t.Helper()
	out := map[string]string{}
	srv.do(func() {
		_ = srv.store.ActiveProcessInstances(func(key uint64, _ *model.ProcessInstanceValue) error {
			return srv.store.VariablesOfScope(key, func(v *model.VariableValue) error {
				out[v.Name] = v.Text
				return nil
			})
		})
	})
	return out
}

func TestAGmailWatchCannotBackfill(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	x := deployTestHarness{t, srv.Handler()}
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "g1", Name: "gpost", Kind: connectorKindMail, Provider: "gmail",
			Sender: "a@b.ch", CredentialsRef: "r", Enabled: true, CreatedAt: 1})
	})
	code, b := x.do(http.MethodPost, "/api/v1/connectors/g1/inbound-subscriptions", `{"messageName":"m","startFromTip":false}`)
	if code != http.StatusBadRequest || !strings.Contains(string(b), "cannot backfill") {
		t.Fatalf("= %d %s, want a refusal that says why", code, b)
	}
	if code, b := x.do(http.MethodPost, "/api/v1/connectors/g1/inbound-subscriptions", `{"messageName":"m"}`); code != http.StatusOK {
		t.Fatalf("a forward-only Gmail watch = %d %s", code, b)
	}
}

func TestUpdatingAMailWatchsPolicy(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	x := deployTestHarness{t, srv.Handler()}
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "m1", Name: "postfach", Kind: connectorKindMail, Provider: "smtp", Sender: "a@b.ch", Enabled: true, CreatedAt: 1})
		_ = srv.connectors.Save(connector{ID: "c1", Name: "ereignisse", Kind: connectorKindClio, Endpoint: "http://x", Enabled: true, CreatedAt: 1})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "w-m", ConnectorID: "m1", MailFolder: "INBOX", MessageName: "m", CreatedAt: 1})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "w-c", ConnectorID: "c1", WatchedSubject: "s", MessageName: "c", CreatedAt: 1})
	})
	code, b := x.do(http.MethodPatch, "/api/v1/inbound-subscriptions/w-m", `{"allowedSenders":["Chef@Firma.ch"],"requireDmarcPass":true,"includeBody":true}`)
	if code != http.StatusOK {
		t.Fatalf("update = %d %s", code, b)
	}
	var got inboundSubscription
	_ = json.Unmarshal(b, &got)
	if strings.Join(got.AllowedSenders, ",") != "chef@firma.ch" || !got.RequireDmarcPass || !got.IncludeBody {
		t.Errorf("updated = %+v", got)
	}
	if code, b := x.do(http.MethodPatch, "/api/v1/inbound-subscriptions/w-m", `{"allowedSenders":["kein absender"]}`); code != http.StatusBadRequest {
		t.Errorf("a malformed allow-list = %d %s", code, b)
	}
	if code, b := x.do(http.MethodPatch, "/api/v1/inbound-subscriptions/w-c", `{"includeBody":true}`); code != http.StatusBadRequest {
		t.Errorf("a mail field on a clio watch = %d %s", code, b)
	}
}

// The bridge stores a cursor a source moved without publishing anything — and for
// every source that answers no cursor on an empty page, nothing changes.
func TestTheBridgeKeepsACursorAnEmptyPageMoved(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "m1", Name: "postfach", Kind: connectorKindMail, Provider: "smtp", Sender: "a@b.ch", Enabled: true, CreatedAt: 1})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "w", ConnectorID: "m1", MailFolder: "INBOX", MessageName: "m",
			Enabled: true, Primed: true, StartFromTip: true, LastEventID: "7:3", CreatedAt: 1})
	})
	fake := &fakeMailbox{pages: []mail.WatchPage{{Cursor: "9:0", Gap: "renumbered"}, {}}}
	srv.do(func() { srv.mailRegistry.Replace(map[string]mail.Client{"postfach": fake}) })
	srv.pollInbound(context.Background())
	var rec inboundSubscription
	srv.do(func() { rec, _, _ = srv.inboundSubs.Get("w") })
	if rec.LastEventID != "9:0" {
		t.Fatalf("cursor = %q, want the one the empty page moved to", rec.LastEventID)
	}
	srv.do(func() { srv.markInboundPolled("w", 0) })
	srv.pollInbound(context.Background())
	srv.do(func() { rec, _, _ = srv.inboundSubs.Get("w") })
	if rec.LastEventID != "9:0" {
		t.Fatalf("an empty page with no cursor moved the cursor to %q", rec.LastEventID)
	}
}

// The IMAP endpoint is SMTP's alone: normalized there, and cleared — not refused — for
// a provider that reads through its own API or has no mailbox, so switching a Worker
// away from SMTP needs no second edit.
func TestTheIMAPEndpointBelongsToSMTP(t *testing.T) {
	p := createConnectorParams{Name: "a", Kind: connectorKindMail, Provider: "smtp", Endpoint: "smtp.example.com",
		Sender: "a@b.ch", MailboxEndpoint: "imap.example.com:143"}
	if msg := validateMailConnector(&p); msg != "" || p.MailboxEndpoint != "imap://imap.example.com:143" {
		t.Fatalf("smtp = %q, %q", msg, p.MailboxEndpoint)
	}
	p.MailboxEndpoint = "pop3://x"
	if msg := validateMailConnector(&p); !strings.Contains(msg, "pop3") {
		t.Errorf("a bad IMAP endpoint = %q", msg)
	}
	for _, provider := range []string{"gmail", "microsoft", "preview"} {
		q := createConnectorParams{Name: "a", Kind: connectorKindMail, Provider: provider, Sender: "a@b.ch",
			CredentialsRef: "r", MailboxEndpoint: "imap.example.com"}
		if msg := validateMailConnector(&q); msg != "" || q.MailboxEndpoint != "" {
			t.Errorf("%s = %q, mailboxEndpoint %q; want it cleared", provider, msg, q.MailboxEndpoint)
		}
	}
	rec := connector{Name: "a", Kind: connectorKindMail, Provider: "gmail", Sender: "a@b.ch", CredentialsRef: "r",
		MailboxEndpoint: "imaps://imap.example.com:993"}
	if msg := normalizeConnectorUpdate(&rec); msg != "" || rec.MailboxEndpoint != "" {
		t.Errorf("switching to gmail = %q, %q", msg, rec.MailboxEndpoint)
	}
}
