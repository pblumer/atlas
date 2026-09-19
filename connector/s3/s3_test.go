package s3_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/s3"
)

// A store that records what it was asked and answers what the test told it to. Every
// request is signed, so what this also proves is that the signature the worker builds
// survives a real round trip through net/http — the canonical path in particular, which
// url.URL is free to re-encode between building the request and writing it.
type fakeStore struct {
	t *testing.T
	// last is the request as it arrived, minus its body.
	last *http.Request
	// body is what arrived on a PUT.
	body string
	// reply is what to answer with, keyed by nothing: one test, one answer.
	status  int
	payload string
	headers map[string]string
	srv     *httptest.Server
}

func newFakeStore(t *testing.T) *fakeStore {
	t.Helper()
	f := &fakeStore{t: t, status: http.StatusOK, headers: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1<<20)
		n, _ := r.Body.Read(buf)
		f.body = string(buf[:n])
		f.last = r.Clone(r.Context())
		for k, v := range f.headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.payload))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// client builds a Worker pointed at the fake, at a fixed instant so a presigned URL is a
// value a test can assert rather than something that changes every run.
func (f *fakeStore) client() *s3.HTTPClient {
	return s3.NewHTTPClient(s3.Connector{
		Endpoint:        f.srv.URL,
		Region:          "eu-central-1",
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: "s3cr3t",
		Now:             func() time.Time { return time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC) },
	})
}

func (f *fakeStore) do(req s3.Request) (any, error) {
	f.t.Helper()
	return f.client().Do(context.Background(), req)
}

// obj is the shorthand every assertion below reads a result through.
func obj(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("result is %T, want an object a process variable can hold", v)
	}
	return m
}

func TestPutObjectSendsTheDocumentAndAnswersWithItsIdentity(t *testing.T) {
	f := newFakeStore(t)
	f.headers["ETag"] = `"d41d8cd98f00b204e9800998ecf8427e"`
	f.headers["x-amz-version-id"] = "v7"

	got, err := f.do(s3.Request{
		Operation: "put-object", Bucket: "rechnungen", Key: "faelle/4711/antrag.txt",
		Content: "Guten Tag", ContentType: "text/plain",
		Metadata: map[string]string{"fall": "4711", "x-amz-storage-class": "STANDARD_IA"},
	})
	if err != nil {
		t.Fatalf("put-object: %v", err)
	}
	if f.last.Method != http.MethodPut {
		t.Errorf("method = %s, want PUT", f.last.Method)
	}
	if f.last.URL.Path != "/rechnungen/faelle/4711/antrag.txt" {
		t.Errorf("path = %s, want the bucket and key under a path-style endpoint", f.last.URL.Path)
	}
	if f.body != "Guten Tag" {
		t.Errorf("body = %q, want the authored content verbatim", f.body)
	}
	// A plain name becomes user metadata; a name that already is an x-amz-* header is
	// sent as itself, which is how a model reaches storage class or encryption.
	if got := f.last.Header.Get("x-amz-meta-fall"); got != "4711" {
		t.Errorf("x-amz-meta-fall = %q, want the case number", got)
	}
	if got := f.last.Header.Get("x-amz-storage-class"); got != "STANDARD_IA" {
		t.Errorf("x-amz-storage-class = %q, want it sent as itself rather than as user metadata", got)
	}
	// Both are signed, or the store refuses the request without saying which header.
	if auth := f.last.Header.Get("Authorization"); !strings.Contains(auth, "x-amz-meta-fall;x-amz-storage-class") {
		t.Errorf("the metadata headers are sent but not signed: %s", auth)
	}
	out := obj(t, got)
	if out["etag"] != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Errorf("etag = %v, want it unquoted", out["etag"])
	}
	if out["versionId"] != "v7" || out["key"] != "faelle/4711/antrag.txt" {
		t.Errorf("result = %v, want the stored object's identity", out)
	}
}

