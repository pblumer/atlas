package s3

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// awsHostFormat is where a Worker with no endpoint points: AWS's regional S3 host, with
// the bucket as a subdomain.
//
// Virtual-host addressing is what AWS supports for buckets created since September 2020,
// and path-style is what every self-hosted store serves. Which one this worker uses
// follows from the one field an operator sets: no endpoint means AWS and virtual-host, an
// endpoint means their store and path-style. See ADR-draft-s3-object-store-worker's open
// question for the case that leaves uncovered.
const awsHostFormat = "s3.%s.amazonaws.com"

// Connector is the server-side configuration of one S3 Worker: where the store is, which
// region signs for it, and the access key.
//
// The three credential fields are resolved from the vault at build time and held only
// here, never persisted and never in a model (ADR-0069/0168). Region is in the credential
// bundle with them because SigV4 signs with it — a region that disagreed with the key
// produces a signature the store rejects, so the two belong in the one place they are set
// together.
type Connector struct {
	// Endpoint is the store's base URL. Empty means AWS at the regional host, which is
	// the only case where the bucket goes in the hostname.
	Endpoint string
	Region   string

	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string

	// Now supplies the signing instant. Nil means time.Now, which is what the engine and
	// every worker leave it as; a test injects one so a signature and a presigned URL are
	// assertable values rather than something that changes every run (AGENTS.md's
	// determinism rule).
	Now func() time.Time
}

// HTTPClient calls a real S3-compatible store over its HTTP API. It is stateless — every
// request carries its own signature rather than a session — so it is safe for concurrent
// use by the worker.
type HTTPClient struct {
	conn Connector
	http *http.Client
	// pathStyle is derived once at construction from whether an endpoint was configured,
	// so every call and every presigned URL address the store the same way. Deriving it
	// per call would be the same decision made eight times.
	pathStyle bool
	// base is the scheme and host the store answers on, without a trailing slash.
	base string
}

// NewHTTPClient builds an S3 client for a configured Worker, bounded by the shared worker
// call budget (ADR-0149). The worker may run on the run-loop goroutine, so an unbounded
// call would let a hung store stall the whole engine; see the nettimeout package doc.
func NewHTTPClient(conn Connector) *HTTPClient {
	conn.Endpoint = strings.TrimRight(strings.TrimSpace(conn.Endpoint), "/")
	conn.Region = strings.TrimSpace(conn.Region)
	c := &HTTPClient{conn: conn, http: nettimeout.HTTPClient()}
	if conn.Endpoint == "" {
		c.base = "https://" + fmt.Sprintf(awsHostFormat, conn.Region)
		c.pathStyle = false
		return c
	}
	c.base = conn.Endpoint
	// An endpoint given without a scheme is the one paste mistake worth fixing rather
	// than reporting: "minio.example:9000" is unambiguous about what was meant, and a URL
	// with no scheme parses into a path, which would address the bucket as part of the
	// hostname's text.
	if !strings.Contains(c.base, "://") {
		c.base = "https://" + c.base
	}
	c.pathStyle = true
	return c
}

// Base is the store's base URL, after defaulting and trimming. It is exported so a test
// can assert where a Worker is pointed without making a request, and so an
// operator-facing diagnostic can say it.
func (c *HTTPClient) Base() string { return c.base }

// now reads the signing instant through the injected clock, or the wall clock when there
// is none.
func (c *HTTPClient) now() time.Time {
	if c.conn.Now != nil {
		return c.conn.Now()
	}
	return time.Now()
}

// credentials is the access key this client signs with.
func (c *HTTPClient) credentials() credentials {
	return credentials{
		AccessKeyID:     c.conn.AccessKeyID,
		SecretAccessKey: c.conn.SecretAccessKey,
		SessionToken:    c.conn.SessionToken,
	}
}

