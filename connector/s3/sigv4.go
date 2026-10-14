package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AWS Signature Version 4, the reason this is a Worker Type at all.
//
// Two shapes of the same signature live here, and they differ in exactly two ways:
//
//   - **Header signing** (every call this worker makes) puts the signature in an
//     Authorization header and signs the payload's own SHA-256.
//   - **Query signing** (the two presigns) puts every signing parameter in the query
//     string and signs the literal UNSIGNED-PAYLOAD, because the bytes are not here — the
//     browser that will use the URL has them.
//
// Both derive the same signing key and build the same canonical request, so they share
// [canonicalRequest] and [signingKey] rather than being written twice. The failure this
// arrangement prevents is the one that makes SigV4 unpleasant: a signature that differs
// from the store's by one byte is reported as SignatureDoesNotMatch with no indication of
// *which* byte, so two implementations of the canonical request that disagree anywhere
// produce an error message that points at neither of them.
//
// What is deliberately not here: the double URI encoding every other AWS service uses in
// its canonical path. S3 signs the path encoded exactly once, and a signer that forgot
// that works for every key without a space in it.

const (
	// signAlgorithm is the only algorithm this signer produces. SigV4a (multi-region) is a
	// different scheme with a different key derivation, and nothing here pretends to it.
	signAlgorithm = "AWS4-HMAC-SHA256"
	// signService is the service name in the credential scope. It is "s3" for every
	// S3-compatible store, including the ones that are not AWS: the scope is part of the
	// signature contract, not a routing decision.
	signService = "s3"
	// signTerminator ends the credential scope and the key derivation chain.
	signTerminator = "aws4_request"
	// unsignedPayload is what a query-signed request states instead of a body hash. The
	// signer does not have the body — whoever uses the URL does.
	unsignedPayload = "UNSIGNED-PAYLOAD"
	// emptyPayloadHash is sha256 of nothing, which is what a GET, HEAD or DELETE signs.
	// Spelled as a constant so the hot path of every read does not hash an empty slice to
	// rediscover it.
	emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	// amzDateFormat and scopeDateFormat are SigV4's two renderings of the signing time:
	// the full instant in the X-Amz-Date header, and the date alone in the credential
	// scope. They must be the same instant — a request whose scope says one day and whose
	// date header says the next is refused, which is the failure that appears at midnight
	// UTC and nowhere else.
	amzDateFormat   = "20060102T150405Z"
	scopeDateFormat = "20060102"
)

// credentials are the three values SigV4 signs with. SessionToken is empty for a
// long-lived access key and set for one STS issued, in which case it also travels as a
// header (or a query parameter) and is part of what is signed.
type credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// signedRequest carries everything a signature is computed over, assembled by the caller
// so that the header and query forms build the same canonical request from the same
// inputs rather than each assembling its own.
type signedRequest struct {
	Method string
	// Host is what goes into the canonical `host` header and, therefore, into the
	// signature. It is the URL's host including a non-default port: a store on :9000
	// signed without its port is refused, and the message says nothing about ports.
	Host string
	// Path is the URL path, already built from the bucket and key but *not* yet encoded.
	// canonicalPath encodes it, once, per segment.
	Path string
	// Query is the request's query parameters, without any of the signing ones.
	Query url.Values
	// Headers are the headers to sign beyond host. Names are matched case-insensitively
	// and rendered lowercase, as the canonical form requires.
	Headers http.Header
	// PayloadHash is the hex SHA-256 of the body, or [unsignedPayload] for a query
	// signature.
	PayloadHash string
}