// Base64 is what makes a binary round-trip possible at all, and a value that picked up
// line breaks on its way through a form or a mail is still a whole document.
func TestPutObjectDecodesBase64Content(t *testing.T) {
	f := newFakeStore(t)
	raw := []byte{0x25, 0x50, 0x44, 0x46, 0x2d} // "%PDF-"
	encoded := base64.StdEncoding.EncodeToString(raw)

	if _, err := f.do(s3.Request{
		Operation: "put-object", Bucket: "b", Key: "k.pdf",
		Content: encoded[:2] + "\n " + encoded[2:], Encoding: s3.EncodingBase64,
	}); err != nil {
		t.Fatalf("put-object: %v", err)
	}
	if f.body != string(raw) {
		t.Errorf("body = %q, want the decoded bytes", f.body)
	}
	// And content that says it is base64 and is not fails here, with the key in view,
	// rather than landing in the bucket as the characters of a broken document.
	if _, err := f.do(s3.Request{
		Operation: "put-object", Bucket: "b", Key: "k.pdf",
		Content: "not base64 !!", Encoding: s3.EncodingBase64,
	}); err == nil {
		t.Error("put-object accepted content that is not base64 while being told it is")
	}
}

func TestGetObjectReadsTheDocumentBack(t *testing.T) {
	f := newFakeStore(t)
	f.payload = "Spalte;Wert\na;1\n"
	f.headers["Content-Type"] = "text/csv; charset=utf-8"
	f.headers["ETag"] = `"abc"`
	f.headers["Last-Modified"] = "Fri, 19 Sep 2026 09:00:00 GMT"

	out := obj(t, mustDo(t, f, s3.Request{Operation: "get-object", Bucket: "b", Key: "export.csv"}))
	if out["content"] != "Spalte;Wert\na;1\n" {
		t.Errorf("content = %q", out["content"])
	}
	if out["contentType"] != "text/csv" {
		t.Errorf("contentType = %v, want the media type without its parameters", out["contentType"])
	}
	if fmt.Sprint(out["size"]) != "16" {
		t.Errorf("size = %v, want the byte count", out["size"])
	}

	// The same object read as base64 is the round trip a binary needs.
	out = obj(t, mustDo(t, f, s3.Request{Operation: "get-object", Bucket: "b", Key: "export.csv", Encoding: s3.EncodingBase64}))
	if out["content"] != base64.StdEncoding.EncodeToString([]byte(f.payload)) {
		t.Errorf("content = %v, want the base64 of the body", out["content"])
	}
}

