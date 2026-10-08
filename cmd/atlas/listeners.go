package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/internal/trustedproxy"
	"github.com/pblumer/atlas/logging"
)

// Listener timeouts. Go's zero values mean "no limit", which for a server that
// can be exposed directly is a resource leak waiting for a slow client: a
// connection that dribbles a request header, or a keep-alive connection nobody
// comes back to, is held until the peer gives up. The request-body byte caps do
// not bound either — they limit how much is read, not how long it takes.
//
// A reverse proxy in front will usually impose its own; these are what the server
// guarantees on its own behalf, so a direct deployment is not relying on someone
// else's configuration.
const (
	// readHeaderTimeout bounds the cheap attack: how long a client may take to send
	// its request line and headers. It is deliberately short — no legitimate client
	// needs longer to send a few kilobytes it already has.
	readHeaderTimeout = 10 * time.Second
	// idleTimeout reclaims a keep-alive connection that is not being reused. Longer
	// than a browser's typical reuse window, so ordinary navigation still benefits
	// from the connection it already has.
	idleTimeout = 120 * time.Second
)

// newHTTPServer builds one of this process's HTTP servers with that timeout
// contract. Both listeners go through it, so the loopback server — which serves
// this process's own children and the MCP adapter — is not quietly the unbounded
// one.
//
// ReadTimeout and WriteTimeout stay at zero, and that is a decision rather than an
// omission:
//
//   - ReadTimeout would bound the whole request read, body included. A restore
//     upload is a gzip-tar of an entire data directory and is bounded by a byte
//     cap (limits.Archive); over a slow link a legitimate one can take longer
//     than any deadline worth setting for a header. The byte cap is the right
//     limit for a body, and it is already enforced.
//   - WriteTimeout would bound the whole response write. A worker long-polling for
//     a job (ADR-0007) and a streamed whole-instance backup both hold a response
//     open on purpose; a global deadline would cut off exactly the endpoints that
//     are working correctly.
//
// Endpoints that need a bound on their own body or response set it themselves,
// where the right duration is known.
//
// Both also write net/http's own complaints through the logging package rather than
// the bare standard logger, so a load balancer's TCP health check — a connection that
// closes before its TLS handshake begins — is a DEBUG line instead of an INFO one every
// interval (ADR-draft-trusted-proxies).
func newHTTPServer(addr string, h http.Handler, tlsCfg *tls.Config) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          logging.HTTPServerErrorLog(),
	}
}

// newPublicServer is the server --addr is served by: newHTTPServer, taking the client's
// address from the proxies --trusted-proxies names (ADR-draft-trusted-proxies). With
// none named the handler is the handler and the hook finds nothing to record.
//
// The loopback server is deliberately not one. Its peers are this process's own
// children and the MCP adapter; nothing on it is a load balancer, so nothing on it is
// read as one, whatever the operator's list says about 127.0.0.1.
func newPublicServer(addr string, h http.Handler, tlsCfg *tls.Config, proxies trustedproxy.Set) *http.Server {
	srv := newHTTPServer(addr, proxies.Handler(h), tlsCfg)
	srv.ConnContext = trustedproxy.ConnContext
	return srv
}

// servePublic binds --addr and serves it. It binds here, when serving starts, so the
// port stays shut until recovery has replayed the log, exactly as ListenAndServe did;
// it binds by hand so the PROXY protocol listener can sit under the TLS one, where the
// header arrives — in front of the ClientHello.
func servePublic(srv *http.Server, tlsOn bool, proxies trustedproxy.Set) error {
	addr := srv.Addr
	if addr == "" {
		addr = ":http"
		if tlsOn {
			addr = ":https"
		}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	ln = proxies.Listener(ln)
	if !tlsOn {
		return srv.Serve(ln)
	}
	defer ln.Close() // ServeTLS can fail before Serve has taken the listener over
	// The pair is served by TLSConfig's GetCertificate, which re-reads it when it
	// changes; the filename arguments here would read it once and never again, so
	// they are deliberately empty (ADR-0191).
	return srv.ServeTLS(ln, "", "")
}

// httpListener is one of this process's HTTP servers together with how it starts
// serving. The two differ on purpose: the public server binds --addr when it
// starts, so the port stays shut until recovery has replayed the log, while the
// loopback server serves a listener bound earlier — its port has to be known
// before the handler is built, because that port is what this process's children
// are handed (ADR-0191).
type httpListener struct {
	srv   *http.Server
	serve func() error
}

// serveUntil runs every listener until ctx is cancelled or one of them stops, then
// shuts them all down inside one grace period.
//
// Either listener failing ends the process. A server that reached half of its
// interfaces is worse than one that stopped: monitoring says it is up, and the
// half that is missing fails where nobody is watching.
func serveUntil(ctx context.Context, shutdownTimeout time.Duration, ls ...httpListener) error {
	errCh := make(chan error, len(ls))
	for _, l := range ls {
		go func() {
			if err := l.serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
				return
			}
			errCh <- nil
		}()
	}

	var err error
	select {
	case err = <-errCh:
	case <-ctx.Done():
		logging.Info(logging.ServerShuttingDown, "shutting down")
	}

	// One deadline for all of them, whichever one ended the wait: the operator asked
	// for a single grace period, not one per listener.
	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, l := range ls {
		if e := l.srv.Shutdown(shutCtx); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// internalURL is what this process's own children call back on: the MCP adapter's
// loopback client (ADR-0016) and every supervised worker, which is handed this
// server's internal service token (ADR-0049).
//
// Where this server terminates TLS that is the plaintext loopback listener rather
// than --addr, and the reason is naming rather than policy. A certificate issued
// for atlas.example.com carries no SAN for 127.0.0.1, so verification fails
// whichever root the child trusts, and the only thing that would make it pass is
// the skip-verify switch this repository has decided twice not to have. So the hop
// stays plaintext on loopback, which does not cross a network — the same exception
// validateTargetURL already carves out for a loopback target (ADR-0191).
func internalURL(addr string, loopbackLn net.Listener) string {
	if loopbackLn == nil {
		return loopbackURL(addr)
	}
	return "http://" + loopbackLn.Addr().String()
}

// reachableOrigin is the origin the startup lines point an operator at: the URL to
// paste into a browser, or into a remote worker's --server.
//
// It is not internalURL. Where this server terminates TLS those two part company —
// the children keep an ephemeral plaintext loopback port that nothing outside this
// process can use — and the startup log is read by a person, so it gets the one
// that works (ADR-0191).
func reachableOrigin(externalURL, addr string, tlsOn bool) string {
	if s := strings.TrimRight(strings.TrimSpace(externalURL), "/"); s != "" {
		return s
	}
	origin := loopbackURL(addr)
	if tlsOn {
		origin = "https://" + strings.TrimPrefix(origin, "http://")
	}
	return origin
}
