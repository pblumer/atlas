package exchange

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/exchange/catalog"
)

func TestBundleBuildsAnOnlineClientWithASecret(t *testing.T) {
	var form url.Values
	var tokenPath string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form, tokenPath = r.PostForm, r.URL.Path
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	}))
	defer idp.Close()
	exo := newFakeExchange(t, fakeReply{body: `{"value":[]}`})

	b, err := NewBackend(exo.srv.URL, `{"organization":"contoso.onmicrosoft.com","clientId":"app","clientSecret":"s3cret","authorityUrl":"`+idp.URL+`"}`, Options{HTTPClient: exo.srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if b.Target() != catalog.Online {
		t.Fatalf("target = %v", b.Target())
	}
	if _, err := b.Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox"}, 0); err != nil {
		t.Fatal(err)
	}
	// The tenant defaults to the organization: the identity platform takes the initial
	// domain where it takes the tenant id, so an operator who knows one need not look up
	// the other.
	if tokenPath != "/contoso.onmicrosoft.com/oauth2/v2.0/token" {
		t.Errorf("token path = %q", tokenPath)
	}
	if form.Get("client_secret") != "s3cret" || form.Get("scope") != exo.srv.URL+"/.default" {
		t.Errorf("form = %v", form)
	}
	if exo.calls[0].auth != "Bearer tok" {
		t.Errorf("auth = %q", exo.calls[0].auth)
	}
}

func TestBundleBuildsAnOnlineClientWithACertificate(t *testing.T) {
	_, _, pemBundle := testCertificate(t)
	var form url.Values
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	}))
	defer idp.Close()
	exo := newFakeExchange(t, fakeReply{body: `{"value":[]}`})
	raw := `{"organization":"contoso.onmicrosoft.com","tenantId":"0000-1111","clientId":"app","certificate":` + jsonString(pemBundle) + `,"authorityUrl":"` + idp.URL + `"}`
	b, err := NewBackend(exo.srv.URL, raw, Options{HTTPClient: exo.srv.Client(), Now: func() time.Time { return time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox"}, 0); err != nil {
		t.Fatal(err)
	}
	if form.Get("client_assertion") == "" || form.Get("client_secret") != "" {
		t.Errorf("form = %v", form)
	}
}

func TestBundleBuildsAnOnPremClient(t *testing.T) {
	b, err := NewBackend("http://exch01.contoso.local/PowerShell/", `{"target":"onprem","username":"CONTOSO\\svc","password":"p"}`, Options{PowerShell: "powershell.exe"})
	if err != nil {
		t.Fatal(err)
	}
	c, ok := b.(*OnPremClient)
	if !ok {
		t.Fatalf("backend is a %T", b)
	}
	if c.uri != "http://exch01.contoso.local/PowerShell/" || c.username != `CONTOSO\svc` || c.powershell != "powershell.exe" || c.auth != "Kerberos" {
		t.Errorf("client = %+v", c)
	}
	// The URI may live in the bundle too, for an operator who keeps the whole
	// configuration in one place; the endpoint wins when both are set.
	b, err = NewBackend("", `{"target":"onprem","connectionUri":"https://exch02/PowerShell/","authentication":"Negotiate","timeoutSeconds":30}`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if c := b.(*OnPremClient); c.uri != "https://exch02/PowerShell/" || c.auth != "Negotiate" || c.timeout != 30*time.Second {
		t.Errorf("client = %+v", c)
	}
}

func TestBundleRefusals(t *testing.T) {
	cases := []struct {
		name, endpoint, raw, want string
	}{
		{"empty", "", "", "empty"},
		{"not json", "", "{", "JSON"},
		{"typo", "", `{"organization":"c","clientId":"a","clientSecert":"s"}`, "clientSecert"},
		{"unknown target", "", `{"target":"cloud"}`, "target"},
		{"no organization", "", `{"clientId":"a","clientSecret":"s"}`, "organization"},
		{"no client", "", `{"organization":"c","clientSecret":"s"}`, "clientId"},
		{"no credential", "", `{"organization":"c","clientId":"a"}`, "clientSecret or a certificate"},
		{"both credentials", "", `{"organization":"c","clientId":"a","clientSecret":"s","certificate":"x"}`, "not both"},
		{"bad certificate", "", `{"organization":"c","clientId":"a","certificate":"x"}`, "certificate"},
		{"online fields on prem", "http://e/PowerShell/", `{"target":"onprem","clientId":"a"}`, "clientId"},
		{"prem fields online", "", `{"organization":"c","clientId":"a","clientSecret":"s","username":"u"}`, "username"},
		{"prem without uri", "", `{"target":"onprem"}`, "connection URI"},
		{"negative timeout", "http://e/PowerShell/", `{"target":"onprem","timeoutSeconds":-1}`, "timeoutSeconds"},
		{"online endpoint not a url", "outlook", `{"organization":"c","clientId":"a","clientSecret":"s"}`, "https"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewBackend(c.endpoint, c.raw, Options{})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want mention of %q", err, c.want)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "exchange:") {
				t.Errorf("err %q does not name the worker type", err)
			}
			if CheckBundle(c.endpoint, c.raw) == nil {
				t.Error("CheckBundle accepted what NewBackend refused")
			}
		})
	}
	if err := CheckBundle("", `{"organization":"c","clientId":"a","clientSecret":"s"}`); err != nil {
		t.Errorf("CheckBundle refused a usable bundle: %v", err)
	}
}

// TestBundleErrorsDoNotQuoteSecrets: a refused bundle is reported in a worker's log and
// the Console, and the reason must not carry what it refused.
func TestBundleErrorsDoNotQuoteSecrets(t *testing.T) {
	for _, raw := range []string{
		`{"organization":"c","clientId":"a","clientSecret":"TOPSECRET","certificate":"TOPSECRET"}`,
		`{"target":"onprem","password":"TOPSECRET"}`,
		`{"organization":"c","clientId":"a","certificate":"TOPSECRET"}`,
	} {
		_, err := NewBackend("", raw, Options{})
		if err == nil || strings.Contains(err.Error(), "TOPSECRET") {
			t.Errorf("err = %v", err)
		}
	}
}

func testCertificate(t *testing.T) (*x509.Certificate, *rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "atlas-exchange"},
		NotBefore: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	return cert, key, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
