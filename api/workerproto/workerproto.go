// Package workerproto is the server's half of the Worker protocol handshake
// (ADR-0535 §3): which protocol versions this server speaks, and the Worker Type a
// Worker Instance reports running.
//
// A Worker Instance names both on every poll — the job poll and the query poll — so a
// server that restarted, or an instance that was upgraded underneath it, is never
// working from a stale answer. Nothing here is durable: the handshake is runtime
// topology, kept in the Workers view and gone with a restart, like the rest of what a
// poll reports.
//
// # Compatibility rule
//
// The protocol has a major version. Within a major version every change is additive:
// a new request field is optional and ignored by a server that does not know it, a new
// response field is ignored by a client that does not know it. A change a client or a
// server could not ignore is a new major version.
//
// A server accepts the current major version and the one before it, so a fleet of
// Worker Instances can be upgraded after the server rather than with it, for at least
// one release. A poll that names no version is version 1: that is every client from
// before the handshake. A poll that names a version the server does not accept is
// refused with 400 naming the versions it does, and every answer carries those
// versions in [Header], so a newer client can tell an older server from a broken one.
package workerproto

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Current is the protocol major version this server speaks natively.
const Current = 1

// supported are the major versions this server accepts: Current and, once there is
// one, Current-1. Ordered ascending.
var supported = []int{1}

// Header names the response header listing the supported versions, comma-separated.
const Header = "X-Atlas-Worker-Protocol"

// HeaderValue is [Header]'s value on every answer to a Worker poll.
var HeaderValue = func() string {
	parts := make([]string, len(supported))
	for i, v := range supported {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}()

// Supported returns the accepted major versions, ascending.
func Supported() []int { return slices.Clone(supported) }

// Check accepts or refuses the protocol version a poll named. Zero is a poll that
// named none, which is version 1.
func Check(v int) error {
	if v == 0 {
		v = 1
	}
	if slices.Contains(supported, v) {
		return nil
	}
	return fmt.Errorf("worker protocol %d is not spoken here; this server accepts %s (header %s)", v, HeaderValue, Header)
}

// Identity is the Worker Type a Worker Instance reports running: the manifest's id
// and version, and the digest of its artifact where the instance knows it.
type Identity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest,omitempty"`
}
