package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/s3"
	"github.com/pblumer/atlas/state"
)

// docHTTP is a small client for these tests: method, path, content type and body in,
// status and body out.
func docHTTP(t *testing.T, ts *httptest.Server) func(method, path, ct, body string) (int, []byte) {
	t.Helper()
	return func(method, path, ct, body string) (int, []byte) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, _ := io.ReadAll(res.Body)
		return res.StatusCode, out
	}
}

const offloadedCSVBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="d">
  <bpmn:process id="offcsv" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:csvConnector source="upload" resultVariable="rows" hasHeader="true"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// The worker-lease route answers a document only to the worker holding the job's lease,
// and only one the job's instance was given (ADR-draft-a-file-is-a-document-not-a-variable).
// The CSV payload names the document instead of carrying its text.
func TestJobDocumentRoute(t *testing.T) {
	srv, _ := newOffLoopServer(t, WithOffloadedConnectorKinds([]string{"csv"}))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	call := docHTTP(t, ts)

	_, ref := call("POST", "/api/v1/documents?name=a.csv", "text/csv", "name\nAda\n")
	_, foreign := call("POST", "/api/v1/documents?name=b.csv", "text/csv", "name\nGrace\n")
	var dep struct{ Key uint64 }
	_, out := call("POST", "/api/v1/deployments", "application/xml", offloadedCSVBPMN)
	_ = json.Unmarshal(out, &dep)
	if code, out := call("POST", fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "application/json",
		`{"variables":{"upload":`+string(ref)+`}}`); code != http.StatusOK {
		t.Fatalf("start: %d %s", code, out)
	}
	_, out = call("POST", "/api/v1/jobs/activate", "application/json",
		`{"type":"io.atlas.csv-import","worker":"csv-1","leaseMs":60000,"waitMs":0,"maxJobs":1}`)
	var leased struct {
		Jobs []struct {
			JobKey     uint64 `json:"jobKey"`
			LeaseToken uint64 `json:"leaseToken"`
			Connector  struct {
				Fields map[string]any `json:"fields"`
			} `json:"connector"`
		} `json:"jobs"`
	}
	_ = json.Unmarshal(out, &leased)
	if len(leased.Jobs) != 1 {
		t.Fatalf("leased %s", out)
	}
	job := leased.Jobs[0]
	idOf := func(raw []byte) string {
		var r struct {
			ID string `json:"$document"`
		}
		_ = json.Unmarshal(raw, &r)
		return r.ID
	}
	if job.Connector.Fields["sourceDocument"] != idOf(ref) || job.Connector.Fields["source"] != "" {
		t.Fatalf("payload = %v, want the document's id and no text", job.Connector.Fields)
	}
	path := func(key uint64, id string) string { return fmt.Sprintf("/api/v1/jobs/%d/documents/%s", key, id) }
	lease := fmt.Sprintf(`{"worker":"csv-1","leaseToken":%d}`, job.LeaseToken)

	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/api/v1/jobs/nope/documents/x", lease, http.StatusBadRequest},
		{path(job.JobKey, idOf(ref)), `{"worker":""}`, http.StatusBadRequest},
		{path(job.JobKey, idOf(ref)), `not json`, http.StatusBadRequest},
		{path(999_999, idOf(ref)), lease, http.StatusNotFound},
		{path(job.JobKey, idOf(ref)), fmt.Sprintf(`{"worker":"other","leaseToken":%d}`, job.LeaseToken), http.StatusConflict},
		{path(job.JobKey, idOf(foreign)), lease, http.StatusNotFound},
		{path(job.JobKey, "doc_aaaaaaaaaaaaaaaaaaaaaaaaaa"), lease, http.StatusNotFound},
	} {
		if code, out := call("POST", tc.path, "application/json", tc.body); code != tc.want {
			t.Errorf("POST %s %s = %d %s, want %d", tc.path, tc.body, code, out, tc.want)
		}
	}
	code, body := call("POST", path(job.JobKey, idOf(ref)), "application/json", lease)
	if code != http.StatusOK || string(body) != "name\nAda\n" {
		t.Fatalf("the lease holder's read = %d %q", code, body)
	}

	// The bytes gone — a restore without documents — is 410, not 404.
	docs, _ := srv.documents()
	rec, _ := docs.Get(idOf(ref))
	_ = os.Remove(filepath.Join(srv.dataDir, documentsDir, "blobs", rec.SHA256))
	if code, _ := call("POST", path(job.JobKey, idOf(ref)), "application/json", lease); code != http.StatusGone {
		t.Fatalf("missing bytes = %d, want 410", code)
	}
	if code, _ := call("GET", "/api/v1/documents/"+idOf(ref), "", ""); code != http.StatusGone {
		t.Fatalf("download of missing bytes = %d, want 410", code)
	}
	if code, out := call("GET", "/api/v1/documents/"+idOf(ref)+"/meta", "", ""); code != http.StatusOK || !strings.Contains(string(out), `"available":false`) {
		t.Fatalf("meta of missing bytes = %d %s", code, out)
	}
	// The instance holds it, so a variable naming it is found there.
	names, err := srv.variablesNaming(rec.BoundTo[0], idOf(ref))
	if err != nil || len(names) != 1 || names[0] != "upload" {
		t.Fatalf("variablesNaming = %v, %v", names, err)
	}
	if names, _ := srv.variablesNaming(rec.BoundTo[0], idOf(foreign)); len(names) != 0 {
		t.Fatalf("a document the instance does not hold was found in it: %v", names)
	}
}

