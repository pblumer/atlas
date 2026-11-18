package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
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
// interval (ADR-0448).
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
// address from the proxies --trusted-proxies names (ADR-0448). With
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

// redirectToHTTPS answers plain HTTP for a server that terminates TLS itself
// (--redirect-addr, ADR-0560): a GET or HEAD is sent to the
// same host and path over HTTPS, on httpsPort where that is not 443 (empty means
// 443). The host is the one the request names, so every name this server has a
// certificate for keeps its own (ADR-0562).
//
// The redirect is temporary. A browser keeps a permanent one without an end, which
// would outlast a server that stops terminating TLS, with nothing to withdraw it;
// what makes HTTPS stick is the transport policy of --hsts-max-age, which has an end
// and a way back. Any other method is refused rather than redirected: its body and
// credentials have crossed the network in the clear already, and a client that is
// redirected would send them so again every time, unnoticed.
//
// Nothing else is answered here — no API, no Console, no health endpoint — so the
// port that is open to plain HTTP reaches none of the server.
func redirectToHTTPS(httpsPort string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
			if strings.Contains(h, ":") {
				host = "[" + h + "]"
			}
		}
		if !plainHost(host) || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.Error(w, "this server answers over HTTPS only", http.StatusBadRequest)
			return
		}
		if httpsPort != "" && httpsPort != "443" {
			host = net.JoinHostPort(strings.Trim(host, "[]"), httpsPort)
		}
		to := url.URL{Scheme: "https", Host: host, Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery}
		http.Redirect(w, r, to.String(), http.StatusFound)
	})
}

// plainHost reports whether host is a name or an address and nothing else, so that
// what a request calls its host cannot carry a user, a path or another host into the
// address the redirect names.
func plainHost(host string) bool {
	for i := 0; i < len(host); i++ {
		c := host[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '.', c == '_', c == ':', c == '[', c == ']':
		default:
			return false
		}
	}
	return host != ""
}

// httpsPort is the port readers reach HTTPS at when it is not the standard one: the
// port --external-url names, if it is an https origin that names one. The port of
// --addr does not tell: a container or a load balancer publishes it under another.
func httpsPort(externalURL string) string {
	u, err := url.Parse(strings.TrimSpace(externalURL))
	if err != nil || u.Scheme != "https" {
		return ""
	}
	return u.Port()
}

// hstsMaxAge reads --hsts-max-age: a number of seconds, and whether one was given at
// all, since zero is a setting of its own — it tells browsers to forget the policy.
func hstsMaxAge(setting string) (seconds uint64, set bool, err error) {
	setting = strings.TrimSpace(setting)
	if setting == "" {
		return 0, false, nil
	}
	seconds, err = strconv.ParseUint(setting, 10, 63)
	if err != nil {
		return 0, false, fmt.Errorf("--hsts-max-age %q is not a number of seconds", setting)
	}
	return seconds, true, nil
}

// hstsHeader is the policy as the startup line states it: the header's value, or
// empty where none is sent.
func hstsHeader(seconds uint64, set bool) string {
	if !set {
		return ""
	}
	return "max-age=" + strconv.FormatUint(seconds, 10)
}
