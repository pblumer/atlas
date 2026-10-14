package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// apiClient is how the CLI's client commands — `atlas playground` and `atlas
// import` — talk to a running atlas: a base URL, an optional bearer token, and one
// request at a time whose failure carries the server's own words.
type apiClient struct {
	base   string
	bearer string
	http   http.Client
}

func newAPIClient(server, token string) apiClient {
	return apiClient{base: strings.TrimRight(server, "/"), bearer: strings.TrimSpace(token)}
}

// do makes one request with a JSON body, or none, and decodes the answer into out,
// which may be nil when the answer is not wanted.
func (c *apiClient) do(method, path string, body json.RawMessage, out any) error {
	contentType := ""
	if len(body) > 0 {
		contentType = "application/json"
	}
	return c.send(method, path, contentType, body, out)
}

// send makes one request with a body of any type. A non-2xx carries the server's
// own message, because a CI log that says only "400" costs somebody an afternoon.
func (c *apiClient) send(method, path, contentType string, body []byte, out any) error {
	var rdr io.Reader
	if len(body) > 0 {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("%s %s: read response: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, serverMessage(raw))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: decode response: %w", method, path, err)
	}
	return nil
}

// serverMessage is what a refusal says, in the first of the shapes the API answers
// one in that it carries: an error, a list of problems — one line each, since an
// import refused for five reasons should name all five — or a reason. Anything else
// is shown as it came.
func serverMessage(raw []byte) string {
	var e struct {
		Error    string `json:"error"`
		Reason   string `json:"reason"`
		Problems []struct {
			Subject string `json:"subject"`
			Problem string `json:"problem"`
		} `json:"problems"`
	}
	if json.Unmarshal(raw, &e) == nil {
		switch {
		case e.Error != "":
			return e.Error
		case len(e.Problems) > 0:
			lines := make([]string, 0, len(e.Problems))
			for _, p := range e.Problems {
				lines = append(lines, "\n  "+p.Subject+": "+p.Problem)
			}
			return "refused:" + strings.Join(lines, "")
		case e.Reason != "":
			return e.Reason
		}
	}
	return strings.TrimSpace(string(raw))
}
