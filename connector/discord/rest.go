package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// DefaultBaseURL is the Discord HTTP API this worker speaks, version included. Discord
// versions its API in the path, so the version is part of the base rather than a
// separate constant to forget: bumping it is one edit here and a test.
//
// It is the default rather than a requirement. An operator behind a proxy overrides it
// on the Worker record, as they can for SharePoint and Google Sheets — the API base is
// the same for everyone else.
const DefaultBaseURL = "https://discord.com/api/v10"

// publicThreadType is the channel type a standalone thread is created as. Discord
// requires a type on the thread-without-a-message endpoint and takes none on the
// thread-from-a-message one, where the parent decides it.
//
// Public is the default because a thread a process opens is the discussion *about* a
// case, and one only its creator can see is not that. A model that wants otherwise sets
// type through a discordField, which is merged last and so wins over this.
const publicThreadType = 11

// Connector is the server-side configuration of one Discord Worker: the BaseURL (the
// API base, defaulted to [DefaultBaseURL]) and the bot token.
//
// BotToken is the token as Discord's developer portal shows it, without the `Bot `
// prefix — the client composes the scheme. The value is resolved from the vault at
// build time and held only here, never persisted (I6).
type Connector struct {
	BaseURL  string
	BotToken string
}

// HTTPClient calls a real Discord over its HTTP API. It is stateless — the token is
// sent per request rather than exchanged for a session — so it is safe for concurrent
// use by the worker.
type HTTPClient struct {
	conn Connector
	http *http.Client
}

// NewHTTPClient builds a Discord API client for a configured worker, bounded by the
// shared worker call budget (ADR-0149). The worker may run on the run-loop goroutine,
// so an unbounded call would let a hung Discord stall the whole engine; see the
// nettimeout package doc.
func NewHTTPClient(conn Connector) *HTTPClient {
	conn.BaseURL = strings.TrimRight(strings.TrimSpace(conn.BaseURL), "/")
	if conn.BaseURL == "" {
		conn.BaseURL = DefaultBaseURL
	}
	conn.BotToken = strings.TrimSpace(conn.BotToken)
	return &HTTPClient{conn: conn, http: nettimeout.HTTPClient()}
}

// BaseURL is the API base this client calls, after defaulting and trimming. It is
// exported so a test can assert the default without making a request to Discord, and so
// an operator-facing diagnostic can say where a worker is actually pointed.
func (c *HTTPClient) BaseURL() string { return c.conn.BaseURL }

// Do performs one operation. Every failure — a transport error, a non-2xx status, an
// operation nothing implements — is returned so the job stays pending and is retried,
// then raises an incident (ADR-0061), rather than completing a token on work that did
// not happen.
func (c *HTTPClient) Do(ctx context.Context, req Request) (any, error) {
	req = trimIdentifiers(req)
	spec, ok := Ops[req.Operation]
	if !ok {
		return nil, fmt.Errorf("discord: unknown operation %q (want %s)", req.Operation, strings.Join(OpNames(), ", "))
	}
	// The compiler guarantees the *attribute* is authored; it cannot guarantee what a
	// FEEL expression evaluates to at call time. An empty id here would otherwise be
	// spliced into a path and answered with a 404 that names a URL the author never
	// wrote, so it is caught where the fix — the expression — is still in view.
	if spec.NeedsChannel && req.Channel == "" {
		return nil, fmt.Errorf("discord: %s has no channel id (the task's channel resolved to nothing)", req.Operation)
	}
	if spec.NeedsMessage && req.Message == "" {
		return nil, fmt.Errorf("discord: %s has no message id (the task's messageId resolved to nothing)", req.Operation)
	}
	channel := url.PathEscape(req.Channel)
	switch req.Operation {
	case "send-message":
		return c.call(ctx, http.MethodPost, "/channels/"+channel+"/messages", c.sendBody(req), req)
	case "edit-message":
		return c.call(ctx, http.MethodPatch, "/channels/"+channel+"/messages/"+url.PathEscape(req.Message), c.editBody(req), req)
	case "delete-message":
		return c.call(ctx, http.MethodDelete, "/channels/"+channel+"/messages/"+url.PathEscape(req.Message), nil, req)
	case "get-message":
		return c.call(ctx, http.MethodGet, "/channels/"+channel+"/messages/"+url.PathEscape(req.Message), nil, req)
	case "list-messages":
		return c.call(ctx, http.MethodGet, "/channels/"+channel+"/messages"+listQuery(req), nil, req)
	case "create-thread":
		return c.createThread(ctx, channel, req)
	default:
		// Ops named it and the switch does not — a row added without its call. Better
		// said here than by a nil answer that completes a token on nothing.
		return nil, fmt.Errorf("discord: operation %q is in the table but not implemented", req.Operation)
	}
}

