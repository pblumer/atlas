package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/limits"
)

// The client ID metadata document and the signed assertion, piece by piece
// (ADR-0512 §1). The flow through the server is in connectedsystem_http_test.go; these
// hold each rule on its own, so a refusal is known to be refused for its own reason.

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// metaDoc is a document that passes every check, for a client at clientID.
func metaDoc(clientID string, pub ed25519.PublicKey) map[string]any {
	u, _ := url.Parse(clientID)
	return map[string]any{
		"client_id":                       clientID,
		"client_name":                     "Example wiki",
		"client_uri":                      u.Scheme + "://" + u.Host,
		"redirect_uris":                   []string{u.Scheme + "://" + u.Host + "/api/engines/atlas/connect/callback"},
		"grant_types":                     []string{"authorization_code", "refresh_token"},
		"response_types":                  []string{"code"},
		"token_endpoint_auth_method":      "private_key_jwt",
		"token_endpoint_auth_signing_alg": "EdDSA",
		"jwks": map[string]any{"keys": []any{map[string]any{
			"kty": "OKP", "crv": "Ed25519", "x": b64(pub), "kid": "k1", "use": "sig", "alg": "EdDSA",
		}}},
	}
}

func TestADocumentIsCheckedAgainstTheURLItIsPublishedAt(t *testing.T) {
	const id = "https://wiki.example.org/api/engines/client-metadata"
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	u, _ := url.Parse(id)

	good := metaDoc(id, pub)
	body, _ := json.Marshal(good)
	md, err := parseClientMetadata(u, body)
	if err != nil {
		t.Fatalf("a valid document was refused: %v", err)
	}
	if !md.client.Metadata || md.client.ID != id || md.client.Name != "Example wiki" || len(md.keys) != 1 || md.keys[0].alg != "EdDSA" {
		t.Fatalf("parsed = %+v", md)
	}

	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"another client_id", func(d map[string]any) { d["client_id"] = "https://wiki.example.org/other" }, "another client_id"},
		{"a redirect on another host", func(d map[string]any) {
			d["redirect_uris"] = []string{"https://evil.example.com/callback"}
		}, "not on the origin"},
		{"a redirect on another port", func(d map[string]any) {
			d["redirect_uris"] = []string{"https://wiki.example.org:8443/callback"}
		}, "not on the origin"},
		{"no redirect", func(d map[string]any) { delete(d, "redirect_uris") }, "redirect URI"},
		{"a secret method", func(d map[string]any) { d["token_endpoint_auth_method"] = "client_secret_post" }, "private_key_jwt"},
		{"a published secret", func(d map[string]any) { d["client_secret"] = "s3cret" }, "client_secret"},
		{"an implicit grant", func(d map[string]any) { d["grant_types"] = []string{"implicit"} }, "grant_type"},
		{"a token response", func(d map[string]any) { d["response_types"] = []string{"token"} }, "response_type"},
		{"only a jwks_uri", func(d map[string]any) {
			delete(d, "jwks")
			d["jwks_uri"] = "https://wiki.example.org/jwks"
		}, "jwks_uri"},
		{"no keys", func(d map[string]any) { delete(d, "jwks") }, "no keys"},
		{"no Ed25519 key", func(d map[string]any) {
			d["jwks"] = map[string]any{"keys": []any{map[string]any{"kty": "RSA", "n": "AQAB", "e": "AQAB"}}}
		}, "Ed25519"},
		{"an encryption key only", func(d map[string]any) {
			d["jwks"] = map[string]any{"keys": []any{map[string]any{"kty": "OKP", "crv": "Ed25519", "x": b64(pub), "use": "enc"}}}
		}, "Ed25519"},
		{"a short key", func(d map[string]any) {
			d["jwks"] = map[string]any{"keys": []any{map[string]any{"kty": "OKP", "crv": "Ed25519", "x": b64(pub[:16])}}}
		}, "Ed25519"},
		{"a name with a line break", func(d map[string]any) { d["client_name"] = "Wiki\nApprove me" }, "control"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := metaDoc(id, pub)
			tc.mutate(d)
			body, _ := json.Marshal(d)
			if _, err := parseClientMetadata(u, body); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
	if _, err := parseClientMetadata(u, []byte("[1,2]")); err == nil {
		t.Error("a document that is not an object was accepted")
	}
	// A nameless document is shown by its host.
	d := metaDoc(id, pub)
	delete(d, "client_name")
	body, _ = json.Marshal(d)
	if md, err := parseClientMetadata(u, body); err != nil || md.client.Name != "wiki.example.org" {
		t.Errorf("nameless document = %+v %v, want the host as its name", md.client, err)
	}
}

