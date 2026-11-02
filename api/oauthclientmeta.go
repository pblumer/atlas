package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client ID metadata documents (ADR-0512 §1).
//
// A connected system names itself by an address of its own: its client_id is the
// https URL of a JSON document it publishes, which says what it is called, where it
// may be sent back to, and the public keys it signs its token requests with
// (oauthassertion.go). Nobody enters it here and it never registers: an
// authorization request that names such a client_id makes Atlas read the document,
// and an administrator's approval is the first thing that makes Atlas remember it.
//
// That puts an outbound request behind an unauthenticated route — anybody can send
// an authorization request naming any URL — which is the one new kind of reach this
// record accepts, and why every part of the read is bounded:
//
//   - **Only https, or http to this machine.** The loopback carve-out is the one
//     redirect URIs have (validRedirectURIs), for a system tried out beside Atlas.
//   - **Only public addresses**, unless an operator allowed private ones
//     (WithClientMetadataPrivateAddresses). The check is made on the address dialled,
//     after the name resolved, so a name that resolves to an internal host is refused
//     however it is spelt — and no proxy is used, because the address a proxy dials is
//     not one this process can judge.
//   - **No redirect.** A document is at its own URL; one that sends Atlas elsewhere is
//     asking it to fetch something nobody named.
//   - **A size, a time and a throttle per host,** as budgets (limits.ClientMetadata,
//     ClientMetadataSeconds, ClientMetadataHostBurst/Rate).
//   - **Nothing durable.** A document read for a request is held in this cache and
//     nowhere else; the client record is saved in the run-loop turn that saves an
//     approved grant (issueTokens), never earlier. A stranger can make Atlas read a
//     document; they cannot make it remember anything.
//
// The fields and rules follow the OAuth Client ID Metadata Document draft as it stood
// in 2026-10 (ADR-0512's first open question), narrowed to what Atlas implements: a
// key set inline as `jwks` — `jwks_uri` is not read — and `private_key_jwt` as the only
// way such a client authenticates, so no shared secret exists for it at all.

const (
	// clientMetadataMaxAge is the longest a document is kept before it is read again.
	// A shorter max-age in the document's own Cache-Control wins; a key is rotated by
	// changing the document, and an hour is how long a removed key can still be used.
	clientMetadataMaxAge = time.Hour

	// maxMetadataClientID bounds how long a client_id URL may be. The client store
	// names a record's file by the hex of its id, and a file name has 255 bytes: this
	// keeps the name inside them with room for the extension.
	maxMetadataClientID = 120

	// privateKeyJWT is the one token endpoint authentication method a client ID
	// metadata document may name (RFC 7523 §2.2).
	privateKeyJWT = "private_key_jwt"
)

// WithClientMetadataPrivateAddresses lets Atlas read a client ID metadata document
// from a private, loopback or link-local address (ATLAS_CLIENT_METADATA_ALLOW_PRIVATE).
//
// Off by default, because the read is caused by an unauthenticated request: with it on,
// anybody who can send this server an authorization request can make it send a GET to a
// host inside its network and learn whether the answer was a valid document. It is for
// a connected system that runs beside Atlas inside one network, and for tests.
func WithClientMetadataPrivateAddresses() Option {
	return func(s *Server) { s.clientMetadataPrivate = true }
}

// isMetadataClientID reports whether a client_id names a metadata document rather than
// a client entered here. The ids this server issues are hex, so an absolute http(s)
// URL is never one of them.
func isMetadataClientID(id string) bool {
	return strings.HasPrefix(id, "https://") || strings.HasPrefix(id, "http://")
}

