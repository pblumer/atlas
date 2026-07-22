package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a thin HTTP client for the Atlas server API (see the api package).
// The MCP server owns no engine state of its own; every tool call is translated
// into an HTTP request against a running Atlas server, which remains the single
// writer of its partition (invariant I3). That keeps the MCP surface a pure
// adapter — it can never violate an engine invariant because it never touches
// the engine directly.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient builds a Client for the Atlas server at baseURL (e.g.
// "http://localhost:8080"). A trailing slash is tolerated.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// apiError carries the {"error": "..."} body Atlas returns on a 4xx/5xx so tool
// handlers can surface the server's own message rather than a bare status code.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("atlas server returned status %d", e.Status)
	}
	return fmt.Sprintf("atlas server error (%d): %s", e.Status, e.Message)
}

// get issues a GET and returns the raw response body on 2xx, or an *apiError.
func (c *Client) get(path string) ([]byte, error) {
	return c.do(http.MethodGet, path, "", nil)
}

// post issues a POST with the given content type and body.
func (c *Client) post(path, contentType string, body []byte) ([]byte, error) {
	return c.do(http.MethodPost, path, contentType, body)
}

func (c *Client) do(method, path, contentType string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach atlas server at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &apiError{Status: resp.StatusCode, Message: extractError(data)}
	}
	return data, nil
}

// extractError pulls the "error" field out of an Atlas JSON error body, falling
// back to the raw (trimmed) body when it isn't the expected shape.
func extractError(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(body))
}
