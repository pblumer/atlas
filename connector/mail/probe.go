package mail

import (
	"context"
	"fmt"
)

// Prober is a [Client] that can check its own configuration without delivering a
// message. Every provider in this package implements it, each answering the question
// its own configuration actually raises: SMTP opens the session a send opens (connect,
// TLS, AUTH), a native provider acquires an access token, and preview confirms it has
// somewhere to deliver. It is a separate interface rather than a method on Client so
// that "can this be checked?" stays an honest question — a provider that genuinely
// cannot be checked short of sending says so instead of returning a hollow success.
type Prober interface {
	Probe(ctx context.Context) error
}

// Probe checks a client as far as it can be checked without sending anything
// (ADR-0150). A nil error means the configuration works: the server answered, the
// credential was accepted, the connector is ready to carry a message.
func Probe(ctx context.Context, c Client) error {
	p, ok := c.(Prober)
	if !ok {
		return fmt.Errorf("mail: this provider cannot be checked without sending a message — send a test message instead")
	}
	return p.Probe(ctx)
}

// Probe acquires an access token, which is exactly the step that fails when a Gmail
// refresh token has been revoked or has expired — the failure that otherwise appears
// as "invalid_grant" on a parked token days after the credential was configured, on
// an OAuth client left in Testing publishing status. A cached, still-valid token
// answers without a round trip, which is the same thing a send would do.
func (c *GmailClient) Probe(ctx context.Context) error {
	if _, err := c.tokens.Token(ctx); err != nil {
		return fmt.Errorf("mail: gmail: %w", err)
	}
	return nil
}

// Probe acquires an access token from the Graph token endpoint — a wrong tenant,
// client id, or expired client secret fails here rather than at the first send.
func (c *GraphClient) Probe(ctx context.Context) error {
	if _, err := c.tokens.Token(ctx); err != nil {
		return fmt.Errorf("mail: microsoft: %w", err)
	}
	return nil
}

// Probe confirms the preview connector has an outbox to deliver into. There is
// nothing to dial and no credential to present, which is the whole point of the
// provider — so this succeeds, and says so, rather than pretending to check a network
// that is not involved.
func (c *PreviewClient) Probe(context.Context) error {
	if c.outbox == nil {
		return fmt.Errorf("mail: preview: connector %q has no outbox", c.connector)
	}
	return nil
}
