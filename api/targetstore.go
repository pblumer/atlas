package api

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
)

// deploymentTarget is another Atlas server this one can promote a release to
// (ADR-0129). It is operator configuration in the same category as managed
// connectors (ADR-0041) and per-server call-activity overrides (ADR-0105): it
// belongs to this server, not to any one application, and applications merely
// reference it.
//
// Like a connector, it stores only a *reference* to its credential, never the
// secret: CredentialRef names a vault entry (ADR-0069/0070) that holds the peer's
// deploy token. The token itself is resolved at promotion time, used for one
// request, and never written anywhere.
type deploymentTarget struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	BaseURL       string `json:"baseUrl"`
	Kind          string `json:"kind,omitempty"` // free operator label, e.g. "prod"
	CredentialRef string `json:"credentialRef,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
	// Bindings maps a *local* application id to the id the same application has on
	// this target, learned from the remote's reply on the first successful
	// promotion (ADR-0129 option C1). The two servers keep their own ids; this is
	// how the publisher addresses the application over there afterwards.
	Bindings map[string]string `json:"bindings,omitempty"`
}

// validateTargetURL checks a target's base URL and returns it normalized.
//
// A target is a trust relationship, so plaintext is refused: a deploy token
// presented over http:// is a credential handed to anyone on the path. Loopback is
// the one exception — it is what a single-host setup and the tests need, and it
// does not cross a network.
//
// There is deliberately no "skip TLS verification" option anywhere in this file:
// it would be the first thing reached for when a certificate is wrong, which is
// exactly when it must not be available.
func validateTargetURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("base URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("base URL is not a valid URL: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("base URL must include a host")
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		if !isLoopbackHost(host) {
			return "", fmt.Errorf("base URL must use https (plaintext is allowed only for loopback)")
		}
	default:
		return "", fmt.Errorf("base URL must use https")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// isLoopbackHost reports whether a hostname denotes this machine.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// targetStore is a durable store for deployment targets, one JSON file per id under
// a single directory — the same sidecar approach as the deployment, project, and
// release stores. Owned solely by the run-loop goroutine, so it needs no locking.

// targetStore is a durable store for deploymentTarget records, one JSON file per id
// under a single directory (ADR-0019). Like every design-time store it is owned
// solely by the server's run-loop goroutine, so it needs no locking of its own.
type targetStore = sidecar.Store[deploymentTarget]

// newTargetStore opens (creating if needed) the target directory.
func newTargetStore(dir string) (*targetStore, error) {
	return sidecar.NewStore(dir, "targetstore",
		func(rec deploymentTarget) string { return rec.ID },
		sidecar.Order(func(a, b deploymentTarget) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
}
