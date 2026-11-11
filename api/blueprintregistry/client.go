// Public registry transport is deliberately isolated from the atlas run loop.
// It retrieves signed metadata and exact package bytes, never installs, unpacks,
// deploys or trusts any artifact merely because its signature is valid.
package blueprintregistry

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"
)

const maxSignatureBytes = 88 // RFC 4648 base64 encoding of an Ed25519 signature

// PublicClient retrieves a preconfigured public HTTPS registry using one or,
// during a key rotation, two pinned Ed25519 keys. Configuration must be
// restricted to an administrator before this client is exposed through any API
// or Console control.
type PublicClient struct {
	indexURL   *url.URL
	publicKeys []ed25519.PublicKey
	httpClient *http.Client
	// lookup and dial are the resolver and the TCP dial every connection goes
	// through, after dialPublic has vetted the answer. Tests replace them to reach
	// a local server under a public name; nothing else does.
	lookup publicLookup
	dial   publicDial
}

// NewPublicClient validates a static registry URL and the trusted public keys:
// at least one, at most MaxPinnedKeys, each 32 bytes and none twice. It never
// accepts an unauthenticated publisher key from the registry index.
func NewPublicClient(indexURL string, publicKeys ...ed25519.PublicKey) (*PublicClient, error) {
	u, err := parsePublicIndexURL(indexURL)
	if err != nil {
		return nil, err
	}
	if len(publicKeys) == 0 || len(publicKeys) > MaxPinnedKeys {
		return nil, fmt.Errorf("registry requires 1..%d pinned Ed25519 public keys", MaxPinnedKeys)
	}
	keys := make([]ed25519.PublicKey, 0, len(publicKeys))
	for _, key := range publicKeys {
		if len(key) != ed25519.PublicKeySize {
			return nil, errors.New("registry requires a pinned 32-byte Ed25519 public key")
		}
		for _, other := range keys {
			if string(other) == string(key) {
				return nil, errors.New("registry key is pinned twice")
			}
		}
		keys = append(keys, append(ed25519.PublicKey(nil), key...))
	}
	transport := &http.Transport{
		Proxy:               nil,  // never forward public registry requests via ambient proxy
		DisableCompression:  true, // bound exact response bytes, not auto-decompressed content
		DisableKeepAlives:   true, // force DNS/IP validation for each outbound connection
		MaxConnsPerHost:     2,
		MaxIdleConnsPerHost: 0,
	}
	dialer := &net.Dialer{Timeout: dialTimeout}
	c := &PublicClient{
		indexURL:   u,
		publicKeys: keys,
		lookup:     net.DefaultResolver.LookupIPAddr,
		dial:       dialer.DialContext,
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialPublic(ctx, network, address, c.lookup, c.dial)
	}
	c.httpClient = &http.Client{
		Transport: transport,
		Timeout:   25 * time.Second,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			// Where it was sent is the clue (a captive portal, a proxy's block
			// page), so a traced load names the host; the redirect is refused alike.
			tracerFrom(req.Context()).redirected(req.URL.Host)
			return errors.New("registry redirects are disabled")
		},
	}
	return c, nil
}

// dialTimeout bounds one TCP connection attempt to one vetted address.
const dialTimeout = 5 * time.Second

// LoadIndex fetches signed metadata and refuses to expose any release until
// the Ed25519 signature and the strict manifest checks both succeed. A load that
// fails at a station returns a *StopError naming it (Stopped), as a traced load
// would have reported it last.
func (c *PublicClient) LoadIndex(ctx context.Context) (Index, error) {
	tr := newTracer(nil)
	defer tr.close()
	index, _, err := c.loadIndex(ctx, tr)
	return index, err
}

