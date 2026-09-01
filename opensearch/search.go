package opensearch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Reading back what was exported (ADR-0114, ADR-0189 P5b).
//
// The exporter writes Atlas's durable event log into an index. Nothing until now
// read it back: the index was an archive for somebody else's tooling. Panorama's
// historical context (ADR-0189 P5b) is the first Atlas surface to ask it a
// question, and it asks the same cluster under the same configuration — a second
// endpoint for reading what this one wrote would be two sources of truth about one
// index.
//
// The split of responsibility is deliberate. This file moves bytes: it builds the
// request, bounds the response, and turns a transport or status failure into an
// error a caller can tell apart. It does not know what a Panorama element is, what
// an aggregation means, or which measures matter — that belongs to the caller,
// which is what keeps this package free of the API's vocabulary.

// maxSearchBytes bounds a search response. A query this package sends is an
// aggregation with no documents requested, so a correct answer is small; a large
// body means a cluster answering something other than what was asked, and reading
// it into memory to discover that is the failure mode this prevents.
const maxSearchBytes = 1 << 20

// ErrSearchRefused is returned when the cluster answered and declined: it is
// reachable and this server may not have what it asked for. Callers separate it
// from a transport failure because the two send an operator to different places —
// one to the credentials, the other to the network.
var ErrSearchRefused = fmt.Errorf("opensearch: search refused")

// Searcher runs one search against an index. It is an interface so a caller is
// testable without a live cluster, exactly as [Client] is.
type Searcher interface {
	// Search posts query (an OpenSearch query DSL body) to index/_search and returns
	// the raw response body. The context's deadline is the caller's bound on how long
	// somebody else's cluster may hold up this request.
	Search(ctx context.Context, index string, query []byte) ([]byte, error)
}

// Search implements [Searcher] against a real cluster.
//
// It sends no `_source` and requests no hits — every caller here wants an
// aggregation — so what comes back is bounded by the aggregation's own shape
// rather than by the size of the index.
func (c *HTTPClient) Search(ctx context.Context, index string, query []byte) ([]byte, error) {
	if index == "" {
		index = DefaultIndex
	}
	url := strings.TrimRight(c.cfg.URL, "/") + "/" + index + "/_search"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(query))
	if err != nil {
		return nil, fmt.Errorf("opensearch: build search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.Username != "" || c.cfg.Password != "" {
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The address is deliberately not carried out of here. A context surface is
		// readable by anyone who may read the model, and an endpoint is infrastructure
		// they were not necessarily told about (ADR-0189 §6 disclosure).
		return nil, fmt.Errorf("opensearch: search request failed")
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, ErrSearchRefused
	case resp.StatusCode == http.StatusNotFound:
		// An index that does not exist is not a failure to report as one: nothing has
		// been exported yet, or it was rotated away. The caller renders that as an
		// empty answer, which is what it is.
		return nil, nil
	case resp.StatusCode/100 != 2:
		return nil, fmt.Errorf("opensearch: search returned HTTP %d", resp.StatusCode)
	}

	// Read one byte past the bound, so a body exactly at it is not mistaken for a
	// truncated one.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBytes+1))
	if err != nil {
		return nil, fmt.Errorf("opensearch: read search response: %w", err)
	}
	if len(body) > maxSearchBytes {
		return nil, fmt.Errorf("opensearch: search response exceeds %d bytes", maxSearchBytes)
	}
	return body, nil
}
