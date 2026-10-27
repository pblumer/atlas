package blueprintregistry

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func response(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func testPublicClient(t *testing.T, index, signature []byte, pkg []byte) *PublicClient {
	t.Helper()
	_, key := signIndex(index)
	c, err := NewPublicClient("https://catalog.example.org/blueprints/index.json", key)
	if err != nil {
		t.Fatal(err)
	}
	c.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || req.URL.Host != "catalog.example.org" {
			t.Errorf("off-origin request: %s", req.URL)
		}
		if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Error("registry request transmitted ambient credentials")
		}
		switch req.URL.Path {
		case "/blueprints/index.json":
			return response(http.StatusOK, string(index)), nil
		case "/blueprints/index.json.sig":
			return response(http.StatusOK, string(signature)), nil
		case "/blueprints/packages/purchase-approval-1.0.0.tar.gz":
			return response(http.StatusOK, string(pkg)), nil
		default:
			t.Errorf("unexpected path: %s", req.URL.Path)
			return response(http.StatusNotFound, "not found"), nil
		}
	})
	return c
}

func TestPublicClientLoadAndDownloadPinnedPackage(t *testing.T) {
	pkg := []byte("immutable compressed package bytes")
	hash := sha256.Sum256(pkg)
	index := mutateTestIndex(t, func(root map[string]any) {
		artifact := releaseFields(root)["artifact"].(map[string]any)
		artifact["digest"] = "sha256:" + hex.EncodeToString(hash[:])
		artifact["sizeBytes"] = len(pkg)
	})
	sig, _ := signIndex(index)
	c := testPublicClient(t, index, sig, pkg)
	got, err := c.LoadIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Releases) != 1 || got.Releases[0].BlueprintID != "org.example.purchase-approval" {
		t.Fatalf("invalid trusted list: %+v", got)
	}
	downloaded, err := c.Download(context.Background(), "org.example.purchase-approval", "1.0.0")
	if err != nil || string(downloaded) != string(pkg) {
		t.Fatalf("download: %q, %v", downloaded, err)
	}
	if _, err := c.Download(context.Background(), "org.example.purchase-approval", "2.0.0"); err == nil {
		t.Fatal("unknown version was downloadable")
	}
	if _, err := c.Download(context.Background(), "org.example.other", "1.0.0"); err == nil {
		t.Fatal("unlisted blueprint was downloadable")
	}
}

func TestPublicClientRejectsTamperedIndexAndArtifact(t *testing.T) {
	pkg := []byte("trusted bytes")
	sum := sha256.Sum256(pkg)
	index := mutateTestIndex(t, func(root map[string]any) {
		artifact := releaseFields(root)["artifact"].(map[string]any)
		artifact["digest"] = "sha256:" + hex.EncodeToString(sum[:])
		artifact["sizeBytes"] = len(pkg)
	})
	sig, _ := signIndex(index)
	t.Run("wrong signature", func(t *testing.T) {
		c := testPublicClient(t, index, []byte(base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))), pkg)
		if _, err := c.LoadIndex(context.Background()); err == nil {
			t.Fatal("unsigned catalogue accepted")
		}
		if _, err := c.Download(context.Background(), "org.example.purchase-approval", "1.0.0"); err == nil {
			t.Fatal("unsigned package downloaded")
		}
	})
	t.Run("incorrect package bytes", func(t *testing.T) {
		c := testPublicClient(t, index, sig, []byte("forged bytes!"))
		if _, err := c.Download(context.Background(), "org.example.purchase-approval", "1.0.0"); err == nil {
			t.Fatal("tampered package accepted")
		}
	})
	t.Run("size mismatch", func(t *testing.T) {
		c := testPublicClient(t, index, sig, []byte("short"))
		if _, err := c.Download(context.Background(), "org.example.purchase-approval", "1.0.0"); err == nil {
			t.Fatal("truncated package accepted")
		}
	})
}