// LoadIndexTraced is LoadIndex reporting each station of the way as it is passed
// or where it stops (Step): the name resolved, the connection made, the TLS
// handshake, the index read, its signature verified. It is the same load — the
// same transport, the same checks, the same refusals — not a separate probe, so
// what it reports is what LoadIndex meets. report may be called from the
// transport's own goroutines, never after LoadIndexTraced has returned.
func (c *PublicClient) LoadIndexTraced(ctx context.Context, report func(Step)) (Index, error) {
	tr := newTracer(report)
	defer tr.close()
	index, _, err := c.loadIndex(ctx, tr)
	return index, err
}

// loadIndex answers the verified index and the pinned key whose signature it carries.
func (c *PublicClient) loadIndex(ctx context.Context, tr *tracer) (_ Index, _ ed25519.PublicKey, err error) {
	if c == nil || c.httpClient == nil || c.indexURL == nil {
		return Index{}, nil, errors.New("registry client is not configured")
	}
	defer func() { err = tr.wrap(err) }()
	// Only the index request is traced station by station; the signature request
	// takes the same way again and is reported as one station of its own.
	raw, err := c.readBounded(withTracer(ctx, tr), c.indexURL, MaxIndexBytes)
	if err != nil {
		tr.failRequest(StageIndex, err)
		return Index{}, nil, fmt.Errorf("load registry index: %w", err)
	}
	tr.passIndex(len(raw))
	signatureURL := *c.indexURL
	signatureURL.Path += ".sig"
	signature, err := c.readBounded(ctx, &signatureURL, maxSignatureBytes)
	if err != nil {
		tr.fail(Step{Stage: StageSignature, Cause: CauseSignatureUnavailable, Status: httpStatusOf(err), Err: err})
		return Index{}, nil, fmt.Errorf("load registry signature: %w", err)
	}
	index, key, err := VerifySignedIndexWithAny(raw, signature, c.publicKeys)
	if err != nil {
		cause := CauseIndexInvalid
		if errors.Is(err, ErrSignatureMismatch) {
			cause = CauseNoPinnedKey
		}
		tr.fail(Step{Stage: StageSignature, Cause: cause, Err: err})
		return Index{}, nil, fmt.Errorf("verify registry: %w", err)
	}
	tr.pass(Step{Stage: StageSignature, Key: KeyFingerprint(key)})
	return index, key, nil
}

// Fingerprints names the pinned keys, in the order the operator gave them.
func (c *PublicClient) Fingerprints() []string {
	out := make([]string, 0, len(c.publicKeys))
	for _, key := range c.publicKeys {
		out = append(out, KeyFingerprint(key))
	}
	return out
}

// Download verifies the current signed index before resolving a listed,
// version-pinned artifact relative to the configured registry base directory.
// It verifies exact compressed bytes, but DOES NOT validate archive members,
// import data, publish a process or make the payload executable.
// Download returns a release's pinned bytes. Call Fetch when the caller also
// needs provenance from the same verified index snapshot.
func (c *PublicClient) Download(ctx context.Context, blueprintID, version string) ([]byte, error) {
	_, content, err := c.Fetch(ctx, blueprintID, version)
	return content, err
}

// Fetch obtains a release descriptor and archive bytes from one verified
// signed-index snapshot. The descriptor always matches the downloaded bytes,
// even if the registry index changes between separate HTTP requests. A fetch
// that fails at a station — of the index load, or the package's own
// (StagePackage) — returns a *StopError naming it.
func (c *PublicClient) Fetch(ctx context.Context, blueprintID, version string) (Release, []byte, error) {
	release, _, data, err := c.FetchOrigin(ctx, blueprintID, version)
	return release, data, err
}

// Origin is where a fetched release came from: the registry the signed index names,
// the index URL the operator configured, and the fingerprint (KeyFingerprint) of the
// pinned key whose signature verified that index. During a key rotation two keys are
// pinned and SignedBy is the one that signed, so an installation records which key
// vouched for its package (ADR-0499).
type Origin struct {
	RegistryID string `json:"registryId"`
	IndexURL   string `json:"indexUrl"`
	SignedBy   string `json:"signedBy"`
}

