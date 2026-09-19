package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:s3Connector> extension is an object-store task
// (ADR-draft-s3-object-store-worker): it performs one operation against a configured S3
// Worker via the job path. The access key lives server-side, like Jira's credential and
// Google's (ADR-0201/0235); only what the task is *about* — the operation, the bucket,
// the key and its values — is authored in the model.
const s3ConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:s3Connector connector="archiv" operation="put-object"
                           bucket="rechnungen" key="=&quot;faelle/&quot; + vorgang + &quot;/antrag.txt&quot;"
                           content="=inhalt" contentType="text/plain" retries="5">
          <atlas:s3Meta name="fall" value="=vorgang"/>
          <atlas:s3Meta name="x-amz-storage-class" value="STANDARD_IA"/>
        </atlas:s3Connector>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// The whole task compiles to one connector-task node carrying the reserved job type, the
// Worker's name, and every authored value as either a literal or a compiled expression.
func TestS3TaskCompilesToItsReservedJobType(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(s3ConnectorBPMN))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	detail := s3Detail(t, cp)
	if got := cp.Intern(detail.JobType); got != S3JobType {
		t.Errorf("job type = %q, want %q", got, S3JobType)
	}
	if got := cp.Intern(detail.Connector); got != "archiv" {
		t.Errorf("worker = %q, want the configured Worker's name", got)
	}
	if got := cp.Intern(detail.S3Op); got != "put-object" {
		t.Errorf("operation = %q", got)
	}
	// A literal stays a literal and a FEEL value is compiled at deploy (I5): the runtime
	// parses nothing.
	if detail.S3Bucket.Literal != "rechnungen" || detail.S3Bucket.Expr != nil {
		t.Errorf("bucket = %+v, want the literal", detail.S3Bucket)
	}
	if detail.S3Key.Expr == nil {
		t.Errorf("key = %+v, want a compiled expression", detail.S3Key)
	}
	// The encoding default is applied here rather than at call time, for the same reason:
	// a runtime that decided it would be a second place the answer lives.
	if got := cp.Intern(detail.S3Encoding); got != s3EncodingText {
		t.Errorf("encoding = %q, want the default applied at deploy", got)
	}
	if detail.Retries != 5 {
		t.Errorf("retries = %d, want the task's own budget (ADR-0135)", detail.Retries)
	}
	if len(detail.S3Metadata) != 2 || detail.S3Metadata[0].Name != "fall" || detail.S3Metadata[0].Val.Expr == nil {
		t.Errorf("metadata = %+v, want both headers with the FEEL one compiled", detail.S3Metadata)
	}
}

