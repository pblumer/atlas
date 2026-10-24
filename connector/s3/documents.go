package s3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/limits"
)

// A document another Worker hands to an S3 Worker
// (ADR-0478).
//
// put-object writes what a process composed, through a variable, under the variable's
// budget. A mail attachment exported by the mail Worker never was a variable: it goes
// from the mailbox to the store inside one Worker Instance, and the engine holds
// neither its bytes nor this Worker's access key. So it is its own path, bounded by
// limits.Document — the budget for exactly that — and not reachable from a model as an
// operation of its own: only a Worker Type that already holds a document can call it.

// DocumentPutter is a [Client] that can store a document handed to it by another Worker.
type DocumentPutter interface {
	PutDocument(ctx context.Context, bucket, key, contentType, disposition string, data []byte, metadata map[string]string) (map[string]any, error)
}

// PutDocument stores data under bucket/key with the given content type, disposition
// and user metadata, and answers where it went — the same shape put-object answers.
func (c *HTTPClient) PutDocument(ctx context.Context, bucket, key, contentType, disposition string, data []byte, metadata map[string]string) (map[string]any, error) {
	req := trimIdentifiers(Request{Operation: "put-object", Bucket: bucket, Key: key, ContentType: contentType, Metadata: metadata})
	if req.Bucket == "" || req.Key == "" {
		return nil, fmt.Errorf("s3: a document needs a bucket and a key")
	}
	if err := checkBucket(req.Bucket); err != nil {
		return nil, err
	}
	if c.conn.Region == "" {
		return nil, fmt.Errorf("s3: this Worker has no region (add \"region\" to its vault bundle); SigV4 signs with it, so there is no default that could be right")
	}
	if limit := limits.Default().Document; int64(len(data)) > limit {
		return nil, fmt.Errorf("s3: a document of %d bytes is more than the %d a Worker carries in one piece (ATLAS_LIMIT_DOCUMENT)", len(data), limit)
	}
	header := http.Header{}
	if req.ContentType != "" {
		header.Set("Content-Type", req.ContentType)
	}
	if d := strings.TrimSpace(disposition); d != "" {
		header.Set("Content-Disposition", d)
	}
	if err := applyMetadata(header, req.Metadata); err != nil {
		return nil, err
	}
	resp, _, err := c.call(ctx, http.MethodPut, c.objectURL(req.Bucket, req.Key, nil), header, data, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"bucket":    req.Bucket,
		"key":       req.Key,
		"etag":      unquoteETag(resp.Header.Get("ETag")),
		"versionId": resp.Header.Get("x-amz-version-id"),
		"size":      json.Number(strconv.Itoa(len(data))),
	}, nil
}

// Documents adapts a registry to the mail Worker's document store: the Worker a task
// names is resolved here, among the S3 Workers this process holds, exactly as an S3
// task's would be — so an export reaches a store under the same identity and the same
// configuration as any other task naming it.
func Documents(reg *Registry) Documenter { return Documenter{reg: reg} }

// Documenter is the adapter [Documents] returns.
type Documenter struct{ reg *Registry }

// PutDocument puts a document through the named Worker.
func (d Documenter) PutDocument(ctx context.Context, worker, bucket, key, contentType, disposition string, data []byte, metadata map[string]string) (map[string]any, error) {
	if d.reg == nil {
		return nil, fmt.Errorf("s3: no S3 Worker is held here, so %q cannot be reached", worker)
	}
	client, ok := d.reg.Client(worker)
	if !ok {
		return nil, d.reg.Unresolved("s3", worker)
	}
	p, ok := client.(DocumentPutter)
	if !ok {
		return nil, fmt.Errorf("s3: Worker %q cannot store a document handed to it by another Worker", worker)
	}
	return p.PutDocument(ctx, bucket, key, contentType, disposition, data, metadata)
}
