package mail_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// mailboxClient is a mail Worker client that is also a mailbox, recording what it was
// asked to do.
type mailboxClient struct {
	calls []string
	fail  error
	moved string
}

func (c *mailboxClient) Send(context.Context, mail.Message) error {
	c.calls = append(c.calls, "send")
	return c.fail
}
func (c *mailboxClient) WatchTip(context.Context, string) (string, error) {
	return "", nil
}
func (c *mailboxClient) WatchSince(context.Context, mail.WatchRequest) (mail.WatchPage, error) {
	return mail.WatchPage{}, nil
}
func (c *mailboxClient) List(_ context.Context, r mail.ListRequest) ([]mail.Envelope, error) {
	c.calls = append(c.calls, "list "+r.Folder)
	return []mail.Envelope{{ID: "a", Subject: "eins", Body: "x"}, {ID: "b"}}, c.fail
}
func (c *mailboxClient) Get(_ context.Context, id string, body bool) (mail.Envelope, error) {
	c.calls = append(c.calls, "get "+id)
	return mail.Envelope{ID: id, From: "anna@example.com", Body: "geheim",
		Attachments: []mail.Attachment{{Name: "R.pdf", Size: 12}}}, c.fail
}
func (c *mailboxClient) Move(_ context.Context, id, dest string) (string, error) {
	c.calls = append(c.calls, "move "+id+" "+dest)
	return c.moved, c.fail
}
func (c *mailboxClient) SetRead(_ context.Context, id string, read bool) error {
	if read {
		c.calls = append(c.calls, "read "+id)
	} else {
		c.calls = append(c.calls, "unread "+id)
	}
	return c.fail
}
func (c *mailboxClient) Delete(_ context.Context, id string) error {
	c.calls = append(c.calls, "delete "+id)
	return c.fail
}
func (c *mailboxClient) Reply(_ context.Context, id string, r mail.Reply) error {
	c.calls = append(c.calls, "reply "+id+" "+r.Body+" "+r.MessageID)
	return c.fail
}

func registryWith(c mail.Client) *mail.Registry {
	reg := mail.NewRegistry()
	reg.Register("box", c)
	return reg
}

func TestRunMailboxOperations(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		job   mail.Job
		call  string
		check func(t *testing.T, res any)
	}{
		{mail.Job{Operation: mail.OpList, Folder: "Archiv", IncludeBody: true}, "list Archiv", func(t *testing.T, res any) {
			list := res.([]any)
			if len(list) != 2 || list[0].(map[string]any)["body"] != "x" {
				t.Errorf("list = %v", res)
			}
		}},
		{mail.Job{Operation: mail.OpGet, Target: "m1"}, "get m1", func(t *testing.T, res any) {
			m := res.(map[string]any)
			if m["messageId"] != "m1" || m["from"] != "anna@example.com" {
				t.Errorf("get = %v", m)
			}
			if _, ok := m["body"]; ok {
				t.Error("a get without includeBody answered the body")
			}
		}},
		{mail.Job{Operation: mail.OpMove, Target: "m1", Destination: "Archiv"}, "move m1 Archiv", func(t *testing.T, res any) {
			if res != "m1-new" {
				t.Errorf("move = %v", res)
			}
		}},
		{mail.Job{Operation: mail.OpMarkRead, Target: "m1"}, "read m1", nil},
		{mail.Job{Operation: mail.OpMarkUnread, Target: "m1"}, "unread m1", nil},
		{mail.Job{Operation: mail.OpDelete, Target: "m1"}, "delete m1", nil},
		{mail.Job{Operation: mail.OpReply, Target: "m1", Body: "Danke", MessageID: "42"}, "reply m1 Danke 42", nil},
	} {
		c := &mailboxClient{moved: "m1-new"}
		tc.job.Connector = "box"
		res, err := mail.Run(ctx, tc.job, registryWith(c))
		if err != nil {
			t.Fatalf("%s: %v", tc.job.Operation, err)
		}
		if strings.Join(c.calls, "|") != tc.call {
			t.Errorf("%s: calls = %q, want %q", tc.job.Operation, c.calls, tc.call)
		}
		if tc.check != nil {
			tc.check(t, res)
		} else if res != nil {
			t.Errorf("%s answered %v; it has nothing to answer", tc.job.Operation, res)
		}
	}
}