// Do performs one operation. Every failure — a transport error, a non-2xx status, an
// operation nothing implements — is returned so the job stays pending and is retried, then
// raises an incident (ADR-0061), rather than completing a token on work that did not
// happen.
func (c *HTTPClient) Do(ctx context.Context, req Request) (any, error) {
	req = trimIdentifiers(req)
	spec, ok := Ops[req.Operation]
	if !ok {
		return nil, fmt.Errorf("s3: unknown operation %q (want %s)", req.Operation, strings.Join(OpNames(), ", "))
	}
	// The compiler guarantees the *attribute* is authored; it cannot guarantee what a FEEL
	// expression evaluates to at call time. An empty bucket or key here would otherwise be
	// spliced into a URL and answered with a 404 naming an address the author never wrote,
	// so it is caught where the fix — the expression — is still in view.
	if spec.NeedsBucket && req.Bucket == "" {
		return nil, fmt.Errorf("s3: %s has no bucket (the task's bucket resolved to nothing)", req.Operation)
	}
	if spec.NeedsKey && req.Key == "" {
		return nil, fmt.Errorf("s3: %s has no key (the task's key resolved to nothing)", req.Operation)
	}
	if spec.NeedsSource && (req.SourceBucket == "" || req.SourceKey == "") {
		return nil, fmt.Errorf("s3: %s has no source (the task's sourceBucket or sourceKey resolved to nothing)", req.Operation)
	}
	if err := checkBucket(req.Bucket); err != nil {
		return nil, err
	}
	if c.conn.Region == "" {
		return nil, fmt.Errorf("s3: this Worker has no region (add \"region\" to its vault bundle); SigV4 signs with it, so there is no default that could be right")
	}
	// Checked here rather than only in call, because the two presigns never reach call and
	// would otherwise mint a URL signed with an empty key — a link that looks minted and
	// is answered with a 403 by whoever clicks it.
	if strings.TrimSpace(c.conn.AccessKeyID) == "" || strings.TrimSpace(c.conn.SecretAccessKey) == "" {
		return nil, fmt.Errorf("s3: this Worker has no access key (set credentialsRef to a vault bundle {accessKeyId, secretAccessKey, region})")
	}
	switch req.Operation {
	case "put-object":
		return c.putObject(ctx, req)
	case "get-object":
		return c.getObject(ctx, req)
	case "head-object":
		return c.headObject(ctx, req)
	case "list-objects":
		return c.listObjects(ctx, req)
	case "copy-object":
		return c.copyObject(ctx, req)
	case "delete-object":
		return c.deleteObject(ctx, req)
	case "presign-get":
		return c.presignObject(req, http.MethodGet)
	case "presign-put":
		return c.presignObject(req, http.MethodPut)
	default:
		// Ops named it and the switch does not — a row added without its call. Better said
		// here than by a nil answer that completes a token on nothing.
		return nil, fmt.Errorf("s3: operation %q is in the table but not implemented", req.Operation)
	}
}

// trimIdentifiers strips surrounding whitespace from the values that *address* something
// in the store. A bucket, a key or a prefix that arrived as "rechnungen " is spliced into
// a URL and answered with a 404 that reads as a deleted object rather than as a stray
// space in a form field.
//
// It is deliberately only those. Content is what the model composed, and silently
// reshaping a document on its way into a bucket would be a different kind of surprise —
// one that is invisible until somebody diffs the object against what produced it.
func trimIdentifiers(req Request) Request {
	req.Bucket = strings.TrimSpace(req.Bucket)
	req.Key = strings.TrimSpace(req.Key)
	req.Prefix = strings.TrimSpace(req.Prefix)
	req.Delimiter = strings.TrimSpace(req.Delimiter)
	req.StartAfter = strings.TrimSpace(req.StartAfter)
	req.SourceBucket = strings.TrimSpace(req.SourceBucket)
	req.SourceKey = strings.TrimSpace(req.SourceKey)
	req.ContentType = strings.TrimSpace(req.ContentType)
	return req
}

// checkBucket refuses a bucket name that would change which *host* is addressed. A name
// carrying a slash or a colon splices a second path segment or a port into the URL, which
// under path-style addressing silently points the request at another bucket and under
// virtual-host addressing at another host entirely. The store would answer plausibly in
// both cases, which is what makes it worth refusing here.
func checkBucket(bucket string) error {
	if bucket == "" {
		return nil // an operation that needs none; Do has already checked the ones that do
	}
	if strings.ContainsAny(bucket, "/\\:?#@") || strings.Contains(bucket, "..") {
		return fmt.Errorf("s3: %q is not a bucket name (a bucket is one name, not a path); a value with a slash or a colon in it would address a different bucket or a different host", bucket)
	}
	return nil
}

