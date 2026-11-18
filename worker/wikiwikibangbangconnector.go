package worker

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/connector/wikiwikibangbang"
	"github.com/pblumer/atlas/internal/trustroots"
)

// wikiEnvPrefix is where a wikiwikibangbang worker's wikis live.
const wikiEnvPrefix = "ATLAS_WIKIWIKIBANGBANG_"

// wikiRegistryFromEnv builds the wikis this worker holds. ATLAS_WIKIWIKIBANGBANG_CONNECTORS
// lists the names; each name contributes ATLAS_WIKIWIKIBANGBANG_<NAME>_URL — the wiki's
// base URL — and ATLAS_WIKIWIKIBANGBANG_<NAME>_CREDENTIALS, the bundle
// [wikiwikibangbang.Bundle] describes.
//
// The credential arrives as the whole bundle, one opaque JSON value, for CardDAV's
// reason: [wikiwikibangbang.NewClient] is the one place that reads it, here as in the
// server's supervision check. It comes from the environment and not from a flag because
// argv is readable by anyone who can list processes.
func wikiRegistryFromEnv(env func(string) string) (*wikiwikibangbang.Registry, []string, error) {
	names := splitAndTrim(env(wikiEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured: a nil registry and no error. A *named* wiki
		// that does not build, below, is still an error.
		return nil, nil, nil
	}
	opts, err := wikiClientOptions(env)
	if err != nil {
		return nil, nil, err
	}
	reg := wikiwikibangbang.NewRegistry()
	for _, name := range names {
		key := wikiEnvPrefix + envFold(name) + "_"
		c, err := wikiwikibangbang.NewClient(env(key+"URL"), env(key+"CREDENTIALS"), opts)
		if err != nil {
			return nil, nil, fmt.Errorf("worker: wikiwikibangbang worker %q is not usable: %w (set %sURL to the wiki's base URL and %sCREDENTIALS to the credential bundle)", name, err, key, key)
		}
		reg.Register(name, c)
	}
	return reg, names, nil
}

// wikiCAEnv names the operator's CA bundle: the variable --tls-ca defaults to, which
// the worker's command line hands on where the flag was given instead.
const wikiCAEnv = "ATLAS_TLS_CA"

// wikiClientOptions are the client options every wiki of this worker shares. Where the
// operator named certificate authorities of their own (--tls-ca), the wikis are
// verified against the host's roots and those besides
// (ADR-draft-tls-ca-reaches-the-wiki): a wiki is the operator's own server, as a peer
// Atlas is, and inside an organisation its certificate usually comes from the
// organisation's authority, which no host knows. The client keeps the bound every
// Worker's outbound call has (nettimeout); without a bundle it is that shared client,
// exactly as before.
func wikiClientOptions(env func(string) string) (wikiwikibangbang.Options, error) {
	roots, err := trustroots.Load(env(wikiCAEnv))
	if err != nil {
		return wikiwikibangbang.Options{}, fmt.Errorf("worker: the wikis cannot be verified against the operator's certificate authorities: %w", err)
	}
	if roots == nil {
		return wikiwikibangbang.Options{}, nil
	}
	// The process-wide transport, which carries the outbound proxy this installation
	// uses (ADR-0464), with nothing changed but whom it trusts.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	client := nettimeout.HTTPClient()
	client.Transport = transport
	return wikiwikibangbang.Options{HTTPClient: client}, nil
}

// RunWikiJob performs a resolved wikiwikibangbang task through a registry the caller
// owns. It is exported for the reason RunCardDAVJob is: a caller embedding this package
// can build a registry from its own secret store and get the identical call.
func RunWikiJob(ctx context.Context, j Job, reg *wikiwikibangbang.Registry) (map[string]any, error) {
	if j.Connector == nil {
		// The server resolves a task's values when it hands the job out and sends nothing
		// when it cannot — a propertiesVariable that is not set, blocks that are no list —
		// as well as when it predates this Worker Type.
		return nil, fmt.Errorf("wikiwikibangbang: the job carried no resolved task; the server could not evaluate its values (is the properties variable set, and an object? do the blocks evaluate to a list?) or does not resolve wikiwikibangbang tasks")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task wikiwikibangbang.Job
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&task); err != nil {
		return nil, fmt.Errorf("wikiwikibangbang: cannot read the resolved detail: %w", err)
	}
	return wikiwikibangbang.Run(ctx, task, reg)
}
