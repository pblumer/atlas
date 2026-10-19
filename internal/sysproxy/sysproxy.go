// Package sysproxy chooses the proxy an outbound HTTP request goes through the way .NET,
// and so PowerShell, chooses it on the same host (ADR-draft-windows-system-proxy).
//
// # Why
//
// Go's default transport reads only the HTTPS_PROXY family of variables. A Windows server
// is usually configured the other way: a PAC script or a proxy in Internet Options, set
// by group policy, and no variable at all. There every PowerShell a Worker starts finds
// the proxy and every Go Worker goes direct — into a firewall that drops the packets, so
// the request fails on a timeout rather than with a message. One host then has two
// network paths, and which one a task takes depends on the language its Worker is
// written in.
//
// # The choice, made once at startup
//
//  1. ATLAS_SYSTEM_PROXY=off keeps Go's own behaviour: the variables, nothing else.
//  2. A proxy variable (HTTPS_PROXY, HTTP_PROXY, either case) wins, as it does in .NET,
//     and Go's transport reads it as it always has.
//  3. Otherwise, on Windows, the account's proxy settings as WinHTTP reports them: a
//     PAC URL, automatic detection (WPAD), a manual proxy and its bypass list. That is
//     what .NET's HttpWindowsProxy reads, and the rules below are its rules.
//  4. Anywhere else, or with no settings, nothing changes.
//
// # The rules for one request
//
// Loopback and the host's own addresses never go through a proxy, whatever a PAC script
// says: a supervised Worker reaches its own server over 127.0.0.1, or over the address
// the server listens on. Auto-configuration, when set, is asked first and
// its answer is final, DIRECT included; when it cannot answer, the manual proxy applies
// with its bypass list; with neither, the request goes direct. Auto-detection that found
// nothing is not tried again for two minutes, because WPAD discovery can take seconds.
// An answer is believed for five minutes per scheme, host and port; a fallback after a
// failure for thirty seconds, so a passing PAC outage does not pin a host to the wrong
// route. Only the first proxy of a list is used: Go's transport takes one per request,
// where .NET fails over along the list.
package sysproxy

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// EnvSwitch turns the Windows settings off: off, false, 0 or no keeps Go's default.
const EnvSwitch = "ATLAS_SYSTEM_PROXY"

// Mode says where this process takes its outbound proxy from.
type Mode string

const (
	// ModeEnvironment: a proxy variable is set, and Go's transport reads it.
	ModeEnvironment Mode = "environment"
	// ModeWindows: the Windows proxy settings, read the way .NET reads them.
	ModeWindows Mode = "windows"
	// ModeOff: ATLAS_SYSTEM_PROXY switched the Windows settings off.
	ModeOff Mode = "off"
	// ModeNone: no system settings to follow — not Windows, or none configured.
	ModeNone Mode = "none"
)

// Settings are an account's proxy settings as WinHTTP reports them: the Internet Options
// an administrator or a group policy set.
type Settings struct {
	AutoDetect    bool
	AutoConfigURL string
	Proxy         string
	Bypass        string
}

func (s Settings) trimmed() Settings {
	s.AutoConfigURL = strings.TrimSpace(s.AutoConfigURL)
	s.Proxy = strings.TrimSpace(s.Proxy)
	s.Bypass = strings.TrimSpace(s.Bypass)
	return s
}

func (s Settings) empty() bool { return !s.AutoDetect && s.AutoConfigURL == "" && s.Proxy == "" }

// Redacted is s with any credentials in its addresses masked, for a log line.
func (s Settings) Redacted() Settings {
	s.AutoConfigURL = redactProxyList(s.AutoConfigURL)
	s.Proxy = redactProxyList(s.Proxy)
	return s
}

// backend is the operating system's half: reading the settings and running
// auto-configuration for one URL. Only Windows has one; it is an interface so the
// rules above are tested on every platform.
type backend interface {
	readSettings() (Settings, error)
	// autoProxy asks WinHTTP which proxy serves target, by WPAD (autoDetect) and/or the
	// PAC script at configURL. It answers WinHTTP's proxy list, "" for DIRECT.
	autoProxy(target string, autoDetect bool, configURL string) (string, error)
}

// errAutoDetectFailed is a backend's answer when WPAD found no configuration — the one
// failure that holds auto-detection back for a while.
var errAutoDetectFailed = errors.New("sysproxy: automatic proxy detection found no configuration")

