package worker_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/worker"
)

// docMailbox is a mail Worker that is a mailbox holding one attachment.
type docMailbox struct{ doc mail.Document }

func (docMailbox) Send(context.Context, mail.Message) error         { return nil }
func (docMailbox) WatchTip(context.Context, string) (string, error) { return "", nil }
func (docMailbox) WatchSince(context.Context, mail.WatchRequest) (mail.WatchPage, error) {
	return mail.WatchPage{}, nil
}
func (docMailbox) List(context.Context, mail.ListRequest) ([]mail.Envelope, error) { return nil, nil }
func (docMailbox) Get(context.Context, string, bool) (mail.Envelope, error) {
	return mail.Envelope{}, nil
}
func (docMailbox) Move(context.Context, string, string) (string, error) { return "", nil }
func (docMailbox) SetRead(context.Context, string, bool) error          { return nil }
func (docMailbox) Delete(context.Context, string) error                 { return nil }
func (docMailbox) Reply(context.Context, string, mail.Reply) error      { return nil }
func (docMailbox) Forward(context.Context, string, mail.Forward) error  { return nil }
func (m docMailbox) Attachment(context.Context, string, string) (mail.Document, error) {
	return m.doc, nil
}

type recordingStore struct{ worker, key string }

func (s *recordingStore) PutDocument(_ context.Context, worker, _, key, _, _ string, _ []byte, _ map[string]string) (map[string]any, error) {
	s.worker, s.key = worker, key
	return map[string]any{"key": key}, nil
}

func exportJob(store string) worker.Job {
	return worker.Job{JobKey: 1, Type: compiler.MailJobType, Connector: &worker.ConnectorPayload{Kind: "mail", Fields: map[string]any{
		"connector": "box", "operation": mail.OpExportAttachment, "target": "m1", "attachment": "2",
		"store": store, "bucket": "eingang", "key": "a.pdf", "resultVariable": "abgelegt",
		"errorCodes": []any{mail.CodeAttachmentRejected},
	}}}
}

// An offloaded export puts the document through the stores this worker holds and
// answers where it went; the codes the process catches travel with the job, so a
// refused document is thrown on this side exactly as it would be in the engine.
func TestAnOffloadedExportUsesTheWorkersOwnStores(t *testing.T) {
	reg := mail.NewRegistry()
	reg.Register("box", docMailbox{doc: mail.Document{Name: "a.pdf", Data: []byte("%PDF-1.7\n%%EOF")}})
	docs := &recordingStore{}
	vars, err := worker.RunMailJobWith(context.Background(), exportJob("belege"), reg, docs)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if docs.worker != "belege" || docs.key != "a.pdf" || vars["abgelegt"] == nil {
		t.Errorf("put %s/%s, answered %v", docs.worker, docs.key, vars)
	}
	if _, err := worker.RunMailJob(context.Background(), exportJob("belege"), reg); err == nil ||
		!strings.Contains(err.Error(), "holds no S3 Worker") {
		t.Errorf("an export on a worker holding no store = %v", err)
	}
	reg.Register("box", docMailbox{doc: mail.Document{Name: "a.pdf", Data: []byte("MZ\x90\x00\x03\x00")}})
	_, err = worker.RunMailJobWith(context.Background(), exportJob("belege"), reg, docs)
	var be *worker.BPMNError
	if !errors.As(err, &be) || be.Code != mail.CodeAttachmentRejected {
		t.Errorf("a refused document on a worker = %v, want the BPMN error the process catches", err)
	}
}

// The S3 identities a mail worker exports into are read from the same variables the
// s3 kind reads, named either there or in ATLAS_MAIL_STORES; a named store that does
// not build stops the worker at startup, and so does a scanner that does not parse.
func TestAMailWorkerReadsItsStoresAndScannerFromItsEnvironment(t *testing.T) {
	base := map[string]string{
		"ATLAS_MAIL_CONNECTORS":         "office365",
		"ATLAS_MAIL_OFFICE365_PROVIDER": "smtp",
		"ATLAS_MAIL_OFFICE365_ENDPOINT": "smtp.example.com:587",
		"ATLAS_MAIL_OFFICE365_SENDER":   "desk@example.com",
		"ATLAS_MAIL_OFFICE365_SCANNER":  "clamd://clamav.intern:3310",
		"ATLAS_MAIL_STORES":             "belege",
		"ATLAS_S3_BELEGE_CREDENTIALS":   `{"accessKeyId":"AKIA","secretAccessKey":"s","region":"eu-central-1"}`,
	}
	built, err := worker.BuiltinConnectors(fakeEnv(base), "mail")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := built.Handlers[compiler.MailJobType]; !ok {
		t.Fatal("no mail handler")
	}
	for _, n := range built.Names {
		if n == "belege" {
			t.Error("a store held for exports was reported as a Worker this worker serves")
		}
	}
	for name, change := range map[string][2]string{
		"a store without a usable bundle": {"ATLAS_S3_BELEGE_CREDENTIALS", `{"accessKeyId":"AKIA"}`},
		"a scanner that does not parse":   {"ATLAS_MAIL_OFFICE365_SCANNER", "icap://av:1344"},
	} {
		env := map[string]string{}
		for k, v := range base {
			env[k] = v
		}
		env[change[0]] = change[1]
		if _, err := worker.BuiltinConnectors(fakeEnv(env), "mail"); err == nil {
			t.Errorf("%s: the worker started", name)
		}
	}
	legacy := map[string]string{
		"ATLAS_MAIL_CONNECTORS":     "relay",
		"ATLAS_MAIL_RELAY_ENDPOINT": "smtp.example.com:25",
		"ATLAS_MAIL_RELAY_SCANNER":  "not a scanner://",
	}
	if _, err := worker.BuiltinConnectors(fakeEnv(legacy), "mail"); err == nil {
		t.Error("the SMTP-only form started with a scanner that does not parse")
	}
}