// metadataClientURL checks a client_id URL before anything is fetched from it.
func metadataClientURL(id string) (*url.URL, error) {
	if len(id) > maxMetadataClientID {
		return nil, fmt.Errorf("the client_id is longer than %d characters", maxMetadataClientID)
	}
	u, err := url.Parse(id)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return nil, errors.New("the client_id is not an absolute URL")
	}
	if u.User != nil || u.Fragment != "" || strings.Contains(id, "#") {
		return nil, errors.New("the client_id must not carry credentials or a fragment")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil, errors.New("the client_id must be an https URL (or http on this machine)")
	}
	return u, nil
}

// clientKey is one public key a connected system signs its client assertions with.
// Exactly one of ed and ec is set, by alg.
type clientKey struct {
	kid string
	alg string // "EdDSA" or "ES256"
	ed  ed25519.PublicKey
	ec  *ecdsa.PublicKey
}

// clientMetadataDocument is the wire shape of the document, with the fields Atlas
// reads. Anything else in it is ignored: the document is an open set, as RFC 7591's
// registration request is.
type clientMetadataDocument struct {
	ClientID                string          `json:"client_id"`
	ClientName              string          `json:"client_name"`
	RedirectURIs            []string        `json:"redirect_uris"`
	GrantTypes              []string        `json:"grant_types"`
	ResponseTypes           []string        `json:"response_types"`
	TokenEndpointAuthMethod string          `json:"token_endpoint_auth_method"`
	JWKS                    *rawClientJWKS  `json:"jwks"`
	JWKSURI                 string          `json:"jwks_uri"`
	ClientSecret            json.RawMessage `json:"client_secret"`
}

type rawClientJWKS struct {
	Keys []struct {
		Kty string `json:"kty"`
		Crv string `json:"crv"`
		X   string `json:"x"`
		Y   string `json:"y"`
		Kid string `json:"kid"`
		Use string `json:"use"`
		Alg string `json:"alg"`
	} `json:"keys"`
}

// clientMetadata is a document that was read and passed every check: the client it
// describes, its keys, and until when it may be used without reading it again.
type clientMetadata struct {
	client  oauthClient
	keys    []clientKey
	expires time.Time
}

// clientMetadataFetcher reads and caches documents. It is touched from handler
// goroutines and holds no durable state, so it guards itself, as oauthCodeStore does.
type clientMetadataFetcher struct {
	mu    sync.Mutex
	cache map[string]clientMetadata

	hosts    *rateLimiter
	http     *http.Client
	maxBytes int64
	now      func() time.Time
}

// newClientMetadataFetcher builds the fetcher with this installation's budgets.
func (s *Server) newClientMetadataFetcher() *clientMetadataFetcher {
	b := s.budgets()
	allowPrivate := s.clientMetadataPrivate
	dialer := &net.Dialer{Timeout: time.Duration(b.ClientMetadataSeconds) * time.Second}
	transport := &http.Transport{
		// No proxy: the address check below has to judge the address that is dialled,
		// and through a proxy that is the proxy's.
		Proxy:               nil,
		DisableKeepAlives:   true,
		DisableCompression:  true,
		MaxIdleConnsPerHost: -1,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialMetadataHost(ctx, network, address, allowPrivate, net.DefaultResolver.LookupIPAddr, dialer.DialContext)
		},
	}
	return &clientMetadataFetcher{
		cache: map[string]clientMetadata{},
		hosts: newRateLimiter(float64(b.ClientMetadataHostBurst), float64(b.ClientMetadataHostRate)),
		http: &http.Client{
			Transport: transport,
			Timeout:   time.Duration(b.ClientMetadataSeconds) * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("the client's metadata document redirects, and a document is read at its own address only")
			},
		},
		maxBytes: b.ClientMetadata,
		now:      time.Now,
	}
}

