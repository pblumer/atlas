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
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"
)

const maxSignatureBytes = 88 // RFC 4648 base64 encoding of an Ed25519 signature

// PublicClient retrieves a preconfigured public HTTPS registry using a pinned
// Ed25519 key. Configuration must be restricted to an administrator before
// this client is exposed through any API or Console control.
type PublicClient struct {
	indexURL   *url.URL
	publicKey  ed25519.PublicKey
	httpClient *http.Client
}

// NewPublicClient validates a static registry URL and a trusted public key.
// It never accepts an unauthenticated publisher key from the registry index.
func NewPublicClient(indexURL string, publicKey ed25519.PublicKey) (*PublicClient, error) {
	u, err := parsePublicIndexURL(indexURL)
	if err != nil {
		return nil, err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("registry requires a pinned 32-byte Ed25519 public key")
	}
	transport := &http.Transport{
		Proxy:               nil,  // never forward public registry requests via ambient proxy
		DisableCompression:  true, // bound exact response bytes, not auto-decompressed content
		DisableKeepAlives:   true, // force DNS/IP validation for each outbound connection
		MaxConnsPerHost:     2,
		MaxIdleConnsPerHost: 0,
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialPublic(ctx, network, address, net.DefaultResolver.LookupIPAddr, dialer.DialContext)
	}
	return &PublicClient{
		indexURL:  u,
		publicKey: append(ed25519.PublicKey(nil), publicKey...),
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   25 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("registry redirects are disabled")
			},
		},
	}, nil
}

// LoadIndex fetches signed metadata and refuses to expose any release until
// the Ed25519 signature and the strict manifest checks both succeed.
func (c *PublicClient) LoadIndex(ctx context.Context) (Index, error) {
	if c == nil || c.httpClient == nil || c.indexURL == nil {
		return Index{}, errors.New("registry client is not configured")
	}
	raw, err := c.readBounded(ctx, c.indexURL, MaxIndexBytes)
	if err != nil {
		return Index{}, fmt.Errorf("load registry index: %w", err)
	}
	signatureURL := *c.indexURL
	signatureURL.Path += ".sig"
	signature, err := c.readBounded(ctx, &signatureURL, maxSignatureBytes)
	if err != nil {
		return Index{}, fmt.Errorf("load registry signature: %w", err)
	}
	index, err := VerifySignedIndex(raw, signature, c.publicKey)
	if err != nil {
		return Index{}, fmt.Errorf("verify registry: %w", err)
	}
	return index, nil
}

// Download verifies the current signed index before resolving a listed,
// version-pinned artifact relative to the configured registry base directory.
// It verifies exact compressed bytes, but DOES NOT validate archive members,
// import data, publish a process or make the payload executable.
func (c *PublicClient) Download(ctx context.Context, blueprintID, version string) ([]byte, error) {
	index, err := c.LoadIndex(ctx)
	if err != nil {
		return nil, err
	}
	for _, release := range index.Releases {
		if release.BlueprintID != blueprintID || release.Version != version {
			continue
		}
		location := *c.indexURL
		location.Path = path.Join(path.Dir(location.Path), release.Artifact.Path)
		// The signed manifest forbids absolute, escaped and traversal paths.
		// Independently check the derived location before performing I/O.
		if err := validateDerivedArtifactURL(c.indexURL, &location); err != nil {
			return nil, err
		}
		data, err := c.readBounded(ctx, &location, release.Artifact.SizeBytes)
		if err != nil {
			return nil, fmt.Errorf("download signed registry release: %w", err)
		}
		if err := VerifyArchive(release, data); err != nil {
			return nil, fmt.Errorf("verify signed registry release: %w", err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("Blueprint %q release %q not listed in verified registry", blueprintID, version)
}

func (c *PublicClient) readBounded(ctx context.Context, location *url.URL, size int64) ([]byte, error) {
	if size < 1 || size > MaxPackageBytes {
		return nil, errors.New("invalid registry response size limit")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("construct registry request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry HTTPS request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned HTTP %d", resp.StatusCode)
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
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("registry DNS lookup: %w", err)
	}
	if len(ips) == 0 || len(ips) > 16 {
		return nil, errors.New("registry DNS answer is empty or oversized")
	}
	// Validate the entire answer, not just the address ultimately selected.
	addrs := make([]netip.Addr, 0, len(ips))
	for _, candidate := range ips {
		addr, valid := netip.AddrFromSlice(candidate.IP)
		if !valid || candidate.Zone != "" || !publicAddress(addr) {
			return nil, errors.New("registry DNS answer contains nonpublic or invalid address")
		}
		addrs = append(addrs, addr.Unmap())
	}
	var lastErr error
	for _, addr := range addrs {
		// If a connection attempt fails, try only another vetted address.
		conn, err := dial(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
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
