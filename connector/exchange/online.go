package exchange

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pblumer/atlas/connector/exchange/catalog"
	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/connector/oauth2"
	"github.com/pblumer/atlas/limits"
)

// DefaultOnlineBaseURL is Exchange Online's worldwide endpoint. A sovereign cloud is a
// different host, which is why a Worker can name its own.
const DefaultOnlineBaseURL = "https://outlook.office365.com"

// DefaultAuthorityURL is the worldwide Microsoft identity platform.
const DefaultAuthorityURL = "https://login.microsoftonline.com"

// systemAnchor is the routing hint an app-only session anchors on when a call
// addresses no mailbox. It is the one the Exchange Online PowerShell module uses for
// app-only connections, and it is a constant of the service rather than of a tenant.
const systemAnchor = "APP:SystemMailbox{bb558c35-97f1-4cb9-8ff7-d53741dc928c}@"

// hashtableType is how the REST endpoint is told that an object parameter is a
// PowerShell hashtable — the shape every multi-valued property change takes
// (@{Add=…; Remove=…}).
const hashtableType = "#Exchange.GenericHashTable"

// maxPages ends a listing whose endpoint never stops offering another page. At the
// endpoint's page size it is far past anything that belongs in a process variable, so
// reaching it means something is wrong, and saying so is the useful outcome.
const maxPages = 1000

// OnlineConfig is what an [OnlineClient] is built from.
type OnlineConfig struct {
	Tokens oauth2.TokenSource
	// BaseURL is the Exchange Online endpoint; empty is [DefaultOnlineBaseURL].
	BaseURL string
	// Organization is the tenant's initial domain (contoso.onmicrosoft.com) — the
	// value Connect-ExchangeOnline -Organization takes, and the one the endpoint's path
	// and the system anchor carry.
	Organization string
	// AnchorMailbox overrides the routing hint for every call. Empty anchors each call
	// on the mailbox it addresses.
	AnchorMailbox string
	HTTPClient    *http.Client
}

// OnlineClient runs cmdlets in Exchange Online over the REST endpoint the Exchange
// Online PowerShell module itself uses (ADR-0462).
//
// The endpoint is the module's transport, not a published API: Microsoft documents the
// module and not the requests it sends. That is the decision the record weighs — what
// it buys is no PowerShell on the worker host, a token cached across jobs instead of a
// session built per job, and a protocol this package can test without a tenant; what
// it costs is that the request shape is learned from the module and from those who
// have studied it, and can change with a module release. Keeping the transport behind
// [Backend] is what keeps that cost to this one file.
type OnlineClient struct {
	tokens       oauth2.TokenSource
	baseURL      string
	organization string
	anchor       string
	httpc        *http.Client
	// maxPage bounds one response body; a field so a test can lower it.
	maxPage int64
}

// NewOnlineClient builds a client for one organization.
func NewOnlineClient(c OnlineConfig) *OnlineClient {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		base = DefaultOnlineBaseURL
	}
	httpc := c.HTTPClient
	if httpc == nil {
		httpc = nettimeout.HTTPClient()
	}
	return &OnlineClient{
		tokens: c.Tokens, baseURL: base, organization: strings.TrimSpace(c.Organization),
		anchor: strings.TrimSpace(c.AnchorMailbox), httpc: httpc, maxPage: limits.Default().Payload,
	}
}

// Target is [catalog.Online].
func (c *OnlineClient) Target() catalog.Target { return catalog.Online }

// cmdletRequest is the envelope the endpoint takes.
type cmdletRequest struct {
	CmdletInput cmdletInput `json:"CmdletInput"`
}

type cmdletInput struct {
	CmdletName string         `json:"CmdletName"`
	Parameters map[string]any `json:"Parameters"`
}

