package s3_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/s3"
	"github.com/pblumer/atlas/limits"
)

// A document another Worker hands over is bounded by the document budget, not by a
// variable's: an exported attachment never was a variable
// (ADR-0478).
func TestPutDocumentStoresMoreThanAVariableHolds(t *testing.T) {
	var got []byte
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		hdr = r.Header.Clone()
		w.Header().Set("ETag", `"abc"`)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	c := s3.NewHTTPClient(s3.Connector{Endpoint: srv.URL, Region: "eu-central-1", AccessKeyID: "AKIA", SecretAccessKey: "s",
		Now: func() time.Time { return time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC) }})
	doc := bytes.Repeat([]byte("%"), int(limits.Default().Variable)+1)
	reg := s3.NewRegistry()
	reg.Register("belege", c)
	res, err := s3.Documents(reg).PutDocument(context.Background(), "belege", " eingang ", "2026/R.pdf", "application/pdf",
		"inline; filename*=UTF-8''R.pdf", doc, map[string]string{"atlas-scanned": "true"})
	if err != nil {
		t.Fatalf("PutDocument: %v", err)
	}
	if !bytes.Equal(got, doc) {
		t.Errorf("the store received %d bytes, want %d", len(got), len(doc))
	}
	if hdr.Get("Content-Type") != "application/pdf" || hdr.Get("Content-Disposition") != "inline; filename*=UTF-8''R.pdf" ||
		hdr.Get("x-amz-meta-atlas-scanned") != "true" {
		t.Errorf("headers = %v", hdr)
	}
	// SigV4 requires host and the x-amz-* headers to be signed and leaves the standard
	// ones to the signer; the user metadata is what must not travel unsigned.
	if !strings.Contains(hdr.Get("Authorization"), "x-amz-meta-atlas-scanned") {
		t.Errorf("the metadata is sent but not signed: %s", hdr.Get("Authorization"))
	}
	if res["etag"] != "abc" || res["bucket"] != "eingang" || res["key"] != "2026/R.pdf" {
		t.Errorf("answer = %v", res)
	}
}

func TestPutDocumentRefusals(t *testing.T) {
	c := s3.NewHTTPClient(s3.Connector{Endpoint: "http://127.0.0.1:1", Region: "eu-central-1", AccessKeyID: "AKIA", SecretAccessKey: "s"})
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"no key":        func() error { _, err := c.PutDocument(ctx, "b", "", "", "", []byte("x"), nil); return err },
		"a path bucket": func() error { _, err := c.PutDocument(ctx, "b/../c", "k", "", "", []byte("x"), nil); return err },
		"past the budget": func() error {
			_, err := c.PutDocument(ctx, "b", "k", "", "", make([]byte, limits.Default().Document+1), nil)
			return err
		},
		"no region": func() error {
			_, err := s3.NewHTTPClient(s3.Connector{AccessKeyID: "A", SecretAccessKey: "s"}).PutDocument(ctx, "b", "k", "", "", []byte("x"), nil)
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s: stored", name)
		}
	}
	if _, err := s3.Documents(nil).PutDocument(ctx, "belege", "b", "k", "", "", nil, nil); err == nil {
		t.Error("a document store holding no Worker stored a document")
	}
	if _, err := s3.Documents(s3.NewRegistry()).PutDocument(ctx, "belege", "b", "k", "", "", nil, nil); err == nil ||
		!strings.Contains(err.Error(), `"belege"`) {
		t.Errorf("an unknown store = %v, want it named", err)
	}
}

// plainClient is an S3 Worker client that can only perform the eight operations.
type plainClient struct{}

func (plainClient) Do(context.Context, s3.Request) (any, error) { return nil, nil }

func TestAStoreThatCannotTakeADocumentSaysSo(t *testing.T) {
	reg := s3.NewRegistry()
	reg.Register("alt", plainClient{})
	if _, err := s3.Documents(reg).PutDocument(context.Background(), "alt", "b", "k", "", "", []byte("x"), nil); err == nil ||
		!strings.Contains(err.Error(), "cannot store a document") {
		t.Errorf("a client without the document path = %v", err)
	}
}

