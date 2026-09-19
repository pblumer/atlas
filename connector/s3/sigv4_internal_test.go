package s3

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// AWS's own worked example, which is the only way to know this signer is right.
//
// Signature Version 4 has no partial credit: a canonical request that differs from the
// store's by one byte is answered with SignatureDoesNotMatch and no indication of which
// byte. So the guard is a published vector rather than a round-trip against a fake — a
// fake would agree with whatever this file computes, including the mistakes.
//
// This is "Example: GET Bucket (List Objects)" from the AWS documentation on
// authenticating with the Authorization header. Its signed-header set is exactly what
// this signer produces (host, x-amz-content-sha256, x-amz-date), which is why it is the
// one of AWS's examples used here: the others sign a Range or a Date header that this
// worker never sends.
const (
	vectorAccessKey = "AKIAIOSFODNN7EXAMPLE"
	vectorSecret    = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	vectorRegion    = "us-east-1"
	vectorHost      = "examplebucket.s3.amazonaws.com"
	vectorSignature = "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"
)

func vectorTime() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) }

func TestHeaderSignatureMatchesTheAWSVector(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://"+vectorHost+"/?max-keys=2&prefix=J", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	signHeaders(req, credentials{AccessKeyID: vectorAccessKey, SecretAccessKey: vectorSecret}, vectorRegion, emptyPayloadHash, vectorTime())

	auth := req.Header.Get("Authorization")
	for _, want := range []string{
		"AWS4-HMAC-SHA256 Credential=" + vectorAccessKey + "/20130524/us-east-1/s3/aws4_request",
		"SignedHeaders=host;x-amz-content-sha256;x-amz-date",
		"Signature=" + vectorSignature,
	} {
		if !strings.Contains(auth, want) {
			t.Errorf("Authorization is missing %q\ngot: %s", want, auth)
		}
	}
	if got := req.Header.Get("X-Amz-Date"); got != "20130524T000000Z" {
		t.Errorf("X-Amz-Date = %q, want the instant the scope was built from", got)
	}
	if got := req.Header.Get("X-Amz-Content-Sha256"); got != emptyPayloadHash {
		t.Errorf("X-Amz-Content-Sha256 = %q, want the empty-body hash", got)
	}
}

// The canonical request is asserted on its own as well as through the signature, because
// a mismatch in the signature alone says only "something differs". This says what.
func TestCanonicalRequestIsTheDocumentedOne(t *testing.T) {
	q := url.Values{"max-keys": {"2"}, "prefix": {"J"}}
	header := http.Header{}
	header.Set("X-Amz-Content-Sha256", emptyPayloadHash)
	header.Set("X-Amz-Date", "20130524T000000Z")
	signed, canonical := canonicalRequest(signedRequest{
		Method: http.MethodGet, Host: vectorHost, Path: "/", Query: q,
		Headers: header, PayloadHash: emptyPayloadHash,
	})
	want := strings.Join([]string{
		"GET",
		"/",
		"max-keys=2&prefix=J",
		"host:" + vectorHost,
		"x-amz-content-sha256:" + emptyPayloadHash,
		"x-amz-date:20130524T000000Z",
		"",
		"host;x-amz-content-sha256;x-amz-date",
		emptyPayloadHash,
	}, "\n")
	if canonical != want {
		t.Errorf("canonical request\n got:\n%s\nwant:\n%s", canonical, want)
	}
	if got := strings.Join(signed, ";"); got != "host;x-amz-content-sha256;x-amz-date" {
		t.Errorf("signed headers = %q", got)
	}
}

// A header the transport owns must never be signed. Content-Length is added and adjusted
// between this process and the store, and Authorization is the output of signing — a
// signature covering either is refused after something perfectly ordinary touched the
// request, and the store's message names no header.
func TestTransportHeadersAreNotSigned(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Length", "17")
	header.Set("Authorization", "leftover")
	header.Set("Accept-Encoding", "gzip")
	header.Set("Content-Type", "application/pdf")
	header.Set("X-Amz-Meta-Fall", "4711")
	signed, _ := canonicalRequest(signedRequest{
		Method: http.MethodPut, Host: vectorHost, Path: "/x", Headers: header, PayloadHash: emptyPayloadHash,
	})
	got := strings.Join(signed, ";")
	if want := "content-type;host;x-amz-meta-fall"; got != want {
		t.Errorf("signed headers = %q, want %q — only what the store reads and this worker set", got, want)
	}
}

