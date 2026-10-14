package trustedproxy

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// The PROXY protocol (haproxy.org/download/2.9/doc/proxy-protocol.txt) is how a load
// balancer that forwards TCP rather than HTTP says whose connection it is forwarding:
// it cannot add a header to a TLS stream it does not decrypt, so it writes one line (v1)
// or one binary block (v2) in front of the stream instead.

// balancer is the address the tests' trusted peer connects from.
var balancer = &net.TCPAddr{IP: net.IPv4(10, 179, 2, 139), Port: 40000}

// pipePeer is one end of a net.Pipe that reports a chosen remote address, so a
// connection can be made to come "from" the balancer without a routable socket.
type pipePeer struct {
	net.Conn
	remote net.Addr
}

func (p pipePeer) RemoteAddr() net.Addr { return p.remote }

// fromBalancer returns the server side of a connection whose peer has just written
// sent, already wrapped as a trusted connection.
func fromBalancer(t *testing.T, sent []byte, closeAfter bool) *conn {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close(); client.Close() })
	go func() {
		_, _ = client.Write(sent)
		if closeAfter {
			client.Close()
		}
	}()
	return newConn(pipePeer{Conn: server, remote: balancer}, time.Second)
}

// readAll reads what the connection delivers after its header, up to n bytes.
func readN(t *testing.T, c net.Conn, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read the stream after the header: %v", err)
	}
	return buf
}

const payload = "\x16\x03\x01 the ClientHello that follows"

func v2Header(cmd, fam byte, addrs []byte, tlvs []byte) []byte {
	h := append([]byte{}, v2Signature...)
	h = append(h, 0x20|cmd, fam)
	h = binary.BigEndian.AppendUint16(h, uint16(len(addrs)+len(tlvs)))
	h = append(h, addrs...)
	return append(h, tlvs...)
}

func v4Addrs(src, dst string, sport, dport uint16) []byte {
	s, d := netip.MustParseAddr(src).As4(), netip.MustParseAddr(dst).As4()
	b := append(s[:], d[:]...)
	b = binary.BigEndian.AppendUint16(b, sport)
	return binary.BigEndian.AppendUint16(b, dport)
}

func v6Addrs(src, dst string, sport, dport uint16) []byte {
	s, d := netip.MustParseAddr(src).As16(), netip.MustParseAddr(dst).As16()
	b := append(s[:], d[:]...)
	b = binary.BigEndian.AppendUint16(b, sport)
	return binary.BigEndian.AppendUint16(b, dport)
}

// TestAHeaderNamesTheClient: each form a balancer sends, and what the connection then
// reports. The stream after the header must arrive byte for byte — it is the TLS
// handshake.
func TestAHeaderNamesTheClient(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header []byte
		want   string // the RemoteAddr the connection reports
	}{
		{"v1 tcp4", []byte("PROXY TCP4 203.0.113.5 10.0.0.9 51234 443\r\n"), "203.0.113.5:51234"},
		{"v1 tcp6", []byte("PROXY TCP6 2001:db8::7 2001:db8::1 51234 443\r\n"), "[2001:db8::7]:51234"},
		{"v1 unknown keeps the balancer", []byte("PROXY UNKNOWN\r\n"), balancer.String()},
		{"v1 unknown with addresses", []byte("PROXY UNKNOWN ffff:f...f:ffff ffff:f...f:ffff 65535 65535\r\n"), balancer.String()},
		{"v2 tcp4", v2Header(0x1, 0x11, v4Addrs("203.0.113.5", "10.0.0.9", 51234, 443), nil), "203.0.113.5:51234"},
		{"v2 tcp4 with TLVs", v2Header(0x1, 0x11, v4Addrs("203.0.113.5", "10.0.0.9", 51234, 443),
			[]byte{0x05, 0x00, 0x04, 'v', 'p', 'c', 'e'}), "203.0.113.5:51234"},
		{"v2 tcp6", v2Header(0x1, 0x21, v6Addrs("2001:db8::7", "2001:db8::1", 51234, 443), nil), "[2001:db8::7]:51234"},
		{"v2 tcp6 mapped", v2Header(0x1, 0x21, v6Addrs("::ffff:203.0.113.5", "::ffff:10.0.0.9", 51234, 443), nil), "203.0.113.5:51234"},
		// LOCAL is the balancer speaking for itself — the spec's example is a health
		// check — so the connection is the balancer's.
		{"v2 local keeps the balancer", v2Header(0x0, 0x00, nil, nil), balancer.String()},
		{"v2 unspec keeps the balancer", v2Header(0x1, 0x00, nil, nil), balancer.String()},
		{"v2 unix keeps the balancer", v2Header(0x1, 0x31, make([]byte, 216), nil), balancer.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fromBalancer(t, append(tc.header, payload...), false)
			if got := c.RemoteAddr().String(); got != tc.want {
				t.Errorf("RemoteAddr = %s, want %s", got, tc.want)
			}
			if got := readN(t, c, len(payload)); string(got) != payload {
				t.Errorf("stream after the header = %q, want %q", got, payload)
			}
		})
	}
}

