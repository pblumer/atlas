package s3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
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

// DocumentGetter is a [Client] that can read a document back whole, bounded by
// limits.Document rather than by a variable's budget, and delete it — what Atlas's own
// document store needs of a bucket it keeps documents in
// (ADR-draft-a-file-is-a-document-not-a-variable).
type DocumentGetter interface {
	GetDocument(ctx context.Context, bucket, key string) ([]byte, error)
	DeleteDocument(ctx context.Context, bucket, key string) error
}

// ErrNoSuchDocument is GetDocument's answer for a key the bucket does not hold.
var ErrNoSuchDocument = errors.New("s3: no such object")

// GetDocument reads bucket/key whole. It refuses an object over limits.Document rather
// than returning part of it.
func (c *HTTPClient) GetDocument(ctx context.Context, bucket, key string) ([]byte, error) {
	req := trimIdentifiers(Request{Operation: "get-object", Bucket: bucket, Key: key})
	if err := checkBucket(req.Bucket); err != nil {
		return nil, err
	}
	limit := limits.Default().Document
	_, raw, err := c.callUpTo(ctx, http.MethodGet, c.objectURL(req.Bucket, req.Key, nil), nil, nil, req, limit)
	if errors.Is(err, errNoSuchKey) {
		return nil, fmt.Errorf("%w: %s/%s", ErrNoSuchDocument, req.Bucket, req.Key)
	}
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("s3: %s/%s is larger than the %d bytes a document may be (ATLAS_LIMIT_DOCUMENT)", req.Bucket, req.Key, limit)
	}
	return raw, nil
}

// DeleteDocument removes bucket/key; a key that is not there is no error, as for
// delete-object.
func (c *HTTPClient) DeleteDocument(ctx context.Context, bucket, key string) error {
	req := trimIdentifiers(Request{Operation: "delete-object", Bucket: bucket, Key: key})
	if err := checkBucket(req.Bucket); err != nil {
		return err
	}
	_, _, err := c.call(ctx, http.MethodDelete, c.objectURL(req.Bucket, req.Key, nil), nil, nil, req)
	return err
}

// GetDocument reads a document through the named Worker.
func (d Documenter) GetDocument(ctx context.Context, worker, bucket, key string) ([]byte, error) {
	g, err := d.getter(worker)
	if err != nil {
		return nil, err
	}
	return g.GetDocument(ctx, bucket, key)
}

// DeleteDocument deletes a document through the named Worker.
func (d Documenter) DeleteDocument(ctx context.Context, worker, bucket, key string) error {
	g, err := d.getter(worker)
	if err != nil {
		return err
	}
	return g.DeleteDocument(ctx, bucket, key)
}

func (d Documenter) getter(worker string) (DocumentGetter, error) {
	if d.reg == nil {
		return nil, fmt.Errorf("s3: no S3 Worker is held here, so %q cannot be reached", worker)
	}
	client, ok := d.reg.Client(worker)
	if !ok {
		return nil, d.reg.Unresolved("s3", worker)
	}
	g, ok := client.(DocumentGetter)
	if !ok {
		return nil, fmt.Errorf("s3: Worker %q cannot read or delete a document for Atlas's document store", worker)
	}
	return g, nil
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

// DocumentStore is Atlas's own document store as an S3 task reaches it
// (ADR-draft-a-file-is-a-document-not-a-variable): the server's store in process, the
// job's lease-bound access on a Worker Instance.
type DocumentStore interface {
	// ReadDocument reads a document the task's instance holds.
	ReadDocument(ctx context.Context, id string) (data []byte, name, contentType string, err error)
	// StoreDocument stores bytes as a new document and answers its reference.
	StoreDocument(ctx context.Context, name, contentType string, data []byte) (map[string]any, error)
}

// documentIDOf reads the document id from a resolved content value: the canonical JSON
// of a reference, {"$document": "doc_…", …}.
func documentIDOf(content string) string {
	var ref struct {
		ID string `json:"$document"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(content)), &ref) != nil || !strings.HasPrefix(ref.ID, "doc_") {
		return ""
	}
	return ref.ID
}

// putFromStore performs put-document: the document a reference names goes from the
// document store into the bucket, under its detected type. A type a browser would run
// as a page is stored as an attachment, so a link to it downloads it.
func putFromStore(ctx context.Context, j Job, client Client, docs DocumentStore) (any, error) {
	id := documentIDOf(j.Content)
	if id == "" {
		return nil, fmt.Errorf("s3: put-document's content must be a document reference (a variable holding {\"$document\": …}), and it is not")
	}
	if docs == nil {
		return nil, fmt.Errorf("s3: put-document names document %s, and this worker has no access to the document store", id)
	}
	p, ok := client.(DocumentPutter)
	if !ok {
		return nil, fmt.Errorf("s3: Worker %q cannot store a document", j.Connector)
	}
	data, name, contentType, err := docs.ReadDocument(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("s3: read document %s: %w", id, err)
	}
	disposition := "inline"
	if rendersAsPage(contentType) {
		disposition = "attachment"
	}
	if name != "" {
		disposition += "; filename*=UTF-8''" + url.PathEscape(name)
	}
	res, err := p.PutDocument(ctx, j.Bucket, j.Key, contentType, disposition, data, j.Metadata)
	if err != nil {
		return nil, err
	}
	res["document"] = id
	return res, nil
}

// getIntoStore performs get-document: the object is read whole, bounded by
// limits.Document, and stored as a new document whose reference is the answer.
func getIntoStore(ctx context.Context, j Job, client Client, docs DocumentStore) (any, error) {
	if docs == nil {
		return nil, fmt.Errorf("s3: get-document stores into the document store, and this worker has no access to it")
	}
	g, ok := client.(DocumentGetter)
	if !ok {
		return nil, fmt.Errorf("s3: Worker %q cannot read a document for the document store", j.Connector)
	}
	data, err := g.GetDocument(ctx, j.Bucket, j.Key)
	if err != nil {
		return nil, err
	}
	name := path.Base(strings.TrimSpace(j.Key))
	return docs.StoreDocument(ctx, name, "", data)
}

// rendersAsPage reports whether a browser would run a document of this type as a page.
// The same list as the document store's (api/docstore.RendersAsPage), kept here because
// a connector does not import the server.
func rendersAsPage(contentType string) bool {
	ct := strings.ToLower(contentType)
	for _, p := range []string{"text/html", "image/svg", "text/xml", "application/xml", "application/xhtml", "text/xsl"} {
		if strings.HasPrefix(ct, p) {
			return true
		}
	}
	return false
}
