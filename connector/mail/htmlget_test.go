package mail_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// htmlMailbox is a mailbox client that reads HTML bodies too, as every provider does.
type htmlMailbox struct {
	mailboxClient
	html string
}

func (c *htmlMailbox) GetHTML(_ context.Context, id string) (mail.Envelope, error) {
	c.calls = append(c.calls, "get-html "+id)
	return mail.Envelope{ID: id, From: "anna@example.com", Body: "Hallo", HTML: c.html}, c.fail
}

// TestGetWithHTMLEndToEnd: a get with includeBody="html" resolves to a job asking for the
// HTML body, and the variable it completes with carries it (ADR-0489).
func TestGetWithHTMLEndToEnd(t *testing.T) {
	log, store := openStore(t)
	target, err := expr.CompileAuto(`mail.messageId`)
	if err != nil {
		t.Fatal(err)
	}
	cp, jobType := mailProcess(t, compiler.MailConfig{
		Connector: "box", Operation: mail.OpGet, Message: compiler.RestExpr{Expr: target},
		IncludeBody: true, IncludeHTML: true, ResultVar: "fetched", Retries: 3,
	})
	var resolved mail.Job
	driveResolving(t, cp, jobType, store, log, func(ei *model.ElementInstanceValue, eik, jobKey uint64, detail *compiler.ConnectorTaskDetail) {
		if resolved, err = mail.Resolve(store, cp, detail, ei, eik, jobKey, nil); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	}, model.VariableValue{Name: "mail", Kind: model.VarJSON, Text: `{"messageId":"m-9"}`})
	if !resolved.IncludeHTML || !resolved.IncludeBody || resolved.Target != "m-9" {
		t.Fatalf("resolved = %+v; want a get of m-9 with text and HTML", resolved)
	}

	c := &htmlMailbox{html: "<p>Hallo</p>"}
	res, err := mail.Run(context.Background(), resolved, registryWith(c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(c.calls, "|") != "get-html m-9" {
		t.Fatalf("calls = %v", c.calls)
	}
	v := mail.ResultVariable("fetched", res)
	var env map[string]any
	if err := json.Unmarshal([]byte(v.Text), &env); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if env["body"] != "Hallo" || env["html"] != "<html><head></head><body><p>Hallo</p></body></html>" || env["htmlTruncated"] != false {
		t.Errorf("envelope = %v", env)
	}
}

// A mailbox that cannot read HTML refuses the get rather than answering without it.
func TestGetWithHTMLNeedsAnHTMLMailbox(t *testing.T) {
	_, err := mail.Run(context.Background(), mail.Job{Connector: "box", Operation: mail.OpGet, Target: "m", IncludeBody: true, IncludeHTML: true},
		registryWith(&mailboxClient{}))
	if err == nil || !strings.Contains(err.Error(), "HTML") {
		t.Errorf("Run = %v; want a refusal naming the HTML body", err)
	}
}