// TestNoHeaderIsNoChange: the header is optional from a trusted peer. A balancer in
// HTTP mode, a TCP health check, or one that was never configured to send it, must all
// arrive exactly as they did before — the first bytes peeked at, none consumed.
func TestNoHeaderIsNoChange(t *testing.T) {
	for _, first := range []string{
		payload,
		"POST /api/v1/auth/login HTTP/1.1\r\n",
		"PUT /x HTTP/1.1\r\n",
		"PROXZ is not a header\r\n",
		"\r\nGET / HTTP/1.1\r\n\r\n",
	} {
		c := fromBalancer(t, []byte(first), false)
		if got := c.RemoteAddr().String(); got != balancer.String() {
			t.Errorf("%q: RemoteAddr = %s, want the balancer", first, got)
		}
		if got := readN(t, c, len(first)); string(got) != first {
			t.Errorf("stream = %q, want %q untouched", got, first)
		}
	}
}

// TestAClosedConnectionIsTheBalancers: the health check from the log that started this —
// connect, send nothing, close. No header, the balancer's own address, and the EOF the
// TLS handshake reports.
func TestAClosedConnectionIsTheBalancers(t *testing.T) {
	c := fromBalancer(t, nil, true)
	if got := c.RemoteAddr().String(); got != balancer.String() {
		t.Errorf("RemoteAddr = %s, want the balancer", got)
	}
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("Read = %v, want io.EOF", err)
	}
}

// TestASlowPeerIsNotAHeader: a trusted peer that sends the first byte of something and
// then stalls is given the header timeout and no more; what it did send is kept for
// whatever reads next.
func TestASlowPeerIsNotAHeader(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close(); client.Close() })
	c := newConn(pipePeer{Conn: server, remote: balancer}, 50*time.Millisecond)

	written := make(chan struct{})
	go func() {
		_, _ = client.Write([]byte("PR"))
		<-written
		_, _ = client.Write([]byte("OXY is late\r\n"))
	}()
	start := time.Now()
	if got := c.RemoteAddr().String(); got != balancer.String() {
		t.Errorf("RemoteAddr = %s, want the balancer", got)
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Fatalf("waited %v for a header that was never coming", waited)
	}
	close(written)
	want := "PROXY is late\r\n"
	if got := readN(t, c, len(want)); string(got) != want {
		t.Errorf("stream = %q, want %q", got, want)
	}
}

// TestABrokenHeaderEndsTheConnection: a peer that began a header and got it wrong is not
// guessed at. Its connection fails, and the reason is what net/http then logs.
func TestABrokenHeaderEndsTheConnection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header []byte
		reason string
	}{
		{"v1 bad address", []byte("PROXY TCP4 203.0.113.999 10.0.0.9 51234 443\r\n"), "address"},
		{"v1 family mismatch", []byte("PROXY TCP4 2001:db8::7 10.0.0.9 51234 443\r\n"), "TCP4"},
		{"v1 bad port", []byte("PROXY TCP4 203.0.113.5 10.0.0.9 70000 443\r\n"), "port"},
		{"v1 too few fields", []byte("PROXY TCP4 203.0.113.5 10.0.0.9 51234\r\n"), "fields"},
		{"v1 unknown protocol", []byte("PROXY UDP4 203.0.113.5 10.0.0.9 51234 443\r\n"), "UDP4"},
		{"v1 no CRLF", []byte("PROXY TCP4 203.0.113.5 10.0.0.9 51234 443\n"), "CRLF"},
		{"v1 too long", []byte("PROXY " + strings.Repeat("x", 120) + "\r\n"), "107"},
		{"v1 zone", []byte("PROXY TCP6 fe80::1%eth0 2001:db8::1 51234 443\r\n"), "address"},
		{"v2 wrong version", func() []byte {
			h := v2Header(0x1, 0x11, v4Addrs("203.0.113.5", "10.0.0.9", 1, 2), nil)
			h[12] = 0x11
			return h
		}(), "version"},
		{"v2 unknown command", v2Header(0x2, 0x11, v4Addrs("203.0.113.5", "10.0.0.9", 1, 2), nil), "command"},
		{"v2 short addresses", v2Header(0x1, 0x11, []byte{1, 2, 3}, nil), "short"},
		{"v2 short ipv6", v2Header(0x1, 0x21, make([]byte, 20), nil), "short"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fromBalancer(t, append(tc.header, payload...), true)
			if got := c.RemoteAddr().String(); got != balancer.String() {
				t.Errorf("RemoteAddr = %s, want the balancer after a broken header", got)
			}
			_, err := c.Read(make([]byte, 1))
			if err == nil {
				t.Fatal("a broken header was read past")
			}
			if !strings.Contains(err.Error(), "proxy protocol") || !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error %q should say proxy protocol and %q", err, tc.reason)
			}
		})
	}
}

