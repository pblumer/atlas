package logging

import (
	"log"
	"log/slog"
	"strings"
)

// HTTPServerErrorLog is the ErrorLog for this process's HTTP servers.
//
// Without one, net/http writes through the standard logger, which Setup has already
// pointed at the same handler as everything else — so its lines were never lost, only
// undifferentiated: every one of them INFO, none with an event name. One of them is not
// news. A load balancer's TCP health check connects to the TLS port and closes before
// sending a ClientHello, and net/http reports each such connection as
//
//	http: TLS handshake error from 10.179.2.139:42658: EOF
//
// At a ten-second check that is 8640 lines a day saying the check works, and they bury
// the handshake failures that do mean something — a client that cannot speak TLS 1.3
// (ADR-0191), a certificate somebody refused. So that one shape is written at DEBUG
// under server.tls_handshake_aborted, with the peer as an attribute, and everything
// else goes on through the standard logger exactly as before (ADR-draft-trusted-proxies).
//
// It is demoted rather than dropped: a scan of the port leaves the same trace, and an
// operator who wants to see it asks for --log-level=debug.
func HTTPServerErrorLog() *log.Logger { return log.New(httpErrorWriter{}, "", 0) }

// The two halves of the one line that is demoted. Matched exactly: a handshake that
// failed with "unexpected EOF" stopped part-way through a ClientHello, which is not what
// a health check does, and stays where it was.
const (
	handshakeErrorPrefix = "http: TLS handshake error from "
	handshakeEOFSuffix   = ": EOF"
)

type httpErrorWriter struct{}

func (httpErrorWriter) Write(p []byte) (int, error) {
	line := strings.TrimSuffix(string(p), "\n")
	if peer, ok := strings.CutPrefix(line, handshakeErrorPrefix); ok {
		if peer, ok = strings.CutSuffix(peer, handshakeEOFSuffix); ok {
			emit(slog.LevelDebug, ServerTLSHandshakeAborted,
				"a connection closed before its TLS handshake began; a load balancer's TCP health check does exactly this",
				[]slog.Attr{slog.String("remote", peer)})
			return len(p), nil
		}
	}
	// Everything else is written as net/http would have written it with no ErrorLog
	// set: through the standard logger, which reaches the same handler at INFO.
	log.Print(line)
	return len(p), nil
}
