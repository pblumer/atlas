// Package nettimeout defines the outbound-call budget every Atlas worker
// shares.
//
// # Why workers must never block indefinitely
//
// The single-binary server drives worker jobs synchronously ON the run-loop
// goroutine (api/server.go loop, job.Runner.Drive): the mail, REST, SharePoint,
// Remedy, Clio, temis, web-scrape and DMN workers all execute there. That
// goroutine is the partition's single writer (invariant I3), so it is also the
// only goroutine that can serve any handler's s.do closure.
//
// The consequence is blunt: an outbound call with no deadline hands an external
// host the power to stop the whole engine. A worker host that accepts a
// connection and then never answers parks the run loop forever — every HTTP
// handler that touches the loop blocks behind it, and the instance is wedged
// while still looking alive (endpoints that need no loop, like /info, keep
// answering). That is not theoretical: it is exactly how an unreachable mail
// provider took an instance down, and why Go's http.DefaultClient — which has
// no timeout at all — must never be used by a worker.
//
// So every worker bounds its calls with the budget below. A slow host then
// fails its job, which retries and finally raises an incident (ADR-0061) — a
// visible, recoverable stall of one process, instead of an invisible stall of
// the entire engine.
package nettimeout

import (
	"net/http"
	"time"
)

// Default is the wall-clock budget for a single outbound worker call,
// covering the whole exchange (connect, TLS handshake, request, response body)
// rather than any one phase.
//
// **What this bounds changed.** ADR-0149 introduced it as a stall budget: handlers
// ran on the single writer, so a hung host held the whole engine and ten seconds
// was how long that could last. ADR-0157 step 6 moved handlers off the run loop, so
// this no longer bounds an engine stall at all — the engine keeps serving while the
// call hangs. What it bounds now is the *instance*: how long a token waits on a
// call before the job fails, retries, and eventually raises an incident an operator
// can see. Without it a hung host would leave a token parked in silence forever.
//
// Ten seconds stays right for that. It is generous for the calls workers make —
// a token grant, a REST POST, a message send — and short enough that a bad endpoint
// becomes a visible incident quickly rather than a process that looks alive. A
// worker that legitimately needs longer wants an asynchronous design (a job that
// polls), not a longer wait on one call.
const Default = 10 * time.Second

// HTTPClient returns an HTTP client bounded by [Default]. Workers use this in
// place of http.DefaultClient, whose zero Timeout means "wait forever".
//
// http.Client.Timeout covers the entire call including reading the body, so a
// host that dribbles bytes cannot outlast it either.
func HTTPClient() *http.Client {
	return &http.Client{Timeout: Default}
}

// Workers that do not speak HTTP (SMTP submission) apply [Default] themselves,
// as a net.Dialer timeout plus an absolute deadline on the accepted connection —
// dialing is only the first way a host can hang. See connector/mail's
// sendMailWithin.