// TestAHeaderCutShortEndsTheConnection: the signature arrived and the rest did not.
func TestAHeaderCutShortEndsTheConnection(t *testing.T) {
	full := v2Header(0x1, 0x11, v4Addrs("203.0.113.5", "10.0.0.9", 51234, 443), nil)
	withTLVs := v2Header(0x1, 0x11, v4Addrs("203.0.113.5", "10.0.0.9", 51234, 443), []byte{0x05, 0x00, 0x04, 'v', 'p', 'c', 'e'})
	local := v2Header(0x0, 0x00, nil, []byte{0x05, 0x00, 0x04, 'v', 'p', 'c', 'e'})
	for _, sent := range [][]byte{full[:14], full[:20], withTLVs[:len(withTLVs)-3], local[:len(local)-3], []byte("PROXY TCP4 203.0.113.5")} {
		c := fromBalancer(t, sent, true)
		if _, err := c.Read(make([]byte, 1)); err == nil || !strings.Contains(err.Error(), "proxy protocol") {
			t.Errorf("%q: Read = %v, want a proxy protocol error", sent, err)
		}
	}
}

// TestOnlyATrustedPeerIsHeard is the security half of the listener: a header from
// anybody else is not a header, it is the first bytes of their stream — which a TLS
// handshake or an HTTP parser then refuses on its own. The peer's address is never
// replaced by one it wrote.
func TestOnlyATrustedPeerIsHeard(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trusted string
		want    func(client net.Addr) string
		header  bool // whether the header is consumed
	}{
		{"untrusted", "192.0.2.1", func(c net.Addr) string { return c.String() }, false},
		{"trusted", "127.0.0.1", func(net.Addr) string { return "203.0.113.5:51234" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ln := mustParse(tc.trusted).Listener(raw)
			defer ln.Close()

			header := "PROXY TCP4 203.0.113.5 10.0.0.9 51234 443\r\n"
			client, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if _, err := client.Write([]byte(header + payload)); err != nil {
				t.Fatal(err)
			}

			c, err := ln.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if got, want := c.RemoteAddr().String(), tc.want(client.LocalAddr()); got != want {
				t.Errorf("RemoteAddr = %s, want %s", got, want)
			}
			want := payload
			if !tc.header {
				want = header + payload
			}
			if got := readN(t, c, len(want)); !bytes.Equal(got, []byte(want)) {
				t.Errorf("stream = %q, want %q", got, want)
			}
		})
	}
}

// TestAnEmptySetIsTheListener: without --trusted-proxies nothing is wrapped.
func TestAnEmptySetIsTheListener(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if got := (Set{}).Listener(raw); got != raw {
		t.Fatal("an empty set wrapped the listener")
	}
}

// TestAddrOfReadsWhatASocketReports: a listener is not always TCP, and a peer it cannot
// place is never a trusted one.
func TestAddrOfReadsWhatASocketReports(t *testing.T) {
	if a, ok := addrOf(&net.TCPAddr{IP: net.ParseIP("::ffff:10.179.2.139"), Port: 1}); !ok || a.String() != "10.179.2.139" {
		t.Errorf("addrOf(4-in-6 TCP) = %v, %v", a, ok)
	}
	if a, ok := addrOf(fakeAddr("203.0.113.5:1")); !ok || a.String() != "203.0.113.5" {
		t.Errorf("addrOf(host:port) = %v, %v", a, ok)
	}
	if _, ok := addrOf(&net.UnixAddr{Name: "/run/atlas.sock", Net: "unix"}); ok {
		t.Error("a unix socket path read as an address")
	}
	if _, ok := addrOf(nil); ok {
		t.Error("no address read as an address")
	}
}

type fakeAddr string

func (fakeAddr) Network() string  { return "tcp" }
func (a fakeAddr) String() string { return string(a) }
