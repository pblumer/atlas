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
// workers (ADR-0041) and per-server call-activity overrides (ADR-0105): it
// belongs to this server, not to any one application, and applications merely
// reference it.
//
// Like a worker, it stores only a *reference* to its credential, never the
// secret: CredentialRef names a vault entry (ADR-0069/0070) that holds the peer's
// deploy token. The token itself is resolved at promotion time, used for one
// request, and never written anywhere.
type deploymentTarget struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	BaseURL       string `json:"baseUrl"`
	Kind          string `json:"kind,omitempty"` // free operator label, e.g. "prod"
	CredentialRef string `json:"credentialRef,omitempty"`
	// ReadCredentialRef names the vault entry a *read* of this peer presents, where it
	// differs from the one a promotion presents. Empty means the two are the same
	// credential, which is what every target configured before this had.
	//
	// Two references rather than one, because the two jobs a target's credential does are
	// disjoint by design and a token carries one scope. Promotion needs the import route
	// and nothing else — ADR-0129's deploy token is deliberately the narrowest thing that
	// can publish. Reading a peer needs its descriptor and, for the estate altitude
	// (ADR-0402), its derived landscape, and a credential
	// that reaches those is refused the import route. Measured against two installations:
	// a deploy token answers 401 at both read routes, and a landscape credential answers
	// 403 at the import route. So a target configured for promotion — which is what a
	// target is for — was drawn on the estate as unreachable, and the picture was honest
	// about knowing nothing while an operator could see the peer was plainly there.
	//
	// Both are handles into the vault and never secrets, so a second one discloses no more
	// than the first (ADR-0069/0070).
	ReadCredentialRef string `json:"readCredentialRef,omitempty"`
	CreatedAt         int64  `json:"createdAt"`
	// Bindings maps a *local* application id to the id the same application has on
	// this target, learned from the remote's reply on the first successful
	// promotion (ADR-0129 option C1). The two servers keep their own ids; this is
	// how the publisher addresses the application over there afterwards.
	Bindings map[string]string `json:"bindings,omitempty"`
}

// readRef is the reference a read of this peer presents: the read credential where one is
// configured, and otherwise the promotion credential.
//
// The fallback is what makes this change invisible to every target that already exists. A
// target with one reference keeps presenting it for both jobs, exactly as before — including
// the case where that one credential is a status token an operator configured so the landscape
// could draw the peer at all.
func (t deploymentTarget) readRef() string {
	if ref := strings.TrimSpace(t.ReadCredentialRef); ref != "" {
		return ref
	}
	return t.CredentialRef
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
