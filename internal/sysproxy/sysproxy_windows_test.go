//go:build windows

package sysproxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
	"unsafe"
)

// The structs are WinHTTP's own; a field out of place would be read as another, so
// their sizes are pinned to the C ones on the 64-bit build Atlas ships.
func TestTheWinHTTPStructsMatchTheirCLayout(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("the sizes below are the 64-bit ones")
	}
	for name, got := range map[string]uintptr{
		"WINHTTP_CURRENT_USER_IE_PROXY_CONFIG": unsafe.Sizeof(ieProxyConfig{}),
		"WINHTTP_AUTOPROXY_OPTIONS":            unsafe.Sizeof(autoProxyOptions{}),
		"WINHTTP_PROXY_INFO":                   unsafe.Sizeof(proxyInfo{}),
	} {
		want := map[string]uintptr{
			"WINHTTP_CURRENT_USER_IE_PROXY_CONFIG": 32,
			"WINHTTP_AUTOPROXY_OPTIONS":            32,
			"WINHTTP_PROXY_INFO":                   24,
		}[name]
		if got != want {
			t.Errorf("%s is %d bytes, want %d", name, got, want)
		}
	}
}

// The runner's own Internet Options are whatever its image carries; what is tested here
// is that WinHTTP is reached, answers, and that its strings come back intact.
func TestWinHTTPIsAvailable(t *testing.T) {
	b := platform()
	if b == nil {
		t.Fatal("WinHTTP is not available on this Windows")
	}
	s, err := b.readSettings()
	t.Logf("settings of this account: %+v (err %v)", s.Redacted(), err)
}

// A PAC script WinHTTP downloads and runs is the path a group-policy proxy takes. Its
// answer — a proxy list, or DIRECT — arrives as the proxy string the resolver parses.
func TestWinHTTPRunsAPACScript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		_, _ = w.Write([]byte(`function FindProxyForURL(url, host) {
	if (host == "intranet.example") return "DIRECT";
	return "PROXY prx.example:8080; PROXY second.example:8080";
}`))
	}))
	defer srv.Close()

	b := platform()
	list, err := b.autoProxy("https://graph.microsoft.com/", false, srv.URL+"/proxy.pac")
	if err != nil {
		t.Fatalf("autoProxy: %v", err)
	}
	if got := proxyString(parseProxyList(list, true)); got != "http://prx.example:8080" {
		t.Errorf("PAC answer %q resolved to %s, want http://prx.example:8080", list, got)
	}
	direct, err := b.autoProxy("https://intranet.example/", false, srv.URL+"/proxy.pac")
	if err != nil {
		t.Fatalf("autoProxy: %v", err)
	}
	if direct != "" {
		t.Errorf("PAC DIRECT came back as %q, want empty", direct)
	}
}

// A PAC URL nobody answers fails quickly and is not mistaken for a failed detection.
func TestWinHTTPReportsAnUnreachablePAC(t *testing.T) {
	_, err := platform().autoProxy("https://graph.microsoft.com/", false, "http://127.0.0.1:1/proxy.pac")
	if err == nil {
		t.Fatal("an unreachable PAC answered")
	}
	if errors.Is(err, errAutoDetectFailed) {
		t.Errorf("an unreachable PAC was reported as a failed detection: %v", err)
	}
}

// Whatever the runner's settings, loopback stays direct once Install has run.
func TestInstallOnWindowsKeepsLoopbackDirect(t *testing.T) {
	tr := &http.Transport{Proxy: sentinelProxy}
	mode, _ := install(func(string) string { return "" }, platform(), tr, time.Now)
	t.Logf("mode on this runner: %s", mode)
	if mode != ModeWindows {
		return
	}
	got, err := tr.Proxy(&http.Request{URL: mustURL(t, "http://127.0.0.1:8080/api/v1/jobs/activate")})
	if err != nil || got != nil {
		t.Errorf("loopback went through %v (err %v), want DIRECT", got, err)
	}
}
