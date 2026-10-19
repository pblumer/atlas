package sysproxy

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBackend stands in for WinHTTP: it answers autoProxy from a table and counts what
// it was asked, so the resolver's choices — when to ask Windows at all, when to fall
// back to the manual settings, how long to believe an answer — are testable anywhere.
type fakeBackend struct {
	mu       sync.Mutex
	s        Settings
	sErr     error
	answer   func(target string, autoDetect bool, configURL string) (string, error)
	calls    []autoCall
	settings int
}

type autoCall struct {
	target     string
	autoDetect bool
	configURL  string
}

func (f *fakeBackend) readSettings() (Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings++
	return f.s, f.sErr
}

func (f *fakeBackend) autoProxy(target string, autoDetect bool, configURL string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, autoCall{target, autoDetect, configURL})
	answer := f.answer
	f.mu.Unlock()
	if answer == nil {
		return "", errors.New("no answer configured")
	}
	return answer(target, autoDetect, configURL)
}

func (f *fakeBackend) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// clock is a settable time source, so cache lifetimes and the auto-detection back-off are
// tested without sleeping.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}

func proxyString(u *url.URL) string {
	if u == nil {
		return "DIRECT"
	}
	return u.String()
}

// The proxy strings Windows hands over come in the shapes Internet Options and a PAC
// script produce: one address for every protocol, a per-protocol list, several
// addresses in failover order. Each resolves to the one proxy Go's transport can use.
func TestParseProxyList(t *testing.T) {
	cases := []struct {
		list   string
		secure bool
		want   string
	}{
		{"prxp.example:8080", true, "http://prxp.example:8080"},
		{"prxp.example:8080", false, "http://prxp.example:8080"},
		{"http=a.example:80;https=b.example:8443", true, "http://b.example:8443"},
		{"http=a.example:80;https=b.example:8443", false, "http://a.example:80"},
		{"HTTPS=b.example:1", true, "http://b.example:1"},
		{"http=a.example:80", true, "DIRECT"},
		{"https=b.example:8443;fallback.example:3128", false, "http://fallback.example:3128"},
		{"first.example:3128;second.example:3128", true, "http://first.example:3128"},
		{"first.example:3128 second.example:3128", true, "http://first.example:3128"},
		{"http://a.example:3128", true, "http://a.example:3128"},
		{"https://a.example", true, "https://a.example:443"},
		{"a.example", false, "http://a.example:80"},
		{"user:pw@a.example:8080", true, "http://user:pw@a.example:8080"},
		{"[fd00::1]:8080", true, "http://[fd00::1]:8080"},
		{"[fd00::2]", true, "http://[fd00::2]:80"},
		{"fd00::3", true, "http://[fd00::3]:80"},
		{"u@a.example:1", true, "http://u@a.example:1"},
		{"a.example:", true, "DIRECT"},
		{"a.example:1:2", true, "DIRECT"},
		{"http://a.example:3128/", true, "http://a.example:3128"},
		{"socks5://s.example:1080", true, "socks5://s.example:1080"},
		{"socks=s.example:1080", true, "DIRECT"},
		{"socks4://s.example:1080", true, "DIRECT"},
		{"a.example:notaport", true, "DIRECT"},
		{"a.example:70000", true, "DIRECT"},
		{"", true, "DIRECT"},
		{" ; ", true, "DIRECT"},
		{"bad.example:x;good.example:8080", true, "http://good.example:8080"},
	}
	for _, c := range cases {
		if got := proxyString(parseProxyList(c.list, c.secure)); got != c.want {
			t.Errorf("parseProxyList(%q, secure=%v) = %s, want %s", c.list, c.secure, got, c.want)
		}
	}
}