// signHeaders signs a request in place: it sets the x-amz-date, x-amz-content-sha256 and
// (where there is a session token) x-amz-security-token headers, computes the signature
// over them, and sets Authorization.
//
// The headers are set here rather than by the caller because they are *part of* what is
// signed: a caller that set one afterwards would produce a request whose signature covers
// a different set of headers than the one it sends, which the store reports as a mismatch
// that names no header.
func signHeaders(req *http.Request, creds credentials, region string, payloadHash string, now time.Time) {
	now = now.UTC()
	req.Header.Set("X-Amz-Date", now.Format(amzDateFormat))
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}
	sr := signedRequest{
		Method:      req.Method,
		Host:        req.URL.Host,
		Path:        req.URL.Path,
		Query:       req.URL.Query(),
		Headers:     req.Header,
		PayloadHash: payloadHash,
	}
	signed, canonical := canonicalRequest(sr)
	scope := credentialScope(now, region)
	sig := sign(creds.SecretAccessKey, now, region, stringToSign(now, scope, canonical))
	req.Header.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		signAlgorithm, creds.AccessKeyID, scope, strings.Join(signed, ";"), sig))
}

// presign returns a URL that performs one operation, valid for expiresIn seconds and
// carrying its own signature in the query string.
//
// It makes no network call, which is the whole reason the two presign operations fit a
// job: the work is a hash chain over strings this process already holds. A caller may sign
// a URL for an object that does not exist and for a bucket they cannot reach — the store
// decides that when the URL is used, not when it is minted — which is stated in the panel
// so that an author does not read a minted link as proof the object is there.
//
// signHeaders is what every other operation uses; the two differ only in where the
// signature goes and in signing [unsignedPayload] instead of a body hash.
func presign(method, rawURL string, header http.Header, creds credentials, region string, expiresIn int32, now time.Time) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("s3: cannot build a URL to sign: %w", err)
	}
	now = now.UTC()
	scope := credentialScope(now, region)
	q := u.Query()
	q.Set("X-Amz-Algorithm", signAlgorithm)
	q.Set("X-Amz-Credential", creds.AccessKeyID+"/"+scope)
	q.Set("X-Amz-Date", now.Format(amzDateFormat))
	q.Set("X-Amz-Expires", strconv.FormatInt(int64(expiresIn), 10))
	if creds.SessionToken != "" {
		q.Set("X-Amz-Security-Token", creds.SessionToken)
	}
	sr := signedRequest{
		Method:      method,
		Host:        u.Host,
		Path:        u.Path,
		Query:       q,
		Headers:     header,
		PayloadHash: unsignedPayload,
	}
	// The signed-header list has to be in the query *before* the signature is computed,
	// because it is one of the query parameters the canonical request covers.
	signed, _ := canonicalRequest(sr)
	q.Set("X-Amz-SignedHeaders", strings.Join(signed, ";"))
	sr.Query = q
	_, canonical := canonicalRequest(sr)
	sig := sign(creds.SecretAccessKey, now, region, stringToSign(now, scope, canonical))
	q.Set("X-Amz-Signature", sig)
	u.RawQuery = encodeQuery(q)
	return u.String(), nil
}

// canonicalRequest renders SigV4's canonical request and returns the sorted list of header
// names it signed alongside it. The two come back together because they are two views of
// the same decision — a caller that recomputed the list would be the second implementation
// this function exists to avoid.
func canonicalRequest(sr signedRequest) (signed []string, canonical string) {
	headers := map[string]string{"host": sr.Host}
	for name, values := range sr.Headers {
		lower := strings.ToLower(name)
		// Authorization is the output of signing, and Content-Length is added and altered
		// by transports between here and the store. Signing either is how a request that
		// was correct when it was built is refused after a proxy touched it.
		if lower == "authorization" || lower == "content-length" {
			continue
		}
		if !signableHeader(lower) {
			continue
		}
		headers[lower] = strings.Join(trimAll(values), ",")
	}
	signed = make([]string, 0, len(headers))
	for name := range headers {
		signed = append(signed, name)
	}
	sort.Strings(signed)
	var canonicalHeaders strings.Builder
	for _, name := range signed {
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(headers[name])
		canonicalHeaders.WriteByte('\n')
	}
	canonical = strings.Join([]string{
		sr.Method,
		canonicalPath(sr.Path),
		encodeQuery(sr.Query),
		canonicalHeaders.String(),
		strings.Join(signed, ";"),
		sr.PayloadHash,
	}, "\n")
	return signed, canonical
}

