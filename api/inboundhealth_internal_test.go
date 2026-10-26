package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/pblumer/atlas/connector/mail"
)

// listedWatch reads one watch back through the listing route, the way the Console and
// an operator see it.
func listedWatch(t *testing.T, x deployTestHarness, workerID, subID string) inboundSubView {
	t.Helper()
	code, b := x.do(http.MethodGet, "/api/v1/connectors/"+workerID+"/inbound-subscriptions", "")
	if code != http.StatusOK {
		t.Fatalf("list watches: %d %s", code, b)
	}
	var subs []inboundSubView
	if err := json.Unmarshal(b, &subs); err != nil {
		t.Fatalf("decode watches: %v (%s)", err, b)
	}
	for _, s := range subs {
		if s.ID == subID {
			return s
		}
	}
	t.Fatalf("watch %s not listed in %s", subID, b)
	return inboundSubView{}
}

// A watch on a mail Worker that cannot read a mailbox would be accepted and then never
// read anything, without a word to anyone. It is refused where somebody is still there
// to read why.
func TestAMailWatchIsRefusedOnAWorkerWithNoMailbox(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	x := deployTestHarness{t, srv.Handler()}
	worker := func(body string) string {
		t.Helper()
		code, b := x.do(http.MethodPost, "/api/v1/connectors", body)
		if code != http.StatusOK {
			t.Fatalf("create worker: %d %s", code, b)
		}
		var c connector
		_ = json.Unmarshal(b, &c)
		return c.ID
	}
	sender := worker(`{"name":"absender","kind":"mail","provider":"smtp","endpoint":"smtp.example.com","sender":"desk@example.com"}`)
	preview := worker(`{"name":"vorschau","kind":"mail","provider":"preview","sender":"desk@example.com"}`)
	reader := worker(`{"name":"postfach","kind":"mail","provider":"smtp","endpoint":"smtp.example.com",` +
		`"sender":"desk@example.com","mailboxEndpoint":"imap.example.com"}`)

	for name, tc := range map[string]struct{ id, want string }{
		"smtp without IMAP": {sender, "IMAP endpoint"},
		"preview":           {preview, "preview"},
	} {
		code, b := x.do(http.MethodPost, "/api/v1/connectors/"+tc.id+"/inbound-subscriptions", `{"messageName":"mail.eingang"}`)
		if code != http.StatusBadRequest || !strings.Contains(string(b), tc.want) {
			t.Errorf("%s: watch = %d %s; want a 400 naming %q", name, code, b, tc.want)
		}
	}
	var subs []inboundSubscription
	srv.do(func() { subs, _ = srv.inboundSubs.LoadAll() })
	if len(subs) != 0 {
		t.Fatalf("a refused watch was stored: %+v", subs)
	}
	if code, b := x.do(http.MethodPost, "/api/v1/connectors/"+reader+"/inbound-subscriptions", `{"messageName":"mail.eingang"}`); code != http.StatusOK {
		t.Fatalf("a watch on a Worker with an IMAP endpoint = %d %s", code, b)
	}
}

// The configuration check and the built client must give the same answer: a Worker the
// create route admits is one the bridge can read, and the reverse.
func TestTheMailboxCheckAgreesWithTheBuiltClient(t *testing.T) {
	for _, tc := range []struct {
		provider, sender, imap string
		reads                  bool
	}{
		{"smtp", "a@b.ch", "", false},
		{"", "a@b.ch", "", false},
		{"smtp", "a@b.ch", "imaps://imap.example.com:993", true},
		{"preview", "a@b.ch", "", false},
		{"microsoft", "a@b.ch", "", true},
		{"microsoft", "", "", false},
		{"gmail", "a@b.ch", "", true},
	} {
		err := mail.CanReadMailbox(tc.provider, tc.sender, tc.imap)
		if (err == nil) != tc.reads {
			t.Errorf("%s sender %q imap %q: CanReadMailbox = %v, want reads=%v", tc.provider, tc.sender, tc.imap, err, tc.reads)
		}
	}
	// The SMTP refusal is the very error the built client answers.
	built := mail.NewSMTPClient(mail.Connector{Endpoint: "smtp.example.com:587"})
	_, fromClient := mail.MailboxOf(built)
	fromConfig := mail.CanReadMailbox("smtp", "a@b.ch", "")
	if fromClient == nil || fromConfig == nil || fromClient.Error() != fromConfig.Error() {
		t.Errorf("client says %v, configuration says %v; one reason, said once", fromClient, fromConfig)
	}
}

