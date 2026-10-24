package googlecontacts

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func testKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func bundleJSON(t *testing.T, b map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestParseBundleRefusals(t *testing.T) {
	for _, c := range []struct {
		name, raw, want string
	}{
		{"empty", " ", "empty"},
		{"syntax", `{"privateKey": "-----BEGIN`, "not a JSON object"},
		{"unknown field", `{"method":"serviceAccount","subjekt":"a@b.ch"}`, `unknown field "subjekt"`},
		{"not an object", `["serviceAccount"]`, "expected shape"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseBundle(c.raw)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want mention of %q", err, c.want)
			}
			if err != nil && strings.Contains(err.Error(), "BEGIN") {
				t.Errorf("the message quotes the key: %v", err)
			}
		})
	}
}

func TestNewClientRefusals(t *testing.T) {
	key := testKeyPEM(t)
	sa := func(extra map[string]any) map[string]any {
		b := map[string]any{"method": "serviceAccount", "clientEmail": "atlas@p.iam.gserviceaccount.com", "privateKey": key, "subject": "verkauf@firma.ch"}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	rt := map[string]any{"method": "refreshToken", "clientId": "id", "clientSecret": "s", "refreshToken": "r"}
	for _, c := range []struct {
		name     string
		endpoint string
		bundle   map[string]any
		want     string
	}{
		{"endpoint no URL", "people.example", sa(nil), "must be an https URL"},
		{"endpoint ftp", "ftp://people.example", sa(nil), "must be an https URL"},
		{"no subject", "", sa(map[string]any{"subject": " "}), "needs a subject"},
		{"no key", "", sa(map[string]any{"privateKey": ""}), "needs clientEmail and privateKey"},
		{"bad key", "", sa(map[string]any{"privateKey": "kein schlüssel"}), "googlecontacts"},
		{"refresh field on a service account", "", sa(map[string]any{"refreshToken": "r"}), "belongs to a refreshToken bundle"},
		{"service-account field on a refresh token", "", map[string]any{"method": "refreshToken", "clientId": "id", "clientSecret": "s", "refreshToken": "r", "subject": "x"}, "belongs to a serviceAccount bundle"},
		{"incomplete refresh token", "", map[string]any{"method": "refreshToken", "clientId": "id"}, "needs clientId, clientSecret and refreshToken"},
		{"unknown method", "", map[string]any{"method": "apiKey"}, "unknown auth method"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := CheckBundle(c.endpoint, bundleJSON(t, c.bundle))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want mention of %q", err, c.want)
			}
		})
	}
	if err := CheckBundle("", bundleJSON(t, sa(nil))); err != nil {
		t.Errorf("service account: %v", err)
	}
	if err := CheckBundle("https://proxy.example/people/", bundleJSON(t, rt)); err != nil {
		t.Errorf("refresh token: %v", err)
	}
	if err := CheckBundle("", ""); err == nil {
		t.Error("an empty bundle built a client")
	}
}

// tokenServer is Google's token endpoint: it records each form it was sent and answers
// with the access token the fake People API accepts.
type tokenServer struct {
	srv   *httptest.Server
	mu    sync.Mutex
	forms []url.Values
}

func newTokenServer(t *testing.T) *tokenServer {
	ts := &tokenServer{}
	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		ts.mu.Lock()
		ts.forms = append(ts.forms, r.PostForm)
		ts.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(ts.srv.Close)
	return ts
}

func claimsOf(t *testing.T, assertion string) map[string]any {
	t.Helper()
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		t.Fatalf("assertion %q is no JWT", assertion)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

// TestServiceAccountActsAsItsSubject: the assertion names the Workspace user whose
// address book the Worker reads, and asks for the contacts scope alone unless the
// bundle names more.
func TestServiceAccountActsAsItsSubject(t *testing.T) {
	people := newFakePeople(t)
	people.add(anna())
	tokens := newTokenServer(t)
	c, err := NewClient(people.srv.URL, bundleJSON(t, map[string]any{
		"method": "serviceAccount", "clientEmail": "atlas@p.iam.gserviceaccount.com", "privateKey": testKeyPEM(t),
		"subject": "verkauf@firma.ch", "tokenUrl": tokens.srv.URL,
	}), Options{HTTPClient: tokens.srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register("verkauf", c)
	out, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "list-contacts", MaxResults: 10, ResultVariable: "r"}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(out["r"].([]any)) != 1 {
		t.Errorf("list = %v", out)
	}
	if len(tokens.forms) != 1 {
		t.Fatalf("token requests = %d", len(tokens.forms))
	}
	form := tokens.forms[0]
	if form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Errorf("grant = %v", form)
	}
	claims := claimsOf(t, form.Get("assertion"))
	if claims["sub"] != "verkauf@firma.ch" || claims["scope"] != ScopeContacts || claims["iss"] != "atlas@p.iam.gserviceaccount.com" {
		t.Errorf("claims = %v", claims)
	}
}

// TestRefreshTokenAsksForNoScopeOfItsOwn: a refresh token carries the scopes its
// consent granted, and asking for another one would be refused.
func TestRefreshTokenAsksForNoScopeOfItsOwn(t *testing.T) {
	people := newFakePeople(t)
	tokens := newTokenServer(t)
	for _, scope := range []string{"", ScopeContacts + " " + ScopeDirectory} {
		c, err := NewClient(people.srv.URL, bundleJSON(t, map[string]any{
			"method": "refreshToken", "clientId": "id", "clientSecret": "s", "refreshToken": "r",
			"tokenUrl": tokens.srv.URL, "scope": scope,
		}), Options{HTTPClient: tokens.srv.Client()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.groups(context.Background()); err != nil {
			t.Fatal(err)
		}
		form := tokens.forms[len(tokens.forms)-1]
		if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "r" {
			t.Errorf("form = %v", form)
		}
		if _, sent := form["scope"]; sent != (scope != "") || form.Get("scope") != scope {
			t.Errorf("scope %q: form = %v", scope, form)
		}
	}
}

func TestAnUnreachableAPIIsNamedWithoutItsURL(t *testing.T) {
	people := newFakePeople(t)
	c := people.client()
	people.srv.Close()
	_, err := c.search(context.Background(), "geheim@kunde.ch", 5, readFields)
	if err == nil || strings.Contains(err.Error(), "geheim") || !strings.Contains(err.Error(), "could not be reached") {
		t.Errorf("err = %v", err)
	}
}