// An installation that keeps documents in a bucket reaches it through an S3 Worker it
// already defines; documents written before the switch stay readable, and an unused
// upload is deleted from the bucket by the sweep.
func TestDocumentsInABucket(t *testing.T) {
	bucket := &fakeBucket{objects: map[string][]byte{}, types: map[string]string{}}
	store := httptest.NewServer(bucket)
	t.Cleanup(store.Close)
	var deletes atomic.Int32
	counting := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes.Add(1)
			bucket.mu.Lock()
			delete(bucket.objects, r.URL.Path)
			bucket.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		bucket.ServeHTTP(w, r)
	})
	store.Config.Handler = counting

	srv, _ := newOffLoopServer(t, WithDocumentBackend("s3", "archiv", "akten", "atlas/docs"), WithDocumentPendingTTL(time.Nanosecond))
	srv.s3Registry.Register("archiv", s3.NewHTTPClient(s3.Connector{Endpoint: store.URL, Region: "eu-central-1", AccessKeyID: "AKIA", SecretAccessKey: "s"}))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	call := docHTTP(t, ts)

	code, ref := call("POST", "/api/v1/documents?name=a.txt", "text/plain", "im Bucket")
	if code != http.StatusCreated {
		t.Fatalf("upload: %d %s", code, ref)
	}
	var r struct {
		ID     string `json:"$document"`
		SHA256 string `json:"sha256"`
	}
	_ = json.Unmarshal(ref, &r)
	if string(bucket.objects["/akten/atlas/docs/"+r.SHA256]) != "im Bucket" {
		t.Fatalf("bucket = %v", bucket.objects)
	}
	if code, body := call("GET", "/api/v1/documents/"+r.ID, "", ""); code != http.StatusOK || string(body) != "im Bucket" {
		t.Fatalf("download from the bucket = %d %q", code, body)
	}
	docs, _ := srv.documents()
	if docs.DefaultBackend() != "s3" || docs.MaxBytes() != srv.budgets().Document {
		t.Fatalf("store = %s, %d", docs.DefaultBackend(), docs.MaxBytes())
	}
	time.Sleep(time.Millisecond)
	srv.sweepDocuments()
	if n := deletes.Load(); n != 1 {
		t.Fatalf("the sweep deleted %d objects, want the unused upload's", n)
	}
}

