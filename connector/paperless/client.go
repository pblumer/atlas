// Package paperless provides metadata-only Paperless-ngx operations for external workers.
// Raw document API responses may contain OCR and highlights: they are never returned.
package paperless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/limits"
)

// Options controls a worker's outbound transport.
type Options struct {
	HTTPClient *http.Client
	AllowHTTP  bool
}

// Client holds endpoint and token exclusively on the worker side of the boundary.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

// NewClient configures a Paperless-ngx API client; TLS is mandatory by default.
func NewClient(endpoint, token string, opts Options) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(opts.AllowHTTP && u.Scheme == "http")) {
		return nil, errors.New("paperless: invalid server endpoint (HTTPS required unless explicitly permitted)")
	}
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("paperless: missing or invalid API token")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	hc := &http.Client{Timeout: 15 * time.Second}
	if opts.HTTPClient != nil {
		copy := *opts.HTTPClient
		hc = &copy
		if hc.Timeout == 0 {
			hc.Timeout = 15 * time.Second
		}
	}
	// Redirects would otherwise risk giving a credential to another origin.
	hc.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: u, token: token, http: hc}, nil
}

// Request is a resolved, credential-free and binary-free Paperless operation.
type Request struct {
	Operation         string `json:"operation"`
	DocumentID        int    `json:"documentId,omitempty"`
	Query             string `json:"query,omitempty"`
	Page              int    `json:"page,omitempty"`
	Name              string `json:"name,omitempty"`
	Match             string `json:"match,omitempty"`
	MatchingAlgorithm int    `json:"matchingAlgorithm,omitempty"`
	IsInsensitive     *bool  `json:"isInsensitive,omitempty"`
}

// Document is a projection of allowed metadata; OCR and notes are intentionally absent.
type Document struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	Created       string `json:"created,omitempty"`
	Modified      string `json:"modified,omitempty"`
	Filename      string `json:"filename,omitempty"`
	Tags          []int  `json:"tags,omitempty"`
	Correspondent *int   `json:"correspondent,omitempty"`
	DocumentType  *int   `json:"documentType,omitempty"`
}

type documentWire struct {
	ID               int    `json:"id"`
	Title            string `json:"title"`
	Created          string `json:"created"`
	Modified         string `json:"modified"`
	OriginalFileName string `json:"original_file_name"`
	Tags             []int  `json:"tags"`
	Correspondent    *int   `json:"correspondent"`
	DocumentType     *int   `json:"document_type"`
}

func (d documentWire) project() Document {
	return Document{ID: d.ID, Title: d.Title, Created: d.Created, Modified: d.Modified, Filename: d.OriginalFileName, Tags: d.Tags, Correspondent: d.Correspondent, DocumentType: d.DocumentType}
}

// DocumentPage is one bounded search page without content or snippets.
type DocumentPage struct {
	Count     int        `json:"count"`
	Page      int        `json:"page"`
	Documents []Document `json:"documents"`
	HasNext   bool       `json:"hasNext"`
}

// Presentation is an authenticated Paperless UI route, not a bearer share link.
type Presentation struct {
	ID  int    `json:"id"`
	URL string `json:"url"`
}

// Correspondent contains only its stable ID and name.
type Correspondent struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// CorrespondentResult reports whether this attempt performed the creation.
type CorrespondentResult struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Created bool   `json:"created"`
}

// Do executes a named operation, returning only intentionally allowed fields.
func (c *Client) Do(ctx context.Context, r Request) (any, error) {
	switch r.Operation {
	case "get-document", "present-document":
		if r.DocumentID <= 0 {
			return nil, errors.New("paperless: documentId must be positive")
		}
		var wire documentWire
		status, err := c.request(ctx, http.MethodGet, "api/documents/"+strconv.Itoa(r.DocumentID)+"/", nil, nil, &wire)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, statusError(status)
		}
		if r.Operation == "get-document" {
			return wire.project(), nil
		}
		if wire.ID != r.DocumentID {
			return nil, errors.New("paperless: inconsistent document ID")
		}
		u := *c.base
		u.Path = strings.TrimRight(c.base.Path, "/") + "/documents/" + strconv.Itoa(r.DocumentID) + "/details"
		return Presentation{ID: r.DocumentID, URL: u.String()}, nil
	case "search-documents":
		if strings.TrimSpace(r.Query) == "" {
			return nil, errors.New("paperless: search query is required")
		}
		page, err := validatePage(r.Page)
		if err != nil {
			return nil, err
		}
		q := url.Values{"text": {strings.TrimSpace(r.Query)}, "page_size": {"25"}, "page": {strconv.Itoa(page)}}
		var raw struct {
			Count   int             `json:"count"`
			Next    json.RawMessage `json:"next"`
			Results []documentWire  `json:"results"`
		}
		status, err := c.request(ctx, http.MethodGet, "api/documents/", q, nil, &raw)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, statusError(status)
		}
		if len(raw.Results) > 25 {
			return nil, errors.New("paperless: search page exceeded limit")
		}
		out := DocumentPage{Count: raw.Count, Page: page, Documents: make([]Document, 0, len(raw.Results)), HasNext: hasNext(raw.Next)}
		for _, d := range raw.Results {
			out.Documents = append(out.Documents, d.project())
		}
		return out, nil
	case "list-correspondents":
		page, err := validatePage(r.Page)
		if err != nil {
			return nil, err
		}
		all, _, err := c.correspondents(ctx, page, 25)
		if err != nil {
			return nil, err
		}
		out := make([]Correspondent, 0, len(all))
		for _, item := range all {
			out = append(out, Correspondent{ID: item.ID, Name: item.Name})
		}
		return out, nil
	case "add-correspondent":
		return c.addCorrespondent(ctx, r)
	default:
		return nil, fmt.Errorf("paperless: unsupported operation %q", r.Operation)
	}
}

