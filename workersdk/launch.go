package workersdk

import (
	"encoding/json"
	"fmt"
	"strings"
)

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
	// EnvConfig is the instance's configured Workers as JSON,
	// {"workers":[{"name":"…","config":{…}}]}: each configuration with its secrets
	// resolved and checked against the Worker Type's schema (ADR-0535 §4). Absent when
	// nothing is configured.
	EnvConfig = "ATLAS_WORKER_CONFIG"
)

// ConfiguredWorker is one configured Worker an instance serves.
type ConfiguredWorker struct {
	Name   string         `json:"name"`
	Config map[string]any `json:"config"`
}

// LaunchConfig reads the configured Workers of the launch contract from getenv. None
// configured is an empty list, not an error; a value that is not the contract's shape
// is an error, because serving on a misread configuration is worse than not starting.
func LaunchConfig(getenv func(string) string) ([]ConfiguredWorker, error) {
	raw := strings.TrimSpace(getenv(EnvConfig))
	if raw == "" {
		return nil, nil
	}
	var doc struct {
		Workers []ConfiguredWorker `json:"workers"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", EnvConfig, err)
	}
	return doc.Workers, nil
}

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
	// The instance reports the Workers it was configured with, so the Workers view
	// shows them and a job naming one is leased only to an instance holding it.
	if workers, err := LaunchConfig(getenv); err == nil {
		for _, w := range workers {
			opts.Connectors = append(opts.Connectors, w.Name)
		}
	}
	if id := get(EnvTypeID); id != "" {
		opts.Identity = &Identity{ID: id, Version: get(EnvTypeVersion), Digest: get(EnvTypeDigest)}
	}
	return opts
}
