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
	// doc is the attachment Attachment answers and Forward hands its inspection.
	doc mail.Document
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
func (c *mailboxClient) Attachment(_ context.Context, id, att string) (mail.Document, error) {
	c.calls = append(c.calls, "attachment "+id+" "+att)
	return c.doc, c.fail
}
func (c *mailboxClient) Forward(_ context.Context, id string, f mail.Forward) error {
	c.calls = append(c.calls, "forward "+id+" "+strings.Join(f.To, ",")+" "+f.Body+" "+f.MessageID)
	if c.fail != nil {
		return c.fail
	}
	if f.Inspect != nil {
		return f.Inspect(c.doc)
	}
	return nil
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

// fakeDocuments records what an export put into it.
type fakeDocuments struct {
	worker, bucket, key, contentType, disposition string
	data                                          []byte
	meta                                          map[string]string
	fail                                          error
}

func (f *fakeDocuments) PutDocument(_ context.Context, worker, bucket, key, contentType, disposition string, data []byte, meta map[string]string) (map[string]any, error) {
	f.worker, f.bucket, f.key, f.contentType, f.disposition, f.data, f.meta = worker, bucket, key, contentType, disposition, data, meta
	if f.fail != nil {
		return nil, f.fail
	}
	return map[string]any{"bucket": bucket, "key": key, "etag": "e1"}, nil
}

var pdfDoc = mail.Document{ID: "2", Name: "Rechnung März.pdf", ContentType: "application/octet-stream", Data: []byte("%PDF-1.7\n%%EOF")}

func TestGetAttachmentAnswersTheContentWithWhatInspectionFound(t *testing.T) {
	c := &mailboxClient{doc: pdfDoc}
	res, err := mail.Run(context.Background(), mail.Job{Connector: "box", Operation: mail.OpGetAttachment, Target: "m1", Attachment: "2"}, registryWith(c))
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["contentType"] != "application/pdf" || m["declaredType"] != "application/octet-stream" || m["encoding"] != "base64" ||
		m["content"] != "JVBERi0xLjcKJSVFT0Y=" || m["name"] != "Rechnung März.pdf" || m["id"] != "2" {
		t.Errorf("get-attachment = %v", m)
	}
	if insp := m["inspection"].(map[string]any); insp["scanned"] != false {
		t.Errorf("inspection = %v; nothing scanned it", insp)
	}
	if strings.Join(c.calls, ";") != "attachment m1 2" {
		t.Errorf("calls = %v", c.calls)
	}
	if v := mail.ResultVariable("doc", res); v.Kind != model.VarJSON {
		t.Errorf("the answer is not a structured variable: %+v", v)
	}
}

func TestGetAttachmentRefusesWhatDoesNotFitAVariable(t *testing.T) {
	big := mail.Document{Name: "scan.pdf", Data: append([]byte("%PDF-1.7\n"), make([]byte, 900<<10)...)}
	_, err := mail.Run(context.Background(), mail.Job{Connector: "box", Operation: mail.OpGetAttachment, Target: "m1", Attachment: "1"}, registryWith(&mailboxClient{doc: big}))
	if err == nil || !strings.Contains(err.Error(), "use export-attachment") {
		t.Errorf("a document too large for a variable = %v", err)
	}
}

func TestARefusedDocumentIsABPMNErrorOnlyWhereTheProcessCatchesIt(t *testing.T) {
	exe := mail.Document{Name: "rechnung.pdf", Data: []byte("MZ\x90\x00")}
	j := mail.Job{Connector: "box", Operation: mail.OpGetAttachment, Target: "m1", Attachment: "1"}
	_, err := mail.Run(context.Background(), j, registryWith(&mailboxClient{doc: exe}))
	var rej *mail.Rejection
	var be *job.BPMNError
	if !errors.As(err, &rej) || errors.As(err, &be) {
		t.Errorf("uncaught = %v, want a plain failure carrying the rejection", err)
	}
	j.ErrorCodes = []string{mail.CodeAttachmentRejected}
	_, err = mail.Run(context.Background(), j, registryWith(&mailboxClient{doc: exe}))
	if !errors.As(err, &be) || be.Code != mail.CodeAttachmentRejected || !strings.Contains(be.Message, "is a program") {
		t.Errorf("caught = %v, want the BPMN error %s", err, mail.CodeAttachmentRejected)
	}
}

func TestExportAttachmentPutsTheDocumentUnderItsDetectedType(t *testing.T) {
	docs := &fakeDocuments{}
	c := &mailboxClient{doc: pdfDoc}
	j := mail.Job{Connector: "box", Operation: mail.OpExportAttachment, Target: "m1", Attachment: "2",
		Store: "belege", Bucket: "eingang", Key: "2026/4711.pdf"}
	res, err := mail.RunWith(context.Background(), j, registryWith(c), docs)
	if err != nil {
		t.Fatal(err)
	}
	if docs.worker != "belege" || docs.bucket != "eingang" || docs.key != "2026/4711.pdf" || docs.contentType != "application/pdf" ||
		string(docs.data) != string(pdfDoc.Data) {
		t.Errorf("put = %+v", docs)
	}
	if docs.disposition != "inline; filename*=UTF-8''Rechnung%20M%C3%A4rz.pdf" {
		t.Errorf("disposition = %q", docs.disposition)
	}
	if docs.meta["atlas-detected-type"] != "application/pdf" || docs.meta["atlas-scanned"] != "false" {
		t.Errorf("metadata = %v", docs.meta)
	}
	m := res.(map[string]any)
	if m["etag"] != "e1" || m["contentType"] != "application/pdf" || m["name"] != pdfDoc.Name {
		t.Errorf("export answer = %v", m)
	}

	html := mail.Document{Name: "quittung.html", Data: []byte("<!DOCTYPE html><html>Danke</html>")}
	_, err = mail.RunWith(context.Background(), j, registryWith(&mailboxClient{doc: html}), docs)
	if err != nil || !strings.HasPrefix(docs.disposition, "attachment;") {
		t.Errorf("a page a browser would run = %v, disposition %q; it must download, not open", err, docs.disposition)
	}
}

func TestExportWithoutAStoreFailsBeforeReadingTheMailbox(t *testing.T) {
	c := &mailboxClient{doc: pdfDoc}
	j := mail.Job{Connector: "box", Operation: mail.OpExportAttachment, Target: "m1", Attachment: "2", Store: "belege", Bucket: "b", Key: "k"}
	_, err := mail.Run(context.Background(), j, registryWith(c))
	if err == nil || !strings.Contains(err.Error(), `"belege"`) {
		t.Errorf("an export with no store held = %v", err)
	}
	if len(c.calls) != 0 {
		t.Errorf("the mailbox was read for an export that could not land: %v", c.calls)
	}
	j.Bucket = ""
	if _, err := mail.RunWith(context.Background(), j, registryWith(&mailboxClient{doc: pdfDoc}), &fakeDocuments{}); err == nil {
		t.Error("an export whose bucket resolved to nothing was put")
	}
}

func TestForwardInspectsBeforeItSends(t *testing.T) {
	c := &mailboxClient{doc: pdfDoc}
	j := mail.Job{Connector: "box", Operation: mail.OpForward, Target: "m1", To: []string{"ablage@example.ch"}, Body: "Ablage", MessageID: "7"}
	if res, err := mail.Run(context.Background(), j, registryWith(c)); err != nil || res != nil {
		t.Errorf("forward = %v, %v", res, err)
	}
	if strings.Join(c.calls, ";") != "forward m1 ablage@example.ch Ablage 7" {
		t.Errorf("calls = %v", c.calls)
	}
	_, err := mail.Run(context.Background(), j, registryWith(&mailboxClient{doc: mail.Document{Name: "x.js", Data: []byte("alert(1)")}}))
	var rej *mail.Rejection
	if !errors.As(err, &rej) {
		t.Errorf("a forward carrying a script = %v", err)
	}
	j.To = nil
	if _, err := mail.Run(context.Background(), j, registryWith(&mailboxClient{})); err == nil {
		t.Error("a forward with no recipient ran")
	}
	j.To, j.Target = []string{"a@b.ch"}, ""
	if _, err := mail.Run(context.Background(), j, registryWith(&mailboxClient{})); err == nil {
		t.Error("a forward of no message ran")
	}
}

func TestAWorkersScannerSeesEveryDocumentHandedOn(t *testing.T) {
	sc := &countingScanner{sig: "Eicar"}
	reg := registryWith(mail.WithScanner(&mailboxClient{doc: pdfDoc}, sc))
	_, err := mail.Run(context.Background(), mail.Job{Connector: "box", Operation: mail.OpGetAttachment, Target: "m1", Attachment: "2"}, reg)
	var rej *mail.Rejection
	if !errors.As(err, &rej) || sc.n != 1 {
		t.Errorf("an infected attachment = %v (scanned %d)", err, sc.n)
	}
}

type countingScanner struct {
	sig string
	n   int
}

func (s *countingScanner) Name() string { return "count" }
func (s *countingScanner) Scan(context.Context, []byte) (string, error) {
	s.n++
	return s.sig, nil
}
func (s *countingScanner) Ping(context.Context) error { return nil }

// TestExportAttachmentEndToEnd drives an export through the engine: the message, the
// attachment and the key are FEEL over the instance's variables, the document goes to
// the store the handler was given, and where it went comes back as the result.
func TestExportAttachmentEndToEnd(t *testing.T) {
	log, store := openStore(t)
	compile := func(src string) compiler.RestExpr {
		e, err := expr.CompileAuto(src)
		if err != nil {
			t.Fatal(err)
		}
		return compiler.RestExpr{Expr: e}
	}
	cp, jobType := mailProcess(t, compiler.MailConfig{
		Connector: "box", Operation: mail.OpExportAttachment,
		Message:    compile(`mail.messageId`),
		Attachment: compile(`mail.attachments[1].id`),
		Store:      "belege", Bucket: compiler.RestExpr{Literal: "eingang"},
		Key:       compile(`"rechnungen/" + mail.messageId + ".pdf"`),
		ResultVar: "abgelegt", Retries: 3,
	})
	c := &mailboxClient{doc: pdfDoc}
	docs := &fakeDocuments{}
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	var got []model.VariableValue
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		inner := mail.HandlerWith(store, func(uint64) *compiler.CompiledProcess { return cp }, registryWith(c), nil, docs)
		return func(j job.Job) ([]model.VariableValue, error) {
			vars, err := inner(j)
			got = append(got, vars...)
			return vars, err
		}
	})
	p.CreateInstance(cp.Key, model.VariableValue{Name: "mail", Kind: model.VarJSON,
		Text: `{"messageId":"m-9","attachments":[{"id":"2","name":"R.pdf"}]}`})
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if strings.Join(c.calls, "|") != "attachment m-9 2" {
		t.Fatalf("calls = %v", c.calls)
	}
	if docs.worker != "belege" || docs.bucket != "eingang" || docs.key != "rechnungen/m-9.pdf" {
		t.Errorf("put = %s/%s/%s", docs.worker, docs.bucket, docs.key)
	}
	if len(got) != 1 || got[0].Name != "abgelegt" || !strings.Contains(got[0].Text, `"etag":"e1"`) {
		t.Errorf("result = %+v", got)
	}
}