// trimIdentifiers strips surrounding whitespace from the values that *address*
// something in Discord — a channel, a message, a list's lower bound. Those are
// snowflake ids: whitespace is never part of one, and an id that arrived as "123 " is
// spliced into a path and answered with a 404 that reads as a deleted message rather
// than as a stray space in a form field.
//
// It is deliberately only those. Content and a thread name are what the model composed,
// and silently reshaping authored text would be a different kind of surprise.
func trimIdentifiers(req Request) Request {
	req.Channel = strings.TrimSpace(req.Channel)
	req.Message = strings.TrimSpace(req.Message)
	req.After = strings.TrimSpace(req.After)
	return req
}

// sendBody builds the body a message is created from: the content, the job key as the
// nonce so an at-least-once replay's duplicate is recognizable, and the model's extra
// fields merged in last so a model can override anything composed here.
func (c *HTTPClient) sendBody(req Request) map[string]any {
	body := map[string]any{"content": req.Content}
	if req.Nonce != "" {
		body["nonce"] = req.Nonce
	}
	return mergeFields(body, req.Fields)
}

// editBody builds an edit's body. There is no nonce: a nonce identifies a *creation*,
// and an edit addresses a message that already exists by its id.
func (c *HTTPClient) editBody(req Request) map[string]any {
	return mergeFields(map[string]any{"content": req.Content}, req.Fields)
}

// createThread opens a thread, through whichever of Discord's two endpoints the model
// asked for: naming a message hangs the thread under that message, naming none starts a
// standalone thread in the channel. They are one operation because they are one intent
// and differ only in what the thread hangs from.
//
// Only the standalone endpoint takes a type, and the parent message decides it for the
// other — so sending one there would be a field Discord ignores on half the calls.
//
// channel is already path-escaped by [HTTPClient.Do], which escapes it once for every
// operation.
func (c *HTTPClient) createThread(ctx context.Context, channel string, req Request) (any, error) {
	body := map[string]any{"name": req.Name}
	if req.Message != "" {
		return c.call(ctx, http.MethodPost,
			"/channels/"+channel+"/messages/"+url.PathEscape(req.Message)+"/threads",
			mergeFields(body, req.Fields), req)
	}
	body["type"] = publicThreadType
	return c.call(ctx, http.MethodPost, "/channels/"+channel+"/threads", mergeFields(body, req.Fields), req)
}

// mergeFields merges the model's extra body properties over what the worker composed.
// Last wins on purpose: a model that needs to send its own allowed_mentions, or an
// embed instead of plain content, should not be blocked by a default this package
// chose.
func mergeFields(body map[string]any, fields map[string]any) map[string]any {
	for k, v := range fields {
		body[k] = v
	}
	return body
}

