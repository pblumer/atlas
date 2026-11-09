package docstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// ObjectStore is what the bucket backend needs of an S3 Worker: put, read back and
// delete one object, through a Worker the installation already defines, so the store
// never holds the Worker's access key (connector/s3.Documenter satisfies it).
type ObjectStore interface {
	PutDocument(ctx context.Context, worker, bucket, key, contentType, disposition string, data []byte, metadata map[string]string) (map[string]any, error)
	GetDocument(ctx context.Context, worker, bucket, key string) ([]byte, error)
	DeleteDocument(ctx context.Context, worker, bucket, key string) error
}

// Bucket keeps documents' bytes in an S3 bucket, under prefix + hash. It does not list
// the bucket — the store keeps a journal of what it wrote instead — because a bucket
// may hold other objects and listing one can be slow and costly.
type Bucket struct {
	objects  ObjectStore
	worker   string
	bucket   string
	prefix   string
	notFound error
}

// NewBucket returns the bucket backend. notFound is the error the object store answers
// for a missing key, so Open can say "not available" rather than "failed".
func NewBucket(objects ObjectStore, worker, bucket, prefix string, notFound error) (*Bucket, error) {
	if strings.TrimSpace(worker) == "" || strings.TrimSpace(bucket) == "" {
		return nil, errors.New("docstore: the s3 backend needs a Worker and a bucket")
	}
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &Bucket{objects: objects, worker: worker, bucket: bucket, prefix: prefix, notFound: notFound}, nil
}

// Name implements Backend.
func (b *Bucket) Name() string { return "s3" }

func (b *Bucket) key(hash string) string { return b.prefix + hash }

// Put implements Backend. The document is at most limits.Document, so it is read whole:
// the S3 Worker signs a payload it holds.
func (b *Bucket) Put(ctx context.Context, hash, path string, _ int64, contentType string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	disposition := "inline"
	if RendersAsPage(contentType) {
		disposition = "attachment"
	}
	_, err = b.objects.PutDocument(ctx, b.worker, b.bucket, b.key(hash), contentType, disposition, data,
		map[string]string{"atlas-sha256": hash})
	return err
}

// Open implements Backend.
func (b *Bucket) Open(ctx context.Context, hash string) (io.ReadCloser, error) {
	data, err := b.objects.GetDocument(ctx, b.worker, b.bucket, b.key(hash))
	if b.notFound != nil && errors.Is(err, b.notFound) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("docstore: s3: %w", err)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Delete implements Backend.
func (b *Bucket) Delete(ctx context.Context, hash string) error {
	return b.objects.DeleteDocument(ctx, b.worker, b.bucket, b.key(hash))
}