// The half that is easy to forget and expensive to have forgotten: a value the operation
// does not use would otherwise compile and then be silently dropped at call time, which
// from the author's side is indistinguishable from a Worker that ignored it.
func TestS3RefusesAValueItsOperationWouldIgnore(t *testing.T) {
	for _, tc := range []struct{ name, attrs, want string }{
		{"prefix on a put", `connector="a" operation="put-object" bucket="b" key="k" content="x" prefix="p/"`, "does not use prefix"},
		{"expiry on a delete", `connector="a" operation="delete-object" bucket="b" key="k" expiresIn="60"`, "does not use expiresIn"},
		{"content type on a read", `connector="a" operation="get-object" bucket="b" key="k" contentType="text/plain" resultVariable="r"`, "does not use contentType"},
		{"encoding on a listing", `connector="a" operation="list-objects" bucket="b" encoding="base64" resultVariable="r"`, "does not use encoding"},
		{"a key on a listing", `connector="a" operation="list-objects" bucket="b" key="k" resultVariable="r"`, "does not use key"},
		{"a source on a put", `connector="a" operation="put-object" bucket="b" key="k" content="x" sourceKey="j"`, "does not use sourceKey"},
		// A delete answers with nothing, so a result variable there would name a value
		// that is never written — the panel hides the field, and this refuses it.
		{"a result on a delete", `connector="a" operation="delete-object" bucket="b" key="k" resultVariable="r"`, "does not use resultVariable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := compileS3Task(tc.attrs, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// Metadata is a request header, so an operation with no request to put one on refuses it
// rather than dropping it.
func TestS3RefusesMetadataOnAnOperationThatSendsNone(t *testing.T) {
	err := compileS3Task(`connector="a" operation="get-object" bucket="b" key="k" resultVariable="r"`,
		`<atlas:s3Meta name="fall" value="4711"/>`)
	if err == nil || !strings.Contains(err.Error(), "s3Meta") {
		t.Errorf("err = %v, want a refusal naming the ignored children", err)
	}
}

// The two numeric ceilings are refused at deploy rather than turned into a silently
// different call: a maxKeys of ten thousand is answered with a thousand and no complaint,
// and an expiry past seven days comes back as a signature error that says nothing about
// expiry.
func TestS3RefusesANumberTheStoreWouldNotHonour(t *testing.T) {
	for _, tc := range []struct{ name, attrs, want string }{
		{"not a number", `connector="a" operation="list-objects" bucket="b" maxKeys="viele" resultVariable="r"`, "non-numeric maxKeys"},
		{"zero", `connector="a" operation="list-objects" bucket="b" maxKeys="0" resultVariable="r"`, "must be positive"},
		{"negative expiry", `connector="a" operation="presign-get" bucket="b" key="k" expiresIn="-1" resultVariable="r"`, "must be positive"},
		{"past the page cap", `connector="a" operation="list-objects" bucket="b" maxKeys="10000" resultVariable="r"`, "the most that takes effect"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := compileS3Task(tc.attrs, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A task naming no Worker has nowhere for its credential to come from, and an unknown
// operation is a model that would park on its first token. Both are deploy-time answers.
func TestS3NeedsAWorkerAndAKnownOperation(t *testing.T) {
	if err := compileS3Task(`operation="get-object" bucket="b" key="k" resultVariable="r"`, ""); err == nil ||
		!strings.Contains(err.Error(), "connector attribute") {
		t.Errorf("err = %v, want a refusal naming the missing Worker", err)
	}
	if err := compileS3Task(`connector="a" bucket="b" key="k" resultVariable="r"`, ""); err == nil ||
		!strings.Contains(err.Error(), "needs an operation") {
		t.Errorf("err = %v, want a refusal naming the missing operation", err)
	}
	err := compileS3Task(`connector="a" operation="rename-object" bucket="b" key="k" resultVariable="r"`, "")
	if err == nil || !strings.Contains(err.Error(), "put-object") {
		t.Errorf("err = %v, want the refusal to list the operations that do exist", err)
	}
}

// Defaults are applied at deploy, so the runtime interprets nothing (I5).
func TestS3AppliesItsDefaultsAtDeploy(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(s3Model(`connector="a" operation="list-objects" bucket="b" resultVariable="r"`, "")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	detail := s3Detail(t, cp)
	if detail.S3MaxKeys != s3DefaultMaxKeys {
		t.Errorf("maxKeys = %d, want the default", detail.S3MaxKeys)
	}
	cp, err = Parse(1, 1, strings.NewReader(s3Model(`connector="a" operation="presign-get" bucket="b" key="k" resultVariable="r"`, "")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	detail = s3Detail(t, cp)
	if detail.S3ExpiresIn != s3DefaultExpiresIn {
		t.Errorf("expiresIn = %d, want the one-hour default", detail.S3ExpiresIn)
	}
	// An operation that takes no ceiling carries none, rather than a default nothing
	// reads — which would make a reader of the detail believe it applied.
	cp, err = Parse(1, 1, strings.NewReader(s3Model(`connector="a" operation="delete-object" bucket="b" key="k"`, "")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	detail = s3Detail(t, cp)
	if detail.S3MaxKeys != 0 || detail.S3ExpiresIn != 0 || cp.Intern(detail.S3Encoding) != "" {
		t.Errorf("detail = %+v, want no ceilings on an operation that takes none", detail)
	}
}

// An unparseable FEEL value is a deploy-time failure, not a call-time one: a model that
// cannot be evaluated must not reach a bucket.
func TestS3RefusesAnUnparseableExpression(t *testing.T) {
	if err := compileS3Task(`connector="a" operation="get-object" bucket="b" key="=(((" resultVariable="r"`, ""); err == nil {
		t.Error("a task with an unparseable key compiled")
	}
}

// s3Detail is the compiled task behind the one service task these models carry.
func s3Detail(t *testing.T, cp *CompiledProcess) *ConnectorTaskDetail {
	t.Helper()
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	return cp.ConnectorTask(node.Detail)
}

func s3Model(attrs, children string) string {
	ext := `<atlas:s3Connector ` + attrs + `/>`
	if children != "" {
		ext = `<atlas:s3Connector ` + attrs + `>` + children + `</atlas:s3Connector>`
	}
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>` + ext + `</bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func compileS3Task(attrs, children string) error {
	_, err := Parse(1, 1, strings.NewReader(s3Model(attrs, children)))
	return err
}