// objectURL builds the URL for one key, addressed the way this Worker's store expects. The
// path is encoded exactly once, by the same function the signature is computed over, so
// the wire and the canonical request cannot disagree about a key with a space in it.
func (c *HTTPClient) objectURL(bucket, key string, query url.Values) string {
	// A listing addresses the bucket itself and carries no key, and the two shapes must
	// not differ by a trailing slash: the signature covers the path, so "/bucket/" and
	// "/bucket" are two different requests and only one of them is the one being made.
	suffix := ""
	if key != "" {
		suffix = "/" + key
	}
	var raw string
	if c.pathStyle {
		raw = c.base + canonicalPath("/"+bucket+suffix)
	} else {
		host := fmt.Sprintf(awsHostFormat, c.conn.Region)
		raw = "https://" + bucket + "." + host + canonicalPath(suffix)
	}
	if encoded := encodeQuery(query); encoded != "" {
		raw += "?" + encoded
	}
	return raw
}

// putObject writes one object. The content is the model's, decoded from base64 first when
// the task says so, and it is bounded by [MaxObjectBytes] *before* the request is built:
// a body that cannot land in a process variable on the way back out has no business going
// in, and refusing here says so with the key still in view.
func (c *HTTPClient) putObject(ctx context.Context, req Request) (any, error) {
	body, err := decodeContent(req)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	if req.ContentType != "" {
		header.Set("Content-Type", req.ContentType)
	}
	if err := applyMetadata(header, req.Metadata); err != nil {
		return nil, err
	}
	// A successful put answers with no body: the stored object's identity is in the
	// headers, which is why nothing here reads the response body.
	resp, _, err := c.call(ctx, http.MethodPut, c.objectURL(req.Bucket, req.Key, nil), header, body, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"bucket":    req.Bucket,
		"key":       req.Key,
		"etag":      unquoteETag(resp.Header.Get("ETag")),
		"versionId": resp.Header.Get("x-amz-version-id"),
		"size":      json.Number(strconv.Itoa(len(body))),
	}, nil
}

// getObject reads one object back into a process variable, in the form the task asked for.
//
// It is the operation an author reaches for by habit and the one that most often should
// have been presign-get, so the size refusal names that alternative rather than only the
// number that was exceeded — at the moment it fails, the key and the intent are both still
// in view, and "use the other operation" is the whole of the fix.
func (c *HTTPClient) getObject(ctx context.Context, req Request) (any, error) {
	resp, raw, err := c.call(ctx, http.MethodGet, c.objectURL(req.Bucket, req.Key, nil), nil, nil, req)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > MaxObjectBytes {
		return nil, fmt.Errorf("s3: %s/%s is larger than the %d byte limit a process variable holds; "+
			"it is refused rather than cut short, because a truncated document passes every format check and is still broken. "+
			"Use presign-get and hand the link out instead — the bytes then go straight from the store to whoever opens it",
			req.Bucket, req.Key, MaxObjectBytes)
	}
	return map[string]any{
		"bucket":       req.Bucket,
		"key":          req.Key,
		"content":      encodeContent(req, raw),
		"encoding":     contentEncoding(req),
		"contentType":  firstMediaType(resp.Header.Get("Content-Type")),
		"size":         json.Number(strconv.Itoa(len(raw))),
		"etag":         unquoteETag(resp.Header.Get("ETag")),
		"lastModified": resp.Header.Get("Last-Modified"),
	}, nil
}

