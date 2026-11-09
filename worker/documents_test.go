package worker_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
	"github.com/pblumer/atlas/worker"
	"github.com/pblumer/atlas/workersdk"
)

// bareAtlas is a live server with a model deployed and no instance started, so a test
// can put a document into the store before starting one.
func bareAtlas(t *testing.T, model string, opts ...api.Option) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatal(err)
	}
	srv, err := api.New(proc, store, dir, opts...)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close(); _ = store.Close(); _ = log.Close() })
	deployInApp(t, ts, model)
	return ts
}

func uploadDocument(t *testing.T, ts *httptest.Server, name, body string) string {
	t.Helper()
	res, err := http.Post(ts.URL+"/api/v1/documents?name="+name, "text/csv", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var ref map[string]any
	_ = json.NewDecoder(res.Body).Decode(&ref)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload: %d %v", res.StatusCode, ref)
	}
	raw, _ := json.Marshal(ref)
	return string(raw)
}

// A Worker Instance parses an uploaded CSV it was handed only by reference
// (ADR-0543, slice 5): the engine resolved the
// task on its run loop, where no document is read, so the payload names the document,
// and the worker reads it under the job's lease.
func TestWorkerReadsAReferencedCsvUnderItsLease(t *testing.T) {
	ts := bareAtlas(t, csvConnectorModel, api.WithOffloadedConnectorKinds([]string{"csv"}))
	ref := uploadDocument(t, ts, "orders.csv", "name,amount\nAda,12\nGrace,7\n")
	post(t, ts, "/api/v1/processes/1/instances", `{"variables":{"upload":`+ref+`}}`, "application/json")

	handlers := mustConnectors(t, "csv")
	var handlerErr error
	for typ, h := range handlers {
		inner := h
		handlers[typ] = worker.ExecFunc(func(ctx context.Context, j worker.Job) (map[string]any, error) {
			out, err := inner.Run(ctx, j)
			handlerErr = err
			return out, err
		})
	}
	w := worker.New(worker.Options{Server: ts.URL, ID: "csv-1", Handlers: handlers})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if handlerErr != nil {
		t.Fatalf("the worker's handler failed: %v", handlerErr)
	}
	if running := runningInstances(t, ts); running != 0 {
		t.Fatalf("%d instances still running — the worker did not complete the job", running)
	}
	vars := instanceVariables(t, ts)
	if list, _ := vars["orders"].([]any); len(list) != 2 {
		t.Fatalf("orders = %v, want two rows parsed from the document", vars["orders"])
	}
}

// Outside a job there is no lease to read under: a handler called directly is told so
// rather than reading anything.
func TestDocumentsNeedAJob(t *testing.T) {
	if _, _, err := workersdk.ReadDocument(context.Background(), "doc_x"); err != workersdk.ErrNoDocuments {
		t.Fatalf("ReadDocument outside a job = %v", err)
	}
	if _, err := workersdk.UploadDocument(context.Background(), "a.csv", "text/csv", []byte("x")); err != workersdk.ErrNoDocuments {
		t.Fatalf("UploadDocument outside a job = %v", err)
	}
	if workersdk.DocumentID(map[string]any{"$document": "doc_y"}) != "doc_y" || workersdk.DocumentID(`{"$document":"doc_z"}`) != "doc_z" {
		t.Fatal("DocumentID does not read a reference")
	}
}

// The lease is what makes a read the job's: a worker that does not hold it, or asks
// for a document the job's instance was never given, reads nothing.
func TestAJobDocumentIsReadOnlyUnderItsLease(t *testing.T) {
	ts := bareAtlas(t, csvConnectorModel, api.WithOffloadedConnectorKinds([]string{"csv"}))
	ref := uploadDocument(t, ts, "orders.csv", "name,amount\nAda,12\n")
	other := uploadDocument(t, ts, "other.csv", "name,amount\nGrace,7\n")
	post(t, ts, "/api/v1/processes/1/instances", `{"variables":{"upload":`+ref+`}}`, "application/json")

	var leased struct {
		Jobs []struct {
			JobKey     uint64 `json:"jobKey"`
			LeaseToken uint64 `json:"leaseToken"`
		} `json:"jobs"`
	}
	_ = json.Unmarshal(post(t, ts, "/api/v1/jobs/activate",
		`{"type":"io.atlas.csv-import","worker":"csv-1","leaseMs":60000,"waitMs":0,"maxJobs":1}`, "application/json"), &leased)
	if len(leased.Jobs) != 1 {
		t.Fatalf("leased %d jobs", len(leased.Jobs))
	}
	job := leased.Jobs[0]
	read := func(id, worker string, token uint64) int {
		body := `{"worker":"` + worker + `","leaseToken":` + itoa(token) + `}`
		res, err := http.Post(ts.URL+"/api/v1/jobs/"+itoa(job.JobKey)+"/documents/"+id, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	idOf := func(raw string) string { return workersdk.DocumentID(raw) }
	if code := read(idOf(ref), "csv-1", job.LeaseToken); code != http.StatusOK {
		t.Fatalf("the lease holder could not read its job's document: %d", code)
	}
	if code := read(idOf(ref), "someone-else", job.LeaseToken); code != http.StatusConflict {
		t.Fatalf("another worker read under a lease it does not hold: %d", code)
	}
	if code := read(idOf(ref), "csv-1", job.LeaseToken+1); code != http.StatusConflict {
		t.Fatalf("a stale lease token read: %d", code)
	}
	if code := read(idOf(other), "csv-1", job.LeaseToken); code != http.StatusNotFound {
		t.Fatalf("the lease holder read a document its instance was never given: %d", code)
	}
}
