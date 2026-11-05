package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
// (ADR-draft-a-file-is-a-document-not-a-variable): an upload no variable named within
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