// headObject answers what the store knows about one key, and whether it is there at all.
//
// A missing object is an *answer* here rather than a failure, which is the opposite of
// get-object and deliberate: "is it there" is the question this operation is asked, and a
// model that has to branch on it cannot do so from an incident. So a 404 comes back as
// exists:false rather than erroring, and every other non-2xx is still a failure — a 403
// means the credential cannot see the bucket, which is not the same statement as "there is
// nothing there" and must not read like one.
func (c *HTTPClient) headObject(ctx context.Context, req Request) (any, error) {
	resp, _, err := c.call(ctx, http.MethodHead, c.objectURL(req.Bucket, req.Key, nil), nil, nil, req)
	if err != nil {
		if errors.Is(err, errNoSuchKey) {
			return map[string]any{"bucket": req.Bucket, "key": req.Key, "exists": false}, nil
		}
		return nil, err
	}
	out := map[string]any{
		"bucket":       req.Bucket,
		"key":          req.Key,
		"exists":       true,
		"contentType":  firstMediaType(resp.Header.Get("Content-Type")),
		"etag":         unquoteETag(resp.Header.Get("ETag")),
		"lastModified": resp.Header.Get("Last-Modified"),
	}
	// Go parses Content-Length off a HEAD into the response itself, so that is what is
	// read first; the raw header is the fallback for a transport that left it only there.
	if resp.ContentLength >= 0 {
		out["size"] = json.Number(strconv.FormatInt(resp.ContentLength, 10))
	} else if n, err := strconv.ParseInt(strings.TrimSpace(resp.Header.Get("Content-Length")), 10, 64); err == nil {
		out["size"] = json.Number(strconv.FormatInt(n, 10))
	}
	if meta := readMetadata(resp.Header); len(meta) > 0 {
		out["metadata"] = meta
	}
	return out, nil
}

// listObjects is the search: every key under a prefix, optionally rolled up at a
// delimiter, from a cursor, capped.
//
// The answer carries nextStartAfter rather than S3's continuation token, and that is a
// choice worth stating. A continuation token is opaque, which makes a paging loop in a
// model a variable nobody can read; start-after is a key, so the loop is visible in the
// diagram and resumable by hand. The resume point is the greater of the last key and the
// last common prefix, because with a delimiter either may be the last thing on the page.
func (c *HTTPClient) listObjects(ctx context.Context, req Request) (any, error) {
	q := url.Values{}
	q.Set("list-type", "2")
	if req.Prefix != "" {
		q.Set("prefix", req.Prefix)
	}
	if req.Delimiter != "" {
		q.Set("delimiter", req.Delimiter)
	}
	if req.StartAfter != "" {
		q.Set("start-after", req.StartAfter)
	}
	if req.MaxKeys > 0 {
		q.Set("max-keys", strconv.FormatInt(int64(req.MaxKeys), 10))
	}
	// The listing addresses the bucket itself, so there is no key in the path.
	_, raw, err := c.call(ctx, http.MethodGet, c.objectURL(req.Bucket, "", q), nil, nil, req)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > MaxObjectBytes {
		return nil, fmt.Errorf("s3: the listing of %s is larger than the %d bytes this worker reads in one answer; lower maxKeys and page with startAfter",
			req.Bucket, MaxObjectBytes)
	}
	var parsed listBucketResult
	if err := xml.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("s3: list-objects returned a body that is not a listing: %w", err)
	}
	objects := make([]any, 0, len(parsed.Contents))
	var resume string
	for _, o := range parsed.Contents {
		objects = append(objects, map[string]any{
			"key":          o.Key,
			"size":         json.Number(strconv.FormatInt(o.Size, 10)),
			"etag":         unquoteETag(o.ETag),
			"lastModified": o.LastModified,
			"storageClass": o.StorageClass,
		})
		if o.Key > resume {
			resume = o.Key
		}
	}
	prefixes := make([]any, 0, len(parsed.CommonPrefixes))
	for _, p := range parsed.CommonPrefixes {
		prefixes = append(prefixes, p.Prefix)
		if p.Prefix > resume {
			resume = p.Prefix
		}
	}
	out := map[string]any{
		"bucket":    req.Bucket,
		"prefix":    req.Prefix,
		"objects":   objects,
		"prefixes":  prefixes,
		"count":     json.Number(strconv.Itoa(len(objects))),
		"truncated": parsed.IsTruncated,
	}
	// Only a truncated page has somewhere to resume from. Answering with a cursor on a
	// complete listing is how a model's paging loop never ends.
	if parsed.IsTruncated {
		out["nextStartAfter"] = resume
	} else {
		out["nextStartAfter"] = ""
	}
	return out, nil
}

