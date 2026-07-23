package dmn

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ServiceResolver resolves a DMN reference handle against a temis model source
// reachable over HTTP: it GETs <BaseURL>/<modelRef>.dmn and returns the body. It
// is the networked alternative to [DirResolver] behind the same [Resolver]
// interface — a temis git host (raw file URLs) or a temis model service both fit
// this shape, so which source Atlas reads from is a deployment choice, not a code
// change (ADR-0034/0014).
//
// A 404 is reported as [ErrNotFound] (an unresolved, user-fixable reference); any
// other non-2xx response or a transport failure is returned as an infrastructure
// error, so a caller can tell "no such model" from "the model source is broken".
// A ServiceResolver is safe for concurrent use.
type ServiceResolver struct {
	// BaseURL is the model source root; the handle "risk-score" resolves to
	// <BaseURL>/risk-score.dmn.
	BaseURL string
	// Client is the HTTP client to use; nil uses http.DefaultClient.
	Client *http.Client
	// Token, if set, is sent as an "Authorization: Bearer <Token>" header — the
	// credential for a private temis service or git host.
	Token string
}

// Resolve fetches the model file for a handle from the service. A missing model
// (404) yields ErrNotFound; any other non-2xx status or a transport error is
// returned as-is so the caller reports it as an infrastructure failure, not an
// unresolved reference. The handle is validated exactly as DirResolver validates
// it, so it can never escape BaseURL's path.
func (r ServiceResolver) Resolve(ctx context.Context, modelRef string) ([]byte, error) {
	name, ok := safeModelRef(modelRef)
	if !ok {
		return nil, ErrNotFound
	}
	target, err := r.modelURL(name)
	if err != nil {
		return nil, fmt.Errorf("dmn: build model url for %q: %w", modelRef, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("dmn: request model %q: %w", modelRef, err)
	}
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dmn: fetch model %q: %w", modelRef, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("dmn: model service returned %s for %q", resp.Status, modelRef)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("dmn: read model %q: %w", modelRef, err)
	}
	return body, nil
}

// modelURL joins the validated handle onto BaseURL as "<base path>/<name>.dmn".
// The handle is already free of path separators (safeModelRef), so this cannot
// traverse outside the configured base path.
func (r ServiceResolver) modelURL(name string) (string, error) {
	base, err := url.Parse(r.BaseURL)
	if err != nil {
		return "", err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + name + ".dmn"
	return base.String(), nil
}