// The cap refuses rather than truncates, and says what to do instead. Half a document is
// not a smaller document: it passes every format check there is and is broken.
func TestGetObjectRefusesAnObjectLargerThanAVariable(t *testing.T) {
	f := newFakeStore(t)
	f.payload = strings.Repeat("x", int(s3.MaxObjectBytes)+1)

	_, err := f.do(s3.Request{Operation: "get-object", Bucket: "b", Key: "gross.pdf"})
	if err == nil {
		t.Fatal("get-object accepted an object past the variable budget")
	}
	for _, want := range []string{"refused rather than cut short", "presign-get"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestPutObjectRefusesADocumentLargerThanAVariable(t *testing.T) {
	f := newFakeStore(t)
	_, err := f.do(s3.Request{
		Operation: "put-object", Bucket: "b", Key: "gross.pdf",
		Content: strings.Repeat("x", int(s3.MaxObjectBytes)+1),
	})
	if err == nil {
		t.Fatal("put-object accepted a document past the variable budget")
	}
	if !strings.Contains(err.Error(), "presign-put") {
		t.Errorf("error %q should point at the operation that exists for this case", err)
	}
	if f.last != nil {
		t.Error("the over-large body was sent before it was refused")
	}
}

// "Is it there" is the question head-object is asked, so a missing object is an answer
// and not an incident — a model cannot branch on an incident. A 403 stays a failure: it
// says the credential cannot see the bucket, which must not read as "there is nothing
// there".
func TestHeadObjectAnswersWhetherItIsThere(t *testing.T) {
	f := newFakeStore(t)
	f.headers["Content-Length"] = "2048"
	f.headers["Content-Type"] = "application/pdf"
	f.headers["x-amz-meta-fall"] = "4711"

	out := obj(t, mustDo(t, f, s3.Request{Operation: "head-object", Bucket: "b", Key: "da.pdf"}))
	if out["exists"] != true {
		t.Errorf("exists = %v, want true", out["exists"])
	}
	if fmt.Sprint(out["size"]) != "2048" {
		t.Errorf("size = %v", out["size"])
	}
	if meta := obj(t, out["metadata"]); meta["fall"] != "4711" {
		t.Errorf("metadata = %v, want the names the model wrote, without the x-amz-meta- prefix", meta)
	}

	f.status, f.headers = http.StatusNotFound, map[string]string{}
	out = obj(t, mustDo(t, f, s3.Request{Operation: "head-object", Bucket: "b", Key: "weg.pdf"}))
	if out["exists"] != false {
		t.Errorf("exists = %v for a key that is not there, want false", out["exists"])
	}

	f.status = http.StatusForbidden
	if _, err := f.do(s3.Request{Operation: "head-object", Bucket: "b", Key: "geheim.pdf"}); err == nil {
		t.Error("a 403 was reported as absence; a credential that cannot see the bucket is a failure")
	}
}

func TestListObjectsIsThePrefixSearch(t *testing.T) {
	f := newFakeStore(t)
	f.payload = `<?xml version="1.0"?>
<ListBucketResult>
  <Name>b</Name><Prefix>faelle/4711/</Prefix><IsTruncated>true</IsTruncated>
  <Contents><Key>faelle/4711/antrag.pdf</Key><Size>2048</Size><ETag>"a1"</ETag>
    <LastModified>2026-09-19T09:00:00.000Z</LastModified><StorageClass>STANDARD</StorageClass></Contents>
  <Contents><Key>faelle/4711/beleg.pdf</Key><Size>512</Size><ETag>"b2"</ETag>
    <LastModified>2026-09-19T09:05:00.000Z</LastModified><StorageClass>STANDARD</StorageClass></Contents>
  <CommonPrefixes><Prefix>faelle/4711/anhaenge/</Prefix></CommonPrefixes>
</ListBucketResult>`

	out := obj(t, mustDo(t, f, s3.Request{
		Operation: "list-objects", Bucket: "b", Prefix: "faelle/4711/",
		Delimiter: "/", MaxKeys: 2, StartAfter: "faelle/4710/zzz",
	}))
	q := f.last.URL.Query()
	for k, want := range map[string]string{
		"list-type": "2", "prefix": "faelle/4711/", "delimiter": "/",
		"max-keys": "2", "start-after": "faelle/4710/zzz",
	} {
		if q.Get(k) != want {
			t.Errorf("query %s = %q, want %q", k, q.Get(k), want)
		}
	}
	objects, ok := out["objects"].([]any)
	if !ok || len(objects) != 2 {
		t.Fatalf("objects = %v, want the two keys", out["objects"])
	}
	first := obj(t, objects[0])
	if first["key"] != "faelle/4711/antrag.pdf" || fmt.Sprint(first["size"]) != "2048" || first["etag"] != "a1" {
		t.Errorf("first object = %v", first)
	}
	if prefixes, _ := out["prefixes"].([]any); len(prefixes) != 1 || prefixes[0] != "faelle/4711/anhaenge/" {
		t.Errorf("prefixes = %v, want the one rolled up at the delimiter", out["prefixes"])
	}
	// The resume point is the greater of the last key and the last common prefix: with a
	// delimiter either may be the last thing on the page, and resuming from the smaller
	// of the two would re-read what was already delivered.
	if out["truncated"] != true || out["nextStartAfter"] != "faelle/4711/beleg.pdf" {
		t.Errorf("truncated=%v nextStartAfter=%v", out["truncated"], out["nextStartAfter"])
	}
}

// A complete listing answers with no cursor. Handing one back on the last page is how a
// model's paging loop never ends.
func TestACompleteListingAnswersWithNoCursor(t *testing.T) {
	f := newFakeStore(t)
	f.payload = `<ListBucketResult><Name>b</Name><IsTruncated>false</IsTruncated>
	  <Contents><Key>a</Key><Size>1</Size><ETag>"x"</ETag></Contents></ListBucketResult>`
	out := obj(t, mustDo(t, f, s3.Request{Operation: "list-objects", Bucket: "b"}))
	if out["truncated"] != false || out["nextStartAfter"] != "" {
		t.Errorf("truncated=%v nextStartAfter=%q, want no cursor on a complete listing", out["truncated"], out["nextStartAfter"])
	}
}

// Object metadata is a request header and both halves of it are model data: the name is
// authored, the value is whatever a FEEL expression resolved to. A line break in either
// is refused here, naming the metadata entry — Go's writer would refuse it too, but with
// a message about a header field and nothing about the variable that caused it.
func TestMetadataThatCannotBeAHeaderIsRefused(t *testing.T) {
	f := newFakeStore(t)
	for _, tc := range []struct {
		name string
		meta map[string]string
		want string
	}{
		{"a newline in the value", map[string]string{"fall": "4711\r\nX-Injected: yes"}, "line break"},
		{"a name that is not a header", map[string]string{"fall nummer": "4711"}, "cannot be a request header"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.do(s3.Request{
				Operation: "put-object", Bucket: "b", Key: "k", Content: "x", Metadata: tc.meta,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
			if f.last != nil {
				t.Error("the request was sent before the metadata was refused")
			}
		})
	}
}

// A listing addresses the bucket itself, and the path it signs must be the path it sends:
// "/bucket" and "/bucket/" are two different canonical requests, and only one of them is
// the request being made.
func TestAListingAddressesTheBucketWithoutATrailingSlash(t *testing.T) {
	f := newFakeStore(t)
	f.payload = `<ListBucketResult><Name>b</Name><IsTruncated>false</IsTruncated></ListBucketResult>`
	mustDo(t, f, s3.Request{Operation: "list-objects", Bucket: "rechnungen"})
	if f.last.URL.Path != "/rechnungen" {
		t.Errorf("path = %q, want the bucket alone", f.last.URL.Path)
	}
}

func TestCopyObjectMovesTheBytesInsideTheStore(t *testing.T) {
	f := newFakeStore(t)
	f.payload = `<CopyObjectResult><ETag>"c3"</ETag><LastModified>2026-09-19T10:00:00.000Z</LastModified></CopyObjectResult>`

	out := obj(t, mustDo(t, f, s3.Request{
		Operation: "copy-object", Bucket: "archiv", Key: "2026/4711.pdf",
		SourceBucket: "eingang", SourceKey: "scan 17.pdf",
		Metadata: map[string]string{"fall": "4711"},
	}))
	// The source is URI-encoded exactly once, like the path — a space in a key is %20
	// here too, and the store answers a raw space with a signature error.
	if got := f.last.Header.Get("x-amz-copy-source"); got != "/eingang/scan%2017.pdf" {
		t.Errorf("x-amz-copy-source = %q", got)
	}
	// Metadata on a copy replaces the source's; the store offers only those two modes,
	// and saying so is better than a silent merge that needs a read of the source first.
	if got := f.last.Header.Get("x-amz-metadata-directive"); got != "REPLACE" {
		t.Errorf("x-amz-metadata-directive = %q, want REPLACE when the model authored metadata", got)
	}
	if out["etag"] != "c3" || out["sourceKey"] != "scan 17.pdf" {
		t.Errorf("result = %v", out)
	}
}

// A copy's 200 can still carry a failure in its body: the store keeps the connection
// open during a long copy and writes the error into a response it already committed to.
// A caller reading the status alone would report a copy that did not happen as done.
func TestCopyObjectReadsAFailureOutOfA200(t *testing.T) {
	f := newFakeStore(t)
	f.payload = `<Error><Code>InternalError</Code><Message>We encountered an internal error</Message></Error>`
	_, err := f.do(s3.Request{
		Operation: "copy-object", Bucket: "a", Key: "k", SourceBucket: "b", SourceKey: "j",
	})
	if err == nil || !strings.Contains(err.Error(), "InternalError") {
		t.Fatalf("err = %v, want the failure the store wrote into its 200", err)
	}
}

// A delete answers with nothing a model could branch on, and that is deliberate: the
// store answers a delete of an object that was never there exactly as it answers one
// that was, so there is no "was it there before" to report.
func TestDeleteObjectAnswersWithNothing(t *testing.T) {
	f := newFakeStore(t)
	f.status = http.StatusNoContent
	got, err := f.do(s3.Request{Operation: "delete-object", Bucket: "b", Key: "weg.pdf"})
	if err != nil {
		t.Fatalf("delete-object: %v", err)
	}
	if got != nil {
		t.Errorf("result = %v, want nothing", got)
	}
	if f.last.Method != http.MethodDelete {
		t.Errorf("method = %s", f.last.Method)
	}
}

// The two presigns are the point of this Worker Type: they make no call at all, which is
// what lets a 40 MB document reach a person without a byte of it entering Atlas.
func TestPresignMintsALinkWithoutCallingTheStore(t *testing.T) {
	f := newFakeStore(t)
	out := obj(t, mustDo(t, f, s3.Request{Operation: "presign-get", Bucket: "rechnungen", Key: "faelle/4711/antrag.pdf"}))
	if f.last != nil {
		t.Fatal("presign-get called the store; a signature is computed, not requested")
	}
	raw, _ := out["url"].(string)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("minted URL does not parse: %v", err)
	}
	if u.Path != "/rechnungen/faelle/4711/antrag.pdf" {
		t.Errorf("path = %s", u.Path)
	}
	if got := u.Query().Get("X-Amz-Expires"); got != "3600" {
		t.Errorf("X-Amz-Expires = %q, want the one-hour default", got)
	}
	if out["method"] != http.MethodGet {
		t.Errorf("method = %v", out["method"])
	}
	if out["expiresAt"] != "2026-09-19T11:00:00Z" {
		t.Errorf("expiresAt = %v, want an hour past the signing instant", out["expiresAt"])
	}
}

// A content type on an upload link is bound into the signature, so whoever uses the URL
// must send the same one. That is what stops a link minted for a PDF being used to store
// something else.
func TestPresignPutBindsTheContentType(t *testing.T) {
	f := newFakeStore(t)
	out := obj(t, mustDo(t, f, s3.Request{
		Operation: "presign-put", Bucket: "eingang", Key: "scan.pdf",
		ContentType: "application/pdf", ExpiresIn: 600,
	}))
	raw, _ := out["url"].(string)
	u, _ := url.Parse(raw)
	if got := u.Query().Get("X-Amz-SignedHeaders"); got != "content-type;host" {
		t.Errorf("X-Amz-SignedHeaders = %q, want the content type signed alongside the host", got)
	}
	if got := u.Query().Get("X-Amz-Expires"); got != "600" {
		t.Errorf("X-Amz-Expires = %q", got)
	}
}

func TestPresignRefusesALifetimePastSevenDays(t *testing.T) {
	f := newFakeStore(t)
	_, err := f.do(s3.Request{Operation: "presign-get", Bucket: "b", Key: "k", ExpiresIn: s3.MaxExpiresIn + 1})
	if err == nil || !strings.Contains(err.Error(), "seven days") {
		t.Fatalf("err = %v, want a refusal naming the ceiling", err)
	}
}

// The compiler guarantees the attribute is authored; it cannot guarantee what a FEEL
// expression evaluates to. An empty bucket or key spliced into a URL is answered with a
// 404 naming an address the author never wrote, so it is caught here instead.
func TestAResolvedEmptyAddressIsRefusedBeforeTheCall(t *testing.T) {
	f := newFakeStore(t)
	for _, tc := range []struct {
		name string
		req  s3.Request
		want string
	}{
		{"no bucket", s3.Request{Operation: "get-object", Key: "k"}, "no bucket"},
		{"no key", s3.Request{Operation: "get-object", Bucket: "b"}, "no key"},
		{"no source", s3.Request{Operation: "copy-object", Bucket: "b", Key: "k"}, "no source"},
		{"unknown operation", s3.Request{Operation: "rename-object", Bucket: "b", Key: "k"}, "unknown operation"},
		// A bucket carrying a path would address another bucket under path-style and
		// another host under virtual-host, and the store would answer plausibly for both.
		{"bucket is a path", s3.Request{Operation: "get-object", Bucket: "b/c", Key: "k"}, "not a bucket name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.do(tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
			if f.last != nil {
				t.Error("the request was sent before it was refused")
			}
		})
	}
}

// The store's own error code is what distinguishes three problems that all arrive as a
// 403 or a 404 — a missing bucket, a credential without the grant, and a signature that
// did not match.
func TestAFailureCarriesTheStoresOwnCode(t *testing.T) {
	f := newFakeStore(t)
	f.status = http.StatusForbidden
	f.payload = `<Error><Code>SignatureDoesNotMatch</Code><Message>The request signature we calculated does not match</Message></Error>`
	_, err := f.do(s3.Request{Operation: "get-object", Bucket: "b", Key: "k"})
	if err == nil || !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Fatalf("err = %v, want the store's code", err)
	}
	// A proxy's HTML page is not the store's envelope, and quoting it is what answers
	// "why is this failing" when the store was never reached.
	f.payload = "<html><body>502 Bad Gateway</body></html>"
	f.status = http.StatusBadGateway
	_, err = f.do(s3.Request{Operation: "get-object", Bucket: "b", Key: "k"})
	if err == nil || !strings.Contains(err.Error(), "Bad Gateway") {
		t.Fatalf("err = %v, want the body quoted when it is not an S3 error", err)
	}
}