// A watch whose mailbox refuses it says so, from when, and stops saying so the moment a
// read succeeds — through priming and through a read after it.
func TestAWatchThatCannotReadSaysWhy(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	srv.inboundClock = func() time.Time { return now }
	x := deployTestHarness{t, srv.Handler()}
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "m1", Name: "postfach", Kind: connectorKindMail, Provider: "smtp", Sender: "a@b.ch", Enabled: true, CreatedAt: 1})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "w", ConnectorID: "m1", MailFolder: "INBOX", MessageName: "m",
			Enabled: true, StartFromTip: true, CreatedAt: 1})
	})
	fake := &fakeMailbox{err: errors.New("imap: login refused: AUTHENTICATIONFAILED")}
	srv.do(func() { srv.mailRegistry.Replace(map[string]mail.Client{"postfach": fake}) })

	// Priming fails: the watch has never read anything, and now it says why.
	srv.pollInbound(context.Background())
	got := listedWatch(t, x, "m1", "w")
	if !strings.Contains(got.LastError, "AUTHENTICATIONFAILED") || got.FailingSince != now.Unix() {
		t.Fatalf("after a failed prime: lastError %q, failingSince %d", got.LastError, got.FailingSince)
	}

	// Still failing a minute later, for another reason: the reason is the newest, the
	// start of the streak is kept.
	first := now
	now = now.Add(time.Minute)
	fake.err = errors.New("imap: dial imap.example.com:993: i/o timeout")
	srv.pollInbound(context.Background())
	got = listedWatch(t, x, "m1", "w")
	if !strings.Contains(got.LastError, "i/o timeout") || got.FailingSince != first.Unix() {
		t.Fatalf("still failing: lastError %q, failingSince %d, want the streak's start %d", got.LastError, got.FailingSince, first.Unix())
	}

	// The mailbox answers: primed, and the failure is gone.
	fake.err, fake.tip = nil, "7:10"
	now = now.Add(time.Minute)
	srv.pollInbound(context.Background())
	got = listedWatch(t, x, "m1", "w")
	if got.LastError != "" || got.FailingSince != 0 || !got.Primed {
		t.Fatalf("after a good prime: lastError %q, failingSince %d, primed %v", got.LastError, got.FailingSince, got.Primed)
	}

	// A read after priming fails the same way, and recovers the same way.
	fake.err = errors.New("imap: EXAMINE INBOX: NO mailbox unavailable")
	now = now.Add(time.Minute)
	srv.pollInbound(context.Background())
	if got = listedWatch(t, x, "m1", "w"); !strings.Contains(got.LastError, "mailbox unavailable") {
		t.Fatalf("after a failed read: lastError %q", got.LastError)
	}
	fake.err = nil
	now = now.Add(time.Minute)
	srv.pollInbound(context.Background())
	if got = listedWatch(t, x, "m1", "w"); got.LastError != "" {
		t.Fatalf("after a good read: lastError %q", got.LastError)
	}
}

// A watch the bridge cannot even start reading — its Worker cannot read a mailbox, is
// switched off, or has no client — says that too, instead of being skipped in silence.
func TestAWatchWhoseWorkerCannotServeItSaysWhy(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	x := deployTestHarness{t, srv.Handler()}
	srv.do(func() {
		// Saved straight into the store: a watch from before the create route refused
		// one, or a Worker whose IMAP endpoint was removed after its watch was made.
		_ = srv.connectors.Save(connector{ID: "m1", Name: "postfach", Kind: connectorKindMail, Provider: "smtp", Sender: "a@b.ch", Enabled: true, CreatedAt: 1})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "w", ConnectorID: "m1", MailFolder: "INBOX", MessageName: "m",
			Enabled: true, StartFromTip: true, CreatedAt: 1})
		srv.mailRegistry.Replace(map[string]mail.Client{"postfach": mail.NewSMTPClient(mail.Connector{Endpoint: "smtp.example.com:587"})})
	})
	srv.pollInbound(context.Background())
	if got := listedWatch(t, x, "m1", "w"); !strings.Contains(got.LastError, "no IMAP endpoint") {
		t.Errorf("a sender-only Worker: lastError %q", got.LastError)
	}

	// No client at all for the Worker's name.
	srv.do(func() { srv.mailRegistry.Replace(map[string]mail.Client{}) })
	srv.pollInbound(context.Background())
	if got := listedWatch(t, x, "m1", "w"); !strings.Contains(got.LastError, "postfach") {
		t.Errorf("a Worker with no client: lastError %q, want it to name the Worker", got.LastError)
	}

	// The Worker switched off.
	srv.do(func() {
		c, _, _ := srv.connectors.Get("m1")
		c.Enabled = false
		_ = srv.connectors.Save(c)
	})
	srv.pollInbound(context.Background())
	if got := listedWatch(t, x, "m1", "w"); !strings.Contains(got.LastError, "switched off") {
		t.Errorf("a disabled Worker: lastError %q", got.LastError)
	}

	// A watch switched off reads nothing on purpose; a failure it had is no longer news.
	srv.do(func() {
		rec, _, _ := srv.inboundSubs.Get("w")
		rec.Enabled = false
		_ = srv.inboundSubs.Save(rec)
	})
	srv.pollInbound(context.Background())
	if got := listedWatch(t, x, "m1", "w"); got.LastError != "" || got.FailingSince != 0 {
		t.Errorf("a disabled watch still reports %q since %d", got.LastError, got.FailingSince)
	}
}

