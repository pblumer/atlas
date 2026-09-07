package api

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/discord"
)

// TestDiscordBundleShapeMatchesTheWorker is the drift guard between this package's
// description of the credential bundle — which is what the Console's shape hint and the
// vault documentation are written from — and the decoder that actually reads it. The
// two are separate types because connector/discord's is unexported, so nothing but a
// test holds them together.
func TestDiscordBundleShapeMatchesTheWorker(t *testing.T) {
	raw, err := json.Marshal(discordCredentials{BotToken: "MTIzNDU2.abc.def"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := discord.NewProviderClient(discord.ProviderConfig{Secret: string(raw)}); err != nil {
		t.Fatalf("connector/discord rejected a bundle this package describes (%s): %v", raw, err)
	}
}

// TestValidateDiscordWorkerNeedsACredential: for this Worker Type the credential *is*
// the whole configuration — Discord's API base is the same for everyone — so a record
// without one is a Worker that can never do anything, and the refusal names the bundle
// shape rather than leaving an operator to guess.
func TestValidateDiscordWorkerNeedsACredential(t *testing.T) {
	p := &createConnectorParams{Kind: connectorKindDiscord, Provider: "gmail", Sender: "x@y"}
	msg := validateDiscordConnector(p)
	if !strings.Contains(msg, "credentialsRef") {
		t.Errorf("message %q should name the missing credentialsRef", msg)
	}
	if !strings.Contains(msg, "botToken") {
		t.Errorf("message %q should name the bundle's one field", msg)
	}
	// The mail-only fields are cleared rather than stored on a Worker Type that has no
	// use for them.
	if p.Provider != "" || p.Sender != "" {
		t.Errorf("params = %+v; want the mail-only fields cleared", p)
	}
	// No endpoint is demanded: Discord's API base is not a per-tenant address, so a
	// record carrying only a credential is complete.
	if msg := validateDiscordConnector(&createConnectorParams{
		Kind: connectorKindDiscord, CredentialsRef: "discord_team",
	}); msg != "" {
		t.Errorf("a record with a credentialsRef and no endpoint was refused: %s", msg)
	}
}

// TestDiscordIsAConfigurableWorkerType ties the registry entry to the reserved job type
// it serves: an entry naming the wrong index would compile and then leave every Discord
// task unhandled.
func TestDiscordIsAConfigurableWorkerType(t *testing.T) {
	kind, ok := lookupManagedConnectorKind(connectorKindDiscord)
	if !ok {
		t.Fatal("discord is not an operator-configurable Worker Type")
	}
	if kind.workerOnly {
		t.Error("discord is marked worker-only, but the engine registers a handler for it")
	}
	if len(kind.jobTypes) != 1 {
		t.Fatalf("jobTypes = %v; want the one reserved index", kind.jobTypes)
	}
}

// TestBuildDiscordClients keeps a Worker out of the registry unless it is enabled and
// its credentialsRef resolves to a usable bundle. Each exclusion records *why* on the
// Worker instead (ADR-0158), because "nothing registered under that name" reads as "you
// never configured it" when the truth is that the key is malformed.
//
// Unlike Jira there is no endpoint to be missing: Discord's API base is the same for
// everyone, so a record with only a credential is complete.
func TestBuildDiscordClients(t *testing.T) {
	srv, _ := newValidateServer(t)
	// The bundle lives in the vault; here it resolves from the env fallback
	// (ATLAS_CONNECTOR_<REF>_TOKEN), never from the record itself.
	t.Setenv("ATLAS_CONNECTOR_DISCORD_CREDS_TOKEN", `{"botToken":"MTIz.abc.def"}`)
	t.Setenv("ATLAS_CONNECTOR_BAD_DISCORD_TOKEN", `not valid json`)
	t.Setenv("ATLAS_CONNECTOR_EMPTY_DISCORD_TOKEN", `{"token":"MTIz.abc.def"}`)

	_ = srv.connectors.Save(connector{ID: "1", Name: "team", Kind: connectorKindDiscord, CredentialsRef: "discord_creds", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.Save(connector{ID: "2", Name: "off", Kind: connectorKindDiscord, CredentialsRef: "discord_creds", Enabled: false, CreatedAt: 2})
	_ = srv.connectors.Save(connector{ID: "3", Name: "nocred", Kind: connectorKindDiscord, Enabled: true, CreatedAt: 3})
	_ = srv.connectors.Save(connector{ID: "4", Name: "broken", Kind: connectorKindDiscord, CredentialsRef: "bad_discord", Enabled: true, CreatedAt: 4})
	_ = srv.connectors.Save(connector{ID: "5", Name: "wrongfield", Kind: connectorKindDiscord, CredentialsRef: "empty_discord", Enabled: true, CreatedAt: 5})
	_ = srv.connectors.Save(connector{ID: "6", Name: "amail", Kind: connectorKindMail, Endpoint: "smtp:587", Sender: "a@x", Enabled: true, CreatedAt: 6})

	clients, problems, err := srv.buildDiscordClients()
	if err != nil {
		t.Fatalf("buildDiscordClients: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("clients = %v, want only the usable record", clients)
	}
	if _, ok := clients["team"]; !ok {
		t.Errorf("clients = %v, want the bot record among them", clients)
	}
	// And every exclusion says why, in words pointed at the fix.
	for _, tc := range []struct{ name, want string }{
		{"off", "disabled"},
		{"nocred", "no credential"},
		{"broken", "not valid JSON"},
		{"wrongfield", "botToken"},   // a bundle whose one field is named something else
		{"amail", "not \"discord\""}, // a mail Worker named by a Discord task
	} {
		got, ok := problems[tc.name]
		if !ok {
			t.Errorf("no problem recorded for %q; a parked task would say only that nothing is registered", tc.name)
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("problem[%q] = %q, want it to mention %q", tc.name, got, tc.want)
		}
	}
}

// TestBuildDiscordClientsLoadError covers the store-read failure.
func TestBuildDiscordClientsLoadError(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
	if _, _, err := srv.buildDiscordClients(); err == nil {
		t.Error("buildDiscordClients with a broken store: want error")
	}
}