func validatePage(page int) (int, error) {
	if page == 0 {
		return 1, nil
	}
	if page < 1 || page > 10000 {
		return 0, errors.New("paperless: invalid page")
	}
	return page, nil
}

func hasNext(v json.RawMessage) bool { return len(v) > 0 && string(v) != "null" && string(v) != "\"\"" }

func (c *Client) request(ctx context.Context, method, path string, query url.Values, body any, dst any) (int, error) {
	u := *c.base
	u.Path = strings.TrimRight(c.base.Path, "/") + "/" + strings.TrimLeft(path, "/")
	u.RawQuery = query.Encode()
	var data io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, errors.New("paperless: cannot encode request")
		}
		data = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), data)
	if err != nil {
		return 0, errors.New("paperless: cannot prepare request")
	}
	req.Header.Set("Accept", "application/json; version=10")
	req.Header.Set("Authorization", "Token "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("paperless: provider request failed: %w", sanitizeNetworkError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, nil
	}
	if dst == nil {
		return resp.StatusCode, nil
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, limits.Default().Payload+1))
	if err := dec.Decode(dst); err != nil {
		return 0, errors.New("paperless: invalid or excessive API response")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return 0, errors.New("paperless: trailing or excessive API response")
	}
	return resp.StatusCode, nil
}

func sanitizeNetworkError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return errors.New("network or TLS error")
}

func statusError(status int) error { return fmt.Errorf("paperless: provider returned HTTP %d", status) }

type correspondentWire struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	Match             string `json:"match"`
	MatchingAlgorithm int    `json:"matching_algorithm"`
	IsInsensitive     bool   `json:"is_insensitive"`
}

func (c *Client) correspondents(ctx context.Context, page, size int) ([]correspondentWire, bool, error) {
	q := url.Values{"page_size": {strconv.Itoa(size)}, "page": {strconv.Itoa(page)}}
	var out struct {
		Next    json.RawMessage     `json:"next"`
		Results []correspondentWire `json:"results"`
	}
	status, err := c.request(ctx, http.MethodGet, "api/correspondents/", q, nil, &out)
	if err != nil {
		return nil, false, err
	}
	if status != http.StatusOK {
		return nil, false, statusError(status)
	}
	if len(out.Results) > size {
		return nil, false, errors.New("paperless: correspondents page exceeded limit")
	}
	return out.Results, hasNext(out.Next), nil
}

func (c *Client) findCorrespondent(ctx context.Context, name string) (*correspondentWire, error) {
	var found *correspondentWire
	for page := 1; page <= 50; page++ {
		entries, next, err := c.correspondents(ctx, page, 100)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if strings.EqualFold(e.Name, name) {
				if found != nil {
					return nil, errors.New("paperless: correspondent name is ambiguous")
				}
				copy := e
				found = &copy
			}
		}
		if !next {
			return found, nil
		}
	}
	return nil, errors.New("paperless: correspondent search requires more than 50 pages; refusing duplicate creation")
}

func (c *Client) addCorrespondent(ctx context.Context, r Request) (any, error) {
	name := strings.TrimSpace(r.Name)
	if name == "" || len(name) > 255 || strings.ContainsAny(name, "\r\n\x00") {
		return nil, errors.New("paperless: invalid correspondent name")
	}
	if r.MatchingAlgorithm < 0 || r.MatchingAlgorithm > 6 {
		return nil, errors.New("paperless: invalid matching algorithm")
	}
	if len(r.Match) > 1024 {
		return nil, errors.New("paperless: matching expression too long")
	}
	existing, err := c.findCorrespondent(ctx, name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if err := checkCorrespondentOptions(*existing, r); err != nil {
			return nil, err
		}
		return CorrespondentResult{ID: existing.ID, Name: existing.Name, Created: false}, nil
	}
	payload := map[string]any{"name": name, "match": r.Match, "matching_algorithm": r.MatchingAlgorithm}
	if r.IsInsensitive != nil {
		payload["is_insensitive"] = *r.IsInsensitive
	}
	var created correspondentWire
	status, err := c.request(ctx, http.MethodPost, "api/correspondents/", nil, payload, &created)
	if err != nil {
		return nil, err
	}
	if status == http.StatusCreated || status == http.StatusOK {
		if created.ID <= 0 {
			return nil, errors.New("paperless: correspondent create returned no ID")
		}
		return CorrespondentResult{ID: created.ID, Name: created.Name, Created: true}, nil
	}
	if status == http.StatusBadRequest || status == http.StatusConflict {
		existing, err = c.findCorrespondent(ctx, name)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			if err := checkCorrespondentOptions(*existing, r); err != nil {
				return nil, err
			}
			return CorrespondentResult{ID: existing.ID, Name: existing.Name, Created: false}, nil
		}
	}
	return nil, statusError(status)
}

// An ensure must not silently rewrite existing correspondence classification rules.
func checkCorrespondentOptions(existing correspondentWire, r Request) error {
	if existing.Match != r.Match {
		return errors.New("paperless: existing correspondent has a different match rule")
	}
	if existing.MatchingAlgorithm != r.MatchingAlgorithm {
		return errors.New("paperless: existing correspondent has a different matching algorithm")
	}
	if r.IsInsensitive != nil && existing.IsInsensitive != *r.IsInsensitive {
		return errors.New("paperless: existing correspondent has different case-sensitivity")
	}
	return nil
}