// TestARejectionReachesTheProcessThatCatchesIt drives a refused attachment through the
// engine with an error boundary on the task: the code the boundary names travels with
// the resolved job, so the refusal is thrown rather than failed.
func TestARejectionReachesTheProcessThatCatchesIt(t *testing.T) {
	log, store := openStore(t)
	b := compiler.NewBuilder(mailDefKey, "inbox", 1)
	start := b.AddStartEvent()
	task := b.AddMailConnectorTask(compiler.MailConfig{
		Connector: "box", Operation: mail.OpGetAttachment,
		Message: compiler.RestExpr{Literal: "m1"}, Attachment: compiler.RestExpr{Literal: "1"},
		ResultVar: "doc", Retries: 3,
	})
	caught := b.AddBoundaryErrorEvent(task, mail.CodeAttachmentRejected)
	done, refused := b.AddEndEvent(), b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, done)
	b.Connect(caught, refused)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ConnectorTask(cp.Node(task).Detail).JobType
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	var thrown error
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		inner := mail.Handler(store, func(uint64) *compiler.CompiledProcess { return cp },
			registryWith(&mailboxClient{doc: mail.Document{Name: "rechnung.pdf", Data: []byte("MZ\x90\x00\x03\x00")}}), nil)
		return func(j job.Job) ([]model.VariableValue, error) {
			vars, err := inner(j)
			thrown = err
			return vars, err
		}
	})
	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	var be *job.BPMNError
	if !errors.As(thrown, &be) || be.Code != mail.CodeAttachmentRejected {
		t.Errorf("the handler ended with %v, want the BPMN error the boundary catches", thrown)
	}
}