// copyObject copies server-side: the bytes move between keys inside the store and never
// come near this process, which is what makes archiving a 40 MB document a step a process
// can take at all.
//
// Metadata authored on a copy replaces the source's rather than adding to it, because S3
// offers only those two modes and "add" is not one of them. Saying so in the panel is the
// only honest option; silently merging would need a read of the source's metadata first,
// which turns one idempotent call into two that can interleave.
func (c *HTTPClient) copyObject(ctx context.Context, req Request) (any, error) {
	if err := checkBucket(req.SourceBucket); err != nil {
		return nil, err
	}
	header := http.Header{}
	header.Set("x-amz-copy-source", canonicalPath("/"+req.SourceBucket+"/"+req.SourceKey))
	if len(req.Metadata) > 0 {
		header.Set("x-amz-metadata-directive", "REPLACE")
		if err := applyMetadata(header, req.Metadata); err != nil {
			return nil, err
		}
	}
	_, raw, err := c.call(ctx, http.MethodPut, c.objectURL(req.Bucket, req.Key, nil), header, nil, req)
	if err != nil {
		return nil, err
	}
	// A copy's 200 can still carry a failure in its body — S3 keeps the connection open
	// during a long copy and writes the error into the response it has already committed
	// to a 200. A caller that read the status alone would report a copy that did not
	// happen as done, which is the one failure mode this operation has that the others
	// do not.
	var parsed copyObjectResult
	if err := xml.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("s3: copy-object returned a body that is not a copy result: %w", err)
	}
	if parsed.Code != "" {
		return nil, fmt.Errorf("s3: copy-object failed after the store had answered 200: %s: %s", parsed.Code, parsed.Message)
	}
	return map[string]any{
		"bucket":       req.Bucket,
		"key":          req.Key,
		"sourceBucket": req.SourceBucket,
		"sourceKey":    req.SourceKey,
		"etag":         unquoteETag(parsed.ETag),
		"lastModified": parsed.LastModified,
	}, nil
}

// deleteObject removes one key. S3 answers a delete of an object that was never there with
// 204 exactly as it answers one that was, which is what makes this idempotent under replay
// and is why the operation returns nothing a model could branch on: there is no answer to
// the question "was it there before", and inventing one would be a value that is sometimes
// a guess.
func (c *HTTPClient) deleteObject(ctx context.Context, req Request) (any, error) {
	if _, _, err := c.call(ctx, http.MethodDelete, c.objectURL(req.Bucket, req.Key, nil), nil, nil, req); err != nil {
		return nil, err
	}
	return nil, nil
}

// presignObject mints a URL that performs one verb on one key until it expires. It makes
// no call, so it cannot fail on the network and cannot tell whether the object is there —
// which is stated in the panel, because a link that is minted successfully and 404s when
// clicked is otherwise read as a bug in Atlas.
func (c *HTTPClient) presignObject(req Request, method string) (any, error) {
	expires := req.ExpiresIn
	if expires <= 0 {
		expires = DefaultExpiresIn
	}
	if expires > MaxExpiresIn {
		return nil, fmt.Errorf("s3: a presigned URL may live at most %d seconds (seven days), not %d", MaxExpiresIn, expires)
	}
	header := http.Header{}
	// A content type bound into an upload URL is bound for the client too: S3 refuses the
	// PUT unless the caller sends the same one. That is the point — it is what stops a URL
	// minted for a PDF being used to store something else — and it is why the field only
	// appears on presign-put.
	if method == http.MethodPut && req.ContentType != "" {
		header.Set("Content-Type", req.ContentType)
	}
	now := c.now().UTC()
	signed, err := presign(method, c.objectURL(req.Bucket, req.Key, nil), header, c.credentials(), c.conn.Region, expires, now)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"url":         signed,
		"method":      method,
		"bucket":      req.Bucket,
		"key":         req.Key,
		"contentType": req.ContentType,
		"expiresIn":   json.Number(strconv.FormatInt(int64(expires), 10)),
		"expiresAt":   now.Add(time.Duration(expires) * time.Second).Format(time.RFC3339),
	}, nil
}