// A manual bypass list is matched the way Internet Options documents it: <local> for a
// name without a dot, * as a wildcard, entries separated by semicolons, an optional
// scheme in front, case ignored.
func TestBypassList(t *testing.T) {
	cases := []struct {
		list string
		host string
		want bool
	}{
		{"<local>", "intranet", true},
		{"<local>", "intranet.example.ch", false},
		{"<local>", "10.0.0.1", false},
		{"<local>", "fd00::1", false},
		{"*.example.ch;10.*", "a.example.ch", true},
		{"*.example.ch;10.*", "example.ch", false},
		{"*.example.ch;10.*", "10.1.2.3", true},
		{"*.example.ch;10.*", "110.1.2.3", false},
		{"*.EXAMPLE.ch", "A.Example.CH", true},
		{"http://exact.example", "exact.example", true},
		{"https://exact.example", "exact.example", true},
		{"[fd00::1]", "fd00::1", true},
		{"exact.example", "notexact.example", false},
		{" ; garbage$ ; ;", "garbage$", true},
		{"", "anything", false},
		{"<LOCAL>", "intranet", true},
		{"host.example:8080", "host.example", false},
	}
	for _, c := range cases {
		if got := parseBypass(c.list).matches(c.host); got != c.want {
			t.Errorf("bypass %q matches %q = %v, want %v", c.list, c.host, got, c.want)
		}
	}
}

// The worker reaches its own server over loopback. Whatever Windows says — even a PAC
// script that sends everything to the proxy — that request must never leave the host.
func TestLoopbackIsNeverProxied(t *testing.T) {
	b := &fakeBackend{answer: func(string, bool, string) (string, error) { return "prx.example:8080", nil }}
	r := newResolver(b, Settings{AutoDetect: true, Proxy: "prx.example:8080"}, time.Now)
	for _, raw := range []string{"http://127.0.0.1:8080/api", "http://localhost:8080/", "http://[::1]:8080/", "https://127.0.0.2/"} {
		if got := r.proxyFor(mustURL(t, raw)); got != nil {
			t.Errorf("%s went through %s, want DIRECT", raw, got)
		}
	}
	if n := b.callCount(); n != 0 {
		t.Errorf("Windows was asked %d times about loopback, want never", n)
	}
}

// A server that listens on one of the host's own addresses rather than on loopback is
// reached by its supervised Workers there. That request does not leave the host either,
// so no PAC script gets to send it to a proxy that cannot route it back.
func TestTheHostsOwnAddressesAreNeverProxied(t *testing.T) {
	b := &fakeBackend{answer: func(string, bool, string) (string, error) { return "prx.example:8080", nil }}
	r := newResolver(b, Settings{AutoDetect: true}, time.Now)
	r.local = map[string]bool{"10.1.2.3": true, "fd00::7": true}
	for _, raw := range []string{"http://10.1.2.3:8080/api/v1/jobs/activate", "https://[fd00::7]:8443/"} {
		if got := r.proxyFor(mustURL(t, raw)); got != nil {
			t.Errorf("%s went through %s, want DIRECT", raw, got)
		}
	}
	if n := b.callCount(); n != 0 {
		t.Errorf("Windows was asked %d times about this host's own addresses, want never", n)
	}
	if got := proxyString(r.proxyFor(mustURL(t, "http://10.1.2.4:8080/"))); got != "http://prx.example:8080" {
		t.Errorf("another host's address went %s, want the PAC answer", got)
	}
}

// The host's addresses are read once, and only addresses come back.
func TestLocalAddressesAreAddresses(t *testing.T) {
	for a := range localAddresses() {
		if ip := net.ParseIP(a); ip == nil || ip.String() != a {
			t.Errorf("local address %q is not a normalised IP", a)
		}
	}
}