// TestADocumentOperationStopsAtTheFirstThingThatIsNotThere: an attachment id that
// resolved to nothing, a mailbox that cannot read the attachment, a store that refuses
// the put — each ends the task with its own reason and hands nothing on.
func TestADocumentOperationStopsAtTheFirstThingThatIsNotThere(t *testing.T) {
	ctx := context.Background()
	_, err := mail.RunWith(ctx, mail.Job{Connector: "box", Operation: mail.OpGetAttachment, Target: "m1"}, registryWith(&mailboxClient{}), nil)
	if err == nil || !strings.Contains(err.Error(), "attachmentId resolved to nothing") {
		t.Errorf("no attachment id = %v", err)
	}
	gone := errors.New("message gone")
	_, err = mail.RunWith(ctx, mail.Job{Connector: "box", Operation: mail.OpGetAttachment, Target: "m1", Attachment: "1"},
		registryWith(&mailboxClient{fail: gone}), nil)
	if !errors.Is(err, gone) {
		t.Errorf("a mailbox that cannot read the attachment = %v", err)
	}
	refused := errors.New("bucket says no")
	j := mail.Job{Connector: "box", Operation: mail.OpExportAttachment, Target: "m1", Attachment: "2", Store: "s", Bucket: "b", Key: "k"}
	if _, err := mail.RunWith(ctx, j, registryWith(&mailboxClient{doc: pdfDoc}), &fakeDocuments{fail: refused}); !errors.Is(err, refused) {
		t.Errorf("a store that refuses the put = %v", err)
	}
	docs := &fakeDocuments{}
	reg := registryWith(mail.WithScanner(&mailboxClient{doc: pdfDoc}, &countingScanner{}))
	if _, err := mail.RunWith(ctx, j, reg, docs); err != nil || docs.meta["atlas-scanned"] != "true" || docs.meta["atlas-scanner"] != "count" {
		t.Errorf("a scanned export = %v, metadata %v; the object must say which scanner looked", err, docs.meta)
	}
}
