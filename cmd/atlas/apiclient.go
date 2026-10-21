package main

import (
	"github.com/pblumer/atlas/api/apppkg"

	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// apiClient is how the CLI's client commands — `atlas playground`, `atlas import`
// and `atlas export` — talk to a running atlas: a base URL, an optional bearer token, and one
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
	raw, err := c.exchange(method, path, contentType, body)
	if err != nil || out == nil || len(raw) == 0 {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: decode response: %w", method, path, err)
	}
	return nil
}

// fetch makes one GET and answers the body as it came: an archive, or a document
// read as data rather than decoded into a type that would drop what it does not
// name.
func (c *apiClient) fetch(path string) ([]byte, error) {
	return c.exchange(http.MethodGet, path, "", nil)
}

// exchange makes one request and answers the body of a 2xx.
func (c *apiClient) exchange(method, path, contentType string, body []byte) ([]byte, error) {
	var rdr io.Reader
	if len(body) > 0 {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("%s %s: read response: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &statusError{status: resp.StatusCode,
			msg: fmt.Sprintf("%s %s: %s: %s", method, path, resp.Status, serverMessage(raw))}
	}
	return raw, nil
}

// statusError is a refusal the server answered, with its status, so a caller can
// tell "not allowed" from "broken" without reading the message.
type statusError struct {
	status int
	msg    string
}

func (e *statusError) Error() string { return e.msg }

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

// directory is the server's directory in a package's terms (apppkg.DirectoryEntry):
// its groups and users by id, as GET /api/v1/principals lists them, and its accounts
// by username, as GET /api/v1/users/assignable lists them — a fixed approver is an
// account, because the approval becomes a task assigned to that username.
func (c *apiClient) directory() ([]directoryEntry, error) {
	var dir []directoryEntry
	if err := c.do(http.MethodGet, "/api/v1/principals", nil, &dir); err != nil {
		return nil, err
	}
	var accounts []struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
	}
	if err := c.do(http.MethodGet, "/api/v1/users/assignable", nil, &accounts); err != nil {
		return nil, err
	}
	for _, a := range accounts {
		name := a.DisplayName
		if name == "" {
			name = a.Username
		}
		dir = append(dir, directoryEntry{Type: apppkg.KindAccount, ID: a.Username, Name: name})
	}
	return dir, nil
}
