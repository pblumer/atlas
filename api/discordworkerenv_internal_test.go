package api

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/worker"
)

// Discord is offloaded by default, and the reason it can be is that the engine hands
// its identities over. A Discord task names its Worker and nothing more — the bot token
// is a worker record and a vault secret, which a supervised worker can read no more
// than it can read the engine's memory.
//
// Without this handover the default would have moved every chat task to a worker with
// no bot to post as, which is the failure mail had before ADR-0168.

// discordBundle is a well-formed bot bundle, the shape an operator stores in the vault
// under the record's credentialsRef.
const discordBundle = `{"botToken":"MTIzNDU2Nzg5.GAbCdE.fGhIjKlMnOpQrStUvWxYz"}`

func saveDiscordWorker(t *testing.T, srv *Server, id, name, endpoint, ref string) {
	t.Helper()
	if err := srv.connectors.Save(connector{
		ID: id, Name: name, Kind: connectorKindDiscord,
		Endpoint: endpoint, CredentialsRef: ref, Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save %s: %v", name, err)
	}
}

// What the engine renders is what a worker builds a client from. This is the test that
// actually holds the two halves together: it takes the rendered environment and asks
// worker.BuiltinConnectors to configure itself from it, so a variable named differently
// on either side fails here rather than in a parked job.
func TestSupervisedDiscordEnvUsesTheWorkersOwnNames(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveDiscordWorker(t, srv, "1", "team", "", "discord-creds")
	t.Setenv("ATLAS_CONNECTOR_DISCORD_CREDS_TOKEN", discordBundle)

	env := envOf(t, srv.discordWorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindDiscord)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "team") {
		t.Errorf("the worker holds %v, want the identity the engine handed it", built.Names)
	}
}

// The token travels as the bare value, not as the bundle. Unlike Google's, this bundle
// has exactly one field, so there is nothing for the far side to parse — and handing
// over the JSON would mean the worker's ATLAS_DISCORD_<NAME>_TOKEN, which an operator
// also sets by hand for an external worker, had two accepted spellings.
func TestSupervisedDiscordEnvHandsOverTheBareToken(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveDiscordWorker(t, srv, "1", "team", "", "discord-creds")
	t.Setenv("ATLAS_CONNECTOR_DISCORD_CREDS_TOKEN", discordBundle)

	env := envOf(t, srv.discordWorkerEnv())
	if got := env["ATLAS_DISCORD_TEAM_TOKEN"]; got != "MTIzNDU2Nzg5.GAbCdE.fGhIjKlMnOpQrStUvWxYz" {
		t.Errorf("the handed token = %q, want the bare value out of the bundle", got)
	}
	// No endpoint was authored, so none is rendered: blank means Discord's own API
	// base, which is what the engine's own client build passes too.
	if _, ok := env["ATLAS_DISCORD_TEAM_URL"]; ok {
		t.Error("an endpoint was rendered for a record that authored none")
	}
}

// A token an operator pasted with the scheme still attached is handed over stripped,
// exactly as connector/discord would have built its own client from it. Handed over
// whole, the worker would send "Bot Bot …" and be answered by a 401 explaining nothing.
func TestSupervisedDiscordEnvStripsAPastedScheme(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveDiscordWorker(t, srv, "1", "team", "", "discord-creds")
	t.Setenv("ATLAS_CONNECTOR_DISCORD_CREDS_TOKEN", `{"botToken":"Bot MTIz.abc.def"}`)

	env := envOf(t, srv.discordWorkerEnv())
	if got := env["ATLAS_DISCORD_TEAM_TOKEN"]; got != "MTIz.abc.def" {
		t.Errorf("the handed token = %q, want the scheme prefix stripped", got)
	}
}