// Invoke runs one cmdlet and follows the endpoint's pages until there are none, or
// until it holds more than limit objects.
func (c *OnlineClient) Invoke(ctx context.Context, inv catalog.Invocation, limit int) ([]any, error) {
	body, err := json.Marshal(cmdletRequest{CmdletInput: cmdletInput{CmdletName: inv.Cmdlet, Parameters: encodeParameters(inv.Parameters)}})
	if err != nil {
		return nil, fmt.Errorf("exchange: encode %s: %w", inv.Cmdlet, err)
	}
	target := c.baseURL + "/adminapi/beta/" + url.PathEscape(c.organization) + "/InvokeCommand"
	anchor := c.anchorFor(inv)
	items := []any{}
	for pages := 0; pages < maxPages; pages++ {
		page, next, err := c.post(ctx, inv.Cmdlet, target, anchor, body)
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
		if limit > 0 && len(items) >= limit {
			return items, nil
		}
		if next == "" {
			return items, nil
		}
		if target, err = c.confine(next); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("exchange: %s still offered another page after %d requests; narrow it, or lower maxResults so an oversized listing fails by its own bound", inv.Cmdlet, maxPages)
}

// post sends one request and reads one page.
func (c *OnlineClient) post(ctx context.Context, cmdlet, target, anchor string, body []byte) ([]any, string, error) {
	tok, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("exchange: build %s request: %w", cmdlet, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("X-AnchorMailbox", anchor)
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("exchange: %s: %w", cmdlet, err)
	}
	defer resp.Body.Close()
	// budget is limits.Default().Payload, set by NewOnlineClient. One byte past it, so an
	// oversized page is refused rather than cut short.
	budget := c.maxPage
	raw, err := io.ReadAll(io.LimitReader(resp.Body, budget+1))
	if err != nil {
		return nil, "", fmt.Errorf("exchange: read %s response: %w", cmdlet, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, "", failure(cmdlet, resp.StatusCode, raw)
	}
	if int64(len(raw)) > budget {
		return nil, "", fmt.Errorf("exchange: %s answered a page larger than the %d byte payload budget; narrow the call", cmdlet, budget)
	}
	var page struct {
		Value    *[]any `json:"value"`
		NextLink string `json:"@odata.nextLink"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&page); err != nil {
		return nil, "", fmt.Errorf("exchange: %s answered a body that is not JSON: %w", cmdlet, err)
	}
	if page.Value == nil {
		return nil, "", fmt.Errorf("exchange: %s answered without a %q collection", cmdlet, "value")
	}
	out := *page.Value
	for i, item := range out {
		out[i] = stripAnnotations(item)
	}
	return out, page.NextLink, nil
}

// confine allows a continuation only on this client's own endpoint: the bearer it
// carries administers every mailbox in the tenant, and a continuation is the one place
// where a response decides the next URL.
func (c *OnlineClient) confine(next string) (string, error) {
	u, err := url.Parse(next)
	if err != nil {
		return "", fmt.Errorf("exchange: cannot follow the paged result: %q is not a URL", next)
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("exchange: this worker's own base URL %q is not a URL", c.baseURL)
	}
	if u.Scheme != base.Scheme || u.Host != base.Host {
		return "", fmt.Errorf("exchange: refusing to follow a paged result to %s://%s: a continuation may only stay on this worker's own endpoint (%s://%s)",
			u.Scheme, u.Host, base.Scheme, base.Host)
	}
	return next, nil
}

// anchorFor picks the routing hint for one call: the configured one when an operator
// set it, else the mailbox the call addresses, else the system mailbox app-only
// sessions anchor on.
func (c *OnlineClient) anchorFor(inv catalog.Invocation) string {
	if c.anchor != "" {
		if strings.Contains(c.anchor, ":") {
			return c.anchor
		}
		return "UPN:" + c.anchor
	}
	if id, ok := inv.Parameters["Identity"].(string); ok {
		// A folder is addressed as mailbox:\folder; the mailbox is the part to route on.
		if i := strings.Index(id, ":"); i >= 0 {
			id = id[:i]
		}
		if id = strings.TrimSpace(id); strings.Contains(id, "@") && !strings.ContainsAny(id, " \t") {
			return "UPN:" + id
		}
	}
	return systemAnchor + c.organization
}

// encodeParameters copies the parameters and marks every object among them as the
// hashtable it stands for. The authored map is left as it was.
func encodeParameters(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if h, ok := v.(map[string]any); ok {
			if _, typed := h["@odata.type"]; !typed {
				cp := make(map[string]any, len(h)+1)
				for hk, hv := range h {
					cp[hk] = hv
				}
				cp["@odata.type"] = hashtableType
				v = cp
			}
		}
		out[k] = v
	}
	return out
}

// stripAnnotations drops the OData annotations from one returned object: they describe
// the protocol, not the mailbox.
func stripAnnotations(v any) any {
	obj, ok := v.(map[string]any)
	if !ok {
		return v
	}
	for k := range obj {
		if strings.HasPrefix(k, "@") || strings.Contains(k, "@odata.") {
			delete(obj, k)
		}
	}
	return obj
}

// failure turns a non-2xx answer into the most specific error the body allows.
// Exchange puts the exception's type in front of its message between bars
// (|Microsoft.Exchange…MemberAlreadyExistsException|The recipient …); the type is kept,
// shortened, because it is what tells "already a member" from "no such group".
func failure(cmdlet string, status int, raw []byte) error {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && (env.Error.Code != "" || env.Error.Message != "") {
		msg := env.Error.Message
		if strings.HasPrefix(msg, "|") {
			if end := strings.Index(msg[1:], "|"); end >= 0 {
				typ := msg[1 : end+1]
				if dot := strings.LastIndex(typ, "."); dot >= 0 {
					typ = typ[dot+1:]
				}
				msg = strings.TrimSpace(msg[end+2:]) + " (" + typ + ")"
			}
		}
		return fmt.Errorf("exchange: %s returned HTTP %d: %s: %s", cmdlet, status, env.Error.Code, msg)
	}
	quote := strings.TrimSpace(string(raw))
	if max := int(limits.Default().ErrorBody); len(quote) > max {
		quote = quote[:max] + "…"
	}
	return fmt.Errorf("exchange: %s returned HTTP %d: %s", cmdlet, status, quote)
}
