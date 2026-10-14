package trustedproxy

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The PROXY protocol, versions 1 and 2, as HAProxy specifies it
// (https://www.haproxy.org/download/2.9/doc/proxy-protocol.txt) and every TCP load
// balancer that offers it writes it.

// headerTimeout bounds how long a trusted peer may take to show whether a header is
// coming. It matches the listener's ReadHeaderTimeout: no balancer needs longer to send
// a hundred bytes it already has.
const headerTimeout = 10 * time.Second

var v2Signature = []byte("\r\n\r\n\x00\r\nQUIT\n")

const (
	v1Prefix = "PROXY "
	// v1MaxLine is the spec's bound on a v1 header, CRLF included. A peer that says
	// PROXY and keeps talking past it is broken, not slow.
	v1MaxLine = 107
)

// Listener reads a PROXY header, where there is one, from every connection a trusted
// proxy opens, and leaves every other connection untouched. The header is optional: a
// balancer in HTTP mode, or a TCP health check that sends nothing, arrives exactly as it
// did before.
func (s Set) Listener(ln net.Listener) net.Listener {
	if s.Empty() {
		return ln
	}
	return &listener{Listener: ln, set: s}
}

type listener struct {
	net.Listener
	set Set
}

// Accept hands a trusted connection over unread. The header is read on the
// connection's own goroutine, at its first use, so a peer that is slow to send one holds
// up only itself and never the accept loop.
func (l *listener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if peer, ok := addrOf(c.RemoteAddr()); ok && l.set.Contains(peer) {
		return newConn(c, headerTimeout), nil
	}
	return c, nil
}

// conn is a connection from a trusted proxy.
//
// The header is read once, by whichever of RemoteAddr and Read comes first. In net/http
// that is RemoteAddr, the first thing a connection's goroutine does and before it sets
// any deadline of its own — which is what makes it safe to set one here for the header
// and clear it afterwards.
type conn struct {
	net.Conn
	peer    netip.Addr // the proxy, as the socket reports it
	timeout time.Duration

	once sync.Once
	r    *bufio.Reader
	src  net.Addr // the client the header declared; nil where it declared none
	err  error    // a header that began and was broken
}

func newConn(c net.Conn, timeout time.Duration) *conn {
	peer, _ := addrOf(c.RemoteAddr())
	return &conn{Conn: c, peer: peer, timeout: timeout}
}

func (c *conn) init() {
	c.once.Do(func() {
		c.r = bufio.NewReader(c.Conn)
		_ = c.Conn.SetReadDeadline(time.Now().Add(c.timeout))
		c.src, c.err = readHeader(c.r)
		_ = c.Conn.SetReadDeadline(time.Time{})
	})
}

func (c *conn) Read(b []byte) (int, error) {
	c.init()
	if c.err != nil {
		return 0, c.err
	}
	return c.r.Read(b)
}

// RemoteAddr is the client the header declared, or the proxy where it declared none.
func (c *conn) RemoteAddr() net.Addr {
	c.init()
	if c.src != nil {
		return c.src
	}
	return c.Conn.RemoteAddr()
}

// proxiedBy is the proxy that declared this connection's client, and whether one did.
func (c *conn) proxiedBy() (netip.Addr, bool) {
	c.init()
	return c.peer, c.src != nil
}

// readHeader reads a header if the stream starts with one. Nothing is consumed unless
// it does: the first bytes are peeked at, and a TLS ClientHello or an HTTP request line
// stays in the buffer for whatever reads next. A peer that sends nothing in time is
// treated the same way — its stream, when it comes, is its stream.
func readHeader(r *bufio.Reader) (net.Addr, error) {
	first, err := r.Peek(1)
	if err != nil {
		return nil, nil
	}
	switch first[0] {
	case v1Prefix[0]:
		if p, _ := r.Peek(len(v1Prefix)); string(p) == v1Prefix {
			return readV1(r)
		}
	case v2Signature[0]:
		if p, _ := r.Peek(len(v2Signature)); bytes.Equal(p, v2Signature) {
			return readV2(r)
		}
	}
	return nil, nil
}

func cutShort(err error) error {
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	return fmt.Errorf("proxy protocol: header cut short: %w", err)
}

