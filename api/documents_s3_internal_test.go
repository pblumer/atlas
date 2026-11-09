package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/connector/s3"
)

// fakeBucket is an S3 endpoint holding objects in memory.
type fakeBucket struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
}

func (f *fakeBucket) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		f.objects[r.URL.Path], _ = io.ReadAll(r.Body)
		f.types[r.URL.Path] = r.Header.Get("Content-Type")
		w.Header().Set("ETag", `"e"`)
	case http.MethodGet:
		b, ok := f.objects[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code><Message>none</Message></Error>`)
			return
		}
		_, _ = w.Write(b)
	}
}

func s3DocModel(op, extra string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="d">
  <bpmn:process id="p` + strings.ReplaceAll(op, "-", "") + `" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:s3Connector connector="archiv" operation="` + op + `" bucket="akten" ` + extra + `/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:userTask id="wait"/>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="wait"/>
    <bpmn:sequenceFlow id="f3" sourceRef="wait" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

// An in-process S3 task moves documents between a bucket and the document store
// (ADR-0543): put-document puts the document a
// reference names, get-document takes an object in and answers a reference that is
// bound to the instance. A document the instance was never given is refused, whatever
// id a model writes.
func TestS3TasksMoveDocuments(t *testing.T) {
	bucket := &fakeBucket{objects: map[string][]byte{}, types: map[string]string{}}
	store := httptest.NewServer(bucket)
	t.Cleanup(store.Close)
	srv, _ := newOffLoopServer(t)
	srv.s3Registry.Register("archiv", s3.NewHTTPClient(s3.Connector{Endpoint: store.URL, Region: "eu-central-1", AccessKeyID: "AKIA", SecretAccessKey: "s"}))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	do := func(method, path, body, ct string) (int, []byte) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", ct)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, _ := io.ReadAll(res.Body)
		return res.StatusCode, out
	}
	deploy := func(xml string) uint64 {
		code, out := do("POST", "/api/v1/deployments", xml, "application/xml")
		if code != http.StatusOK {
			t.Fatalf("deploy: %d %s", code, out)
		}
		var d struct{ Key uint64 }
		_ = json.Unmarshal(out, &d)
		return d.Key
	}
	start := func(key uint64, vars string) uint64 {
		code, out := do("POST", fmt.Sprintf("/api/v1/processes/%d/instances", key), `{"variables":`+vars+`}`, "application/json")
		if code != http.StatusOK {
			t.Fatalf("start: %d %s", code, out)
		}
		var r struct{ InstanceKey uint64 }
		_ = json.Unmarshal(out, &r)
		return r.InstanceKey
	}
	vars := func(inst uint64) map[string]any {
		_, out := do("GET", fmt.Sprintf("/api/v1/instances/%d/variables", inst), "", "")
		var m map[string]any
		_ = json.Unmarshal(out, &m)
		return m
	}
	_, upload := do("POST", "/api/v1/documents?name=antrag.pdf", "%PDF-1.4 fake", "application/pdf")

	// put-document: the bytes reach the bucket under their detected type.
	putKey := deploy(s3DocModel("put-document", `key="faelle/1/antrag.pdf" content="=datei" resultVariable="ablage"`))
	inst := start(putKey, `{"datei":`+string(upload)+`}`)
	if got := string(bucket.objects["/akten/faelle/1/antrag.pdf"]); got != "%PDF-1.4 fake" {
		_, inc := do("GET", "/api/v1/incidents", "", "")
		t.Fatalf("bucket holds %q (variables %v) incidents %s", got, vars(inst), inc)
	}
	if ct := bucket.types["/akten/faelle/1/antrag.pdf"]; ct != "application/pdf" {
		t.Errorf("stored as %q", ct)
	}

	// get-document: the object becomes a document, bound to the instance that took it.
	bucket.objects["/akten/eingang/brief.txt"] = []byte("Sehr geehrte Damen und Herren")
	getKey := deploy(s3DocModel("get-document", `key="eingang/brief.txt" resultVariable="brief"`))
	inst2 := start(getKey, `{}`)
	brief, _ := vars(inst2)["brief"].(map[string]any)
	id, _ := brief["$document"].(string)
	if id == "" || brief["name"] != "brief.txt" {
		t.Fatalf("brief = %v, want a document reference", vars(inst2)["brief"])
	}
	docs, _ := srv.documents()
	rec, err := docs.Get(id)
	if err != nil || !boundTo(rec, inst2) {
		t.Fatalf("the taken document is %+v, %v; want it bound to %d", rec, err, inst2)
	}

	// A model that names a document its instance was never given is refused.
	var ref struct {
		ID string `json:"$document"`
	}
	_ = json.Unmarshal(upload, &ref)
	stealKey := deploy(s3DocModel("put-document", `key="gestohlen.pdf" content="={&quot;$document&quot;: &quot;`+ref.ID+`&quot;}"`))
	start(stealKey, `{}`)
	if _, ok := bucket.objects["/akten/gestohlen.pdf"]; ok {
		t.Fatal("a document another instance holds was put into the bucket")
	}
}
