package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A record the store cannot parse, bytes that are gone and a store directory that
// cannot be written are each reported where they are met, with a status that tells
// them apart from a document that does not exist
// (ADR-draft-a-file-is-a-document-not-a-variable).
func TestDocumentRoutesReportAStoreThatFails(t *testing.T) {
	srv, _ := newOffLoopServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	call := docHTTP(t, ts)
	idOf := func(raw []byte) string {
		var r struct {
			ID string `json:"$document"`
		}
		_ = json.Unmarshal(raw, &r)
		return r.ID
	}

	_, good := call("POST", "/api/v1/documents?name=a.csv", "text/csv", "name\nAda\n")
	_, bad := call("POST", "/api/v1/documents?name=b.csv", "text/csv", "name\nGrace\n")
	docs, err := srv.documents()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(srv.dataDir, documentsDir)

	// A bound document read for an instance of its root, then with its bytes gone.
	id := idOf(good)
	if err := docs.Bind(id, 4242); err != nil {
		t.Fatal(err)
	}
	if data, err := srv.readDocumentBytes(4242, id); err != nil || string(data) != "name\nAda\n" {
		t.Fatalf("a bound read = %q, %v", data, err)
	}
	// Sealed for one data subject, it is refused for another: one key cannot be erased
	// for two people.
	if err := srv.sealDocuments("K-1", []string{id}); err != nil {
		t.Fatalf("seal = %v", err)
	}
	if data, err := srv.readDocumentBytes(4242, id); err != nil || string(data) != "name\nAda\n" {
		t.Fatalf("a sealed read = %q, %v", data, err)
	}
	if err := srv.sealDocuments("K-2", []string{id}); err == nil || !strings.Contains(err.Error(), "another subject") {
		t.Errorf("sealing for a second subject = %v", err)
	}
	rec, _ := docs.Get(id)
	_ = os.Remove(filepath.Join(dir, "blobs", rec.Blob))
	if _, err := srv.readDocumentBytes(4242, id); err == nil {
		t.Error("a read of bytes that are gone answered")
	}

	// A record that does not parse is a store failure, not an unknown document.
	broken := idOf(bad)
	if err := os.WriteFile(filepath.Join(dir, "meta", broken+".json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := call("GET", "/api/v1/documents/"+broken, "", ""); code != http.StatusInternalServerError {
		t.Errorf("download of a broken record = %d %s", code, out)
	}
	if _, err := srv.readDocumentBytes(4242, broken); err == nil {
		t.Error("a worker read of a broken record answered")
	}
	var dep struct{ Key uint64 }
	_, out := call("POST", "/api/v1/deployments", "application/xml", docSweepBPMN)
	_ = json.Unmarshal(out, &dep)
	if code, out := call("POST", fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "application/json",
		`{"variables":{"d":`+string(bad)+`}}`); code < 500 {
		t.Errorf("a start naming a broken record = %d %s, want a server error", code, out)
	}
	srv.sweepDocuments()                      // logs the record it cannot read, and does not panic
	srv.bindDocuments([]string{broken}, 4242) // logs the bind it cannot make, and does not panic

	// A public form's visitor uploaded nothing, so a reference there is refused before
	// the link is even resolved.
	if code, out := call("POST", "/public/forms/no-such-token/start", "application/json",
		`{"variables":{"d":`+string(good)+`}}`); code != http.StatusBadRequest || !strings.Contains(string(out), "does not accept document references") {
		t.Errorf("a public start naming a document = %d %s", code, out)
	}

	// A store whose upload directory is gone refuses the upload with the reason.
	if err := os.RemoveAll(filepath.Join(dir, "tmp")); err != nil {
		t.Fatal(err)
	}
	if code, out := call("POST", "/api/v1/documents?name=c.csv", "text/csv", "x"); code != http.StatusInternalServerError || !strings.Contains(string(out), "store document") {
		t.Errorf("an upload the store cannot write = %d %s", code, out)
	}
	if _, err := (jobDocuments{s: srv}).StoreDocument(t.Context(), "d.csv", "text/csv", []byte("x")); err == nil {
		t.Error("an in-process worker stored a document the store cannot write")
	}
}

// The sweeper sweeps on its own schedule until the server stops.
func TestDocumentSweeperRunsOnItsSchedule(t *testing.T) {
	srv, _ := newOffLoopServer(t, WithDocumentPendingTTL(time.Nanosecond))
	docs, err := srv.documents()
	if err != nil {
		t.Fatal(err)
	}
	rec, err := docs.Put(t.Context(), "a.csv", "text/csv", "", strings.NewReader("x\n"))
	if err != nil {
		t.Fatal(err)
	}
	srv.wg.Add(1)
	go srv.documentSweeper(time.Millisecond)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := docs.Get(rec.ID); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sweeper never removed the expired upload")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A bucket that is configured but not the default is still opened, so documents written
// while it was the default stay readable; and a sweep of a store that cannot be opened
// does nothing.
func TestDocumentBackendsBesideTheDefault(t *testing.T) {
	srv, _ := newOffLoopServer(t, WithDocumentBackend("local", "archiv", "akten", ""))
	docs, err := srv.documents()
	if err != nil || docs.DefaultBackend() != "local" {
		t.Fatalf("store = %v, %v", docs, err)
	}

	broken, _ := newOffLoopServer(t, WithDocumentBackend("ftp", "", "", ""))
	if err := os.MkdirAll(filepath.Join(broken.dataDir, documentsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	broken.sweepDocuments()
	if _, err := broken.documents(); err == nil {
		t.Fatal("an unknown backend opened")
	}
}