// Atlas's own document store reads a document back whole and deletes it through the
// Worker it keeps documents with (ADR-draft-a-file-is-a-document-not-a-variable): the
// read is bounded by the document budget, not by a variable's, and a missing key is an
// answer the store can tell apart from a failure.
func TestGetAndDeleteDocument(t *testing.T) {
	objects := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			objects[r.URL.Path], _ = io.ReadAll(r.Body)
		case http.MethodGet:
			b, ok := objects[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code><Message>gone</Message></Error>`)
				return
			}
			_, _ = w.Write(b)
		case http.MethodDelete:
			delete(objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	c := s3.NewHTTPClient(s3.Connector{Endpoint: srv.URL, Region: "eu-central-1", AccessKeyID: "AKIA", SecretAccessKey: "s"})
	reg := s3.NewRegistry()
	reg.Register("docs", c)
	d := s3.Documents(reg)
	ctx := context.Background()
	doc := bytes.Repeat([]byte("x"), int(limits.Default().Variable)+10)
	if _, err := d.PutDocument(ctx, "docs", "atlas", "documents/abc", "text/plain", "", doc, nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := d.GetDocument(ctx, "docs", "atlas", "documents/abc")
	if err != nil || !bytes.Equal(got, doc) {
		t.Fatalf("get = %d bytes, %v; want %d", len(got), err, len(doc))
	}
	if err := d.DeleteDocument(ctx, "docs", "atlas", "documents/abc"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := d.GetDocument(ctx, "docs", "atlas", "documents/abc"); !errors.Is(err, s3.ErrNoSuchDocument) {
		t.Fatalf("get after delete: %v, want ErrNoSuchDocument", err)
	}
	if _, err := d.GetDocument(ctx, "nobody", "atlas", "k"); err == nil {
		t.Fatalf("an unknown Worker answered")
	}
}

// fakeDocs stands in for the document store a Worker reaches.
type fakeDocs struct {
	held   map[string][]byte
	stored []string
}

func (f *fakeDocs) ReadDocument(_ context.Context, id string) ([]byte, string, string, error) {
	b, ok := f.held[id]
	if !ok {
		return nil, "", "", errors.New("not given to this instance")
	}
	return b, "antrag.pdf", "application/pdf", nil
}

func (f *fakeDocs) StoreDocument(_ context.Context, name, _ string, data []byte) (map[string]any, error) {
	f.stored = append(f.stored, name+"="+string(data))
	return map[string]any{"$document": "doc_new", "name": name}, nil
}

// The two document operations move a document between a bucket and Atlas's document
// store without it passing through a variable (ADR-draft-a-file-is-a-document-not-a-variable).
func TestDocumentOperations(t *testing.T) {
	objects := map[string][]byte{}
	disposition := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			objects[r.URL.Path], _ = io.ReadAll(r.Body)
			disposition[r.URL.Path] = r.Header.Get("Content-Disposition")
		case http.MethodGet:
			_, _ = w.Write(objects[r.URL.Path])
		}
	}))
	t.Cleanup(srv.Close)
	reg := s3.NewRegistry()
	reg.Register("archiv", s3.NewHTTPClient(s3.Connector{Endpoint: srv.URL, Region: "eu-central-1", AccessKeyID: "AKIA", SecretAccessKey: "s"}))
	docs := &fakeDocs{held: map[string][]byte{"doc_a": []byte("%PDF")}}
	ctx := context.Background()

	res, err := s3.RunWith(ctx, s3.Job{Connector: "archiv", Operation: "put-document", Bucket: "akten", Key: "1/antrag.pdf",
		Content: `{"$document":"doc_a","name":"antrag.pdf"}`}, reg, docs)
	if err != nil {
		t.Fatalf("put-document: %v", err)
	}
	if string(objects["/akten/1/antrag.pdf"]) != "%PDF" || !strings.Contains(disposition["/akten/1/antrag.pdf"], "antrag.pdf") {
		t.Fatalf("bucket = %q, disposition %q", objects["/akten/1/antrag.pdf"], disposition["/akten/1/antrag.pdf"])
	}
	if m, _ := res.(map[string]any); m["document"] != "doc_a" {
		t.Errorf("answer = %v", res)
	}

	objects["/akten/eingang/brief.txt"] = []byte("Brief")
	res, err = s3.RunWith(ctx, s3.Job{Connector: "archiv", Operation: "get-document", Bucket: "akten", Key: "eingang/brief.txt"}, reg, docs)
	if err != nil {
		t.Fatalf("get-document: %v", err)
	}
	if m, _ := res.(map[string]any); m["$document"] != "doc_new" || len(docs.stored) != 1 || docs.stored[0] != "brief.txt=Brief" {
		t.Fatalf("answer %v, stored %v", res, docs.stored)
	}

	for name, j := range map[string]s3.Job{
		"not a reference": {Connector: "archiv", Operation: "put-document", Bucket: "akten", Key: "k", Content: "plain text"},
		"not given":       {Connector: "archiv", Operation: "put-document", Bucket: "akten", Key: "k", Content: `{"$document":"doc_b"}`},
	} {
		if _, err := s3.RunWith(ctx, j, reg, docs); err == nil {
			t.Errorf("%s: put-document succeeded", name)
		}
	}
	if _, err := s3.Run(ctx, s3.Job{Connector: "archiv", Operation: "get-document", Bucket: "akten", Key: "k"}, reg); err == nil ||
		!strings.Contains(err.Error(), "document store") {
		t.Errorf("get-document without a store: %v", err)
	}
}
