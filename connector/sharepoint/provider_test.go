package sharepoint

import (
	"strings"
	"testing"
)

func TestNewProviderClient(t *testing.T) {
	c, err := NewProviderClient(ProviderConfig{
		Secret: `{"method":"clientCredentials","tenantId":"tid","clientId":"c","clientSecret":"s"}`,
	})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	if _, ok := c.(*GraphClient); !ok {
		t.Errorf("client = %T, want *GraphClient", c)
	}
}

func TestNewProviderClientErrors(t *testing.T) {
	cases := map[string]ProviderConfig{
		"no credential":  {Secret: ""},
		"invalid json":   {Secret: "not json"},
		"missing tenant": {Secret: `{"method":"clientCredentials","clientId":"c","clientSecret":"s"}`}, // no tokenUrl and no tenantId
		"missing fields": {Secret: `{"method":"clientCredentials","tenantId":"t"}`},
		"unknown method": {Secret: `{"method":"magic","tenantId":"t"}`},
	}
	for name, cfg := range cases {
		if _, err := NewProviderClient(cfg); err == nil {
			t.Errorf("%s: want an error, got nil", name)
		}
	}
}

// TestApplyGraphDefaults proves the tenant builds the token URL and the scope
// defaults to Graph .default, while an explicit tokenUrl/scope is preserved.
func TestApplyGraphDefaults(t *testing.T) {
	b := credentialBundle{TenantID: "my-tenant"}
	applyGraphDefaults(&b)
	if !strings.Contains(b.TokenURL, "my-tenant") || !strings.HasSuffix(b.TokenURL, "/oauth2/v2.0/token") {
		t.Errorf("tokenURL = %q, want the tenant token endpoint", b.TokenURL)
	}
	if b.Scope != graphScope {
		t.Errorf("scope = %q, want the Graph default", b.Scope)
	}
	explicit := credentialBundle{TokenURL: "http://custom/token", Scope: "custom"}
	applyGraphDefaults(&explicit)
	if explicit.TokenURL != "http://custom/token" || explicit.Scope != "custom" {
		t.Errorf("explicit overrides changed: %+v", explicit)
	}
}
