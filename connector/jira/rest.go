package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// apiBase is the REST API version this connector speaks. See the package doc for why
// it is 2 and not 3: v3 requires an Atlassian Document Format tree where v2 takes a
// string, and a process writing one sentence into a description should not have to
// build a document tree to do it.
const apiBase = "/rest/api/2"

// searchPageSize is how many issues one search request asks for. Jira caps a page far
// lower than a large query's result set, so a search pages either way; asking for a
// round hundred keeps the request count low without asking for a response nobody
// wants in memory at once.
const searchPageSize = 100

// Connector is the server-side configuration of one Jira instance: the BaseURL (e.g.
// "https://acme.atlassian.net") and exactly one credential shape.
//
// Email and APIToken are Jira Cloud's: an account's address and an API token, sent as
// HTTP Basic, which is how Atlassian documents an API token. Token alone is a Jira
// Data Center personal access token, sent as a bearer. The values are resolved from
// the vault at build time and held only here, never persisted (I6).
type Connector struct {
	BaseURL  string
	Email    string
	APIToken string
	Token    string
}

// HTTPClient calls a real Jira over its REST API. It is stateless — the credential is
// sent per request rather than exchanged for a session — so it is safe for concurrent
// use by the worker.
type HTTPClient struct {
	conn Connector
	http *http.Client
}

// NewHTTPClient builds a Jira REST client for a configured connector, bounded by the
// shared connector call budget (ADR-0149). The worker may run on the run-loop
// goroutine, so an unbounded call would let a hung Jira stall the whole engine; see
// the nettimeout package doc.
func NewHTTPClient(conn Connector) *HTTPClient {
	conn.BaseURL = strings.TrimRight(strings.TrimSpace(conn.BaseURL), "/")
	return &HTTPClient{conn: conn, http: nettimeout.HTTPClient()}
}

// cloud reports whether this connector authenticates as Jira Cloud does. It is what
// decides both the authentication scheme and how an account is addressed when
// assigning an issue — the two follow from the same fact, so neither is guessed.
func (c *HTTPClient) cloud() bool { return strings.TrimSpace(c.conn.Token) == "" }

// Do performs one operation. Every failure — a transport error, a non-2xx status, an
// operation nothing implements — is returned so the job stays pending and is retried,
// then raises an incident (ADR-0061), rather than completing a token on work that did
// not happen.
func (c *HTTPClient) Do(ctx context.Context, req Request) (any, error) {
	switch req.Operation {
	case "create-issue":
		return c.call(ctx, http.MethodPost, apiBase+"/issue", map[string]any{"fields": c.createFields(req)}, req)
	case "get-issue":
		return c.call(ctx, http.MethodGet, apiBase+"/issue/"+url.PathEscape(req.Issue), nil, req)
	case "update-issue":
		return c.call(ctx, http.MethodPut, apiBase+"/issue/"+url.PathEscape(req.Issue), map[string]any{"fields": c.updateFields(req)}, req)
	case "transition-issue":
		return c.transition(ctx, req)
	case "add-comment":
		return c.call(ctx, http.MethodPost, apiBase+"/issue/"+url.PathEscape(req.Issue)+"/comment", map[string]any{"body": req.Comment}, req)
	case "assign-issue":
		field := "name" // Data Center addresses an account by username
		if c.cloud() {
			field = "accountId"
		}
		return c.call(ctx, http.MethodPut, apiBase+"/issue/"+url.PathEscape(req.Issue)+"/assignee", map[string]any{field: req.Assignee}, req)
	case "search":
		return c.search(ctx, req)
	default:
		return nil, fmt.Errorf("jira: unknown operation %q (want %s)", req.Operation, strings.Join(OpNames(), ", "))
	}
}

// createFields builds the field object an issue is created from: the project and the
// issue type wrapped as Jira wants them, the summary and description plain, and the
// model's extra fields merged in — those last, so a model can override anything this
// connector composed rather than be blocked by it.
func (c *HTTPClient) createFields(req Request) map[string]any {
	fields := map[string]any{
		"project":   idOrKey(req.Project, "key"),
		"issuetype": idOrKey(req.IssueType, "name"),
		"summary":   req.Summary,
	}
	if req.Description != "" {
		fields["description"] = req.Description
	}
	for k, v := range req.Fields {
		fields[k] = v
	}
	return fields
}

// updateFields builds the field object an update sends: only what the model actually
// changed. An update carrying every field would overwrite what somebody edited in Jira
// between the two steps of a process, which is the sort of loss nobody attributes to
// the workflow engine.
func (c *HTTPClient) updateFields(req Request) map[string]any {
	fields := map[string]any{}
	if req.Summary != "" {
		fields["summary"] = req.Summary
	}
	if req.Description != "" {
		fields["description"] = req.Description
	}
	for k, v := range req.Fields {
		fields[k] = v
	}
	return fields
}

