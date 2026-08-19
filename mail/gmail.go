package mail

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
)

// gmailDefaultBase is the Gmail v1 API base a connector uses when it authors no
// endpoint override.
const gmailDefaultBase = "https://gmail.googleapis.com/gmail/v1"

// GmailClient sends mail through the Gmail API (ADR-0093). It posts a base64url-encoded
// RFC 5322 message to /users/me/messages/send with a bearer token from its
// TokenSource; "me" resolves to the authenticated user (the impersonated subject under
// a service account, or the refresh token's user). It frames the message with the same
// MIME builder the SMTP client uses.
type GmailClient struct {
	http    *http.Client
	tokens  TokenSource
	baseURL string
	sender  string
}

// NewGmailClient builds a Gmail mail client. baseURL defaults to the Gmail v1 API when
// empty; sender is the default From address a task without one falls back to.
func NewGmailClient(tokens TokenSource, baseURL, sender string) *GmailClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = gmailDefaultBase
	}
	return &GmailClient{http: http.DefaultClient, tokens: tokens, baseURL: baseURL, sender: sender}
}

func (c *GmailClient) Send(ctx context.Context, m Message) error {
	from := firstNonEmpty(m.From, c.sender)
	if from == "" {
		return fmt.Errorf("mail: gmail: no sender configured (set the connector's sender or the task's from)")
	}
	if len(m.To) == 0 {
		return fmt.Errorf("mail: gmail: message has no recipients")
	}
	// Gmail's messages.send takes the whole RFC 5322 message base64url-encoded under
	// "raw"; we reuse the SMTP client's MIME framing so both providers send an
	// identical message.
	raw := base64.URLEncoding.EncodeToString(buildRFC822(m, from))
	endpoint := strings.TrimRight(c.baseURL, "/") + "/users/me/messages/send"
	return sendJSON(ctx, c.http, c.tokens, endpoint, map[string]string{"raw": raw}, "gmail send")
}