// dialMetadataHost resolves a host and dials it only at an address it may reach: a
// public one, or any where private addresses are allowed. Every address the name
// resolves to is checked, not only the one dialled, so a name that answers with one
// public and one internal address is refused rather than tried until it lands inside.
func dialMetadataHost(ctx context.Context, network, address string, allowPrivate bool,
	lookup func(context.Context, string) ([]net.IPAddr, error),
	dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%s resolves to no address", host)
	}
	addrs := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok {
			return nil, fmt.Errorf("%s resolves to an address that cannot be read", host)
		}
		addr = addr.Unmap()
		if !allowPrivate && !publicAddress(addr) {
			return nil, fmt.Errorf("%s resolves to %s, which is not a public address; an operator allows "+
				"private ones with ATLAS_CLIENT_METADATA_ALLOW_PRIVATE", host, addr)
		}
		addrs = append(addrs, addr)
	}
	var lastErr error
	for _, addr := range addrs {
		conn, err := dial(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// publicAddress reports whether an address is one on the public internet: not
// private, loopback, link-local, unspecified or multicast, and in none of the ranges
// reserved for documentation, carrier NAT, benchmarking or tunnelling an internal
// address. It is the rule the blueprint registry's client dials by
// (api/blueprintregistry), for the same reason.
func publicAddress(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublicPrefixes = [...]netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"),
}

// get returns the document a client_id names: from the cache while it is fresh and
// fresh was not asked for, read again otherwise. An error says, in words a person on
// the consent screen can pass on, why the document cannot be used.
func (f *clientMetadataFetcher) get(ctx context.Context, clientID string, fresh bool) (clientMetadata, error) {
	u, err := metadataClientURL(clientID)
	if err != nil {
		return clientMetadata{}, err
	}
	now := f.now()
	if !fresh {
		f.mu.Lock()
		md, ok := f.cache[clientID]
		f.mu.Unlock()
		if ok && now.Before(md.expires) {
			return md, nil
		}
	}
	if !f.hosts.allow(u.Host) {
		return clientMetadata{}, errors.New("the client's metadata document was read too often in a short time; try again shortly")
	}
	md, maxAge, err := f.read(ctx, u)
	if err != nil {
		f.forget(clientID)
		return clientMetadata{}, err
	}
	md.expires = now.Add(maxAge)
	f.mu.Lock()
	// Bounded like the rate limiter's buckets: a flood of distinct URLs empties the
	// cache rather than growing it, which only ever costs a re-read.
	if len(f.cache) > 1000 {
		f.cache = map[string]clientMetadata{}
	}
	if maxAge > 0 {
		f.cache[clientID] = md
	} else {
		delete(f.cache, clientID)
	}
	f.mu.Unlock()
	return md, nil
}

// forget drops a cached document, so the next use reads it again.
func (f *clientMetadataFetcher) forget(clientID string) {
	f.mu.Lock()
	delete(f.cache, clientID)
	f.mu.Unlock()
}

// read fetches and checks one document, and says how long it may be kept.
func (f *clientMetadataFetcher) read(ctx context.Context, u *url.URL) (clientMetadata, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return clientMetadata{}, 0, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		return clientMetadata{}, 0, fmt.Errorf("the client's metadata document could not be read: %w", unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return clientMetadata{}, 0, fmt.Errorf("the client's metadata document answered %d, not 200", resp.StatusCode)
	}
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil ||
		(mt != "application/json" && !strings.HasSuffix(mt, "+json")) {
		return clientMetadata{}, 0, errors.New("the client's metadata document is not served as JSON")
	}
	// One byte past the budget, so a document that is too large is named as such
	// rather than cut off and then reported as malformed.
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return clientMetadata{}, 0, fmt.Errorf("the client's metadata document could not be read: %w", err)
	}
	if int64(len(body)) > f.maxBytes {
		return clientMetadata{}, 0, fmt.Errorf("the client's metadata document is larger than %d bytes", f.maxBytes)
	}
	md, err := parseClientMetadata(u, body)
	if err != nil {
		return clientMetadata{}, 0, err
	}
	return md, documentMaxAge(resp.Header.Get("Cache-Control")), nil
}

// unwrapURLError drops the "Get <url>:" prefix net/http puts on a failure, because the
// person reading it was just shown the URL and the cause is what they need.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// documentMaxAge is how long a document may be kept: its own max-age when that is
// shorter than an hour, nothing when it asks not to be stored, and an hour otherwise.
func documentMaxAge(cacheControl string) time.Duration {
	maxAge := clientMetadataMaxAge
	for _, directive := range strings.Split(cacheControl, ",") {
		directive = strings.ToLower(strings.TrimSpace(directive))
		switch {
		case directive == "no-store" || directive == "no-cache":
			return 0
		case strings.HasPrefix(directive, "max-age="):
			if secs, err := strconv.Atoi(strings.TrimPrefix(directive, "max-age=")); err == nil && secs >= 0 {
				if d := time.Duration(secs) * time.Second; d < maxAge {
					maxAge = d
				}
			}
		}
	}
	return maxAge
}

// parseClientMetadata checks a document against the URL it was read from and turns it
// into the client it describes. Each refusal names the field, because the reader is
// whoever runs the system that published it.
func parseClientMetadata(u *url.URL, body []byte) (clientMetadata, error) {
	var doc clientMetadataDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return clientMetadata{}, errors.New("the client's metadata document is not a JSON object")
	}
	if doc.ClientID != u.String() {
		return clientMetadata{}, errors.New("the client's metadata document names another client_id than the URL it is published at")
	}
	if len(doc.ClientSecret) > 0 {
		// A secret in a public document is a secret nobody has any more.
		return clientMetadata{}, errors.New("the client's metadata document carries a client_secret; a document is public and must not")
	}
	if doc.TokenEndpointAuthMethod != privateKeyJWT {
		return clientMetadata{}, errors.New("the client's metadata document must name token_endpoint_auth_method " + privateKeyJWT)
	}
	for _, g := range doc.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return clientMetadata{}, errUnsupported("grant_type", g, "authorization_code and refresh_token")
		}
	}
	for _, rt := range doc.ResponseTypes {
		if rt != "code" {
			return clientMetadata{}, errUnsupported("response_type", rt, "code")
		}
	}
	uris, err := validRedirectURIs(doc.RedirectURIs)
	if err != nil {
		return clientMetadata{}, errors.New("the client's metadata document: " + err.Error())
	}
	for _, raw := range uris {
		// On the document's own origin, so a document cannot send a code to a host that
		// is not the one TLS vouched for when it was read.
		ru, _ := url.Parse(raw)
		if ru.Scheme != u.Scheme || ru.Host != u.Host {
			return clientMetadata{}, errors.New("the client's metadata document names redirect URI " + raw +
				", which is not on the origin of its client_id")
		}
	}
	if doc.JWKS == nil && doc.JWKSURI != "" {
		return clientMetadata{}, errors.New("the client's metadata document names jwks_uri, which this server does not read; publish the keys inline as jwks")
	}
	keys, err := parseClientKeys(doc.JWKS)
	if err != nil {
		return clientMetadata{}, err
	}
	name, err := registrationName(doc.ClientName, u.String())
	if err != nil {
		return clientMetadata{}, errors.New("the client's metadata document: " + err.Error())
	}
	return clientMetadata{
		client: oauthClient{ID: u.String(), Name: name, RedirectURIs: uris, Metadata: true},
		keys:   keys,
	}, nil
}