// signableHeader reports whether a header belongs in the signature. Everything the caller
// set that S3 reads is signed — content-type, the x-amz-* family, the copy source — and
// the transport's own bookkeeping is not.
//
// It is a whitelist rather than a blacklist on purpose. A header this worker does not know
// about is one a proxy or the Go transport may add, rewrite or drop between signing and
// sending, and every one of those turns a correct request into SignatureDoesNotMatch.
func signableHeader(lower string) bool {
	return lower == "content-type" || lower == "content-md5" || strings.HasPrefix(lower, "x-amz-")
}

// trimAll trims each header value, which is what the canonical form requires: leading and
// trailing whitespace is not part of a header's value, and a store that trimmed where the
// signer did not would disagree about the signature and say nothing about whitespace.
func trimAll(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, strings.TrimSpace(v))
	}
	return out
}

// canonicalPath encodes a path for the canonical request: each segment URI-encoded, the
// separators left alone, and an empty path rendered as "/".
//
// S3 encodes the path exactly *once*. Every other AWS service encodes it twice, and the
// SigV4 documentation describes the double encoding as the rule with S3 as the exception —
// which is why a signer written from that documentation works for every key that contains
// no character needing encoding, and fails on the first key with a space in it.
func canonicalPath(p string) string {
	if p == "" {
		return "/"
	}
	segments := strings.Split(p, "/")
	for i, s := range segments {
		segments[i] = uriEncode(s)
	}
	return strings.Join(segments, "/")
}

// encodeQuery renders query parameters in the canonical form: RFC 3986 encoding, sorted by
// name and then by value, joined with & — and a parameter with no value still carrying its
// "=". Go's url.Values.Encode is almost this, but it encodes a space as "+" in some paths
// and leaves a few characters alone that RFC 3986 does not, so the encoding is done here
// rather than borrowed.
func encodeQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		values := append([]string(nil), q[k]...)
		sort.Strings(values)
		for _, v := range values {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(uriEncode(k))
			b.WriteByte('=')
			b.WriteString(uriEncode(v))
		}
	}
	return b.String()
}

// uriEncode is RFC 3986's percent-encoding with SigV4's unreserved set: letters, digits
// and the four characters -._~ pass through, everything else becomes %XX with uppercase
// hex. A space is %20 and never "+", which is the single most common way a hand-written
// signer disagrees with the store.
func uriEncode(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			const hexDigits = "0123456789ABCDEF"
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		}
	}
	return b.String()
}

// credentialScope is the date/region/service/terminator path a signature is valid within.
// It is also what binds a signature to a *day*: a signing key derived for yesterday
// produces a signature the store refuses, which is why the instant used here and the one
// in X-Amz-Date must be the same value rather than two calls to a clock.
func credentialScope(now time.Time, region string) string {
	return strings.Join([]string{now.Format(scopeDateFormat), region, signService, signTerminator}, "/")
}

// stringToSign is the second of SigV4's three steps: the algorithm, the instant, the scope
// and the hash of the canonical request.
func stringToSign(now time.Time, scope, canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return strings.Join([]string{signAlgorithm, now.Format(amzDateFormat), scope, hex.EncodeToString(sum[:])}, "\n")
}

// signingKey derives the key a signature is computed with: HMAC-SHA256 chained over the
// date, the region, the service and the terminator, starting from "AWS4" plus the secret.
//
// The chain is what makes the key scoped: a key derived for one day and one region signs
// nothing outside them, so a signature that leaks is worth a day in one region rather than
// the account.
func signingKey(secret string, now time.Time, region string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), now.Format(scopeDateFormat))
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, signService)
	return hmacSHA256(k, signTerminator)
}

// sign performs the last step: the signing key over the string to sign, in lowercase hex.
func sign(secret string, now time.Time, region, toSign string) string {
	return hex.EncodeToString(hmacSHA256(signingKey(secret, now, region), toSign))
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// hashPayload is the hex SHA-256 of a request body, which S3 requires in
// x-amz-content-sha256 on every header-signed request. An empty body answers with the
// constant rather than hashing nothing.
func hashPayload(body []byte) string {
	if len(body) == 0 {
		return emptyPayloadHash
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