func TestAnES256KeyIsReadBesideTheEd25519One(t *testing.T) {
	const id = "https://wiki.example.org/meta"
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecBytes, _ := ec.PublicKey.Bytes() // 0x04 || X || Y
	d := metaDoc(id, pub)
	d["jwks"] = map[string]any{"keys": []any{
		d["jwks"].(map[string]any)["keys"].([]any)[0],
		map[string]any{"kty": "EC", "crv": "P-256", "x": b64(ecBytes[1:33]), "y": b64(ecBytes[33:]), "kid": "e1"},
		// Off the curve: passed over, not an error.
		map[string]any{"kty": "EC", "crv": "P-256", "x": b64(make([]byte, 32)), "y": b64(make([]byte, 32)), "kid": "bad"},
	}}
	u, _ := url.Parse(id)
	body, _ := json.Marshal(d)
	md, err := parseClientMetadata(u, body)
	if err != nil || len(md.keys) != 2 || md.keys[1].alg != "ES256" || md.keys[1].kid != "e1" {
		t.Fatalf("keys = %+v %v, want the Ed25519 key and the P-256 one", md.keys, err)
	}
}

func TestAClientIDMustBeASafeURL(t *testing.T) {
	for _, id := range []string{
		"http://wiki.example.org/meta",           // plain http to another host
		"https://user:pw@wiki.example.org/meta",  // credentials
		"https://wiki.example.org/meta#fragment", // fragment
		"https:///meta",                          // no host
		"https://wiki.example.org/" + strings.Repeat("a", maxMetadataClientID),
	} {
		if _, err := metadataClientURL(id); err == nil {
			t.Errorf("%q was accepted as a client_id", id)
		}
	}
	for _, id := range []string{"https://wiki.example.org/meta", "http://127.0.0.1:8080/meta", "http://localhost/meta"} {
		if _, err := metadataClientURL(id); err != nil {
			t.Errorf("%q was refused: %v", id, err)
		}
	}
}

func TestADocumentIsKeptForItsLifetimeAndAnHourAtMost(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   time.Duration
	}{
		{"", time.Hour},
		{"max-age=300", 5 * time.Minute},
		{"public, max-age=86400", time.Hour},
		{"no-store", 0},
		{"no-cache, max-age=60", 0},
		{"max-age=nonsense", time.Hour},
	} {
		if got := documentMaxAge(tc.header); got != tc.want {
			t.Errorf("documentMaxAge(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestOnlyPublicAddressesArePublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"93.184.216.34": true, "2606:2800:220:1:248:1893:25c8:1946": true,
		"127.0.0.1": false, "10.1.2.3": false, "192.168.1.1": false, "172.16.0.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "::1": false,
		"fe80::1": false, "fc00::1": false, "64:ff9b::a00:1": false, "::ffff:127.0.0.1": false,
	} {
		if got := publicAddress(netip.MustParseAddr(addr)); got != want {
			t.Errorf("publicAddress(%s) = %v, want %v", addr, got, want)
		}
	}
}

// TestAHostThatResolvesInsideIsNotDialled: every address a name resolves to is judged,
// so one internal address among public ones refuses the whole name, and nothing is
// dialled — unless private addresses are allowed.
func TestAHostThatResolvesInsideIsNotDialled(t *testing.T) {
	lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("10.0.0.7")}}, nil
	}
	dialled := []string{}
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		dialled = append(dialled, address)
		return nil, errors.New("not in a test")
	}
	if _, err := dialMetadataHost(context.Background(), "tcp", "wiki.example.org:443", false, lookup, dial); err == nil ||
		!strings.Contains(err.Error(), "ATLAS_CLIENT_METADATA_ALLOW_PRIVATE") {
		t.Fatalf("err = %v, want a refusal naming the setting", err)
	}
	if len(dialled) != 0 {
		t.Fatalf("dialled %v before every address was judged", dialled)
	}
	_, _ = dialMetadataHost(context.Background(), "tcp", "wiki.example.org:443", true, lookup, dial)
	if len(dialled) != 2 || dialled[0] != "93.184.216.34:443" {
		t.Fatalf("with private addresses allowed it dialled %v, want each resolved address in turn", dialled)
	}
	none := func(context.Context, string) ([]net.IPAddr, error) { return nil, nil }
	if _, err := dialMetadataHost(context.Background(), "tcp", "wiki.example.org:443", true, none, dial); err == nil {
		t.Error("a name with no address was dialled")
	}
}

