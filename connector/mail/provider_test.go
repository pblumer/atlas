package mail

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNewProviderClientDispatch(t *testing.T) {
	smtp, err := NewProviderClient(ProviderConfig{Provider: "", Endpoint: "smtp.x:587", Sender: "bot@x", Secret: "pw"})
	if err != nil {
		t.Fatalf("smtp: %v", err)
	}
	if _, ok := smtp.(*SMTPClient); !ok {
		t.Errorf("default provider = %T, want *SMTPClient", smtp)
	}

	graphBundle := `{"method":"clientCredentials","tenantId":"t","clientId":"c","clientSecret":"s"}`
	graph, err := NewProviderClient(ProviderConfig{Provider: ProviderMicrosoft, Sender: "bot@x", Secret: graphBundle})
	if err != nil {
		t.Fatalf("microsoft: %v", err)
	}
	if _, ok := graph.(*GraphClient); !ok {
		t.Errorf("microsoft provider = %T, want *GraphClient", graph)
	}

	gmailBundle := `{"method":"refreshToken","clientId":"c","clientSecret":"s","refreshToken":"r"}`
	gmail, err := NewProviderClient(ProviderConfig{Provider: ProviderGmail, Sender: "bot@x", Secret: gmailBundle})
	if err != nil {
		t.Fatalf("gmail: %v", err)
	}
	if _, ok := gmail.(*GmailClient); !ok {
		t.Errorf("gmail provider = %T, want *GmailClient", gmail)
	}
}

func TestNewProviderClientErrors(t *testing.T) {
	cases := map[string]ProviderConfig{
		"unknown provider": {Provider: "carrierpigeon", Sender: "s@x", Secret: "{}"},
		"empty credential": {Provider: ProviderGmail, Sender: "s@x", Secret: ""},
		"bad json":         {Provider: ProviderGmail, Sender: "s@x", Secret: "not json"},
		"graph no tenant":  {Provider: ProviderMicrosoft, Sender: "s@x", Secret: `{"method":"clientCredentials","clientId":"c","clientSecret":"s"}`},
		"missing fields":   {Provider: ProviderGmail, Sender: "s@x", Secret: `{"method":"refreshToken","clientId":"c"}`},
	}
	for name, cfg := range cases {
		if _, err := NewProviderClient(cfg); err == nil {
			t.Errorf("%s: want an error, got nil", name)
		}
	}
}

// TestApplyProviderDefaults covers the per-provider token-URL/scope/subject defaults.
func TestApplyProviderDefaults(t *testing.T) {
	gmail := credentialBundle{Method: methodServiceAccount}
	applyProviderDefaults(ProviderGmail, &gmail, "user@example.com")
	if gmail.TokenURL != gmailTokenURL || gmail.Scope != gmailScope || gmail.Subject != "user@example.com" {
		t.Errorf("gmail defaults = %+v, want token URL, scope, and subject filled", gmail)
	}
	// An explicit subject is preserved.
	explicit := credentialBundle{Method: methodServiceAccount, Subject: "other@example.com"}
	applyProviderDefaults(ProviderGmail, &explicit, "user@example.com")
	if explicit.Subject != "other@example.com" {
		t.Errorf("subject = %q, want the bundle's explicit subject preserved", explicit.Subject)
	}
	graph := credentialBundle{Method: methodClientCredentials, TenantID: "tenant-42"}
	applyProviderDefaults(ProviderMicrosoft, &graph, "s@x")
	if graph.TokenURL != "https://login.microsoftonline.com/tenant-42/oauth2/v2.0/token" || graph.Scope != graphScope {
		t.Errorf("graph defaults = %+v, want tenant token URL and .default scope", graph)
	}
}

// TestProviderClientEndToEnd wires a Gmail provider client against fake token and API
// servers and sends a message — proving the whole native path (bundle → token →
// authenticated send) works through the public constructor.
func TestProviderClientEndToEnd(t *testing.T) {
	tokSrv := newTokenServer(t, 3600)
	api := newAPIServer(t)
	api.status = 200
	bundle, _ := json.Marshal(credentialBundle{
		Method: methodRefreshToken, TokenURL: tokSrv.srv.URL, ClientID: "c", ClientSecret: "s", RefreshToken: "r",
	})
	client, err := NewProviderClient(ProviderConfig{Provider: ProviderGmail, Endpoint: api.srv.URL, Sender: "bot@example.com", Secret: string(bundle)})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	if err := client.Send(context.Background(), Message{To: []string{"a@example.com"}, Subject: "Hi", Body: "Hello"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if api.auth == "" || api.path != "/users/me/messages/send" {
		t.Errorf("api call = auth %q path %q, want an authenticated messages.send", api.auth, api.path)
	}
	if tokSrv.lastForm["grant_type"] != "refresh_token" {
		t.Errorf("token grant = %q, want refresh_token", tokSrv.lastForm["grant_type"])
	}
}
