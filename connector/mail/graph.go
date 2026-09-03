package mail

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

// graphDefaultBase is the Microsoft Graph v1.0 API base a worker uses when it
// authors no endpoint override.
const graphDefaultBase = "https://graph.microsoft.com/v1.0"

// GraphClient sends mail through the Microsoft Graph sendMail API (ADR-0093). It
// posts a structured message to /users/{mailbox}/sendMail with a bearer token from
// its TokenSource; the mailbox is the message's From or the worker's default
// sender. It reaches Microsoft 365 mailboxes with an app-only or refresh-token grant.
type GraphClient struct {
	http    *http.Client
	tokens  TokenSource
	baseURL string
	sender  string
}

// NewGraphClient builds a Graph mail client. baseURL defaults to the Graph v1.0 API
// when empty; sender is the default mailbox to send as.
func NewGraphClient(tokens TokenSource, baseURL, sender string) *GraphClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = graphDefaultBase
	}
	return &GraphClient{http: nettimeout.HTTPClient(), tokens: tokens, baseURL: baseURL, sender: sender}
}

type graphRecipient struct {
	EmailAddress graphAddress `json:"emailAddress"`
}
type graphAddress struct {
	Address string `json:"address"`
}
type graphBody struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}
type graphMessage struct {
	Subject       string           `json:"subject"`
	Body          graphBody        `json:"body"`
	ToRecipients  []graphRecipient `json:"toRecipients"`
	CcRecipients  []graphRecipient `json:"ccRecipients,omitempty"`
	BccRecipients []graphRecipient `json:"bccRecipients,omitempty"`
}
type graphSendMail struct {
	Message         graphMessage `json:"message"`
	SaveToSentItems bool         `json:"saveToSentItems"`
}

func (c *GraphClient) Send(ctx context.Context, m Message) error {
	from := firstNonEmpty(m.From, c.sender)
	if from == "" {
		return fmt.Errorf("mail: graph: no sender mailbox configured (set the worker's sender or the task's from)")
	}
	if len(m.To) == 0 {
		return fmt.Errorf("mail: graph: message has no recipients")
	}
	// Graph carries exactly one body with a declared content type — there is no
	// multipart/alternative to send — so an HTML body wins when the author wrote one,
	// and the plain text is what a text-only message sends, unchanged.
	body := graphBody{ContentType: "Text", Content: m.Body}
	if m.HTML != "" {
		body = graphBody{ContentType: "HTML", Content: m.HTML}
	}
	payload := graphSendMail{
		Message: graphMessage{
			Subject:       m.Subject,
			Body:          body,
			ToRecipients:  graphRecipients(m.To),
			CcRecipients:  graphRecipients(m.Cc),
			BccRecipients: graphRecipients(m.Bcc),
		},
		SaveToSentItems: true,
	}
	endpoint := strings.TrimRight(c.baseURL, "/") + "/users/" + url.PathEscape(from) + "/sendMail"
	return sendJSON(ctx, c.http, c.tokens, endpoint, payload, "graph sendMail")
}

// graphRecipients wraps each address in Graph's {emailAddress:{address}} shape,
// returning nil for an empty list so an optional cc/bcc is omitted from the payload.
func graphRecipients(addrs []string) []graphRecipient {
	if len(addrs) == 0 {
		return nil
	}
	out := make([]graphRecipient, len(addrs))
	for i, a := range addrs {
		out[i] = graphRecipient{EmailAddress: graphAddress{Address: a}}
	}
	return out
}

// firstNonEmpty returns the first non-empty of its arguments (trimmed), or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// sendJSON acquires a bearer token and POSTs a JSON payload to a provider API
// endpoint, returning an error unless the response is 2xx (so a rejected send fails
// the job and is retried). It is shared by the Graph and Gmail clients.
func sendJSON(ctx context.Context, httpc *http.Client, tokens TokenSource, endpoint string, payload any, what string) error {
	tok, err := tokens.Token(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mail: encode %s: %w", what, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mail: build %s request: %w", what, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpc.Do(req)
	if err != nil {
		return fmt.Errorf("mail: %s: %w", what, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("mail: %s returned HTTP %d: %s", what, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}