// FetchOrigin is Fetch answering also the Origin of the index snapshot the release
// and its bytes were checked against. The release, the bytes and the origin always
// come from that one snapshot.
func (c *PublicClient) FetchOrigin(ctx context.Context, blueprintID, version string) (Release, Origin, []byte, error) {
	indexTrace := newTracer(nil)
	defer indexTrace.close()
	index, key, err := c.loadIndex(ctx, indexTrace)
	if err != nil {
		return Release{}, Origin{}, nil, err
	}
	origin := Origin{RegistryID: index.RegistryID, IndexURL: c.indexURL.String(), SignedBy: KeyFingerprint(key)}
	for _, release := range index.Releases {
		if release.BlueprintID != blueprintID || release.Version != version {
			continue
		}
		location := *c.indexURL
		location.Path = path.Join(path.Dir(location.Path), release.Artifact.Path)
		if err := validateDerivedArtifactURL(c.indexURL, &location); err != nil {
			return Release{}, Origin{}, nil, err
		}
		// The download takes the way again, so it may stop at a network station
		// as well as at the package's own.
		tr := newTracer(nil)
		defer tr.close()
		data, err := c.readBounded(withTracer(ctx, tr), &location, release.Artifact.SizeBytes)
		if err != nil {
			tr.failRequest(StagePackage, err)
			return Release{}, Origin{}, nil, tr.wrap(fmt.Errorf("download signed registry release: %w", err))
		}
		if err := VerifyArchive(release, data); err != nil {
			tr.fail(Step{Stage: StagePackage, Cause: CausePackageMismatch, Err: err})
			return Release{}, Origin{}, nil, tr.wrap(fmt.Errorf("verify signed registry release: %w", err))
		}
		return release, origin, data, nil
	}
	return Release{}, Origin{}, nil, fmt.Errorf("Blueprint %q release %q not listed in verified registry", blueprintID, version)
}

func (c *PublicClient) readBounded(ctx context.Context, location *url.URL, size int64) ([]byte, error) {
	if size < 1 || size > MaxPackageBytes {
		return nil, errors.New("invalid registry response size limit")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("construct registry request: %w", err)
	}
	tr := tracerFrom(ctx)
	if tr != nil {
		req = req.WithContext(httptrace.WithClientTrace(ctx, tr.clientTrace()))
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry HTTPS request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError{resp.StatusCode}
	}
	if resp.ContentLength > size {
		return nil, errors.New("registry response exceeds configured byte limit")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, size+1))
	if err != nil {
		return nil, fmt.Errorf("read registry response: %w", err)
	}
	if int64(len(data)) > size {
		return nil, errors.New("registry response exceeds configured byte limit")
	}
	return data, nil
}

func parsePublicIndexURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid registry URL: %w", err)
	}
	if u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return nil, errors.New("registry URL must be plain HTTPS without credentials, query or fragment")
	}
	if port := u.Port(); port != "" && port != "443" {
		return nil, errors.New("public registry must use HTTPS port 443")
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil || !validDNSHostname(host) {
		return nil, errors.New("public registry requires a valid DNS hostname, not an IP address")
	}
	if !strings.HasSuffix(u.Path, "/index.json") && u.Path != "/index.json" {
		return nil, errors.New("registry URL must end in index.json")
	}
	if u.Path != path.Clean(u.Path) || u.EscapedPath() != u.Path || strings.Contains(raw, "%") {
		return nil, errors.New("registry URL contains unsafe path or escapes")
	}
	return u, nil
}

func validDNSHostname(host string) bool {
	if host == "" || len(host) > 253 || host[len(host)-1] == '.' {
		return false
	}
	if !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || !isAlphaNumeric(label[0]) || !isAlphaNumeric(label[len(label)-1]) {
			return false
		}
		for i := 1; i < len(label)-1; i++ {
			c := label[i]
			if !isAlphaNumeric(c) && c != '-' {
				return false
			}
		}
	}
	return true
}

func isAlphaNumeric(c byte) bool {
	return isASCIILetterOrDigit(c)
}