// The failure state is runtime only and keyed by watch: a deleted watch leaves nothing
// behind, and nothing of it is written into the stored record.
func TestAWatchsFailureIsNeitherStoredNorKeptAfterItIsGone(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	x := deployTestHarness{t, srv.Handler()}
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "m1", Name: "postfach", Kind: connectorKindMail, Provider: "smtp", Sender: "a@b.ch", Enabled: true, CreatedAt: 1})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "w", ConnectorID: "m1", MailFolder: "INBOX", MessageName: "m",
			Enabled: true, StartFromTip: true, CreatedAt: 1})
		srv.mailRegistry.Replace(map[string]mail.Client{"postfach": &fakeMailbox{err: errors.New("imap down")}})
	})
	srv.pollInbound(context.Background())
	if _, failing := srv.inboundHealth.failure("w"); !failing {
		t.Fatal("the failing watch recorded nothing")
	}
	var stored inboundSubscription
	srv.do(func() { stored, _, _ = srv.inboundSubs.Get("w") })
	raw, _ := json.Marshal(stored)
	if strings.Contains(string(raw), "imap down") {
		t.Errorf("the failure reached the stored record: %s", raw)
	}
	if code, b := x.do(http.MethodDelete, "/api/v1/inbound-subscriptions/w", ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", code, b)
	}
	srv.pollInbound(context.Background())
	if f, failing := srv.inboundHealth.failure("w"); failing {
		t.Errorf("a deleted watch is still reported failing: %+v", f)
	}
}

// The Modeler's message panel and the MCP tool ask "what feeds this name" of the
// message sources, not of a Worker's watch list. A failing watch feeds its name as
// little as a disabled one, so it says so there too.
func TestAFailingWatchSaysSoWhereItsMessageNameIsListed(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	srv.inboundClock = func() time.Time { return now }
	x := deployTestHarness{t, srv.Handler()}
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "m1", Name: "postfach", Kind: connectorKindMail, Provider: "smtp", Sender: "a@b.ch", Enabled: true, CreatedAt: 1})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "w", ConnectorID: "m1", MailFolder: "INBOX", MessageName: "mail.eingang",
			Enabled: true, StartFromTip: true, CreatedAt: 1})
		srv.mailRegistry.Replace(map[string]mail.Client{"postfach": &fakeMailbox{err: errors.New("imap: login refused")}})
	})
	sourceRow := func() messageSourceView {
		t.Helper()
		code, b := x.do(http.MethodGet, "/api/v1/message-sources", "")
		var rows []messageSourceView
		if code != http.StatusOK || json.Unmarshal(b, &rows) != nil {
			t.Fatalf("message sources: %d %s", code, b)
		}
		for _, r := range rows {
			if r.SourceKind == sourceInboundWatch && r.MessageName == "mail.eingang" {
				return r
			}
		}
		t.Fatalf("no inbound-watch row in %s", b)
		return messageSourceView{}
	}
	if r := sourceRow(); r.LastError != "" {
		t.Fatalf("a watch nothing has read yet reports %q", r.LastError)
	}
	srv.pollInbound(context.Background())
	if r := sourceRow(); !strings.Contains(r.LastError, "login refused") || r.FailingSince != now.Unix() || !r.Enabled {
		t.Errorf("row = %+v; want the watch still on, and why it is not reading", r)
	}
}

// A reason is a line for a person: a provider's error page is cut, on a rune boundary,
// and says that it was.
func TestAWatchsReasonIsBounded(t *testing.T) {
	long := strings.Repeat("ä", maxInboundError) // two bytes each
	got := clipInboundError(long)
	if !strings.HasSuffix(got, "…") || len(got) > maxInboundError+len("…") || !utf8.ValidString(got) {
		t.Errorf("clipped to %d bytes, valid %v, marked %v", len(got), utf8.ValidString(got), strings.HasSuffix(got, "…"))
	}
	if short := "imap down"; clipInboundError(short) != short {
		t.Errorf("a short reason was changed: %q", clipInboundError(short))
	}
}
