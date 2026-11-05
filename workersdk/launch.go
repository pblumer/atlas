package workersdk

import "strings"

// The launch contract: what atlas hands a Worker Type executable it launches from an
// installed artifact (ADR-0535 §2). The executable is started with no arguments and
// this environment, and nothing else of the server's; it reads its own configuration
// from what it is given here and from the Worker protocol.
const (
	// EnvServer is the base URL of the atlas the instance works for.
	EnvServer = "ATLAS_SERVER_URL"
	// EnvWorkerID is the instance's id in the Workers view.
	EnvWorkerID = "ATLAS_WORKER_ID"
	// EnvToken is the bearer credential the instance authenticates with.
	EnvToken = "ATLAS_TOKEN"
	// EnvTypeID, EnvTypeVersion and EnvTypeDigest are the installed release the
	// supervisor verified before it started this process: the handshake's identity.
	EnvTypeID      = "ATLAS_WORKER_TYPE_ID"
	EnvTypeVersion = "ATLAS_WORKER_TYPE_VERSION"
	EnvTypeDigest  = "ATLAS_WORKER_TYPE_DIGEST"
)

// LaunchOptions reads the launch contract from getenv (os.Getenv in a real process):
// the server, the instance id, the token and the identity. A Worker Type adds its
// Handlers and Queries and passes the result to [New].
func LaunchOptions(getenv func(string) string) Options {
	get := func(k string) string { return strings.TrimSpace(getenv(k)) }
	opts := Options{
		Server: strings.TrimRight(get(EnvServer), "/"),
		ID:     get(EnvWorkerID),
		Token:  get(EnvToken),
	}
	if id := get(EnvTypeID); id != "" {
		opts.Identity = &Identity{ID: id, Version: get(EnvTypeVersion), Digest: get(EnvTypeDigest)}
	}
	return opts
}
