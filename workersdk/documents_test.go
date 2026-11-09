package workersdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeDocumentServer answers the two routes a job's document access calls: the read under
// the job's lease, and the upload.
func fakeDocumentServer(t *testing.T, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Method+" "+r.URL.RequestURI()+" auth="+r.Header.Get("Authorization"))
		switch {
		case r.URL.Path == "/api/v1/jobs/7/documents/doc_ok":
			var body struct {
				Worker     string `json:"worker"`
				LeaseToken uint64 `json:"leaseToken"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Worker != "w-1" || body.LeaseToken != 42 {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"error":"not the lease holder"}`)
				return
			}
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("X-Atlas-Document-Name", "m%C3%A4rz.csv")
			_, _ = io.WriteString(w, "a,b\n1,2\n")
		case r.URL.Path == "/api/v1/jobs/7/documents/doc_gone":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"no document with that id for this job"}`)
		case r.URL.Path == "/api/v1/documents" && r.URL.Query().Get("name") == "fail.csv":
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = io.WriteString(w, `{"error":"too large"}`)
		case r.URL.Path == "/api/v1/documents" && r.URL.Query().Get("name") == "garbled.csv":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `not json`)
		case r.URL.Path == "/api/v1/documents":
			data, _ := io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"$document": "doc_new", "name": r.URL.Query().Get("name"),
				"contentType": r.Header.Get("Content-Type"), "size": len(data),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A job's handler reads a document under its lease and stores a new one through the
// upload route (ADR-draft-a-file-is-a-document-not-a-variable).
func TestDocumentContextReadsAndUploadsForItsJob(t *testing.T) {
	var seen []string
	srv := fakeDocumentServer(t, &seen)
	w := New(Options{Server: srv.URL, ID: "w-1", Token: "tok"})
	ctx := w.DocumentContext(context.Background(), Job{JobKey: 7, LeaseToken: 42})

	data, doc, err := ReadDocument(ctx, "doc_ok")
	if err != nil || string(data) != "a,b\n1,2\n" || doc.Name != "märz.csv" || doc.ContentType != "text/csv; charset=utf-8" || doc.ID != "doc_ok" {
		t.Fatalf("read = %q %+v %v", data, doc, err)
	}
	if _, _, err := ReadDocument(ctx, "doc_gone"); err == nil || !strings.Contains(err.Error(), "no document with that id") {
		t.Fatalf("a document the job was not given = %v", err)
	}
	other := w.DocumentContext(context.Background(), Job{JobKey: 7, LeaseToken: 1})
	var se *statusError
	if _, _, err := ReadDocument(other, "doc_ok"); !errors.As(err, &se) || se.status != http.StatusConflict {
		t.Fatalf("a stale lease = %v", err)
	}

	ref, err := UploadDocument(ctx, "bericht.csv", "", []byte("x,y\n"))
	if err != nil || ref["$document"] != "doc_new" || ref["contentType"] != "application/octet-stream" {
		t.Fatalf("upload = %v, %v", ref, err)
	}
	if DocumentID(ref) != "doc_new" {
		t.Fatalf("DocumentID of the answer = %q", DocumentID(ref))
	}
	if _, err := UploadDocument(ctx, "fail.csv", "text/csv", []byte("x")); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("a refused upload = %v", err)
	}
	if _, err := UploadDocument(ctx, "garbled.csv", "text/csv", []byte("x")); err == nil {
		t.Fatal("an upload answered with no reference succeeded")
	}
	for _, s := range seen {
		if !strings.Contains(s, "auth=Bearer tok") {
			t.Errorf("request without the worker's credential: %s", s)
		}
	}
}

func TestDocumentAccessWithoutAServer(t *testing.T) {
	w := New(Options{Server: "http://127.0.0.1:1", ID: "w-1"})
	ctx := w.DocumentContext(context.Background(), Job{JobKey: 7, LeaseToken: 42})
	if _, _, err := ReadDocument(ctx, "doc_ok"); err == nil {
		t.Error("a read with no server answered")
	}
	if _, err := UploadDocument(ctx, "a.csv", "text/csv", []byte("x")); err == nil {
		t.Error("an upload with no server answered")
	}
	// A request that cannot even be built fails before it is sent.
	bad := New(Options{Server: "http://bad host", ID: "w-1"}).DocumentContext(context.Background(), Job{JobKey: 7, LeaseToken: 42})
	if _, _, err := ReadDocument(bad, "doc_ok"); err == nil {
		t.Error("a read against an unparseable server URL answered")
	}
	if _, err := UploadDocument(bad, "a.csv", "", nil); err == nil {
		t.Error("an upload against an unparseable server URL answered")
	}
	// Outside a job there is no lease to read under.
	if _, _, err := ReadDocument(context.Background(), "doc_ok"); !errors.Is(err, ErrNoDocuments) {
		t.Errorf("read outside a job = %v", err)
	}
	if _, err := UploadDocument(context.Background(), "a.csv", "", nil); !errors.Is(err, ErrNoDocuments) {
		t.Errorf("upload outside a job = %v", err)
	}
	if DocumentID(`{"$document":"doc_t"}`) != "doc_t" {
		t.Error("DocumentID of JSON text")
	}
	for _, v := range []any{nil, 42, "not json", map[string]any{"name": "x"}} {
		if DocumentID(v) != "" {
			t.Errorf("DocumentID(%v) = %q", v, DocumentID(v))
		}
	}
}
