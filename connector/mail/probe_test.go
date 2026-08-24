package mail

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func gmailBundleFor(url string) string {
	b, _ := json.Marshal(credentialBundle{
		Method: methodRefreshToken, TokenURL: url, ClientID: "c", ClientSecret: "s", RefreshToken: "r",
	})
	return string(b)
}

func TestGmailProbeAcceptsAWorkingCredential(t *testing.T) {
	tok := newTokenServer(t, 3600)
	c, err := NewProviderClient(ProviderConfig{Provider: ProviderGmail, Sender: "bot@x", Secret: gmailBundleFor(tok.srv.URL)})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	if err := Probe(context.Background(), c); err != nil {
		t.Fatalf("Probe: %v", err)
	}
}

// TestGmailProbeReportsARevokedRefreshToken is the outage the check was built for: a
// Google OAuth client left in Testing publishing status expires its refresh tokens
// after seven days, and the first thing that ever noticed used to be an incident on a
// parked token. The check now answers it in a second, in the provider's own words.
func TestGmailProbeReportsARevokedRefreshToken(t *testing.T) {
	tok := newTokenServer(t, 3600)
	tok.status = 400
	tok.body = `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`

	c, err := NewProviderClient(ProviderConfig{Provider: ProviderGmail, Sender: "bot@x", Secret: gmailBundleFor(tok.srv.URL)})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	err = Probe(context.Background(), c)
	if err == nil {
		t.Fatal("Probe succeeded with a revoked refresh token")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error = %q, want the provider's own reason", err)
	}
}

func TestGraphProbeReportsARejectedClientSecret(t *testing.T) {
	tok := newTokenServer(t, 3600)
	tok.status = 401
	tok.body = `{"error":"invalid_client","error_description":"AADSTS7000215: Invalid client secret provided."}`
	bundle, _ := json.Marshal(credentialBundle{
		Method: methodClientCredentials, TokenURL: tok.srv.URL, TenantID: "t", ClientID: "c", ClientSecret: "wrong",
	})
	c, err := NewProviderClient(ProviderConfig{Provider: ProviderMicrosoft, Sender: "bot@x", Secret: string(bundle)})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	if err := Probe(context.Background(), c); err == nil {
		t.Fatal("Probe succeeded with a rejected client secret")
	}
}

func TestPreviewProbeIsReadyWithoutANetwork(t *testing.T) {
	box := NewOutbox(0)
	c := NewPreviewClient(box, "trial", "bot@x")
	if err := Probe(context.Background(), c); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if n := box.Len(); n != 0 {
		t.Errorf("the check delivered %d message(s), want none", n)
	}
	if err := Probe(context.Background(), NewPreviewClient(nil, "trial", "bot@x")); err == nil {
		t.Error("a preview connector with no outbox passed the check")
	}
}

// unprobeable is a provider that only knows how to send — the case the Prober
// interface exists to keep honest.
type unprobeable struct{}

func (unprobeable) Send(context.Context, Message) error { return nil }

func TestProbeSaysSoWhenAProviderCannotBeChecked(t *testing.T) {
	err := Probe(context.Background(), unprobeable{})
	if err == nil {
		t.Fatal("Probe reported success for a provider that cannot be checked")
	}
	if !strings.Contains(err.Error(), "test message") {
		t.Errorf("error = %q, want it to point at the way that does work", err)
	}
}