// call performs one signed HTTP request and returns the response and its body. The body is
// read under [MaxObjectBytes] plus one byte, so an over-large object is detected rather
// than held: the cap belongs at the read, not after it, because a store that answers with a
// gigabyte would otherwise be allowed to put a gigabyte in this process's memory before
// anything refused it.
func (c *HTTPClient) call(ctx context.Context, method, rawURL string, header http.Header, body []byte, req Request) (*http.Response, []byte, error) {
	if strings.TrimSpace(c.conn.AccessKeyID) == "" || strings.TrimSpace(c.conn.SecretAccessKey) == "" {
		return nil, nil, fmt.Errorf("s3: this Worker has no access key (set credentialsRef to a vault bundle {accessKeyId, secretAccessKey, region})")
	}
	var payload io.Reader
	if body != nil {
		payload = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, rawURL, payload)
	if err != nil {
		return nil, nil, fmt.Errorf("s3: build %s request: %w", req.Operation, err)
	}
	for name, values := range header {
		for _, v := range values {
			httpReq.Header.Add(name, v)
		}
	}
	signHeaders(httpReq, c.credentials(), c.conn.Region, hashPayload(body), c.now())
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("s3: %s: %w", req.Operation, err)
	}
	defer resp.Body.Close()
	// One byte past the cap, so an over-large body is recognised as over-large rather than
	// truncated into a smaller one that still parses.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxObjectBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("s3: read %s response: %w", req.Operation, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, nil, storeFailure(req.Operation, method, resp.StatusCode, raw)
	}
	return resp, raw, nil
}

// decodeContent turns the model's authored content into the bytes to store, and refuses
// anything past the cap before a request is built.
func decodeContent(req Request) ([]byte, error) {
	if contentEncoding(req) == EncodingBase64 {
		// Whitespace is what a base64 value picks up on its way through a form, a mail or
		// a FEEL string concatenation, and rejecting it would fail a document that is
		// entirely intact. StdEncoding with the whitespace removed is the reading that
		// accepts what people actually have.
		raw, err := base64.StdEncoding.DecodeString(stripWhitespace(req.Content))
		if err != nil {
			return nil, fmt.Errorf("s3: put-object was told the content is base64, and it is not: %w", err)
		}
		return checkSize(raw)
	}
	return checkSize([]byte(req.Content))
}

// checkSize refuses a body past the cap, naming the operation that exists for exactly this
// case rather than only the number that was exceeded.
func checkSize(raw []byte) ([]byte, error) {
	if int64(len(raw)) > MaxObjectBytes {
		return nil, fmt.Errorf("s3: put-object was given %d bytes and the limit is %d — a process variable holds no more. "+
			"Use presign-put and let whoever has the document upload it straight to the store", len(raw), MaxObjectBytes)
	}
	return raw, nil
}

// encodeContent renders a read object as the task asked for it: the characters as they
// are, or base64 for anything that is not text.
func encodeContent(req Request, raw []byte) string {
	if contentEncoding(req) == EncodingBase64 {
		return base64.StdEncoding.EncodeToString(raw)
	}
	return string(raw)
}

// contentEncoding reads the task's encoding, treating anything unset as text. The compiler
// has already applied the default and refused an unknown value, so this is the runtime's
// belt rather than its interpretation (I5).
func contentEncoding(req Request) string {
	if strings.EqualFold(strings.TrimSpace(req.Encoding), EncodingBase64) {
		return EncodingBase64
	}
	return EncodingText
}

// stripWhitespace removes the line breaks and spaces a base64 value collects in transit.
func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, s)
}

