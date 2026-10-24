package api

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/worker"
)

const exportBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" targetNamespace="http://atlas.test">
  <bpmn:process id="ablage" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements><atlas:mailConnector connector="desk" operation="export-attachment"
        messageId="=mailId" attachmentId="=attId" store="belege" bucket="eingang" key="=mailId"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// A supervised mail worker is handed its scanner, and the S3 identities the deployed
// models export attachments into — those and no others, because an access key is the
// whole of a Worker's authority over a bucket (ADR-0478).
func TestASupervisedMailWorkerHoldsTheStoresItsModelsExportInto(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "desk", Kind: connectorKindMail, Provider: "smtp", Endpoint: "smtp.example.com:587",
		Sender: "desk@example.com", MailboxEndpoint: "imaps://imap.example.com:993", Scanner: "clamd://clamav.intern:3310",
		Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	saveS3Worker(t, srv, "2", "belege", "https://minio.example:9000", "s3-creds")
	saveS3Worker(t, srv, "3", "fremd", "", "s3-creds")
	t.Setenv("ATLAS_CONNECTOR_S3_CREDS_TOKEN", s3Bundle)

	env := envOf(t, srv.mailWorkerEnv())
	if got := env["ATLAS_MAIL_DESK_SCANNER"]; got != "clamd://clamav.intern:3310" {
		t.Errorf("the scanner handed over = %q", got)
	}
	if _, ok := env[mailStoresEnv]; ok {
		t.Error("a store was handed over although no deployed model exports into one")
	}

	deployInternal(t, srv, exportBPMN)
	env = envOf(t, srv.mailWorkerEnv())
	if env[mailStoresEnv] != "belege" || env["ATLAS_S3_BELEGE_CREDENTIALS"] != s3Bundle || env["ATLAS_S3_BELEGE_URL"] != "https://minio.example:9000" {
		t.Errorf("stores = %q, credentials %q, url %q", env[mailStoresEnv], env["ATLAS_S3_BELEGE_CREDENTIALS"], env["ATLAS_S3_BELEGE_URL"])
	}
	if _, ok := env["ATLAS_S3_FREMD_CREDENTIALS"]; ok {
		t.Error("an S3 Worker no model exports into was handed to the mail worker")
	}
	if _, ok := env["ATLAS_S3_CONNECTORS"]; ok {
		t.Error("the mail worker's stores were written into the s3 kind's own list")
	}
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindMail)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if _, ok := built.Handlers[compiler.MailJobType]; !ok {
		t.Error("no mail handler")
	}
}

// The scanner is validated where it is typed, and dropped where it cannot apply.
func TestAMailWorkersScannerIsNormalizedWhereItIsTyped(t *testing.T) {
	p := createConnectorParams{Provider: "smtp", Sender: "a@b.ch", Endpoint: "smtp.example.com", Scanner: "clamav.intern"}
	if msg := validateMailConnector(&p); msg != "" || p.Scanner != "clamd://clamav.intern:3310" {
		t.Errorf("= %q, scanner %q", msg, p.Scanner)
	}
	p = createConnectorParams{Provider: "smtp", Sender: "a@b.ch", Endpoint: "smtp.example.com", Scanner: "icap://av"}
	if msg := validateMailConnector(&p); msg == "" {
		t.Error("a scanner that is not clamd was accepted")
	}
	p = createConnectorParams{Provider: "preview", Sender: "a@b.ch", Scanner: "clamav.intern"}
	if msg := validateMailConnector(&p); msg != "" || p.Scanner != "" {
		t.Errorf("a preview Worker hands nothing on and keeps no scanner: %q, %q", msg, p.Scanner)
	}
	rec := connector{Kind: connectorKindMail, Provider: "microsoft", Sender: "a@b.ch", CredentialsRef: "graph", Scanner: " clamd://av "}
	if msg := normalizeConnectorUpdate(&rec); msg != "" || rec.Scanner != "clamd://av:3310" {
		t.Errorf("a patched scanner = %q, %q", msg, rec.Scanner)
	}
}

// An export run inside this server reaches the S3 Workers this server holds, read when
// the export runs rather than when the handler was registered.
func TestAnExportInsideTheServerReachesItsOwnS3Workers(t *testing.T) {
	srv, _ := newValidateServer(t)
	_, err := serverDocuments{srv}.PutDocument(context.Background(), "niemand", "b", "k", "application/pdf", "inline", []byte("%PDF"), nil)
	if err == nil || !strings.Contains(err.Error(), `"niemand"`) {
		t.Errorf("an export into an S3 Worker this server does not hold = %v, want it named", err)
	}
}