// readV1 reads "PROXY TCP4 <src> <dst> <sport> <dport>\r\n" and its TCP6 and UNKNOWN
// forms.
func readV1(r *bufio.Reader) (net.Addr, error) {
	line := make([]byte, 0, v1MaxLine)
	for len(line) < v1MaxLine {
		b, err := r.ReadByte()
		if err != nil {
			return nil, cutShort(err)
		}
		line = append(line, b)
		if b == '\n' {
			break
		}
	}
	if !bytes.HasSuffix(line, []byte("\r\n")) {
		if line[len(line)-1] == '\n' {
			return nil, errors.New("proxy protocol: v1 header is not terminated by CRLF")
		}
		return nil, fmt.Errorf("proxy protocol: v1 header is longer than %d bytes", v1MaxLine)
	}
	f := strings.Split(string(line[:len(line)-2]), " ")
	// UNKNOWN: the proxy could not tell, so the connection's own address stands.
	if len(f) >= 2 && f[1] == "UNKNOWN" {
		return nil, nil
	}
	if len(f) != 6 {
		return nil, fmt.Errorf("proxy protocol: v1 header has %d fields, want 6", len(f))
	}
	var want func(netip.Addr) bool
	switch f[1] {
	case "TCP4":
		want = netip.Addr.Is4
	case "TCP6":
		want = netip.Addr.Is6
	default:
		return nil, fmt.Errorf("proxy protocol: v1 protocol %q is neither TCP4 nor TCP6", f[1])
	}
	var addrs [2]netip.Addr
	for i, s := range f[2:4] {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" {
			return nil, fmt.Errorf("proxy protocol: v1 address %q is not an address", s)
		}
		if !want(a) {
			return nil, fmt.Errorf("proxy protocol: v1 address %s is not %s", a, f[1])
		}
		addrs[i] = a
	}
	var ports [2]uint16
	for i, s := range f[4:6] {
		p, err := strconv.ParseUint(s, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("proxy protocol: v1 port %q is not a port", s)
		}
		ports[i] = uint16(p)
	}
	return net.TCPAddrFromAddrPort(netip.AddrPortFrom(addrs[0].Unmap(), ports[0])), nil
}

// readV2 reads the binary form: the signature, version and command, family, length,
// then the addresses and any TLVs, which are skipped — none of them is needed to know
// who the client is.
func readV2(r *bufio.Reader) (net.Addr, error) {
	var hdr [16]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, cutShort(err)
	}
	version, command, family := hdr[12]>>4, hdr[12]&0x0F, hdr[13]
	length := int(binary.BigEndian.Uint16(hdr[14:16]))
	if version != 2 {
		return nil, fmt.Errorf("proxy protocol: v2 header carries version %d", version)
	}
	switch command {
	case 0x0:
		// LOCAL: the proxy speaking for itself, as a health check does. The connection
		// is the proxy's.
		return nil, discard(r, length)
	case 0x1:
	default:
		return nil, fmt.Errorf("proxy protocol: v2 command %#x is neither LOCAL nor PROXY", command)
	}

	var size int
	switch family {
	case 0x11: // TCP over IPv4
		size = 12
	case 0x21: // TCP over IPv6
		size = 36
	default:
		// UNSPEC, UDP, a UNIX socket: nothing that names a TCP client, so the
		// connection's own address stands.
		return nil, discard(r, length)
	}
	if length < size {
		return nil, fmt.Errorf("proxy protocol: v2 address block of %d bytes is too short for family %#x", length, family)
	}
	var block [36]byte
	if _, err := io.ReadFull(r, block[:size]); err != nil {
		return nil, cutShort(err)
	}
	if err := discard(r, length-size); err != nil {
		return nil, err
	}
	var src netip.Addr
	var port uint16
	if family == 0x11 {
		src = netip.AddrFrom4([4]byte(block[0:4]))
		port = binary.BigEndian.Uint16(block[8:10])
	} else {
		src = netip.AddrFrom16([16]byte(block[0:16])).Unmap()
		port = binary.BigEndian.Uint16(block[32:34])
	}
	return net.TCPAddrFromAddrPort(netip.AddrPortFrom(src, port)), nil
}

func discard(r *bufio.Reader, n int) error {
	if _, err := r.Discard(n); err != nil {
		return cutShort(err)
	}
	return nil
}

// addrOf is a socket address as netip, the form Set compares, zone included.
func addrOf(a net.Addr) (netip.Addr, bool) {
	if t, ok := a.(*net.TCPAddr); ok {
		ip, ok := netip.AddrFromSlice(t.IP)
		return ip.Unmap().WithZone(t.Zone), ok
	}
	if a == nil {
		return netip.Addr{}, false
	}
	return parseHop(a.String())
}
