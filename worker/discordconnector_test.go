package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/discord"
)

// recordingDiscordClient stands in for Discord: it records the operation a resolved job
// asked for and answers with what the API would.
type recordingDiscordClient struct {
	got    discord.Request
	result any
	err    error
}

func (c *recordingDiscordClient) Do(_ context.Context, req discord.Request) (any, error) {
	c.got = req
	return c.result, c.err
}

func discordJobFrom(t *testing.T, task discord.Job) Job {
	t.Helper()
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal fields: %v", err)
	}
	return Job{Connector: &ConnectorPayload{Kind: "discord", Fields: fields}}
}

func discordRegistryWith(name string, client discord.Client) *discord.Registry {
	reg := discord.NewRegistry()
	reg.Register(name, client)
	return reg
}

// The operation performed is the one the engine resolved — channel, content and the
// already-shaped extra body properties all travel — and what Discord returned reaches
// the model under the task's result variable as a real object rather than a stringified
// blob.
func TestRunDiscordJobPerformsWhatTheEngineResolved(t *testing.T) {
	client := &recordingDiscordClient{result: map[string]any{"id": "999", "channel_id": "42"}}
	job := discordJobFrom(t, discord.Job{
		Connector:      "team",
		Operation:      "send-message",
		Channel:        "42",
		Content:        "Antrag 17 ist genehmigt",
		Fields:         map[string]any{"tts": false},
		Nonce:          "7",
		ResultVariable: "nachricht",
	})

	out, err := RunDiscordJob(context.Background(), job, discordRegistryWith("team", client))
	if err != nil {
		t.Fatalf("RunDiscordJob: %v", err)
	}
	if client.got.Operation != "send-message" || client.got.Channel != "42" {
		t.Errorf("request = %+v, want the resolved operation and channel", client.got)
	}
	if client.got.Content != "Antrag 17 ist genehmigt" {
		t.Errorf("content = %q, want what the engine's FEEL evaluation produced", client.got.Content)
	}
	// The fields kept their JSON shape in the engine, where the FEEL scope is; a worker
	// never sees the expression the model held.
	if client.got.Fields["tts"] != false {
		t.Errorf("fields = %#v, want the boolean the engine resolved", client.got.Fields)
	}
	// The job key travels as the nonce, so a retry after an elapsed lease carries the
	// same value and its duplicate is recognizable.
	if client.got.Nonce != "7" {
		t.Errorf("nonce = %q, want the job key the engine froze at resolve time", client.got.Nonce)
	}
	res, ok := out["nachricht"].(map[string]any)
	if !ok {
		t.Fatalf("outputs = %#v, want what Discord returned under the result variable", out)
	}
	if res["id"] != "999" {
		t.Errorf("result = %#v, want the answer kept whole", res)
	}
}

// A task that discards its answer, and an operation that answers with nothing, both
// complete with no variables — the same distinction the in-process handler makes, so an
// offloaded delete does not write a null where a read would write a value.
func TestRunDiscordJobWritesNothingWhenThereIsNothingToWrite(t *testing.T) {
	for name, task := range map[string]discord.Job{
		"no result variable": {Connector: "team", Operation: "send-message", Channel: "42", Content: "x"},
		"nothing returned":   {Connector: "team", Operation: "delete-message", Channel: "42", Message: "7", ResultVariable: "r"},
	} {
		client := &recordingDiscordClient{} // result stays nil
		out, err := RunDiscordJob(context.Background(), discordJobFrom(t, task), discordRegistryWith("team", client))
		if err != nil {
			t.Fatalf("%s: RunDiscordJob: %v", name, err)
		}
		if len(out) != 0 {
			t.Errorf("%s: outputs = %#v, want none", name, out)
		}
	}
}

// A job that carries no resolved detail is a server that is not offloading this kind,
// and saying so is more use than a nil dereference.
func TestRunDiscordJobWithoutResolvedDetail(t *testing.T) {
	_, err := RunDiscordJob(context.Background(), Job{}, discord.NewRegistry())
	if err == nil || !strings.Contains(err.Error(), "offloading") {
		t.Errorf("a job with no detail: want an error naming the cause, got %v", err)
	}
}