// parseClientKeys reads the signing keys a document publishes. An Ed25519 key is
// required — it is what the contract with a connected system names — and P-256 keys
// for ES256 are read beside it. A key published for encryption, or for another
// algorithm, is not a key to verify an assertion with and is passed over.
func parseClientKeys(set *rawClientJWKS) ([]clientKey, error) {
	if set == nil || len(set.Keys) == 0 {
		return nil, errors.New("the client's metadata document publishes no keys (jwks)")
	}
	var (
		keys    []clientKey
		hasEd25 bool
	)
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		switch {
		case k.Kty == "OKP" && k.Crv == "Ed25519" && (k.Alg == "" || k.Alg == "EdDSA"):
			x, err := base64.RawURLEncoding.DecodeString(k.X)
			if err != nil || len(x) != ed25519.PublicKeySize {
				continue
			}
			keys = append(keys, clientKey{kid: k.Kid, alg: "EdDSA", ed: ed25519.PublicKey(x)})
			hasEd25 = true
		case k.Kty == "EC" && k.Crv == "P-256" && (k.Alg == "" || k.Alg == "ES256"):
			x, errX := base64.RawURLEncoding.DecodeString(k.X)
			y, errY := base64.RawURLEncoding.DecodeString(k.Y)
			if errX != nil || errY != nil || len(x) != 32 || len(y) != 32 {
				continue
			}
			// Parsed as an uncompressed point, which also refuses one off the curve.
			pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
			if err != nil {
				continue
			}
			keys = append(keys, clientKey{kid: k.Kid, alg: "ES256", ec: pub})
		}
	}
	if !hasEd25 {
		return nil, errors.New("the client's metadata document publishes no Ed25519 signing key (kty OKP, crv Ed25519)")
	}
	return keys, nil
}

