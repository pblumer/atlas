// Package opensearch exports Atlas's durable event history to an OpenSearch (or
// API-compatible Elasticsearch) index. It is a WAL-tailing sink (ADR-0114): an
// [Exporter] tails the committed event log off the processor goroutine and
// bulk-indexes each record, so the engine's hot path is untouched (invariant I1)
// and a record is exported only once it is durable (invariant I2). Delivery is
// at-least-once and idempotent — each record's document id is its globally-unique
// log position, so a retry after a crash or a transient failure overwrites rather
// than duplicates.
//
// The sink is opt-in and server-configured (endpoint, credentials, index), never
// authored in a model — mirroring the clio connector's shape (ADR-0036). An empty
// endpoint disables it.
package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DefaultIndex is the OpenSearch index events are written to when the config does
// not name one.
const DefaultIndex = "atlas-events"

// Item is one document to index: a stable, idempotent id (the record's log
// position) and its JSON body.
type Item struct {
	ID   string
	Body []byte
}

// Client indexes documents into OpenSearch. It is an interface so the [Exporter]
// is testable without a live cluster and so a different transport can be swapped
// in.
type Client interface {
	// Bulk indexes items into index using the _bulk API, in one request. It must
	// return an error if the request fails or the cluster reports any per-item
	// failure, so an at-least-once caller retries the whole batch (safe because
	// document ids are stable).
	Bulk(ctx context.Context, index string, items []Item) error
}

// Config is the server-side configuration of the OpenSearch sink. URL is the
// cluster base URL (e.g. "https://opensearch:9200"); an empty URL disables the
// exporter. Username/Password are optional HTTP basic-auth credentials. Index is
// the target index (defaults to [DefaultIndex]).
type Config struct {
	URL      string
	Username string
	Password string
	Index    string
}

// Enabled reports whether a URL is configured — the opt-in switch (ADR-0114).
func (c Config) Enabled() bool { return strings.TrimSpace(c.URL) != "" }

// HTTPClient talks to a real OpenSearch cluster over its HTTP _bulk API.
type HTTPClient struct {
	cfg  Config
	http *http.Client
}

// NewHTTPClient builds an OpenSearch client for a configured sink. It uses a
// bounded-timeout HTTP client so a stalled cluster cannot wedge the export loop.
func NewHTTPClient(cfg Config) *HTTPClient {
	return &HTTPClient{cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}}
}

// bulkResponse is the subset of the _bulk reply we inspect: a top-level errors
// flag, and per-item results carried under an action key ("index") so a failing
// document can be named in the returned error.
type bulkResponse struct {
	Errors bool `json:"errors"`
	Items  []map[string]struct {
		Status int `json:"status"`
		Error  struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		} `json:"error"`
	} `json:"items"`
}

// Bulk sends items as one newline-delimited _bulk request. Each item contributes
// two lines: an index action naming the target index and the stable document id,
// then the document body. A non-2xx status, or a 2xx whose body reports
// item-level errors, is returned as an error so the caller retries the batch.
func (c *HTTPClient) Bulk(ctx context.Context, index string, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, it := range items {
		action, err := json.Marshal(map[string]any{
			"index": map[string]any{"_index": index, "_id": it.ID},
		})
		if err != nil {
			return fmt.Errorf("opensearch: encode bulk action: %w", err)
		}
		buf.Write(action)
		buf.WriteByte('\n')
		buf.Write(it.Body)
		buf.WriteByte('\n')
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.cfg.URL, "/")+"/_bulk", &buf)
	if err != nil {
		return fmt.Errorf("opensearch: build bulk request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if c.cfg.Username != "" || c.cfg.Password != "" {
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("opensearch: bulk request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("opensearch: bulk returned HTTP %d", resp.StatusCode)
	}
	var br bulkResponse
	if err := json.NewDecoder(resp.Body).Decode(&br); err != nil {
		return fmt.Errorf("opensearch: decode bulk response: %w", err)
	}
	if br.Errors {
		return fmt.Errorf("opensearch: bulk reported item errors: %s", firstItemError(br))
	}
	return nil
}

// firstItemError renders the first failing item's error for a diagnostic message.
func firstItemError(br bulkResponse) string {
	for _, item := range br.Items {
		for _, res := range item {
			if res.Error.Type != "" || res.Error.Reason != "" {
				return fmt.Sprintf("%s: %s", res.Error.Type, res.Error.Reason)
			}
		}
	}
	return "unspecified"
}