// fetcherFor is a fetcher with the default budgets, reading from loopback.
func fetcherFor(t *testing.T, private bool) *clientMetadataFetcher {
	t.Helper()
	s := &Server{limits: limits.Default(), clientMetadataPrivate: private}
	return s.newClientMetadataFetcher()
}

func TestTheReadIsBounded(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	var (
		status   = http.StatusOK
		ctype    = "application/json"
		pad      = 0
		location = ""
		reads    = 0
	)
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if location != "" {
			http.Redirect(w, r, location, http.StatusFound)
			return
		}
		d := metaDoc(ts.URL+"/meta", pub)
		if pad > 0 {
			d["padding"] = strings.Repeat("x", pad)
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "max-age=300")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(d)
	}))
	defer ts.Close()
	id := ts.URL + "/meta"

	if _, err := fetcherFor(t, false).get(context.Background(), id, false); err == nil || !strings.Contains(err.Error(), "not a public address") {
		t.Fatalf("a loopback document was read without the setting: %v", err)
	}
	f := fetcherFor(t, true)
	md, err := f.get(context.Background(), id, false)
	if err != nil || md.client.ID != id {
		t.Fatalf("get = %+v %v", md.client, err)
	}
	// Cached: a second request reads nothing; fresh reads again.
	if _, _ = f.get(context.Background(), id, false); reads != 1 {
		t.Fatalf("the cached document was read again (%d reads)", reads)
	}
	if _, _ = f.get(context.Background(), id, true); reads != 2 {
		t.Fatalf("a fresh read did not read (%d reads)", reads)
	}

	for _, tc := range []struct {
		name  string
		setup func()
		want  string
	}{
		{"too large", func() { pad = 70 << 10 }, "larger than"},
		{"not JSON", func() { ctype = "text/html" }, "not served as JSON"},
		{"not found", func() { status = http.StatusNotFound }, "answered 404"},
		{"a redirect", func() { location = "/elsewhere" }, "redirects"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, ctype, pad, location = http.StatusOK, "application/json", 0, ""
			tc.setup()
			if _, err := f.get(context.Background(), id, true); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
	// A failed read drops what was cached, so a broken document is not served on.
	status, ctype, pad, location = http.StatusOK, "application/json", 0, ""
	status = http.StatusInternalServerError
	_, _ = f.get(context.Background(), id, true)
	status = http.StatusOK
	before := reads
	if _, err := f.get(context.Background(), id, false); err != nil || reads != before+1 {
		t.Fatalf("after a failed read the cache still answered (%v, %d reads)", err, reads-before)
	}
}

func TestReadsAreThrottledPerHost(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metaDoc(ts.URL+"/meta", pub))
	}))
	defer ts.Close()
	f := fetcherFor(t, true)
	burst := int(limits.Default().ClientMetadataHostBurst)
	for i := 0; i < burst; i++ {
		if _, err := f.get(context.Background(), ts.URL+"/meta", true); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	if _, err := f.get(context.Background(), ts.URL+"/meta", true); err == nil || !strings.Contains(err.Error(), "too often") {
		t.Fatalf("read past the burst = %v, want the throttle", err)
	}
}

