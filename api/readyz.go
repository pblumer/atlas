package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Readiness, distinct from liveness (ADR-0142 slice 7).
//
// /healthz answers one question — is this process alive — and answering it with an
// unconditional "ok" is correct: a liveness probe's only remedy is a restart, so it must
// not fail for anything a restart would not fix. A readiness probe asks something else:
// should traffic be routed here *now*. Conflating the two is expensive in both
// directions. A liveness probe that waited for recovery would kill a pod in the middle of
// a long replay, and every restart makes that replay start over. A readiness probe that
// only checks liveness — which is what the Helm chart did until this slice, pointing all
// three probes at /healthz — routes traffic to a server whose store cannot be read or
// whose single writer is wedged.
//
// So /readyz reports what a load balancer needs and /healthz keeps reporting what a
// supervisor needs. The checks below are ordered cheapest-first and none of them touches
// engine state or the hot path: two are reads of values the server already holds, one is
// a single point read of the store (the same read the metrics collector does at scrape
// time), and the last is an empty closure on the run loop.

// readyTimeoutDefault bounds the run-loop check. It matches the timeoutSeconds the Helm
// chart gives the probe: the probe gives up at its deadline either way, and failing on
// our side produces a 503 that names the reason rather than a client-side timeout that
// names nothing.
const readyTimeoutDefault = 2 * time.Second

// handleReadyz reports whether this server should be routed traffic. It is
// unauthenticated for the same reason /healthz and /metrics are: a kubelet has no
// session, and the body carries a fixed reason string — no instance data, no counts, no
// identifiers.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if reason := s.notReady(r.Context()); reason != "" {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "not ready: "+reason+"\n")
		return
	}
	_, _ = io.WriteString(w, "ok\n")
}

// notReady returns why this server is not ready, or "" when it is. The reason is a fixed
// string chosen here — never an operator-supplied one — so the endpoint cannot be used to
// reflect input back to an unauthenticated caller.
func (s *Server) notReady(ctx context.Context) string {
	// Shutting down first: it explains everything below it, and it is the state in which
	// do() silently drops the work it is handed, so a server that kept reporting ready
	// here would accept writes that go nowhere.
	select {
	case <-s.quit:
		return "server is shutting down"
	default:
	}
	// Recovery is the ordering contract (ADR-0005): until the log has been replayed the
	// store does not yet describe reality, and answering a query from it would report
	// work that exists as though it did not. The shipped binary starts its listener only
	// after recovery returns, so in production this is a guard on the contract rather
	// than a state a kubelet observes — it is reachable by anything that mounts the
	// handler itself, which is exactly what the guard is for.
	if !s.proc.LastRecovery().Done {
		return "startup recovery has not finished"
	}
	// A store that cannot answer a read is a store the engine cannot serve from. This is
	// one point read of a fixed key — the same one the metrics collector does per scrape.
	if err := s.storeReadable(); err != nil {
		return "state store is not readable: " + err.Error()
	}
	// And finally the failure /healthz structurally cannot see: the process is alive and
	// answering HTTP while the goroutine that owns the processor is stuck. A blocked
	// fsync on a hung volume looks exactly like this.
	if !s.pingRunLoop(ctx) {
		return "the partition writer is not responding"
	}
	return ""
}

// storeReadable does one point read of the store and reports what went wrong.
//
// The recover is deliberate and narrow. Pebble panics rather than returning an error when
// it is used after Close, and a panic here would reach net/http, which drops the
// connection and logs a stack trace — every ten seconds, for as long as the probe runs.
// Readiness is precisely the endpoint where "the store is gone" should be *reported*,
// so it is converted into the 503 it means. Nothing is retried and nothing is repaired:
// the panic is turned into a reason string and nothing else.
func (s *Server) storeReadable() (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	_, err = s.store.LastAppliedPosition()
	return err
}

// pingRunLoop reports whether the run loop is reachable within the readiness
// deadline. The liveness check itself lives on the loop (ADR-0147), which owns
// the queue it has to hand a closure to; this is the server's thin wrapper over
// it so the probe reads in the terms of the rest of this file.
func (s *Server) pingRunLoop(ctx context.Context) bool {
	return s.runLoop.Ping(ctx, s.readyTimeout)
}
