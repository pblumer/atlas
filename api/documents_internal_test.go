package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/docstore"
)

const docSweepBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="halt" isExecutable="true">
    <startEvent id="s"/>
    <userTask id="wait"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`

// The sweep removes what nobody can reach and keeps what an instance holds
// (ADR-0543): an upload no variable named within
// its time to live, and a document whose every instance is gone; a document a running
// instance refers to stays, and its binding is on disk for the next server to read.
func TestDocumentSweepKeepsWhatAnInstanceHolds(t *testing.T) {
	srv, _ := newOffLoopServer(t, WithDocumentPendingTTL(time.Nanosecond))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	docs, err := srv.documents()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	put := func(name string) docstore.Record {
		rec, err := docs.Put(ctx, name, "text/csv", "", strings.NewReader("a,b\n1,"+name+"\n"))
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	unused, orphan, held := put("unused"), put("orphan"), put("held")
	// Bound to an instance that does not exist: as if retention had purged it.
	if err := docs.Bind(orphan.ID, 999_999); err != nil {
		t.Fatal(err)
	}

	post := func(path, ct, body string) []byte {
		res, err := http.Post(ts.URL+path, ct, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out json.RawMessage
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode/100 != 2 {
			t.Fatalf("POST %s: %d %s", path, res.StatusCode, out)
		}
		return out
	}
	var dep struct{ Key uint64 }
	_ = json.Unmarshal(post("/api/v1/deployments", "application/xml", docSweepBPMN), &dep)
	ref, _ := json.Marshal(held.Ref())
	var started struct{ InstanceKey uint64 }
	_ = json.Unmarshal(post(fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "application/json",
		`{"variables":{"datei":`+string(ref)+`}}`), &started)

	time.Sleep(time.Millisecond)
	srv.sweepDocuments()

	for id, want := range map[string]bool{unused.ID: false, orphan.ID: false, held.ID: true} {
		_, err := docs.Get(id)
		if (err == nil) != want {
			t.Errorf("document %s present=%v, want %v (%v)", id, err == nil, want, err)
		}
	}
	// The binding is in the store's files, so a server opening the same directory —
	// after a restart — sees the document as the instance's.
	again, err := docstore.Open(filepath.Join(srv.dataDir, documentsDir), docstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := again.Get(held.ID)
	if err != nil || len(rec.BoundTo) != 1 || rec.BoundTo[0] != started.InstanceKey {
		t.Fatalf("after reopening: %+v, %v; want bound to %d", rec, err, started.InstanceKey)
	}
	if _, err := again.Get(unused.ID); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("the expired upload came back: %v", err)
	}
}

// A store that was never used is not created by the sweep looking for it: the
// directory is on-demand (storeregistry.go).
func TestDocumentSweepDoesNotCreateTheStore(t *testing.T) {
	srv, _ := newOffLoopServer(t)
	srv.sweepDocuments()
	if srv.documentsExist() {
		t.Fatal("the sweep created the document store")
	}
}

func TestValidateDocumentBackend(t *testing.T) {
	for _, tc := range []struct {
		backend, worker, bucket string
		ok                      bool
	}{
		{"", "", "", true}, {"local", "", "", true}, {"s3", "docs", "atlas", true},
		{"s3", "", "atlas", false}, {"s3", "docs", "", false}, {"ftp", "", "", false},
	} {
		if err := ValidateDocumentBackend(tc.backend, tc.worker, tc.bucket); (err == nil) != tc.ok {
			t.Errorf("ValidateDocumentBackend(%q,%q,%q) = %v", tc.backend, tc.worker, tc.bucket, err)
		}
	}
}

const personalDocBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="personaldoc" isExecutable="true" atlas:personal="ausweis" atlas:dataSubject="kunde">
    <startEvent id="s"/>
    <userTask id="wait"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`

// A document a personal variable names is personal data: its bytes are enciphered under
// the data subject's key, it reads back the same while the subject exists, and an erasure
// makes it unreadable and the sweep removes it (ADR-0314,
// ADR-0543).
func TestAPersonalDocumentIsErasedWithItsSubject(t *testing.T) {
	srv, _ := newOffLoopServer(t)
	if srv.vault == nil {
		t.Skip("this server runs without a vault")
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	call := func(method, path, ct, body string) (int, []byte) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var buf strings.Builder
		_, _ = io.Copy(&buf, res.Body)
		return res.StatusCode, []byte(buf.String())
	}
	const content = "Ausweis von Anna Meier"
	_, ref := call("POST", "/api/v1/documents?name=ausweis.txt", "text/plain", content)
	var dep struct{ Key uint64 }
	_, out := call("POST", "/api/v1/deployments", "application/xml", personalDocBPMN)
	_ = json.Unmarshal(out, &dep)
	if code, out := call("POST", fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "application/json",
		`{"variables":{"kunde":"K-1","ausweis":`+string(ref)+`}}`); code != http.StatusOK {
		t.Fatalf("start: %d %s", code, out)
	}
	var r struct {
		ID string `json:"$document"`
	}
	_ = json.Unmarshal(ref, &r)
	docs, _ := srv.documents()
	rec, _ := docs.Get(r.ID)
	if rec.Subject != "K-1" || rec.Blob == "" {
		t.Fatalf("record = %+v, want it enciphered for K-1", rec)
	}
	if code, body := call("GET", "/api/v1/documents/"+r.ID, "", ""); code != http.StatusOK || string(body) != content {
		t.Fatalf("download before erasure: %d %q", code, body)
	}
	srv.sweepDocuments() // drops the plain bytes
	blobs, _ := os.ReadDir(filepath.Join(srv.dataDir, documentsDir, "blobs"))
	for _, b := range blobs {
		data, _ := os.ReadFile(filepath.Join(srv.dataDir, documentsDir, "blobs", b.Name()))
		if strings.Contains(string(data), "Anna") {
			t.Fatal("the plain bytes of a personal document are still on disk")
		}
	}
	if code, out := call("DELETE", "/api/v1/personal-data/K-1", "", ""); code != http.StatusOK {
		t.Fatalf("erase: %d %s", code, out)
	}
	if code, body := call("GET", "/api/v1/documents/"+r.ID, "", ""); code != http.StatusGone || !strings.Contains(string(body), "erased") {
		t.Fatalf("download after erasure: %d %s", code, body)
	}
	srv.sweepDocuments()
	if _, err := docs.Get(r.ID); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("the erased subject's document survived the sweep: %v", err)
	}
}