// A payload this worker cannot read is a mismatch between engine and worker versions.
// It fails rather than acting on whatever survived the decode.
func TestRunDiscordJobWithAnUnreadablePayload(t *testing.T) {
	job := Job{Connector: &ConnectorPayload{Kind: "discord", Fields: map[string]any{"maxResults": "nicht numerisch"}}}
	if _, err := RunDiscordJob(context.Background(), job, discord.NewRegistry()); err == nil {
		t.Error("an unreadable payload: want an error, got nil")
	}
}

// The client's failure is the job's failure: it stays pending for a retry and then an
// incident, rather than completing a token on work that did not happen.
func TestRunDiscordJobPropagatesTheClientError(t *testing.T) {
	client := &recordingDiscordClient{err: errors.New("Discord said no")}
	job := discordJobFrom(t, discord.Job{Connector: "team", Operation: "send-message", Channel: "42", Content: "x"})
	if _, err := RunDiscordJob(context.Background(), job, discordRegistryWith("team", client)); err == nil {
		t.Error("a failed call: want the error propagated, got nil")
	}
}

// The registry is built from the same variables the engine renders. A name listed with
// a token becomes an identity this worker holds; the URL is optional, because Discord's
// API base is the same for everyone.
func TestDiscordRegistryFromEnv(t *testing.T) {
	env := map[string]string{
		"ATLAS_DISCORD_CONNECTORS":   "team, zweite",
		"ATLAS_DISCORD_TEAM_TOKEN":   "MTIz.abc.def",
		"ATLAS_DISCORD_ZWEITE_TOKEN": "MTIz.ghi.jkl",
		"ATLAS_DISCORD_ZWEITE_URL":   "https://discord.intern/api/v10",
	}
	reg, names, err := discordRegistryFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("discordRegistryFromEnv: %v", err)
	}
	if len(names) != 2 || names[0] != "team" {
		t.Errorf("names = %v, want both identities the engine listed", names)
	}
	for _, n := range names {
		if _, ok := reg.Client(n); !ok {
			t.Errorf("the registry holds no client for %q", n)
		}
	}
}

// Told to serve the kind while holding no identity: unconfigured, not misconfigured. A
// nil registry and no error, so a worker serving other kinds still starts.
func TestDiscordRegistryFromEnvWithNothingConfigured(t *testing.T) {
	reg, names, err := discordRegistryFromEnv(func(string) string { return "" })
	if reg != nil || names != nil || err != nil {
		t.Errorf("= %v, %v, %v; want nothing and no error", reg, names, err)
	}
}

// A *named* identity with no token is an error at startup, where the operator is still
// watching — not a queue to lease work from and then fail on.
func TestDiscordRegistryFromEnvRefusesANamedIdentityItCannotBuild(t *testing.T) {
	env := map[string]string{"ATLAS_DISCORD_CONNECTORS": "team"} // no token
	_, _, err := discordRegistryFromEnv(func(k string) string { return env[k] })
	if err == nil {
		t.Fatal("a named identity with no token: want an error, got nil")
	}
	// The message names the variable to set, because that is the fix.
	if !strings.Contains(err.Error(), "ATLAS_DISCORD_TEAM_TOKEN") {
		t.Errorf("error %q should name the variable an operator has to set", err)
	}
}

// BuiltinConnectors is what a supervised worker is actually configured through, so the
// kind must reach a handler from the environment alone.
func TestBuiltinConnectorsServesDiscord(t *testing.T) {
	env := map[string]string{
		"ATLAS_DISCORD_CONNECTORS": "team",
		"ATLAS_DISCORD_TEAM_TOKEN": "MTIz.abc.def",
	}
	built, err := BuiltinConnectors(func(k string) string { return env[k] }, "discord")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := built.Handlers["io.atlas.discord"]; !ok {
		t.Errorf("handlers = %v, want one under the reserved Discord job type", built.Handlers)
	}
}