// errUnknownClient is a client_id that names nothing: no client entered or registered
// here, and not a metadata document's URL.
var errUnknownClient = errors.New("unknown client_id")

// lookupClient resolves a client_id to the client an authorization request is checked
// against. A client entered or registered here is read from the index. A client named
// by its metadata document is read from the document — from the cache, unless fresh
// asks for it to be read again, which an approval does — so a name or a redirect URI the
// system changed applies from its next request, whether or not a record of it was saved.
func (s *Server) lookupClient(ctx context.Context, clientID string, fresh bool) (oauthClient, error) {
	if !isMetadataClientID(clientID) {
		c, ok := s.oauthClients.lookup(clientID)
		if !ok {
			return oauthClient{}, errUnknownClient
		}
		return c, nil
	}
	if s.clientMetadata == nil {
		return oauthClient{}, errNoMetadataReader
	}
	md, err := s.clientMetadata.get(ctx, clientID, fresh)
	if err != nil {
		return oauthClient{}, err
	}
	return s.withStoredFields(md.client), nil
}

// errNoMetadataReader is a server built without New — a test's struct literal — asked
// for a document it has nothing to read one with.
var errNoMetadataReader = errors.New("this server does not read client metadata documents")

// withStoredFields is a client read from its document with what this server keeps
// about it, where it keeps a record: when it was first saved and by whose approval.
// The document's own fields win, because the document is what the system says now.
func (s *Server) withStoredFields(c oauthClient) oauthClient {
	if rec, ok := s.oauthClients.lookup(c.ID); ok {
		c.CreatedAt, c.CreatedBy = rec.CreatedAt, rec.CreatedBy
	}
	return c
}

// rememberMetadataClient saves the record of a client named by its metadata document,
// or brings the one saved earlier up to what the document says now. It runs inside the
// run-loop turn that saves the grant somebody approved for the client — the only place
// it is called — so no request a stranger sends can cause it. approver is whoever's
// approval this is, recorded as the client's CreatedBy the first time.
//
// The record is written only when something in it changed, so a person connecting
// their account to a system already known costs no extra write.
func (s *Server) rememberMetadataClient(c oauthClient, approver string) error {
	rec, ok := s.oauthClients.lookup(c.ID)
	if ok && rec.Name == c.Name && slices.Equal(rec.RedirectURIs, c.RedirectURIs) {
		return nil
	}
	if !ok {
		rec = oauthClient{ID: c.ID, Metadata: true, CreatedAt: time.Now().Unix(), CreatedBy: approver}
	}
	rec.Name, rec.RedirectURIs = c.Name, c.RedirectURIs
	if err := s.oauthClientStore.Save(rec); err != nil {
		return err
	}
	s.oauthClients.add(rec)
	return nil
}
