package s3_test

import (
	"bytes"
	"context"
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