func validateDerivedArtifactURL(indexURL, artifact *url.URL) error {
	if artifact.Scheme != "https" || artifact.Host != indexURL.Host || artifact.User != nil ||
		artifact.RawQuery != "" || artifact.Fragment != "" || artifact.RawPath != "" ||
		!strings.HasPrefix(artifact.Path, path.Dir(indexURL.Path)+"/") {
		return errors.New("registry artifact path escaped the pinned HTTPS origin and base directory")
	}
	return nil
}

type publicLookup func(context.Context, string) ([]net.IPAddr, error)
type publicDial func(context.Context, string, string) (net.Conn, error)

// dialPublic resolves each request, refuses any mixed private/reserved DNS
// answers, and dials the already-validated numeric address. This prevents a
// second DNS lookup from rebinding the connection to an internal endpoint.
func dialPublic(ctx context.Context, network, address string, lookup publicLookup, dial publicDial) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" || !validDNSHostname(host) {
		return nil, errors.New("registry dial target must be a public DNS host on port 443")
	}
	tr := tracerFrom(ctx)
	ips, err := lookup(ctx, host)
	if err != nil {
		tr.fail(Step{Stage: StageResolve, Cause: causeOfLookup(err), Host: host, Err: err})
		return nil, fmt.Errorf("registry DNS lookup: %w", err)
	}
	answer := make([]string, 0, len(ips))
	for _, candidate := range ips {
		answer = append(answer, candidate.String())
	}
	if len(ips) == 0 || len(ips) > 16 {
		err := errors.New("registry DNS answer is empty or oversized")
		tr.fail(Step{Stage: StageResolve, Cause: CauseDNSEmpty, Host: host, Addresses: answer, Err: err})
		return nil, err
	}
	// Validate the entire answer, not just the address ultimately selected.
	addrs := make([]netip.Addr, 0, len(ips))
	for _, candidate := range ips {
		addr, valid := netip.AddrFromSlice(candidate.IP)
		if !valid || candidate.Zone != "" || !publicAddress(addr) {
			err := errors.New("registry DNS answer contains nonpublic or invalid address")
			tr.fail(Step{Stage: StageResolve, Cause: CauseNonpublicAddress, Host: host, Addresses: answer, Err: err})
			return nil, err
		}
		addrs = append(addrs, addr.Unmap())
	}
	tr.pass(Step{Stage: StageResolve, Host: host, Addresses: answer})
	var (
		lastErr  error
		lastAddr string
	)
	for _, addr := range addrs {
		// If a connection attempt fails, try only another vetted address.
		target := net.JoinHostPort(addr.String(), port)
		conn, err := dial(ctx, network, target)
		if err == nil {
			tr.pass(Step{Stage: StageConnect, Address: target})
			return conn, nil
		}
		lastErr, lastAddr = err, target
	}
	tr.fail(Step{Stage: StageConnect, Cause: causeOfDial(lastErr), Address: lastAddr, Tried: len(addrs), Err: lastErr})
	return nil, fmt.Errorf("registry HTTPS dial failed: %w", lastErr)
}

var blockedPublicIPv4 = [...]netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
}
var blockedPublicIPv6 = [...]netip.Prefix{
	netip.MustParsePrefix("2001:db8::/32"),
	// Special-purpose protocol assignments.
	netip.MustParsePrefix("2001::/32"),
	// NAT64 and 6to4 ranges can tunnel internal IPv4 destinations.
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"),
}

func publicAddress(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	if ip.Is4() {
		for _, prefix := range blockedPublicIPv4 {
			if prefix.Contains(ip) {
				return false
			}
		}
	} else if ip.Is6() {
		for _, prefix := range blockedPublicIPv6 {
			if prefix.Contains(ip) {
				return false
			}
		}
	}
	return true
}

// Host is the registry's host name, as the operator configured it.
func (c *PublicClient) Host() string {
	if c == nil || c.indexURL == nil {
		return ""
	}
	return c.indexURL.Hostname()
}