func TestCredentialBundleIsReadTheOneWay(t *testing.T) {
	for _, tc := range []struct{ name, bundle, want string }{
		{"empty", "", "no credential"},
		{"not json", "AKIAEXAMPLE", "not valid JSON"},
		{"no key", `{"region":"eu-central-1"}`, "accessKeyId"},
		{"no region", `{"accessKeyId":"A","secretAccessKey":"B"}`, "region"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s3.NewProviderClient(s3.ProviderConfig{Secret: tc.bundle}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	id, secret, region, token, err := s3.CredentialsFromBundle(`{"accessKeyId":" A ","secretAccessKey":"B","region":"eu-central-1","sessionToken":"T"}`)
	if err != nil {
		t.Fatalf("CredentialsFromBundle: %v", err)
	}
	if id != "A" || secret != "B" || region != "eu-central-1" || token != "T" {
		t.Errorf("got %q %q %q %q, want the four values trimmed", id, secret, region, token)
	}
}

// A Worker with no access key is refused before a request is built, rather than calling
// the store unsigned and being told nothing useful by a 403.
func TestAWorkerWithNoKeyRefusesBeforeCalling(t *testing.T) {
	f := newFakeStore(t)
	c := s3.NewHTTPClient(s3.Connector{Endpoint: f.srv.URL, Region: "eu-central-1"})
	if _, err := c.Do(context.Background(), s3.Request{Operation: "get-object", Bucket: "b", Key: "k"}); err == nil {
		t.Error("a Worker with no access key made a request")
	}
	// And one with no region, which SigV4 has no defensible default for.
	c = s3.NewHTTPClient(s3.Connector{Endpoint: f.srv.URL, AccessKeyID: "a", SecretAccessKey: "b"})
	_, err := c.Do(context.Background(), s3.Request{Operation: "get-object", Bucket: "b", Key: "k"})
	if err == nil || !strings.Contains(err.Error(), "region") {
		t.Errorf("err = %v, want a refusal naming the missing region", err)
	}
}

// The two presigns never reach the HTTP path, so a Worker with no key would mint a URL
// signed with nothing — a link that looks minted and is answered with a 403 by whoever
// clicks it, minutes or days later and nowhere near the model.
func TestPresignRefusesAWorkerWithNoKey(t *testing.T) {
	c := s3.NewHTTPClient(s3.Connector{Region: "eu-central-1"})
	_, err := c.Do(context.Background(), s3.Request{Operation: "presign-get", Bucket: "b", Key: "k"})
	if err == nil || !strings.Contains(err.Error(), "access key") {
		t.Fatalf("err = %v, want a refusal naming the missing access key", err)
	}
}

// Run resolves the Worker name before anything else, because an unconfigured name is the
// most actionable failure a job can carry here (ADR-0158).
func TestRunNamesAnUnconfiguredWorker(t *testing.T) {
	_, err := s3.Run(context.Background(), s3.Job{Connector: "archiv", Operation: "get-object"}, s3.NewRegistry())
	if err == nil || !strings.Contains(err.Error(), "archiv") {
		t.Fatalf("err = %v, want the unconfigured Worker named", err)
	}
}

// TestS3OpsMatchTheWorkerType is the drift guard between this package's [s3.Ops] table
// and the compiler's own copy of the operation rules.
//
// The compiler cannot import this package — connector/s3 imports compiler, so the
// dependency only runs one way — which is why the rules exist twice. The check is
// therefore behavioural: for every operation, a model supplying exactly what Ops says is
// required must compile, and a model missing any one of those values must not.
func TestS3OpsMatchTheWorkerType(t *testing.T) {
	attrsFor := func(op string, spec s3.Op, omit string) string {
		parts := []string{`connector="archiv"`, `operation="` + op + `"`}
		add := func(name, attr, value string) {
			if omit != name {
				parts = append(parts, attr+`="`+value+`"`)
			}
		}
		if spec.NeedsBucket {
			add("bucket", "bucket", "rechnungen")
		}
		if spec.NeedsKey {
			add("key", "key", "faelle/4711/antrag.pdf")
		}
		if spec.NeedsContent {
			add("content", "content", "Guten Tag")
		}
		if spec.NeedsSource {
			add("sourceBucket", "sourceBucket", "eingang")
			add("sourceKey", "sourceKey", "scan.pdf")
		}
		if spec.NeedsResult {
			add("result", "resultVariable", "datei")
		}
		return strings.Join(parts, " ")
	}
	for op, spec := range s3.Ops {
		t.Run(op, func(t *testing.T) {
			if err := compileS3(attrsFor(op, spec, "")); err != nil {
				t.Fatalf("the compiler rejects a model that satisfies Ops[%q]: %v", op, err)
			}
			required := map[string]bool{
				"bucket": spec.NeedsBucket, "key": spec.NeedsKey, "content": spec.NeedsContent,
				"sourceBucket": spec.NeedsSource, "sourceKey": spec.NeedsSource,
				"result": spec.NeedsResult,
			}
			for omit, need := range required {
				if !need {
					continue
				}
				if err := compileS3(attrsFor(op, spec, omit)); err == nil {
					t.Errorf("the compiler accepts %q without its required %s, which Ops says it needs", op, omit)
				}
			}
		})
	}
}

// The compiler's authored ceilings are this package's own. The two exist separately for
// the dependency's sake, and a number that drifted would be refused at deploy under one
// rule and honoured at call time under another.
func TestTheCompilersCeilingsAreThisPackages(t *testing.T) {
	if err := compileS3(fmt.Sprintf(`connector="a" operation="list-objects" bucket="b" maxKeys="%d" resultVariable="r"`, s3.MaxListPageSize)); err != nil {
		t.Errorf("the compiler refuses a listing at this package's page cap: %v", err)
	}
	if err := compileS3(fmt.Sprintf(`connector="a" operation="list-objects" bucket="b" maxKeys="%d" resultVariable="r"`, s3.MaxListPageSize+1)); err == nil {
		t.Error("the compiler accepts a listing past this package's page cap")
	}
	if err := compileS3(fmt.Sprintf(`connector="a" operation="presign-get" bucket="b" key="k" expiresIn="%d" resultVariable="r"`, s3.MaxExpiresIn)); err != nil {
		t.Errorf("the compiler refuses the longest lifetime a signature allows: %v", err)
	}
	if err := compileS3(fmt.Sprintf(`connector="a" operation="presign-get" bucket="b" key="k" expiresIn="%d" resultVariable="r"`, s3.MaxExpiresIn+1)); err == nil {
		t.Error("the compiler accepts a lifetime past what a signature allows")
	}
	// Both encodings this package names are authorable, and nothing else is.
	for _, enc := range []string{s3.EncodingText, s3.EncodingBase64} {
		if err := compileS3(`connector="a" operation="get-object" bucket="b" key="k" encoding="` + enc + `" resultVariable="r"`); err != nil {
			t.Errorf("the compiler refuses encoding %q, which this package names: %v", enc, err)
		}
	}
	if err := compileS3(`connector="a" operation="get-object" bucket="b" key="k" encoding="hex" resultVariable="r"`); err == nil {
		t.Error("the compiler accepts an encoding neither side implements")
	}
}

func compileS3(attrs string) error {
	bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:s3Connector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	_, err := compiler.Parse(1, 1, strings.NewReader(bpmn))
	return err
}

func mustDo(t *testing.T, f *fakeStore, req s3.Request) any {
	t.Helper()
	got, err := f.do(req)
	if err != nil {
		t.Fatalf("%s: %v", req.Operation, err)
	}
	return got
}
