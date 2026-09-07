package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/discord"
)

// discordEnvPrefix is where a Discord worker's bot identities live.
const discordEnvPrefix = "ATLAS_DISCORD_"

// discordRegistryFromEnv builds the Discord bot identities this worker holds.
// ATLAS_DISCORD_CONNECTORS lists the names; each name contributes
// ATLAS_DISCORD_<NAME>_TOKEN (the bot token, without the "Bot " scheme the client
// composes) and optionally ATLAS_DISCORD_<NAME>_URL — an API base override for a
// worker behind a proxy, blank for Discord's own. Those are the values
// [discord.Connector] is built from, so a worker builds the identical client the engine
// would have.
//
// Unlike Jira there is no URL to be missing: Discord's API base is the same for
// everyone, so a named identity carrying only a token is complete. The token is the one
// value that must be there, and a named identity without one is an error rather than a
// queue to lease work from and then fail.
//
// The token comes from the environment and not from a flag because argv is readable by
// anyone who can list processes — and a bot token is the whole of a bot's authority.
func discordRegistryFromEnv(env func(string) string) (*discord.Registry, []string, error) {
	names := splitAndTrim(env(discordEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured — a nil registry and no error, which the
		// caller reports as a kind this worker does not serve. A *named* identity
		// missing its token, below, is still an error: the operator named it, so the
		// omission is a mistake to report at startup.
		return nil, nil, nil
	}
	reg := discord.NewRegistry()
	for _, name := range names {
		key := discordEnvPrefix + envFold(name) + "_"
		token := env(key + "TOKEN")
		if token == "" {
			return nil, nil, fmt.Errorf("worker: discord worker %q is missing its bot token: set %sTOKEN", name, key)
		}
		reg.Register(name, discord.NewHTTPClient(discord.Connector{
			BaseURL:  env(key + "URL"),
			BotToken: token,
		}))
	}
	return reg, names, nil
}

// RunDiscordJob performs a resolved Discord task through a registry the caller owns. It
// is exported for the same reason RunJiraJob and RunGoogleSheetsJob are: the environment
// is only the default place a worker's identities come from, and a caller embedding this
// package can build a registry from a vault or an instance profile and get the identical
// call.
//
// It shares [discord.Run] with the in-process path, so no two of those can disagree
// about what a resolved Discord task means — only about which identities are in reach.
func RunDiscordJob(ctx context.Context, j Job, reg *discord.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("discord: the job carried no resolved worker detail; is this server offloading the discord kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task discord.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("discord: cannot read the resolved detail: %w", err)
	}
	res, err := discord.Run(ctx, task, reg)
	if err != nil {
		return nil, err
	}
	if task.ResultVariable == "" || res == nil {
		// Either the model discards the answer, or the operation is one Discord answers
		// with no content — the same distinction the in-process handler makes, so an
		// offloaded delete does not write a null where a read would write a value.
		return nil, nil
	}
	return map[string]any{task.ResultVariable: res}, nil
}