// S3 encodes the canonical path exactly once, unlike every other AWS service. A signer
// written from the general documentation double-encodes, which works for every key
// without a character needing encoding and fails on the first one with a space in it.
func TestCanonicalPathEncodesExactlyOnce(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/rechnungen/Antrag 2026.pdf", "/rechnungen/Antrag%202026.pdf"},
		{"/a/b+c", "/a/b%2Bc"},
		{"/schon%20kodiert", "/schon%2520kodiert"}, // a literal % is a %, not half an escape
		{"/faelle/4711/übersicht.txt", "/faelle/4711/%C3%BCbersicht.txt"},
		{"", "/"},
	} {
		if got := canonicalPath(tc.in); got != tc.want {
			t.Errorf("canonicalPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A space in a query value is %20 and never "+", which is the single most common way a
// hand-written signer disagrees with the store.
func TestQueryEncodingIsRFC3986AndSorted(t *testing.T) {
	got := encodeQuery(url.Values{"prefix": {"a b"}, "delimiter": {"/"}, "list-type": {"2"}})
	if want := "delimiter=%2F&list-type=2&prefix=a%20b"; got != want {
		t.Errorf("encodeQuery = %q, want %q", got, want)
	}
}

// A presigned URL carries every signing parameter in its query, signs UNSIGNED-PAYLOAD
// (the bytes are not here — whoever uses the URL has them), and is deterministic for a
// fixed instant, which is what lets a test assert one at all.
func TestPresignCarriesItsSignatureInTheQuery(t *testing.T) {
	raw, err := presign(http.MethodGet, "https://"+vectorHost+"/rechnungen/4711.pdf", http.Header{},
		credentials{AccessKeyID: vectorAccessKey, SecretAccessKey: vectorSecret}, vectorRegion, 900, vectorTime())
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"X-Amz-Algorithm":     "AWS4-HMAC-SHA256",
		"X-Amz-Credential":    vectorAccessKey + "/20130524/us-east-1/s3/aws4_request",
		"X-Amz-Date":          "20130524T000000Z",
		"X-Amz-Expires":       "900",
		"X-Amz-SignedHeaders": "host",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if len(q.Get("X-Amz-Signature")) != 64 {
		t.Errorf("X-Amz-Signature = %q, want 64 hex characters", q.Get("X-Amz-Signature"))
	}
	// Signing the same request at the same instant twice must produce the same URL:
	// anything else would mean the signature depends on something other than its inputs,
	// and a job that is replayed would hand out a link the store never agreed to.
	again, err := presign(http.MethodGet, "https://"+vectorHost+"/rechnungen/4711.pdf", http.Header{},
		credentials{AccessKeyID: vectorAccessKey, SecretAccessKey: vectorSecret}, vectorRegion, 900, vectorTime())
	if err != nil || again != raw {
		t.Errorf("presign is not deterministic for a fixed instant:\n%s\n%s (%v)", raw, again, err)
	}
}

// A session token is part of the identity, so it travels *and* is signed. A presigned URL
// that carried it outside the signature would be refused, and one that omitted it would
// be anonymous.
func TestSessionTokenIsSignedOnBothPaths(t *testing.T) {
	creds := credentials{AccessKeyID: vectorAccessKey, SecretAccessKey: vectorSecret, SessionToken: "FwoGZXIvYXdz…"}
	req, _ := http.NewRequest(http.MethodGet, "https://"+vectorHost+"/x", nil)
	signHeaders(req, creds, vectorRegion, emptyPayloadHash, vectorTime())
	if got := req.Header.Get("X-Amz-Security-Token"); got != creds.SessionToken {
		t.Errorf("X-Amz-Security-Token = %q, want the session token", got)
	}
	if !strings.Contains(req.Header.Get("Authorization"), "x-amz-security-token") {
		t.Errorf("the session token is sent but not signed: %s", req.Header.Get("Authorization"))
	}
	raw, err := presign(http.MethodGet, "https://"+vectorHost+"/x", http.Header{}, creds, vectorRegion, 60, vectorTime())
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	if got := mustParseQuery(t, raw).Get("X-Amz-Security-Token"); got != creds.SessionToken {
		t.Errorf("presigned X-Amz-Security-Token = %q, want the session token", got)
	}
}

func mustParseQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Query()
}

// Which addressing a Worker uses follows from its one configured field, and nothing else.
// Getting this wrong points a request at a host that exists and answers — AWS's own — so
// it is worth pinning rather than discovering.
func TestAddressingFollowsTheConfiguredEndpoint(t *testing.T) {
	aws := NewHTTPClient(Connector{Region: "eu-central-1", AccessKeyID: "a", SecretAccessKey: "b"})
	if got := aws.objectURL("rechnungen", "faelle/4711/antrag.pdf", nil); got != "https://rechnungen.s3.eu-central-1.amazonaws.com/faelle/4711/antrag.pdf" {
		t.Errorf("with no endpoint the bucket belongs in the hostname; got %s", got)
	}
	self := NewHTTPClient(Connector{Endpoint: "https://minio.example:9000/", Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "b"})
	if got := self.objectURL("rechnungen", "faelle/4711/antrag.pdf", nil); got != "https://minio.example:9000/rechnungen/faelle/4711/antrag.pdf" {
		t.Errorf("with an endpoint the bucket belongs in the path; got %s", got)
	}
	// An endpoint pasted without a scheme parses into a path, which would make the
	// hostname part of the bucket's address. Fixing it is safe here because there is only
	// one thing "minio.example:9000" can mean.
	bare := NewHTTPClient(Connector{Endpoint: "minio.example:9000", Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "b"})
	if got := bare.Base(); got != "https://minio.example:9000" {
		t.Errorf("Base() = %q, want a scheme put in front of a bare host", got)
	}
}