// Install points http.DefaultTransport's proxy at the choice described in the package
// documentation, and reports which it made and the settings it follows. Call it once at
// startup, before anything clones the default transport.
func Install(getenv func(string) string) (Mode, Settings) {
	return install(getenv, platform(), http.DefaultTransport.(*http.Transport), time.Now)
}

func install(getenv func(string) string, b backend, tr *http.Transport, now func() time.Time) (Mode, Settings) {
	switch strings.ToLower(strings.TrimSpace(getenv(EnvSwitch))) {
	case "off", "false", "0", "no":
		return ModeOff, Settings{}
	}
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if strings.TrimSpace(getenv(name)) != "" {
			return ModeEnvironment, Settings{}
		}
	}
	if b == nil {
		return ModeNone, Settings{}
	}
	s, err := b.readSettings()
	if err != nil {
		return ModeNone, Settings{}
	}
	if s = s.trimmed(); s.empty() {
		return ModeNone, Settings{}
	}
	r := newResolver(b, s, now)
	r.local = localAddresses()
	tr.Proxy = func(req *http.Request) (*url.URL, error) { return r.proxyFor(req.URL), nil }
	return ModeWindows, s
}

// localAddresses are this host's own IP addresses, read once: a request to one of them
// does not leave the host, so it has no business with a proxy.
func localAddresses() map[string]bool {
	local := map[string]bool{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return local
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			local[n.IP.String()] = true
		}
	}
	return local
}

const (
	// cacheTTL is how long an answer is believed.
	cacheTTL = 5 * time.Minute
	// fallbackTTL is how long an answer given after Windows failed is believed.
	fallbackTTL = 30 * time.Second
	// autoDetectHoldBack is how long auto-detection rests after it found nothing.
	autoDetectHoldBack = 2 * time.Minute
	// cacheMax bounds the cache: a process talking to more hosts starts it over.
	cacheMax = 512
)

type resolver struct {
	b      backend
	s      Settings
	bypass bypassList
	local  map[string]bool
	now    func() time.Time

	mu             sync.Mutex
	cache          map[string]cachedAnswer
	detectFailedAt time.Time
}

type cachedAnswer struct {
	proxy *url.URL
	until time.Time
}

func newResolver(b backend, s Settings, now func() time.Time) *resolver {
	return &resolver{b: b, s: s, bypass: parseBypass(s.Bypass), now: now, cache: map[string]cachedAnswer{}}
}