// signAssertion signs claims with an Ed25519 key, under kid.
func signAssertion(t *testing.T, priv ed25519.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]any{"alg": "EdDSA", "typ": "JWT", "kid": kid})
	c, _ := json.Marshal(claims)
	input := b64(h) + "." + b64(c)
	return input + "." + b64(ed25519.Sign(priv, []byte(input)))
}

func validClaims(id, aud string, now time.Time) map[string]any {
	return map[string]any{"iss": id, "sub": id, "aud": aud, "jti": "j-" + now.String(),
		"iat": now.Unix(), "exp": now.Add(2 * time.Minute).Unix()}
}

func TestAnAssertionIsBelievedOnlyAfterEveryCheck(t *testing.T) {
	const (
		id  = "https://wiki.example.org/meta"
		aud = "https://atlas.example.org/oauth/token"
	)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	keys := []clientKey{{kid: "k1", alg: "EdDSA", ed: pub}}
	now := time.Unix(1_800_000_000, 0)
	auds := []string{aud, "https://atlas.example.org"}

	if c, err := verifyClientAssertion(signAssertion(t, priv, "k1", validClaims(id, aud, now)), id, keys, auds, now); err != nil || c.Issuer != id {
		t.Fatalf("a valid assertion was refused: %v", err)
	}
	// The issuer URL is an accepted audience as well.
	if _, err := verifyClientAssertion(signAssertion(t, priv, "k1", validClaims(id, "https://atlas.example.org", now)), id, keys, auds, now); err != nil {
		t.Fatalf("an assertion to the issuer was refused: %v", err)
	}

	for _, tc := range []struct {
		name   string
		raw    func() string
		keyErr bool
		want   string
	}{
		{"signed by another key", func() string { return signAssertion(t, other, "k1", validClaims(id, aud, now)) }, true, ""},
		{"an unknown kid", func() string { return signAssertion(t, priv, "k9", validClaims(id, aud, now)) }, true, ""},
		{"alg none", func() string {
			h, _ := json.Marshal(map[string]any{"alg": "none"})
			c, _ := json.Marshal(validClaims(id, aud, now))
			return b64(h) + "." + b64(c) + "."
		}, false, "algorithm"},
		{"alg ES256 on an EdDSA key", func() string {
			h, _ := json.Marshal(map[string]any{"alg": "ES256", "kid": "k1"})
			c, _ := json.Marshal(validClaims(id, aud, now))
			input := b64(h) + "." + b64(c)
			return input + "." + b64(ed25519.Sign(priv, []byte(input)))
		}, true, ""},
		{"the wrong audience", func() string {
			return signAssertion(t, priv, "k1", validClaims(id, "https://other.example.org/oauth/token", now))
		}, false, "audience"},
		{"another issuer", func() string {
			c := validClaims(id, aud, now)
			c["iss"] = "https://evil.example.com/meta"
			return signAssertion(t, priv, "k1", c)
		}, false, "iss and sub"},
		{"another subject", func() string {
			c := validClaims(id, aud, now)
			c["sub"] = "someone"
			return signAssertion(t, priv, "k1", c)
		}, false, "iss and sub"},
		{"expired past the leeway", func() string {
			c := validClaims(id, aud, now.Add(-5*time.Minute))
			return signAssertion(t, priv, "k1", c)
		}, false, "expired"},
		{"from the future", func() string {
			c := validClaims(id, aud, now.Add(3*time.Minute))
			return signAssertion(t, priv, "k1", c)
		}, false, "future"},
		{"good for an hour", func() string {
			c := validClaims(id, aud, now)
			c["exp"] = now.Add(time.Hour).Unix()
			return signAssertion(t, priv, "k1", c)
		}, false, "five minutes"},
		{"no exp", func() string {
			c := validClaims(id, aud, now)
			delete(c, "exp")
			return signAssertion(t, priv, "k1", c)
		}, false, "exp"},
		{"no jti", func() string {
			c := validClaims(id, aud, now)
			delete(c, "jti")
			return signAssertion(t, priv, "k1", c)
		}, false, "jti"},
		{"two segments", func() string { return "a.b" }, false, "segments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := verifyClientAssertion(tc.raw(), id, keys, auds, now)
			switch {
			case err == nil:
				t.Fatal("accepted")
			case tc.keyErr && !errors.Is(err, errAssertionKey):
				t.Fatalf("err = %v, want the key refusal that reads the document again", err)
			case !tc.keyErr && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

func TestAnES256AssertionVerifiesWithItsKey(t *testing.T) {
	const id = "https://wiki.example.org/meta"
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	keys := []clientKey{{kid: "e1", alg: "ES256", ec: &ec.PublicKey}}
	now := time.Unix(1_800_000_000, 0)
	h, _ := json.Marshal(map[string]any{"alg": "ES256", "kid": "e1"})
	c, _ := json.Marshal(validClaims(id, "https://atlas.example.org", now))
	input := b64(h) + "." + b64(c)
	sum := sha256.Sum256([]byte(input))
	r, s, _ := ecdsa.Sign(rand.Reader, ec, sum[:])
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	if _, err := verifyClientAssertion(input+"."+b64(sig), id, keys, []string{"https://atlas.example.org"}, now); err != nil {
		t.Fatalf("an ES256 assertion was refused: %v", err)
	}
	sig[5] ^= 0xff
	if _, err := verifyClientAssertion(input+"."+b64(sig), id, keys, []string{"https://atlas.example.org"}, now); !errors.Is(err, errAssertionKey) {
		t.Fatalf("a tampered ES256 signature = %v", err)
	}
}

func TestAnAssertionIsGoodOnce(t *testing.T) {
	c := newAssertionReplayCache(2)
	now := time.Unix(1_800_000_000, 0)
	until := now.Add(time.Minute)
	if !c.spend("a", "j1", until, now) {
		t.Fatal("the first use was refused")
	}
	if c.spend("a", "j1", until, now.Add(30*time.Second)) {
		t.Fatal("a replay within the lifetime was accepted")
	}
	// The same jti from another client is another assertion.
	if !c.spend("b", "j1", until, now) {
		t.Fatal("another client's jti collided")
	}
	// Full of live assertions: refused rather than forgotten.
	if c.spend("a", "j2", until, now) {
		t.Fatal("a full cache forgot an assertion to make room")
	}
	// Once they could no longer be presented, they make room.
	later := until.Add(assertionLeeway + time.Second)
	if !c.spend("a", "j2", later.Add(time.Minute), later) {
		t.Fatal("expired entries did not make room")
	}
}

func TestAnIssuerIsReadUnverifiedOnlyToFindTheDocument(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	raw := signAssertion(t, priv, "k1", map[string]any{"iss": "https://wiki.example.org/meta"})
	if got := unverifiedIssuer(raw); got != "https://wiki.example.org/meta" {
		t.Errorf("unverifiedIssuer = %q", got)
	}
	for _, bad := range []string{"", "a.b", "a.!!.c", "a." + b64([]byte("[]")) + ".c"} {
		if got := unverifiedIssuer(bad); got != "" {
			t.Errorf("unverifiedIssuer(%q) = %q, want nothing", bad, got)
		}
	}
}

func TestAServerWithoutAReaderSaysSo(t *testing.T) {
	s := &Server{oauthClients: newOAuthClientIndex()}
	if _, err := s.lookupClient(context.Background(), "https://wiki.example.org/meta", false); !errors.Is(err, errNoMetadataReader) {
		t.Errorf("lookupClient = %v", err)
	}
	if _, err := s.lookupClient(context.Background(), "nope", false); !errors.Is(err, errUnknownClient) {
		t.Errorf("lookupClient(unknown) = %v", err)
	}
}
