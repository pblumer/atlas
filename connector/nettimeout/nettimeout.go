// Package nettimeout defines the outbound-call budget every Atlas connector
// shares.
//
// # Why connectors must never block indefinitely
//
// The single-binary server drives connector jobs synchronously ON the run-loop
// goroutine (api/server.go loop, job.Runner.Drive): the mail, REST, SharePoint,
// Remedy, Clio, temis, web-scrape and DMN workers all execute there. That
// goroutine is the partition's single writer (invariant I3), so it is also the
// only goroutine that can serve any handler's s.do closure.
//
// The consequence is blunt: an outbound call with no deadline hands an external
// host the power to stop the whole engine. A connector host that accepts a
// connection and then never answers parks the run loop forever — every HTTP
// handler that touches the loop blocks behind it, and the instance is wedged
// while still looking alive (endpoints that need no loop, like /info, keep
// answering). That is not theoretical: it is exactly how an unreachable mail
// provider took an instance down, and why Go's http.DefaultClient — which has
// no timeout at all — must never be used by a connector.
//
// So every connector bounds its calls with the budget below. A slow host then
// fails its job, which retries and finally raises an incident (ADR-0061) — a
// visible, recoverable stall of one process, instead of an invisible stall of
// the entire engine.
package nettimeout

import (
	"net/http"
	"time"
)

// Default is the wall-clock budget for a single outbound connector call,
// covering the whole exchange (connect, TLS handshake, request, response body)
// rather than any one phase.
//
// Ten seconds is deliberately short. It is generous for the API calls connectors
// actually make — a token grant, a REST POST, a message send — while keeping a
// hung host's hold on the run loop to a bounded blip. A connector that needs
// longer wants an asynchronous design (a job that polls), not a longer stall of
// the single writer.
const Default = 10 * time.Second

// HTTPClient returns an HTTP client bounded by [Default]. Connectors use this in
// place of http.DefaultClient, whose zero Timeout means "wait forever".
//
// http.Client.Timeout covers the entire call including reading the body, so a
// host that dribbles bytes cannot outlast it either.
func HTTPClient() *http.Client {
	return &http.Client{Timeout: Default}
}

// Connectors that do not speak HTTP (SMTP submission) apply [Default] themselves,
// as a net.Dialer timeout plus an absolute deadline on the accepted connection —
// dialing is only the first way a host can hang. See connector/mail's
// sendMailWithin.
