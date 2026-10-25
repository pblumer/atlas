package mail_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/expr/feelers"
	"github.com/pblumer/atlas/model"
)

func template(t *testing.T, src string) *feelers.Template {
	t.Helper()
	tpl, err := feelers.Compile(src, expr.CompileAuto)
	if err != nil {
		t.Fatalf("compile template %q: %v", src, err)
	}
	return tpl
}

// TestMailConnectorTemplates is the send end to end with templates
// (ADR-0487): the subject and both bodies render over the variables the
// task sees, the HTML body escapes what a placeholder inserts and keeps its own markup,
// and a subject a section broke over two lines goes out as one.
func TestMailConnectorTemplates(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := mailProcess(t, compiler.MailConfig{
		Connector:        "office365",
		To:               compiler.RestExpr{Literal: "ops@example.com"},
		SubjectTemplate:  template(t, "Bestellung {{order.id}}\n{{#if order.vip}}(VIP){{/if}}"),
		BodyTemplate:     template(t, "Hallo {{customer}}\n{{#loop order.items}}\n- {{qty}} × {{name}}\n{{/loop}}Fall {{processInstanceKey}}"),
		BodyHTMLTemplate: template(t, `<p>Hallo <b>{{customer}}</b></p>`),
		Retries:          3,
	})
	rc := &recordingClient{}
	reg := mail.NewRegistry()
	reg.Register("office365", rc)

	if err := drive(t, cp, jobType, reg, store, log,
		model.VariableValue{Name: "customer", Kind: model.VarString, Text: "<Anna & Co>"},
		model.VariableValue{Name: "order", Kind: model.VarJSON, Text: `{"id":"42","vip":true,"items":[{"name":"Tee","qty":2},{"name":"Kaffee","qty":1}]}`},
	); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.sent) != 1 {
		t.Fatalf("messages sent = %d, want 1", len(rc.sent))
	}
	m := rc.sent[0]
	if m.Subject != "Bestellung 42 (VIP)" {
		t.Errorf("Subject = %q, want the rendered subject on one line", m.Subject)
	}
	if want := "Hallo <Anna & Co>\n- 2 × Tee\n- 1 × Kaffee\nFall "; len(m.Body) <= len(want) || m.Body[:len(want)] != want {
		t.Errorf("Body = %q, want it to start with %q and end with the instance key", m.Body, want)
	}
	if m.HTML != `<p>Hallo <b>&lt;Anna &amp; Co&gt;</b></p>` {
		t.Errorf("HTML = %q, want the variable escaped and the template's markup kept", m.HTML)
	}
}

// A reply's body is a template like a send's: Resolve renders it for the mailbox
// operation path too.
func TestMailReplyRendersItsTemplate(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := mailProcess(t, compiler.MailConfig{
		Connector:        "office365",
		Operation:        mail.OpReply,
		Message:          compiler.RestExpr{Literal: "m1"},
		BodyTemplate:     template(t, "Danke, {{name}}."),
		BodyHTMLTemplate: template(t, "<p>Danke, {{this.name}}.</p>"), // this: the whole scope, as in feelers
		Retries:          3,
	})
	var j mail.Job
	driveResolving(t, cp, jobType, store, log, func(ei *model.ElementInstanceValue, eik, jobKey uint64, detail *compiler.ConnectorTaskDetail) {
		var err error
		if j, err = mail.Resolve(store, cp, detail, ei, eik, jobKey, nil); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	}, model.VariableValue{Name: "name", Kind: model.VarString, Text: "Ada <ada@example.ch>"})
	if j.Body != "Danke, Ada <ada@example.ch>." {
		t.Errorf("Body = %q", j.Body)
	}
	if j.HTML != "<p>Danke, Ada &lt;ada@example.ch&gt;.</p>" {
		t.Errorf("HTML = %q", j.HTML)
	}
}