// A backend configuration the store cannot open is reported where it matters: every
// document route answers 503 with the reason, and a variable that names a document is
// refused rather than accepted into an instance whose document nothing can keep.
func TestAnUnusableDocumentStoreSaysSo(t *testing.T) {
	srv, _ := newOffLoopServer(t, WithDocumentBackend("ftp", "", "", ""))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	call := docHTTP(t, ts)
	for _, tc := range [][2]string{
		{"POST", "/api/v1/documents?name=a.txt"},
		{"GET", "/api/v1/documents/doc_aaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"GET", "/api/v1/documents/doc_aaaaaaaaaaaaaaaaaaaaaaaaaa/meta"},
	} {
		if code, out := call(tc[0], tc[1], "text/plain", "x"); code != http.StatusServiceUnavailable || !strings.Contains(string(out), "ftp") {
			t.Errorf("%s %s = %d %s, want 503 naming the backend", tc[0], tc[1], code, out)
		}
	}
	var dep struct{ Key uint64 }
	_, out := call("POST", "/api/v1/deployments", "application/xml", docSweepBPMN)
	_ = json.Unmarshal(out, &dep)
	if code, _ := call("POST", fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "application/json",
		`{"variables":{"d":{"$document":"doc_aaaaaaaaaaaaaaaaaaaaaaaaaa"}}}`); code != http.StatusServiceUnavailable {
		t.Errorf("a start naming a document with no store = %d, want 503", code)
	}
	if _, err := srv.readDocumentBytes(1, "doc_aaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Error("a worker read with no store answered")
	}
	if err := srv.sealDocuments("K-1", []string{"doc_aaaaaaaaaaaaaaaaaaaaaaaaaa"}); err == nil {
		t.Error("sealing with no store answered")
	}
	srv.bindDocuments([]string{"doc_aaaaaaaaaaaaaaaaaaaaaaaaaa"}, 1) // logs, does not panic
	if _, err := (jobDocuments{s: srv}).StoreDocument(context.Background(), "a", "", []byte("x")); err == nil {
		t.Error("storing with no store answered")
	}
}

// The mail Worker's document store inside the server: an attachment stored as a document
// is pending until the job binds it, and an export to an S3 Worker this server does not
// hold says so.
func TestServerMailDocuments(t *testing.T) {
	srv, _ := newOffLoopServer(t)
	d := serverDocuments{s: srv}
	ref, err := d.StoreDocument(context.Background(), "anhang.pdf", "application/pdf", []byte("%PDF-1.4"))
	if err != nil || ref["$document"] == nil || ref["name"] != "anhang.pdf" {
		t.Fatalf("store = %v, %v", ref, err)
	}
	if _, err := d.PutDocument(context.Background(), "nobody", "b", "k", "text/plain", "", []byte("x"), nil); err == nil {
		t.Fatal("an export to an S3 Worker the server does not hold succeeded")
	}
}

// rootInstanceOf answers the instance itself for an instance with no caller, and for an
// unknown key.
func TestRootInstanceOfAnInstanceWithoutACaller(t *testing.T) {
	srv, _ := newOffLoopServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	call := docHTTP(t, ts)
	var dep struct{ Key uint64 }
	_, out := call("POST", "/api/v1/deployments", "application/xml", docSweepBPMN)
	_ = json.Unmarshal(out, &dep)
	var started struct{ InstanceKey uint64 }
	_, out = call("POST", fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "application/json", `{}`)
	_ = json.Unmarshal(out, &started)
	for _, key := range []uint64{started.InstanceKey, 424242} {
		var root uint64
		if err := srv.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
			var err error
			root, err = rootInstanceOf(rv, key)
			return err
		}); err != nil || root != key {
			t.Errorf("rootInstanceOf(%d) = %d, %v", key, root, err)
		}
	}
}

const callChildBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="kind" isExecutable="true">
    <startEvent id="s"/>
    <userTask id="wait"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`

const callParentBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="eltern" isExecutable="true">
    <startEvent id="s"/>
    <callActivity id="c">
      <extensionElements><zeebe:calledElement processId="kind" bindingType="latest"/></extensionElements>
    </callActivity>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="c"/>
    <sequenceFlow id="f2" sourceRef="c" targetRef="e"/>
  </process>
</definitions>`

// A document that enters a called instance is bound to the instance at the top of the
// call chain, which is the one whose history governs it and whose readers may read it.
func TestRootInstanceOfACalledInstance(t *testing.T) {
	srv, _ := newOffLoopServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	call := docHTTP(t, ts)
	var child, parent struct{ Key uint64 }
	_, out := call("POST", "/api/v1/deployments", "application/xml", callChildBPMN)
	_ = json.Unmarshal(out, &child)
	code, out := call("POST", "/api/v1/deployments", "application/xml", callParentBPMN)
	if code != http.StatusOK {
		t.Fatalf("deploy parent: %d %s", code, out)
	}
	_ = json.Unmarshal(out, &parent)
	var started struct{ InstanceKey uint64 }
	_, out = call("POST", fmt.Sprintf("/api/v1/processes/%d/instances", parent.Key), "application/json", `{}`)
	_ = json.Unmarshal(out, &started)
	_, out = call("GET", fmt.Sprintf("/api/v1/instances?process=%d&state=active", child.Key), "", "")
	var page struct {
		Items []struct{ Key uint64 } `json:"items"`
	}
	_ = json.Unmarshal(out, &page)
	if len(page.Items) != 1 {
		t.Fatalf("called instances = %s", out)
	}
	var root uint64
	if err := srv.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		var err error
		root, err = rootInstanceOf(rv, page.Items[0].Key)
		return err
	}); err != nil || root != started.InstanceKey {
		t.Fatalf("root of the called instance = %d, %v; want the caller %d", root, err, started.InstanceKey)
	}
}