// An identity whose vault secret is not set yet, or whose bundle carries no botToken,
// is left out rather than handed over empty. Handed over empty, the worker refuses at
// startup on a *named* identity it cannot build — which takes down every other kind
// that worker serves.
func TestSupervisedDiscordEnvSkipsAnUnresolvedCredential(t *testing.T) {
	for _, tc := range []struct{ name, secret string }{
		{"never set", ""},
		{"malformed", "{"},
		{"no botToken", `{"token":"MTIz.abc.def"}`},
		{"only the scheme", `{"botToken":"Bot "}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
			saveDiscordWorker(t, srv, "1", "team", "", "discord-creds")
			if tc.secret != "" {
				t.Setenv("ATLAS_CONNECTOR_DISCORD_CREDS_TOKEN", tc.secret)
			}
			if env := srv.discordWorkerEnv(); env != nil {
				t.Errorf("rendered %v for an identity with no usable credential; want nothing", env)
			}
		})
	}
}

// The intent behind the default, pinned separately from the mechanism: a fresh install
// must not hold a bot token on the engine's run loop. A bot token is the whole of a
// bot's authority, and ADR-0164 puts every connector task on a worker anyway.
func TestDiscordIsOffloadedByDefault(t *testing.T) {
	defaults := map[string]bool{}
	for _, kind := range DefaultOffloadedKinds() {
		defaults[kind] = true
	}
	if !defaults[connectorKindDiscord] {
		t.Error("discord is not offloaded by default: a fresh install still calls Discord from the engine's run loop")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()[connectorKindDiscord]; !provisioned {
		t.Error("discord is defaulted onto a worker but its credential is not handed over")
	}
}

// And the outcome the author actually sees. The badge is computed from what the job
// runner holds after applyOffloadedKinds has run, so this exercises the default the way
// a booted server does rather than re-deriving it from the list.
func TestDiscordBadgeSaysOnAWorker(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds(DefaultOffloadedKinds()))
	var got string
	srv.do(func() { got = srv.placementOfCatalogKind(connectorKindDiscord) })
	if got != placementWorker {
		t.Errorf("the Modeler shows discord as %q, want %q — a connector task belongs on a worker (ADR-0164)", got, placementWorker)
	}
}

// An identity an operator set on the host is inherited by the child already, so nothing
// is rendered for it — but its name must stay in the list, or a store identity would
// silently take the whole list away from it and the host one would stop being served
// the moment somebody added a record in the Console.
func TestSupervisedDiscordEnvKeepsHostNamesInTheList(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveDiscordWorker(t, srv, "1", "aus-dem-store", "", "discord-creds")
	t.Setenv("ATLAS_CONNECTOR_DISCORD_CREDS_TOKEN", discordBundle)
	t.Setenv("ATLAS_DISCORD_CONNECTORS", "vom-host")

	env := envOf(t, srv.discordWorkerEnv())
	names := strings.Split(env["ATLAS_DISCORD_CONNECTORS"], ",")
	if !slices.Contains(names, "vom-host") || !slices.Contains(names, "aus-dem-store") {
		t.Errorf("the rendered list is %v, want the union of host and store identities", names)
	}
	// Nothing is rendered for the host one: the child inherits its variables as they are.
	if _, ok := env["ATLAS_DISCORD_VOM_HOST_TOKEN"]; ok {
		t.Error("a token was rendered for an identity the child already inherits")
	}
}

// Two names that fold to one environment variable would silently give one the other's
// token — a bot posting as somebody else. The second is left out rather than
// overwriting the first.
func TestSupervisedDiscordEnvRefusesACollidingName(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveDiscordWorker(t, srv, "1", "team-eins", "", "discord-creds")
	saveDiscordWorker(t, srv, "2", "team.eins", "", "discord-creds")
	t.Setenv("ATLAS_CONNECTOR_DISCORD_CREDS_TOKEN", discordBundle)

	env := envOf(t, srv.discordWorkerEnv())
	names := strings.Split(env["ATLAS_DISCORD_CONNECTORS"], ",")
	if len(names) != 1 {
		t.Errorf("the rendered list is %v, want only the identity that won the fold", names)
	}
	if _, ok := env["ATLAS_DISCORD_TEAM_EINS_TOKEN"]; !ok {
		t.Errorf("env = %v, want the one folded token", env)
	}
}

// An authored endpoint is rendered; a record of another kind, and a disabled one, are
// not this worker's to serve.
func TestSupervisedDiscordEnvRendersAnEndpointAndSkipsWhatIsNotItsOwn(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveDiscordWorker(t, srv, "1", "team", "https://discord.intern/api/v10", "discord-creds")
	if err := srv.connectors.Save(connector{
		ID: "2", Name: "aus", Kind: connectorKindDiscord, CredentialsRef: "discord-creds", Enabled: false, CreatedAt: 2,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := srv.connectors.Save(connector{
		ID: "3", Name: "post", Kind: connectorKindMail, Endpoint: "smtp:587", Sender: "a@x", Enabled: true, CreatedAt: 3,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv("ATLAS_CONNECTOR_DISCORD_CREDS_TOKEN", discordBundle)

	env := envOf(t, srv.discordWorkerEnv())
	if got := env["ATLAS_DISCORD_TEAM_URL"]; got != "https://discord.intern/api/v10" {
		t.Errorf("endpoint = %q, want the authored override", got)
	}
	if got := env["ATLAS_DISCORD_CONNECTORS"]; got != "team" {
		t.Errorf("the rendered list is %q, want only the enabled Discord identity", got)
	}
}

// A store that cannot be read renders nothing rather than a half list: a supervised
// worker handed half its identities would serve some tasks and park others with no sign
// of why.
func TestSupervisedDiscordEnvOnAnUnreadableStore(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
	if env := srv.discordWorkerEnv(); env != nil {
		t.Errorf("rendered %v from a store that cannot be read; want nothing", env)
	}
}

// A name with nothing an environment variable can be built from is left out rather than
// rendered as ATLAS_DISCORD__TOKEN, which would be one nameless slot every such
// identity overwrote in turn.
func TestSupervisedDiscordEnvSkipsANameThatFoldsToNothing(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveDiscordWorker(t, srv, "1", "—", "", "discord-creds")
	t.Setenv("ATLAS_CONNECTOR_DISCORD_CREDS_TOKEN", discordBundle)

	if env := srv.discordWorkerEnv(); env != nil {
		t.Errorf("rendered %v for a name no variable can carry; want nothing", env)
	}
}
