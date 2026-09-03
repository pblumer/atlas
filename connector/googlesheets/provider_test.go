package googlesheets

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// keyPEM is a fresh service-account signing key in the format Google's JSON key file
// carries.
func keyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// TestApplyGoogleDefaults: an operator copying two fields out of a key file should not
// also have to know Google's token endpoint or spell out two scope URLs.
func TestApplyGoogleDefaults(t *testing.T) {
	b := credentialBundle{Method: methodServiceAccount}
	applyGoogleDefaults(&b)
	if b.TokenURL != googleTokenURL {
		t.Errorf("tokenUrl = %q; want Google's default", b.TokenURL)
	}
	if !strings.Contains(b.Scope, "spreadsheets") || !strings.Contains(b.Scope, "drive") {
		t.Errorf("scope = %q; want both halves the operations need", b.Scope)
	}
	// An authored override is left alone — narrowing to drive.file is the common one.
	narrowed := credentialBundle{Scope: "https://www.googleapis.com/auth/drive.file", TokenURL: "https://x/token"}
	applyGoogleDefaults(&narrowed)
	if narrowed.Scope != "https://www.googleapis.com/auth/drive.file" || narrowed.TokenURL != "https://x/token" {
		t.Errorf("defaults overwrote an authored bundle: %+v", narrowed)
	}
}

// TestNewProviderClientRejectsBadBundles: every one of these is a configuration
// mistake an operator makes once, and each has to name what is missing rather than
// fail later as an HTTP error on a job.
func TestNewProviderClientRejectsBadBundles(t *testing.T) {
	for name, tc := range map[string]struct{ secret, want string }{
		"no credential":         {"", "no credential"},
		"not JSON":              {"{nope", "not valid JSON"},
		"unknown method":        {`{"method":"magic"}`, "unknown auth method"},
		"service account bare":  {`{"method":"serviceAccount"}`, "clientEmail and privateKey"},
		"service account key":   {`{"method":"serviceAccount","clientEmail":"a@b","privateKey":"nope"}`, "private key"},
		"refresh token missing": {`{"method":"refreshToken","clientId":"c"}`, "clientId, clientSecret and refreshToken"},
	} {
		_, err := NewProviderClient(ProviderConfig{Secret: tc.secret})
		if err == nil {
			t.Errorf("%s: want an error, got nil", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q should mention %q", name, err, tc.want)
		}
	}
}

// TestNewProviderClientBuildsAServiceAccountClient covers the normal case end to end:
// a bundle in, a usable client out, and the token it mints reaching the API call.
func TestNewProviderClientBuildsAServiceAccountClient(t *testing.T) {
	var tokenCalls int
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			tokenCalls++
			_, _ = w.Write([]byte(`{"access_token":"minted","expires_in":3600}`))
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer minted" {
			t.Errorf("Authorization = %q; want the minted token", got)
		}
		_, _ = w.Write([]byte(`{"values":[["a"]]}`))
	}))
	defer google.Close()

	bundle, err := json.Marshal(credentialBundle{
		Method: methodServiceAccount, ClientEmail: "atlas@example.iam.gserviceaccount.com",
		PrivateKey: keyPEM(t), TokenURL: google.URL + "/token",
	})
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	client, err := NewProviderClient(ProviderConfig{Secret: string(bundle), Endpoint: google.URL})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := client.Do(context.Background(), Request{
			Operation: "read-range", Spreadsheet: sheetIDForTest, Range: "A1",
		}); err != nil {
			t.Fatalf("Do: %v", err)
		}
	}
	// The cache is the point of NewCached: two calls, one token exchange.
	if tokenCalls != 1 {
		t.Errorf("minted %d tokens for 2 calls; want 1", tokenCalls)
	}
}

// TestNewTokenSourceRefreshTokenGrant: the consumer-account path, which needs no key
// and no impersonation.
func TestNewTokenSourceRefreshTokenGrant(t *testing.T) {
	b := credentialBundle{Method: methodRefreshToken, ClientID: "c", ClientSecret: "s", RefreshToken: "r"}
	applyGoogleDefaults(&b)
	if _, err := newTokenSource(b, http.DefaultClient, func() time.Time { return time.Now() }); err != nil {
		t.Errorf("refreshToken bundle: unexpected error %v", err)
	}
}

// TestNewTokenSourceNeedsATokenURL: an override that blanks it out is a bundle that
// cannot mint anything, and the failure belongs at build time.
func TestNewTokenSourceNeedsATokenURL(t *testing.T) {
	_, err := newTokenSource(credentialBundle{Method: methodServiceAccount}, http.DefaultClient, nil)
	if err == nil || !strings.Contains(err.Error(), "token URL") {
		t.Errorf("bundle with no token URL: want a token-URL error, got %v", err)
	}
}

const sheetIDForTest = "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms"
