package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/logging"
)

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