// proxyFor answers the proxy for u, nil for DIRECT. It never fails a request: a Windows
// that cannot answer leaves the manual settings, or a direct connection.
func (r *resolver) proxyFor(u *url.URL) *url.URL {
	host := u.Hostname()
	if isLoopback(host) {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && r.local[ip.String()] {
		return nil
	}
	key := strings.ToLower(u.Scheme + "://" + u.Host)
	now := r.now()
	r.mu.Lock()
	if c, ok := r.cache[key]; ok && now.Before(c.until) {
		r.mu.Unlock()
		return c.proxy
	}
	holdBack := !r.detectFailedAt.IsZero() && now.Sub(r.detectFailedAt) < autoDetectHoldBack
	r.mu.Unlock()

	// Windows is asked without the lock: a PAC script may take a while, and two
	// goroutines asking about the same host at once cost one redundant answer.
	proxy, ttl := r.resolve(u, key+"/", holdBack)

	r.mu.Lock()
	if len(r.cache) >= cacheMax {
		r.cache = map[string]cachedAnswer{}
	}
	r.cache[key] = cachedAnswer{proxy: proxy, until: now.Add(ttl)}
	r.mu.Unlock()
	return proxy
}

// resolve applies the rules for one request. target carries the scheme, host and port
// only: a path or query is no business of a PAC script and may carry a token.
func (r *resolver) resolve(u *url.URL, target string, holdBack bool) (*url.URL, time.Duration) {
	secure := u.Scheme == "https" || u.Scheme == "wss"
	autoDetect := r.s.AutoDetect && !holdBack
	ttl := cacheTTL
	if autoDetect || r.s.AutoConfigURL != "" {
		list, err := r.b.autoProxy(target, autoDetect, r.s.AutoConfigURL)
		if err == nil {
			return parseProxyList(list, secure), cacheTTL
		}
		if errors.Is(err, errAutoDetectFailed) {
			r.mu.Lock()
			r.detectFailedAt = r.now()
			r.mu.Unlock()
		}
		ttl = fallbackTTL
	}
	if r.s.Proxy == "" || r.bypass.matches(u.Hostname()) {
		return nil, ttl
	}
	return parseProxyList(r.s.Proxy, secure), ttl
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// parseProxyList picks the proxy for one scheme out of a Windows proxy string: one
// address for every protocol ("host:port"), or a per-protocol list
// ("http=host:port;https=host:port"), entries separated by semicolons or blanks. A
// protocol's own entry wins over a plain one; an entry for another protocol, or one that
// does not parse, is skipped. nil means DIRECT.
func parseProxyList(list string, secure bool) *url.URL {
	want := "http"
	if secure {
		want = "https"
	}
	var plain, own *url.URL
	for _, entry := range strings.FieldsFunc(list, func(r rune) bool { return r == ';' || r == ' ' || r == '\t' }) {
		if i := strings.IndexByte(entry, '='); i > 0 {
			if own == nil && strings.EqualFold(entry[:i], want) {
				own = parseProxyAddress(entry[i+1:])
			}
			continue
		}
		if plain == nil {
			plain = parseProxyAddress(entry)
		}
	}
	if own != nil {
		return own
	}
	return plain
}

// parseProxyAddress reads "[scheme://][user[:password]@]host[:port]". Without a scheme a
// proxy speaks HTTP; without a port it listens on its scheme's default. SOCKS4 is not
// something Go's transport can use, so it is skipped rather than misread.
func parseProxyAddress(addr string) *url.URL {
	scheme, port := "http", "80"
	lower := strings.ToLower(addr)
	switch {
	case strings.HasPrefix(lower, "http://"):
		addr = addr[len("http://"):]
	case strings.HasPrefix(lower, "https://"):
		scheme, port, addr = "https", "443", addr[len("https://"):]
	case strings.HasPrefix(lower, "socks5://"):
		scheme, port, addr = "socks5", "1080", addr[len("socks5://"):]
	case strings.Contains(lower, "://"):
		return nil
	}
	addr = strings.TrimRight(addr, "/")
	var user *url.Userinfo
	if at := strings.LastIndexByte(addr, '@'); at >= 0 {
		name, pw, hasPW := strings.Cut(addr[:at], ":")
		if hasPW {
			user = url.UserPassword(name, pw)
		} else {
			user = url.User(name)
		}
		addr = addr[at+1:]
	}
	host := addr
	if h, p, err := net.SplitHostPort(addr); err == nil {
		host, port = h, p
	} else if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		host = addr[1 : len(addr)-1]
	} else if strings.Contains(addr, ":") && net.ParseIP(addr) == nil {
		return nil // a port that does not parse
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || host == "" {
		return nil
	}
	return &url.URL{Scheme: scheme, User: user, Host: net.JoinHostPort(host, port)}
}

// bypassList is a manual bypass list: Internet Options' "Do not use proxy server for
// addresses beginning with", and its "<local>" for names without a dot.
type bypassList struct {
	local    bool
	patterns []*regexp.Regexp
}

// parseBypass reads the list the way .NET does: entries separated by semicolons, an
// optional http:// or https:// in front, IPv6 brackets dropped, * a wildcard, case
// ignored. An entry with a port never matches, since a host is compared without one.
func parseBypass(list string) bypassList {
	var b bypassList
	for _, entry := range strings.Split(list, ";") {
		entry = strings.TrimSpace(entry)
		lower := strings.ToLower(entry)
		switch {
		case strings.HasPrefix(lower, "http://"):
			entry = entry[len("http://"):]
		case strings.HasPrefix(lower, "https://"):
			entry = entry[len("https://"):]
		}
		entry = strings.TrimSuffix(strings.TrimPrefix(entry, "["), "]")
		if entry == "" {
			continue
		}
		if strings.EqualFold(entry, "<local>") {
			b.local = true
			continue
		}
		pattern := "(?i)^" + strings.ReplaceAll(regexp.QuoteMeta(entry), `\*`, ".*") + "$"
		b.patterns = append(b.patterns, regexp.MustCompile(pattern))
	}
	return b
}

func (b bypassList) matches(host string) bool {
	if b.local && net.ParseIP(host) == nil && !strings.Contains(host, ".") {
		return true
	}
	for _, p := range b.patterns {
		if p.MatchString(host) {
			return true
		}
	}
	return false
}

// userinfo is the credential part of a proxy address, wherever an entry starts.
var userinfo = regexp.MustCompile(`(^|[=;\s]|//)[^\s;=/@]+@`)

// redactProxyList masks the credentials a proxy address may carry.
func redactProxyList(list string) string {
	return userinfo.ReplaceAllString(list, "${1}***@")
}