// applyMetadata turns the task's named values into request headers. A name that is already
// an x-amz-* header is sent as itself — which is how a model reaches server-side
// encryption or a storage class — and anything else becomes user metadata under
// x-amz-meta-, which is where a case number or a process instance key belongs.
//
// Both halves are model data: a name is authored and a value is whatever a FEEL
// expression resolved to at call time. So both are checked here rather than left to the
// transport. Go's writer does refuse a header carrying a newline, which is what makes
// this not a request-splitting hole to begin with — but it refuses it with a message
// about a header field, from inside net/http, on a job whose actual fault is a variable
// with a line break in it. Saying so here names the metadata entry instead.
func applyMetadata(header http.Header, meta map[string]string) error {
	for name, value := range meta {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !validHeaderName(name) {
			return fmt.Errorf("s3: metadata name %q cannot be a request header (letters, digits and -_ only)", name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("s3: the value for metadata %q contains a line break; object metadata is a request header, so it must be one line", name)
		}
		if strings.HasPrefix(strings.ToLower(name), "x-amz-") {
			header.Set(name, value)
			continue
		}
		header.Set("x-amz-meta-"+name, value)
	}
	return nil
}

// validHeaderName reports whether a name can be an HTTP header field. It is the
// conservative subset rather than RFC 7230's full token set: an object's metadata names
// travel back through a listing and a head, and a name made of punctuation is a name
// nobody meant to author.
func validHeaderName(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// readMetadata collects the user metadata off a response, with the x-amz-meta- prefix
// removed so a model reads back the names it wrote.
func readMetadata(header http.Header) map[string]any {
	out := map[string]any{}
	for name, values := range header {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, "x-amz-meta-") || len(values) == 0 {
			continue
		}
		out[strings.TrimPrefix(lower, "x-amz-meta-")] = values[0]
	}
	return out
}

// unquoteETag strips the quotes S3 wraps an ETag in. They are part of the HTTP header's
// syntax rather than of the value, and a model comparing two etags should not have to know
// that one of them came back quoted.
func unquoteETag(raw string) string {
	return strings.Trim(strings.TrimSpace(raw), `"`)
}

// firstMediaType drops a content type's parameters, so "application/pdf; charset=binary"
// reads back as the type a model branches on.
func firstMediaType(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexByte(raw, ';'); i >= 0 {
		raw = raw[:i]
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

// listBucketResult is S3's ListObjectsV2 answer. Only the fields a process has a use for
// are named; the rest of the document is ignored rather than refused, because a store that
// adds an element must not break a listing.
type listBucketResult struct {
	XMLName     xml.Name `xml:"ListBucketResult"`
	Name        string   `xml:"Name"`
	Prefix      string   `xml:"Prefix"`
	Delimiter   string   `xml:"Delimiter"`
	IsTruncated bool     `xml:"IsTruncated"`
	Contents    []struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
		StorageClass string `xml:"StorageClass"`
	} `xml:"Contents"`
	CommonPrefixes []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
}

// copyObjectResult is what a copy answers with. Code and Message are the failure S3 can
// write into a 200's body once a long copy has started; see copyObject.
type copyObjectResult struct {
	ETag         string `xml:"ETag"`
	LastModified string `xml:"LastModified"`
	Code         string `xml:"Code"`
	Message      string `xml:"Message"`
}

// storeError is S3's error envelope, which every compatible store writes. Code is the part
// worth reading — NoSuchBucket, AccessDenied and SignatureDoesNotMatch are three very
// different problems that all arrive as a 403 or a 404.
type storeError struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

// errNoSuchKey marks the one failure head-object turns into an answer. It is a sentinel
// wrapped into the message rather than a substring to search for, because the distinction
// it carries — "there is nothing there" versus "the credential cannot see the bucket" —
// is exactly the one a message that happened to contain the right words could get wrong.
//
// Only head-object asks the question, and it asks it here, where the error is still a Go
// value. Everywhere else the wrapping is invisible: the message reads as an ordinary 404
// and travels into an incident as one.
var errNoSuchKey = errors.New("no such key")

// storeFailure renders a non-2xx as an error a person can act on: the store's own code and
// message where there is one, and enough of the body to identify the failure where there
// is not — a proxy's HTML error page, most often, which is itself the answer to "why is
// this failing".
func storeFailure(op, method string, status int, raw []byte) error {
	var e storeError
	if err := xml.Unmarshal(raw, &e); err == nil && strings.TrimSpace(e.Code) != "" {
		if status == http.StatusNotFound && (e.Code == "NoSuchKey" || e.Code == "NotFound") {
			return fmt.Errorf("s3: %s returned HTTP 404: %s: %s (%w)", op, e.Code, e.Message, errNoSuchKey)
		}
		return fmt.Errorf("s3: %s returned HTTP %d: %s: %s", op, status, e.Code, e.Message)
	}
	// A HEAD has no body by definition, so its status is the whole message the store sent.
	if method == http.MethodHead && status == http.StatusNotFound {
		return fmt.Errorf("s3: %s returned HTTP 404 (%w)", op, errNoSuchKey)
	}
	body := strings.TrimSpace(string(raw))
	if body == "" {
		return fmt.Errorf("s3: %s returned HTTP %d with no response body", op, status)
	}
	const max = 300 // enough to identify the failure, short enough for an incident message
	if len(body) > max {
		body = body[:max] + "…"
	}
	return fmt.Errorf("s3: %s returned HTTP %d: %s", op, status, body)
}

// ProviderConfig is the per-Worker data the server resolves before building a client: the
// store's base URL (Endpoint, empty for AWS at the bundle's region) and the resolved
// Secret — the credential JSON bundle held in the vault under the Worker's credentialsRef.
// The secret lives only here at build time, never in a model or an event (ADR-0069/0168).
type ProviderConfig struct {
	Endpoint string
	Secret   string
}

// credentialBundle is the JSON an operator stores in the vault under an S3 Worker's
// credentialsRef.
//
// Region sits with the key rather than beside it because SigV4 signs with it: a region
// that disagreed with the credential produces a signature the store rejects, and the two
// are set in the same breath when an access key is issued. SessionToken is empty for a
// long-lived key and set for one STS issued, in which case it is part of what is signed.
type credentialBundle struct {
	AccessKeyID     string `json:"accessKeyId,omitempty"`
	SecretAccessKey string `json:"secretAccessKey,omitempty"`
	Region          string `json:"region,omitempty"`
	SessionToken    string `json:"sessionToken,omitempty"`
}

// CredentialsFromBundle reads an S3 Worker's vault bundle into the four values a client is
// built from.
//
// It is exported because two callers have to agree on it exactly: [NewProviderClient]
// builds the engine's own client from it, and the server renders the same values into a
// supervised worker's environment (superviseEnv). Two readings of one JSON that drifted
// would hand a worker a different identity from the one the engine would have used — the
// kind of difference that shows up as a signature failure on half a deployment.
func CredentialsFromBundle(secret string) (accessKeyID, secretAccessKey, region, sessionToken string, err error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", "", "", "", fmt.Errorf("s3: this Worker has no credential (set credentialsRef to a vault bundle {accessKeyId, secretAccessKey, region})")
	}
	var b credentialBundle
	if err := json.Unmarshal([]byte(secret), &b); err != nil {
		return "", "", "", "", fmt.Errorf("s3: credential is not valid JSON: %w", err)
	}
	b.AccessKeyID = strings.TrimSpace(b.AccessKeyID)
	b.SecretAccessKey = strings.TrimSpace(b.SecretAccessKey)
	b.Region = strings.TrimSpace(b.Region)
	b.SessionToken = strings.TrimSpace(b.SessionToken)
	if b.AccessKeyID == "" || b.SecretAccessKey == "" {
		return "", "", "", "", fmt.Errorf("s3: credential bundle needs both \"accessKeyId\" and \"secretAccessKey\"")
	}
	// The region is refused here rather than defaulted, because every default is wrong
	// somewhere: us-east-1 signs correctly for AWS's oldest buckets and for nothing else,
	// and a MinIO deployment that happens to accept any region would hide the mistake
	// until the first request against a store that does not.
	if b.Region == "" {
		return "", "", "", "", fmt.Errorf("s3: credential bundle needs a \"region\" (SigV4 signs with it; self-hosted stores commonly use \"us-east-1\")")
	}
	return b.AccessKeyID, b.SecretAccessKey, b.Region, b.SessionToken, nil
}

// NewProviderClient builds the S3 client for a managed Worker. A misconfigured Worker
// returns an error so the caller can skip it — its tasks then park with that reason
// (ADR-0158) rather than calling the store unsigned and being told nothing useful by a
// 403.
func NewProviderClient(cfg ProviderConfig) (Client, error) {
	id, secret, region, token, err := CredentialsFromBundle(cfg.Secret)
	if err != nil {
		return nil, err
	}
	return NewHTTPClient(Connector{
		Endpoint:        strings.TrimSpace(cfg.Endpoint),
		Region:          region,
		AccessKeyID:     id,
		SecretAccessKey: secret,
		SessionToken:    token,
	}), nil
}