func TestPublicClientRejectsUnsafeIndexURLs(t *testing.T) {
	_, key := signIndex(testIndex(t))
	for _, raw := range []string{
		"", "http://catalog.example.org/index.json",
		"https://127.0.0.1/index.json", "https://[::1]/index.json",
		"https://user:password@catalog.example.org/index.json",
		"https://catalog.example.org:8443/index.json",
		"https://catalog.example.org/index.json?token=secret",
		"https://catalog.example.org/index.json#frag",
		"https://catalog.example.org/safe/%2e%2e/index.json",
		"https://catalog.example.org/blueprints/../index.json",
		"https://catalog.example.org/other.json",
		"https://catalog.example.org./index.json",
		"https://catalog_example.org/index.json",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := NewPublicClient(raw, key); err == nil {
				t.Fatalf("unsafe index URL accepted: %q", raw)
			}
		})
	}
	if _, err := NewPublicClient("https://catalog.example.org/index.json", key[:31]); err == nil {
		t.Fatal("invalid pin accepted")
	}
	if _, err := NewPublicClient("https://catalog.example.org/index.json", key); err != nil {
		t.Fatalf("valid URL refused: %v", err)
	}
}

func TestPublicClientRejectsResponsesAndRedirects(t *testing.T) {
	index := testIndex(t)
	sig, _ := signIndex(index)
	for _, tc := range []struct {
		name  string
		path  string
		reply *http.Response
	}{
		{"server error", "/blueprints/index.json", response(http.StatusServiceUnavailable, "offline")},
		{"index too large", "/blueprints/index.json", response(http.StatusOK, strings.Repeat("a", MaxIndexBytes+1))},
		{"signature too large", "/blueprints/index.json.sig", response(http.StatusOK, strings.Repeat("x", 90))},
		{"cross-origin redirect", "/blueprints/index.json", func() *http.Response {
			r := response(http.StatusFound, "")
			r.Header.Set("Location", "https://attacker.invalid/index.json")
			return r
		}()},
		{"same-origin redirect", "/blueprints/index.json", func() *http.Response {
			r := response(http.StatusFound, "")
			r.Header.Set("Location", "/redirected.json")
			return r
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testPublicClient(t, index, sig, nil)
			base := c.httpClient.Transport
			c.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == tc.path {
					return tc.reply, nil
				}
				return base.RoundTrip(req)
			})
			if _, err := c.LoadIndex(context.Background()); err == nil {
				t.Fatal("unsafe registry response accepted")
			}
		})
	}
}

func TestPublicClientIPRestrictions(t *testing.T) {
	for _, tc := range []struct {
		address string
		allowed bool
	}{
		{"8.8.8.8", true}, {"1.1.1.1", true},
		{"2606:4700:4700::1111", true},
		{"127.0.0.1", false}, {"10.1.2.3", false}, {"172.16.5.1", false},
		{"192.168.1.1", false}, {"169.254.169.254", false},
		{"100.64.1.3", false}, {"198.18.0.1", false},
		{"192.0.2.1", false}, {"198.51.100.9", false},
		{"203.0.113.10", false}, {"0.0.0.0", false},
		{"::1", false}, {"fc00::1", false}, {"fe80::1", false},
		{"2001:db8::1", false}, {"::ffff:127.0.0.1", false},
	} {
		ip := netip.MustParseAddr(tc.address)
		if got := publicAddress(ip); got != tc.allowed {
			t.Errorf("%s public=%v, expected=%v", tc.address, got, tc.allowed)
		}
	}
}

func TestPublicClientDialRefusesPrivateDNSBeforeConnect(t *testing.T) {
	lookedUp, dialed := false, false
	lookup := func(_ context.Context, _ string) ([]net.IPAddr, error) {
		lookedUp = true
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}, {IP: net.ParseIP("8.8.8.8")}}, nil
	}
	dial := func(_ context.Context, _, _ string) (net.Conn, error) {
		dialed = true
		return nil, fmt.Errorf("should not dial")
	}
	_, err := dialPublic(context.Background(), "tcp", "catalog.example.org:443", lookup, dial)
	if err == nil || !lookedUp || dialed {
		t.Fatalf("mixed public/private DNS was not refused before dialing: lookup=%v, dialed=%v, err=%v", lookedUp, dialed, err)
	}
}
