package sharepoint

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// graphDefaultBase is the Microsoft Graph v1.0 API base a connector uses when it
// authors no endpoint override.
const graphDefaultBase = "https://graph.microsoft.com/v1.0"

// GraphClient creates SharePoint list items through the Microsoft Graph API
// (ADR-0141). It POSTs {fields:{…}} to /sites/{site}/lists/{list}/items with a bearer
// token from its TokenSource, and returns the created item as decoded JSON.
type GraphClient struct {
	http    *http.Client
	tokens  TokenSource
	baseURL string
}

// NewGraphClient builds a SharePoint Graph client. baseURL defaults to the Graph
// v1.0 API when empty.
func NewGraphClient(tokens TokenSource, baseURL string) *GraphClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = graphDefaultBase
	}
	return &GraphClient{http: http.DefaultClient, tokens: tokens, baseURL: baseURL}
}

// graphListItem is the create-item request body: a list item is created from its
// column values under the "fields" object (Graph's shape).
type graphListItem struct {
	Fields map[string]string `json:"fields"`
}

// CreateItem creates a list item in the connector's site/list and returns the
// created item decoded from Graph's JSON response. A missing site or list, or a
// non-2xx response, is an error so the job stays pending and is retried (at-least-once).
func (c *GraphClient) CreateItem(ctx context.Context, req ItemRequest) (any, error) {
	site := strings.TrimSpace(req.Site)
	if site == "" {
		return nil, fmt.Errorf("sharepoint: request has no site")
	}
	list := strings.TrimSpace(req.List)
	if list == "" {
		return nil, fmt.Errorf("sharepoint: request has no list")
	}
	fields := req.Fields
	if fields == nil {
		fields = map[string]string{}
	}
	endpoint := strings.TrimRight(c.baseURL, "/") +
		"/sites/" + url.PathEscape(site) +
		"/lists/" + url.PathEscape(list) + "/items"
	return c.postJSON(ctx, endpoint, graphListItem{Fields: fields})
}

// postJSON acquires a bearer token and POSTs a JSON payload to a Graph endpoint,
// returning the decoded response body. Any non-2xx status is an error (so a rejected
// create fails the job and is retried).
func (c *GraphClient) postJSON(ctx context.Context, endpoint string, payload any) (any, error) {
	tok, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("sharepoint: encode create item: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sharepoint: build create item request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+tok)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sharepoint: create item: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("sharepoint: create item returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep numeric ids/values exact through the variable round-trip
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("sharepoint: decode create item response: %w", err)
	}
	return out, nil
}