// listQuery renders a list's query string. limit is always sent — the compiler has
// already applied the default, so there is no call where it is absent — and after only
// when the model authored one, because an empty after is not "from the beginning" to
// Discord but a malformed snowflake.
func listQuery(req Request) string {
	q := url.Values{}
	if req.MaxResults > 0 {
		q.Set("limit", strconv.FormatInt(int64(req.MaxResults), 10))
	}
	if req.After != "" {
		q.Set("after", req.After)
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// call performs one HTTP request and decodes what Discord answered. A 204 (delete) and
// an empty body both decode to nil, which is what tells the worker there is nothing to
// write into a result variable.
func (c *HTTPClient) call(ctx context.Context, method, path string, body any, req Request) (any, error) {
	if strings.TrimSpace(c.conn.BotToken) == "" {
		return nil, fmt.Errorf("discord: worker has no bot token")
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("discord: encode %s request: %w", req.Operation, err)
		}
		payload = bytes.NewReader(encoded)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, c.conn.BaseURL+path, payload)
	if err != nil {
		return nil, fmt.Errorf("discord: build %s request: %w", req.Operation, err)
	}
	// The bot scheme, composed here rather than stored: what an operator has in hand is
	// the token, and a vault entry that already carried the prefix would be a second
	// valid-looking shape for the same secret.
	httpReq.Header.Set("Authorization", "Bot "+c.conn.BotToken)
	httpReq.Header.Set("Accept", "application/json")
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("discord: %s: %w", req.Operation, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, rateLimited(req.Operation, raw)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("discord: %s returned HTTP %d: %s", req.Operation, resp.StatusCode, describeError(raw))
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // snowflake ids arrive as strings, but keep every other number exact through the variable round-trip
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("discord: decode %s response: %w", req.Operation, err)
	}
	return out, nil
}

// rateLimited renders a 429 as an error naming how long Discord asked the caller to
// wait. The handler deliberately does not sleep it out: the worker may run on the
// run-loop goroutine, where waiting would stall the engine for every other request
// (ADR-0149). Returning leaves the job pending for the ordinary retry-then-incident
// path (ADR-0061), which is where a wait belongs.
func rateLimited(op string, raw []byte) error {
	var e discordError
	if err := json.Unmarshal(raw, &e); err == nil && e.RetryAfter > 0 {
		return fmt.Errorf("discord: %s was rate limited; Discord asks for %.3gs before the next attempt", op, e.RetryAfter)
	}
	return fmt.Errorf("discord: %s was rate limited", op)
}

// discordError is Discord's error envelope. Its message and numeric code are what a
// human needs (code 50001 is "Missing Access", which reads very differently from a bare
// 403), and retry_after rides on the 429 form of the same object.
type discordError struct {
	Message    string  `json:"message"`
	Code       int     `json:"code"`
	RetryAfter float64 `json:"retry_after"`
}

// describeError renders Discord's error envelope, falling back to the raw body when the
// response is not one (a proxy's HTML error page, most often — which is itself the
// answer to "why is this failing").
func describeError(raw []byte) string {
	var e discordError
	if err := json.Unmarshal(raw, &e); err == nil && strings.TrimSpace(e.Message) != "" {
		if e.Code != 0 {
			return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
		}
		return e.Message
	}
	body := strings.TrimSpace(string(raw))
	if body == "" {
		return "no response body"
	}
	const max = 300 // enough to identify the failure, short enough for an incident message
	if len(body) > max {
		return body[:max] + "…"
	}
	return body
}

// ProviderConfig is the per-worker data the server resolves before building a client:
// the API base (Endpoint, empty for [DefaultBaseURL]) and the resolved Secret — the
// credential JSON bundle held in the vault under the worker's credentialsRef. The
// secret lives only here at build time, never in a model or an event (I6).
type ProviderConfig struct {
	Endpoint string
	Secret   string
}

// credentialBundle is the JSON an operator stores in the vault under a Discord
// worker's credentialsRef. One field, because a bot has one credential — there is
// nothing here for two shapes to be ambiguous about, as they are for Jira.
type credentialBundle struct {
	BotToken string `json:"botToken,omitempty"`
}

// TokenFromBundle reads a Discord Worker's vault bundle and answers with the bare bot
// token, ready to be sent behind the "Bot " scheme this package composes.
//
// It is exported because two callers have to agree on it exactly: [NewProviderClient]
// builds the engine's own client from it, and the server renders the same value into a
// supervised worker's environment (superviseEnv). Two readings of one JSON that drifted
// would hand a worker a different identity from the one the engine would have used —
// the kind of difference that shows up as a 401 on half a deployment.
func TokenFromBundle(secret string) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", fmt.Errorf("discord: worker has no credential (set credentialsRef to a vault bundle {botToken})")
	}
	var b credentialBundle
	if err := json.Unmarshal([]byte(secret), &b); err != nil {
		return "", fmt.Errorf("discord: credential is not valid JSON: %w", err)
	}
	token := strings.TrimSpace(b.BotToken)
	if token == "" {
		return "", fmt.Errorf("discord: credential bundle has no \"botToken\"")
	}
	// A token pasted with the scheme already on it is the one paste mistake this worker
	// can fix rather than report: the client composes "Bot ", so leaving the prefix in
	// place would send it twice and be answered by a 401 that explains nothing.
	token = cutScheme(token)
	if token == "" {
		return "", fmt.Errorf("discord: credential bundle's \"botToken\" is only the scheme prefix")
	}
	return token, nil
}

// NewProviderClient builds the Discord client for a managed worker. A misconfigured
// worker returns an error so the caller can skip it — its tasks then park with that
// reason (ADR-0158) rather than calling Discord unauthenticated and being told nothing
// useful by a 401.
func NewProviderClient(cfg ProviderConfig) (Client, error) {
	token, err := TokenFromBundle(cfg.Secret)
	if err != nil {
		return nil, err
	}
	return NewHTTPClient(Connector{BaseURL: strings.TrimSpace(cfg.Endpoint), BotToken: token}), nil
}

// cutScheme strips a pasted "Bot" scheme prefix from a stored token, and only when the
// three letters are followed by whitespace or by nothing at all. A real token that
// happens to begin with those letters is not a scheme, and mangling it would turn a
// working credential into a 401 nobody could explain.
func cutScheme(token string) string {
	rest, found := strings.CutPrefix(token, "Bot")
	if !found {
		return token
	}
	if rest != "" && strings.TrimLeft(rest, " \t") == rest {
		return token // "Botsomething" is a token, not a scheme
	}
	return strings.TrimSpace(rest)
}
