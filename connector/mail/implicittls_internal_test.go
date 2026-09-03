package mail

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// TestUsesImplicitTLS pins the transport decision. It is made by port because the
// worker configuration carries no TLS mode, and getting it wrong does not fail
// cleanly — it hangs until the deadline.
func TestUsesImplicitTLS(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"mx1.example.org:465", true}, // SMTPS: TLS from the first byte
		{"mx1.example.org:587", false},
		{"mx1.example.org:25", false},
		{"mx1.example.org:2525", false},
		{"127.0.0.1:465", true},
		{"[::1]:465", true},
		{"mx1.example.org", false}, // no port at all: not a usable endpoint
		{"", false},
		{"mx1.example.org:4650", false}, // must not match on a prefix
		{"mx1.example.org:1465", false},
	} {
		if got := usesImplicitTLS(tc.addr); got != tc.want {
			t.Errorf("usesImplicitTLS(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

// testTLSConfig returns a server TLS config with a throwaway self-signed
// certificate for 127.0.0.1, plus the client config that trusts it.
func testTLSConfig(t *testing.T) (server, client *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}}},
		&tls.Config{RootCAs: pool, ServerName: "127.0.0.1"}
}

// smtpScript serves a minimal SMTP conversation over ln, recording the commands it
// received. It answers every command with a generic 250 except DATA (354 then 250
// after the terminating dot) and QUIT (221).
func smtpScript(t *testing.T, ln net.Listener, got *[]string, done chan<- struct{}) {
	t.Helper()
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write([]byte("220 test ESMTP\r\n")); err != nil {
			return
		}
		buf := make([]byte, 4096)
		inData := false
		for {
			n, err := conn.Read(buf)
			if err != nil || n == 0 {
				return
			}
			chunk := string(buf[:n])
			if inData {
				if strings.Contains(chunk, "\r\n.\r\n") {
					inData = false
					_, _ = conn.Write([]byte("250 ok\r\n"))
				}
				continue
			}
			for _, line := range strings.Split(strings.TrimRight(chunk, "\r\n"), "\r\n") {
				if line == "" {
					continue
				}
				*got = append(*got, line)
				switch {
				case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
					// Advertise nothing: no STARTTLS (already encrypted), no AUTH.
					_, _ = conn.Write([]byte("250 test\r\n"))
				case strings.HasPrefix(line, "DATA"):
					inData = true
					_, _ = conn.Write([]byte("354 send it\r\n"))
				case strings.HasPrefix(line, "QUIT"):
					_, _ = conn.Write([]byte("221 bye\r\n"))
					return
				default:
					_, _ = conn.Write([]byte("250 ok\r\n"))
				}
			}
		}
	}()
}

// TestSendMailOverImplicitTLSDelivers is the feature: against a server that speaks
// TLS from the first byte (an SMTPS endpoint, as on port 465), the send completes
// the whole envelope. Before implicit-TLS support this could only hang — the
// client waited for a plaintext greeting the server would never send.
func TestSendMailOverImplicitTLSDelivers(t *testing.T) {
	serverCfg, clientCfg := testTLSConfig(t)
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer raw.Close()
	ln := tls.NewListener(raw, serverCfg)

	var got []string
	done := make(chan struct{})
	smtpScript(t, ln, &got, done)

	err = sendMailOver(5*time.Second, ln.Addr().String(), true, clientCfg, nil,
		"from@example.org", []string{"to@example.org"}, []byte("Subject: hi\r\n\r\nbody"))
	if err != nil {
		t.Fatalf("implicit-TLS send: %v", err)
	}
	<-done

	joined := strings.Join(got, "|")
	for _, want := range []string{"MAIL FROM:<from@example.org>", "RCPT TO:<to@example.org>", "DATA", "QUIT"} {
		if !strings.Contains(joined, want) {
			t.Errorf("server never saw %q; conversation was: %v", want, got)
		}
	}
}

// TestPlaintextSendStillWorks guards the other side of the branch: a non-SMTPS
// endpoint must keep speaking plaintext (and would upgrade via STARTTLS if the
// server offered it). The script here advertises no extensions.
func TestPlaintextSendStillWorks(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var got []string
	done := make(chan struct{})
	smtpScript(t, ln, &got, done)

	err = sendMailOver(5*time.Second, ln.Addr().String(), false, nil, nil,
		"from@example.org", []string{"to@example.org"}, []byte("Subject: hi\r\n\r\nbody"))
	if err != nil {
		t.Fatalf("plaintext send: %v", err)
	}
	<-done
	if joined := strings.Join(got, "|"); !strings.Contains(joined, "MAIL FROM:<from@example.org>") {
		t.Errorf("plaintext conversation incomplete: %v", got)
	}
}

// TestPlaintextAgainstTLSServerHangsButIsBounded reproduces the production
// incident this feature fixes: a plaintext client against an SMTPS server. Both
// sides wait — the client for a greeting, the server for a ClientHello — so the
// send can only end at the deadline, which is what the observed
// "read tcp …:465: i/o timeout" was.
//
// It asserts both halves of the lesson: choosing the wrong transport cannot
// succeed, and (since the mail worker runs on the run-loop goroutine) it must
// still return within the budget rather than park the engine.
func TestPlaintextAgainstTLSServerHangsButIsBounded(t *testing.T) {
	serverCfg, _ := testTLSConfig(t)
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer raw.Close()
	ln := tls.NewListener(raw, serverCfg)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		t.Cleanup(func() { conn.Close() })
		<-t.Context().Done()
	}()

	const budget = 200 * time.Millisecond
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- sendMailOver(budget, ln.Addr().String(), false, nil, nil,
			"from@example.org", []string{"to@example.org"}, []byte("hi"))
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("plaintext send to an SMTPS server reported success")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("send took %v; the budget (%v) did not bound it", elapsed, budget)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("plaintext send to an SMTPS server never returned — this is the hang that wedged the run loop")
	}
}

// TestImplicitTLSAgainstPlaintextServerFailsBounded: pointing the implicit-TLS
// transport at a plaintext server must fail within the budget rather than hang —
// the mirror of the bug this feature fixes, so a misconfigured port is an incident
// and never a stalled run loop.
func TestImplicitTLSAgainstPlaintextServerFailsBounded(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		t.Cleanup(func() { conn.Close() })
		_, _ = conn.Write([]byte("220 plaintext ESMTP\r\n"))
		<-t.Context().Done()
	}()

	done := make(chan error, 1)
	go func() {
		done <- sendMailOver(200*time.Millisecond, ln.Addr().String(), true, nil, nil,
			"from@example.org", []string{"to@example.org"}, []byte("hi"))
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("TLS handshake against a plaintext server reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("implicit-TLS dial to a plaintext server never returned; the budget did not bound it")
	}
}
