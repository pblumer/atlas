package s3_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/s3"
)

// The document paths refuse what they cannot do with a reason, rather than calling a
// store half-way (ADR-0543).
func TestDocumentPathsRefuse(t *testing.T) {
	ctx := context.Background()
	if _, err := s3.Documents(nil).GetDocument(ctx, "w", "b", "k"); err == nil {
		t.Error("a read with no registry answered")
	}
	if err := s3.Documents(nil).DeleteDocument(ctx, "w", "b", "k"); err == nil {
		t.Error("a delete with no registry answered")
	}
	reg := s3.NewRegistry()
	reg.Register("plain", plainClient{})
	if _, err := s3.Documents(reg).GetDocument(ctx, "plain", "b", "k"); err == nil || !strings.Contains(err.Error(), "cannot read or delete") {
		t.Errorf("a read through a client that cannot = %v", err)
	}
	if _, err := s3.Documents(reg).GetDocument(ctx, "nobody", "b", "k"); err == nil {
		t.Error("a read through an unknown Worker answered")
	}
	docs := &fakeDocs{held: map[string][]byte{"doc_a": []byte("x")}}
	if _, err := s3.RunWith(ctx, s3.Job{Connector: "plain", Operation: "put-document", Bucket: "b", Key: "k",
		Content: `{"$document":"doc_a"}`}, reg, docs); err == nil || !strings.Contains(err.Error(), "cannot store") {
		t.Errorf("put-document through a client that cannot = %v", err)
	}
	if _, err := s3.RunWith(ctx, s3.Job{Connector: "plain", Operation: "put-document", Bucket: "b", Key: "k",
		Content: `{"$document":"doc_a"}`}, reg, nil); err == nil || !strings.Contains(err.Error(), "no access") {
		t.Errorf("put-document with no store = %v", err)
	}
	if _, err := s3.RunWith(ctx, s3.Job{Connector: "plain", Operation: "get-document", Bucket: "b", Key: "k"}, reg, docs); err == nil {
		t.Error("get-document through a client that cannot read answered")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>no</Message></Error>`))
	}))
	t.Cleanup(srv.Close)
	c := s3.NewHTTPClient(s3.Connector{Endpoint: srv.URL, Region: "eu-central-1", AccessKeyID: "AKIA", SecretAccessKey: "s"})
	if _, err := c.GetDocument(ctx, "akten", "k"); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("a refused read = %v", err)
	}
	if err := c.DeleteDocument(ctx, "akten", "k"); err == nil {
		t.Error("a refused delete answered")
	}
	if _, err := c.GetDocument(ctx, "a/b", "k"); err == nil {
		t.Error("a bucket that is a path was accepted")
	}
	if err := c.DeleteDocument(ctx, "a/b", "k"); err == nil {
		t.Error("a delete in a bucket that is a path was accepted")
	}
	reg.Register("denied", c)
	if _, err := s3.RunWith(ctx, s3.Job{Connector: "denied", Operation: "put-document", Bucket: "akten", Key: "k",
		Content: `{"$document":"doc_a"}`}, reg, docs); err == nil {
		t.Error("a put the store refused answered")
	}
	if _, err := s3.RunWith(ctx, s3.Job{Connector: "denied", Operation: "get-document", Bucket: "akten", Key: "k"}, reg, docs); err == nil {
		t.Error("a get the store refused answered")
	}
}

// A page a browser would run is put as an attachment, so a link to it downloads it.
func TestPutDocumentStoresAPageAsAnAttachment(t *testing.T) {
	var disposition string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		disposition = r.Header.Get("Content-Disposition")
	}))
	t.Cleanup(srv.Close)
	reg := s3.NewRegistry()
	reg.Register("archiv", s3.NewHTTPClient(s3.Connector{Endpoint: srv.URL, Region: "eu-central-1", AccessKeyID: "AKIA", SecretAccessKey: "s"}))
	docs := pageDocs{}
	if _, err := s3.RunWith(context.Background(), s3.Job{Connector: "archiv", Operation: "put-document", Bucket: "akten", Key: "x.html",
		Content: `{"$document":"doc_p"}`}, reg, docs); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(disposition, "attachment") {
		t.Errorf("disposition = %q", disposition)
	}
}

type pageDocs struct{}

func (pageDocs) ReadDocument(context.Context, string) ([]byte, string, string, error) {
	return []byte("<html></html>"), "", "text/html; charset=utf-8", nil
}
func (pageDocs) StoreDocument(context.Context, string, string, []byte) (map[string]any, error) {
	return nil, nil
}