// idOrKey addresses a project or an issue type the way Jira expects: by id when the
// authored value is all digits, otherwise by the named member (a project's key, an
// issue type's name). Jira accepts both forms and rejects the wrong member outright,
// so choosing here is what lets a model write "OPS" or "10000" and have either work.
func idOrKey(value, member string) map[string]any {
	if isDigits(value) {
		return map[string]any{"id": value}
	}
	return map[string]any{member: value}
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// transition moves an issue through its workflow, resolving a transition authored by
// name first. Jira's API takes only a transition id; a model that had to carry the id
// would be pinned to one workflow configuration, and the id is not what a person
// reading the process sees in Jira.
func (c *HTTPClient) transition(ctx context.Context, req Request) (any, error) {
	id := req.Transition
	if !isDigits(id) {
		resolved, err := c.transitionID(ctx, req)
		if err != nil {
			return nil, err
		}
		id = resolved
	}
	body := map[string]any{"transition": map[string]any{"id": id}}
	if req.Comment != "" {
		// Jira carries a comment on a transition in the update block, not the fields
		// block: it is an *add* to a list, which is why the shape is nested this way.
		body["update"] = map[string]any{"comment": []any{map[string]any{"add": map[string]any{"body": req.Comment}}}}
	}
	if len(req.Fields) > 0 {
		body["fields"] = req.Fields
	}
	return c.call(ctx, http.MethodPost, apiBase+"/issue/"+url.PathEscape(req.Issue)+"/transitions", body, req)
}

// transitionID looks up the transition whose name the model authored, matched without
// regard to case or surrounding space. A name that is not on offer is an error that
// lists the ones that are — the issue's current status decides them, so "no such
// transition" alone would send an operator looking in the wrong place.
func (c *HTTPClient) transitionID(ctx context.Context, req Request) (string, error) {
	raw, err := c.call(ctx, http.MethodGet, apiBase+"/issue/"+url.PathEscape(req.Issue)+"/transitions", nil, req)
	if err != nil {
		return "", err
	}
	envelope, _ := raw.(map[string]any)
	list, _ := envelope["transitions"].([]any)
	want := strings.ToLower(strings.TrimSpace(req.Transition))
	var available []string
	for _, entry := range list {
		t, _ := entry.(map[string]any)
		name, _ := t["name"].(string)
		if name == "" {
			continue
		}
		available = append(available, name)
		if strings.ToLower(name) != want {
			continue
		}
		switch id := t["id"].(type) {
		case string:
			return id, nil
		case json.Number:
			return id.String(), nil
		}
	}
	return "", fmt.Errorf("jira: issue %s has no transition named %q; from its current status it offers %s",
		req.Issue, req.Transition, strings.Join(available, ", "))
}

// search runs a JQL query and returns the matching issues, following Jira's paging
// until the model's cap is reached or the result set is exhausted. The paging
// envelope stays here: a model that had to unwrap {startAt,total,issues} and loop
// would be modelling the API rather than the work.
func (c *HTTPClient) search(ctx context.Context, req Request) (any, error) {
	issues := []any{}
	for {
		page := searchPageSize
		if req.MaxResults > 0 {
			remaining := int(req.MaxResults) - len(issues)
			if remaining <= 0 {
				break
			}
			if remaining < page {
				page = remaining
			}
		}
		body := map[string]any{"jql": req.JQL, "startAt": len(issues), "maxResults": page}
		raw, err := c.call(ctx, http.MethodPost, apiBase+"/search", body, req)
		if err != nil {
			return nil, err
		}
		envelope, _ := raw.(map[string]any)
		got, _ := envelope["issues"].([]any)
		issues = append(issues, got...)
		// Jira normally honours the page size asked of it, but the cap is the model's
		// statement about what may reach its result variable, so it is applied to what
		// arrived rather than trusted to the server.
		if req.MaxResults > 0 && len(issues) >= int(req.MaxResults) {
			return issues[:req.MaxResults], nil
		}
		// An empty page is the end of the result set whatever the total says: trusting
		// the total alone is how a query whose matches shrink mid-read loops forever.
		if len(got) == 0 {
			break
		}
		total, ok := envelope["total"].(json.Number)
		if !ok {
			break
		}
		n, err := total.Int64()
		if err != nil || int64(len(issues)) >= n {
			break
		}
	}
	return issues, nil
}

// jiraError is the error envelope Jira returns. Surfacing its messages is the
// difference between "HTTP 400" and "Field 'summary' is required", which is the whole
// of what an operator needs from a rejected call.
type jiraError struct {
	ErrorMessages []string          `json:"errorMessages"`
	Errors        map[string]string `json:"errors"`
}

// call performs one request and decodes the response. A 2xx with no body — which is
// what Jira answers an update, a transition and an assign with — returns nil rather
// than an error: the operation succeeded, and there is simply nothing to write back.
func (c *HTTPClient) call(ctx context.Context, method, path string, body any, req Request) (any, error) {
	if strings.TrimSpace(c.conn.BaseURL) == "" {
		return nil, fmt.Errorf("jira: connector has no base URL")
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("jira: encode %s request: %w", req.Operation, err)
		}
		payload = bytes.NewReader(encoded)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, c.conn.BaseURL+path, payload)
	if err != nil {
		return nil, fmt.Errorf("jira: build %s request: %w", req.Operation, err)
	}
	if c.cloud() {
		httpReq.SetBasicAuth(c.conn.Email, c.conn.APIToken)
	} else {
		httpReq.Header.Set("Authorization", "Bearer "+c.conn.Token)
	}
	httpReq.Header.Set("Accept", "application/json")
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if req.RequestID != "" {
		httpReq.Header.Set("X-Request-ID", req.RequestID)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("jira: %s: %w", req.Operation, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("jira: %s returned HTTP %d: %s", req.Operation, resp.StatusCode, describeError(raw))
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep issue ids and numeric field values exact through the variable round-trip
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("jira: decode %s response: %w", req.Operation, err)
	}
	return out, nil
}

// describeError renders Jira's error envelope, falling back to the raw body when the
// response is not one (a proxy's HTML error page, most often — which is itself the
// answer to "why is this failing").
func describeError(raw []byte) string {
	var e jiraError
	if err := json.Unmarshal(raw, &e); err == nil && (len(e.ErrorMessages) > 0 || len(e.Errors) > 0) {
		parts := append([]string{}, e.ErrorMessages...)
		for field, msg := range e.Errors {
			parts = append(parts, field+": "+msg)
		}
		sortStrings(parts)
		return strings.Join(parts, "; ")
	}
	text := strings.TrimSpace(string(raw))
	if len(text) > 500 {
		text = text[:500] + "…"
	}
	return text
}

// sortStrings keeps a multi-field error message stable across runs: Go's map
// iteration is randomized, and an error text that reorders itself is one a test
// cannot assert and an operator cannot compare between two incidents.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ProviderConfig is the per-connector data the server resolves before building a
// client: the Jira base URL (Endpoint) and the resolved Secret — the credential JSON
// bundle held in the vault under the connector's credentialsRef. The secret lives only
// here at build time, never in a model or an event (I6).
type ProviderConfig struct {
	Endpoint string
	Secret   string
}

// credentialBundle is the JSON an operator stores in the vault under a Jira
// connector's credentialsRef. There is deliberately no "method" field: which of the
// two shapes a bundle is, is already said by the fields it carries, and a method
// naming one while the fields say the other is a state with no right answer.
type credentialBundle struct {
	Email    string `json:"email,omitempty"`
	APIToken string `json:"apiToken,omitempty"`
	Token    string `json:"token,omitempty"`
}

// NewProviderClient builds the Jira client for a managed connector. A misconfigured
// connector returns an error so the caller can skip it — its tasks then park with that
// reason (ADR-0158) rather than calling Jira unauthenticated and being told nothing
// useful by a 401.
func NewProviderClient(cfg ProviderConfig) (Client, error) {
	base := strings.TrimSpace(cfg.Endpoint)
	if base == "" {
		return nil, fmt.Errorf("jira: connector has no base URL (set it to the Jira site, e.g. https://acme.atlassian.net)")
	}
	secret := strings.TrimSpace(cfg.Secret)
	if secret == "" {
		return nil, fmt.Errorf("jira: connector has no credential (set credentialsRef to a vault bundle {email, apiToken} for Jira Cloud or {token} for a Data Center personal access token)")
	}
	var b credentialBundle
	if err := json.Unmarshal([]byte(secret), &b); err != nil {
		return nil, fmt.Errorf("jira: credential is not valid JSON: %w", err)
	}
	switch {
	case strings.TrimSpace(b.Token) != "":
		return NewHTTPClient(Connector{BaseURL: base, Token: strings.TrimSpace(b.Token)}), nil
	case strings.TrimSpace(b.Email) != "" && strings.TrimSpace(b.APIToken) != "":
		return NewHTTPClient(Connector{BaseURL: base, Email: strings.TrimSpace(b.Email), APIToken: strings.TrimSpace(b.APIToken)}), nil
	default:
		return nil, fmt.Errorf("jira: credential is neither shape: a Jira Cloud bundle needs \"email\" and \"apiToken\", a Data Center bundle needs \"token\"")
	}
}