// When auto-configuration answers, its answer stands — DIRECT included — and the manual
// proxy is not consulted, exactly as .NET's HttpWindowsProxy does.
func TestAnAutoConfigurationAnswerIsFinal(t *testing.T) {
	answers := map[string]string{"https://graph.microsoft.com/": "prx.example:8080", "https://intranet.example/": ""}
	b := &fakeBackend{answer: func(target string, _ bool, _ string) (string, error) { return answers[target], nil }}
	r := newResolver(b, Settings{AutoConfigURL: "http://wpad.example/proxy.pac", Proxy: "manual.example:3128"}, time.Now)
	if got := proxyString(r.proxyFor(mustURL(t, "https://graph.microsoft.com/v1.0/users?$select=id"))); got != "http://prx.example:8080" {
		t.Errorf("graph went through %s, want the PAC's proxy", got)
	}
	if got := proxyString(r.proxyFor(mustURL(t, "https://intranet.example/x"))); got != "DIRECT" {
		t.Errorf("intranet went through %s, want DIRECT as the PAC said", got)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.calls {
		if strings.Contains(c.target, "users") || strings.Contains(c.target, "select") {
			t.Errorf("Windows was handed %q: the path and query stay out of proxy resolution", c.target)
		}
		if c.configURL != "http://wpad.example/proxy.pac" || c.autoDetect {
			t.Errorf("auto call %+v, want the configured PAC URL and no auto-detection", c)
		}
	}
}

// When auto-configuration cannot answer, the manual settings apply — with their bypass
// list — and with none, the request goes direct.
func TestAFailedAutoConfigurationFallsBackToTheManualSettings(t *testing.T) {
	fail := func(string, bool, string) (string, error) { return "", errors.New("PAC download failed") }
	r := newResolver(&fakeBackend{answer: fail}, Settings{AutoDetect: true, Proxy: "manual.example:3128", Bypass: "*.intern.example;<local>"}, time.Now)
	if got := proxyString(r.proxyFor(mustURL(t, "https://login.microsoftonline.com/"))); got != "http://manual.example:3128" {
		t.Errorf("got %s, want the manual proxy", got)
	}
	if got := proxyString(r.proxyFor(mustURL(t, "https://app.intern.example/"))); got != "DIRECT" {
		t.Errorf("got %s for a bypassed host, want DIRECT", got)
	}
	if got := proxyString(r.proxyFor(mustURL(t, "http://fileserver/"))); got != "DIRECT" {
		t.Errorf("got %s for a <local> name, want DIRECT", got)
	}
	none := newResolver(&fakeBackend{answer: fail}, Settings{AutoDetect: true}, time.Now)
	if got := proxyString(none.proxyFor(mustURL(t, "https://login.microsoftonline.com/"))); got != "DIRECT" {
		t.Errorf("got %s with no manual proxy, want DIRECT", got)
	}
}

// Manual settings alone need no call into Windows at all.
func TestManualSettingsAloneAreAppliedWithoutAskingWindows(t *testing.T) {
	b := &fakeBackend{}
	r := newResolver(b, Settings{Proxy: "http=plain.example:80;https=secure.example:8443"}, time.Now)
	if got := proxyString(r.proxyFor(mustURL(t, "https://graph.microsoft.com/"))); got != "http://secure.example:8443" {
		t.Errorf("https went through %s, want the https= entry", got)
	}
	if got := proxyString(r.proxyFor(mustURL(t, "http://example.org/"))); got != "http://plain.example:80" {
		t.Errorf("http went through %s, want the http= entry", got)
	}
	if n := b.callCount(); n != 0 {
		t.Errorf("Windows was asked %d times with no auto-configuration, want never", n)
	}
}

// WPAD discovery that finds nothing can take seconds, so after it fails it is not tried
// again for two minutes — .NET's rule. A PAC URL, if one is set, is still used meanwhile.
func TestAFailedAutoDetectionIsNotRetriedForTwoMinutes(t *testing.T) {
	clk := &clock{t: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	b := &fakeBackend{answer: func(_ string, autoDetect bool, configURL string) (string, error) {
		if autoDetect {
			return "", errAutoDetectFailed
		}
		if configURL != "" {
			return "pac.example:8080", nil
		}
		return "", errors.New("nothing to ask")
	}}
	r := newResolver(b, Settings{AutoDetect: true, Proxy: "manual.example:3128"}, clk.now)

	if got := proxyString(r.proxyFor(mustURL(t, "https://a.example/"))); got != "http://manual.example:3128" {
		t.Errorf("first request went through %s, want the manual proxy after detection failed", got)
	}
	if n := b.callCount(); n != 1 {
		t.Fatalf("Windows asked %d times, want 1", n)
	}
	clk.t = clk.t.Add(90 * time.Second)
	r.proxyFor(mustURL(t, "https://b.example/"))
	if n := b.callCount(); n != 1 {
		t.Errorf("auto-detection retried after 90s (%d calls), want it held back", n)
	}
	clk.t = clk.t.Add(31 * time.Second)
	r.proxyFor(mustURL(t, "https://c.example/"))
	if n := b.callCount(); n != 2 {
		t.Errorf("auto-detection not retried after two minutes (%d calls)", n)
	}

	withPAC := &fakeBackend{answer: b.answer}
	rp := newResolver(withPAC, Settings{AutoDetect: true, AutoConfigURL: "http://wpad.example/p.pac"}, clk.now)
	rp.proxyFor(mustURL(t, "https://a.example/"))
	if got := proxyString(rp.proxyFor(mustURL(t, "https://b.example/"))); got != "http://pac.example:8080" {
		t.Errorf("with detection held back, got %s, want the PAC URL still used", got)
	}
	withPAC.mu.Lock()
	last := withPAC.calls[len(withPAC.calls)-1]
	withPAC.mu.Unlock()
	if last.autoDetect || last.configURL == "" {
		t.Errorf("held-back call was %+v, want the PAC URL without auto-detection", last)
	}
}

// One answer per scheme, host and port is believed for five minutes; an answer that was
// a fallback after Windows failed only for thirty seconds, so a passing PAC outage does
// not pin a host to the wrong route.
func TestAnswersAreCachedPerHost(t *testing.T) {
	clk := &clock{t: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	healthy := true
	b := &fakeBackend{answer: func(string, bool, string) (string, error) {
		if healthy {
			return "prx.example:8080", nil
		}
		return "", errors.New("PAC unreachable")
	}}
	r := newResolver(b, Settings{AutoConfigURL: "http://wpad.example/p.pac"}, clk.now)
	r.proxyFor(mustURL(t, "https://a.example/one"))
	r.proxyFor(mustURL(t, "https://a.example/two"))
	if n := b.callCount(); n != 1 {
		t.Errorf("same host asked %d times, want 1", n)
	}
	r.proxyFor(mustURL(t, "https://a.example:8443/"))
	r.proxyFor(mustURL(t, "http://a.example/"))
	if n := b.callCount(); n != 3 {
		t.Errorf("another port and scheme asked %d times in all, want 3", n)
	}
	clk.t = clk.t.Add(5*time.Minute + time.Second)
	r.proxyFor(mustURL(t, "https://a.example/"))
	if n := b.callCount(); n != 4 {
		t.Errorf("an expired answer was not refreshed (%d calls)", n)
	}

	healthy = false
	r.proxyFor(mustURL(t, "https://b.example/"))
	clk.t = clk.t.Add(31 * time.Second)
	healthy = true
	if got := proxyString(r.proxyFor(mustURL(t, "https://b.example/"))); got != "http://prx.example:8080" {
		t.Errorf("after the fallback expired, got %s, want the PAC answer again", got)
	}
}

// The cache cannot grow with the number of hosts a process ever talks to.
func TestTheCacheIsBounded(t *testing.T) {
	b := &fakeBackend{answer: func(string, bool, string) (string, error) { return "", nil }}
	r := newResolver(b, Settings{AutoDetect: true}, time.Now)
	for i := 0; i < cacheMax+50; i++ {
		r.proxyFor(&url.URL{Scheme: "https", Host: "h" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + ".example:" + itoa(1000+i)})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cache) > cacheMax {
		t.Errorf("cache holds %d answers, want at most %d", len(r.cache), cacheMax)
	}
}

func itoa(i int) string {
	var b [20]byte
	n := len(b)
	for {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
		if i == 0 {
			return string(b[n:])
		}
	}
}

func sentinelProxy(*http.Request) (*url.URL, error) {
	return &url.URL{Scheme: "http", Host: "sentinel.example:1"}, nil
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// Install chooses once, at startup, and says what it chose. The proxy variables win, as
// they do in .NET; ATLAS_SYSTEM_PROXY=off keeps Go's own behaviour; a host with no
// settings, or a platform with no system settings to read, is left alone.
func TestInstallChoosesTheSourceOfTheProxy(t *testing.T) {
	manual := &fakeBackend{s: Settings{Proxy: "prx.example:8080"}}
	cases := []struct {
		name string
		env  map[string]string
		b    backend
		want Mode
	}{
		{"switched off", map[string]string{EnvSwitch: "off"}, manual, ModeOff},
		{"switched off, any case", map[string]string{EnvSwitch: "OFF"}, manual, ModeOff},
		{"switched off by false", map[string]string{EnvSwitch: "false"}, manual, ModeOff},
		{"switched off by 0", map[string]string{EnvSwitch: "0"}, manual, ModeOff},
		{"switched off by no", map[string]string{EnvSwitch: "no"}, manual, ModeOff},
		{"HTTPS_PROXY", map[string]string{"HTTPS_PROXY": "http://env.example:1"}, manual, ModeEnvironment},
		{"https_proxy", map[string]string{"https_proxy": "http://env.example:1"}, manual, ModeEnvironment},
		{"HTTP_PROXY", map[string]string{"HTTP_PROXY": "http://env.example:1"}, manual, ModeEnvironment},
		{"http_proxy", map[string]string{"http_proxy": "http://env.example:1"}, manual, ModeEnvironment},
		{"blank variable", map[string]string{"HTTPS_PROXY": "  "}, manual, ModeWindows},
		{"NO_PROXY alone", map[string]string{"NO_PROXY": "intern.example"}, manual, ModeWindows},
		{"switch on", map[string]string{EnvSwitch: "on"}, manual, ModeWindows},
		{"no platform", nil, nil, ModeNone},
		{"settings unreadable", nil, &fakeBackend{sErr: errors.New("no profile")}, ModeNone},
		{"no settings", nil, &fakeBackend{}, ModeNone},
		{"settings with blanks only", nil, &fakeBackend{s: Settings{Proxy: "  ", AutoConfigURL: " "}}, ModeNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := &http.Transport{Proxy: sentinelProxy}
			mode, _ := install(envOf(c.env), c.b, tr, time.Now)
			if mode != c.want {
				t.Fatalf("mode = %s, want %s", mode, c.want)
			}
			got, err := tr.Proxy(&http.Request{URL: mustURL(t, "https://graph.microsoft.com/")})
			if err != nil {
				t.Fatalf("Proxy: %v", err)
			}
			if c.want == ModeWindows {
				if proxyString(got) != "http://prx.example:8080" {
					t.Errorf("installed proxy answered %s, want the Windows setting", proxyString(got))
				}
				loop, _ := tr.Proxy(&http.Request{URL: mustURL(t, "http://127.0.0.1:8080/")})
				if loop != nil {
					t.Errorf("installed proxy sends loopback through %s", loop)
				}
				return
			}
			if proxyString(got) != "http://sentinel.example:1" {
				t.Errorf("mode %s replaced the transport's proxy; it must be left alone", mode)
			}
		})
	}
}

// What Install read is reported back, so the startup line can say which settings it
// follows — without the credentials a proxy address may carry.
func TestInstallReportsTheSettingsItFollows(t *testing.T) {
	b := &fakeBackend{s: Settings{AutoDetect: true, AutoConfigURL: " http://wpad.example/p.pac ", Proxy: " user:pw@prx.example:8080 ", Bypass: " <local> "}}
	mode, s := install(envOf(nil), b, &http.Transport{}, time.Now)
	if mode != ModeWindows {
		t.Fatalf("mode = %s", mode)
	}
	want := Settings{AutoDetect: true, AutoConfigURL: "http://wpad.example/p.pac", Proxy: "user:pw@prx.example:8080", Bypass: "<local>"}
	if s != want {
		t.Errorf("settings = %+v, want %+v", s, want)
	}
	if got := s.Redacted().Proxy; strings.Contains(got, "pw") || !strings.Contains(got, "prx.example:8080") {
		t.Errorf("Redacted proxy = %q, want the address without its credentials", got)
	}
}

func TestRedactProxyList(t *testing.T) {
	cases := map[string]string{
		"user:pw@a.example:8080;b.example:1":   "***@a.example:8080;b.example:1",
		"http=u@a.example:1;https=b.example:2": "http=***@a.example:1;https=b.example:2",
		"http://user:pw@a.example:3128":        "http://***@a.example:3128",
		"plain.example:8080":                   "plain.example:8080",
		"":                                     "",
	}
	for in, want := range cases {
		if got := redactProxyList(in); got != want {
			t.Errorf("redactProxyList(%q) = %q, want %q", in, got, want)
		}
	}
}

// A request the transport makes concurrently from many goroutines resolves safely.
func TestTheResolverIsSafeForConcurrentUse(t *testing.T) {
	b := &fakeBackend{answer: func(string, bool, string) (string, error) { return "prx.example:8080", nil }}
	r := newResolver(b, Settings{AutoDetect: true}, time.Now)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.proxyFor(&url.URL{Scheme: "https", Host: "h" + itoa(i%4) + ".example"})
		}(i)
	}
	wg.Wait()
}