func TestRunMailboxRefusals(t *testing.T) {
	ctx := context.Background()
	c := &mailboxClient{}
	if _, err := mail.Run(ctx, mail.Job{Connector: "box", Operation: mail.OpGet}, registryWith(c)); err == nil ||
		!strings.Contains(err.Error(), "messageId resolved to nothing") {
		t.Errorf("an empty target = %v", err)
	}
	if _, err := mail.Run(ctx, mail.Job{Connector: "box", Operation: mail.OpMove, Target: "m"}, registryWith(c)); err == nil {
		t.Error("a move without a destination ran")
	}
	if _, err := mail.Run(ctx, mail.Job{Connector: "box", Operation: "peek", Target: "m"}, registryWith(c)); err == nil {
		t.Error("an unknown operation ran")
	}
	if len(c.calls) != 0 {
		t.Errorf("a refused job reached the mailbox: %v", c.calls)
	}
	smtpOnly := mail.NewSMTPClient(mail.Connector{Endpoint: "smtp.example.com:587"})
	_, err := mail.Run(ctx, mail.Job{Connector: "box", Operation: mail.OpList}, registryWith(smtpOnly))
	if err == nil || !strings.Contains(err.Error(), `worker "box"`) || !strings.Contains(err.Error(), "IMAP") {
		t.Errorf("a sender-only worker = %v; it must name the worker and what is missing", err)
	}
	failing := &mailboxClient{fail: errors.New("provider says no")}
	if _, err := mail.Run(ctx, mail.Job{Connector: "box", Operation: mail.OpGet, Target: "m"}, registryWith(failing)); err == nil {
		t.Error("a provider failure was swallowed")
	}
	// A move whose new id the server does not say leaves the variable alone.
	c2 := &mailboxClient{}
	if res, err := mail.Run(ctx, mail.Job{Connector: "box", Operation: mail.OpMove, Target: "m", Destination: "d"}, registryWith(c2)); err != nil || res != nil {
		t.Errorf("an unknown new id = %v, %v", res, err)
	}
}

// TestMailboxOperationEndToEnd drives a get through the engine: the messageId is FEEL
// over the instance's variables, and the envelope comes back as the task's result.
func TestMailboxOperationEndToEnd(t *testing.T) {
	log, store := openStore(t)
	target, err := expr.CompileAuto(`mail.messageId`)
	if err != nil {
		t.Fatal(err)
	}
	cp, jobType := mailProcess(t, compiler.MailConfig{
		Connector: "box", Operation: mail.OpGet,
		Message: compiler.RestExpr{Expr: target}, IncludeBody: true, ResultVar: "fetched", Retries: 3,
	})
	c := &mailboxClient{}
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	var got []model.VariableValue
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		inner := mail.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, registryWith(c), nil)
		return func(j job.Job) ([]model.VariableValue, error) {
			vars, err := inner(j)
			got = append(got, vars...)
			return vars, err
		}
	})
	p.CreateInstance(cp.Key, model.VariableValue{Name: "mail", Kind: model.VarJSON, Text: `{"messageId":"m-77"}`})
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if strings.Join(c.calls, "|") != "get m-77" {
		t.Fatalf("calls = %v", c.calls)
	}
	if len(got) != 1 || got[0].Name != "fetched" || got[0].Kind != model.VarJSON {
		t.Fatalf("result = %+v", got)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(got[0].Text), &env); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if env["messageId"] != "m-77" || env["body"] != "geheim" {
		t.Errorf("envelope = %v", env)
	}
	if atts := env["attachments"].([]any); atts[0].(map[string]any)["size"] != float64(12) {
		t.Errorf("an attachment's size must arrive as a number: %v", atts)
	}
	if pi := mustActiveProcs(t, store); pi != 0 {
		t.Errorf("the instance did not complete: active=%d", pi)
	}
}

// TestMailOpsMatchTheConnector is the drift guard between this package's operations
// and the compiler's table: the compiler cannot import this package, which imports
// it, so the list exists twice.
func TestMailOpsMatchTheConnector(t *testing.T) {
	ours := mail.Operations()
	sort.Strings(ours)
	if strings.Join(ours, ",") != strings.Join(compiler.MailOperations(), ",") {
		t.Errorf("mail.Operations = %v, compiler.MailOperations = %v", ours, compiler.MailOperations())
	}
}
