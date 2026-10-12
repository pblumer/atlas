package mail

import (
	"context"
	"testing"
)

// An internal relay admits a server by its network address — typically on port 25 —
// and either offers no login or refuses every one. A Worker configured with no
// credential is exactly that case, and it used to present its sender with an empty
// password anyway: every server refuses that ("535 5.7.8 authentication failed"), so
// a relay that needs no login could not be configured in the Console at all.

// TestAnSMTPWorkerWithoutACredentialDoesNotLogIn: the server offers AUTH and refuses
// every login, as the relay that prompted this did. A Worker with nothing to present
// must not try, and must still hand over the message.
func TestAnSMTPWorkerWithoutACredentialDoesNotLogIn(t *testing.T) {
	srv := newFakeSMTP(t)
	srv.authOK = false
	client, err := NewProviderClient(ProviderConfig{Provider: ProviderSMTP, Endpoint: srv.addr, Sender: "bot@example.com"})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	ctx := context.Background()
	if err := Probe(ctx, client); err != nil {
		t.Fatalf("Probe: %v — a Worker with no credential tried to log in", err)
	}
	if err := client.Send(ctx, Message{To: []string{"ops@example.com"}, Subject: "relay", Body: "hello"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sawAuth, from, rcpts, _ := srv.seen()
	if sawAuth {
		t.Error("the Worker sent AUTH although it has no credential to present")
	}
	if from != "bot@example.com" || len(rcpts) != 1 || rcpts[0] != "ops@example.com" {
		t.Errorf("envelope = from %q to %v, want bot@example.com to ops@example.com", from, rcpts)
	}
}

// TestAnSMTPWorkerWithoutACredentialReachesARelayThatOffersNoLogin: the classic
// port-25 relay does not advertise AUTH at all, which a Worker with a username
// refuses by name. One without a credential has no reason to.
func TestAnSMTPWorkerWithoutACredentialReachesARelayThatOffersNoLogin(t *testing.T) {
	addr := scriptedSMTP{quitReply: "221 bye"}.start(t)
	client, err := NewProviderClient(ProviderConfig{Provider: ProviderSMTP, Endpoint: addr, Sender: "bot@example.com"})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	if err := Probe(context.Background(), client); err != nil {
		t.Fatalf("Probe: %v, want a relay that offers no login reachable without one", err)
	}
}

// TestAnSMTPWorkerWithACredentialStillLogsIn is the other half: a credential is
// presented as the sender, so a server that requires a login is never reached
// anonymously by a Worker that was given one.
func TestAnSMTPWorkerWithACredentialStillLogsIn(t *testing.T) {
	srv := newFakeSMTP(t)
	client, err := NewProviderClient(ProviderConfig{Provider: ProviderSMTP, Endpoint: srv.addr, Sender: "bot@example.com", Secret: "pw"})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	if err := Probe(context.Background(), client); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if sawAuth, _, _, _ := srv.seen(); !sawAuth {
		t.Error("a Worker with a credential did not log in")
	}
}
